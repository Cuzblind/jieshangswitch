import { ArrowUpRight, CheckCircle2, Circle, Download, Loader2, Monitor, Play, SquareTerminal, Wrench } from "lucide-react";
import { Button } from "@/components/ui/button";
import { BrandIcon } from "@/components/BrandIcon";
import { InstallSteps } from "@/components/InstallSteps";
import type { Action, Agent, DesktopState } from "@/lib/desktop";

type Tool = NonNullable<DesktopState["tools"]>[number];

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
    <li className={installed && !pending ? "on" : ""}>
      <Icon size={14} aria-hidden="true" />
      <span className="component-name">{label}</span>
      {pending ? (
        <span className="component-value pending" aria-label="检测中" />
      ) : installed ? (
        <span className="component-value">
          <CheckCircle2 size={12} aria-hidden="true" />
          {version || "已安装"}
        </span>
      ) : install ? (
        <Button className="component-install" variant="ghost" size="sm" disabled={install.disabled} onClick={install.onClick}>
          <Download size={12} aria-hidden="true" />
          安装{label}
          <span className="sr-only"> · {install.name}</span>
        </Button>
      ) : (
        <span className="component-value">
          <Circle size={12} aria-hidden="true" />
          {available ? "待安装" : "暂不支持"}
        </span>
      )}
    </li>
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
  // Left: the next step, which is opening the CLI once everything works.
  const action: Action = needsInstall || missing ? "install-suite" : ready ? "launch" : needsRepair ? "repair" : state?.authorized ? "connect" : "open-web";
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
  const pending = !tool || checking;
  const showCheck = !checking && !!s && !missing && !needsInstall && s.state !== "none";
  return (
    <article className={`assistant ${agent}`} aria-label={name}>
      <header className="assistant-head">
        <div className="assistant-icon">
          <BrandIcon agent={agent} size={24} />
        </div>
        <div className="assistant-title">
          <h2>{name}</h2>
          {recommended && <span className="rec-badge">推荐先装</span>}
          <span className={`status ${checking ? "checking" : (s?.state ?? "checking")}`}>
            {checking ? "正在检测" : (s?.badge ?? "正在检测")}
          </span>
        </div>
      </header>
      <p className="desc">{s?.note || description}</p>
      <ul className="components" aria-label={`${name} 组件`}>
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
            needsDesktop
              ? { name, disabled: off || !!task?.busy, onClick: () => act("install-suite", agent) }
              : undefined
          }
        />
      </ul>
      {failure && (
        <p className="failure-code" role="alert">
          {failure.message}
          <span className="failure-kind">（{failure.nativeCode || failure.code}）</span>
        </p>
      )}
      {task?.busy && <InstallSteps busy phase={running?.phase} source={running?.source} />}
      {task && (
        <div className={`install-progress ${task.error ? "error" : ""}`} role={task.error ? "alert" : "status"}>
          <span>{task.error || task.line}</span>
          <div>
            {task.busy && (
              <Button variant="ghost" size="sm" disabled={cancelling} onClick={() => cancelInstall(agent)}>
                取消安装
              </Button>
            )}
            {!task.busy && (task.error || task.cancelled) && (
              <Button variant="ghost" size="sm" disabled={off} onClick={() => act("install-suite", agent)}>
                重试安装
              </Button>
            )}
            {!task.busy && task.error && (
              <Button variant="ghost" size="sm" disabled={off} onClick={() => act("support")}>
                查看诊断
              </Button>
            )}
          </div>
        </div>
      )}
      {ready && s?.tiers && s.tiers.length > 1 && onSwitchTier && (
        <div className="tier-switch" role="group" aria-label={`${name} 线路档位`}>
          <span className="tier-switch-label">线路</span>
          <div className="tier-switch-pills">
            {s.tiers.map((t) => {
              const active = t.name === s.tier;
              return (
                <button
                  key={t.name}
                  type="button"
                  aria-pressed={active}
                  disabled={off || !!task?.busy || disabled}
                  className={active ? "active" : ""}
                  onClick={() => {
                    if (!active) onSwitchTier(agent, t.name);
                  }}
                >
                  {t.label}
                </button>
              );
            })}
          </div>
        </div>
      )}
      <div className="assistant-actions">
        <Button
          className={desktopReady ? "" : demotePrimary ? "secondary-fill" : "primary"}
          size="lg"
          variant={ready && !checking && !desktopReady ? "default" : "outline"}
          disabled={off || checking || !!task?.busy || !s || disabled || s.state === "checking"}
          onClick={() => act(action, agent)}
        >
          {checking ? (
            <Loader2 size={14} className="spin" />
          ) : ready ? (
            <SquareTerminal size={14} />
          ) : missing || needsInstall ? (
            <Download size={14} />
          ) : needsRepair ? (
            <Wrench size={14} />
          ) : (
            <ArrowUpRight size={14} />
          )}
          {label}
          <span className="sr-only"> · {name}</span>
        </Button>
        {desktopReady && (
          <Button
            className="primary"
            size="lg"
            variant="default"
            disabled={off || !!task?.busy}
            onClick={() => act("launch-gui", agent)}
          >
            <Play size={14} />
            打开桌面版
            <span className="sr-only"> · {name}</span>
          </Button>
        )}
      </div>
      {showCheck && (
        <div className="tools">
          <Button
            variant="ghost"
            size="sm"
            disabled={off || !!task?.busy || disabled}
            onClick={() => act("check", agent)}
          >
            检查连接<span className="sr-only"> · {name}</span>
          </Button>
        </div>
      )}
    </article>
  );
}
