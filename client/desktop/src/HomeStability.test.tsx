import { act, render, screen, waitFor } from "@testing-library/react";
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
  guiInstalled: false,
  guiAvailable: true,
  inspected: true,
  cliVersion: "2.1.0",
  ...over,
});
const fixture = (over: Partial<DesktopState> = {}): DesktopState => ({
  view: { screen: "home" },
  statuses: [
    { agent: "claude", name: "Claude Code", state: "ok", badge: "能用了" },
    { agent: "codex", name: "Codex", state: "none", badge: "还没接" },
  ],
  version: "0.2.0-beta.8",
  os: "Windows",
  authorized: true,
  support: true,
  engineReady: true,
  ...over,
});

beforeEach(() => {
  vi.clearAllMocks();
  api.subscribe.mockResolvedValue(() => {});
  api.appCheck.mockResolvedValue({ available: false });
  api.appProgress.mockResolvedValue(() => {});
  api.action.mockResolvedValue(fixture());
  localStorage.clear();
});

describe("home screen stability", () => {
  it("does not call an uninspected assistant missing", async () => {
    api.state.mockResolvedValue(
      fixture({ tools: [tool({ agent: "codex", cliInstalled: false, cliVersion: undefined, inspected: false })] }),
    );
    render(<App />);
    expect(await screen.findByRole("button", { name: "正在检测 · Codex" })).toBeDisabled();
    expect(screen.queryByRole("button", { name: "安装应用 · Codex" })).not.toBeInTheDocument();
    expect(screen.queryByText("待安装")).not.toBeInTheDocument();
  });

  it("waits for the desktop-app check before calling the desktop app missing", async () => {
    api.state.mockResolvedValue(fixture({ tools: [tool({ guiInspected: false })] }));
    render(<App />);
    expect(await screen.findByText("2.1.0")).toBeInTheDocument();
    expect(screen.getAllByLabelText("检测中").length).toBeGreaterThan(0);
    expect(screen.queryByText("待安装")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "安装桌面版 · Claude Code" })).toBeNull();
  });

  it("retries the daily update check after a failed one", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      api.state.mockResolvedValue(fixture());
      api.action.mockResolvedValue(
        fixture({ toolUpdates: { checking: false, error: "部分更新源暂时无法访问，已保留上次检查结果。", checkedAt: "2026-11-02T10:00:00Z" } }),
      );
      render(<App />);
      // The first snapshot arrives long before the 3s check, as in the real app.
      await screen.findByRole("button", { name: "检查连接 · Claude Code" });
      await act(async () => {
        vi.advanceTimersByTime(3500);
      });
      await waitFor(() => expect(api.action).toHaveBeenCalledWith("check-updates"));
      expect(localStorage.getItem("jieshangai:tool-update-check")).toBeNull();
    } finally {
      vi.useRealTimers();
    }
  });

  it("counts the daily update check once it succeeded", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      api.state.mockResolvedValue(fixture());
      api.action.mockResolvedValue(fixture({ toolUpdates: { checking: false, checkedAt: "2026-11-02T10:00:00Z" } }));
      render(<App />);
      await screen.findByRole("button", { name: "检查连接 · Claude Code" });
      await act(async () => {
        vi.advanceTimersByTime(3500);
      });
      await waitFor(() =>
        expect(localStorage.getItem("jieshangai:tool-update-check")).toBe(String(Date.parse("2026-11-02T10:00:00Z"))),
      );
    } finally {
      vi.useRealTimers();
    }
  });

  it("shows the same notice again when the action is repeated", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      const checked = fixture({ view: { screen: "home", notice: "连接检查通过。" } });
      api.state.mockResolvedValue(fixture());
      api.action.mockResolvedValue(checked);
      render(<App />);
      await userEvent.click(await screen.findByRole("button", { name: "检查连接 · Claude Code" }));
      expect(await screen.findByText("连接检查通过。")).toBeInTheDocument();
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
      await act(async () => {
        vi.advanceTimersByTime(4500);
      });
      await waitFor(() => expect(screen.queryByText("连接检查通过。")).not.toBeInTheDocument());
      await userEvent.click(screen.getByRole("button", { name: "检查连接 · Claude Code" }));
      expect(await screen.findByText("连接检查通过。")).toBeInTheDocument();
      await act(async () => {
        vi.advanceTimersByTime(4500);
      });
      await waitFor(() => expect(screen.queryByText("连接检查通过。")).not.toBeInTheDocument());
    } finally {
      vi.useRealTimers();
    }
  });
});
