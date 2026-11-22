import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import type { DesktopState } from "./lib/desktop";

const api = vi.hoisted(() => ({
  state: vi.fn(),
  action: vi.fn(),
  prompt: vi.fn(),
  subscribe: vi.fn(),
  unlisten: vi.fn(),
}));
vi.mock("./lib/desktop", async (original) => ({
  ...(await original<typeof import("./lib/desktop")>()),
  desktopState: api.state,
  desktopAction: api.action,
  desktopPromptAction: api.prompt,
  onDesktopState: api.subscribe,
  checkAppUpdate: vi.fn().mockResolvedValue({ available: false }),
  applyAppUpdate: vi.fn(),
  cancelAppUpdate: vi.fn(),
  onAppUpdateProgress: vi.fn().mockResolvedValue(() => {}),
}));

const fixture = (): DesktopState => ({
  view: { screen: "home" },
  statuses: [
    { agent: "claude", name: "Claude Code", state: "ok", badge: "能用了" },
    { agent: "codex", name: "Codex", state: "none", badge: "还没接" },
  ],
  version: "0.2.0-beta.4",
  os: "Windows",
  authorized: true,
  support: true,
  engineReady: true,
});

beforeEach(() => {
  vi.clearAllMocks();
  api.state.mockResolvedValue(fixture());
  api.action.mockResolvedValue(fixture());
  api.subscribe.mockResolvedValue(api.unlisten);
  api.prompt.mockResolvedValue({
    ...fixture(),
    prompts: { claude: [{ id: "p-1", name: "论文写作", content: "学术语气", enabled: false, updatedAt: "" }] },
  });
  localStorage.clear();
});

describe("prompts page", () => {
  it("loads the list when opened and sends the selected assistant with each action", async () => {
    render(<App />);
    await userEvent.click(await screen.findByRole("button", { name: "提示词" }));
    expect(api.prompt).toHaveBeenCalledWith("prompts-list", "claude");
    const page = await screen.findByRole("region", { name: "提示词" });
    expect(await within(page).findByText("论文写作")).toBeInTheDocument();

    await userEvent.click(within(page).getByRole("button", { name: "启用" }));
    expect(api.prompt).toHaveBeenCalledWith("prompt-enable", "claude", { promptId: "p-1" });

    await userEvent.click(screen.getByRole("button", { name: "Codex" }));
    expect(api.prompt).toHaveBeenCalledWith("prompts-list", "codex");
  });

  it("shows an engine error when the list cannot load", async () => {
    api.prompt.mockRejectedValue("没能写入助手的提示词文件");
    render(<App />);
    await userEvent.click(await screen.findByRole("button", { name: "提示词" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("没能写入助手的提示词文件");
  });
});
