//go:build windows

package clientruntime

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
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
	if strings.EqualFold(filepath.Ext(program), ".cmd") || strings.EqualFold(filepath.Ext(program), ".bat") {
		quoted := "\"" + strings.ReplaceAll(program, "\"", "") + "\""
		for _, a := range args {
			quoted += " \"" + strings.ReplaceAll(a, "\"", "") + "\""
		}
		program = filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
		args = []string{"/d", "/s", "/c", "\"" + quoted + "\""}
	}
	c := hidden(exec.CommandContext(ctx, program, args...))
	c.Env = env
	c.Stdin = nil
	c.WaitDelay = 2 * time.Second
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(job)
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		return "", err
	}
	c.Cancel = func() error {
		_ = windows.TerminateJobObject(job, 1)
		if c.Process != nil {
			return c.Process.Kill()
		}
		return nil
	}
	var b tailBuffer
	c.Stdout = &b
	c.Stderr = &b
	if err = c.Start(); err != nil {
		return "", err
	}
	h, e := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(c.Process.Pid))
	if e != nil {
		_ = c.Process.Kill()
		_ = c.Wait()
		return "", e
	}
	e = windows.AssignProcessToJobObject(job, h)
	windows.CloseHandle(h)
	if e != nil {
		_ = c.Process.Kill()
		_ = c.Wait()
		return "", e
	}
	err = c.Wait()
	return string(b.b), err
}
func launchToolTerminal(agent string, e toolEntry, secret string, env []string) error {
	return launch(agent, secret)
}

// WindowsApps (MSIX) folders deny CreateProcess to normal callers — the beta.5
// field launch died with "fork/exec ...Claude.exe: Access is denied". The
// Start-menu route is the supported one: activate
// shell:AppsFolder\<PackageFamilyName>!<ApplicationId> through the shell.
func shellActivateTarget(info guiInfo) string {
	if info.Family == "" || info.AppID == "" {
		return ""
	}
	return `shell:AppsFolder\` + info.Family + "!" + info.AppID
}

func shellActivate(target string) error {
	shell32 := windows.NewLazySystemDLL("shell32.dll")
	open := shell32.NewProc("ShellExecuteW")
	verb, _ := windows.UTF16PtrFromString("open")
	file, _ := windows.UTF16PtrFromString(target)
	dir, _ := windows.UTF16PtrFromString(homeDir())
	rc, _, _ := open.Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(file)), 0, uintptr(unsafe.Pointer(dir)), 1) // SW_SHOWNORMAL
	if rc <= 32 {                                                                                                              // >32 means success; 5 = SE_ERR_ACCESSDENIED etc.
		return fmt.Errorf("桌面版启动失败（code %d），请重试或联系客服", int32(rc))
	}
	return nil
}

func startGUI(info guiInfo, agent string, env []string) error {
	if target := shellActivateTarget(info); target != "" {
		if agent == "codex" {
			// Shell activation does not inherit our process env; codex keys ride
			// the persisted user variable (install.ps1 Set-CodexProcessEnv). The
			// broadcast is a best-effort refresh so Explorer-brokered launches
			// see a value written earlier this session.
			_ = broadcastEnvironment()
		}
		return shellActivate(target)
	}
	c := exec.Command(info.Path)
	c.Env = env
	c.Dir = homeDir()
	if e := c.Start(); e != nil {
		return e
	}
	go c.Wait()
	return nil
}
func guiIsRunning(info guiInfo) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	script := "$p=Get-Process -ErrorAction SilentlyContinue | Where-Object {$_.Path -eq $env:JIESHANGAI_GUI_PATH};if($p){exit 0};exit 1"
	_, err := runToolCommand(ctx, powershell(), []string{"-NoProfile", "-NonInteractive", "-Command", script}, append(cmdEnv(), "JIESHANGAI_GUI_PATH="+info.Path), false)
	return err == nil
}
func guiConfigDirs() (string, string) {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base = filepath.Join(homeDir(), "AppData", "Local")
	}
	return filepath.Join(base, "Claude"), filepath.Join(base, "Claude-3p")
}
func guiManagedElsewhere() bool {
	k, e := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Policies\Claude`, registry.QUERY_VALUE)
	if e != nil {
		return false
	}
	defer k.Close()
	names, e := k.ReadValueNames(1)
	return e == nil && len(names) > 0
}
