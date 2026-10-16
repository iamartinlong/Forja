import { useMemo } from "react"
import ReactFlow, { Background, Controls, MiniMap } from "reactflow"
import "reactflow/dist/style.css"
import * as yaml from "js-yaml"

export function ERDPreview({ content }: { content: string }) {
  const { nodes, edges } = useMemo(() => {
    let data: any = null
    try {
      data = JSON.parse(content)
    } catch {
      try {
        data = yaml.load(content) as any
      } catch {
        return { nodes: [], edges: [] }
      }
    }
    if (!data || !data.entity_name) {
      return { nodes: [], edges: [] }
    }
    const fields: any[] = data.fields || []
    const mainNode = {
      id: "entity",
      data: { label: `${data.entity_name} (${fields.length} 字段)` },
      position: { x: 50, y: 50 },
      style: { background: "#E65100", color: "white", padding: 10, borderRadius: 8 },
    }
    const fieldNodes = fields.map((f: any, i: number) => ({
      id: `field-${i}`,
      data: { label: `${f.name}: ${f.type}${f.primary_key ? " PK" : ""}` },
      position: { x: 300, y: 50 + i * 50 },
      style: { background: "white", border: "1px solid #E65100", padding: 8, borderRadius: 6, fontSize: 12 },
    }))
    const fieldEdges = fields.map((_: any, i: number) => ({
      id: `e-entity-${i}`,
      source: "entity",
      target: `field-${i}`,
      animated: false,
    }))
    return { nodes: [mainNode, ...fieldNodes], edges: fieldEdges }
  }, [content])

  if (nodes.length === 0) {
    return <div className="text-xs text-muted-foreground p-4">未检测到 entity_name/fields，待补充 DS 设计</div>
  }
  return (
    <div className="border rounded-md overflow-hidden" style={{ height: 320 }}>
      <ReactFlow nodes={nodes} edges={edges} fitView>
        <Background />
        <Controls />
        <MiniMap />
      </ReactFlow>
    </div>
  )
}
export default ERDPreview
