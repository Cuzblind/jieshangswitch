package clientruntime

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestDesktopPipeReservesReadsBeforeConnectionIO(t *testing.T) {
	g := &fakeGW{models: 200}
	gw := httptest.NewServer(g.handler())
	defer gw.Close()
	a, _ := testDesktopApp(t, gw)
	releaseRefresh := make(chan struct{})
	a.env.InspectTools = func() { <-releaseRefresh }
	a.connectionMu.Lock()
	var unlock sync.Once
	releaseConnection := func() { unlock.Do(a.connectionMu.Unlock) }
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	served := make(chan struct{})
	go func() { _ = a.ServeDesktop(inR, outW); close(served) }()
	replies := make(chan desktopReply, 16)
	go func() {
		scanner := bufio.NewScanner(outR)
		for scanner.Scan() {
			var reply desktopReply
			if json.Unmarshal(scanner.Bytes(), &reply) == nil && reply.ID != "" {
				replies <- reply
			}
		}
	}()
	defer func() {
		releaseConnection()
		close(releaseRefresh)
		_ = inW.Close()
		waitDesktopRead(t, served)
		_ = outR.Close()
		_ = outW.Close()
	}()
	go func() {
		_, _ = io.WriteString(inW, `{"id":"u1","action":"usage-load","range":"24h"}
{"id":"u2","action":"usage-load","range":"7d"}
{"id":"c1","action":"catalog-load","agent":"claude"}
{"id":"c2","action":"catalog-load","agent":"claude"}
{"id":"invalid","action":"catalog-load","agent":"other"}
{"id":"state","action":"state"}
`)
	}()
	seen := make(map[string]desktopReply)
	for seen["state"].ID == "" {
		select {
		case reply := <-replies:
			seen[reply.ID] = reply
		case <-time.After(3 * time.Second):
			t.Fatal("connection I/O blocked the input scanner")
		}
	}
	a.mu.Lock()
	usage, catalog, invalid := a.usageRequest, a.catalogRequests["claude"], a.catalogRequests["other"]
	a.mu.Unlock()
	if usage != 2 || catalog != 2 || invalid != 0 {
		t.Errorf("reads were not reserved in pipe order before starting: usage=%d catalog=%d invalid=%d", usage, catalog, invalid)
	}
	releaseConnection()
	for len(seen) < 6 {
		select {
		case reply := <-replies:
			seen[reply.ID] = reply
		case <-time.After(3 * time.Second):
			t.Fatal("reserved reads did not finish")
		}
	}
	if seen["invalid"].Error == "" {
		t.Fatal("invalid catalog agent was accepted")
	}
}

// Opening the MCP page used to wait behind the startup refresh, which runs
// both assistants' checks; list reads must answer while it is still running.
func TestListReadsDoNotWaitBehindRefresh(t *testing.T) {
	for _, phase := range []string{"inspection", "cold-cli-probe"} {
		t.Run(phase, func(t *testing.T) { testListReadsDuringRefresh(t, phase) })
	}
}

func testListReadsDuringRefresh(t *testing.T, phase string) {
	g := &fakeGW{models: 200}
	gw := httptest.NewServer(g.handler())
	defer gw.Close()
	a, _ := testDesktopApp(t, gw)
	release := make(chan struct{})
	if phase == "inspection" {
		a.env.InspectTools = func() { <-release }
	} else {
		a.env.ToolSnapshot = func() []ToolStatus { return []ToolStatus{{Agent: "claude"}, {Agent: "codex"}} }
		a.env.FindCLI = func(string) bool { <-release; return true }
	}
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
	sendErrors := make(chan error, 1)
	go func() {
		for _, req := range []string{
			`{"id":"state","action":"state"}`,
			`{"id":"mcp","action":"mcp-list","agent":"claude"}`,
			`{"id":"skills","action":"skills-list","agent":"claude"}`,
			`{"id":"prompts","action":"prompts-list","agent":"codex"}`,
		} {
			if _, err := io.WriteString(inW, req+"\n"); err != nil {
				sendErrors <- err
				return
			}
		}
	}()
	want := map[string]bool{"state|": true, "mcp|": true, "skills|": true, "prompts|": true}
	deadline := time.After(3 * time.Second)
	for len(want) > 0 {
		select {
		case got := <-replies:
			delete(want, got)
		case err := <-sendErrors:
			t.Fatal(err)
		case <-deadline:
			t.Fatalf("list reads still waiting behind the refresh: %v", want)
		}
	}
}
