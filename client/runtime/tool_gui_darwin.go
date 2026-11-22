//go:build darwin

package clientruntime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func inspectGUI(ctx context.Context, agent string) (guiInfo, error) {
	for _, name := range guiAppNames(agent) {
		for _, base := range []string{filepath.Join(homeDir(), "Applications"), "/Applications"} {
			p := filepath.Join(base, name)
			if _, e := os.Stat(filepath.Join(p, "Contents", "Info.plist")); e != nil {
				continue
			}
			out, e := runToolCommand(ctx, "/usr/libexec/PlistBuddy", []string{"-c", "Print :CFBundleShortVersionString", filepath.Join(p, "Contents", "Info.plist")}, nil, false)
			if e == nil {
				return guiInfo{Path: p, Version: strings.TrimSpace(out)}, nil
			}
		}
	}
	return guiInfo{}, nil
}
func (m *toolManager) latestGUI(ctx context.Context, agent string) (guiRelease, error) {
	return m.cask(ctx, agent)
}
func (m *toolManager) latestGUIVersion(ctx context.Context, agent string) (string, error) {
	r, err := m.cask(ctx, agent)
	return r.Version, err
}

// update only steers the Windows Store channel; the macOS path always uses
// the offline/mirror/cask sources in order.
func (m *toolManager) installGUIPlatform(ctx context.Context, agent string, update bool, progress func(string)) error {
	info, _ := inspectGUI(ctx, agent)
	if info.Path != "" {
		if !pathInside(homeDir(), info.Path) {
			return errors.New("这个桌面版由系统安装，请使用它自己的更新入口")
		}
		if guiIsRunning(info) {
			return errors.New("请先保存任务并退出桌面版，再更新")
		}
	}
	file, r, e := offlineGUI(agent)
	if e != nil && !os.IsNotExist(e) {
		return e
	}
	if file == "" {
		if rel, ok := m.overrideGUIRelease(ctx, agent); ok {
			progress("正在从代理加速源获取官方桌面版……")
			if dl, dlErr := m.overrideDownloader().file(ctx, rel.URL, "", progress); dlErr == nil {
				r, file = rel, dl
			} else {
				m.diagnostic(agent, "gui", "download", "override-mirror", "", dlErr)
			}
		}
	}
	if file == "" {
		// downloads.claude.ai is unreachable from mainland China; the mirror
		// serves the official archive from a reachable node.
		// Its identity is still gated by the codesign/TeamID/spctl checks below.
		if rel, mirrorErr := mirrorGUIRelease(m.goos, m.arch, agent); mirrorErr == nil {
			progress("正在从国内镜像获取官方桌面版……")
			if mirrored, dlErr := m.download.file(ctx, rel.URL, "", progress); dlErr == nil {
				r, file = rel, mirrored
			} else {
				m.diagnostic(agent, "gui", "download", "agentsmirror", "", dlErr)
			}
		} else {
			m.diagnostic(agent, "gui", "metadata", "agentsmirror", "", mirrorErr)
		}
	}
	if file == "" {
		r, e = m.latestGUI(ctx, agent)
		if e != nil {
			return e
		}
		file, e = m.download.file(ctx, r.URL, r.SHA256, progress)
		if e != nil {
			return e
		}
	}
	stage, e := os.MkdirTemp(m.root, "desktop-stage-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(stage)
	source := stage
	if r.Format == "zip" {
		progress("正在解包官方桌面版……")
		if e = validateGUIZIP(file, stage); e != nil {
			return e
		}
		if _, e = runToolCommand(ctx, "/usr/bin/ditto", []string{"-x", "-k", file, stage}, nil, true); e != nil {
			return errors.New("桌面版解包失败")
		}
	} else {
		source = filepath.Join(stage, "mount")
		if e = os.Mkdir(source, 0700); e != nil {
			return e
		}
		if _, e = runToolCommand(ctx, "/usr/bin/hdiutil", []string{"attach", file, "-readonly", "-nobrowse", "-mountpoint", source}, nil, true); e != nil {
			return errors.New("桌面版安装文件无法打开")
		}
		defer func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			_, _ = runToolCommand(cleanup, "/usr/bin/hdiutil", []string{"detach", source}, nil, true)
		}()
	}
	name := findGUIApp(source, agent)
	if name == "" {
		return errors.New("桌面版安装文件里没有找到官方应用")
	}
	vendor := "Q6L2SF6YDW"
	if agent == "codex" {
		vendor = "2DC432GLL2"
	}
	app := filepath.Join(source, name)
	progress("正在验证官方应用签名……")
	if _, e = runToolCommand(ctx, "/usr/bin/codesign", []string{"--verify", "--deep", "--strict", app}, nil, false); e != nil {
		return errors.New("桌面版签名验证失败")
	}
	out, e := runToolCommand(ctx, "/usr/bin/codesign", []string{"-dv", "--verbose=4", app}, nil, false)
	if e != nil || !strings.Contains(out, "TeamIdentifier="+vendor) {
		return errors.New("桌面版发布者不匹配")
	}
	if _, e = runToolCommand(ctx, "/usr/sbin/spctl", []string{"--assess", "--type", "execute", app}, nil, false); e != nil {
		return errors.New("macOS 没有通过官方应用的安全验证")
	}
	dir := filepath.Join(homeDir(), "Applications")
	if e = os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	copyDir, e := os.MkdirTemp(dir, ".jieshangai-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(copyDir)
	next := filepath.Join(copyDir, name)
	if _, e = runToolCommand(ctx, "/usr/bin/ditto", []string{app, next}, nil, true); e != nil {
		return errors.New("桌面版复制失败")
	}
	dest := filepath.Join(dir, name)
	old := ""
	if _, e = os.Stat(dest); e == nil {
		backup, e := os.MkdirTemp(m.root, "desktop-backup-")
		if e != nil {
			return e
		}
		old = filepath.Join(backup, name)
		if e = os.Rename(dest, old); e != nil {
			return e
		}
	}
	if e = os.Rename(next, dest); e != nil {
		if old != "" {
			_ = os.Rename(old, dest)
		}
		return e
	}
	return nil
}
