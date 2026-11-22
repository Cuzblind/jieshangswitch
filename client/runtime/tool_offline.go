package clientruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Offline bundles are prepared on the matching OS/architecture. They contain
// complete, tested CLI trees and vendor GUI packages, never user configuration.
type offlineBundle struct {
	OS        string                `json:"os"`
	Arch      string                `json:"arch"`
	BuildRoot string                `json:"buildRoot"`
	Files     map[string]string     `json:"files"`
	GUI       map[string]guiRelease `json:"gui"`
}

func fileDigest(path string) (string, error) {
	// Antivirus software on Windows briefly denies access to freshly written or
	// renamed files (observed lock windows of one to two seconds). A transient
	// denial must not invalidate a just-finished download's cache entry, which
	// would force a full re-download later.
	var last error
	for delay := 100 * time.Millisecond; ; delay *= 2 {
		digest, err := fileDigestOnce(path)
		if err == nil {
			return digest, nil
		}
		if os.IsNotExist(err) {
			return "", err
		}
		last = err
		if delay > 1600*time.Millisecond {
			return "", last
		}
		time.Sleep(delay)
	}
}

func fileDigestOnce(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	_, err = io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), err
}
func loadOffline() (string, offlineBundle, error) {
	var b offlineBundle
	exe, err := os.Executable()
	if err != nil {
		return "", b, err
	}
	dir := filepath.Join(filepath.Dir(exe), "jieshangai-offline")
	if runtime.GOOS == "darwin" {
		dir = filepath.Join(filepath.Dir(exe), "..", "Resources", "jieshangai-offline")
	}
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return "", b, err
	}
	if len(data) > 8<<20 || json.Unmarshal(data, &b) != nil || b.OS != runtime.GOOS || b.Arch != runtime.GOARCH || b.BuildRoot == "" {
		return "", b, errors.New("离线资源包与这台电脑不匹配")
	}
	return dir, b, nil
}
func offlineFile(dir string, b offlineBundle, name string) (string, error) {
	p, err := archivePath(dir, name)
	if err != nil {
		return "", err
	}
	// Reject substituted symlinks; file hashes detect corruption, while the GUI
	// installer still verifies its vendor signature before installation.
	// Resolve both sides: the bundle directory itself may be reached through a
	// link (macOS /var, Windows 8.3 names).
	root, rootErr := filepath.EvalSymlinks(dir)
	resolved, e := filepath.EvalSymlinks(p)
	if rootErr != nil || e != nil || !pathInside(root, resolved) {
		return "", errors.New("离线资源路径无效")
	}
	digest, err := fileDigest(p)
	if err != nil || b.Files[name] == "" || digest != b.Files[name] {
		return "", errors.New("离线资源校验失败，请重新获取完整资源包")
	}
	return p, nil
}
func (m *toolManager) importOfflineCLI(ctx context.Context, agent string, progress func(string)) (bool, error) {
	dir, b, err := loadOffline()
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	progress("正在使用随包提供的离线命令行资源……")
	for name := range b.Files {
		if !strings.HasPrefix(name, "tools/node/") && !strings.HasPrefix(name, "tools/"+agent+"/") {
			continue
		}
		if err := ctx.Err(); err != nil {
			return false, err
		}
		source, err := offlineFile(dir, b, name)
		if err != nil {
			return false, err
		}
		dest, err := archivePath(m.root, strings.TrimPrefix(name, "tools/"))
		if err != nil {
			return false, err
		}
		if digest, err := fileDigest(dest); err == nil && digest == b.Files[name] {
			continue
		}
		tmp := dest + ".offline-part"
		_ = os.Remove(tmp)
		if err = copyToolFile(source, tmp, 0700); err != nil {
			return false, err
		}
		if err = os.Rename(tmp, dest); err != nil {
			return false, err
		}
	}
	rebase := func(p string) (string, error) {
		if !pathInside(b.BuildRoot, p) {
			return "", errors.New("离线安装记录无效")
		}
		rel, err := filepath.Rel(b.BuildRoot, p)
		if err != nil {
			return "", err
		}
		return archivePath(m.root, filepath.ToSlash(rel))
	}
	// Receipts are rewritten only after all payload checks have succeeded.
	var node struct {
		Path string `json:"path"`
	}
	nodeReceipt, err := offlineFile(dir, b, "tools/node/active.json")
	if err != nil {
		return false, err
	}
	data, err := os.ReadFile(nodeReceipt)
	if err != nil {
		return false, err
	}
	if json.Unmarshal(data, &node) != nil {
		return false, errors.New("离线运行环境记录无效")
	}
	if !pathInside(m.root, node.Path) {
		node.Path, err = rebase(node.Path)
		if err != nil {
			return false, err
		}
	}
	data, _ = json.Marshal(node)
	if err = writePrivateFile(filepath.Join(m.root, "node", "active.json"), data); err != nil {
		return false, err
	}
	entryReceipt, err := offlineFile(dir, b, "tools/"+agent+"/active.json")
	if err != nil {
		return false, err
	}
	data, err = os.ReadFile(entryReceipt)
	if err != nil {
		return false, err
	}
	var entry toolEntry
	if json.Unmarshal(data, &entry) != nil {
		return false, errors.New("离线命令行记录无效")
	}
	entry.Program, err = rebase(entry.Program)
	if err != nil {
		return false, err
	}
	for i, arg := range entry.Args {
		if filepath.IsAbs(arg) {
			entry.Args[i], err = rebase(arg)
			if err != nil {
				return false, err
			}
		}
	}
	data, _ = json.Marshal(entry)
	if err = writePrivateFile(m.receipt(agent), data); err != nil {
		return false, err
	}
	m.mu.Lock()
	m.node = node.Path
	m.mu.Unlock()
	m.forgetCLIProbe(agent)
	if !m.cliReady(agent) {
		return false, errors.New("离线命令行启动验证失败")
	}
	return true, m.writeLauncher(agent, entry)
}
func offlineGUI(agent string) (string, guiRelease, error) {
	dir, b, err := loadOffline()
	if err != nil {
		return "", guiRelease{}, err
	}
	r, ok := b.GUI[agent]
	if !ok {
		return "", r, errors.New("离线资源包缺少桌面版")
	}
	p, err := offlineFile(dir, b, "gui/"+agent+"."+r.Format)
	return p, r, err
}

// PrepareOffline is a packaging command, not an IPC endpoint. Run it on a
// matching native CI runner with access to the official vendor downloads.
func PrepareOffline(ctx context.Context, output string, progress func(string)) error {
	if runtime.GOOS != "windows" && runtime.GOOS != "darwin" {
		return errors.New("请在 Windows 或 macOS 的目标架构机器准备离线资源")
	}
	output, err := filepath.Abs(output)
	if err != nil {
		return err
	}
	if err = os.Mkdir(output, 0700); err != nil {
		return fmt.Errorf("离线输出目录必须不存在：%w", err)
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(output)
		}
	}()
	m := newToolManager()
	m.root = filepath.Join(output, "tools")
	m.isolate = true
	m.download.cache = filepath.Join(output, "download-cache")
	b := offlineBundle{OS: runtime.GOOS, Arch: runtime.GOARCH, BuildRoot: m.root, Files: map[string]string{}, GUI: map[string]guiRelease{}}
	for _, agent := range []string{"claude", "codex"} {
		if err = m.installCLI(ctx, agent, false, progress); err != nil {
			return err
		}
		r, err := m.latestGUI(ctx, agent)
		if err != nil {
			return err
		}
		if agent == "claude" {
			osname := "win32"
			if runtime.GOOS == "darwin" {
				osname = "darwin"
				r.Format = "dmg"
			}
			arch := map[string]string{"amd64": "x64", "arm64": "arm64"}[runtime.GOARCH]
			r.URL = "https://claude.ai/api/desktop/" + osname + "/" + arch + "/offline/latest/redirect"
			r.SHA256 = ""
		}
		file, err := m.download.file(ctx, r.URL, r.SHA256, progress)
		if err != nil {
			return err
		}
		if err = os.MkdirAll(filepath.Join(output, "gui"), 0700); err != nil {
			return err
		}
		if err = copyToolFile(file, filepath.Join(output, "gui", agent+"."+r.Format), 0600); err != nil {
			return err
		}
		b.GUI[agent] = r
	}
	// Avoid bundling machine-specific launchers, caches or npm settings. npm's
	// internal symlinks are materialized as regular files for portability.
	for _, name := range []string{"bin", "npm-cache", "npmrc", "npm-globalrc"} {
		_ = os.RemoveAll(filepath.Join(m.root, name))
	}
	_ = os.RemoveAll(m.download.cache)
	err = filepath.WalkDir(output, func(path string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if e = ctx.Err(); e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		rel, e := filepath.Rel(output, path)
		if e != nil {
			return e
		}
		if d.Type()&os.ModeSymlink != 0 {
			resolved, e := filepath.EvalSymlinks(path)
			if e != nil || !pathInside(m.root, resolved) {
				return errors.New("离线包存在外部链接")
			}
			tmp := path + ".materialized"
			if e = copyToolFile(resolved, tmp, 0700); e != nil {
				return e
			}
			if e = os.Remove(path); e != nil {
				return e
			}
			if e = os.Rename(tmp, path); e != nil {
				return e
			}
		}
		hash, e := fileDigest(path)
		if e != nil {
			return e
		}
		b.Files[filepath.ToSlash(rel)] = hash
		return nil
	})
	if err != nil {
		return err
	}
	data, _ := json.MarshalIndent(b, "", "  ")
	if err = writePrivateFile(filepath.Join(output, "manifest.json"), data); err != nil {
		return err
	}
	ok = true
	return nil
}
