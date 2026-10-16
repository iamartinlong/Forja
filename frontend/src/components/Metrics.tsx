import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { Cost, Specs, Tasks, Pipelines } from "@/lib/api"
import { useForjaStore } from "@/lib/store"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { InlineLoader } from "@/components/ui/page-loader"
import { BarChart, Bar, XAxis, YAxis, Tooltip, ResponsiveContainer, PieChart, Pie, Cell, ComposedChart, Line } from "recharts"

// observability P2：趋势卡接 /pipelines/metrics 真聚合（按日 success rate / 平均时长），
// 原 W1-W4 mock 已删除（G5 断链收口）

export function Metrics() {
  const projectId = useForjaStore(s => s.projectId)
  const { data: specs } = useQuery({ queryKey: ["specs-metrics", projectId], queryFn: () => Specs.list(undefined, projectId) })
  const { data: tasks } = useQuery({ queryKey: ["tasks-metrics", projectId], queryFn: () => Tasks.list(projectId) })
  // 授权范围由后端裁剪（Admin 全量 / 其余限成员项目），无需显式传 project_id
  const { data: metrics, isLoading: metricsLoading } = useQuery({
    queryKey: ["pipelines-metrics"],
    queryFn: () => Pipelines.metrics({ days: 14 }),
    refetchInterval: 60_000,
  })

  const items = (specs as any)?.items || []
  const titems = (tasks as any)?.items || []

  // R7 成本分包：按项目/周期聚合 agent_executions（服务端按角色裁剪范围）
  const [costGroup, setCostGroup] = useState<"project" | "period">("project")
  const [costPeriod, setCostPeriod] = useState<"day" | "week" | "month">("day")
  const { data: costReport, isLoading: costLoading } = useQuery({
    queryKey: ["cost-report", costGroup, costPeriod],
    queryFn: () => Cost.report({ group: costGroup, period: costGroup === "period" ? costPeriod : undefined }),
    refetchInterval: 60_000,
  })
  const costItems = costReport?.items ?? []

  // 4 metrics per roadmap: Spec pass, Agent adopt, Fix, Intervention
  const layerData = ["BS","IS","DS","TS"].map(l => ({ name: l, count: items.filter((s:any)=>s.layer===l).length }))
  const statusData = ["PENDING","RUNNING","VALIDATING","PASSED","RELEASE_PENDING","RELEASED","FAILED","FIXED","ESCALATED"].map(s => ({ name: s, value: titems.filter((t:any)=>t.status===s).length })).filter(d=>d.value>0)
  const pieColors = ["#FFB300","#E65100","#2E7D32","#C62828","#6A1B9A"]

  // 真聚合行：成功率（%）+ 平均时长（分钟）
  const trend = (metrics?.items ?? []).map(d => ({
    day: d.day.slice(5), // MM-DD
    rate: d.success_rate ?? null,
    minutes: d.avg_duration_ms != null ? Math.round((d.avg_duration_ms / 60_000) * 10) / 10 : null,
  }))

  return (
    <div className="grid grid-cols-2 gap-4">
      <Card>
        <CardHeader className="pb-2"><CardTitle className="text-sm">Spec 分层</CardTitle></CardHeader>
        <CardContent className="h-48">
          <ResponsiveContainer width="100%" height="100%">
            <BarChart data={layerData}><XAxis dataKey="name" /><YAxis allowDecimals={false} /><Tooltip /><Bar dataKey="count" fill="#E65100" radius={[4,4,0,0]} /></BarChart>
          </ResponsiveContainer>
        </CardContent>
      </Card>
      <Card>
        <CardHeader className="pb-2"><CardTitle className="text-sm">Task 状态</CardTitle></CardHeader>
        <CardContent className="h-48">
          <ResponsiveContainer width="100%" height="100%">
            <PieChart><Pie data={statusData} dataKey="value" nameKey="name" outerRadius={70} label>{statusData.map((_,i)=><Cell key={i} fill={pieColors[i%pieColors.length]} />)}</Pie><Tooltip /></PieChart>
          </ResponsiveContainer>
        </CardContent>
      </Card>
      <Card className="col-span-2">
        <CardHeader className="pb-2 flex flex-row items-center justify-between">
          <CardTitle className="text-sm">流水线趋势 · 近 14 天（按日成功率 / 平均时长，pipeline_runs 真聚合）</CardTitle>
          {metricsLoading && <InlineLoader />}
        </CardHeader>
        <CardContent className="h-56">
          {trend.length === 0 ? (
            <div className="flex h-full items-center justify-center text-xs text-muted-foreground">
              近 14 天暂无流水线记录 —— push 项目仓库触发 CI 后出现
            </div>
          ) : (
            <ResponsiveContainer width="100%" height="100%">
              <ComposedChart data={trend}>
                <XAxis dataKey="day" />
                <YAxis yAxisId="rate" domain={[0, 100]} unit="%" width={44} />
                <YAxis yAxisId="min" orientation="right" unit="m" width={40} />
                <Tooltip />
                <Line yAxisId="rate" type="monotone" dataKey="rate" name="成功率" stroke="#2E7D32" dot={{ r: 2 }} connectNulls />
                <Line yAxisId="min" type="monotone" dataKey="minutes" name="平均时长(m)" stroke="#E65100" dot={{ r: 2 }} connectNulls />
              </ComposedChart>
            </ResponsiveContainer>
          )}
        </CardContent>
      </Card>
      <Card className="col-span-2">
        <CardHeader className="pb-2 flex flex-row items-center gap-2">
          <CardTitle className="text-sm">成本分包 · agent_executions 聚合（Admin 全量 / 普通用户仅成员项目）</CardTitle>
          {costLoading && <InlineLoader />}
          <div className="ml-auto flex items-center gap-2">
            <select value={costGroup} onChange={(e) => setCostGroup(e.target.value as any)} className="border rounded-md h-8 px-2 text-xs bg-background" aria-label="成本分组">
              <option value="project">按项目</option>
              <option value="period">按周期</option>
            </select>
            <select value={costPeriod} onChange={(e) => setCostPeriod(e.target.value as any)} disabled={costGroup !== "period"} className="border rounded-md h-8 px-2 text-xs bg-background disabled:opacity-50" aria-label="周期粒度">
              <option value="day">按日</option>
              <option value="week">按周</option>
              <option value="month">按月</option>
            </select>
            <Button asChild size="sm" variant="outline" className="h-8">
              <a href={Cost.reportURL({ group: costGroup, period: costGroup === "period" ? costPeriod : undefined })}>导出 CSV</a>
            </Button>
          </div>
        </CardHeader>
        <CardContent>
          {costItems.length === 0 ? (
            <div className="text-xs text-muted-foreground py-6 text-center">暂无成本数据 —— 锻造产生 agent_executions 后展示</div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{costGroup === "period" ? `周期（${costPeriod === "day" ? "日" : costPeriod === "week" ? "周" : "月"}）` : "项目"}</TableHead>
                  <TableHead className="text-right">执行数</TableHead>
                  <TableHead className="text-right">成本（分）</TableHead>
                  <TableHead className="text-right">输入 tokens</TableHead>
                  <TableHead className="text-right">输出 tokens</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {costItems.map((r: any) => (
                  <TableRow key={r.key}>
                    <TableCell className="text-xs">{r.label && r.label !== "period" ? r.label : r.key}</TableCell>
                    <TableCell className="text-right text-xs">{r.executions}</TableCell>
                    <TableCell className="text-right text-xs">{r.cost_cents}</TableCell>
                    <TableCell className="text-right text-xs">{r.tokens_in}</TableCell>
                    <TableCell className="text-right text-xs">{r.tokens_out}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </div>
  )
}

export default Metrics
