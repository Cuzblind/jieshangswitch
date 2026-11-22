package core

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
