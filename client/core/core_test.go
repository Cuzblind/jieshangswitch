package core

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

const (
	testSecret = "sk-TESTSECRET0123456789abcdef"
	testOrigin = "https://relay.example.com"
)

var scriptDir = filepath.Join("..", "..", "install", "sh")

func seed(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func tree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel) // expectations key trees with forward slashes
		b, _ := os.ReadFile(p)
		perm := info.Mode().Perm().String()
		if runtime.GOOS == "windows" {
			// Windows files carry no unix permission bits: Go reports 0666 for
			// writable and 0444 for read-only. Normalize writable files to the
			// canonical 0644 rendering the expectations use; read-only stays
			// visible so a stray lock is still caught.
			if info.Mode().Perm()&0o222 != 0 {
				perm = "-rw-r--r--"
			} else {
				perm = "-r--r--r--"
			}
		}
		out[rel] = perm + "\n" + string(b)
		return nil
	})
	return out
}

func diffTrees(t *testing.T, label string, want, got map[string]string) {
	t.Helper()
	keys := map[string]bool{}
	for k := range want {
		keys[k] = true
	}
	for k := range got {
		keys[k] = true
	}
	var ks []string
	for k := range keys {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	for _, k := range ks {
		if want[k] != got[k] {
			t.Errorf("%s: %s differs\n--- script\n%q\n--- core\n%q", label, k, want[k], got[k])
		}
	}
}

func runScript(t *testing.T, name, root string, env ...string) string {
	t.Helper()
	out, err := runScriptErr(t, name, root, env...)
	if err != nil {
		t.Fatalf("%s: %v\n%s", name, err, out)
	}
	return out
}

// runScriptErr runs a script in its own session (no controlling tty), so a
// confirmation prompt reading /dev/tty fails like an unattended pipe.
func runScriptErr(t *testing.T, name, root string, env ...string) (string, error) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash missing")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 missing")
	}
	cmd := exec.Command("bash", filepath.Join(scriptDir, name))
	// Keep python3 from writing bytecode caches into the fake HOME (macOS
	// /usr/bin/python3 puts them under ~/Library/Caches/com.apple.python).
	cmd.Env = append([]string{"PATH=/usr/bin:/bin", "HOME=" + root, "RELAY_HOME=" + root, "PYTHONDONTWRITEBYTECODE=1"}, env...)
	isolateScript(t, cmd)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if strings.Contains(out.String(), testSecret) {
		t.Fatalf("%s printed the secret", name)
	}
	return out.String(), err
}

func lastLine(s string) string {
	// PowerShell on Windows emits CRLF; on the Linux dev host only LF.
	ls := strings.Split(strings.TrimRight(s, "\r\n"), "\n")
	return strings.TrimRight(ls[len(ls)-1], "\r")
}

func installEnv(agent, shell string) []string {
	return []string{"RELAY_AGENT=" + agent, "RELAY_TOKEN=" + testSecret, "RELAY_SKIP_CLI=1",
		"RELAY_SITE_ORIGIN=" + testOrigin, "SHELL=" + shell}
}

var fixtures = map[string]map[string]string{
	"empty": {},
	"existing": {
		".claude/settings.json": "{\"model\": \"opus\", \"env\": {\"FOO\": \"bar\", \"ANTHROPIC_API_KEY\": \"old\"}, \"permissions\": {\"allow\": [\"Bash(ls)\", \"中文\"]}, \"n\": 3, \"x\": null, \"b\": true, \"e\": {}, \"a\": []}",
		".codex/config.toml":    "model = \"o3\"\nmodel_provider = \"openai\"\n\n[profiles.x]\nfoo = 1\n\n\n",
		".bashrc":               "export PATH=$PATH:/opt/bin\nalias ll='ls -l'\n",
		".zshrc":                "# zsh\n",
	},
	"no-model": {
		".codex/config.toml":    "approval = \"never\"\n",
		".claude/settings.json": "",
	},
}

// Parity: core write == /i, core clear == /u (file contents + modes, incl. .bak).
func TestParityWithScripts(t *testing.T) {
	for name, fx := range fixtures {
		for _, agent := range []string{"claude", "codex"} {
			for _, shell := range []string{"/bin/bash", "/bin/zsh"} {
				label := name + "/" + agent + "/" + shell
				a, b := t.TempDir(), t.TempDir()
				seed(t, a, fx)
				seed(t, b, fx)
				runScript(t, "install.sh", a, installEnv(agent, shell)...)
				p := Paths{Root: b}
				var err error
				if agent == "claude" {
					err = p.WriteClaude(ClaudeOpts{Origin: testOrigin, Secret: testSecret})
				} else {
					err = p.WriteCodex(CodexOpts{Origin: testOrigin, Secret: testSecret, Shell: shell})
				}
				if err == nil {
					err = p.WriteStamp(agent, "skipped")
				}
				if err != nil {
					t.Fatalf("%s: %v", label, err)
				}
				diffTrees(t, label+" write", tree(t, a), tree(t, b))

				out := runScript(t, "uninstall.sh", a)
				r, err := p.Restore(RestoreOpts{})
				if err != nil {
					t.Fatal(err)
				}
				if lastLine(out) != r.Message() {
					t.Errorf("%s: /u said %q, core %q", label, lastLine(out), r.Message())
				}
				diffTrees(t, label+" restore", tree(t, a), tree(t, b))
				// pre-install state must come back exactly when every file had a backup or did not exist
				pre := t.TempDir()
				seed(t, pre, fx)
				if name == "existing" {
					diffTrees(t, label+" vs pre-install", tree(t, pre), tree(t, b))
				}
			}
		}
	}
}

func TestParityCodexModelAndRewrite(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	fx := fixtures["existing"]
	seed(t, a, fx)
	seed(t, b, fx)
	env := append(installEnv("codex", "/bin/bash"), "RELAY_CODEX_MODEL=gpt-x")
	runScript(t, "install.sh", a, env...)
	runScript(t, "install.sh", a, env...) // second write must replace our block, keep first .bak
	p := Paths{Root: b}
	for i := 0; i < 2; i++ {
		if err := p.WriteCodex(CodexOpts{Origin: testOrigin, Secret: testSecret, Model: "gpt-x", Shell: "/bin/bash"}); err != nil {
			t.Fatal(err)
		}
	}
	p.WriteStamp("codex", "skipped")
	diffTrees(t, "rewrite", tree(t, a), tree(t, b))
}

func TestWriteAndClear(t *testing.T) {
	root := t.TempDir()
	p := Paths{Root: root}
	if err := p.WriteClaude(ClaudeOpts{Origin: testOrigin + "/v1/", Secret: testSecret}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p.ClaudeSettings())
	if !strings.Contains(string(raw), `"ANTHROPIC_BASE_URL": "https://relay.example.com"`) {
		t.Fatalf("bad claude settings: %s", raw)
	}
	if err := p.WriteCodex(CodexOpts{Origin: testOrigin, Secret: testSecret, Shell: "/bin/zsh"}); err != nil {
		t.Fatal(err)
	}
	cfg, _ := os.ReadFile(p.CodexConfig())
	for _, want := range []string{MarkerStart, `base_url = "https://relay.example.com/v1"`, `model = "` + DefaultCodexModel + `"`} {
		if !strings.Contains(string(cfg), want) {
			t.Fatalf("config missing %q", want)
		}
	}
	if err := p.Clear(); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(p.ClaudeSettings())
	if string(raw) != "{}\n" {
		t.Fatalf("claude not cleared: %q", raw)
	}
	cfg, _ = os.ReadFile(p.CodexConfig())
	if strings.Contains(string(cfg), "jieshangai") {
		t.Fatalf("codex not cleared: %q", cfg)
	}
	if _, err := os.Stat(p.CodexEnv()); !os.IsNotExist(err) {
		t.Fatal("codex.env left behind")
	}
}

func TestBackupFingerprint(t *testing.T) {
	root := t.TempDir()
	orig := "{\"env\": {\"FOO\": \"1\"}}"
	seed(t, root, map[string]string{".claude/settings.json": orig})
	p := Paths{Root: root}
	p.WriteClaude(ClaudeOpts{Origin: testOrigin, Secret: testSecret})
	p.WriteClaude(ClaudeOpts{Origin: testOrigin, Secret: "sk-OTHER0123456789"})
	bak, _ := os.ReadFile(p.ClaudeSettings() + BackupSuffix)
	if string(bak) != orig {
		t.Fatalf("backup not original: %q", bak)
	}
	m, _ := p.loadManifest()
	e := m.Files[".claude/settings.json"]
	if e == nil || !e.HadOriginal || e.Original != hashBytes([]byte(orig)) || e.Written == "" {
		t.Fatalf("bad manifest entry %+v", e)
	}
	mraw, _ := os.ReadFile(p.manifestPath())
	if strings.Contains(string(mraw), "sk-") {
		t.Fatal("manifest contains secret")
	}
}

func TestRestoreFromBackup(t *testing.T) {
	root := t.TempDir()
	fx := fixtures["existing"]
	seed(t, root, fx)
	p := Paths{Root: root}
	if err := p.WriteClaude(ClaudeOpts{Origin: testOrigin, Secret: testSecret}); err != nil {
		t.Fatal(err)
	}
	if err := p.WriteCodex(CodexOpts{Origin: testOrigin, Secret: testSecret, Shell: "/bin/bash"}); err != nil {
		t.Fatal(err)
	}
	r, err := p.Restore(RestoreOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !r.UsedBackup || len(r.Files) != 3 {
		t.Fatalf("UsedBackup flags %+v", r)
	}
	for _, f := range r.Files {
		if !f.UsedBackup {
			t.Fatalf("file %s UsedBackup=false", f.Path)
		}
	}
	if len(r.Restored) != 3 {
		t.Fatalf("restored %v", r.Restored)
	}
	for rel, body := range fx {
		got, _ := os.ReadFile(filepath.Join(root, rel))
		if string(got) != body {
			t.Errorf("%s not restored verbatim: %q", rel, got)
		}
		if _, err := os.Stat(filepath.Join(root, rel) + BackupSuffix); !os.IsNotExist(err) {
			t.Errorf("%s backup left", rel)
		}
	}
	if _, err := os.Stat(p.Dir()); !os.IsNotExist(err) {
		t.Error(".jieshangai dir left")
	}
}

func TestRestoreEditedAfterWrite(t *testing.T) {
	root := t.TempDir()
	seed(t, root, map[string]string{".codex/config.toml": "model = \"o3\"\n"})
	p := Paths{Root: root}
	p.WriteCodex(CodexOpts{Origin: testOrigin, Secret: testSecret, Shell: "/bin/bash"})
	f, _ := os.OpenFile(p.CodexConfig(), os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("user_edit = true\n")
	f.Close()
	before := tree(t, root)
	r, err := p.Restore(RestoreOpts{})
	if !errors.Is(err, ErrNeedsConfirm) || len(r.Edited) != 1 {
		t.Fatalf("want ErrNeedsConfirm, got %v %v", err, r.Edited)
	}
	diffTrees(t, "unconfirmed restore must not change files", before, tree(t, root))
	if _, err := p.Restore(RestoreOpts{Confirm: true}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p.CodexConfig())
	if string(got) != "model = \"o3\"\n" {
		t.Fatalf("not restored: %q", got)
	}
}

func TestRestoreNoBackup(t *testing.T) {
	root := t.TempDir()
	p := Paths{Root: root}
	// files did not exist before: no backup -> only our entries removed.
	p.WriteClaude(ClaudeOpts{Origin: testOrigin, Secret: testSecret})
	p.WriteCodex(CodexOpts{Origin: testOrigin, Secret: testSecret, Shell: "/bin/bash"})
	r, err := p.Restore(RestoreOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if r.UsedBackup || len(r.Files) != 3 {
		t.Fatalf("UsedBackup flags %+v", r)
	}
	for _, f := range r.Files {
		if f.UsedBackup {
			t.Fatalf("file %s UsedBackup=true", f.Path)
		}
	}
	if len(r.Restored) != 0 || len(r.Cleaned) != 3 {
		t.Fatalf("report %+v", r)
	}
	// files we created and that are now empty are deleted
	for _, f := range []string{p.ClaudeSettings(), p.CodexConfig(), p.Bashrc()} {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Fatalf("%s left behind", f)
		}
	}

	// backup deleted by user -> same: only our entries removed, other keys kept.
	root2 := t.TempDir()
	seed(t, root2, map[string]string{".claude/settings.json": "{\"keep\": 1}"})
	p2 := Paths{Root: root2}
	p2.WriteClaude(ClaudeOpts{Origin: testOrigin, Secret: testSecret})
	os.Remove(p2.ClaudeSettings() + BackupSuffix)
	if _, err := p2.Restore(RestoreOpts{}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p2.ClaudeSettings())
	if string(raw) != "{\n  \"keep\": 1\n}\n" {
		t.Fatalf("claude2: %q", raw)
	}
}

func TestLegacyBakAdopted(t *testing.T) {
	root := t.TempDir()
	seed(t, root, map[string]string{".zshrc": "orig\n"})
	runScript(t, "install.sh", root, installEnv("codex", "/bin/zsh")...) // /i made .bak, no manifest
	p := Paths{Root: root}
	p.WriteCodex(CodexOpts{Origin: testOrigin, Secret: testSecret, Shell: "/bin/zsh"})
	if _, err := p.Restore(RestoreOpts{}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p.Zshrc())
	if string(got) != "orig\n" {
		t.Fatalf("zshrc %q", got)
	}
}

func TestSecretsNeverInErrorsOrMask(t *testing.T) {
	if m := MaskSecret(testSecret); strings.Contains(m, testSecret[3:len(testSecret)-2]) || m == testSecret {
		t.Fatal("mask leaks")
	}
	p := Paths{Root: t.TempDir()}
	bad := "sk-bad\nsecret"
	if err := p.WriteClaude(ClaudeOpts{Origin: testOrigin, Secret: bad}); err == nil || strings.Contains(err.Error(), "secret") && strings.Contains(err.Error(), bad) {
		t.Fatalf("bad secret handling: %v", err)
	}
	seed(t, p.Root, map[string]string{".claude/settings.json": "[1]"})
	if err := p.WriteClaude(ClaudeOpts{Origin: testOrigin, Secret: testSecret}); !errors.Is(err, ErrConfigUnreadable) || strings.Contains(err.Error(), testSecret) {
		t.Fatalf("got %v", err)
	}
}

func TestRestoreMixedUsedBackup(t *testing.T) {
	root := t.TempDir()
	seed(t, root, map[string]string{".bashrc": "x\n"}) // rc has backup; config.toml did not exist
	p := Paths{Root: root}
	p.WriteCodex(CodexOpts{Origin: testOrigin, Secret: testSecret, Shell: "/bin/bash"})
	r, err := p.Restore(RestoreOpts{})
	if err != nil || !r.UsedBackup || len(r.Files) != 2 {
		t.Fatalf("%v %+v", err, r)
	}
	for _, f := range r.Files {
		if want := strings.HasSuffix(f.Path, ".bashrc"); f.UsedBackup != want {
			t.Fatalf("%s UsedBackup=%v", f.Path, f.UsedBackup)
		}
	}
	if NoBackupMessage != "已去掉我们加的设置。如果你以前自己选过模型，需要重新选一次。" {
		t.Fatal("copy drift")
	}
}

func TestParityEditedAfterWrite(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	fx := fixtures["existing"]
	seed(t, a, fx)
	seed(t, b, fx)
	runScript(t, "install.sh", a, installEnv("codex", "/bin/bash")...)
	p := Paths{Root: b}
	p.WriteCodex(CodexOpts{Origin: testOrigin, Secret: testSecret, Shell: "/bin/bash"})
	p.WriteStamp("codex", "skipped")
	for _, r := range []string{a, b} {
		f, _ := os.OpenFile(filepath.Join(r, ".codex/config.toml"), os.O_APPEND|os.O_WRONLY, 0)
		f.WriteString("user_edit = 1\n")
		f.Close()
	}
	before := tree(t, a)
	out, err := runScriptErr(t, "uninstall.sh", a)
	if err == nil || !strings.Contains(out, EditedConfirmPrompt) || lastLine(out) != DeclinedMessage {
		t.Fatalf("/u should refuse without confirmation: %v %q", err, out)
	}
	if strings.Count(strings.TrimSpace(out), "\n") != 1 {
		t.Fatalf("expected prompt line + declined line, got %q", out)
	}
	diffTrees(t, "declined /u changed files", before, tree(t, a))
	if _, err := p.Restore(RestoreOpts{}); !errors.Is(err, ErrNeedsConfirm) {
		t.Fatal(err)
	}
	out = runScript(t, "uninstall.sh", a, "RELAY_CONFIRM=1")
	r, err := p.Restore(RestoreOpts{Confirm: true})
	if err != nil || lastLine(out) != r.Message() || !r.UsedBackup {
		t.Fatalf("%v %q %+v", err, out, r)
	}
	diffTrees(t, "confirmed", tree(t, a), tree(t, b))
	pre := t.TempDir()
	seed(t, pre, fx)
	diffTrees(t, "confirmed vs pre-install", tree(t, pre), tree(t, b))
}

// PM: old /i user (pre-manifest script, no backup) -> only our entries removed.
func TestOldInstallUserWithoutBackup(t *testing.T) {
	// what the previous /i left: our block + provider line (+ untagged default model), no .bak, no manifest
	old := map[string]string{
		".codex/config.toml":             "model_provider = \"jieshangai\"\nmodel = \"o3\"\n[profiles.x]\nfoo = 1\n\n" + MarkerStart + "\n[model_providers.jieshangai]\nbase_url = \"x\"\n" + MarkerEnd + "\n",
		".bashrc":                        "alias ll='ls -l'\n\n" + RCStart + "\n. x\n" + RCEnd + "\n",
		".claude/settings.json":          "{\n  \"theme\": \"dark\",\n  \"env\": {\n    \"FOO\": \"1\",\n    \"ANTHROPIC_API_KEY\": \"" + testSecret + "\"\n  }\n}\n",
		".jieshangai/codex.env":          "export X=1\n",
		".jieshangai/install-stamp.json": "{}\n",
	}
	want := map[string]string{
		".codex/config.toml":    "-rw-r--r--\nmodel = \"o3\"\n[profiles.x]\nfoo = 1\n",
		".bashrc":               "-rw-r--r--\nalias ll='ls -l'\n",
		".claude/settings.json": "-rw-r--r--\n{\n  \"theme\": \"dark\",\n  \"env\": {\n    \"FOO\": \"1\"\n  }\n}\n",
	}
	a, b := t.TempDir(), t.TempDir()
	seed(t, a, old)
	seed(t, b, old)
	out := runScript(t, "uninstall.sh", a)
	r, err := Paths{Root: b}.Restore(RestoreOpts{})
	if err != nil || r.UsedBackup {
		t.Fatalf("%v %+v", err, r)
	}
	if lastLine(out) != NoBackupMessage {
		t.Fatalf("/u last line %q", lastLine(out))
	}
	diffTrees(t, "old user /u", want, tree(t, a))
	diffTrees(t, "old user core", want, tree(t, b))
}

// PM: Codex with backup -> byte-identical to pre-install (both /i+/u and core).
func TestCodexWithBackupByteIdentical(t *testing.T) {
	fx := map[string]string{
		".codex/config.toml": "model = \"o3\"\nmodel_provider = \"openai\"\n\n[profiles.x]\nfoo = 1\n\n\n",
		".zshrc":             "# zsh\nexport A=1",
	}
	for _, viaScript := range []bool{true, false} {
		root := t.TempDir()
		seed(t, root, fx)
		os.Chmod(filepath.Join(root, ".zshrc"), 0o640)
		pre := tree(t, root)
		if viaScript {
			runScript(t, "install.sh", root, append(installEnv("codex", "/bin/zsh"), "RELAY_CODEX_MODEL=gpt-x")...)
			out := runScript(t, "uninstall.sh", root)
			if lastLine(out) != RestoredMessage {
				t.Fatalf("last line %q", lastLine(out))
			}
		} else {
			p := Paths{Root: root}
			if err := p.WriteCodex(CodexOpts{Origin: testOrigin, Secret: testSecret, Model: "gpt-x", Shell: "/bin/zsh"}); err != nil {
				t.Fatal(err)
			}
			p.WriteStamp("codex", "skipped")
			if _, err := p.Restore(RestoreOpts{}); err != nil {
				t.Fatal(err)
			}
		}
		diffTrees(t, "codex backup restore", pre, tree(t, root))
		if _, err := os.Stat(filepath.Join(root, ".jieshangai")); !os.IsNotExist(err) {
			t.Error(".jieshangai left")
		}
	}
}

// install.ps1 / uninstall.ps1 follow the same manifest + restore rules. Their
// JSON formatting differs from Python's, so this checks behaviour, not bytes
// against core. Skipped when pwsh is missing.
func runPS1(t *testing.T, name, root string, env ...string) (string, error) {
	t.Helper()
	pw, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skip("pwsh missing")
	}
	// The hermetic env below (no SystemRoot and friends) makes .NET fall back
	// to the ANSI code page for redirected stdout, so a zh-CN Windows host
	// emits GBK and the UTF-8 assertions below fail although the script
	// behaved correctly. Force the child's console encoding before dot-running
	// the script; on the Linux dev host this is a no-op.
	script := filepath.Join("..", "..", "install", "ps1", name)
	quoted := "'" + strings.ReplaceAll(script, "'", "''") + "'"
	cmd := exec.Command(pw, "-NoProfile", "-NonInteractive", "-Command", "[Console]::OutputEncoding=[Text.Encoding]::UTF8; & "+quoted)
	cmd.Env = append([]string{"PATH=/usr/bin:/bin:/usr/local/bin", "HOME=" + t.TempDir(), "RELAY_HOME=" + root}, env...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	cmd.Stdin = nil
	err = cmd.Run()
	if strings.Contains(out.String(), testSecret) {
		t.Fatalf("%s printed the secret", name)
	}
	return out.String(), err
}

func TestPS1RestoreRules(t *testing.T) {
	fx := map[string]string{
		".codex/config.toml":    "model = \"o3\"\nmodel_provider = \"openai\"\n\n[p]\nx = 1\n",
		".claude/settings.json": "{\"theme\":\"dark\",\"env\":{\"A\":\"1\"}}",
	}
	inst := func(root, agent string) {
		if out, err := runPS1(t, "install.ps1", root, "RELAY_AGENT="+agent, "RELAY_TOKEN="+testSecret, "RELAY_SKIP_CLI=1", "RELAY_CODEX_MODEL=gpt-x"); err != nil {
			t.Fatalf("install.ps1 %s: %v\n%s", agent, err, out)
		}
	}
	// with backup: byte-identical to pre-install; edited -> one line + refuse
	root := t.TempDir()
	seed(t, root, fx)
	pre := tree(t, root)
	inst(root, "claude")
	inst(root, "codex")
	m, _ := Paths{Root: root}.loadManifest()
	if len(m.Files) != 2 || !m.Files[".codex/config.toml"].HadOriginal || m.Files[".codex/config.toml"].Written == "" {
		t.Fatalf("ps1 manifest %+v", m.Files)
	}
	f, _ := os.OpenFile(filepath.Join(root, ".codex/config.toml"), os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("edit = 1\n")
	f.Close()
	out, err := runPS1(t, "uninstall.ps1", root)
	if err == nil || !strings.Contains(out, EditedConfirmPrompt) || lastLine(out) != DeclinedMessage {
		t.Fatalf("ps1 should refuse: %v %q", err, out)
	}
	out, err = runPS1(t, "uninstall.ps1", root, "RELAY_CONFIRM=1")
	if err != nil || lastLine(out) != RestoredMessage {
		t.Fatalf("ps1 restore: %v %q", err, out)
	}
	diffTrees(t, "ps1 vs pre-install", pre, tree(t, root))

	// old user without backup -> only our entries removed, NoBackupMessage
	root = t.TempDir()
	seed(t, root, map[string]string{
		".codex/config.toml":    "model_provider = \"jieshangai\"\nmodel = \"o3\"\n\n" + MarkerStart + "\n[model_providers.jieshangai]\n" + MarkerEnd + "\n",
		".jieshangai/codex.env": "x\n",
	})
	out, err = runPS1(t, "uninstall.ps1", root)
	if err != nil || lastLine(out) != NoBackupMessage {
		t.Fatalf("ps1 old user: %v %q", err, out)
	}
	got := tree(t, root)
	if len(got) != 1 || got[".codex/config.toml"] != "-rw-r--r--\nmodel = \"o3\"\n" {
		t.Fatalf("ps1 old user tree %q", got)
	}
}
