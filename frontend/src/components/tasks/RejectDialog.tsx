import { useEffect } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { toast } from "sonner";
import { Tasks } from "@/lib/api";
import { Button } from "@/components/ui/button";
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle
} from "@/components/ui/dialog";
import { Textarea } from "@/components/ui/textarea";

const schema = z.object({ comment: z.string().min(5, "打回理由至少 5 个字") });
type Form = z.infer<typeof schema>;

interface Props { task: any | null; onClose: () => void; onDone: () => void; }

export function RejectDialog({ task, onClose, onDone }: Props) {
  const form = useForm<Form>({ resolver: zodResolver(schema), defaultValues: { comment: "" } });
  const { register, handleSubmit, reset, formState: { errors, isSubmitting } } = form;

  useEffect(() => { if (task) reset({ comment: "" }); }, [task?.id]);

  const submit = async (values: Form) => {
    try {
      await Tasks.reject(String(task.id), values.comment);
      toast.success(`已打回：${task.title}`);
      onDone(); onClose();
    } catch (e: any) { toast.error(e.message); }
  };

  return (
    <Dialog open={!!task} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>打回任务</DialogTitle>
          <DialogDescription>任务将退回，请说明问题以便重锻修正。</DialogDescription>
        </DialogHeader>
        <form onSubmit={handleSubmit(submit)} className="flex flex-col gap-2">
          <div className="flex flex-col gap-2">
            <label className="text-xs font-medium" htmlFor="comment">打回理由</label>
            <Textarea id="comment" rows={3} placeholder="描述缺陷或需调整的方向…" {...register("comment")} />
            {errors.comment && <span className="text-xs text-destructive">{errors.comment.message}</span>}
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>取消</Button>
            <Button type="submit" variant="destructive" disabled={isSubmitting}>确认打回</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
