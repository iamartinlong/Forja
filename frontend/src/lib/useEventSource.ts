import { useEffect, useRef, useState } from "react"

// 轻量 SSE 客户端（observability P1，design §4.2；v2.0 M2 改 fetch 流式）
// - v2.0 M2：用 fetch + Authorization header，不再把 token 放 ?access_token= 查询参数
//   （同源 /api 反代后 URL 不再携带凭证；EventSource 无法带 header，故改 ReadableStream 解析）
// - 断线自动重连 ≤3 次（2s/4s/6s 退避），超限置 offline——数据层仍有轮询兜底；
//   SSE 只做"脏通知"，收到事件由调用方 invalidate query 统一重拉
export interface SSEMessage {
  name: string
  data?: any
}

export function useEventSource(url: string | null, onEvent?: (m: SSEMessage) => void) {
  const [connected, setConnected] = useState(false)
  const handlerRef = useRef(onEvent)
  handlerRef.current = onEvent

  useEffect(() => {
    if (!url) {
      setConnected(false)
      return
    }
    let closed = false
    let retries = 0
    let controller: AbortController | null = null
    let timer: ReturnType<typeof setTimeout> | null = null

    const dispatch = (payload: string) => {
      try {
        handlerRef.current?.(JSON.parse(payload))
      } catch {
        /* 非 JSON 心跳等忽略 */
      }
    }

    const connect = async () => {
      if (closed) return
      controller = new AbortController()
      const token = localStorage.getItem("forja_token") ?? ""
      try {
        const res = await fetch(url, {
          headers: token ? { Authorization: `Bearer ${token}` } : {},
          signal: controller.signal,
        })
        if (!res.ok || !res.body) throw new Error(`sse http ${res.status}`)
        retries = 0
        setConnected(true)
        const reader = res.body.getReader()
        const decoder = new TextDecoder()
        let buf = ""
        for (;;) {
          const { value, done } = await reader.read()
          if (done) break
          buf += decoder.decode(value, { stream: true })
          let idx: number
          while ((idx = buf.indexOf("\n\n")) >= 0) {
            const chunk = buf.slice(0, idx)
            buf = buf.slice(idx + 2)
            const data = chunk
              .split("\n")
              .filter((l) => l.startsWith("data:"))
              .map((l) => l.slice(5).trim())
              .join("\n")
            if (data) dispatch(data)
          }
        }
      } catch {
        /* AbortError 或网络断开：走下方重连 */
      }
      setConnected(false)
      if (closed) return
      retries++
      if (retries <= 3) {
        timer = setTimeout(connect, 2000 * retries)
      } // 超过 3 次放弃：保持离线，轮询兜底继续生效
    }
    connect()

    return () => {
      closed = true
      if (timer) clearTimeout(timer)
      controller?.abort()
      setConnected(false)
    }
  }, [url])

  return { connected }
}
