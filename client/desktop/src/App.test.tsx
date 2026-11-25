import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import type { DesktopState } from "./lib/desktop";

const api = vi.hoisted(() => ({
  state: vi.fn(),
  action: vi.fn(),
  subscribe: vi.fn(),
  unlisten: vi.fn(),
  appCheck: vi.fn(),
  appInstall: vi.fn(),
  appCancel: vi.fn(),
  appProgress: vi.fn(),
  switchTier: vi.fn(),
  usage: vi.fn(),
  catalog: vi.fn(),
  setWorkDir: vi.fn(),
}));
vi.mock("./lib/desktop", async (original) => ({
  ...(await original<typeof import("./lib/desktop")>()),
  desktopState: api.state,
  desktopAction: api.action,
  desktopSwitchTier: api.switchTier,
  desktopUsage: api.usage,
  desktopCatalog: api.catalog,
  desktopSetWorkDir: api.setWorkDir,
  onDesktopState: api.subscribe,
  checkAppUpdate: api.appCheck,
  applyAppUpdate: api.appInstall,
  cancelAppUpdate: api.appCancel,
  onAppUpdateProgress: api.appProgress,
}));

const fixture = (): DesktopState => ({
  view: { screen: "home" },
  statuses: [
    { agent: "claude", name: "Claude Code", state: "ok", badge: "能用了" },
    { agent: "codex", name: "Codex", state: "none", badge: "还没接" },
  ],
  version: "0.2.0-beta.1",
  os: "Windows",
  authorized: true,
  support: true,
  qq: "12345678",
  balance: { text: "余额 ¥12.30", low: false },
  engineReady: true,
});

beforeEach(() => {
  vi.resetAllMocks();
  api.state.mockResolvedValue(fixture());
  api.subscribe.mockResolvedValue(api.unlisten);
  api.action.mockResolvedValue(fixture());
  api.appCheck.mockResolvedValue({ available: false });
  api.appInstall.mockResolvedValue(undefined);
  api.appCancel.mockResolvedValue(undefined);
  api.appProgress.mockResolvedValue(() => {});
  api.usage.mockResolvedValue(fixture());
  api.catalog.mockResolvedValue(fixture());
  localStorage.clear();
  // Automatic checks have dedicated HomeStability coverage. Their generic
  // fixture response must not replace Usage mid-interaction on slower runners.
  localStorage.setItem("jieshangai:tool-update-check", String(Date.now()));
  localStorage.setItem("jieshangai:client-update-check", String(Date.now()));
});

describe("desktop assistant flow", () => {
  it("cancels a client update and closes its dialog while downloading", async () => {
    api.appCheck.mockResolvedValue({ available: true, version: "0.2.1" });
    api.appInstall.mockReturnValue(new Promise(() => {}));
    render(<App />);
    await userEvent.click(await screen.findByRole("button", { name: "设置" }));
    await userEvent.click(await screen.findByRole("button", { name: "检查客户端更新" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("button", { name: "下载并更新" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "取消更新并关闭" }));
    expect(api.appCancel).toHaveBeenCalledOnce();
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    await userEvent.click(screen.getByRole("button", { name: "首页" }));
    expect(screen.getByRole("button", { name: "打开命令行 · Claude Code" })).toBeEnabled();
  });
  it("installs CLI and desktop together without a component chooser", async () => {
    api.state.mockResolvedValue({
      ...fixture(),
      tools: [
        {
          agent: "codex",
          cliInstalled: false,
          guiInstalled: false,
          guiAvailable: true,
        },
      ],
    });
    render(<App />);
    await userEvent.click(
      await screen.findByRole("button", { name: "安装应用 · Codex" }),
    );
    expect(api.action).toHaveBeenCalledWith("install-suite", "codex", false);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("keeps the other assistant available while an installation can be cancelled", async () => {
    api.state.mockResolvedValue({
      ...fixture(),
      installTasks: [{ agent: "codex", busy: true, line: "正在下载" }],
    });
    render(<App />);
    expect(
      await screen.findByRole("button", { name: "打开命令行 · Claude Code" }),
    ).toBeEnabled();
    await userEvent.click(screen.getByRole("button", { name: "取消安装" }));
    expect(api.action).toHaveBeenCalledWith("cancel-install", "codex");
  });

  it("keeps a foreground action pending when an earlier background request finishes", async () => {
    let checked!: (state: DesktopState) => void;
    let launched!: (state: DesktopState) => void;
    api.action.mockImplementation((action) => new Promise<DesktopState>((resolve) => {
      if (action === "check-updates") checked = resolve;
      if (action === "launch") launched = resolve;
    }));
    render(<App />);
    await userEvent.click(await screen.findByRole("button", { name: "检查更新" }));
    const launch = screen.getByRole("button", { name: "打开命令行 · Claude Code" });
    await userEvent.click(launch);
    expect(launch).toBeDisabled();
    await act(async () => { checked(fixture()); });
    expect(launch).toBeDisabled();
    await act(async () => { launched(fixture()); });
    expect(launch).toBeEnabled();
  });

  it("closes an error immediately even if returning home is delayed", async () => {
    api.state.mockResolvedValue({
      ...fixture(),
      view: {
        screen: "error",
        line: "下载失败",
        button: "重试",
        action: "install-cli",
        agent: "codex",
      },
    });
    api.action.mockReturnValue(new Promise(() => {}));
    render(<App />);
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("button", { name: "关闭" }));
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
    expect(api.action).not.toHaveBeenCalledWith(
      "install-suite",
      "codex",
      false,
    );
  });

  it("checks client updates before requiring an explicit install click", async () => {
    api.appCheck.mockResolvedValue({
      available: true,
      version: "0.2.1",
      notes: "修复安装",
    });
    render(<App />);
    await userEvent.click(await screen.findByRole("button", { name: "设置" }));
    await userEvent.click(
      await screen.findByRole("button", { name: "检查客户端更新" }),
    );
    const dialog = await screen.findByRole("dialog");
    expect(api.appInstall).not.toHaveBeenCalled();
    await userEvent.click(
      within(dialog).getByRole("button", { name: "下载并更新" }),
    );
    expect(api.appInstall).toHaveBeenCalledWith("0.2.1");
  });
  it("shows two assistants and launches the selected one", async () => {
    render(<App />);
    expect(await screen.findByText("余额 ¥12.30")).toBeInTheDocument();
    await userEvent.click(
      screen.getByRole("button", { name: "打开命令行 · Claude Code" }),
    );
    expect(api.action).toHaveBeenCalledWith("launch", "claude", false);
    expect(screen.queryByText("供应商")).not.toBeInTheDocument();
    expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
  });

  it("requires confirmation before restoring both assistants", async () => {
    api.action.mockImplementation(async (action) =>
      action === "restore"
        ? {
            ...fixture(),
            view: {
              screen: "confirm",
              line: "这会恢复 Claude Code 和 Codex 接上之前的设置。",
              button: "全部恢复",
              action: "restore-confirm",
            },
          }
        : fixture(),
    );
    render(<App />);
    await screen.findByText("余额 ¥12.30");
    await userEvent.click(await screen.findByRole("button", { name: "设置" }));
    await userEvent.click(screen.getByRole("button", { name: "全部恢复设置" }));
    const dialog = await screen.findByRole("dialog");
    expect(
      within(dialog).getByText(/Claude Code 和 Codex/),
    ).toBeInTheDocument();
    expect(api.action).not.toHaveBeenCalledWith(
      "restore-confirm",
      undefined,
      true,
    );
    await userEvent.click(
      within(dialog).getByRole("button", { name: "全部恢复" }),
    );
    expect(api.action).toHaveBeenCalledWith("restore-confirm", undefined, true);
  });

  it("shows launch errors without pretending the assistant opened", async () => {
    api.action.mockRejectedValue("没能打开助手，请再试一次或联系客服");
    render(<App />);
    await userEvent.click(
      await screen.findByRole("button", { name: "打开命令行 · Claude Code" }),
    );
    expect(await screen.findByRole("alert")).toHaveTextContent("没能打开助手");
    expect(screen.queryByText("准备好了")).not.toBeInTheDocument();
  });

  it("disables operations when the native engine stops", async () => {
    api.state.mockResolvedValue({
      ...fixture(),
      engineReady: false,
      engineError: "客户端服务已停止，请重新打开。",
    });
    render(<App />);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "客户端服务已停止",
    );
    expect(
      screen.getByRole("button", { name: "打开命令行 · Claude Code" }),
    ).toBeDisabled();
    await userEvent.click(await screen.findByRole("button", { name: "设置" }));
    expect(screen.getByRole("button", { name: "全部恢复设置" })).toBeDisabled();
  });

  it("keeps balance unavailability separate from authorization", async () => {
    api.state.mockResolvedValue({ ...fixture(), balance: undefined });
    render(<App />);
    expect(await screen.findByText("余额暂不可用")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "接上助手 · Codex" }),
    ).toBeEnabled();
  });

  it("cleans up state listeners", async () => {
    const view = render(<App />);
    await screen.findByText("余额 ¥12.30");
    view.unmount();
    await waitFor(() => expect(api.unlisten).toHaveBeenCalledOnce());
  });

  it("keeps newer progress when the startup query arrives late", async () => {
    let publish!: (state: DesktopState) => void;
    let resolveInitial!: (state: DesktopState) => void;
    api.subscribe.mockImplementation(async (callback) => {
      publish = callback;
      return api.unlisten;
    });
    api.state.mockReturnValue(
      new Promise<DesktopState>((resolve) => {
        resolveInitial = resolve;
      }),
    );
    render(<App />);
    await waitFor(() => expect(api.state).toHaveBeenCalled());
    act(() =>
      publish({
        ...fixture(),
        revision: 2,
        balance: { text: "余额 ¥20.00", low: false },
      }),
    );
    await act(async () => resolveInitial({ ...fixture(), revision: 1 }));
    expect(screen.getByText("余额 ¥20.00")).toBeInTheDocument();
    expect(screen.queryByText("余额 ¥12.30")).not.toBeInTheDocument();
  });

  it("copies support details through the native clipboard", async () => {
    api.action.mockImplementation(async (action) =>
      action === "support"
        ? { ...fixture(), view: { screen: "support" } }
        : fixture(),
    );
    render(<App />);
    await userEvent.click(
      await screen.findByRole("button", { name: "联系客服" }),
    );
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(
      within(dialog).getByRole("button", { name: "复制 QQ 号" }),
    );
    expect(api.action).toHaveBeenCalledWith("copy-qq");
    expect(await screen.findByText("QQ 号已复制")).toBeInTheDocument();
  });

  it("switches a connected assistant's routing tier inline from the card", async () => {
    const tiers = [
      { name: "economy", label: "省钱" },
      { name: "balanced", label: "均衡", recommended: true },
      { name: "stable", label: "稳定" },
    ];
    const withTiers = (tier: string): DesktopState => ({
      ...fixture(),
      statuses: [
        { agent: "claude", name: "Claude Code", state: "ok", badge: "能用了", tier, tiers },
        { agent: "codex", name: "Codex", state: "none", badge: "还没接" },
      ],
    });
    api.state.mockResolvedValue(withTiers("balanced"));
    api.switchTier.mockResolvedValue({
      ...withTiers("economy"),
      view: { screen: "home", agent: "claude", notice: "已将 Claude Code 切到「省钱」线路，下一个任务生效。" },
    });
    render(<App />);
    const group = await screen.findByRole("group", { name: "Claude Code 线路档位" });
    expect(within(group).getByRole("button", { name: "均衡" })).toHaveAttribute("aria-pressed", "true");
    await userEvent.click(within(group).getByRole("button", { name: "省钱" }));
    expect(api.switchTier).toHaveBeenCalledWith("claude", "economy");
    expect(await screen.findByText("已将 Claude Code 切到「省钱」线路，下一个任务生效。")).toBeInTheDocument();
  });

  it("runs a real test-chat probe from the connected assistant card", async () => {
    api.action.mockResolvedValue({
      ...fixture(),
      view: { screen: "home", agent: "claude", notice: "对话已通（claude-sonnet-4-5 · 1.1s），打开 Claude Code 就能直接发话。" },
    });
    render(<App />);
    const btn = await screen.findByRole("button", { name: "测试对话 · Claude Code" });
    await userEvent.click(btn);
    expect(api.action).toHaveBeenCalledWith("test-chat", "claude", false);
    expect(await screen.findByText(/对话已通（claude-sonnet-4-5/)).toBeInTheDocument();
  });

  it("silently refreshes balance when the window regains focus", async () => {
    api.action.mockResolvedValue({
      ...fixture(),
      balance: { text: "余额 ¥88.00", low: false },
    });
    render(<App />);
    await screen.findByText("余额 ¥12.30");
    await act(async () => {
      window.dispatchEvent(new Event("focus"));
    });
    await waitFor(() => expect(api.action).toHaveBeenCalledWith("refresh"));
    expect(await screen.findByText("余额 ¥88.00")).toBeInTheDocument();
  });

  it("shows workspace identity switcher and triggers switch-account", async () => {
    api.state.mockResolvedValue({
      ...fixture(),
      accountLabel: "zh***@example.com",
      workspace: { kind: "team", label: "团队 · 研究组", teamId: 7 },
    });
    render(<App />);
    const trigger = await screen.findByRole("button", { name: "工作区菜单：团队 · 研究组" });
    expect(trigger).toHaveTextContent("团队 · 研究组");
    expect(trigger).toHaveTextContent("zh***@example.com");
    await userEvent.click(trigger);
    const switchItem = await screen.findByRole("menuitem", { name: "切换工作区 / 账号" });
    await userEvent.click(switchItem);
    expect(api.action).toHaveBeenCalledWith("switch-account", undefined, false);
  });

  it("shows progressive today strip when connected and loads Usage & Logs dashboard", async () => {
    const withUsage = (range: "24h" | "7d"): DesktopState => ({
      ...fixture(),
      today: { costCny: "2.40", requests: 4, tokens: 9600 },
      usage: {
        range,
        costCny: range === "7d" ? "15.80" : "2.40",
        requests: range === "7d" ? 21 : 4,
        tokens: range === "7d" ? 64000 : 9600,
        promptTokens: 48000,
        completionTokens: 16000,
        heatmap: [{ date: "2026-11-22", requests: 4, tokens: 9600, costCny: "2.40" }],
        logs: [
          {
            id: 42,
            createdAt: 1795300000,
            model: "claude-sonnet-4-5",
            promptTokens: 800,
            completionTokens: 400,
            costCny: "0.42",
            useTimeMs: 1200,
          },
        ],
      },
    });
    api.state.mockResolvedValue(withUsage("24h"));
    api.usage.mockImplementation(async (r: "24h" | "7d") => withUsage(r));

    render(<App />);
    const strip = await screen.findByRole("region", { name: "今日用量概览" });
    expect(strip).toHaveTextContent("¥2.40");
    await userEvent.click(within(strip).getByRole("button", { name: "查看全部日志" }));

    expect(api.usage).toHaveBeenCalledWith("24h");
    expect(api.usage).toHaveBeenCalledTimes(1);
    expect(await screen.findByText("claude-sonnet-4-5")).toBeInTheDocument();

    const rangeGroup = screen.getByRole("group", { name: "统计时间范围" });
    await userEvent.click(within(rangeGroup).getByRole("button", { name: "7天" }));
    expect(api.usage).toHaveBeenCalledWith("7d");
    expect(await screen.findByText("¥15.80")).toBeInTheDocument();

    // Failing to switch to 90d must revert to the loaded range (7d) and never label 7d data as 90天.
    api.usage.mockRejectedValueOnce("读取 90 天用量失败");
    await userEvent.click(within(rangeGroup).getByRole("button", { name: "90天" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("读取 90 天用量失败");
    expect(within(rangeGroup).getByRole("button", { name: "7天" })).toHaveAttribute("aria-pressed", "true");
    expect(within(rangeGroup).getByRole("button", { name: "90天" })).toHaveAttribute("aria-pressed", "false");
    expect(api.usage.mock.calls.map(([range]) => range)).toEqual(["24h", "7d", "90d"]);
  });

  it.each([
    ["用量与日志", "usage", "刷新数据"],
    ["可用模型", "catalog", "刷新清单"],
  ] as const)("keeps a failed first %s load stable until the user retries", async (page, method, retry) => {
    // A second unexpected automatic request stays pending so a regression fails
    // on its call count instead of spinning an unlimited rejected-promise loop.
    api[method].mockRejectedValueOnce("首次读取失败").mockReturnValue(new Promise(() => {}));
    render(<App />);
    await userEvent.click(await screen.findByRole("button", { name: page }));
    expect(await screen.findByRole("alert")).toHaveTextContent("首次读取失败");
    expect(api[method]).toHaveBeenCalledTimes(1);
    api[method].mockResolvedValueOnce(fixture());
    await userEvent.click(screen.getByRole("button", { name: retry }));
    await waitFor(() => expect(screen.queryByRole("alert")).not.toBeInTheDocument());
    expect(api[method]).toHaveBeenCalledTimes(2);
  });

  it.each(["usage", "catalog"] as const)("replaces an in-flight %s read when switching between identically named teams", async (method) => {
    let publish!: (state: DesktopState) => void;
    api.subscribe.mockImplementation(async (callback) => { publish = callback; return api.unlisten; });
    const wallet = (teamId: number, walletEpoch: number): DesktopState => ({
      ...fixture(), workspace: { kind: "team", label: "研发团队", teamId }, accountLabel: "alice@example.com", walletEpoch,
    });
    const loaded = (state: DesktopState, cost: string): DesktopState => method === "usage"
      ? { ...state, usage: { range: "24h", costCny: cost, requests: 1, tokens: 2, promptTokens: 1, completionTokens: 1, heatmap: [], logs: [] } }
      : { ...state, catalogs: { claude: { enabled: true, version: 1, agent: "claude_code", tier: "balanced", default_model: cost, models: [{ id: cost }] } } };
    let finishOld!: (state: DesktopState) => void;
    api.state.mockResolvedValue(wallet(7, 1));
    api[method].mockReturnValueOnce(new Promise<DesktopState>((resolve) => { finishOld = resolve; }))
      .mockResolvedValueOnce(loaded(wallet(8, 2), "9.50"));
    render(<App />);
    await userEvent.click(await screen.findByRole("button", { name: method === "usage" ? "用量与日志" : "可用模型" }));
    expect(api[method]).toHaveBeenCalledTimes(1);
    act(() => publish(wallet(8, 2)));
    await waitFor(() => expect(api[method]).toHaveBeenCalledTimes(2));
    expect(await screen.findAllByText(method === "usage" ? "¥9.50" : "9.50")).not.toHaveLength(0);
    await act(async () => finishOld(loaded(wallet(7, 1), "1.20")));
    expect(screen.queryByText(method === "usage" ? "¥1.20" : "1.20")).not.toBeInTheDocument();
    expect(api[method]).toHaveBeenCalledTimes(2);
  });

  it("loads authorized 6-brand models on the Models page", async () => {
    api.catalog.mockResolvedValue({
      ...fixture(),
      catalogs: {
        claude: {
          enabled: true,
          version: 3,
          agent: "claude_code",
          tier: "balanced",
          default_model: "claude-sonnet-4-5",
          models: [
            { id: "claude-sonnet-4-5", display_name: "Claude Sonnet 4.5", vendor: "Anthropic", context_window: 200000 },
            { id: "deepseek-r1", display_name: "DeepSeek R1", vendor: "DeepSeek", context_window: 128000 },
          ],
        },
      },
    });

    render(<App />);
    await userEvent.click(await screen.findByRole("button", { name: "可用模型" }));
    expect(api.catalog).toHaveBeenCalledWith("claude");
    expect(await screen.findByText("Claude Sonnet 4.5")).toBeInTheDocument();
    expect(screen.getByText("DeepSeek R1")).toBeInTheDocument();
    expect(screen.getByText("默认")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "查看价格说明" }));
    expect(api.action).toHaveBeenCalledWith("open-pricing", undefined, false);
  });

  it("shows low-balance banner, working directory selector, starter prompt copy, and diagnostic export", async () => {
    api.state.mockResolvedValue({
      ...fixture(),
      balance: { text: "余额 ¥1.20", low: true },
      workDir: "/Users/alice/Desktop/接上AI工作区",
      recentDirs: ["/Users/alice/Desktop/接上AI工作区", "/Users/alice/Documents/论文草稿"],
    });
    api.action.mockImplementation(async () => ({
      ...fixture(),
      balance: { text: "余额 ¥1.20", low: true },
      workDir: "/Users/alice/Desktop/接上AI工作区",
    }));

    render(<App />);
    expect(await screen.findByText(/当前账户余额偏低/)).toBeInTheDocument();

    const workDirCard = screen.getByRole("region", { name: "命令行工作目录" });
    expect(workDirCard).toHaveTextContent("接上AI工作区");
    await userEvent.click(within(workDirCard).getByRole("button", { name: "选择文件夹" }));
    expect(api.action).toHaveBeenCalledWith("pick-work-dir", undefined, false);

    await userEvent.click(screen.getByRole("button", { name: /复制首句提示词/ }));
    expect(api.action).toHaveBeenCalledWith("copy-first-prompt");

    await userEvent.click(screen.getByRole("button", { name: "设置" }));
    await userEvent.click(await screen.findByRole("button", { name: "复制诊断信息" }));
    expect(api.action).toHaveBeenCalledWith("copy-report");
  });

  it("keeps CLI launch usable while GUI is downloading in the background", async () => {
    api.state.mockResolvedValue({
      ...fixture(),
      tools: [
        { agent: "claude", cliInstalled: true, guiInstalled: false, guiAvailable: true, inspected: true, guiInspected: true },
      ],
      installTasks: [{ agent: "claude", busy: true, line: "桌面版 · 正在下载：120.0 / 402.0 MB（30%）" }],
    });
    render(<App />);
    const claude = await screen.findByRole("article", { name: "Claude Code" });
    const launchBtn = within(claude).getByRole("button", { name: "打开命令行 · Claude Code" });
    expect(launchBtn).toBeEnabled();
    await userEvent.click(launchBtn);
    expect(api.action).toHaveBeenCalledWith("launch", "claude", false);
  });

  it("auto-reloads usage when the active wallet switches while on the Usage page and keeps heatmap to one tab stop", async () => {
    let publish!: (state: DesktopState) => void;
    api.subscribe.mockImplementation(async (callback) => {
      publish = callback;
      return api.unlisten;
    });
    const personalState: DesktopState = {
      ...fixture(),
      revision: 1,
      workspace: { kind: "personal", label: "个人工作区" },
      accountLabel: "alice@example.com",
    };
    const personalUsageState: DesktopState = {
      ...personalState,
      revision: 2,
      usage: {
        range: "24h",
        costCny: "1.20",
        requests: 2,
        tokens: 500,
        promptTokens: 300,
        completionTokens: 200,
        heatmap: [
          { date: "2026-11-22", requests: 1, tokens: 200, costCny: "0.40" },
          { date: "2026-11-23", requests: 1, tokens: 300, costCny: "0.80" },
        ],
        logs: [],
      },
    };
    const teamUsageState: DesktopState = {
      ...fixture(),
      revision: 4,
      workspace: { kind: "team", label: "研发团队", teamId: 7 },
      accountLabel: "alice@example.com",
      usage: {
        range: "24h",
        costCny: "9.50",
        requests: 5,
        tokens: 1200,
        promptTokens: 800,
        completionTokens: 400,
        heatmap: [
          { date: "2026-11-22", requests: 2, tokens: 400, costCny: "3.00" },
          { date: "2026-11-23", requests: 3, tokens: 800, costCny: "6.50" },
        ],
        logs: [],
      },
    };
    api.state.mockResolvedValue(personalState);
    api.usage.mockResolvedValueOnce(personalUsageState).mockResolvedValueOnce(teamUsageState);

    render(<App />);
    await userEvent.click(await screen.findByRole("button", { name: "用量与日志" }));
    expect(await screen.findByText("¥1.20")).toBeInTheDocument();

    // Verify heatmap uses roving tabIndex: exactly 1 tabIndex=0 cell across the entire grid
    const grid = screen.getByRole("grid", { name: "近 365 天活跃度日历，使用方向键浏览日期" });
    const tabbableCells = grid.querySelectorAll("[tabindex='0']");
    expect(tabbableCells.length).toBe(1);

    // Simulate backend wallet switch pushing usage=undefined with new workspace
    act(() => {
      publish({
        ...fixture(),
        revision: 3,
        workspace: { kind: "team", label: "研发团队", teamId: 7 },
        accountLabel: "alice@example.com",
        usage: undefined,
      });
    });

    await waitFor(() => expect(api.usage).toHaveBeenCalledTimes(2));
    expect(await screen.findByText("¥9.50")).toBeInTheDocument();
  });

  it("exposes 恢复默认 for custom workDir even when recentDirs is empty", async () => {
    api.state.mockResolvedValue({
      ...fixture(),
      workDir: "/Users/alice/Projects/thesis",
      recentDirs: [],
    });
    api.setWorkDir.mockResolvedValue({
      ...fixture(),
      workDir: "/Users/alice/Desktop/接上AI工作区",
      recentDirs: [],
      view: { screen: "home", notice: "已恢复默认工作区" },
    });
    render(<App />);
    const workDirCard = await screen.findByRole("region", { name: "命令行工作目录" });
    const resetBtn = within(workDirCard).getByRole("button", { name: "恢复默认" });
    await userEvent.click(resetBtn);
    expect(api.setWorkDir).toHaveBeenCalledWith("");
  });

  it("shows prominent update banner, sidebar version badge, and dialog when an update is found", async () => {
    api.appCheck.mockResolvedValue({
      available: true,
      version: "0.2.0-beta.12",
      notes: "新增官方 Skill 广场一键安装",
    });
    render(<App />);
    await userEvent.click(await screen.findByRole("button", { name: "检查更新" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText("更新至 0.2.0-beta.12")).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "关闭" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());

    expect(screen.getByText("接上AI 有新版本 0.2.0-beta.12 可用")).toBeInTheDocument();
    expect(screen.getByText("有更新")).toBeInTheDocument();

    // Banner remains visible on other tabs until dismissed, and sidebar version opens dialog again.
    await userEvent.click(screen.getByRole("button", { name: "设置" }));
    expect(screen.getByText("接上AI 有新版本 0.2.0-beta.12 可用")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "暂不提醒此版本" }));
    expect(screen.queryByText("接上AI 有新版本 0.2.0-beta.12 可用")).toBeNull();

    await userEvent.click(screen.getByTitle("点击更新至 0.2.0-beta.12"));
    expect(await screen.findByRole("dialog")).toBeInTheDocument();
  });
});
