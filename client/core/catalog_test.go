package core

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestWriteCodexWithMultiBrandCatalogAndModelPreservation(t *testing.T) {
	p := Paths{Root: t.TempDir()}
	models := []CatalogModel{
		{ID: "claude-sonnet-4-5", DisplayName: "Claude Sonnet 4.5", Vendor: "Anthropic", ContextWindow: 200000},
		{ID: "gpt-5", DisplayName: "GPT-5", Vendor: "OpenAI", ContextWindow: 272000},
		{ID: "gemini-2.5-pro", DisplayName: "Gemini 2.5 Pro", Vendor: "Google", ContextWindow: 1000000},
		{ID: "deepseek-r1", DisplayName: "DeepSeek R1", Vendor: "DeepSeek", ContextWindow: 128000},
		{ID: "glm-4.7", DisplayName: "GLM 4.7", Vendor: "Zhipu", ContextWindow: 128000},
		{ID: "kimi-k2", DisplayName: "Kimi K2", Vendor: "Moonshot", ContextWindow: 128000},
	}
	allowed := []string{"claude-sonnet-4-5", "gpt-5", "gemini-2.5-pro", "deepseek-r1", "glm-4.7", "kimi-k2"}

	// Pre-populate an existing [profiles.work] table to prove model_catalog_json
	// lands in the TOML root table, never swallowed inside a profile table.
	if err := os.MkdirAll(filepath.Dir(p.CodexConfig()), 0o700); err != nil {
		t.Fatal(err)
	}
	preexisting := "[profiles.work]\napproval_policy = \"on-request\"\n"
	if err := os.WriteFile(p.CodexConfig(), []byte(preexisting), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := p.WriteCodex(CodexOpts{
		Origin:        "https://jieshangai.com",
		Secret:        "sk-test-codex-123",
		Model:         "gpt-5",
		CatalogModels: models,
		AllowedModels: allowed,
	}); err != nil {
		t.Fatalf("WriteCodex failed: %v", err)
	}
	cfgBytes, err := os.ReadFile(p.CodexConfig())
	if err != nil {
		t.Fatal(err)
	}
	cfg := string(cfgBytes)
	catIdx := strings.Index(cfg, "model_catalog_json = ")
	profileIdx := strings.Index(cfg, "[profiles.work]")
	if catIdx < 0 || profileIdx < 0 || catIdx > profileIdx {
		t.Fatalf("model_catalog_json must be placed at TOML root before [profiles.work], got:\n%s", cfg)
	}
	if !strings.Contains(cfg, `model = "gpt-5"`) {
		t.Fatalf("expected authorized default model gpt-5, got:\n%s", cfg)
	}

	catRaw, err := os.ReadFile(p.CodexCatalog())
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Models []struct {
			Slug                     string `json:"slug"`
			SupportedReasoningLevels []any  `json:"supported_reasoning_levels"`
		} `json:"models"`
	}
	if err := json.Unmarshal(catRaw, &parsed); err != nil || len(parsed.Models) != 6 {
		t.Fatalf("expected 6 models in codex-catalog.json, got %d (err=%v)", len(parsed.Models), err)
	}
	for _, m := range parsed.Models {
		if len(m.SupportedReasoningLevels) == 0 {
			t.Fatalf("model %s missing supported_reasoning_levels", m.Slug)
		}
	}

	// If real codex CLI is installed on this host, validate the generated files directly
	// inside our isolated temp directory (never touching real $HOME).
	if codexBin, err := exec.LookPath("codex"); err == nil {
		cmd := exec.Command(codexBin, "debug", "models")
		cmd.Env = append(os.Environ(),
			"HOME="+p.Root,
			"CODEX_HOME="+filepath.Join(p.Root, ".codex"),
			CodexEnvName+"=sk-test-codex-123",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("real codex debug models failed: %v\n%s", err, string(out))
		}
		for _, slug := range allowed {
			if !strings.Contains(string(out), `"`+slug+`"`) {
				t.Fatalf("codex debug models output missing %q:\n%s", slug, string(out))
			}
		}
	}

	// User switches model to deepseek-r1; re-running WriteCodex must preserve valid choice.
	updated := strings.Replace(cfg, `model = "gpt-5"`, `model = "deepseek-r1"`, 1)
	if err := os.WriteFile(p.CodexConfig(), []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := p.WriteCodex(CodexOpts{
		Origin:        "https://jieshangai.com",
		Secret:        "sk-test-codex-123",
		Model:         "gpt-5",
		CatalogModels: models,
		AllowedModels: allowed,
	}); err != nil {
		t.Fatalf("second WriteCodex failed: %v", err)
	}
	cfg2, _ := os.ReadFile(p.CodexConfig())
	if !strings.Contains(string(cfg2), `model = "deepseek-r1"`) {
		t.Fatalf("expected user-chosen deepseek-r1 to be preserved, got:\n%s", cfg2)
	}

	// Explicit catalog withdrawal clears managed model_catalog_json & codex-catalog.json.
	if err := p.WriteCodex(CodexOpts{
		Origin:       "https://jieshangai.com",
		Secret:       "sk-test-codex-123",
		ClearCatalog: true,
	}); err != nil {
		t.Fatalf("clear catalog WriteCodex failed: %v", err)
	}
	cfg3, _ := os.ReadFile(p.CodexConfig())
	if strings.Contains(string(cfg3), "model_catalog_json") {
		t.Fatalf("expected model_catalog_json removed on withdrawal, got:\n%s", cfg3)
	}
	if _, err := os.Stat(p.CodexCatalog()); !os.IsNotExist(err) {
		t.Fatalf("expected codex-catalog.json deleted on withdrawal")
	}
}

func TestWriteClaudeManagedSlotsAndCleanClear(t *testing.T) {
	p := Paths{Root: t.TempDir()}

	if err := p.WriteClaude(ClaudeOpts{
		Origin:       testOrigin,
		Secret:       testSecret,
		SonnetModel:  "claude-sonnet-5-5",
		ManageModels: true,
	}); err == nil {
		t.Fatal("managed Claude aliases require a startup model")
	}

	// 1) Write a startup model and three independently configurable aliases.
	if err := p.WriteClaude(ClaudeOpts{
		Origin:       "https://jieshangai.com",
		Secret:       "sk-test-claude-slots",
		Model:        "claude-sonnet-5-5",
		SonnetModel:  "claude-sonnet-5-5",
		OpusModel:    "claude-opus-4-6",
		HaikuModel:   "claude-haiku-4-5",
		ManageModels: true,
	}); err != nil {
		t.Fatalf("WriteClaude native slots failed: %v", err)
	}
	raw, err := os.ReadFile(p.ClaudeSettings())
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	env := doc["env"].(map[string]any)
	if env["ANTHROPIC_MODEL"] != "claude-sonnet-5-5" ||
		env["ANTHROPIC_DEFAULT_SONNET_MODEL"] != "claude-sonnet-5-5" ||
		env["ANTHROPIC_DEFAULT_OPUS_MODEL"] != "claude-opus-4-6" ||
		env["ANTHROPIC_DEFAULT_HAIKU_MODEL"] != "claude-haiku-4-5" {
		t.Fatalf("expected the native startup model and three aliases, got: %+v", env)
	}

	// 2) Switch to CN 4-slot override
	if err := p.WriteClaude(ClaudeOpts{
		Origin:       "https://jieshangai.com",
		Secret:       "sk-test-claude-slots",
		Model:        "deepseek-v3.2",
		SonnetModel:  "deepseek-v3.2",
		OpusModel:    "deepseek-v3.2",
		HaikuModel:   "deepseek-v3.2",
		ManageModels: true,
	}); err != nil {
		t.Fatalf("WriteClaude CN override failed: %v", err)
	}
	raw2, _ := os.ReadFile(p.ClaudeSettings())
	var doc2 map[string]any
	_ = json.Unmarshal(raw2, &doc2)
	env2 := doc2["env"].(map[string]any)
	for _, k := range []string{"ANTHROPIC_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL"} {
		if env2[k] != "deepseek-v3.2" {
			t.Fatalf("expected %s=deepseek-v3.2, got %v", k, env2[k])
		}
	}

	// 3) Clear removes all 4 managed model env keys (zero residue)
	if err := p.Clear(); err != nil {
		t.Fatalf("Clear failed: %v", err)
	}
	raw3, _ := os.ReadFile(p.ClaudeSettings())
	var doc3 map[string]any
	_ = json.Unmarshal(raw3, &doc3)
	if _, hasEnv := doc3["env"]; hasEnv {
		t.Fatalf("expected all managed Claude env keys removed on Clear, got: %+v", doc3)
	}
}

func TestWriteCodexCatalogRespectsListAndHideVisibility(t *testing.T) {
	p := Paths{Root: t.TempDir()}
	models := []CatalogModel{
		{ID: "gpt-5.6-sol", DisplayName: "GPT 5.6", Vendor: "OpenAI", Visibility: "list"},
		{ID: "gpt-5.0", DisplayName: "GPT 5.0", Vendor: "OpenAI", Visibility: "hide"},
	}
	if _, err := p.WriteCodexCatalog(models); err != nil {
		t.Fatalf("WriteCodexCatalog failed: %v", err)
	}
	raw, err := os.ReadFile(p.CodexCatalog())
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Models []struct {
			Slug       string `json:"slug"`
			Visibility string `json:"visibility"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil || len(parsed.Models) != 2 {
		t.Fatalf("unexpected codex-catalog.json: %s", raw)
	}
	if parsed.Models[0].Visibility != "list" || parsed.Models[1].Visibility != "hide" {
		t.Fatalf("expected list/hide visibility preserved, got: %+v", parsed.Models)
	}
}

func TestWriteCodexSkipShellEnvForWindows(t *testing.T) {
	p := Paths{Root: t.TempDir()}
	if err := p.WriteCodex(CodexOpts{
		Origin:        "https://jieshangai.com",
		Secret:        "sk-test-win-codex",
		Model:         "gpt-6.2-sol",
		CatalogModels: []CatalogModel{{ID: "gpt-6.2-sol", DisplayName: "GPT 6.2 Sol", Visibility: "list"}},
		AllowedModels: []string{"gpt-6.2-sol"},
		SkipShellEnv:  true,
	}); err != nil {
		t.Fatalf("WriteCodex with SkipShellEnv failed: %v", err)
	}
	if _, err := os.Stat(p.CodexConfig()); err != nil {
		t.Fatalf("expected config.toml written: %v", err)
	}
	if _, err := os.Stat(p.CodexCatalog()); err != nil {
		t.Fatalf("expected codex-catalog.json written: %v", err)
	}
	if _, err := os.Stat(p.Bashrc()); !os.IsNotExist(err) {
		t.Fatalf("SkipShellEnv must NOT create .bashrc")
	}
	if _, err := os.Stat(p.CodexEnv()); !os.IsNotExist(err) {
		t.Fatalf("SkipShellEnv must NOT create codex.env")
	}
	if err := os.WriteFile(filepath.Join(p.Dir(), "model-prefs.json"), []byte(`{"codex_model":"gpt-6.2-sol"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := p.Clear(); err != nil {
		t.Fatalf("Clear failed: %v", err)
	}
	for _, gone := range []string{p.CodexCatalog(), filepath.Join(p.Dir(), "model-prefs.json")} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Fatalf("expected %s removed on Clear", gone)
		}
	}
	cfgAfter, _ := os.ReadFile(p.CodexConfig())
	if strings.Contains(string(cfgAfter), "model_catalog_json") {
		t.Fatalf("expected model_catalog_json stripped on Clear, got:\n%s", cfgAfter)
	}
}

func TestInstallAndUninstallScriptsSyncCatalog(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash missing")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 missing")
	}
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl missing")
	}

	var withdrawCatalog, codexCatalog, explicitClaudeDefault atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/install/progress":
			_, _ = w.Write([]byte(`{"success":true}`))
		case "/api/install/claim":
			_, _ = w.Write([]byte(`{"success":true,"data":{"key":"sk-CLAIMED1234567890"}}`))
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"claude-fable-5-2"},{"id":"gpt-6.2-sol"}]}`))
		case "/api/install/catalog":
			if r.Header.Get("Authorization") != "Bearer sk-CLAIMED1234567890" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			if withdrawCatalog.Load() {
				_, _ = w.Write([]byte(`{"success":true,"data":{"enabled":false,"models":[]}}`))
				return
			}
			defaultModel := "claude-fable-5-2"
			if codexCatalog.Load() {
				defaultModel = "gpt-6.2-sol"
			} else if explicitClaudeDefault.Load() {
				defaultModel = "claude-opus-5-5"
			}
			_, _ = w.Write([]byte(`{
				"success": true,
				"data": {
					"enabled": true,
					"default_model": "` + defaultModel + `",
					"claude_slots": {
						"primary": "claude-fable-5-2",
						"sonnet": "claude-fable-5-2",
						"opus": "claude-opus-5-5",
						"haiku": "claude-haiku-5-5"
					},
					"models": [
						{"id": "claude-fable-5-2"},
						{"id": "claude-opus-5-5"},
						{"id": "claude-haiku-5-5"},
						{"id": "gpt-6.2-sol", "display_name": "GPT 6.2 Sol", "vendor": "OpenAI", "context_window": 272000, "visibility": "list"},
						{"id": "gpt-5.0", "display_name": "GPT 5.0", "vendor": "OpenAI", "context_window": 200000, "visibility": "hide"}
					]
				}
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	for _, ext := range []string{".sh", ".ps1"} {
		t.Run("claude"+ext, func(t *testing.T) {
			root := t.TempDir()
			run := func(script string, env ...string) {
				t.Helper()
				if ext == ".sh" {
					runScript(t, script+ext, root, env...)
				} else if out, err := runPS1(t, script+ext, root, env...); err != nil {
					t.Fatalf("%s%s: %v\n%s", script, ext, err, out)
				}
			}
			readEnv := func() map[string]string {
				t.Helper()
				raw, err := os.ReadFile((Paths{Root: root}).ClaudeSettings())
				if err != nil {
					t.Fatal(err)
				}
				var doc struct{ Env map[string]string }
				if err := json.Unmarshal(raw, &doc); err != nil {
					t.Fatal(err)
				}
				return doc.Env
			}
			install := func() {
				t.Helper()
				run("install", "RELAY_AGENT=claude", "RELAY_TOKEN=ticket_test_claude_1234",
					"RELAY_CLAIM=1", "RELAY_SITE_ORIGIN="+srv.URL, "RELAY_SKIP_CLI=1")
			}
			install()
			claudeModels := map[string]string{
				"ANTHROPIC_MODEL":                "claude-fable-5-2",
				"ANTHROPIC_DEFAULT_SONNET_MODEL": "claude-fable-5-2",
				"ANTHROPIC_DEFAULT_OPUS_MODEL":   "claude-opus-5-5",
				"ANTHROPIC_DEFAULT_HAIKU_MODEL":  "claude-haiku-5-5",
			}
			checkModels := func() {
				t.Helper()
				env := readEnv()
				for key, want := range claudeModels {
					if env[key] != want {
						t.Errorf("%s = %q, want %q", key, env[key], want)
					}
				}
			}
			checkModels()

			explicitClaudeDefault.Store(true)
			install()
			explicitClaudeDefault.Store(false)
			claudeModels["ANTHROPIC_MODEL"] = "claude-opus-5-5"
			checkModels()

			withdrawCatalog.Store(true)
			install()
			withdrawCatalog.Store(false)
			env := readEnv()
			for key := range claudeModels {
				if _, exists := env[key]; exists {
					t.Errorf("%s survived catalog withdrawal", key)
				}
			}

			install()
			run("uninstall", "RELAY_CONFIRM=1")
			if _, err := os.Stat((Paths{Root: root}).ClaudeSettings()); !os.IsNotExist(err) {
				t.Fatalf("expected fresh settings removed after uninstall, got %v", err)
			}
		})
	}

	// 2) Codex terminal install in claim mode writes default_model + model_catalog_json + codex-catalog.json
	codexCatalog.Store(true)
	codexHome := t.TempDir()
	// Pre-seed a user profile table so uninstall must strip our lines without removing user's table
	if err := os.MkdirAll(filepath.Join(codexHome, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexHome, ".codex", "config.toml"), []byte("[profiles.work]\napproval_policy = \"on-request\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runScript(t, "install.sh", codexHome,
		"RELAY_AGENT=codex",
		"RELAY_TOKEN=ticket_test_codex_1234",
		"RELAY_CLAIM=1",
		"RELAY_SITE_ORIGIN="+srv.URL,
		"RELAY_SKIP_CLI=1",
	)
	cfgBytes, err := os.ReadFile(filepath.Join(codexHome, ".codex", "config.toml"))
	if err != nil {
		t.Fatalf("read codex config.toml: %v", err)
	}
	cfg := string(cfgBytes)
	if !strings.Contains(cfg, `model = "gpt-6.2-sol" # jieshangai-model`) {
		t.Fatalf("expected curated default_model gpt-6.2-sol in config.toml, got:\n%s", cfg)
	}
	catIdx := strings.Index(cfg, "model_catalog_json = ")
	profIdx := strings.Index(cfg, "[profiles.work]")
	if catIdx < 0 || profIdx < 0 || catIdx > profIdx {
		t.Fatalf("expected model_catalog_json at TOML root before [profiles.work], got:\n%s", cfg)
	}
	catRaw, err := os.ReadFile(filepath.Join(codexHome, ".jieshangai", "codex-catalog.json"))
	if err != nil {
		t.Fatalf("expected codex-catalog.json written by install.sh: %v", err)
	}
	if !strings.Contains(string(catRaw), `"visibility": "hide"`) || !strings.Contains(string(catRaw), `"slug": "gpt-6.2-sol"`) {
		t.Fatalf("unexpected codex-catalog.json content:\n%s", catRaw)
	}

	// #8: Re-installing when server explicitly withdraws catalog (enabled=false) must clear
	// stale model_catalog_json and delete codex-catalog.json.
	withdrawCatalog.Store(true)
	runScript(t, "install.sh", codexHome,
		"RELAY_AGENT=codex",
		"RELAY_TOKEN=ticket_test_codex_withdraw",
		"RELAY_CLAIM=1",
		"RELAY_SITE_ORIGIN="+srv.URL,
		"RELAY_SKIP_CLI=1",
	)
	cfgWithdrawn, err := os.ReadFile(filepath.Join(codexHome, ".codex", "config.toml"))
	if err != nil {
		t.Fatalf("read codex config.toml after withdrawal: %v", err)
	}
	if strings.Contains(string(cfgWithdrawn), "model_catalog_json") {
		t.Fatalf("expected stale model_catalog_json cleared when catalog withdrawn, got:\n%s", cfgWithdrawn)
	}
	if _, err := os.Stat(filepath.Join(codexHome, ".jieshangai", "codex-catalog.json")); !os.IsNotExist(err) {
		t.Fatalf("expected stale codex-catalog.json deleted when catalog withdrawn")
	}
	withdrawCatalog.Store(false)

	// Re-install with active catalog before testing uninstall cleanup
	runScript(t, "install.sh", codexHome,
		"RELAY_AGENT=codex",
		"RELAY_TOKEN=ticket_test_codex_reinstall",
		"RELAY_CLAIM=1",
		"RELAY_SITE_ORIGIN="+srv.URL,
		"RELAY_SKIP_CLI=1",
	)

	// Simulate no-backup uninstall (remove .bak) to verify strip_marked_file removes model_catalog_json & codex-catalog.json
	_ = os.Remove(filepath.Join(codexHome, ".codex", "config.toml.jieshangai.bak"))
	_ = os.WriteFile(filepath.Join(codexHome, ".jieshangai", "model-prefs.json"), []byte(`{"codex_model":"gpt-6.2-sol"}`), 0o600)
	runScript(t, "uninstall.sh", codexHome, "RELAY_CONFIRM=1")
	cfgAfter, err := os.ReadFile(filepath.Join(codexHome, ".codex", "config.toml"))
	if err != nil {
		t.Fatalf("read codex config.toml after uninstall: %v", err)
	}
	if strings.Contains(string(cfgAfter), "model_catalog_json") || strings.Contains(string(cfgAfter), "jieshangai") {
		t.Fatalf("expected all jieshangai/model_catalog_json lines stripped on uninstall.sh, got:\n%s", cfgAfter)
	}
	if !strings.Contains(string(cfgAfter), "[profiles.work]") {
		t.Fatalf("expected user [profiles.work] preserved after uninstall.sh, got:\n%s", cfgAfter)
	}
	for _, gone := range []string{
		filepath.Join(codexHome, ".jieshangai", "codex-catalog.json"),
		filepath.Join(codexHome, ".jieshangai", "model-prefs.json"),
	} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Fatalf("expected %s deleted by uninstall.sh", gone)
		}
	}
}
