import { useState } from "react"
import { useForm } from "react-hook-form"
import { zodResolver } from "@hookform/resolvers/zod"
import { z } from "zod"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { Pipelines, Projects } from "@/lib/api"
import type { PipelineRun } from "@/lib/api"
import { useEventSource } from "@/lib/useEventSource"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { cn } from "@/lib/utils"
import { InlineLoader } from "@/components/ui/page-loader"
import { Pagination } from "@/components/ui/pagination"
import { formatDuration, relativeTime } from "@/lib/format"
import { can } from "@/lib/auth"
import { useForjaStore } from "@/lib/store"
import { RefreshCw, GitBranch, Loader2, ChevronRight, Rocket, Hammer } from "lucide-react"

// 构建部署看板（observability P0，observability-design §5.4；S6 构建部署分离改造）
// 数据源：GET /pipelines（webhook 落库的本地表，刷新不依赖 Gitea 存活）
// S6 交互：push 行为 PENDING（待构建）→ [构建] 发起验证+制品；
//          SUCCESS 行 → [部署]（立即/定时）→ 横幅展示当前线上版本。

const STATUSES = ["PENDING", "RUNNING", "SUCCESS", "FAILURE", "SKIPPED", "CANCELLED"] as const

function StatusBadge({ status }: { status: string }) {
  switch (status) {
    case "SUCCESS":
      return <Badge variant="success">成功</Badge>
    case "FAILURE":
      return <Badge variant="destructive">失败</Badge>
    case "PENDING":
      return (
        <Badge variant="outline" title="代码已入库，等待人工发起构建">
          待构建
        </Badge>
      )
    case "SKIPPED":
      return (
        <Badge variant="outline" title="未被调度（runner 掉线）">
          未触发
        </Badge>
      )
    case "CANCELLED":
      return <Badge variant="outline">已取消</Badge>
    default:
      return (
        <span className="inline-flex items-center gap-1 rounded-full border border-transparent bg-blue-500/15 px-2.5 py-0.5 text-xs font-semibold text-blue-600">
          <Loader2 className="size-3 animate-spin" /> 运行中
        </span>
      )
  }
}

function ConclusionMark({ conclusion }: { conclusion: string }) {
  // U-4：success/skipped/neutral 均计为绿；空/running/queued/in_progress/waiting 视为转圈
  if (conclusion === "success" || conclusion === "skipped" || conclusion === "neutral")
    return <Badge variant="success" className="px-1.5 py-0 text-[10px]">✓</Badge>
  if (conclusion === "failure" || conclusion === "cancelled")
    return <Badge variant="destructive" className="px-1.5 py-0 text-[10px]">✗</Badge>
  // running 态：空字符串或显式状态均转圈
  return (
    <span className="inline-flex items-center justify-center rounded-full bg-blue-500/15 px-1.5 py-0.5">
      <Loader2 className="size-3 animate-spin text-blue-600" />
    </span>
  )
}

const EVENT_LABELS: Record<string, string> = {
  push: "push",
  manual: "手动构建",
  workflow_dispatch: "dispatch",
  workflow_job: "ci",
  schedule: "schedule",
}

const deploySchema = z
  .object({
    mode: z.enum(["now", "scheduled"]),
    minutes: z.number().int().min(1).max(7 * 24 * 60).optional(),
  })
  .refine((d) => d.mode === "now" || (d.minutes ?? 0) >= 1, {
    path: ["minutes"],
    message: "请输入 1-10080 分钟",
  })
type DeployForm = z.infer<typeof deploySchema>

/** 部署确认弹窗：目标版本回显 vs 当前线上对比，立即 / 定时二选一 */
export function DeployDialog({ run, deployedTag, onClose, onDone }: {
  run: PipelineRun | null
  deployedTag?: string
  onClose: () => void
  onDone: () => void
}) {
  const form = useForm<DeployForm>({ resolver: zodResolver(deploySchema), defaultValues: { mode: "now" } })
  const { register, handleSubmit, watch, reset, formState: { errors, isSubmitting } } = form
  const mode = watch("mode")

  // 关闭即 reset（AGENTS 表单规范 #5）
  const close = () => { reset({ mode: "now" }); onClose() }

  const submit = async (d: DeployForm) => {
    try {
      // P0 按项目隔离：显式透传 project_id，后端按 project_id 维度互斥（fallback repo 映射）
      const pid = useForjaStore.getState().projectId
      await Pipelines.dispatch({
        kind: "deploy",
        sha: run!.sha,
        ...(d.mode === "scheduled" ? { delay_seconds: (d.minutes ?? 0) * 60 } : {}),
        ...(pid ? { project_id: pid } : {}),
      })
      toast.success(d.mode === "scheduled" ? `已排期 ${d.minutes} 分钟后部署 ${run!.sha.slice(0, 7)}` : `已触发部署 ${run!.sha.slice(0, 7)}`)
      onDone(); close()
    } catch (e: any) { toast.error(e.message) }
  }

  // U-1：按钮 loading+disabled 防连点（AGENTS 前端规范 #4，isSubmitting）

  return (
    <Dialog open={!!run} onOpenChange={(o) => !o && close()}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>部署确认</DialogTitle>
          <DialogDescription>把该版本的镜像滚动更新到宿主环境。</DialogDescription>
        </DialogHeader>
        <div className="text-xs space-y-1.5">
          <div className="flex items-center gap-2">
            <span className="text-muted-foreground">目标版本</span>
            <code className="bg-muted px-1.5 py-0.5 rounded">master-{run?.sha.slice(0, 8)}</code>
            <code className="text-muted-foreground">{run?.sha.slice(0, 7)}</code>
          </div>
          {deployedTag && (
            <div className="flex items-center gap-2">
              <span className="text-muted-foreground">当前线上</span>
              <code className={cn("px-1.5 py-0.5 rounded", deployedTag === `master-${run?.sha.slice(0, 8)}` ? "bg-muted" : "bg-amber-500/15 text-amber-700")}>
                {deployedTag}
              </code>
            </div>
          )}
        </div>
        <form onSubmit={handleSubmit(submit)} className="flex flex-col gap-2">
          <div className="flex flex-col gap-2">
            <label className="flex items-center gap-2 text-sm">
              <input type="radio" value="now" {...register("mode")} /> 立即部署
            </label>
            <label className="flex items-center gap-2 text-sm">
              <input type="radio" value="scheduled" {...register("mode")} />
              定时部署
              <Input type="number" min={1} max={10080} className="h-8 w-28" placeholder="分钟" {...register("minutes", { setValueAs: (v) => (v === "" || v === null ? undefined : Number(v)) })} disabled={mode !== "scheduled"} />
            </label>
            {errors.minutes && <span className="text-xs text-destructive">{errors.minutes.message}</span>}
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={close} disabled={isSubmitting}>取消</Button>
            <Button type="submit" disabled={isSubmitting}>
              {isSubmitting && <Loader2 className="size-3.5 animate-spin" />}
              {mode === "scheduled" ? "排期部署" : "确认部署"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

export default function PipelineBoard() {
  // 交付页为平台全局视图（不跟随 header 业务项目切换），避免 forja-admin/forja 流水线
  // 被业务项目过滤筛空；页内如需按项目筛选后续加独立下拉（U-2 页内筛选方案）
  const dispatchProjectId = useForjaStore((s) => s.projectId)
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(10)
  const [status, setStatus] = useState("")
  const [expandedId, setExpandedId] = useState("")
  const [deployRun, setDeployRun] = useState<PipelineRun | null>(null)
  // U-1: Tabs 状态（流水线 vs 部署记录，shadcn Tabs 单层不堆叠）
  const [activeTab, setActiveTab] = useState("pipeline")
  // U-1: 部署记录全量分页（后端已支持 page/page_size，修复前端仅 10 条截断）
  const [deployPage, setDeployPage] = useState(1)
  const [deployPageSize, setDeployPageSize] = useState(10)
  // U-1: 构建按钮幂等：loading+disabled 防连点（后端已有 409，isSubmitting 等价态）
  const [buildingShas, setBuildingShas] = useState<Set<string>>(new Set())

  const canOperate = can("user.update")
  const colCount = canOperate ? 9 : 8

  const qc = useQueryClient()

  // U-4：SSE 脏通知（全局视图，仍用当前项目通道作脏通知，轮询兜底全量）
  useEventSource(can("task.read") ? Projects.eventsURL(dispatchProjectId) : null, (m) => {
    if (m.name === "ci.completed" || m.name === "ci.progress" || m.name === "repo.push") {
      qc.invalidateQueries({ queryKey: ["pipelines"] })
      if (expandedId) qc.invalidateQueries({ queryKey: ["pipeline-detail", expandedId] })
    }
  })

  const listQuery = useQuery({
    queryKey: ["pipelines", status, page, pageSize],
    queryFn: () => Pipelines.list({ status: status || undefined, page, pageSize }),
    refetchInterval: 30_000,
  })
  const summaryQuery = useQuery({
    queryKey: ["pipelines-summary"],
    queryFn: () => Pipelines.summary(),
    // S6 §11：部署在途时加速刷新（deploy 实测 ~1min 完成），空闲 30s 兜底
    refetchInterval: (query) => ((query.state.data?.deploying ?? 0) > 0 ? 10_000 : 30_000),
  })
  const deploymentsQuery = useQuery({
    queryKey: ["pipelines-deployments", deployPage, deployPageSize],
    queryFn: () => Pipelines.deployments({ page: deployPage, pageSize: deployPageSize }),
    refetchInterval: (query) => {
      const items = query.state.data?.items ?? []
      return items.some((d) => d.status === "running" || d.status === "scheduled") ? 10_000 : 30_000
    },
  })
  const detailQuery = useQuery({
    queryKey: ["pipeline-detail", expandedId],
    queryFn: () => Pipelines.get(expandedId),
    enabled: !!expandedId,
    // U-4：运行中明细 5s 轮询兜底，完成后停止；SSE 已做到即时失效，此处仅为断线兜底
    refetchInterval: (q) => {
      const data: any = q.state.data
      if (!data) return false
      if (data.status === "RUNNING") return 5_000
      const jobs = data.jobs ?? []
      const hasRunning = jobs.some((j: any) => !j.conclusion || ["running", "queued", "in_progress", "waiting"].includes(j.conclusion))
      return hasRunning ? 5_000 : false
    },
  })

  const invalidate = () => { listQuery.refetch(); summaryQuery.refetch(); deploymentsQuery.refetch() }

  const doBuild = async (run: PipelineRun) => {
    const allowed = !run.branch || run.branch === "master" || run.branch.startsWith("iteration/")
    if (!allowed) {
      toast.error(`仅 master 和 iteration/* 分支可构建（当前: ${run.branch}）`)
      return
    }
    if (buildingShas.has(run.sha)) return
    setBuildingShas((prev) => new Set(prev).add(run.sha))
    try {
      // P0 透传 project_id（build 审计归属，后端 dispatchBuild 同样校验 project_id）
      await Pipelines.dispatch({ kind: "build", sha: run.sha, project_id: dispatchProjectId || undefined })
      toast.success(`已发起构建 ${run.sha.slice(0, 7)}（门禁 + 制品一体）`)
      invalidate()
    } catch (e: any) { toast.error(e.message) } finally {
      setBuildingShas((prev) => { const n = new Set(prev); n.delete(run.sha); return n })
    }
  }

  const items = listQuery.data?.items ?? []
  const total = listQuery.data?.total ?? 0
  const totalPages = Math.max(1, Math.ceil(total / pageSize))
  // §11 横幅只信 deployments 表的 success 行——构建完成/指令发出都不算"已上线"
  const deployed = summaryQuery.data?.deployed
  const deploying = summaryQuery.data?.deploying ?? 0
  const deploymentItems = deploymentsQuery.data?.items ?? []
  const deployTotal = deploymentsQuery.data?.total ?? 0
  const deployTotalPages = Math.max(1, Math.ceil(deployTotal / deployPageSize))

  return (
    <div className="space-y-4 max-w-6xl">
      <div className="flex items-center gap-2">
        <h1 className="text-lg font-bold">构建部署</h1>
        {(listQuery.isFetching || deploymentsQuery.isFetching) && <RefreshCw className="size-3 animate-spin text-muted-foreground" />}
      </div>

      {/* S6 §11 当前线上版本横幅：仅部署确认成功（success 回流）才显示 */}
      {(deployed || deploying > 0) && (
        <div data-testid="deployed-banner" className="flex flex-wrap items-center gap-2 border rounded-md px-3 py-2 bg-card text-xs">
          {deployed ? (
            <>
              <Rocket className="size-3.5 text-emerald-600" />
              <span className="font-medium">当前线上</span>
              <code className="bg-muted px-1.5 py-0.5 rounded">{deployed.tag}</code>
              <span className="text-muted-foreground">{relativeTime(deployed.at)} · by {deployed.by}</span>
            </>
          ) : null}
          {deploying > 0 && (
            <span className="inline-flex items-center gap-1 text-blue-600 font-medium">
              <Loader2 className="size-3 animate-spin" /> 部署中 ×{deploying}
            </span>
          )}
        </div>
      )}

      {/* U-1: Tabs 切换（流水线 / 部署记录），shadcn Tabs 单层不堆叠 */}
      <Tabs value={activeTab} onValueChange={setActiveTab} className="w-full">
        <TabsList>
          <TabsTrigger value="pipeline">流水线</TabsTrigger>
          <TabsTrigger value="deployments">部署记录</TabsTrigger>
        </TabsList>

        <TabsContent value="pipeline" className="space-y-4 mt-4">
          <div className="flex items-center justify-end gap-2">
            <select
              value={status}
              onChange={(e) => { setStatus(e.target.value); setPage(1) }}
              className="border rounded-md h-8 px-2 text-sm bg-background"
              aria-label="按结论筛选"
            >
              <option value="">全部结论</option>
              {STATUSES.map((s) => <option key={s} value={s}>{s}</option>)}
            </select>
            <Button variant="outline" size="sm" onClick={() => listQuery.refetch()} disabled={listQuery.isFetching}>
              <RefreshCw className={cn("size-3.5 mr-1", listQuery.isFetching && "animate-spin")} /> 刷新
            </Button>
          </div>

          <Card>
            <CardContent className="p-0">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="w-8"></TableHead>
                    <TableHead>仓库 / 分支</TableHead>
                    <TableHead>Commit</TableHead>
                    <TableHead>事件</TableHead>
                    <TableHead>结论</TableHead>
                    <TableHead>耗时</TableHead>
                    <TableHead>触发者</TableHead>
                    <TableHead className="text-right">时间</TableHead>
                    {canOperate && <TableHead className="text-right">操作</TableHead>}
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {items.map((run) => (
                    <PipelineRow
                      key={run.id}
                      run={run}
                      expanded={expandedId === run.id}
                      onToggle={() => setExpandedId(expandedId === run.id ? "" : run.id)}
                      jobs={expandedId === run.id ? detailQuery.data?.jobs : undefined}
                      showActions={canOperate}
                      onBuild={doBuild}
                      buildingShas={buildingShas}
                      onDeploy={setDeployRun}
                    />
                  ))}
                  {items.length === 0 && !listQuery.isLoading && (
                    <TableRow>
                      <TableCell colSpan={colCount} className="text-center text-sm text-muted-foreground py-10">
                        暂无流水线记录 —— push 项目仓库后出现待构建行
                      </TableCell>
                    </TableRow>
                  )}
                  {listQuery.isLoading && (
                    <TableRow>
                      <TableCell colSpan={colCount} className="text-center py-10">
                        <InlineLoader />
                      </TableCell>
                    </TableRow>
                  )}
                </TableBody>
              </Table>
            </CardContent>
          </Card>

          <Pagination
            page={page}
            totalPages={totalPages}
            total={total}
            pageSize={pageSize}
            disabled={listQuery.isFetching}
            sizeLabel="流水线每页条数"
            onPageChange={setPage}
            onPageSizeChange={(s) => { setPageSize(s); setPage(1) }}
          />        </TabsContent>

        <TabsContent value="deployments" className="space-y-4 mt-4">
          {/* S6 §11 部署记录：状态机 scheduled/running/success/failed 全程可见，全量分页 */}
          <Card>
            <CardHeader className="pb-2 flex flex-row items-center justify-between space-y-0">
              <CardTitle className="text-xs">部署记录</CardTitle>
              {deploymentsQuery.isFetching && <RefreshCw className="size-3 animate-spin text-muted-foreground" />}
            </CardHeader>
            <CardContent className="p-0">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>版本</TableHead>
                    <TableHead>状态</TableHead>
                    <TableHead>方式</TableHead>
                    <TableHead>操作者</TableHead>
                    <TableHead>发起时间</TableHead>
                    <TableHead className="text-right">上线时间</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {deploymentItems.map((d) => (
                    <TableRow key={d.id}>
                      <TableCell>
                        <div className="flex items-center gap-1.5">
                          <code className={cn("text-xs px-1 py-0.5 rounded", d.tag === deployed?.tag && "bg-emerald-500/15 text-emerald-700 font-semibold")}>{d.tag}</code>
                          {d.tag === deployed?.tag && <Badge variant="success" className="text-[10px] px-1.5 py-0">线上版本</Badge>}
                        </div>
                      </TableCell>
                      <TableCell><DeployStatusBadge status={d.status} /></TableCell>
                      <TableCell className="text-xs text-muted-foreground">{d.mode === "scheduled" ? "定时" : "手动"}</TableCell>
                      <TableCell className="text-xs text-muted-foreground">{d.actor || "—"}</TableCell>
                      <TableCell className="text-xs text-muted-foreground" title={d.created_at}>{relativeTime(d.created_at)}</TableCell>
                      <TableCell className="text-right text-xs text-muted-foreground" title={d.deployed_at || undefined}>{d.deployed_at ? relativeTime(d.deployed_at) : "—"}</TableCell>
                    </TableRow>
                  ))}
                  {deploymentItems.length === 0 && !deploymentsQuery.isLoading && (
                    <TableRow>
                      <TableCell colSpan={6} className="text-center text-sm text-muted-foreground py-6">
                        暂无部署记录 —— 构建成功后点「部署」发起
                      </TableCell>
                    </TableRow>
                  )}
                  {deploymentsQuery.isLoading && (
                    <TableRow>
                      <TableCell colSpan={6} className="text-center text-sm text-muted-foreground py-6">加载中…</TableCell>
                    </TableRow>
                  )}
                </TableBody>
              </Table>
            </CardContent>
          </Card>

          <Pagination
            page={deployPage}
            totalPages={deployTotalPages}
            total={deployTotal}
            pageSize={deployPageSize}
            disabled={deploymentsQuery.isFetching}
            sizeLabel="部署记录每页条数"
            onPageChange={setDeployPage}
            onPageSizeChange={(s) => { setDeployPageSize(s); setDeployPage(1) }}
          />
        </TabsContent>
      </Tabs>

      <DeployDialog run={deployRun} deployedTag={deployed?.tag} onClose={() => setDeployRun(null)} onDone={invalidate} />
    </div>
  )
}

function DeployStatusBadge({ status }: { status: string }) {
  switch (status) {
    case "success":
      return <Badge variant="success">已上线</Badge>
    case "failed":
      return <Badge variant="destructive">失败</Badge>
    case "scheduled":
      return <Badge variant="outline" title="已排期，等待到期执行">已排期</Badge>
    default:
      return (
        <span className="inline-flex items-center gap-1 rounded-full border border-transparent bg-blue-500/15 px-2.5 py-0.5 text-xs font-semibold text-blue-600">
          <Loader2 className="size-3 animate-spin" /> 部署中
        </span>
      )
  }
}

function PipelineRow({ run, expanded, onToggle, jobs, showActions, onBuild, buildingShas, onDeploy }: {
  run: PipelineRun
  expanded: boolean
  onToggle: () => void
  jobs?: { name: string; conclusion: string; duration_ms: number }[]
  showActions: boolean
  onBuild: (run: PipelineRun) => void
  buildingShas?: Set<string>
  onDeploy: (run: PipelineRun) => void
}) {
  const stop = (fn: () => void) => (e: React.MouseEvent) => { e.stopPropagation(); fn() }
  const isBuilding = !!buildingShas?.has(run.sha)
  const isAllowedBranch = !run.branch || run.branch === "master" || run.branch.startsWith("iteration/")
  const buildDisabled = isBuilding || !isAllowedBranch
  return (
    <>
      <TableRow
        className="cursor-pointer hover:bg-accent/50"
        onClick={onToggle}
        data-testid={`pipeline-row-${run.sha.slice(0, 7)}`}
      >
        <TableCell>
          <ChevronRight className={cn("size-4 transition-transform text-muted-foreground", expanded && "rotate-90")} />
        </TableCell>
        <TableCell>
          <div className="flex flex-col">
            <span className="text-sm font-medium truncate max-w-56">{run.repo}</span>
            {run.branch && (
              <span className="flex items-center gap-1 text-xs text-muted-foreground">
                <GitBranch className="size-3" />{run.branch}
              </span>
            )}
          </div>
        </TableCell>
        <TableCell><code className="text-xs">{run.sha.slice(0, 7)}</code></TableCell>
        <TableCell><span className="text-xs">{EVENT_LABELS[run.event] ?? run.event}</span></TableCell>
        <TableCell><StatusBadge status={run.status} /></TableCell>
        <TableCell className="text-xs">{formatDuration(run.duration_ms)}</TableCell>
        <TableCell className="text-xs text-muted-foreground">{run.actor || "—"}</TableCell>
        <TableCell className="text-right text-xs text-muted-foreground" title={run.created_at}>{relativeTime(run.created_at)}</TableCell>
        {showActions && (
          <TableCell className="text-right whitespace-nowrap" onClick={(e) => e.stopPropagation()}>
            {run.status === "PENDING" && (
              <Button
                size="sm"
                variant="forge"
                className="h-6 px-2 text-[11px]"
                disabled={buildDisabled}
                title={!isAllowedBranch ? `仅 master 和 iteration/* 分支可构建（当前: ${run.branch}）` : undefined}
                onClick={stop(() => onBuild(run))}
              >
                {isBuilding ? <Loader2 className="size-3 animate-spin" /> : <Hammer className="size-3 mr-1" />}
                {isBuilding ? "构建中…" : "构建"}
              </Button>
            )}
            {run.status === "SUCCESS" && (
              <Button size="sm" variant="outline" className="h-6 px-2 text-[11px]" onClick={stop(() => onDeploy(run))}>
                部署
              </Button>
            )}
          </TableCell>
        )}
      </TableRow>
      {expanded && (
        <TableRow className="hover:bg-transparent">
          <TableCell colSpan={showActions ? 9 : 8} className="bg-muted/40 py-3">
            {jobs === undefined ? (
              <span className="text-xs text-muted-foreground">加载步骤…</span>
            ) : jobs.length === 0 ? (
              <span className="text-xs text-muted-foreground">
                暂无 job 明细{run.status === "PENDING" ? "（待构建——点击行内「构建」发起）" : "（等待 workflow_job 回流）"}
              </span>
            ) : (
              <ol className="flex flex-col gap-1.5 pl-2 border-l border-border ml-2">
                {jobs.map((j) => (
                  <li key={j.name} className="flex items-center gap-2 text-xs">
                    <ConclusionMark conclusion={j.conclusion} />
                    <span className="font-medium">{j.name}</span>
                    <span className="text-muted-foreground">{formatDuration(j.duration_ms)}</span>
                  </li>
                ))}
              </ol>
            )}
          </TableCell>
        </TableRow>
      )}
    </>
  )
}
