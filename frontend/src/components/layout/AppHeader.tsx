import { SidebarTrigger } from "@/components/ui/sidebar"
import { Separator } from "@/components/ui/separator"
import { Button } from "@/components/ui/button"
import { useNavigate } from "@tanstack/react-router"
import { useEffect, useState } from "react"
import { Projects } from "@/lib/api"
import { globalRole } from "@/lib/auth"
import { useForjaStore } from "@/lib/store"
import { useTheme, setTheme, type ThemeMode } from "@/lib/theme"
import { Moon, Monitor, Sun, BookOpen } from "lucide-react"
import { DOCS_BASE } from "@/lib/glossary"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
const themeLabel: Record<ThemeMode, string> = { light: "亮色", dark: "暗色", system: "跟随系统" }
const ThemeIcon = { light: Sun, dark: Moon, system: Monitor }

export function AppHeader({ onOpenSearch }: { onOpenSearch?: () => void }) {
  const navigate = useNavigate()
  const role = globalRole()
  // 单一真相源：选择器直绑 store（2026-09-08 修双源分叉：此前本地 state 读 forja_project
  // 展示、业务查 store 的 forja_project_id，键分叉时"显示与查询不一致"，必须手动重选才好）
  const { projectId, setProjectId } = useForjaStore()
  const { mode: themeMode } = useTheme()
  const CurrentThemeIcon = ThemeIcon[themeMode]
  const [projects, setProjects] = useState<any[]>([])
  useEffect(() => {
    if (role) Projects.list().then((r: any) => setProjects(r.items || [])).catch(() => {})
  }, [role])

  return (
    <header className="h-14 flex items-center gap-2 px-4 border-b shrink-0 bg-card">
      <SidebarTrigger />
      <Separator orientation="vertical" className="h-5 mx-1" />
      <span className="font-bold tracking-widest hidden md:block">FORJA</span>
      <div className="ml-4 flex items-center gap-2">
        <select value={projectId} onChange={e => { setProjectId(e.target.value) }} className="border rounded-md h-8 px-2 text-sm bg-background min-w-[140px]">
          {projects.length
            ? projects.map((p: any) => <option key={p.id} value={p.id}>{p.name}</option>)
            : <option value={projectId} disabled>加载项目…</option>}
        </select>
        <span className="text-xs text-muted-foreground hidden md:block">Project</span>
      </div>
      <div className="ml-auto flex items-center gap-2">
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="sm" title={`主题：${themeLabel[themeMode]}`}>
              <CurrentThemeIcon className="h-4 w-4" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuRadioGroup value={themeMode} onValueChange={v => setTheme(v as ThemeMode)}>
              {(Object.keys(themeLabel) as ThemeMode[]).map(m => {
                const Icon = ThemeIcon[m]
                return (
                  <DropdownMenuRadioItem key={m} value={m}>
                    <Icon className="h-4 w-4" /> {themeLabel[m]}
                  </DropdownMenuRadioItem>
                )
              })}
            </DropdownMenuRadioGroup>
          </DropdownMenuContent>
        </DropdownMenu>
        <Button variant="ghost" size="sm" onClick={() => onOpenSearch?.()} className="hidden md:flex">⌘K 搜索</Button>
        <Button variant="ghost" size="sm" className="hidden md:flex" title={`文档站（本地预览 ${DOCS_BASE}）`} asChild>
          <a href={DOCS_BASE} target="_blank" rel="noreferrer"><BookOpen className="h-4 w-4" />文档</a>
        </Button>
        {!role && <Button size="sm" onClick={() => navigate({ to: "/login" })}>登录</Button>}
      </div>
    </header>
  )
}
