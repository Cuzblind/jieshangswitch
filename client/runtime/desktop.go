package clientruntime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// TestOrigin is set at link time by explicitly labelled test builds only.
// Neither the browser, deep links nor environment variables can set an origin.
var TestOrigin string

const ProductionOrigin = "https://jieshangai.com"

type DesktopRequest struct {
	ID      string `json:"id"`
	Action  string `json:"action"`
	Agent   string `json:"agent,omitempty"`
	Ticket  string `json:"ticket,omitempty"`
	Confirm bool   `json:"confirm,omitempty"`
	// Prompt management fields. PromptID also carries the MCP ID.
	PromptID string `json:"promptId,omitempty"`
	Name     string `json:"name,omitempty"`
	Content  string `json:"content,omitempty"`
	// MCP management: create/edit carries the whole server, the rest only its ID.
	MCP *MCPServer `json:"mcp,omitempty"`
	// SkillDir names a skill folder; it can be longer than an ID.
	SkillDir string `json:"skillDir,omitempty"`
	// SkillZip is filled by the native file picker for skill-import-zip only.
	SkillZip string `json:"skillZip,omitempty"`
	// Tier names the target routing line (economy / balanced / stable) for switch-tier.
	Tier string `json:"tier,omitempty"`
	// Range selects the usage time window (24h / 7d / 30d / 90d) for usage-load.
	Range string `json:"range,omitempty"`
}

type DesktopState struct {
	View         View                              `json:"view"`
	Statuses     []AgentStatus                     `json:"statuses"`
	Balance      *Balance                          `json:"balance,omitempty"`
	AccountLabel string                            `json:"accountLabel,omitempty"`
	Workspace    *WorkspaceInfo                    `json:"workspace,omitempty"`
	Today        *TodayUsageSummary                `json:"today,omitempty"`
	Usage        *UsageSnapshot                    `json:"usage,omitempty"`
	Catalogs     map[string]*DesktopCatalogPayload `json:"catalogs,omitempty"`
	Version      string                            `json:"version"`
	OS           string                            `json:"os"`
	Authorized   bool                              `json:"authorized"`
	Support      bool                              `json:"support"`
	QQ           string                            `json:"qq,omitempty"`
	Email        string                            `json:"email,omitempty"`
	Report       string                            `json:"report,omitempty"`
	Tools        []ToolStatus                      `json:"tools,omitempty"`
	InstallTasks []InstallTask                     `json:"installTasks,omitempty"`
	ToolUpdates  UpdateCheckState                  `json:"toolUpdates"`
	Prompts      map[string][]Prompt               `json:"prompts,omitempty"`
	MCP          map[string][]MCPServer            `json:"mcp,omitempty"`
	Skills       map[string][]Skill                `json:"skills,omitempty"`
}

type desktopReply struct {
	ID    string        `json:"id,omitempty"`
	State *DesktopState `json:"state,omitempty"`
	Error string        `json:"error,omitempty"`
}

func (a *App) desktopSuccess(agent string) {
	if a.getView().Screen != "success" {
		return
	}
	v := View{Screen: "home", Agent: agent, Notice: agentName(agent) + " 已经接上，可以打开使用了。"}
	if a.env.Running(agent) {
		v.Note = "助手还在运行。请先保存任务并关闭它，再打开以使用新的设置。"
	}
	a.setView(v)
}

func DesktopEnv() (Env, error) {
	origin := ProductionOrigin
	if TestOrigin != "" {
		u, err := url.Parse(TestOrigin)
		if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "http" && u.Scheme != "https") {
			return Env{}, errors.New("invalid test build origin")
		}
		origin = strings.TrimSuffix(TestOrigin, "/")
	}
	e := baseEnv()
	e.Origin, e.Web = origin, origin
	tools := newToolManager()
	e.FindCLI = tools.cliReady
	e.InstallDesktop = tools.install
	e.InstallCLI = func(agent string) error { return tools.installCLI(context.Background(), agent, false, func(string) {}) }
	e.ToolSnapshot, e.InspectTools, e.CheckToolUpdates = tools.snapshot, tools.inspect, tools.checkUpdates
	e.InstallDiagnostics = tools.diagnostics
	e.Launch, e.LaunchGUI = tools.launchCLI, tools.launchGUI
	e.Edited = func() bool { return editedSinceWrite() || desktopGUIEdited() }
	e.Restore = func(confirm bool) (bool, error) {
		if _, err := readGUIBackups(); err != nil {
			return false, err
		}
		used, err := engineRestore(confirm)
		if err == nil {
			err = removePersistentToolPath()
		}
		if err == nil {
			err = restoreDesktopGUI()
		}
		return used, err
	}
	e.HTTP = &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || req.URL.Scheme+"://"+req.URL.Host != origin {
			return errors.New("redirect rejected")
		}
		return nil
	}}
	// Only this compiled address may reach the shared writer.
	e.Write = func(agent, target, secret string) error {
		if !validAgent(agent) || target != origin {
			return errors.New("connection rejected")
		}
		return engineWrite(agent, target, secret)
	}
	return e, nil
}

// RestoreForUninstall runs when the user asks the uninstaller to also put their
// assistants back. It keeps the desktop authorization logic out of the way: the
// connection is local state, and revoking it online stays in the account page.
func RestoreForUninstall() error {
	env, err := DesktopEnv()
	if err != nil {
		return err
	}
	return NewDesktop(env, "").restoreAll()
}

func (a *App) restoreAll() error {
	if _, err := a.restoreConnections(); err != nil {
		return err
	}
	if err := clearMCP(); err != nil {
		return err
	}
	return clearSkills()
}

func NewDesktop(env Env, appVersion string) *App {
	a := newApp(env)
	a.desktop = true
	a.desktopStates = make(map[string]AgentStatus)
	a.seenTickets = make(map[string]bool)
	a.appVersion = appVersion
	return a
}

func (a *App) DesktopState() DesktopState {
	s := a.snapshot()
	var tools []ToolStatus
	if a.env.ToolSnapshot != nil {
		tools = a.env.ToolSnapshot()
	}
	a.mu.Lock()
	accountLabel := a.accountLabel
	var workspace *WorkspaceInfo
	if a.workspace != nil {
		cp := *a.workspace
		workspace = &cp
	}
	var today *TodayUsageSummary
	if a.today != nil {
		cp := *a.today
		today = &cp
	}
	usage := a.usage
	var catalogs map[string]*DesktopCatalogPayload
	if len(a.catalogs) > 0 {
		catalogs = make(map[string]*DesktopCatalogPayload, len(a.catalogs))
		for k, v := range a.catalogs {
			catalogs[k] = v
		}
	}
	a.mu.Unlock()
	return DesktopState{
		View: s.View, Statuses: s.Statuses, Balance: s.Balance,
		AccountLabel: accountLabel, Workspace: workspace, Today: today, Usage: usage, Catalogs: catalogs,
		Version: s.Version, OS: s.OS, Authorized: s.Authorized, Support: s.Support,
		QQ: supportQQ(), Email: supportEmail(), Report: a.supportReport(), Tools: tools, InstallTasks: a.installTaskSnapshot(), ToolUpdates: a.updateSnapshot(), Prompts: promptSnapshot(), MCP: mcpSnapshot(), Skills: skillSnapshot(),
	}
}

func (a *App) desktopRefresh() {
	if a.env.InspectTools != nil {
		a.env.InspectTools()
	}
	a.refreshStatus(true)
	a.mu.Lock()
	for i := range a.statuses {
		live := a.statuses[i]
		if live.State == "ok" && a.env.OfficialLogin(live.Agent) {
			live.State, live.Badge = "conflict", "需要修复登录"
			a.statuses[i] = live
		}
		if checked, ok := a.desktopStates[live.Agent]; ok && live.State == "ok" {
			checked.Installed, checked.Configured, checked.Running = live.Installed, live.Configured, live.Running
			checked.Tier, checked.Tiers = live.Tier, live.Tiers
			a.statuses[i] = checked
		}
	}

	a.mu.Unlock()
}

func (a *App) desktopCheck(agent string) error {
	secret, ok := a.env.Configured(agent, a.origin)
	if !ok {
		return errExpired
	}
	err := a.check(secret)
	st := AgentStatus{Agent: agent, Name: agentName(agent), State: "ok", Badge: "能用了", Installed: a.env.FindCLI(agent), Configured: ok, Running: a.env.Running(agent)}
	if err != nil {
		st.State, st.Badge = "offline", "暂时连不上"
		if errors.Is(err, errExpired) {
			st.State, st.Badge = "expired", "需要重新接上"
		}
	} else if a.env.OfficialLogin(agent) {
		st.State, st.Badge = "conflict", "需要修复登录"
	}
	a.mu.Lock()
	a.desktopStates[agent] = st
	a.mu.Unlock()
	return err
}

// DesktopAction is called by a single worker. Only explicitly enumerated
// operations are available; there is no generic process or filesystem API.
func (a *App) DesktopAction(r DesktopRequest) error {
	switch r.Action {
	case "refresh", "home", "cancel-install", "check-updates", "restore", "restore-confirm", "support", "recharge", "open-web", "usage-load", "switch-account":
	default:
		if !validAgent(r.Agent) {
			return errors.New("请选择 Claude Code 或 Codex")
		}
	}
	switch r.Action {
	case "refresh":
		// Runs behind the cards: they keep their last state until the new one is in.
		for _, agent := range []string{"claude", "codex"} {
			if _, configured := a.env.Configured(agent, a.origin); configured {
				_ = a.desktopCheck(agent)
			}
		}
		a.desktopRefresh()
	case "home":
		a.setView(View{Screen: "home"})
	case "cancel-install":
		a.cancelDesktopInstall(r.Agent)
	case "install-suite":
		a.setView(View{Screen: "home"})
		return a.startDesktopInstall(r.Agent, "all")
	case "update-tool":
		if a.env.Running(r.Agent) {
			return errors.New("请先保存任务并关闭这个助手，再更新")
		}
		a.setView(View{Screen: "home"})
		return a.startDesktopInstall(r.Agent, "update")
	case "check-updates":
		a.startUpdateCheck()
	case "open-web", "switch-account":
		agent := r.Agent
		if !validAgent(agent) {
			agent = "claude"
		}
		if err := a.env.Open(a.web() + "/download?agent=" + agent + "&continue=1"); err != nil {
			return errors.New("没能打开浏览器，请访问 jieshangai.com")
		}
	case "usage-load":
		_, err := a.fetchUsage(r.Range)
		return err
	case "catalog-load":
		secret, ok := a.env.Configured(r.Agent, a.origin)
		if !ok || strings.TrimSpace(secret) == "" {
			a.mu.Lock()
			if a.catalogs != nil {
				delete(a.catalogs, r.Agent)
			}
			a.mu.Unlock()
			return nil
		}
		cat, err := a.fetchDesktopCatalog(secret)
		if err != nil {
			if errors.Is(err, errExpired) {
				return errors.New("连接凭证已失效，请回首页重新接上后再看可用模型")
			}
			return errors.New("暂时读不到可用模型清单，请稍后再试")
		}
		a.mu.Lock()
		if a.catalogs == nil {
			a.catalogs = make(map[string]*DesktopCatalogPayload)
		}
		a.catalogs[r.Agent] = cat
		a.mu.Unlock()
	case "recharge":
		if err := a.env.Open(a.web() + "/account"); err != nil {
			return errors.New("没能打开浏览器，请访问 jieshangai.com")
		}
	case "support":
		if !hasSupport() {
			return errors.New("暂时没有可用的客服联系方式")
		}
		a.act("support", "claude")
	case "prompts-list", "prompt-save", "prompt-enable", "prompt-delete":
		var list []Prompt
		var err error
		switch r.Action {
		case "prompts-list":
			list, err = ListPrompts(r.Agent)
		case "prompt-save":
			list, err = SavePrompt(r.Agent, r.PromptID, r.Name, r.Content)
		case "prompt-enable":
			list, err = EnablePrompt(r.Agent, r.PromptID)
		default:
			list, err = DeletePrompt(r.Agent, r.PromptID)
		}
		if err != nil {
			return err
		}
		cachePrompts(r.Agent, list)
	case "skills-list", "skill-import", "skill-import-zip", "skill-enable", "skill-disable", "skill-delete":
		var list []Skill
		var err error
		switch r.Action {
		case "skill-import-zip":
			list, err = ImportSkillZip(r.Agent, r.SkillZip)
		case "skills-list":
			list, err = ListSkills(r.Agent)
		case "skill-import":
			list, err = ImportSkill(r.Agent, r.SkillDir)
		case "skill-enable", "skill-disable":
			list, err = SetSkillEnabled(r.Agent, r.SkillDir, r.Action == "skill-enable")
		default:
			list, err = DeleteSkill(r.Agent, r.SkillDir)
		}
		if err != nil {
			return err
		}
		cacheSkills(r.Agent, list)
	case "mcp-list", "mcp-save", "mcp-enable", "mcp-disable", "mcp-delete":
		// Claude Code rewrites ~/.claude.json from memory while it runs and would
		// drop the change, so edits wait until it is closed.
		if r.Action != "mcp-list" && r.Agent == "claude" && a.env.Running("claude") {
			return errors.New("请先关闭 Claude Code，再修改 MCP 服务")
		}
		var list []MCPServer
		var err error
		switch r.Action {
		case "mcp-list":
			list, err = ListMCP(r.Agent)
		case "mcp-save":
			if r.MCP == nil {
				return errors.New("请填写 MCP 服务")
			}
			list, err = SaveMCP(r.Agent, *r.MCP)
		case "mcp-enable", "mcp-disable":
			list, err = SetMCPEnabled(r.Agent, r.PromptID, r.Action == "mcp-enable")
		default:
			list, err = DeleteMCP(r.Agent, r.PromptID)
		}
		if err != nil {
			return err
		}
		cacheMCP(r.Agent, list)
	case "connect", "connect-ticket":
		if r.Action == "connect-ticket" {
			if !reTicket.MatchString(r.Ticket) {
				return errors.New("连接无效，请回网页重新接上")
			}
			if a.seenTickets[r.Ticket] {
				return nil
			}
			a.seenTickets[r.Ticket] = true
			a.setView(connecting(r.Agent, 1))
			a.connect(r.Agent, r.Ticket)
			a.mu.Lock()
			claimed := a.current.ticket == r.Ticket && a.current.secret != ""
			a.mu.Unlock()
			if !claimed {
				delete(a.seenTickets, r.Ticket)
			}
		} else if a.hasClientAuth() {
			a.setView(connecting(r.Agent, 1))
			a.connectFromAuth(r.Agent)
		} else {
			a.setView(errorView("reauth", r.Agent))
		}
		_ = a.desktopCheck(r.Agent)
		if secret, ok := a.env.Configured(r.Agent, a.origin); ok {
			a.syncDesktopCatalog(r.Agent, secret)
		}
		a.desktopSuccess(r.Agent)
		a.desktopRefresh()
	case "install":
		err := a.installDesktopComponent(r.Agent, "cli")
		a.desktopLocalRefresh()
		if err != nil || !a.env.FindCLI(r.Agent) {
			a.setView(desktopInstallFailure(r.Agent, "cli", err))
			return nil
		}
		a.setView(View{Screen: "home", Notice: "安装好了，现在可以接上。"})
	case "launch":
		if !a.env.FindCLI(r.Agent) {
			return errors.New("请先安装这个助手")
		}
		if _, ok := a.env.Configured(r.Agent, a.origin); !ok {
			return errors.New("请先接上这个助手")
		}
		if a.env.Running(r.Agent) {
			a.setView(View{Screen: "notice", Agent: r.Agent, Line: "请先保存正在进行的任务，并关闭这个助手。接上AI不会替你关闭任务。", Button: "已关闭，打开助手", Action: "launch"})
			return nil
		}
		if a.env.OfficialLogin(r.Agent) {
			return errors.New("请先修复这个助手的登录状态，再打开助手")
		}
		if err := a.env.Launch(r.Agent, a.secretFor(r.Agent)); err != nil {
			return errors.New("没能打开助手，请再试一次或联系客服")
		}
		a.setView(View{Screen: "home", Agent: r.Agent, Notice: "已请求打开 " + agentName(r.Agent) + "，请在新窗口中继续。"})
	case "launch-gui":
		secret, ok := a.env.Configured(r.Agent, a.origin)
		if !ok {
			return errors.New("请先接上这个助手")
		}
		if a.env.LaunchGUI == nil {
			return errors.New("这个版本暂不支持桌面版启动")
		}
		a.syncDesktopCatalog(r.Agent, secret)
		if err := a.env.LaunchGUI(r.Agent, secret, a.origin); err != nil {
			return err
		}
		a.setView(View{Screen: "home", Agent: r.Agent, Notice: "已请求打开官方桌面版，请在新窗口中继续。"})
	case "check":
		a.setView(working(r.Agent, "正在检查连接……"))
		err := a.desktopCheck(r.Agent)
		if err != nil {
			a.fail(r.Agent, err, "connection-test-failed", "")
		} else if a.env.OfficialLogin(r.Agent) {
			a.setView(errorView("conflict", r.Agent))
		} else {
			a.setView(View{Screen: "home", Notice: "连接检查通过。"})
		}
		a.desktopRefresh()
	case "test-chat":
		a.setView(working(r.Agent, "正在发送真实测试消息……"))
		usedModel, elapsed, err := a.testChat(r.Agent)
		if err != nil {
			a.setView(View{Screen: "home"})
			return err
		}
		sec := elapsed.Seconds()
		if sec < 0.1 {
			sec = 0.1
		}
		a.setView(View{
			Screen: "home",
			Agent:  r.Agent,
			Notice: fmt.Sprintf("对话已通（%s · %.1fs），打开 %s 就能直接发话。", usedModel, sec, agentName(r.Agent)),
		})
		a.desktopRefresh()
	case "switch-tier":
		label, err := a.switchTier(r.Agent, r.Tier)
		if err != nil {
			return err
		}
		if secret, ok := a.env.Configured(r.Agent, a.origin); ok {
			a.syncDesktopCatalog(r.Agent, secret)
		}
		a.desktopRefresh()
		a.setView(View{Screen: "home", Agent: r.Agent, Notice: "已将 " + agentName(r.Agent) + " 切到「" + label + "」线路，下一个任务生效。"})
	case "repair", "logout-confirm":
		previous := a.getView()
		a.setView(working(r.Agent, "正在修复 "+agentName(r.Agent)+"……"))
		if a.env.OfficialLogin(r.Agent) {
			if r.Action != "logout-confirm" || !r.Confirm || previous.Screen != "confirm" || previous.Action != "logout-confirm" || previous.Agent != r.Agent {
				a.setView(View{Screen: "confirm", Agent: r.Agent, Line: "修复需要退出这个助手的官方账号。你的官方订阅不会改变。", Button: "确认退出并修复", Action: "logout-confirm", Second: "先不", Second2: "home"})
				return nil
			}
			if err := a.env.Logout(r.Agent); err != nil {
				return errors.New("没能退出官方账号，请手动退出后重试")
			}
		}
		secret, ok := a.env.Configured(r.Agent, a.origin)
		checkErr := errExpired
		if ok {
			checkErr = a.desktopCheck(r.Agent)
		}
		if errors.Is(checkErr, errNetwork) {
			a.fail(r.Agent, checkErr, "connection-test-failed", "")
			return nil
		}
		if ok && checkErr == nil {
			a.mu.Lock()
			a.current = session{agent: r.Agent, secret: secret}
			a.mu.Unlock()
			a.finish(r.Agent, true)
		} else if a.hasClientAuth() {
			a.connectFromAuth(r.Agent)
		} else {
			a.setView(errorView("reauth", r.Agent))
		}
		_ = a.desktopCheck(r.Agent)
		if secret, ok := a.env.Configured(r.Agent, a.origin); ok {
			a.syncDesktopCatalog(r.Agent, secret)
		}
		a.desktopSuccess(r.Agent)
		a.desktopRefresh()
	case "restore":
		v := View{Screen: "confirm", Line: "这会恢复 Claude 和 Codex 命令行及桌面版的接入设置，并停用接上AI添加的 MCP 服务和 Skills，不会删除助手或你的文件。", Button: "全部恢复", Action: "restore-confirm", Second: "先不", Second2: "home", Note: "只恢复这台电脑。网上停用连接请前往我的账户。"}
		if a.env.Edited() {
			v.Extra = "检测到你接上后又修改过设置；继续会还原这些文件。"
		}
		a.setView(v)
	case "restore-confirm":
		if !r.Confirm || a.getView().Screen != "confirm" || a.getView().Action != "restore-confirm" {
			return errors.New("请先确认全部恢复设置")
		}
		a.setView(working("", "正在恢复设置……"))
		if _, err := a.restoreConnections(); err != nil {
			return errors.New("没能完整恢复，请联系客服")
		}
		if err := clearMCP(); err != nil {
			return errors.New("接入设置已恢复，但没能移除接上AI添加的 MCP 服务：" + err.Error())
		}
		if err := clearSkills(); err != nil {
			return errors.New("接入设置已恢复，但没能停用接上AI添加的 Skills：" + err.Error())
		}
		for _, agent := range []string{"claude", "codex"} {
			if list, err := ListMCP(agent); err == nil {
				cacheMCP(agent, list)
			}
			if list, err := ListSkills(agent); err == nil {
				cacheSkills(agent, list)
			}
		}
		a.mu.Lock()
		a.current = session{}
		a.balance = nil
		a.walletAgent = ""
		a.accountLabel = ""
		a.workspace = nil
		a.today = nil
		a.usage = nil
		a.catalogs = nil
		a.desktopStates = make(map[string]AgentStatus)
		a.mu.Unlock()
		a.setView(View{Screen: "home", Notice: "两个助手的接上AI设置已经恢复。"})
		a.desktopRefresh()
	default:
		return errors.New("这个操作不可用")
	}
	return nil
}

// listOnly reports the read-only list actions that bypass the serial worker.
func listOnly(action string) bool {
	switch action {
	case "prompts-list", "mcp-list", "skills-list", "usage-load", "catalog-load":
		return true
	}
	return false
}

// ServeDesktop exposes JSON Lines over private pipes, never over HTTP.
// EOF terminates the process; writes are serialized, snapshots remain readable
// while the worker installs a CLI or waits on the gateway.
func (a *App) ServeDesktop(input io.Reader, output io.Writer) error {
	var outputMu sync.Mutex
	write := func(reply desktopReply) {
		outputMu.Lock()
		defer outputMu.Unlock()
		_ = json.NewEncoder(output).Encode(reply)
	}
	emit := func(id, message string) {
		s := a.DesktopState()
		write(desktopReply{ID: id, State: &s, Error: message})
	}
	jobs := make(chan DesktopRequest, 8)
	done := make(chan struct{})
	defer close(done)
	defer func() {
		a.cancelDesktopInstall()
		a.installMu.Lock()
		var pending []chan struct{}
		for _, op := range a.installations {
			if op.state.Busy {
				pending = append(pending, op.done)
			}
		}
		a.installMu.Unlock()
		timer := time.NewTimer(1500 * time.Millisecond)
		defer timer.Stop()
		for _, task := range pending {
			select {
			case <-task:
			case <-timer.C:
				return
			}
		}
	}()
	go func() {
		for {
			select {
			case <-done:
				return
			case r := <-jobs:
				err := a.DesktopAction(r)
				message := ""
				if err != nil {
					message = err.Error()
					a.setView(View{Screen: "error", Agent: r.Agent, Line: message, Button: "返回首页", Action: "home"})
				}
				emit(r.ID, message)
			}
		}
	}()
	go func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		var previous []byte
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				s := a.DesktopState()
				raw, _ := json.Marshal(s)
				if !bytes.Equal(raw, previous) {
					write(desktopReply{State: &s})
					previous = raw
				}
			}
		}
	}()
	for _, agent := range []string{"claude", "codex"} {
		_, configured := a.env.Configured(agent, a.origin)
		installed := a.env.FindCLI(agent)
		st := AgentStatus{Agent: agent, Name: agentName(agent), Installed: installed, Configured: configured, State: "none", Badge: badgeUnconnected}
		if !installed {
			st.State, st.Badge = "nocli", badgeNoCLI
		} else if configured {
			st.State, st.Badge = "checking", "正在检查"
		}
		a.mu.Lock()
		a.statuses = append(a.statuses, st)
		a.mu.Unlock()
	}
	emit("", "")
	jobs <- DesktopRequest{Action: "refresh"}
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 64*1024)
	for scanner.Scan() {
		var r DesktopRequest
		d := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
		d.DisallowUnknownFields()
		if err := d.Decode(&r); err != nil {
			write(desktopReply{Error: "请求格式无效"})
			continue
		}
		var extra any
		if err := d.Decode(&extra); err != io.EOF || len(r.ID) == 0 || len(r.ID) > 64 {
			write(desktopReply{Error: "请求格式无效"})
			continue
		}
		if r.Action == "state" {
			emit(r.ID, "")
			continue
		}
		if r.Action == "cancel-install" {
			if r.Agent != "" && !validAgent(r.Agent) {
				write(desktopReply{ID: r.ID, Error: "应用无效"})
				continue
			}
			a.cancelDesktopInstall(r.Agent)
			emit(r.ID, "")
			continue
		}
		// List reads never wait behind the serial worker (the startup refresh
		// can take seconds); their own locks keep them consistent with writes.
		if listOnly(r.Action) {
			go func(r DesktopRequest) {
				message := ""
				if err := a.DesktopAction(r); err != nil {
					message = err.Error()
				}
				emit(r.ID, message)
			}(r)
			continue
		}
		select {
		case jobs <- r:
		default:
			write(desktopReply{ID: r.ID, Error: "操作正在进行，请稍后再试"})
		}
	}
	return scanner.Err()
}
