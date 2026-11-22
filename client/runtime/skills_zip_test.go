package clientruntime

import (
	"archive/zip"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	for name, body := range files {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func leftovers(t *testing.T, dir string) []string {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".jieshangai-") {
			out = append(out, e.Name())
		}
	}
	return out
}

// Skills are usually shared as a zip holding one folder with a SKILL.md.
func TestSkillZipWithFolderIsImported(t *testing.T) {
	t.Setenv("RELAY_HOME", t.TempDir())
	claudeDir, _ := skillAgentDir("claude")
	zipPath := filepath.Join(t.TempDir(), "download.zip")
	writeZip(t, zipPath, map[string]string{
		"论文写作/SKILL.md":      "---\nname: 论文写作\n---\n",
		"论文写作/refs/style.md": "style",
		"__MACOSX/._论文写作":    "junk",
	})
	list, err := ImportSkillZip("claude", zipPath)
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := skillByDir(list, "论文写作"); !s.Managed || !s.Enabled {
		t.Fatalf("zip skill not managed: %+v", list)
	}
	if _, err := os.Stat(filepath.Join(claudeDir, "论文写作", "refs", "style.md")); err != nil {
		t.Fatal("nested file missing")
	}
	if l := leftovers(t, claudeDir); len(l) > 0 {
		t.Fatalf("staging left behind: %v", l)
	}
}

// A zip of the folder's contents names the skill after the zip file.
func TestSkillZipWithRootSkillUsesZipName(t *testing.T) {
	t.Setenv("RELAY_HOME", t.TempDir())
	zipPath := filepath.Join(t.TempDir(), "notes.zip")
	writeZip(t, zipPath, map[string]string{"SKILL.md": "body"})
	list, err := ImportSkillZip("codex", zipPath)
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := skillByDir(list, "notes"); !s.Managed {
		t.Fatalf("root skill not imported as notes: %+v", list)
	}
}

func TestSkillZipRefusesUnsafeOrUnusableArchives(t *testing.T) {
	t.Setenv("RELAY_HOME", t.TempDir())
	claudeDir, _ := skillAgentDir("claude")
	base := t.TempDir()
	cases := map[string]map[string]string{
		"escape.zip":  {"skill/SKILL.md": "x", "../evil.txt": "x"},
		"empty.zip":   {"readme.txt": "no skill here"},
		"two.zip":     {"a/SKILL.md": "x", "b/SKILL.md": "x"},
		"badname.zip": {"con/SKILL.md": "x"},
	}
	for name, files := range cases {
		p := filepath.Join(base, name)
		writeZip(t, p, files)
		if _, err := ImportSkillZip("claude", p); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(claudeDir), "evil.txt")); !os.IsNotExist(err) {
		t.Fatal("archive wrote outside the skills folder")
	}
	if l := leftovers(t, claudeDir); len(l) > 0 {
		t.Fatalf("staging left behind: %v", l)
	}
	if stored, _ := loadSkills(); len(stored) != 0 {
		t.Fatalf("failed imports were recorded: %+v", stored)
	}
}

// The user's own folder of the same name is never replaced.
func TestSkillZipNeverOverwritesExistingFolder(t *testing.T) {
	t.Setenv("RELAY_HOME", t.TempDir())
	claudeDir, _ := skillAgentDir("claude")
	writeSkill(t, filepath.Join(claudeDir, "tool"), "mine")
	zipPath := filepath.Join(t.TempDir(), "tool.zip")
	writeZip(t, zipPath, map[string]string{"tool/SKILL.md": "theirs"})
	if _, err := ImportSkillZip("claude", zipPath); err == nil {
		t.Fatal("overwrote the user's folder")
	}
	if data, _ := os.ReadFile(filepath.Join(claudeDir, "tool", "SKILL.md")); string(data) != "mine" {
		t.Fatalf("user's folder changed: %q", data)
	}
}

func TestSkillZipIsCheckedAgainstSkillLimits(t *testing.T) {
	t.Setenv("RELAY_HOME", t.TempDir())
	files := map[string]string{"big/SKILL.md": "x"}
	for i := 0; i <= maxSkillFiles; i++ {
		files[filepath.ToSlash(filepath.Join("big", "f", strings.Repeat("x", 3)+string(rune('a'+i%26))+strings.Repeat("0", 1)+itoa(i)))] = ""
	}
	zipPath := filepath.Join(t.TempDir(), "big.zip")
	writeZip(t, zipPath, files)
	if _, err := ImportSkillZip("claude", zipPath); err == nil {
		t.Fatal("zip over the file limit accepted")
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for ; i > 0; i /= 10 {
		b = append([]byte{byte('0' + i%10)}, b...)
	}
	return string(b)
}

// The engine takes the zip path only from the request field the native picker fills.
func TestDesktopSkillZipAction(t *testing.T) {
	g := &fakeGW{models: 200}
	gw := httptest.NewServer(g.handler())
	defer gw.Close()
	a, _ := testDesktopApp(t, gw)
	zipPath := filepath.Join(t.TempDir(), "shared.zip")
	writeZip(t, zipPath, map[string]string{"shared/SKILL.md": "x"})
	if err := a.DesktopAction(DesktopRequest{Action: "skill-import-zip", Agent: "claude", SkillZip: zipPath}); err != nil {
		t.Fatal(err)
	}
	list, _ := ListSkills("claude")
	if s, _ := skillByDir(list, "shared"); !s.Managed {
		t.Fatalf("skill-import-zip did not import: %+v", list)
	}
}
