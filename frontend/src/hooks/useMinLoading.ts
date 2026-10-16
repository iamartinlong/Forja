import { useEffect, useRef, useState } from "react"

/**
 * 最小展示时长 loading — 避免 <200ms 闪烁
 * loading=true 立即显；loading=false 延时 ms 后才隐
 */
export function useMinLoading(loading: boolean, ms = 400) {
  const [visible, setVisible] = useState(false)
  const timer = useRef<number | null>(null)

  useEffect(() => {
    if (loading) {
      if (timer.current) { window.clearTimeout(timer.current); timer.current = null }
      setVisible(true)
    } else {
      if (visible) {
        timer.current = window.setTimeout(() => setVisible(false), ms) as unknown as number
        return () => { if (timer.current) window.clearTimeout(timer.current) }
      }
    }
  }, [loading, ms, visible])

  // 卸载清理
  useEffect(() => () => { if (timer.current) window.clearTimeout(timer.current) }, [])
  return visible
}
