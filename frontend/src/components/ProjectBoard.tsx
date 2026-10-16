import { useEffect, useState } from "react"
import { useForm } from "react-hook-form"
import { zodResolver } from "@hookform/resolvers/zod"
import { z } from "zod"
import { useNavigate } from "@tanstack/react-router"
import { toast } from "sonner"
import { Projects } from "@/lib/api"
import { can } from "@/lib/auth"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Badge } from "@/components/ui/badge"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from "@/components/ui/dialog"
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Switch } from "@/components/ui/switch"
import { Plus, RefreshCw, Search, Pencil, Trash2, Eye, Archive, ArchiveRestore, Loader2, FolderKanban } from "lucide-react"
import { useMinLoading } from "@/hooks/useMinLoading"
import { Empty, EmptyHeader, EmptyMedia, EmptyTitle, EmptyDescription } from "@/components/ui/empty"
import { Pagination } from "@/components/ui/pagination"

const projectSchema = z.object({
  name: z.string().trim().min(1, "项目名必填"),
  description: z.string(),
})
type ProjectForm = z.infer<typeof projectSchema>

// v2.0 建项：模板（新建）或导入既有仓二选一
const createSchema = z
  .object({
    name: z.string().trim().min(1, "项目名必填"),
    description: z.string(),
    mode: z.enum(["create", "import"]),
    template: z.string(),
    repo_full_name: z.string(),
    mirror_url: z.string(),
  })
  .superRefine((v, ctx) => {
    if (v.mode === "import" && !v.repo_full_name.trim() && !v.mirror_url.trim()) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        path: ["repo_full_name"],
        message: "导入需填同实例仓 owner/repo，或外网镜像地址",
      })
    }
  })
type CreateForm = z.infer<typeof createSchema>

const createDefaults: CreateForm = {
  name: "",
  description: "",
  mode: "create",
  template: "standard-go-react",
  repo_full_name: "",
  mirror_url: "",
}

// 彻底删除确认（S1.4）：必须输入完整项目名，杜绝误触级联清空
const purgeSchema = z.object({
  confirmName: z.string().min(1, "请输入项目名以确认"),
})
type PurgeForm = z.infer<typeof purgeSchema>

export default function ProjectBoard() {
  const navigate = useNavigate()
  const [projects, setProjects] = useState<any[]>([])
  const [msg, setMsg] = useState("")
  const [q, setQ] = useState("")
  const [tab, setTab] = useState<"active" | "archived">("active")
  const [createOpen, setCreateOpen] = useState(false)
  const [editOpen, setEditOpen] = useState(false)
  const [editId, setEditId] = useState("")
  const [purgeTarget, setPurgeTarget] = useState<any>(null)
  const [loading, setLoading] = useState(false)
  const showLoading = useMinLoading(loading, 400)
  // 分页（服务端 page/page_size；查询/切页签回到第 1 页，刷新保持当前页）
  const [page, setPage] = useState(1)
  const [total, setTotal] = useState(0)
  const [pageSize, setPageSize] = useState(10)
  const totalPages = Math.max(1, Math.ceil(total / pageSize))

  // 表单（AGENTS.md 规范：RHF+zod 内联错误，新建/编辑共用 schema）
  const createForm = useForm<CreateForm>({
    resolver: zodResolver(createSchema),
    defaultValues: createDefaults,
  })
  const [templates, setTemplates] = useState<any[]>([])
  const createMode = createForm.watch("mode")
  const createTemplate = createForm.watch("template")
  const selectedTpl = templates.find((t) => t.id === createTemplate)
  const templateHint = selectedTpl?.description || ""
  // v2.2 T2：模板参数用局部 state 管理（动态 key 走 RHF 路径订阅不稳），提交时合并进 payload
  const tplOptions: any[] = selectedTpl?.options || []
  const [optState, setOptState] = useState<Record<string, string>>({})
  useEffect(() => {
    if (!selectedTpl) {
      setOptState({})
      return
    }
    const defaults: Record<string, string> = {}
    for (const o of selectedTpl.options || []) {
      defaults[o.key] = o.default ?? (o.type === "bool" ? "false" : o.values?.[0] ?? "")
    }
    setOptState(defaults)
  }, [createTemplate, templates])
  const editForm = useForm<ProjectForm>({
    resolver: zodResolver(projectSchema),
    defaultValues: { name: "", description: "" },
  })
  const purgeForm = useForm<PurgeForm>({
    resolver: zodResolver(purgeSchema),
    defaultValues: { confirmName: "" },
  })

  const load = async (query = q, which: "active" | "archived" = tab, targetPage = page, targetSize = pageSize) => {
    if (loading) return
    setLoading(true)
    try {
      const r: any = await Projects.list(query || undefined, targetPage, targetSize, which === "archived")
      setProjects(r.items || [])
      setTotal(Number(r.total) || 0)
      if (r.page_size) setPageSize(Number(r.page_size))
      setPage(targetPage)
    } catch (e: any) { setMsg(e.message) } finally { setLoading(false) }
  }
  useEffect(() => { load("", "active") }, [])

  const changePageSize = (size: number) => {
    setPageSize(size)
    load(q, tab, 1, size)
  }

  const switchTab = (v: string) => {
    setTab(v as any)
    setProjects([])
    setTotal(0)
    setPage(1)
    setMsg("")
    load(q, v as any, 1)
  }

  const onOpenCreate = (open: boolean) => {
    setCreateOpen(open)
    if (!open) {
      createForm.reset(createDefaults)
      setOptState({})
    }
  }
  // 受控 Dialog 打开不会触发 onOpenChange：模板列表必须在 open 翻转时主动拉取
  // （2026-09-16 实录：此前建项下拉只有兜底项，registry/builtin 列表从未加载）
  useEffect(() => {
    if (!createOpen) return
    createForm.reset(createDefaults)
    Projects.templates()
      .then((r: any) => {
        const items = r?.items || []
        setTemplates(items)
        if (items[0]?.id) createForm.setValue("template", items[0].id)
      })
      .catch(() => {})
  }, [createOpen])
  const onCreate = createForm.handleSubmit(async (values) => {
    try {
      // v2.2 T2：模板参数随建项提交（导入模式服务端忽略）
      const payload: any = { ...values }
      if (values.mode === "create" && Object.keys(optState).length > 0) payload.options = optState
      const r: any = await Projects.create(payload)
      const gerr = r?.gitea?.error
      if (gerr) {
        toast.error(`项目已创建，但 Gitea 集成失败：${gerr}`)
      } else {
        toast.success(`${values.mode === "import" ? "导入" : "创建"}项目 ${values.name} 成功`)
      }
      setMsg(`项目 ${values.name} 已创建`)
      createForm.reset(createDefaults)
      setCreateOpen(false)
      load()
    } catch { /* API 错误由 http 拦截器全局 toast */ }
  })

  const openEdit = (p: any) => {
    setEditId(p.id)
    editForm.reset({ name: p.name, description: p.description || "" })
    setEditOpen(true)
  }
  const onOpenEdit = (open: boolean) => {
    setEditOpen(open)
    if (!open) editForm.reset()
  }
  const onSaveEdit = editForm.handleSubmit(async (values) => {
    try {
      await Projects.update(editId, values)
      toast.success("项目已更新")
      setMsg("项目已更新")
      setEditOpen(false)
      load()
    } catch { /* 全局 toast */ }
  })

  // S1.4：删除按钮 → 归档（软删除，可恢复）
  const archiveProject = async (p: any) => {
    if (!confirm(`确认归档项目 ${p.name}？\n归档后项目转为只读，可在「已归档」列表中恢复`)) return
    try {
      await Projects.archive(p.id)
      toast.success(`项目 ${p.name} 已归档`)
      load()
    } catch (e: any) { setMsg(e.message) }
  }

  const restoreProject = async (p: any) => {
    try {
      await Projects.restore(p.id)
      toast.success(`项目 ${p.name} 已恢复`)
      load()
    } catch (e: any) { setMsg(e.message) }
  }

  const onOpenPurge = (open: boolean) => {
    if (!open) {
      purgeForm.reset()
      setPurgeTarget(null)
    }
  }
  const onPurge = purgeForm.handleSubmit(async (values) => {
    if (!purgeTarget || values.confirmName !== purgeTarget.name) return
    try {
      await Projects.purge(purgeTarget.id)
      toast.success(`项目 ${purgeTarget.name} 已彻底删除`)
      onOpenPurge(false)
      load()
    } catch (e: any) { setMsg(e.message); purgeForm.setError("confirmName", { message: e.message }) }
  })

  const canEdit = can("project.update")
  const canDelete = can("project.delete")
  const isArchivedView = tab === "archived"

  return (
    <div className="space-y-4 w-full">
      <Card>
        <CardHeader className="pb-2 flex-row items-center">
          <CardTitle className="text-sm">{isArchivedView ? `已归档项目 (${total})` : `项目列表 (${total || projects.length})`}</CardTitle>
          <div className="ml-auto flex gap-2 items-center">
            <div className="relative">
              <Search className="h-3.5 w-3.5 absolute left-2.5 top-1/2 -translate-y-1/2 text-muted-foreground" />
              <Input value={q} onChange={e => setQ(e.target.value)} onKeyDown={e => { if (e.key === "Enter") load(q, tab, 1) }} placeholder="项目名或描述" className="h-8 w-56 pl-7" />
            </div>
            <Button size="sm" variant="outline" onClick={() => load(q, tab, 1)} disabled={showLoading} className="h-8">{showLoading ? <Loader2 className="h-4 w-4 animate-spin" /> : null}查询</Button>
            {!isArchivedView && <Button size="sm" variant="forge" onClick={() => setCreateOpen(true)} disabled={!can("project.create")} className="h-8"><Plus className="h-4 w-4" /> 新建项目</Button>}
            <Button size="sm" variant="ghost" onClick={() => load(q, tab, page)} title="刷新" disabled={showLoading} className="h-8 w-8 p-0">{showLoading ? <Loader2 className="h-4 w-4 animate-spin" /> : <RefreshCw className="h-4 w-4" />}</Button>
          </div>
        </CardHeader>
        <CardContent className="relative">
          {showLoading && (
            <div className="absolute inset-0 bg-background/60 backdrop-blur-[1px] z-10 flex items-center justify-center rounded-b-lg">
              <span className="inline-flex items-center gap-2 text-xs text-muted-foreground bg-background border rounded-full px-3 py-1.5 shadow-sm">
                <Loader2 className="h-3.5 w-3.5 animate-spin" /> 正在刷新…
              </span>
            </div>
          )}
          <div className={showLoading ? "opacity-60 pointer-events-none transition-opacity" : "transition-opacity"}>
          <Tabs value={tab} onValueChange={switchTab} className="mb-3">
            <TabsList className="h-8">
              <TabsTrigger value="active" className="text-xs">进行中</TabsTrigger>
              <TabsTrigger value="archived" className="text-xs">已归档</TabsTrigger>
            </TabsList>
          </Tabs>
          <Table>
            <TableHeader><TableRow><TableHead>名称</TableHead><TableHead>Slug</TableHead><TableHead>描述</TableHead><TableHead>Owner</TableHead><TableHead>成员数</TableHead><TableHead className="text-right">操作</TableHead></TableRow></TableHeader>
            <TableBody>{projects.map((p: any) => (
              <TableRow key={p.id}>
                <TableCell className="text-xs font-medium cursor-pointer hover:underline" onClick={() => navigate({ to: "/projects/$projectId", params: { projectId: p.id } })}>{p.name}</TableCell>
                <TableCell><Badge variant="outline" className="text-[10px]">{p.slug || "-"}</Badge></TableCell>
                <TableCell className="text-xs text-muted-foreground max-w-[240px] truncate">{p.description || "-"}</TableCell>
                <TableCell className="text-xs">{p.owner || "-"}</TableCell>
                <TableCell className="text-xs">{p.member_count ?? "-"}</TableCell>
                <TableCell className="text-right whitespace-nowrap">
                  <Button size="sm" variant="ghost" className="h-6 px-1.5" title="详情" onClick={() => navigate({ to: "/projects/$projectId", params: { projectId: p.id } })}><Eye className="h-3.5 w-3.5" /></Button>
                  {!isArchivedView && canEdit && <Button size="sm" variant="ghost" className="h-6 px-1.5" title="编辑" onClick={() => openEdit(p)}><Pencil className="h-3.5 w-3.5" /></Button>}
                  {!isArchivedView && canDelete && <Button size="sm" variant="ghost" className="h-6 px-1.5" title="归档（可恢复）" onClick={() => archiveProject(p)}><Archive className="h-3.5 w-3.5" /></Button>}
                  {isArchivedView && canEdit && <Button size="sm" variant="ghost" className="h-6 px-1.5 text-emerald-600 hover:text-emerald-600" title="恢复项目" onClick={() => restoreProject(p)}><ArchiveRestore className="h-3.5 w-3.5" /></Button>}
                  {isArchivedView && canDelete && <Button size="sm" variant="ghost" className="h-6 px-1.5 text-destructive hover:text-destructive" title="彻底删除（不可恢复）" onClick={() => { setPurgeTarget(p); purgeForm.reset() }}><Trash2 className="h-3.5 w-3.5" /></Button>}
                </TableCell>
              </TableRow>
            ))}</TableBody>
          </Table>
          {!showLoading && !projects.length && !msg && (
            <Empty className="py-10 mt-2">
              <EmptyHeader>
                <EmptyMedia>{q ? <Search className="size-4" /> : isArchivedView ? <Archive className="size-4" /> : <FolderKanban className="size-4" />}</EmptyMedia>
                <EmptyTitle>{q ? "未找到匹配项目" : isArchivedView ? "暂无归档项目" : "暂无项目"}</EmptyTitle>
                <EmptyDescription>{q ? `关键词 “${q}” 无结果，试试其他关键词` : isArchivedView ? "归档后项目将在此展示" : "点击「新建项目」创建首个项目"}</EmptyDescription>
              </EmptyHeader>
              {q ? (
                <Button size="sm" variant="outline" className="h-8 mt-2" onClick={() => { setQ(""); load("", isArchivedView ? "archived" : "active", 1) }}>清除筛选</Button>
              ) : !isArchivedView && can("project.create") ? (
                <Button size="sm" variant="forge" className="h-8 mt-2" onClick={() => setCreateOpen(true)}><Plus className="h-4 w-4" />新建项目</Button>
              ) : null}
            </Empty>
          )}
          {msg && <div className="text-xs text-muted-foreground mt-2 break-all">{msg}</div>}
          </div>
          <Pagination
            page={page}
            totalPages={totalPages}
            total={total}
            pageSize={pageSize}
            disabled={showLoading}
            onPageChange={(p) => load(q, tab, p)}
            onPageSizeChange={changePageSize}
          />
        </CardContent>
      </Card>

      <Dialog open={createOpen} onOpenChange={onOpenCreate}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>新建项目</DialogTitle>
            <DialogDescription>选择模板生成可运行骨架，或导入既有仓库；创建者成为项目 Admin</DialogDescription>
          </DialogHeader>
          <form onSubmit={onCreate} noValidate className="space-y-3">
            <div className="flex flex-col gap-2">
              <label className="text-xs">方式</label>
              <div className="flex gap-2">
                <Button type="button" size="sm" className="h-8" variant={createMode === "create" ? "forge" : "outline"} onClick={() => createForm.setValue("mode", "create")}>新建模板</Button>
                <Button type="button" size="sm" className="h-8" variant={createMode === "import" ? "forge" : "outline"} onClick={() => createForm.setValue("mode", "import")}>导入仓库</Button>
              </div>
            </div>
            <div className="flex flex-col gap-2">
              <label className="text-xs">项目名 *</label>
              <Input {...createForm.register("name")} placeholder="如 team-alpha" className="h-8" />
              {createForm.formState.errors.name && <span className="text-xs text-destructive">{createForm.formState.errors.name.message}</span>}
            </div>
            <div className="flex flex-col gap-2">
              <label className="text-xs">描述</label>
              <Input {...createForm.register("description")} placeholder="可选" className="h-8" />
            </div>
            {createMode === "create" ? (
              <div className="flex flex-col gap-2">
                <label className="text-xs">项目模板 *</label>
                <select {...createForm.register("template")} className="h-8 rounded-md border bg-background px-2 text-xs">
                  {templates.length === 0 && <option value="standard-go-react">FORJA 标准栈（Go + React）</option>}
                  {templates.map((t) => (
                    <option key={t.id} value={t.id}>{t.name}</option>
                  ))}
                </select>
                {templateHint && <span className="text-[11px] text-muted-foreground">{templateHint}</span>}
                {tplOptions.length > 0 && (
                  <div className="flex flex-col gap-2 mt-1 border rounded-md p-2 bg-muted/20">
                    <span className="text-[11px] font-medium">模板参数</span>
                    {tplOptions.map((o: any) => (
                      <div key={o.key} className="flex items-center gap-2">
                        <span className="text-xs text-muted-foreground w-32 shrink-0">{o.label}</span>
                        {o.type === "enum" ? (
                          <select
                            value={optState[o.key] ?? ""}
                            onChange={(e) => setOptState((s) => ({ ...s, [o.key]: e.target.value }))}
                            className="h-8 flex-1 rounded-md border bg-background px-2 text-xs"
                          >
                            {(o.values || []).map((v: string) => (
                              <option key={v} value={v}>{v}</option>
                            ))}
                          </select>
                        ) : (
                          <Switch
                            checked={optState[o.key] === "true"}
                            onCheckedChange={(v: boolean) => setOptState((s) => ({ ...s, [o.key]: v ? "true" : "false" }))}
                          />
                        )}
                      </div>
                    ))}
                  </div>
                )}
              </div>
            ) : (
              <>
                <div className="flex flex-col gap-2">
                  <label className="text-xs">同实例仓库（owner/repo）</label>
                  <Input {...createForm.register("repo_full_name")} placeholder="forja-admin/my-project" className="h-8" />
                  {createForm.formState.errors.repo_full_name && <span className="text-xs text-destructive">{createForm.formState.errors.repo_full_name.message}</span>}
                </div>
                <div className="flex flex-col gap-2">
                  <label className="text-xs">或 外网镜像地址</label>
                  <Input {...createForm.register("mirror_url")} placeholder="https://github.com/org/repo.git" className="h-8" />
                </div>
                <p className="text-[11px] text-muted-foreground">导入要求仓根含合法 project.yaml；缺失会被拒绝并返回模板。</p>
              </>
            )}
            <div className="flex justify-end gap-2 pt-2">
              <Button size="sm" variant="outline" type="button" onClick={() => setCreateOpen(false)}>取消</Button>
              <Button size="sm" variant="forge" type="submit" disabled={createForm.formState.isSubmitting}>
                {createForm.formState.isSubmitting && <Loader2 className="h-4 w-4 animate-spin" />}
                {createMode === "import" ? "导入" : "创建"}
              </Button>
            </div>
          </form>
        </DialogContent>
      </Dialog>

      <Dialog open={editOpen} onOpenChange={onOpenEdit}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>编辑项目</DialogTitle>
            <DialogDescription>修改名称会同步更新 slug</DialogDescription>
          </DialogHeader>
          <form onSubmit={onSaveEdit} noValidate className="space-y-3">
            <div className="flex flex-col gap-2">
              <label className="text-xs">项目名 *</label>
              <Input {...editForm.register("name")} className="h-8" />
              {editForm.formState.errors.name && <span className="text-xs text-destructive">{editForm.formState.errors.name.message}</span>}
            </div>
            <div className="flex flex-col gap-2">
              <label className="text-xs">描述</label>
              <Input {...editForm.register("description")} className="h-8" />
            </div>
            <div className="flex justify-end gap-2 pt-2">
              <Button size="sm" variant="outline" type="button" onClick={() => setEditOpen(false)}>取消</Button>
              <Button size="sm" variant="forge" type="submit" disabled={editForm.formState.isSubmitting}>保存</Button>
            </div>
          </form>
        </DialogContent>
      </Dialog>

      {/* S1.4 彻底删除确认：输入完整项目名 */}
      <Dialog open={!!purgeTarget} onOpenChange={onOpenPurge}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle className="text-destructive">彻底删除 — {purgeTarget?.name}</DialogTitle>
            <DialogDescription>将级联清除该项目全部规格、任务、收件箱与审计记录，<span className="text-destructive font-medium">不可恢复</span></DialogDescription>
          </DialogHeader>
          <form onSubmit={onPurge} noValidate className="space-y-3">
            <div className="flex flex-col gap-2">
              <label className="text-xs">输入项目名 <span className="font-medium text-foreground">{purgeTarget?.name}</span> 以确认 *</label>
              <Input {...purgeForm.register("confirmName")} placeholder={purgeTarget?.name} className="h-8" autoFocus />
              {purgeForm.formState.errors.confirmName && <span className="text-xs text-destructive">{purgeForm.formState.errors.confirmName.message}</span>}
            </div>
            <div className="flex justify-end gap-2 pt-2">
              <Button size="sm" variant="outline" type="button" onClick={() => onOpenPurge(false)}>取消</Button>
              <Button size="sm" variant="destructive" type="submit" disabled={purgeForm.formState.isSubmitting || purgeForm.watch("confirmName") !== purgeTarget?.name}>彻底删除</Button>
            </div>
          </form>
        </DialogContent>
      </Dialog>
    </div>
  )
}
