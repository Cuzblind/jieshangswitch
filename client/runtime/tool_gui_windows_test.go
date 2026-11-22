//go:build windows

package clientruntime

import (
	"testing"
)

func TestAppxNeedsElevation(t *testing.T) {
	for _, output := range []string{
		"The operation requires elevation",
		"The package installation failed because administrator privileges are required. (0x80073D28)",
	} {
		if !appxNeedsElevation(output) {
			t.Errorf("expected elevation fallback for %q", output)
		}
	}
	for _, output := range []string{
		"Add-AppxPackage failed: Access is denied",
		"HRESULT 0x80070005",
		"The package deployment failed because the package is in use. (0x80073D02)",
		"The package signature could not be verified",
	} {
		if appxNeedsElevation(output) {
			t.Errorf("unexpected elevation fallback for %q", output)
		}
	}
}

// Field regression (Win10, 0.2.0-beta.5): the MSIX install succeeded, then the
// launch failed with
//
//	fork/exec C:\Program Files\WindowsApps\Claude_4.29.0.0_x64__pzs8sxrjxfjjc\app\Claude.exe: Access is denied.
//
// WindowsApps folders grant execute only through shell (Start-menu style)
// activation, not CreateProcess from a normal caller. Packaged apps must be
// activated as shell:AppsFolder\<PackageFamilyName>!<ApplicationId>; a direct
// fork/exec on the InstallLocation exe must never be attempted for them.
func TestPackagedGUIUsesShellActivationTarget(t *testing.T) {
	info := guiInfo{
		Path:   `C:\Program Files\WindowsApps\Claude_4.29.0.0_x64__pzs8sxrjxfjjc\app\Claude.exe`,
		Family: "Claude_pzs8sxrjxfjjc",
		AppID:  "Claude",
	}
	if got := shellActivateTarget(info); got != `shell:AppsFolder\Claude_pzs8sxrjxfjjc!Claude` {
		t.Fatalf("packaged app must activate via shell:AppsFolder\\family!appId, got %q", got)
	}
	// Anything missing from the manifest lookup (old dev install under
	// LOCALAPPDATA, appId parse failure) keeps the direct-exec fallback.
	for _, info := range []guiInfo{
		{Path: `C:\Users\a\AppData\Local\AnthropicClaude\claude.exe`},
		{Path: `C:\Program Files\WindowsApps\Claude_4.29.0.0_x64__pzs8sxrjxfjjc\app\Claude.exe`, Family: "Claude_pzs8sxrjxfjjc"},
		{Path: `C:\Program Files\WindowsApps\Claude_4.29.0.0_x64__pzs8sxrjxfjjc\app\Claude.exe`, AppID: "Claude"},
	} {
		if got := shellActivateTarget(info); got != "" {
			t.Fatalf("non-packaged info must stay on direct exec, got %q", got)
		}
	}
}

func TestShellActivateReportsFailure(t *testing.T) {
	// A package id that cannot exist: ShellExecuteW returns <= 32 and the
	// wrapper must surface an error instead of reporting a successful launch.
	if err := shellActivate(`shell:AppsFolder\Jieshangai.Missing_zzzzzzz!Nope`); err == nil {
		t.Fatal("activating a missing package must be reported as a failure")
	}
}

func TestSamePathEntry(t *testing.T) {
	dir := `C:\Users\a\.jieshangai\tools\bin`
	for _, entry := range []string{dir, dir + `\`, "  " + dir + " ", `c:\users\A\.jieshangai\tools\BIN`} {
		if !samePathEntry(entry, dir) {
			t.Errorf("expected %q to match %q", entry, dir)
		}
	}
	if samePathEntry(`C:\Users\a\.jieshangai\tools`, dir) {
		t.Error("tools root must not match the bin directory")
	}
}
