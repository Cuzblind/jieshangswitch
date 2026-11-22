package clientruntime

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSkill(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func skillByDir(list []Skill, dir string) (Skill, bool) {
	for _, s := range list {
		if s.Dir == dir {
			return s, true
		}
	}
	return Skill{}, false
}

func TestSkillImportEnableAcrossAssistantsAndRestore(t *testing.T) {
	t.Setenv("RELAY_HOME", t.TempDir())
	claudeDir, _ := skillAgentDir("claude")
	codexDir, _ := skillAgentDir("codex")
	writeSkill(t, filepath.Join(claudeDir, "paper"), "---\nname: 论文写作\ndescription: \"学术语气\"\n---\n正文")

	list, err := ListSkills("claude")
	if err != nil {
		t.Fatal(err)
	}
	if s, ok := skillByDir(list, "paper"); !ok || s.Managed || !s.Enabled || s.Name != "论文写作" || s.Description != "学术语气" {
		t.Fatalf("unmanaged skill not listed: %+v", list)
	}
	if list, err = ImportSkill("claude", "paper"); err != nil {
		t.Fatal(err)
	}
	if s, _ := skillByDir(list, "paper"); !s.Managed || !s.Enabled {
		t.Fatalf("import did not take the skill under management: %+v", s)
	}
	if list, err = SetSkillEnabled("codex", "paper", true); err != nil {
		t.Fatal(err)
	}
	if s, _ := skillByDir(list, "paper"); !s.Enabled {
		t.Fatal("not enabled for codex")
	}
	if _, err := os.Stat(filepath.Join(codexDir, "paper", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := DeleteSkill("codex", "paper"); err == nil {
		t.Fatal("deleted a skill that is still enabled")
	}
	if err := clearSkills(); err != nil {
		t.Fatal(err)
	}
	// The user's own folder survives a restore; our copy for Codex does not.
	if _, err := os.Stat(filepath.Join(claudeDir, "paper", "SKILL.md")); err != nil {
		t.Fatal("restore removed the user's original skill")
	}
	if hasMarker(filepath.Join(claudeDir, "paper")) {
		t.Fatal("restore left the marker behind")
	}
	if _, err := os.Stat(filepath.Join(codexDir, "paper")); !os.IsNotExist(err) {
		t.Fatal("restore left the copy in Codex")
	}
	// Still stored, so it can be enabled again.
	if list, err = SetSkillEnabled("codex", "paper", true); err != nil {
		t.Fatal(err)
	}
	if s, _ := skillByDir(list, "paper"); !s.Enabled {
		t.Fatal("could not enable again after restore")
	}
}

func TestSkillNeverOverwritesUserFolder(t *testing.T) {
	t.Setenv("RELAY_HOME", t.TempDir())
	claudeDir, _ := skillAgentDir("claude")
	codexDir, _ := skillAgentDir("codex")
	writeSkill(t, filepath.Join(claudeDir, "tool"), "ours")
	writeSkill(t, filepath.Join(codexDir, "tool"), "theirs")
	if _, err := ImportSkill("claude", "tool"); err != nil {
		t.Fatal(err)
	}
	list, err := ListSkills("codex")
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := skillByDir(list, "tool"); !s.Conflict || s.Enabled {
		t.Fatalf("conflict not reported: %+v", s)
	}
	if _, err := SetSkillEnabled("codex", "tool", true); err == nil {
		t.Fatal("overwrote the user's folder")
	}
	if _, err := SetSkillEnabled("codex", "tool", false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(codexDir, "tool", "SKILL.md"))
	if string(data) != "theirs" {
		t.Fatal("user's folder was changed")
	}
}

func TestSkillDisableKeepsEditsAndRejectsBadNames(t *testing.T) {
	t.Setenv("RELAY_HOME", t.TempDir())
	claudeDir, _ := skillAgentDir("claude")
	writeSkill(t, filepath.Join(claudeDir, "notes"), "v1")
	if _, err := ImportSkill("claude", "notes"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "notes", "SKILL.md"), []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := SetSkillEnabled("claude", "notes", false); err != nil {
		t.Fatal(err)
	}
	if _, err := SetSkillEnabled("claude", "notes", true); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(claudeDir, "notes", "SKILL.md"))
	if string(data) != "v2" {
		t.Fatalf("hand edit lost: %q", data)
	}
	for _, bad := range []string{"../x", "a/b", ".hidden", ""} {
		if _, err := ImportSkill("claude", bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	if _, err := ListSkills("gemini"); err == nil {
		t.Fatal("accepted an unknown assistant")
	}
}
