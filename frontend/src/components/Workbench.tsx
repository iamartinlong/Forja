import { useEffect, useState } from "react"
import { useNavigate } from "@tanstack/react-router"
import { useQuery } from "@tanstack/react-query"
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Empty, EmptyHeader, EmptyMedia, EmptyTitle, EmptyDescription } from "@/components/ui/empty"
import { Switch } from "@/components/ui/switch"
import { Label } from "@/components/ui/label"
import { Separator } from "@/components/ui/separator"
import { useForjaStore } from "@/lib/store"
import { can, globalRole } from "@/lib/auth"
import { api, Audit, Cost, Inbox, Iterations, Pipelines, Projects, Releases, Specs, Tasks } from "@/lib/api"
import { useMinLoading } from "@/hooks/useMinLoading"
import { useEventSource } from "@/lib/useEventSource"
import { relativeTime } from "@/lib/format"
import { Inbox as InboxIcon, FileText, Kanban, GitMerge, Wallet, Flame, Factory, Users, FolderKanban, ArrowRight, RefreshCw, Loader2, LayoutDashboard, ShieldCheck, Search, BarChart3, GanttChart, MessageCircle } from "lucide-react"
import { ResponsiveContainer, ComposedChart, Line, Bar, XAxis, YAxis, Tooltip, Legend } from "recharts"

function StatCard({ title, value, sub, icon: Icon, variant }: any) {
  return (
    <Card>
      <CardContent className="p-4">
        <div className="flex items-center justify-between">
          <div className="text-xs text-muted-foreground">{title}</div>
          {Icon && <Icon className="h-4 w-4 text-muted-foreground" />}
        </div>
        <div className="text-2xl font-bold mt-1">{value}</div>
        {sub && <div className="text-xs text-muted-foreground mt-1">{sub}</div>}
        {variant && <Badge variant={variant} className="mt-2 text-[10px]">{variant}</Badge>}
      </CardContent>
    </Card>
  )
}

export default function Workbench() {
  const navigate = useNavigate()
  const role = globalRole()
  const projectId = useForjaStore((s) => s.projectId)
  const [agg, setAgg] = useState(role === "PM")
  const [tab, setTab] = useState("overview")

  // projects for agg mode + single project meta
  const projectsQ = useQuery({ queryKey: ["projects", "workbench-list"], queryFn: () => Projects.list("", 1, 50), refetchInterval: 60_000 })
  const projectIds: string[] = agg && projectsQ.data?.items ? projectsQ.data.items.map((p: any) => p.id) : [projectId]
  const singleProjectQ = useQuery({ queryKey: ["project", projectId], queryFn: () => Projects.get(projectId), enabled: !!projectId })
  const myRole = (singleProjectQ.data as any)?.my_role || role
  useEffect(() => {
    if (role === "Viewer" && tab === "cost") setTab("overview")
    if (!["Admin","Architect","Developer","PM","QA"].includes(myRole) && tab === "inbox") setTab("overview")
    if (!["Admin","Architect","Developer","QA"].includes(myRole) && tab === "specs") setTab("overview")
    if (!["Admin","Architect","Developer","QA"].includes(myRole) && tab === "tasks") setTab("overview")
    if (!can("task.read", myRole) && (tab === "pipeline" || tab === "cost")) setTab("overview")
  }, [role, myRole, tab])

  const sseUrl = can("task.read", myRole) ? Projects.eventsURL(projectId) : null
  useEventSource(sseUrl, () => {
    // invalidate handled by sidebar; workbench relies on refetchInterval
  })

  // helper: single vs agg total
  const inboxNewQ = useQuery({
    queryKey: ["wb", "inbox-new", agg ? "ALL" : projectId],
    queryFn: async () => {
      if (agg) {
        const ids = projectIds.length ? projectIds : [projectId]
        const rs = await Promise.allSettled(ids.map((pid) => Inbox.list(pid, "NEW", 1, 1, { silent: true }).catch(() => ({ total: 0 } as any))))
        return rs.reduce((a, r) => a + (r.status === "fulfilled" ? ((r.value as any).total ?? 0) : 0), 0)
      }
      const r: any = await Inbox.list(projectId, "NEW", 1, 1, { silent: true }).catch(() => ({ total: 0 }))
      return r.total ?? 0
    },
    refetchInterval: 60_000,
    enabled: can("inbox.read", myRole),
  })
  const inboxDraftedQ = useQuery({
    queryKey: ["wb", "inbox-drafted", agg ? "ALL" : projectId],
    queryFn: async () => {
      if (agg) {
        const ids = projectIds.length ? projectIds : [projectId]
        const rs = await Promise.allSettled(ids.map((pid) => Inbox.list(pid, "DRAFTED", 1, 1, { silent: true }).catch(() => ({ total: 0 } as any))))
        return rs.reduce((a, r) => a + (r.status === "fulfilled" ? ((r.value as any).total ?? 0) : 0), 0)
      }
      const r: any = await Inbox.list(projectId, "DRAFTED", 1, 1, { silent: true }).catch(() => ({ total: 0 }))
      return r.total ?? 0
    },
    refetchInterval: 60_000,
    enabled: can("inbox.read", myRole),
  })
  const inboxRecentQ = useQuery({
    queryKey: ["wb", "inbox-recent", projectId],
    queryFn: () => Inbox.list(projectId, undefined, 1, 5, { silent: true }).catch(() => ({ items: [] } as any)),
    enabled: can("inbox.read", myRole),
  })

  const specsQ = useQuery({
    queryKey: ["wb", "specs", agg ? "ALL" : projectId],
    queryFn: async () => {
      if (agg) {
        const ids = projectIds.length ? projectIds : [projectId]
        const rs = await Promise.allSettled(ids.map((pid) => Specs.list(undefined, pid, 1, 1).catch(() => ({ total: 0 } as any))))
        return rs.reduce((a, r) => a + (r.status === "fulfilled" ? ((r.value as any).total ?? 0) : 0), 0)
      }
      const r: any = await Specs.list(undefined, projectId, 1, 1).catch(() => ({ total: 0 }))
      return r.total ?? 0
    },
  })
  const specsListQ = useQuery({
    queryKey: ["wb", "specs-list", projectId],
    queryFn: () => Specs.list(undefined, projectId, 1, 5).catch(() => ({ items: [] } as any)),
    enabled: can("spec.read", myRole),
  })

  const tasksPendingQ = useQuery({
    queryKey: ["wb", "tasks-pending", agg ? "ALL" : projectId],
    queryFn: async () => {
      if (agg) {
        const ids = projectIds.length ? projectIds : [projectId]
        const rs = await Promise.allSettled(ids.map((pid) => Tasks.list(pid, "PENDING", 1, 1).catch(() => ({ total: 0 } as any))))
        return rs.reduce((a, r) => a + (r.status === "fulfilled" ? ((r.value as any).total ?? 0) : 0), 0)
      }
      const r: any = await Tasks.list(projectId, "PENDING", 1, 1).catch(() => ({ total: 0 }))
      return r.total ?? 0
    },
    refetchInterval: 30_000,
  })
  const tasksRunningQ = useQuery({
    queryKey: ["wb", "tasks-running", agg ? "ALL" : projectId],
    queryFn: async () => {
      if (agg) {
        const ids = projectIds.length ? projectIds : [projectId]
        const rs = await Promise.allSettled(ids.map((pid) => Tasks.list(pid, "RUNNING", 1, 1).catch(() => ({ total: 0 } as any))))
        return rs.reduce((a, r) => a + (r.status === "fulfilled" ? ((r.value as any).total ?? 0) : 0), 0)
      }
      const r: any = await Tasks.list(projectId, "RUNNING", 1, 1).catch(() => ({ total: 0 }))
      return r.total ?? 0
    },
    refetchInterval: 15_000,
  })
  const tasksReleasePendingQ = useQuery({
    queryKey: ["wb", "tasks-release-pending", agg ? "ALL" : projectId],
    queryFn: async () => {
      if (agg) {
        const ids = projectIds.length ? projectIds : [projectId]
        const rs = await Promise.allSettled(ids.map((pid) => Tasks.list(pid, "RELEASE_PENDING", 1, 1).catch(() => ({ total: 0 } as any))))
        return rs.reduce((a, r) => a + (r.status === "fulfilled" ? ((r.value as any).total ?? 0) : 0), 0)
      }
      const r: any = await Tasks.list(projectId, "RELEASE_PENDING", 1, 1).catch(() => ({ total: 0 }))
      return r.total ?? 0
    },
    refetchInterval: 30_000,
  })
  const tasksFailedQ = useQuery({
    queryKey: ["wb", "tasks-failed", agg ? "ALL" : projectId],
    queryFn: async () => {
      if (agg) {
        const ids = projectIds.length ? projectIds : [projectId]
        const rs = await Promise.allSettled(ids.map((pid) => Tasks.list(pid, "FAILED", 1, 1).catch(() => ({ total: 0 } as any))))
        const rs2 = await Promise.allSettled(ids.map((pid) => Tasks.list(pid, "ESCALATED", 1, 1).catch(() => ({ total: 0 } as any))))
        return rs.reduce((a, r) => a + (r.status === "fulfilled" ? ((r.value as any).total ?? 0) : 0), 0) + rs2.reduce((a, r) => a + (r.status === "fulfilled" ? ((r.value as any).total ?? 0) : 0), 0)
      }
      const a: any = await Tasks.list(projectId, "FAILED", 1, 1).catch(() => ({ total: 0 }))
      const b: any = await Tasks.list(projectId, "ESCALATED", 1, 1).catch(() => ({ total: 0 }))
      return (a.total ?? 0) + (b.total ?? 0)
    },
    refetchInterval: 30_000,
  })
  const tasksRecentQ = useQuery({
    queryKey: ["wb", "tasks-recent", projectId],
    queryFn: () => Tasks.list(projectId, undefined, 1, 5).catch(() => ({ items: [] } as any)),
    enabled: can("task.read", myRole),
  })

  const iterationsQ = useQuery({
    queryKey: ["wb", "iterations", agg ? "ALL" : projectId],
    queryFn: async () => {
      if (agg) {
        const ids = projectIds.length ? projectIds : [projectId]
        const rs = await Promise.allSettled(ids.map((pid) => Iterations.list(pid).catch(() => ({ items: [] } as any))))
        const all: any[] = []
        rs.forEach((r) => { if (r.status === "fulfilled") all.push(...((r.value as any).items || [])) })
        return { items: all.slice(0, 20) }
      }
      return Iterations.list(projectId).catch(() => ({ items: [] } as any))
    },
    enabled: !!projectId,
  })
  const pipelinesSummaryQ = useQuery({
    queryKey: ["wb", "pipelines-summary", agg ? "ALL" : projectId],
    queryFn: () => Pipelines.summary(agg ? undefined : projectId).catch(() => ({ running: 0, failed_today: 0, pending_builds: 0, deployed: null } as any)),
    refetchInterval: 30_000,
  })
  const pipelinesMetricsQ = useQuery({
    queryKey: ["wb", "pipelines-metrics", agg ? "ALL" : projectId],
    queryFn: () => Pipelines.metrics({ projectId: agg ? undefined : projectId, days: 14 }).catch(() => ({ items: [] } as any)),
    refetchInterval: 60_000,
  })
  const pipelinesRecentQ = useQuery({
    queryKey: ["wb", "pipelines-recent", agg ? "ALL" : projectId],
    queryFn: () => Pipelines.list({ projectId: agg ? undefined : projectId, page: 1, pageSize: 5 }).catch(() => ({ items: [] } as any)),
    enabled: can("task.read", myRole),
  })
  const costQ = useQuery({
    queryKey: ["wb", "cost", agg ? "ALL" : projectId],
    queryFn: async () => {
      if (agg) {
        const ids = projectIds.length ? projectIds : [projectId]
        const rs = await Promise.allSettled(ids.map((pid) => Cost.get(pid).catch(() => ({ summary: { total_tasks: 0, total_executions: 0, tokens_in: 0, tokens_out: 0, cost_cents: 0 }, breakdown: [] } as any))))
        let total_tasks = 0, total_executions = 0, tokens_in = 0, tokens_out = 0, cost_cents = 0
        const breakdown: any[] = []
        rs.forEach((r) => {
          if (r.status === "fulfilled") {
            const s = (r.value as any).summary || {}
            total_tasks += s.total_tasks ?? 0
            total_executions += s.total_executions ?? 0
            tokens_in += s.tokens_in ?? 0
            tokens_out += s.tokens_out ?? 0
            cost_cents += s.cost_cents ?? 0
            breakdown.push(...((r.value as any).breakdown || []))
          }
        })
        return { summary: { total_tasks, total_executions, tokens_in, tokens_out, cost_cents }, breakdown }
      }
      return Cost.get(projectId).catch(() => ({ summary: { total_tasks: 0, total_executions: 0, tokens_in: 0, tokens_out: 0, cost_cents: 0 }, breakdown: [] } as any))
    },
    enabled: can("task.read", myRole),
  })
  const releasesQ = useQuery({
    queryKey: ["wb", "releases", agg ? "ALL" : projectId],
    queryFn: async () => {
      if (agg) {
        const ids = projectIds.length ? projectIds : [projectId]
        const rs = await Promise.allSettled(ids.map((pid) => Releases.list(pid).catch(() => ({ items: [] } as any))))
        const all: any[] = []
        rs.forEach((r) => { if (r.status === "fulfilled") all.push(...((r.value as any).items || [])) })
        all.sort((a, b) => new Date(b.released_at || b.created_at || 0).getTime() - new Date(a.released_at || a.created_at || 0).getTime())
        return { items: all.slice(0, 20) }
      }
      return Releases.list(projectId).catch(() => ({ items: [] } as any))
    },
    enabled: can("task.read", myRole),
  })
  const auditQ = useQuery({
    queryKey: ["wb", "audit", agg ? "ALL" : projectId],
    queryFn: async () => {
      if (agg) {
        const ids = projectIds.length ? projectIds : [projectId]
        const rs = await Promise.allSettled(ids.map((pid) => Audit.list(pid, 1, 5).catch(() => ({ items: [] } as any))))
        const all: any[] = []
        rs.forEach((r) => { if (r.status === "fulfilled") all.push(...((r.value as any).items || [])) })
        all.sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime())
        return { items: all.slice(0, 10) }
      }
      return Audit.list(projectId, 1, 10).catch(() => ({ items: [] } as any))
    },
  })
  const healthQ = useQuery({
    queryKey: ["wb", "health"],
    queryFn: async () => {
      const h: any = await api("/health", {}, { silent: true }).catch(() => ({ status: "unknown" }))
      const r: any = await api("/ready", {}, { silent: true }).catch(() => ({ checks: {} }))
      return { health: h, ready: r }
    },
    refetchInterval: 60_000,
    enabled: role === "Admin",
  })

  const loading = projectsQ.isLoading || tasksPendingQ.isLoading || pipelinesSummaryQ.isLoading
  const showLoading = useMinLoading(loading, 400)

  return (
    <div className="space-y-4">
      <Card>
        <CardHeader className="flex-row items-center gap-3">
          <div className="flex items-center gap-2">
            <LayoutDashboard className="h-5 w-5" />
            <CardTitle>工作台</CardTitle>
            <Badge variant="outline" className="text-[10px]">{role}</Badge>
            {myRole && myRole !== role && <Badge variant="secondary" className="text-[10px]">项目内 {myRole}</Badge>}
          </div>
          <div className="ml-auto flex items-center gap-3">
            <div className="flex items-center gap-2">
              <Switch id="agg" checked={agg} onCheckedChange={setAgg} />
              <Label htmlFor="agg" className="text-xs">聚合所有项目</Label>
            </div>
            <Badge variant="outline" className="text-[10px] max-w-[180px] truncate">{(singleProjectQ.data as any)?.name || projectId.slice(0, 8)}</Badge>
            <Button variant="ghost" size="icon" className="h-8 w-8" onClick={() => { projectsQ.refetch(); pipelinesSummaryQ.refetch(); tasksRunningQ.refetch() }}><RefreshCw className="h-4 w-4" /></Button>
          </div>
        </CardHeader>
        <CardContent className="text-xs text-muted-foreground">
          {role === "PM" && "PM 聚合视角：原料堆积与 BS 产出一览，支持一键转 BS"}
          {role === "Architect" && "Architect 视角：门禁待审、待发布队列与迭代分支健康"}
          {role === "Developer" && "Developer 视角：待审批/进行中/失败任务与炉池容量"}
          {role === "QA" && "QA 视角：待测与回归，覆盖率与趋势"}
          {role === "Viewer" && "Viewer 只读视角：总览与追溯，无写操作"}
          {role === "Admin" && "Admin 治理视角：平台健康、炉池、模型与部署"}
        </CardContent>
      </Card>

      <Tabs value={tab} onValueChange={setTab}>
        <TabsList>
          <TabsTrigger value="overview">总览</TabsTrigger>
          {["Admin","Architect","Developer","PM","QA"].includes(myRole) && <TabsTrigger value="inbox">原料</TabsTrigger>}
          {["Admin","Architect","Developer","QA"].includes(myRole) && <TabsTrigger value="specs">图纸</TabsTrigger>}
          {["Admin","Architect","Developer","QA"].includes(myRole) && <TabsTrigger value="tasks">任务</TabsTrigger>}
          {can("task.read", myRole) && <TabsTrigger value="pipeline">流水线</TabsTrigger>}
          {can("task.read", myRole) && role !== "Viewer" && <TabsTrigger value="cost">成本/发布</TabsTrigger>}
        </TabsList>

        <TabsContent value="overview" className="space-y-4 mt-4">
          <div className="relative">
            {showLoading && <div className="absolute inset-0 bg-background/60 backdrop-blur-sm z-10 flex items-center justify-center"><Loader2 className="h-5 w-5 animate-spin" /></div>}
            <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
              <StatCard title={agg ? "待我审批(聚合)" : "待我审批"} value={tasksPendingQ.data ?? 0} sub={can("task.approve", myRole) ? "可 approve" : "只读"} icon={Kanban} />
              <StatCard title="进行中" value={tasksRunningQ.data ?? 0} sub={`${pipelinesSummaryQ.data?.running ?? 0} 流水运行中`} icon={Flame} />
              <StatCard title="待发布" value={tasksReleasePendingQ.data ?? 0} sub="RELEASE_PENDING" icon={GitMerge} />
              <StatCard title="失败/升级" value={tasksFailedQ.data ?? 0} sub="FAILED+ESCALATED" icon={ShieldCheck} />
            </div>
          </div>

          <div className={`grid gap-3 ${role === "Viewer" ? "grid-cols-2 md:grid-cols-3" : "grid-cols-2 md:grid-cols-4"}`}>
            <StatCard title="Inbox NEW" value={inboxNewQ.data ?? 0} sub={`DRAFTED ${inboxDraftedQ.data ?? 0}`} icon={InboxIcon} />
            <StatCard title="图纸" value={specsQ.data ?? 0} icon={FileText} />
            <StatCard title="迭代" value={(iterationsQ.data as any)?.items?.length ?? 0} sub={`${((iterationsQ.data as any)?.items || []).filter((i: any) => i.status === "active").length} 活跃`} icon={GanttChart} />
            {role !== "Viewer" && <StatCard title="成本" value={`¥${(((costQ.data as any)?.summary?.cost_cents ?? 0) / 100).toFixed(2)}`} sub={`${(costQ.data as any)?.summary?.total_tasks ?? 0} 任务`} icon={Wallet} />}
          </div>

          {role === "Admin" && (
            <Card>
              <CardHeader className="flex-row items-center"><CardTitle className="text-sm">平台健康</CardTitle><Badge variant={healthQ.data?.health?.status === "ok" ? "success" : "secondary"} className="ml-2 text-[10px]">{healthQ.data?.health?.status || "…"}</Badge><Badge variant="outline" className="ml-auto text-[10px]">/ready 4探针 60s</Badge></CardHeader>
              <CardContent>
                <div className="grid grid-cols-2 md:grid-cols-4 gap-3 text-xs">
                  {["db", "redis", "gitea", "opencode"].map((k) => {
                    const v = (healthQ.data?.ready as any)?.checks?.[k]
                    const ok = typeof v === "string" ? !v.includes("disabled") && !v.includes("mock") : v === "up" || v?.includes?.("up")
                    const label = typeof v === "string" ? v : (v || "—")
                    return (
                      <div key={k} className="border rounded p-2">
                        <div className="flex items-center justify-between"><span className="font-medium">{k}</span><Badge variant={ok ? "success" : "secondary"} className="text-[10px]">{ok ? "healthy" : "—"}</Badge></div>
                        <div className="text-[10px] text-muted-foreground truncate mt-1">{label}</div>
                      </div>
                    )
                  })}
                </div>
                <div className="text-[10px] text-muted-foreground mt-2">embedding mock / opencode disabled 为预期次级态，非 red</div>
              </CardContent>
            </Card>
          )}

          <div className="grid lg:grid-cols-2 gap-3">
            <Card>
              <CardHeader className="flex-row items-center"><CardTitle className="text-sm">最近动态</CardTitle><Button variant="ghost" size="sm" className="ml-auto h-7 text-xs" onClick={() => navigate({ to: "/projects/$projectId", params: { projectId } })}>查看项目 <ArrowRight className="h-3 w-3 ml-1" /></Button></CardHeader>
              <CardContent>
                {(auditQ.data as any)?.items?.length ? (
                  <div className="space-y-2">
                    {(auditQ.data as any).items.slice(0, 6).map((a: any) => (
                      <div key={a.id || a.created_at} className="flex items-center gap-2 text-xs border rounded p-2">
                        <Badge variant="outline" className="text-[10px]">{a.action}</Badge>
                        <span className="truncate flex-1">{a.detail || a.actor}</span>
                        <span className="text-[10px] text-muted-foreground shrink-0">{relativeTime(a.created_at)}</span>
                      </div>
                    ))}
                  </div>
                ) : (
                  <Empty><EmptyHeader><EmptyMedia><Search className="h-4 w-4" /></EmptyMedia><EmptyTitle className="text-sm">暂无动态</EmptyTitle><EmptyDescription>审计日志为空</EmptyDescription></EmptyHeader></Empty>
                )}
              </CardContent>
            </Card>
            <Card>
              <CardHeader className="flex-row items-center"><CardTitle className="text-sm">我的项目</CardTitle><Button variant="ghost" size="sm" className="ml-auto h-7 text-xs" onClick={() => navigate({ to: "/projects" })}>全部项目 <ArrowRight className="h-3 w-3 ml-1" /></Button></CardHeader>
              <CardContent>
                <div className="space-y-2">
                  {(projectsQ.data as any)?.items?.slice(0, 5).map((p: any) => (
                    <div key={p.id} className="flex items-center gap-2 text-xs border rounded p-2">
                      <FolderKanban className="h-3 w-3 shrink-0" />
                      <span className="truncate flex-1">{p.name}</span>
                      <Badge variant="outline" className="text-[10px]">{p.slug || p.id.slice(0, 6)}</Badge>
                      <Button variant="ghost" size="sm" className="h-6 text-[10px]" onClick={() => navigate({ to: "/projects/$projectId", params: { projectId: p.id } })}>详情</Button>
                    </div>
                  ))}
                  {!(projectsQ.data as any)?.items?.length && <div className="text-xs text-muted-foreground">暂无项目</div>}
                </div>
              </CardContent>
            </Card>
          </div>

          <Card>
            <CardHeader className="flex-row items-center"><CardTitle className="text-sm">迭代看板</CardTitle><CardDescription className="ml-2 text-xs">分支为集成对象</CardDescription><Button variant="outline" size="sm" className="ml-auto h-7 text-xs" onClick={() => navigate({ to: "/projects/$projectId", params: { projectId } })}>项目迭代</Button></CardHeader>
            <CardContent>
              {(iterationsQ.data as any)?.items?.length ? (
                <Table>
                  <TableHeader><TableRow><TableHead>名称</TableHead><TableHead>状态</TableHead><TableHead>分支</TableHead><TableHead className="text-right">周期</TableHead></TableRow></TableHeader>
                  <TableBody>
                    {(iterationsQ.data as any).items.slice(0, 5).map((it: any) => (
                      <TableRow key={it.id}><TableCell className="text-xs">{it.name}</TableCell><TableCell><Badge variant={it.status === "active" ? "success" : "secondary"} className="text-[10px]">{it.status}</Badge></TableCell><TableCell className="font-mono text-[10px] truncate max-w-[180px]">{it.branch || "—"}</TableCell><TableCell className="text-right text-xs">{it.start_date?.slice(0, 10)} ~ {it.end_date?.slice(0, 10) || "—"}</TableCell></TableRow>
                    ))}
                  </TableBody>
                </Table>
              ) : (
                <Empty><EmptyHeader><EmptyMedia><GanttChart className="h-4 w-4" /></EmptyMedia><EmptyTitle className="text-sm">暂无迭代</EmptyTitle></EmptyHeader></Empty>
              )}
            </CardContent>
          </Card>

          <Card>
            <CardHeader><CardTitle className="text-sm">快捷入口</CardTitle></CardHeader>
            <CardContent className="flex flex-wrap gap-2">
              {["Admin","Architect","Developer","PM","QA"].includes(myRole) && <Button variant="forge" size="sm" onClick={() => navigate({ to: "/inbox" })}><InboxIcon className="h-3 w-3 mr-1" /> 原料堆</Button>}
              {["Admin","Architect","Developer","QA"].includes(myRole) && <Button variant="outline" size="sm" onClick={() => navigate({ to: "/specs" })}><FileText className="h-3 w-3 mr-1" /> 图纸</Button>}
              {["Admin","Architect","Developer","QA"].includes(myRole) && <Button variant="outline" size="sm" onClick={() => navigate({ to: "/tasks" })}><Kanban className="h-3 w-3 mr-1" /> 任务看板</Button>}
              {["Admin","Architect","PM","QA"].includes(myRole) && <Button variant="outline" size="sm" onClick={() => navigate({ to: "/gantt" })}><GanttChart className="h-3 w-3 mr-1" /> 甘特</Button>}
              {can("task.read", myRole) && <Button variant="outline" size="sm" onClick={() => navigate({ to: "/pipelines" })}><GitMerge className="h-3 w-3 mr-1" /> 构建部署</Button>}
              <Button variant="outline" size="sm" onClick={() => navigate({ to: "/search" })}><Search className="h-3 w-3 mr-1" /> 知识库</Button>
              {(role === "Admin" || role === "Developer") && <Button variant="outline" size="sm" onClick={() => navigate({ to: "/chat" })}><MessageCircle className="h-3 w-3 mr-1" /> AI 对话</Button>}
              {["Admin","Architect","QA"].includes(myRole) && <Button variant="outline" size="sm" onClick={() => navigate({ to: "/metrics" })}><BarChart3 className="h-3 w-3 mr-1" /> 指标</Button>}
              {can("user.list") && <Button variant="outline" size="sm" onClick={() => navigate({ to: "/admin/furnaces" })}><Factory className="h-3 w-3 mr-1" /> 炉池</Button>}
              {can("user.list") && <Button variant="outline" size="sm" onClick={() => navigate({ to: "/admin/users" })}><Users className="h-3 w-3 mr-1" /> 用户</Button>}
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="inbox" className="space-y-3 mt-4">
          <Card>
            <CardHeader className="flex-row items-center"><CardTitle className="text-sm">原料</CardTitle><CardDescription className="ml-2 text-xs">NEW {inboxNewQ.data ?? 0} · DRAFTED {inboxDraftedQ.data ?? 0}</CardDescription><Button variant="forge" size="sm" className="ml-auto h-7" onClick={() => navigate({ to: "/inbox" })}>去原料堆 <ArrowRight className="h-3 w-3 ml-1" /></Button></CardHeader>
            <CardContent>
              {(inboxRecentQ.data as any)?.items?.length ? (
                <Table><TableHeader><TableRow><TableHead>摘要</TableHead><TableHead>状态</TableHead><TableHead>来源</TableHead><TableHead className="text-right">时间</TableHead></TableRow></TableHeader><TableBody>
                  {(inboxRecentQ.data as any).items.map((it: any) => (
                    <TableRow key={it.id}><TableCell className="text-xs max-w-[280px] truncate">{it.snippet || it.id.slice(0, 8)}</TableCell><TableCell><Badge variant="outline" className="text-[10px]">{it.status}</Badge></TableCell><TableCell className="text-xs">{it.source_type}</TableCell><TableCell className="text-right text-xs">{relativeTime(it.created_at)}</TableCell></TableRow>
                  ))}
                </TableBody></Table>
              ) : (
                <Empty><EmptyHeader><EmptyMedia><InboxIcon className="h-4 w-4" /></EmptyMedia><EmptyTitle className="text-sm">暂无原料</EmptyTitle><EmptyDescription>{can("inbox.write", myRole) ? "去原料堆入堆" : "只读，无入堆权限"}</EmptyDescription></EmptyHeader></Empty>
              )}
              {can("inbox.write", myRole) ? <div className="text-[10px] text-muted-foreground mt-2">PM 可选中转 BS，Viewer 只读</div> : null}
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="specs" className="space-y-3 mt-4">
          <Card>
            <CardHeader className="flex-row items-center"><CardTitle className="text-sm">图纸</CardTitle><CardDescription className="ml-2 text-xs">总数 {specsQ.data ?? 0}</CardDescription><Button variant="outline" size="sm" className="ml-auto h-7" onClick={() => navigate({ to: "/specs" })}>编辑器 <ArrowRight className="h-3 w-3 ml-1" /></Button></CardHeader>
            <CardContent>
              {(specsListQ.data as any)?.items?.length ? (
                <Table><TableHeader><TableRow><TableHead>标题</TableHead><TableHead>分层</TableHead><TableHead>状态</TableHead><TableHead className="text-right">版本</TableHead></TableRow></TableHeader><TableBody>
                  {(specsListQ.data as any).items.map((s: any) => (
                    <TableRow key={s.id}><TableCell className="text-xs max-w-[260px] truncate">{s.title}</TableCell><TableCell><Badge variant="outline" className="text-[10px]">{s.layer}</Badge></TableCell><TableCell><Badge variant={s.status === "VALIDATED" ? "success" : "secondary"} className="text-[10px]">{s.status}</Badge></TableCell><TableCell className="text-right text-xs">{s.current_version}</TableCell></TableRow>
                  ))}
                </TableBody></Table>
              ) : (
                <Empty><EmptyHeader><EmptyMedia><FileText className="h-4 w-4" /></EmptyMedia><EmptyTitle className="text-sm">暂无图纸</EmptyTitle></EmptyHeader></Empty>
              )}
              <div className="text-[10px] text-muted-foreground mt-2">门禁 validate 仅 Architect/Admin，QA 可写</div>
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="tasks" className="space-y-3 mt-4">
          <Card>
            <CardHeader className="flex-row items-center"><CardTitle className="text-sm">任务</CardTitle><CardDescription className="ml-2 text-xs">PENDING {tasksPendingQ.data ?? 0} · RUNNING {tasksRunningQ.data ?? 0} · 待发布 {tasksReleasePendingQ.data ?? 0} · 失败 {tasksFailedQ.data ?? 0}</CardDescription><Button variant="outline" size="sm" className="ml-auto h-7" onClick={() => navigate({ to: "/tasks" })}>看板 <ArrowRight className="h-3 w-3 ml-1" /></Button></CardHeader>
            <CardContent>
              {(tasksRecentQ.data as any)?.items?.length ? (
                <Table><TableHeader><TableRow><TableHead>标题</TableHead><TableHead>状态</TableHead><TableHead>分支</TableHead><TableHead className="text-right">更新</TableHead></TableRow></TableHeader><TableBody>
                  {(tasksRecentQ.data as any).items.map((t: any) => (
                    <TableRow key={t.id}><TableCell className="text-xs max-w-[240px] truncate">{t.title || t.id.slice(0, 8)}</TableCell><TableCell><Badge variant="outline" className="text-[10px]">{t.status}</Badge></TableCell><TableCell className="font-mono text-[10px] truncate max-w-[160px]">{t.branch || "—"}</TableCell><TableCell className="text-right text-xs">{relativeTime(t.updated_at || t.created_at)}</TableCell></TableRow>
                  ))}
                </TableBody></Table>
              ) : (
                <Empty><EmptyHeader><EmptyMedia><Kanban className="h-4 w-4" /></EmptyMedia><EmptyTitle className="text-sm">暂无任务</EmptyTitle></EmptyHeader></Empty>
              )}
              <Separator className="my-3" />
              <div className="flex flex-wrap gap-2 text-xs">
                <Badge variant="outline">approve 需 task.approve</Badge><Badge variant="outline">release 需 task.release</Badge><Badge variant="outline">重跑需 task.run (QA可)</Badge>
              </div>
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="pipeline" className="space-y-3 mt-4">
          <Card>
            <CardHeader className="flex-row items-center"><CardTitle className="text-sm">流水线</CardTitle><CardDescription className="ml-2 text-xs">running {pipelinesSummaryQ.data?.running ?? 0} · failed_today {pipelinesSummaryQ.data?.failed_today ?? 0} · pending {pipelinesSummaryQ.data?.pending_builds ?? 0}</CardDescription><Button variant="outline" size="sm" className="ml-auto h-7" onClick={() => navigate({ to: "/pipelines" })}>构建部署 <ArrowRight className="h-3 w-3 ml-1" /></Button></CardHeader>
            <CardContent>
              {(pipelinesRecentQ.data as any)?.items?.length ? (
                <Table><TableHeader><TableRow><TableHead>仓库</TableHead><TableHead>分支</TableHead><TableHead>状态</TableHead><TableHead className="text-right">时间</TableHead></TableRow></TableHeader><TableBody>
                  {(pipelinesRecentQ.data as any).items.map((r: any) => (
                    <TableRow key={r.id}><TableCell className="text-xs truncate max-w-[160px]">{r.repo}</TableCell><TableCell className="font-mono text-[10px]">{r.branch || "—"}</TableCell><TableCell><Badge variant={r.status === "SUCCESS" ? "success" : r.status === "FAILURE" ? "destructive" : "secondary"} className="text-[10px]">{r.status}</Badge></TableCell><TableCell className="text-right text-xs">{relativeTime(r.created_at)}</TableCell></TableRow>
                  ))}
                </TableBody></Table>
              ) : (
                <Empty><EmptyHeader><EmptyMedia><GitMerge className="h-4 w-4" /></EmptyMedia><EmptyTitle className="text-sm">近 14 天暂无记录</EmptyTitle><EmptyDescription>push 后出现</EmptyDescription></EmptyHeader></Empty>
              )}
              {pipelinesSummaryQ.data?.deployed && <div className="text-xs mt-2">已线上 <Badge variant="outline" className="text-[10px] font-mono">{(pipelinesSummaryQ.data.deployed as any).tag}</Badge> {(pipelinesSummaryQ.data.deployed as any).by} {relativeTime((pipelinesSummaryQ.data.deployed as any).at)}</div>}
              <div className="h-48 mt-3">
                <ResponsiveContainer width="100%" height="100%">
                  <ComposedChart data={(pipelinesMetricsQ.data as any)?.items || []}>
                    <XAxis dataKey="day" tick={{ fontSize: 10 }} tickFormatter={(v: string) => v.slice(5)} />
                    <YAxis yAxisId="left" tick={{ fontSize: 10 }} domain={[0, 100]} />
                    <YAxis yAxisId="right" orientation="right" tick={{ fontSize: 10 }} />
                    <Tooltip />
                    <Legend />
                    <Bar yAxisId="right" dataKey="total" name="总数" fill="#90CAF9" />
                    <Line yAxisId="left" type="monotone" dataKey="success_rate" name="成功率%" stroke="#2E7D32" dot={false} connectNulls />
                  </ComposedChart>
                </ResponsiveContainer>
              </div>
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="cost" className="space-y-3 mt-4">
          <div className="grid lg:grid-cols-2 gap-3">
            <Card>
              <CardHeader><CardTitle className="text-sm">成本</CardTitle><CardDescription>¥{(((costQ.data as any)?.summary?.cost_cents ?? 0) / 100).toFixed(2)} · {(costQ.data as any)?.summary?.total_tasks ?? 0} 任务 · {(costQ.data as any)?.summary?.total_executions ?? 0} 执行</CardDescription></CardHeader>
              <CardContent>
                {(costQ.data as any)?.breakdown?.length ? (
                  <Table><TableHeader><TableRow><TableHead>分组</TableHead><TableHead>执行</TableHead><TableHead className="text-right">成本</TableHead></TableRow></TableHeader><TableBody>
                    {(costQ.data as any).breakdown.slice(0, 5).map((b: any) => (
                      <TableRow key={b.key || b.group}><TableCell className="text-xs truncate max-w-[160px]">{b.key || b.group || b.furnace_id || "—"}</TableCell><TableCell className="text-xs">{b.executions ?? b.count ?? 0}</TableCell><TableCell className="text-right text-xs">¥{((b.cost_cents ?? 0) / 100).toFixed(2)}</TableCell></TableRow>
                    ))}
                  </TableBody></Table>
                ) : (
                  <div className="text-xs text-muted-foreground">暂无成本数据（Agent usage 回写后出现）</div>
                )}
              </CardContent>
            </Card>
            <Card>
              <CardHeader className="flex-row items-center"><CardTitle className="text-sm">发布</CardTitle><Button variant="ghost" size="sm" className="ml-auto h-7 text-xs" onClick={() => navigate({ to: "/pipelines" })}>部署记录</Button></CardHeader>
              <CardContent>
                {(releasesQ.data as any)?.items?.length ? (
                  <Table><TableHeader><TableRow><TableHead>标题</TableHead><TableHead>Tag</TableHead><TableHead className="text-right">时间</TableHead></TableRow></TableHeader><TableBody>
                    {(releasesQ.data as any).items.slice(0, 5).map((r: any) => (
                      <TableRow key={r.id}><TableCell className="text-xs truncate max-w-[180px]">{r.title || r.spec_title || r.id.slice(0, 8)}</TableCell><TableCell className="font-mono text-[10px]">{r.tag}</TableCell><TableCell className="text-right text-xs">{r.released_at ? relativeTime(r.released_at) : "—"}</TableCell></TableRow>
                    ))}
                  </TableBody></Table>
                ) : (
                  <div className="text-xs text-muted-foreground">暂无发布（释放后 forja/* 自动聚合）</div>
                )}
              </CardContent>
            </Card>
          </div>
        </TabsContent>
      </Tabs>
    </div>
  )
}
