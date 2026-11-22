package clientruntime

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Re-probing used to blank the CLI version first, so every snapshot taken
// during the 4s --version run showed "待安装" and the home screen flickered.
func TestCLIProbeKeepsLastResultWhileRunning(t *testing.T) {
	m := newToolManager()
	m.root = t.TempDir()
	m.put(ToolStatus{Agent: "claude", CLIInstalled: true, CLIVersion: "2.1.0"})
	started := make(chan struct{})
	release := make(chan struct{})
	m.probeCLI = func(string) (toolEntry, cliProbe) {
		close(started)
		<-release
		return toolEntry{Program: "claude", Version: "2.1.1"}, cliFound
	}
	done := make(chan bool)
	go func() { done <- m.cliReady("claude") }()
	<-started
	if s := m.state("claude"); !s.CLIInstalled || s.CLIVersion != "2.1.0" {
		t.Fatalf("probe blanked the last result: %+v", s)
	}
	close(release)
	if !<-done {
		t.Fatal("probe result lost")
	}
	if s := m.state("claude"); !s.CLIInstalled || s.CLIVersion != "2.1.1" || !s.Inspected {
		t.Fatalf("probe result not applied: %+v", s)
	}
}

// Several callers (inspect, refreshStatus, FindCLI) ask at once; one --version
// run must serve all of them, and a missing CLI must be cached too.
func TestCLIProbeRunsOnceForConcurrentCallersAndCachesMissing(t *testing.T) {
	m := newToolManager()
	m.root = t.TempDir()
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	m.probeCLI = func(string) (toolEntry, cliProbe) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return toolEntry{}, cliMissing
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); m.cliReady("codex") }()
	<-started
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); m.cliReady("codex") }()
	}
	time.Sleep(100 * time.Millisecond)
	close(release)
	wg.Wait()
	if n := calls.Load(); n != 1 {
		t.Fatalf("concurrent callers ran %d probes", n)
	}
	if m.cliReady("codex") {
		t.Fatal("missing CLI reported as installed")
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("missing CLI was not cached: %d probes", n)
	}
	if s := m.state("codex"); !s.Inspected {
		t.Fatalf("a finished probe must mark the tool inspected: %+v", s)
	}
}

// An unknown installed version must not light up the update button.
func TestNoUpdateWhenInstalledVersionUnknown(t *testing.T) {
	m := newToolManager()
	m.root = t.TempDir()
	m.put(ToolStatus{Agent: "claude", CLIInstalled: true, LatestCLI: "2.1.1", LatestGUI: "4.30.0.0"})
	for _, s := range m.snapshot() {
		if s.Agent == "claude" && s.UpdateAvailable {
			t.Fatalf("empty installed version reported as outdated: %+v", s)
		}
	}
	m.put(ToolStatus{Agent: "claude", CLIInstalled: true, CLIVersion: "2.1.0", LatestCLI: "2.1.1"})
	for _, s := range m.snapshot() {
		if s.Agent == "claude" && !s.UpdateAvailable {
			t.Fatalf("real update hidden: %+v", s)
		}
	}
}
