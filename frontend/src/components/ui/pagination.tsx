import { Button } from "@/components/ui/button"

// R8 统一分页条：每页条数选择（10/20/30/50/100，默认由调用方控制）+ 页码/总数 + 翻页。
// 有数据即渲染（单页时按钮禁用）；total<=0 时不渲染。
export const PAGE_SIZES = [10, 20, 30, 50, 100]

export interface PaginationProps {
  page: number
  totalPages: number
  total: number
  pageSize: number
  onPageChange: (page: number) => void
  onPageSizeChange: (size: number) => void
  disabled?: boolean
  /** 每页条数选择器的 aria-label（同屏多个分页条时需区分） */
  sizeLabel?: string
}

export function Pagination({
  page,
  totalPages,
  total,
  pageSize,
  onPageChange,
  onPageSizeChange,
  disabled = false,
  sizeLabel = "每页条数",
}: PaginationProps) {
  if (total <= 0) return null
  return (
    <div className="flex items-center justify-end gap-2 text-xs text-muted-foreground mt-3">
      <label className="flex items-center gap-1">
        每页
        <select
          value={pageSize}
          onChange={(e) => onPageSizeChange(Number(e.target.value))}
          disabled={disabled}
          className="h-8 rounded-md border bg-background px-2 text-xs"
          aria-label={sizeLabel}
        >
          {PAGE_SIZES.map((s) => <option key={s} value={s}>{s}</option>)}
        </select>
        条
      </label>
      <span>第 {page} / {totalPages} 页 · 共 {total} 条</span>
      <Button variant="outline" size="sm" className="h-8" disabled={disabled || page <= 1} onClick={() => onPageChange(page - 1)}>上一页</Button>
      <Button variant="outline" size="sm" className="h-8" disabled={disabled || page >= totalPages} onClick={() => onPageChange(page + 1)}>下一页</Button>
    </div>
  )
}
