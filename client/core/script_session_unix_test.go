//go:build !windows

package core

import (
	"os/exec"
	"syscall"
	"testing"
)

func isolateScript(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
