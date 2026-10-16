import { useMemo } from "react"
import SwaggerUI from "swagger-ui-react"
import "swagger-ui-react/swagger-ui.css"
import * as yaml from "js-yaml"

export function SwaggerPreview({ content }: { content: string }) {
  const spec = useMemo(() => {
    if (!content.trim()) return null
    try {
      return JSON.parse(content)
    } catch {
      try {
        return yaml.load(content) as any
      } catch {
        return null
      }
    }
  }, [content])

  if (!spec) {
    return <div className="text-xs text-muted-foreground p-4">OpenAPI 解析失败，请检查 JSON/YAML 语法</div>
  }
  if (!spec.paths) {
    return <div className="text-xs text-muted-foreground p-4">未检测到 paths，待补充 OpenAPI</div>
  }
  return (
    <div className="border rounded-md overflow-auto max-h-[320px] bg-white">
      <SwaggerUI spec={spec} />
    </div>
  )
}
export default SwaggerPreview
