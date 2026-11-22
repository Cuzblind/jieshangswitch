package clientruntime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkDirPersistenceAndLaunchDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("RELAY_HOME", home)
	desktop := filepath.Join(home, "Desktop")
	if err := os.MkdirAll(desktop, 0o755); err != nil {
		t.Fatal(err)
	}

	// Default points to Desktop/接上AI工作区 without eagerly creating it on snapshot.
	dir, recent := workDirSnapshot()
	wantDefault := filepath.Join(desktop, defaultWorkspaceFolderName)
	if dir != wantDefault {
		t.Fatalf("default workDir = %q, want %q", dir, wantDefault)
	}
	if len(recent) != 0 {
		t.Fatalf("expected empty recentDirs, got %v", recent)
	}
	if _, err := os.Stat(wantDefault); !os.IsNotExist(err) {
		t.Fatalf("snapshot should not eagerly create default workspace dir")
	}

	// Launch resolves and creates the default folder on demand.
	resolved := resolveLaunchWorkDir()
	if resolved != wantDefault {
		t.Fatalf("resolveLaunchWorkDir = %q, want %q", resolved, wantDefault)
	}
	if st, err := os.Stat(wantDefault); err != nil || !st.IsDir() {
		t.Fatalf("expected default workspace dir to be created on launch: %v", err)
	}

	// Setting a real user directory persists and deduplicates recentDirs.
	projA := filepath.Join(home, "paper-a")
	projB := filepath.Join(home, "paper-b")
	_ = os.MkdirAll(projA, 0o755)
	_ = os.MkdirAll(projB, 0o755)

	got, rec, err := setWorkDir(projA)
	if err != nil || got != projA || len(rec) != 1 || rec[0] != projA {
		t.Fatalf("setWorkDir(A) = (%q, %v, %v)", got, rec, err)
	}
	got, rec, err = setWorkDir(projB)
	if err != nil || got != projB || len(rec) != 2 || rec[0] != projB || rec[1] != projA {
		t.Fatalf("setWorkDir(B) = (%q, %v, %v)", got, rec, err)
	}
	if resolveLaunchWorkDir() != projB {
		t.Fatalf("expected resolveLaunchWorkDir to return chosen dir %q", projB)
	}

	// Invalid / non-existent directory is rejected without corrupting state.
	if _, _, err := setWorkDir(filepath.Join(home, "does-not-exist")); err == nil {
		t.Fatal("expected error for missing directory")
	}
	if _, _, err := setWorkDir("relative/path"); err == nil {
		t.Fatal("expected error for relative path")
	}

	// Resetting with empty string returns to default workspace.
	got, _, err = setWorkDir("")
	if err != nil || got != wantDefault {
		t.Fatalf("reset workDir = (%q, %v), want %q", got, err, wantDefault)
	}
}
