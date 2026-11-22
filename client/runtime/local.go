package clientruntime

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"jieshangai/client/core"
)

// Local file facts shared by every OS. Field paths come from install/ only.

func homeDir() string {
	if h := os.Getenv("RELAY_HOME"); h != "" { // same override as /i and /u (tests)
		return h
	}
	h, _ := os.UserHomeDir()
	return h
}

func paths() core.Paths { return core.Paths{Root: homeDir()} }

var reCodexEnv = regexp.MustCompile(`(?m)^export ` + core.CodexEnvName + `='([^'\n]*)'`)

// configuredSecret reports whether our connection for origin is written, and
// returns it for the light check / balance. The value never reaches the UI.
func configuredSecret(agent, origin string) (string, bool) {
	p := paths()
	switch agent {
	case "claude":
		b, err := os.ReadFile(p.ClaudeSettings())
		if err != nil {
			return "", false
		}
		var v struct {
			Env map[string]any `json:"env"`
		}
		if json.Unmarshal(b, &v) != nil {
			return "", false
		}
		base, _ := v.Env["ANTHROPIC_BASE_URL"].(string)
		tok, _ := v.Env["ANTHROPIC_AUTH_TOKEN"].(string)
		if strings.TrimSuffix(base, "/") != origin || tok == "" {
			return "", false
		}
		return tok, true
	case "codex":
		cfg, err := os.ReadFile(p.CodexConfig())
		if err != nil || !strings.Contains(string(cfg), core.MarkerStart) || !strings.Contains(string(cfg), origin+"/v1") {
			return "", false
		}
		env, err := os.ReadFile(p.CodexEnv())
		if err != nil {
			return "", false
		}
		m := reCodexEnv.FindStringSubmatch(string(env))
		if m == nil || m[1] == "" {
			return "", false
		}
		return m[1], true
	}
	return "", false
}

func editedSinceWrite() bool {
	e, err := paths().EditedSinceWrite()
	return err == nil && len(e) > 0
}

// ---------- CLI detection (M7): PATH + the dirs /i refreshes ----------

func cliDirs() []string {
	h := homeDir()
	if runtime.GOOS == "windows" {
		return []string{
			filepath.Join(toolRoot(), "bin"),
			filepath.Join(h, ".local", "bin"),
			filepath.Join(os.Getenv("APPDATA"), "npm"),
			filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "claude"),
			filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "codex"),
		}
	}
	return []string{filepath.Join(toolRoot(), "bin"), filepath.Join(h, ".local", "bin"), "/opt/homebrew/bin", "/usr/local/bin",
		filepath.Join(h, ".npm-global", "bin"), filepath.Join(h, ".codex", "bin")}
}

// augmentedPath is PATH plus the install dirs (GUI apps on macOS start with a
// minimal PATH).
func augmentedPath() string {
	parts := append([]string{filepath.Join(toolRoot(), "bin")}, filepath.SplitList(os.Getenv("PATH"))...)
	seen := map[string]bool{}
	for _, p := range parts {
		seen[p] = true
	}
	for _, d := range cliDirs() {
		if d != "" && !seen[d] {
			parts = append(parts, d)
		}
	}
	return strings.Join(parts, string(os.PathListSeparator))
}

func findCLI(agent string) bool {
	if os.Getenv("JIESHANGAI_FAKE_CLI") == "1" {
		return true
	}
	return findInstalledCLI(agent)
}

func findInstalledCLI(agent string) bool {
	if !validAgent(agent) {
		return false
	}
	name := agent
	if _, err := exec.LookPath(name); err == nil {
		return true
	}
	exts := []string{""}
	if runtime.GOOS == "windows" {
		exts = []string{".exe", ".cmd", ".bat", ".ps1"}
	}
	for _, d := range cliDirs() {
		for _, e := range exts {
			if st, err := os.Stat(filepath.Join(d, name+e)); err == nil && !st.IsDir() {
				return true
			}
		}
	}
	return false
}

const (
	claudeInstallSH  = "https://claude.ai/install.sh"
	codexInstallSH   = "https://chatgpt.com/codex/install.sh"
	claudeInstallPS1 = "https://claude.ai/install.ps1"
	codexInstallPS1  = "https://chatgpt.com/codex/install.ps1"
)

func cmdEnv(extra ...string) []string {
	env := []string{}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "PATH=") && !strings.HasPrefix(kv, "Path=") && !strings.HasPrefix(kv, "PSModulePath=") {
			env = append(env, kv)
		}
	}
	env = append(env, "PATH="+augmentedPath())
	// Drop PSModulePath entirely: a value exported by a PowerShell 7 parent
	// (or a msys shell) breaks module auto-loading in the Windows PowerShell
	// 5.1 children — Get-FileHash, Add-AppxPackage, … become CommandNotFound.
	// With the variable absent, powershell.exe rebuilds its own default paths.
	return append(env, extra...)
}

// ---------- official login conflict (memo #4) ----------

func claudeCredFile() string { return filepath.Join(homeDir(), ".claude", ".credentials.json") }

func claudeFileLogin() bool {
	_, err := os.Stat(claudeCredFile())
	return err == nil
}
