import { useState } from "react"
import { useNavigate, useRouterState } from "@tanstack/react-router"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { Sidebar, SidebarContent, SidebarFooter, SidebarGroup, SidebarHeader, SidebarMenu, SidebarMenuItem, useSidebar } from "@/components/ui/sidebar"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { cn } from "@/lib/utils"
import { can, globalRole } from "@/lib/auth"
import { Auth, Inbox as InboxApi, Pipelines, Projects } from "@/lib/api"
import { queryClient } from "@/lib/query"
import { silenceAuthErrors } from "@/lib/http"
import { DEFAULT_PROJECT_ID, useForjaStore } from "@/lib/store"
import { useEventSource } from "@/lib/useEventSource"
import { NavBadge, DangerDot } from "@/components/layout/NavBadge"
import { Inbox, FileText, Kanban, GanttChart, Flame, Factory, BarChart3, Search, MessageCircle, FolderKanban, Users, UserCircle, LogOut, GitMerge, Settings, LayoutDashboard } from "lucide-react"

export const sidebarNav: any = [
  { group: "工作台", items: [{ label: "工作台", to: "/", icon: LayoutDashboard, roles: ["Admin","Architect","Developer","Viewer","PM","QA"], preload: () => { import("@/components/Workbench") } }] },
  // observability P2：静态 "NEW" 徽章迁 NavBadge 动态计数（badgeKey="inboxNew"）
  { group: "原料", items: [{ label: "Inbox 原料堆", to: "/inbox", icon: Inbox, badgeKey: "inboxNew", action: "inbox.read", roles: ["Admin","Architect","Developer","PM","QA"] }] },
  { group: "图纸", items: [
    { label: "Spec 编辑器", to: "/specs", icon: FileText, roles: ["Admin","Architect","Developer","QA"],
      // S1.6 同类修复：悬停预热 SpecsPanel chunk
      preload: () => { import("@/components/specs/SpecsPanel") } },
    { label: "任务看板", to: "/tasks", icon: Kanban, roles: ["Admin","Architect","Developer","QA"] },
    { label: "甘特", to: "/gantt", icon: GanttChart, roles: ["Admin","Architect","PM","QA"],
      // S1.6 / KI-001：悬停预热 GanttView chunk
      preload: () => { import("@/components/GanttView") } },
  ]},
  { group: "锻造", items: [
    { label: "锻造流水线", to: "/forge", icon: Flame, roles: ["Admin","Developer","Architect","QA"] },
    { label: "炉池", to: "/admin/furnaces", icon: Factory, action: "user.list", preload: () => { import("@/components/FurnaceBoard") } },
    { label: "指标", to: "/metrics", icon: BarChart3, roles: ["Admin","Architect","QA"] },
    { label: "知识库", to: "/search", icon: Search, roles: ["Admin","Architect","Developer","Viewer","PM","QA"] },
    { label: "AI 对话", to: "/chat", icon: MessageCircle, roles: ["Admin","Developer"] },
  ]},
  { group: "交付", items: [
    // observability P0：动态徽章 badgeKey="pipelines"（AppSidebar 内 summary 轮询驱动）
    { label: "构建部署", to: "/pipelines", icon: GitMerge, action: "task.read", badgeKey: "pipelines",
      preload: () => { import("@/components/PipelineBoard") } },
  ]},
  { group: "管理", items: [
    { label: "项目", to: "/projects", icon: FolderKanban, action: "project.list" },
    { label: "用户", to: "/admin/users", icon: Users, action: "user.list" },
    { label: "模型提供商", to: "/admin/model-providers", icon: Settings, action: "user.list" },
  ]},
]

// NavUser：sidebar footer 账户块（对齐 shadcn sidebar-07 模式）
function NavUser() {
  const navigate = useNavigate()
  const role = globalRole()
  const user = localStorage.getItem("forja_user") || ""
  const [logoutOpen, setLogoutOpen] = useState(false)
  if (!role) return null

  const logout = () => {
    silenceAuthErrors()
    // S1.2：先吊销服务端 refresh token（静默），再清理本地凭证
    const rt = localStorage.getItem("forja_refresh")
    if (rt) Auth.logout(rt)
    ;["forja_token", "forja_refresh", "forja_role", "forja_user"].forEach(k => localStorage.removeItem(k))
    navigate({ to: "/login" })
    setTimeout(() => {
      queryClient.clear()
      useForjaStore.setState({ projectId: DEFAULT_PROJECT_ID })
      localStorage.removeItem("forja_project_id")
      localStorage.removeItem("forja_project")
    }, 0)
    setLogoutOpen(false)
  }

  const initial = (user[0] || "?").toUpperCase()
  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <button
            type="button"
            className="w-full flex items-center gap-2 px-2 py-1.5 rounded-md text-left text-white/90 hover:bg-white/10 transition-colors"
            title={`${user}（${role}）`}
          >
            <span className="flex size-7 shrink-0 items-center justify-center rounded-full bg-forge-ember text-xs font-semibold text-white">
              {initial}
            </span>
            <NavUserName text={user} sub={role} />
          </button>
        </DropdownMenuTrigger>
        <DropdownMenuContent side="top" align="start" className="w-52">
          <DropdownMenuLabel className="flex items-center justify-between gap-2">
            <span className="truncate">{user}</span>
            <Badge variant="secondary" className="text-[10px] shrink-0">{role}</Badge>
          </DropdownMenuLabel>
          <DropdownMenuSeparator />
          <DropdownMenuItem onClick={() => navigate({ to: "/profile" })}>
            <UserCircle className="h-4 w-4" /> 个人中心
          </DropdownMenuItem>
          <DropdownMenuItem onClick={() => setLogoutOpen(true)} className="text-destructive focus:text-destructive">
            <LogOut className="h-4 w-4" /> 退出登录
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      <Dialog open={logoutOpen} onOpenChange={setLogoutOpen}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle>退出登录</DialogTitle>
            <DialogDescription>确定要退出当前账号吗？未保存的本地状态将被清除。</DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setLogoutOpen(false)}>取消</Button>
            <Button variant="destructive" onClick={logout}>退出</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}

// 折叠态只显示头像；展开态显示用户名+角色
function NavUserName({ text, sub }: { text: string; sub: string }) {
  const { collapsed } = useSidebar()
  if (collapsed) return null
  return (
    <span className="flex flex-col min-w-0">
      <span className="text-sm font-medium truncate leading-tight">{text}</span>
      <span className="text-[10px] text-white/60 truncate leading-tight">{sub}</span>
    </span>
  )
}

export function AppSidebar() {
  const navigate = useNavigate()
  const state = useRouterState()
  const pathname = state.location.pathname
  const role = globalRole()
  const { collapsed } = useSidebar()
  const qc = useQueryClient()
  const projectId = useForjaStore((s) => s.projectId)

  // observability P0 §5.2：流水线动态徽章（30s 轮询兜底）
  const pipelinesSummary = useQuery({
    queryKey: ["pipelines", "summary"],
    queryFn: () => Pipelines.summary(),
    refetchInterval: 30_000,
  })
  const running = pipelinesSummary.data?.running ?? 0
  const failedToday = pipelinesSummary.data?.failed_today ?? 0
  // S6：待构建计数并入徽章（push 已登记等人工构建）
  const pendingBuilds = pipelinesSummary.data?.pending_builds ?? 0

  // P1 真 SSE：订阅当前项目频道，ci.*/repo.push 即时失效 summary（轮询降为兜底）
  // U-4：ci.progress 同步失效，保证列表 running 态与徽章即时更新
  useEventSource(can("task.read") ? Projects.eventsURL(projectId) : null, (m) => {
    if (m.name === "ci.completed" || m.name === "ci.progress" || m.name === "repo.push") {
      qc.invalidateQueries({ queryKey: ["pipelines", "summary"] })
      qc.invalidateQueries({ queryKey: ["pipelines"] })
    }
  })

  // observability P2：Inbox NEW 动态计数（复用列表接口 status=NEW 取 total，60s 轮询兜底）
  // silent：非成员项目上下文下 403 属预期，后台轮询不得弹全局 toast（审计修复）
  const inboxNewQuery = useQuery({
    queryKey: ["inbox", "new-count", projectId],
    queryFn: () => InboxApi.list(projectId, "NEW", 1, 1, { silent: true }),
    enabled: can("inbox.read"),
    refetchInterval: 60_000,
  })
  const inboxNewCount = inboxNewQuery.data?.total ?? 0

  // 动态徽章统一在此渲染：badgeKey → NavBadge 计数（Inbox NEW / 流水线 running+failed）
  const renderBadge = (item: any) => {
    if (item.badge) return <Badge variant="secondary" className="text-[10px]">{item.badge}</Badge>
    if (item.badgeKey === "pipelines") {
      return (
        <>
          <NavBadge count={running} spinning />
          {pendingBuilds > 0 && <NavBadge count={pendingBuilds} className="bg-sky-500/15 text-sky-600" />}
          {failedToday > 0 && <DangerDot />}
        </>
      )
    }
    if (item.badgeKey === "inboxNew") {
      return <NavBadge count={inboxNewCount} className="bg-orange-500/15 text-orange-600" />
    }
    return undefined
  }

  const canSee = (item: any) => {
    if (item.action) return can(item.action)
    if (!item.roles) return true
    return item.roles.includes(role) || item.roles.includes("All")
  }

  return (
    <Sidebar className="h-screen shrink-0 overflow-hidden">
      <SidebarHeader className="border-white/10">
        <div className={cn("flex items-center gap-2", collapsed && "justify-center")}>
          <div className="flex size-6 shrink-0 items-center justify-center rounded-md bg-primary text-primary-foreground">
            <Flame className="size-4" />
          </div>
          {!collapsed && <span className="font-bold tracking-widest text-sm">FORJA</span>}
        </div>
      </SidebarHeader>
      <SidebarContent>
        {sidebarNav.map((g: any) => {
          const visibleItems = g.items.filter(canSee)
          if (visibleItems.length === 0) return null
          return (
            <SidebarGroup key={g.group} label={g.group}>
              <SidebarMenu>
                {visibleItems.map((item: any) => {
                  const Icon = item.icon
                  return (
                    <SidebarMenuItem
                      key={item.to}
                      active={pathname === item.to || pathname.startsWith(item.to + "/")}
                      onClick={() => { if (pathname === item.to) return; navigate({ to: item.to }) }}
                      // S1.6 / KI-001：悬停预热懒组件 chunk，消除首击竞态
                      onMouseEnter={() => { item.preload?.() }}
                      icon={Icon ? <Icon className="h-4 w-4 shrink-0" /> : undefined}
                      badge={renderBadge(item)}
                    >
                      {item.label}
                    </SidebarMenuItem>
                  )
                })}
              </SidebarMenu>
            </SidebarGroup>
          )
        })}
      </SidebarContent>
      <SidebarFooter className="border-white/10">
        <NavUser />
      </SidebarFooter>
    </Sidebar>
  )
}
