package clientruntime

import "testing"

// The CLI answers in milliseconds while the desktop-app check takes seconds;
// until that check finished the desktop app is unknown, not missing.
func TestDesktopAppHasItsOwnInspectedFlag(t *testing.T) {
	home := t.TempDir()
	t.Setenv("RELAY_HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	m := newToolManager()
	m.probeCLI = func(string) (toolEntry, cliProbe) { return toolEntry{Program: "claude", Version: "2.1.0"}, cliFound }
	m.cliReady("claude")
	if s := m.state("claude"); !s.Inspected || s.GUIInspected {
		t.Fatalf("CLI probe must not mark the desktop app inspected: %+v", s)
	}
	// A saved CLI-only result must not claim the desktop app after a restart.
	if s := newToolManager().state("claude"); s.GUIInspected {
		t.Fatalf("restart marked an unchecked desktop app inspected: %+v", s)
	}
	m.recordGUI("claude", guiInfo{Path: `C:\apps\Claude.exe`, Version: "4.29.0.0"})
	if s := m.state("claude"); !s.GUIInspected {
		t.Fatalf("desktop-app check did not mark it inspected: %+v", s)
	}
	if s := newToolManager().state("claude"); !s.GUIInspected || s.GUIVersion != "4.29.0.0" {
		t.Fatalf("desktop-app result lost on restart: %+v", s)
	}
}
