package clientruntime

import (
	"context"
	"errors"
	"strings"
	"time"
)

type InstallTask struct {
	Agent     string `json:"agent"`
	Line      string `json:"line"`
	Busy      bool   `json:"busy"`
	Error     string `json:"error,omitempty"`
	Cancelled bool   `json:"cancelled,omitempty"`
}
type desktopInstallation struct {
	cancel context.CancelFunc
	done   chan struct{}
	state  InstallTask
}

var errInstallNetwork = errors.New("installer download unavailable")
var errInstallDependency = errors.New("installer prerequisite unavailable")

type UpdateCheckState struct {
	Checking  bool   `json:"checking"`
	Error     string `json:"error,omitempty"`
	CheckedAt string `json:"checkedAt,omitempty"`
}

func (a *App) updateSnapshot() UpdateCheckState {
	a.updateMu.Lock()
	defer a.updateMu.Unlock()
	return a.toolUpdates
}
func (a *App) startUpdateCheck() {
	if a.env.CheckToolUpdates == nil {
		return
	}
	a.updateMu.Lock()
	if a.toolUpdates.Checking {
		a.updateMu.Unlock()
		return
	}
	a.toolUpdates.Checking = true
	a.toolUpdates.Error = ""
	a.updateMu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		err := a.env.CheckToolUpdates(ctx)
		a.updateMu.Lock()
		defer a.updateMu.Unlock()
		a.toolUpdates.Checking = false
		a.toolUpdates.CheckedAt = time.Now().UTC().Format(time.RFC3339)
		if err != nil {
			a.toolUpdates.Error = "部分更新源暂时无法访问，已保留上次检查结果。"
		}
	}()
}

func (a *App) installTaskSnapshot() []InstallTask {
	a.installMu.Lock()
	defer a.installMu.Unlock()
	var out []InstallTask
	for _, agent := range []string{"claude", "codex"} {
		if op := a.installations[agent]; op != nil {
			out = append(out, op.state)
		}
	}
	return out
}

// Pipe reader cancellation does not wait for the operation queue or a download.
func (a *App) cancelDesktopInstall(agents ...string) {
	a.installMu.Lock()
	defer a.installMu.Unlock()
	for agent, op := range a.installations {
		if len(agents) == 0 || agents[0] == "" || agents[0] == agent {
			if op.state.Busy {
				op.state.Line = "正在取消，已安装的组件会保留……"
			}
			op.cancel()
		}
	}
}
func (a *App) beginInstall(agent string) (context.Context, *desktopInstallation, error) {
	a.installMu.Lock()
	defer a.installMu.Unlock()
	if a.installations == nil {
		a.installations = map[string]*desktopInstallation{}
	}
	if old := a.installations[agent]; old != nil && old.state.Busy {
		return nil, nil, errors.New("这个应用正在安装")
	}
	// A large active transfer can take more than a few minutes. Network stalls
	// are bounded separately by the downloader; the user can cancel at any time.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	op := &desktopInstallation{cancel: cancel, done: make(chan struct{}), state: InstallTask{Agent: agent, Line: "正在准备安装……", Busy: true}}
	a.installations[agent] = op
	return ctx, op, nil
}
func (a *App) taskProgress(op *desktopInstallation, line string) {
	a.installMu.Lock()
	op.state.Line = line
	a.installMu.Unlock()
}
func (a *App) finishInstall(op *desktopInstallation, err error) {
	op.cancel()
	a.installMu.Lock()
	defer a.installMu.Unlock()
	op.state.Busy = false
	switch {
	case errors.Is(err, context.Canceled):
		op.state.Cancelled = true
		op.state.Line = "已取消，已经装好的组件会保留。"
	case err != nil:
		op.state.Error = installErrorMessage(err)
		op.state.Line = "部分组件未完成，已经装好的组件会保留。"
	default:
		op.state.Line = "CLI 和桌面版均已安装；新打开的终端即可输入 claude / codex。"
	}
	close(op.done)
}
func (a *App) startDesktopInstall(agent, component string) error {
	ctx, op, err := a.beginInstall(agent)
	if err != nil {
		return err
	}
	go func() {
		err := a.callDesktopInstaller(ctx, agent, component, func(s string) { a.taskProgress(op, s) })
		a.desktopLocalRefresh()
		a.finishInstall(op, err)
	}()
	return nil
}
func (a *App) callDesktopInstaller(ctx context.Context, agent, component string, progress func(string)) error {
	if a.env.InstallDesktop != nil {
		return a.env.InstallDesktop(ctx, agent, component, progress)
	}
	if component == "cli" {
		return a.env.InstallCLI(agent)
	}
	return errors.New("这个版本暂不支持桌面版自动安装")
}
func (a *App) installDesktopComponent(agent, component string) error {
	ctx, op, err := a.beginInstall(agent)
	if err != nil {
		return err
	}
	progress := func(s string) { a.taskProgress(op, s); v := working(agent, s); v.Cancellable = true; a.setView(v) }
	progress("正在安装 " + agentName(agent) + "……")
	err = a.callDesktopInstaller(ctx, agent, component, progress)
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	a.finishInstall(op, err)
	return err
}
func (a *App) waitForCLIInstall(agent string) error {
	a.installMu.Lock()
	op := a.installations[agent]
	a.installMu.Unlock()
	if op == nil {
		return nil
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		if a.env.FindCLI(agent) {
			return nil
		}
		select {
		case <-op.done:
			if a.env.FindCLI(agent) {
				return nil
			}
			return errors.New("请先完成命令行版本安装")
		case <-ticker.C:
		}
	}
}
func installErrorMessage(err error) string {
	var failure *InstallFailure
	if errors.As(err, &failure) {
		if failure.NativeCode != "" {
			return failure.Message + "（" + failure.NativeCode + "）"
		}
		return failure.Message
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "安装已超时并停止，可以稍后重试。"
	case errors.Is(err, errInstallNetwork):
		return "当前下载源暂时无法访问，请检查网络后重试；不需要反复重开应用。"
	case errors.Is(err, errInstallDependency):
		return "系统依赖或用户安装权限不足，请检查系统提示；已装好的命令行版本仍可使用。"
	default:
		for _, s := range []string{"签名", "校验", "架构", "暂不支持", "请先保存", "管理员", "系统安装", "其他安装器"} {
			if strings.Contains(err.Error(), s) {
				return err.Error()
			}
		}
		return "安装未完成，请检查是否有正在运行的旧版本，或尝试离线安装包。"
	}
}
func desktopInstallFailure(agent, component string, err error) View {
	if errors.Is(err, context.Canceled) {
		return View{Screen: "home", Agent: agent, Notice: "已取消安装，已经装好的组件会保留。"}
	}
	if err == nil {
		err = errors.New("installation did not become runnable")
	}
	return View{Screen: "cli-missing", Agent: agent, Line: installErrorMessage(err), Button: "重试安装", Action: "install-suite", Second: "返回首页", Second2: "home"}
}
func (a *App) desktopLocalRefresh() {
	// Probe outside a.mu: slow process startup must not block snapshots.
	type facts struct {
		agent                 string
		installed, configured bool
	}
	var all []facts
	for _, agent := range []string{"claude", "codex"} {
		_, configured := a.env.Configured(agent, a.origin)
		all = append(all, facts{agent, a.env.FindCLI(agent), configured})
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, f := range all {
		for i := range a.statuses {
			s := &a.statuses[i]
			if s.Agent != f.agent {
				continue
			}
			s.Installed, s.Configured = f.installed, f.configured
			if !s.Installed {
				s.State, s.Badge = "nocli", badgeNoCLI
			} else if s.State == "nocli" {
				s.State, s.Badge = "none", badgeUnconnected
			}
		}
	}
}
