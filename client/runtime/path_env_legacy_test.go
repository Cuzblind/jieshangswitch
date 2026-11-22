//go:build !windows

package clientruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A machine that ran the earlier build has a block pointing at the tools root.
// Installing again must move it to the launcher directory, not keep both.
func TestPersistentToolPathReplacesLegacyBlock(t *testing.T) {
	home := t.TempDir()
	t.Setenv("RELAY_HOME", home)
	legacy := pathBlockStart + "\nexport PATH=" + shellQuote(legacyToolPathEntry()) + ":$PATH\n" + pathBlockEnd + "\n"
	for _, name := range []string{".zprofile", ".bash_profile"} {
		if err := os.WriteFile(filepath.Join(home, name), []byte("export A=1\n"+legacy), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := ensurePersistentToolPath(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".zprofile", ".bash_profile"} {
		b, err := os.ReadFile(filepath.Join(home, name))
		if err != nil {
			t.Fatal(err)
		}
		text := string(b)
		if strings.Count(text, pathBlockStart) != 1 || !strings.Contains(text, shellQuote(toolBinDir())) {
			t.Fatalf("%s not corrected to launcher dir:\n%s", name, text)
		}
		if strings.Contains(text, shellQuote(legacyToolPathEntry())) || !strings.Contains(text, "export A=1") {
			t.Fatalf("%s kept legacy entry or lost user content:\n%s", name, text)
		}
	}
	if err := removePersistentToolPath(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".zprofile", ".bash_profile"} {
		b, _ := os.ReadFile(filepath.Join(home, name))
		if strings.Contains(string(b), pathBlockStart) || !strings.Contains(string(b), "export A=1") {
			t.Fatalf("%s not cleanly restored:\n%s", name, b)
		}
	}
}
