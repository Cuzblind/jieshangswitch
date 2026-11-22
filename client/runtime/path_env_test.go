//go:build !windows

package clientruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPersistentToolPathUnixIsIdempotentAndRemovable(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("unix path implementation only")
	}
	home := t.TempDir()
	t.Setenv("RELAY_HOME", home)
	dir := filepath.Join(home, ".jieshangai", "tools", "bin")
	if err := ensureUserPath(dir); err != nil {
		t.Fatal(err)
	}
	if err := ensureUserPath(dir); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".zprofile", ".bash_profile"} {
		b, err := os.ReadFile(filepath.Join(home, name))
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Count(string(b), pathBlockStart); got != 1 {
			t.Fatalf("%s block count=%d", name, got)
		}
	}
	if err := removeUserPath(dir); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".zprofile", ".bash_profile"} {
		b, err := os.ReadFile(filepath.Join(home, name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), pathBlockStart) || strings.Contains(string(b), dir) {
			t.Fatalf("%s still contains managed path", name)
		}
	}
}
