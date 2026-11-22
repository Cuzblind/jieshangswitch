//go:build windows

package clientruntime

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runElevatedPayload(t *testing.T, encoded string) int {
	t.Helper()
	c := exec.Command(powershell(), "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-EncodedCommand", encoded)
	c.Env = cmdEnv()
	if err := c.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode()
		}
		t.Fatal(err)
	}
	return 0
}

// The elevated step runs no file from a user-writable directory; paths with
// spaces and apostrophes must still arrive intact, and failures must reach the
// result file. Runs unelevated: Add-AppxPackage fails on the fake package.
func TestElevatedPayloadReportsThroughResultFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "O'Brien Files")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	result := filepath.Join(dir, "result.log")
	sid, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	code := runElevatedPayload(t, elevatedAppxPayload(filepath.Join(dir, "missing.msix"), result, sid))
	if code != 3 {
		t.Fatalf("exit %d", code)
	}
	data, err := os.ReadFile(result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "JIESHANGAI_APPX_START") || !strings.Contains(string(data), "JIESHANGAI_APPX_ERROR") {
		t.Fatalf("result file: %s", data)
	}
}

// A standard user who types another administrator's password elevates into that
// account; deploying there would leave this user without the app.
func TestElevatedPayloadRefusesAnotherAccount(t *testing.T) {
	dir := t.TempDir()
	result := filepath.Join(dir, "result.log")
	code := runElevatedPayload(t, elevatedAppxPayload(filepath.Join(dir, "missing.msix"), result, "S-1-5-21-0-0-0-1000"))
	if code != elevatedWrongAccountExit {
		t.Fatalf("exit %d", code)
	}
	if data, _ := os.ReadFile(result); strings.Contains(string(data), "JIESHANGAI_APPX_ERROR") {
		t.Fatalf("install was attempted under the wrong account: %s", data)
	}
}

func TestElevationLaunchHasNoScriptFile(t *testing.T) {
	cmd := elevationLaunchCommand()
	if strings.Contains(cmd, "'-File'") || !strings.Contains(cmd, "-EncodedCommand") {
		t.Fatalf("elevated command must not run a user-writable script: %s", cmd)
	}
	if !strings.Contains(cmd, "uac-dismissed") {
		t.Fatal("dismissed UAC must stay distinguishable")
	}
}

func TestWrongAccountElevationHasItsOwnMessage(t *testing.T) {
	failure := processFailure("install", "mirror", "JIESHANGAI_ELEVATION_FAILED exit=5", errors.New("exit status 5"))
	if !strings.Contains(installErrorMessage(failure), "当前 Windows 账号") {
		t.Fatalf("message: %s", installErrorMessage(failure))
	}
}
