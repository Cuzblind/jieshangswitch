package clientruntime

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const fakeKey = "sk-FAKECLIENTKEY0123456789abcdef"
const fakeClientAuth = "test-relay-client-auth"

type fakeGW struct {
	mu          sync.Mutex
	claimed     map[string]bool
	validTicket map[string]bool
	progress    []string
	models      int // status for /v1/models
	clientCalls int
	clientAuth  string
	denyClient  bool
	devices     []map[string]string
}

func (g *fakeGW) handler() http.Handler {
	if g.claimed == nil {
		g.claimed = map[string]bool{}
	}
	if g.validTicket == nil {
		g.validTicket = map[string]bool{"goodticket123": true}
	}
	if g.clientAuth == "" {
		g.clientAuth = fakeClientAuth
	}
	m := http.NewServeMux()
	m.HandleFunc("/api/install/claim", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Token  string
			Client bool
			Device map[string]string
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		g.mu.Lock()
		defer g.mu.Unlock()
		g.devices = append(g.devices, req.Device)
		if !req.Client || !g.validTicket[req.Token] {
			w.WriteHeader(404)
			return
		}
		if g.claimed[req.Token] {
			w.WriteHeader(410)
			return
		}
		g.claimed[req.Token] = true
		_, _ = w.Write([]byte(`{"success":true,"data":{"key":"` + fakeKey + `","client_auth":"` + g.clientAuth + `"}}`))
	})
	m.HandleFunc("/api/install/client/token", func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		g.clientCalls++
		if g.denyClient || r.Header.Get("Authorization") != "Bearer "+g.clientAuth {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"success":false,"message":"这个连接过期了，需要重新接一次。"}`))
			return
		}
		ticket := "issuedticket12"
		g.validTicket[ticket] = true
		_, _ = w.Write([]byte(`{"success":true,"data":{"token":"` + ticket + `","agent":"codex"}}`))
	})
	m.HandleFunc("/api/install/progress", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		g.mu.Lock()
		g.progress = append(g.progress, req["status"].(string))
		g.mu.Unlock()
		_, _ = w.Write([]byte(`{"success":true}`))
	})
	m.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+fakeKey {
			w.WriteHeader(401)
			return
		}
		w.WriteHeader(g.models)
		if g.models == http.StatusOK {
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"claude-sonnet-4-5"}]}`))
		}
	})
	m.HandleFunc("/api/install/balance", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":{"balance_cny":"12.3","balance_low":false}}`))
	})
	return m
}

func testApp(t *testing.T, gw *httptest.Server) (*App, string) {
	t.Helper()
	a, home, _ := testAppOpened(t, gw)
	return a, home
}

func testAppOpened(t *testing.T, gw *httptest.Server) (*App, string, *[]string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("RELAY_HOME", home)
	t.Setenv("HOME", home) // the client config folder on macOS
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	if runtime.GOOS == "windows" {
		t.Setenv("APPDATA", filepath.Join(home, "appdata"))
		t.Setenv("LOCALAPPDATA", filepath.Join(home, "localappdata"))
		t.Setenv("USERPROFILE", home)
	}
	t.Setenv("SHELL", "/bin/bash")
	env := baseEnv()
	env.Origin = gw.URL
	env.FindCLI = func(string) bool { return true }
	env.InstallCLI = func(string) error { return nil }
	env.Running = func(string) bool { return false }
	env.OfficialLogin = func(string) bool { return false }
	opened := []string{}
	env.Open = func(s string) error { opened = append(opened, s); return nil }
	env.Launch = func(string, string) error { return nil }
	return newApp(env), home, &opened
}

func waitIdle(t *testing.T, a *App) View {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		if v := a.getView(); !v.Busy && v.Screen != "home" {
			return v
		}
	}
	t.Fatalf("still busy: %+v", a.getView())
	return View{}
}

func TestParseDeepLink(t *testing.T) {
	for _, c := range []struct {
		in     string
		ok     bool
		ticket string
	}{
		{"jieshangai://install?ticket=abcDEF123_-x&agent=claude", true, "abcDEF123_-x"},
		{"jieshangai://install/?ticket=abcDEF123&agent=codex", true, "abcDEF123"},
		{"jieshangai:install?ticket=abcDEF123&agent=codex", true, "abcDEF123"},
		{"jieshangai://install?ticket=abc&agent=claude", false, ""},
		{"jieshangai://install?ticket=abcDEF123&agent=gemini", false, ""},
		{"jieshangai://evil?ticket=abcDEF123&agent=claude", false, ""},
		{"https://install?ticket=abcDEF123&agent=claude", false, ""},
		{"jieshangai://install?ticket=a%27b%22cdefgh&agent=claude", false, ""},
	} {
		tk, _, ok := ParseDeepLink(c.in)
		if ok != c.ok || tk != c.ticket {
			t.Errorf("%s: got %q %v", c.in, tk, ok)
		}
	}
}

func TestConnectHappyPathClaude(t *testing.T) {
	g := &fakeGW{models: 200}
	gw := httptest.NewServer(g.handler())
	defer gw.Close()
	a, home := testApp(t, gw)
	a.HandleURL("jieshangai://install?ticket=goodticket123&agent=claude")
	v := waitIdle(t, a)
	if v.Screen != "success" || v.Line != "先试一句：复制下面这句话，在 Claude Code 里发出去。" || v.Button != "复制，打开 Claude Code" || v.Action != "launch-try" || v.Data["try"] != "用三句话告诉我你能帮我做什么" {
		t.Fatalf("view %+v", v)
	}
	if v.Second != "再配 Codex" || v.Second2 != "connect" {
		t.Fatalf("second %+v", v)
	}
	b, _ := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if !strings.Contains(string(b), gw.URL) || !strings.Contains(string(b), fakeKey) {
		t.Fatalf("settings not written")
	}
	if s, ok := configuredSecret("claude", gw.URL); !ok || s != fakeKey {
		t.Fatal("configured not detected")
	}
	a.refreshStatus(true)
	snap := a.snapshot()
	if snap.Statuses[0].Badge != "能用了" || snap.Statuses[1].Badge != "还没接" || snap.Balance == nil || snap.Balance.Text != "余额 ¥12.30" {
		t.Fatalf("status %+v bal %+v", snap.Statuses, snap.Balance)
	}
	g.mu.Lock()
	last := g.progress[len(g.progress)-1]
	g.mu.Unlock()
	if last != "done" {
		t.Fatalf("progress %v", g.progress)
	}
	// secret never leaks into what the page gets
	raw, _ := json.Marshal(snap)
	if bytes.Contains(raw, []byte(fakeKey)) || bytes.Contains(raw, []byte("FAKECLIENT")) {
		t.Fatal("secret leaked to UI")
	}
	// restore (M4/M5): nothing existed before → files gone
	a.act("restore", "claude")
	if v := a.getView(); v.Screen != "confirm" || v.Extra != "" || !strings.Contains(v.Note, "只清这台电脑") {
		t.Fatalf("confirm %+v", v)
	}
	a.act("restore-yes", "claude")
	v = waitIdle(t, a)
	if v.Screen != "done" {
		t.Fatalf("restore %+v", v)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Fatal("settings should be removed")
	}
}

func TestConnectExpiredAndNetwork(t *testing.T) {
	g := &fakeGW{models: 200}
	gw := httptest.NewServer(g.handler())
	defer gw.Close()
	a, _ := testApp(t, gw)
	a.HandleURL("jieshangai://install?ticket=badticket123&agent=codex")
	v := waitIdle(t, a)
	if v.Line != "这个连接过期了，需要重新接一次。" || v.Button != "重新接上" {
		t.Fatalf("%+v", v)
	}
	// network: second failure turns the one button into 联系客服
	g2 := &fakeGW{models: 502}
	gw2 := httptest.NewServer(g2.handler())
	defer gw2.Close()
	a2, _ := testApp(t, gw2)
	a2.HandleURL("jieshangai://install?ticket=goodticket123&agent=codex")
	v = waitIdle(t, a2)
	if v.Line != "现在连不上，可能是网络不太好。" || v.Button != "再试一次" {
		t.Fatalf("%+v", v)
	}
	a2.act("retry", "codex")
	v = waitIdle(t, a2)
	if v.Button != "再试一次" || v.Action != "retry" || v.Line != "还是连不上。换个网络，或过会儿再试一次。" { // D30/D31
		t.Fatalf("%+v", v)
	}
}

func TestBadLinkAndConflictAndCLI(t *testing.T) {
	g := &fakeGW{models: 200}
	gw := httptest.NewServer(g.handler())
	defer gw.Close()
	a, _ := testApp(t, gw)
	a.HandleURL("jieshangai://install?agent=claude")
	if v := a.getView(); v.Line != "没收到网页发来的连接。" || v.Button != "回网页再点一次" || v.Action != "open-web" {
		t.Fatalf("%+v", v)
	}
	loggedIn := true
	a.env.OfficialLogin = func(string) bool { return loggedIn }
	a.env.Logout = func(string) error { loggedIn = false; return nil }
	a.HandleURL("jieshangai://install?ticket=goodticket123&agent=claude")
	v := waitIdle(t, a)
	if v.Button != "一键修好" {
		t.Fatalf("%+v", v)
	}
	a.act("fix-login", "claude")
	if v := a.getView(); v.Screen != "confirm" || v.Button != "好，帮我修好" {
		t.Fatalf("%+v", v)
	}
	a.act("logout-yes", "claude")
	if v := waitIdle(t, a); v.Screen != "success" {
		t.Fatalf("%+v", v)
	}
	// CLI cannot be installed → 客服
	a.env.FindCLI = func(string) bool { return false }
	a.env.InstallCLI = func(string) error { return os.ErrPermission }
	a.act("install-cli-run", "codex")
	if v := waitIdle(t, a); v.Line != "没能自动装上。重新打开接上AI，再试一次。" || v.Button != "再试一次" || v.Action != "install-cli" { // D30
		t.Fatalf("%+v", v)
	}
}

func TestGuard(t *testing.T) {
	gw := httptest.NewServer(http.NotFoundHandler())
	defer gw.Close()
	a, _ := testApp(t, gw)
	h := a.routes()
	for _, c := range []struct {
		host, secret string
		want         int
	}{
		{"127.0.0.1:" + lockPort, a.secret, 200},
		{"127.0.0.1:" + lockPort, "nope", 403},
		{"evil.example:" + lockPort, a.secret, 403},
	} {
		r := httptest.NewRequest("GET", "/api/state", nil)
		r.Host = c.host
		r.Header.Set("X-S", c.secret)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != c.want {
			t.Errorf("%+v: %d", c, w.Code)
		}
	}
}

// Windows runs the bundled copies of install.ps1 / uninstall.ps1; they must be
// byte-identical to what /i and /u serve.
func TestScriptsInSync(t *testing.T) {
	for _, n := range []string{"install.ps1", "uninstall.ps1"} {
		a, _ := os.ReadFile(filepath.Join("scripts", n))
		b, _ := os.ReadFile(filepath.Join("..", "..", "install", "ps1", n))
		if len(a) == 0 || !bytes.Equal(a, b) {
			t.Fatalf("%s drifted: run go generate", n)
		}
	}
}

// Windows PowerShell 5.1 unrolls string[] rows piped through Sort-Object
// -Property, which turned each manifest row into its four bare fields and made
// restore iterate single characters: backups were never restored, secrets
// stayed in settings.json, and the script still reported success.
func TestEngineRestoreRemovesFreshInstall(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows engine")
	}
	home := t.TempDir()
	t.Setenv("RELAY_HOME", home)
	if err := engineWrite("claude", "https://relay.example.com", fakeKey); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(home, ".claude", "settings.json")
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("settings not written: %v", err)
	}
	if _, err := engineRestore(true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("restore left the freshly-written settings (and its secrets) behind")
	}
}

// The Windows engine (ps1 via env, as engine_windows.go calls it) must give
// the same tree as the POSIX core for Claude. Runs where pwsh exists.
func TestPS1EngineMatchesCoreClaude(t *testing.T) {
	pw, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skip("no pwsh")
	}
	origin := "https://relay.example.com"
	r1 := t.TempDir()
	cmd := exec.Command(pw, "-NoProfile", "-NonInteractive", "-File", filepath.Join("scripts", "install.ps1"))
	cmd.Env = append(os.Environ(), "RELAY_HOME="+r1, "RELAY_AGENT=claude", "RELAY_TOKEN="+fakeKey, "RELAY_SECRET="+fakeKey,
		"RELAY_SITE_ORIGIN="+origin, "RELAY_SKIP_CLI=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	r2 := t.TempDir()
	t.Setenv("RELAY_HOME", r2)
	if err := engineWrite("claude", origin, fakeKey); err != nil {
		t.Fatal(err)
	}
	a, _ := os.ReadFile(filepath.Join(r1, ".claude", "settings.json"))
	b, _ := os.ReadFile(filepath.Join(r2, ".claude", "settings.json"))
	var ja, jb any
	_ = json.Unmarshal(a, &ja)
	_ = json.Unmarshal(b, &jb)
	sa, _ := json.Marshal(ja)
	sb, _ := json.Marshal(jb)
	if string(sa) != string(sb) {
		t.Fatalf("ps1 vs core differ:\n%s\n%s", a, b)
	}
}

func TestHelloCORS(t *testing.T) {
	gw := httptest.NewServer(http.NotFoundHandler())
	defer gw.Close()
	a, _ := testApp(t, gw)
	a.env.Web = "http://192.168.116.22:18080"
	h := a.routes()
	for _, c := range []struct{ origin, want string }{
		{"https://jieshangai.com", "https://jieshangai.com"},
		{"http://127.0.0.1:3100", "http://127.0.0.1:3100"},
		{"http://192.168.116.22:18080", "http://192.168.116.22:18080"},
		{"http://192.168.116.22:18082", ""},
		{"https://evil.example", ""},
	} {
		r := httptest.NewRequest("GET", "/hello", nil)
		r.Header.Set("Origin", c.origin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 204 || w.Header().Get("Access-Control-Allow-Origin") != c.want {
			t.Errorf("%s: %d %q", c.origin, w.Code, w.Header().Get("Access-Control-Allow-Origin"))
		}
	}
	a.act("web-continue", "")
}

func TestContinueWebResumesSelectedAssistant(t *testing.T) {
	gw := httptest.NewServer(http.NotFoundHandler())
	defer gw.Close()
	a, _, opened := testAppOpened(t, gw)
	a.env.Web = "http://192.168.116.22:18080"
	for _, agent := range []string{"claude", "codex"} {
		a.act("open-web", agent)
		want := a.env.Web + "/download?agent=" + agent + "&continue=1"
		if got := (*opened)[len(*opened)-1]; got != want {
			t.Fatalf("continue %s: got %q, want %q", agent, got, want)
		}
	}
}

func TestSharedCopyInSync(t *testing.T) {
	web, _ := os.ReadFile(filepath.Join("..", "..", "web-app", "src", "lib", "shared-copy.json"))
	if len(web) == 0 || !bytes.Equal(web, sharedCopyJSON) {
		t.Fatal("shared-copy.json drifted from web-app: run go generate")
	}
	// web copy.ts must use the shared generator, not a hard-coded string
	// (checked when running inside the full monorepo; standalone client repo omits web-app source).
	if ts, err := os.ReadFile(filepath.Join("..", "..", "web-app", "src", "lib", "copy.ts")); err == nil {
		if !bytes.Contains(ts, []byte(`restoreCommand("mac")`)) || !bytes.Contains(ts, []byte(`restoreCommand("win")`)) || bytes.Contains(ts, []byte("/u | ")) {
			t.Fatal("web copy.ts no longer uses the shared restore command")
		}
	} else if !os.IsNotExist(err) {
		t.Fatalf("read web-app/src/lib/copy.ts: %v", err)
	}
	if got := restoreCommand("darwin", "https://jieshangai.com/"); got != "curl -fsSL https://jieshangai.com/u | bash" {
		t.Fatal(got)
	}
	if got := restoreCommand("windows", "https://jieshangai.com"); got != "irm https://jieshangai.com/u | iex" {
		t.Fatal(got)
	}
	// and it matches the shape docs/INSTALL-COMMANDS.md documents (monorepo only).
	if doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "INSTALL-COMMANDS.md")); err == nil {
		for _, c := range []string{`curl -fsSL https://jieshangai.com/u \| bash`, "irm https://jieshangai.com/u \\| iex"} {
			if !bytes.Contains(doc, []byte(c)) {
				t.Fatalf("doc lacks %s", c)
			}
		}
	} else if !os.IsNotExist(err) {
		t.Fatalf("read docs/INSTALL-COMMANDS.md: %v", err)
	}
}

func TestStoppedStatus(t *testing.T) {
	g := &fakeGW{models: 200}
	mux := http.NewServeMux()
	var stopped atomic.Bool
	mux.Handle("/", g.handler())
	mux.HandleFunc("/api/install/balance", func(w http.ResponseWriter, r *http.Request) {
		if stopped.Load() {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"success":false,"stopped":true}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"balance_cny":1,"balance_low":false}}`))
	})
	gw := httptest.NewServer(mux)
	defer gw.Close()
	a, _ := testApp(t, gw)
	a.HandleURL("jieshangai://install?ticket=goodticket123&agent=claude")
	waitIdle(t, a)
	stopped.Store(true)
	a.act("refresh-balance", "")
	time.Sleep(300 * time.Millisecond)
	s := a.snapshot().Statuses[0]
	if s.Badge != "已停用" || s.Button != "重新接上 Claude Code" || s.State != "stopped" {
		t.Fatalf("%+v", s)
	}
}

func TestBalanceUnavailableDoesNotLookExpired(t *testing.T) {
	g := &fakeGW{models: 200}
	mux := http.NewServeMux()
	mux.Handle("/", g.handler())
	mux.HandleFunc("/api/install/balance", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"success":false,"message":"暂时读不到余额，过一会儿再试。"}`))
	})
	gw := httptest.NewServer(mux)
	defer gw.Close()
	a, _ := testApp(t, gw)
	a.HandleURL("jieshangai://install?ticket=goodticket123&agent=claude")
	waitIdle(t, a)
	a.act("refresh-balance", "")
	time.Sleep(300 * time.Millisecond)
	snap := a.snapshot()
	s := snap.Statuses[0]
	if s.Badge != "能用了" || s.State != "ok" {
		t.Fatalf("balance read failure keeps connection usable: %+v", s)
	}
	if snap.Balance == nil || snap.Balance.Text != "暂时读不到余额，过一会儿再试。" {
		t.Fatalf("want soft balance line, got %+v", snap.Balance)
	}
	if a.getView().Screen == "error" {
		if kind := a.getView().Data["kind"]; kind == "expired" {
			t.Fatalf("must not jump to expired on 503: %+v", a.getView())
		}
	}
}

func TestDisabledAccountRefreshNotUsable(t *testing.T) {
	g := &fakeGW{models: 200}
	mux := http.NewServeMux()
	mux.Handle("/", g.handler())
	mux.HandleFunc("/api/install/balance", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"success":false,"account_disabled":true,"message":"这个账户已停用，助手暂时用不了。"}`))
	})
	gw := httptest.NewServer(mux)
	defer gw.Close()
	a, _ := testApp(t, gw)
	a.HandleURL("jieshangai://install?ticket=goodticket123&agent=claude")
	waitIdle(t, a)
	a.act("refresh-balance", "")
	time.Sleep(300 * time.Millisecond)
	s := a.snapshot().Statuses[0]
	if s.Badge == "能用了" || s.State == "ok" {
		t.Fatalf("disabled account must not show 能用了: %+v", s)
	}
	if s.Badge != "不能用" || s.State != "disabled" {
		t.Fatalf("want badge 不能用, got %+v", s)
	}
	if s.Note != "这个账户已停用，助手暂时用不了。" {
		t.Fatalf("want disabled note, got %+v", s)
	}
}

func TestBalanceViewStates(t *testing.T) {
	cases := []struct {
		f          float64
		text, hint string
		red        bool
	}{
		{12.3, "余额 ¥12.30", "", false},
		{3.456, "余额 ¥3.46", "余额快用完了，联系客服续上", false}, // QQ set → CS on
		{0, "余额 ¥0.00", "余额用完了，联系客服续上", true},
		{-1.234, "欠 ¥1.23，联系客服续上后先扣", "", true},
	}
	for _, c := range cases {
		b := balanceView(c.f, false)
		if b.Text != c.text || b.Hint != c.hint || b.Red != c.red {
			t.Errorf("%v: %+v", c.f, b)
		}
	}
	// D16: QQ and email both empty → CS off, no 「联系客服」.
	t.Cleanup(resetRuntimeSupportEmail)
	resetRuntimeSupportEmail()
	prev := shared.Support.QQ
	shared.Support.QQ = ""
	t.Cleanup(func() { shared.Support.QQ = prev })
	off := []struct {
		f          float64
		text, hint string
		red        bool
	}{
		{3.456, "余额 ¥3.46", "余额快用完了，记得续费", false},
		{0, "余额 ¥0.00", "余额用完了，开通续费后再用", true},
		{-1.234, "欠 ¥1.23，续上后先扣", "", true},
	}
	for _, c := range off {
		b := balanceView(c.f, false)
		if b.Text != c.text || b.Hint != c.hint || b.Red != c.red {
			t.Errorf("off %v: %+v", c.f, b)
		}
	}
}

// Support is QQ only: the page shows the number and a copy button, never a QR or mailto.
func TestSupportPageIsQQ(t *testing.T) {
	if shared.Support.QQ != "2088199761" {
		t.Fatalf("shared support.qq=%q", shared.Support.QQ)
	}
	for _, l := range shared.Support.Lines {
		if strings.Contains(l, "微信") || strings.Contains(l, "邮件") || strings.Contains(l, "扫码") {
			t.Fatalf("support line still points at WeChat/email: %q", l)
		}
	}
	if shared.Support.Lines[1] != "工作时间：工作日 10:00–19:00" {
		t.Fatalf("hours line=%q", shared.Support.Lines[1])
	}
	t.Cleanup(resetRuntimeSupportEmail)
	resetRuntimeSupportEmail()
	g := &fakeGW{models: 200}
	gw := httptest.NewServer(g.handler())
	defer gw.Close()
	a, _ := testApp(t, gw)
	if !a.snapshot().Support {
		t.Fatal("QQ alone should turn support on")
	}
	a.act("support", "claude")
	v := a.getView()
	if v.Screen != "support" || v.Data["qq"] != "2088199761" || v.Button != "复制 QQ 号" || v.Action != "__copyqq" {
		t.Fatalf("%+v", v)
	}
	if _, ok := v.Data["email"]; ok {
		t.Fatalf("support page should not carry an email line: %+v", v.Data)
	}
	if r := v.Data["report"]; !strings.Contains(r, "接上AI") || !strings.Contains(r, "软件：") {
		t.Fatalf("report=%q", r)
	}
	page, _ := uiFS.ReadFile("ui/index.html")
	for _, bad := range []string{"qr.png", "扫码", "mailto", "d.email"} {
		if bytes.Contains(page, []byte(bad)) {
			t.Fatalf("ui still has %q", bad)
		}
	}
}

func TestOpenDeepLinkGoesHome(t *testing.T) {
	g := &fakeGW{models: 200}
	srv := httptest.NewServer(g.handler())
	defer srv.Close()
	a, _ := testApp(t, srv)
	a.HandleURL("jieshangai://open")
	if v := a.getView(); v.Screen != "home" {
		t.Fatalf("%+v", v)
	}
}

// D36: a 401 on the balance poll (not 已停用) moves the home screen to error-expired 「重新接上」.
func TestExpiredOnHomeGoesToReconnect(t *testing.T) {
	g := http.NewServeMux()
	g.HandleFunc("/api/install/balance", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"success":false}`))
	})
	srv := httptest.NewServer(g)
	defer srv.Close()
	a, _ := testApp(t, srv)
	a.env.Configured = func(agent, origin string) (string, bool) { return "x", agent == "claude" }
	a.setView(View{Screen: "home"})
	a.refreshStatus(true)
	v := a.getView()
	if v.Screen != "error" || v.Button != "重新接上" || v.Action != "connect" || v.Data["kind"] != "expired" {
		t.Fatalf("%+v", v)
	}
}

func TestConnectDoesNotOpenWeb(t *testing.T) {
	g := &fakeGW{models: 200}
	gw := httptest.NewServer(g.handler())
	defer gw.Close()
	a, home, opened := testAppOpened(t, gw)
	a.HandleURL("jieshangai://install?ticket=goodticket123&agent=claude")
	if v := waitIdle(t, a); v.Screen != "success" {
		t.Fatalf("%+v", v)
	}
	if loadClientAuth(gw.URL) == "" {
		t.Fatal("client auth not stored")
	}
	st, err := os.Stat(clientAuthPath(gw.URL))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if st.Mode().Perm() != 0o600 {
			t.Fatalf("auth file mode %v", st.Mode())
		}
	}
	snap := a.snapshot()
	raw, _ := json.Marshal(snap)
	if !snap.Authorized || bytes.Contains(raw, []byte(fakeClientAuth)) || bytes.Contains(raw, []byte(fakeKey)) {
		t.Fatalf("auth leaked or missing: %s", raw)
	}
	if len(*opened) != 0 {
		t.Fatalf("opened %v", *opened)
	}
	a.act("connect", "codex")
	if v := waitIdle(t, a); v.Screen != "success" || v.Agent != "codex" {
		t.Fatalf("%+v", v)
	}
	g.mu.Lock()
	calls := g.clientCalls
	g.mu.Unlock()
	if calls < 1 {
		t.Fatal("did not issue client ticket")
	}
	if len(*opened) != 0 {
		t.Fatalf("opened during connect %v", *opened)
	}
	b, _ := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if !bytes.Contains(b, []byte(gw.URL)) {
		t.Fatal("codex not written")
	}
}

func TestUsedTicketRetriesWithNewClientTicket(t *testing.T) {
	g := &fakeGW{models: 200}
	gw := httptest.NewServer(g.handler())
	defer gw.Close()
	a, _, opened := testAppOpened(t, gw)
	g.validTicket["usedticket123"] = true
	g.claimed["usedticket123"] = true
	a.HandleURL("jieshangai://install?ticket=goodticket123&agent=claude")
	waitIdle(t, a)
	a.HandleURL("jieshangai://install?ticket=usedticket123&agent=codex")
	v := waitIdle(t, a)
	if v.Data["kind"] != "expired" {
		t.Fatalf("%+v", v)
	}
	a.act("retry", "codex")
	if v := waitIdle(t, a); v.Screen != "success" || v.Agent != "codex" {
		t.Fatalf("%+v", v)
	}
	if len(*opened) != 0 {
		t.Fatalf("opened %v", *opened)
	}
	// Every claim names this computer with the same kept id.
	g.mu.Lock()
	defer g.mu.Unlock()
	first := g.devices[0]
	if !validDeviceID(first["id"]) || first["os"] != deviceOS(runtime.GOOS) || first["name"] == "" {
		t.Fatalf("device %v", first)
	}
	for _, d := range g.devices {
		if d["id"] != first["id"] {
			t.Fatalf("device id changed: %v", g.devices)
		}
	}
	if b, err := os.ReadFile(deviceIDPath()); err != nil || string(b) != first["id"] {
		t.Fatalf("device id not kept: %q %v", b, err)
	}
}

func TestExpiredClientAuthClearsAndDoesNotOpen(t *testing.T) {
	g := &fakeGW{models: 200, denyClient: true}
	gw := httptest.NewServer(g.handler())
	defer gw.Close()
	a, _, opened := testAppOpened(t, gw)
	a.saveClientAuth(fakeClientAuth)
	a.act("connect", "codex")
	v := waitIdle(t, a)
	if v.Data["kind"] != "reauth" || v.Action != "open-web" || v.Button != "回网页继续" {
		t.Fatalf("%+v", v)
	}
	if a.hasClientAuth() {
		t.Fatal("expired auth should be cleared")
	}
	if len(*opened) != 0 {
		t.Fatalf("opened %v", *opened)
	}
}

func TestAuthorizedHomeShowsCardsWhenBothUnconnected(t *testing.T) {
	g := &fakeGW{models: 200}
	gw := httptest.NewServer(g.handler())
	defer gw.Close()
	a, _ := testApp(t, gw)
	a.saveClientAuth(fakeClientAuth)
	a.setView(View{Screen: "home"})
	a.refreshStatus(true)
	snap := a.snapshot()
	if !snap.Authorized || snap.Statuses[0].Action != "connect" || snap.Statuses[1].Action != "connect" {
		t.Fatalf("%+v", snap)
	}
	raw, _ := json.Marshal(snap)
	if bytes.Contains(raw, []byte(fakeClientAuth)) {
		t.Fatal("auth leaked")
	}
}

func TestClientAuthRestartAndBusy(t *testing.T) {
	g := &fakeGW{models: 200}
	gw := httptest.NewServer(g.handler())
	defer gw.Close()
	a, _ := testApp(t, gw)
	a.saveClientAuth(fakeClientAuth)
	a2 := newApp(a.env)
	if !a2.hasClientAuth() {
		t.Fatal("auth not loaded after restart")
	}
	a2.setView(connecting("claude", 1))
	a2.act("connect", "codex")
	if v := a2.getView(); v.Agent != "claude" || !v.Busy {
		t.Fatalf("busy ignored: %+v", v)
	}
}

func TestSpendingStillOpensWeb(t *testing.T) {
	g := &fakeGW{models: 200}
	gw := httptest.NewServer(g.handler())
	defer gw.Close()
	a, _, opened := testAppOpened(t, gw)
	a.env.Web = "http://127.0.0.1:3100"
	a.act("spending", "")
	if len(*opened) != 1 || (*opened)[0] != "http://127.0.0.1:3100/spending" {
		t.Fatalf("%v", *opened)
	}
}

// U2: hasSupport prefers runtime /api/status email; build-time shared-copy is fallback.
func TestHasSupportRuntime(t *testing.T) {
	t.Cleanup(resetRuntimeSupportEmail)
	resetRuntimeSupportEmail()
	prevQQ := shared.Support.QQ
	shared.Support.QQ = ""
	t.Cleanup(func() { shared.Support.QQ = prevQQ })

	if hasSupport() {
		t.Fatal("QQ and email both empty still false")
	}

	setRuntimeSupportEmail("help@example.com")
	if !hasSupport() {
		t.Fatal("after injecting runtime email want true")
	}
	if got := supportEmail(); got != "help@example.com" {
		t.Fatalf("supportEmail=%q", got)
	}

	resetRuntimeSupportEmail()
	prev := shared.Support.Email
	shared.Support.Email = "build@example.com"
	t.Cleanup(func() { shared.Support.Email = prev })
	if !hasSupport() {
		t.Fatal("build-time non-empty still works as fallback")
	}
	if got := supportEmail(); got != "build@example.com" {
		t.Fatalf("fallback supportEmail=%q", got)
	}
}

// U2: refreshStatus (open / manual refresh) fetches relay_support_email; support page shows it.
func TestRefreshStatusFetchesSupportEmail(t *testing.T) {
	t.Cleanup(resetRuntimeSupportEmail)
	resetRuntimeSupportEmail()
	prevQQ := shared.Support.QQ
	shared.Support.QQ = ""
	t.Cleanup(func() { shared.Support.QQ = prevQQ })

	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":{"relay_support_email":"cs@example.com"}}`))
	})
	mux.HandleFunc("/api/install/balance", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":{"balance_cny":"12.3","balance_low":false}}`))
	})
	gw := httptest.NewServer(mux)
	defer gw.Close()

	a, _ := testApp(t, gw)
	a.refreshStatus(true)
	if !hasSupport() || supportEmail() != "cs@example.com" {
		t.Fatalf("want runtime email after refresh, has=%v email=%q", hasSupport(), supportEmail())
	}
	if !a.snapshot().Support {
		t.Fatal("snapshot.Support should be true after status fetch")
	}

	a.act("support", "claude")
	if v := a.getView(); v.Screen != "support" {
		t.Fatalf("runtime email alone should open the support page: %+v", v)
	}
}

// U2: failed /api/status keeps build-time fallback (no false runtime set).
func TestFetchSiteStatusFailureKeepsFallback(t *testing.T) {
	t.Cleanup(resetRuntimeSupportEmail)
	resetRuntimeSupportEmail()
	prev := shared.Support.Email
	shared.Support.Email = "fallback@example.com"
	t.Cleanup(func() { shared.Support.Email = prev })

	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})
	gw := httptest.NewServer(mux)
	defer gw.Close()

	a, _ := testApp(t, gw)
	a.fetchSiteStatus()
	if !hasSupport() || supportEmail() != "fallback@example.com" {
		t.Fatalf("want build-time fallback after failed fetch, has=%v email=%q", hasSupport(), supportEmail())
	}
}
