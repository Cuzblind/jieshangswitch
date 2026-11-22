//go:build windows

package clientruntime

import (
	"strings"
	"testing"
)

// Field regression (Win10, 0.2.0-beta.5): process environment does not cross the
// ShellExecute "runas" boundary, so the elevated script must carry its inputs in
// its own text.
func TestElevatedAppxScriptNeedsNoEnvironment(t *testing.T) {
	script := elevatedAppxScript("a.msix", "r.log", "S-1-5-21")
	if strings.Contains(script, "$env:") {
		t.Fatalf("elevated script depends on the environment: %s", script)
	}
}

// Shell activation needs shell:AppsFolder\<family>!<appId>, and the
// Application Id is only in the package manifest — it cannot be derived from
// the family name or the exe path. inspectGUI must therefore return it.
func TestInspectGUIScriptExtractsAppId(t *testing.T) {
	script := inspectGUIScript([]string{"AnthropicPBC.Claude", "Claude"})
	if !strings.Contains(script, "appId=$a.Id") {
		t.Fatal("inspectGUI script must extract the manifest Application Id for shell activation")
	}
}
