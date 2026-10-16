import * as React from "react"
import { PanelLeft } from "lucide-react"
import { cn } from "@/lib/utils"

const SidebarContext = React.createContext<{ open: boolean; setOpen: (v: boolean) => void; collapsed: boolean; setCollapsed: (v: boolean) => void } | null>(null)

export function SidebarProvider({ children, defaultOpen = true, className }: { children: React.ReactNode; defaultOpen?: boolean; className?: string }) {
  const [open, setOpen] = React.useState(defaultOpen)
  const [collapsed, setCollapsed] = React.useState(false)
  return <SidebarContext.Provider value={{ open, setOpen, collapsed, setCollapsed }}><div className={cn("flex w-full", className || "min-h-screen")}>{children}</div></SidebarContext.Provider>
}

export function Sidebar({ children, className }: { children: React.ReactNode; className?: string }) {
  const ctx = React.useContext(SidebarContext)
  const collapsed = ctx?.collapsed
  return (
    <aside className={cn("border-r flex flex-col transition-all bg-forge-anvil dark:bg-forge-anvil-deep text-white border-white/10", collapsed ? "w-[60px]" : "w-[240px]", className)}>
      {children}
    </aside>
  )
}

export function SidebarHeader({ children, className }: { children: React.ReactNode; className?: string }) {
  return <div className={cn("h-14 flex items-center px-3 border-b shrink-0", className)}>{children}</div>
}

export function SidebarFooter({ children, className }: { children: React.ReactNode; className?: string }) {
  return <div className={cn("shrink-0 border-t border-white/10 p-2", className)}>{children}</div>
}

export function SidebarContent({ children, className }: { children: React.ReactNode; className?: string }) {
  return <div className={cn("flex-1 overflow-auto py-2", className)}>{children}</div>
}

export function SidebarGroup({ children, className, label }: { children: React.ReactNode; className?: string; label?: string }) {
  return (
    <div className={cn("px-2 py-2", className)}>
      {label && <div className="px-2 py-1 text-xs font-semibold text-white/60 tracking-wider">{label}</div>}
      <div className="space-y-1">{children}</div>
    </div>
  )
}

export function SidebarMenu({ children }: { children: React.ReactNode }) {
  return <div className="space-y-1">{children}</div>
}

export function SidebarMenuItem({ children, active, onClick, onMouseEnter, icon, badge }: { children: React.ReactNode; active?: boolean; onClick?: () => void; onMouseEnter?: () => void; icon?: React.ReactNode; badge?: React.ReactNode }) {
  const ctx = React.useContext(SidebarContext)
  const collapsed = ctx?.collapsed
  return (
    <button
      type="button"
      onClick={onClick}
      onMouseEnter={onMouseEnter}
      className={cn(
        "w-full flex items-center gap-2 px-2 py-2 rounded-md text-sm text-left transition-colors",
        active ? "bg-forge-ember text-white hover:bg-forge-ember-deep" : "text-white/80 hover:bg-white/10 hover:text-white",
        collapsed && "justify-center"
      )}
      title={collapsed ? String(children) : undefined}
    >
      {icon && <span className="shrink-0">{icon}</span>}
      {!collapsed && <span className="flex-1 truncate">{children}</span>}
      {!collapsed && badge}
    </button>
  )
}

export function SidebarInset({ children, className }: { children: React.ReactNode; className?: string }) {
  return <div className={cn("flex-1 flex flex-col min-w-0", className)}>{children}</div>
}

export function SidebarTrigger({ className }: { className?: string }) {
  const ctx = React.useContext(SidebarContext)
  if (!ctx) return null
  return (
    <button onClick={() => ctx.setCollapsed(!ctx.collapsed)} className={cn("p-2 rounded-md hover:bg-accent", className)}>
      <PanelLeft className="h-4 w-4" />
      <span className="sr-only">Toggle Sidebar</span>
    </button>
  )
}

export function useSidebar() {
  const ctx = React.useContext(SidebarContext)
  if (!ctx) throw new Error("useSidebar must be used within SidebarProvider")
  return ctx
}
