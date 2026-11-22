package clientruntime

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDesktopSwitchTierUpdatesStateAndNotice(t *testing.T) {
	currentTier := "balanced"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/install/balance":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data": map[string]any{
					"balance_cny": "25.00",
					"balance_low": false,
					"tier":        currentTier,
					"tiers": []map[string]any{
						{"name": "economy", "label": "省钱"},
						{"name": "balanced", "label": "均衡", "recommended": true},
						{"name": "stable", "label": "稳定"},
					},
				},
			})
		case r.Method == http.MethodPut && r.URL.Path == "/api/install/tier":
			if got := r.Header.Get("Authorization"); got != "Bearer sk-claude-test" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			var body struct {
				Group string `json:"group"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			currentTier = body.Group
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data": map[string]any{
					"tier": currentTier,
					"tiers": []map[string]any{
						{"name": "economy", "label": "省钱"},
						{"name": "balanced", "label": "均衡", "recommended": true},
						{"name": "stable", "label": "稳定"},
					},
				},
			})
		case r.URL.Path == "/api/status":
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	app, _ := testDesktopApp(t, srv)
	app.env.FindCLI = func(agent string) bool { return agent == "claude" }
	app.env.Configured = func(agent, origin string) (string, bool) {
		if agent == "claude" {
			return "sk-claude-test", true
		}
		return "", false
	}
	app.desktopRefresh()

	st := app.DesktopState()
	if st.Statuses[0].Tier != "balanced" || len(st.Statuses[0].Tiers) != 3 {
		t.Fatalf("expected balanced with 3 tiers, got tier=%q len=%d", st.Statuses[0].Tier, len(st.Statuses[0].Tiers))
	}

	if err := app.DesktopAction(DesktopRequest{Action: "switch-tier", Agent: "claude", Tier: "economy"}); err != nil {
		t.Fatalf("switch-tier failed: %v", err)
	}
	after := app.DesktopState()
	if after.Statuses[0].Tier != "economy" {
		t.Fatalf("expected economy after switch, got %q", after.Statuses[0].Tier)
	}
	if after.View.Notice == "" {
		t.Fatalf("expected success notice after switch-tier")
	}
}
