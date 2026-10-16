import { useEffect, useState } from "react"
import { useNavigate } from "@tanstack/react-router"
import { useForm, useFieldArray } from "react-hook-form"
import { zodResolver } from "@hookform/resolvers/zod"
import { z } from "zod"
import { toast } from "sonner"
import { api } from "@/lib/api"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Badge } from "@/components/ui/badge"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Switch } from "@/components/ui/switch"
import { Plus, RefreshCw, Search, Pencil, Trash2, FlaskConical, Loader2, Settings } from "lucide-react"
import { ConfirmDialog, type ConfirmState } from "@/components/shared/ConfirmDialog"
import { useMinLoading } from "@/hooks/useMinLoading"
import { Empty, EmptyHeader, EmptyMedia, EmptyTitle, EmptyDescription } from "@/components/ui/empty"

const baseFields = {
  provider_id: z.string().min(1, "请输入提供商ID").regex(/^[a-z0-9_-]+$/, "小写字母数字-_"),
  display_name: z.string().min(1, "请输入显示名称"),
  base_url: z.string().min(1, "请输入基础URL").url("请输入正确的 https:// 域名").regex(/^https:\/\//, "请输入正确的 https:// 域名").regex(/^https:\/\/[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}(:\d+)?(\/.*)?$/, "请输入正确的 https:// 域名"),
  models: z.array(z.object({ model_id: z.string().min(1, "请输入模型ID"), display_name: z.string().optional(), enabled: z.boolean().optional() })).optional(),
  headers: z.array(z.object({ name: z.string().min(1), value: z.string().min(1) })).optional(),
}
type Form = z.infer<ReturnType<typeof getFormSchema>>
function getFormSchema(isEditing: boolean) {
  return z.object({ auth_mode: z.enum(["Bearer", "Header", "None"]), api_key: z.string().optional(), ...baseFields }).superRefine((data, ctx) => {
    if (data.auth_mode === "Bearer" && !isEditing && !data.api_key?.trim()) {
      ctx.addIssue({ code: z.ZodIssueCode.custom, path: ["api_key"], message: "请输入 API 密钥" })
    }
  })
}

export default function ModelProviderBoard() {
  const navigate = useNavigate()
  const [items, setItems] = useState<any[]>([])
  const [open, setOpen] = useState(false)
  const [editing, setEditing] = useState<any>(null)
  const [testing, setTesting] = useState(false)
  const [q, setQ] = useState("")
  const [loading, setLoading] = useState(false)
  const showLoading = useMinLoading(loading, 400)
  const [confirmState, setConfirmState] = useState<ConfirmState | null>(null)
  const form = useForm<Form>({
    // 动态校验：新建 Bearer 必填，编辑留空不改；用闭包捕获 editing 保证 zod 内联错误
    resolver: (values: any, ctx: any, opts: any) => zodResolver(getFormSchema(!!editing) as any)(values, ctx, opts),
    defaultValues: { provider_id: "", display_name: "", base_url: "https://", auth_mode: "Bearer", api_key: "", models: [{ model_id: "", display_name: "", enabled: true }], headers: [] },
  } as any)
  const { control, register, handleSubmit, watch, formState: { errors, isSubmitting } } = form as any
  const authMode = (watch as any)("auth_mode")
  const modelsFA = useFieldArray({ control: control as any, name: "models" as any })
  const headersFA = useFieldArray({ control: control as any, name: "headers" as any })
  const load = async (query = q) => {
    if (loading) return
    setLoading(true)
    try {
      const r: any = await api(`/api/v1/model-providers`)
      let list = r.items || []
      if (query) {
        const lower = query.toLowerCase()
        list = list.filter((p: any) => p.provider_id?.toLowerCase().includes(lower) || p.display_name?.toLowerCase().includes(lower))
      }
      setItems(list)
    } catch (e: any) { toast.error(e.message) } finally { setLoading(false) }
  }
  useEffect(() => { load("") }, [])
  const openCreate = () => { setEditing(null); form.reset({ provider_id: "", display_name: "", base_url: "https://", auth_mode: "Bearer", api_key: "", models: [{ model_id: "", display_name: "", enabled: true }], headers: [] }); setOpen(true) }
  const openEdit = (it: any) => { setEditing(it); form.reset({ provider_id: it.provider_id, display_name: it.display_name, base_url: it.base_url, auth_mode: it.auth_mode || "Bearer", api_key: "", models: (it.models?.length ? it.models : [{ model_id: "", display_name: "", enabled: true }]), headers: it.headers || [] }); setOpen(true) }
  const submit = async (v: Form) => {
    // 动态必填：仅新建且 Bearer 时密钥必填；编辑留空表示不改（与后端 PATCH 语义一致）
    if (!editing && v.auth_mode === "Bearer" && !v.api_key?.trim()) {
      form.setError("api_key", { message: "请输入 API 密钥" })
      return
    }
    try {
      const payload: any = { ...v, models: v.models?.map(m => ({ model_id: m.model_id, display_name: m.display_name, enabled: m.enabled ?? true })) }
      if (editing && !v.api_key?.trim()) delete payload.api_key
      if (editing) await api(`/api/v1/model-providers/${editing.id}`, { method: "PATCH", body: JSON.stringify(payload) })
      else await api(`/api/v1/model-providers`, { method: "POST", body: JSON.stringify(payload) })
      toast.success(editing ? "已更新" : "已创建"); setOpen(false); setEditing(null); load()
    } catch (e: any) { toast.error(e.message) }
  }
  const test = async (id: string) => {
    try { const r: any = await api(`/api/v1/model-providers/${id}/test`, { method: "POST" }); toast[r.status === "healthy" ? "success" : "error"](r.status + (r.detail ? " " + r.detail : "")); load() } catch (e: any) { toast.error(e.message) }
  }
  const testDraft = async () => {
    const vals = watch()
    if (!vals.base_url) { toast.error("请先填写 URL"); return }
    if (vals.auth_mode === "Bearer" && !vals.api_key?.trim() && !(editing && !vals.api_key?.trim())) {
      // create mode Bearer requires key, edit with empty key will use stored
      if (!editing || vals.api_key?.trim()) {
        toast.error("Bearer 模式请填写密钥"); return
      }
    }
    // 编辑且未改密钥 → 用存量密钥测已有提供商
    if (editing && !vals.api_key?.trim()) {
      setTesting(true)
      try {
        const r: any = await api(`/api/v1/model-providers/${editing.id}/test`, { method: "POST" })
        toast[r.status === "healthy" ? "success" : "error"](r.status + (r.detail ? " " + r.detail : " 已连通")); load()
      } catch (e: any) { toast.error(e.message) } finally { setTesting(false) }
      return
    }
    setTesting(true)
    try {
      const r: any = await api(`/api/v1/model-providers/test-preview`, { method: "POST", body: JSON.stringify({ base_url: vals.base_url, api_key: vals.api_key, auth_mode: vals.auth_mode, headers: vals.headers }) })
      toast[r.status === "healthy" ? "success" : "error"](r.status + (r.detail ? " " + r.detail : " 已连通"))
    } catch (e: any) { toast.error(e.message) } finally { setTesting(false) }
  }
  const removeProvider = async (p: any) => {
    try {
      const ref: any = await api(`/api/v1/model-providers/${p.id}/references`)
      const furnaces = ref.furnaces || []
      if (furnaces.length > 0) {
        setConfirmState({
          title: `无法删除 ${p.provider_id}`,
          description: `被 ${furnaces.length} 个炉引用（${furnaces.map((f: any) => f.name).join("、")}），请先停用模型或迁移炉，或勾选强制并输入 provider_id 确认。建议改用“停用”替代删除。`,
          confirmText: "仍要强制删除",
          danger: true,
          action: async () => {
            const input = window.prompt(`输入 provider_id "${p.provider_id}" 以确认强制删除，将置空相关炉的模型引用`)
            if (input !== p.provider_id) { toast.error("输入不一致，已取消"); throw new Error("cancel") }
            try { await api(`/api/v1/model-providers/${p.id}?force=true`, { method: "DELETE" }); toast.success(`已强制删除 ${p.provider_id}`); load() } catch (e: any) { toast.error(e.message); throw e }
          },
        })
        return
      }
    } catch {}
    setConfirmState({
      title: `删除提供商 ${p.provider_id}`,
      description: "该操作不可恢复。空引用提供商可直接删除。",
      confirmText: "删除",
      danger: true,
      action: async () => {
        try { await api(`/api/v1/model-providers/${p.id}`, { method: "DELETE" }); toast.success(`已删除 ${p.provider_id}`); load() } catch (e: any) { toast.error(e.message); throw e }
      },
    })
  }
  return (
    <div className="space-y-4 w-full">
      <Card>
        <CardHeader className="pb-2 flex-row items-center">
          <CardTitle className="text-sm">模型提供商 ({items.length})</CardTitle>
          <div className="ml-auto flex gap-2 items-center">
            <div className="relative">
              <Search className="h-3.5 w-3.5 absolute left-2.5 top-1/2 -translate-y-1/2 text-muted-foreground" />
              <Input value={q} onChange={e => setQ(e.target.value)} onKeyDown={e => { if (e.key === "Enter") load() }} placeholder="ID或名称" className="h-8 w-56 pl-7" />
            </div>
            <Button size="sm" variant="outline" onClick={() => load()} disabled={showLoading} className="h-8">{showLoading ? <Loader2 className="h-4 w-4 animate-spin" /> : null}查询</Button>
            <Button size="sm" variant="forge" onClick={openCreate} className="h-8"><Plus className="h-4 w-4" /> 新增</Button>
            <Button size="sm" variant="ghost" onClick={() => load()} title="刷新" disabled={showLoading} className="h-8 w-8 p-0">{showLoading ? <Loader2 className="h-4 w-4 animate-spin" /> : <RefreshCw className="h-4 w-4" />}</Button>
          </div>
        </CardHeader>
        <CardContent className="relative">
          {showLoading && (
            <div className="absolute inset-0 bg-background/60 backdrop-blur-[1px] z-10 flex items-center justify-center rounded-b-lg">
              <span className="inline-flex items-center gap-2 text-xs text-muted-foreground bg-background border rounded-full px-3 py-1.5 shadow-sm">
                <Loader2 className="h-3.5 w-3.5 animate-spin" /> 正在刷新…
              </span>
            </div>
          )}
          <div className={showLoading ? "opacity-60 pointer-events-none transition-opacity" : "transition-opacity"}>
            <Table>
              <TableHeader><TableRow><TableHead>提供商ID</TableHead><TableHead>显示名称</TableHead><TableHead>基础URL</TableHead><TableHead>模型</TableHead><TableHead>状态</TableHead><TableHead className="text-right">操作</TableHead></TableRow></TableHeader>
              <TableBody>{items.map((p: any) => (
                <TableRow key={p.id}><TableCell className="text-xs font-mono"><button className="hover:underline" onClick={() => navigate({ to: "/admin/model-providers/$id", params: { id: p.id } as any })}>{p.provider_id}</button></TableCell><TableCell className="text-xs">{p.display_name}</TableCell><TableCell className="text-xs truncate max-w-[180px]">{p.base_url}</TableCell><TableCell className="text-xs">{(p.models||[]).length} { (p.models||[]).filter((m:any)=>m.enabled===false).length ? <Badge variant="secondary" className="text-[10px]">含停用</Badge> : null }</TableCell><TableCell><Badge variant={p.status==="healthy"?"outline":"destructive"} className="text-[10px]">{p.status}</Badge></TableCell><TableCell className="text-right"><div className="flex gap-1 justify-end"><Button size="sm" variant="ghost" className="h-6 px-1.5" title="编辑" onClick={() => openEdit(p)}><Pencil className="h-3.5 w-3.5" /></Button><Button size="sm" variant="ghost" className="h-6 px-1.5" title="测试" onClick={() => test(p.id)}><FlaskConical className="h-3.5 w-3.5" /></Button><Button size="sm" variant="ghost" className="h-6 w-6 p-0 text-destructive hover:text-destructive" title="删除" onClick={() => removeProvider(p)}><Trash2 className="h-3.5 w-3.5" /></Button></div></TableCell></TableRow>
              ))}</TableBody>
            </Table>
            {!showLoading && items.length === 0 && (
              <Empty className="py-10 mt-2">
                <EmptyHeader>
                  <EmptyMedia><Settings className="size-4" /></EmptyMedia>
                  <EmptyTitle>{q ? "未找到匹配提供商" : "暂无提供商"}</EmptyTitle>
                  <EmptyDescription>{q ? `关键词 “${q}” 无结果` : "点击「新增」创建首个模型提供商"}</EmptyDescription>
                </EmptyHeader>
                {q ? (
                  <Button size="sm" variant="outline" className="h-8 mt-2" onClick={() => { setQ(""); load("") }}>清除筛选</Button>
                ) : (
                  <Button size="sm" variant="forge" className="h-8 mt-2" onClick={openCreate}><Plus className="h-4 w-4" />新增提供商</Button>
                )}
              </Empty>
            )}
          </div>
        </CardContent>
      </Card>
      <ConfirmDialog state={confirmState} onClose={() => setConfirmState(null)} />
      <Dialog open={open} onOpenChange={(o) => { setOpen(o); if (!o) { setEditing(null); form.reset({ provider_id: "", display_name: "", base_url: "https://", auth_mode: "Bearer", api_key: "", models: [{ model_id: "", display_name: "", enabled: true }], headers: [] }) } }}><DialogContent className="max-w-2xl max-h-[85vh] overflow-auto" onInteractOutside={(e: any) => e.preventDefault()}><DialogHeader><DialogTitle>{editing ? "编辑" : "新增"}提供商</DialogTitle></DialogHeader><form onSubmit={handleSubmit(submit)} className="flex flex-col gap-3">
        <div className="flex flex-col gap-2"><label className="text-xs">提供商ID *</label><Input {...register("provider_id")} className="h-8" placeholder="myprovider" /><span className="text-xs text-destructive">{errors.provider_id?.message}</span></div>
        <div className="flex flex-col gap-2"><label className="text-xs">显示名称 *</label><Input {...register("display_name")} className="h-8" placeholder="我的AI" /><span className="text-xs text-destructive">{errors.display_name?.message}</span></div>
        <div className="flex flex-col gap-2"><label className="text-xs">认证模式</label><select {...register("auth_mode")} className="border rounded-md h-8 px-2 text-sm bg-background"><option value="Bearer">Bearer (API Key)</option><option value="Header">Header</option><option value="None">None</option></select></div>
        <div className="flex flex-col gap-2"><label className="text-xs">基础URL *</label><Input {...register("base_url")} className="h-8" placeholder="https://api.myprovider.com/v1" /><span className="text-xs text-destructive">{errors.base_url?.message}</span></div>
        <div className="flex flex-col gap-2"><label className="text-xs">API密钥 {authMode === "Bearer" ? (editing ? "(留空不改)" : "*") : "(可选)"} </label><Input {...register("api_key")} className="h-8" placeholder={editing ? "•••••••• 留空不改" : authMode === "Bearer" ? "sk-..." : "可选"} type="password" /><span className="text-xs text-destructive">{errors.api_key?.message}</span></div>
        <div className="space-y-2"><div className="flex items-center gap-2"><span className="text-xs font-medium">模型</span><Button type="button" size="sm" variant="outline" className="h-6 text-xs ml-auto" onClick={() => modelsFA.append({ model_id: "", display_name: "", enabled: true })}>+ 添加模型</Button></div>{modelsFA.fields.map((f, i) => (
          <div key={f.id} className="flex gap-2 items-center p-2 rounded-md border bg-muted/20"><Input {...register(`models.${i}.model_id`)} className="h-8 flex-1 font-mono" placeholder="model-id" /><Input {...register(`models.${i}.display_name`)} className="h-8 flex-1" placeholder="显示名称" /><div className="flex items-center gap-1 text-xs"><Switch checked={watch(`models.${i}.enabled`) ?? true} onCheckedChange={v => form.setValue(`models.${i}.enabled`, v)} /><span className="text-[10px]">{watch(`models.${i}.enabled`) === false ? "停用" : "启用"}</span></div><Button type="button" variant="ghost" size="icon" className="h-6 w-6" onClick={() => modelsFA.remove(i)}><Trash2 className="h-3.5 w-3.5" /></Button></div>
        ))}</div>
        <div className="space-y-2"><div className="flex items-center gap-2"><span className="text-xs font-medium">请求头</span><Button type="button" size="sm" variant="outline" className="h-6 text-xs ml-auto" onClick={() => headersFA.append({ name: "", value: "" })}>+ 添加请求头</Button></div>{headersFA.fields.map((f, i) => (
          <div key={f.id} className="flex gap-2"><Input {...register(`headers.${i}.name`)} className="h-8 flex-1" placeholder="Header-Name" /><Input {...register(`headers.${i}.value`)} className="h-8 flex-1" placeholder="value" /><Button type="button" variant="ghost" size="icon" className="h-6 w-6" onClick={() => headersFA.remove(i)}><Trash2 className="h-3.5 w-3.5" /></Button></div>
        ))}</div>
        <div className="flex justify-end gap-2 pt-2"><Button type="button" variant="outline" size="sm" onClick={testDraft} disabled={testing || isSubmitting}>{testing ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : null}测试</Button><Button type="button" variant="outline" size="sm" onClick={() => setOpen(false)}>取消</Button><Button size="sm" variant="forge" type="submit" disabled={isSubmitting}>提交</Button></div>
      </form></DialogContent></Dialog>
    </div>
  )
}
