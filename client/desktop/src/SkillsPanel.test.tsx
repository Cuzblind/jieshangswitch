import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { SkillsPanel } from "@/components/SkillsPanel";
import type { RemoteSkillsCatalog, Skill } from "@/lib/desktop";

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

  it("renders the official Skill Store with one-click install, copy command, and locked recharge actions", async () => {
    const run = vi.fn().mockResolvedValue(true);
    const onRecharge = vi.fn();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText },
    });
    const catalog: RemoteSkillsCatalog = {
      categories: [
        { id: "literature", label: "文献调研" },
        { id: "figure", label: "科研绘图" },
      ],
      items: [
        {
          id: 1,
          slug: "lit-helper",
          title: "文献综述助手",
          category: "literature",
          tier: "free",
          summary: "自动梳理文献脉络",
          supported_agents: ["claude", "codex"],
          install_command: "npx skills add lab/lit-helper",
          has_install_command: true,
          has_zip: true,
          zip_size_bytes: 2048,
          zip_sha256: "abc",
          has_preset: false,
          unlocked: true,
          install_count: 25,
          sort_order: 1,
          updated_at: 1,
        },
        {
          id: 2,
          slug: "paper",
          title: "论文写作",
          category: "literature",
          tier: "free",
          summary: "已在本地安装",
          supported_agents: ["claude"],
          has_zip: true,
          zip_size_bytes: 1024,
          zip_sha256: "def",
          has_preset: false,
          unlocked: true,
          install_count: 180,
          sort_order: 2,
          updated_at: 1,
        },
        {
          id: 3,
          slug: "nature-plot",
          title: "Nature 科研配色绘图",
          category: "figure",
          tier: "premium",
          summary: "期刊级矢量图模板",
          supported_agents: ["codex"],
          has_zip: true,
          zip_size_bytes: 4096,
          zip_sha256: "ghi",
          has_preset: false,
          unlocked: false,
          install_count: 420,
          sort_order: 3,
          updated_at: 1,
        },
      ],
      entitlement: {
        authenticated: true,
        paid_fen: 0,
        threshold_fen: 3000,
        is_team_member: false,
        premium_unlocked: false,
      },
    };

    render(
      <SkillsPanel
        agent="claude"
        skills={skills}
        busy={false}
        run={run}
        catalog={catalog}
        connected={true}
        onRecharge={onRecharge}
      />,
    );

    expect(screen.getByText("官方 Skill 广场")).toBeInTheDocument();
    expect(screen.getByText("已安装")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "一键安装 · 论文写作" })).toBeNull();

    await userEvent.click(screen.getByRole("button", { name: "一键安装 · 文献综述助手" }));
    expect(run).toHaveBeenCalledWith("skill-install-remote", "lit-helper");

    await userEvent.click(screen.getByRole("button", { name: "复制命令 · 文献综述助手" }));
    expect(writeText).toHaveBeenCalledWith("npx skills add lab/lit-helper");

    await userEvent.click(screen.getByRole("button", { name: "去充值解锁 · Nature 科研配色绘图" }));
    expect(onRecharge).toHaveBeenCalledOnce();

    await userEvent.click(screen.getByRole("button", { name: "科研绘图" }));
    expect(screen.getByText("Nature 科研配色绘图")).toBeInTheDocument();
    expect(screen.queryByText("文献综述助手")).toBeNull();

    await userEvent.click(screen.getByRole("button", { name: "全部" }));
    await userEvent.click(screen.getByRole("button", { name: "热门" }));
    expect(screen.getByText("#1")).toBeInTheDocument();
    expect(screen.getByText("420 次")).toBeInTheDocument();
  });
});
