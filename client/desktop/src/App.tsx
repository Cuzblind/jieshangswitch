import { useEffect, useRef, useState } from "react";
import "@/i18n";
import {
  ArrowLeftRight,
  ArrowUpRight,
  BarChart3,
  BookOpen,
  Building2,
  ChevronRight,
  ChevronsUpDown,
  Cpu,
  ExternalLink,
  FileText,
  House,
  LifeBuoy,
  Loader2,
  Plug,
  RefreshCw,
  Settings,
  User,
  Wallet,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Banner, BannerAction, BannerActions, BannerDescription, BannerTitle } from "@/components/ui/banner";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import {
  DropdownContent,
  DropdownLabel,
  DropdownMenu,
  DropdownSeparator,
  DropdownTrigger,
} from "@/components/ui/dropdown";
import { MenuItem } from "@/components/ui/menu-item";
import { Tooltip, TooltipProvider } from "@/components/ui/tooltip";
import { FluidHoverHighlight } from "@/components/fluid-hover-highlight";
import { AssistantCard } from "@/components/AssistantCard";
import { ModelsPage } from "@/components/ModelsPage";
import { PromptsPanel } from "@/components/PromptsPanel";
import { McpPanel } from "@/components/McpPanel";
import { SkillsPanel } from "@/components/SkillsPanel";
import { SettingsPage } from "@/components/SettingsPage";
import { UsagePage, formatCompactTokens } from "@/components/UsagePage";
import { OperationDialog, ClientUpdateDialog } from "@/components/Dialogs";
import { BrandIcon } from "@/components/BrandIcon";
import { useFluidHover } from "@/hooks/use-fluid-hover";
import { Elevated } from "@/lib/elevated";
import { FluidProvider } from "@/lib/fluid-provider";
import { fontWeights } from "@/lib/font-weight";
import { useShape } from "@/lib/shape-context";
import { typeClass } from "@/lib/size-context";
import { cn } from "@/lib/utils";
import { useTranslation } from "react-i18next";
import {
  desktopAction,
  desktopCatalog,
  desktopMcpAction,
  desktopSkillAction,
  desktopSwitchTier,
  desktopPromptAction,
  desktopState,
  desktopUsage,
  onDesktopState,
  checkAppUpdate,
  applyAppUpdate,
  cancelAppUpdate,
  onAppUpdateProgress,
  type Action,
  type Agent,
  type DesktopState,
  type McpInput,
  type PromptArgs,
  type AppUpdate,
  type UsageRange,
} from "@/lib/desktop";

export const agents = [
  {
    agent: "claude" as const,
    name: "Claude Code",
    description: "整理文献、处理数据，把想法一步步做出来。",
  },
  {
    agent: "codex" as const,
    name: "Codex",
    description: "写代码、修问题，帮你完成研究中的重复工作。",
  },
];

type Page = "home" | "usage" | "models" | "skills" | "mcp" | "prompts" | "settings";
const pages: { page: Page; icon: typeof House; title: string; subtitle: string }[] = [
  { page: "home", icon: House, title: "你的助手，准备好就能出发", subtitle: "安装、接上、切换线路与测试对话，在这里一次完成。" },
  { page: "usage", icon: BarChart3, title: "用量与日志", subtitle: "查看真实消费金额、Token 用量、年度活跃度与最近请求记录。" },
  { page: "models", icon: Cpu, title: "可用模型", subtitle: "当前已授权的 6 大品牌模型清单，点击即可复制模型 ID。" },
  { page: "skills", icon: BookOpen, title: "Skills", subtitle: "给助手装上专门的本领，比如写论文、做表格、查文献。" },
  { page: "mcp", icon: Plug, title: "MCP 服务", subtitle: "让助手连接文件、数据库、浏览器等外部工具。" },
  { page: "prompts", icon: FileText, title: "提示词", subtitle: "为助手准备常用的规则，启用后新开的会话会按它工作。" },
  { page: "settings", icon: Settings, title: "设置", subtitle: "外观、更新和恢复。" },
];

function AgentTabs({ value, busy, onChange }: { value: Agent; busy: boolean; onChange: (a: Agent) => void }) {
  const ref = useRef<HTMLDivElement>(null);
  const hover = useFluidHover(ref, { axis: "x" });
  const shape = useShape();

  return (
    <div
      ref={ref}
      className="relative inline-flex items-center gap-0.5 rounded-lg bg-surface-2 p-0.5 ring-1 ring-border"
      role="group"
      aria-label="选择助手"
      {...hover.handlers}
    >
      <FluidHoverHighlight hover={hover} className={cn(shape.bg, "z-0")} />
      {agents.map((item, index) => {
        const active = value === item.agent;
        return (
          <button
            key={item.agent}
            ref={(el) => hover.registerItem(index, el)}
            type="button"
            aria-pressed={active}
            disabled={busy}
            onClick={() => onChange(item.agent)}
            className={cn(
              "relative z-10 inline-flex h-7 items-center gap-1.5 rounded-md px-3 whitespace-nowrap transition-colors duration-80 disabled:opacity-50",
              typeClass("caption"),
              active ? "bg-active text-foreground" : "text-muted-foreground hover:text-foreground",
            )}
            style={{ fontVariationSettings: active ? fontWeights.semibold : fontWeights.normal }}
          >
            <BrandIcon agent={item.agent} size={14} />
            {item.name}
          </button>
        );
      })}
    </div>
  );
}

const message = (err: unknown, fallback: string) => (typeof err === "string" ? err : fallback);

function DesktopShell() {
  const { t } = useTranslation();
  const shape = useShape();
  const navRef = useRef<HTMLElement>(null);
  const navHover = useFluidHover(navRef, { axis: "y" });
  const lastFocusRefreshRef = useRef(0);

  const [state, setState] = useState<DesktopState>();
  const [page, setPage] = useState<Page>("home");
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const [copied, setCopied] = useState("");
  const [dismissedView, setDismissedView] = useState("");
  const [goneNotice, setGoneNotice] = useState("");
  // Bumped after each finished action so a repeated, identical notice shows again.
  const [noticeSeq, setNoticeSeq] = useState(0);
  const [cancelling, setCancelling] = useState(false);
  const [panelAgent, setPanelAgent] = useState<Agent>("claude");
  const [usageRange, setUsageRange] = useState<UsageRange>("24h");
  const [usageLoading, setUsageLoading] = useState(false);
  const [catalogLoading, setCatalogLoading] = useState(false);
  const [clientUpdate, setClientUpdate] = useState<AppUpdate>();
  const [availableVersion, setAvailableVersion] = useState("");
  const [clientChecking, setClientChecking] = useState(false);
  const [clientUpdating, setClientUpdating] = useState(false);
  const [updateProgress, setUpdateProgress] = useState("");

  const applyState = (next: DesktopState) =>
    setState((previous) =>
      !previous || (next.revision ?? 0) >= (previous.revision ?? 0) ? next : previous,
    );

  useEffect(() => {
    let active = true;
    let dispose: (() => void) | undefined;
    const autoUpdate = window.setTimeout(() => {
      try {
        const last = Number(localStorage.getItem("jieshangai:tool-update-check") || 0);
        if (Date.now() - last < 24 * 60 * 60 * 1000) return;
      } catch {
        /* Storage can be disabled; the manual check still works. */
      }
      void desktopAction("check-updates")
        .then((next) => {
          if (active) applyState(next);
        })
        .catch(() => {});
    }, 3000);
    // Subscribe before reading the initial snapshot so cold-start events survive.
    void onDesktopState((next) => {
      if (active) applyState(next);
    })
      .then((unlisten) => {
        if (!active) {
          unlisten();
          return;
        }
        dispose = unlisten;
        return desktopState().then((next) => {
          if (active) applyState(next);
        });
      })
      .catch(() => {
        if (active) setError("没能连接客户端服务，请关闭接上AI后重新打开。");
      });
    return () => {
      active = false;
      window.clearTimeout(autoUpdate);
      dispose?.();
    };
  }, []);

  useEffect(() => {
    let live = true;
    let dispose: (() => void) | undefined;
    void onAppUpdateProgress(({ received, total }) => {
      if (live)
        setUpdateProgress(
          `正在下载更新：${(received / 1048576).toFixed(1)} MB${total ? ` / ${(total / 1048576).toFixed(1)} MB` : ""}`,
        );
    })
      .then((fn) => {
        if (live) dispose = fn;
        else fn();
      })
      .catch(() => {});
    return () => {
      live = false;
      dispose?.();
    };
  }, []);

  useEffect(() => {
    let active = true;
    const timer = window.setTimeout(() => {
      try {
        const key = "jieshangai:client-update-check";
        if (Date.now() - Number(localStorage.getItem(key) || 0) < 86400000) return;
        localStorage.setItem(key, String(Date.now()));
      } catch {
        /* Manual checking remains available. */
      }
      void checkAppUpdate()
        .then((update) => {
          if (active && update.available) setAvailableVersion(update.version || "新版本");
        })
        .catch(() => {});
    }, 5000);
    return () => {
      active = false;
      window.clearTimeout(timer);
    };
  }, []);

  const busy = pending || !!state?.view.busy || clientUpdating;
  const unavailable = state?.engineReady === false || !!state?.engineError;
  const installing = !!state?.installTasks?.some((task) => task.busy);

  // Silently refresh balance and tier status when returning from the browser (e.g. after recharge).
  useEffect(() => {
    let active = true;
    const onFocus = () => {
      if (!active || busy || unavailable || installing || !state?.authorized) return;
      const now = Date.now();
      if (now - lastFocusRefreshRef.current < 5000) return;
      lastFocusRefreshRef.current = now;
      void desktopAction("refresh")
        .then((next) => {
          if (active) applyState(next);
        })
        .catch(() => {});
    };
    window.addEventListener("focus", onFocus);
    return () => {
      active = false;
      window.removeEventListener("focus", onFocus);
    };
  }, [busy, unavailable, installing, state?.authorized]);

  async function checkClientUpdate() {
    setClientChecking(true);
    setError("");
    try {
      const update = await checkAppUpdate();
      setAvailableVersion(update.available ? update.version || "新版本" : "");
      setClientUpdate(update);
    } catch (err) {
      setError(message(err, "更新检查未完成，当前版本仍可使用。"));
    } finally {
      setClientChecking(false);
    }
  }
  async function installClientUpdate() {
    if (!clientUpdate?.version) return;
    setClientUpdating(true);
    setUpdateProgress("正在准备更新……");
    try {
      await applyAppUpdate(clientUpdate.version);
    } catch (err) {
      if (!(typeof err === "string" && err.includes("已取消")))
        setError(message(err, "更新未完成，当前版本已保留。"));
    } finally {
      setClientUpdating(false);
    }
  }
  function closeClientUpdate() {
    setClientUpdate(undefined);
    if (clientUpdating) void cancelAppUpdate().catch(() => {});
    setClientUpdating(false);
  }

  // The daily check counts only once it finished cleanly; a failed one retries next launch.
  const updates = state?.toolUpdates;
  const checkedClean = updates && !updates.checking && !updates.error ? (updates.checkedAt ?? "") : "";
  useEffect(() => {
    if (!checkedClean) return;
    try {
      localStorage.setItem("jieshangai:tool-update-check", String(Date.parse(checkedClean) || Date.now()));
    } catch {
      /* Storage can be disabled; the manual check still works. */
    }
  }, [checkedClean]);

  async function loadUsage(nextRange: UsageRange) {
    setUsageRange(nextRange);
    setUsageLoading(true);
    setError("");
    try {
      applyState(await desktopUsage(nextRange));
    } catch (err) {
      setError(message(err, "没能读取用量记录，请再试一次。"));
    } finally {
      setUsageLoading(false);
    }
  }
  async function loadPanel(next: Page, agent: Agent) {
    setPanelAgent(agent);
    setError("");
    try {
      if (next === "models") {
        setCatalogLoading(true);
        try {
          applyState(await desktopCatalog(agent));
        } finally {
          setCatalogLoading(false);
        }
        return;
      }
      if (next === "prompts") applyState(await desktopPromptAction("prompts-list", agent));
      if (next === "mcp") applyState(await desktopMcpAction("mcp-list", agent));
      if (next === "skills") applyState(await desktopSkillAction("skills-list", agent));
    } catch (err) {
      const what =
        next === "models"
          ? "可用模型"
          : next === "mcp"
            ? "MCP 服务"
            : next === "skills"
              ? "Skills"
              : "提示词";
      setError(message(err, `没能读取${what}，请再试一次。`));
    }
  }
  function go(next: Page) {
    setPage(next);
    setError("");
    if (next === "usage") {
      void loadUsage(usageRange);
      return;
    }
    if (next !== "home" && next !== "settings") void loadPanel(next, panelAgent);
  }
  async function runPrompt(action: "prompt-save" | "prompt-enable" | "prompt-delete", args: PromptArgs) {
    setPending(true);
    setError("");
    try {
      applyState(await desktopPromptAction(action, panelAgent, args));
    } catch (err) {
      setError(message(err, "提示词操作没能完成，请再试一次。"));
    } finally {
      setPending(false);
    }
  }
  async function runMcp(
    action: "mcp-save" | "mcp-enable" | "mcp-disable" | "mcp-delete",
    args: { promptId?: string; mcp?: McpInput },
  ) {
    setPending(true);
    setError("");
    try {
      applyState(await desktopMcpAction(action, panelAgent, args));
      return true;
    } catch (err) {
      setError(message(err, "MCP 操作没能完成，请再试一次。"));
      return false;
    } finally {
      setPending(false);
    }
  }
  async function runSkill(
    action: "skill-import" | "skill-import-zip" | "skill-enable" | "skill-disable" | "skill-delete",
    dir: string,
  ) {
    setPending(true);
    setError("");
    try {
      applyState(await desktopSkillAction(action, panelAgent, dir));
      return true;
    } catch (err) {
      setError(message(err, "Skill 操作没能完成，请再试一次。"));
      return false;
    } finally {
      setPending(false);
    }
  }
  async function switchTier(agent: Agent, tier: string) {
    if (busy || unavailable) return;
    setPending(true);
    setError("");
    try {
      const next = await desktopSwitchTier(agent, tier);
      applyState(next);
      if (next.view.notice) {
        setGoneNotice("");
        setNoticeSeq((n) => n + 1);
      }
    } catch (err) {
      setError(message(err, "线路没换成，还是原来的线路。"));
    } finally {
      setPending(false);
    }
  }
  async function act(action: Action, agent?: Agent, confirm = false) {
    if (busy || unavailable) return;
    const background = ["install-suite", "update-tool", "check-updates"].includes(action);
    if (!background) setPending(true);
    setDismissedView("");
    setError("");
    try {
      applyState(await desktopAction(action, agent, confirm));
      setNoticeSeq((n) => n + 1);
    } catch (err) {
      setError(message(err, "操作没能完成，请再试一次。"));
    } finally {
      setPending(false);
    }
  }
  // One button checks both assistants and this client; updates follow from it.
  const toolUpdatesReady = (state?.tools ?? []).filter((t) => t.updateAvailable).map((t) => t.agent);
  async function checkAll() {
    void checkAppUpdate()
      .then((update) => setAvailableVersion(update.available ? update.version || "新版本" : ""))
      .catch(() => {});
    await act("check-updates");
  }
  async function updateAll() {
    for (const agent of toolUpdatesReady) await act("update-tool", agent);
  }
  async function cancelInstall(agent?: Agent) {
    if (cancelling) return;
    setCancelling(true);
    try {
      applyState(await desktopAction("cancel-install", agent));
    } catch {
      setError("暂时没能取消安装，请稍后再试。");
    } finally {
      setCancelling(false);
    }
  }
  async function copy(action: "copy-qq" | "copy-email" | "copy-report", label: string) {
    try {
      await desktopAction(action);
      setCopied(label);
    } catch {
      setError("没能复制，请手动选中并复制。");
    }
  }

  const view = state?.view;
  const viewKey = JSON.stringify(view ?? {});
  const dismissDialog = () => {
    setDismissedView(viewKey);
    if (!unavailable && !busy)
      void desktopAction("home")
        .then(applyState)
        .catch(() => {});
  };
  // Success notices fade on their own; only errors and confirmations ask for a click.
  const noticeKey = `${viewKey}#${noticeSeq}`;
  const notice = view?.notice && goneNotice !== noticeKey ? view.notice : "";
  useEffect(() => {
    if (!view?.notice) return;
    const key = noticeKey;
    const timer = window.setTimeout(() => setGoneNotice(key), 4500);
    return () => window.clearTimeout(timer);
  }, [noticeKey]); // eslint-disable-line react-hooks/exhaustive-deps
  const modal =
    !!view && !["home", "working", "connecting"].includes(view.screen) && !view.busy && !pending && dismissedView !== viewKey;
  const current = pages.find((p) => p.page === page)!;
  const test = state?.version?.includes("-test");
  const anyConnected = !!state?.statuses?.some((s) => s.state === "ok");
  const panelAgentConnected = !!state?.statuses?.some((s) => s.agent === panelAgent && s.state === "ok");
  const workspaceLabel = state?.workspace?.label || "个人工作区";
  const isTeamWorkspace = state?.workspace?.kind === "team";
  const WorkspaceIcon = isTeamWorkspace ? Building2 : User;

  return (
    <div className="flex h-full min-h-0 w-full overflow-hidden bg-surface-1 text-foreground">
      {/* Sidebar */}
      <aside className="flex w-56 shrink-0 flex-col border-r border-border bg-surface-2 px-3 py-4">
        <div className="mb-3 flex items-center gap-2.5 px-2">
          <img src="/brand.svg" alt="" className="size-6 rounded-md" />
          <span className={cn(typeClass("subtitle"), "text-foreground")} style={{ fontVariationSettings: fontWeights.semibold }}>
            接上AI
          </span>
          {test && (
            <Badge variant="solid" color="amber" size="compact">
              测试版
            </Badge>
          )}
        </div>

        {/* Workspace & Account Switcher */}
        <div className="mb-3">
          <DropdownMenu size="compact" disabled={busy || unavailable}>
            <DropdownTrigger
              render={
                <button
                  type="button"
                  aria-label={`工作区菜单：${workspaceLabel}`}
                  className={cn(
                    "flex w-full items-center gap-2 rounded-lg border border-border bg-surface-1 px-2.5 py-1.5 text-left transition-colors duration-80 hover:bg-hover focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50",
                  )}
                >
                  <WorkspaceIcon size={15} className="shrink-0 text-primary" aria-hidden="true" />
                  <div className="min-w-0 flex-1">
                    <div
                      className={cn(typeClass("caption"), "truncate text-foreground")}
                      style={{ fontVariationSettings: fontWeights.semibold }}
                    >
                      {workspaceLabel}
                    </div>
                    {state?.accountLabel && (
                      <div className={cn(typeClass("micro"), "truncate text-muted-foreground")}>
                        {state.accountLabel}
                      </div>
                    )}
                  </div>
                  <ChevronsUpDown size={13} className="shrink-0 text-muted-foreground" aria-hidden="true" />
                </button>
              }
            />
            <DropdownContent align="start" sideOffset={6} className="w-56">
              <DropdownLabel>
                {state?.accountLabel || (state?.authorized ? "已授权本机账号" : "未登录工作区")}
              </DropdownLabel>
              <DropdownSeparator />
              <MenuItem
                index={0}
                icon={ArrowLeftRight}
                label="切换工作区 / 账号"
                onSelect={() => void act("switch-account")}
              />
              <MenuItem
                index={1}
                icon={ExternalLink}
                label="打开网页控制台"
                onSelect={() => void act("open-web")}
              />
              <MenuItem
                index={2}
                icon={Wallet}
                label="账户充值"
                onSelect={() => void act("recharge")}
              />
              <DropdownSeparator />
              <MenuItem
                index={3}
                icon={RefreshCw}
                label="刷新账户与余额"
                onSelect={() => void act("refresh")}
              />
            </DropdownContent>
          </DropdownMenu>
        </div>

        <nav ref={navRef} aria-label="主导航" className="relative flex flex-1 flex-col gap-0.5" {...navHover.handlers}>
          <FluidHoverHighlight hover={navHover} className={cn(shape.bg, "z-0")} />
          {pages.map(({ page: value, icon: Icon }, index) => {
            const active = page === value;
            return (
              <div key={value} className="flex flex-col">
                {value === "skills" && (
                  <span className={cn(typeClass("micro"), "mt-3 mb-1 px-2 text-muted-foreground")}>
                    进阶扩展（选配）
                  </span>
                )}
                {value === "settings" && <span className="my-2 h-px bg-border" aria-hidden="true" />}
                <button
                  ref={(el) => navHover.registerItem(index, el)}
                  type="button"
                  aria-current={active ? "page" : undefined}
                  onClick={() => go(value)}
                  className={cn(
                    "relative z-10 flex h-9 items-center gap-2.5 rounded-lg px-2.5 text-left transition-colors duration-80",
                    typeClass("body"),
                    active ? "bg-active text-foreground" : "text-muted-foreground hover:text-foreground",
                  )}
                  style={{ fontVariationSettings: active ? fontWeights.semibold : fontWeights.normal }}
                >
                  <Icon size={16} strokeWidth={active ? 2 : 1.5} aria-hidden="true" />
                  <span>{t(`nav.${value}`)}</span>
                </button>
              </div>
            );
          })}
        </nav>

        <div className="mt-4 flex flex-col gap-2">
          <Elevated offset={2} shadowLevel={1} className="rounded-xl">
            <Card aria-label="账户余额" role="region" className="flex flex-col gap-2 rounded-xl p-3">
              <div className="flex items-center justify-between gap-2">
                <span className={cn(typeClass("caption"), "inline-flex items-center gap-1.5 text-muted-foreground")}>
                  <Wallet size={12} aria-hidden="true" />
                  账户余额
                </span>
                {state?.balance?.low && (
                  <Badge variant="solid" color="amber" size="compact">
                    偏低
                  </Badge>
                )}
              </div>
              <p className={cn(typeClass("subtitle"), "m-0 tabular-nums text-foreground")} style={{ fontVariationSettings: fontWeights.semibold }}>
                {state?.balance?.text ?? (state?.authorized ? "余额暂不可用" : "接上后查看余额")}
              </p>
              {state?.balance?.hint && (
                <small className={cn(typeClass("micro"), state.balance.red ? "text-destructive" : "text-muted-foreground")}>
                  {state.balance.hint}
                </small>
              )}
              <div className="flex items-center gap-1.5 pt-1">
                <Button
                  className="flex-1"
                  variant="tertiary"
                  size="compact"
                  trailingIcon={ArrowUpRight}
                  disabled={busy || unavailable}
                  onClick={() => void act("recharge")}
                >
                  去网页充值
                </Button>
                <Tooltip content="刷新状态与余额" side="top">
                  <Button
                    variant="ghost"
                    size="compact"
                    aria-label="刷新状态与余额"
                    disabled={busy || unavailable}
                    onClick={() => void act("refresh")}
                  >
                    <RefreshCw size={14} aria-hidden="true" />
                  </Button>
                </Tooltip>
              </div>
            </Card>
          </Elevated>

          {state?.support && (
            <Button
              variant="ghost"
              size="compact"
              leadingIcon={LifeBuoy}
              className="justify-start"
              disabled={busy || unavailable}
              onClick={() => void act("support")}
            >
              联系客服
            </Button>
          )}
          <span className={cn(typeClass("micro"), "px-2 text-muted-foreground")}>
            接上AI {state?.version ?? ""}
          </span>
        </div>
      </aside>

      {/* Main Content */}
      <main className="flex min-w-0 flex-1 flex-col overflow-y-auto px-7 py-6">
        <div className="mx-auto flex w-full max-w-4xl flex-col gap-5">
          <header className="flex flex-wrap items-start justify-between gap-4">
            <div className="min-w-0">
              <h1 className={cn(typeClass("display"), "m-0 text-foreground")} style={{ fontVariationSettings: fontWeights.bold }}>
                {current.title}
              </h1>
              <p className={cn(typeClass("body"), "mt-1 mb-0 text-muted-foreground")}>{current.subtitle}</p>
            </div>
            {page === "home" && (
              <div className="flex items-center gap-2">
                {availableVersion && (
                  <Button
                    variant="ghost"
                    size="compact"
                    disabled={clientChecking || clientUpdating}
                    onClick={() => void checkClientUpdate()}
                  >
                    接上AI 有更新
                  </Button>
                )}
                {toolUpdatesReady.length > 0 ? (
                  <Button
                    variant="primary"
                    size="compact"
                    leadingIcon={RefreshCw}
                    disabled={busy || unavailable || installing}
                    onClick={() => void updateAll()}
                  >
                    全部更新
                  </Button>
                ) : (
                  <Button
                    variant="tertiary"
                    size="compact"
                    loading={!!state?.toolUpdates?.checking}
                    leadingIcon={state?.toolUpdates?.checking ? undefined : RefreshCw}
                    disabled={busy || unavailable || !!state?.toolUpdates?.checking}
                    onClick={() => void checkAll()}
                  >
                    {state?.toolUpdates?.checking ? "正在检查" : "检查更新"}
                  </Button>
                )}
              </div>
            )}
            {(page === "models" || page === "skills" || page === "mcp" || page === "prompts") && (
              <AgentTabs value={panelAgent} busy={busy || catalogLoading} onChange={(a) => void loadPanel(page, a)} />
            )}
          </header>

          {test && page === "home" && (
            <Banner status="warning" contrast="low" size="compact">
              <BannerTitle>测试版，请勿用于正式账户。</BannerTitle>
            </Banner>
          )}

          {/* Status & feedback banners */}
          <div className="flex flex-col gap-2">
            {(error || state?.engineError) && (
              <Banner status="error" contrast="high" size="compact">
                <BannerTitle>{state?.engineError || error}</BannerTitle>
              </Banner>
            )}
            {busy && (
              <Banner status="info" contrast="low" size="compact" icon={Loader2}>
                <BannerTitle>{view?.line || "正在处理，请稍等……"}</BannerTitle>
                {view?.cancellable && (
                  <BannerActions>
                    <BannerAction variant="secondary" disabled={cancelling} onClick={() => void cancelInstall()}>
                      {cancelling ? "正在取消…" : "取消安装"}
                    </BannerAction>
                  </BannerActions>
                )}
              </Banner>
            )}
            {!state && !error && (
              <Banner status="default" contrast="low" size="compact" icon={Loader2}>
                <BannerTitle>正在检测本机助手……</BannerTitle>
              </Banner>
            )}
            {notice && (
              <Banner
                status="success"
                contrast="low"
                size="compact"
                dismissible
                dismissLabel="关闭提示"
                onDismiss={() => setGoneNotice(noticeKey)}
              >
                <BannerTitle>{notice}</BannerTitle>
              </Banner>
            )}
          </div>

          {page === "home" && (() => {
            const neitherInstalled =
              !!state?.tools?.length &&
              state.tools.every((t) => t.inspected !== false && !t.cliInstalled && !t.guiInstalled);
            return (
              <>
                <section className="grid grid-cols-1 gap-4 md:grid-cols-2" aria-label="助手">
                  {agents.map((item) => (
                    <AssistantCard
                      key={item.agent}
                      {...item}
                      state={state}
                      busy={busy}
                      unavailable={unavailable}
                      cancelling={cancelling}
                      recommended={neitherInstalled && item.agent === "claude"}
                      demotePrimary={neitherInstalled && item.agent === "codex"}
                      act={(a, agent) => void act(a, agent)}
                      cancelInstall={(agent) => void cancelInstall(agent)}
                      onSwitchTier={(agent, tier) => void switchTier(agent, tier)}
                    />
                  ))}
                </section>
                {anyConnected && (
                  <Elevated offset={2} shadowLevel={1} className="rounded-xl">
                    <Card
                      aria-label="今日用量概览"
                      role="region"
                      className="flex flex-wrap items-center justify-between gap-3 rounded-xl px-4 py-3"
                    >
                      <div className="flex flex-wrap items-center gap-x-5 gap-y-1.5">
                        <span
                          className={cn(typeClass("caption"), "inline-flex items-center gap-1.5 text-muted-foreground")}
                          style={{ fontVariationSettings: fontWeights.semibold }}
                        >
                          <BarChart3 size={14} aria-hidden="true" />
                          近 24 小时用量
                        </span>
                        <span className={cn(typeClass("caption"), "tabular-nums text-foreground")}>
                          花费{" "}
                          <strong style={{ fontVariationSettings: fontWeights.semibold }}>
                            ¥{state?.today?.costCny ?? "0.00"}
                          </strong>
                        </span>
                        <span className={cn(typeClass("caption"), "tabular-nums text-foreground")}>
                          请求{" "}
                          <strong style={{ fontVariationSettings: fontWeights.semibold }}>
                            {(state?.today?.requests ?? 0).toLocaleString("zh-CN")}
                          </strong>{" "}
                          次
                        </span>
                        <span className={cn(typeClass("caption"), "tabular-nums text-foreground")}>
                          Tokens{" "}
                          <strong style={{ fontVariationSettings: fontWeights.semibold }}>
                            {formatCompactTokens(state?.today?.tokens ?? 0)}
                          </strong>
                        </span>
                      </div>
                      <Button
                        variant="ghost"
                        size="compact"
                        trailingIcon={ChevronRight}
                        onClick={() => go("usage")}
                      >
                        查看全部日志
                      </Button>
                    </Card>
                  </Elevated>
                )}
                {anyConnected && (
                  <Banner status="default" contrast="low" size="compact">
                    <BannerTitle>第一次打开就能直接发话</BannerTitle>
                    <BannerDescription>
                      打开命令行后，如遇到文件夹信任提示直接按回车确认；也可以先点卡片右下角「测试对话」验证已真通。
                    </BannerDescription>
                  </Banner>
                )}
              </>
            );
          })()}

          {page === "home" && state?.toolUpdates?.error && (
            <p className={cn(typeClass("caption"), "m-0 text-muted-foreground")} role="status">
              {state.toolUpdates.error}
            </p>
          )}

          {page === "usage" && (
            <UsagePage
              usage={state?.usage}
              range={usageRange}
              loading={usageLoading}
              anyConnected={anyConnected}
              onRangeChange={(nextRange) => void loadUsage(nextRange)}
              onRefresh={() => void loadUsage(usageRange)}
              onGoHome={() => go("home")}
            />
          )}
          {page === "models" && (
            <ModelsPage
              agent={panelAgent}
              catalog={state?.catalogs?.[panelAgent]}
              connected={panelAgentConnected}
              loading={catalogLoading}
              onRefresh={() => void loadPanel("models", panelAgent)}
              onGoHome={() => go("home")}
            />
          )}
          {page === "skills" && (
            <SkillsPanel key={panelAgent} agent={panelAgent} skills={state?.skills?.[panelAgent] ?? []} busy={busy} run={runSkill} />
          )}
          {page === "mcp" && (
            <McpPanel key={panelAgent} agent={panelAgent} servers={state?.mcp?.[panelAgent] ?? []} busy={busy} run={runMcp} />
          )}
          {page === "prompts" && (
            <PromptsPanel
              key={panelAgent}
              agent={panelAgent}
              prompts={state?.prompts?.[panelAgent] ?? []}
              busy={busy}
              run={runPrompt}
            />
          )}
          {page === "settings" && (
            <SettingsPage
              version={state?.version}
              availableVersion={availableVersion}
              clientChecking={clientChecking}
              clientUpdating={clientUpdating}
              toolChecking={!!state?.toolUpdates?.checking}
              toolError={state?.toolUpdates?.error}
              checkedAt={state?.toolUpdates?.checkedAt}
              installing={installing}
              disabled={busy || unavailable}
              onClientUpdate={() => void checkClientUpdate()}
              onToolUpdates={() => void act("check-updates")}
              onRestore={() => void act("restore")}
            />
          )}
        </div>
      </main>

      <OperationDialog
        open={modal}
        view={view}
        state={state}
        busy={busy}
        copied={copied}
        onDismiss={dismissDialog}
        onCopy={(a, label) => void copy(a, label)}
        onAct={(a, agent, confirm) => void act(a, agent, confirm)}
      />
      <ClientUpdateDialog
        update={clientUpdate}
        updating={clientUpdating}
        progress={updateProgress}
        onClose={closeClientUpdate}
        onInstall={() => void installClientUpdate()}
      />
    </div>
  );
}

export default function App() {
  return (
    <FluidProvider>
      <TooltipProvider>
        <DesktopShell />
      </TooltipProvider>
    </FluidProvider>
  );
}
