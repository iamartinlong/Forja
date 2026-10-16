import { useEffect, useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { toast } from "sonner";
import { Tasks } from "@/lib/api";
import { Button } from "@/components/ui/button";
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";

const schema = z.object({ confirmTitle: z.string().min(1) });
type Form = z.infer<typeof schema>;

interface Props { task: any | null; onClose: () => void; onDone: () => void; }

export function ReleaseDialog({ task, onClose, onDone }: Props) {
  const [detail, setDetail] = useState<any>(null);
  const form = useForm<Form>({ resolver: zodResolver(schema) });
  const { register, handleSubmit, reset, formState: { errors, isSubmitting } } = form;

  useEffect(() => {
    if (!task) return;
    reset({});
    setDetail(null);
    Tasks.get(String(task.id)).then(setDetail).catch(() => setDetail(null));
  }, [task?.id]);

  const submit = async () => {
    if (!task) return;
    try {
      await Tasks.release(String(task.id));
      toast.success(`任务已发布：${task.title}`);
      onDone(); onClose();
    } catch (e: any) { toast.error(e.message); }
  };

  const failedTests = (detail?.test_results || []).filter((r: any) => r.status && r.status !== "passed");
  const files: string[] = (detail?.code_changes || []).map((c: any) => c.file_path);

  return (
    <Dialog open={!!task} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>发布签核</DialogTitle>
          <DialogDescription>确认锻造产物无误后输入任务名以确认发布。</DialogDescription>
        </DialogHeader>
        <div className="space-y-2 text-xs">
          <div className="flex flex-wrap gap-1 items-center">
            <Badge variant="outline">attempt {detail?.forge_attempt ?? task?.forge_attempt ?? "-"}</Badge>
            {(detail?.commit_sha || task?.commit_sha) && (
              <code className="bg-muted px-1 rounded">sha {String(detail?.commit_sha || task?.commit_sha).slice(0, 7)}</code>
            )}
          </div>
          <div><span className="text-muted-foreground">变更文件：</span>{files.length ? files.join("、") : "-"}</div>
          {failedTests.length > 0 && (
            <div className="text-destructive">
              未通过测试 {failedTests.length} 项：
              {failedTests.slice(0, 3).map((r: any, i: number) => <div key={i} className="truncate">{r.name}: {String(r.output || "").slice(0, 120)}</div>)}
            </div>
          )}
        </div>
        <form onSubmit={handleSubmit(submit)} className="flex flex-col gap-2">
          <label className="text-xs font-medium" htmlFor="confirmTitle">输入任务名「{task?.title}」确认</label>
          <Input id="confirmTitle" placeholder={task?.title} {...register("confirmTitle")} />
          {errors.confirmTitle && <span className="text-xs text-destructive">请输入完整任务名</span>}
          {form.watch("confirmTitle") !== task?.title && form.watch("confirmTitle") !== "" && (
            <span className="text-xs text-destructive">任务名不匹配</span>
          )}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>取消</Button>
            <Button type="submit" disabled={isSubmitting || form.watch("confirmTitle") !== task?.title}>确认发布</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
