import { CheckCircle2, Circle, Loader2 } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { typeClass } from "@/lib/size-context";
import { cn } from "@/lib/utils";

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
    <div className="flex flex-col gap-2 rounded-lg border border-border bg-surface-2 px-3 py-2.5">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <ol aria-label="安装步骤" className="m-0 flex flex-wrap items-center gap-3 p-0 list-none">
          {steps.map((step, index) => {
            const done = !busy ? false : index < current;
            const running = busy && index === current;
            return (
              <li
                key={step.phase}
                className={cn(
                  "inline-flex items-center gap-1.5",
                  typeClass("caption"),
                  done ? "done text-foreground" : running ? "running text-foreground" : "text-muted-foreground",
                )}
              >
                {done ? (
                  <CheckCircle2 size={13} className="text-success shrink-0" aria-hidden="true" />
                ) : running ? (
                  <Loader2 size={13} className="animate-spin text-fluid-accent shrink-0" aria-hidden="true" />
                ) : (
                  <Circle size={13} className="text-muted-foreground/60 shrink-0" aria-hidden="true" />
                )}
                <span>{step.label}</span>
              </li>
            );
          })}
        </ol>
        {source && sources[source] && (
          <Badge variant="solid" color="gray" size="compact">
            来源：{sources[source]}
          </Badge>
        )}
      </div>
      {line && <p className={cn(typeClass("caption"), "m-0 text-muted-foreground")}>{line}</p>}
    </div>
  );
}
