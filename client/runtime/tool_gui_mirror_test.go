package clientruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestMirrorGUIReleaseURLsAndSubdomainPolicy(t *testing.T) {
	cases := []struct {
		goos, arch, agent, wantURL, wantFmt string
	}{
		{"darwin", "arm64", "claude", "https://claudeapp.agentsmirror.com/latest/mac", "dmg"},
		{"darwin", "amd64", "claude", "https://claudeapp.agentsmirror.com/latest/mac", "dmg"},
		{"windows", "amd64", "claude", "https://claudeapp.agentsmirror.com/latest/win-x64", "msix"},
		{"windows", "arm64", "claude", "https://claudeapp.agentsmirror.com/latest/win-arm64", "msix"},
		{"darwin", "arm64", "codex", "https://codexapp.agentsmirror.com/latest/mac-arm64", "dmg"},
		{"darwin", "amd64", "codex", "https://codexapp.agentsmirror.com/latest/mac-intel", "dmg"},
		{"windows", "amd64", "codex", "https://codexapp.agentsmirror.com/latest/win-x64", "msix"},
		{"windows", "arm64", "codex", "https://codexapp.agentsmirror.com/latest/win-arm64", "msix"},
	}
	for _, tc := range cases {
		rel, err := mirrorGUIRelease(tc.goos, tc.arch, tc.agent)
		if err != nil {
			t.Fatalf("%+v: unexpected err %v", tc, err)
		}
		if rel.URL != tc.wantURL || rel.Format != tc.wantFmt {
			t.Fatalf("%+v: got (%q, %q), want (%q, %q)", tc, rel.URL, rel.Format, tc.wantURL, tc.wantFmt)
		}
		if !allowedDownloadURL(rel.URL) {
			t.Fatalf("mirror URL rejected by allowlist: %s", rel.URL)
		}
	}

	for _, allowed := range []string{
		"https://codexapp-r2.agentsmirror.com/latest/win-x64",
		"https://codexapp-r2.agentsmirror.com/latest/mac-arm64",
		"https://claudeapp-r2.agentsmirror.com/latest/mac",
	} {
		if !allowedDownloadURL(allowed) {
			t.Fatalf("expected agentsmirror R2 subdomain to be allowed: %s", allowed)
		}
	}
	for _, blocked := range []string{
		"https://agentsmirror.com/latest/mac",
		"https://evil.agentsmirror.com/latest/mac",
		"https://a.codexapp.agentsmirror.com/latest/mac",
	} {
		if allowedDownloadURL(blocked) {
			t.Fatalf("unexpectedly allowed untrusted host: %s", blocked)
		}
	}
}

func TestOverrideGUIMirrorsJSON(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/client-dist/gui-mirrors.json" {
			http.NotFound(w, r)
			return
		}
		hits++
		_ = json.NewEncoder(w).Encode(map[string]map[string]string{
			"claude": {
				"mac":     "https://proxy.example.com/claude/mac.dmg",
				"win-x64": "http://insecure.example.com/claude.msix",
			},
			"codex": {
				"win-x64": "https://proxy.example.com/codex/win-x64.msix",
			},
		})
	}))
	defer srv.Close()

	m := &toolManager{root: t.TempDir(), goos: "darwin", arch: "arm64", origin: srv.URL}
	rel, ok := m.overrideGUIRelease(context.Background(), "claude")
	if !ok || rel.URL != "https://proxy.example.com/claude/mac.dmg" || rel.Format != "dmg" {
		t.Fatalf("unexpected override result: ok=%v rel=%+v", ok, rel)
	}
	// Second call hits 5m cache without another network request.
	if _, ok := m.overrideGUIRelease(context.Background(), "claude"); !ok || hits != 1 {
		t.Fatalf("expected cached mirror config (hits=%d)", hits)
	}

	// Non-HTTPS URL must be rejected.
	mWin := &toolManager{root: t.TempDir(), goos: "windows", arch: "amd64", origin: srv.URL}
	if _, ok := mWin.overrideGUIRelease(context.Background(), "claude"); ok {
		t.Fatal("expected http:// override URL to be rejected")
	}
	if rel, ok := mWin.overrideGUIRelease(context.Background(), "codex"); !ok || rel.URL != "https://proxy.example.com/codex/win-x64.msix" {
		t.Fatalf("expected codex win-x64 override, got ok=%v rel=%+v", ok, rel)
	}

	// Missing 404 file silently falls back without error.
	emptySrv := httptest.NewServer(http.NotFoundHandler())
	defer emptySrv.Close()
	m404 := &toolManager{root: t.TempDir(), goos: "darwin", arch: "arm64", origin: emptySrv.URL}
	if _, ok := m404.overrideGUIRelease(context.Background(), "claude"); ok {
		t.Fatal("expected 404 mirror config to return ok=false")
	}
}

func TestToolDownloadReportsPercentageProgress(t *testing.T) {
	payload := bytes.Repeat([]byte("a"), 256<<10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	d := &toolDownloader{
		client: srv.Client(),
		cache:  t.TempDir(),
		allow:  func(u string) bool { return strings.HasPrefix(u, srv.URL+"/") },
	}
	var lines []string
	if _, err := d.file(context.Background(), srv.URL+"/pkg.dmg", "", func(s string) {
		lines = append(lines, s)
	}); err != nil {
		t.Fatal(err)
	}
	foundPct := false
	for _, line := range lines {
		if strings.Contains(line, "%）") && strings.Contains(line, "MB/s") {
			foundPct = true
			break
		}
	}
	if !foundPct {
		t.Fatalf("expected percentage + speed progress line, got %v", lines)
	}
}

func TestConnectAutoStartsMissingGUIAndTierSwitchWarnsRunningGUI(t *testing.T) {
	g := &fakeGW{models: 200}
	base := g.handler()
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/install/tier" && r.Method == http.MethodPut {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data": map[string]any{
					"tier": "economy",
					"tiers": []map[string]any{
						{"name": "economy", "label": "省钱"},
						{"name": "balanced", "label": "均衡", "recommended": true},
					},
				},
			})
			return
		}
		base.ServeHTTP(w, r)
	}))
	defer gw.Close()
	a, _ := testDesktopApp(t, gw)

	var mu sync.Mutex
	installedComponent := ""
	installDone := make(chan struct{})
	a.env.ToolSnapshot = func() []ToolStatus {
		return []ToolStatus{
			{Agent: "claude", CLIInstalled: true, GUIAvailable: true, GUIInspected: true, GUIInstalled: false},
		}
	}
	a.env.InstallDesktop = func(ctx context.Context, agent, component string, progress func(string)) error {
		mu.Lock()
		installedComponent = agent + ":" + component
		mu.Unlock()
		close(installDone)
		return nil
	}

	if err := a.DesktopAction(DesktopRequest{Action: "connect-ticket", Agent: "claude", Ticket: "goodticket123"}); err != nil {
		t.Fatal(err)
	}
	<-installDone
	mu.Lock()
	gotComp := installedComponent
	mu.Unlock()
	if gotComp != "claude:gui" {
		t.Fatalf("expected auto-install of claude:gui, got %q", gotComp)
	}
	if !strings.Contains(a.getView().Notice, "安装官方桌面版") {
		t.Fatalf("expected GUI background install notice, got %q", a.getView().Notice)
	}

	// Switching tier while GUI is running should prompt user to restart the desktop app.
	a.env.GUIRunning = func(agent string) bool { return agent == "claude" }
	if err := a.DesktopAction(DesktopRequest{Action: "switch-tier", Agent: "claude", Tier: "economy"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(a.getView().Notice, "请退出并重开") {
		t.Fatalf("expected running-GUI restart reminder on tier switch, got %q", a.getView().Notice)
	}
}
