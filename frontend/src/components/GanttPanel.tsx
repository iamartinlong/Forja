import { lazy, Suspense, useEffect, useState } from "react";
import { toast } from "sonner";
import { Tasks } from "@/lib/api";
import { useForjaStore } from "@/lib/store";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Separator } from "@/components/ui/separator";
import { InlineLoader } from "@/components/ui/page-loader";

const GanttView = lazy(() => import("@/components/GanttView"));

export default function GanttPanel() {
  const [dag, setDag] = useState<any>(null);
  const [est, setEst] = useState<any>(null);
  const [estTitle, setEstTitle] = useState("新功能开发");
  const projectId = useForjaStore((s) => s.projectId);
  const load = async () => {
    try { const d: any = await Tasks.dag(projectId); setDag(d); } catch (e: any) { toast.error(e.message); }
  };
  const doEstimate = async () => {
    try { const r: any = await Tasks.estimate({ title: estTitle, priority: 5 }); setEst(r); } catch (e: any) { toast.error(e.message); }
  };
  useEffect(() => { load(); }, [projectId]);
  return (
    <div className="space-y-3">
      <div className="flex gap-2"><Button variant="outline" size="sm" onClick={load}>刷新甘特</Button><span className="text-xs text-muted-foreground">基于 DAG 并行度与 estimated_hours 自动排期，依赖连线展示关键路径</span></div>
      {/* S1.6 / KI-001：局部 Suspense 边界——懒挂起不得逃逸到路由层触发整页 reload */}
      {dag ? (
        <Suspense fallback={<InlineLoader text="甘特图加载中…" />}>
          <GanttView nodes={dag.nodes || []} edges={dag.edges || []} />
        </Suspense>
      ) : <InlineLoader />}
      <Card>
        <CardHeader className="pb-2"><CardTitle className="text-xs">工时估算 (历史偏差模型)</CardTitle></CardHeader>
        <CardContent className="space-y-2">
          <div className="flex gap-2"><Input value={estTitle} onChange={(e) => setEstTitle(e.target.value)} placeholder="任务标题" className="flex-1 h-8" /><Button size="sm" variant="forge" onClick={doEstimate}>估算</Button></div>
          {est && <div className="text-xs">预估 <Badge variant="forge">{est.estimated_hours}h</Badge> 偏差 ±{(est.deviation * 100).toFixed(0)}% 模型 {est.model}</div>}
          <Separator />
          <div className="text-xs space-y-1"><div>节点 {dag?.nodes?.length || 0} 边 {dag?.edges?.length || 0} 并行 ready {dag?.ready?.length || 0}</div><div className="text-muted-foreground">工时：estimated_hours，無则 4h × 优先级系数；偏差随历史样本收敛 (15%→8%)；进度同前</div></div>
        </CardContent>
      </Card>
    </div>
  );
}
