import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { McpPanel } from "./components/McpPanel";
import type { McpServer } from "./lib/desktop";

const servers: McpServer[] = [
  { id: "m-1", name: "filesystem", type: "stdio", command: "npx", args: ["-y", "fs"], env: { TOKEN: "" }, enabled: true },
  { id: "ext:mine", name: "mine", type: "http", url: "https://x", enabled: true, external: true },
];

describe("McpPanel", () => {
  it("names the target file and leaves external servers read-only", () => {
    render(<McpPanel agent="codex" servers={servers} busy={false} run={vi.fn()} />);
    expect(screen.getByText(/config\.toml/)).toBeInTheDocument();
    expect(screen.getByText("外部")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "停用 · filesystem" })).toBeEnabled();
    expect(screen.queryByRole("button", { name: "停用 · mine" })).not.toBeInTheDocument();
  });

  it("disables an enabled server by id", async () => {
    const run = vi.fn().mockResolvedValue(true);
    render(<McpPanel agent="claude" servers={servers} busy={false} run={run} />);
    await userEvent.click(screen.getByRole("button", { name: "停用 · filesystem" }));
    expect(run).toHaveBeenCalledWith("mcp-disable", { promptId: "m-1" });
  });

  it("saves a local command server with arguments and env", async () => {
    const run = vi.fn().mockResolvedValue(true);
    render(<McpPanel agent="claude" servers={[]} busy={false} run={run} />);
    await userEvent.click(screen.getByRole("button", { name: "添加 MCP 服务" }));
    await userEvent.type(screen.getByLabelText("名称"), "db");
    await userEvent.type(screen.getByLabelText("命令"), "uvx");
    await userEvent.type(screen.getByLabelText("参数（每行一个）"), "mcp-db{enter}--ro");
    await userEvent.type(screen.getByLabelText("环境变量（每行一个 名称=值）"), "KEY=abc");
    await userEvent.click(screen.getByRole("button", { name: "保存" }));
    expect(run).toHaveBeenCalledWith("mcp-save", {
      mcp: { id: "", name: "db", type: "stdio", command: "uvx", args: ["mcp-db", "--ro"], env: { KEY: "abc" } },
    });
  });

  it("rejects an env line without a value separator", async () => {
    const run = vi.fn();
    render(<McpPanel agent="claude" servers={[]} busy={false} run={run} />);
    await userEvent.click(screen.getByRole("button", { name: "添加 MCP 服务" }));
    await userEvent.type(screen.getByLabelText("名称"), "db");
    await userEvent.type(screen.getByLabelText("命令"), "uvx");
    await userEvent.type(screen.getByLabelText("环境变量（每行一个 名称=值）"), "BROKEN");
    await userEvent.click(screen.getByRole("button", { name: "保存" }));
    expect(screen.getByRole("alert")).toHaveTextContent("名称=值");
    expect(run).not.toHaveBeenCalled();
  });
});
