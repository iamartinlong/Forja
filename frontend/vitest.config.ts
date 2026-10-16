import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'
import path from 'path'

export default defineConfig({
  plugins: [react()],
  resolve: { alias: { "@": path.resolve(import.meta.dirname, "./src") } },
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    globals: true,
    // CI 节点（DinD 全栈并存）下 jsdom 用例偶发 >5s（run228/232 连续三个用例超时），
    // 本地单跑均 <1s；coverage(v8) 插桩叠加宿主争抢后曾两次击穿 15s（run341
    // TasksBoard/ForgePanel 两例 15s 超时，npm test 步同用例全过）——
    // 阈值再放宽到 30s，覆盖"资源饥饿型假失败"，真实挂起由用例内 waitFor 兜底
    testTimeout: 30_000,
    coverage: { provider: 'v8', reporter: ['text', 'html'], thresholds: { lines: 90 } },
    exclude: ['e2e/**', 'node_modules/**'],
  },
})
