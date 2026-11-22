import { useRef } from "react";
import {
  Activity,
  ArrowUpRight,
  CheckCircle2,
  Circle,
  Download,
  Loader2,
  MessageSquare,
  Monitor,
  Play,
  SquareTerminal,
  Wrench,
} from "lucide-react";
import { Badge, type BadgeColor } from "@/components/ui/badge";
import { Banner, BannerDescription, BannerTitle } from "@/components/ui/banner";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { FluidHoverHighlight } from "@/components/fluid-hover-highlight";
import { BrandIcon } from "@/components/BrandIcon";
import { InstallSteps } from "@/components/InstallSteps";
import { useFluidHover } from "@/hooks/use-fluid-hover";
import { Elevated } from "@/lib/elevated";
import { fontWeights } from "@/lib/font-weight";
import { useShape } from "@/lib/shape-context";
import { typeClass } from "@/lib/size-context";
import { cn } from "@/lib/utils";
import type { Action, Agent, DesktopState } from "@/lib/desktop";

type Tool = NonNullable<DesktopState["tools"]>[number];

const TIER_HINTS: Record<string, string> = {
  economy: "倍率更低，适合日常问答与轻度任务",
  balanced: "速度与成本均衡，适合绝大多数日常开发与写作",
  stable: "高可用通道，适合长上下文与重要复杂任务",
};

function statusBadgeColor(state?: string, checking?: boolean): BadgeColor {
  if (checking) return "gray";
  switch (state) {
    case "ok":
      return "green";
    case "checking":
    case "expired":
    case "offline":
    case "conflict":
      return "amber";
    case "broken":
    case "disabled":
      return "red";
    default:
      return "gray";
  }
}

// Nothing is missing before the engine has finished its first probe.
export function missingTool(tool: Tool | undefined): boolean {
  return (
    !!tool &&
    tool.inspected !== false &&
    (!tool.cliInstalled || (tool.guiAvailable && tool.guiInspected !== false && !tool.guiInstalled))
  );
}

function ComponentRow({
  icon: Icon,
  label,
  installed,
  version,
  available = true,
  pending = false,
  install,
}: {
  icon: typeof Monitor;
  label: string;
  installed: boolean;
  version?: string;
  available?: boolean;
  pending?: boolean;
  // Shown in place of "待安装" when this component can be installed on its own.
  install?: { name: string; disabled: boolean; onClick: () => void };
}) {
  return (
    <li className="flex min-h-9 items-center justify-between gap-3 border-b border-border/60 py-1.5 last:border-b-0">
      <div className="flex items-center gap-2 text-muted-foreground">
        <Icon size={14} strokeWidth={1.75} aria-hidden="true" className={installed && !pending ? "text-foreground" : ""} />
        <span className={cn(typeClass("body"), installed && !pending ? "text-foreground" : "text-muted-foreground")}>
          {label}
        </span>
      </div>
      <div className="flex items-center">
        {pending ? (
          <span
            aria-label="检测中"
            className="inline-block h-4 w-16 animate-pulse rounded bg-hover"
          />
        ) : installed ? (
          <Badge variant="ghost" color="green" size="compact">
            {version || "已安装"}
          </Badge>
        ) : install ? (
          <Button
            variant="ghost"
            size="compact"
            leadingIcon={Download}
            disabled={install.disabled}
            onClick={install.onClick}
          >
            安装{label}
            <span className="sr-only"> · {install.name}</span>
          </Button>
        ) : (
          <Badge variant="ghost" color="gray" size="compact">
            {available ? "待安装" : "暂不支持"}
          </Badge>
        )}
      </div>
    </li>
  );
}

function TierSelector({
  agent,
  name,
  tier,
  tiers,
  disabled,
  onSwitchTier,
}: {
  agent: Agent;
  name: string;
  tier?: string;
  tiers: NonNullable<NonNullable<DesktopState["statuses"]>[number]["tiers"]>;
  disabled: boolean;
  onSwitchTier: (agent: Agent, tier: string) => void;
}) {
  const containerRef = useRef<HTMLDivElement>(null);
  const hover = useFluidHover(containerRef, { axis: "x" });
  const shape = useShape();
  const activeTier = tiers.find((t) => t.name === tier) ?? tiers[0];
  const hint = activeTier ? TIER_HINTS[activeTier.name] : "";

  return (
    <div className="flex flex-col gap-1.5 rounded-lg bg-surface-2 px-3 py-2.5 ring-1 ring-border/70">
      <div className="flex items-center justify-between gap-2">
        <span className={cn(typeClass("caption"), "text-muted-foreground")}>当前线路</span>
        {activeTier?.recommended && (
          <Badge variant="solid" color="accent" size="compact">
            推荐
          </Badge>
        )}
      </div>
      <div
        ref={containerRef}
        role="group"
        aria-label={`${name} 线路档位`}
        className="relative flex items-center gap-0.5 rounded-lg bg-background p-0.5 ring-1 ring-border/70"
        {...hover.handlers}
      >
        <FluidHoverHighlight hover={hover} className={cn(shape.bg, "z-0")} />
        {tiers.map((t, index) => {
          const active = t.name === tier;
          return (
            <button
              key={t.name}
              ref={(el) => hover.registerItem(index, el)}
              type="button"
              aria-pressed={active}
              disabled={disabled}
              onClick={() => {
                if (!active) onSwitchTier(agent, t.name);
              }}
              className={cn(
                "relative z-10 flex h-7 flex-1 items-center justify-center rounded-md px-2 whitespace-nowrap transition-colors duration-80 disabled:opacity-50",
                typeClass("caption"),
                active
                  ? "bg-active text-foreground"
                  : "text-muted-foreground hover:text-foreground",
              )}
              style={{ fontVariationSettings: active ? fontWeights.semibold : fontWeights.normal }}
            >
              {t.label}
            </button>
          );
        })}
      </div>
      {hint && <p className={cn(typeClass("micro"), "m-0 text-muted-foreground")}>{hint}</p>}
    </div>
  );
}

export function AssistantCard({
  agent,
  name,
  description,
  state,
  busy,
  unavailable,
  cancelling,
  recommended = false,
  demotePrimary = false,
  act,
  cancelInstall,
  onSwitchTier,
}: {
  agent: Agent;
  name: string;
  description: string;
  state?: DesktopState;
  busy: boolean;
  unavailable: boolean;
  cancelling: boolean;
  recommended?: boolean;
  demotePrimary?: boolean;
  act: (action: Action, agent?: Agent) => void;
  cancelInstall: (agent?: Agent) => void;
  onSwitchTier?: (agent: Agent, tier: string) => void;
}) {
  const s = state?.statuses?.find((item) => item.agent === agent);
  const tool = state?.tools?.find((item) => item.agent === agent);
  const task = state?.installTasks?.find((item) => item.agent === agent);
  const checking = tool?.inspected === false;
  // The desktop-app check finishes seconds after the CLI one.
  const guiChecking = checking || tool?.guiInspected === false;
  const needsInstall = missingTool(tool) && !tool?.cliInstalled;
  const needsDesktop = !guiChecking && !!tool?.cliInstalled && tool.guiAvailable && !tool.guiInstalled;
  const ready = s?.state === "ok";
  const missing = s?.state === "nocli";
  const disabled = s?.state === "disabled";
  const needsRepair = ["broken", "offline", "expired", "conflict"].includes(s?.state ?? "");
  // GUI-first: once connected on macOS/Windows, the primary CTA opens or installs
  // the official desktop app while keeping the CLI terminal one click away.
  const action: Action =
    needsInstall || missing
      ? "install-suite"
      : ready
        ? "launch"
        : needsRepair
          ? "repair"
          : state?.authorized
            ? "connect"
            : "open-web";
  const label = task?.busy
    ? "正在安装…"
    : checking
      ? "正在检测"
      : disabled
        ? "账户暂不可用"
        : needsInstall
          ? "安装应用"
          : ready
            ? "打开命令行"
            : missing
              ? "安装助手"
              : needsRepair
                ? "一键修复"
                : state?.authorized
                  ? "接上助手"
                  : "去网页接上";
  const running = tool?.guiInstall?.status === "running" ? tool.guiInstall : tool?.cliInstall;
  const failure = tool?.cliInstall?.failure || tool?.guiInstall?.failure;
  const off = busy || unavailable;
  const desktopReady = ready && !guiChecking && !!tool?.guiInstalled;
  const desktopInstallPrimary = ready && needsDesktop && !task?.busy;
  const pending = !tool || checking;
  const showCheck = !checking && !!s && !missing && !needsInstall && s.state !== "none";
  const primaryLeadingIcon = checking
    ? Loader2
    : ready
      ? SquareTerminal
      : missing || needsInstall
        ? Download
        : needsRepair
          ? Wrench
          : ArrowUpRight;

  const isPrimaryCta = !desktopReady && !desktopInstallPrimary && !demotePrimary;

  return (
    <Elevated offset={2} shadowLevel={2} className="flex flex-col rounded-xl">
      <article aria-label={name} className="flex h-full flex-col">
        <Card className="flex h-full flex-col gap-4 rounded-xl p-5">
          <CardHeader className="p-0">
            <div className="flex items-start justify-between gap-3">
              <div className="flex items-center gap-3 min-w-0">
                <div className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-surface-2 ring-1 ring-border">
                  <BrandIcon agent={agent} size={22} />
                </div>
                <div className="min-w-0">
                  <div className="flex flex-wrap items-center gap-2">
                    <CardTitle className={cn(typeClass("title"), "m-0 text-foreground")} style={{ fontVariationSettings: fontWeights.semibold }}>
                      {name}
                    </CardTitle>
                    {recommended && (
                      <Badge variant="solid" color="accent" size="compact">
                        推荐先装
                      </Badge>
                    )}
                  </div>
                  <div className="mt-1">
                    <Badge variant="dot" color={statusBadgeColor(s?.state, checking)} size="compact">
                      {checking ? "正在检测" : (s?.badge ?? "正在检测")}
                    </Badge>
                  </div>
                </div>
              </div>
            </div>
            <CardDescription className={cn(typeClass("body"), "mt-2 text-muted-foreground")}>
              {s?.note || description}
            </CardDescription>
          </CardHeader>

          <CardContent className="flex flex-1 flex-col gap-3 p-0">
            <ul className="m-0 flex flex-col p-0 list-none" aria-label={`${name} 组件`}>
              <ComponentRow
                icon={SquareTerminal}
                label="命令行"
                installed={!!tool?.cliInstalled}
                version={tool?.cliVersion}
                pending={pending}
              />
              <ComponentRow
                icon={Monitor}
                label="桌面版"
                installed={!!tool?.guiInstalled}
                version={tool?.guiVersion}
                available={tool?.guiAvailable ?? true}
                pending={!tool || guiChecking}
                install={
                  needsDesktop && !desktopInstallPrimary
                    ? { name, disabled: off || !!task?.busy, onClick: () => act("install-suite", agent) }
                    : undefined
                }
              />
            </ul>

            {failure && (
              <Banner status="error" contrast="low" size="compact">
                <BannerTitle>{failure.message}</BannerTitle>
                <BannerDescription>错误码：{failure.nativeCode || failure.code}</BannerDescription>
              </Banner>
            )}

            {task?.busy && <InstallSteps busy phase={running?.phase} source={running?.source} />}

            {task && (
              <div
                role={task.error ? "alert" : "status"}
                className={cn(
                  "flex flex-wrap items-center justify-between gap-2 rounded-lg px-3 py-2",
                  typeClass("caption"),
                  task.error ? "bg-destructive-light text-destructive" : "bg-surface-2 text-muted-foreground",
                )}
              >
                <span className="min-w-0 flex-1">{task.error || task.line}</span>
                <div className="flex items-center gap-1">
                  {task.busy && (
                    <Button variant="ghost" size="compact" disabled={cancelling} onClick={() => cancelInstall(agent)}>
                      取消安装
                    </Button>
                  )}
                  {!task.busy && (task.error || task.cancelled) && (
                    <Button variant="ghost" size="compact" disabled={off} onClick={() => act("install-suite", agent)}>
                      重试安装
                    </Button>
                  )}
                  {!task.busy && task.error && (
                    <Button variant="ghost" size="compact" disabled={off} onClick={() => act("support")}>
                      查看诊断
                    </Button>
                  )}
                </div>
              </div>
            )}

            {ready && s?.tiers && s.tiers.length > 1 && onSwitchTier && (
              <TierSelector
                agent={agent}
                name={name}
                tier={s.tier}
                tiers={s.tiers}
                disabled={off || !!task?.busy || disabled}
                onSwitchTier={onSwitchTier}
              />
            )}
          </CardContent>

          <CardFooter className="mt-auto flex flex-col items-stretch gap-2 p-0 pt-2">
            <div className="flex flex-wrap items-center gap-2">
              {desktopReady && (
                <Button
                  className="primary flex-1"
                  variant="primary"
                  leadingIcon={Play}
                  disabled={off || !!task?.busy}
                  onClick={() => act("launch-gui", agent)}
                >
                  打开桌面版
                  <span className="sr-only"> · {name}</span>
                </Button>
              )}
              {desktopInstallPrimary && (
                <Button
                  className="primary flex-1"
                  variant="primary"
                  leadingIcon={Download}
                  disabled={off || !!task?.busy}
                  onClick={() => act("install-suite", agent)}
                >
                  安装桌面版
                  <span className="sr-only"> · {name}</span>
                </Button>
              )}
              <Button
                className={cn("flex-1", isPrimaryCta && "primary")}
                variant={isPrimaryCta ? "primary" : "tertiary"}
                loading={!!task?.busy}
                leadingIcon={task?.busy ? undefined : primaryLeadingIcon}
                disabled={off || checking || !!task?.busy || !s || disabled || s.state === "checking"}
                onClick={() => act(action, agent)}
              >
                {label}
                <span className="sr-only"> · {name}</span>
              </Button>
            </div>

            {showCheck && (
              <div className="flex items-center justify-end gap-1 border-t border-border/60 pt-2">
                <Button
                  variant="ghost"
                  size="compact"
                  leadingIcon={Activity}
                  disabled={off || !!task?.busy || disabled}
                  onClick={() => act("check", agent)}
                >
                  检查连接<span className="sr-only"> · {name}</span>
                </Button>
                {ready && (
                  <Button
                    variant="ghost"
                    size="compact"
                    leadingIcon={MessageSquare}
                    disabled={off || !!task?.busy || disabled}
                    onClick={() => act("test-chat", agent)}
                  >
                    测试对话<span className="sr-only"> · {name}</span>
                  </Button>
                )}
              </div>
            )}
          </CardFooter>
        </Card>
      </article>
    </Elevated>
  );
}
