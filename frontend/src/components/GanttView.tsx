import { useMemo } from "react"
import { Gantt, ViewMode } from "gantt-task-react"
import "gantt-task-react/dist/index.css"

type Props = { nodes: any[]; edges: any[] }

export function GanttView({ nodes, edges }: Props) {
  const tasks = useMemo(() => {
    const now = new Date()
    return nodes.map((n: any, idx: number) => {
      const start = new Date(now.getTime() + idx * 86400000 * 0.5)
      const end = new Date(start.getTime() + (n.estimated_hours || 4) * 3600000)
      // progress based on status
      const progress = n.status === "PASSED" ? 100 : n.status === "RUNNING" ? 50 : n.status === "FAILED" ? 20 : 0
      const depIds: string[] = edges.filter((e: any) => e.to === n.id).map((e: any) => e.from)
      return {
        id: n.id,
        name: n.title,
        start,
        end,
        progress,
        type: "task" as const,
        dependencies: depIds,
        styles: { progressColor: n.status === "PASSED" ? "#2E7D32" : n.status === "RUNNING" ? "#E65100" : "#9E9E9E", progressSelectedColor: "#FFB300" },
      }
    })
  }, [nodes, edges])

  if (tasks.length === 0) return <div className="text-sm text-muted-foreground p-4">暂无任务</div>
  return (
    <div className="border rounded-lg overflow-auto bg-white">
      <Gantt tasks={tasks} viewMode={ViewMode.Day} columnWidth={60} listCellWidth="180px" />
    </div>
  )
}

export default GanttView
