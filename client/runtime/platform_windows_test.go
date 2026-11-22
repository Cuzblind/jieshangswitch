//go:build windows

package clientruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTerminalWrapperScriptStaysASCIIRelativeAndScoped(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		s := terminalWrapperScript(agent, "key-"+agent)
		for i := 0; i < len(s); i++ {
			if s[i] > 0x7e {
				t.Fatalf("%s: non-ASCII byte %q would be corrupted by cmd codepages", agent, s[i])
			}
		}
		// Paths must come from runtime expansion, never embedded absolutes that
		// break on non-ASCII profile directories.
		if !strings.Contains(s, "set \"PATH=%~dp0..\\tools\\bin;%PATH%\"") {
			t.Fatalf("%s: PATH line is not runtime-relative: %q", agent, s)
		}
		if !strings.Contains(s, "call \"%~dp0..\\tools\\bin\\"+agent+".cmd\"") {
			t.Fatalf("%s: shim call missing: %q", agent, s)
		}
		if agent == "codex" && !strings.Contains(s, "JIESHANGAI_CODEX_KEY=key-codex") {
			t.Fatalf("codex session key missing: %q", s)
		}
		if agent == "claude" && strings.Contains(s, "JIESHANGAI_CODEX_KEY") {
			t.Fatalf("claude wrapper must not carry the codex key: %q", s)
		}
	}
}

func TestCmdSafeEnvValue(t *testing.T) {
	for _, v := range []string{"", "ascii-key-123", "aB+/=~"} {
		if !cmdSafeEnvValue(v) {
			t.Fatalf("batch-safe value rejected: %q", v)
		}
	}
	for _, v := range []string{"a%PATH%", "密码", "tab\tchar", "nl\nchar"} {
		if cmdSafeEnvValue(v) {
			t.Fatalf("batch-unsafe value accepted: %q", v)
		}
	}
}

func TestShortPathNeverReturnsSpaces(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "wrapper.cmd")
	if err := os.WriteFile(p, []byte("@echo off\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// The exact result depends on 8.3-name availability; both outcomes are
	// valid as long as a returned path is usable as one command-line token.
	if sp := shortPath(p); sp != "" && strings.ContainsAny(sp, " \"") {
		t.Fatalf("short path still contains separators: %q", sp)
	}
	if sp := shortPath(filepath.Join(dir, "definitely-missing-file")); sp != "" {
		t.Fatalf("short path returned for a missing file: %q", sp)
	}
}
