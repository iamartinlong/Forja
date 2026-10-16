import { useEffect, useState } from "react"
import { useParams, useNavigate } from "@tanstack/react-router"
import { toast } from "sonner"
import { api } from "@/lib/api"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { ArrowLeft } from "lucide-react"
import { PageLoader } from "@/components/ui/page-loader"

export default function ModelProviderDetail() {
  const { id } = useParams({ strict: false }) as { id: string }
  const navigate = useNavigate()
  const [provider, setProvider] = useState<any>(null)
  const [refs, setRefs] = useState<any[]>([])
  useEffect(() => {
    if (!id) return
    api(`/api/v1/model-providers/${id}`).then((r: any) => setProvider(r)).catch((e: any) => toast.error(e.message))
    api(`/api/v1/model-providers/${id}/references`).then((r: any) => setRefs(r.furnaces || [])).catch(() => {})
  }, [id])
  if (!provider) return <PageLoader />
  return (
    <div className="space-y-4 w-full">
      <Card>
        <CardContent className="pt-4">
          <div className="flex items-start gap-3">
            <Button size="sm" variant="ghost" className="h-7 px-1.5 mt-0.5" onClick={() => navigate({ to: "/admin/model-providers" })}><ArrowLeft className="h-4 w-4" /></Button>
            <div className="min-w-0 flex-1">
              <div className="flex items-center gap-2 flex-wrap">
                <h2 className="text-lg font-semibold">{provider.display_name}</h2>
                <Badge variant="outline" className="text-[10px]">{provider.provider_id}</Badge>
                <Badge variant={provider.status==="healthy"?"secondary":"destructive"} className="text-[10px]">{provider.status}</Badge>
              </div>
              <div className="text-xs text-muted-foreground mt-1">
                创建于 {provider.created_at ? new Date(provider.created_at).toLocaleString("zh-CN", { hour12: false }) : "-"} · 更新于 {provider.updated_at ? new Date(provider.updated_at).toLocaleString("zh-CN", { hour12: false }) : "-"}
              </div>
            </div>
          </div>
        </CardContent>
      </Card>
      <Tabs defaultValue="basic">
        <TabsList><TabsTrigger value="basic">基本信息</TabsTrigger><TabsTrigger value="refs">引用炉 ({refs.length})</TabsTrigger></TabsList>
        <TabsContent value="basic" className="space-y-4">
          <Card><CardHeader className="pb-2"><CardTitle className="text-sm">基本信息</CardTitle></CardHeader><CardContent className="space-y-2 text-xs"><div>提供商ID: <span className="font-mono">{provider.provider_id}</span></div><div>显示名称: {provider.display_name}</div><div>基础URL: {provider.base_url}</div><div>认证模式: <Badge variant="outline">{provider.auth_mode}</Badge> · API密钥: ••••••</div><div>状态: <Badge variant={provider.status==="healthy"?"outline":"destructive"}>{provider.status}</Badge></div></CardContent></Card>
          <Card><CardHeader className="pb-2"><CardTitle className="text-sm">模型 ({(provider.models||[]).length})</CardTitle></CardHeader><CardContent>{(provider.models||[]).length ? <Table><TableHeader><TableRow><TableHead>模型ID</TableHead><TableHead>显示名称</TableHead><TableHead>状态</TableHead></TableRow></TableHeader><TableBody>{(provider.models||[]).map((m: any) => <TableRow key={m.model_id}><TableCell className="text-xs font-mono">{m.model_id}</TableCell><TableCell className="text-xs">{m.display_name || "-"}</TableCell><TableCell><Badge variant={m.enabled===false?"secondary":"outline"} className="text-[10px]">{m.enabled===false?"停用":"启用"}</Badge></TableCell></TableRow>)}</TableBody></Table> : <div className="text-xs text-muted-foreground p-2">暂无模型</div>}</CardContent></Card>
        </TabsContent>
        <TabsContent value="refs"><Card><CardContent className="pt-4">{refs.length ? <Table><TableHeader><TableRow><TableHead>炉名</TableHead><TableHead>URL</TableHead></TableRow></TableHeader><TableBody>{refs.map((f: any) => <TableRow key={f.id}><TableCell className="text-xs">{f.name}</TableCell><TableCell className="text-xs">{f.base_url}</TableCell></TableRow>)}</TableBody></Table> : <div className="text-xs text-muted-foreground">暂无炉引用，可安全删除</div>}</CardContent></Card></TabsContent>
      </Tabs>
    </div>
  )
}
