package clientruntime

import (
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// The skill folder travels in its own field: promptId is capped at 64 bytes,
// which a Chinese folder name of 22 characters already exceeds.
func TestDesktopSkillActionUsesSkillDir(t *testing.T) {
	g := &fakeGW{models: 200}
	gw := httptest.NewServer(g.handler())
	defer gw.Close()
	a, _ := testDesktopApp(t, gw)
	claudeDir, _ := skillAgentDir("claude")
	dir := "论文写作与学术语气润色专用技能包第二版测试名称"
	writeSkill(t, filepath.Join(claudeDir, dir), "body")
	if err := a.DesktopAction(DesktopRequest{Action: "skill-import", Agent: "claude", SkillDir: dir}); err != nil {
		t.Fatal(err)
	}
	list, err := ListSkills("claude")
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := skillByDir(list, dir); !s.Managed {
		t.Fatalf("skill-import ignored skillDir: %+v", s)
	}
}
