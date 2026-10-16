import { useState } from "react"
import { useParams, useNavigate } from "@tanstack/react-router"
import { useQuery } from "@tanstack/react-query"
import { toast } from "sonner"
import { Inbox } from "@/lib/api"
import { useForjaStore } from "@/lib/store"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Badge } from "@/components/ui/badge"
import { Checkbox } from "@/components/ui/checkbox"
import { Table, TableHeader, TableBody, TableRow, TableHead, TableCell } from "@/components/ui/table"
import { Empty, EmptyHeader, EmptyMedia, EmptyTitle, EmptyDescription } from "@/components/ui/empty"
import { InlineLoader } from "@/components/ui/page-loader"
import { relativeTime } from "@/lib/format"
import { InboxDetailDialog } from "./InboxDetailDialog"
import { CircleCheckBig, Download, Search, RotateCcw, ArrowUpDown, FileText } from "lucide-react"

function absoluteTime(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleString()
}

// §2e+ CONVERTED subpage: route /projects/:projectId/inbox/converted (frontend/src/components/inbox/InboxConvertedPage.tsx:1)
export function InboxConvertedPage() {
  const params = useParams({ strict: false }) as { projectId?: string }
  const storeProjectId = useForjaStore(s => s.projectId)
  const projectId = params.projectId || storeProjectId
  const navigate = useNavigate()

  const [q, setQ] = useState("")
  const [sourceType, setSourceType] = useState("")
  const [createdBy, setCreatedBy] = useState("")
  const [from, setFrom] = useState("")
  const [to, setTo] = useState("")
  const [sort, setSort] = useState("updated_at")
  const [order, setOrder] = useState("desc")
  const [page, setPage] = useState(1)
  const pageSize = 50
  const [selected, setSelected] = useState<string[]>([])
  const [detailId, setDetailId] = useState<string | null>(null)
  const [detailOpen, setDetailOpen] = useState(false)

  // Trigger search: queryKey includes all filters
  const query = useQuery({
    queryKey: ["inbox-converted", projectId, q, sourceType, createdBy, from, to, sort, order, page],
    queryFn: () => Inbox.listFiltered({ projectId, status: "CONVERTED", q: q || undefined, source_type: sourceType || undefined, created_by: createdBy || undefined, created_from: from || undefined, created_to: to || undefined, sort, order, page, pageSize }),
    enabled: !!projectId,
  })

  // Overview counts: total (from query) + recent 7d (second query)
  const sevenDaysAgo = new Date(Date.now() - 7 * 24 * 3600 * 1000).toISOString().slice(0, 10)
  const recentQuery = useQuery({
    queryKey: ["inbox-converted-recent", projectId],
    queryFn: () => Inbox.listFiltered({ projectId, status: "CONVERTED", created_from: sevenDaysAgo, page: 1, pageSize: 1 }),
    enabled: !!projectId,
  })

  const items: any[] = (query.data as any)?.items || []
  const total = (query.data as any)?.total ?? 0
  const recent = (recentQuery.data as any)?.total ?? 0
  const totalPages = Math.max(1, Math.ceil(total / pageSize))

  const toggleSort = () => {
    if (sort === "updated_at") {
      setOrder(o => (o === "desc" ? "asc" : "desc"))
    } else {
      setSort("updated_at")
      setOrder("desc")
    }
    setPage(1)
  }

  const toggleSelect = (id: string) => setSelected(s => s.includes(id) ? s.filter(x => x !== id) : [...s, id])
  const toggleAll = (checked: boolean) => {
    if (checked) setSelected(items.map((it: any) => it.id))
    else setSelected([])
  }

  const exportCSV = () => {
    toast.info("CSV 导出预留位 — 待后端 /inbox/export 接口实现，当前可在浏览器控制台复制数据")
    // placeholder: generate CSV client side from current page
    if (items.length === 0) return
    const header = ["id", "source_type", "snippet", "creator", "created_at", "updated_at", "content_hash", "spec_id"]
    const rows = items.map((it: any) => [it.id, it.source_type, `"${(it.snippet || "").replace(/"/g, '""')}"`, it.creator || it.created_by || "", it.created_at, it.updated_at, it.content_hash || "", it.spec_id || ""].join(","))
    const csv = [header.join(","), ...rows].join("\n")
    const blob = new Blob([csv], { type: "text/csv;charset=utf-8;" })
    const url = URL.createObjectURL(blob)
    const a = document.createElement("a")
    a.href = url
    a.download = `converted-${projectId.slice(0, 8)}-p${page}.csv`
    a.click()
    URL.revokeObjectURL(url)
    toast.success(`已导出当前页 ${items.length} 条 CSV（客户端占位）`)
  }

  const openDetail = (id: string) => {
    setDetailId(id)
    setDetailOpen(true)
  }

  const handleSearch = () => setPage(1)
  const handleReset = () => {
    setQ("")
    setSourceType("")
    setCreatedBy("")
    setFrom("")
    setTo("")
    setSort("updated_at")
    setOrder("desc")
    setPage(1)
  }

  return (
    <div className="space-y-4 max-w-6xl">
      {/* Page header (total/recent overview + export CSV placeholder) (frontend/src/components/inbox/InboxConvertedPage.tsx:1) */}
      <div className="flex items-center justify-between gap-3 flex-wrap">
        <div>
          <h1 className="text-lg font-bold flex items-center gap-2">
            <CircleCheckBig className="size-5 text-forge-success" />
            已转化原料 · CONVERTED
            <Badge variant="secondary" className="text-xs">{total} 累计</Badge>
            <Badge variant="outline" className="text-xs">近7日 {recent}</Badge>
          </h1>
          <div className="text-xs text-muted-foreground mt-1">
            项目 <code className="bg-muted px-1 rounded">{projectId.slice(0, 8)}</code> · 转化时间倒序可切排序 · Spec 直跳 · 勾选批量复用
            <Button variant="link" size="sm" className="h-auto p-0 ml-2 text-xs" onClick={() => navigate({ to: "/inbox" })}>← 返回看板</Button>
          </div>
        </div>
        <Button variant="outline" size="sm" onClick={exportCSV}>
          <Download className="size-3.5 mr-1" /> 导出 CSV (占位)
        </Button>
      </div>

      {/* Server-side filters (q/source_type/created_by/time range) (frontend/src/components/inbox/InboxConvertedPage.tsx:1) */}
      <Card>
        <CardHeader className="pb-2"><CardTitle className="text-sm">服务端过滤</CardTitle></CardHeader>
        <CardContent className="space-y-3">
          <div className="grid grid-cols-1 md:grid-cols-3 gap-3">
            <div className="flex flex-col gap-2">
              <Label className="text-xs">关键词 q (raw_content ILIKE)</Label>
              <Input value={q} onChange={e => setQ(e.target.value)} placeholder="搜索原始内容关键词" className="h-8 text-xs" />
            </div>
            <div className="flex flex-col gap-2">
              <Label className="text-xs">来源类型 source_type</Label>
              <select value={sourceType} onChange={e => setSourceType(e.target.value)} className="h-8 rounded-md border bg-background px-2 text-xs">
                <option value="">全部</option>
                <option value="TEXT_PASTE">TEXT_PASTE</option>
                <option value="DOCX">DOCX</option>
                <option value="PDF">PDF</option>
              </select>
            </div>
            <div className="flex flex-col gap-2">
              <Label className="text-xs">创建人 created_by (UUID 或用户名)</Label>
              <Input value={createdBy} onChange={e => setCreatedBy(e.target.value)} placeholder="粘贴 UUID 或用户名" className="h-8 text-xs" />
            </div>
          </div>
          <div className="grid grid-cols-1 md:grid-cols-3 gap-3">
            <div className="flex flex-col gap-2">
              <Label className="text-xs">时间范围 起 created_from</Label>
              <Input type="date" value={from} onChange={e => setFrom(e.target.value)} className="h-8 text-xs" />
            </div>
            <div className="flex flex-col gap-2">
              <Label className="text-xs">时间范围 止 created_to</Label>
              <Input type="date" value={to} onChange={e => setTo(e.target.value)} className="h-8 text-xs" />
            </div>
            <div className="flex flex-col gap-2 justify-end">
              <div className="flex gap-2">
                <Button size="sm" variant="forge" onClick={handleSearch}><Search className="size-3.5 mr-1" />搜索</Button>
                <Button size="sm" variant="outline" onClick={handleReset}><RotateCcw className="size-3.5 mr-1" />重置</Button>
              </div>
            </div>
          </div>
          {query.isFetching && <InlineLoader text="筛选加载中…" />}
        </CardContent>
      </Card>

      {/* Table (converted time desc sortable, spec link deep, checkbox batch reuse) + pagination 50/page (frontend/src/components/inbox/InboxConvertedPage.tsx:1) */}
      <Card>
        <CardHeader className="pb-2 flex flex-row items-center justify-between">
          <CardTitle className="text-sm">已转化列表 · 50 条/页 · 第 {page}/{totalPages} 页 · 共 {total} 条</CardTitle>
          {selected.length > 0 && (
            <div className="flex items-center gap-2 text-xs">
              <span>已选 {selected.length} 项</span>
              <Button size="sm" variant="forge" className="h-6 text-xs" onClick={() => toast.info(`批量复用占位：已选 ${selected.length} 项，待 /inbox/convert 批量接口`)}>批量复用 ({selected.length})</Button>
              <Button size="sm" variant="ghost" className="h-6 text-xs" onClick={() => setSelected([])}>取消选择</Button>
            </div>
          )}
        </CardHeader>
        <CardContent className="p-0">
          {query.isLoading ? (
            // skeleton empty state (frontend/src/components/inbox/InboxConvertedPage.tsx:1)
            <div className="p-4 space-y-3">
              {Array.from({ length: 6 }).map((_, i) => (
                <div key={i} className="flex gap-3 animate-pulse">
                  <div className="size-4 bg-muted rounded" />
                  <div className="flex-1 h-4 bg-muted rounded" />
                  <div className="w-24 h-4 bg-muted rounded" />
                  <div className="w-16 h-4 bg-muted rounded" />
                </div>
              ))}
            </div>
          ) : items.length === 0 ? (
            <Empty className="py-12">
              <EmptyHeader>
                <EmptyMedia><FileText className="size-4" /></EmptyMedia>
                <EmptyTitle>暂无已转化原料</EmptyTitle>
                <EmptyDescription>调整筛选条件或将看板中的 NEW/DRAFTED 原料转化为 Spec</EmptyDescription>
              </EmptyHeader>
            </Empty>
          ) : (
            <div className="overflow-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="w-8"><Checkbox checked={selected.length === items.length && items.length > 0} onCheckedChange={v => toggleAll(!!v)} aria-label="全选" /></TableHead>
                    <TableHead className="text-xs">内容摘要</TableHead>
                    <TableHead className="text-xs">来源</TableHead>
                    <TableHead className="text-xs">创建人</TableHead>
                    <TableHead className="text-xs cursor-pointer select-none" onClick={toggleSort} title="点击切换转化时间排序">
                      <span className="flex items-center gap-1">转化时间 <ArrowUpDown className="size-3" /></span>
                      <span className="text-[10px] text-muted-foreground">{sort === "updated_at" ? (order === "desc" ? "倒序" : "正序") : ""}</span>
                    </TableHead>
                    <TableHead className="text-xs">去重哈希</TableHead>
                    <TableHead className="text-xs">生成 Spec↗</TableHead>
                    <TableHead className="text-xs">操作</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {items.map((it: any) => (
                    <TableRow key={it.id} className={selected.includes(it.id) ? "bg-muted/50" : ""}>
                      <TableCell><Checkbox checked={selected.includes(it.id)} onCheckedChange={() => toggleSelect(it.id)} aria-label="选择" /></TableCell>
                      <TableCell className="max-w-[320px]">
                        <div className="text-xs line-clamp-2" title={it.snippet}>{it.snippet}</div>
                        <div className="text-[10px] text-muted-foreground flex gap-2 mt-1">
                          <span title={absoluteTime(it.created_at)} className="cursor-help underline decoration-dotted">{relativeTime(it.created_at)}</span>
                          <span>·</span>
                          <span className="truncate">{it.creator || (it.created_by ? it.created_by.slice(0, 6) : "—")}</span>
                        </div>
                      </TableCell>
                      <TableCell><Badge variant="outline" className="text-[10px]">{it.source_type}</Badge></TableCell>
                      <TableCell className="text-xs">
                        <span title={it.created_by || ""}>{it.creator || (it.created_by ? it.created_by.slice(0, 6) : "—")}</span>
                      </TableCell>
                      <TableCell className="text-xs" title={absoluteTime(it.updated_at || it.created_at)}>
                        <span className="cursor-help underline decoration-dotted">{relativeTime(it.updated_at || it.created_at)}</span>
                        <div className="text-[10px] text-muted-foreground">{absoluteTime(it.updated_at || it.created_at)}</div>
                      </TableCell>
                      <TableCell className="text-[10px] font-mono truncate max-w-[80px]" title={it.content_hash || ""}>{it.content_hash ? it.content_hash.slice(0, 8) + "…" : "—"}</TableCell>
                      <TableCell>
                        {it.spec_id ? (
                          <button onClick={() => navigate({ to: "/specs/$specId", params: { specId: it.spec_id } as any })} className="text-xs text-forge-ember hover:underline flex items-center gap-1">
                            {it.spec_id.slice(0, 6)} <span className="text-[10px]">↗</span>
                          </button>
                        ) : (
                          <span className="text-xs text-muted-foreground">—</span>
                        )}
                      </TableCell>
                      <TableCell>
                        <Button size="sm" variant="ghost" className="h-6 text-xs" onClick={() => openDetail(it.id)}>详情</Button>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
          {/* pagination 50/page (frontend/src/components/inbox/InboxConvertedPage.tsx:1) */}
          {total > 0 && (
            <div className="flex items-center justify-between p-3 border-t text-xs">
              <div className="text-muted-foreground">第 {page} 页 / 共 {totalPages} 页 · {total} 条 · 50 条/页</div>
              <div className="flex gap-2">
                <Button size="sm" variant="outline" className="h-7" disabled={page <= 1} onClick={() => setPage(p => Math.max(1, p - 1))}>上一页</Button>
                <span className="flex items-center px-2">{page} / {totalPages}</span>
                <Button size="sm" variant="outline" className="h-7" disabled={page >= totalPages} onClick={() => setPage(p => Math.min(totalPages, p + 1))}>下一页</Button>
              </div>
            </div>
          )}
        </CardContent>
      </Card>

      <InboxDetailDialog id={detailId} open={detailOpen} onOpenChange={o => { setDetailOpen(o); if (!o) setDetailId(null) }} />
    </div>
  )
}

export default InboxConvertedPage
