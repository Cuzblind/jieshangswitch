package clientruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestAuditWaveAFixes(t *testing.T) {
	// #5: pick-work-dir, set-work-dir, open-pricing must succeed without Agent
	g := &fakeGW{models: 200}
	gw := httptest.NewServer(g.handler())
	defer gw.Close()
	a, home := testDesktopApp(t, gw)
	workDir := filepath.Join(home, "docs")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var openedURL string
	a.env.Open = func(target string) error {
		openedURL = target
		return nil
	}
	if err := a.DesktopAction(DesktopRequest{Action: "set-work-dir", WorkDir: workDir}); err != nil {
		t.Fatalf("set-work-dir without agent failed: %v", err)
	}
	if got := a.DesktopState().WorkDir; got != workDir {
		t.Fatalf("WorkDir = %q, want %q", got, workDir)
	}
	if err := a.DesktopAction(DesktopRequest{Action: "open-pricing"}); err != nil {
		t.Fatalf("open-pricing without agent failed: %v", err)
	}
	if !strings.HasSuffix(openedURL, "/models") {
		t.Fatalf("open-pricing opened %q, want /models", openedURL)
	}

	// #3: team workspace recharge opens /team instead of personal /account
	a.mu.Lock()
	a.workspace = &WorkspaceInfo{Kind: "team", Label: "机密内部科研团队", TeamID: 7}
	a.mu.Unlock()
	if err := a.DesktopAction(DesktopRequest{Action: "recharge"}); err != nil {
		t.Fatalf("team recharge failed: %v", err)
	}
	if !strings.HasSuffix(openedURL, "/team") {
		t.Fatalf("team recharge opened %q, want /team", openedURL)
	}

	// #22: sanitized support report must never leak internal team name
	if rep := a.supportReport(); strings.Contains(rep, "机密内部科研团队") || !strings.Contains(rep, "团队工作区") {
		t.Fatalf("supportReport leaked team name or missed workspace type: %s", rep)
	}

	// #19: ZIP URL with query params + ZIP magic byte detection
	rel, ok := pickOverrideRelease(map[string]map[string]string{
		"claude": {"mac": "https://cdn.example.com/Claude.zip?token=abc&sig=123"},
	}, "claude", "mac", "dmg")
	if !ok || rel.Format != "zip" {
		t.Fatalf("expected zip format for signed URL, got ok=%v rel=%+v", ok, rel)
	}
	zipFile := filepath.Join(home, "archive.bin")
	_ = os.WriteFile(zipFile, []byte{'P', 'K', 0x03, 0x04, 0x00, 0x00}, 0o600)
	if !isZIPFile(zipFile) {
		t.Fatal("isZIPFile failed to identify PK header")
	}

	// #21: onboarding backup & restore reverts false back to false and preserves user keys
	claudeJSON := filepath.Join(home, ".claude.json")
	_ = os.WriteFile(claudeJSON, []byte(`{"hasCompletedOnboarding":false,"mcpServers":{"keep":{"command":"x"}}}`), 0o600)
	if err := ensureClaudeOnboarding(); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(claudeJSON)
	if !strings.Contains(string(raw), `"hasCompletedOnboarding": true`) {
		t.Fatalf("onboarding not set: %s", raw)
	}
	if err := restoreClaudeOnboarding(); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(claudeJSON)
	if !strings.Contains(string(raw), `"hasCompletedOnboarding": false`) || !strings.Contains(string(raw), `"keep"`) {
		t.Fatalf("onboarding restore failed: %s", raw)
	}
}

func TestWalletSwitchClearsUsageAndRejectsLateStaleResponse(t *testing.T) {
	personalKey := "sk-PERSONAL0123456789abcdef"
	teamKey := "sk-TEAMKEY0123456789abcdef"
	usageStarted := make(chan struct{})
	usageRelease := make(chan struct{})
	var personalRevoked bool
	var mu sync.Mutex

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		switch r.URL.Path {
		case "/api/install/balance":
			mu.Lock()
			revoked := personalRevoked
			mu.Unlock()
			if auth == personalKey && revoked {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"success":false,"stopped":true}`))
				return
			}
			if auth == personalKey {
				_, _ = w.Write([]byte(`{"success":true,"data":{"balance_cny":10,"workspace":{"kind":"personal","label":"个人工作区"},"today":{"cost_cny":"1.00","requests":1,"tokens":100}}}`))
				return
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"balance_cny":99,"workspace":{"kind":"team","label":"团队A","team_id":9},"today":{"cost_cny":"0.00","requests":0,"tokens":0}}}`))
		case "/api/install/usage":
			if auth == personalKey {
				select {
				case <-usageStarted:
				default:
					close(usageStarted)
				}
				<-usageRelease
				_, _ = w.Write([]byte(`{"success":true,"data":{"range":"24h","cost_cny":"1.00","requests":1,"tokens":100,"heatmap":[],"logs":[]}}`))
				return
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"range":"24h","cost_cny":"0.00","requests":0,"tokens":0,"heatmap":[],"logs":[]}}`))
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	a, _ := testDesktopApp(t, srv)
	_ = a.env.Write("claude", srv.URL, personalKey)
	_ = a.env.Write("codex", srv.URL, teamKey)
	a.desktopRefresh()
	if st := a.DesktopState(); st.Workspace == nil || st.Workspace.Kind != "personal" {
		t.Fatalf("expected personal workspace first, got %+v", st.Workspace)
	}

	// Start in-flight usage fetch for personal wallet, then revoke personal & switch to team wallet.
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = a.fetchUsage("24h")
	}()
	<-usageStarted
	mu.Lock()
	personalRevoked = true
	mu.Unlock()
	a.desktopRefresh()
	close(usageRelease)
	<-done

	st := a.DesktopState()
	if st.Workspace == nil || st.Workspace.Kind != "team" {
		t.Fatalf("expected team workspace after personal revoked, got %+v", st.Workspace)
	}
	if st.Usage != nil {
		t.Fatalf("stale personal usage leaked onto team workspace: %+v", st.Usage)
	}
}

func TestOpenWebAndCodexSwitchAccount(t *testing.T) {
	g := &fakeGW{models: 200}
	gw := httptest.NewServer(g.handler())
	defer gw.Close()
	a, _ := testDesktopApp(t, gw)
	var openedURL string
	a.env.Open = func(target string) error {
		openedURL = target
		return nil
	}

	// open-web for personal workspace -> /account
	if err := a.DesktopAction(DesktopRequest{Action: "open-web"}); err != nil {
		t.Fatalf("open-web personal failed: %v", err)
	}
	if !strings.HasSuffix(openedURL, "/account") {
		t.Fatalf("open-web personal opened %q, want /account", openedURL)
	}

	// open-web for team workspace -> /team
	a.mu.Lock()
	a.workspace = &WorkspaceInfo{Kind: "team", Label: "测试团队", TeamID: 5}
	a.mu.Unlock()
	if err := a.DesktopAction(DesktopRequest{Action: "open-web"}); err != nil {
		t.Fatalf("open-web team failed: %v", err)
	}
	if !strings.HasSuffix(openedURL, "/team") {
		t.Fatalf("open-web team opened %q, want /team", openedURL)
	}

	// switch-account when only Codex is connected -> agent=codex
	_ = a.env.Write("codex", gw.URL, "sk-CODEXONLY0123456789abcdef")
	if err := a.DesktopAction(DesktopRequest{Action: "switch-account"}); err != nil {
		t.Fatalf("switch-account failed: %v", err)
	}
	if !strings.Contains(openedURL, "agent=codex") || !strings.Contains(openedURL, "continue=1") {
		t.Fatalf("switch-account for codex-only opened %q, want agent=codex&continue=1", openedURL)
	}
}
