package clientruntime

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Successful desktop actions report on the home screen as a notice; a dialog
// that must be closed after every connect, check or launch was noise.
func TestDesktopSuccessIsANoticeOnHome(t *testing.T) {
	g := &fakeGW{models: 200}
	gw := httptest.NewServer(g.handler())
	defer gw.Close()
	a, _ := testDesktopApp(t, gw)
	expect := func(step, want string) {
		t.Helper()
		v := a.getView()
		if v.Screen != "home" || !strings.Contains(v.Notice, want) {
			t.Fatalf("%s: want home with notice %q, got %+v", step, want, v)
		}
	}
	if err := a.DesktopAction(DesktopRequest{Action: "connect-ticket", Agent: "claude", Ticket: "goodticket123"}); err != nil {
		t.Fatal(err)
	}
	expect("connect", "已经接上")
	if err := a.DesktopAction(DesktopRequest{Action: "check", Agent: "claude"}); err != nil {
		t.Fatal(err)
	}
	expect("check", "连接检查通过")
	if err := a.DesktopAction(DesktopRequest{Action: "launch", Agent: "claude"}); err != nil {
		t.Fatal(err)
	}
	expect("launch", "已请求打开")
	if err := a.DesktopAction(DesktopRequest{Action: "restore"}); err != nil {
		t.Fatal(err)
	}
	if v := a.getView(); v.Screen != "confirm" {
		t.Fatalf("restore must still ask first: %+v", v)
	}
	if err := a.DesktopAction(DesktopRequest{Action: "restore-confirm", Confirm: true}); err != nil {
		t.Fatal(err)
	}
	expect("restore", "已经恢复")
}

// A background refresh that finds an expired connection marks the card; it
// must not pop a dialog the user never asked for.
func TestDesktopBackgroundExpiryDoesNotPopDialog(t *testing.T) {
	g := &fakeGW{models: 200}
	gw := httptest.NewServer(g.handler())
	defer gw.Close()
	a, _ := testDesktopApp(t, gw)
	if err := a.env.Write("claude", gw.URL, fakeKey); err != nil {
		t.Fatal(err)
	}
	expired := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"success":false}`))
	}))
	defer expired.Close()
	a.origin = expired.URL
	if err := a.env.Write("claude", expired.URL, fakeKey); err != nil {
		t.Fatal(err)
	}
	a.setView(View{Screen: "home"})
	a.refreshStatus(true)
	if v := a.getView(); v.Screen != "home" {
		t.Fatalf("background refresh opened %+v", v)
	}
	for _, s := range a.DesktopState().Statuses {
		if s.Agent == "claude" && s.State != "expired" {
			t.Fatalf("expired connection not shown on the card: %+v", s)
		}
	}
}
