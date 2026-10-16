import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Badge } from "@/components/ui/badge"
import { Inbox } from "@/lib/api"

type Msg = { role: "user" | "assistant"; text: string }

export function ChatPanel() {
  const [msgs, setMsgs] = useState<Msg[]>([{ role: "assistant", text: "FORJA 神匠已就绪。输入自然语言需求，我将按 BS 规范熔炼草稿 (forja-plan)。" }])
  const [input, setInput] = useState("")
  const [loading, setLoading] = useState(false)
  const qc = useQueryClient()

  const send = async () => {
    if (!input.trim() || loading) return
    const q = input.trim()
    setMsgs(m => [...m, { role: "user", text: q }])
    setInput("")
    setLoading(true)
    try {
      // POST /inbox → forja-plan draft；失败时降级为本地模拟草稿
      let draftText = ""
      try {
        const created: any = await Inbox.create({ raw_content: q, source_type: "TEXT_PASTE" }, { silent: true })
        const id = created.id
        try {
          const j2: any = await Inbox.draft(id, { silent: true })
          draftText = `草稿已生成：\n标题: ${j2.title || "-"} \nAC: ${JSON.stringify(j2.acceptance_criteria || j2, null, 2)}\n缺失: ${(j2.missing||[]).join(", ")||"无"}`
        } catch {
          draftText = `已入原料堆 ${id}，草稿生成需 forja-plan 在线，可去 /inbox 看板查看`
        }
      } catch {
        // fallback mock when /inbox not yet deployed
        draftText = `模拟草稿 (Inbox 未部署)：\n{\n  "story_id": "S-AUTO-1",\n  "title": "${q.slice(0,12)}",\n  "acceptance_criteria": [{"given":"用户已登录","when":"${q}","then":"操作成功"}]\n}`
      }
      setMsgs(m => [...m, { role: "assistant", text: draftText }])
      qc.invalidateQueries({ queryKey: ["specs-metrics"] })
    } catch (e: any) {
      setMsgs(m => [...m, { role: "assistant", text: `出错: ${e.message}` }])
    } finally {
      setLoading(false)
    }
  }

  return (
    <Card>
      <CardHeader className="pb-2 flex flex-row items-center justify-between">
        <CardTitle className="text-sm">AI 对话 — forja-plan 只读熔炼</CardTitle>
        <Badge variant="forge">P3</Badge>
      </CardHeader>
      <CardContent className="space-y-3">
        <div className="h-64 overflow-auto border rounded-md p-3 bg-muted/30 space-y-2">
          {msgs.map((m,i) => (
            <div key={i} className={`flex ${m.role==="user"?"justify-end":"justify-start"}`}>
              <div className={`max-w-[80%] rounded-lg px-3 py-2 text-xs whitespace-pre-wrap ${m.role==="user" ? "bg-primary text-primary-foreground" : "bg-card border"}`}>{m.text}</div>
            </div>
          ))}
          {loading && <div className="text-xs text-muted-foreground">熔炼中…</div>}
        </div>
        <div className="flex gap-2">
          <Input value={input} onChange={e=>setInput(e.target.value)} placeholder="输入：作为用户，我想… (回车发送)" onKeyDown={e=>{ if(e.key==="Enter") send() }} />
          <Button variant="forge" onClick={send} disabled={loading}>发送</Button>
        </div>
        <div className="text-xs text-muted-foreground">背后：POST /inbox → forja-plan (prompt 0.1 只读) → 结构化 BS 草稿 → 人审转 BS</div>
      </CardContent>
    </Card>
  )
}

export default ChatPanel
