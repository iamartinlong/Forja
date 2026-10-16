import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { toast } from "sonner"
import { Inbox } from "@/lib/api"
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from "@/components/ui/dialog"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Separator } from "@/components/ui/separator"
import { relativeTime } from "@/lib/format"
import { Loader2, RefreshCw } from "lucide-react"

function absoluteTime(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleString()
}

export interface InboxDetailDialogProps {
  id: string | null
  open: boolean
  onOpenChange: (open: boolean) => void
}

// §2e+ Detail Dialog: show all fields (creator/dedup hash/timeline/linked Spec) — reuse same component for kanban cards and subpage (frontend/src/components/inbox/InboxDetailDialog.tsx:1)
export function InboxDetailDialog({ id, open, onOpenChange }: InboxDetailDialogProps) {
  const navigate = useNavigate()
  const [reforging, setReforging] = useState(false)
  const query = useQuery({
    queryKey: ["inbox-detail", id],
    queryFn: () => Inbox.get(id!),
    enabled: !!id && open,
  })
  // M4b：附件列表（上传走 Inbox 创建/独立附件端点；此处展示与下载）
  const atts = useQuery({
    queryKey: ["inbox-attachments", id],
    queryFn: () => Inbox.attachments(id!),
    enabled: !!id && open,
  })
  const attachments = (atts.data as any)?.items || []

  const data = query.data as any

  // v2.2 T3：UAT 反馈一键回炉（生成修复 Task，幂等；重复点击返回既有任务）
  const reforge = async () => {
    if (!id) return
    setReforging(true)
    try {
      const r: any = await Inbox.reforge(id)
      if (r?.dedup) {
        toast.info(`该 UAT 已生成修复任务 ${String(r.id).slice(0, 8)}（未重复建单）`)
      } else {
        toast.success(`已生成修复任务 ${String(r.id).slice(0, 8)}（PENDING 待审批）`)
      }
      onOpenChange(false)
      navigate({ to: "/forge/$taskId", params: { taskId: r.id } as any })
    } catch { /* API 错误由 http 拦截器全局 toast */ } finally {
      setReforging(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-lg max-h-[80vh] overflow-auto">
        <DialogHeader>
          <DialogTitle className="text-sm flex items-center gap-2">
            原料详情 {id && <code className="text-[10px] bg-muted px-1.5 py-0.5 rounded">{id.slice(0, 8)}</code>}
            {data && <Badge variant="outline" className="text-[10px]">{data.source_type}</Badge>}
            {data && <Badge variant={data.status === "CONVERTED" ? "success" : data.status === "DRAFTED" ? "forge" : "secondary"} className="text-[10px]">{data.status}</Badge>}
          </DialogTitle>
          <DialogDescription className="text-xs">创建人 / 去重哈希 / 时间线 / 关联 Spec 一览</DialogDescription>
        </DialogHeader>

        {!id && <div className="text-xs text-muted-foreground">未选择原料</div>}
        {query.isLoading && (
          <div className="space-y-3 animate-pulse">
            <div className="h-4 bg-muted rounded w-3/4" />
            <div className="h-20 bg-muted rounded" />
            <div className="h-4 bg-muted rounded w-1/2" />
          </div>
        )}
        {query.isError && <div className="text-xs text-destructive">加载失败，请重试</div>}
        {data && (
          <div className="space-y-4 text-xs">
            {/* raw content */}
            <div>
              <div className="font-semibold mb-1">原始内容</div>
              <div className="bg-muted p-3 rounded text-xs whitespace-pre-wrap break-all max-h-48 overflow-auto">{data.raw_content}</div>
            </div>

            <Separator />

            {/* creator + dedup hash */}
            <div className="grid grid-cols-2 gap-3">
              <div>
                <div className="text-muted-foreground">创建人</div>
                <div className="font-medium truncate" title={data.created_by || ""}>
                  {data.creator ? `${data.creator}` : data.created_by ? `${data.created_by.slice(0, 8)}…` : "— (历史数据无创建人)"}
                </div>
                {data.created_by && <div className="text-[10px] text-muted-foreground truncate">{data.created_by}</div>}
              </div>
              <div>
                <div className="text-muted-foreground">去重哈希</div>
                <code className="text-[10px] break-all bg-muted px-1 py-0.5 rounded">{data.content_hash || "—"}</code>
              </div>
            </div>

            {/* timeline */}
            <div>
              <div className="font-semibold mb-1">时间线</div>
              <div className="space-y-1">
                <div className="flex justify-between">
                  <span className="text-muted-foreground">创建时间</span>
                  <span title={absoluteTime(data.created_at)} className="cursor-help underline decoration-dotted">{relativeTime(data.created_at)} <span className="text-[10px] text-muted-foreground">({absoluteTime(data.created_at)})</span></span>
                </div>
                <div className="flex justify-between">
                  <span className="text-muted-foreground">更新时间</span>
                  <span title={absoluteTime(data.updated_at)} className="cursor-help underline decoration-dotted">{relativeTime(data.updated_at)} <span className="text-[10px] text-muted-foreground">({absoluteTime(data.updated_at)})</span></span>
                </div>
                {data.status === "CONVERTED" && <div className="text-[10px] text-muted-foreground">转化时间以更新时间为准（倒序主要排序键）</div>}
              </div>
            </div>

            <Separator />

            {/* attachments (M4b) */}
            <div>
              <div className="font-semibold mb-1">附件 <Badge variant="outline" className="text-[10px]">{attachments.length}</Badge></div>
              {attachments.length === 0 ? (
                <div className="text-muted-foreground">无附件</div>
              ) : (
                <div className="space-y-1">
                  {attachments.map((a: any) => (
                    <div key={a.id} className="flex items-center gap-2 border rounded px-2 py-1.5">
                      <span className="truncate flex-1" title={a.filename}>{a.filename}</span>
                      <span className="text-[10px] text-muted-foreground">{((a.size || 0) / 1024).toFixed(1)}KB</span>
                      <Button asChild size="sm" variant="outline" className="h-6 px-2 text-[10px]">
                        <a href={Inbox.attachmentURL(a.id)}>下载</a>
                      </Button>
                    </div>
                  ))}
                </div>
              )}
            </div>

            <Separator />

            {/* linked Spec */}
            <div>
              <div className="font-semibold mb-1">关联 Spec <Badge variant="outline" className="text-[10px]">{(data.spec_ids || []).length}</Badge></div>
              {(data.spec_ids || []).length === 0 ? (
                <div className="text-muted-foreground">暂无关联 Spec（未转化或血缘未落库）</div>
              ) : (
                <div className="space-y-1">
                  {data.spec_ids.map((sid: string) => (
                    <button
                      key={sid}
                      onClick={() => {
                        onOpenChange(false)
                        navigate({ to: "/specs/$specId", params: { specId: sid } as any })
                      }}
                      className="flex items-center gap-2 w-full text-left border rounded px-2 py-1.5 hover:bg-accent transition-colors"
                    >
                      <Badge variant="secondary" className="text-[10px]">Spec</Badge>
                      <code className="text-xs">{sid.slice(0, 8)}</code>
                      <span className="text-[10px] text-muted-foreground truncate flex-1">{sid}</span>
                      <span className="text-[10px] text-forge-ember">↗ 直跳</span>
                    </button>
                  ))}
                </div>
              )}
            </div>

            <div className="flex justify-end gap-2 pt-2">
              {data.source_type === "UAT" && (
                <Button size="sm" variant="forge" onClick={reforge} disabled={reforging} title="从该 UAT 反馈生成修复任务（幂等）">
                  {reforging ? <Loader2 className="size-3.5 animate-spin mr-1" /> : <RefreshCw className="size-3.5 mr-1" />}
                  生成修复任务
                </Button>
              )}
              <Button size="sm" variant="outline" onClick={() => onOpenChange(false)}>关闭</Button>
            </div>
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}

export default InboxDetailDialog
