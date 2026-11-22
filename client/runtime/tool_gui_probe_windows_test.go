//go:build windows

package clientruntime

import (
	"strings"
	"testing"
	"time"
)

// Listing every installed package and filtering it was the slow part of the
// desktop-app check; ask for the known package names directly.
func TestInspectGUIQueriesPackagesByName(t *testing.T) {
	script := inspectGUIScript([]string{"AnthropicPBC.Claude", "Claude"})
	if !strings.Contains(script, "Get-AppxPackage -Name") || strings.Contains(script, "Get-AppxPackage |") {
		t.Fatalf("script still lists every package: %s", script)
	}
	if guiProbeTimeout < 20*time.Second {
		t.Fatalf("desktop-app check gives up after %s on a slow machine", guiProbeTimeout)
	}
}
