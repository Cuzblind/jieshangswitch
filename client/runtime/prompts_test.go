package clientruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnablePromptKeepsHandWrittenFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("RELAY_HOME", home)
	target := filepath.Join(home, ".claude", "CLAUDE.md")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("我手写的规则\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	list, err := SavePrompt("claude", "", "论文写作", "请用学术语气")
	if err != nil {
		t.Fatal(err)
	}
	if list, err = EnablePrompt("claude", list[0].ID); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "请用学术语气" {
		t.Fatalf("live file = %q", got)
	}
	backup := false
	for _, p := range list {
		if strings.Contains(p.Content, "我手写的规则") && !p.Enabled {
			backup = true
		}
	}
	if !backup {
		t.Fatalf("hand-written file was not preserved: %+v", list)
	}
}

func TestEnabledPromptFollowsEditsAndCannotBeDeleted(t *testing.T) {
	t.Setenv("RELAY_HOME", t.TempDir())
	list, _ := SavePrompt("codex", "", "代码审查", "先读测试")
	id := list[0].ID
	if _, err := EnablePrompt("codex", id); err != nil {
		t.Fatal(err)
	}
	if _, err := SavePrompt("codex", id, "代码审查", "先读测试，再改代码"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(homeDir(), ".codex", "AGENTS.md"))
	if string(got) != "先读测试，再改代码" {
		t.Fatalf("live file = %q", got)
	}
	if _, err := DeletePrompt("codex", id); err == nil {
		t.Fatal("deleted an enabled prompt")
	}
	if _, err := EnablePrompt("codex", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := DeletePrompt("codex", id); err != nil {
		t.Fatal(err)
	}
}

func TestPromptRejectsBadInput(t *testing.T) {
	t.Setenv("RELAY_HOME", t.TempDir())
	if _, err := SavePrompt("gemini", "", "x", "y"); err == nil {
		t.Fatal("accepted an unknown assistant")
	}
	if _, err := SavePrompt("claude", "", "  ", "y"); err == nil {
		t.Fatal("accepted an empty name")
	}
	if _, err := SavePrompt("claude", "", "x", strings.Repeat("a", maxPromptContent+1)); err == nil {
		t.Fatal("accepted oversized content")
	}
}

func TestPromptActionsThroughDesktop(t *testing.T) {
	t.Setenv("RELAY_HOME", t.TempDir())
	app := NewDesktop(Env{}, "test")
	if err := app.DesktopAction(DesktopRequest{Action: "prompt-save", Agent: "claude", Name: "写作", Content: "简洁"}); err != nil {
		t.Fatal(err)
	}
	list := promptSnapshot()["claude"]
	if len(list) != 1 || list[0].Name != "写作" {
		t.Fatalf("cache not updated: %+v", list)
	}
	if err := app.DesktopAction(DesktopRequest{Action: "prompt-enable", Agent: "claude", PromptID: list[0].ID}); err != nil {
		t.Fatal(err)
	}
	if !promptSnapshot()["claude"][0].Enabled {
		t.Fatal("enable not reflected in state")
	}
	if err := app.DesktopAction(DesktopRequest{Action: "prompt-save", Agent: "gemini", Name: "x"}); err == nil {
		t.Fatal("accepted an unknown assistant")
	}
}
