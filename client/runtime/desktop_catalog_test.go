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
				"data":    map[string]any{"enabled": true, "version": 2, "models": []any{}},
			})
			return
		}
		if mode == "null" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data":    map[string]any{"enabled": true, "version": 2, "models": nil},
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

	// Valid empty array ("models": []) represents last model revoked and must be accepted.
	mode = "empty"
	emptyCat, err := app.fetchDesktopCatalog("sk-test")
	if err != nil || emptyCat == nil || !emptyCat.Enabled || len(emptyCat.Models) != 0 {
		t.Fatalf("expected valid empty enabled catalog, got cat=%+v err=%v", emptyCat, err)
	}

	// Missing/null models when enabled must still be rejected as malformed.
	mode = "null"
	if _, err := app.fetchDesktopCatalog("sk-test"); err == nil {
		t.Fatalf("expected null enabled catalog to be rejected")
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

	// Re-populate then revoke last model (enabled: true, models: []): must also clear stale menu.
	if err := configureDesktopGUIWithCatalog("claude", ProductionOrigin, "sk-test-gui", cat); err != nil {
		t.Fatalf("re-populate configureDesktopGUIWithCatalog failed: %v", err)
	}
	revokedAll := &DesktopCatalogPayload{Enabled: true, Version: 5, Models: []DesktopCatalogModel{}}
	if err := configureDesktopGUIWithCatalog("claude", ProductionOrigin, "sk-test-gui", revokedAll); err != nil {
		t.Fatalf("revokedAll configureDesktopGUIWithCatalog failed: %v", err)
	}
	raw3, _ := os.ReadFile(profilePath)
	var profile3 map[string]any
	_ = json.Unmarshal(raw3, &profile3)
	if _, stillHas := profile3["inferenceModels"]; stillHas {
		t.Fatalf("expected inferenceModels cleared when enabled with 0 models, got: %s", raw3)
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
	catalogEnabled := true
	var lastCodexModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/install/catalog":
			if !catalogEnabled {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"success": true,
					"data":    map[string]any{"enabled": false, "version": 1, "models": []any{}},
				})
				return
			}
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
			if mode == "responses-404" {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":{"message":"not found"}}`))
				return
			}
			var body struct {
				Model string `json:"model"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			lastCodexModel = body.Model
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":          "resp_1",
				"model":       body.Model,
				"output_text": "OK",
			})
		case r.URL.Path == "/v1/chat/completions":
			// Must NEVER be called as a fallback for Codex Responses API.
			t.Errorf("Codex test-chat must not fall back to /v1/chat/completions")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":      "chat_1",
				"model":   "gpt-5",
				"choices": []map[string]any{{"message": map[string]string{"content": "OK"}}},
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

	// When catalog is disabled, Codex must probe core.DefaultCodexModel ("gpt-5.6-sol") on /v1/responses.
	catalogEnabled = false
	if err := app.DesktopAction(DesktopRequest{Action: "test-chat", Agent: "codex"}); err != nil {
		t.Fatalf("codex test-chat failed: %v", err)
	}
	if lastCodexModel != "gpt-5.6-sol" {
		t.Fatalf("expected codex default model gpt-5.6-sol when catalog disabled, got %q", lastCodexModel)
	}

	// When /v1/responses returns 404, Codex test-chat must fail (never fall back to /v1/chat/completions).
	mode = "responses-404"
	if err := app.DesktopAction(DesktopRequest{Action: "test-chat", Agent: "codex"}); err == nil {
		t.Fatalf("expected error when /v1/responses returns 404")
	}

	catalogEnabled = true
	mode = "quota"
	if err := app.DesktopAction(DesktopRequest{Action: "test-chat", Agent: "claude"}); err == nil {
		t.Fatalf("expected balance error on 402")
	}
	mode = "busy"
	if err := app.DesktopAction(DesktopRequest{Action: "test-chat", Agent: "claude"}); err == nil {
		t.Fatalf("expected busy error on 503")
	}
}

func TestDesktopDualWalletKeepsWorkspaceBalanceAndUsageAligned(t *testing.T) {
	var usageAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/api/status":
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{}})
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": []map[string]any{{"id": "claude-sonnet-4-5"}}})
		case "/api/install/balance":
			if auth == "Bearer sk-personal" {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"success": true,
					"data": map[string]any{
						"balance_cny":    "12.00",
						"balance_low":    false,
						"account_label":  "me***@personal.test",
						"workspace":      map[string]any{"kind": "personal", "label": "个人工作区"},
						"today_cost_cny": "1.00",
						"today_requests": 2,
						"today_tokens":   2000,
					},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data": map[string]any{
					"balance_cny":    "99.00",
					"balance_low":    false,
					"account_label":  "tm***@team.test",
					"workspace":      map[string]any{"kind": "team", "label": "团队 · 研发", "team_id": 9},
					"today_cost_cny": "9.00",
					"today_requests": 9,
					"today_tokens":   9000,
				},
			})
		case "/api/install/usage":
			usageAuth = auth
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data": map[string]any{
					"range":    "24h",
					"cost_cny": "1.00",
					"requests": 2,
					"tokens":   2000,
					"heatmap":  []any{},
					"logs":     []any{},
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	app, _ := testDesktopApp(t, srv)
	app.env.FindCLI = func(agent string) bool { return true }
	app.env.Configured = func(agent, origin string) (string, bool) {
		if agent == "claude" {
			return "sk-personal", true
		}
		return "sk-team", true
	}

	if err := app.DesktopAction(DesktopRequest{Action: "refresh"}); err != nil {
		t.Fatalf("refresh failed: %v", err)
	}
	if err := app.DesktopAction(DesktopRequest{Action: "usage-load", Range: "24h"}); err != nil {
		t.Fatalf("usage-load failed: %v", err)
	}
	st := app.DesktopState()
	if st.Balance == nil || st.Balance.Text != "余额 ¥12.00" {
		t.Fatalf("expected personal balance ¥12.00, got %+v", st.Balance)
	}
	if st.Workspace == nil || st.Workspace.Kind != "personal" || st.Workspace.Label != "个人工作区" {
		t.Fatalf("expected personal workspace (not overwritten by codex team wallet), got %+v", st.Workspace)
	}
	if st.AccountLabel != "me***@personal.test" {
		t.Fatalf("expected personal accountLabel, got %q", st.AccountLabel)
	}
	if st.Today == nil || st.Today.CostCNY != "1.00" || st.Today.Requests != 2 {
		t.Fatalf("expected personal today summary, got %+v", st.Today)
	}
	if usageAuth != "Bearer sk-personal" {
		t.Fatalf("expected usage-load to use same wallet credential sk-personal, got %q", usageAuth)
	}
}

func TestDesktopBalanceMetaUsageAndCatalogLoad(t *testing.T) {
	var openedURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status":
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{}})
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"object": "list",
				"data":   []map[string]any{{"id": "claude-sonnet-4-5"}},
			})
		case "/api/install/balance":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data": map[string]any{
					"balance_cny":    "42.50",
					"balance_low":    false,
					"tier":           "balanced",
					"account_label":  "zh***@example.com",
					"workspace":      map[string]any{"kind": "team", "label": "团队 · 研究组", "team_id": 7},
					"today_cost_cny": "3.20",
					"today_requests": 5,
					"today_tokens":   12400,
				},
			})
		case "/api/install/usage":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data": map[string]any{
					"range":             r.URL.Query().Get("range"),
					"cost_cny":          "12.80",
					"requests":          18,
					"tokens":            64000,
					"prompt_tokens":     48000,
					"completion_tokens": 16000,
					"heatmap": []map[string]any{
						{"date": "2026-11-22", "requests": 5, "tokens": 12400, "cost_cny": "3.20"},
					},
					"logs": []map[string]any{
						{
							"id":                101,
							"created_at":        1795300000,
							"model":             "claude-sonnet-4-5",
							"prompt_tokens":     800,
							"completion_tokens": 400,
							"cost_cny":          "0.42",
							"use_time_ms":       1250,
						},
					},
				},
			})
		case "/api/install/catalog":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data": map[string]any{
					"enabled":       true,
					"version":       5,
					"agent":         "claude_code",
					"tier":          "balanced",
					"default_model": "claude-sonnet-4-5",
					"models": []map[string]any{
						{"id": "claude-sonnet-4-5", "display_name": "Claude Sonnet 4.5", "vendor": "Anthropic", "context_window": 200000},
						{"id": "deepseek-r1", "display_name": "DeepSeek R1", "vendor": "DeepSeek", "context_window": 128000},
					},
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	app, _ := testDesktopApp(t, srv)
	app.env.FindCLI = func(agent string) bool { return true }
	app.env.Configured = func(agent, origin string) (string, bool) { return "sk-test-usage", true }
	app.env.Open = func(target string) error {
		openedURL = target
		return nil
	}

	if err := app.DesktopAction(DesktopRequest{Action: "refresh"}); err != nil {
		t.Fatalf("refresh failed: %v", err)
	}
	st := app.DesktopState()
	if st.AccountLabel != "zh***@example.com" {
		t.Fatalf("unexpected accountLabel: %q", st.AccountLabel)
	}
	if st.Workspace == nil || st.Workspace.Kind != "team" || st.Workspace.Label != "团队 · 研究组" || st.Workspace.TeamID != 7 {
		t.Fatalf("unexpected workspace: %+v", st.Workspace)
	}
	if st.Today == nil || st.Today.CostCNY != "3.20" || st.Today.Requests != 5 || st.Today.Tokens != 12400 {
		t.Fatalf("unexpected today summary: %+v", st.Today)
	}

	if err := app.DesktopAction(DesktopRequest{Action: "usage-load", Range: "7d"}); err != nil {
		t.Fatalf("usage-load failed: %v", err)
	}
	st = app.DesktopState()
	if st.Usage == nil || st.Usage.Range != "7d" || st.Usage.Requests != 18 || len(st.Usage.Logs) != 1 || len(st.Usage.Heatmap) != 1 {
		t.Fatalf("unexpected usage snapshot: %+v", st.Usage)
	}

	if err := app.DesktopAction(DesktopRequest{Action: "catalog-load", Agent: "claude"}); err != nil {
		t.Fatalf("catalog-load failed: %v", err)
	}
	st = app.DesktopState()
	cat := st.Catalogs["claude"]
	if cat == nil || !cat.Enabled || len(cat.Models) != 2 || cat.DefaultModel != "claude-sonnet-4-5" {
		t.Fatalf("unexpected catalog snapshot: %+v", cat)
	}

	if err := app.DesktopAction(DesktopRequest{Action: "switch-account"}); err != nil {
		t.Fatalf("switch-account failed: %v", err)
	}
	if openedURL == "" {
		t.Fatalf("expected switch-account to open web URL")
	}
}
