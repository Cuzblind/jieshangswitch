import { useMemo } from "react";
import { RefreshCw, Sparkles } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Banner, BannerDescription, BannerTitle } from "@/components/ui/banner";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { InputCopy } from "@/components/ui/input-copy";
import { Elevated } from "@/lib/elevated";
import { fontWeights } from "@/lib/font-weight";
import { typeClass } from "@/lib/size-context";
import { cn } from "@/lib/utils";
import type { Agent, CatalogModel, CatalogSnapshot } from "@/lib/desktop";

const VENDOR_ORDER = ["Anthropic", "OpenAI", "Google", "DeepSeek", "Zhipu", "Moonshot"];

const VENDOR_LABEL: Record<string, string> = {
  Anthropic: "Anthropic · Claude",
  OpenAI: "OpenAI · GPT",
  Google: "Google · Gemini",
  DeepSeek: "DeepSeek · 深度求索",
  Zhipu: "Zhipu · 智谱 GLM",
  Moonshot: "Moonshot · Kimi",
};

const TIER_LABEL: Record<string, string> = {
  economy: "省钱线路",
  balanced: "均衡线路",
  stable: "稳定线路",
};

function formatContextWindow(tokens?: number): string {
  if (!tokens || tokens <= 0) return "";
  if (tokens >= 1_000_000) return `${(tokens / 1_000_000).toFixed(tokens % 1_000_000 === 0 ? 0 : 1)}M 上下文`;
  return `${Math.round(tokens / 1_000)}K 上下文`;
}

function normalizeVendor(raw?: string): string {
  const v = (raw ?? "").trim();
  if (!v) return "其他模型";
  const found = VENDOR_ORDER.find((known) => known.toLowerCase() === v.toLowerCase());
  return found ?? v;
}

export function ModelsPage({
  agent,
  catalog,
  connected,
  loading,
  onRefresh,
  onGoHome,
}: {
  agent: Agent;
  catalog?: CatalogSnapshot;
  connected: boolean;
  loading: boolean;
  onRefresh: () => void;
  onGoHome: () => void;
}) {
  const agentTitle = agent === "codex" ? "Codex" : "Claude Code";

  const groups = useMemo(() => {
    const list = catalog?.models ?? [];
    const byVendor = new Map<string, CatalogModel[]>();
    for (const model of list) {
      const vendor = normalizeVendor(model.vendor);
      const bucket = byVendor.get(vendor) ?? [];
      bucket.push(model);
      byVendor.set(vendor, bucket);
    }
    const keys = Array.from(byVendor.keys()).sort((a, b) => {
      const ia = VENDOR_ORDER.indexOf(a);
      const ib = VENDOR_ORDER.indexOf(b);
      if (ia !== -1 && ib !== -1) return ia - ib;
      if (ia !== -1) return -1;
      if (ib !== -1) return 1;
      return a.localeCompare(b);
    });
    return keys.map((vendor) => ({
      vendor,
      label: VENDOR_LABEL[vendor] ?? vendor,
      models: byVendor.get(vendor) ?? [],
    }));
  }, [catalog?.models]);

  if (!connected) {
    return (
      <Elevated offset={2} shadowLevel={1} className="rounded-xl">
        <Card className="flex flex-col items-start gap-3 rounded-xl p-6">
          <h2 className={cn(typeClass("subtitle"), "m-0 text-foreground")} style={{ fontVariationSettings: fontWeights.semibold }}>
            先接上 {agentTitle}，即可查看已授权的模型清单
          </h2>
          <p className={cn(typeClass("body"), "m-0 text-muted-foreground")}>
            接上AI 会按你当前选择的线路自动同步可用模型（支持 Anthropic、OpenAI、Google、DeepSeek、智谱与 Kimi），并可在这里一键复制模型 ID。
          </p>
          <Button variant="primary" size="compact" onClick={onGoHome}>
            前往首页接上
          </Button>
        </Card>
      </Elevated>
    );
  }

  const totalModels = catalog?.models?.length ?? 0;

  return (
    <div className="flex flex-col gap-5">
      {/* Toolbar */}
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex flex-wrap items-center gap-2">
          <Badge variant="solid" color="gray" size="compact">
            共 {totalModels} 个可用模型
          </Badge>
          {catalog?.tier && (
            <Badge variant="solid" color="blue" size="compact">
              {TIER_LABEL[catalog.tier] ?? catalog.tier}
            </Badge>
          )}
          {groups.length > 0 && (
            <Badge variant="solid" color="gray" size="compact">
              {groups.length} 家厂商
            </Badge>
          )}
        </div>
        <Button
          variant="tertiary"
          size="compact"
          leadingIcon={loading ? undefined : RefreshCw}
          loading={loading}
          disabled={loading}
          onClick={onRefresh}
        >
          {loading ? "正在同步" : "刷新清单"}
        </Button>
      </div>

      {catalog?.default_model && (
        <Banner status="info" contrast="low" size="compact" icon={Sparkles}>
          <BannerTitle>当前默认主模型：{catalog.default_model}</BannerTitle>
          <BannerDescription>
            在 {agentTitle} 中无需手动配置即可直接开聊；如需临时切换其他模型，点击下方卡片右侧即可一键复制模型 ID。
          </BannerDescription>
        </Banner>
      )}

      {totalModels === 0 ? (
        <Elevated offset={2} shadowLevel={1} className="rounded-xl">
          <Card className="flex flex-col items-start gap-2 rounded-xl p-6">
            <h2 className={cn(typeClass("subtitle"), "m-0 text-foreground")} style={{ fontVariationSettings: fontWeights.semibold }}>
              {loading ? "正在获取可用模型……" : "暂时没有展示的模型清单"}
            </h2>
            <p className={cn(typeClass("body"), "m-0 text-muted-foreground")}>
              {loading
                ? "请稍等片刻，正在从当前线路同步授权模型。"
                : "你仍可以直接打开助手使用默认模型，或点击右上角「刷新清单」重新同步。"}
            </p>
          </Card>
        </Elevated>
      ) : (
        <div className="flex flex-col gap-5">
          {groups.map((group) => (
            <section key={group.vendor} className="flex flex-col gap-2.5" aria-label={group.label}>
              <div className="flex items-center gap-2">
                <h2 className={cn(typeClass("caption"), "m-0 text-foreground")} style={{ fontVariationSettings: fontWeights.semibold }}>
                  {group.label}
                </h2>
                <Badge variant="solid" color="gray" size="compact">
                  {group.models.length}
                </Badge>
              </div>
              <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                {group.models.map((m) => {
                  const isDefault = !!catalog?.default_model && m.id === catalog.default_model;
                  const ctx = formatContextWindow(m.context_window);
                  return (
                    <Elevated key={m.id} offset={2} shadowLevel={1} className="rounded-xl">
                      <Card className="flex flex-col gap-2.5 rounded-xl p-4">
                        <div className="flex items-start justify-between gap-2">
                          <span
                            className={cn(typeClass("body"), "text-foreground")}
                            style={{ fontVariationSettings: fontWeights.semibold }}
                          >
                            {m.display_name || m.id}
                          </span>
                          <div className="flex shrink-0 items-center gap-1.5">
                            {isDefault && (
                              <Badge variant="solid" color="green" size="compact">
                                默认
                              </Badge>
                            )}
                            {ctx && (
                              <Badge variant="solid" color="gray" size="compact">
                                {ctx}
                              </Badge>
                            )}
                          </div>
                        </div>
                        <InputCopy
                          value={m.id}
                          size="compact"
                          variant="icon"
                          aria-label={`复制模型 ID ${m.id}`}
                        />
                      </Card>
                    </Elevated>
                  );
                })}
              </div>
            </section>
          ))}
        </div>
      )}
    </div>
  );
}
