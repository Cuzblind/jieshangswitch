//go:build windows

package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jchv/go-webview2"
	"golang.org/x/sys/windows/registry"
)

func installDir() string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "jieshangai")
}

// platformPrepare: per-user install (no admin) like cc-switch's NSIS
// currentUser mode, then register jieshangai:// under HKCU\Software\Classes.
// Any failure is ignored: the web keeps the command path as the fallback (M1).
func platformPrepare() {
	self, err := os.Executable()
	if err != nil {
		return
	}
	target := filepath.Join(installDir(), "接上AI.exe")
	if !strings.EqualFold(filepath.Clean(self), filepath.Clean(target)) {
		if err := copySelf(self, target); err == nil {
			shortcuts(target)
		} else {
			target = self
		}
	}
	registerScheme(target)
}

func copySelf(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".new"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	out.Close()
	_ = os.Remove(dst)
	return os.Rename(tmp, dst)
}

func shortcuts(target string) {
	ps := `$s=New-Object -ComObject WScript.Shell;` +
		`foreach($d in @([Environment]::GetFolderPath('Programs'),[Environment]::GetFolderPath('Desktop'))){` +
		`$l=$s.CreateShortcut((Join-Path $d '接上AI.lnk'));$l.TargetPath='` + strings.ReplaceAll(target, "'", "''") + `';$l.Save()}`
	_, _ = runPS1([]byte(ps))
}

func registerScheme(exe string) {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Classes\jieshangai`, registry.SET_VALUE)
	if err != nil {
		return
	}
	_ = k.SetStringValue("", "URL:接上AI")
	_ = k.SetStringValue("URL Protocol", "")
	k.Close()
	c, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Classes\jieshangai\shell\open\command`, registry.SET_VALUE)
	if err != nil {
		return
	}
	_ = c.SetStringValue("", `"`+exe+`" "%1"`)
	c.Close()
}

func platformRun(app *App, uiURL string) {
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     false,
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title: "接上AI", Width: 560, Height: 760, Center: true,
		},
	})
	if w == nil { // no WebView2 runtime: same page in the default browser
		app.SetOpener(func() { _ = openTarget(uiURL) })
		app.OpenWindow()
		select {}
	}
	app.SetOpener(func() {
		w.Dispatch(func() { w.Navigate(uiURL) })
	})
	w.Navigate(uiURL)
	w.Run()
	os.Exit(0)
}
