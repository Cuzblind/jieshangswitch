import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { SkillsPanel } from "@/components/SkillsPanel";
import type { Skill } from "@/lib/desktop";

const skills: Skill[] = [
  { dir: "paper", name: "论文写作", description: "学术语气", enabled: true, managed: true },
  { dir: "sheet", name: "表格", enabled: false, managed: true },
  { dir: "mine", name: "自己的", enabled: true, managed: false },
  { dir: "clash", name: "冲突", enabled: false, managed: true, conflict: true },
];

describe("SkillsPanel", () => {
  it("names the target directory per assistant", () => {
    const { unmount } = render(<SkillsPanel agent="claude" skills={[]} busy={false} run={vi.fn()} />);
    expect(screen.getAllByText(/~\/\.claude\/skills/).length).toBeGreaterThan(0);
    unmount();
    render(<SkillsPanel agent="codex" skills={[]} busy={false} run={vi.fn()} />);
    expect(screen.getAllByText(/~\/\.codex\/skills/).length).toBeGreaterThan(0);
  });

  it("imports from a zip through the native picker without sending a path", async () => {
    const run = vi.fn().mockResolvedValue(true);
    render(<SkillsPanel agent="codex" skills={[]} busy={false} run={run} />);
    await userEvent.click(screen.getByRole("button", { name: "从 zip 导入" }));
    expect(run).toHaveBeenLastCalledWith("skill-import-zip", "");
  });

  it("explains a folder that cannot be managed instead of offering a failing button", () => {
    render(
      <SkillsPanel
        agent="claude"
        skills={[{ dir: "link", name: "链接的", enabled: true, managed: false, unsupported: "这是链接文件夹，暂不支持纳入管理" }]}
        busy={false}
        run={vi.fn()}
      />,
    );
    expect(screen.getByText("这是链接文件夹，暂不支持纳入管理")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "纳入管理 · 链接的" })).toBeNull();
  });

  it("sends the folder name and only offers delete for disabled managed skills", async () => {
    const run = vi.fn().mockResolvedValue(true);
    render(<SkillsPanel agent="claude" skills={skills} busy={false} run={run} />);
    expect(screen.getByText("学术语气")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "删除 · 论文写作" })).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "停用 · 论文写作" }));
    expect(run).toHaveBeenLastCalledWith("skill-disable", "paper");
    await userEvent.click(screen.getByRole("button", { name: "启用 · 表格" }));
    expect(run).toHaveBeenLastCalledWith("skill-enable", "sheet");
    await userEvent.click(screen.getByRole("button", { name: "删除 · 表格" }));
    expect(run).toHaveBeenLastCalledWith("skill-delete", "sheet");
    await userEvent.click(screen.getByRole("button", { name: "纳入管理 · 自己的" }));
    expect(run).toHaveBeenLastCalledWith("skill-import", "mine");
    expect(screen.getByRole("button", { name: "启用 · 冲突" })).toBeDisabled();
  });
});
