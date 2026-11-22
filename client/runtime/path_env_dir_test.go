package clientruntime

import (
	"path/filepath"
	"testing"
)

// The persisted PATH entry must be the directory writeLauncher fills.
func TestToolBinDirMatchesLauncherDirectory(t *testing.T) {
	t.Setenv("RELAY_HOME", t.TempDir())
	m := newToolManager()
	if want := filepath.Join(m.root, "bin"); toolBinDir() != want {
		t.Fatalf("toolBinDir=%q, launcher dir=%q", toolBinDir(), want)
	}
}
