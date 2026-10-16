import * as React from "react"
import { cn } from "@/lib/utils"

function Empty({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return <div data-slot="empty" className={cn("flex min-w-0 flex-1 flex-col items-center justify-center gap-2 rounded-lg border-dashed p-6 text-center", className)} {...props} />
}

function EmptyHeader({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return <div data-slot="empty-header" className={cn("flex flex-col items-center gap-1", className)} {...props} />
}

function EmptyMedia({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return <div data-slot="empty-media" className={cn("flex size-8 shrink-0 items-center justify-center rounded-full bg-muted text-muted-foreground", className)} {...props} />
}

function EmptyTitle({ className, ...props }: React.HTMLAttributes<HTMLHeadingElement>) {
  return <div data-slot="empty-title" className={cn("text-xs font-medium leading-none", className)} {...props} />
}

function EmptyDescription({ className, ...props }: React.HTMLAttributes<HTMLParagraphElement>) {
  return <div data-slot="empty-description" className={cn("text-[10px] leading-relaxed text-muted-foreground", className)} {...props} />
}

export { Empty, EmptyHeader, EmptyMedia, EmptyTitle, EmptyDescription }
