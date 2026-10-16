import { createRouter, createRoute, createRootRoute, Outlet, redirect, useRouter } from "@tanstack/react-router"
import { SidebarProvider, SidebarInset } from "@/components/ui/sidebar"
import { AppSidebar } from "@/components/layout/AppSidebar"
import { AppHeader } from "@/components/layout/AppHeader"
import { lazy, Suspense, useState } from "react"
import { CommandPalette } from "@/components/layout/CommandPalette"
import { PageLoader } from "@/components/ui/page-loader"
const SpecsPanel = lazy(() => import("@/components/specs/SpecsPanel"))
const TasksBoard = lazy(() => import("@/components/tasks/TasksBoard"))
const GanttPanel = lazy(() => import("@/components/GanttPanel"))
const ForgePanel = lazy(() => import("@/components/forge/ForgePanel"))
const ForgeTaskPicker = lazy(() => import("@/components/forge/ForgePanel").then((m) => ({ default: m.ForgeTaskPicker })))
const Workbench = lazy(() => import("@/components/Workbench"))
const InboxBoard = lazy(() => import("@/components/InboxBoard"))
const ModelProviderDetail = lazy(() => import("@/components/ModelProviderDetail"))
const Metrics = lazy(() => import("@/components/Metrics"))
const ChatPanel = lazy(() => import("@/components/ChatPanel"))
const SearchBoard = lazy(() => import("@/components/SearchBoard"))
const ProjectBoard = lazy(() => import("@/components/ProjectBoard"))
const ProjectDetail = lazy(() => import("@/components/ProjectDetail"))
const UserBoard = lazy(() => import("@/components/UserBoard"))
const UserDetail = lazy(() => import("@/components/UserDetail"))
const FurnaceBoard = lazy(() => import("@/components/FurnaceBoard"))
const FurnaceDetail = lazy(() => import("@/components/FurnaceDetail"))
const ModelProviderBoard = lazy(() => import("@/components/ModelProviderBoard"))
const Profile = lazy(() => import("@/components/Profile"))
const PipelineBoard = lazy(() => import("@/components/PipelineBoard"))
const SpecLifecyclePage = lazy(() => import("@/components/specs/SpecLifecyclePage"))
const InboxConvertedPage = lazy(() => import("@/components/inbox/InboxConvertedPage"))
const Fallback = () => <PageLoader />

function checkAuth() {
  const token = localStorage.getItem("forja_token")
  return !!token
}

const rootRoute = createRootRoute({
  component: () => <Outlet />,
})

const layoutRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: "layout",
  component: function Layout() {
    const [cmdOpen, setCmdOpen] = useState(false)
    return (
      <div className="flex h-screen overflow-hidden">
        <CommandPalette open={cmdOpen} onOpenChange={setCmdOpen} />
        <SidebarProvider className="flex h-screen w-full overflow-hidden">
          <AppSidebar />
          <SidebarInset className="flex flex-1 flex-col overflow-hidden min-w-0">
            <AppHeader onOpenSearch={() => setCmdOpen(true)} />
            <main className="flex-1 overflow-auto p-6 bg-background w-full min-w-0">
              <Outlet />
            </main>
          </SidebarInset>
        </SidebarProvider>
      </div>
    )
  },
})

import Login from "./routes/login"

const loginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/login",
  component: Login,
})

const indexRoute = createRoute({
  getParentRoute: () => layoutRoute,
  path: "/",
  beforeLoad: () => { if (!checkAuth()) throw redirect({ to: "/login" }) },
  component: () => <Suspense fallback={<Fallback />}><Workbench /></Suspense>,
})

const inboxRoute = createRoute({ getParentRoute: () => layoutRoute, path: "/inbox", beforeLoad: () => { if (!checkAuth()) throw redirect({ to: "/login" }) }, component: () => <Suspense fallback={<Fallback />}><InboxBoard /></Suspense> })
const specsRoute = createRoute({ getParentRoute: () => layoutRoute, path: "/specs", beforeLoad: () => { if (!checkAuth()) throw redirect({ to: "/login" }) }, component: () => <Suspense fallback={<Fallback />}><SpecsPanel /></Suspense> })
const tasksRoute = createRoute({ getParentRoute: () => layoutRoute, path: "/tasks", beforeLoad: () => { if (!checkAuth()) throw redirect({ to: "/login" }) }, component: () => <Suspense fallback={<Fallback />}><TasksBoard /></Suspense> })
// S1.6 / KI-001：路由级错误兜底——提供手动重试，绝不自动整页 reload
function RouteRetryFallback() {
  const router = useRouter()
  return (
    <div className="flex flex-col items-center justify-center gap-3 p-12 text-center">
      <div className="text-sm font-medium">页面加载失败</div>
      <div className="text-xs text-muted-foreground">资源加载出现异常，可重试或刷新页面</div>
      <div className="flex gap-2">
        <button
          className="inline-flex h-8 items-center rounded-md border px-3 text-xs font-medium hover:bg-accent"
          onClick={() => router.invalidate()}
        >
          重试
        </button>
        <button
          className="inline-flex h-8 items-center rounded-md border px-3 text-xs font-medium hover:bg-accent"
          onClick={() => window.location.reload()}
        >
          刷新页面
        </button>
      </div>
    </div>
  )
}

const ganttRoute = createRoute({
  getParentRoute: () => layoutRoute,
  path: "/gantt",
  beforeLoad: () => { if (!checkAuth()) throw redirect({ to: "/login" }) },
  component: () => <Suspense fallback={<Fallback />}><GanttPanel /></Suspense>,
  errorComponent: RouteRetryFallback,
})
// /forge 索引：任务发现（替代手粘 UUID）；/forge/$taskId：单任务锻造观测（深链可分享）
const forgeIndexRoute = createRoute({ getParentRoute: () => layoutRoute, path: "/forge", beforeLoad: () => { if (!checkAuth()) throw redirect({ to: "/login" }) }, component: () => <Suspense fallback={<Fallback />}><ForgeTaskPicker /></Suspense> })
const forgeDetailRoute = createRoute({ getParentRoute: () => layoutRoute, path: "/forge/$taskId", beforeLoad: () => { if (!checkAuth()) throw redirect({ to: "/login" }) }, component: () => <Suspense fallback={<Fallback />}><ForgePanel /></Suspense>, errorComponent: RouteRetryFallback })
const metricsRoute = createRoute({ getParentRoute: () => layoutRoute, path: "/metrics", beforeLoad: () => { if (!checkAuth()) throw redirect({ to: "/login" }) }, component: () => <Suspense fallback={<Fallback />}><Metrics /></Suspense> })
const chatRoute = createRoute({ getParentRoute: () => layoutRoute, path: "/chat", beforeLoad: () => { if (!checkAuth()) throw redirect({ to: "/login" }) }, component: () => <Suspense fallback={<Fallback />}><ChatPanel /></Suspense> })
const searchRoute = createRoute({ getParentRoute: () => layoutRoute, path: "/search", beforeLoad: () => { if (!checkAuth()) throw redirect({ to: "/login" }) }, component: () => <Suspense fallback={<Fallback />}><SearchBoard /></Suspense> })
const projectsRoute = createRoute({ getParentRoute: () => layoutRoute, path: "/projects", beforeLoad: () => { if (!checkAuth()) throw redirect({ to: "/login" }) }, component: () => <Suspense fallback={<Fallback />}><ProjectBoard /></Suspense> })
// §2e+ CONVERTED subpage: route /projects/:projectId/inbox/converted (frontend/src/router.tsx:105)
const inboxConvertedRoute = createRoute({ getParentRoute: () => layoutRoute, path: "/projects/$projectId/inbox/converted", beforeLoad: () => { if (!checkAuth()) throw redirect({ to: "/login" }) }, component: () => <Suspense fallback={<Fallback />}><InboxConvertedPage /></Suspense> })
const projectDetailRoute = createRoute({ getParentRoute: () => layoutRoute, path: "/projects/$projectId", beforeLoad: () => { if (!checkAuth()) throw redirect({ to: "/login" }) }, component: () => <Suspense fallback={<Fallback />}><ProjectDetail /></Suspense> })
const adminUsersRoute = createRoute({ getParentRoute: () => layoutRoute, path: "/admin/users", beforeLoad: () => { if (!checkAuth()) throw redirect({ to: "/login" }) }, component: () => <Suspense fallback={<Fallback />}><UserBoard /></Suspense> })
const furnacesRoute = createRoute({ getParentRoute: () => layoutRoute, path: "/admin/furnaces", beforeLoad: () => { if (!checkAuth()) throw redirect({ to: "/login" }) }, component: () => <Suspense fallback={<Fallback />}><FurnaceBoard /></Suspense> })
const furnaceDetailRoute = createRoute({ getParentRoute: () => layoutRoute, path: "/admin/furnaces/$id", beforeLoad: () => { if (!checkAuth()) throw redirect({ to: "/login" }) }, component: () => <Suspense fallback={<Fallback />}><FurnaceDetail /></Suspense> })
const modelProvidersRoute = createRoute({ getParentRoute: () => layoutRoute, path: "/admin/model-providers", beforeLoad: () => { if (!checkAuth()) throw redirect({ to: "/login" }) }, component: () => <Suspense fallback={<Fallback />}><ModelProviderBoard /></Suspense> })
const modelProviderDetailRoute = createRoute({ getParentRoute: () => layoutRoute, path: "/admin/model-providers/$id", beforeLoad: () => { if (!checkAuth()) throw redirect({ to: "/login" }) }, component: () => <Suspense fallback={<Fallback />}><ModelProviderDetail /></Suspense> })
const userDetailRoute = createRoute({ getParentRoute: () => layoutRoute, path: "/admin/users/$id", beforeLoad: () => { if (!checkAuth()) throw redirect({ to: "/login" }) }, component: () => <Suspense fallback={<Fallback />}><UserDetail /></Suspense> })
const profileRoute = createRoute({ getParentRoute: () => layoutRoute, path: "/profile", beforeLoad: () => { if (!checkAuth()) throw redirect({ to: "/login" }) }, component: () => <Suspense fallback={<Fallback />}><Profile /></Suspense> })
// observability P0：构建部署看板
const pipelinesRoute = createRoute({ getParentRoute: () => layoutRoute, path: "/pipelines", beforeLoad: () => { if (!checkAuth()) throw redirect({ to: "/login" }) }, component: () => <Suspense fallback={<Fallback />}><PipelineBoard /></Suspense> })
// observability P2：Spec 生命周期页（深链可分享）
const specDetailRoute = createRoute({ getParentRoute: () => layoutRoute, path: "/specs/$specId", beforeLoad: () => { if (!checkAuth()) throw redirect({ to: "/login" }) }, component: () => <Suspense fallback={<Fallback />}><SpecLifecyclePage /></Suspense> })

const routeTree = rootRoute.addChildren([loginRoute, layoutRoute.addChildren([indexRoute, inboxRoute, specsRoute, specDetailRoute, tasksRoute, ganttRoute, forgeIndexRoute, forgeDetailRoute, metricsRoute, chatRoute, searchRoute, projectsRoute, inboxConvertedRoute, projectDetailRoute, adminUsersRoute, furnacesRoute, furnaceDetailRoute, modelProvidersRoute, modelProviderDetailRoute, userDetailRoute, profileRoute, pipelinesRoute])])

export const router = createRouter({ routeTree, defaultErrorComponent: RouteRetryFallback })
