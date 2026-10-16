import { useNavigate, useParams } from "@tanstack/react-router"
import { useQuery } from "@tanstack/react-query"
import { toast } from "sonner"
import { ArrowLeft, Share2, GitBranch, FileCode2, FlaskConical, CheckCircle2, CircleDashed, Loader2 } from "lucide-react"
import { Specs } from "@/lib/api"
import type { LifecycleTask, PipelineRun } from "@/lib/api"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Badge } from "@/components/ui/badge"
import { cn } from "@/lib/utils"
import { formatDuration, relativeTime } from "@/lib/format"

// Spec 生命周期页（observability P2，observability-design §6.3）
// 数据源：GET /specs/:id/lifecycle 单次聚合；Stepper 状态由各源推导，不新增状态字段
// 深链：/specs/$specId 可直接分享；行内追溯链接互跳

const TASK_STATUS_VARIANT: Record<string, "success" | "destructive" | "forge" | "secondary" | "warning"> = {
  RELEASED: "success",
  PASSED: "success",
  FIXED: "success",
  FAILED: "destructive",
  ESCALATED: "destructive",
  RUNNING: "forge",
  VALIDATING: "forge",
  RELEASE_PENDING: "warning",
  PENDING: "secondary",
}

function TaskStatusBadge({ status }: { status: string }) {
  return <Badge variant={TASK_STATUS_VARIANT[status] ?? "secondary"}>{status}</Badge>
}

function RunStatusBadge({ status }: { status: string }) {
  if (status === "SUCCESS") return <Badge variant="success">成功</Badge>
  if (status === "FAILURE") return <Badge variant="destructive">失败</Badge>
  if (status === "CANCELLED") return <Badge variant="outline">已取消</Badge>
  return (
    <span className="inline-flex items-center gap-1 rounded-full bg-blue-500/15 px-2.5 py-0.5 text-xs font-semibold text-blue-600">
      <Loader2 className="size-3 animate-spin" /> 运行中
    </span>
  )
}

interface Stage {
  key: string
  label: string
  done: boolean
  note?: string
}

function LifecycleStepper({ stages }: { stages: Stage[] }) {
  // 已完成的最远阶段索引：其后未达阶段显示为虚位
  let lastDone = -1
  stages.forEach((s, i) => { if (s.done) lastDone = i })
  return (
    <div className="flex items-start gap-1.5 flex-wrap" data-testid="spec-lifecycle-stepper">
      {stages.map((s, i) => {
        const reached = i <= lastDone
        return (
          <div key={s.key} className="flex items-center gap-1.5">
            <div className={cn("flex flex-col items-center gap-1 min-w-16 text-center")}>
              <span
                className={cn(
                  "inline-flex size-6 items-center justify-center rounded-full",
                  s.done ? "bg-forge-success text-white"
                    : reached ? "bg-forge-success/40 text-white"
                    : "bg-muted text-muted-foreground",
                )}
              >
                {s.done ? <CheckCircle2 className="size-4" /> : <CircleDashed className="size-4" />}
              </span>
              <span className={cn("text-[11px] leading-tight", s.done ? "font-semibold" : "text-muted-foreground")}>
                {s.label}
              </span>
              {s.note && <span className="text-[10px] text-muted-foreground leading-none">{s.note}</span>}
            </div>
            {i < stages.length - 1 && <span className="text-muted-foreground/40 mt-1">→</span>}
          </div>
        )
      })}
    </div>
  )
}

export function SpecLifecyclePage() {
  const { specId } = useParams({ strict: false }) as { specId: string }
  const navigate = useNavigate()

  const query = useQuery({
    queryKey: ["spec-lifecycle", specId],
    queryFn: () => Specs.lifecycle(specId),
    enabled: !!specId,
  })

  if (query.isLoading) {
    return <div className="text-sm p-4 flex items-center gap-2"><Loader2 className="size-4 animate-spin text-primary" /> 生命周期加载中…</div>
  }
  if (query.isError || !query.data) {
    return (
      <div className="space-y-3 p-4">
        <div className="text-sm text-destructive">生命周期加载失败（规格不存在或无权访问）</div>
        <Button variant="outline" size="sm" onClick={() => query.refetch()}>重试</Button>
      </div>
    )
  }

  const { spec, requirements, versions, tasks, code_changes, ci_runs } = query.data

  const stages: Stage[] = [
    { key: "inbox", label: "Inbox 原料", done: requirements.length > 0, note: requirements.length ? `${requirements.length} 条` : undefined },
    { key: "draft", label: "DRAFT", done: true },
    { key: "validated", label: "VALIDATED", done: spec.status === "VALIDATED" },
    { key: "tasks", label: "任务", done: tasks.length > 0, note: tasks.length ? `${tasks.length} 个` : undefined },
    { key: "code", label: "代码", done: code_changes.length > 0 || tasks.some(t => t.commit_sha), note: code_changes.length ? `${code_changes.length} 文件` : undefined },
    { key: "ci", label: "CI Run", done: ci_runs.length > 0 },
    { key: "released", label: "RELEASED", done: tasks.some(t => t.status === "RELEASED") },
  ]

  const share = async () => {
    try {
      await navigator.clipboard.writeText(window.location.href)
      toast.success("深链已复制，可直接分享")
    } catch {
      toast.error("复制失败，请手动复制地址栏链接")
    }
  }

  return (
    <div className="space-y-4 max-w-6xl">
      <div className="flex items-center justify-between gap-3">
        <div className="flex items-center gap-2 min-w-0">
          <Button variant="ghost" size="icon" onClick={() => navigate({ to: "/specs" })} aria-label="返回列表">
            <ArrowLeft className="size-4" />
          </Button>
          <div className="min-w-0">
            <h1 className="text-lg font-bold truncate">{spec.title}</h1>
            <div className="flex items-center gap-2 text-xs text-muted-foreground">
              <Badge variant="outline">{spec.layer}</Badge>
              <TaskStatusBadge status={spec.status} />
              <span>当前版本 {spec.current_version}</span>
              <code className="hidden md:inline">{spec.id.slice(0, 8)}</code>
            </div>
          </div>
        </div>
        <Button variant="outline" size="sm" onClick={share}>
          <Share2 className="size-3.5 mr-1" /> 分享深链
        </Button>
      </div>

      <Card>
        <CardHeader className="pb-2"><CardTitle className="text-sm">生命周期</CardTitle></CardHeader>
        <CardContent><LifecycleStepper stages={stages} /></CardContent>
      </Card>

      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="text-sm">版本演进时间线 <Badge variant="outline">{versions.length}</Badge></CardTitle>
        </CardHeader>
        <CardContent>
          {versions.length === 0 ? (
            <div className="text-xs text-muted-foreground">暂无版本记录</div>
          ) : (
            <ol className="flex flex-col gap-3 border-l border-border ml-2 pl-4">
              {versions.map((v) => (
                <li key={v.id} className="relative">
                  <span className="absolute -left-[21px] top-1 size-2 rounded-full bg-forge-ember" />
                  <div className="flex items-center gap-2 flex-wrap">
                    <code className="text-xs font-semibold">{v.version}</code>
                    <span className="text-[10px] text-muted-foreground">{relativeTime(v.created_at)}</span>
                  </div>
                  {v.change_log && <div className="text-xs text-muted-foreground mt-0.5 break-all">{v.change_log}</div>}
                </li>
              ))}
            </ol>
          )}
        </CardContent>
      </Card>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
        <Card>
          <CardHeader className="pb-2"><CardTitle className="text-sm">来源原料 <Badge variant="outline">{requirements.length}</Badge></CardTitle></CardHeader>
          <CardContent className="space-y-2">
            {requirements.length === 0 && <div className="text-xs text-muted-foreground">无关联 Inbox 原料</div>}
            {requirements.map(r => (
              <div key={r.id} className="border rounded-md p-2 text-xs space-y-1">
                <div className="flex items-center gap-2">
                  <Badge variant="outline">{r.source_type}</Badge>
                  <Badge variant={r.status === "CONVERTED" ? "success" : "secondary"}>{r.status}</Badge>
                  <code className="ml-auto text-[10px] text-muted-foreground">{r.id.slice(0, 8)}</code>
                </div>
                <div className="line-clamp-2 text-muted-foreground">{r.snippet}</div>
              </div>
            ))}
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="pb-2"><CardTitle className="text-sm">关联任务 <Badge variant="outline">{tasks.length}</Badge></CardTitle></CardHeader>
          <CardContent className="space-y-2">
            {tasks.length === 0 && <div className="text-xs text-muted-foreground">尚无任务 —— 在锻造流水线以该规格创建</div>}
            {tasks.map(t => <TaskRow key={t.id} task={t} onOpen={() => navigate({ to: "/forge" })} />)}
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="pb-2"><CardTitle className="text-sm">代码产出 <Badge variant="outline">{code_changes.length}</Badge></CardTitle></CardHeader>
          <CardContent className="space-y-1.5">
            {code_changes.length === 0 && <div className="text-xs text-muted-foreground">暂无代码变更</div>}
            {code_changes.map((c, i) => (
              <div key={`${c.task_id}-${i}`} className="flex items-center gap-2 text-xs">
                <FileCode2 className="size-3.5 shrink-0 text-muted-foreground" />
                <code className="truncate flex-1">{c.file_path}</code>
                <span className="text-[10px] text-muted-foreground shrink-0">{relativeTime(c.created_at)}</span>
              </div>
            ))}
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="pb-2"><CardTitle className="text-sm">CI Run <Badge variant="outline">{ci_runs.length}</Badge></CardTitle></CardHeader>
          <CardContent className="space-y-2">
            {ci_runs.length === 0 && <div className="text-xs text-muted-foreground">暂无流水线记录 —— 任务产物 push 后按 commit sha 锚定回流</div>}
            {ci_runs.map(r => <RunRow key={r.id} run={r} onOpen={() => navigate({ to: "/pipelines" })} />)}
          </CardContent>
        </Card>
      </div>

      <div className="text-xs text-muted-foreground flex items-center gap-1.5">
        <FlaskConical className="size-3.5" />
        追溯链：Inbox 原料 → Spec 版本 → 任务（attempt）→ 代码 → CI Run → 发布签核；各区块点击可跳转对应看板
      </div>
    </div>
  )
}

function TaskRow({ task, onOpen }: { task: LifecycleTask; onOpen: () => void }) {
  return (
    <button type="button" onClick={onOpen} className="w-full border rounded-md p-2 text-left hover:border-ring transition-colors space-y-1">
      <div className="flex items-center gap-2 flex-wrap">
        <TaskStatusBadge status={task.status} />
        <span className="text-xs font-medium truncate flex-1">{task.title}</span>
        {task.forge_attempt > 0 && <Badge variant="outline" className="text-[10px]">attempt {task.forge_attempt}</Badge>}
      </div>
      <div className="flex items-center gap-2 text-[10px] text-muted-foreground">
        <code>{task.id.slice(0, 8)}</code>
        {task.commit_sha && <code>sha {task.commit_sha.slice(0, 7)}</code>}
        <span className="ml-auto">{relativeTime(task.created_at)}</span>
      </div>
    </button>
  )
}

function RunRow({ run, onOpen }: { run: PipelineRun; onOpen: () => void }) {
  return (
    <button type="button" onClick={onOpen} className="w-full border rounded-md p-2 text-left hover:border-ring transition-colors flex items-center gap-2 flex-wrap text-xs">
      <RunStatusBadge status={run.status} />
      <GitBranch className="size-3 text-muted-foreground" />
      <span className="text-muted-foreground">{run.branch || run.event}</span>
      <code>{run.sha.slice(0, 7)}</code>
      <span className="ml-auto text-[10px] text-muted-foreground">{formatDuration(run.duration_ms)} · {relativeTime(run.created_at)}</span>
    </button>
  )
}

export default SpecLifecyclePage
