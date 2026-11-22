package clientruntime

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"
	"time"
)

// Opening the MCP page used to wait behind the startup refresh, which runs
// both assistants' checks; list reads must answer while it is still running.
func TestListReadsDoNotWaitBehindRefresh(t *testing.T) {
	g := &fakeGW{models: 200}
	gw := httptest.NewServer(g.handler())
	defer gw.Close()
	a, _ := testDesktopApp(t, gw)
	release := make(chan struct{})
	a.env.InspectTools = func() { <-release }
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	served := make(chan struct{})
	go func() { _ = a.ServeDesktop(inR, outW); _ = outW.Close(); close(served) }()
	replies := make(chan string, 64)
	go func() {
		scanner := bufio.NewScanner(outR)
		scanner.Buffer(make([]byte, 64<<10), 4<<20)
		for scanner.Scan() {
			var r desktopReply
			if json.Unmarshal(scanner.Bytes(), &r) == nil && r.ID != "" {
				replies <- r.ID + "|" + r.Error
			}
		}
	}()
	defer func() {
		close(release)
		_ = inW.Close()
		select {
		case <-served:
		case <-time.After(10 * time.Second):
		}
	}()
	for _, req := range []string{
		`{"id":"mcp","action":"mcp-list","agent":"claude"}`,
		`{"id":"skills","action":"skills-list","agent":"claude"}`,
		`{"id":"prompts","action":"prompts-list","agent":"codex"}`,
	} {
		if _, err := io.WriteString(inW, req+"\n"); err != nil {
			t.Fatal(err)
		}
	}
	want := map[string]bool{"mcp|": true, "skills|": true, "prompts|": true}
	deadline := time.After(3 * time.Second)
	for len(want) > 0 {
		select {
		case got := <-replies:
			delete(want, got)
		case <-deadline:
			t.Fatalf("list reads still waiting behind the refresh: %v", want)
		}
	}
}
