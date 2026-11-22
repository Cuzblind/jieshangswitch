package clientruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type storedClientAuth struct {
	Origin string `json:"origin"`
	Auth   string `json:"auth"`
}

func clientConfigDir() string {
	return filepath.Dir(lockFile())
}

func clientAuthPath(origin string) string {
	sum := sha256.Sum256([]byte(strings.TrimSuffix(origin, "/")))
	return filepath.Join(clientConfigDir(), "auth", hex.EncodeToString(sum[:]))
}

func loadClientAuth(origin string) string {
	origin = strings.TrimSuffix(origin, "/")
	if origin == "" {
		return ""
	}
	b, err := os.ReadFile(clientAuthPath(origin))
	if err != nil {
		return ""
	}
	var rec storedClientAuth
	if json.Unmarshal(b, &rec) != nil {
		return ""
	}
	if rec.Origin != origin || rec.Auth == "" {
		return ""
	}
	return rec.Auth
}

func writeClientAuth(origin, auth string) error {
	origin = strings.TrimSuffix(origin, "/")
	auth = strings.TrimSpace(auth)
	if origin == "" || auth == "" {
		return nil
	}
	path := clientAuthPath(origin)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(storedClientAuth{Origin: origin, Auth: auth})
	if err != nil {
		return err
	}
	return writePrivateFile(path, b)
}

func removeClientAuth(origin string) error {
	origin = strings.TrimSuffix(origin, "/")
	if origin == "" {
		return nil
	}
	err := os.Remove(clientAuthPath(origin))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (a *App) clientAuthLocked() string {
	if !a.clientAuthCached {
		a.clientAuthVal = loadClientAuth(a.origin)
		a.clientAuthCached = true
	}
	return a.clientAuthVal
}

func (a *App) hasClientAuth() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.clientAuthLocked() != ""
}

func (a *App) clientAuth() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.clientAuthLocked()
}

func (a *App) saveClientAuth(auth string) {
	auth = strings.TrimSpace(auth)
	if auth == "" {
		return
	}
	if err := writeClientAuth(a.origin, auth); err != nil {
		return
	}
	a.mu.Lock()
	a.clientAuthVal = auth
	a.clientAuthCached = true
	a.mu.Unlock()
}

func (a *App) clearClientAuth() {
	_ = removeClientAuth(a.origin)
	a.mu.Lock()
	a.clientAuthVal = ""
	a.clientAuthCached = true
	a.mu.Unlock()
}
