import { useEffect, useRef, useState } from "react"
import { useNavigate } from "@tanstack/react-router"
import { toast } from "sonner"
import { RefreshCw, KanbanSquare, Inbox as InboxIcon, FileText, CircleCheckBig, ExternalLink, Loader2 } from "lucide-react"
import { useMinLoading } from "@/hooks/useMinLoading"
import { useForm } from "react-hook-form"
import { zodResolver } from "@hookform/resolvers/zod"
import * as z from "zod"
import { Inbox } from "@/lib/api"
import { useForjaStore } from "@/lib/store"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Badge } from "@/components/ui/badge"
import { Checkbox } from "@/components/ui/checkbox"
import { Textarea } from "@/components/ui/textarea"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from "@/components/ui/dialog"
import { Separator } from "@/components/ui/separator"
import { Empty, EmptyHeader, EmptyMedia, EmptyTitle, EmptyDescription } from "@/components/ui/empty"
import { relativeTime } from "@/lib/format"
import { InboxDetailDialog } from "@/components/inbox/InboxDetailDialog"

const draftSchema = z.object({
  title: z.string().min(1, "标题必填"),
  acText: z.string().min(1, "AC 不能为空").refine((v) => {
    try { const j = JSON.parse(v); return Array.isArray(j) && j.length > 0 } catch { return false }
  }, "AC 需为合法 JSON 数组"),
})
type DraftForm = z.infer<typeof draftSchema>

function absoluteTime(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleString()
}

// v2.0 M3：UAT 卡片结论着色（raw_content 首行由后端注入【UAT｜结论｜tag…】）
function uatResultOf(it: any): string | null {
  if (it?.source_type !== "UAT") return null
  const m = /【UAT｜(通过|部分通过|不通过)/.exec(it.snippet || "")
  return m ? m[1] : null
}

function uatBadgeVariant(result: string | null): "success" | "warning" | "destructive" | "outline" {
  if (result === "通过") return "success"
  if (result === "部分通过") return "warning"
  if (result === "不通过") return "destructive"
  return "outline"
}

export function InboxBoard() {
  const [items, setItems] = useState<any[]>([])
  const [raw, setRaw] = useState("")
  const [file, setFile] = useState<File | null>(null)
  const [selected, setSelected] = useState<string[]>([])
  const [srcFilter, setSrcFilter] = useState<string>("ALL")
  const [editing, setEditing] = useState<any>(null)
  const [editRaw, setEditRaw] = useState("")
  const [saving, setSaving] = useState(false)
  const projectId = useForjaStore(s => s.projectId)
  const navigate = useNavigate()

  // draft dialog state
  const [draft, setDraft] = useState<any>(null)
  const [draftId, setDraftId] = useState("")
  const [draftOpen, setDraftOpen] = useState(false)
  const rawRef = useRef<HTMLTextAreaElement>(null)

  // detail dialog state (reused for kanban cards and subpage) (frontend/src/components/InboxBoard.tsx:38)
  const [detailId, setDetailId] = useState<string | null>(null)
  const [detailOpen, setDetailOpen] = useState(false)
  const [loading, setLoading] = useState(false)
  const showLoading = useMinLoading(loading, 400)

  const draftForm = useForm<DraftForm>({
    resolver: zodResolver(draftSchema),
    defaultValues: { title: "", acText: "[]" },
  })

  const load = async () => {
    if (loading) return
    setLoading(true)
    try { const r: any = await Inbox.list(projectId); setItems(r.items || []); } catch (e: any) { toast.error(e.message) } finally { setLoading(false) }
  }
  useEffect(() => { load() }, [projectId])

  const create = async () => {
    if (!file && !raw.trim()) { rawRef.current?.focus(); toast.warning("请填写需求内容或选择文件后再入堆"); return }
    try {
      if (file) {
        const r: any = await Inbox.createFile(file)
        toast.success(`已入堆 ${r.id} (${file.name})`); setFile(null); setRaw(""); load()
      } else {
        const r: any = await Inbox.create({ raw_content: raw.trim(), source_type: "TEXT_PASTE" }); toast.success(`已入堆 ${r.id}`); setRaw(""); load()
      }
    } catch (e: any) { toast.error(e.message) }
  }

  const openDraftDialog = (d: any, id: string) => {
    setDraft(d); setDraftId(id)
    draftForm.reset({ title: d.title || "", acText: JSON.stringify(d.acceptance_criteria || [], null, 2) })
    setDraftOpen(true)
  }

  const doDraft = async (id: string) => {
    try {
      const r: any = await Inbox.draft(id)
      openDraftDialog(r, id)
      load()
    } catch (e: any) { toast.error(e.message) }
  }

  const toggle = (id: string) => setSelected(s => s.includes(id) ? s.filter(x=>x!==id) : [...s, id])
  const openEdit = async (id: string) => {
    try {
      const r: any = await Inbox.get(id)
      setEditing(r); setEditRaw(r.raw_content || "")
    } catch (e: any) { toast.error(e.message) }
  }
  const saveEdit = async () => {
    if (!editing) return
    if (!editRaw.trim()) { toast.warning("内容不能为空"); return }
    setSaving(true)
    try {
      await Inbox.update(editing.id, { raw_content: editRaw.trim() }, { silent: true })
      toast.success("已保存"); setEditing(null); load()
    } catch (e: any) { toast.error(e.message) } finally { setSaving(false) }
  }
  const doDelete = async (id: string) => {
    if (!window.confirm("确定删除该原料？删除后不可恢复。")) return
    try { await Inbox.remove(id, { silent: true }); toast.success("已删除"); setSelected(s => s.filter(x=>x!==id)); load() } catch (e: any) { toast.error(e.message) }
  }

  const convert = async (ids?: string[]) => {
    const target = ids || (selected.length ? selected : (draftId ? [draftId] : []))
    if (!target.length) { toast.warning("先选原料或生成草稿"); return }
    try { const r: any = await Inbox.convert({ requirement_ids: target }); toast.success(`已转 BS ${r.id}`); setSelected([]); setDraft(null); setDraftOpen(false); load() } catch (e: any) { toast.error(e.message) }
  }

  const handleDraftConvert = draftForm.handleSubmit(async (vals) => {
    let ac: any = []
    try { ac = JSON.parse(vals.acText) } catch { toast.error("AC JSON 解析失败"); return }
    if (!draftId) return
    try {
      const r: any = await Inbox.convert({ requirement_ids: [draftId], title: vals.title, acceptance_criteria: ac })
      toast.success(`已转 BS ${r.id}`)
      setDraftOpen(false); setDraft(null); setDraftId(""); load()
    } catch (e: any) { toast.error(e.message) }
  })

  const openDetail = (id: string) => { setDetailId(id); setDetailOpen(true) }

  // v2.0 M3：来源筛选（UAT 优先展示，另含实际出现的来源）
  const sourceOptions = Array.from(new Set(items.map((x: any) => x.source_type).filter(Boolean))) as string[]
  const visible = srcFilter === "ALL" ? items : items.filter((x: any) => x.source_type === srcFilter)
  const cols: Record<string, any[]> = { NEW: [], DRAFTED: [], CONVERTED: [] }
  visible.forEach((x:any) => { const s = x.status in cols ? x.status : "NEW"; cols[s].push(x) })

  // Card meta row: relative time (hover shows absolute) + creator (created_by) (frontend/src/components/InboxBoard.tsx:118)
  const renderMetaRow = (it: any) => {
    const creator = it.creator || (it.created_by ? it.created_by.slice(0, 6) : "—")
    const creatorFull = it.creator || it.created_by || "—"
    const createdAt = it.created_at
    const abs = createdAt ? absoluteTime(createdAt) : "—"
    const rel = createdAt ? relativeTime(createdAt) : "—"
    return (
      <div className="flex items-center gap-1 text-[10px] text-muted-foreground mt-1">
        <span title={abs} className="cursor-help underline decoration-dotted">{rel}</span>
        <span>·</span>
        <span title={creatorFull} className="truncate max-w-[80px]">{creator}</span>
        {it.content_hash && <span title={it.content_hash} className="ml-auto font-mono text-[9px] bg-muted px-1 rounded truncate max-w-[60px]">{it.content_hash.slice(0, 6)}…</span>}
      </div>
    )
  }

  const renderCardActions = (it: any, col: string) => {
    if (col === "NEW") {
      return (
        <div className="flex gap-1 mt-2">
          <Button size="sm" variant="ghost" className="h-6 text-[10px]" onClick={(e)=>{e.stopPropagation(); doDraft(it.id)}}>生成草稿</Button>
          <Button size="sm" variant="ghost" className="h-6 text-[10px]" onClick={(e)=>{e.stopPropagation(); convert([it.id])}}>直接转 BS</Button>
          <Button size="sm" variant="ghost" className="h-6 text-[10px] ml-auto" onClick={(e)=>{e.stopPropagation(); openEdit(it.id)}}>编辑</Button>
          <Button size="sm" variant="ghost" className="h-6 text-[10px] text-forge-danger hover:text-forge-danger" onClick={(e)=>{e.stopPropagation(); doDelete(it.id)}}>删除</Button>
        </div>
      )
    }
    if (col === "DRAFTED") {
      return (
        <div className="flex gap-1 mt-2">
          <Button size="sm" variant="ghost" className="h-6 text-[10px]" onClick={(e)=>{e.stopPropagation(); doDraft(it.id)}}>查看/编辑草稿</Button>
          <Button size="sm" variant="ghost" className="h-6 text-[10px]" onClick={(e)=>{e.stopPropagation(); convert([it.id])}}>转 BS</Button>
          <Button size="sm" variant="ghost" className="h-6 text-[10px] ml-auto" onClick={(e)=>{e.stopPropagation(); doDelete(it.id)}}>删除</Button>
        </div>
      )
    }
    // CONVERTED: 仅展示已转化，不提供草稿/转换/编辑/删除，但提供 Spec 跳链与详情
    return (
      <div className="flex gap-1 mt-2 items-center">
        <Badge variant="success" className="text-[10px]">已转化</Badge>
        {it.spec_id ? (
          <button onClick={(e)=>{e.stopPropagation(); navigate({ to: "/specs/$specId", params: { specId: it.spec_id } as any })}} className="ml-1 text-[10px] text-forge-ember hover:underline flex items-center gap-0.5">
            Spec {it.spec_id.slice(0,6)} <ExternalLink className="size-3" />
          </button>
        ) : (
          <span className="text-[10px] text-muted-foreground ml-1">只读留痕</span>
        )}
        <Button size="sm" variant="ghost" className="h-6 text-[10px] ml-auto" onClick={(e)=>{e.stopPropagation(); openDetail(it.id)}}>详情</Button>
      </div>
    )
  }

  // CONVERTED column mini-card logic: only latest ~10 + header link (frontend/src/components/InboxBoard.tsx:145)
  const convertedMini = cols.CONVERTED.slice(0, 10)
  const convertedOverflow = cols.CONVERTED.length > 10

  return (
    <div className="space-y-4">
      <Card>
        <CardHeader className="pb-2"><CardTitle className="text-sm">Inbox 原料堆 — 熔炼为 BS (支持 .txt/.md/.docx/.pdf ≤5MB)</CardTitle></CardHeader>
        <CardContent className="space-y-2">
          <Textarea ref={rawRef} value={raw} onChange={e=>setRaw(e.target.value)} placeholder="粘贴自然语言需求 (会议纪要/聊天碎片)…" className="resize-y" rows={3} />
          <div className="flex gap-2">
            <Button variant="forge" onClick={create}>入堆</Button>
          </div>
          <div className="flex gap-2 items-center">
            <input type="file" accept=".txt,.md,.docx,.pdf" onChange={e=>setFile(e.target.files?.[0] || null)} className="text-xs" />
            {file && <span className="text-xs">{file.name} {(file.size/1024).toFixed(1)}KB</span>}
            {file && <Button size="sm" variant="ghost" onClick={()=>setFile(null)}>清除文件</Button>}
            <span className="text-xs text-muted-foreground ml-auto">文件优先于文本，≤5MB 自动抽取</span>
          </div>
        </CardContent>
      </Card>
      <div className="relative flex items-center justify-between">
        <div className="flex items-center gap-3">
          <h3 className="text-sm font-semibold flex items-center gap-1.5"><KanbanSquare className="h-4 w-4 text-forge-ember" />原料看板</h3>
          <div className="flex items-center gap-1">
            {[{ k: "ALL", label: "全部" }, ...sourceOptions.map(s => ({ k: s, label: s }))].map(o => (
              <Button key={o.k} size="sm" variant={srcFilter === o.k ? "forge" : "outline"} className="h-6 px-2 text-[10px]" onClick={() => setSrcFilter(o.k)}>{o.label}</Button>
            ))}
          </div>
        </div>
        {selected.length > 0 && (
          <div className="absolute left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 z-10 flex items-center gap-3 rounded-lg border bg-background pl-4 pr-1.5 py-1 text-xs shadow-md animate-in fade-in slide-in-from-bottom-1">
            <span className="font-medium">已选 {selected.length} 项</span>
            <div className="flex gap-1.5">
              <Button size="sm" variant="forge" className="h-6 rounded-md shadow-md shadow-forge-ember/40 transition-all hover:-translate-y-px hover:shadow-lg hover:shadow-forge-ember/50 active:translate-y-0" onClick={()=>convert()}>一键转 BS ({selected.length})</Button>
              <Button size="sm" variant="ghost" className="h-6 rounded-md" onClick={()=>setSelected([])}>取消选择</Button>
            </div>
          </div>
        )}
        <Button size="sm" variant="ghost" onClick={load} disabled={showLoading}>{showLoading ? <Loader2 className="h-3 w-3 animate-spin mr-1" /> : <RefreshCw className="h-3 w-3 mr-1" />}刷新看板</Button>
      </div>
      <div className="relative">
        {showLoading && (
          <div className="absolute inset-0 bg-background/60 backdrop-blur-[1px] z-10 flex items-center justify-center rounded-lg">
            <span className="inline-flex items-center gap-2 text-xs text-muted-foreground bg-background border rounded-full px-3 py-1.5 shadow-sm">
              <Loader2 className="h-3.5 w-3.5 animate-spin" /> 正在刷新…
            </span>
          </div>
        )}
        <div className={showLoading ? "opacity-60 pointer-events-none transition-opacity" : "transition-opacity"}>
        <div className="grid grid-cols-3 gap-3">
        {(["NEW","DRAFTED","CONVERTED"] as const).map(col => (
          <Card key={col}>
            <CardHeader className="pb-2">
              <CardTitle className="text-xs flex justify-between items-center">
                <span>{col}</span>
                <span className="flex items-center gap-2">
                  {col === "CONVERTED" && cols[col].length > 0 && (
                    <button onClick={()=>navigate({ to: "/projects/$projectId/inbox/converted", params: { projectId } as any })} className="text-[10px] text-forge-ember hover:underline">查看全部 ({cols[col].length})</button>
                  )}
                  <Badge variant={col==="NEW"?"secondary":col==="DRAFTED"?"forge":"success"}>{cols[col].length}</Badge>
                </span>
              </CardTitle>
            </CardHeader>
            <CardContent className="space-y-2 min-h-[200px]">
              {(col === "CONVERTED" ? convertedMini : cols[col]).map((it:any) => (
                <div key={it.id} onClick={()=>openDetail(it.id)} className={`border rounded-md p-2 text-xs ${selected.includes(it.id)?"ring-2 ring-orange-300":""} bg-card cursor-pointer hover:border-ring transition-colors`}>
                  <div className="flex gap-1 items-center"><Checkbox checked={selected.includes(it.id)} onCheckedChange={(e)=>{ e; toggle(it.id)}} onClick={(e:any)=>e.stopPropagation()} aria-label="选择原料" /><Badge variant={uatResultOf(it) ? uatBadgeVariant(uatResultOf(it)) : "outline"} className="text-[10px]">{uatResultOf(it) ? `UAT·${uatResultOf(it)}` : it.source_type}</Badge><span className="ml-auto text-[10px] text-muted-foreground">{it.id.slice(0,6)}</span></div>
                  <div className="mt-1 line-clamp-2" title={it.snippet}>{it.snippet}</div>
                  {renderMetaRow(it)}
                  {renderCardActions(it, col)}
                </div>
              ))}
              {col === "CONVERTED" && convertedOverflow && (
                <Button size="sm" variant="outline" className="w-full h-7 text-xs" onClick={()=>navigate({ to: "/projects/$projectId/inbox/converted", params: { projectId } as any })}>查看全部 {cols[col].length} 条 → 子页面</Button>
              )}
              {cols[col].length===0 && (
                <Empty className="py-8">
                  <EmptyHeader>
                    <EmptyMedia>
                      {col==="NEW" ? <InboxIcon className="size-4" /> : col==="DRAFTED" ? <FileText className="size-4" /> : <CircleCheckBig className="size-4" />}
                    </EmptyMedia>
                    <EmptyTitle>{col==="NEW" ? "暂无原料" : col==="DRAFTED" ? "暂无草稿" : "暂无转化"}</EmptyTitle>
                    <EmptyDescription>{col==="NEW" ? "在上方粘贴文本或上传文件入堆" : col==="DRAFTED" ? "对 NEW 原料点击「生成草稿」" : "将原料或草稿「一键转 BS」"}</EmptyDescription>
                  </EmptyHeader>
                </Empty>
              )}
            </CardContent>
            </Card>
        ))}
        </div>
        </div>
      </div>

      {/* Detail Dialog: show all fields (creator/dedup hash/timeline/linked Spec) — reuse same component for kanban cards and subpage (frontend/src/components/InboxBoard.tsx:145) */}
      <InboxDetailDialog id={detailId} open={detailOpen} onOpenChange={(o)=>{ setDetailOpen(o); if(!o) setDetailId(null)}} />

      {/* 草稿 Dialog：可编辑标题/AC，确认即转 BS */}
      <Dialog open={draftOpen} onOpenChange={(o)=>{ setDraftOpen(o); if(!o) { setDraft(null); draftForm.reset() } }}>
        <DialogContent className="max-w-lg">
          <DialogHeader>
            <DialogTitle className="text-sm">草稿 — {draftId.slice(0,6)} {draft && <Badge variant="outline" className="ml-1 text-[10px]">{draft.story_id}</Badge>}</DialogTitle>
            <DialogDescription className="text-xs">标题与 AC 可编辑，确认后直接转 BS；取消可保留在 DRAFTED 列</DialogDescription>
          </DialogHeader>
          <form onSubmit={handleDraftConvert} className="space-y-3">
            <div className="flex flex-col gap-2">
              <Label className="text-xs">标题</Label>
              <Input {...draftForm.register("title")} className="h-8 text-xs" placeholder="输入 BS 标题" />
              {draftForm.formState.errors.title && <span className="text-xs text-destructive">{draftForm.formState.errors.title.message}</span>}
            </div>
            <div className="flex flex-col gap-2">
              <Label className="text-xs">AC（JSON 数组，given/when/then）</Label>
              <Textarea {...draftForm.register("acText")} rows={6} className="text-xs font-mono" />
              {draftForm.formState.errors.acText && <span className="text-xs text-destructive">{draftForm.formState.errors.acText.message}</span>}
            </div>
            {draft && (
              <div className="text-xs space-y-1 bg-muted p-2 rounded">
                <div><b>缺失:</b> {(draft.missing||[]).join(", ")||"无"} <b className="ml-2">建议:</b> {(draft.suggestions||[]).join(", ")}</div>
                <div><b>置信度:</b> {draft.confidence}</div>
              </div>
            )}
            <div className="flex gap-2 justify-end">
              <Button type="button" size="sm" variant="outline" onClick={()=>setDraftOpen(false)}>取消</Button>
              <Button type="submit" size="sm" variant="forge" disabled={draftForm.formState.isSubmitting}>{draftForm.formState.isSubmitting ? "转 BS 中…" : "确认转 BS"}</Button>
            </div>
          </form>
        </DialogContent>
      </Dialog>

      <Dialog open={!!editing} onOpenChange={(o)=>!o && setEditing(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle className="text-sm">编辑原料 {editing && <Badge variant="outline" className="ml-1 text-[10px]">{editing.id.slice(0,6)}</Badge>}</DialogTitle>
            <DialogDescription className="text-xs">仅 NEW 状态可编辑；保存后将重建语义索引</DialogDescription>
          </DialogHeader>
          <Textarea value={editRaw} onChange={e=>setEditRaw(e.target.value)} rows={8} className="resize-y text-xs" />
          <div className="flex gap-2 justify-end">
            <Button size="sm" variant="outline" onClick={()=>setEditing(null)}>取消</Button>
            <Button size="sm" variant="forge" disabled={saving} onClick={saveEdit}>{saving ? "保存中…" : "保存"}</Button>
          </div>
        </DialogContent>
      </Dialog>
      <Separator />
      <div className="text-xs text-muted-foreground">流程：文本→POST /inbox(NEW+embedding)→POST /inbox/:id/draft(AC/missing)→POST /inbox/convert→BS Spec + requirement_spec_links 可追溯 · 卡片已含相对时间+创建人，详情 Dialog 展示去重哈希/时间线/关联 Spec</div>
    </div>
  )
}

export default InboxBoard
