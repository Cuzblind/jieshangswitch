//go:build !windows

package clientruntime

import (
	"context"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"time"
)

type tailBuffer struct{ b []byte }

func (b *tailBuffer) Write(p []byte) (int, error) {
	n := len(p)
	b.b = append(b.b, p...)
	if len(b.b) > 16384 {
		b.b = b.b[len(b.b)-16384:]
	}
	return n, nil
}
func runToolCommand(ctx context.Context, program string, args, env []string, install bool) (string, error) {
	c := exec.CommandContext(ctx, program, args...)
	c.Env = env
	c.Stdin = nil
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error {
		if c.Process != nil {
			return syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	c.WaitDelay = 2 * time.Second
	var b tailBuffer
	c.Stdout = &b
	c.Stderr = &b
	e := c.Run()
	return string(b.b), e
}
func launchToolTerminal(agent string, e toolEntry, secret string, env []string) error {
	// Reuse the existing terminal adapter; its PATH begins with our managed shims.
	return launch(agent, secret)
}
func startGUI(info guiInfo, agent string, env []string) error {
	program := info.Path
	if strings.HasSuffix(info.Path, ".app") {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		name, e := exec.CommandContext(ctx, "/usr/libexec/PlistBuddy", "-c", "Print :CFBundleExecutable", filepath.Join(info.Path, "Contents", "Info.plist")).Output()
		if e != nil {
			return e
		}
		program = filepath.Join(info.Path, "Contents", "MacOS", strings.TrimSpace(string(name)))
	}
	c := exec.Command(program)
	c.Env = env
	c.Dir = homeDir()
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if e := c.Start(); e != nil {
		return e
	}
	go c.Wait()
	return nil
}
func guiIsRunning(info guiInfo) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "/usr/bin/pgrep", "-f", regexp.QuoteMeta(filepath.Join(info.Path, "Contents", "MacOS"))).Run() == nil
}
func guiConfigDirs() (string, string) {
	base := filepath.Join(homeDir(), "Library", "Application Support")
	if runtime.GOOS != "darwin" {
		base = filepath.Join(homeDir(), ".config")
	}
	return filepath.Join(base, "Claude"), filepath.Join(base, "Claude-3p")
}
func guiManagedElsewhere() bool { return false }
