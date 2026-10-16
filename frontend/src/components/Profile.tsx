import { useEffect, useState } from "react"
import { useForm } from "react-hook-form"
import { zodResolver } from "@hookform/resolvers/zod"
import { z } from "zod"
import { useQuery } from "@tanstack/react-query"
import { toast } from "sonner"
import { Auth, ApiError, Me } from "@/lib/api"
import { passwordSchema } from "@/lib/validation"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Badge } from "@/components/ui/badge"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Empty, EmptyHeader, EmptyMedia, EmptyTitle, EmptyDescription } from "@/components/ui/empty"
import { useMinLoading } from "@/hooks/useMinLoading"
import { Monitor, History, Loader2 } from "lucide-react"

// 修改密码表单（AGENTS.md 表单规范：RHF+zod 内联错误；API 错误 silent 后映射到字段）
const changePwSchema = z
  .object({
    old_password: z.string().min(1, "请输入当前密码"),
    new_password: passwordSchema,
    confirm: z.string().min(1, "请再次输入新密码"),
  })
  .refine((v) => v.new_password === v.confirm, { path: ["confirm"], message: "两次输入的新密码不一致" })
type ChangePwForm = z.infer<typeof changePwSchema>

function parseUA(ua: string) {
  if (!ua) return "-"
  const s = ua.slice(0, 120)
  if (s.includes("Chrome")) return s.match(/Chrome\/[\d.]+/)?.[0] || "Chrome"
  if (s.includes("Firefox")) return s.match(/Firefox\/[\d.]+/)?.[0] || "Firefox"
  if (s.includes("Safari") && !s.includes("Chrome")) return s.match(/Version\/[\d.]+/)?.[0] || "Safari"
  if (s.includes("Edg")) return s.match(/Edg\/[\d.]+/)?.[0] || "Edge"
  return s.slice(0, 40)
}

export function Profile() {
  const { data } = useQuery({ queryKey: ["me"], queryFn: () => Auth.me() })
  const me: any = data || {}
  const memberships: any[] = me.memberships || []

  const pwForm = useForm<ChangePwForm>({
    resolver: zodResolver(changePwSchema),
    defaultValues: { old_password: "", new_password: "", confirm: "" },
  })
  const onChangePw = pwForm.handleSubmit(async (values) => {
    try {
      await Auth.changePassword({ old_password: values.old_password, new_password: values.new_password })
      toast.success("密码已修改")
      pwForm.reset()
    } catch (e: any) {
      if (e instanceof ApiError && e.field === "old_password") {
        pwForm.setError("old_password", { message: "当前密码不正确" })
      } else if (e instanceof ApiError) {
        pwForm.setError("new_password", { message: e.message })
      }
    }
  })

  // 登录日志（本人）
  const [logs, setLogs] = useState<any[]>([])
  const [logLoading, setLogLoading] = useState(false)
  const showLogLoading = useMinLoading(logLoading, 400)
  const [logPage, setLogPage] = useState(1)
  const [logTotal, setLogTotal] = useState(0)
  const [logSuccess, setLogSuccess] = useState<string>("")
  const pageSize = 20
  const logTotalPages = Math.max(1, Math.ceil(logTotal / pageSize))

  const loadLogs = async (page = logPage, success = logSuccess) => {
    setLogLoading(true)
    try {
      const r: any = await Me.loginLogs(page, pageSize, success ? { success: success === "true" } : {})
      setLogs(r.items || [])
      setLogTotal(r.total ?? 0)
      setLogPage(page)
    } catch (e: any) { toast.error(e.message) } finally { setLogLoading(false) }
  }
  useEffect(() => { loadLogs(1, "") }, [])
  useEffect(() => { loadLogs(1, logSuccess) }, [logSuccess])

  return (
    <div className="space-y-4 w-full max-w-3xl">
      <Tabs defaultValue="overview">
        <TabsList>
          <TabsTrigger value="overview">概览</TabsTrigger>
          <TabsTrigger value="logins">登录日志</TabsTrigger>
          <TabsTrigger value="security">安全</TabsTrigger>
        </TabsList>

        <TabsContent value="overview" className="space-y-4 mt-4">
          <Card>
            <CardHeader className="pb-2"><CardTitle className="text-sm">账户信息</CardTitle></CardHeader>
            <CardContent>
              <Table>
                <TableHeader><TableRow><TableHead>用户名</TableHead><TableHead>Email</TableHead><TableHead>全局角色</TableHead><TableHead>创建时间</TableHead><TableHead>最后登录</TableHead></TableRow></TableHeader>
                <TableBody>
                  <TableRow>
                    <TableCell className="text-xs font-medium">{me.username || "-"}</TableCell>
                    <TableCell className="text-xs">{me.email || "-"}</TableCell>
                    <TableCell><Badge variant="secondary" className="text-[10px]">{me.global_role || "-"}</Badge></TableCell>
                    <TableCell className="text-xs text-muted-foreground">{me.created_at ? new Date(me.created_at).toLocaleString("zh-CN", { hour12: false }) : "-"}</TableCell>
                    <TableCell className="text-xs text-muted-foreground">{me.last_login_at ? new Date(me.last_login_at).toLocaleString("zh-CN", { hour12: false }) : "从未登录"}</TableCell>
                  </TableRow>
                </TableBody>
              </Table>
            </CardContent>
          </Card>

          <Card>
            <CardHeader className="pb-2"><CardTitle className="text-sm">我的项目身份</CardTitle></CardHeader>
            <CardContent>
              {memberships.length ? (
                <Table>
                  <TableHeader><TableRow><TableHead>项目</TableHead><TableHead>角色</TableHead></TableRow></TableHeader>
                  <TableBody>{memberships.map((m: any) => (
                    <TableRow key={m.project_id}>
                      <TableCell className="text-xs font-medium">{m.project_name}</TableCell>
                      <TableCell><Badge variant="outline" className="text-[10px]">{m.role}</Badge></TableCell>
                    </TableRow>
                  ))}</TableBody>
                </Table>
              ) : (
                <div className="text-xs text-muted-foreground">暂未加入任何项目，请联系项目管理员邀请你加入</div>
              )}
              <div className="text-xs text-muted-foreground mt-2">项目角色决定你在各项目内的操作权限（如 PM 可管理需求堆、QA 可补断言重跑、Architect/Admin 可发布任务）</div>
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="logins" className="space-y-4 mt-4">
          <Card>
            <CardHeader className="pb-2 flex-row items-center">
              <CardTitle className="text-sm">登录日志（UA 排障，去 IP）</CardTitle>
              <div className="ml-auto flex gap-2 items-center">
                <select value={logSuccess} onChange={e => setLogSuccess(e.target.value)} className="border rounded-md h-8 px-2 text-xs bg-background">
                  <option value="">全部结果</option>
                  <option value="true">成功</option>
                  <option value="false">失败</option>
                </select>
                <Button size="sm" variant="outline" onClick={() => loadLogs(1, logSuccess)} disabled={showLogLoading} className="h-8">{showLogLoading ? <Loader2 className="h-4 w-4 animate-spin" /> : null}刷新</Button>
              </div>
            </CardHeader>
            <CardContent className="relative">
              {showLogLoading && (
                <div className="absolute inset-0 bg-background/60 backdrop-blur-[1px] z-10 flex items-center justify-center rounded-b-lg">
                  <span className="inline-flex items-center gap-2 text-xs text-muted-foreground bg-background border rounded-full px-3 py-1.5 shadow-sm"><Loader2 className="h-3.5 w-3.5 animate-spin" />正在刷新…</span>
                </div>
              )}
              <div className={showLogLoading ? "opacity-60 pointer-events-none transition-opacity" : "transition-opacity"}>
              <Table>
                <TableHeader><TableRow><TableHead>时间</TableHead><TableHead>浏览器</TableHead><TableHead>结果</TableHead><TableHead>原因</TableHead><TableHead className="text-right">UA 全量</TableHead></TableRow></TableHeader>
                <TableBody>{logs.map((l: any) => (
                  <TableRow key={l.id}>
                    <TableCell className="text-xs text-muted-foreground">{new Date(l.created_at).toLocaleString("zh-CN", { hour12: false })}</TableCell>
                    <TableCell className="text-xs"><span title={l.user_agent} className="inline-flex items-center gap-1"><Monitor className="h-3 w-3 text-muted-foreground" />{parseUA(l.user_agent)}</span></TableCell>
                    <TableCell>{l.success ? <Badge variant="secondary" className="text-[10px] bg-emerald-500/15 text-emerald-700">成功</Badge> : <Badge variant="destructive" className="text-[10px]">失败</Badge>}</TableCell>
                    <TableCell className="text-xs">{l.failure_reason || "—"}</TableCell>
                    <TableCell className="text-xs text-muted-foreground max-w-[260px] truncate text-right" title={l.user_agent}>{l.user_agent || "—"}</TableCell>
                  </TableRow>
                ))}</TableBody>
              </Table>
              {!showLogLoading && logs.length === 0 && (
                <Empty className="py-10 mt-2">
                  <EmptyHeader><EmptyMedia><History className="size-4" /></EmptyMedia><EmptyTitle>暂无登录记录</EmptyTitle><EmptyDescription>暂无登录日志</EmptyDescription></EmptyHeader>
                </Empty>
              )}
              </div>
            </CardContent>
          </Card>
          {logTotalPages > 1 && (
            <div className="flex items-center justify-end gap-2 text-xs text-muted-foreground">
              <span>第 {logPage} / {logTotalPages} 页 · 共 {logTotal} 条</span>
              <Button size="sm" variant="outline" disabled={logPage <= 1} onClick={() => loadLogs(logPage - 1)}>上一页</Button>
              <Button size="sm" variant="outline" disabled={logPage >= logTotalPages} onClick={() => loadLogs(logPage + 1)}>下一页</Button>
            </div>
          )}
        </TabsContent>

        <TabsContent value="security" className="space-y-4 mt-4">
          <Card>
            <CardHeader className="pb-2">
              <CardTitle className="text-sm">修改密码</CardTitle>
              <p className="text-xs text-muted-foreground mt-1">验证当前密码后设置新密码；其他已登录会话不受影响</p>
            </CardHeader>
            <CardContent>
              <form onSubmit={onChangePw} noValidate className="space-y-3 max-w-sm">
                <div className="flex flex-col gap-2">
                  <label className="text-xs">当前密码 *</label>
                  <Input type="password" {...pwForm.register("old_password")} className="h-8" />
                  {pwForm.formState.errors.old_password && <span className="text-xs text-destructive">{pwForm.formState.errors.old_password.message}</span>}
                </div>
                <div className="flex flex-col gap-2">
                  <label className="text-xs">新密码 *</label>
                  <Input type="password" {...pwForm.register("new_password")} className="h-8" />
                  {pwForm.formState.errors.new_password && <span className="text-xs text-destructive">{pwForm.formState.errors.new_password.message}</span>}
                </div>
                <div className="flex flex-col gap-2">
                  <label className="text-xs">确认新密码 *</label>
                  <Input type="password" {...pwForm.register("confirm")} className="h-8" />
                  {pwForm.formState.errors.confirm && <span className="text-xs text-destructive">{pwForm.formState.errors.confirm.message}</span>}
                </div>
                <Button variant="forge" size="sm" type="submit" disabled={pwForm.formState.isSubmitting}>确认修改</Button>
              </form>
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>
    </div>
  )
}

export default Profile
