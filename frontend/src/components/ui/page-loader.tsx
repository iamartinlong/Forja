import { Flame } from "lucide-react"

export function PageLoader({ text = "加载中…" }: { text?: string }) {
  return (
    <div className="flex flex-col items-center justify-center gap-3 py-12">
      <div className="flex size-10 items-center justify-center rounded-lg bg-primary/10">
        <Flame className="size-6 text-primary animate-pulse" />
      </div>
      <span className="text-sm text-muted-foreground">{text}</span>
    </div>
  )
}

export function InlineLoader({ text = "加载中…" }: { text?: string }) {
  return (
    <span className="inline-flex items-center gap-2 text-sm text-muted-foreground">
      <Flame className="size-4 text-primary animate-pulse" />
      {text}
    </span>
  )
}
