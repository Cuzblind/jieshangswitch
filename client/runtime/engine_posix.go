//go:build !windows

package clientruntime

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"time"

	"jieshangai/client/core"
)

// POSIX engine: client/core, which is parity-tested byte-for-byte against
// install/sh/install.sh (/i) and uninstall.sh (/u). No python3 needed.

func loginShell() string {
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	if runtime.GOOS == "darwin" {
		return "/bin/zsh"
	}
	return "/bin/bash"
}

func engineWrite(agent, origin, secret string) error {
	p := paths()
	if agent == "claude" {
		return p.WriteClaude(core.ClaudeOpts{Origin: origin, Secret: secret})
	}
	return p.WriteCodex(core.CodexOpts{Origin: origin, Secret: secret, Shell: loginShell()})
}

func engineStamp(agent, cli string) error { return paths().WriteStamp(agent, cli) }

func engineRestore(confirm bool) (bool, error) {
	r, err := paths().Restore(core.RestoreOpts{Confirm: confirm})
	if err != nil {
		return false, err
	}
	return r.UsedBackup, nil
}

// installCLI mirrors install_official: curl -fsSL <official> | bash.
func installCLI(agent string) error {
	if os.Getenv("JIESHANGAI_FAKE_CLI") == "1" {
		return nil
	}
	return installOfficialCLI(agent)
}

func installOfficialCLI(agent string) error {
	url := claudeInstallSH
	if agent == "codex" {
		url = codexInstallSH
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/bash", "-o", "pipefail", "-c", "curl -fsSL '"+url+"' | bash")
	cmd.Env = cmdEnv("CODEX_NON_INTERACTIVE=1")
	if err := cmd.Run(); err != nil {
		return errors.New("install failed")
	}
	return nil
}

func officialLogin(agent string) bool {
	if agent != "claude" {
		return false
	}
	if claudeFileLogin() {
		return true
	}
	if runtime.GOOS == "darwin" {
		return exec.Command("/usr/bin/security", "find-generic-password", "-s", "Claude Code-credentials").Run() == nil
	}
	return false
}

// logout removes only this computer's sign-in state (memo #4).
func logout(agent string) error {
	if agent == "codex" {
		cmd := exec.Command("codex", "logout")
		cmd.Env = cmdEnv()
		return cmd.Run()
	}
	_ = os.Remove(claudeCredFile())
	if runtime.GOOS == "darwin" {
		for i := 0; i < 5 && exec.Command("/usr/bin/security", "delete-generic-password", "-s", "Claude Code-credentials").Run() == nil; i++ {
		}
	}
	return nil
}

func running(agent string) bool { return exec.Command("pgrep", "-x", agent).Run() == nil }

func kill(agent string) error {
	_ = exec.Command("pkill", "-x", agent).Run()
	time.Sleep(500 * time.Millisecond)
	if running(agent) {
		_ = exec.Command("pkill", "-9", "-x", agent).Run()
	}
	return nil
}
