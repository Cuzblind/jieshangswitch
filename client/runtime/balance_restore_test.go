package clientruntime

import (
	"net/http/httptest"
	"testing"
)

// Restore removes the connection keys but keeps the desktop authorization, so
// the home screen must keep showing the account balance.
func TestBalanceStaysAfterRestoreWithClientAuth(t *testing.T) {
	g := &fakeGW{models: 200}
	gw := httptest.NewServer(g.handler())
	defer gw.Close()
	a, _ := testDesktopApp(t, gw)
	if err := a.DesktopAction(DesktopRequest{Action: "connect-ticket", Agent: "claude", Ticket: "goodticket123"}); err != nil {
		t.Fatal(err)
	}
	if !a.hasClientAuth() {
		t.Fatal("connect did not leave a desktop authorization")
	}
	if err := a.DesktopAction(DesktopRequest{Action: "restore"}); err != nil {
		t.Fatal(err)
	}
	if err := a.DesktopAction(DesktopRequest{Action: "restore-confirm", Confirm: true}); err != nil {
		t.Fatal(err)
	}
	if a.DesktopState().Balance == nil {
		t.Fatal("balance disappeared after restore")
	}
}
