package clientruntime

import (
	"os"
	"path/filepath"
	"testing"
)

// The bundle directory may be reached through a link (macOS /var, Windows 8.3
// names): that must not reject a genuine payload, while a payload link leaving
// the bundle must still be refused.
func TestOfflineFileThroughLinkedDirectory(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.Mkdir(real, 0700); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(base, "linked")
	if err := os.Symlink(real, linked); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	payload := filepath.Join(real, "payload")
	if err := os.WriteFile(payload, []byte("vendor"), 0600); err != nil {
		t.Fatal(err)
	}
	hash, err := fileDigest(payload)
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(base, "outside")
	if err := os.WriteFile(outside, []byte("vendor"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(real, "escape")); err != nil {
		t.Fatal(err)
	}
	b := offlineBundle{Files: map[string]string{"payload": hash, "escape": hash}}
	if _, err := offlineFile(linked, b, "payload"); err != nil {
		t.Fatalf("genuine payload rejected: %v", err)
	}
	if _, err := offlineFile(linked, b, "escape"); err == nil {
		t.Fatal("payload link escaping the bundle accepted")
	}
}
