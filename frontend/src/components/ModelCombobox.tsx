import { useState } from "react"
import { Check, Search, Settings } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from "@/components/ui/command"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { can } from "@/lib/auth"
import { useNavigate } from "@tanstack/react-router"

export function ModelCombobox({ providers, value, onChange }: { providers: any[]; value: string; onChange: (v: string) => void }) {
  const [open, setOpen] = useState(false)
  const navigate = useNavigate()
  const providerGroups = providers.map((p: any) => ({
    provider: p,
    heading: (p.display_name?.trim() || p.provider_id),
    models: (p.models || []).filter((m: any) => m.enabled !== false).map((m: any) => ({ provider_id: p.provider_id, model_id: m.model_id, display_name: m.display_name })),
  })).filter(g => g.models.length > 0)
  const allModels = providerGroups.flatMap(g => g.models)
  const selected = allModels.find((m: any) => m.model_id === value)
  const isEmpty = providerGroups.length === 0
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button variant="outline" role="combobox" aria-expanded={open} className="h-8 justify-between w-full font-normal">{selected ? selected.model_id : "搜索模型"}<Search className="h-3 w-3 opacity-50" /></Button>
      </PopoverTrigger>
      <PopoverContent className="w-[--radix-popover-trigger-width] min-w-[320px] p-0 bg-background" align="start" sideOffset={8} collisionPadding={16}>
        <Command className="bg-background">
          <CommandInput placeholder="搜索模型" />
          <CommandList className="max-h-[240px] bg-background">
            <CommandEmpty>{isEmpty ? "暂无模型，请先在管理模型中配置" : "未找到模型"}</CommandEmpty>
            {!isEmpty && providerGroups.map(g => (
              <CommandGroup key={g.provider.provider_id} heading={g.heading} className="bg-background">
                {g.models.map((m: any) => (
                  <CommandItem key={g.provider.provider_id + "/" + m.model_id} value={`${g.provider.provider_id} ${m.model_id} ${m.display_name}`} onSelect={() => { onChange(m.model_id); setOpen(false) }} className="flex justify-between bg-background">
                    <span className="truncate">{m.model_id} <span className="text-muted-foreground text-xs">{m.display_name || ""}</span></span>
                    {m.model_id === value && <Check className="ml-auto h-4 w-4 text-primary" />}
                  </CommandItem>
                ))}
              </CommandGroup>
            ))}
          </CommandList>
          {can("user.list") && (
            <>
              <div className="border-t" />
              <div className="p-1 bg-background">
                <button className="w-full flex items-center gap-2 px-2 py-1.5 text-sm rounded-sm hover:bg-accent" onClick={() => { setOpen(false); navigate({ to: "/admin/model-providers" }) }}><Settings className="h-3.5 w-3.5" /> 管理模型</button>
              </div>
            </>
          )}
        </Command>
      </PopoverContent>
    </Popover>
  )
}
