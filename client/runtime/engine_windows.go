//go:build windows

package clientruntime

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Windows engine: the very same install.ps1 / uninstall.ps1 that /i and /u
// serve (copied in by `go generate`; TestScriptsInSync guards drift), run
// silently with the connection passed through the environment only.

//go:generate cp ../../install/ps1/install.ps1 ../../install/ps1/uninstall.ps1 scripts/

//go:embed scripts/install.ps1
var installPS1 []byte

//go:embed scripts/uninstall.ps1
var uninstallPS1 []byte

const createNoWindow = 0x08000000

func hidden(cmd *exec.Cmd) *exec.Cmd {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	return cmd
}

func powershell() string {
	p := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return "powershell.exe"
}

func runPS1(body []byte, env ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	return runPS1Context(ctx, body, env...)
}

func runPS1Context(ctx context.Context, body []byte, env ...string) (string, error) {
	f, err := os.CreateTemp("", "jieshangai-*.ps1")
	if err != nil {
		return "", err
	}
	// UTF-8 BOM so Windows PowerShell 5.1 reads the Chinese strings correctly.
	// Strip a BOM the script may already carry (editors save .ps1 with one):
	// doubled, 5.1 decodes the second BOM as content and line 1 stops parsing,
	// which broke every engine operation while install.ps1 shipped with a BOM.
	body = bytes.TrimPrefix(body, []byte{0xEF, 0xBB, 0xBF})
	_, _ = f.Write([]byte{0xEF, 0xBB, 0xBF})
	_, _ = f.Write(body)
	f.Close()
	defer os.Remove(f.Name())
	cmd := hidden(exec.CommandContext(ctx, powershell(), "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", f.Name()))
	cmd.Env = cmdEnv(env...)
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		killCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = hidden(exec.CommandContext(killCtx, "taskkill.exe", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))).Run()
		return cmd.Process.Kill()
	}
	cmd.WaitDelay = 3 * time.Second
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func engineWrite(agent, origin, secret string) error {
	// Only a build explicitly targeting an HTTP test gateway can opt the
	// bundled installer into that same origin.
	testOrigin := ""
	if (origin == defaultOrigin && strings.HasPrefix(defaultOrigin, "http://")) || (origin == TestOrigin && strings.HasPrefix(TestOrigin, "http://")) {
		testOrigin = origin
	}
	_, err := runPS1(installPS1, "RELAY_AGENT="+agent, "RELAY_TOKEN="+secret, "RELAY_SECRET="+secret,
		"RELAY_SITE_ORIGIN="+origin, "RELAY_TEST_ORIGIN="+testOrigin, "RELAY_SKIP_CLI=1", "RELAY_CLAIM=", "RELAY_QUERY=")
	return err
}

// install.ps1 already writes the stamp on success.
func engineStamp(agent, cli string) error { return nil }

func engineRestore(confirm bool) (bool, error) {
	env := []string{}
	if confirm {
		env = append(env, "RELAY_CONFIRM=1")
	}
	out, err := runPS1(uninstallPS1, env...)
	if err != nil {
		return false, err
	}
	return strings.Contains(out, "已经恢复好了"), nil
}

// installCLI mirrors Install-OfficialCli in install.ps1.
func installCLI(agent string) error { return installOfficialCLI(agent) }

func installOfficialCLI(agent string) error {
	url := claudeInstallPS1
	if agent == "codex" {
		url = codexInstallPS1
	}
	script := "$ErrorActionPreference='Stop';$t=Join-Path $env:TEMP ('jieshangai-cli-'+[guid]::NewGuid().ToString('n')+'.ps1');" +
		"try{Invoke-WebRequest -Uri '" + url + "' -OutFile $t -UseBasicParsing;$env:CODEX_NON_INTERACTIVE='1';" +
		"& '" + powershell() + "' -NoProfile -ExecutionPolicy Bypass -File $t;exit $LASTEXITCODE}finally{Remove-Item -LiteralPath $t -Force -ErrorAction SilentlyContinue}"
	_, err := runPS1([]byte(script))
	if err != nil {
		return errors.New("install failed")
	}
	return nil
}

func officialLogin(agent string) bool { return agent == "claude" && claudeFileLogin() }

func logout(agent string) error {
	if agent == "codex" {
		cmd := hidden(exec.Command("codex", "logout"))
		cmd.Env = cmdEnv()
		return cmd.Run()
	}
	err := os.Remove(claudeCredFile())
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func running(agent string) bool {
	out, err := hidden(exec.Command("tasklist", "/FI", "IMAGENAME eq "+agent+".exe", "/NH")).Output()
	return err == nil && strings.Contains(strings.ToLower(string(out)), agent+".exe")
}

func kill(agent string) error {
	return hidden(exec.Command("taskkill", "/F", "/T", "/IM", agent+".exe")).Run()
}
