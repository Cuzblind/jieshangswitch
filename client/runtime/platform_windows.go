//go:build windows

package clientruntime

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func platformOSLabel() string {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE)
	if err != nil {
		return "Windows"
	}
	defer key.Close()
	build, _, _ := key.GetStringValue("CurrentBuild")
	if value, _ := strconv.Atoi(build); value >= 22000 {
		return "Windows 11"
	}
	return "Windows 10"
}

func openTarget(target string) error {
	return hidden(exec.Command("rundll32", "url.dll,FileProtocolHandler", target)).Start()
}

func openDir(which string) error {
	p := paths()
	dir := map[string]string{"claude": filepath.Dir(p.ClaudeSettings()), "codex": filepath.Dir(p.CodexConfig()), "jieshangai": p.Dir()}[which]
	if dir == "" {
		return nil
	}
	if _, err := os.Stat(dir); err != nil {
		dir = homeDir()
	}
	return exec.Command("explorer.exe", dir).Start()
}

// launch opens an interactive terminal running the assistant: Windows Terminal
// first, then PowerShell, then cmd. Codex gets its env var explicitly because
// our own process environment predates install.ps1. The secret never appears on
// any command line — it travels through process env or the ACL-protected
// wrapper that the Windows Terminal tier uses.
func launch(agent, secret string) error {
	if !validAgent(agent) {
		return errors.New("invalid assistant")
	}
	extra := []string(nil)
	if agent == "codex" && secret != "" {
		extra = append(extra, "JIESHANGAI_CODEX_KEY="+secret)
	}
	workDir := resolveLaunchWorkDir()
	extra = append(extra, "JIESHANGAI_WORKDIR="+workDir)
	if wt := windowsTerminalPath(); wt != "" && cmdSafeEnvValue(secret) {
		if err := launchWindowsTerminal(wt, agent, secret, workDir); err == nil {
			return nil
		}
	}
	if err := launchPowerShellTerminal(agent, extra); err == nil {
		return nil
	}
	return launchCmdTerminal(agent, extra)
}

// windowsTerminalPath prefers Windows Terminal (Windows 11 default; Windows 10
// only when the Store package is installed).
func windowsTerminalPath() string {
	if p, err := exec.LookPath("wt.exe"); err == nil {
		return p
	}
	// App execution aliases are per-user and not always inherited by helper
	// processes; probe the well-known alias location as well.
	candidate := filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "WindowsApps", "wt.exe")
	if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
		return candidate
	}
	return ""
}

// launchWindowsTerminal hands the tab to the terminal process, and a tab in an
// already-running Windows Terminal inherits that process's environment — not
// ours. The wrapper script re-establishes PATH and the Codex session key.
func launchWindowsTerminal(wt, agent, secret, workDir string) error {
	dir := filepath.Join(homeDir(), ".jieshangai", "launch")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	wrapper := filepath.Join(dir, agent+"-terminal.cmd")
	if err := writePrivateFile(wrapper, []byte(terminalWrapperScript(agent, secret))); err != nil {
		return err
	}
	target := wrapper
	if strings.ContainsAny(target, " ") {
		// wt re-tokenizes its command line; a short path avoids quote mangling
		// when the profile directory contains spaces.
		if sp := shortPath(target); sp != "" {
			target = sp
		} else {
			return errors.New("terminal wrapper path cannot be passed to wt safely")
		}
	}
	if workDir == "" {
		workDir = homeDir()
	}
	c := exec.Command(wt, "-w", "0", "new-tab", "--title", agentName(agent), "--startingDirectory", workDir, "cmd", "/k", target)
	return c.Run()
}

// terminalWrapperScript is executed by the Windows Terminal tab. Every path is
// expanded at runtime (%~dp0 is the script's own directory), keeping the file
// pure ASCII so cmd's codepage cannot corrupt a non-ASCII profile or workDir
// path (wt already sets the working directory via --startingDirectory). The
// caller must reject non-cmd-safe secrets (see cmdSafeEnvValue).
func terminalWrapperScript(agent, secret string) string {
	s := "@echo off\r\n" +
		"set \"PATH=%~dp0..\\tools\\bin;%PATH%\"\r\n"
	if agent == "codex" && secret != "" {
		s += "set \"JIESHANGAI_CODEX_KEY=" + secret + "\"\r\n"
	}
	return s + "call \"%~dp0..\\tools\\bin\\" + agent + ".cmd\"\r\n"
}

// cmdSafeEnvValue reports whether a value survives batch-file parsing: cmd
// expands %...% sequences and decodes batch files in the console codepage, so
// anything non-ASCII or containing % must use the PowerShell tier instead.
func cmdSafeEnvValue(v string) bool {
	for i := 0; i < len(v); i++ {
		if v[i] < 0x20 || v[i] > 0x7e || v[i] == '%' {
			return false
		}
	}
	return true
}

// shortPath returns an 8.3 path without spaces, or "" when unavailable.
func shortPath(p string) string {
	p16, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return ""
	}
	buf := make([]uint16, 1024)
	n, err := windows.GetShortPathName(p16, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 {
		return ""
	}
	s := windows.UTF16ToString(buf[:n])
	if strings.ContainsAny(s, " \"") {
		return ""
	}
	return s
}

func launchPowerShellTerminal(agent string, extra []string) error {
	script := "$wd = if ($env:JIESHANGAI_WORKDIR) { $env:JIESHANGAI_WORKDIR } else { $env:USERPROFILE }; Start-Process -FilePath (Join-Path $PSHOME 'powershell.exe') -WorkingDirectory $wd -ArgumentList @('-NoExit','-NoProfile','-Command','Set-Location -LiteralPath (if ($env:JIESHANGAI_WORKDIR) { $env:JIESHANGAI_WORKDIR } else { $env:USERPROFILE }); " + agent + "') -ErrorAction Stop"
	cmd := hidden(exec.Command(powershell(), "-NoProfile", "-NonInteractive", "-Command", script))
	cmd.Env = cmdEnv(extra...)
	return cmd.Run()
}

func launchCmdTerminal(agent string, extra []string) error {
	script := "$wd = if ($env:JIESHANGAI_WORKDIR) { $env:JIESHANGAI_WORKDIR } else { $env:USERPROFILE }; Start-Process -FilePath $env:ComSpec -WorkingDirectory $wd -ArgumentList '/k " + agent + "' -ErrorAction Stop"
	cmd := hidden(exec.Command(powershell(), "-NoProfile", "-NonInteractive", "-Command", script))
	cmd.Env = cmdEnv(extra...)
	return cmd.Run()
}
