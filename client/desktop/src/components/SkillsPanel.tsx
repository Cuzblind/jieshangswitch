import { useMemo, useState } from "react";
import { BookOpen, Check, Copy, Download, FileArchive, Flame, Lock, RefreshCw, Search, Sparkles, Terminal } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Elevated } from "@/lib/elevated";
import { fontWeights } from "@/lib/font-weight";
import { typeClass } from "@/lib/size-context";
import { cn } from "@/lib/utils";
import type { Agent, RemoteSkillItem, RemoteSkillsCatalog, Skill } from "@/lib/desktop";

type Run = (
  action: "skill-import" | "skill-import-zip" | "skill-install-remote" | "skill-enable" | "skill-disable" | "skill-delete",
  dir: string,
) => Promise<boolean>;

type CatalogSort = "recommended" | "popular" | "latest";

const other = (agent: Agent) => (agent === "claude" ? "Codex" : "Claude Code");

function formatSupportedAgents(agents: string[]): string {
  if (!agents || agents.length === 0) return "Claude Code / Codex";
  return agents
    .map((a) => (a === "claude" ? "Claude Code" : a === "codex" ? "Codex" : a))
    .join(" · ");
}

function formatUsageCount(count?: number): string {
  const n = count ?? 0;
  if (n >= 10000) return `${(n / 10000).toFixed(1)}w`;
  if (n >= 1000) return `${(n / 1000).toFixed(1)}k`;
  return String(n);
}

// Skills are folders with a SKILL.md. The engine only removes folders it
// marked itself, so a skill the user added by hand is listed but left alone.
export function SkillsPanel({
  agent,
  skills,
  busy,
  run,
  catalog,
  catalogLoading = false,
  catalogError = "",
  connected,
  onRefreshCatalog,
  onRecharge,
  onGoHome,
}: {
  agent: Agent;
  skills: Skill[];
  busy: boolean;
  run: Run;
  catalog?: RemoteSkillsCatalog;
  catalogLoading?: boolean;
  catalogError?: string;
  connected?: boolean;
  onRefreshCatalog?: () => void;
  onRecharge?: () => void;
  onGoHome?: () => void;
}) {
  const target = agent === "claude" ? "~/.claude/skills" : "~/.codex/skills";
  const [selectedCategory, setSelectedCategory] = useState("all");
  const [sortBy, setSortBy] = useState<CatalogSort>("recommended");
  const [searchQuery, setSearchQuery] = useState("");
  const [installingSlug, setInstallingSlug] = useState("");
  const [copiedSlug, setCopiedSlug] = useState("");
  const [copyHint, setCopyHint] = useState("");

  const installedDirs = useMemo(() => new Set(skills.map((s) => s.dir)), [skills]);

  const showCatalogSection =
    catalog !== undefined ||
    catalogLoading ||
    !!catalogError ||
    onRefreshCatalog !== undefined ||
    connected === false;

  const filteredItems = useMemo(() => {
    const q = searchQuery.trim().toLowerCase();
    const matched = (catalog?.items ?? []).filter((item) => {
      if (selectedCategory !== "all" && item.category !== selectedCategory) return false;
      if (!q) return true;
      return `${item.title} ${item.summary} ${item.slug}`.toLowerCase().includes(q);
    });
    const sorted = [...matched];
    if (sortBy === "popular") {
      sorted.sort((a, b) => (b.install_count ?? 0) - (a.install_count ?? 0) || a.sort_order - b.sort_order || a.id - b.id);
    } else if (sortBy === "latest") {
      sorted.sort((a, b) => (b.created_at || b.updated_at || 0) - (a.created_at || a.updated_at || 0) || b.id - a.id);
    } else {
      sorted.sort((a, b) => a.sort_order - b.sort_order || a.id - b.id);
    }
    return sorted;
  }, [catalog?.items, selectedCategory, searchQuery, sortBy]);

  async function handleRemoteInstall(item: RemoteSkillItem) {
    setInstallingSlug(item.slug);
    try {
      await run("skill-install-remote", item.slug);
    } finally {
      setInstallingSlug("");
    }
  }

  async function handleCopyCommand(item: RemoteSkillItem) {
    const cmd = (item.install_command || "").trim();
    if (!cmd) return;
    try {
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(cmd);
        setCopiedSlug(item.slug);
        setCopyHint("");
        window.setTimeout(() => setCopiedSlug((cur) => (cur === item.slug ? "" : cur)), 2500);
        return;
      }
    } catch {
      /* Fallback message below when clipboard API is unavailable */
    }
    setCopyHint(`请手动复制：${cmd}`);
  }

  return (
    <section aria-label="Skills" className="flex flex-col gap-5">
      <div className="flex flex-col gap-3">
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
            <Card className="flex flex-col items-center gap-1 rounded-xl px-6 py-7 text-center">
              <p className={cn(typeClass("subtitle"), "m-0 text-foreground")} style={{ fontVariationSettings: fontWeights.semibold }}>
                还没有本地 Skills
              </p>
              <small className={cn(typeClass("caption"), "text-muted-foreground")}>
                可从下方官方 Skill 广场一键安装，或点“从 zip 导入” / 把带 SKILL.md 的文件夹放进 {target}。
              </small>
            </Card>
          </Elevated>
        )}

        {skills.length > 0 && (
          <Elevated offset={2} shadowLevel={2} className="rounded-xl">
            <Card className="rounded-xl px-4 py-1">
              <ul className="m-0 flex flex-col divide-y divide-border/70 p-0 list-none">
                {skills.map((s) => (
                  <li key={s.dir} className="flex flex-wrap items-center justify-between gap-3 py-2.5">
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
      </div>

      {showCatalogSection && (
        <section aria-label="官方 Skill 广场" className="flex flex-col gap-3 pt-1">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div className="flex items-center gap-2">
              <Sparkles size={15} className="text-primary" aria-hidden="true" />
              <h2 className={cn(typeClass("subtitle"), "m-0 text-foreground")} style={{ fontVariationSettings: fontWeights.semibold }}>
                官方 Skill 广场
              </h2>
              {catalog && (
                <Badge variant="solid" color="gray" size="compact">
                  {catalog.items.length} 个
                </Badge>
              )}
            </div>
            <div className="flex flex-wrap items-center gap-1.5">
              {connected !== false && catalog && (
                <>
                  <div className="inline-flex items-center gap-0.5 rounded-lg bg-surface-2 p-0.5 ring-1 ring-border" role="group" aria-label="排序方式">
                    {(
                      [
                        { id: "recommended", label: "推荐" },
                        { id: "popular", label: "热门" },
                        { id: "latest", label: "最新" },
                      ] as const
                    ).map((opt) => (
                      <button
                        key={opt.id}
                        type="button"
                        aria-pressed={sortBy === opt.id}
                        onClick={() => setSortBy(opt.id)}
                        className={cn(
                          "rounded-md px-2 py-0.5 text-[11px] font-medium transition-colors",
                          sortBy === opt.id ? "bg-active text-foreground" : "text-muted-foreground hover:text-foreground",
                        )}
                      >
                        {opt.label}
                      </button>
                    ))}
                  </div>
                  <label className="relative flex items-center">
                    <Search size={12} className="pointer-events-none absolute left-2 text-muted-foreground" aria-hidden="true" />
                    <input
                      type="search"
                      value={searchQuery}
                      onChange={(e) => setSearchQuery(e.target.value)}
                      aria-label="搜索官方 Skill"
                      placeholder="搜索 Skill…"
                      className="h-7 w-36 rounded-md border border-border bg-surface-1 pl-6 pr-2 text-xs text-foreground placeholder:text-muted-foreground focus:border-primary focus:outline-none"
                    />
                  </label>
                </>
              )}
              {onRefreshCatalog && connected !== false && (
                <Button
                  variant="ghost"
                  size="compact"
                  leadingIcon={RefreshCw}
                  loading={catalogLoading}
                  disabled={busy || catalogLoading}
                  onClick={onRefreshCatalog}
                >
                  刷新广场
                </Button>
              )}
            </div>
          </div>

          {connected === false ? (
            <Elevated offset={2} shadowLevel={1} className="rounded-xl">
              <Card className="flex flex-wrap items-center justify-between gap-3 rounded-xl px-4 py-4">
                <div className="min-w-0">
                  <p className={cn(typeClass("body"), "m-0 text-foreground")} style={{ fontVariationSettings: fontWeights.semibold }}>
                    接上助手后即可从官方广场一键安装 Skill
                  </p>
                  <p className={cn(typeClass("caption"), "mt-0.5 mb-0 text-muted-foreground")}>
                    支持自动校验压缩包并一键启用到 {target}，无需手动下载或命令行配置。
                  </p>
                </div>
                {onGoHome && (
                  <Button variant="primary" size="compact" onClick={onGoHome}>
                    前往首页接上
                  </Button>
                )}
              </Card>
            </Elevated>
          ) : catalogError ? (
            <Elevated offset={2} shadowLevel={1} className="rounded-xl">
              <Card className="flex flex-wrap items-center justify-between gap-3 rounded-xl px-4 py-3.5">
                <span className={cn(typeClass("caption"), "text-muted-foreground")}>{catalogError}</span>
                {onRefreshCatalog && (
                  <Button variant="tertiary" size="compact" disabled={busy || catalogLoading} onClick={onRefreshCatalog}>
                    重试
                  </Button>
                )}
              </Card>
            </Elevated>
          ) : catalogLoading && !catalog ? (
            <Elevated offset={2} shadowLevel={1} className="rounded-xl">
              <Card className="rounded-xl px-4 py-6 text-center">
                <span className={cn(typeClass("caption"), "text-muted-foreground")}>正在同步官方 Skill 广场……</span>
              </Card>
            </Elevated>
          ) : (
            <>
              {catalog && catalog.categories.length > 0 && (
                <div className="flex flex-wrap items-center gap-1" role="group" aria-label="Skill 分类筛选">
                  <Button
                    variant={selectedCategory === "all" ? "primary" : "ghost"}
                    size="compact"
                    onClick={() => setSelectedCategory("all")}
                  >
                    全部
                  </Button>
                  {catalog.categories.map((cat) => (
                    <Button
                      key={cat.id}
                      variant={selectedCategory === cat.id ? "primary" : "ghost"}
                      size="compact"
                      onClick={() => setSelectedCategory(cat.id)}
                    >
                      {cat.label}
                    </Button>
                  ))}
                </div>
              )}

              {copyHint && (
                <p className={cn(typeClass("caption"), "m-0 text-muted-foreground")} role="status">
                  {copyHint}
                </p>
              )}

              {filteredItems.length === 0 ? (
                <Elevated offset={2} shadowLevel={1} className="rounded-xl">
                  <Card className="rounded-xl px-5 py-6 text-center">
                    <p className={cn(typeClass("caption"), "m-0 text-muted-foreground")}>
                      {searchQuery.trim()
                        ? "没有匹配的官方 Skill，试试换个关键词。"
                        : "当前分类暂未上架官方 Skill，在后台配置发布后点「刷新广场」即可一键安装。"}
                    </p>
                  </Card>
                </Elevated>
              ) : (
                <Elevated offset={2} shadowLevel={1} className="rounded-xl">
                  <Card className="rounded-xl px-4 py-1">
                    <ul className="m-0 flex flex-col divide-y divide-border/70 p-0 list-none">
                      {filteredItems.map((item, idx) => {
                        const installed = installedDirs.has(item.slug);
                        const isInstalling = installingSlug === item.slug;
                        const isCopied = copiedSlug === item.slug;
                        const count = item.install_count ?? 0;
                        const showRank = sortBy === "popular" && idx < 3 && count > 0;
                        return (
                          <li key={item.slug} className="flex flex-wrap items-center justify-between gap-3 py-2.5">
                            <div className="min-w-0 flex-1">
                              <div className="flex flex-wrap items-center gap-1.5">
                                {showRank && (
                                  <Badge variant="solid" color={idx === 0 ? "amber" : "gray"} size="compact">
                                    #{idx + 1}
                                  </Badge>
                                )}
                                <span
                                  className={cn(typeClass("body"), "text-foreground")}
                                  style={{ fontVariationSettings: fontWeights.semibold }}
                                >
                                  {item.title}
                                </span>
                                {item.tier === "premium" ? (
                                  <Badge variant="solid" color={item.unlocked ? "green" : "amber"} size="compact">
                                    {item.unlocked ? "精品 · 已解锁" : "精品"}
                                  </Badge>
                                ) : (
                                  <Badge variant="solid" color="gray" size="compact">
                                    免费
                                  </Badge>
                                )}
                                {installed && (
                                  <Badge variant="dot" color="green" size="compact">
                                    已安装
                                  </Badge>
                                )}
                                <span className={cn(typeClass("micro"), "text-muted-foreground")}>
                                  {formatSupportedAgents(item.supported_agents)}
                                </span>
                                {count > 0 && (
                                  <span className={cn(typeClass("micro"), "inline-flex items-center gap-0.5 text-muted-foreground")}>
                                    <Flame size={11} className="text-amber-500" aria-hidden="true" />
                                    {formatUsageCount(count)} 次
                                  </span>
                                )}
                              </div>
                              {item.summary && (
                                <p className={cn(typeClass("caption"), "mt-0.5 mb-0 truncate text-muted-foreground")}>
                                  {item.summary}
                                </p>
                              )}
                              {item.unlocked && item.install_command && (
                                <div className="mt-1 inline-flex max-w-full items-center gap-1 rounded bg-surface-2 px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground">
                                  <Terminal size={11} className="shrink-0" aria-hidden="true" />
                                  <span className="truncate">{item.install_command}</span>
                                </div>
                              )}
                            </div>

                            <div className="flex shrink-0 flex-wrap items-center gap-1.5">
                              {!item.unlocked ? (
                                <Button
                                  variant="tertiary"
                                  size="compact"
                                  leadingIcon={Lock}
                                  disabled={busy || !onRecharge}
                                  onClick={() => onRecharge?.()}
                                >
                                  去充值解锁
                                  <span className="sr-only"> · {item.title}</span>
                                </Button>
                              ) : (
                                <>
                                  {item.has_zip && !installed && (
                                    <Button
                                      variant="primary"
                                      size="compact"
                                      leadingIcon={Download}
                                      loading={isInstalling}
                                      disabled={busy || isInstalling}
                                      onClick={() => void handleRemoteInstall(item)}
                                    >
                                      一键安装
                                      <span className="sr-only"> · {item.title}</span>
                                    </Button>
                                  )}
                                  {item.install_command && (
                                    <Button
                                      variant="tertiary"
                                      size="compact"
                                      leadingIcon={isCopied ? Check : Copy}
                                      disabled={busy}
                                      onClick={() => void handleCopyCommand(item)}
                                    >
                                      {isCopied ? "已复制" : "复制命令"}
                                      <span className="sr-only"> · {item.title}</span>
                                    </Button>
                                  )}
                                </>
                              )}
                            </div>
                          </li>
                        );
                      })}
                    </ul>
                  </Card>
                </Elevated>
              )}
            </>
          )}
        </section>
      )}
    </section>
  );
}
