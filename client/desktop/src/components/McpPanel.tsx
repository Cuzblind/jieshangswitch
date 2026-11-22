import { useState } from "react";
import { Globe, Plus, SquareTerminal } from "lucide-react";
import { Button } from "@/components/ui/button";
import type { Agent, McpInput, McpServer } from "@/lib/desktop";

type Run = (
  action: "mcp-save" | "mcp-enable" | "mcp-disable" | "mcp-delete",
  args: { promptId?: string; mcp?: McpInput },
) => Promise<boolean>;

type Transport = McpServer["type"];

interface Draft {
  id: string;
  name: string;
  type: Transport;
  command: string;
  args: string;
  env: string;
  cwd: string;
  url: string;
  headers: string;
}

const blank: Draft = { id: "", name: "", type: "stdio", command: "", args: "", env: "", cwd: "", url: "", headers: "" };

const transports: Array<{ value: Transport; label: string }> = [
  { value: "stdio", label: "本地命令" },
  { value: "http", label: "HTTP" },
  { value: "sse", label: "SSE" },
];

// Stored values never reach the page; "KEY=" keeps the saved value.
const keysOnly = (values?: Record<string, string>) =>
  Object.keys(values ?? {})
    .map((k) => `${k}=`)
    .join("\n");

function toDraft(s: McpServer): Draft {
  return {
    id: s.id,
    name: s.name,
    type: s.type,
    command: s.command ?? "",
    args: (s.args ?? []).join("\n"),
    env: keysOnly(s.env),
    cwd: s.cwd ?? "",
    url: s.url ?? "",
    headers: keysOnly(s.headers),
  };
}

function pairs(text: string, what: string): Record<string, string> | string {
  const out: Record<string, string> = {};
  for (const line of text.split("\n").map((l) => l.trim()).filter(Boolean)) {
    const at = line.indexOf("=");
    if (at <= 0) return `${what}“${line}”要写成 名称=值`;
    out[line.slice(0, at).trim()] = line.slice(at + 1);
  }
  return out;
}

function toInput(d: Draft): McpInput | string {
  if (d.type !== "stdio") {
    const headers = pairs(d.headers, "请求头");
    if (typeof headers === "string") return headers;
    const input: McpInput = { id: d.id, name: d.name.trim(), type: d.type, url: d.url.trim() };
    if (Object.keys(headers).length > 0) input.headers = headers;
    return input;
  }
  const env = pairs(d.env, "环境变量");
  if (typeof env === "string") return env;
  const input: McpInput = {
    id: d.id,
    name: d.name.trim(),
    type: "stdio",
    command: d.command.trim(),
    args: d.args.split("\n").map((a) => a.trim()).filter(Boolean),
    env,
  };
  if (d.cwd.trim()) input.cwd = d.cwd.trim();
  return input;
}

function summary(s: McpServer): string {
  if (s.external) return "你自己添加的服务，这里只显示，不会改动";
  if (s.type === "sse") return `SSE · ${s.url ?? ""}`;
  if (s.type === "http") return s.url ?? "";
  return [s.command, ...(s.args ?? [])].join(" ");
}

// MCP servers are stored locally and written into ~/.claude.json or
// ~/.codex/config.toml by the engine only while enabled.
export function McpPanel({
  agent,
  servers,
  busy,
  run,
}: {
  agent: Agent;
  servers: McpServer[];
  busy: boolean;
  run: Run;
}) {
  const [draft, setDraft] = useState<Draft | null>(null);
  const [formError, setFormError] = useState("");
  const target = agent === "claude" ? "~/.claude.json" : "~/.codex/config.toml";
  const edit = (patch: Partial<Draft>) => setDraft((d) => (d ? { ...d, ...patch } : d));
  const save = async () => {
    if (!draft) return;
    const input = toInput(draft);
    if (typeof input === "string") {
      setFormError(input);
      return;
    }
    setFormError("");
    if (await run("mcp-save", { mcp: input })) setDraft(null);
  };
  const ready =
    !!draft?.name.trim() && (draft.type === "stdio" ? !!draft.command.trim() : !!draft.url.trim());
  return (
    <section className="panel" aria-label="MCP 服务">
      <p className="panel-note">启用后写入 {target}；第一次改动前会自动备份原文件。你自己加的服务不会被覆盖。</p>
      {servers.length === 0 && draft === null && (
        <div className="empty">
          <p>还没有 MCP 服务</p>
          <small>MCP 让助手连接文件、数据库、浏览器等外部工具。</small>
        </div>
      )}
      <ul className="item-list">
        {servers.map((s) => (
          <li key={s.id} className={s.external ? "external" : ""}>
            <div className="item-icon">
              {s.type === "stdio" ? <SquareTerminal size={15} aria-hidden="true" /> : <Globe size={15} aria-hidden="true" />}
            </div>
            <div className="item-main">
              <span className="item-name">
                {s.name}
                {s.enabled && !s.external && <em className="tag on">使用中</em>}
                {s.external && <em className="tag">外部</em>}
                {s.conflict && <em className="tag warn">名称冲突</em>}
              </span>
              <code className="item-sub">{summary(s)}</code>
            </div>
            {!s.external && (
              <div className="item-actions">
                <Button variant="ghost" size="sm" disabled={busy} onClick={() => { setFormError(""); setDraft(toDraft(s)); }}>
                  编辑<span className="sr-only"> · {s.name}</span>
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  disabled={busy || s.conflict}
                  onClick={() => void run(s.enabled ? "mcp-disable" : "mcp-enable", { promptId: s.id })}
                >
                  {s.enabled ? "停用" : "启用"}
                  <span className="sr-only"> · {s.name}</span>
                </Button>
                <Button variant="ghost" size="sm" disabled={busy} onClick={() => void run("mcp-delete", { promptId: s.id })}>
                  删除<span className="sr-only"> · {s.name}</span>
                </Button>
              </div>
            )}
          </li>
        ))}
      </ul>
      {draft === null ? (
        <Button variant="outline" disabled={busy} onClick={() => { setFormError(""); setDraft(blank); }}>
          <Plus size={14} />
          添加 MCP 服务
        </Button>
      ) : (
        <form
          className="editor"
          onSubmit={(e) => {
            e.preventDefault();
            void save();
          }}
        >
          <label>
            名称
            <input
              value={draft.name}
              maxLength={40}
              placeholder="例如 filesystem"
              onChange={(e) => edit({ name: e.target.value })}
            />
          </label>
          <div className="segmented" role="group" aria-label="连接方式">
            {transports.map((t) => (
              <button key={t.value} type="button" aria-pressed={draft.type === t.value} onClick={() => edit({ type: t.value })}>
                {t.label}
              </button>
            ))}
          </div>
          {draft.type === "stdio" ? (
            <>
              <label>
                命令
                <input value={draft.command} placeholder="例如 npx" onChange={(e) => edit({ command: e.target.value })} />
              </label>
              <label>
                参数（每行一个）
                <textarea
                  rows={3}
                  value={draft.args}
                  placeholder={"-y\n@modelcontextprotocol/server-filesystem"}
                  onChange={(e) => edit({ args: e.target.value })}
                />
              </label>
              <label>
                环境变量（每行一个 名称=值）
                <textarea
                  rows={2}
                  value={draft.env}
                  placeholder="API_KEY=…"
                  onChange={(e) => edit({ env: e.target.value })}
                />
                {draft.id && <small>已保存的值不会显示；“名称=”留空表示保留原值。</small>}
              </label>
              <label>
                工作目录（可选）
                <input value={draft.cwd} placeholder="留空则由助手决定" onChange={(e) => edit({ cwd: e.target.value })} />
              </label>
            </>
          ) : (
            <>
              <label>
                地址
                <input
                  value={draft.url}
                  placeholder={draft.type === "sse" ? "https://example.com/sse" : "https://example.com/mcp"}
                  onChange={(e) => edit({ url: e.target.value })}
                />
              </label>
              <label>
                请求头（每行一个 名称=值）
                <textarea
                  rows={2}
                  value={draft.headers}
                  placeholder="Authorization=Bearer …"
                  onChange={(e) => edit({ headers: e.target.value })}
                />
                {draft.id && <small>已保存的值不会显示；“名称=”留空表示保留原值。</small>}
              </label>
            </>
          )}
          {formError && (
            <p className="form-error" role="alert">
              {formError}
            </p>
          )}
          <div className="editor-actions">
            <Button type="button" variant="ghost" onClick={() => setDraft(null)}>
              取消
            </Button>
            <Button type="submit" disabled={busy || !ready}>
              保存
            </Button>
          </div>
        </form>
      )}
    </section>
  );
}
