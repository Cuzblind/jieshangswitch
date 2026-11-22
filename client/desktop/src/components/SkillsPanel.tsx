import { BookOpen, FileArchive } from "lucide-react";
import { Button } from "@/components/ui/button";
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
    <section className="panel" aria-label="Skills">
      <div className="panel-actions">
        {/* The path is chosen in the system picker; the page never sends one. */}
        <Button variant="outline" size="sm" disabled={busy} onClick={() => void run("skill-import-zip", "")}>
          <FileArchive size={14} aria-hidden="true" />
          从 zip 导入
        </Button>
      </div>
      <p className="panel-note">
        启用后复制到 {target}，新开的会话会自动加载。纳入管理后，可以在 {other(agent)} 里一键启用同一个 Skill。
      </p>
      {skills.length === 0 && (
        <div className="empty">
          <p>还没有 Skills</p>
          <small>点“从 zip 导入”，或把带 SKILL.md 的文件夹放进 {target}，回到这里就能看到。</small>
        </div>
      )}
      <ul className="item-list">
        {skills.map((s) => (
          <li key={s.dir} className={s.managed ? "" : "external"}>
            <div className="item-icon">
              <BookOpen size={15} aria-hidden="true" />
            </div>
            <div className="item-main">
              <span className="item-name">
                {s.name}
                {s.managed && s.enabled && <em className="tag on">使用中</em>}
                {!s.managed && <em className="tag">未管理</em>}
                {s.conflict && <em className="tag warn">同名文件夹</em>}
              </span>
              <span className="item-sub">{s.description || s.dir}</span>
            </div>
            <div className="item-actions">
              {s.managed ? (
                <>
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={busy || s.conflict}
                    onClick={() => void run(s.enabled ? "skill-disable" : "skill-enable", s.dir)}
                  >
                    {s.enabled ? "停用" : "启用"}
                    <span className="sr-only"> · {s.name}</span>
                  </Button>
                  {!s.enabled && (
                    <Button variant="ghost" size="sm" disabled={busy} onClick={() => void run("skill-delete", s.dir)}>
                      删除<span className="sr-only"> · {s.name}</span>
                    </Button>
                  )}
                </>
              ) : s.unsupported ? (
                <span className="item-sub">{s.unsupported}</span>
              ) : (
                <Button variant="outline" size="sm" disabled={busy} onClick={() => void run("skill-import", s.dir)}>
                  纳入管理<span className="sr-only"> · {s.name}</span>
                </Button>
              )}
            </div>
          </li>
        ))}
      </ul>
    </section>
  );
}
