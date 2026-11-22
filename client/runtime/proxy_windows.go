//go:build windows

package clientruntime

import (
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows/registry"
)

type winProxyCache struct {
	mu        sync.Mutex
	checkedAt time.Time
	enabled   bool
	server    string
	override  string
}

var sysProxy winProxyCache

func readWindowsSystemProxy() (bool, string, string) {
	sysProxy.mu.Lock()
	defer sysProxy.mu.Unlock()
	if time.Since(sysProxy.checkedAt) < 30*time.Second {
		return sysProxy.enabled, sysProxy.server, sysProxy.override
	}
	sysProxy.checkedAt = time.Now()
	sysProxy.enabled, sysProxy.server, sysProxy.override = false, "", ""
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE)
	if err != nil {
		return false, "", ""
	}
	defer k.Close()
	en, _, err := k.GetIntegerValue("ProxyEnable")
	if err != nil || en == 0 {
		return false, "", ""
	}
	srv, _, err := k.GetStringValue("ProxyServer")
	if err != nil || strings.TrimSpace(srv) == "" {
		return false, "", ""
	}
	ovr, _, _ := k.GetStringValue("ProxyOverride")
	sysProxy.enabled = true
	sysProxy.server = strings.TrimSpace(srv)
	sysProxy.override = strings.TrimSpace(ovr)
	return sysProxy.enabled, sysProxy.server, sysProxy.override
}

func proxyBypassed(host, override string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "" || h == "localhost" || h == "127.0.0.1" || h == "::1" {
		return true
	}
	for _, item := range strings.Split(override, ";") {
		p := strings.ToLower(strings.TrimSpace(item))
		if p == "" {
			continue
		}
		if p == "<local>" && !strings.Contains(h, ".") {
			return true
		}
		if p == h {
			return true
		}
		if strings.HasPrefix(p, "*.") && strings.HasSuffix(h, p[1:]) {
			return true
		}
		if strings.HasPrefix(p, "*") && strings.HasSuffix(h, p[1:]) {
			return true
		}
	}
	return false
}

func parseWindowsProxyForScheme(server, scheme string) string {
	if !strings.Contains(server, "=") {
		return server
	}
	want := strings.ToLower(scheme)
	var fallback string
	for _, part := range strings.Split(server, ";") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		k := strings.ToLower(strings.TrimSpace(kv[0]))
		v := strings.TrimSpace(kv[1])
		if v == "" {
			continue
		}
		if k == want {
			return v
		}
		if k == "http" && fallback == "" {
			fallback = v
		}
	}
	return fallback
}

// systemAwareProxy checks process HTTP(S)_PROXY env vars first, then falls back
// to the Windows user system proxy (Clash / v2rayN system proxy mode).
func systemAwareProxy(req *http.Request) (*url.URL, error) {
	if u, err := http.ProxyFromEnvironment(req); err != nil || u != nil {
		return u, err
	}
	if req == nil || req.URL == nil {
		return nil, nil
	}
	enabled, server, override := readWindowsSystemProxy()
	if !enabled || server == "" || proxyBypassed(req.URL.Hostname(), override) {
		return nil, nil
	}
	raw := parseWindowsProxyForScheme(server, req.URL.Scheme)
	if raw == "" {
		return nil, nil
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, nil
	}
	return u, nil
}
