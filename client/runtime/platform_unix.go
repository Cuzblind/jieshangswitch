//go:build !windows

package clientruntime

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func platformOSLabel() string {
	if runtime.GOOS != "darwin" {
		return "Linux"
	}
	out, err := exec.Command("/usr/bin/sw_vers", "-productVersion").Output()
	if err != nil {
		return "macOS"
	}
	return "macOS " + strings.TrimSpace(string(out))
}

func openDir(which string) error {
	p := paths()
	dir := map[string]string{
		"claude":     filepath.Dir(p.ClaudeSettings()),
		"codex":      filepath.Dir(p.CodexConfig()),
		"jieshangai": p.Dir(),
	}[which]
	if dir == "" {
		return nil
	}
	if _, err := os.Stat(dir); err != nil {
		dir = homeDir()
	}
	return openTarget(dir)
}

func openTarget(target string) error {
	if os.Getenv("JIESHANGAI_NO_OPEN") == "1" { // e2e
		f, _ := os.OpenFile(filepath.Join(os.TempDir(), "jieshangai-opened.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if f != nil {
			defer f.Close()
			_, _ = f.WriteString(target + "\n")
		}
		return nil
	}
	if runtime.GOOS == "darwin" {
		return exec.Command("/usr/bin/open", target).Start()
	}
	return exec.Command("xdg-open", target).Start()
}

// launch opens a new terminal window running the assistant (M9 「帮我打开」).
// macOS: a temp .command file opened by Terminal — no automation permission.
func launch(agent, secret string) error {
	if !validAgent(agent) {
		return errors.New("invalid assistant")
	}
	_ = secret // Codex reads the existing private env file, never argv.
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
	dir, err := os.MkdirTemp("", "jieshangai-launch-")
	if err != nil {
		return err
	}
	f := filepath.Join(dir, agent+".command")
	script := "#!/bin/sh\nexport PATH=" + quote(augmentedPath()) + "\n"
	if agent == "codex" {
		script += "[ -f \"$HOME/.jieshangai/codex.env\" ] && . \"$HOME/.jieshangai/codex.env\"\n"
	}
	script += "cd \"$HOME\"\nrm -f -- \"$0\"\nrmdir -- " + quote(dir) + "\nexec " + agent + "\n"
	if err := os.WriteFile(f, []byte(script), 0o700); err != nil {
		return err
	}
	if os.Getenv("JIESHANGAI_NO_OPEN") == "1" {
		return openTarget(f)
	}
	if runtime.GOOS == "darwin" {
		return exec.Command("/usr/bin/open", "-a", "Terminal", f).Run()
	}
	for _, terminal := range []string{"x-terminal-emulator", "gnome-terminal", "xterm"} {
		if _, err := exec.LookPath(terminal); err == nil {
			return exec.Command(terminal, "-e", f).Start()
		}
	}
	_ = os.Remove(f)
	_ = os.Remove(dir)
	return errors.New("no terminal available")
}
