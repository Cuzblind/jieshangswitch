import { useState } from "react";
import { FileText, Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import type { Agent, Prompt, PromptArgs } from "@/lib/desktop";

type Run = (
  action: "prompt-save" | "prompt-enable" | "prompt-delete",
  args: PromptArgs,
) => Promise<void> | void;

// Prompts are local files projected into CLAUDE.md / AGENTS.md by the engine.
export function PromptsPanel({
  agent,
  prompts,
  busy,
  run,
}: {
  agent: Agent;
  prompts: Prompt[];
  busy: boolean;
  run: Run;
}) {
  const [editing, setEditing] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [content, setContent] = useState("");
  const target = agent === "claude" ? "CLAUDE.md" : "AGENTS.md";
  const begin = (p?: Prompt) => {
    setEditing(p?.id ?? "");
    setName(p?.name ?? "");
    setContent(p?.content ?? "");
  };
  const save = async () => {
    await run("prompt-save", { promptId: editing || undefined, name, content });
    setEditing(null);
  };
  return (
    <section className="panel prompts" aria-label="提示词">
      <p className="panel-note">启用后会写入 {target}；你原来手写的内容会先自动备份，不会丢。同一时间只启用一条。</p>
      {prompts.length === 0 && editing === null && (
        <div className="empty">
          <p>还没有提示词</p>
          <small>例如“用学术语气回答，引用要给出处”。</small>
        </div>
      )}
      <ul className="item-list">
        {prompts.map((p) => (
          <li key={p.id}>
            <div className="item-icon">
              <FileText size={15} aria-hidden="true" />
            </div>
            <div className="item-main">
              <span className="item-name">
                {p.name}
                {p.enabled && <em className="tag on">使用中</em>}
              </span>
              <span className="item-sub">{p.content.split("\n")[0] || "（空）"}</span>
            </div>
            <div className="item-actions">
              <Button variant="ghost" size="sm" disabled={busy} onClick={() => begin(p)}>
                编辑
              </Button>
              <Button
                variant="outline"
                size="sm"
                disabled={busy}
                onClick={() => void run("prompt-enable", { promptId: p.enabled ? "" : p.id })}
              >
                {p.enabled ? "停用" : "启用"}
              </Button>
              {!p.enabled && (
                <Button variant="ghost" size="sm" disabled={busy} onClick={() => void run("prompt-delete", { promptId: p.id })}>
                  删除
                </Button>
              )}
            </div>
          </li>
        ))}
      </ul>
      {editing === null ? (
        <Button variant="outline" disabled={busy} onClick={() => begin()}>
          <Plus size={14} />
          新建提示词
        </Button>
      ) : (
        <div className="editor">
          <label>
            名称
            <input aria-label="提示词名称" value={name} maxLength={80} onChange={(e) => setName(e.target.value)} />
          </label>
          <label>
            内容
            <textarea aria-label="提示词内容" value={content} rows={9} onChange={(e) => setContent(e.target.value)} />
          </label>
          <div className="editor-actions">
            <Button variant="ghost" onClick={() => setEditing(null)}>
              取消
            </Button>
            <Button disabled={busy || !name.trim()} onClick={() => void save()}>
              保存
            </Button>
          </div>
        </div>
      )}
    </section>
  );
}
