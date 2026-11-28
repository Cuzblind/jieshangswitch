package clientruntime

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func buildTestSkillZipBytes(t *testing.T, slug, markdown string) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(slug + "/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(markdown)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(sum[:])
}

func TestDesktopRemoteSkillsCatalogAndInstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	zipBytes, zipSHA := buildTestSkillZipBytes(t, "lit-review", "---\nname: lit-review\ndescription: Systematic literature review helper\n---\n# Literature Review\n")
	var lastAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastAuth = r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/api/install/skills":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data": map[string]any{
					"categories": []map[string]string{{"id": "literature", "label": "文献调研"}},
					"items": []map[string]any{
						{
							"id":                  1,
							"slug":                "lit-review",
							"title":               "系统文献综述",
							"category":            "literature",
							"tier":                "free",
							"summary":             "自动归纳检索结果",
							"supported_agents":    []string{"claude", "codex"},
							"install_command":     "npx skills add lab/lit-review",
							"has_install_command": true,
							"has_zip":             true,
							"zip_size_bytes":      len(zipBytes),
							"zip_sha256":          zipSHA,
							"unlocked":            true,
						},
					},
					"entitlement": map[string]any{
						"authenticated":    true,
						"paid_fen":         5000,
						"threshold_fen":    3000,
						"is_team_member":   false,
						"premium_unlocked": true,
					},
				},
			})
		case "/api/install/skills/lit-review/download":
			w.Header().Set("Content-Type", "application/zip")
			w.Header().Set("X-Content-SHA256", zipSHA)
			_, _ = w.Write(zipBytes)
		case "/api/install/skills/bad-sha/download":
			w.Header().Set("Content-Type", "application/zip")
			w.Header().Set("X-Content-SHA256", "0000000000000000000000000000000000000000000000000000000000000000")
			_, _ = w.Write(zipBytes)
		case "/api/install/skills/locked-pro/download":
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": false,
				"code":    "SKILL_PREMIUM_LOCKED",
				"message": "需要解锁",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	env := baseEnv()
	env.Origin = srv.URL
	env.Web = srv.URL
	env.HTTP = srv.Client()
	env.Configured = func(agent, origin string) (string, bool) {
		if agent == "claude" && origin == srv.URL {
			return "sk-test-desktop-key", true
		}
		return "", false
	}

	app := NewDesktop(env, "test")

	// 1. Load remote catalog via DesktopAction.
	if err := app.DesktopAction(DesktopRequest{Action: "skills-catalog-load", Agent: "claude"}); err != nil {
		t.Fatalf("skills-catalog-load failed: %v", err)
	}
	if lastAuth != "Bearer sk-test-desktop-key" {
		t.Fatalf("unexpected auth header: %q", lastAuth)
	}
	st := app.DesktopState()
	if st.SkillCatalog == nil || len(st.SkillCatalog.Items) != 1 || st.SkillCatalog.Items[0].Slug != "lit-review" {
		t.Fatalf("expected 1 remote skill in state, got %+v", st.SkillCatalog)
	}

	// 2. Reject invalid slug traversal.
	if err := app.DesktopAction(DesktopRequest{Action: "skill-install-remote", Agent: "claude", SkillDir: "../evil"}); err == nil {
		t.Fatal("expected error for invalid slug")
	}

	// 3. Reject SHA-256 mismatch.
	if err := app.DesktopAction(DesktopRequest{Action: "skill-install-remote", Agent: "claude", SkillDir: "bad-sha"}); err == nil {
		t.Fatal("expected error for sha256 mismatch")
	}

	// 4. Surface locked 403 clearly.
	if err := app.DesktopAction(DesktopRequest{Action: "skill-install-remote", Agent: "claude", SkillDir: "locked-pro"}); err == nil {
		t.Fatal("expected error for locked skill")
	}

	// 5. Full install into temp HOME and verify enabled in ~/.claude/skills/lit-review/SKILL.md.
	if err := app.DesktopAction(DesktopRequest{Action: "skill-install-remote", Agent: "claude", SkillDir: "lit-review"}); err != nil {
		t.Fatalf("skill-install-remote failed: %v", err)
	}
	installedMD := filepath.Join(home, ".claude", "skills", "lit-review", "SKILL.md")
	if _, err := os.Stat(installedMD); err != nil {
		t.Fatalf("expected installed SKILL.md at %s: %v", installedMD, err)
	}
	st = app.DesktopState()
	if st.View.Notice == "" {
		t.Fatal("expected success notice after remote skill install")
	}
	found := false
	for _, s := range st.Skills["claude"] {
		if s.Dir == "lit-review" && s.Enabled && s.Managed {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected lit-review in managed enabled claude skills, got %+v", st.Skills["claude"])
	}

	// 6. Root-level SKILL.md zip installed via preferred slug uses slug dir (never temp filename) and blocks duplicates.
	var rootBuf bytes.Buffer
	rzw := zip.NewWriter(&rootBuf)
	rw, err := rzw.Create("SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rw.Write([]byte("---\nname: Root Skill\ndescription: root level skill\n---\n")); err != nil {
		t.Fatal(err)
	}
	if err := rzw.Close(); err != nil {
		t.Fatal(err)
	}
	rootZipPath := filepath.Join(t.TempDir(), "jieshangai-skill-random999.zip")
	if err := os.WriteFile(rootZipPath, rootBuf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	list, err := ImportSkillZipWithName("claude", rootZipPath, "root-skill-slug")
	if err != nil {
		t.Fatalf("ImportSkillZipWithName failed: %v", err)
	}
	foundRoot := false
	for _, s := range list {
		if s.Dir == "root-skill-slug" {
			foundRoot = true
		}
	}
	if !foundRoot {
		t.Fatalf("expected root-skill-slug dir, got %+v", list)
	}
	if _, err := ImportSkillZipWithName("claude", rootZipPath, "root-skill-slug"); err == nil {
		t.Fatal("expected duplicate install of same slug to fail")
	}

	// 7. Connection invalidation clears cached skill catalog.
	app.connectionMu.Lock()
	app.invalidateConnectionData()
	app.connectionMu.Unlock()
	if app.DesktopState().SkillCatalog != nil {
		t.Fatal("expected skillCatalog cleared on invalidateConnectionData")
	}
}
