import { CheckCircle2, Circle, Loader2 } from "lucide-react";

const steps = [
  { phase: "prepare", label: "准备" },
  { phase: "download", label: "下载" },
  { phase: "verify", label: "校验" },
  { phase: "install", label: "安装" },
] as const;

const sources: Record<string, string> = {
  msstore: "微软商店",
  official: "官方安装包",
  offline: "离线包",
  "rg-adguard": "微软商店直连源",
  npmmirror: "国内镜像",
};

// Phase and source come from the engine's component status; this only renders them.
export function InstallSteps({
  phase,
  source,
  line,
  busy,
}: {
  phase?: string;
  source?: string;
  line?: string;
  busy: boolean;
}) {
  const active = steps.findIndex((s) => s.phase === phase);
  const current = active < 0 ? 0 : active;
  return (
    <div className="install-steps">
      <ol aria-label="安装步骤">
        {steps.map((step, index) => {
          const done = !busy ? false : index < current;
          const running = busy && index === current;
          return (
            <li key={step.phase} className={done ? "done" : running ? "running" : ""}>
              {done ? (
                <CheckCircle2 size={12} />
              ) : running ? (
                <Loader2 size={12} className="spin" />
              ) : (
                <Circle size={12} />
              )}
              {step.label}
            </li>
          );
        })}
      </ol>
      {source && sources[source] && <small>来源：{sources[source]}</small>}
      {line && <p>{line}</p>}
    </div>
  );
}
