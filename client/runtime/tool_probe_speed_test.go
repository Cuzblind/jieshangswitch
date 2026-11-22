package clientruntime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A CLI we installed carries the version verified at install time; starting a
// large Node script just to read it again took seconds on a cold VM.
func TestManagedCLIVersionComesFromItsReceipt(t *testing.T) {
	m := newToolManager()
	m.root = t.TempDir()
	program := filepath.Join(m.root, "claude", "2.1.0", "not-runnable")
	if err := os.MkdirAll(filepath.Dir(program), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(program, []byte("not a program"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(toolEntry{Program: program, Version: "2.1.0"})
	if err := os.WriteFile(m.receipt("claude"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if !m.cliReady("claude") || m.state("claude").CLIVersion != "2.1.0" {
		t.Fatalf("receipt not used: %+v", m.state("claude"))
	}
	// A receipt whose program is gone is not trusted.
	_ = os.Remove(program)
	m.forgetCLIProbe("claude")
	if m.cliReady("claude") {
		t.Fatal("receipt trusted after its program was removed")
	}
}

// A probe that timed out says nothing about the install; it must not turn an
// installed CLI into "待安装".
func TestCLIProbeTimeoutKeepsLastResult(t *testing.T) {
	m := newToolManager()
	m.root = t.TempDir()
	m.put(ToolStatus{Agent: "claude", CLIInstalled: true, CLIVersion: "2.1.0", Inspected: true})
	m.probeCLI = func(string) (toolEntry, cliProbe) { return toolEntry{}, cliUnknown }
	if !m.cliReady("claude") {
		t.Fatal("timeout reported the CLI as missing")
	}
	if s := m.state("claude"); !s.CLIInstalled || s.CLIVersion != "2.1.0" {
		t.Fatalf("timeout erased the last result: %+v", s)
	}
	m.put(ToolStatus{Agent: "codex"})
	m.cliReady("codex")
	if s := m.state("codex"); s.Inspected {
		t.Fatalf("a first probe that timed out must stay 'checking': %+v", s)
	}
}

// Opening the app shows the last known versions at once, then refreshes them.
func TestLastInspectionShownAtStartup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("RELAY_HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	m := newToolManager()
	m.probeCLI = func(string) (toolEntry, cliProbe) { return toolEntry{Program: "claude", Version: "2.1.0"}, cliFound }
	m.cliReady("claude")
	m.recordGUI("claude", guiInfo{Path: `C:\apps\Claude.exe`, Version: "4.29.0.0"})

	next := newToolManager()
	s := next.state("claude")
	if !s.Inspected || !s.CLIInstalled || s.CLIVersion != "2.1.0" || !s.GUIInstalled || s.GUIVersion != "4.29.0.0" {
		t.Fatalf("last inspection not shown at startup: %+v", s)
	}
	if !strings.Contains(m.diagnostics("claude"), "elapsed_ms") {
		t.Fatal("probe timing missing from diagnostics")
	}
}
