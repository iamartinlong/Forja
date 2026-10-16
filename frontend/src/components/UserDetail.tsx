import { useEffect, useState } from "react"
import { useParams, useNavigate } from "@tanstack/react-router"
import { toast } from "sonner"
import { Users } from "@/lib/api"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Empty, EmptyHeader, EmptyMedia, EmptyTitle, EmptyDescription } from "@/components/ui/empty"
import { useMinLoading } from "@/hooks/useMinLoading"
import { ArrowLeft, Shield, Clock, Monitor, History } from "lucide-react"
import { Loader2 } from "lucide-react"
import { PageLoader } from "@/components/ui/page-loader"

function parseUA(ua: string) {
  if (!ua) return "-"
  // 简易 UA 首段截取，取到第一个 ) 或前 40 字符
  const s = ua.slice(0, 120)
  // 尝试提取浏览器名
  if (s.includes("Chrome")) return s.match(/Chrome\/[\d.]+/)?.[0] || "Chrome"
  if (s.includes("Firefox")) return s.match(/Firefox\/[\d.]+/)?.[0] || "Firefox"
  if (s.includes("Safari") && !s.includes("Chrome")) return s.match(/Version\/[\d.]+/)?.[0] || "Safari"
  if (s.includes("Edg")) return s.match(/Edg\/[\d.]+/)?.[0] || "Edge"
  return s.slice(0, 40)
}

export default function UserDetail() {
  const { id } = useParams({ strict: false }) as { id: string }
  const navigate = useNavigate()
  const [user, setUser] = useState<any>(null)
  const [loading, setLoading] = useState(false)
  const [logs, setLogs] = useState<any[]>([])
  const [logLoading, setLogLoading] = useState(false)
  const showLogLoading = useMinLoading(logLoading, 400)
  const [logPage, setLogPage] = useState(1)
  const [logTotal, setLogTotal] = useState(0)
  const [logSuccess, setLogSuccess] = useState<string>("")
  const pageSize = 20
  const logTotalPages = Math.max(1, Math.ceil(logTotal / pageSize))

  const loadUser = async () => {
    if (!id) return
    setLoading(true)
    try {
      const r: any = await Users.get(id)
      setUser(r.data || r)
    } catch (e: any) { toast.error(e.message) } finally { setLoading(false) }
  }
  const loadLogs = async (page = logPage, success = logSuccess) => {
    if (!id) return
    setLogLoading(true)
    try {
      const r: any = await Users.loginLogs(id, page, pageSize, success ? { success: success === "true" } : {})
      setLogs(r.items || [])
      setLogTotal(r.total ?? 0)
      setLogPage(page)
    } catch (e: any) { toast.error(e.message) } finally { setLogLoading(false) }
  }
  useEffect(() => { loadUser(); loadLogs(1, "") }, [id])
  useEffect(() => { loadLogs(1, logSuccess) }, [logSuccess])

  if (!user && loading) return <PageLoader />
  if (!user && !loading) return <div className="text-sm p-4 text-muted-foreground">用户不存在</div>

  return (
    <div className="space-y-4 w-full max-w-5xl">
      <Card>
        <CardContent className="pt-4">
          <div className="flex items-start gap-3">
            <Button size="sm" variant="ghost" className="h-8 w-8 p-0 shrink-0" onClick={() => navigate({ to: "/admin/users" })}><ArrowLeft className="h-4 w-4" /></Button>
            <div className="flex-1 min-w-0">
              <div className="flex items-center gap-2 flex-wrap">
                <h2 className="text-base font-semibold truncate">{user.username}</h2>
                <Badge variant={user.enabled ? "secondary" : "destructive"} className="text-[10px]">{user.enabled ? "启用" : "禁用"}</Badge>
                <Badge variant="outline" className="text-[10px]">{user.role}</Badge>
                <span className="text-xs text-muted-foreground">{user.email || "—"}</span>
              </div>
              <div className="flex items-center gap-2 text-xs text-muted-foreground mt-1 flex-wrap">
                <span className="inline-flex items-center gap-1"><Shield className="h-3 w-3" />创建于 {user.created_at ? new Date(user.created_at).toLocaleString("zh-CN", { hour12: false }) : "-"}</span>
                <span>·</span>
                <span className="inline-flex items-center gap-1"><Clock className="h-3 w-3" />最后登录 {user.last_login_at ? new Date(user.last_login_at).toLocaleString("zh-CN", { hour12: false }) : "从未登录"}</span>
              </div>
            </div>
          </div>
        </CardContent>
      </Card>

      <Tabs defaultValue="overview">
        <TabsList>
          <TabsTrigger value="overview">概览</TabsTrigger>
          <TabsTrigger value="logins">登录日志</TabsTrigger>
          <TabsTrigger value="activity">操作审计</TabsTrigger>
          <TabsTrigger value="sessions">会话</TabsTrigger>
        </TabsList>

        <TabsContent value="overview" className="space-y-4 mt-4">
          <div className="grid grid-cols-2 lg:grid-cols-3 gap-3">
            <Card><CardHeader className="pb-2"><CardTitle className="text-xs">基本信息</CardTitle></CardHeader><CardContent className="text-xs space-y-1">
              <div>用户名：<span className="font-medium">{user.username}</span></div>
              <div>Email：{user.email || "—"}</div>
              <div>角色：<Badge variant="secondary" className="text-[10px]">{user.role}</Badge></div>
              <div>状态：{user.enabled ? "启用" : "禁用"}</div>
              <div>创建时间：{user.created_at ? new Date(user.created_at).toLocaleString("zh-CN", { hour12: false }) : "-"}</div>
              <div>更新时间：{user.updated_at ? new Date(user.updated_at).toLocaleString("zh-CN", { hour12: false }) : "-"}</div>
            </CardContent></Card>
            <Card><CardHeader className="pb-2"><CardTitle className="text-xs">成员项目 ({user.memberships?.length || 0})</CardTitle></CardHeader><CardContent>
              {(user.memberships && user.memberships.length) ? (
                <Table><TableHeader><TableRow><TableHead>项目</TableHead><TableHead>角色</TableHead></TableRow></TableHeader>
                <TableBody>{user.memberships.map((m: any) => (
                  <TableRow key={m.project_id}><TableCell className="text-xs">{m.project_name}</TableCell><TableCell><Badge variant="outline" className="text-[10px]">{m.role}</Badge></TableCell></TableRow>
                ))}</TableBody></Table>
              ) : <div className="text-xs text-muted-foreground">暂未加入任何项目</div>}
            </CardContent></Card>
            <Card><CardHeader className="pb-2"><CardTitle className="text-xs">近期审计</CardTitle></CardHeader><CardContent className="space-y-1">
              {(user.recent_audits && user.recent_audits.length) ? user.recent_audits.map((a: any, i: number) => (
                <div key={i} className="text-xs flex gap-2 border-b py-1 last:border-0"><span className="text-muted-foreground">{new Date(a.created_at).toLocaleString("zh-CN", { hour12: false })}</span><Badge variant="outline" className="text-[10px]">{a.action}</Badge><span className="truncate">{a.target_type}</span></div>
              )) : <div className="text-xs text-muted-foreground">暂无动态</div>}
            </CardContent></Card>
          </div>
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
                  <EmptyHeader><EmptyMedia><History className="size-4" /></EmptyMedia><EmptyTitle>暂无登录记录</EmptyTitle><EmptyDescription>该用户暂无登录日志，或筛选条件无结果</EmptyDescription></EmptyHeader>
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

        <TabsContent value="activity" className="mt-4">
          <Card><CardContent className="py-10 text-center">
            <Empty className="border-0">
              <EmptyHeader><EmptyMedia><History className="size-4" /></EmptyMedia><EmptyTitle>操作审计（后续）</EmptyTitle><EmptyDescription>将聚合该用户在各项目的审计（audit_logs actor={user.username}），支持按项目/动作筛选</EmptyDescription></EmptyHeader>
            </Empty>
          </CardContent></Card>
        </TabsContent>

        <TabsContent value="sessions" className="mt-4">
          <Card><CardContent className="py-10 text-center">
            <Empty className="border-0">
              <EmptyHeader><EmptyMedia><Monitor className="size-4" /></EmptyMedia><EmptyTitle>会话（后续）</EmptyTitle><EmptyDescription>将展示 refresh_tokens 会话列表（最后使用/过期）及吊销按钮</EmptyDescription></EmptyHeader>
            </Empty>
          </CardContent></Card>
        </TabsContent>
      </Tabs>
    </div>
  )
}
