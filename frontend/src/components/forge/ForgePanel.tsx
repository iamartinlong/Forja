import { useCallback, useEffect, useMemo, useState } from "react";
import { useNavigate, useParams } from "@tanstack/react-router";
import { ArrowLeft, ChevronRight, Flame, Loader2, RefreshCw } from "lucide-react";
import { toast } from "sonner";
import { Projects, Tasks } from "@/lib/api";
import { useForjaStore } from "@/lib/store";
import { can } from "@/lib/auth";
import { useMinLoading } from "@/hooks/useMinLoading";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty";
import { ConfirmDialog, type ConfirmState } from "@/components/shared/ConfirmDialog";
import { CreateTaskDialog } from "@/components/tasks/CreateTaskDialog";
import { ForgeStepper, readableActionRequired, statusVariant } from "./ForgeStepper";

// ---------- /forge（无参）：任务发现，替代手粘 UUID（audit-2026-08-29 B2） ----------
export function ForgeTaskPicker() {
  const projectId = useForjaStore((s) => s.projectId);
  const navigate = useNavigate();
  const [items, setItems] = useState<any[] | null>(null);
  const [refreshing, setRefreshing] = useState(false);
  const [createOpen, setCreateOpen] = useState(false);
  const showLoading = useMinLoading(refreshing, 400);

  const load = useCallback(async () => {
    setRefreshing(true);
    try {
      const r: any = await Tasks.list(projectId);
      setItems(r.items || []);
    } catch {
      /* 业务错误由 http 拦截器统一 toast（AGENTS.md 错误分层） */
    } finally {
      setRefreshing(false);
    }
  }, [projectId]);
  useEffect(() => { load(); }, [load]);

  // 进行中的任务排前，方便先观测再翻历史
  const sorted = useMemo(() => {
    const order: Record<string, number> = { RUNNING: 0, RELEASE_PENDING: 1, PENDING: 2, FAILED: 3, ESCALATED: 4, RELEASED: 5, PASSED: 6 };
    return [...(items || [])].sort((a, b) => (order[a.status] ?? 9) - (order[b.status] ?? 9));
  }, [items]);

  return (
    <div className="space-y-3">
      <Card>
        <CardHeader className="pb-2 flex flex-row items-start justify-between gap-2">
          <div className="min-w-0">
            <CardTitle className="text-sm">锻造流水线</CardTitle>
            <p className="text-xs text-muted-foreground mt-1">选择任务观测锻造过程：Agent 锻造 → 门禁校验 → 合并推送 → CI 构建 → 发布签核。</p>
          </div>
          <div className="flex gap-2 shrink-0">
            <Button variant="forge" size="sm" className="h-8" onClick={() => setCreateOpen(true)}>派生锻造任务</Button>
            <Button variant="ghost" size="sm" className="h-8 w-8 p-0" onClick={load} disabled={showLoading} aria-label="刷新">
              {showLoading ? <Loader2 className="size-3.5 animate-spin" /> : <RefreshCw className="size-3.5" />}
            </Button>
          </div>
        </CardHeader>
        <CardContent className="relative">
          {showLoading && (
            <div className="absolute inset-0 bg-background/60 backdrop-blur-[1px] z-10 flex items-center justify-center rounded-md">
              <span className="inline-flex items-center gap-2 text-xs text-muted-foreground bg-background border rounded-full px-3 py-1.5 shadow-sm">
                <Loader2 className="size-3.5 animate-spin text-primary" /> 加载中…
              </span>
            </div>
          )}
          {sorted.length === 0 ? (
            <Empty className="py-10 border">
              <EmptyHeader>
                <EmptyMedia><Flame className="size-4" /></EmptyMedia>
                <EmptyTitle>还没有锻造任务</EmptyTitle>
                <EmptyDescription>先在 Spec 编辑器完成并验证规格（VALIDATED），再从这里派生锻造任务。</EmptyDescription>
              </EmptyHeader>
              <Button variant="forge" size="sm" className="mt-2" onClick={() => setCreateOpen(true)}>派生首个锻造任务</Button>
            </Empty>
          ) : (
            <div className="divide-y rounded-md border overflow-hidden">
              {sorted.map((t) => (
                <button
                  key={t.id}
                  type="button"
                  onClick={() => navigate({ to: "/forge/$taskId", params: { taskId: String(t.id) } })}
                  className="w-full flex items-center gap-3 px-3 py-2 text-xs hover:bg-accent transition-colors text-left"
                >
                  <Badge variant={statusVariant(t.status)} className="shrink-0">{t.status}</Badge>
                  <span className="font-medium truncate flex-1">{t.title}</span>
                  <span className="text-muted-foreground shrink-0">P{t.priority}</span>
                  <span className="text-muted-foreground shrink-0 hidden sm:inline">{t.estimated_hours || "-"}h</span>
                  <ChevronRight className="size-3.5 text-muted-foreground shrink-0" />
                </button>
              ))}
            </div>
          )}
        </CardContent>
      </Card>
      <CreateTaskDialog
        open={createOpen}
        onClose={() => setCreateOpen(false)}
        onDone={(id) => navigate({ to: "/forge/$taskId", params: { taskId: id } })}
      />
    </div>
  );
}

// ---------- /forge/$taskId：单任务锻造观测（结构化状态卡 + SSE Stepper） ----------
export default function ForgePanel() {
  const params = useParams({ strict: false }) as { taskId?: string };
  const taskId = params.taskId || "";
  const navigate = useNavigate();
  const [task, setTask] = useState<any>(null);
  const [loadFailed, setLoadFailed] = useState(false);
  const [converge, setConverge] = useState<any>(null);
  const [loading, setLoading] = useState(false);
  const [approving, setApproving] = useState(false);
  const [converging, setConverging] = useState(false);
  const [confirm, setConfirm] = useState<ConfirmState | null>(null);
  // 项目内生效角色：undefined=加载中 / null=获取失败（均放行按钮，后端兜底 403）
  const [myRole, setMyRole] = useState<string | null | undefined>(undefined);
  const showLoading = useMinLoading(loading || approving || converging, 400);

  const load = useCallback(async () => {
    if (!taskId) return;
    setLoading(true);
    try {
      const r: any = await Tasks.get(taskId);
      setTask(r);
      setLoadFailed(false);
    } catch {
      setLoadFailed(true);
    } finally {
      setLoading(false);
    }
  }, [taskId]);
  useEffect(() => { setTask(null); setConverge(null); setLoadFailed(false); load(); }, [load]);

  const projectId = task?.project_id as string | undefined;
  useEffect(() => {
    if (!projectId) return;
    setMyRole(undefined);
    Projects.get(projectId).then((r: any) => setMyRole(r.my_role ?? null)).catch(() => setMyRole(null));
  }, [projectId]);

  // myRole: undefined=加载中（先隐藏，避免权限闪烁后消失）；null=获取失败（放行，后端兜底 403）
  const mayApprove = myRole === null ? true : myRole ? can("task.approve", myRole) : false;
  const mayConverge = myRole === null ? true : myRole ? can("task.run", myRole) : false;
  const canApproveNow = !!task && ["PENDING", "FAILED", "ESCALATED"].includes(task.status);

  const approve = async () => {
    setApproving(true);
    try {
      await Tasks.approve(taskId);
      toast.success("已进入锻造（RUNNING）");
      await load();
    } finally {
      setApproving(false);
    }
  };

  const doConverge = async () => {
    setConverging(true);
    try {
      const r: any = await Tasks.converge(taskId);
      setConverge(r);
      toast.success("收敛检查完成");
    } finally {
      setConverging(false);
    }
  };

  if (loadFailed) {
    return (
      <Empty className="py-12 border">
        <EmptyHeader>
          <EmptyMedia><Flame className="size-4" /></EmptyMedia>
          <EmptyTitle>任务不存在或无权访问</EmptyTitle>
          <EmptyDescription>task id：{taskId || "-"}</EmptyDescription>
        </EmptyHeader>
        <div className="flex gap-2 mt-2">
          <Button variant="outline" size="sm" onClick={load}>重试</Button>
          <Button variant="forge" size="sm" onClick={() => navigate({ to: "/forge" })}>返回任务列表</Button>
        </div>
      </Empty>
    );
  }

  return (
    <div className="space-y-3">
      <Card>
        <CardContent className="pt-4 flex items-center gap-2 flex-wrap min-w-0">
          <Button variant="ghost" size="sm" className="h-8 px-2" onClick={() => navigate({ to: "/forge" })}>
            <ArrowLeft className="size-3.5" />任务列表
          </Button>
          <span className="text-sm font-medium truncate max-w-[360px]">{task?.title || "…"}</span>
          {task && <Badge variant={statusVariant(task.status)}>{task.status}</Badge>}
          <div className="ml-auto flex gap-2">
            <Button variant="outline" size="sm" className="h-8" onClick={load} disabled={showLoading}>
              {showLoading ? <Loader2 className="size-3.5 animate-spin" /> : <RefreshCw className="size-3.5" />}刷新
            </Button>
            {mayApprove && canApproveNow && (
              <Button
                variant="forge" size="sm" className="h-8"
                onClick={() => setConfirm({
                  title: "开始锻造？",
                  description: "将调用 Agent 按 Spec 产出代码与测试，验证通过后自动推送并合并，最多 3 轮自动修复。",
                  confirmText: "开始锻造",
                  action: async () => { try { await approve(); } catch { /* 业务错误由 http 拦截器统一 toast（AGENTS.md 错误分层） */ } },
                })}
                disabled={showLoading}
              >
                Approve → RUNNING
              </Button>
            )}
            {mayConverge && task && (
              <Button variant="secondary" size="sm" className="h-8" onClick={doConverge} disabled={converging || showLoading}>Converge</Button>
            )}
          </div>
        </CardContent>
      </Card>
      {task && <ForgeStepper key={taskId} task={task} />}
      {task && <StatusCards task={task} />}
      {converge && <ConvergeCard converge={converge} />}
      <ConfirmDialog state={confirm} onClose={() => setConfirm(null)} />
    </div>
  );
}

// 结构化状态卡：替代原始 JSON dump（audit-2026-08-29 B5）
function StatusCards({ task }: { task: any }) {
  const execs: any[] = task.agent_executions || [];
  const tests: any[] = task.test_results || [];
  const files: string[] = (task.code_changes || []).map((c: any) => c.file_path).filter(Boolean);
  const failedTests = tests.filter((r: any) => r.status && r.status !== "passed").length;
  const ar = readableActionRequired(task.action_required);
  return (
    <div className="grid grid-cols-1 md:grid-cols-3 gap-3">
      <Card>
        <CardHeader className="pb-2"><CardTitle className="text-xs">状态</CardTitle></CardHeader>
        <CardContent className="text-xs space-y-1.5">
          <div className="flex items-center gap-2">
            <Badge variant={statusVariant(task.status)}>{task.status}</Badge>
            <span className="text-muted-foreground">置信度 {task.confidence == null ? "-" : `${Math.round(Number(task.confidence) * 100)}%`}</span>
          </div>
          {task.branch && <div className="text-muted-foreground break-all">锻造分支 <span className="font-mono">{task.branch}</span></div>}
          {task.commit_sha && <div className="text-muted-foreground">产物 <span className="font-mono">{String(task.commit_sha).slice(0, 7)}</span></div>}
          {ar && <div className="text-destructive break-words">需人工介入：{ar}</div>}
        </CardContent>
      </Card>
      <Card>
        <CardHeader className="pb-2"><CardTitle className="text-xs">锻造轮次{execs.length ? `（${execs.length}）` : ""}</CardTitle></CardHeader>
        <CardContent className="text-xs">
          {execs.length === 0 ? (
            <div className="text-muted-foreground">尚无 Agent 执行记录</div>
          ) : (
            <div className="space-y-1">
              {execs.map((e: any, i: number) => (
                <div key={i} className="flex items-center justify-between gap-2">
                  <span className="truncate">第 {e.attempt ?? "-"} 轮 · {e.agent_type}</span>
                  <Badge variant={e.status === "SUCCESS" ? "success" : e.status === "FAILED" ? "destructive" : "secondary"} className="shrink-0">{e.status}</Badge>
                </div>
              ))}
            </div>
          )}
        </CardContent>
      </Card>
      <Card>
        <CardHeader className="pb-2"><CardTitle className="text-xs">产物与验证</CardTitle></CardHeader>
        <CardContent className="text-xs space-y-1.5">
          <div className="break-all">变更文件 {files.length} 个{files.length ? <span className="text-muted-foreground">：{files.slice(0, 4).join(" · ")}{files.length > 4 ? " 等" : ""}</span> : ""}</div>
          <div>验证记录 {tests.length} 条{tests.length ? (failedTests ? <span className="text-destructive">，{failedTests} 条未通过</span> : "，全部通过") : ""}</div>
          {files.length === 0 && tests.length === 0 && <div className="text-muted-foreground">暂无产物记录</div>}
        </CardContent>
      </Card>
    </div>
  );
}

function ConvergeCard({ converge }: { converge: any }) {
  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="text-xs">
          Converge 报告 {converge.converged ? <Badge variant="success">收敛</Badge> : <Badge variant="destructive">漂移</Badge>}
        </CardTitle>
      </CardHeader>
      <CardContent className="text-xs space-y-1">
        <div>
          相似度 {(Number(converge.similarity || 0) * 100).toFixed(0)}% 缺失: {(converge.missing || []).join(",") || "-"} 漂移: {(converge.drift || []).join(",") || "-"}
        </div>
      </CardContent>
    </Card>
  );
}
