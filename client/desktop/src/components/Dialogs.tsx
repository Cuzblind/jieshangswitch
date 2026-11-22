import { CheckCircle2 } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { InputCopy } from "@/components/ui/input-copy";
import { typeClass } from "@/lib/size-context";
import { cn } from "@/lib/utils";
import { viewAction, type Action, type Agent, type AppUpdate, type DesktopState, type View } from "@/lib/desktop";

type CopyAction = "copy-qq" | "copy-email" | "copy-report";

export function OperationDialog({
  open,
  view,
  state,
  busy,
  copied,
  onDismiss,
  onCopy,
  onAct,
}: {
  open: boolean;
  view?: View;
  state?: DesktopState;
  busy: boolean;
  copied: string;
  onDismiss: () => void;
  onCopy: (action: CopyAction, label: string) => void;
  onAct: (action: Action, agent?: Agent, confirm?: boolean) => void;
}) {
  const confirm = view?.screen === "confirm";
  const support = view?.screen === "support";
  const done = view?.screen === "success" || view?.screen === "done";
  const title = support ? "联系客服" : confirm ? "请确认" : done ? "准备好了" : "还差一步";

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) onDismiss();
      }}
    >
      <DialogContent size={support ? "lg" : "sm"} aria-describedby="operation-description">
        <DialogHeader>
          {done && (
            <div className="mb-1">
              <Badge variant="solid" color="green" size="compact">
                <CheckCircle2 size={12} className="mr-1 inline" aria-hidden="true" />
                已完成
              </Badge>
            </div>
          )}
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription id="operation-description">
            {support ? "把遇到的问题告诉我们，我们帮你一起解决。" : view?.line}
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-3 py-1">
          {view?.extra && <p className={cn(typeClass("body"), "m-0 text-foreground")}>{view.extra}</p>}
          {view?.note && <p className={cn(typeClass("caption"), "m-0 text-muted-foreground")}>{view.note}</p>}

          {support && (
            <div className="flex flex-col gap-2.5">
              {state?.qq && (
                <div className="flex items-center justify-between gap-2">
                  <div className="min-w-0 flex-1">
                    <InputCopy
                      value={state.qq}
                      label="客服 QQ"
                      onCopy={() => onCopy("copy-qq", "QQ 号已复制")}
                    />
                  </div>
                  <Button variant="tertiary" size="compact" onClick={() => onCopy("copy-qq", "QQ 号已复制")}>
                    复制 QQ 号
                  </Button>
                </div>
              )}
              {state?.email && (
                <div className="flex items-center justify-between gap-2">
                  <div className="min-w-0 flex-1">
                    <InputCopy
                      value={state.email}
                      label="客服邮箱"
                      onCopy={() => onCopy("copy-email", "邮箱已复制")}
                    />
                  </div>
                  <Button variant="tertiary" size="compact" onClick={() => onCopy("copy-email", "邮箱已复制")}>
                    复制邮箱
                  </Button>
                </div>
              )}
              {state?.report && (
                <div className="flex flex-col gap-2">
                  <pre className={cn(typeClass("caption"), "m-0 max-h-40 overflow-auto rounded-lg bg-surface-2 p-3 font-mono text-foreground ring-1 ring-border")}>
                    {state.report}
                  </pre>
                  <div>
                    <Button variant="ghost" size="compact" onClick={() => onCopy("copy-report", "问题报告已复制")}>
                      复制问题报告
                    </Button>
                  </div>
                </div>
              )}
              {copied && (
                <p role="status" className={cn(typeClass("caption"), "m-0 text-muted-foreground")}>
                  {copied}
                </p>
              )}
            </div>
          )}

          {view?.screen === "success" && (
            <p className={cn(typeClass("caption"), "m-0 text-muted-foreground")}>
              如果助手之前已经打开，请先保存任务并关闭，再重新打开。
            </p>
          )}
        </div>

        <DialogFooter>
          {!support && (
            <Button variant="tertiary" onClick={onDismiss}>
              {confirm ? "先不" : "关闭"}
            </Button>
          )}
          {!support && (
            <Button
              variant={confirm && view?.action === "restore-confirm" ? "destructive" : "primary"}
              disabled={busy}
              onClick={() => onAct(viewAction(view?.action), view?.agent, confirm)}
            >
              {view?.action === "restart" ? "返回首页" : view?.button?.replace("复制，", "") || "返回首页"}
            </Button>
          )}
          {support && (
            <Button variant="primary" onClick={onDismiss}>
              返回首页
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export function ClientUpdateDialog({
  update,
  updating,
  progress,
  onClose,
  onInstall,
}: {
  update?: AppUpdate;
  updating: boolean;
  progress: string;
  onClose: () => void;
  onInstall: () => void;
}) {
  return (
    <Dialog
      open={!!update}
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <DialogContent size="sm" aria-describedby="client-update-description">
        <DialogHeader>
          <DialogTitle>{update?.available ? `更新至 ${update.version}` : "接上AI已是最新版本"}</DialogTitle>
          <DialogDescription id="client-update-description">
            {update?.available ? "更新完成后会重新打开接上AI，保留你的连接设置。" : "当前没有可安装的客户端更新。"}
          </DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-2 py-1">
          {update?.notes && <p className={cn(typeClass("body"), "m-0 text-foreground")}>{update.notes}</p>}
          {updating && (
            <p role="status" className={cn(typeClass("caption"), "m-0 text-muted-foreground")}>
              {progress}
            </p>
          )}
        </div>
        <DialogFooter>
          <Button variant="tertiary" onClick={onClose}>
            {updating ? "取消更新并关闭" : "关闭"}
          </Button>
          {update?.available && (
            <Button variant="primary" loading={updating} disabled={updating} onClick={onInstall}>
              下载并更新
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
