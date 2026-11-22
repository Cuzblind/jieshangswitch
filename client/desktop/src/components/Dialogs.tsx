import { CheckCircle2, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
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
      <DialogContent aria-describedby="operation-description">
        <DialogClose asChild>
          <Button variant="ghost" size="icon" className="absolute right-3 top-3" aria-label="关闭弹窗">
            <X size={16} />
          </Button>
        </DialogClose>
        <DialogHeader>
          {done && (
            <div className="dialog-badge ok" aria-hidden="true">
              <CheckCircle2 size={20} />
            </div>
          )}
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription id="operation-description">
            {support ? "把遇到的问题告诉我们，我们帮你一起解决。" : view?.line}
          </DialogDescription>
        </DialogHeader>
        <div className="modal-body">
          {view?.extra && <p>{view.extra}</p>}
          {view?.note && <p className="modal-note">{view.note}</p>}
          {support && (
            <>
              {state?.qq && (
                <div className="contact">
                  <span>客服 QQ：{state.qq}</span>
                  <Button variant="outline" size="sm" onClick={() => onCopy("copy-qq", "QQ 号已复制")}>
                    复制 QQ 号
                  </Button>
                </div>
              )}
              {state?.email && (
                <div className="contact">
                  <span>邮箱：{state.email}</span>
                  <Button variant="outline" size="sm" onClick={() => onCopy("copy-email", "邮箱已复制")}>
                    复制邮箱
                  </Button>
                </div>
              )}
              {state?.report && (
                <>
                  <pre className="report">{state.report}</pre>
                  <Button variant="ghost" size="sm" onClick={() => onCopy("copy-report", "问题报告已复制")}>
                    复制问题报告
                  </Button>
                </>
              )}
              {copied && (
                <p role="status" className="modal-note">
                  {copied}
                </p>
              )}
            </>
          )}
          {view?.screen === "success" && (
            <p className="modal-note">如果助手之前已经打开，请先保存任务并关闭，再重新打开。</p>
          )}
        </div>
        <DialogFooter>
          {!support && (
            <Button variant="outline" onClick={onDismiss}>
              {confirm ? "先不" : "关闭"}
            </Button>
          )}
          {!support && (
            <Button disabled={busy} onClick={() => onAct(viewAction(view?.action), view?.agent, confirm)}>
              {view?.action === "restart" ? "返回首页" : view?.button?.replace("复制，", "") || "返回首页"}
            </Button>
          )}
          {support && <Button onClick={onDismiss}>返回首页</Button>}
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
      <DialogContent aria-describedby="client-update-description">
        <DialogHeader>
          <DialogTitle>{update?.available ? `更新至 ${update.version}` : "接上AI已是最新版本"}</DialogTitle>
          <DialogDescription id="client-update-description">
            {update?.available ? "更新完成后会重新打开接上AI，保留你的连接设置。" : "当前没有可安装的客户端更新。"}
          </DialogDescription>
        </DialogHeader>
        <div className="modal-body">
          {update?.notes && <p>{update.notes}</p>}
          {updating && <p role="status">{progress}</p>}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            {updating ? "取消更新并关闭" : "关闭"}
          </Button>
          {update?.available && (
            <Button disabled={updating} onClick={onInstall}>
              下载并更新
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
