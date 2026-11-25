import { useState } from "react";
import { FileText, Plus } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Field, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Elevated } from "@/lib/elevated";
import { fontWeights } from "@/lib/font-weight";
import { typeClass } from "@/lib/size-context";
import { cn } from "@/lib/utils";
import type { Agent, Prompt, PromptArgs } from "@/lib/desktop";

type Run = (
  action: "prompt-save" | "prompt-enable" | "prompt-delete",
  args: PromptArgs,
) => Promise<boolean>;

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
    if (await run("prompt-save", { promptId: editing || undefined, name, content })) setEditing(null);
  };

  return (
    <section aria-label="提示词" className="flex flex-col gap-4">
      <p className={cn(typeClass("caption"), "m-0 text-muted-foreground")}>
        启用后会写入 {target}；你原来手写的内容会先自动备份，不会丢。同一时间只启用一条。
      </p>

      {prompts.length === 0 && editing === null && (
        <Elevated offset={2} shadowLevel={1} className="rounded-xl">
          <Card className="flex flex-col items-center gap-1 rounded-xl px-6 py-10 text-center">
            <p className={cn(typeClass("subtitle"), "m-0 text-foreground")} style={{ fontVariationSettings: fontWeights.semibold }}>
              还没有提示词
            </p>
            <small className={cn(typeClass("caption"), "text-muted-foreground")}>
              例如“用学术语气回答，引用要给出处”。
            </small>
          </Card>
        </Elevated>
      )}

      {prompts.length > 0 && (
        <Elevated offset={2} shadowLevel={2} className="rounded-xl">
          <Card className="rounded-xl px-4 py-1">
            <ul className="m-0 flex flex-col divide-y divide-border/70 p-0 list-none">
              {prompts.map((p) => (
                <li key={p.id} className="flex flex-wrap items-center justify-between gap-3 py-3">
                  <div className="flex min-w-0 flex-1 items-center gap-3">
                    <div className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-surface-2 text-muted-foreground ring-1 ring-border">
                      <FileText size={15} aria-hidden="true" />
                    </div>
                    <div className="min-w-0 flex-1">
                      <div className="flex flex-wrap items-center gap-1.5">
                        <span className={cn(typeClass("body"), "text-foreground")} style={{ fontVariationSettings: fontWeights.semibold }}>
                          {p.name}
                        </span>
                        {p.enabled && (
                          <Badge variant="dot" color="green" size="compact">
                            使用中
                          </Badge>
                        )}
                      </div>
                      <span className={cn(typeClass("caption"), "block truncate text-muted-foreground")}>
                        {p.content.split("\n")[0] || "（空）"}
                      </span>
                    </div>
                  </div>
                  <div className="flex items-center gap-1">
                    <Button variant="ghost" size="compact" disabled={busy} onClick={() => begin(p)}>
                      编辑
                    </Button>
                    <Button
                      variant="tertiary"
                      size="compact"
                      disabled={busy}
                      onClick={() => void run("prompt-enable", { promptId: p.enabled ? "" : p.id })}
                    >
                      {p.enabled ? "停用" : "启用"}
                    </Button>
                    {!p.enabled && (
                      <Button
                        variant="ghost"
                        size="compact"
                        disabled={busy}
                        onClick={() => void run("prompt-delete", { promptId: p.id })}
                      >
                        删除
                      </Button>
                    )}
                  </div>
                </li>
              ))}
            </ul>
          </Card>
        </Elevated>
      )}

      {editing === null ? (
        <div>
          <Button variant="tertiary" leadingIcon={Plus} disabled={busy} onClick={() => begin()}>
            新建提示词
          </Button>
        </div>
      ) : (
        <Elevated offset={2} shadowLevel={2} className="rounded-xl">
          <Card className="flex flex-col gap-3 rounded-xl p-5">
            <Field>
              <FieldLabel htmlFor="prompt-name">名称</FieldLabel>
              <Input
                id="prompt-name"
                aria-label="提示词名称"
                value={name}
                maxLength={80}
                disabled={busy}
                onChange={(e) => setName(e.target.value)}
              />
            </Field>
            <Field>
              <FieldLabel htmlFor="prompt-content">内容</FieldLabel>
              <Textarea
                id="prompt-content"
                aria-label="提示词内容"
                value={content}
                rows={9}
                disabled={busy}
                onChange={(e) => setContent(e.target.value)}
              />
            </Field>
            <div className="flex items-center justify-end gap-2 pt-1">
              <Button variant="ghost" disabled={busy} onClick={() => setEditing(null)}>
                取消
              </Button>
              <Button variant="primary" disabled={busy || !name.trim()} onClick={() => void save()}>
                保存
              </Button>
            </div>
          </Card>
        </Elevated>
      )}
    </section>
  );
}
