import axios from "axios"
import type { AxiosError, AxiosResponse, InternalAxiosRequestConfig } from "axios"
import { toast } from "sonner"

// 全局 HTTP 基础设施（axios）
// - 请求拦截：注入 Authorization
// - 响应拦截：解包统一 envelope {code,message,data,trace_id}，错误分类兜底
//   网络错误 / 401 跳登录 / 403 权限 / 4xx 业务错 / 5xx 服务异常
// 契约见 docs/architecture/api-contract.md

// v2.0 M2：生产走同源（nginx /api 反代 → api:8080），BASE 留空即相对路径；
// 本地 dev（vite :5173）未配 VITE_API_URL 时回落 :8080 直连。
export const BASE = import.meta.env.VITE_API_URL || (import.meta.env.DEV ? "http://localhost:8080" : "")

export class ApiError extends Error {
  /** HTTP 状态码；网络错误为 0 */
  code: number
  /** envelope 业务码（40001 等） */
  bizCode?: number
  /** 机器可读错误码 INVALID_PARAM 等 */
  errCode?: string
  detail?: string
  field?: string
  traceId?: string

  constructor(
    message: string,
    code: number,
    errCode?: string,
    detail?: string,
    field?: string,
    traceId?: string,
    bizCode?: number,
  ) {
    super(detail ? `${message}: ${detail}` : message)
    this.name = "ApiError"
    this.code = code
    this.bizCode = bizCode
    this.errCode = errCode
    this.detail = detail
    this.field = field
    this.traceId = traceId
  }
}

declare module "axios" {
  export interface AxiosRequestConfig {
    /** 静默模式：不弹全局 toast（表单等内联展示错误的场景） */
    silent?: boolean
  }
}

export const http = axios.create({
  baseURL: BASE,
  timeout: 30_000,
})

http.interceptors.request.use((cfg) => {
  const t = localStorage.getItem("forja_token")
  if (t) cfg.headers.Authorization = `Bearer ${t}`
  return cfg
})

/** 主动登出的静默窗口：窗口期内的 401 不弹 toast、不重复跳登录 */
let authSilentUntil = 0
export function silenceAuthErrors(ms = 2000) {
  authSilentUntil = Date.now() + ms
}

const REFRESH_KEY = "forja_refresh"

/** 当前保存的 refresh token（S1.2 静默续期用） */
export function getRefreshToken(): string | null {
  return localStorage.getItem(REFRESH_KEY)
}

function clearAuthStorage() {
  localStorage.removeItem("forja_token")
  localStorage.removeItem(REFRESH_KEY)
  localStorage.removeItem("forja_role")
  localStorage.removeItem("forja_user")
}

// S1.2 单飞静默续期：并发 401 共享同一次刷新请求；失败清除 refresh 凭证
let refreshing: Promise<string | null> | null = null

async function silentRefresh(): Promise<string | null> {
  const rt = getRefreshToken()
  if (!rt) return null
  if (!refreshing) {
    refreshing = (async () => {
      try {
        // 走本实例以复用 adapter/envelope；url 命中 auth 白名单，不会触发递归跳转
        const res = await http.post("/api/v1/auth/refresh", { refresh_token: rt }, { silent: true })
        // 响应拦截器已解包 envelope：res 即 data
        const d: any = (res as any)?.data ?? res
        if (!d?.token) return null
        localStorage.setItem("forja_token", d.token)
        if (d.refresh_token) localStorage.setItem(REFRESH_KEY, d.refresh_token)
        return d.token as string
      } catch {
        localStorage.removeItem(REFRESH_KEY)
        return null
      } finally {
        refreshing = null
      }
    })()
  }
  return refreshing
}

function redirectToLogin() {
  clearAuthStorage()
  const { pathname, search } = window.location
  if (pathname === "/login") return
  const redirect = encodeURIComponent(pathname + search)
  window.location.assign(`/login?redirect=${redirect}`)
}

interface EnvelopeShape {
  code: number
  message?: string
  data?: unknown
  error?: { code?: string; detail?: string; field?: string }
  trace_id?: string
}

function isEnvelope(v: unknown): v is EnvelopeShape {
  return !!v && typeof v === "object" && "code" in (v as Record<string, unknown>)
}

function failUnlessSilent(cfg: { silent?: boolean } | undefined, fn: () => void) {
  if (!cfg?.silent) fn()
}

function buildApiError(status: number, body: unknown): ApiError {
  if (isEnvelope(body)) {
    return new ApiError(
      body.message || "请求失败",
      status,
      body.error?.code,
      body.error?.detail,
      body.error?.field,
      body.trace_id,
      body.code,
    )
  }
  return new ApiError(`请求失败 (${status})`, status, "HTTP_ERROR", typeof body === "string" ? body : undefined)
}

http.interceptors.response.use(
  (res: AxiosResponse) => {
    // SSE 等非 JSON 载荷原样透传
    if (!isEnvelope(res.data)) return res
    if (res.data.code !== 0) {
      // 防御：HTTP 200 但业务失败
      const err = buildApiError(res.status, res.data)
      failUnlessSilent(res.config, () => toast.error(err.detail || err.message))
      return Promise.reject(err)
    }
    res.data = res.data.data
    return res
  },
  async (error: AxiosError) => {
    const cfg = (error.config || {}) as InternalAxiosRequestConfig
    const status = error.response?.status

    // 网络层错误（断网 / 超时 / CORS）
    if (!error.response) {
      const err = new ApiError("网络连接失败", 0, "NETWORK_ERROR", error.message)
      failUnlessSilent(cfg, () => toast.error("网络连接失败，请检查网络后重试"))
      return Promise.reject(err)
    }

    const err = buildApiError(status!, error.response.data)

    if (status === 401) {
      const url = cfg.url || ""
      const isAuthPath =
        url.includes("/auth/login") || url.includes("/auth/refresh") || url.includes("/auth/logout")
      // S1.2：业务请求 401 → 单飞静默续期后重放一次；刷新失败才跳登录
      if (!isAuthPath && !(cfg as any)._retried) {
        ;(cfg as any)._retried = true
        const newTok = await silentRefresh()
        if (newTok) {
          return http.request(cfg) // 请求拦截器会注入新 token
        }
        if (Date.now() >= authSilentUntil) {
          failUnlessSilent(cfg, () => toast.error("登录已过期，请重新登录"))
          redirectToLogin()
        }
      }
      return Promise.reject(err)
    } else if (status === 403) {
      failUnlessSilent(cfg, () => toast.error("权限不足"))
    } else if (status! >= 500) {
      const suffix = err.traceId ? `（追踪号 ${err.traceId}）` : ""
      failUnlessSilent(cfg, () => toast.error(`服务异常，请稍后重试${suffix}`))
    } else {
      // 4xx 业务错误：展示后端 detail
      failUnlessSilent(cfg, () => toast.error(err.detail || err.message))
    }
    return Promise.reject(err)
  },
)

/** 登录成功后回跳地址（供 login 页读取） */
export function consumeRedirect(): string | null {
  const r = new URLSearchParams(window.location.search).get("redirect")
  if (!r || !r.startsWith("/") || r.startsWith("//")) return null
  return r
}
