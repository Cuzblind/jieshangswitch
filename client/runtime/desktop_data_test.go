package clientruntime

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func waitDesktopRead(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("desktop read stalled")
	}
}

func TestDesktopAdmittedReadsRejectDelayedStarts(t *testing.T) {
	for _, action := range []string{"usage-load", "catalog-load"} {
		for _, change := range []string{"newer-read", "reconnect"} {
			t.Run(action+"/"+change, func(t *testing.T) {
				var calls atomic.Int32
				gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					n := calls.Add(1)
					if action == "usage-load" {
						_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{
							"range": r.URL.Query().Get("range"), "cost_cny": "1.00", "requests": n,
						}})
					} else {
						_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{
							"enabled": true, "version": n, "models": []map[string]any{{"id": "test-model"}},
						}})
					}
				}))
				defer gw.Close()
				a, _ := testDesktopApp(t, gw)
				if err := a.env.Write("claude", gw.URL, fakeKey); err != nil {
					t.Fatal(err)
				}
				a.walletAgent = "claude"
				// Admit the old request, but do not start its worker until after
				// the newer request finishes or the local connection changes.
				old := a.reserveDesktopRequest(DesktopRequest{Action: action, Agent: "claude", Range: "24h"})
				if change == "newer-read" {
					newer := a.reserveDesktopRequest(DesktopRequest{Action: action, Agent: "claude", Range: "7d"})
					if err := a.DesktopAction(newer); err != nil {
						t.Fatal(err)
					}
				} else if err := a.writeConnection("claude", "sk-replacement"); err != nil {
					t.Fatal(err)
				}
				if err := a.DesktopAction(old); err != nil {
					t.Fatal(err)
				}
				state := a.DesktopState()
				if change == "reconnect" {
					if calls.Load() != 0 || state.Usage != nil || len(state.Catalogs) != 0 {
						t.Fatal("a delayed old read used the replacement connection")
					}
				} else if action == "usage-load" {
					if state.Usage == nil || state.Usage.Range != "7d" || state.Usage.Requests != 1 {
						t.Fatalf("delayed old range replaced the admitted newer range: %+v", state.Usage)
					}
				} else if state.Catalogs["claude"] == nil || state.Catalogs["claude"].Version != 1 {
					t.Fatalf("delayed old read replaced the admitted newer catalog: %+v", state.Catalogs)
				}
			})
		}
	}
}

// Exercise the queue-bypassing public actions with real HTTP requests. The first
// response deliberately finishes last, after a new read, write, or restore.
func TestDesktopRemoteReadsRejectLateResponses(t *testing.T) {
	for _, action := range []string{"usage-load", "catalog-load"} {
		for _, change := range []string{"newer-read", "reconnect", "tier-switch", "restore", "restore-error-response"} {
			t.Run(action+"/"+change, func(t *testing.T) {
				started, release := make(chan struct{}), make(chan struct{})
				var releaseOnce sync.Once
				unblock := func() { releaseOnce.Do(func() { close(release) }) }
				var calls atomic.Int32
				gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/api/install/tier" {
						_, _ = w.Write([]byte(`{"success":true,"data":{"tier":"stable"}}`))
						return
					}
					n := calls.Add(1)
					if n == 1 {
						close(started)
						<-release
						if change == "restore-error-response" {
							w.WriteHeader(http.StatusUnauthorized)
							return
						}
					}
					if action == "usage-load" {
						_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{
							"range": r.URL.Query().Get("range"), "cost_cny": "1.00", "requests": n,
						}})
					} else {
						_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{
							"enabled": true, "version": n, "models": []map[string]any{{"id": "test-model"}},
						}})
					}
				}))
				defer gw.Close()
				defer unblock()
				a, _ := testDesktopApp(t, gw)
				if err := a.env.Write("claude", gw.URL, fakeKey); err != nil {
					t.Fatal(err)
				}
				a.walletAgent = "claude"
				finished := make(chan struct{})
				var lateErr error
				go func() {
					lateErr = a.DesktopAction(DesktopRequest{Action: action, Agent: "claude", Range: "24h"})
					close(finished)
				}()
				waitDesktopRead(t, started)
				switch change {
				case "newer-read":
					if err := a.DesktopAction(DesktopRequest{Action: action, Agent: "claude", Range: "7d"}); err != nil {
						t.Fatal(err)
					}
				case "reconnect":
					if err := a.writeConnection("claude", "sk-replacement"); err != nil {
						t.Fatal(err)
					}
				case "tier-switch":
					if _, err := a.switchTier("claude", "stable"); err != nil {
						t.Fatal(err)
					}
				default:
					if _, err := a.restoreConnections(); err != nil {
						t.Fatal(err)
					}
				}
				unblock()
				waitDesktopRead(t, finished)
				if lateErr != nil {
					t.Fatalf("stale failure escaped into the new connection: %v", lateErr)
				}
				state := a.DesktopState()
				if change == "newer-read" {
					if action == "usage-load" && (state.Usage == nil || state.Usage.Range != "7d" || state.Usage.Requests != 2) {
						t.Fatalf("old usage replaced the requested range: %+v", state.Usage)
					}
					if action == "catalog-load" && (state.Catalogs["claude"] == nil || state.Catalogs["claude"].Version != 2) {
						t.Fatalf("old catalog replaced the new one: %+v", state.Catalogs)
					}
				} else if state.Usage != nil || len(state.Catalogs) != 0 {
					t.Fatalf("old connection data returned after %s", change)
				}
			})
		}
	}
}

func TestDesktopNativeCatalogCannotUndoRestore(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		_, _ = w.Write([]byte(`{"success":true,"data":{"enabled":true,"version":1,"models":[{"id":"test-model"}]}}`))
	}))
	defer gw.Close()
	defer unblock()
	a, _ := testDesktopApp(t, gw)
	if err := a.env.Write("codex", gw.URL, fakeKey); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { a.syncDesktopCatalog("codex", fakeKey); close(done) }()
	waitDesktopRead(t, started)
	if _, err := a.restoreConnections(); err != nil {
		t.Fatal(err)
	}
	unblock()
	waitDesktopRead(t, done)
	if _, ok := a.env.Configured("codex", gw.URL); ok {
		t.Fatal("late native catalog sync reconnected Codex after restore")
	}
}

func TestDesktopNativeCatalogUsesOvertakingPanelResult(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	var calls atomic.Int32
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		model := "new-model"
		if calls.Add(1) == 1 {
			close(started)
			<-release
			model = "old-model"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{
			"enabled": true, "models": []map[string]any{{"id": model}},
		}})
	}))
	defer gw.Close()
	defer unblock()
	a, _ := testDesktopApp(t, gw)
	if err := a.env.Write("codex", gw.URL, fakeKey); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { a.syncDesktopCatalog("codex", fakeKey); close(done) }()
	waitDesktopRead(t, started)
	if err := a.DesktopAction(DesktopRequest{Action: "catalog-load", Agent: "codex"}); err != nil {
		t.Fatal(err)
	}
	unblock()
	waitDesktopRead(t, done)
	raw, err := os.ReadFile(paths().CodexCatalog())
	if err != nil || !strings.Contains(string(raw), "new-model") || strings.Contains(string(raw), "old-model") {
		t.Fatalf("native menu lost or overwritten by overtaken sync: %s (%v)", raw, err)
	}
}
