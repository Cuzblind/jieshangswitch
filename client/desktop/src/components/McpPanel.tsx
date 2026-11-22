import { useRef, useState } from "react";
import { Globe, Plus, SquareTerminal } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Field, FieldDescription, FieldError, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { FluidHoverHighlight } from "@/components/fluid-hover-highlight";
import { useFluidHover } from "@/hooks/use-fluid-hover";
import { Elevated } from "@/lib/elevated";
import { fontWeights } from "@/lib/font-weight";
import { useShape } from "@/lib/shape-context";
import { typeClass } from "@/lib/size-context";
import { cn } from "@/lib/utils";
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
  const transportRef = useRef<HTMLDivElement>(null);
  const transportHover = useFluidHover(transportRef, { axis: "x" });
  const shape = useShape();

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
    <section aria-label="MCP 服务" className="flex flex-col gap-4">
      <p className={cn(typeClass("caption"), "m-0 text-muted-foreground")}>
        启用后写入 {target}；第一次改动前会自动备份原文件。你自己加的服务不会被覆盖。
      </p>

      {servers.length === 0 && draft === null && (
        <Elevated offset={2} shadowLevel={1} className="rounded-xl">
          <Card className="flex flex-col items-center gap-1 rounded-xl px-6 py-10 text-center">
            <p className={cn(typeClass("subtitle"), "m-0 text-foreground")} style={{ fontVariationSettings: fontWeights.semibold }}>
              还没有 MCP 服务
            </p>
            <small className={cn(typeClass("caption"), "text-muted-foreground")}>
              MCP 让助手连接文件、数据库、浏览器等外部工具。
            </small>
          </Card>
        </Elevated>
      )}

      {servers.length > 0 && (
        <Elevated offset={2} shadowLevel={2} className="rounded-xl">
          <Card className="rounded-xl px-4 py-1">
            <ul className="m-0 flex flex-col divide-y divide-border/70 p-0 list-none">
              {servers.map((s) => (
                <li key={s.id} className="flex flex-wrap items-center justify-between gap-3 py-3">
                  <div className="flex min-w-0 flex-1 items-center gap-3">
                    <div className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-surface-2 text-muted-foreground ring-1 ring-border">
                      {s.type === "stdio" ? <SquareTerminal size={15} aria-hidden="true" /> : <Globe size={15} aria-hidden="true" />}
                    </div>
                    <div className="min-w-0 flex-1">
                      <div className="flex flex-wrap items-center gap-1.5">
                        <span className={cn(typeClass("body"), "text-foreground")} style={{ fontVariationSettings: fontWeights.semibold }}>
                          {s.name}
                        </span>
                        {s.enabled && !s.external && (
                          <Badge variant="dot" color="green" size="compact">
                            使用中
                          </Badge>
                        )}
                        {s.external && (
                          <Badge variant="solid" color="gray" size="compact">
                            外部
                          </Badge>
                        )}
                        {s.conflict && (
                          <Badge variant="solid" color="amber" size="compact">
                            名称冲突
                          </Badge>
                        )}
                      </div>
                      <code className={cn(typeClass("caption"), "block truncate font-mono text-muted-foreground")}>
                        {summary(s)}
                      </code>
                    </div>
                  </div>
                  {!s.external && (
                    <div className="flex items-center gap-1">
                      <Button
                        variant="ghost"
                        size="compact"
                        disabled={busy}
                        onClick={() => {
                          setFormError("");
                          setDraft(toDraft(s));
                        }}
                      >
                        编辑<span className="sr-only"> · {s.name}</span>
                      </Button>
                      <Button
                        variant="tertiary"
                        size="compact"
                        disabled={busy || s.conflict}
                        onClick={() => void run(s.enabled ? "mcp-disable" : "mcp-enable", { promptId: s.id })}
                      >
                        {s.enabled ? "停用" : "启用"}
                        <span className="sr-only"> · {s.name}</span>
                      </Button>
                      <Button
                        variant="ghost"
                        size="compact"
                        disabled={busy}
                        onClick={() => void run("mcp-delete", { promptId: s.id })}
                      >
                        删除<span className="sr-only"> · {s.name}</span>
                      </Button>
                    </div>
                  )}
                </li>
              ))}
            </ul>
          </Card>
        </Elevated>
      )}

      {draft === null ? (
        <div>
          <Button
            variant="tertiary"
            leadingIcon={Plus}
            disabled={busy}
            onClick={() => {
              setFormError("");
              setDraft(blank);
            }}
          >
            添加 MCP 服务
          </Button>
        </div>
      ) : (
        <Elevated offset={2} shadowLevel={2} className="rounded-xl">
          <Card className="rounded-xl p-5">
            <form
              className="flex flex-col gap-3"
              onSubmit={(e) => {
                e.preventDefault();
                void save();
              }}
            >
              <Field>
                <FieldLabel htmlFor="mcp-name">名称</FieldLabel>
                <Input
                  id="mcp-name"
                  value={draft.name}
                  maxLength={40}
                  placeholder="例如 filesystem"
                  onChange={(e) => edit({ name: e.target.value })}
                />
              </Field>

              <div
                ref={transportRef}
                role="group"
                aria-label="连接方式"
                className="relative inline-flex w-fit items-center gap-0.5 rounded-lg bg-surface-2 p-0.5 ring-1 ring-border"
                {...transportHover.handlers}
              >
                <FluidHoverHighlight hover={transportHover} className={cn(shape.bg, "z-0")} />
                {transports.map((t, index) => {
                  const active = draft.type === t.value;
                  return (
                    <button
                      key={t.value}
                      ref={(el) => transportHover.registerItem(index, el)}
                      type="button"
                      aria-pressed={active}
                      onClick={() => edit({ type: t.value })}
                      className={cn(
                        "relative z-10 inline-flex h-7 items-center rounded-md px-3 whitespace-nowrap transition-colors duration-80",
                        typeClass("caption"),
                        active ? "bg-active text-foreground" : "text-muted-foreground hover:text-foreground",
                      )}
                      style={{ fontVariationSettings: active ? fontWeights.semibold : fontWeights.normal }}
                    >
                      {t.label}
                    </button>
                  );
                })}
              </div>

              {draft.type === "stdio" ? (
                <>
                  <Field>
                    <FieldLabel htmlFor="mcp-cmd">命令</FieldLabel>
                    <Input
                      id="mcp-cmd"
                      value={draft.command}
                      placeholder="例如 npx"
                      onChange={(e) => edit({ command: e.target.value })}
                    />
                  </Field>
                  <Field>
                    <FieldLabel htmlFor="mcp-args">参数（每行一个）</FieldLabel>
                    <Textarea
                      id="mcp-args"
                      rows={3}
                      value={draft.args}
                      placeholder={"-y\n@modelcontextprotocol/server-filesystem"}
                      onChange={(e) => edit({ args: e.target.value })}
                    />
                  </Field>
                  <Field>
                    <FieldLabel htmlFor="mcp-env">环境变量（每行一个 名称=值）</FieldLabel>
                    <Textarea
                      id="mcp-env"
                      rows={2}
                      value={draft.env}
                      placeholder="API_KEY=…"
                      onChange={(e) => edit({ env: e.target.value })}
                    />
                    {draft.id && <FieldDescription>已保存的值不会显示；“名称=”留空表示保留原值。</FieldDescription>}
                  </Field>
                  <Field>
                    <FieldLabel htmlFor="mcp-cwd">工作目录（可选）</FieldLabel>
                    <Input
                      id="mcp-cwd"
                      value={draft.cwd}
                      placeholder="留空则由助手决定"
                      onChange={(e) => edit({ cwd: e.target.value })}
                    />
                  </Field>
                </>
              ) : (
                <>
                  <Field>
                    <FieldLabel htmlFor="mcp-url">地址</FieldLabel>
                    <Input
                      id="mcp-url"
                      value={draft.url}
                      placeholder={draft.type === "sse" ? "https://example.com/sse" : "https://example.com/mcp"}
                      onChange={(e) => edit({ url: e.target.value })}
                    />
                  </Field>
                  <Field>
                    <FieldLabel htmlFor="mcp-headers">请求头（每行一个 名称=值）</FieldLabel>
                    <Textarea
                      id="mcp-headers"
                      rows={2}
                      value={draft.headers}
                      placeholder="Authorization=Bearer …"
                      onChange={(e) => edit({ headers: e.target.value })}
                    />
                    {draft.id && <FieldDescription>已保存的值不会显示；“名称=”留空表示保留原值。</FieldDescription>}
                  </Field>
                </>
              )}

              {formError && <FieldError>{formError}</FieldError>}

              <div className="flex items-center justify-end gap-2 pt-1">
                <Button type="button" variant="ghost" onClick={() => setDraft(null)}>
                  取消
                </Button>
                <Button type="submit" variant="primary" disabled={busy || !ready}>
                  保存
                </Button>
              </div>
            </form>
          </Card>
        </Elevated>
      )}
    </section>
  );
}
