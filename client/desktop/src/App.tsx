import { useEffect, useState } from "react";
import "@/i18n";
import {
  ArrowUpRight,
  BookOpen,
  CircleAlert,
  FileText,
  House,
  LifeBuoy,
  Loader2,
  Plug,
  RefreshCw,
  Settings,
  Wallet,
  CheckCircle2,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { AssistantCard } from "@/components/AssistantCard";
import { PromptsPanel } from "@/components/PromptsPanel";
import { McpPanel } from "@/components/McpPanel";
import { SkillsPanel } from "@/components/SkillsPanel";
import { SettingsPage } from "@/components/SettingsPage";
import { OperationDialog, ClientUpdateDialog } from "@/components/Dialogs";
import { BrandIcon } from "@/components/BrandIcon";
import { useTranslation } from "react-i18next";
import {
  desktopAction,
  desktopMcpAction,
  desktopSkillAction,
  desktopSwitchTier,
  desktopPromptAction,
  desktopState,
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

type Page = "home" | "skills" | "mcp" | "prompts" | "settings";
const pages: { page: Page; icon: typeof House; title: string; subtitle: string }[] = [
  { page: "home", icon: House, title: "你的助手，准备好就能出发", subtitle: "安装、接上、打开，在这里一次完成。" },
  { page: "skills", icon: BookOpen, title: "Skills", subtitle: "给助手装上专门的本领，比如写论文、做表格、查文献。" },
  { page: "mcp", icon: Plug, title: "MCP 服务", subtitle: "让助手连接文件、数据库、浏览器等外部工具。" },
  { page: "prompts", icon: FileText, title: "提示词", subtitle: "为助手准备常用的规则，启用后新开的会话会按它工作。" },
  { page: "settings", icon: Settings, title: "设置", subtitle: "外观、更新和恢复。" },
];

function AgentTabs({ value, busy, onChange }: { value: Agent; busy: boolean; onChange: (a: Agent) => void }) {
  return (
    <div className="agent-tabs" role="group" aria-label="选择助手">
      {agents.map((item) => (
        <button
          key={item.agent}
          type="button"
          aria-pressed={value === item.agent}
          disabled={busy}
          onClick={() => onChange(item.agent)}
        >
          <BrandIcon agent={item.agent} size={15} />
          {item.name}
        </button>
      ))}
    </div>
  );
}

const message = (err: unknown, fallback: string) => (typeof err === "string" ? err : fallback);

export default function App() {
  const { t } = useTranslation();
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

  const busy = pending || !!state?.view.busy || clientUpdating;
  const unavailable = state?.engineReady === false || !!state?.engineError;
  const installing = !!state?.installTasks?.some((task) => task.busy);
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

  async function loadPanel(next: Page, agent: Agent) {
    setPanelAgent(agent);
    setError("");
    try {
      if (next === "prompts") applyState(await desktopPromptAction("prompts-list", agent));
      if (next === "mcp") applyState(await desktopMcpAction("mcp-list", agent));
      if (next === "skills") applyState(await desktopSkillAction("skills-list", agent));
    } catch (err) {
      const what = next === "mcp" ? "MCP 服务" : next === "skills" ? "Skills" : "提示词";
      setError(message(err, `没能读取${what}，请再试一次。`));
    }
  }
  function go(next: Page) {
    setPage(next);
    setError("");
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
    const timer = window.setTimeout(() => setGoneNotice(key), 3000);
    return () => window.clearTimeout(timer);
  }, [noticeKey]); // eslint-disable-line react-hooks/exhaustive-deps
  const modal =
    !!view && !["home", "working", "connecting"].includes(view.screen) && !view.busy && !pending && dismissedView !== viewKey;
  const current = pages.find((p) => p.page === page)!;
  const test = state?.version?.includes("-test");

  return (
    <div className="layout">
      <aside className="sidebar">
        <div className="brand">
          <img src="/brand.svg" alt="" />
          <span>接上AI</span>
        </div>
        <nav aria-label="主导航">
          {pages.map(({ page: value, icon: Icon }) => (
            <div key={value} className="nav-slot">
              {value === "skills" && <span className="nav-section">进阶扩展（选配）</span>}
              {value === "settings" && <span className="nav-divider" aria-hidden="true" />}
              <button
                type="button"
                aria-current={page === value ? "page" : undefined}
                onClick={() => go(value)}
              >
                <Icon size={16} aria-hidden="true" />
                {t(`nav.${value}`)}
              </button>
            </div>
          ))}
        </nav>
        <div className="sidebar-foot">
          <div className={`wallet ${state?.balance?.low ? "low" : ""}`} aria-label="账户余额" role="region">
            <p className="wallet-label">
              <Wallet size={12} aria-hidden="true" />
              账户余额
            </p>
            <p className="amount">
              {state?.balance?.text ?? (state?.authorized ? "余额暂不可用" : "接上后查看余额")}
            </p>
            {state?.balance?.hint && (
              <small className={state.balance.red ? "hint-red" : ""}>{state.balance.hint}</small>
            )}
            <div className="wallet-actions">
              <Button variant="outline" size="sm" disabled={busy || unavailable} onClick={() => void act("recharge")}>
                去网页充值
                <ArrowUpRight size={13} />
              </Button>
              <Button
                variant="ghost"
                size="icon-compact"
                aria-label="刷新状态与余额"
                disabled={busy || unavailable}
                onClick={() => void act("refresh")}
              >
                <RefreshCw size={14} />
              </Button>
            </div>
          </div>
          {state?.support && (
            <button className="side-link" disabled={busy || unavailable} onClick={() => void act("support")}>
              <LifeBuoy size={14} aria-hidden="true" />
              联系客服
            </button>
          )}
          <span className="version">
            接上AI {state?.version ?? ""}
            {test && <em className="tag warn">测试版</em>}
          </span>
        </div>
      </aside>
      <main className="content">
        <header className="page-head">
          <div>
            <h1>{current.title}</h1>
            <p>{current.subtitle}</p>
          </div>
          {page === "home" && (
            <div className="page-actions">
              {availableVersion && (
                <Button variant="ghost" size="sm" disabled={clientChecking || clientUpdating} onClick={() => void checkClientUpdate()}>
                  接上AI 有更新
                </Button>
              )}
              {toolUpdatesReady.length > 0 ? (
                <Button size="sm" disabled={busy || unavailable || installing} onClick={() => void updateAll()}>
                  <RefreshCw size={14} />
                  全部更新
                </Button>
              ) : (
                <Button variant="outline" size="sm" disabled={busy || unavailable || !!state?.toolUpdates?.checking} onClick={() => void checkAll()}>
                  {state?.toolUpdates?.checking ? <Loader2 size={14} className="spin" /> : <RefreshCw size={14} />}
                  {state?.toolUpdates?.checking ? "正在检查" : "检查更新"}
                </Button>
              )}
            </div>
          )}
          {(page === "skills" || page === "mcp" || page === "prompts") && (
            <AgentTabs value={panelAgent} busy={busy} onChange={(a) => void loadPanel(page, a)} />
          )}
        </header>
        {test && page === "home" && <p className="test-note">测试版，请勿用于正式账户。</p>}
        <div className="status-slot">
          {(error || state?.engineError) && (
            <div className="banner error" role="alert">
              <CircleAlert size={16} />
              {state?.engineError || error}
            </div>
          )}
          {busy && (
            <div className="banner" role="status">
              <Loader2 size={16} className="spin" />
              {view?.line || "正在处理，请稍等……"}
              {view?.cancellable && (
                <Button variant="outline" size="sm" disabled={cancelling} onClick={() => void cancelInstall()}>
                  {cancelling ? "正在取消…" : "取消安装"}
                </Button>
              )}
            </div>
          )}
          {!state && !error && (
            <div className="banner" role="status">
              <Loader2 size={16} className="spin" />
              正在检测本机助手……
            </div>
          )}
        </div>
        {page === "home" && (() => {
          const neitherInstalled =
            !!state?.tools?.length &&
            state.tools.every((t) => t.inspected !== false && !t.cliInstalled && !t.guiInstalled);
          return (
            <section className="assistants" aria-label="助手">
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
          );
        })()}
        {page === "home" && state?.toolUpdates?.error && (
          <p className="panel-note" role="status">
            {state.toolUpdates.error}
          </p>
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
      </main>
      {notice && (
        <div className="toast" role="status">
          <CheckCircle2 size={16} aria-hidden="true" />
          <span>{notice}</span>
          <button type="button" aria-label="关闭提示" onClick={() => setGoneNotice(noticeKey)}>
            ×
          </button>
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
