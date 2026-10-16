import { useEffect, useState } from "react"
import { Specs, Projects, api } from "@/lib/api"
import { useForjaStore } from "@/lib/store"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Badge } from "@/components/ui/badge"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { History, Loader2, RefreshCw } from "lucide-react"
import { toast } from "sonner"
import { useMinLoading } from "@/hooks/useMinLoading"

// S2/B1：演进时间线——spec_versions 全量按时间倒序（ALL=跨项目成员内）
function Timeline() {
  const [items, setItems] = useState<any[]>([])
  const [msg, setMsg] = useState("")
  const [loading, setLoading] = useState(false)
  const showLoading = useMinLoading(loading, 400)

  const load = async () => {
    if (loading) return
    setLoading(true)
    try {
      const r: any = await api(`/api/v1/specs/timeline?project_id=ALL&limit=100`)
      setItems(r.items || [])
      setMsg(`共 ${r.total ?? items.length} 个版本`)
    } catch (e: any) { toast.error(e.message) } finally { setLoading(false) }
  }
  useEffect(() => { load() }, [])

  return (
    <div className="space-y-2">
      <div className="flex items-center gap-2">
        <Button size="sm" variant="outline" onClick={load} disabled={showLoading}>{showLoading ? <Loader2 className="size-3.5 animate-spin mr-1" /> : <RefreshCw className="size-3.5 mr-1" />}刷新</Button>
        <span className="text-xs text-muted-foreground">{msg}</span>
      </div>
      <div className="relative">
        {showLoading && (
          <div className="absolute inset-0 bg-background/60 backdrop-blur-[1px] z-10 flex items-center justify-center rounded-lg">
            <span className="inline-flex items-center gap-2 text-xs text-muted-foreground bg-background border rounded-full px-3 py-1.5 shadow-sm">
              <Loader2 className="h-3.5 w-3.5 animate-spin" /> 正在刷新…
            </span>
          </div>
        )}
        <div className={showLoading ? "opacity-60 pointer-events-none transition-opacity pl-4 border-l border-border/60 space-y-3" : "pl-4 border-l border-border/60 space-y-3 transition-opacity"}>
        {items.map((v: any) => (
          <div key={v.version_id} className="relative">
            <span className="absolute -left-[21px] top-1.5 h-2 w-2 rounded-full bg-forge-ember ring-2 ring-background" />
            <Card>
              <CardContent className="pt-3 pb-3 flex flex-wrap items-center gap-2">
                <Badge variant="outline" className="text-[10px] font-mono">{v.layer} {v.version}</Badge>
                <span className="text-sm font-medium">{v.title}</span>
                {v.change_log && <span className="text-xs text-muted-foreground truncate max-w-[420px]">{v.change_log}</span>}
                <span className="ml-auto text-[10px] text-muted-foreground">{v.created_at ? new Date(v.created_at).toLocaleString("zh-CN", { hour12: false }) : ""}</span>
              </CardContent>
            </Card>
          </div>
        ))}
        {!items.length && !msg.startsWith("共") && <div className="text-xs text-muted-foreground">暂无演进记录</div>}
        </div>
      </div>
    </div>
  )
}

export function SearchBoard() {
  const initialQ = new URLSearchParams(window.location.search).get("q") || "Todo"
  const projectId = useForjaStore(s => s.projectId)
  const [q, setQ] = useState(initialQ)
  const [project, setProject] = useState(projectId)
  const [projects, setProjects] = useState<any[]>([])
  const [items, setItems] = useState<any[]>([])
  const [msg, setMsg] = useState("")

  useEffect(() => {
    Projects.list().then((r: any) => setProjects(r.items || [])).catch(() => {})
  }, [])
  useEffect(() => { setProject(projectId) }, [projectId])

  const search = async (term = q) => {
    if (!term.trim()) { toast.warning("请输入关键词"); return }
    try {
      const r: any = await Specs.search(term, project)
      setItems(r.items || [])
      setMsg(`找到 ${r.items?.length || 0} 条`)
    } catch (e: any) {
      toast.error(e.message)
    }
  }

  useEffect(() => { if (initialQ !== "Todo") search(initialQ) }, [])

  return (
    <Tabs defaultValue="search" className="w-full">
      <TabsList className="mb-3">
        <TabsTrigger value="search">语义检索</TabsTrigger>
        <TabsTrigger value="timeline"><History className="h-3.5 w-3.5 mr-1" />演进时间线</TabsTrigger>
      </TabsList>

      <TabsContent value="search" className="space-y-4">
        <Card>
          <CardHeader className="pb-2"><CardTitle className="text-sm">知识库语义检索 — pgvector 1536 余弦</CardTitle></CardHeader>
          <CardContent className="flex gap-2">
            <Input value={q} onChange={e=>setQ(e.target.value)} placeholder="关键词，如 Todo / 批量导入" className="flex-1" />
            <select value={project} onChange={e=>setProject(e.target.value)} className="border rounded-md px-2 py-2 text-sm bg-background">
              {projects.length
                ? projects.map((p: any) => <option key={p.id} value={p.id}>{p.name}</option>)
                : <option value={project}>当前项目</option>}
            </select>
            <Button variant="forge" onClick={() => search()}>检索</Button>
          </CardContent>
        </Card>
        <div className="grid gap-2">
          {items.map((it:any) => (
            <Card key={it.id}>
              <CardContent className="pt-4 flex justify-between items-center">
                <div>
                  <div className="font-medium text-sm">{it.title}</div>
                  <div className="text-xs text-muted-foreground font-mono">{it.id.slice(0,8)} 距离 {it.distance?.toFixed(3)}</div>
                </div>
                <Badge variant={it.distance < 0.5 ? "success" : it.distance < 0.8 ? "secondary" : "outline"}>{it.distance < 0.5 ? "高相关" : it.distance < 0.8 ? "相关" : "弱相关"}</Badge>
              </CardContent>
            </Card>
          ))}
          {items.length===0 && <div className="text-xs text-muted-foreground">输入关键词后检索当前项目知识库</div>}
        </div>
        {msg && <div className="text-xs text-muted-foreground">{msg}</div>}
        <div className="text-xs text-muted-foreground">背后：`vector.StoreSpecEmbedding` mock 1536 + `ivfflat` 索引 + `GET /specs/search?project_id=&amp;q=` 项目内余弦排序</div>
      </TabsContent>

      <TabsContent value="timeline">
        <Timeline />
      </TabsContent>
    </Tabs>
  )
}

export default SearchBoard
