import { Monitor, Moon, RotateCcw, Sun } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useTheme, type ThemeMode } from "@/lib/theme";
import { useTranslation } from "react-i18next";

const modes: { mode: ThemeMode; icon: typeof Sun; key: string }[] = [
  { mode: "system", icon: Monitor, key: "theme.system" },
  { mode: "light", icon: Sun, key: "theme.light" },
  { mode: "dark", icon: Moon, key: "theme.dark" },
];

function Row({ title, note, children }: { title: string; note?: string; children: React.ReactNode }) {
  return (
    <div className="setting-row">
      <div>
        <p className="setting-title">{title}</p>
        {note && <p className="setting-note">{note}</p>}
      </div>
      <div className="setting-control">{children}</div>
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
  const checked = checkedAt ? new Date(checkedAt) : undefined;
  return (
    <section className="settings" aria-label="设置">
      <div className="setting-group">
        <h2>外观</h2>
        <Row title="主题">
          <div className="segmented" role="group" aria-label="主题">
            {modes.map(({ mode: value, icon: Icon, key }) => (
              <button key={value} type="button" aria-pressed={mode === value} onClick={() => setMode(value)}>
                <Icon size={13} aria-hidden="true" />
                {t(key)}
              </button>
            ))}
          </div>
        </Row>
      </div>
      <div className="setting-group">
        <h2>更新</h2>
        <Row title="接上AI" note={`当前版本 ${version ?? "—"}${availableVersion ? `，可更新到 ${availableVersion}` : ""}`}>
          <Button variant="outline" size="sm" disabled={clientChecking || clientUpdating || installing} onClick={onClientUpdate}>
            {clientChecking ? "正在检查…" : availableVersion ? `接上AI有更新 ${availableVersion}` : "检查客户端更新"}
          </Button>
        </Row>
        <Row
          title="Claude Code 和 Codex"
          note={toolError || (checked && !Number.isNaN(checked.getTime()) ? `上次检查 ${checked.toLocaleString()}` : "每天自动检查一次")}
        >
          <Button variant="outline" size="sm" disabled={disabled || toolChecking} onClick={onToolUpdates}>
            {toolChecking ? "正在检查更新…" : "检查助手更新"}
          </Button>
        </Row>
      </div>
      <div className="setting-group danger">
        <h2>恢复</h2>
        <Row title="全部恢复设置" note="把两个助手恢复到接上之前的设置，停用这里加的 MCP 服务和 Skills，并移除命令行路径。会先请你确认。">
          <Button variant="outline" size="sm" disabled={disabled || installing} onClick={onRestore}>
            <RotateCcw size={13} />
            全部恢复设置
          </Button>
        </Row>
      </div>
    </section>
  );
}
