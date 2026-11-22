//go:build windows

package clientruntime

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestWindowsTerminalProbeLive is a manual acceptance check for the Windows
// Terminal launch tier: it opens one tab that echoes a marker and writes a
// marker file next to the wrapper. The wt stub returns before the tab actually
// spawns, so exit status alone proves nothing — the marker file is the real
// assertion, and the test stays alive until the tab has run (or clearly
// failed). CI never runs it (no interactive desktop); set JIESHANGAI_WT_PROBE=1
// to execute.
func TestWindowsTerminalProbeLive(t *testing.T) {
	if os.Getenv("JIESHANGAI_WT_PROBE") != "1" {
		t.Skip("live desktop probe; set JIESHANGAI_WT_PROBE=1")
	}
	wt := windowsTerminalPath()
	if wt == "" {
		t.Fatal("wt.exe was not detected on this machine")
	}
	t.Log("wt at:", wt)
	dir := t.TempDir()
	wrapper := filepath.Join(dir, "probe-terminal.cmd")
	// Bare command names are deliberately avoided: machines with Git Bash on
	// PATH resolve them to MSYS binaries with different syntax.
	script := "@echo off\r\n" +
		"@echo JIESHANGAI_WT_OK\r\n" +
		"@echo ok > \"%~dp0wt-ok.marker\"\r\n"
	if err := os.WriteFile(wrapper, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	target := wrapper
	if sp := shortPath(target); sp != "" {
		target = sp
	}
	started := time.Now()
	c := exec.Command(wt, "-w", "0", "new-tab", "--title", "JieshangAI WT Probe", "--startingDirectory", dir, "cmd", "/c", target)
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("wt stub failed: %v %s", err, out)
	}
	// The tab spawns asynchronously; cold-starting Windows Terminal can take a
	// few seconds on first launch.
	marker := filepath.Join(dir, "wt-ok.marker")
	deadline := time.Now().Add(20 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			t.Logf("tab executed the wrapper after %v (marker file written)", time.Since(started).Round(time.Millisecond))
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("wt stub returned but the tab never ran the wrapper within 20s — check the error text in the Windows Terminal window (quoting/startingDirectory issue)")
		}
		time.Sleep(200 * time.Millisecond)
	}
}
