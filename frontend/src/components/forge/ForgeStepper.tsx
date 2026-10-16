import { useEffect, useRef, useState } from "react";
import { Link } from "@tanstack/react-router";
import { Tasks } from "@/lib/api";
import { useEventSource, type SSEMessage } from "@/lib/useEventSource";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { cn } from "@/lib/utils";

// ForgeStepper：SSE 实时驱动的锻造阶段视图（observability P1，design §6.1）
// 阶段对齐真实事件源：④ 为「合并推送」（forge.pushed/forge.merged），不再冒充 CI 构建；
// CI 结论由后端 ci.passed/ci.failed 事件（task 频道双发，pipeline_store.go linkCIToTask）驱动独立徽章。
export const FORGE_STAGES = ["规格校验", "Agent 锻造", "门禁校验", "合并推送", "发布签核"] as const;

// stage: -1 未开始 / 0..4 进行中的阶段索引；done: 已完成的最高阶段；failAt: 失败阶段
export interface ForgeStageState {
  active: number
  done: number
  failAt: number
  attempt: number
  sha?: string
  status: string
  reason?: string
  /** CI 结论：pending=已推送待构建 / passed / failed；undefined=无信号 */
  ci?: "pending" | "passed" | "failed"
}

export function deriveStage(status: string): ForgeStageState {
  switch (status) {
    case "RUNNING": return { active: 1, done: 0, failAt: -1, attempt: 0, status }
    case "RELEASE_PENDING": return { active: 4, done: 3, failAt: -1, attempt: 0, status }
    case "RELEASED": return { active: -1, done: 4, failAt: -1, attempt: 0, status }
    // 存量 PASSED（A5 前）等同待发布
    case "PASSED": return { active: 4, done: 3, failAt: -1, attempt: 0, status }
    case "FAILED":
    case "ESCALATED": return { active: -1, done: 0, failAt: 1, attempt: 0, status }
    default: return { active: -1, done: -1, failAt: -1, attempt: 0, status } // PENDING 等
  }
}

// 事件 → 状态纯函数（可单测）；返回 log 行供组件统一落日志
export function reduceForgeEvent(prev: ForgeStageState, m: SSEMessage): { state: ForgeStageState; log?: string } {
  const d = m.data || {}
  const merge = (patch: Partial<ForgeStageState>): ForgeStageState => ({ ...prev, ...patch })
  switch (m.name) {
    case "snapshot": {
      // 连接首帧：无本地进度时以快照状态初始化；有进度则只补 attempt/sha
      const base = prev.attempt > 0 || prev.done >= 0 ? prev : deriveStage(d.status || "PENDING")
      return { state: { ...base, attempt: d.forge_attempt ?? prev.attempt, sha: d.commit_sha || prev.sha, status: d.status || prev.status } }
    }
    case "status": {
      const log = `状态 → ${d.status}${d.reason ? `（${d.reason}）` : ""}`
      if (d.status === "RUNNING") return { state: { ...deriveStage("RUNNING"), attempt: prev.attempt }, log }
      if (d.status === "RELEASE_PENDING") return { state: merge({ active: 4, done: Math.max(prev.done, 3), status: "RELEASE_PENDING", sha: d.commit_sha || prev.sha, attempt: d.attempt ?? prev.attempt }), log }
      if (d.status === "RELEASED" || d.status === "PASSED") return { state: merge({ active: -1, done: 4, status: d.status }), log }
      if (d.status === "FAILED" || d.status === "ESCALATED") {
        // 保留更精确的失败位置（如门禁 failAt=2），不笼统回落到「Agent 锻造」
        const failAt = prev.failAt >= 0 ? prev.failAt : Math.max(prev.active, 1)
        return { state: merge({ active: -1, failAt, status: d.status, reason: d.reason }), log }
      }
      return { state: merge({ status: d.status }), log }
    }
    case "forge.attempt": {
      const a = d.attempt ?? 1
      return { state: merge({ active: 1, done: Math.max(prev.done, 0), failAt: -1, attempt: a }), log: `第 ${a} 轮锻造开始` }
    }
    case "agent.done":
      return { state: merge({ active: 2, done: Math.max(prev.done, 1) }), log: `Agent 完成（${d.files ?? 0} 个文件）` }
    case "validate.result": {
      if (d.passed) return { state: merge({ active: 3, done: Math.max(prev.done, 2) }), log: `门禁通过（attempt ${d.attempt ?? "?"}）` }
      return { state: merge({ active: -1, done: Math.max(prev.done, 1), failAt: 2 }), log: "门禁未过，回炉重试" }
    }
    case "forge.pushed":
      // 推送 ≠ CI 通过：只记 sha 并把 CI 置为「待构建」（webhook 将建 PENDING 流水线行）
      return { state: merge({ active: 3, sha: d.sha || prev.sha, ci: "pending" }), log: `产物已推送 ${String(d.sha || "").slice(0, 7)}` }
    case "forge.merged":
      return { state: merge({ done: Math.max(prev.done, 3) }), log: `已合并至基分支 ${d.base_branch || ""}`.trim() }
    case "forge.error":
      // 过程性异常（attempt 循环内）：只记日志，不置终态红叉；终态由 status 事件宣告
      return { state: prev, log: `第 ${d.attempt ?? "?"} 轮 ${d.stage || ""} 阶段异常：${d.error || ""}`.trim() }
    case "ci.passed":
      return { state: merge({ ci: "passed" }), log: `CI 通过（${d.job || "pipeline"}）` }
    case "ci.failed":
      return { state: merge({ ci: "failed" }), log: `CI 失败（${d.job || "pipeline"}）` }
    default:
      return { state: prev }
  }
}

export function statusVariant(status: string): "success" | "forge" | "destructive" | "secondary" {
  switch (status) {
    case "RELEASED":
    case "PASSED": return "success"
    case "RUNNING":
    case "RELEASE_PENDING": return "forge"
    case "FAILED":
    case "ESCALATED": return "destructive"
    default: return "secondary"
  }
}

// action_required 落库为字符串（可能为 JSON 数组序列化），转人类可读
export function readableActionRequired(v: any): string {
  if (v == null || v === "") return ""
  if (Array.isArray(v)) return v.join("；")
  if (typeof v === "string") {
    const t = v.trim()
    if (t.startsWith("[")) {
      try {
        const a = JSON.parse(t)
        if (Array.isArray(a)) return a.map(String).join("；")
      } catch { /* 非 JSON 原样返回 */ }
    }
    return t
  }
  return String(v)
}

function initialFromTask(task: any): ForgeStageState {
  const base = deriveStage(task?.status || "PENDING")
  return { ...base, attempt: task?.forge_attempt || 0, sha: task?.commit_sha || undefined }
}

// 基础回放：SSE 无历史，任务事实由 Tasks.get 落地为日志首屏（不再"等待事件…"空白）
function replayFacts(task: any): string[] {
  if (!task) return []
  const facts: string[] = []
  facts.push(`任务当前状态 ${task.status}`)
  if ((task.forge_attempt || 0) > 0) facts.push(`已完成 ${task.forge_attempt} 轮锻造 attempt`)
  if (task.commit_sha) facts.push(`产物 commit ${String(task.commit_sha).slice(0, 7)}${task.branch ? ` · 分支 ${task.branch}` : ""}`)
  const ar = readableActionRequired(task.action_required)
  if (ar) facts.push(`人工介入信息：${ar}`)
  const tests: any[] = task.test_results || []
  if (tests.length) {
    const failed = tests.filter((r: any) => r.status && r.status !== "passed").length
    facts.push(`验证记录 ${tests.length} 条${failed ? `，其中 ${failed} 条未通过` : "，全部通过"}`)
  }
  return facts
}

export function ForgeStepper({ task }: { task: any }) {
  const [st, setSt] = useState<ForgeStageState>(() => initialFromTask(task));
  const [log, setLog] = useState<string[]>(() => replayFacts(task));
  const stRef = useRef(st);
  stRef.current = st;
  const connectedRef = useRef(false);

  const { connected } = useEventSource(Tasks.streamURL(String(task.id)), (m) => {
    const { state, log: line } = reduceForgeEvent(stRef.current, m);
    stRef.current = state;
    setSt(state);
    if (line) setLog((prev) => [`${new Date().toLocaleTimeString()} ${line}`, ...prev].slice(0, 30));
  });
  connectedRef.current = connected;

  // SSE 离线时以任务实体兜底同步（如 Approve 后刷新）；在线则以事件流为准，避免覆盖直播进度
  useEffect(() => {
    if (connectedRef.current) return;
    setSt((prev) => {
      const base = deriveStage(task.status);
      return { ...base, attempt: Math.max(base.attempt, task.forge_attempt || 0, prev.attempt), sha: task.commit_sha || prev.sha, ci: prev.ci };
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [task?.id, task?.status, task?.forge_attempt, task?.commit_sha]);

  const view = st;
  const dot = (i: number) => {
    if (view.failAt === i) return "bg-destructive text-destructive-foreground";
    if (view.done >= i) return "bg-forge-success text-white";
    if (view.active === i) return "bg-forge-ember text-white animate-pulse";
    return "bg-muted text-muted-foreground";
  };
  const showCIFallback = !view.ci && !!view.sha && ["RELEASE_PENDING", "PASSED", "RELEASED"].includes(view.status);

  return (
    <Card>
      <CardHeader className="pb-2 flex flex-row items-center justify-between gap-2">
        <CardTitle className="text-xs">锻造流水线 · attempt {view.attempt || "-"}{view.sha ? ` · ${String(view.sha).slice(0, 7)}` : ""}</CardTitle>
        <div className="flex items-center gap-2">
          <span className={cn("inline-flex items-center gap-1 text-[10px]", connected ? "text-emerald-600" : "text-muted-foreground")}>
            <span className={cn("size-1.5 rounded-full", connected ? "bg-emerald-500 animate-pulse" : "bg-muted-foreground/40")} />
            {connected ? "实时同步" : "离线重连中"}
          </span>
          <Badge variant={view.failAt >= 0 ? "destructive" : statusVariant(view.status)}>{view.status}</Badge>
        </div>
      </CardHeader>
      <CardContent className="space-y-3">
        <div className="flex items-center gap-1.5 flex-wrap">
          {FORGE_STAGES.map((label, i) => (
            <div key={label} className="flex items-center gap-1.5">
              <span className={`inline-flex size-5 items-center justify-center rounded-full text-[10px] font-bold ${dot(i)}`}>{view.done >= i ? "✓" : view.failAt === i ? "✗" : i + 1}</span>
              <span className={`text-[11px] ${view.active === i ? "font-semibold" : "text-muted-foreground"}`}>{label}</span>
              {i < FORGE_STAGES.length - 1 && <span className="text-muted-foreground/40">→</span>}
            </div>
          ))}
        </div>
        {view.reason && <div className="text-[11px] text-destructive break-all">{view.reason}</div>}
        {(view.ci || showCIFallback) && (
          <div className="flex items-center gap-2 text-[11px] text-muted-foreground">
            <span>CI 构建：</span>
            {view.ci === "passed" && <Badge variant="success">通过</Badge>}
            {view.ci === "failed" && <Badge variant="destructive">失败</Badge>}
            {view.ci === "pending" && <Badge variant="outline">待构建</Badge>}
            {!view.ci && <Badge variant="secondary">结论未同步</Badge>}
            <Link to="/pipelines" className="underline-offset-2 hover:underline">查看流水线</Link>
          </div>
        )}
        <pre data-testid="forge-event-log" className="text-[10px] bg-forge-anvil text-zinc-100 p-3 rounded whitespace-pre-wrap min-h-16 max-h-40 overflow-auto">{log.length ? log.join("\n") : "等待事件…"}</pre>
      </CardContent>
    </Card>
  );
}
