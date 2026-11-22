//go:build windows

package clientruntime

import (
	"os"
	"path/filepath"
	"testing"
)

// Field regression found via the skill suite: writePrivateFile also restricts
// the file's parent DIRECTORY. That grant carried no inheritance flags, so
// Windows' automatic ACL propagation removed the subdirectories' inherited
// ACEs (they mirrored the directory's old inheritable ACEs) while the new
// object-only ACE gave them nothing to re-inherit — children ended up with an
// EMPTY (deny-all) DACL and every later open failed with Access denied. The
// directory grant must be inheritable so propagation re-grants the children.
func TestWritePrivateFileKeepsSiblingDirectoriesAccessible(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "files", "skill", "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "SKILL.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writePrivateFile(filepath.Join(root, "index.json"), []byte("{}")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadDir(filepath.Join(root, "files")); err != nil {
		t.Fatalf("sibling directory became inaccessible after private write: %v", err)
	}
	if _, err := os.ReadFile(filepath.Join(nested, "SKILL.md")); err != nil {
		t.Fatalf("nested file became inaccessible after private write: %v", err)
	}
}
