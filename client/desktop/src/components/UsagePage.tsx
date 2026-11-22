import { useMemo, useRef } from "react";
import { Activity, Coins, Hash, RefreshCw } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableEmpty,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Tooltip, TooltipProvider } from "@/components/ui/tooltip";
import { FluidHoverHighlight } from "@/components/fluid-hover-highlight";
import { useFluidHover } from "@/hooks/use-fluid-hover";
import { Elevated } from "@/lib/elevated";
import { fontWeights } from "@/lib/font-weight";
import { useShape } from "@/lib/shape-context";
import { typeClass } from "@/lib/size-context";
import { cn } from "@/lib/utils";
import type { UsageHeatmapDay, UsageRange, UsageSnapshot } from "@/lib/desktop";

const RANGES: { value: UsageRange; label: string }[] = [
  { value: "24h", label: "24小时" },
  { value: "7d", label: "7天" },
  { value: "30d", label: "30天" },
  { value: "90d", label: "90天" },
];

export function formatCompactTokens(tokens: number): string {
  if (!Number.isFinite(tokens) || tokens <= 0) return "0";
  if (tokens >= 1_000_000) return `${(tokens / 1_000_000).toFixed(2)}M`;
  if (tokens >= 1_000) return `${(tokens / 1_000).toFixed(1)}K`;
  return tokens.toLocaleString("zh-CN");
}

function formatLogTime(unixSeconds: number): string {
  if (!unixSeconds) return "-";
  const d = new Date(unixSeconds * 1000);
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function formatLatency(ms: number): string {
  if (!ms || ms <= 0) return "-";
  if (ms >= 1000) return `${(ms / 1000).toFixed(1)}s`;
  return `${ms}ms`;
}

function localDateKey(d: Date): string {
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, "0");
  const day = String(d.getDate()).padStart(2, "0");
  return `${y}-${m}-${day}`;
}

interface HeatmapCell {
  date: string;
  month: number;
  dayOfMonth: number;
  requests: number;
  tokens: number;
  costCny: string;
  level: 0 | 1 | 2 | 3 | 4;
}

function buildHeatmapWeeks(days: UsageHeatmapDay[]): {
  weeks: HeatmapCell[][];
  monthMarkers: { weekIndex: number; label: string }[];
} {
  const byDate = new Map<string, UsageHeatmapDay>();
  let maxReqs = 0;
  for (const d of days) {
    byDate.set(d.date, d);
    if (d.requests > maxReqs) maxReqs = d.requests;
  }

  const today = new Date();
  today.setHours(0, 0, 0, 0);
  // Start 52 full weeks back aligned to Sunday so columns line up cleanly.
  const start = new Date(today);
  start.setDate(start.getDate() - 364 - start.getDay());

  const weeks: HeatmapCell[][] = [];
  const monthMarkers: { weekIndex: number; label: string }[] = [];
  let lastMonth = -1;

  const cursor = new Date(start);
  while (cursor <= today) {
    const week: HeatmapCell[] = [];
    const weekIndex = weeks.length;
    for (let dow = 0; dow < 7; dow++) {
      const date = localDateKey(cursor);
      const hit = byDate.get(date);
      const requests = hit?.requests ?? 0;
      let level: 0 | 1 | 2 | 3 | 4 = 0;
      if (requests > 0 && maxReqs > 0) {
        const ratio = requests / maxReqs;
        if (ratio <= 0.25) level = 1;
        else if (ratio <= 0.5) level = 2;
        else if (ratio <= 0.75) level = 3;
        else level = 4;
      }
      if (dow === 0) {
        const m = cursor.getMonth();
        const prevMarker = monthMarkers[monthMarkers.length - 1];
        if (m !== lastMonth && (!prevMarker || weekIndex - prevMarker.weekIndex >= 3)) {
          monthMarkers.push({ weekIndex, label: `${m + 1}月` });
          lastMonth = m;
        }
      }
      week.push({
        date,
        month: cursor.getMonth() + 1,
        dayOfMonth: cursor.getDate(),
        requests,
        tokens: hit?.tokens ?? 0,
        costCny: hit?.costCny ?? "0.00",
        level,
      });
      cursor.setDate(cursor.getDate() + 1);
    }
    weeks.push(week);
  }
  return { weeks, monthMarkers };
}

const HEATMAP_LEVEL_CLASS: Record<0 | 1 | 2 | 3 | 4, string> = {
  0: "bg-surface-3 ring-1 ring-inset ring-border/60",
  1: "bg-primary/25",
  2: "bg-primary/50",
  3: "bg-primary/75",
  4: "bg-primary",
};

function RangeTabs({
  value,
  disabled,
  onChange,
}: {
  value: UsageRange;
  disabled: boolean;
  onChange: (next: UsageRange) => void;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const hover = useFluidHover(ref, { axis: "x" });
  const shape = useShape();

  return (
    <div
      ref={ref}
      role="group"
      aria-label="统计时间范围"
      className="relative inline-flex items-center gap-0.5 rounded-lg bg-surface-2 p-0.5 ring-1 ring-border"
      {...hover.handlers}
    >
      <FluidHoverHighlight hover={hover} className={cn(shape.bg, "z-0")} />
      {RANGES.map((item, index) => {
        const active = item.value === value;
        return (
          <button
            key={item.value}
            ref={(el) => hover.registerItem(index, el)}
            type="button"
            aria-pressed={active}
            disabled={disabled}
            onClick={() => onChange(item.value)}
            className={cn(
              "relative z-10 inline-flex h-7 items-center rounded-md px-3 whitespace-nowrap transition-colors duration-80 disabled:opacity-50",
              typeClass("caption"),
              active ? "bg-active text-foreground" : "text-muted-foreground hover:text-foreground",
            )}
            style={{ fontVariationSettings: active ? fontWeights.semibold : fontWeights.normal }}
          >
            {item.label}
          </button>
        );
      })}
    </div>
  );
}

export function UsagePage({
  usage,
  range,
  loading,
  anyConnected,
  onRangeChange,
  onRefresh,
  onGoHome,
}: {
  usage?: UsageSnapshot;
  range: UsageRange;
  loading: boolean;
  anyConnected: boolean;
  onRangeChange: (next: UsageRange) => void;
  onRefresh: () => void;
  onGoHome: () => void;
}) {
  const rangeLabel = RANGES.find((r) => r.value === range)?.label ?? "24小时";
  const matched = !!usage && usage.range === range;
  const { weeks, monthMarkers } = useMemo(() => buildHeatmapWeeks(usage?.heatmap ?? []), [usage?.heatmap]);

  if (!anyConnected) {
    return (
      <Elevated offset={2} shadowLevel={1} className="rounded-xl">
        <Card className="flex flex-col items-start gap-3 rounded-xl p-6">
          <h2 className={cn(typeClass("subtitle"), "m-0 text-foreground")} style={{ fontVariationSettings: fontWeights.semibold }}>
            接上助手后即可查看用量与日志
          </h2>
          <p className={cn(typeClass("body"), "m-0 text-muted-foreground")}>
            这里会汇总你在当前工作区的真实消费金额、Token 用量、365 天活跃度热力图，以及最近请求日志。
          </p>
          <Button variant="primary" size="compact" onClick={onGoHome}>
            前往首页接上
          </Button>
        </Card>
      </Elevated>
    );
  }

  const logs = matched ? (usage?.logs ?? []) : [];
  const totalRequests = matched ? (usage?.requests ?? 0) : 0;
  const totalTokens = matched ? (usage?.tokens ?? 0) : 0;
  const promptTokens = matched ? (usage?.promptTokens ?? 0) : 0;
  const completionTokens = matched ? (usage?.completionTokens ?? 0) : 0;
  const costCny = matched ? (usage?.costCny ?? "0.00") : "—";

  return (
    <div className="flex flex-col gap-5">
      {/* Toolbar */}
      <div className="flex flex-wrap items-center justify-between gap-3">
        <RangeTabs value={range} disabled={loading} onChange={onRangeChange} />
        <Button
          variant="tertiary"
          size="compact"
          leadingIcon={loading ? undefined : RefreshCw}
          loading={loading}
          disabled={loading}
          onClick={onRefresh}
        >
          {loading ? "正在读取" : "刷新数据"}
        </Button>
      </div>

      {/* KPI cards */}
      <section className="grid grid-cols-1 gap-3 sm:grid-cols-3" aria-label="用量统计">
        <Elevated offset={2} shadowLevel={1} className="rounded-xl">
          <Card className="flex flex-col gap-2 rounded-xl p-4">
            <div className="flex items-center justify-between gap-2">
              <span className={cn(typeClass("caption"), "inline-flex items-center gap-1.5 text-muted-foreground")}>
                <Coins size={13} aria-hidden="true" />
                花费
              </span>
              <span className={cn(typeClass("micro"), "text-muted-foreground")}>{rangeLabel}</span>
            </div>
            <p className={cn(typeClass("display"), "m-0 tabular-nums text-foreground")} style={{ fontVariationSettings: fontWeights.bold }}>
              {matched ? `¥${costCny}` : "—"}
            </p>
            <span className={cn(typeClass("micro"), "text-muted-foreground")}>
              {matched ? "按当前工作区实际计费汇总" : loading ? "正在读取该周期数据……" : "该周期暂无可用数据"}
            </span>
          </Card>
        </Elevated>

        <Elevated offset={2} shadowLevel={1} className="rounded-xl">
          <Card className="flex flex-col gap-2 rounded-xl p-4">
            <div className="flex items-center justify-between gap-2">
              <span className={cn(typeClass("caption"), "inline-flex items-center gap-1.5 text-muted-foreground")}>
                <Hash size={13} aria-hidden="true" />
                Tokens
              </span>
              <span className={cn(typeClass("micro"), "text-muted-foreground")}>{rangeLabel}</span>
            </div>
            <p className={cn(typeClass("display"), "m-0 tabular-nums text-foreground")} style={{ fontVariationSettings: fontWeights.bold }}>
              {matched ? formatCompactTokens(totalTokens) : "—"}
            </p>
            <span className={cn(typeClass("micro"), "tabular-nums text-muted-foreground")}>
              {matched
                ? `输入 ${formatCompactTokens(promptTokens)} · 输出 ${formatCompactTokens(completionTokens)}`
                : loading
                  ? "正在读取……"
                  : "暂无数据"}
            </span>
          </Card>
        </Elevated>

        <Elevated offset={2} shadowLevel={1} className="rounded-xl">
          <Card className="flex flex-col gap-2 rounded-xl p-4">
            <div className="flex items-center justify-between gap-2">
              <span className={cn(typeClass("caption"), "inline-flex items-center gap-1.5 text-muted-foreground")}>
                <Activity size={13} aria-hidden="true" />
                请求数
              </span>
              <span className={cn(typeClass("micro"), "text-muted-foreground")}>{rangeLabel}</span>
            </div>
            <p className={cn(typeClass("display"), "m-0 tabular-nums text-foreground")} style={{ fontVariationSettings: fontWeights.bold }}>
              {matched ? totalRequests.toLocaleString("zh-CN") : "—"}
            </p>
            <span className={cn(typeClass("micro"), "tabular-nums text-muted-foreground")}>
              {!matched
                ? loading
                  ? "正在读取……"
                  : "暂无数据"
                : totalRequests > 0
                  ? `平均每次 ${formatCompactTokens(Math.round(totalTokens / totalRequests))} tokens`
                  : "尚无调用记录"}
            </span>
          </Card>
        </Elevated>
      </section>

      {/* 365-day Activity Heatmap */}
      <Elevated offset={2} shadowLevel={1} className="rounded-xl">
        <Card className="flex flex-col gap-3 rounded-xl p-4" aria-label="年度活跃度热力图" role="region">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <span className={cn(typeClass("caption"), "text-foreground")} style={{ fontVariationSettings: fontWeights.semibold }}>
              近 365 天活跃度
            </span>
            <div className="flex items-center gap-1.5">
              <span className={cn(typeClass("micro"), "text-muted-foreground")}>少</span>
              {([0, 1, 2, 3, 4] as const).map((lvl) => (
                <span key={lvl} className={cn("size-2.5 rounded-[2px]", HEATMAP_LEVEL_CLASS[lvl])} aria-hidden="true" />
              ))}
              <span className={cn(typeClass("micro"), "text-muted-foreground")}>多</span>
            </div>
          </div>

          <div className="overflow-x-auto pb-1">
            <TooltipProvider delayDuration={80} skipDelayDuration={250} disableHoverableContent>
              <div className="inline-flex flex-col gap-1.5 min-w-max">
                <div className="relative h-4">
                  {monthMarkers.map((m) => (
                    <span
                      key={`${m.weekIndex}-${m.label}`}
                      className={cn(typeClass("micro"), "absolute text-muted-foreground")}
                      style={{ left: `${m.weekIndex * 13}px` }}
                    >
                      {m.label}
                    </span>
                  ))}
                </div>
                <div className="flex gap-[3px]">
                  {weeks.map((week, wi) => (
                    <div key={wi} className="flex flex-col gap-[3px]">
                      {week.map((cell) => (
                        <Tooltip
                          key={cell.date}
                          side="top"
                          content={
                            cell.requests > 0
                              ? `${cell.date} · ${cell.requests} 次请求 · ${formatCompactTokens(cell.tokens)} tokens · ¥${cell.costCny}`
                              : `${cell.date} · 暂无调用`
                          }
                        >
                          <span
                            tabIndex={0}
                            aria-label={`${cell.date} ${cell.requests} 次请求`}
                            className={cn(
                              "block size-[10px] rounded-[2px] transition-opacity duration-80 hover:opacity-80 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring",
                              HEATMAP_LEVEL_CLASS[cell.level],
                            )}
                          />
                        </Tooltip>
                      ))}
                    </div>
                  ))}
                </div>
              </div>
            </TooltipProvider>
          </div>
        </Card>
      </Elevated>

      {/* Recent Request Logs Table */}
      <Elevated offset={2} shadowLevel={1} className="rounded-xl">
        <Card className="flex flex-col gap-3 rounded-xl p-4" aria-label="最近请求日志" role="region">
          <div className="flex items-center justify-between gap-2">
            <span className={cn(typeClass("caption"), "text-foreground")} style={{ fontVariationSettings: fontWeights.semibold }}>
              最近请求日志
            </span>
            <Badge variant="solid" color="gray" size="compact">
              {logs.length > 0 ? `最新 ${logs.length} 条` : "暂无记录"}
            </Badge>
          </div>

          <Table size="compact">
            <TableHeader>
              <TableRow>
                <TableHead>时间</TableHead>
                <TableHead>模型</TableHead>
                <TableHead className="text-right">输入 / 输出</TableHead>
                <TableHead className="text-right">耗时</TableHead>
                <TableHead className="text-right">费用</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {logs.length === 0 ? (
                <TableEmpty
                  columns={5}
                  title="该时间段暂无请求记录"
                  description="在 Claude Code 或 Codex 中发出对话后，这里会自动展示模型、Token 与费用明细。"
                />
              ) : (
                logs.map((row, index) => (
                  <TableRow key={row.id} index={index}>
                    <TableCell className="whitespace-nowrap tabular-nums">
                      {formatLogTime(row.createdAt)}
                    </TableCell>
                    <TableCell className="max-w-[240px] truncate font-mono text-foreground">
                      {row.model || "-"}
                    </TableCell>
                    <TableCell className="text-right whitespace-nowrap tabular-nums">
                      {formatCompactTokens(row.promptTokens)} / {formatCompactTokens(row.completionTokens)}
                    </TableCell>
                    <TableCell className="text-right whitespace-nowrap tabular-nums">
                      {formatLatency(row.useTimeMs)}
                    </TableCell>
                    <TableCell className="text-right whitespace-nowrap tabular-nums text-foreground">
                      ¥{row.costCny}
                    </TableCell>
                  </TableRow>
                ))
              )}
            </TableBody>
          </Table>
        </Card>
      </Elevated>
    </div>
  );
}
