package clientruntime

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func lockFile() string {
	d, err := os.UserConfigDir()
	if err != nil {
		d = os.TempDir()
	}
	return filepath.Join(d, "jieshangai-client", "instance")
}

// These overrides are retained for the legacy adapter only. Desktop builds
// use DesktopEnv, which never consults runtime origin overrides.
func siteOrigin() string {
	o := strings.TrimSuffix(strings.TrimSpace(os.Getenv("JIESHANGAI_ORIGIN")), "/")
	if strings.HasPrefix(o, "http://127.0.0.1:") || strings.HasPrefix(o, "http://localhost:") || o == "https://jieshangai.com" {
		return o
	}
	return defaultOrigin
}
func webOrigin() string {
	o := strings.TrimSuffix(strings.TrimSpace(os.Getenv("JIESHANGAI_WEB")), "/")
	if strings.HasPrefix(o, "http://127.0.0.1:") || strings.HasPrefix(o, "http://localhost:") {
		return o
	}
	return siteOrigin()
}
func goos() string { return runtime.GOOS }
func baseEnv() Env {
	return Env{
		GOOS: goos(), OSLabel: platformOSLabel(), Origin: siteOrigin(), Web: webOrigin(),
		HTTP:  &http.Client{Timeout: 20 * time.Second},
		Write: engineWrite, WriteStamp: engineStamp, Restore: engineRestore,
		Edited: editedSinceWrite, Configured: configuredSecret,
		FindCLI: findCLI, InstallCLI: installCLI, Running: running, Kill: kill, Launch: launch,
		OfficialLogin: officialLogin, Logout: logout, Open: openTarget, OpenDir: openDir,
	}
}
func Configure(v, origin string)     { version, defaultOrigin = v, origin }
func New(env Env) *App               { return newApp(env) }
func BaseEnv() Env                   { return baseEnv() }
func LockFile() string               { return lockFile() }
func OpenTarget(target string) error { return openTarget(target) }
func (a *App) LegacySecret() string  { return a.secret }
func (a *App) Routes() http.Handler  { return a.routes() }
func (a *App) Refresh()              { a.refreshStatus(true) }
func (a *App) CurrentView() View     { return a.getView() }
func (a *App) SetOpener(open func()) { a.opener = open }
func (a *App) OpenWindow() {
	if a.opener != nil {
		a.opener()
	}
}
