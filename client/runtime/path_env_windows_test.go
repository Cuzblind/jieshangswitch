//go:build windows

package clientruntime

import (
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

// Reproduces the Win10 beta.5 field report: both CLI installs were marked
// failed with "写入命令行 PATH 失败：This operation returned because the
// timeout period expired." although the CLIs verified working. That text is
// ERROR_TIMEOUT (1460) — the code SendMessageTimeoutW leaves in GetLastError
// when one hung top-level window misses the 5s WM_SETTINGCHANGE budget (258 /
// WAIT_TIMEOUT is WaitForSingleObject's variant of the same benign signal).
// HKCU\Environment\Path is already written when the broadcast runs, and the
// message reaches the remaining windows anyway, so the timeout must not fail
// the install.
func TestBroadcastWaitTimeoutDoesNotFailPathWrite(t *testing.T) {
	if windows.ERROR_TIMEOUT != syscall.Errno(1460) {
		t.Fatalf("ERROR_TIMEOUT is no longer errno 1460 (the field-report code): %v", windows.ERROR_TIMEOUT)
	}
	if err := broadcastResult(windows.ERROR_TIMEOUT); err != nil {
		t.Fatalf("hung-window broadcast timeout must not fail PATH writing: %v", err)
	}
	if err := broadcastResult(windows.WAIT_TIMEOUT); err != nil {
		t.Fatalf("wait-timeout variant must not fail PATH writing either: %v", err)
	}
	if err := broadcastResult(syscall.Errno(0)); err != nil {
		t.Fatalf("clean call classified as failure: %v", err)
	}
	if err := broadcastResult(nil); err != nil {
		t.Fatalf("nil call classified as failure: %v", err)
	}
	if err := broadcastResult(windows.ERROR_ACCESS_DENIED); err == nil {
		t.Fatal("genuine broadcast failures must still surface")
	}
}
