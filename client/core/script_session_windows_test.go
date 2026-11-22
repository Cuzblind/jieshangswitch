//go:build windows

package core

import (
	"os/exec"
	"testing"
)

func isolateScript(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	t.Skip("POSIX shell parity runs on Linux/macOS; Windows runs the native PowerShell contract tests")
}
