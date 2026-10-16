import { useEffect, useState } from "react"
import { useNavigate } from "@tanstack/react-router"
import { useForm } from "react-hook-form"
import { zodResolver } from "@hookform/resolvers/zod"
import { z } from "zod"
import { toast } from "sonner"
import { api, Furnaces } from "@/lib/api"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Badge } from "@/components/ui/badge"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { ModelCombobox } from "@/components/ModelCombobox"
import { Plus, RefreshCw, Search, Trash2, Power, Loader2, Factory, Pencil, FlaskConical, RotateCcw } from "lucide-react"
import { ConfirmDialog, type ConfirmState } from "@/components/shared/ConfirmDialog"
import { useMinLoading } from "@/hooks/useMinLoading"
import { Empty, EmptyHeader, EmptyMedia, EmptyTitle, EmptyDescription } from "@/components/ui/empty"

const schema = z.object({
  name: z.string().min(1, "名称必填"),
  base_url: z.string().optional(),
  capacity: z.coerce.number().min(1).max(20).optional(),
  labels: z.string().optional(),
  project_id: z.string().optional(),
  model_id: z.string().optional(),
}).superRefine((v, ctx) => {
  if (!v.base_url && !v.project_id) ctx.addIssue({ code: "custom", path: ["base_url"], message: "共享池炉需填写 Base URL（默认 http://opencode:4096），专炉可留空自动生成" })
})
type Form = z.infer<typeof schema>

export default function FurnaceBoard() {
  const navigate = useNavigate()
  const [items, setItems] = useState<any[]>([])
  const [providers, setProviders] = useState<any[]>([])
  const [open, setOpen] = useState(false)
  const [editing, setEditing] = useState<any>(null)
  const [selectedModel, setSelectedModel] = useState("")
  const [testingId, setTestingId] = useState<string | null>(null)
  const [previewTesting, setPreviewTesting] = useState(false)
  const [previewResult, setPreviewResult] = useState<any>(null)
  const [q, setQ] = useState("")
  const [loading, setLoading] = useState(false)
  const showLoading = useMinLoading(loading, 400)
  const [confirmState, setConfirmState] = useState<ConfirmState | null>(null)
  const form = useForm<Form>({ resolver: zodResolver(schema) as any, defaultValues: { name: "", base_url: "http://opencode:4096", capacity: 2, labels: "go,react", project_id: "", model_id: "" } })
  const load = async (query = q) => {
    if (loading) return
    setLoading(true)
    try {
      const r: any = await Furnaces.list()
      let list = r.items || []
      if (query) {
        const lower = query.toLowerCase()
        list = list.filter((f: any) => f.name?.toLowerCase().includes(lower) || f.base_url?.toLowerCase().includes(lower))
      }
      setItems(list)
    } catch (e: any) { toast.error(e.message) } finally { setLoading(false) }
  }
  const loadProviders = async () => {
    try { const r: any = await api(`/api/v1/model-providers`); setProviders(r.items || []) } catch {}
  }
  useEffect(() => { load(""); loadProviders() }, [])
  const openCreate = () => {
    setEditing(null)
    setSelectedModel("")
    form.reset({ name: "", base_url: "http://opencode:4096", capacity: 2, labels: "go,react", project_id: "", model_id: "" })
    setOpen(true)
  }
  const openEdit = (f: any) => {
    setEditing(f)
    const labels = Array.isArray(f.labels) ? f.labels.join(",") : (f.labels || "")
    const model = f.model_profile?.model || ""
    setSelectedModel(model)
    form.reset({ name: f.name || "", base_url: f.base_url || "", capacity: f.capacity || 2, labels, project_id: f.project_id || "", model_id: model })
    setOpen(true)
  }
  const submit = async (v: Form) => {
    try {
      const payload: any = { name: v.name, base_url: v.base_url, capacity: v.capacity }
      if (selectedModel) {
        payload.model_profile = { model: selectedModel }
        const prov = providers.find((p: any) => (p.models || []).some((m: any) => m.model_id === selectedModel))
        if (prov) payload.model_profile.provider_id = prov.provider_id
      } else {
        payload.model_profile = null
      }
      if (v.labels) payload.labels = v.labels.split(/[,，\n]/).map((s: string) => s.trim()).filter(Boolean)
      else payload.labels = []
      if (v.project_id) payload.project_id = v.project_id.trim()
      else payload.project_id = null
      if (editing) {
        await Furnaces.patch(editing.id, payload)
        toast.success("已更新")
      } else {
        await Furnaces.create(payload)
        toast.success("已创建")
      }
      setOpen(false); setEditing(null); setSelectedModel(""); load()
    } catch (e: any) { toast.error(e.message) }
  }
  const removeFurnace = (f: any) => {
    setConfirmState({
      title: `删除炉 ${f.name}`,
      description: f.id === "forja-default" ? "默认炉禁止删除" : "该操作不可恢复。",
      confirmText: "删除",
      danger: true,
      action: async () => {
        try { await Furnaces.remove(f.id); toast.success(`已删除 ${f.name}`); load() } catch (e: any) { toast.error(e.message); throw e }
      },
    })
  }
  const recoverFurnace = async (f: any) => {
    try {
      const r: any = await Furnaces.recover(f.id)
      const data = r.data || r
      toast.success(`已恢复 ${f.name}（${data.status}，熔断计数清零）`)
      load()
    } catch (e: any) { toast.error(e.message) }
  }
  const testFurnace = async (f: any) => {
    setTestingId(f.id)
    try {
      const r: any = await Furnaces.testChat(f.id, { prompt: "ping" })
      const data = r.data || r
      if (data.status === "healthy") toast.success(`可用 ${data.latency_ms}ms ${data.model || ""}`)
      else toast.error(data.detail || "不可用")
      load()
    } catch (e: any) { toast.error(e.message) } finally { setTestingId(null) }
  }
  const testPreview = async () => {
    const vals = form.getValues()
    if (!vals.base_url) { toast.error("请先填写 Base URL"); return }
    if (!selectedModel) { toast.error("请先选择模型"); return }
    setPreviewTesting(true); setPreviewResult(null)
    try {
      const prov = providers.find((p: any) => (p.models || []).some((m: any) => m.model_id === selectedModel))
      const body: any = { base_url: vals.base_url, provider_id: prov?.provider_id, model: selectedModel, prompt: "ping" }
      const r: any = await Furnaces.testPreviewChat(body)
      const data = r.data || r
      setPreviewResult(data)
      if (data.status === "healthy") toast.success(`预览可用 ${data.latency_ms}ms`)
      else toast.error(data.detail || "不可用")
    } catch (e: any) { toast.error(e.message); setPreviewResult({ status: "unhealthy", detail: (e as any).message }) } finally { setPreviewTesting(false) }
  }
  return (
    <div className="space-y-4 w-full">
      <Card>
        <CardHeader className="pb-2 flex-row items-center">
          <CardTitle className="text-sm">炉池 ({items.length})</CardTitle>
          <div className="ml-auto flex gap-2 items-center">
            <div className="relative">
              <Search className="h-3.5 w-3.5 absolute left-2.5 top-1/2 -translate-y-1/2 text-muted-foreground" />
              <Input value={q} onChange={e => setQ(e.target.value)} onKeyDown={e => { if (e.key === "Enter") load() }} placeholder="名称或URL" className="h-8 w-56 pl-7" />
            </div>
            <Button size="sm" variant="outline" onClick={() => load()} disabled={showLoading} className="h-8">{showLoading ? <Loader2 className="h-4 w-4 animate-spin" /> : null}查询</Button>
            <Button size="sm" variant="forge" onClick={openCreate} className="h-8"><Plus className="h-4 w-4" /> 新增</Button>
            <Button size="sm" variant="ghost" onClick={() => load()} title="刷新" disabled={showLoading} className="h-8 w-8 p-0">{showLoading ? <Loader2 className="h-4 w-4 animate-spin" /> : <RefreshCw className="h-4 w-4" />}</Button>
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
              <TableHeader><TableRow><TableHead>名称</TableHead><TableHead>URL</TableHead><TableHead>容量</TableHead><TableHead>成功率</TableHead><TableHead>Token</TableHead><TableHead>状态</TableHead><TableHead>熔断</TableHead><TableHead>项目</TableHead><TableHead>模型</TableHead><TableHead className="text-right">操作</TableHead></TableRow></TableHeader>
              <TableBody>{items.map((f: any) => {
                const m = f.model_profile?.model ? `${f.model_profile.provider_id || "forja"}/${f.model_profile.model}` : (f.id==="forja-default" ? "forja/deepseek-v4-flash" : null)
                const rate = typeof f.success_rate === "number" ? f.success_rate : (f.exec_total > 0 ? (f.exec_success ?? 0) / f.exec_total : null)
                const tokens = (f.tokens_in ?? 0) + (f.tokens_out ?? 0)
                return (
                <TableRow key={f.id}><TableCell className="text-xs font-medium"><button className="hover:underline font-mono" onClick={() => navigate({ to: "/admin/furnaces/$id", params: { id: f.id } })}>{f.name}</button></TableCell><TableCell className="text-xs truncate max-w-[150px]">{f.base_url}</TableCell><TableCell className="text-xs">{f.busy ?? 0}/{f.capacity}</TableCell><TableCell className="text-xs">{rate === null || rate === undefined ? <span className="text-muted-foreground">—</span> : `${f.exec_success ?? 0}/${f.exec_total ?? 0} ${(rate * 100).toFixed(0)}%`}</TableCell><TableCell className="text-xs" title={`in:${f.tokens_in ?? 0} out:${f.tokens_out ?? 0}`}>{f.exec_total ? tokens : <span className="text-muted-foreground">—</span>}</TableCell><TableCell><Badge variant={f.status==="healthy"?"success": f.status==="drained"?"secondary":"destructive"} className="text-[10px]">{f.status}</Badge></TableCell><TableCell>{(f.consecutive_failures ?? 0) === 0 ? <span className="text-muted-foreground text-xs">—</span> : <Badge variant={f.breaker_tripped ? "destructive" : "secondary"} className="text-[10px]" title={f.breaker_tripped ? "连续3次失败，已熔断" : "连续失败计数（3次熔断）"}>{f.breaker_tripped ? "已熔断" : `连败${f.consecutive_failures}次`}</Badge>}</TableCell><TableCell className="text-xs">{f.project_id ? <Badge variant="secondary" className="text-[10px]">{f.project_id.slice(0,8)}</Badge> : <span className="text-muted-foreground">共享池</span>}</TableCell><TableCell className="text-xs truncate max-w-[120px]">{m ? m : <span className="text-muted-foreground">—</span>}</TableCell><TableCell className="text-right"><div className="flex gap-1 justify-end"><Button size="sm" variant="ghost" className="h-6 w-6 p-0" title="编辑" onClick={() => openEdit(f)}><Pencil className="h-3.5 w-3.5" /></Button><Button size="sm" variant="ghost" className="h-6 w-6 p-0" title="可用性测试" disabled={testingId===f.id} onClick={() => testFurnace(f)}>{testingId===f.id ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <FlaskConical className="h-3.5 w-3.5" />}</Button>{((f.consecutive_failures ?? 0) > 0 || f.status === "unhealthy") && <Button size="sm" variant="ghost" className="h-6 w-6 p-0" title="手动恢复（清零熔断计数并拉起容器）" onClick={() => recoverFurnace(f)}><RotateCcw className="h-3.5 w-3.5" /></Button>}<Button size="sm" variant="ghost" className="h-6 px-1.5" title={f.status === "healthy" ? "排水" : "恢复"} onClick={async () => { try { await Furnaces.patch(f.id, { status: f.status === "healthy" ? "drained" : "healthy" }); load() } catch (e: any) { toast.error(e.message) } }}><Power className="h-3.5 w-3.5" /></Button><Button size="sm" variant="ghost" className="h-6 w-6 p-0 text-destructive hover:text-destructive" title="删除" onClick={() => removeFurnace(f)}><Trash2 className="h-3.5 w-3.5" /></Button></div></TableCell></TableRow>
                )
              })}</TableBody>
            </Table>
            {!showLoading && items.length === 0 && (
              <Empty className="py-10 mt-2">
                <EmptyHeader>
                  <EmptyMedia><Factory className="size-4" /></EmptyMedia>
                  <EmptyTitle>{q ? "未找到匹配炉" : "空池回落 env 单炉"}</EmptyTitle>
                  <EmptyDescription>{q ? `关键词 “${q}” 无结果` : "暂无炉，空表回落 env 单炉（零破坏）"}</EmptyDescription>
                </EmptyHeader>
                {q ? (
                  <Button size="sm" variant="outline" className="h-8 mt-2" onClick={() => { setQ(""); load("") }}>清除筛选</Button>
                ) : (
                  <Button size="sm" variant="forge" className="h-8 mt-2" onClick={openCreate}><Plus className="h-4 w-4" />新增炉</Button>
                )}
              </Empty>
            )}
          </div>
        </CardContent>
      </Card>
      <ConfirmDialog state={confirmState} onClose={() => setConfirmState(null)} />
      <Dialog open={open} onOpenChange={(o)=>{ if(!o){ setEditing(null); form.reset(); setSelectedModel(""); setPreviewResult(null)} setOpen(o)}}><DialogContent className="max-w-md"><DialogHeader><DialogTitle>{editing ? "编辑炉" : "新增炉"}</DialogTitle></DialogHeader><form onSubmit={form.handleSubmit(submit)} className="flex flex-col gap-3"><div className="flex flex-col gap-2"><label className="text-xs">名称 *</label><Input {...form.register("name")} className="h-8" placeholder="forge-go-1" /><span className="text-xs text-destructive">{form.formState.errors.name?.message}</span></div><div className="flex flex-col gap-2"><label className="text-xs">Base URL（共享池必填，专炉留空自动 forja-furnace-&lt;id8&gt;:4096）</label><Input {...form.register("base_url")} className="h-8" placeholder="http://opencode:4096" /><span className="text-xs text-destructive">{form.formState.errors.base_url?.message}</span></div><div className="flex gap-2"><div className="flex flex-col gap-2 flex-1"><label className="text-xs">容量 max_concurrent</label><Input type="number" {...form.register("capacity")} className="h-8" /></div><div className="flex flex-col gap-2 flex-1"><label className="text-xs">绑定项目ID(空=共享池)</label><Input {...form.register("project_id")} className="h-8" placeholder="可选" /></div></div><div className="flex flex-col gap-2"><label className="text-xs">标签 labels（逗号分隔，对应 project.yaml type/stack）</label><Input {...form.register("labels")} className="h-8" placeholder="go,react" /></div><div className="flex flex-col gap-2"><label className="text-xs">模型</label><ModelCombobox providers={providers} value={selectedModel} onChange={setSelectedModel} /><div className="flex gap-2 items-center"><Button size="sm" variant="outline" type="button" onClick={testPreview} disabled={previewTesting} className="h-7">{previewTesting ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <FlaskConical className="h-3.5 w-3.5" />} 预览测试</Button>{previewResult && <Badge variant={previewResult.status==="healthy"?"success":"destructive"} className="text-[10px]">{previewResult.status} {previewResult.latency_ms ? `${previewResult.latency_ms}ms` : ""}</Badge>}</div>{previewResult && <div className="text-xs p-2 border rounded bg-muted/20 break-all">{previewResult.reply || previewResult.detail || ""}</div>}</div><Button size="sm" variant="forge" type="submit" disabled={form.formState.isSubmitting}>{editing ? "保存" : "创建"}</Button></form></DialogContent></Dialog>
    </div>
  )
}
