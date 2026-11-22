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
}));
vi.mock("./lib/desktop", async (original) => ({
  ...(await original<typeof import("./lib/desktop")>()),
  desktopState: api.state,
  desktopAction: api.action,
  desktopSwitchTier: api.switchTier,
  desktopUsage: api.usage,
  desktopCatalog: api.catalog,
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
  vi.clearAllMocks();
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
});

describe("desktop assistant flow", () => {
  it("keeps a successful CLI usable when the GUI still needs installation", async () => {
    api.state.mockResolvedValue({...fixture(),tools:[{agent:"claude",cliInstalled:true,guiInstalled:false,guiAvailable:true}]});
    render(<App />);
    const launchButton=await screen.findByRole("button", {name:"打开命令行 · Claude Code"});
    expect(screen.getByRole("button", {name:"安装桌面版 · Claude Code"})).toBeEnabled();
    await userEvent.click(launchButton);
    expect(api.action).toHaveBeenCalledWith("launch","claude",false);
  });
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

  it("opens the official GUI and keeps a terminal entry", async () => {
    api.state.mockResolvedValue({
      ...fixture(),
      tools: [
        {
          agent: "claude",
          cliInstalled: true,
          guiInstalled: true,
          guiAvailable: true,
        },
      ],
    });
    render(<App />);
    await userEvent.click(
      await screen.findByRole("button", { name: "打开桌面版 · Claude Code" }),
    );
    expect(api.action).toHaveBeenCalledWith("launch-gui", "claude", false);
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
  });
});
