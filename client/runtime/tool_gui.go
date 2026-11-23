package clientruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type guiInfo struct {
	Path    string `json:"path"`
	Version string `json:"version"`
	Family  string `json:"family"`
	AppID   string `json:"appId"` // manifest Application Id; with Family it forms the shell-activation target
}
type guiRelease struct{ URL, FallbackURL, SHA256, SHA1, Version, Format string }
type caskRelease struct {
	Version    string `json:"version"`
	URL        string `json:"url"`
	SHA256     string `json:"sha256"`
	Variations map[string]struct {
		URL     string `json:"url"`
		SHA256  string `json:"sha256"`
		Version string `json:"version"`
	} `json:"variations"`
}

const storeUnavailableWindow = 24 * time.Hour

func guiPlatformKey(goos, arch, agent string) (string, string, error) {
	osToken := map[string]string{"windows": "win", "darwin": "mac"}[goos]
	archToken := map[string]string{"amd64": "x64", "arm64": "arm64"}[arch]
	if goos == "darwin" && arch == "amd64" {
		archToken = "intel"
	}
	format := "dmg"
	if goos == "windows" {
		format = "msix"
	}
	if osToken == "" || archToken == "" || !validAgent(agent) {
		return "", "", errInstallDependency
	}
	// Claude macOS archive on agentsmirror is a single Universal DMG at /latest/mac.
	if agent == "claude" && goos == "darwin" {
		return "mac", format, nil
	}
	return osToken + "-" + archToken, format, nil
}

// mirrorGUIRelease points at a third-party mirror of the vendors' own desktop
// archives. claude.ai geo-blocks mainland-China IPs and downloads.claude.ai is
// unreachable there, while the mirror serves the official packages from a
// domestic/R2 node. These "latest" URLs carry no checksum; cache reuse depends
// on ETag revalidation and the package is still gated by validateMSIX /
// Add-AppxPackage on Windows and codesign/spctl on macOS after download.
func mirrorGUIRelease(goos, arch, agent string) (guiRelease, error) {
	key, format, err := guiPlatformKey(goos, arch, agent)
	if err != nil {
		return guiRelease{}, err
	}
	host := "https://claudeapp.agentsmirror.com/latest/"
	if agent == "codex" {
		host = "https://codexapp.agentsmirror.com/latest/"
	}
	return guiRelease{URL: host + key, Format: format}, nil
}

// overrideGUIRelease checks an optional operator-maintained JSON file at
// <origin>/client-dist/gui-mirrors.json (2s timeout, cached 5m, silent when absent).
// This allows switching mirrors or pointing to a reverse proxy without shipping a
// new client binary, while keeping OS signature verification mandatory.
func (m *toolManager) overrideGUIRelease(ctx context.Context, agent string) (guiRelease, bool) {
	origin := strings.TrimRight(strings.TrimSpace(m.origin), "/")
	if origin == "" {
		return guiRelease{}, false
	}
	key, format, err := guiPlatformKey(m.goos, m.arch, agent)
	if err != nil {
		return guiRelease{}, false
	}
	m.mirrorMu.Lock()
	if time.Since(m.mirrorCheckedAt) < 5*time.Minute {
		cfg := m.mirrorCache
		m.mirrorMu.Unlock()
		return pickOverrideRelease(cfg, agent, key, format)
	}
	m.mirrorMu.Unlock()

	fetchCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, origin+"/client-dist/gui-mirrors.json", nil)
	if err != nil {
		return guiRelease{}, false
	}
	req.Header.Set("User-Agent", "JieshangAI-Installer/0.2")
	client := &http.Client{
		Transport: &http.Transport{Proxy: systemAwareProxy, ResponseHeaderTimeout: 2 * time.Second, TLSHandshakeTimeout: 2 * time.Second},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	var cfg map[string]map[string]string
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&cfg)
		}
	}
	m.mirrorMu.Lock()
	m.mirrorCache = cfg
	m.mirrorCheckedAt = time.Now()
	m.mirrorMu.Unlock()
	return pickOverrideRelease(cfg, agent, key, format)
}

func pickOverrideRelease(cfg map[string]map[string]string, agent, key, format string) (guiRelease, bool) {
	if cfg == nil {
		return guiRelease{}, false
	}
	agentMap := cfg[agent]
	if agentMap == nil {
		return guiRelease{}, false
	}
	raw := strings.TrimSpace(agentMap[key])
	if raw == "" && strings.HasPrefix(key, "mac-") {
		raw = strings.TrimSpace(agentMap["mac"])
	}
	if !allowedOverrideMirrorURL(raw) {
		return guiRelease{}, false
	}
	if u, err := url.Parse(raw); err == nil && strings.HasSuffix(strings.ToLower(u.Path), ".zip") {
		format = "zip"
	}
	return guiRelease{URL: raw, Format: format}, true
}

func isZIPFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var magic [4]byte
	n, err := io.ReadFull(f, magic[:])
	return err == nil && n == 4 && magic[0] == 'P' && magic[1] == 'K' && (magic[2] == 3 || magic[2] == 5 || magic[2] == 7) && (magic[3] == 4 || magic[3] == 6 || magic[3] == 8)
}

func (m *toolManager) overrideDownloader() *toolDownloader {
	return &toolDownloader{
		client: newDownloadClientWithAllow(allowedOverrideMirrorURL),
		cache:  m.download.cache,
		allow:  allowedOverrideMirrorURL,
		slots:  m.download.slots,
	}
}

// winget 1.9.x accepts --name for source list. The --source option belongs to
// install/search and makes source probing fail before the Store is queried.
func wingetStoreProbeArgs() []string {
	return []string{"source", "list", "--name", "msstore"}
}

// A failed Store probe is remembered briefly so a retry can move to the
// official package path instead of making the user wait through the same
// unavailable source again. The marker expires automatically.
func (m *toolManager) storeUnavailableFor(agent string) bool {
	m.mu.Lock()
	until := m.storeUnavailable[agent]
	m.mu.Unlock()
	return until.After(time.Now())
}

// The Store is one channel: a failure seen for one app also saves the other
// from waiting through the same outage.
func (m *toolManager) markStoreUnavailable(string) {
	until := time.Now().Add(storeUnavailableWindow)
	for _, agent := range []string{"claude", "codex"} {
		m.mu.Lock()
		if m.storeUnavailable == nil {
			m.storeUnavailable = map[string]time.Time{}
		}
		m.storeUnavailable[agent] = until
		m.mu.Unlock()
		_ = os.MkdirAll(filepath.Join(m.root, agent), 0700)
		_ = writePrivateFile(filepath.Join(m.root, agent, "gui-store-unavailable-until"), []byte(until.UTC().Format(time.RFC3339)))
	}
}

func (m *toolManager) clearStoreUnavailable(string) {
	for _, agent := range []string{"claude", "codex"} {
		m.mu.Lock()
		delete(m.storeUnavailable, agent)
		m.mu.Unlock()
		_ = os.Remove(filepath.Join(m.root, agent, "gui-store-unavailable-until"))
	}
}

// Space for the download plus the unpacked app, checked before a long transfer
// instead of failing at deployment. Sizes follow the 2026-11 packages.
func checkGUIDisk(agent, dir string, free func(string) (int64, error)) error {
	need := int64(3 << 29)
	if agent == "codex" {
		need = 3 << 30
	}
	available, err := free(dir)
	if err != nil || available >= need {
		return nil
	}
	return processFailure("verify", "local", "0x80070070", errors.New("not enough disk space"))
}

func guiAppNames(agent string) []string {
	if agent == "codex" {
		return []string{"ChatGPT.app", "Codex.app"}
	}
	return []string{"Claude.app"}
}

func findGUIApp(dir, agent string) string {
	for _, name := range guiAppNames(agent) {
		if st, err := os.Stat(filepath.Join(dir, name)); err == nil && st.IsDir() {
			return name
		}
	}
	return ""
}

func (m *toolManager) cask(ctx context.Context, agent string) (guiRelease, error) {
	token := "claude"
	if agent == "codex" {
		token = "chatgpt"
	}
	var c caskRelease
	var err error
	for _, base := range []string{"https://mirrors.tuna.tsinghua.edu.cn/homebrew-bottles/api/cask/", "https://formulae.brew.sh/api/cask/"} {
		err = m.download.metadata(ctx, base+token+".json", &c)
		if err == nil {
			break
		}
	}
	if err != nil {
		return guiRelease{}, err
	}
	// API defaults to Apple Silicon. Intel variants have unprefixed OS keys.
	if m.arch == "amd64" {
		for _, key := range []string{"tahoe", "sequoia", "sonoma", "ventura"} {
			if v, ok := c.Variations[key]; ok {
				c.URL = v.URL
				c.SHA256 = v.SHA256
				if v.Version != "" {
					c.Version = v.Version
				}
				break
			}
		}
	}
	if !allowedDownloadURL(c.URL) || c.Version == "" {
		return guiRelease{}, errors.New("没有找到适合这台电脑的官方桌面版")
	}
	if m.arch == "amd64" && strings.Contains(c.URL, "arm64") {
		return guiRelease{}, errors.New("官方清单暂未提供 Intel 桌面版")
	}
	sha := c.SHA256
	if sha == "no_check" {
		sha = ""
	}
	if sha != "" && len(sha) != 64 {
		return guiRelease{}, errors.New("无效的桌面版校验信息")
	}
	format := "dmg"
	if strings.Contains(c.URL, ".zip") {
		format = "zip"
	}
	return guiRelease{URL: c.URL, SHA256: sha, Version: strings.Split(c.Version, ",")[0], Format: format}, nil
}
func newerVersion(a, b string) bool {
	clean := func(v string) []int {
		v = strings.TrimPrefix(strings.TrimSpace(v), "v")
		if fields := strings.Fields(v); len(fields) > 0 {
			v = fields[0]
		}
		parts := strings.Split(v, ".")
		out := make([]int, 4)
		for i := 0; i < len(parts) && i < 4; i++ {
			out[i], _ = strconv.Atoi(parts[i])
		}
		return out
	}
	if a == "" {
		return false
	}
	if b == "" {
		return true
	}
	x, y := clean(a), clean(b)
	for i := range x {
		if x[i] != y[i] {
			return x[i] > y[i]
		}
	}
	return false
}

// The desktop-app check starts PowerShell, which a slow VM needs well over
// the old 6s for; a failed check keeps the last result.
const guiProbeTimeout = 30 * time.Second

func (m *toolManager) refreshGUI(ctx context.Context, agent string) {
	started := time.Now()
	info, err := inspectGUI(ctx, agent)
	m.diagnostic(agent, "gui", "probe", "local", fmt.Sprintf("elapsed_ms=%d", time.Since(started).Milliseconds()), err)
	if err == nil {
		m.recordGUI(agent, info)
	}
}

func (m *toolManager) recordGUI(agent string, info guiInfo) {
	m.mu.Lock()
	s := m.states[agent]
	s.GUIInstalled = info.Path != ""
	s.GUIVersion = info.Version
	s.GUIInspected = true
	if s.GUIInstalled {
		s.Note = ""
	}
	m.states[agent] = s
	m.mu.Unlock()
	m.saveInspection(agent)
}

// inspect probes both assistants at once; each probe spawns processes that
// take seconds, and running them in turn doubled the wait before cards settled.
func (m *toolManager) inspect() {
	var wg sync.WaitGroup
	for _, a := range []string{"claude", "codex"} {
		a := a
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.cliReady(a)
			ctx, cancel := context.WithTimeout(context.Background(), guiProbeTimeout)
			m.refreshGUI(ctx, a)
			cancel()
		}()
	}
	wg.Wait()
}
func (m *toolManager) install(ctx context.Context, agent, component string, progress func(string)) error {
	if !validAgent(agent) {
		return errors.New("unsupported application")
	}
	update := component == "update"
	if component == "cli" {
		return m.installCLI(ctx, agent, false, progress)
	}
	if component == "gui" {
		m.component(agent, "gui", "running", "正在准备安装官方桌面版", nil)
		report := func(line string) { m.component(agent, "gui", "running", line, nil); progress("桌面版 · " + line) }
		err := m.installGUI(ctx, agent, false, report)
		m.componentError(agent, "gui", err)
		return err
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var failures []string
	var causes []error
	for _, kind := range []string{"cli", "gui"} {
		kind := kind
		wg.Add(1)
		go func() {
			defer wg.Done()
			label := "CLI"
			if kind == "gui" {
				label = "桌面版"
			}
			m.component(agent, kind, "running", "正在准备安装", nil)
			report := func(line string) { m.component(agent, kind, "running", line, nil); progress(label + " · " + line) }
			var err error
			if kind == "cli" {
				err = m.installCLI(ctx, agent, update, report)
			} else {
				s := m.state(agent)
				if !update || newerVersion(s.LatestGUI, s.GUIVersion) {
					err = m.installGUI(ctx, agent, update, report)
				}
			}
			m.componentError(agent, kind, err)
			if err != nil {
				mu.Lock()
				failures = append(failures, label)
				causes = append(causes, err)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if len(failures) > 0 {
		return fmt.Errorf("%s没有完成：%w", strings.Join(failures, "、"), errors.Join(causes...))
	}
	return nil
}
func (m *toolManager) checkUpdates(ctx context.Context) error {
	var failures []error
	for _, a := range []string{"claude", "codex"} {
		m.refreshGUI(ctx, a)
		s := m.state(a)
		var latestCLI, latestGUI string
		if s.CLIInstalled {
			if r, e := m.latest(ctx, a); e == nil {
				latestCLI = r.Version
			} else {
				failures = append(failures, e)
			}
		}
		if s.GUIInstalled {
			if version, e := m.latestGUIVersion(ctx, a); e == nil {
				latestGUI = version
			} else {
				failures = append(failures, e)
			}
		}
		m.recordLatest(a, latestCLI, latestGUI)
	}
	return errors.Join(failures...)
}
func (m *toolManager) launchCLI(agent, secret string) error {
	m.mu.Lock()
	e, ok := m.entries[agent]
	m.mu.Unlock()
	if !ok {
		if !m.cliReady(agent) {
			return errors.New("助手启动检查失败")
		}
		m.mu.Lock()
		e = m.entries[agent]
		m.mu.Unlock()
	}
	return launchToolTerminal(agent, e, secret, m.environment())
}
func (m *toolManager) launchGUI(agent, secret, origin string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	info, e := inspectGUI(ctx, agent)
	if e != nil || info.Path == "" {
		return errors.New("请先安装桌面版")
	}
	if e = configureDesktopGUI(agent, origin, secret); e != nil {
		return e
	}
	env := m.environment()
	if agent == "codex" {
		env = append(env, "JIESHANGAI_CODEX_KEY="+secret)
	}
	return startGUI(info, agent, env)
}
func (m *toolManager) installGUI(ctx context.Context, agent string, update bool, progress func(string)) error {
	m.guiMu.Lock()
	defer m.guiMu.Unlock() // Windows deployments and application replacement are serialized.
	if info, e := inspectGUI(ctx, agent); e == nil && info.Path != "" && !update {
		m.refreshGUI(ctx, agent)
		return nil
	}
	if err := checkGUIDisk(agent, homeDir(), freeDiskBytes); err != nil {
		return err
	}
	if err := m.installGUIPlatform(ctx, agent, update, progress); err != nil {
		return err
	}
	m.refreshGUI(ctx, agent)
	if !m.state(agent).GUIInstalled {
		return errors.New("桌面版安装后未通过检测")
	}
	return nil
}
func (m *toolManager) guiRunning(agent string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	info, err := inspectGUI(ctx, agent)
	return err == nil && info.Path != "" && guiIsRunning(info)
}
func (m *toolManager) managedCLIPath(agent string) string { return filepath.Join(m.root, "bin", agent) }
