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
  Copy,
  Cpu,
  Download,
  ExternalLink,
  FileText,
  FolderOpen,
  House,
  LifeBuoy,
  Loader2,
  Plug,
  RefreshCw,
  RotateCcw,
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
  desktopSetWorkDir,
  desktopSelectModel,
  desktopSkillAction,
  desktopSkillsCatalog,
  desktopSwitchTier,
  desktopPromptAction,
  desktopState,
  desktopUsage,
  desktopWalletIdentity,
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
    description: "OpenAI 出品（兼容 ChatGPT / Codex 桌面端），写代码、处理表格与研究中的重复工作。",
  },
];

type Page = "home" | "usage" | "models" | "skills" | "mcp" | "prompts" | "settings";
const pages: { page: Page; icon: typeof House; title: string; subtitle: string }[] = [
  { page: "home", icon: House, title: "你的助手，准备好就能出发", subtitle: "安装、接上、切换线路与测试对话，在这里一次完成。" },
  { page: "usage", icon: BarChart3, title: "用量与日志", subtitle: "查看真实消费金额、Token 用量、年度活跃度与最近请求记录。" },
  { page: "models", icon: Cpu, title: "可用模型", subtitle: "当前线路最新可用模型清单，支持一键设为默认或复制模型 ID。" },
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
  const lastUsageWalletRef = useRef("");
  const lastCatalogKeyRef = useRef("");
  const lastSkillCatalogWalletRef = useRef("");
  const usageRequestRef = useRef(0);
  const catalogRequestRef = useRef(0);
  const skillCatalogRequestRef = useRef(0);
  const panelRequestRef = useRef(0);

  const [state, setState] = useState<DesktopState>();
  const stateRef = useRef(state);
  stateRef.current = state;
  const [page, setPage] = useState<Page>("home");
  const pageRef = useRef(page);
  pageRef.current = page;
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
  const [skillCatalogLoading, setSkillCatalogLoading] = useState(false);
  const [skillCatalogError, setSkillCatalogError] = useState("");
  const [clientUpdate, setClientUpdate] = useState<AppUpdate>();
  const [availableUpdate, setAvailableUpdate] = useState<AppUpdate>();
  const [availableVersion, setAvailableVersion] = useState("");
  const [dismissedUpdateVersion, setDismissedUpdateVersion] = useState("");
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
          if (active && update.available) {
            setAvailableUpdate(update);
            setAvailableVersion(update.version || "新版本");
          }
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
      setAvailableUpdate(update.available ? update : undefined);
      setAvailableVersion(update.available ? update.version || "新版本" : "");
      if (update.available) setDismissedUpdateVersion("");
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
    const walletKey = desktopWalletIdentity(stateRef.current);
    const request = ++usageRequestRef.current;
    const previousRange = state?.usage?.range ?? usageRange;
    // An attempt is separate from cached data: a failed first load waits for
    // an explicit retry, page revisit, or connection change.
    lastUsageWalletRef.current = walletKey;
    setUsageRange(nextRange);
    setUsageLoading(true);
    setError("");
    try {
      const next = await desktopUsage(nextRange);
      if (request !== usageRequestRef.current || walletKey !== desktopWalletIdentity(stateRef.current)) return;
      applyState(next);
      if (next.usage?.range) setUsageRange(next.usage.range);
    } catch (err) {
      if (request !== usageRequestRef.current || walletKey !== desktopWalletIdentity(stateRef.current)) return;
      setUsageRange(previousRange);
      if (pageRef.current === "usage") setError(message(err, "没能读取用量记录，请再试一次。"));
    } finally {
      if (request === usageRequestRef.current) setUsageLoading(false);
    }
  }
  async function loadPanel(next: Page, agent: Agent) {
    const panelRequest = ++panelRequestRef.current;
    setPanelAgent(agent);
    setError("");
    try {
      if (next === "models") {
        const walletKey = desktopWalletIdentity(stateRef.current);
        const request = ++catalogRequestRef.current;
        lastCatalogKeyRef.current = `${walletKey}|${agent}`;
        setCatalogLoading(true);
        try {
          const updated = await desktopCatalog(agent);
          if (request !== catalogRequestRef.current || walletKey !== desktopWalletIdentity(stateRef.current)) return;
          applyState(updated);
        } catch (err) {
          if (request === catalogRequestRef.current && walletKey === desktopWalletIdentity(stateRef.current) && pageRef.current === "models") {
            setError(message(err, "没能读取可用模型，请再试一次。"));
          }
        } finally {
          if (request === catalogRequestRef.current) setCatalogLoading(false);
        }
        return;
      }
      if (next === "prompts") applyState(await desktopPromptAction("prompts-list", agent));
      if (next === "mcp") applyState(await desktopMcpAction("mcp-list", agent));
      if (next === "skills") {
        applyState(await desktopSkillAction("skills-list", agent));
        void loadSkillCatalog(agent);
      }
    } catch (err) {
      if (panelRequest !== panelRequestRef.current || pageRef.current !== next) return;
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
    ++panelRequestRef.current;
    if (next === "usage") lastUsageWalletRef.current = "";
    if (next === "models") lastCatalogKeyRef.current = "";
    if (next === "skills") lastSkillCatalogWalletRef.current = "";
    setPage(next);
    setError("");
    if (!["home", "settings", "usage", "models"].includes(next)) void loadPanel(next, panelAgent);
  }
  async function loadSkillCatalog(agent?: Agent) {
    const connectedNow = !!stateRef.current?.statuses?.some((s) => s.state === "ok");
    if (!connectedNow) {
      setSkillCatalogError("");
      return;
    }
    const walletKey = desktopWalletIdentity(stateRef.current);
    const request = ++skillCatalogRequestRef.current;
    lastSkillCatalogWalletRef.current = walletKey;
    setSkillCatalogLoading(true);
    setSkillCatalogError("");
    try {
      const updated = await desktopSkillsCatalog(agent);
      if (request !== skillCatalogRequestRef.current || walletKey !== desktopWalletIdentity(stateRef.current)) return;
      applyState(updated);
    } catch (err) {
      if (request !== skillCatalogRequestRef.current || walletKey !== desktopWalletIdentity(stateRef.current)) return;
      setSkillCatalogError(message(err, "暂时读不到官方 Skill 广场，请稍后再试。"));
    } finally {
      if (request === skillCatalogRequestRef.current) setSkillCatalogLoading(false);
    }
  }
  async function runPrompt(action: "prompt-save" | "prompt-enable" | "prompt-delete", args: PromptArgs) {
    setPending(true);
    setError("");
    try {
      applyState(await desktopPromptAction(action, panelAgent, args));
      return true;
    } catch (err) {
      setError(message(err, "提示词操作没能完成，请再试一次。"));
      return false;
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
    action: "skill-import" | "skill-import-zip" | "skill-install-remote" | "skill-enable" | "skill-disable" | "skill-delete",
    dir: string,
  ) {
    setPending(true);
    setError("");
    try {
      const next = await desktopSkillAction(action, panelAgent, dir);
      applyState(next);
      if (next.view.notice) {
        setGoneNotice("");
        setNoticeSeq((n) => n + 1);
      }
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
  async function selectModel(agent: Agent, opts: { model?: string; mode?: "native" | "cn" }) {
    if (busy || unavailable) return;
    setPending(true);
    setError("");
    try {
      const next = await desktopSelectModel(agent, opts);
      applyState(next);
      if (next.view.notice) {
        setGoneNotice("");
        setNoticeSeq((n) => n + 1);
      }
    } catch (err) {
      setError(message(err, "默认模型没换成，还是原来的配置。"));
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
      if (!background) setPending(false);
    }
  }
  // One button checks both assistants and this client; updates follow from it.
  const toolUpdatesReady = (state?.tools ?? []).filter((t) => t.updateAvailable).map((t) => t.agent);
  async function checkAll() {
    void checkAppUpdate()
      .then((update) => {
        setAvailableUpdate(update.available ? update : undefined);
        setAvailableVersion(update.available ? update.version || "新版本" : "");
        if (update.available) {
          setDismissedUpdateVersion("");
          setClientUpdate(update);
        }
      })
      .catch(() => {});
    await act("check-updates");
  }
  function openAvailableUpdate() {
    if (availableUpdate?.available) {
      setClientUpdate(availableUpdate);
      return;
    }
    void checkClientUpdate();
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
  async function copy(
    action: "copy-qq" | "copy-email" | "copy-report" | "copy-first-prompt",
    label: string,
  ) {
    try {
      await desktopAction(action);
      setCopied(label);
    } catch {
      setError("没能复制，请手动选中并复制。");
    }
  }
  async function selectWorkDir(dir: string) {
    if (busy || unavailable) return;
    setPending(true);
    setError("");
    try {
      const next = await desktopSetWorkDir(dir);
      applyState(next);
      if (next.view.notice) {
        setGoneNotice("");
        setNoticeSeq((n) => n + 1);
      }
    } catch (err) {
      setError(message(err, "没能切换工作目录，请再试一次。"));
    } finally {
      setPending(false);
    }
  }

  useEffect(() => {
    if (!copied) return;
    const timer = window.setTimeout(() => setCopied(""), 3000);
    return () => window.clearTimeout(timer);
  }, [copied]);

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
  const claudeConnected = !!state?.statuses?.some((s) => s.agent === "claude" && s.state === "ok");
  const codexConnected = !!state?.statuses?.some((s) => s.agent === "codex" && s.state === "ok");
  const preferredSwitchAgent: Agent | undefined = codexConnected && !claudeConnected ? "codex" : undefined;
  const panelAgentConnected = !!state?.statuses?.some((s) => s.agent === panelAgent && s.state === "ok");
  const workspaceLabel = state?.workspace?.label || "个人工作区";
  const walletIdentity = desktopWalletIdentity(state);
  const isTeamWorkspace = state?.workspace?.kind === "team";
  const WorkspaceIcon = isTeamWorkspace ? Building2 : User;

  // When a deep-link or account switch changes the active wallet while the user is on Usage or Models,
  // automatically reload that page's server data so stale wallet numbers never linger.
  useEffect(() => {
    if (page !== "usage" || !anyConnected || busy || unavailable) return;
    if (lastUsageWalletRef.current === walletIdentity) return;
    void loadUsage(usageRange);
  }, [page, anyConnected, walletIdentity, usageRange, busy, unavailable]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (page !== "models" || !panelAgentConnected || busy || unavailable) return;
    const key = `${walletIdentity}|${panelAgent}`;
    if (lastCatalogKeyRef.current === key) return;
    void loadPanel("models", panelAgent);
  }, [page, panelAgent, panelAgentConnected, walletIdentity, busy, unavailable]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (page !== "skills" || !anyConnected || busy || unavailable) return;
    if (lastSkillCatalogWalletRef.current === walletIdentity) return;
    void loadSkillCatalog(panelAgent);
  }, [page, panelAgent, anyConnected, walletIdentity, busy, unavailable]); // eslint-disable-line react-hooks/exhaustive-deps

  return (
    <div className="flex h-full min-h-0 w-full overflow-hidden bg-surface-1 text-foreground">
      {/* Sidebar */}
      <aside className="flex min-h-0 w-56 shrink-0 flex-col overflow-hidden border-r border-border bg-surface-2 px-3 py-3">
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
                onSelect={() => void act("switch-account", preferredSwitchAgent)}
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

        <nav ref={navRef} aria-label="主导航" className="relative flex min-h-0 flex-1 flex-col gap-0.5 overflow-y-auto" {...navHover.handlers}>
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
                  {value === "settings" && availableVersion && (
                    <span className="ml-auto size-2 rounded-full bg-primary" aria-hidden="true" />
                  )}
                </button>
              </div>
            );
          })}
        </nav>

        <div className="mt-2 flex shrink-0 flex-col gap-1.5">
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
          <button
            type="button"
            disabled={clientChecking || clientUpdating}
            onClick={() => (availableVersion ? openAvailableUpdate() : void checkClientUpdate())}
            title={availableVersion ? `点击更新至 ${availableVersion}` : "点击检查客户端更新"}
            className={cn(
              typeClass("micro"),
              "flex w-full items-center justify-between gap-2 rounded-md px-2 py-1 text-left text-muted-foreground transition-colors hover:bg-hover hover:text-foreground disabled:opacity-50",
            )}
          >
            <span className="truncate">接上AI {state?.version ?? ""}</span>
            {availableVersion && (
              <Badge variant="solid" color="accent" size="compact">
                有更新
              </Badge>
            )}
          </button>
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
                    variant={toolUpdatesReady.length > 0 ? "tertiary" : "primary"}
                    size="compact"
                    leadingIcon={Download}
                    disabled={clientChecking || clientUpdating}
                    onClick={openAvailableUpdate}
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
          {availableVersion && dismissedUpdateVersion !== availableVersion && (
            <Banner
              status="info"
              contrast="low"
              size="compact"
              dismissible
              dismissLabel="暂不提醒此版本"
              onDismiss={() => setDismissedUpdateVersion(availableVersion)}
            >
              <BannerTitle>接上AI 有新版本 {availableVersion} 可用</BannerTitle>
              <BannerDescription>
                {availableUpdate?.notes || "包含最新功能与稳定性优化，更新后会自动保留你当前的连接配置。"}
              </BannerDescription>
              <BannerActions>
                <BannerAction
                  variant="primary"
                  disabled={clientChecking || clientUpdating || installing}
                  onClick={openAvailableUpdate}
                >
                  立即更新
                </BannerAction>
              </BannerActions>
            </Banner>
          )}
          {page === "home" && anyConnected && state?.balance?.low && (
            <Banner status="warning" contrast="low" size="compact">
              <BannerTitle>当前账户余额偏低（{state.balance.text}），建议提前充值避免对话中断</BannerTitle>
              <BannerActions>
                <BannerAction
                  variant="primary"
                  disabled={busy || unavailable}
                  onClick={() => void act("recharge")}
                >
                  去网页充值
                </BannerAction>
              </BannerActions>
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
                      onSelectModel={(agent, opts) => void selectModel(agent, opts)}
                      onOpenModels={(agent) => {
                        setPanelAgent(agent);
                        go("models");
                      }}
                    />
                  ))}
                </section>
                {anyConnected && (
                  <Elevated offset={2} shadowLevel={1} className="rounded-xl">
                    <Card
                      aria-label="今日用量概览"
                      role="region"
                      className="rounded-xl px-4 py-3"
                    >
                      <div className="flex w-full flex-wrap items-center justify-between gap-3">
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
                      </div>
                    </Card>
                  </Elevated>
                )}
                {anyConnected && state?.workDir && (() => {
                  const fullDir = state.workDir;
                  const shortDir = fullDir.split(/[\\/]/).filter(Boolean).pop() || fullDir;
                  const isDefaultWorkDir = /[\\/](接上AI工作区|jieshangai-workspace)[\\/]?$/.test(fullDir);
                  const recents = (state.recentDirs ?? []).filter((d) => d && d !== fullDir);
                  const basenameCounts = new Map<string, number>();
                  for (const dir of [fullDir, ...recents]) {
                    const base = dir.split(/[\\/]/).filter(Boolean).pop() || dir;
                    basenameCounts.set(base, (basenameCounts.get(base) ?? 0) + 1);
                  }
                  const recentLabel = (dir: string) => {
                    const parts = dir.split(/[\\/]/).filter(Boolean);
                    const base = parts[parts.length - 1] || dir;
                    if ((basenameCounts.get(base) ?? 0) > 1 && parts.length >= 2) {
                      return `${parts[parts.length - 2]}/${base}`;
                    }
                    return base;
                  };
                  return (
                    <Elevated offset={2} shadowLevel={1} className="rounded-xl">
                      <Card
                        aria-label="命令行工作目录"
                        role="region"
                        className="rounded-xl px-4 py-3"
                      >
                        <div className="flex w-full flex-wrap items-center justify-between gap-3">
                          <div className="flex min-w-0 flex-1 items-center gap-2.5">
                            <FolderOpen size={15} className="shrink-0 text-primary" aria-hidden="true" />
                            <div className="min-w-0">
                              <div className="flex items-center gap-2">
                                <span
                                  className={cn(typeClass("caption"), "text-muted-foreground")}
                                  style={{ fontVariationSettings: fontWeights.semibold }}
                                >
                                  命令行工作目录
                                </span>
                                <span
                                  className={cn(typeClass("caption"), "truncate text-foreground")}
                                  style={{ fontVariationSettings: fontWeights.semibold }}
                                >
                                  {shortDir}
                                </span>
                              </div>
                              <p className={cn(typeClass("micro"), "m-0 truncate text-muted-foreground")} title={fullDir}>
                                {fullDir}
                              </p>
                            </div>
                          </div>
                          <div className="flex shrink-0 items-center gap-1.5">
                            {recents.length > 0 && (
                              <DropdownMenu size="compact" disabled={busy || unavailable}>
                                <DropdownTrigger
                                  render={
                                    <Button variant="ghost" size="compact" trailingIcon={ChevronsUpDown}>
                                      最近目录
                                    </Button>
                                  }
                                />
                                <DropdownContent align="end" sideOffset={6} className="w-64">
                                  <DropdownLabel>最近使用过的工作文件夹</DropdownLabel>
                                  <DropdownSeparator />
                                  {recents.map((dir, index) => (
                                    <MenuItem
                                      key={dir}
                                      index={index}
                                      icon={FolderOpen}
                                      label={recentLabel(dir)}
                                      onSelect={() => void selectWorkDir(dir)}
                                    />
                                  ))}
                                  <DropdownSeparator />
                                  <MenuItem
                                    index={recents.length}
                                    icon={RotateCcw}
                                    label="恢复默认工作区"
                                    onSelect={() => void selectWorkDir("")}
                                  />
                                </DropdownContent>
                              </DropdownMenu>
                            )}
                            {!isDefaultWorkDir && (
                              <Button
                                variant="ghost"
                                size="compact"
                                leadingIcon={RotateCcw}
                                disabled={busy || unavailable}
                                onClick={() => void selectWorkDir("")}
                              >
                                恢复默认
                              </Button>
                            )}
                            <Button
                              variant="tertiary"
                              size="compact"
                              leadingIcon={FolderOpen}
                              disabled={busy || unavailable}
                              onClick={() => void act("pick-work-dir")}
                            >
                              选择文件夹
                            </Button>
                          </div>
                        </div>
                      </Card>
                    </Elevated>
                  );
                })()}
                {anyConnected && (
                  <Banner status="default" contrast="low" size="compact">
                    <BannerTitle>第一次打开就能直接发话</BannerTitle>
                    <BannerDescription>
                      推荐首句：「帮我看看当前文件夹里有哪些文件，并按类型整理成一份清单」。点「打开桌面版」直接粘贴发送，或点「打开命令行」粘贴回车即可。
                    </BannerDescription>
                    <BannerActions>
                      <BannerAction
                        variant="secondary"
                        disabled={busy || unavailable}
                        onClick={() => void copy("copy-first-prompt", "首句提示词")}
                      >
                        <Copy size={13} aria-hidden="true" />
                        {copied === "首句提示词" ? "已复制，去粘贴回车" : "复制首句提示词"}
                      </BannerAction>
                    </BannerActions>
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
              busy={busy || unavailable}
              onRefresh={() => void loadPanel("models", panelAgent)}
              onGoHome={() => go("home")}
              onOpenPricing={() => void act("open-pricing")}
              onSelectModel={(model, mode) => void selectModel(panelAgent, { model, mode })}
            />
          )}
          {page === "skills" && (
            <SkillsPanel
              key={panelAgent}
              agent={panelAgent}
              skills={state?.skills?.[panelAgent] ?? []}
              busy={busy}
              run={runSkill}
              catalog={state?.skillCatalog}
              catalogLoading={skillCatalogLoading}
              catalogError={skillCatalogError}
              connected={anyConnected}
              onRefreshCatalog={() => void loadSkillCatalog(panelAgent)}
              onRecharge={() => void act("recharge")}
              onGoHome={() => go("home")}
            />
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
              report={state?.report}
              reportCopied={copied === "诊断信息"}
              onClientUpdate={() => void checkClientUpdate()}
              onToolUpdates={() => void act("check-updates")}
              onCopyReport={() => void copy("copy-report", "诊断信息")}
              onRestore={() => void act("restore")}
            />
          )}
        </div>
      </main>

      {notice && (
        <div className="fixed right-5 bottom-5 z-40 w-[min(26rem,calc(100vw-2.5rem))] shadow-lg">
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
        </div>
      )}
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
