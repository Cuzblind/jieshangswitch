import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { PromptsPanel } from "./components/PromptsPanel";
import type { Prompt } from "./lib/desktop";

const prompts: Prompt[] = [
  { id: "p-1", name: "论文写作", content: "学术语气", enabled: true, updatedAt: "" },
  { id: "p-2", name: "代码审查", content: "先读测试", enabled: false, updatedAt: "" },
];

describe("PromptsPanel", () => {
  it("names the target file and marks the prompt in use", () => {
    render(<PromptsPanel agent="claude" prompts={prompts} busy={false} run={vi.fn()} />);
    expect(screen.getByText(/CLAUDE\.md/)).toBeInTheDocument();
    expect(screen.getByText("论文写作")).toBeInTheDocument();
    expect(screen.getByText("使用中")).toBeInTheDocument();
  });

  it("uses AGENTS.md for Codex", () => {
    render(<PromptsPanel agent="codex" prompts={[]} busy={false} run={vi.fn()} />);
    expect(screen.getByText(/AGENTS\.md/)).toBeInTheDocument();
  });

  it("disables the active prompt by sending an empty id, and only offers delete for inactive ones", async () => {
    const run = vi.fn();
    render(<PromptsPanel agent="claude" prompts={prompts} busy={false} run={run} />);
    await userEvent.click(screen.getByRole("button", { name: "停用" }));
    expect(run).toHaveBeenCalledWith("prompt-enable", { promptId: "" });
    expect(screen.getAllByRole("button", { name: "删除" })).toHaveLength(1);
    await userEvent.click(screen.getByRole("button", { name: "删除" }));
    expect(run).toHaveBeenCalledWith("prompt-delete", { promptId: "p-2" });
  });

  it("locks every control while busy", () => {
    render(<PromptsPanel agent="claude" prompts={prompts} busy run={vi.fn()} />);
    for (const button of screen.getAllByRole("button")) expect(button).toBeDisabled();
  });
});
