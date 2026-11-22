import { BookOpen, FileArchive } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Elevated } from "@/lib/elevated";
import { fontWeights } from "@/lib/font-weight";
import { typeClass } from "@/lib/size-context";
import { cn } from "@/lib/utils";
import type { Agent, Skill } from "@/lib/desktop";

type Run = (
  action: "skill-import" | "skill-import-zip" | "skill-enable" | "skill-disable" | "skill-delete",
  dir: string,
) => Promise<boolean>;

const other = (agent: Agent) => (agent === "claude" ? "Codex" : "Claude Code");

// Skills are folders with a SKILL.md. The engine only removes folders it
// marked itself, so a skill the user added by hand is listed but left alone.
export function SkillsPanel({ agent, skills, busy, run }: { agent: Agent; skills: Skill[]; busy: boolean; run: Run }) {
  const target = agent === "claude" ? "~/.claude/skills" : "~/.codex/skills";

  return (
    <section aria-label="Skills" className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className={cn(typeClass("caption"), "m-0 text-muted-foreground")}>
          启用后复制到 {target}，新开的会话会自动加载。纳入管理后，可以在 {other(agent)} 里一键启用同一个 Skill。
        </p>
        {/* The path is chosen in the system picker; the page never sends one. */}
        <Button
          variant="tertiary"
          size="compact"
          leadingIcon={FileArchive}
          disabled={busy}
          onClick={() => void run("skill-import-zip", "")}
        >
          从 zip 导入
        </Button>
      </div>

      {skills.length === 0 && (
        <Elevated offset={2} shadowLevel={1} className="rounded-xl">
          <Card className="flex flex-col items-center gap-1 rounded-xl px-6 py-10 text-center">
            <p className={cn(typeClass("subtitle"), "m-0 text-foreground")} style={{ fontVariationSettings: fontWeights.semibold }}>
              还没有 Skills
            </p>
            <small className={cn(typeClass("caption"), "text-muted-foreground")}>
              点“从 zip 导入”，或把带 SKILL.md 的文件夹放进 {target}，回到这里就能看到。
            </small>
          </Card>
        </Elevated>
      )}

      {skills.length > 0 && (
        <Elevated offset={2} shadowLevel={2} className="rounded-xl">
          <Card className="rounded-xl px-4 py-1">
            <ul className="m-0 flex flex-col divide-y divide-border/70 p-0 list-none">
              {skills.map((s) => (
                <li key={s.dir} className="flex flex-wrap items-center justify-between gap-3 py-3">
                  <div className="flex min-w-0 flex-1 items-center gap-3">
                    <div className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-surface-2 text-muted-foreground ring-1 ring-border">
                      <BookOpen size={15} aria-hidden="true" />
                    </div>
                    <div className="min-w-0 flex-1">
                      <div className="flex flex-wrap items-center gap-1.5">
                        <span className={cn(typeClass("body"), "text-foreground")} style={{ fontVariationSettings: fontWeights.semibold }}>
                          {s.name}
                        </span>
                        {s.managed && s.enabled && (
                          <Badge variant="dot" color="green" size="compact">
                            使用中
                          </Badge>
                        )}
                        {!s.managed && (
                          <Badge variant="solid" color="gray" size="compact">
                            未管理
                          </Badge>
                        )}
                        {s.conflict && (
                          <Badge variant="solid" color="amber" size="compact">
                            同名文件夹
                          </Badge>
                        )}
                      </div>
                      <span className={cn(typeClass("caption"), "block truncate text-muted-foreground")}>
                        {s.description || s.dir}
                      </span>
                    </div>
                  </div>
                  <div className="flex items-center gap-1">
                    {s.managed ? (
                      <>
                        <Button
                          variant="tertiary"
                          size="compact"
                          disabled={busy || s.conflict}
                          onClick={() => void run(s.enabled ? "skill-disable" : "skill-enable", s.dir)}
                        >
                          {s.enabled ? "停用" : "启用"}
                          <span className="sr-only"> · {s.name}</span>
                        </Button>
                        {!s.enabled && (
                          <Button
                            variant="ghost"
                            size="compact"
                            disabled={busy}
                            onClick={() => void run("skill-delete", s.dir)}
                          >
                            删除<span className="sr-only"> · {s.name}</span>
                          </Button>
                        )}
                      </>
                    ) : s.unsupported ? (
                      <span className={cn(typeClass("caption"), "text-muted-foreground")}>{s.unsupported}</span>
                    ) : (
                      <Button
                        variant="tertiary"
                        size="compact"
                        disabled={busy}
                        onClick={() => void run("skill-import", s.dir)}
                      >
                        纳入管理<span className="sr-only"> · {s.name}</span>
                      </Button>
                    )}
                  </div>
                </li>
              ))}
            </ul>
          </Card>
        </Elevated>
      )}
    </section>
  );
}
