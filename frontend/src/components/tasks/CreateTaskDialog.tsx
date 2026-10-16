import { useEffect, useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { toast } from "sonner";
import { Specs, Tasks, Iterations, Projects } from "@/lib/api";
import { useForjaStore } from "@/lib/store";
import { can } from "@/lib/auth";
import { Button } from "@/components/ui/button";
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";

const schema = z.object({
  title: z.string().min(2, "标题至少 2 个字"),
  description: z.string().min(5, "描述至少 5 个字"),
  spec_id: z.string().min(1, "请选择 Spec"),
  spec_refs: z.string(),
  target_iteration_id: z.string().optional(),
});
type Form = z.infer<typeof schema>;

interface Props { open: boolean; onClose: () => void; onDone: (tid: string) => void; }

export function CreateTaskDialog({ open, onClose, onDone }: Props) {
  const projectId = useForjaStore(s => s.projectId);
  // 项目内生效角色：undefined=加载中 / null=获取失败（均放行渲染，后端兜底 403）
  const [myRole, setMyRole] = useState<string | null | undefined>(undefined);
  const [specs, setSpecs] = useState<any[]>([]);
  const [iterations, setIterations] = useState<any[]>([]);
  const form = useForm<Form>({
    resolver: zodResolver(schema),
    defaultValues: { title: "", description: "", spec_id: "", spec_refs: "", target_iteration_id: "" }
  });
  const { register, handleSubmit, reset, setValue, watch, formState: { errors, isSubmitting } } = form;

  useEffect(() => {
    if (!open) return;
    reset({ title: "", description: "", spec_id: "", spec_refs: "", target_iteration_id: "" });
    setMyRole(undefined);
    Projects.get(projectId).then((r: any) => setMyRole(r.my_role ?? null)).catch(() => setMyRole(null));
    Specs.list(undefined, projectId, 1, 50)
      .then((r: any) => setSpecs((r.items || []).filter((s: any) => ["VALIDATED", "PASSED"].includes(s.status))))
      .catch(() => setSpecs([]));
    Iterations.list(projectId).then((r: any) => setIterations(r.items || r || [])).catch(() => setIterations([]));
  }, [open, projectId]);

  const pickSpec = (id: string) => {
    const s = specs.find(x => String(x.id) === id);
    setValue("spec_id", id, { shouldValidate: true });
    const ref = s?.file_path || s?.path || "";
    if (ref) setValue("spec_refs", ref);
  };

  const submit = async (values: Form) => {
    try {
      const refs = values.spec_refs.split(/[,，\n]/).map(s => s.trim()).filter(Boolean);
      const r: any = await Tasks.create({
        title: values.title, description: values.description,
        spec_id: values.spec_id, spec_refs: refs,
        target_iteration_id: values.target_iteration_id || undefined,
      });
      toast.success(`任务已创建：${values.title}`);
      onDone(r.id); onClose();
    } catch (e: any) { toast.error(e.message); }
  };

  // 仅在项目角色确认且无 task.write 权限时隐藏（修复：task.create 不在平台矩阵导致
  // 非 Admin 一律渲染 null、主按钮点击无反应——见 audit-2026-08-29 /forge 报告 A1）
  if (myRole !== undefined && myRole !== null && !can("task.write", myRole)) return null;

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>派生锻造任务</DialogTitle>
          <DialogDescription>从已验证的 Spec 派生任务，进入看板后拖入 RUNNING 开始锻造。</DialogDescription>
        </DialogHeader>
        <form onSubmit={handleSubmit(submit)} className="flex flex-col gap-3">
          <div className="flex flex-col gap-2">
            <label className="text-xs font-medium" htmlFor="t-title">标题</label>
            <Input id="t-title" placeholder="如：实现登录接口" {...register("title")} />
            {errors.title && <span className="text-xs text-destructive">{errors.title.message}</span>}
          </div>
          <div className="flex flex-col gap-2">
            <label className="text-xs font-medium" htmlFor="t-desc">描述</label>
            <Textarea id="t-desc" rows={3} placeholder="补充验收要点…" {...register("description")} />
            {errors.description && <span className="text-xs text-destructive">{errors.description.message}</span>}
          </div>
          <div className="flex flex-col gap-2">
            <label className="text-xs font-medium" htmlFor="t-spec">来源 Spec（已验证）</label>
            <select id="t-spec" className="h-9 rounded-md border bg-transparent px-2 text-sm"
              value={watch("spec_id")} onChange={e => pickSpec(e.target.value)}>
              <option value="">请选择…</option>
              {specs.map((s: any) => (
                <option key={s.id} value={s.id}>{s.title}（{s.status}）</option>
              ))}
            </select>
            {errors.spec_id && <span className="text-xs text-destructive">{errors.spec_id.message}</span>}
          </div>
          <div className="flex flex-col gap-2">
            <label className="text-xs font-medium" htmlFor="t-refs">产物路径（逗号分隔，可留空）</label>
            <Input id="t-refs" placeholder="pkg/calc/calc.go" {...register("spec_refs")} />
          </div>
          <div className="flex flex-col gap-2">
            <label className="text-xs font-medium" htmlFor="t-iter">迭代（可选）</label>
            <select id="t-iter" className="h-9 rounded-md border bg-transparent px-2 text-sm" {...register("target_iteration_id")}>
              <option value="">未归迭代（backlog）</option>
              {iterations.map((it: any) => (
                <option key={it.id} value={it.id}>{it.name}（{it.status}）</option>
              ))}
            </select>
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>取消</Button>
            <Button type="submit" disabled={isSubmitting}>创建</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
