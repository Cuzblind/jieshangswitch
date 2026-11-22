package main

import (
	"bytes"
	"encoding/json"
	rt "jieshangai/client/runtime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var version = "0.1.0-beta"
var defaultOrigin = "https://jieshangai.com"

const lockPort = "47391"

type App = rt.App

func lockFile() string               { return rt.LockFile() }
func openTarget(target string) error { return rt.OpenTarget(target) }
func deepLinkFromArgs(args []string) string {
	for _, arg := range args {
		if strings.HasPrefix(strings.ToLower(arg), "jieshangai:") {
			return arg
		}
	}
	return ""
}

//go:generate go -C ../runtime generate ./...

func main() {
	rt.Configure(version, defaultOrigin)
	link := deepLinkFromArgs(os.Args[1:])
	ln, err := net.Listen("tcp", "127.0.0.1:"+lockPort)
	if err != nil {
		forward(link)
		return
	}
	platformPrepare() // per-user install + jieshangai:// registration (never blocks)
	app := rt.New(rt.BaseEnv())
	_ = os.MkdirAll(filepath.Dir(lockFile()), 0o700)
	_ = os.WriteFile(lockFile(), []byte(app.LegacySecret()), 0o600)
	go func() { _ = (&http.Server{Handler: app.Routes(), ReadHeaderTimeout: 5 * time.Second}).Serve(ln) }()
	go app.Refresh()
	if link != "" {
		app.HandleURL(link)
	}
	platformRun(app, "http://127.0.0.1:"+lockPort+"/?s="+app.LegacySecret())
}

// forward hands a deep link to the running instance and brings it forward.
func forward(link string) {
	secret, err := os.ReadFile(lockFile())
	if err != nil {
		return
	}
	b, _ := json.Marshal(map[string]string{"url": link})
	req, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1:"+lockPort+"/api/forward", bytes.NewReader(b))
	req.Header.Set("X-S", strings.TrimSpace(string(secret)))
	req.Header.Set("Content-Type", "application/json")
	if resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req); err == nil {
		resp.Body.Close()
	}
}
