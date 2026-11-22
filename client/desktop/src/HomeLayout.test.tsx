import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import type { DesktopState } from "./lib/desktop";

const api = vi.hoisted(() => ({
  state: vi.fn(),
  action: vi.fn(),
  subscribe: vi.fn(),
  appCheck: vi.fn(),
  appProgress: vi.fn(),
}));
vi.mock("./lib/desktop", async (original) => ({
  ...(await original<typeof import("./lib/desktop")>()),
  desktopState: api.state,
  desktopAction: api.action,
  onDesktopState: api.subscribe,
  checkAppUpdate: api.appCheck,
  onAppUpdateProgress: api.appProgress,
}));

type Tool = NonNullable<DesktopState["tools"]>[number];
const tool = (over: Partial<Tool>): Tool => ({
  agent: "claude",
  cliInstalled: true,
  guiInstalled: true,
  guiAvailable: true,
  inspected: true,
  guiInspected: true,
  cliVersion: "2.1.0",
  guiVersion: "4.29.0.0",
  ...over,
});
const fixture = (over: Partial<DesktopState> = {}): DesktopState => ({
  view: { screen: "home" },
  statuses: [
    { agent: "claude", name: "Claude Code", state: "ok", badge: "能用了" },
    { agent: "codex", name: "Codex", state: "expired", badge: "需要重新接上" },
  ],
  version: "0.2.0-beta.11",
  os: "Windows",
  authorized: true,
  support: true,
  engineReady: true,
  tools: [tool({}), tool({ agent: "codex" })],
  ...over,
});
const names = (card: HTMLElement) => within(card).getAllByRole("button").map((b) => b.textContent ?? "");

beforeEach(() => {
  vi.clearAllMocks();
  api.subscribe.mockResolvedValue(() => {});
  api.appCheck.mockResolvedValue({ available: false });
  api.appProgress.mockResolvedValue(() => {});
  api.action.mockResolvedValue(fixture());
  localStorage.setItem("jieshangai:tool-update-check", String(Date.now()));
  localStorage.setItem("jieshangai:client-update-check", String(Date.now()));
});

describe("home layout", () => {
  it("promotes desktop install as the primary action while keeping CLI usable", async () => {
    api.state.mockResolvedValue(fixture({ tools: [tool({ guiInstalled: false, guiVersion: undefined })] }));
    render(<App />);
    const claude = await screen.findByRole("article", { name: "Claude Code" });
    const installBtn = within(claude).getByRole("button", { name: "安装桌面版 · Claude Code" });
    expect(installBtn).toHaveClass("primary");
    await userEvent.click(installBtn);
    expect(api.action).toHaveBeenCalledWith("install-suite", "claude", false);
    expect(within(claude).getByRole("button", { name: "打开命令行 · Claude Code" })).toBeEnabled();
    expect(screen.queryByText(/补装桌面版/)).not.toBeInTheDocument();
  });

  it("shows repair only for an assistant with a problem, once", async () => {
    api.state.mockResolvedValue(fixture());
    render(<App />);
    const claude = await screen.findByRole("article", { name: "Claude Code" });
    expect(names(claude).some((n) => n.includes("一键修复"))).toBe(false);
    const codex = screen.getByRole("article", { name: "Codex" });
    expect(names(codex).filter((n) => n.includes("一键修复"))).toHaveLength(1);
  });

  it("opens the desktop app as the primary left action and keeps CLI on the right", async () => {
    api.state.mockResolvedValue(fixture());
    render(<App />);
    const claude = await screen.findByRole("article", { name: "Claude Code" });
    const list = names(claude);
    expect(list.indexOf("打开桌面版 · Claude Code")).toBeGreaterThanOrEqual(0);
    expect(list.indexOf("打开桌面版 · Claude Code")).toBeLessThan(list.indexOf("打开命令行 · Claude Code"));
    expect(within(claude).getByRole("button", { name: "打开桌面版 · Claude Code" })).toHaveClass("primary");
    expect(within(claude).getByRole("button", { name: "打开命令行 · Claude Code" })).not.toHaveClass("primary");
    expect(list.some((n) => n.includes("打开终端") || n.includes("检查更新"))).toBe(false);
    await userEvent.click(within(claude).getByRole("button", { name: "打开桌面版 · Claude Code" }));
    expect(api.action).toHaveBeenLastCalledWith("launch-gui", "claude", false);
    await userEvent.click(within(claude).getByRole("button", { name: "打开命令行 · Claude Code" }));
    expect(api.action).toHaveBeenLastCalledWith("launch", "claude", false);
  });

  it("checks every assistant and the client from one button at the top", async () => {
    api.state.mockResolvedValue(fixture());
    render(<App />);
    await userEvent.click(await screen.findByRole("button", { name: "检查更新" }));
    expect(api.action).toHaveBeenCalledWith("check-updates", undefined, false);
    await waitFor(() => expect(api.appCheck).toHaveBeenCalled());
  });

  it("updates every assistant that has a new version from the same button", async () => {
    api.state.mockResolvedValue(
      fixture({
        toolUpdates: { checking: false, checkedAt: "2026-11-03T10:00:00Z" },
        tools: [tool({}), tool({ agent: "codex", latestCli: "0.9.0", cliVersion: "0.8.0", updateAvailable: true })],
      }),
    );
    render(<App />);
    await userEvent.click(await screen.findByRole("button", { name: "全部更新" }));
    await waitFor(() => expect(api.action).toHaveBeenCalledWith("update-tool", "codex", false));
    expect(api.action).not.toHaveBeenCalledWith("update-tool", "claude", false);
  });

  it("recommends Claude Code and hides disabled launch/check buttons before installation", async () => {
    api.state.mockResolvedValue(
      fixture({
        authorized: false,
        statuses: [
          { agent: "claude", name: "Claude Code", state: "nocli", badge: "未安装" },
          { agent: "codex", name: "Codex", state: "nocli", badge: "未安装" },
        ],
        tools: [
          tool({ agent: "claude", cliInstalled: false, guiInstalled: false, cliVersion: undefined, guiVersion: undefined }),
          tool({ agent: "codex", cliInstalled: false, guiInstalled: false, cliVersion: undefined, guiVersion: undefined }),
        ],
      }),
    );
    render(<App />);
    const claude = await screen.findByRole("article", { name: "Claude Code" });
    const codex = screen.getByRole("article", { name: "Codex" });
    expect(within(claude).getByText("推荐先装")).toBeInTheDocument();
    expect(within(codex).queryByText("推荐先装")).not.toBeInTheDocument();
    expect(within(claude).queryByRole("button", { name: /打开桌面版/ })).not.toBeInTheDocument();
    expect(within(claude).queryByRole("button", { name: /检查连接/ })).not.toBeInTheDocument();
    expect(within(claude).getByRole("button", { name: "安装应用 · Claude Code" })).toHaveClass("primary");
    expect(within(codex).getByRole("button", { name: "安装应用 · Codex" })).not.toHaveClass("primary");
  });
});
