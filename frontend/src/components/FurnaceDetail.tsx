import { useEffect, useState } from "react"
import { useParams, useNavigate } from "@tanstack/react-router"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Furnaces } from "@/lib/api"
import { toast } from "sonner"
import { ArrowLeft, FlaskConical, Loader2, RotateCcw } from "lucide-react"
import { PageLoader } from "@/components/ui/page-loader"

export default function FurnaceDetail() {
  const params = useParams({ strict: false }) as any
  const id = params.id as string
  const navigate = useNavigate()
  const [data, setData] = useState<any>(null)
  const [loading, setLoading] = useState(true)
  const [prompt, setPrompt] = useState("ping")
  const [testing, setTesting] = useState(false)
  const [recovering, setRecovering] = useState(false)
  const [result, setResult] = useState<any>(null)

  const load = async () => {
    setLoading(true)
    try { const r: any = await Furnaces.get(id); setData(r.data || r) } catch (e: any) { toast.error(e.message) } finally { setLoading(false) }
  }
  useEffect(() => { if (id) load() }, [id])

  const recover = async () => {
    setRecovering(true)
    try {
      const r: any = await Furnaces.recover(id)
      const data = r.data || r
      toast.success(`已恢复（${data.status}，熔断计数清零）`)
      load()
    } catch (e: any) { toast.error(e.message) } finally { setRecovering(false) }
  }

  const test = async () => {
    setTesting(true); setResult(null)
    try {
      const r: any = await Furnaces.testChat(id, { prompt })
      setResult(r.data || r)
      if ((r.data || r).status === "healthy") toast.success(`可用 ${(r.data || r).latency_ms}ms`)
      else toast.error((r.data || r).detail || "不可用")
      load()
    } catch (e: any) { toast.error(e.message); setResult({ status: "unhealthy", detail: e.message }) } finally { setTesting(false) }
  }

  if (loading) return <PageLoader />
  if (!data) return <div className="text-sm p-4">未找到</div>
  const displayModel = data.model_profile?.model ? `${data.model_profile.provider_id || "forja"}/${data.model_profile.model}` : (data.id === "forja-default" ? "forja/deepseek-v4-flash" : null)

  return (
    <div className="space-y-4">
      <Card>
        <CardHeader className="flex-row items-center gap-2">
          <Button variant="ghost" size="icon" className="h-8 w-8" onClick={() => navigate({ to: "/admin/furnaces" })}><ArrowLeft className="h-4 w-4" /></Button>
          <CardTitle className="text-sm">{data.name}</CardTitle>
          <Badge variant="outline" className="text-[10px] font-mono">{data.id.slice(0,8)}</Badge>
          <Badge variant={data.status==="healthy"?"success": data.status==="drained"?"secondary":"destructive"} className="text-[10px]">{data.status}</Badge>
          {((data.consecutive_failures ?? 0) > 0 || data.status === "unhealthy") && (
            <Button size="sm" variant="outline" className="h-7" disabled={recovering} onClick={recover} title="清零熔断计数并拉起容器">
              {recovering ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <RotateCcw className="h-3.5 w-3.5" />} 手动恢复
            </Button>
          )}
          <span className="ml-auto text-xs text-muted-foreground">{data.created_at?.slice(0,10)}</span>
        </CardHeader>
        <CardContent className="text-xs space-y-2">
          <div>URL: <span className="font-mono">{data.base_url}</span></div>
          <div>容量: {data.busy ?? 0}/{data.capacity} {data.project_id ? <Badge variant="secondary" className="text-[10px] ml-2">{data.project_id.slice(0,8)}</Badge> : <Badge variant="outline" className="text-[10px] ml-2">共享池</Badge>}</div>
          <div>模型: {displayModel ? displayModel : <span className="text-muted-foreground">—</span>}</div>
          <div>战绩: {data.exec_total ? `${data.exec_success ?? 0}/${data.exec_total} ${(((data.success_rate ?? ((data.exec_success ?? 0) / data.exec_total)) * 100)).toFixed(0)}%` : <span className="text-muted-foreground">暂无锻造</span>} <span className="ml-2 text-muted-foreground" title={`in:${data.tokens_in ?? 0} out:${data.tokens_out ?? 0}`}>token {((data.tokens_in ?? 0) + (data.tokens_out ?? 0))}</span> {(data.consecutive_failures ?? 0) > 0 && <span className="ml-2">熔断: <Badge variant={data.breaker_tripped ? "destructive" : "secondary"} className="text-[10px]">{data.breaker_tripped ? "已熔断" : `连败${data.consecutive_failures}次`}</Badge></span>}</div>
          <div>标签: {Array.isArray(data.labels) && data.labels.length ? data.labels.map((l:string)=> <Badge key={l} variant="secondary" className="text-[10px] mr-1">{l}</Badge>) : <span className="text-muted-foreground">—</span>}</div>
        </CardContent>
      </Card>

      <Tabs defaultValue="test">
        <TabsList>
          <TabsTrigger value="basic">基本信息</TabsTrigger>
          <TabsTrigger value="test">可用性测试</TabsTrigger>
        </TabsList>
        <TabsContent value="basic" className="mt-4">
          <Card><CardHeader><CardTitle className="text-sm">基本信息</CardTitle></CardHeader><CardContent className="text-xs space-y-2">
            <div>名称: {data.name}</div>
            <div>Base URL: {data.base_url}</div>
            <div>容量: {data.capacity}</div>
            <div>状态: <Badge variant="outline" className="text-[10px]">{data.status}</Badge> busy {data.busy ?? 0}（连续 3 次 FAILED 自动 unhealthy；探活成功或手动恢复回 healthy，恢复清零计数）</div>
            <div>执行: {data.exec_total ? `共 ${data.exec_total} 次，成功 ${data.exec_success ?? 0} 次` : "暂无"} token in:{data.tokens_in ?? 0} out:{data.tokens_out ?? 0}</div>
            <div>项目: {data.project_id || "共享池"}</div>
            <div>标签: {JSON.stringify(data.labels)}</div>
            <div>模型: {displayModel ? displayModel : JSON.stringify(data.model_profile)}</div>
            <div>Auth: {data.auth_ref_masked || "—"}</div>
            <div>创建时间: {data.created_at}</div>
            <div>更新时间: {data.updated_at}</div>
          </CardContent></Card>
        </TabsContent>
        <TabsContent value="test" className="mt-4">
          <Card><CardHeader><CardTitle className="text-sm">对话试跑（端到端）</CardTitle></CardHeader><CardContent className="space-y-3">
            <div className="text-xs text-muted-foreground">通过炉 {data.base_url} 调 opencode /session/message 模型 {displayModel || "deepseek-v4-flash"}，30s 超时，回写 status</div>
            <div className="flex gap-2">
              <Input value={prompt} onChange={e=>setPrompt(e.target.value)} placeholder="ping" className="h-8 flex-1" />
              <Button size="sm" variant="forge" onClick={test} disabled={testing} className="h-8">{testing ? <Loader2 className="h-4 w-4 animate-spin" /> : <FlaskConical className="h-4 w-4" />} 测试</Button>
            </div>
            {result && (
              <div className="border rounded p-3 text-xs space-y-2 bg-muted/20">
                <div>状态: <Badge variant={result.status==="healthy"?"success":"destructive"} className="text-[10px]">{result.status}</Badge> {result.latency_ms ? `${result.latency_ms}ms` : ""} {result.base_url && <span className="ml-2 font-mono text-[10px]">{result.base_url}</span>}</div>
                {result.reply && <div>响应: <span className="font-mono break-all">{result.reply.slice(0,500)}</span> {result.reply==="pong" && <span className="text-muted-foreground">(空回退，见下方原始)</span>}</div>}
                {result.detail && <div className="text-destructive break-all">错误: {result.detail.slice(0,800)}</div>}
                {result.model && <div>模型: {result.provider}/{result.model} {result.prompt && <span className="ml-2">prompt: <span className="font-mono">{result.prompt}</span></span>}</div>}
                {result.session_id && <div>会话: <span className="font-mono">{result.session_id}</span></div>}
                {result.info && (
                  <div className="space-y-1">
                    <div>上游: {result.info?.modelID || result.info?.providerID ? `${result.info.providerID}/${result.info.modelID}` : "—"} {typeof result.info?.cost==="number" ? `cost:${result.info.cost}` : ""} {result.info?.tokens ? `tokens in:${result.info.tokens.input} out:${result.info.tokens.output}` : ""}</div>
                    {result.info?.error && <div className="text-destructive">上游错误: {result.info.error.name} {JSON.stringify(result.info.error.data).slice(0,500)}</div>}
                    {Array.isArray(result.parts) && result.parts.length>0 && <div>parts: <span className="font-mono">{JSON.stringify(result.parts).slice(0,400)}</span></div>}
                  </div>
                )}
                {result.upstream_error && <div className="text-destructive">上游错误: {JSON.stringify(result.upstream_error).slice(0,500)}</div>}
                {result.raw && (
                  <details className="mt-2">
                    <summary className="cursor-pointer text-muted-foreground">原始响应 (raw)</summary>
                    <pre className="mt-1 p-2 bg-background rounded border overflow-auto max-h-60 text-[10px] whitespace-pre-wrap break-all">{typeof result.raw==="string" ? result.raw.slice(0,2000) : JSON.stringify(result.raw,null,2).slice(0,2000)}</pre>
                  </details>
                )}
              </div>
            )}
          </CardContent></Card>
        </TabsContent>
      </Tabs>
    </div>
  )
}
