package clientruntime

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// thisDevice is what a claim tells the gateway about this computer, so each
// computer keeps its own connection and can be stopped on its own from the
// website. The id is random, made once and kept in the client's config folder;
// it is not a hardware fingerprint. nil when the id cannot be kept (the claim
// then works like an older client's).
func (a *App) thisDevice() map[string]string {
	id := deviceID()
	if id == "" {
		return nil
	}
	return map[string]string{"id": id, "name": computerName(), "os": deviceOS(a.env.GOOS)}
}

func deviceIDPath() string { return filepath.Join(clientConfigDir(), "device-id") }

func validDeviceID(id string) bool {
	if len(id) < 16 || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func deviceID() string {
	if b, err := os.ReadFile(deviceIDPath()); err == nil {
		if id := strings.TrimSpace(string(b)); validDeviceID(id) {
			return id
		}
	}
	id := randHex(16)
	if err := os.MkdirAll(clientConfigDir(), 0o700); err != nil {
		return ""
	}
	if err := writePrivateFile(deviceIDPath(), []byte(id)); err != nil {
		return ""
	}
	return id
}

// computerName is the name people see for this computer in system settings
// ("张三的MacBook Air"), falling back to the host name.
func computerName() string {
	if runtime.GOOS == "darwin" {
		if out, err := exec.Command("/usr/sbin/scutil", "--get", "ComputerName").Output(); err == nil {
			if name := strings.TrimSpace(string(out)); name != "" {
				return name
			}
		}
	}
	name, _ := os.Hostname()
	return strings.TrimSuffix(strings.TrimSpace(name), ".local")
}

func deviceOS(goos string) string {
	switch goos {
	case "darwin":
		return "mac"
	case "windows", "linux":
		return goos
	}
	return ""
}
