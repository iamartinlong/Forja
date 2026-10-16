import { useEffect, useState } from "react"
import { useForm } from "react-hook-form"
import { zodResolver } from "@hookform/resolvers/zod"
import { z } from "zod"
import { useNavigate, useParams } from "@tanstack/react-router"
import { toast } from "sonner"
import { Projects, Users, Audit, Specs, Tasks, Iterations, Cost, Releases, Files } from "@/lib/api"
import { can } from "@/lib/auth"
import { PageLoader } from "@/components/ui/page-loader"
import { useMinLoading } from "@/hooks/useMinLoading"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Badge } from "@/components/ui/badge"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from "@/components/ui/dialog"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Empty, EmptyHeader, EmptyMedia, EmptyTitle, EmptyDescription } from "@/components/ui/empty"
import { ArrowLeft, Plus, Trash2, Archive, ArchiveRestore, Loader2, RefreshCw, FolderOpen, Download } from "lucide-react"
import { ConfirmDialog, type ConfirmState } from "@/components/shared/ConfirmDialog"

const inviteSchema = z.object({
  user_id: z.string().min(1, "请选择用户"),
  role: z.string().min(1),
})
type InviteForm = z.infer<typeof inviteSchema>

const settingsSchema = z.object({
  name: z.string().min(1, "项目名必填"),
  description: z.string(),
})
type SettingsForm = z.infer<typeof settingsSchema>

const iterationSchema = z.object({
  name: z.string().min(1, "迭代名必填").max(80, "最多80字"),
  start_date: z.string().optional(),
  end_date: z.string().optional(),
})
type IterationForm = z.infer<typeof iterationSchema>

const uatSchema = z.object({
  result: z.string().min(1, "请选择验收结论"),
  content: z.string().min(1, "反馈内容必填").max(32768, "内容过长（≤32KB）"),
  title: z.string().optional(),
  env: z.string().optional(),
  delivery_id: z.string().optional(),
})
type UatForm = z.infer<typeof uatSchema>

const fileSchema = z.object({
  category: z.string().min(1, "请选择分类"),
  summary: z.string().max(2000, "摘要 ≤2000 字").optional(),
})
type FileForm = z.infer<typeof fileSchema>

const FILE_CATEGORIES = [
  { v: "general", l: "通用" },
  { v: "brand", l: "品牌" },
  { v: "contract", l: "合同" },
  { v: "api", l: "接口" },
  { v: "design", l: "设计" },
  { v: "requirement", l: "需求" },
  { v: "other", l: "其他" },
]

function fileCategoryLabel(v: string): string {
  return FILE_CATEGORIES.find((c) => c.v === v)?.l || v || "-"
}

export default function ProjectDetail() {
  const navigate = useNavigate()
  const { projectId } = useParams({ strict: false }) as { projectId: string }
  const [project, setProject] = useState<any>(null)
  const [members, setMembers] = useState<any[]>([])
  const [stats, setStats] = useState<{ specs: number; tasks: number }>({ specs: 0, tasks: 0 })
  const [audits, setAudits] = useState<any[]>([])
  const [addOpen, setAddOpen] = useState(false)
  const [userOptions, setUserOptions] = useState<any[]>([])
  const [confirmName, setConfirmName] = useState("")
  const [confirmState, setConfirmState] = useState<ConfirmState | null>(null)
  const [iterations, setIterations] = useState<any[]>([])
  const [cost, setCost] = useState<any>(null)
  const [releases, setReleases] = useState<any[]>([])
  // v2.0 项目规范（AGENTS.md）
  const [agents, setAgents] = useState<{ exists: boolean; content: string; sha: string } | null>(null)
  const [agentsDraft, setAgentsDraft] = useState("")
  const [agentsErr, setAgentsErr] = useState("")
  const [agentsSaving, setAgentsSaving] = useState(false)
  // v2.0 M1.2 交付制品
  const [artifacts, setArtifacts] = useState<any[]>([])
  const [building, setBuilding] = useState(false)
  // v2.0 M1.3 交付包
  const [deliveries, setDeliveries] = useState<any[]>([])
  const [delivering, setDelivering] = useState(false)
  const [notes, setNotes] = useState<{ tag: string; notes: string; tasks: number } | null>(null)
  // v2.0 M3 验收反馈（UAT）
  const [uatOpen, setUatOpen] = useState(false)
  const [uatFile, setUatFile] = useState<File | null>(null)
  const uatForm = useForm<UatForm>({
    resolver: zodResolver(uatSchema),
    defaultValues: { result: "pass", content: "", title: "", env: "", delivery_id: "" },
  })
  // v2.0 M4b 资源库
  const [files, setFiles] = useState<any[]>([])
  const [filesLoading, setFilesLoading] = useState(false)
  const showFilesLoading = useMinLoading(filesLoading, 400)
  const [fileCategory, setFileCategory] = useState("")
  const [uploadOpen, setUploadOpen] = useState(false)
  const [uploadFile, setUploadFile] = useState<File | null>(null)
  const fileForm = useForm<FileForm>({
    resolver: zodResolver(fileSchema),
    defaultValues: { category: "general", summary: "" },
  })
  const [iterOpen, setIterOpen] = useState(false)
  const iterationForm = useForm<IterationForm>({ resolver: zodResolver(iterationSchema), defaultValues: { name: "", start_date: "", end_date: "" } })
  const inviteForm = useForm<InviteForm>({
    resolver: zodResolver(inviteSchema),
    defaultValues: { user_id: "", role: "Developer" },
  })
  const settingsForm = useForm<SettingsForm>({ resolver: zodResolver(settingsSchema) })
  const loadProject = async () => {
    try {
      const p: any = await Projects.get(projectId)
      setProject(p)
      settingsForm.reset({ name: p.name, description: p.description || "" })
    } catch (e: any) { toast.error(e.message) }
  }
  const loadMembers = async () => {
    try {
      const r: any = await Projects.members(projectId)
      setMembers(r.items || [])
    } catch (e: any) { toast.error(e.message) }
  }
  const loadAgents = async () => {
    try {
      const r: any = await Projects.agents(projectId)
      setAgents(r)
      setAgentsDraft(r.content || "")
      setAgentsErr("")
    } catch (e: any) { setAgentsErr(e.message) }
  }
  const saveAgents = async () => {
    if (!agents) return
    setAgentsSaving(true)
    try {
      const r: any = await Projects.updateAgents(projectId, {
        content: agentsDraft,
        sha: agents.exists ? agents.sha : undefined,
      })
      setAgents({ exists: true, content: agentsDraft, sha: r?.sha || "" })
      setAgentsErr("")
      toast.success("项目规范已保存（已提交到仓库）")
    } catch (e: any) {
      setAgentsErr(e.message)
    } finally { setAgentsSaving(false) }
  }
  const generateAgents = async () => {
    try {
      await Projects.generateAgents(projectId)
      toast.success("已生成初版项目规范")
      loadAgents()
    } catch (e: any) { setAgentsErr(e.message) }
  }
  const loadArtifacts = async () => {
    try {
      const r: any = await Projects.artifacts(projectId)
      setArtifacts(r.items || [])
    } catch { /* 静默：交付页为空态 */ }
  }
  const triggerBuild = async () => {
    setBuilding(true)
    try {
      const r: any = await Projects.builds(projectId)
      toast.success(`构建已排队：${String(r?.sha || "").slice(0, 8)}`)
      setTimeout(loadArtifacts, 5000)
    } catch (e: any) {
      toast.error(e.message)
    } finally { setBuilding(false) }
  }
  const loadDeliveries = async () => {
    try {
      const r: any = await Projects.deliveries(projectId)
      setDeliveries(r.items || [])
    } catch { /* 静默：交付页为空态 */ }
  }
  const generateDelivery = async () => {
    setDelivering(true)
    try {
      const r: any = await Projects.createDelivery(projectId, {})
      toast.success(`交付包已生成：${r?.tag || ""}`)
      loadDeliveries()
    } catch (e: any) {
      toast.error(e.message)
    } finally { setDelivering(false) }
  }
  const previewNotes = async (tag?: string) => {
    try {
      const r: any = await Projects.releaseNotes(projectId, tag)
      setNotes(r)
    } catch (e: any) { toast.error(e.message) }
  }
  const submitUAT = async (values: UatForm) => {
    try {
      await Projects.submitUAT(projectId, {
        result: values.result as "pass" | "partial" | "fail",
        content: values.content,
        title: values.title || undefined,
        env: values.env || undefined,
        delivery_id: values.delivery_id || undefined,
        file: uatFile || undefined,
      })
      toast.success("验收反馈已提交到 Inbox")
      setUatOpen(false)
      uatForm.reset()
      setUatFile(null)
    } catch (e: any) {
      toast.error(e.message)
    }
  }
  const loadFiles = async (category?: string) => {
    setFilesLoading(true)
    try {
      const r: any = await Files.list(projectId, category !== undefined ? category : fileCategory)
      setFiles(r.items || [])
    } catch { /* 静默：资源库为空态 */ }
    finally { setFilesLoading(false) }
  }
  const submitFile = async (values: FileForm) => {
    if (!uploadFile) return
    try {
      await Files.upload(projectId, { file: uploadFile, category: values.category, summary: values.summary })
      toast.success("已上传到资源库")
      setUploadOpen(false)
      setUploadFile(null)
      fileForm.reset()
      loadFiles()
    } catch (e: any) {
      toast.error(e.message)
    }
  }
  const removeFile = (f: any) => setConfirmState({
    title: `删除文件「${f.filename}」？`,
    description: "将同时删除存储对象，不可恢复。",
    danger: true,
    action: async () => {
      await Files.remove(projectId, f.id)
      toast.success("已删除")
      loadFiles()
    },
  })
  useEffect(() => {
    if (!projectId) return
    loadProject()
    loadMembers()
    loadAgents()
    loadArtifacts()
    loadDeliveries()
    loadFiles()
    Specs.list(undefined, projectId, 1, 1).then((r: any) => setStats(s => ({ ...s, specs: r?.total ?? r?.items?.length ?? 0 }))).catch(() => {})
    Tasks.list(projectId, undefined, 1, 1).then((r: any) => setStats(s => ({ ...s, tasks: r?.total ?? r?.items?.length ?? 0 }))).catch(() => {})
    Audit.list(projectId, 1, 10).then((r: any) => setAudits(r?.items || [])).catch(() => {})
    Iterations.list(projectId).then((r: any) => setIterations(r.items || r || [])).catch(() => {})
    Cost.get(projectId).then((r: any) => setCost(r)).catch(() => {})
    Releases.list(projectId).then((r: any) => setReleases(r.items || [])).catch(() => {})
  }, [projectId])

  const myRole = project?.my_role || ""
  const canEdit = can("project.update", myRole)
  const canDeliver = can("project.deliver", myRole)
  const canUAT = can("inbox.uat", myRole)
  const canWriteFiles = can("file.write", myRole)
  const canManageMembers = can("project.member.manage", myRole)

  const openAddMember = async () => {
    inviteForm.reset({ user_id: "", role: "Developer" })
    setAddOpen(true)
    try {
      const r: any = await Users.list()
      setUserOptions(r.items || [])
    } catch (e: any) { toast.error(e.message) }
  }

  const addMember = async (values: InviteForm) => {
    try {
      await Projects.addMember(projectId, values)
      toast.success("成员已添加")
      setAddOpen(false)
      loadMembers()
      loadProject()
    } catch (e: any) { toast.error(e.message) }
  }

  const changeRole = async (m: any, role: string) => {
    try {
      await Projects.updateMember(projectId, m.user_id, { role })
      toast.success(`${m.username} 角色已改为 ${role}`)
      loadMembers()
    } catch (e: any) { toast.error(e.message); loadMembers() }
  }

  const removeMember = (m: any) => {
    setConfirmState({
      title: `移除成员 ${m.username}`,
      description: "该成员将失去本项目访问权限。",
      confirmText: "移除",
      danger: true,
      action: async () => {
        try {
          await Projects.removeMember(projectId, m.user_id)
          toast.success(`已移除 ${m.username}`)
          loadMembers(); loadProject()
        } catch (e: any) { toast.error(e.message); throw e }
      },
    })
  }

  const saveSettings = async (values: SettingsForm) => {
    try {
      await Projects.update(projectId, values)
      toast.success("项目已更新")
      loadProject()
    } catch (e: any) { toast.error(e.message) }
  }

  // S1.4：删除 → 归档（软删除，可恢复）
  const archiveProject = () => {
    setConfirmState({
      title: `归档项目 ${project?.name}`,
      description: "归档后项目转为只读，可随时在项目列表「已归档」中恢复。",
      confirmText: "归档",
      danger: true,
      action: async () => {
        try {
          await Projects.archive(projectId)
          toast.success("项目已归档")
          navigate({ to: "/projects" })
        } catch (e: any) { toast.error(e.message); throw e }
      },
    })
  }

  const restoreProject = async () => {
    try {
      await Projects.restore(projectId)
      toast.success("项目已恢复")
      loadProject()
    } catch (e: any) { toast.error(e.message) }
  }

  const purgeProject = async () => {
    if (confirmName !== project?.name) { toast.error("请输入完整项目名以确认彻底删除"); return }
    try {
      await Projects.purge(projectId)
      toast.success("项目已彻底删除")
      navigate({ to: "/projects" })
    } catch (e: any) { toast.error(e.message) }
  }

  if (!project) return <PageLoader />

  return (
    <div className="space-y-4 w-full">
      <Card>
        <CardContent className="pt-4">
          <div className="flex items-start gap-3">
            <Button size="sm" variant="ghost" className="h-7 px-1.5 mt-0.5" onClick={() => navigate({ to: "/projects" })} title="返回列表"><ArrowLeft className="h-4 w-4" /></Button>
            <div className="min-w-0 flex-1">
              <div className="flex items-center gap-2 flex-wrap">
                <span className="text-muted-foreground text-xs">项目 /</span>
                <h2 className="text-lg font-semibold">{project.name}</h2>
                <Badge variant="outline" className="text-[10px]">{project.slug}</Badge>
                {project.archived && <Badge variant="secondary" className="text-[10px] bg-amber-500/15 text-amber-600 border-amber-500/30"><Archive className="h-3 w-3 mr-0.5" />已归档（只读）</Badge>}
                {myRole && <Badge variant="secondary" className="text-[10px]">我的角色: {myRole}</Badge>}
                {project.archived && canEdit && (
                  <Button size="sm" variant="outline" className="h-6 text-xs" onClick={restoreProject}><ArchiveRestore className="h-3 w-3" /> 恢复项目</Button>
                )}
              </div>
              <div className="text-xs text-muted-foreground mt-1">
                Owner: {members.find((m: any) => m.user_id === project.owner_id)?.username || "-"} · 成员 {project.member_count} · 创建于 {project.created_at ? new Date(project.created_at).toLocaleString("zh-CN", { hour12: false }) : "-"}
              </div>
              {project.description && <div className="text-xs text-muted-foreground mt-1">{project.description}</div>}
            </div>
          </div>
        </CardContent>
      </Card>

      <Tabs defaultValue="overview">
        <TabsList>
          <TabsTrigger value="overview">概览</TabsTrigger>
          <TabsTrigger value="members">成员</TabsTrigger>
          <TabsTrigger value="iterations">迭代</TabsTrigger>
          <TabsTrigger value="cost">成本</TabsTrigger>
          <TabsTrigger value="releases">发布</TabsTrigger>
          <TabsTrigger value="delivery">交付</TabsTrigger>
          <TabsTrigger value="files">资源库</TabsTrigger>
          <TabsTrigger value="agents">规范</TabsTrigger>
          <TabsTrigger value="settings">设置</TabsTrigger>
        </TabsList>

        <TabsContent value="overview" className="space-y-4">
          <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
            <Card><CardContent className="pt-4"><div className="text-xs text-muted-foreground">规格数</div><div className="text-2xl font-semibold">{stats.specs}</div></CardContent></Card>
            <Card><CardContent className="pt-4"><div className="text-xs text-muted-foreground">任务数</div><div className="text-2xl font-semibold">{stats.tasks}</div></CardContent></Card>
            <Card><CardContent className="pt-4"><div className="text-xs text-muted-foreground">成员数</div><div className="text-2xl font-semibold">{project.member_count}</div></CardContent></Card>
            <Card><CardContent className="pt-4"><div className="text-xs text-muted-foreground">Slug</div><div className="text-sm font-medium truncate pt-1">{project.slug}</div></CardContent></Card>
          </div>
          <Card>
            <CardHeader className="pb-2 flex-row items-center">
              <CardTitle className="text-sm">最近动态</CardTitle>
              <Button asChild size="sm" variant="ghost" className="ml-auto h-8 w-8 p-0" title="导出审计（CSV）">
                <a href={Audit.exportURL(projectId)} aria-label="导出审计"><Download className="h-4 w-4" /></a>
              </Button>
            </CardHeader>
            <CardContent>
              {audits.length ? audits.map((a: any, i: number) => (
                <div key={a.id || i} className="text-xs py-1 border-b last:border-b-0 border-border/50 flex gap-2">
                  <span className="text-muted-foreground shrink-0">{a.created_at ? new Date(a.created_at).toLocaleString("zh-CN", { hour12: false }) : ""}</span>
                  <Badge variant="outline" className="text-[10px] shrink-0">{a.action}</Badge>
                  <span>{a.actor}</span>
                  <span className="text-muted-foreground truncate">{a.detail}</span>
                </div>
              )) : <div className="text-xs text-muted-foreground">暂无动态</div>}
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="iterations">
          <Card>
            <CardHeader className="pb-2 flex-row items-center">
              <CardTitle className="text-sm">迭代 ({iterations.length})</CardTitle>
              <Button size="sm" variant="forge" className="ml-auto" onClick={() => { iterationForm.reset({ name: "", start_date: "", end_date: "" }); setIterOpen(true) }}><Plus className="h-4 w-4" /> 新建迭代</Button>
            </CardHeader>
            <CardContent>
              {iterations.length ? (
                <Table>
                  <TableHeader><TableRow><TableHead>名称</TableHead><TableHead>状态</TableHead><TableHead>分支</TableHead><TableHead>周期</TableHead><TableHead>任务/工时</TableHead><TableHead className="text-right">操作</TableHead></TableRow></TableHeader>
                  <TableBody>{iterations.map((it: any) => (
                    <TableRow key={it.id}>
                      <TableCell className="text-xs font-medium">{it.name}</TableCell>
                      <TableCell><Badge variant="outline" className="text-[10px]">{it.status}</Badge></TableCell>
                      <TableCell className="text-xs font-mono">{it.branch ? <Badge variant="secondary" className="text-[10px]">{it.branch}</Badge> : <span className="text-muted-foreground">—</span>}{it.base_sha && <span className="ml-1 text-[10px] text-muted-foreground" title={it.base_sha}>{it.base_sha.slice(0,7)}</span>}{it.merge_sha && <span className="ml-1 text-[10px] text-green-600" title={it.merge_sha}>→{it.merge_sha.slice(0,7)}</span>}</TableCell>
                      <TableCell className="text-xs">{it.start_date || "-"} ~ {it.end_date || "-"}</TableCell>
                      <TableCell className="text-xs"><div className="flex items-center gap-2"><span>{it.task_count} / {it.estimated_hours}h</span><div className="w-16 h-1.5 bg-muted rounded-full overflow-hidden"><div className={`h-full ${Number(it.estimated_hours) > 40 ? "bg-destructive" : "bg-primary"}`} style={{ width: `${Math.min(100, Number(it.estimated_hours) * 2.5)}%` }} /></div>{Number(it.estimated_hours) > 40 && <Badge variant="destructive" className="text-[10px]">超容</Badge>}</div></TableCell>
                      <TableCell className="text-right">
                        <div className="flex gap-1 justify-end">
                          {it.status === "planned" && <Button size="sm" variant="ghost" className="h-6 text-xs" onClick={async () => { try { await Iterations.patch(projectId, it.id, { status: "active" }); toast.success("已激活 " + (it.branch || "")); Iterations.list(projectId).then((r: any) => setIterations(r.items || r || [])) } catch (e: any) { toast.error(e.message) } }}>激活</Button>}
                          {it.status === "active" && <Button size="sm" variant="forge" className="h-6 text-xs" onClick={async () => { try { await Iterations.release(projectId, it.id); toast.success("已发布到 master"); Iterations.list(projectId).then((r: any) => setIterations(r.items || r || [])); Releases.list(projectId).then((r: any) => setReleases(r.items || [])) } catch (e: any) { toast.error(e.message) } }}>发布</Button>}
                          {it.status === "active" && <Button size="sm" variant="ghost" className="h-6 text-xs" onClick={async () => { try { await Iterations.patch(projectId, it.id, { status: "closed" }); toast.success("已关闭"); Iterations.list(projectId).then((r: any) => setIterations(r.items || r || [])) } catch (e: any) { toast.error(e.message) } }}>关闭</Button>}
                        </div>
                      </TableCell>
                    </TableRow>
                  ))}</TableBody>
                </Table>
              ) : <div className="text-xs text-muted-foreground">暂无迭代（过渡可用 [R1] 标签，正式迭代在此创建）</div>}
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="cost">
          <Card>
            <CardHeader className="pb-2 flex-row items-center"><CardTitle className="text-sm">成本/Token 报表</CardTitle><div className="ml-auto flex gap-2"><select className="h-7 rounded-md border bg-background px-2 text-xs" onChange={(e) => { const v=e.target.value; Cost.get(projectId, v?{group_by:"iteration",iteration_id:v}:{}).then((r:any)=>setCost(r)).catch(()=>{}); }}><option value="">按迭代</option>{iterations.map((it:any)=><option key={it.id} value={it.id}>{it.name}</option>)}</select><Button size="sm" variant="ghost" className="h-7" onClick={() => Cost.get(projectId).then((r:any)=>setCost(r))}>刷新</Button></div></CardHeader>
            <CardContent className="space-y-3">
              {cost ? (
                <>
                  <div className="grid grid-cols-3 gap-3 text-xs">
                    <div>任务 {cost.summary?.total_tasks ?? "-"} / 执行 {cost.summary?.total_executions ?? "-"}</div>
                    <div>Token in/out {cost.summary?.tokens_in ?? 0} / {cost.summary?.tokens_out ?? 0}</div>
                    <div>成本 ¥{((cost.summary?.cost_cents ?? 0) / 100).toFixed(2)}</div>
                  </div>
                  <Table>
                    <TableHeader><TableRow><TableHead>分组</TableHead><TableHead>执行</TableHead><TableHead>成本</TableHead></TableRow></TableHeader>
                    <TableBody>{(cost.breakdown || []).map((b: any, i: number) => (
                      <TableRow key={i}><TableCell className="text-xs">{b.key === "__backlog__" ? "未归迭代" : b.key}</TableCell><TableCell className="text-xs">{b.executions}</TableCell><TableCell className="text-xs">¥{(b.cost_cents / 100).toFixed(2)}</TableCell></TableRow>
                    ))}</TableBody>
                  </Table>
                </>
              ) : <div className="text-xs text-muted-foreground">暂无成本数据（占位 0，需 Agent usage 回写后有值）</div>}
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="releases">
          <Card>
            <CardHeader className="pb-2 flex-row items-center"><CardTitle className="text-sm">Release Notes（forja/* 聚合）</CardTitle><div className="ml-auto flex gap-2"><select className="h-7 rounded-md border bg-background px-2 text-xs" onChange={(e)=>{const v=e.target.value; Releases.list(projectId, v||undefined).then((r:any)=>setReleases(r.items||[]))}}><option value="">全部迭代</option>{iterations.map((it:any)=><option key={it.id} value={it.id}>{it.name}</option>)}</select></div></CardHeader>
            <CardContent>
              {releases.length ? (
                <Table>
                  <TableHeader><TableRow><TableHead>任务</TableHead><TableHead>Spec</TableHead><TableHead>Tag</TableHead><TableHead>时间</TableHead></TableRow></TableHeader>
                  <TableBody>{releases.map((r: any) => (
                    <TableRow key={r.id}><TableCell className="text-xs">{r.title}</TableCell><TableCell className="text-xs">{r.spec_title || "-"}</TableCell><TableCell className="text-xs font-mono">{r.tag}</TableCell><TableCell className="text-xs">{r.released_at ? new Date(r.released_at).toLocaleString("zh-CN", { hour12: false }) : "-"}</TableCell></TableRow>
                  ))}</TableBody>
                </Table>
              ) : <div className="text-xs text-muted-foreground">暂无发布（释放后 forja/&lt;short&gt;-&lt;sha&gt; 自动聚合）</div>}
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="members">
          <Card>
            <CardHeader className="pb-2 flex-row items-center">
              <CardTitle className="text-sm">成员 ({members.length})</CardTitle>
              {canManageMembers && <Button size="sm" variant="forge" className="ml-auto" onClick={openAddMember}><Plus className="h-4 w-4" /> 添加成员</Button>}
            </CardHeader>
            <CardContent>
              <Table>
                <TableHeader><TableRow><TableHead>用户</TableHead><TableHead>Email</TableHead><TableHead>角色</TableHead><TableHead>加入时间</TableHead>{canManageMembers && <TableHead className="text-right">操作</TableHead>}</TableRow></TableHeader>
                <TableBody>{members.map((m: any) => {
                  const isOwner = m.user_id === project.owner_id
                  return (
                    <TableRow key={m.user_id}>
                      <TableCell className="text-xs font-medium">{m.username}{isOwner && <Badge variant="secondary" className="ml-1.5 text-[10px]">Owner</Badge>}</TableCell>
                      <TableCell className="text-xs">{m.email || "-"}</TableCell>
                      <TableCell>
                        {canManageMembers && !isOwner ? (
                          <select value={m.role} onChange={e => changeRole(m, e.target.value)} className="border rounded-md h-7 px-1.5 text-xs bg-background">
                            <option>Admin</option><option>Architect</option><option>Developer</option><option>Viewer</option><option>PM</option><option>QA</option>
                          </select>
                        ) : <Badge variant="outline" className="text-[10px]">{m.role}</Badge>}
                      </TableCell>
                      <TableCell className="text-xs text-muted-foreground">{m.joined_at ? new Date(m.joined_at).toLocaleString("zh-CN", { hour12: false }) : "-"}</TableCell>
                      {canManageMembers && <TableCell className="text-right">
                        {!isOwner && <Button size="sm" variant="ghost" className="h-6 px-1.5 text-destructive hover:text-destructive" title="移除" onClick={() => removeMember(m)}><Trash2 className="h-3.5 w-3.5" /></Button>}
                      </TableCell>}
                    </TableRow>
                  )
                 })}</TableBody>
              </Table>
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="delivery">
          <Card>
            <CardHeader className="pb-2 flex-row items-center">
              <CardTitle className="text-sm">交付制品 ({artifacts.length})</CardTitle>
              <div className="ml-auto flex gap-2">
                {canEdit && (
                  <Button size="sm" variant="forge" className="h-7" disabled={building || !project?.repo_full_name} onClick={triggerBuild}>
                    {building && <Loader2 className="h-3.5 w-3.5 animate-spin" />}触发构建
                  </Button>
                )}
                <Button size="sm" variant="ghost" className="h-7" onClick={loadArtifacts}>刷新</Button>
              </div>
            </CardHeader>
            <CardContent>
              <div className="text-[11px] text-muted-foreground mb-2">
                构建在项目仓 CI 执行（镜像 <span className="font-mono">gitea:3000/{project?.repo_full_name || "<repo>"}:{project?.default_branch || "master"}-&lt;sha8&gt;</span>）；
                成功后制品自动登记，镜像 tar 由 CI 回传后可用于交付包。
              </div>
              {artifacts.length ? (
                <Table>
                  <TableHeader><TableRow><TableHead>Tag</TableHead><TableHead>镜像</TableHead><TableHead>SHA</TableHead><TableHead>状态</TableHead><TableHead>时间</TableHead></TableRow></TableHeader>
                  <TableBody>{artifacts.map((a: any) => (
                    <TableRow key={a.sha}>
                      <TableCell className="text-xs font-mono">{a.tag}</TableCell>
                      <TableCell className="text-xs font-mono max-w-[220px] truncate">{a.image}</TableCell>
                      <TableCell className="text-xs font-mono">{String(a.sha || "").slice(0, 8)}</TableCell>
                      <TableCell><Badge variant="outline" className="text-[10px]">{a.status}</Badge></TableCell>
                      <TableCell className="text-xs text-muted-foreground">{a.created_at ? new Date(a.created_at).toLocaleString("zh-CN", { hour12: false }) : "-"}</TableCell>
                    </TableRow>
                  ))}</TableBody>
                </Table>
              ) : <div className="text-xs text-muted-foreground">暂无制品（点击「触发构建」后等待 CI 完成回填）</div>}
            </CardContent>
          </Card>

          <Card className="mt-4">
            <CardHeader className="pb-2 flex-row items-center">
              <CardTitle className="text-sm">交付包 ({deliveries.length})</CardTitle>
              <div className="ml-auto flex gap-2">
                {canUAT && (
                  <Button size="sm" variant="outline" className="h-7" onClick={() => { uatForm.reset(); setUatFile(null); setUatOpen(true) }}>验收反馈</Button>
                )}
                {canDeliver && (
                  <Button size="sm" variant="forge" className="h-7" disabled={delivering || !artifacts.length} onClick={generateDelivery}>
                    {delivering && <Loader2 className="h-3.5 w-3.5 animate-spin" />}生成交付包
                  </Button>
                )}
                <Button size="sm" variant="ghost" className="h-7" onClick={() => { loadDeliveries(); loadArtifacts() }}>刷新</Button>
              </div>
            </CardHeader>
            <CardContent>
              <div className="text-[11px] text-muted-foreground mb-2">
                交付包含 delivery.json / deploy/compose.yml / README-DEPLOY.md / CHANGELOG.md / project.yaml；
                镜像 tar 由项目 CI 回传（best-effort），可单独下载。
              </div>
              {deliveries.length ? (
                <Table>
                  <TableHeader><TableRow><TableHead>Tag</TableHead><TableHead>SHA</TableHead><TableHead>状态</TableHead><TableHead>镜像 tar</TableHead><TableHead>时间</TableHead><TableHead className="text-right">操作</TableHead></TableRow></TableHeader>
                  <TableBody>{deliveries.map((d: any) => (
                    <TableRow key={d.id}>
                      <TableCell className="text-xs font-mono">{d.tag}</TableCell>
                      <TableCell className="text-xs font-mono">{String(d.sha || "").slice(0, 8)}</TableCell>
                      <TableCell><Badge variant={d.status === "ready" ? "outline" : "secondary"} className="text-[10px]">{d.status}</Badge></TableCell>
                      <TableCell className="text-xs">{d.manifest?.has_tar ? "已回传" : "未回传"}</TableCell>
                      <TableCell className="text-xs text-muted-foreground">{d.created_at ? new Date(d.created_at).toLocaleString("zh-CN", { hour12: false }) : "-"}</TableCell>
                      <TableCell className="text-right">
                        <div className="flex justify-end gap-1">
                          <Button size="sm" variant="ghost" className="h-6 px-1.5 text-xs" onClick={() => previewNotes(d.tag)}>Notes</Button>
                          {d.has_zip && (
                            <Button asChild size="sm" variant="outline" className="h-6 px-2 text-xs">
                              <a href={Projects.deliveryDownloadURL(projectId, d.id)}>交付包</a>
                            </Button>
                          )}
                          {d.manifest?.has_tar && (
                            <Button asChild size="sm" variant="outline" className="h-6 px-2 text-xs">
                              <a href={Projects.deliveryArtifactURL(projectId, d.id)}>镜像</a>
                            </Button>
                          )}
                        </div>
                      </TableCell>
                    </TableRow>
                  ))}</TableBody>
                </Table>
              ) : <div className="text-xs text-muted-foreground">暂无交付包（制品就绪后点击「生成交付包」）</div>}
              {notes && (
                <div className="mt-3">
                  <div className="flex items-center gap-2 mb-1">
                    <span className="text-xs font-medium">Release Notes · {notes.tag}（{notes.tasks} 个已发布任务）</span>
                    <Button size="sm" variant="ghost" className="h-6 px-1.5 ml-auto" onClick={() => setNotes(null)}>关闭</Button>
                  </div>
                  <pre className="max-h-64 overflow-auto rounded-md border bg-muted/30 p-3 text-[11px] leading-relaxed whitespace-pre-wrap">{notes.notes}</pre>
                </div>
              )}
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="files">
          <Card>
            <CardHeader className="pb-2 flex-row items-center">
              <CardTitle className="text-sm">资源库 ({files.length})</CardTitle>
              <div className="ml-auto flex gap-2">
                <select
                  value={fileCategory}
                  onChange={(e) => { setFileCategory(e.target.value); loadFiles(e.target.value) }}
                  className="border rounded-md h-8 px-2 text-xs bg-background"
                  aria-label="分类筛选"
                >
                  <option value="">全部分类</option>
                  {FILE_CATEGORIES.map((c) => <option key={c.v} value={c.v}>{c.l}</option>)}
                </select>
                {canWriteFiles && (
                  <Button size="sm" variant="forge" className="h-8" onClick={() => { fileForm.reset(); setUploadFile(null); setUploadOpen(true) }}>
                    <Plus className="h-4 w-4" /> 上传
                  </Button>
                )}
                <Button size="sm" variant="ghost" className="h-8 w-8 p-0" onClick={() => loadFiles()} disabled={showFilesLoading} title="刷新">
                  {showFilesLoading ? <Loader2 className="h-4 w-4 animate-spin" /> : <RefreshCw className="h-4 w-4" />}
                </Button>
              </div>
            </CardHeader>
            <CardContent className="relative">
              {showFilesLoading && (
                <div className="absolute inset-0 z-10 flex items-center justify-center rounded-lg bg-background/60 backdrop-blur-[1px]">
                  <span className="inline-flex items-center gap-2 rounded-full border bg-background px-3 py-1.5 text-xs text-muted-foreground shadow-sm">
                    <Loader2 className="h-3.5 w-3.5 animate-spin" /> 加载中…
                  </span>
                </div>
              )}
              <div className={showFilesLoading ? "opacity-60 pointer-events-none transition-opacity" : "transition-opacity"}>
                {files.length ? (
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>文件名</TableHead>
                        <TableHead>分类</TableHead>
                        <TableHead>摘要</TableHead>
                        <TableHead>大小</TableHead>
                        <TableHead>上传时间</TableHead>
                        <TableHead className="text-right">操作</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {files.map((f: any) => (
                        <TableRow key={f.id}>
                          <TableCell className="max-w-[260px] truncate text-xs" title={f.filename}>{f.filename}</TableCell>
                          <TableCell><Badge variant="outline" className="text-[10px]">{fileCategoryLabel(f.category)}</Badge></TableCell>
                          <TableCell className="max-w-[220px] truncate text-xs text-muted-foreground" title={f.summary || ""}>{f.summary || "-"}</TableCell>
                          <TableCell className="text-xs">{(f.size / 1024).toFixed(1)}KB</TableCell>
                          <TableCell className="text-xs text-muted-foreground">{f.created_at ? new Date(f.created_at).toLocaleString("zh-CN", { hour12: false }) : "-"}</TableCell>
                          <TableCell className="text-right">
                            <div className="flex justify-end gap-1">
                              <Button asChild size="sm" variant="outline" className="h-6 px-2 text-xs">
                                <a href={Files.downloadURL(projectId, f.id)}>下载</a>
                              </Button>
                              {canWriteFiles && (
                                <Button size="sm" variant="ghost" className="h-6 px-1.5 text-destructive hover:text-destructive" title="删除" onClick={() => removeFile(f)}>
                                  <Trash2 className="h-3.5 w-3.5" />
                                </Button>
                              )}
                            </div>
                          </TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                ) : (
                  <Empty>
                    <EmptyHeader>
                      <EmptyMedia><FolderOpen className="h-5 w-5" /></EmptyMedia>
                      <EmptyTitle>{fileCategory ? "该分类暂无文件" : "暂无资源文件"}</EmptyTitle>
                      <EmptyDescription>{fileCategory ? "切换分类或上传新文件" : "上传品牌/合同/接口/设计等资料，团队共享"}</EmptyDescription>
                    </EmptyHeader>
                  </Empty>
                )}
              </div>
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="agents">
          <Card>
            <CardHeader className="pb-2 flex-row items-center">
              <CardTitle className="text-sm">项目规范（AGENTS.md）</CardTitle>
              <div className="ml-auto flex gap-2 items-center">
                {agents?.exists
                  ? <Badge variant="outline" className="text-[10px] font-mono">{(agents?.sha || "").slice(0, 8) || "—"}</Badge>
                  : <Badge variant="secondary" className="text-[10px]">缺失</Badge>}
                {!agents?.exists && canEdit && (
                  <Button size="sm" variant="outline" className="h-7" onClick={generateAgents}>生成初版</Button>
                )}
                {canEdit && (
                  <Button size="sm" variant="forge" className="h-7" disabled={agentsSaving || !agents} onClick={saveAgents}>
                    {agentsSaving && <Loader2 className="h-3.5 w-3.5 animate-spin" />}保存
                  </Button>
                )}
              </div>
            </CardHeader>
            <CardContent className="space-y-2">
              <div className="text-[11px] text-muted-foreground">
                仓内 AGENTS.md 为唯一真相（Git 版本化）；锻造时自动注入 Agent，优先级高于全局规则。保存即提交到仓库默认分支。
              </div>
              <textarea
                value={agentsDraft}
                onChange={(e) => setAgentsDraft(e.target.value)}
                disabled={!canEdit || !agents}
                spellCheck={false}
                placeholder={"# 项目规范\n技术栈、编码约定、测试要求、禁止事项…"}
                className="w-full h-72 rounded-md border bg-background p-3 font-mono text-xs leading-relaxed"
              />
              {agentsErr && <div className="text-xs text-destructive break-all">{agentsErr}</div>}
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="settings" className="space-y-4">
          <Card>
            <CardHeader className="pb-2"><CardTitle className="text-sm">基本信息</CardTitle></CardHeader>
            <CardContent className="space-y-3">
              {!canEdit ? <div className="text-xs text-muted-foreground">仅项目管理员可编辑</div> : (
                <form onSubmit={settingsForm.handleSubmit(saveSettings)} className="space-y-3 max-w-md">
                  <div className="flex flex-col gap-2">
                    <label className="text-xs" htmlFor="p-name">项目名 *</label>
                    <Input id="p-name" {...settingsForm.register("name")} className="h-8" />
                    {settingsForm.formState.errors.name && <span className="text-xs text-destructive">{settingsForm.formState.errors.name.message}</span>}
                  </div>
                  <div className="flex flex-col gap-2">
                    <label className="text-xs" htmlFor="p-desc">描述</label>
                    <Input id="p-desc" {...settingsForm.register("description")} className="h-8" />
                  </div>
                  <Button size="sm" variant="forge" type="submit" disabled={settingsForm.formState.isSubmitting}>保存</Button>
                </form>
              )}
            </CardContent>
          </Card>
          {can("project.delete") && (
            <Card className="border-destructive/40">
              <CardHeader className="pb-2"><CardTitle className="text-sm text-destructive">危险区</CardTitle></CardHeader>
              <CardContent className="space-y-3">
                {!project.archived ? (
                  <>
                    <div className="text-xs text-muted-foreground">归档后项目转为只读（规格/任务/成员均不可修改），可在项目列表「已归档」中恢复。</div>
                    <Button size="sm" variant="destructive" onClick={archiveProject}><Archive className="h-4 w-4" /> 归档项目</Button>
                  </>
                ) : (
                  <>
                    <div className="text-xs text-muted-foreground">该项目已归档。彻底删除将级联清除规格（{stats.specs}）、任务（{stats.tasks}）、收件箱与审计记录，不可恢复。输入项目名 <span className="font-medium text-foreground">{project.name}</span> 以确认：</div>
                    <div className="flex gap-2 max-w-md">
                      <Input value={confirmName} onChange={e => setConfirmName(e.target.value)} placeholder={project.name} className="h-8" />
                      <Button size="sm" variant="destructive" disabled={confirmName !== project.name} onClick={purgeProject}><Trash2 className="h-4 w-4" /> 彻底删除</Button>
                    </div>
                  </>
                )}
              </CardContent>
            </Card>
          )}
        </TabsContent>
      </Tabs>

      <ConfirmDialog state={confirmState} onClose={() => setConfirmState(null)} />

      <Dialog open={iterOpen} onOpenChange={(o) => { setIterOpen(o); if (!o) iterationForm.reset() }}>
        <DialogContent>
          <DialogHeader><DialogTitle>新建迭代</DialogTitle><DialogDescription>按排期创建迭代，关闭后未完成任务退回 backlog</DialogDescription></DialogHeader>
          <form onSubmit={iterationForm.handleSubmit(async (v) => {
            try {
              await Iterations.create(projectId, { name: v.name, start_date: v.start_date || undefined, end_date: v.end_date || undefined })
              toast.success("迭代已创建")
              setIterOpen(false)
              Iterations.list(projectId).then((r: any) => setIterations(r.items || r || []))
            } catch (e: any) { toast.error(e.message) }
          })} className="space-y-3">
            <div className="flex flex-col gap-2">
              <label className="text-xs">迭代名 *</label>
              <Input {...iterationForm.register("name")} className="h-8" placeholder="R1" />
              <span className="text-xs text-destructive">{iterationForm.formState.errors.name?.message}</span>
            </div>
            <div className="flex gap-2">
              <div className="flex flex-col gap-2 flex-1"><label className="text-xs">开始</label><Input type="date" {...iterationForm.register("start_date")} className="h-8" /></div>
              <div className="flex flex-col gap-2 flex-1"><label className="text-xs">结束</label><Input type="date" {...iterationForm.register("end_date")} className="h-8" /></div>
            </div>
            <div className="flex justify-end gap-2 pt-2">
              <Button size="sm" variant="outline" type="button" onClick={() => setIterOpen(false)}>取消</Button>
              <Button size="sm" variant="forge" type="submit" disabled={iterationForm.formState.isSubmitting}>创建</Button>
            </div>
          </form>
        </DialogContent>
      </Dialog>

      <Dialog open={addOpen} onOpenChange={(o) => { setAddOpen(o); if (o) openAddMember() }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>添加成员 — {project.name}</DialogTitle>
            <DialogDescription>从用户列表选择，角色决定该项目内权限</DialogDescription>
          </DialogHeader>
          <form onSubmit={inviteForm.handleSubmit(addMember)} className="space-y-3">
            <div className="flex flex-col gap-2">
              <label className="text-xs" htmlFor="inv-user">用户 *</label>
              <select id="inv-user" {...inviteForm.register("user_id")} className="border rounded-md h-8 px-2 text-sm w-full bg-background">
                <option value="">请选择…</option>
                {userOptions.map((u: any) => <option key={u.id} value={u.id}>{u.username}{u.email ? ` (${u.email})` : ""}{!u.enabled ? " [已禁用]" : ""}</option>)}
              </select>
              {inviteForm.formState.errors.user_id && <span className="text-xs text-destructive">{inviteForm.formState.errors.user_id.message}</span>}
            </div>
            <div className="flex flex-col gap-2">
              <label className="text-xs" htmlFor="inv-role">角色</label>
              <select id="inv-role" {...inviteForm.register("role")} className="border rounded-md h-8 px-2 text-sm w-full bg-background">
                <option>Admin</option><option>Architect</option><option>Developer</option><option>Viewer</option><option>PM</option><option>QA</option>
              </select>
            </div>
            <div className="flex justify-end gap-2 pt-2">
              <Button size="sm" variant="outline" type="button" onClick={() => setAddOpen(false)}>取消</Button>
              <Button size="sm" variant="forge" type="submit" disabled={inviteForm.formState.isSubmitting}>添加</Button>
            </div>
          </form>
        </DialogContent>
      </Dialog>
      <Dialog open={uatOpen} onOpenChange={(o) => { setUatOpen(o); if (!o) { uatForm.reset(); setUatFile(null) } }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>验收反馈（UAT）</DialogTitle>
            <DialogDescription>提交后落入 Inbox（来源 UAT），由团队跟踪修复；可关联本次验收的交付包</DialogDescription>
          </DialogHeader>
          <form onSubmit={uatForm.handleSubmit(submitUAT)} className="space-y-3">
            <div className="flex flex-col gap-2">
              <label className="text-xs" htmlFor="uat-result">验收结论 *</label>
              <select id="uat-result" {...uatForm.register("result")} className="border rounded-md h-8 px-2 text-sm w-full bg-background">
                <option value="pass">通过</option>
                <option value="partial">部分通过</option>
                <option value="fail">不通过</option>
              </select>
              {uatForm.formState.errors.result && <span className="text-xs text-destructive">{uatForm.formState.errors.result.message}</span>}
            </div>
            <div className="flex flex-col gap-2">
              <label className="text-xs" htmlFor="uat-delivery">关联交付包</label>
              <select id="uat-delivery" {...uatForm.register("delivery_id")} className="border rounded-md h-8 px-2 text-sm w-full bg-background">
                <option value="">不关联</option>
                {deliveries.map((d: any) => (
                  <option key={d.id} value={d.id}>{d.tag}（{String(d.sha || "").slice(0, 8)}）</option>
                ))}
              </select>
            </div>
            <div className="flex flex-col gap-2">
              <label className="text-xs" htmlFor="uat-title">标题</label>
              <Input id="uat-title" {...uatForm.register("title")} className="h-8" placeholder="如：登录页导出按钮无响应" />
            </div>
            <div className="flex flex-col gap-2">
              <label className="text-xs" htmlFor="uat-content">反馈内容 *</label>
              <textarea
                id="uat-content"
                {...uatForm.register("content")}
                rows={5}
                placeholder="复现步骤 / 期望结果 / 实际结果…"
                className="w-full rounded-md border bg-background p-2 text-sm resize-y"
              />
              {uatForm.formState.errors.content && <span className="text-xs text-destructive">{uatForm.formState.errors.content.message}</span>}
            </div>
            <div className="flex flex-col gap-2">
              <label className="text-xs" htmlFor="uat-env">环境信息</label>
              <Input id="uat-env" {...uatForm.register("env")} className="h-8" placeholder="如：客户环境 Chrome 126 / 内网" />
            </div>
            <div className="flex flex-col gap-2">
              <label className="text-xs" htmlFor="uat-file">附件（可选，≤20MB）</label>
              <input
                id="uat-file"
                type="file"
                accept=".pdf,.docx,.doc,.xlsx,.xls,.txt,.md,.csv,.png,.jpg,.jpeg,.gif,.webp,.zip"
                onChange={(e) => setUatFile(e.target.files?.[0] || null)}
                className="text-xs"
              />
              {uatFile && <span className="text-xs text-muted-foreground">{uatFile.name}（{(uatFile.size / 1024).toFixed(1)}KB）<Button type="button" size="sm" variant="ghost" className="h-6 px-1.5 text-xs" onClick={() => setUatFile(null)}>清除</Button></span>}
            </div>
            <div className="flex justify-end gap-2 pt-2">
              <Button size="sm" variant="outline" type="button" onClick={() => setUatOpen(false)}>取消</Button>
              <Button size="sm" variant="forge" type="submit" disabled={uatForm.formState.isSubmitting}>提交反馈</Button>
            </div>
          </form>
        </DialogContent>
      </Dialog>
      <Dialog open={uploadOpen} onOpenChange={(o) => { setUploadOpen(o); if (!o) { fileForm.reset(); setUploadFile(null) } }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>上传到资源库</DialogTitle>
            <DialogDescription>支持文档/图片/压缩包 ≤20MB；分类用于团队检索与交付归档</DialogDescription>
          </DialogHeader>
          <form onSubmit={fileForm.handleSubmit(submitFile)} className="space-y-3">
            <div className="flex flex-col gap-2">
              <label className="text-xs" htmlFor="file-cat">分类 *</label>
              <select id="file-cat" {...fileForm.register("category")} className="border rounded-md h-8 px-2 text-sm w-full bg-background">
                {FILE_CATEGORIES.map((c) => <option key={c.v} value={c.v}>{c.l}</option>)}
              </select>
              {fileForm.formState.errors.category && <span className="text-xs text-destructive">{fileForm.formState.errors.category.message}</span>}
            </div>
            <div className="flex flex-col gap-2">
              <label className="text-xs" htmlFor="file-summary">摘要</label>
              <Input id="file-summary" {...fileForm.register("summary")} className="h-8" placeholder="可选，≤2000 字" />
              {fileForm.formState.errors.summary && <span className="text-xs text-destructive">{fileForm.formState.errors.summary.message}</span>}
            </div>
            <div className="flex flex-col gap-2">
              <label className="text-xs" htmlFor="file-pick">文件 *（≤20MB）</label>
              <input
                id="file-pick"
                type="file"
                accept=".pdf,.docx,.doc,.xlsx,.xls,.pptx,.ppt,.txt,.md,.csv,.png,.jpg,.jpeg,.gif,.webp,.zip,.rar,.7z,.json,.yaml,.yml"
                onChange={(e) => setUploadFile(e.target.files?.[0] || null)}
                className="text-xs"
              />
              {!uploadFile && fileForm.formState.isSubmitted && <span className="text-xs text-destructive">请选择文件</span>}
              {uploadFile && <span className="text-xs text-muted-foreground">{uploadFile.name}（{(uploadFile.size / 1024).toFixed(1)}KB）</span>}
            </div>
            <div className="flex justify-end gap-2 pt-2">
              <Button size="sm" variant="outline" type="button" onClick={() => setUploadOpen(false)}>取消</Button>
              <Button size="sm" variant="forge" type="submit" disabled={fileForm.formState.isSubmitting || !uploadFile}>上传</Button>
            </div>
          </form>
        </DialogContent>
      </Dialog>
    </div>
  )
}
