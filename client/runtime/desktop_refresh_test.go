package clientruntime

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The startup refresh used to switch to the full-screen working view, which
// hid the cards and swallowed clicks for the whole inspect.
func TestStartupRefreshKeepsHomeScreen(t *testing.T) {
	g := &fakeGW{models: 200}
	gw := httptest.NewServer(g.handler())
	defer gw.Close()
	a, _ := testDesktopApp(t, gw)
	var mu sync.Mutex
	var screens []string
	a.env.FindCLI = func(string) bool {
		mu.Lock()
		screens = append(screens, a.getView().Screen)
		mu.Unlock()
		return true
	}
	if err := a.DesktopAction(DesktopRequest{Action: "refresh"}); err != nil {
		t.Fatal(err)
	}
	if len(screens) == 0 {
		t.Fatal("refresh did not look at the assistants")
	}
	for _, s := range screens {
		if s == "working" {
			t.Fatalf("refresh covered the home screen: %v", screens)
		}
	}
}

func badges(a *App) map[string]string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := map[string]string{}
	for _, s := range a.statuses {
		out[s.Agent] = s.State + "/" + s.Badge
	}
	return out
}

// Every refresh path must word the same state the same way, or the badge
// changes width and text while nothing changed.
func TestBadgeTextIsTheSameOnEveryPath(t *testing.T) {
	g := &fakeGW{models: 200}
	gw := httptest.NewServer(g.handler())
	defer gw.Close()
	a, _ := testDesktopApp(t, gw)
	installed := false
	a.env.FindCLI = func(string) bool { return installed }

	a.refreshStatus(false)
	want := badges(a)
	a.desktopLocalRefresh()
	if got := badges(a); got["claude"] != want["claude"] || got["codex"] != want["codex"] {
		t.Fatalf("missing CLI worded differently: %v vs %v", got, want)
	}

	installed = true
	a.desktopLocalRefresh()
	local := badges(a)
	a.refreshStatus(false)
	if got := badges(a); got["claude"] != local["claude"] || got["codex"] != local["codex"] {
		t.Fatalf("unconnected assistant worded differently: %v vs %v", local, got)
	}
}

// Inspecting Claude and then Codex doubled the wait before the cards settled.
func TestInspectProbesBothAssistantsTogether(t *testing.T) {
	m := newToolManager()
	m.root = t.TempDir()
	var arrived sync.WaitGroup
	arrived.Add(2)
	both := make(chan struct{})
	go func() { arrived.Wait(); close(both) }()
	var together atomic.Int32
	m.probeCLI = func(string) (toolEntry, cliProbe) {
		arrived.Done()
		select {
		case <-both:
			together.Add(1)
		case <-time.After(2 * time.Second):
		}
		return toolEntry{}, cliMissing
	}
	m.inspect()
	if together.Load() != 2 {
		t.Fatal("assistants were inspected one after another")
	}
}

// A check belongs to the credential it tested; replacing the local connection
// must not let its old failure mask a healthy wallet on a later desktop action.
func TestDesktopRefreshUsesCurrentConnectionAndLogin(t *testing.T) {
	checkStarted, releaseCheck := make(chan struct{}), make(chan struct{})
	g := &fakeGW{models: 200}
	base := g.handler()
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" && r.Header.Get("Authorization") != "Bearer "+fakeKey {
			close(checkStarted)
			<-releaseCheck
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		base.ServeHTTP(w, r)
	}))
	defer gw.Close()
	a, _ := testDesktopApp(t, gw)
	// This test controls when the local credential changes. Running the Windows
	// installer here would hold the old HTTP response open during PowerShell setup.
	secret := "expired-test-key"
	a.env.Configured = func(agent, origin string) (string, bool) {
		if agent == "claude" && origin == gw.URL {
			return secret, true
		}
		return "", false
	}
	checked := make(chan error, 1)
	oldSecret := a.secretFor("claude")
	go func() { checked <- a.checkConnection("claude", oldSecret) }()
	<-checkStarted
	// Simulate replacing settings while the old HTTP check is still in flight.
	secret = fakeKey
	close(releaseCheck)
	if err := <-checked; !errors.Is(err, errExpired) {
		t.Fatalf("old connection check = %v", err)
	}
	a.desktopRefresh()
	if st := a.DesktopState().Statuses[0]; st.State != "ok" || st.Action != "restore" || st.Button == "" {
		t.Fatalf("old check replaced current connection: %+v", st)
	}

	loggedIn := true
	a.env.OfficialLogin = func(string) bool { return loggedIn }
	if err := a.checkConnection("claude", a.secretFor("claude")); err != nil {
		t.Fatal(err)
	}
	a.desktopRefresh()
	if st := a.DesktopState().Statuses[0]; st.State != "conflict" || st.Action != "fix" {
		t.Fatalf("official login conflict lost: %+v", st)
	}
	loggedIn = false // the user exits the official account outside this app
	a.desktopRefresh()
	if st := a.DesktopState().Statuses[0]; st.State != "ok" || st.Action != "restore" || st.Button == "" {
		t.Fatalf("stale login or partial card replaced live state: %+v", st)
	}
}

// CLI installation can finish between probes. A card must use one sampled
// installation fact for both its fields and its badge.
func TestRefreshSamplesCLIOncePerAssistant(t *testing.T) {
	gw := httptest.NewServer((&fakeGW{models: 200}).handler())
	defer gw.Close()
	a, _ := testDesktopApp(t, gw)
	calls := map[string]int{}
	a.env.FindCLI = func(agent string) bool {
		calls[agent]++
		return calls[agent] > 1
	}
	a.desktopRefresh()
	for _, st := range a.DesktopState().Statuses {
		if calls[st.Agent] != 1 || st.Installed || st.State != "nocli" {
			t.Fatalf("CLI facts sampled inconsistently (%d calls): %+v", calls[st.Agent], st)
		}
	}
}
