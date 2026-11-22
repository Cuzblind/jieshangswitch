import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";
import { McpPanel } from "@/components/McpPanel";

it("adds an SSE server with request headers", async () => {
  const run = vi.fn().mockResolvedValue(true);
  render(<McpPanel agent="claude" servers={[]} busy={false} run={run} />);
  await userEvent.click(screen.getByRole("button", { name: "添加 MCP 服务" }));
  await userEvent.type(screen.getByLabelText("名称"), "events");
  await userEvent.click(screen.getByRole("button", { name: "SSE" }));
  await userEvent.type(screen.getByLabelText("地址"), "https://mcp.example.com/sse");
  await userEvent.type(screen.getByLabelText("请求头（每行一个 名称=值）"), "X-Api-Key=k2");
  await userEvent.click(screen.getByRole("button", { name: "保存" }));
  expect(run).toHaveBeenLastCalledWith("mcp-save", {
    mcp: { id: "", name: "events", type: "sse", url: "https://mcp.example.com/sse", headers: { "X-Api-Key": "k2" } },
  });
});

it("adds a local command with a working directory", async () => {
  const run = vi.fn().mockResolvedValue(true);
  render(<McpPanel agent="codex" servers={[]} busy={false} run={run} />);
  await userEvent.click(screen.getByRole("button", { name: "添加 MCP 服务" }));
  await userEvent.type(screen.getByLabelText("名称"), "files");
  await userEvent.type(screen.getByLabelText("命令"), "npx");
  await userEvent.type(screen.getByLabelText("工作目录（可选）"), "/srv/work");
  await userEvent.click(screen.getByRole("button", { name: "保存" }));
  expect(run).toHaveBeenLastCalledWith("mcp-save", {
    mcp: { id: "", name: "files", type: "stdio", command: "npx", args: [], env: {}, cwd: "/srv/work" },
  });
});

it("shows SSE servers with the network icon label", () => {
  render(
    <McpPanel
      agent="claude"
      servers={[{ id: "m-1", name: "events", type: "sse", url: "https://x/sse", enabled: true }]}
      busy={false}
      run={vi.fn()}
    />,
  );
  expect(screen.getByText("SSE · https://x/sse")).toBeInTheDocument();
});
