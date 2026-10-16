import { useEffect, useState } from "react"
import { useForm } from "react-hook-form"
import { zodResolver } from "@hookform/resolvers/zod"
import { z } from "zod"
import { useNavigate } from "@tanstack/react-router"
import { toast } from "sonner"
import { Users } from "@/lib/api"
import { can, globalRole } from "@/lib/auth"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Badge } from "@/components/ui/badge"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from "@/components/ui/dialog"
import { Switch } from "@/components/ui/switch"
import { Plus, RefreshCw, Search, Pencil, Trash2, Loader2, Users as UsersIcon } from "lucide-react"
import { ConfirmDialog, type ConfirmState } from "@/components/shared/ConfirmDialog"
import { passwordSchema, optionalPasswordSchema } from "@/lib/validation"
import { useMinLoading } from "@/hooks/useMinLoading"
import { Empty, EmptyHeader, EmptyMedia, EmptyTitle, EmptyDescription } from "@/components/ui/empty"
import { Pagination } from "@/components/ui/pagination"

const ROLES = ["Admin", "Architect", "Developer", "Viewer", "PM", "QA"] as const

const createSchema = z.object({
  username: z.string().trim().min(1, "用户名必填"),
  password: passwordSchema,
  email: z.string().email("Email 格式不正确").or(z.literal("")),
  role: z.enum(ROLES),
})
type CreateForm = z.infer<typeof createSchema>

const editSchema = z.object({
  email: z.string().email("Email 格式不正确").or(z.literal("")),
  password: optionalPasswordSchema,
  role: z.enum(ROLES),
})
type EditForm = z.infer<typeof editSchema>

function RoleSelect({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  return (
    <select value={value} onChange={e => onChange(e.target.value)} className="border rounded-md h-8 px-2 text-sm w-full bg-background">
      {ROLES.map(r => <option key={r}>{r}</option>)}
    </select>
  )
}

export default function UserBoard() {
  const navigate = useNavigate()
  const [users, setUsers] = useState<any[]>([])
  const [confirmState, setConfirmState] = useState<ConfirmState | null>(null)
  const [q, setQ] = useState("")
  const [createOpen, setCreateOpen] = useState(false)
  const [editOpen, setEditOpen] = useState(false)
  const [editUsername, setEditUsername] = useState("")
  const [editId, setEditId] = useState("")
  const [loading, setLoading] = useState(false)
  const showLoading = useMinLoading(loading, 400)
  // 分页（服务端 page/page_size；查询回到第 1 页，刷新保持当前页）
  const [page, setPage] = useState(1)
  const [total, setTotal] = useState(0)
  const [pageSize, setPageSize] = useState(10)
  const totalPages = Math.max(1, Math.ceil(total / pageSize))
  const canList = can("user.list")

  const load = async (query = q, targetPage = page, targetSize = pageSize) => {
    if (loading) return
    setLoading(true)
    try {
      const u: any = await Users.list(query || undefined, targetPage, targetSize)
      setUsers(u.items || [])
      setTotal(Number(u.total) || 0)
      if (u.page_size) setPageSize(Number(u.page_size))
      setPage(targetPage)
    } catch (e: any) { toast.error(e.message) } finally { setLoading(false) }
  }

  const changePageSize = (size: number) => {
    setPageSize(size)
    load(q, 1, size)
  }
  useEffect(() => { if (canList) load("") }, [])

  // 新建用户表单（AGENTS.md 表单规范：RHF+zod 内联错误）
  const createForm = useForm<CreateForm>({
    resolver: zodResolver(createSchema),
    defaultValues: { username: "", password: "", email: "", role: "Developer" },
  })
  const onOpenCreate = (open: boolean) => {
    setCreateOpen(open)
    if (!open) createForm.reset()
  }
  const onCreate = createForm.handleSubmit(async (values) => {
    try {
      await Users.create(values)
      toast.success(`用户 ${values.username} 创建成功`)
      createForm.reset({ username: "", password: "", email: "", role: values.role })
      setCreateOpen(false)
      load()
    } catch { /* API 错误由 http 拦截器全局 toast */ }
  })

  // 编辑用户表单
  const editForm = useForm<EditForm>({
    resolver: zodResolver(editSchema),
    defaultValues: { email: "", password: "", role: "Developer" },
  })
  const openEdit = (u: any) => {
    setEditUsername(u.username)
    setEditId(u.id)
    editForm.reset({ email: u.email || "", password: "", role: u.role })
    setEditOpen(true)
  }
  const onOpenEdit = (open: boolean) => {
    setEditOpen(open)
    if (!open) editForm.reset()
  }
  const onSaveEdit = editForm.handleSubmit(async (values) => {
    try {
      const body: any = { email: values.email, role: values.role }
      if (values.password) body.password = values.password
      await Users.update(editId, body)
      toast.success(`用户 ${editUsername} 已更新`)
      setEditOpen(false)
      load()
    } catch { /* 全局 toast */ }
  })

  const toggleEnabled = async (u: any) => {
    // optimistic update, rollback on failure
    setUsers(prev => prev.map(x => (x.id === u.id ? { ...x, enabled: !x.enabled } : x)))
    try {
      if (u.enabled) await Users.disable(u.id)
      else await Users.enable(u.id)
      toast.success(`${u.username} 已${u.enabled ? "禁用" : "启用"}`)
    } catch (e: any) {
      setUsers(prev => prev.map(x => (x.id === u.id ? { ...x, enabled: u.enabled } : x)))
      toast.error(e.message)
    }
  }
  const removeUser = (u: any) => {
    setConfirmState({
      title: `删除用户 ${u.username}`,
      description: "该操作不可恢复。",
      confirmText: "删除",
      danger: true,
      action: async () => {
        try {
          await Users.remove(u.id)
          toast.success(`用户 ${u.username} 已删除`)
          load()
        } catch (e: any) { toast.error(e.message); throw e }
      },
    })
  }

  if (!canList) return <div className="text-sm text-muted-foreground p-4">仅 Admin 可管理用户 (当前 {globalRole()})</div>

  return (
    <div className="space-y-4 w-full">
      <Card>
        <CardHeader className="pb-2 flex-row items-center">
          <CardTitle className="text-sm">用户列表 ({total || users.length})</CardTitle>
          <div className="ml-auto flex gap-2 items-center">
            <div className="relative">
              <Search className="h-3.5 w-3.5 absolute left-2.5 top-1/2 -translate-y-1/2 text-muted-foreground" />
              <Input value={q} onChange={e => setQ(e.target.value)} onKeyDown={e => { if (e.key === "Enter") load(q, 1) }} placeholder="用户名或 Email" className="h-8 w-56 pl-7" />
            </div>
            <Button size="sm" variant="outline" onClick={() => load(q, 1)} disabled={showLoading} className="h-8">{showLoading ? <Loader2 className="h-4 w-4 animate-spin" /> : null}查询</Button>
            <Button size="sm" variant="forge" onClick={() => setCreateOpen(true)} className="h-8"><Plus className="h-4 w-4" /> 新建用户</Button>
            <Button size="sm" variant="ghost" onClick={() => load(q, page)} title="刷新" disabled={showLoading} className="h-8 w-8 p-0">{showLoading ? <Loader2 className="h-4 w-4 animate-spin" /> : <RefreshCw className="h-4 w-4" />}</Button>
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
          <Table>
            <TableHeader><TableRow><TableHead>用户名</TableHead><TableHead>Email</TableHead><TableHead>角色</TableHead><TableHead>创建时间</TableHead><TableHead>最后登录</TableHead><TableHead>状态</TableHead><TableHead className="text-right">操作</TableHead></TableRow></TableHeader>
            <TableBody>{users.map((u: any) => (
              <TableRow key={u.id}>
                <TableCell className="text-xs font-medium"><button className="hover:underline font-medium" onClick={() => navigate({ to: "/admin/users/$id", params: { id: u.id } as any })}>{u.username}</button></TableCell>
                <TableCell className="text-xs">{u.email || "-"}</TableCell>
                <TableCell><Badge variant="secondary" className="text-[10px]">{u.role}</Badge></TableCell>
                <TableCell className="text-xs text-muted-foreground" title={u.created_at ? new Date(u.created_at).toLocaleString("zh-CN", { hour12: false }) : undefined}>{u.created_at ? new Date(u.created_at).toLocaleString("zh-CN", { hour12: false }) : "-"}</TableCell>
                <TableCell className="text-xs text-muted-foreground" title={u.last_login_at ? new Date(u.last_login_at).toLocaleString("zh-CN", { hour12: false }) : undefined}>{u.last_login_at ? new Date(u.last_login_at).toLocaleString("zh-CN", { hour12: false }) : "从未登录"}</TableCell>
                <TableCell>
                  <div className="flex items-center gap-2">
                    <Switch checked={u.enabled} onCheckedChange={() => toggleEnabled(u)} title={u.enabled ? "点击禁用" : "点击启用"} />
                    <span className={`text-xs ${u.enabled ? "" : "text-muted-foreground"}`}>{u.enabled ? "启用" : "禁用"}</span>
                  </div>
                </TableCell>
                <TableCell className="text-right">
                  <Button size="sm" variant="ghost" className="h-6 px-1.5" title="编辑" onClick={() => openEdit(u)}><Pencil className="h-3.5 w-3.5" /></Button>
                  <Button size="sm" variant="ghost" className="h-6 px-1.5 text-destructive hover:text-destructive" title="删除" onClick={() => removeUser(u)}><Trash2 className="h-3.5 w-3.5" /></Button>
                </TableCell>
              </TableRow>
             ))}</TableBody>
          </Table>
          {!showLoading && users.length === 0 && (
            <Empty className="py-10 mt-2">
              <EmptyHeader>
                <EmptyMedia><UsersIcon className="size-4" /></EmptyMedia>
                <EmptyTitle>{q ? "未找到匹配用户" : "暂无用户"}</EmptyTitle>
                <EmptyDescription>{q ? `关键词 “${q}” 无结果，试试其他关键词` : "点击「新建用户」创建首个账号"}</EmptyDescription>
              </EmptyHeader>
              {q ? (
                <Button size="sm" variant="outline" className="h-8 mt-2" onClick={() => { setQ(""); load("", 1) }}>清除筛选</Button>
              ) : (
                <Button size="sm" variant="forge" className="h-8 mt-2" onClick={() => setCreateOpen(true)}><Plus className="h-4 w-4" />新建用户</Button>
              )}
            </Empty>
          )}
          </div>
          <Pagination
            page={page}
            totalPages={totalPages}
            total={total}
            pageSize={pageSize}
            disabled={showLoading}
            onPageChange={(p) => load(q, p)}
            onPageSizeChange={changePageSize}
          />
        </CardContent>
      </Card>
      <ConfirmDialog state={confirmState} onClose={() => setConfirmState(null)} />


      <Dialog open={createOpen} onOpenChange={onOpenCreate}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>新建用户</DialogTitle>
            <DialogDescription>创建后自动加入默认项目 forja，密码 bcrypt 存储</DialogDescription>
          </DialogHeader>
          <form onSubmit={onCreate} noValidate className="space-y-3">
            <div className="flex flex-col gap-2">
              <label className="text-xs">用户名 *</label>
              <Input {...createForm.register("username")} placeholder="如 zhangsan" className="h-8" />
              {createForm.formState.errors.username && <span className="text-xs text-destructive">{createForm.formState.errors.username.message}</span>}
            </div>
            <div className="flex flex-col gap-2">
              <label className="text-xs">密码 *</label>
              <Input {...createForm.register("password")} type="password" placeholder="至少 1 位" className="h-8" />
              {createForm.formState.errors.password && <span className="text-xs text-destructive">{createForm.formState.errors.password.message}</span>}
            </div>
            <div className="flex flex-col gap-2">
              <label className="text-xs">Email</label>
              <Input {...createForm.register("email")} placeholder="可选" className="h-8" />
              {createForm.formState.errors.email && <span className="text-xs text-destructive">{createForm.formState.errors.email.message}</span>}
            </div>
            <div className="flex flex-col gap-2">
              <label className="text-xs">角色</label>
              <RoleSelect value={createForm.watch("role")} onChange={(v) => createForm.setValue("role", v as CreateForm["role"], { shouldValidate: true })} />
            </div>
            <div className="flex justify-end gap-2 pt-2">
              <Button size="sm" variant="outline" type="button" onClick={() => setCreateOpen(false)}>取消</Button>
              <Button size="sm" variant="forge" type="submit" disabled={createForm.formState.isSubmitting}>创建</Button>
            </div>
          </form>
        </DialogContent>
      </Dialog>

      <Dialog open={editOpen} onOpenChange={onOpenEdit}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>编辑用户 — {editUsername}</DialogTitle>
            <DialogDescription>留空密码则不修改；角色变更同步项目成员</DialogDescription>
          </DialogHeader>
          <form onSubmit={onSaveEdit} noValidate className="space-y-3">
            <div className="flex flex-col gap-2">
              <label className="text-xs">Email</label>
              <Input {...editForm.register("email")} className="h-8" />
              {editForm.formState.errors.email && <span className="text-xs text-destructive">{editForm.formState.errors.email.message}</span>}
            </div>
            <div className="flex flex-col gap-2">
              <label className="text-xs">重置密码</label>
              <Input {...editForm.register("password")} type="password" placeholder="留空不修改" className="h-8" />
              {editForm.formState.errors.password && <span className="text-xs text-destructive">{editForm.formState.errors.password.message}</span>}
            </div>
            <div className="flex flex-col gap-2">
              <label className="text-xs">角色</label>
              <RoleSelect value={editForm.watch("role")} onChange={(v) => editForm.setValue("role", v as EditForm["role"], { shouldValidate: true })} />
            </div>
            <div className="flex justify-end gap-2 pt-2">
              <Button size="sm" variant="outline" type="button" onClick={() => setEditOpen(false)}>取消</Button>
              <Button size="sm" variant="forge" type="submit" disabled={editForm.formState.isSubmitting}>保存</Button>
            </div>
          </form>
        </DialogContent>
      </Dialog>
    </div>
  )
}
