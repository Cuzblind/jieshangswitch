package clientruntime

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testDesktopApp(t *testing.T, gw *httptest.Server) (*App, string) {
	t.Helper()
	old, home := testApp(t, gw)
	return NewDesktop(old.env, "0.2.0-beta.1"), home
}

func TestDesktopFixedOriginAndRedirects(t *testing.T) {
	t.Setenv("JIESHANGAI_ORIGIN", "http://127.0.0.1:9999")
	t.Setenv("JIESHANGAI_WEB", "https://evil.example")
	env, err := DesktopEnv()
	if err != nil || env.Origin != ProductionOrigin || env.Web != ProductionOrigin {
		t.Fatal("runtime override escaped desktop origin policy")
	}
	if err := env.Write("claude", "https://evil.example", fakeKey); err == nil {
		t.Fatal("foreign configuration accepted")
	}
	for _, target := range []string{"https://jieshangai.com.evil.example/path", "http://jieshangai.com/path", "https://evil.example/path"} {
		r, _ := http.NewRequest(http.MethodGet, target, nil)
		if env.HTTP.CheckRedirect(r, nil) == nil {
			t.Fatal("foreign redirect accepted")
		}
	}
}

func TestDesktopRepairAndGlobalRestoreMigrateExistingFiles(t *testing.T) {
	g := &fakeGW{models: 200}
	gw := httptest.NewServer(g.handler())
	defer gw.Close()
	a, home := testDesktopApp(t, gw)
	claude := filepath.Join(home, ".claude", "settings.json")
	original := []byte("{\"theme\":\"dark\",\"env\":{\"KEEP_ME\":\"yes\"}}\n")
	_ = os.MkdirAll(filepath.Dir(claude), 0700)
	_ = os.WriteFile(claude, original, 0600)
	if err := a.DesktopAction(DesktopRequest{Action: "connect-ticket", Agent: "claude", Ticket: "goodticket123"}); err != nil {
		t.Fatal(err)
	}
	if v := a.getView(); v.Screen != "home" || v.Notice == "" {
		t.Fatal(a.getView())
	}
	// A repeat link must not consume another ticket or rewrite the backup.
	if err := a.DesktopAction(DesktopRequest{Action: "connect-ticket", Agent: "claude", Ticket: "goodticket123"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(claude)
	data = bytes.ReplaceAll(data, []byte(gw.URL), []byte("https://old.invalid"))
	_ = os.WriteFile(claude, data, 0600)
	if err := a.DesktopAction(DesktopRequest{Action: "repair", Agent: "claude"}); err != nil {
		t.Fatal(err)
	}
	if secret, ok := configuredSecret("claude", gw.URL); !ok || secret != fakeKey {
		t.Fatal("repair did not rewrite the managed connection")
	}
	if err := a.env.Write("codex", gw.URL, fakeKey); err != nil {
		t.Fatal(err)
	}
	if err := a.DesktopAction(DesktopRequest{Action: "restore-confirm", Confirm: true}); err == nil {
		t.Fatal("restore bypassed confirmation")
	}
	if err := a.DesktopAction(DesktopRequest{Action: "restore"}); err != nil {
		t.Fatal(err)
	}
	if err := a.DesktopAction(DesktopRequest{Action: "restore-confirm", Confirm: true}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(claude)
	if !bytes.Equal(got, original) {
		t.Fatal("original backup was overwritten or not restored")
	}
	if _, ok := configuredSecret("codex", gw.URL); ok {
		t.Fatal("Codex was not restored with Claude")
	}
	if !a.hasClientAuth() {
		t.Fatal("local restore unexpectedly revoked desktop authorization")
	}
	raw, _ := json.Marshal(a.DesktopState())
	if bytes.Contains(raw, []byte(fakeKey)) || bytes.Contains(raw, []byte(fakeClientAuth)) {
		t.Fatal("credential leaked into desktop snapshot")
	}
}

func TestDesktopRepairOfflineKeepsAuthAndDoesNotRotateToken(t *testing.T) {
	g := &fakeGW{models: 502}
	gw := httptest.NewServer(g.handler())
	defer gw.Close()
	a, _ := testDesktopApp(t, gw)
	a.saveClientAuth(fakeClientAuth)
	if err := a.env.Write("claude", gw.URL, fakeKey); err != nil {
		t.Fatal(err)
	}
	if err := a.DesktopAction(DesktopRequest{Action: "repair", Agent: "claude"}); err != nil {
		t.Fatal(err)
	}
	if !a.hasClientAuth() {
		t.Fatal("network error cleared auth")
	}
	g.mu.Lock()
	calls := g.clientCalls
	g.mu.Unlock()
	if calls != 0 {
		t.Fatal("network error rotated connection")
	}
}

func TestDesktopRefreshRechecksRecoveredConnection(t *testing.T) {
	g := &fakeGW{models: 200}
	base := g.handler()
	var healthy atomic.Bool
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			if healthy.Load() {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"claude-sonnet-4-5"}]}`))
			} else {
				w.WriteHeader(http.StatusBadGateway)
			}
			return
		}
		base.ServeHTTP(w, r)
	}))
	defer gw.Close()
	a, _ := testDesktopApp(t, gw)
	a.desktop = true
	if err := a.env.Write("claude", gw.URL, fakeKey); err != nil {
		t.Fatal(err)
	}
	_ = a.DesktopAction(DesktopRequest{Action: "check", Agent: "claude"})
	if a.DesktopState().Statuses[0].State != "offline" {
		t.Fatal("failed check was lost")
	}
	healthy.Store(true)
	if err := a.DesktopAction(DesktopRequest{Action: "refresh"}); err != nil {
		t.Fatal(err)
	}
	if a.DesktopState().Statuses[0].State != "ok" {
		t.Fatal("refresh kept a stale failed check")
	}
}

func TestDesktopLaunchAndInstallDoNotReportFalseSuccess(t *testing.T) {
	g := &fakeGW{models: 200}
	gw := httptest.NewServer(g.handler())
	defer gw.Close()
	a, _ := testDesktopApp(t, gw)
	a.env.FindCLI = func(string) bool { return false }
	if err := a.DesktopAction(DesktopRequest{Action: "install", Agent: "claude"}); err != nil {
		t.Fatal(err)
	}
	if a.getView().Screen != "cli-missing" {
		t.Fatal("missing CLI reported installed")
	}
	a.env.FindCLI = func(string) bool { return true }
	_ = a.env.Write("claude", gw.URL, fakeKey)
	a.env.Launch = func(string, string) error { return errors.New("raw launch error " + fakeKey) }
	err := a.DesktopAction(DesktopRequest{Action: "launch", Agent: "claude"})
	if err == nil || strings.Contains(err.Error(), fakeKey) {
		t.Fatal("launch failure lost or leaked")
	}
}

func TestDesktopRepairDoesNotKillRunningTasks(t *testing.T) {
	g := &fakeGW{models: 200}
	gw := httptest.NewServer(g.handler())
	defer gw.Close()
	a, _ := testDesktopApp(t, gw)
	_ = a.env.Write("claude", gw.URL, fakeKey)
	a.env.Running = func(string) bool { return true }
	a.env.Kill = func(string) error { t.Error("repair killed a user task"); return nil }
	if err := a.DesktopAction(DesktopRequest{Action: "repair", Agent: "claude"}); err != nil {
		t.Fatal(err)
	}
	if v := a.getView(); v.Screen != "home" || v.Notice == "" {
		t.Fatal(a.getView())
	}
}

func TestDesktopLogoutRequiresTheDisplayedConfirmation(t *testing.T) {
	g := &fakeGW{models: 200}
	gw := httptest.NewServer(g.handler())
	defer gw.Close()
	a, _ := testDesktopApp(t, gw)
	a.env.OfficialLogin = func(string) bool { return true }
	logouts := 0
	a.env.Logout = func(string) error { logouts++; return nil }
	request := DesktopRequest{Action: "logout-confirm", Agent: "claude", Confirm: true}
	if err := a.DesktopAction(request); err != nil {
		t.Fatal(err)
	}
	if logouts != 0 || a.getView().Action != "logout-confirm" {
		t.Fatal("official logout bypassed its confirmation")
	}
	if err := a.DesktopAction(request); err != nil {
		t.Fatal(err)
	}
	if logouts != 1 {
		t.Fatal("confirmed official logout did not run")
	}
}

func TestDesktopProtocolRejectsUnknownFieldsAndKeepsStateReadable(t *testing.T) {
	g := &fakeGW{models: 200}
	gw := httptest.NewServer(g.handler())
	defer gw.Close()
	a, _ := testDesktopApp(t, gw)
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- a.ServeDesktop(inR, outW) }()
	lines := make(chan desktopReply, 16)
	go func() {
		s := bufio.NewScanner(outR)
		for s.Scan() {
			var r desktopReply
			_ = json.Unmarshal(s.Bytes(), &r)
			lines <- r
		}
	}()
	defer outR.Close()
	defer inR.Close()
	defer outW.Close()
	_, _ = io.WriteString(inW, "{\"id\":\"bad\",\"action\":\"connect\",\"agent\":\"claude\",\"origin\":\"https://evil.example\"}\n{\"id\":\"state-1\",\"action\":\"state\"}\n")
	seenError, seenState := false, false
	deadline := time.After(3 * time.Second)
	for !seenError || !seenState {
		select {
		case r := <-lines:
			if r.Error != "" {
				seenError = true
			}
			if r.ID == "state-1" && r.State != nil {
				seenState = true
			}
		case <-deadline:
			t.Fatal("protocol stalled")
		}
	}
	_ = inW.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("engine ignored EOF")
	}
}

func TestRestoreAllClearsConnectionForUninstall(t *testing.T) {
	g := &fakeGW{models: 200}
	gw := httptest.NewServer(g.handler())
	defer gw.Close()
	a, _ := testDesktopApp(t, gw)
	if err := a.env.Write("claude", gw.URL, fakeKey); err != nil {
		t.Fatal(err)
	}
	if err := a.restoreAll(); err != nil {
		t.Fatal(err)
	}
	if _, ok := configuredSecret("claude", gw.URL); ok {
		t.Fatal("uninstall restore left the connection behind")
	}
}

// Updates reinstall through the same uninstaller, so the hook must leave a
// user's configuration alone unless they are really uninstalling and agreed.
func TestUninstallHookOnlyRestoresOnConfirmedUninstall(t *testing.T) {
	conf, err := os.ReadFile(filepath.Join("..", "desktop", "src-tauri", "tauri.conf.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(conf), "\"installerHooks\": \"windows/hooks.nsh\"") {
		t.Fatal("uninstall hook is not wired into the installer")
	}
	hook, err := os.ReadFile(filepath.Join("..", "desktop", "src-tauri", "windows", "hooks.nsh"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(hook, []byte{0xEF, 0xBB, 0xBF}) {
		t.Fatal("NSIS reads a BOM-less include as ANSI, garbling the Chinese prompt")
	}
	for _, need := range []string{"NSIS_HOOK_PREUNINSTALL", "$UpdateMode <> 1", "$PassiveMode <> 1", "IfSilent", "MB_YESNO", "/SD IDNO", "--restore-on-uninstall"} {
		if !strings.Contains(string(hook), need) {
			t.Fatalf("hook missing %q", need)
		}
	}
}

// A restore script can exit 0 without having removed the connection; both
// entry points must then report failure instead of telling the user it is done.
func TestRestoreFailsWhenConnectionRemains(t *testing.T) {
	g := &fakeGW{models: 200}
	gw := httptest.NewServer(g.handler())
	defer gw.Close()
	noop := func(bool) (bool, error) { return true, nil }

	a, _ := testDesktopApp(t, gw)
	if err := a.env.Write("claude", gw.URL, fakeKey); err != nil {
		t.Fatal(err)
	}
	a.env.Restore = noop
	if err := a.DesktopAction(DesktopRequest{Action: "restore"}); err != nil {
		t.Fatal(err)
	}
	if err := a.DesktopAction(DesktopRequest{Action: "restore-confirm", Confirm: true}); err == nil {
		t.Fatal("desktop restore reported success while the connection remains")
	}
	if v := a.getView(); v.Screen == "done" {
		t.Fatalf("desktop view after failed restore: %+v", v)
	}

	legacy, _ := testApp(t, gw)
	if err := legacy.env.Write("claude", gw.URL, fakeKey); err != nil {
		t.Fatal(err)
	}
	legacy.env.Restore = noop
	legacy.act("restore", "claude")
	legacy.act("restore-yes", "claude")
	if v := waitIdle(t, legacy); v.Screen == "done" {
		t.Fatalf("legacy restore reported success while the connection remains: %+v", v)
	}
}
