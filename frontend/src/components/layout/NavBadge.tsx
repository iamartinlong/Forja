import { cn } from "@/lib/utils"
import { Loader2 } from "lucide-react"

// 动态徽章基建（observability P0，observability-design §5.2）
// 全站侧边栏统一渲染：计数徽章 / 危险红点，数据源由各 useQuery 提供

/** 计数徽章：running 类用蓝色转圈语义，普通计数用 secondary */
export function NavBadge({ count, spinning = false, className }: {
  count: number
  /** true 时显示转圈图标（运行中语义） */
  spinning?: boolean
  className?: string
}) {
  if (count <= 0) return null
  return (
    <span
      className={cn(
        "inline-flex items-center gap-0.5 rounded-full bg-blue-500/15 text-blue-600 px-1.5 py-0.5 text-[10px] font-semibold min-w-4 justify-center",
        className,
      )}
    >
      {spinning && <Loader2 className="size-2.5 animate-spin" />}
      {count > 99 ? "99+" : count}
    </span>
  )
}

/** 危险红点：失败/告警语义 */
export function DangerDot({ className }: { className?: string }) {
  return (
    <span
      aria-label="存在失败"
      className={cn("inline-flex size-2 rounded-full bg-destructive shrink-0", className)}
    />
  )
}
