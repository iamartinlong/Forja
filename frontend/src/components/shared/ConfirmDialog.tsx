import { useState } from "react";
import { Button } from "@/components/ui/button";
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";

export interface ConfirmState {
  title: string;
  description?: string;
  confirmText?: string;
  danger?: boolean;
  /** 需要输入匹配该文本才能确认（高危操作） */
  requireTextMatch?: string;
  action: () => Promise<void> | void;
}

export function ConfirmDialog({ state, onClose }: { state: ConfirmState | null; onClose: () => void }) {
  const [match, setMatch] = useState("");
  const [busy, setBusy] = useState(false);
  if (!state) return null;
  const matched = !state.requireTextMatch || match === state.requireTextMatch;

  const run = async () => {
    setBusy(true);
    try { await state.action(); onClose(); }
    finally { setBusy(false); setMatch(""); }
  };

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>{state.title}</DialogTitle>
          {state.description && <DialogDescription>{state.description}</DialogDescription>}
        </DialogHeader>
        {state.requireTextMatch && (
          <div className="flex flex-col gap-2">
            <label className="text-xs font-medium" htmlFor="confirm-match">
              输入「{state.requireTextMatch}」以确认
            </label>
            <Input id="confirm-match" value={match} onChange={(e) => setMatch(e.target.value)} placeholder={state.requireTextMatch} />
            {match && !matched && <span className="text-xs text-destructive">输入不一致</span>}
          </div>
        )}
        <DialogFooter>
          <Button type="button" variant="outline" onClick={onClose}>取消</Button>
          <Button type="button" variant={state.danger ? "destructive" : "forge"} disabled={!matched || busy} onClick={run}>
            {busy ? "处理中…" : state.confirmText || "确认"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
