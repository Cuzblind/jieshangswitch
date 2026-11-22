//go:build windows

package clientruntime

import (
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func pathSeparator() rune { return ';' }

// samePathEntry compares PATH items ignoring case, spaces and trailing slashes,
// so entries written by install.ps1 and by this runtime match each other.
func samePathEntry(a, b string) bool {
	clean := func(s string) string { return strings.ToLower(strings.TrimRight(strings.TrimSpace(s), `\/`)) }
	return clean(a) == clean(b)
}

func ensureUserPath(dir string) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		key, _, err = registry.CreateKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE|registry.SET_VALUE)
	}
	if err != nil {
		return err
	}
	defer key.Close()
	value, _, err := key.GetStringValue("Path")
	if err != nil && err != registry.ErrNotExist {
		return err
	}
	parts := splitWindowsPath(value)
	for _, p := range parts {
		if samePathEntry(p, dir) {
			return broadcastEnvironment()
		}
	}
	if value != "" && !strings.HasSuffix(value, ";") {
		value += ";"
	}
	value += dir
	if err := key.SetExpandStringValue("Path", value); err != nil {
		return err
	}
	return broadcastEnvironment()
}

func removeUserPath(dir string) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		if err == registry.ErrNotExist {
			return nil
		}
		return err
	}
	defer key.Close()
	value, typ, err := key.GetStringValue("Path")
	if err != nil {
		if err == registry.ErrNotExist {
			return nil
		}
		return err
	}
	parts := splitWindowsPath(value)
	out := parts[:0]
	for _, p := range parts {
		if !samePathEntry(p, dir) {
			out = append(out, p)
		}
	}
	if len(out) == len(parts) {
		return nil
	}
	joined := strings.Join(out, ";")
	if typ == registry.EXPAND_SZ {
		err = key.SetExpandStringValue("Path", joined)
	} else {
		err = key.SetStringValue("Path", joined)
	}
	if err != nil {
		return err
	}
	return broadcastEnvironment()
}

func splitWindowsPath(value string) []string {
	var out []string
	for _, p := range strings.Split(value, ";") {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	return out
}

func broadcastEnvironment() error {
	user32 := windows.NewLazySystemDLL("user32.dll")
	send := user32.NewProc("SendMessageTimeoutW")
	env, _ := windows.UTF16PtrFromString("Environment")
	_, _, callErr := send.Call(uintptr(0xffff), 0x001A, 0, uintptr(unsafe.Pointer(env)), 0x0002, 5000, 0)
	return broadcastResult(callErr)
}

// broadcastResult decides whether a SendMessageTimeoutW outcome should fail
// the caller. It runs only after HKCU\Environment\Path has already been
// written — the persisted value is what matters — so the broadcast to running
// windows is best-effort refresh. ERROR_TIMEOUT (1460, the field-reported
// "This operation returned because the timeout period expired"; 258 /
// WAIT_TIMEOUT is the equivalent wait-code variant) means one hung top-level
// window missed the WM_SETTINGCHANGE budget; the message still reached the
// responsive windows, so it is not an install failure.
func broadcastResult(callErr error) error {
	switch callErr {
	case nil, syscall.Errno(0), windows.ERROR_TIMEOUT, windows.WAIT_TIMEOUT:
		return nil
	}
	return callErr
}
