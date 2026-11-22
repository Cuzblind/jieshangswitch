//go:build windows

package clientruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A root spelled with forward slashes used to put absolute keys in the manifest:
// restore matched nothing, kept the secret in settings.json and still exited 0.
func TestRestoreWorksWhenHomeUsesForwardSlashes(t *testing.T) {
	home := filepath.ToSlash(t.TempDir())
	t.Setenv("RELAY_HOME", home)
	if err := engineWrite("claude", "https://relay.example.com", fakeKey); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(home, ".jieshangai", "backup-manifest.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(manifest), "\n.claude/settings.json\t") {
		t.Fatalf("manifest key is not relative: %q", manifest)
	}
	if _, err := engineRestore(true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Fatal("restore left the secret behind")
	}
}

func TestRestoreReadsAbsoluteKeysFromOlderManifests(t *testing.T) {
	home := t.TempDir()
	t.Setenv("RELAY_HOME", home)
	if err := engineWrite("claude", "https://relay.example.com", fakeKey); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(home, ".jieshangai", "backup-manifest.tsv")
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	absolute := filepath.ToSlash(home) + "/.claude/settings.json\t"
	updated := strings.Replace(string(manifest), ".claude/settings.json\t", absolute, 1)
	if err := os.WriteFile(manifestPath, []byte(updated), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := engineRestore(true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Fatal("older manifest left the secret behind")
	}
}
