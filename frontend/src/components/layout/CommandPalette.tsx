import { useEffect, useState } from "react"
import { useNavigate } from "@tanstack/react-router"
import { CornerDownLeft } from "lucide-react"
import { CommandDialog, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList, CommandSeparator } from "@/components/ui/command"
import { Badge } from "@/components/ui/badge"
import { Specs } from "@/lib/api"
import { can, globalRole } from "@/lib/auth"
import { useForjaStore } from "@/lib/store"
import { sidebarNav } from "./AppSidebar"

export function CommandPalette({ open, onOpenChange }: { open: boolean; onOpenChange: (v: boolean) => void }) {
  const navigate = useNavigate()
  const projectId = useForjaStore(s => s.projectId)
  const [q, setQ] = useState("")
  const [results, setResults] = useState<any[]>([])

  useEffect(() => {
    const down = (e: KeyboardEvent) => {
      if ((e.key === "k" || e.key === "K") && (e.metaKey || e.ctrlKey)) {
        e.preventDefault()
        onOpenChange(!open)
      }
    }
    document.addEventListener("keydown", down)
    return () => document.removeEventListener("keydown", down)
  }, [open, onOpenChange])

  useEffect(() => {
    if (!open) { setQ(""); setResults([]); return }
    const term = q.trim()
    if (!term) { setResults([]); return }
    const t = setTimeout(async () => {
      if (!projectId) { setResults([]); return }
      try {
        const r: any = await Specs.search(term, projectId)
        setResults(r.items || [])
      } catch { setResults([]) }
    }, 300)
    return () => clearTimeout(t)
  }, [q, open, projectId])

  const navItems = sidebarNav.flatMap((g: any) =>
    g.items
      .filter((item: any) => item.action ? can(item.action) : !item.roles || item.roles.includes(globalRole()) || item.roles.includes("All"))
      .map((item: any) => ({ ...item, group: g.group }))
  )

  const goSearch = (term: string) => {
    onOpenChange(false)
    navigate({ to: `/search?q=${encodeURIComponent(term)}` })
  }

  return (
    <CommandDialog open={open} onOpenChange={onOpenChange}>
      <CommandInput value={q} onValueChange={setQ} placeholder="搜索知识库，或输入页面名称跳转…" />
      <CommandList>
        <CommandEmpty>没有匹配的结果</CommandEmpty>
        {results.length > 0 && (
          <CommandGroup heading="知识库">
            {results.map((it: any) => (
              <CommandItem key={it.id} value={`spec-${it.id}`} onSelect={() => goSearch(q.trim())}>
                <CornerDownLeft className="h-4 w-4 shrink-0 opacity-50" />
                <span className="flex-1 truncate">{it.title}</span>
                <Badge variant={it.distance < 0.5 ? "success" : it.distance < 0.8 ? "secondary" : "outline"} className="text-[10px]">
                  {it.distance < 0.5 ? "高相关" : it.distance < 0.8 ? "相关" : "弱相关"}
                </Badge>
              </CommandItem>
            ))}
          </CommandGroup>
        )}
        {results.length > 0 && <CommandSeparator />}
        <CommandGroup heading="页面导航">
          {navItems.map((item: any) => (
            <CommandItem key={item.to} value={`nav-${item.label}`} onSelect={() => { onOpenChange(false); navigate({ to: item.to }) }}>
              {item.icon ? <item.icon className="h-4 w-4" /> : <CornerDownLeft />}
              <span>{item.label}</span>
              <span className="ml-auto text-xs text-muted-foreground">{item.group}</span>
            </CommandItem>
          ))}
        </CommandGroup>
      </CommandList>
    </CommandDialog>
  )
}
