import { useRef } from "react";
import { Monitor, Moon, RotateCcw, Sun } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { FluidHoverHighlight } from "@/components/fluid-hover-highlight";
import { useFluidHover } from "@/hooks/use-fluid-hover";
import { Elevated } from "@/lib/elevated";
import { fontWeights } from "@/lib/font-weight";
import { useShape } from "@/lib/shape-context";
import { typeClass } from "@/lib/size-context";
import { useTheme, type ThemeMode } from "@/lib/theme";
import { cn } from "@/lib/utils";
import { useTranslation } from "react-i18next";

const modes: { mode: ThemeMode; icon: typeof Sun; key: string }[] = [
  { mode: "system", icon: Monitor, key: "theme.system" },
  { mode: "light", icon: Sun, key: "theme.light" },
  { mode: "dark", icon: Moon, key: "theme.dark" },
];

function SettingRow({
  title,
  note,
  children,
}: {
  title: string;
  note?: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex flex-wrap items-center justify-between gap-4 border-b border-border/70 py-3.5 last:border-b-0">
      <div className="min-w-0 flex-1">
        <p className={cn(typeClass("body"), "m-0 text-foreground")} style={{ fontVariationSettings: fontWeights.semibold }}>
          {title}
        </p>
        {note && <p className={cn(typeClass("caption"), "mt-0.5 mb-0 text-muted-foreground")}>{note}</p>}
      </div>
      <div className="shrink-0">{children}</div>
    </div>
  );
}

export function SettingsPage({
  version,
  availableVersion,
  clientChecking,
  clientUpdating,
  toolChecking,
  toolError,
  checkedAt,
  installing,
  disabled,
  onClientUpdate,
  onToolUpdates,
  onRestore,
}: {
  version?: string;
  availableVersion: string;
  clientChecking: boolean;
  clientUpdating: boolean;
  toolChecking: boolean;
  toolError?: string;
  checkedAt?: string;
  installing: boolean;
  disabled: boolean;
  onClientUpdate: () => void;
  onToolUpdates: () => void;
  onRestore: () => void;
}) {
  const { mode, setMode } = useTheme();
  const { t } = useTranslation();
  const shape = useShape();
  const themeGroupRef = useRef<HTMLDivElement>(null);
  const themeHover = useFluidHover(themeGroupRef, { axis: "x" });
  const checked = checkedAt ? new Date(checkedAt) : undefined;

  return (
    <section aria-label="设置" className="flex flex-col gap-5">
      <Elevated offset={2} shadowLevel={2} className="rounded-xl">
        <Card className="rounded-xl px-5 py-4">
          <h2 className={cn(typeClass("subtitle"), "mb-1 mt-0 text-foreground")} style={{ fontVariationSettings: fontWeights.semibold }}>
            外观
          </h2>
          <SettingRow title="主题" note="跟随系统或手动切换浅色 / 深色界面">
            <div
              ref={themeGroupRef}
              role="group"
              aria-label="主题"
              className="relative inline-flex items-center gap-0.5 rounded-lg bg-surface-2 p-0.5 ring-1 ring-border"
              {...themeHover.handlers}
            >
              <FluidHoverHighlight hover={themeHover} className={cn(shape.bg, "z-0")} />
              {modes.map(({ mode: value, icon: Icon, key }, index) => {
                const active = mode === value;
                return (
                  <button
                    key={value}
                    ref={(el) => themeHover.registerItem(index, el)}
                    type="button"
                    aria-pressed={active}
                    onClick={() => setMode(value)}
                    className={cn(
                      "relative z-10 inline-flex h-7 items-center gap-1.5 rounded-md px-2.5 whitespace-nowrap transition-colors duration-80",
                      typeClass("caption"),
                      active ? "bg-active text-foreground" : "text-muted-foreground hover:text-foreground",
                    )}
                    style={{ fontVariationSettings: active ? fontWeights.semibold : fontWeights.normal }}
                  >
                    <Icon size={13} strokeWidth={1.75} aria-hidden="true" />
                    {t(key)}
                  </button>
                );
              })}
            </div>
          </SettingRow>
        </Card>
      </Elevated>

      <Elevated offset={2} shadowLevel={2} className="rounded-xl">
        <Card className="rounded-xl px-5 py-4">
          <div className="mb-1 flex items-center gap-2">
            <h2 className={cn(typeClass("subtitle"), "m-0 text-foreground")} style={{ fontVariationSettings: fontWeights.semibold }}>
              更新
            </h2>
            {availableVersion && (
              <Badge variant="solid" color="accent" size="compact">
                新版本 {availableVersion}
              </Badge>
            )}
          </div>
          <SettingRow
            title="接上AI"
            note={`当前版本 ${version ?? "—"}${availableVersion ? `，可更新到 ${availableVersion}` : ""}`}
          >
            <Button
              variant="tertiary"
              size="compact"
              loading={clientChecking}
              disabled={clientChecking || clientUpdating || installing}
              onClick={onClientUpdate}
            >
              {clientChecking ? "正在检查…" : availableVersion ? `接上AI有更新 ${availableVersion}` : "检查客户端更新"}
            </Button>
          </SettingRow>
          <SettingRow
            title="Claude Code 和 Codex"
            note={
              toolError ||
              (checked && !Number.isNaN(checked.getTime())
                ? `上次检查 ${checked.toLocaleString()}`
                : "每天自动检查一次")
            }
          >
            <Button
              variant="tertiary"
              size="compact"
              loading={toolChecking}
              disabled={disabled || toolChecking}
              onClick={onToolUpdates}
            >
              {toolChecking ? "正在检查更新…" : "检查助手更新"}
            </Button>
          </SettingRow>
        </Card>
      </Elevated>

      <Elevated offset={2} shadowLevel={2} className="rounded-xl">
        <Card className="rounded-xl px-5 py-4">
          <h2 className={cn(typeClass("subtitle"), "mb-1 mt-0 text-destructive")} style={{ fontVariationSettings: fontWeights.semibold }}>
            恢复
          </h2>
          <SettingRow
            title="全部恢复设置"
            note="把两个助手恢复到接上之前的设置，停用这里加的 MCP 服务和 Skills，并移除命令行路径。会先请你确认。"
          >
            <Button
              variant="destructive"
              size="compact"
              leadingIcon={RotateCcw}
              disabled={disabled || installing}
              onClick={onRestore}
            >
              全部恢复设置
            </Button>
          </SettingRow>
        </Card>
      </Elevated>
    </section>
  );
}
