package clientruntime

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckRejectsBusinessFailureAndEmptyCatalog(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{name: "valid models list", body: `{"object":"list","data":[{"id":"claude-sonnet-4-5"}]}`, wantErr: false},
		{name: "empty body rejected", body: ``, wantErr: true},
		{name: "empty object rejected", body: `{}`, wantErr: true},
		{name: "null data rejected", body: `{"data":null}`, wantErr: true},
		{name: "empty data array", body: `{"object":"list","data":[]}`, wantErr: true},
		{name: "entry without id rejected", body: `{"object":"list","data":[{}]}`, wantErr: true},
		{name: "business failure", body: `{"success":false,"message":"denied"}`, wantErr: true},
		{name: "error field", body: `{"error":{"message":"invalid key"}}`, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			app, _ := testDesktopApp(t, srv)
			err := app.check("sk-test")
			if tc.wantErr && err == nil {
				t.Fatalf("expected error for body %s, got nil", tc.body)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error for body %s: %v", tc.body, err)
			}
		})
	}
}

func TestFetchDesktopCatalogParsesSixBrandsAndRejectsEmptyWhenEnabled(t *testing.T) {
	mode := "ok"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/install/catalog" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if mode == "empty" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data":    map[string]any{"enabled": true, "version": 1, "models": []any{}},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"data": map[string]any{
				"enabled":       true,
				"version":       2,
				"agent":         "claude_code",
				"tier":          "balanced",
				"default_model": "claude-sonnet-4-5",
				"models": []map[string]any{
					{"id": "claude-sonnet-4-5", "display_name": "Claude Sonnet 4.5", "vendor": "Anthropic"},
					{"id": "gemini-2.5-pro", "display_name": "Gemini 2.5 Pro", "vendor": "Google"},
				},
			},
		})
	}))
	defer srv.Close()

	app, _ := testDesktopApp(t, srv)
	cat, err := app.fetchDesktopCatalog("sk-test")
	if err != nil {
		t.Fatalf("fetchDesktopCatalog failed: %v", err)
	}
	if !cat.Enabled || len(cat.Models) != 2 || cat.DefaultModel != "claude-sonnet-4-5" {
		t.Fatalf("unexpected catalog: %+v", cat)
	}

	mode = "empty"
	if _, err := app.fetchDesktopCatalog("sk-test"); err == nil {
		t.Fatalf("expected empty enabled catalog to be rejected")
	}
}

func TestClaudeDesktopInferenceModelsSchemaAndWithdrawal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("RELAY_HOME", home)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("APPDATA", filepath.Join(home, "appdata"))

	cat := &DesktopCatalogPayload{
		Enabled:      true,
		Version:      3,
		DefaultModel: "gemini-2.5-pro",
		Models: []DesktopCatalogModel{
			{ID: "claude-sonnet-4-5", DisplayName: "Claude Sonnet 4.5"},
			{ID: "gemini-2.5-pro", DisplayName: "Gemini 2.5 Pro"},
		},
	}
	if err := configureDesktopGUIWithCatalog("claude", ProductionOrigin, "sk-test-gui", cat); err != nil {
		t.Fatalf("configureDesktopGUIWithCatalog failed: %v", err)
	}
	profilePath := desktopGUIFiles()[2]
	raw, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	var profile map[string]any
	if err := json.Unmarshal(raw, &profile); err != nil {
		t.Fatal(err)
	}
	if _, hasLegacy := profile["defaultModel"]; hasLegacy {
		t.Fatalf("must not write non-standard defaultModel key: %s", raw)
	}
	list, ok := profile["inferenceModels"].([]any)
	if !ok || len(list) != 2 {
		t.Fatalf("expected 2 inferenceModels, got: %s", raw)
	}
	first := list[0].(map[string]any)
	if first["name"] != "gemini-2.5-pro" || first["labelOverride"] != "Gemini 2.5 Pro" {
		t.Fatalf("expected default model first with official {name, labelOverride}, got: %+v", first)
	}
	if _, hasOldID := first["id"]; hasOldID {
		t.Fatalf("must not write non-standard id field in inferenceModels: %+v", first)
	}

	// Explicit withdrawal removes managed inferenceModels cleanly.
	withdrawn := &DesktopCatalogPayload{Enabled: false, Version: 4}
	if err := configureDesktopGUIWithCatalog("claude", ProductionOrigin, "sk-test-gui", withdrawn); err != nil {
		t.Fatalf("withdrawal configureDesktopGUIWithCatalog failed: %v", err)
	}
	raw2, _ := os.ReadFile(profilePath)
	var profile2 map[string]any
	_ = json.Unmarshal(raw2, &profile2)
	if _, stillHas := profile2["inferenceModels"]; stillHas {
		t.Fatalf("expected inferenceModels cleared on explicit withdrawal, got: %s", raw2)
	}
}

func TestEnsureClaudeOnboardingPreservesExistingFields(t *testing.T) {
	home := t.TempDir()
	t.Setenv("RELAY_HOME", home)
	t.Setenv("HOME", home)

	// Pre-populate ~/.claude.json with mcpServers & custom user setting
	existing := `{"mcpServers":{"fs":{"command":"npx"}},"customFlag":"keep"}`
	if err := os.WriteFile(claudeJSONPath(), []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensureClaudeOnboarding(); err != nil {
		t.Fatalf("ensureClaudeOnboarding failed: %v", err)
	}
	raw, err := os.ReadFile(claudeJSONPath())
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["hasCompletedOnboarding"] != true {
		t.Fatalf("expected hasCompletedOnboarding=true, got %+v", doc)
	}
	if doc["customFlag"] != "keep" {
		t.Fatalf("expected customFlag preserved, got %+v", doc)
	}
	if _, ok := doc["mcpServers"].(map[string]any); !ok {
		t.Fatalf("expected mcpServers preserved, got %+v", doc)
	}
}

func TestDesktopTestChatClaudeAndCodexAndErrors(t *testing.T) {
	mode := "ok"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/install/catalog":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data": map[string]any{
					"enabled":       true,
					"version":       1,
					"default_model": "claude-sonnet-4-5",
					"models":        []map[string]any{{"id": "claude-sonnet-4-5", "display_name": "Claude Sonnet 4.5"}},
				},
			})
		case r.URL.Path == "/v1/messages":
			if mode == "quota" {
				w.WriteHeader(http.StatusPaymentRequired)
				_, _ = w.Write([]byte(`{"error":{"message":"insufficient balance"}}`))
				return
			}
			if mode == "busy" {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"error":{"message":"upstream busy"}}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":      "msg_1",
				"model":   "claude-sonnet-4-5",
				"content": []map[string]any{{"type": "text", "text": "OK"}},
			})
		case r.URL.Path == "/v1/responses":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":          "resp_1",
				"model":       "gpt-5",
				"output_text": "OK",
			})
		case r.URL.Path == "/api/install/balance":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data":    map[string]any{"balance_cny": "10.00", "balance_low": false},
			})
		case r.URL.Path == "/api/status":
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	app, _ := testDesktopApp(t, srv)
	app.env.FindCLI = func(agent string) bool { return true }
	app.env.Configured = func(agent, origin string) (string, bool) { return "sk-test-chat", true }

	if err := app.DesktopAction(DesktopRequest{Action: "test-chat", Agent: "claude"}); err != nil {
		t.Fatalf("claude test-chat failed: %v", err)
	}
	if notice := app.DesktopState().View.Notice; notice == "" {
		t.Fatalf("expected success notice after claude test-chat")
	}

	if err := app.DesktopAction(DesktopRequest{Action: "test-chat", Agent: "codex"}); err != nil {
		t.Fatalf("codex test-chat failed: %v", err)
	}

	mode = "quota"
	if err := app.DesktopAction(DesktopRequest{Action: "test-chat", Agent: "claude"}); err == nil {
		t.Fatalf("expected balance error on 402")
	}
	mode = "busy"
	if err := app.DesktopAction(DesktopRequest{Action: "test-chat", Agent: "claude"}); err == nil {
		t.Fatalf("expected busy error on 503")
	}
}
