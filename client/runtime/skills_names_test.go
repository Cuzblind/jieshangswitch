package clientruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Folders are listed whatever their name, so import must accept the same
// everyday names; it used to allow only ASCII letters, digits, '.', '_' and '-'.
func TestSkillImportAcceptsEverydayFolderNames(t *testing.T) {
	t.Setenv("RELAY_HOME", t.TempDir())
	claudeDir, _ := skillAgentDir("claude")
	for _, dir := range []string{"论文 写作", "my skill (v2)", "a+b"} {
		writeSkill(t, filepath.Join(claudeDir, dir), "body")
		list, err := ImportSkill("claude", dir)
		if err != nil {
			t.Fatalf("%q: %v", dir, err)
		}
		if s, _ := skillByDir(list, dir); !s.Managed {
			t.Fatalf("%q not managed: %+v", dir, s)
		}
	}
	for _, bad := range []string{"../x", "a/b", `a\b`, ".hidden", "", "con", "NUL.txt", "x:y", "trail ", "trail.", strings.Repeat("长", 65)} {
		if _, err := ImportSkill("claude", bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

// A folder that cannot be managed is still listed, with the reason instead of
// an import button that is bound to fail.
func TestSkillListExplainsUnsupportedFolder(t *testing.T) {
	t.Setenv("RELAY_HOME", t.TempDir())
	claudeDir, _ := skillAgentDir("claude")
	long := strings.Repeat("a", 70)
	writeSkill(t, filepath.Join(claudeDir, long), "body")
	writeSkill(t, filepath.Join(claudeDir, "fine"), "body")
	list, err := ListSkills("claude")
	if err != nil {
		t.Fatal(err)
	}
	if s, ok := skillByDir(list, long); !ok || s.Unsupported == "" {
		t.Fatalf("long name not explained: %+v", s)
	}
	if s, _ := skillByDir(list, "fine"); s.Unsupported != "" {
		t.Fatalf("ordinary folder marked unsupported: %+v", s)
	}
}

// A marker left behind without an index entry (lost index, copied folder) used
// to be reported as "缺少 SKILL.md" and could never be imported.
func TestSkillImportRecoversStaleMarker(t *testing.T) {
	t.Setenv("RELAY_HOME", t.TempDir())
	claudeDir, _ := skillAgentDir("claude")
	dir := filepath.Join(claudeDir, "orphan")
	writeSkill(t, dir, "body")
	if err := os.WriteFile(filepath.Join(dir, skillMarker), []byte("enabled\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	list, err := ImportSkill("claude", "orphan")
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := skillByDir(list, "orphan"); !s.Managed || !s.Enabled {
		t.Fatalf("stale marker not recovered: %+v", s)
	}
}

// Linked skill folders used to vanish from the list without a word.
func TestSkillLinkedFolderIsListedAsUnsupported(t *testing.T) {
	t.Setenv("RELAY_HOME", t.TempDir())
	claudeDir, _ := skillAgentDir("claude")
	target := filepath.Join(t.TempDir(), "elsewhere")
	writeSkill(t, target, "body")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(claudeDir, "linked")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	list, err := ListSkills("claude")
	if err != nil {
		t.Fatal(err)
	}
	if s, ok := skillByDir(list, "linked"); !ok || s.Unsupported == "" || s.Managed {
		t.Fatalf("linked folder not listed with a reason: %+v (found %v)", s, ok)
	}
}
