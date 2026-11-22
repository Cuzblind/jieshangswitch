package clientruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

type ToolStatus struct {
	Agent           string           `json:"agent"`
	CLIInstalled    bool             `json:"cliInstalled"`
	GUIInstalled    bool             `json:"guiInstalled"`
	CLIVersion      string           `json:"cliVersion,omitempty"`
	GUIVersion      string           `json:"guiVersion,omitempty"`
	LatestCLI       string           `json:"latestCli,omitempty"`
	LatestGUI       string           `json:"latestGui,omitempty"`
	GUIAvailable    bool             `json:"guiAvailable"`
	Note            string           `json:"note,omitempty"`
	UpdateAvailable bool             `json:"updateAvailable,omitempty"`
	Inspected       bool             `json:"inspected"`    // false until the first CLI probe finished
	GUIInspected    bool             `json:"guiInspected"` // false until the first desktop-app check finished
	CLIInstall      *ComponentStatus `json:"cliInstall,omitempty"`
	GUIInstall      *ComponentStatus `json:"guiInstall,omitempty"`
}

type toolManager struct {
	root, goos, arch string
	origin           string
	download         *toolDownloader
	mu               sync.Mutex
	nodeMu           sync.Mutex
	configOnce       sync.Once
	guiMu            sync.Mutex
	mirrorMu         sync.Mutex
	mirrorCache      map[string]map[string]string
	mirrorCheckedAt  time.Time
	states           map[string]ToolStatus
	entries          map[string]toolEntry
	probed           map[string]time.Time
	probing          map[string]chan struct{} // closed when the running CLI probe ends
	storeUnavailable map[string]time.Time
	node             string
	isolate          bool                                     // Offline preparation never adopts the build machine's installs.
	probeCLI         func(agent string) (toolEntry, cliProbe) // tests replace the --version run
}
type toolEntry struct {
	Program string   `json:"program"`
	Args    []string `json:"args,omitempty"`
	Version string   `json:"version"`
}
type npmRelease struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Dist    struct {
		Tarball   string `json:"tarball"`
		Integrity string `json:"integrity"`
	} `json:"dist"`
	Optional map[string]string `json:"optionalDependencies"`
}

func packageName(agent string) string {
	if agent == "claude" {
		return "@anthropic-ai/claude-code"
	}
	return "@openai/codex"
}
func toolRoot() string { return filepath.Join(homeDir(), ".jieshangai", "tools") }
func newToolManager() *toolManager {
	root := toolRoot()
	m := &toolManager{root: root, goos: runtime.GOOS, arch: runtime.GOARCH, states: map[string]ToolStatus{}, entries: map[string]toolEntry{}, storeUnavailable: map[string]time.Time{}}
	m.download = &toolDownloader{client: newDownloadClient(), cache: filepath.Join(root, "cache"), allow: allowedDownloadURL, slots: make(chan struct{}, 3)}
	for _, a := range []string{"claude", "codex"} {
		m.states[a] = ToolStatus{Agent: a, GUIAvailable: runtime.GOOS == "windows" || runtime.GOOS == "darwin"}
		for _, kind := range []string{"cli", "gui"} {
			var c ComponentStatus
			if data, e := os.ReadFile(filepath.Join(root, a, kind+"-status.json")); e == nil && json.Unmarshal(data, &c) == nil {
				if c.Status == "running" {
					c.Status = "interrupted"
				}
				st := m.states[a]
				if kind == "cli" {
					st.CLIInstall = &c
				} else {
					st.GUIInstall = &c
				}
				m.states[a] = st
			}
		}
		if data, e := os.ReadFile(filepath.Join(root, a, "gui-store-unavailable-until")); e == nil {
			if until, parseErr := time.Parse(time.RFC3339, strings.TrimSpace(string(data))); parseErr == nil && until.After(time.Now()) {
				m.storeUnavailable[a] = until
			}
		}
		var latest struct {
			CLI string `json:"cli"`
			GUI string `json:"gui"`
		}
		var last inspection
		if data, e := os.ReadFile(filepath.Join(root, a, "inspected.json")); e == nil && json.Unmarshal(data, &last) == nil && len(last.CLIVersion) <= 100 && len(last.GUIVersion) <= 100 {
			st := m.states[a]
			st.Inspected = true
			st.CLIInstalled, st.CLIVersion = last.CLIInstalled, last.CLIVersion
			st.GUIInstalled, st.GUIVersion = last.GUIInstalled, last.GUIVersion
			st.GUIInspected = last.GUIInspected
			m.states[a] = st
		}
		if data, e := os.ReadFile(filepath.Join(root, a, "latest.json")); e == nil && json.Unmarshal(data, &latest) == nil && len(latest.CLI) <= 100 && len(latest.GUI) <= 100 {
			st := m.states[a]
			st.LatestCLI, st.LatestGUI = latest.CLI, latest.GUI
			m.states[a] = st
		}
	}
	return m
}
func (m *toolManager) snapshot() []ToolStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []ToolStatus{m.states["claude"], m.states["codex"]}
	for i := range out {
		s := &out[i]
		// An unknown installed version is not an update; it only means the probe has not finished.
		s.UpdateAvailable = (s.CLIVersion != "" && newerVersion(s.LatestCLI, s.CLIVersion)) ||
			(s.GUIVersion != "" && newerVersion(s.LatestGUI, s.GUIVersion))
	}
	return out
}
func (m *toolManager) state(agent string) ToolStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.states[agent]
}
func (m *toolManager) put(s ToolStatus) { m.mu.Lock(); m.states[s.Agent] = s; m.mu.Unlock() }
func (m *toolManager) receipt(agent string) string {
	return filepath.Join(m.root, agent, "active.json")
}
func (m *toolManager) readEntry(agent string) (toolEntry, bool) {
	var e toolEntry
	b, err := os.ReadFile(m.receipt(agent))
	if err != nil || json.Unmarshal(b, &e) != nil {
		return e, false
	}
	if !pathInside(m.root, e.Program) {
		return e, false
	}
	for _, a := range e.Args {
		if filepath.IsAbs(a) && !pathInside(m.root, a) {
			return e, false
		}
	}
	return e, true
}
func pathInside(root, p string) bool {
	rel, e := filepath.Rel(root, p)
	return e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// A probe result, installed or not, is reused this long. Installs and updates
// call forgetCLIProbe so their result shows at once.
const cliProbeTTL = 60 * time.Second

// A probe that timed out is retried this soon; it proves nothing either way.
const cliRetryAfter = 5 * time.Second

// A cold Node start on a slow VM with antivirus can take far longer than the
// old 4s; giving up early turned an installed CLI into "待安装".
const cliVersionTimeout = 20 * time.Second

type cliProbe int

const (
	cliMissing cliProbe = iota
	cliFound
	cliUnknown // timed out: keep the last result
)

func (p cliProbe) String() string {
	return [...]string{"missing", "found", "unknown"}[p]
}

// cliReady keeps the last result visible while a probe runs and lets
// concurrent callers share one `--version` run.
func (m *toolManager) cliReady(agent string) bool {
	m.mu.Lock()
	for {
		s := m.states[agent]
		if at, ok := m.probed[agent]; ok && time.Since(at) < cliProbeTTL {
			m.mu.Unlock()
			return s.CLIInstalled
		}
		wait, running := m.probing[agent]
		if !running {
			break
		}
		m.mu.Unlock()
		<-wait
		m.mu.Lock()
	}
	if m.probing == nil {
		m.probing = map[string]chan struct{}{}
	}
	done := make(chan struct{})
	m.probing[agent] = done
	m.mu.Unlock()

	probe := m.probeCLI
	if probe == nil {
		probe = m.probeCLIProcess
	}
	started := time.Now()
	e, result := probe(agent)
	elapsed := time.Since(started)

	m.mu.Lock()
	if m.probed == nil {
		m.probed = map[string]time.Time{}
	}
	s := m.states[agent]
	if result == cliUnknown {
		m.probed[agent] = time.Now().Add(cliRetryAfter - cliProbeTTL)
	} else {
		m.probed[agent] = time.Now()
		s.Inspected = true
		s.CLIInstalled = result == cliFound
		s.CLIVersion = ""
		if s.CLIInstalled {
			m.entries[agent] = e
			s.CLIVersion = e.Version
		}
	}
	m.states[agent] = s
	delete(m.probing, agent)
	close(done)
	installed := s.CLIInstalled
	m.mu.Unlock()
	m.diagnostic(agent, "cli", "probe", "local", fmt.Sprintf("elapsed_ms=%d result=%s", elapsed.Milliseconds(), result), nil)
	if result != cliUnknown {
		m.saveInspection(agent)
	}
	return installed
}

type inspection struct {
	CLIInstalled bool   `json:"cliInstalled"`
	CLIVersion   string `json:"cliVersion,omitempty"`
	GUIInstalled bool   `json:"guiInstalled"`
	GUIVersion   string `json:"guiVersion,omitempty"`
	GUIInspected bool   `json:"guiInspected"`
}

// saveInspection keeps the last finished probe so the next launch shows it at
// once and refreshes it in the background.
func (m *toolManager) saveInspection(agent string) {
	m.mu.Lock()
	s := m.states[agent]
	data, _ := json.Marshal(inspection{s.CLIInstalled, s.CLIVersion, s.GUIInstalled, s.GUIVersion, s.GUIInspected})
	m.mu.Unlock()
	_ = os.MkdirAll(filepath.Join(m.root, agent), 0700)
	_ = writePrivateFile(filepath.Join(m.root, agent, "inspected.json"), data)
}

// recordLatest keeps the newest versions seen and stores them, so the update
// button survives a restart. Empty values (source unreachable) keep the old ones.
func (m *toolManager) recordLatest(agent, cli, gui string) {
	m.mu.Lock()
	s := m.states[agent]
	if cli != "" {
		s.LatestCLI = cli
	}
	if gui != "" {
		s.LatestGUI = gui
	}
	m.states[agent] = s
	data, _ := json.Marshal(map[string]string{"cli": s.LatestCLI, "gui": s.LatestGUI})
	m.mu.Unlock()
	_ = os.MkdirAll(filepath.Join(m.root, agent), 0700)
	_ = writePrivateFile(filepath.Join(m.root, agent, "latest.json"), data)
}

// forgetCLIProbe makes the next cliReady probe again.
func (m *toolManager) forgetCLIProbe(agent string) {
	m.mu.Lock()
	delete(m.probed, agent)
	m.mu.Unlock()
}

// probeCLIProcess finds the CLI and runs `--version`; it does not touch state.
func (m *toolManager) probeCLIProcess(agent string) (toolEntry, cliProbe) {
	e, ok := m.readEntry(agent)
	// Our own install recorded the version it verified; its program still exists.
	if ok && e.Version != "" {
		if st, err := os.Stat(e.Program); err == nil && !st.IsDir() {
			return e, cliFound
		}
	}
	if !ok && m.isolate {
		return e, cliMissing
	}
	if !ok {
		if p, err := exec.LookPath(agent); err == nil {
			e.Program = p
		} else {
			for _, d := range cliDirs() {
				for _, x := range []string{"", ".exe", ".cmd"} {
					p := filepath.Join(d, agent+x)
					if st, err := os.Stat(p); err == nil && !st.IsDir() {
						e.Program = p
						break
					}
				}
				if e.Program != "" {
					break
				}
			}
		}
	}
	if e.Program == "" {
		return e, cliMissing
	}
	ctx, cancel := context.WithTimeout(context.Background(), cliVersionTimeout)
	defer cancel()
	out, err := runToolCommand(ctx, e.Program, append(append([]string{}, e.Args...), "--version"), m.environment(), false)
	if err != nil {
		if ctx.Err() != nil {
			return e, cliUnknown
		}
		return e, cliMissing
	}
	v := regexp.MustCompile(`[0-9]+\.[0-9]+\.[0-9]+(?:\.[0-9]+)?`).FindString(out)
	if v == "" {
		return e, cliMissing
	}
	if len(v) > 100 {
		v = v[:100]
	}
	e.Version = v
	return e, cliFound
}
func (m *toolManager) environment() []string {
	m.configOnce.Do(func() {
		_ = os.MkdirAll(m.root, 0700)
		_ = writePrivateFile(filepath.Join(m.root, "npmrc"), []byte("registry="+npmMirror+"\n@openai:registry="+npmMirror+"\n@anthropic-ai:registry="+npmMirror+"\n"))
		_ = writePrivateFile(filepath.Join(m.root, "npm-globalrc"), nil)
	})
	env := []string{}
	for _, kv := range cmdEnv() {
		k, _, _ := strings.Cut(kv, "=")
		k = strings.ToUpper(k)
		if k == "PATH" || strings.HasPrefix(k, "ANTHROPIC_") || strings.HasPrefix(k, "OPENAI_") || strings.HasPrefix(k, "RELAY_") || strings.HasPrefix(k, "JIESHANGAI_") || strings.Contains(k, "TOKEN") || strings.Contains(k, "SECRET") {
			continue
		}
		env = append(env, kv)
	}
	m.mu.Lock()
	node := m.node
	m.mu.Unlock()
	extra := filepath.Join(m.root, "bin")
	if node != "" {
		extra = filepath.Dir(node) + string(os.PathListSeparator) + extra
	}
	return append(env, "PATH="+extra+string(os.PathListSeparator)+augmentedPath(), "DISABLE_AUTOUPDATER=1", "npm_config_registry="+npmMirror, "npm_config_cache="+filepath.Join(m.root, "npm-cache"), "npm_config_userconfig="+filepath.Join(m.root, "npmrc"), "npm_config_globalconfig="+filepath.Join(m.root, "npm-globalrc"), "npm_config_maxsockets=3", "npm_config_ignore_scripts=false", "npm_config_include=optional", "npm_config_allow_scripts=@anthropic-ai/claude-code")
}
func (m *toolManager) latest(ctx context.Context, agent string) (npmRelease, error) {
	var r npmRelease
	err := m.download.metadata(ctx, npmMirror+"/"+packageName(agent)+"/latest", &r)
	if err != nil {
		return r, err
	}
	if r.Name != packageName(agent) || !safeVersion(r.Version) || !allowedDownloadURL(r.Dist.Tarball) {
		return r, errors.New("安装源返回了无效的软件信息")
	}
	return r, nil
}
func safeVersion(v string) bool {
	if len(v) == 0 || len(v) > 64 || v[0] < '0' || v[0] > '9' {
		return false
	}
	for _, c := range v {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || strings.ContainsRune(".-+", c)) {
			return false
		}
	}
	return true
}
func (m *toolManager) ensureNode(ctx context.Context, progress func(string)) (string, error) {
	m.nodeMu.Lock()
	defer m.nodeMu.Unlock()
	active := filepath.Join(m.root, "node", "active.json")
	repair := false
	var saved struct {
		Path string `json:"path"`
	}
	if b, e := os.ReadFile(active); e == nil && json.Unmarshal(b, &saved) == nil && pathInside(m.root, saved.Path) {
		if _, e = os.Stat(saved.Path); e == nil {
			probe, cancel := context.WithTimeout(ctx, 4*time.Second)
			out, healthy := runToolCommand(probe, saved.Path, []string{"--version"}, nil, false)
			cancel()
			if healthy == nil && strings.HasPrefix(strings.TrimSpace(out), "v24.") {
				m.mu.Lock()
				m.node = saved.Path
				m.mu.Unlock()
				return saved.Path, nil
			}
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			repair = true
		}
	}
	progress("正在准备私有 Node.js，无需安装系统环境……")
	var releases []struct {
		Version string `json:"version"`
		LTS     any    `json:"lts"`
	}
	// The app selects the latest patch of the compatible LTS major, not "latest".
	if err := m.download.metadata(ctx, "https://npmmirror.com/mirrors/node/index.json", &releases); err != nil {
		return "", err
	}
	v := ""
	for _, r := range releases {
		if strings.HasPrefix(r.Version, "v24.") {
			v = r.Version
			break
		}
	}
	if v == "" || !safeVersion(strings.TrimPrefix(v, "v")) {
		return "", errInstallDependency
	}
	arch := map[string]string{"amd64": "x64", "arm64": "arm64"}[m.arch]
	osname := map[string]string{"windows": "win", "darwin": "darwin", "linux": "linux"}[m.goos]
	if arch == "" || osname == "" {
		return "", errInstallDependency
	}
	format := "tar.gz"
	if m.goos == "windows" {
		format = "zip"
	}
	name := "node-" + v + "-" + osname + "-" + arch + "." + format
	base := "https://npmmirror.com/mirrors/node/" + v + "/"
	// Checksums accompany the mirrored official Node distribution.
	checks, err := m.download.text(ctx, base+"SHASUMS256.txt", 128<<10)
	if err != nil {
		return "", err
	}
	sha := ""
	for _, l := range strings.Split(string(checks), "\n") {
		f := strings.Fields(l)
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			sha = f[0]
		}
	}
	if len(sha) != 64 {
		return "", errors.New("没有找到运行环境校验信息")
	}
	file, err := m.download.file(ctx, base+name, sha, progress)
	if err != nil {
		return "", err
	}
	dest := filepath.Join(m.root, "node", v)
	stage, err := os.MkdirTemp(filepath.Join(m.root, "node"), "stage-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)
	if err = extractToolArchive(file, stage, format); err != nil {
		return "", err
	}
	inside := "node-" + v + "-" + osname + "-" + arch
	program := "bin/node"
	if m.goos == "windows" {
		program = "node.exe"
	}
	if repair {
		if _, e := os.Stat(dest); e == nil {
			if e = os.Rename(dest, dest+".broken-"+fmt.Sprint(time.Now().UnixNano())); e != nil {
				return "", e
			}
		}
	}
	if err = os.Rename(filepath.Join(stage, inside), dest); err != nil {
		if _, e := os.Stat(filepath.Join(dest, filepath.FromSlash(program))); e != nil {
			return "", err
		}
	}
	node := filepath.Join(dest, filepath.FromSlash(program))
	if _, err = runToolCommand(ctx, node, []string{"--version"}, nil, false); err != nil {
		return "", errInstallDependency
	}
	b, _ := json.Marshal(map[string]string{"path": node})
	if err = writePrivateFile(active, b); err != nil {
		return "", err
	}
	m.mu.Lock()
	m.node = node
	m.mu.Unlock()
	return node, nil
}
func (m *toolManager) installCLI(ctx context.Context, agent string, update bool, progress func(string)) error {
	// The user may have installed or removed the CLI elsewhere since the last probe.
	m.forgetCLIProbe(agent)
	if !update && m.cliReady(agent) {
		progress("正在设置命令行全局 PATH……")
		if err := ensurePersistentToolPath(); err != nil {
			return fmt.Errorf("写入命令行 PATH 失败：%w", err)
		}
		progress("命令行版本已安装，正在检查桌面版……")
		return nil
	}
	if !update && !m.isolate {
		if imported, err := m.importOfflineCLI(ctx, agent, progress); imported || err != nil {
			return err
		}
	}
	release, err := m.latest(ctx, agent)
	if err != nil {
		return err
	}
	if update && !newerVersion(release.Version, m.state(agent).CLIVersion) {
		return nil
	}
	if update {
		if _, owned := m.readEntry(agent); !owned && m.cliReady(agent) {
			return errors.New("这个命令行由其他安装器管理，请使用原渠道更新")
		}
	}
	if err = os.MkdirAll(filepath.Join(m.root, "node"), 0700); err != nil {
		return err
	}
	node, err := m.ensureNode(ctx, progress)
	if err != nil {
		return err
	}
	npm := filepath.Join(filepath.Dir(node), "node_modules", "npm", "bin", "npm-cli.js")
	if m.goos != "windows" {
		npm = filepath.Join(filepath.Dir(node), "..", "lib", "node_modules", "npm", "bin", "npm-cli.js")
	}
	base := filepath.Join(m.root, agent)
	if err = os.MkdirAll(base, 0700); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(base, "install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	progress("正在从国内镜像安装 " + agentName(agent) + " 命令行版本……")
	args := []string{npm, "install", "--global", "--prefix", stage, "--registry", npmMirror, "--ignore-scripts=false", "--include=optional", "--no-audit", "--no-fund", "--fetch-retries=1", "--fetch-timeout=30000", packageName(agent) + "@" + release.Version}
	out, commandErr := runToolCommand(ctx, node, args, m.environment(), true)
	m.diagnostic(agent, "cli", "npm", "npmmirror", out, commandErr)
	if err = commandErr; err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return processFailure("npm", "npmmirror", out, err)
	}
	entry, err := npmEntry(stage, node, agent, m.goos)
	if err != nil {
		return err
	}
	verifyOut, verifyErr := runToolCommand(ctx, entry.Program, append(append([]string{}, entry.Args...), "--version"), m.environment(), false)
	m.diagnostic(agent, "cli", "verify", "npmmirror", verifyOut, verifyErr)
	if verifyErr != nil {
		return processFailure("verify", "npmmirror", verifyOut, verifyErr)
	}
	dest, err := os.MkdirTemp(base, release.Version+"-")
	if err != nil {
		return err
	}
	if err = os.Remove(dest); err != nil {
		return err
	}
	if err = os.Rename(stage, dest); err != nil {
		return err
	}
	entry, err = npmEntry(dest, node, agent, m.goos)
	if err != nil {
		return err
	}
	entry.Version = release.Version
	b, _ := json.Marshal(entry)
	if err = writePrivateFile(m.receipt(agent), b); err != nil {
		return err
	}
	m.mu.Lock()
	m.entries[agent] = entry
	s := m.states[agent]
	s.CLIInstalled = true
	s.CLIVersion = release.Version
	s.Inspected = true
	m.states[agent] = s
	if m.probed == nil {
		m.probed = map[string]time.Time{}
	}
	m.probed[agent] = time.Now()
	m.mu.Unlock()
	if err := m.writeLauncher(agent, entry); err != nil {
		return err
	}
	progress("正在设置命令行全局 PATH……")
	if err := ensurePersistentToolPath(); err != nil {
		return fmt.Errorf("写入命令行 PATH 失败：%w", err)
	}
	return nil
}
func npmEntry(prefix, node, agent, goos string) (toolEntry, error) {
	root := filepath.Join(prefix, "node_modules", filepath.FromSlash(packageName(agent)))
	if goos != "windows" {
		root = filepath.Join(prefix, "lib", "node_modules", filepath.FromSlash(packageName(agent)))
	}
	b, e := os.ReadFile(filepath.Join(root, "package.json"))
	if e != nil {
		return toolEntry{}, e
	}
	var pkg struct {
		Bin json.RawMessage `json:"bin"`
	}
	if e = json.Unmarshal(b, &pkg); e != nil {
		return toolEntry{}, e
	}
	var bin string
	if json.Unmarshal(pkg.Bin, &bin) != nil {
		var bins map[string]string
		if e = json.Unmarshal(pkg.Bin, &bins); e != nil {
			return toolEntry{}, e
		}
		bin = bins[agent]
	}
	p, e := archivePath(root, bin)
	if e != nil || bin == "" {
		return toolEntry{}, errors.New("无效的命令行入口")
	}
	if strings.HasSuffix(p, ".js") {
		return toolEntry{Program: node, Args: []string{p}}, nil
	}
	return toolEntry{Program: p}, nil
}
func (m *toolManager) writeLauncher(agent string, e toolEntry) error {
	d := filepath.Join(m.root, "bin")
	if err := os.MkdirAll(d, 0700); err != nil {
		return err
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	line := "#!/bin/sh\nexec " + quote(e.Program)
	m.mu.Lock()
	node := m.node
	m.mu.Unlock()
	if node != "" {
		line = "#!/bin/sh\nexport PATH=" + quote(filepath.Dir(node)) + ":\"$PATH\"\nexec " + quote(e.Program)
	}
	for _, a := range e.Args {
		line += " " + quote(a)
	}
	line += " \"$@\"\n"
	name := agent
	if m.goos == "windows" {
		name += ".cmd"
		line = "@echo off\r\n\"" + e.Program + "\""
		if node != "" {
			line = "@echo off\r\nset \"PATH=" + filepath.Dir(node) + ";%PATH%\"\r\n\"" + e.Program + "\""
		}
		for _, a := range e.Args {
			line += " \"" + a + "\""
		}
		line += " %*\r\n"
	}
	return os.WriteFile(filepath.Join(d, name), []byte(line), 0700)
}

// metadata text has the same fixed host/redirect policy as binary transfers.
func (d *toolDownloader) text(ctx context.Context, raw string, limit int64) ([]byte, error) {
	if !d.allow(raw) {
		return nil, errors.New("download source rejected")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	r, _ := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	res, e := d.client.Do(r)
	if e != nil {
		return nil, errInstallNetwork
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, errInstallNetwork
	}
	return io.ReadAll(io.LimitReader(res.Body, limit))
}
