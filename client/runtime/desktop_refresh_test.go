package clientruntime

import (
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
