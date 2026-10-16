import { beforeEach, describe, expect, it, vi } from "vitest"
import { AxiosError, AxiosHeaders, type InternalAxiosRequestConfig } from "axios"
import { http, ApiError, consumeRedirect } from "./http"

vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn() },
}))

import { toast } from "sonner"

// 用自定义 adapter 模拟后端响应
function mockAdapter(handler: (config: InternalAxiosRequestConfig) => any) {
  http.defaults.adapter = async (config) => handler(config)
}
function axiosResponse(config: any, status: number, data: unknown) {
  return {
    data,
    status,
    statusText: String(status),
    headers: { "content-type": "application/json" },
    config,
  }
}

beforeEach(() => {
  vi.clearAllMocks()
  localStorage.clear()
  Object.defineProperty(window, "location", {
    value: { pathname: "/tasks", search: "", assign: vi.fn() },
    writable: true,
  })
})

describe("envelope 解包", () => {
  it("code=0 时返回 data 直接量", async () => {
    mockAdapter((cfg) => axiosResponse(cfg, 200, { code: 0, message: "ok", data: { id: "x1" }, trace_id: "t-1" }))
    const data = await http.get("/api/v1/tasks/1")
    expect(data.data).toEqual({ id: "x1" })
  })

  it("请求头注入 Authorization", async () => {
    localStorage.setItem("forja_token", "tok-123")
    let seen: any = null
    mockAdapter((cfg) => {
      seen = cfg.headers
      return axiosResponse(cfg, 200, { code: 0, data: null })
    })
    await http.get("/api/v1/tasks")
    expect(new AxiosHeaders(seen).Authorization).toBe("Bearer tok-123")
  })
})

describe("错误分类兜底", () => {
  it("4xx 业务错误：抛 ApiError 并弹 toast", async () => {
    mockAdapter((cfg) => {
      throw new AxiosError("Bad Request", "400", cfg, null, axiosResponse(cfg, 400, {
        code: 40001, message: "invalid request",
        error: { code: "INVALID_PARAM", detail: "title required", field: "title" },
        trace_id: "t-2",
      }))
    })
    await expect(http.get("/api/v1/tasks")).rejects.toMatchObject({
      code: 400,
      errCode: "INVALID_PARAM",
      detail: "title required",
      field: "title",
      traceId: "t-2",
      bizCode: 40001,
    })
    expect(toast.error).toHaveBeenCalledWith("title required")
  })

  it("silent=true 不弹 toast", async () => {
    mockAdapter((cfg) => {
      throw new AxiosError("Bad Request", "400", cfg, null, axiosResponse(cfg, 400, {
        code: 40001, message: "invalid request", error: { code: "INVALID_PARAM", detail: "bad" },
      }))
    })
    await expect(http.get("/x", { silent: true })).rejects.toBeInstanceOf(ApiError)
    expect(toast.error).not.toHaveBeenCalled()
  })

  it("403 弹权限不足", async () => {
    mockAdapter((cfg) => {
      throw new AxiosError("Forbidden", "403", cfg, null, axiosResponse(cfg, 403, {
        code: 40004, message: "forbidden", error: { code: "FORBIDDEN", detail: "Admin only" },
      }))
    })
    await expect(http.get("/x")).rejects.toBeInstanceOf(ApiError)
    expect(toast.error).toHaveBeenCalledWith("权限不足")
  })

  it("5xx 弹服务异常并带追踪号", async () => {
    mockAdapter((cfg) => {
      throw new AxiosError("Internal", "500", cfg, null, axiosResponse(cfg, 500, {
        code: 50002, message: "database error", error: { code: "DB_ERROR", detail: "boom" }, trace_id: "t-9",
      }))
    })
    await expect(http.get("/x")).rejects.toBeInstanceOf(ApiError)
    expect(toast.error).toHaveBeenCalledWith("服务异常，请稍后重试（追踪号 t-9）")
  })

  it("网络错误：code=0 NETWORK_ERROR + 友好提示", async () => {
    mockAdapter(() => {
      throw new AxiosError("Network Error", "ECONNABORTED")
    })
    await expect(http.get("/x")).rejects.toMatchObject({ code: 0, errCode: "NETWORK_ERROR" })
    expect(toast.error).toHaveBeenCalledWith("网络连接失败，请检查网络后重试")
  })
})

describe("401 处理", () => {
  it("清除 token 并跳转登录页（带回跳地址）", async () => {
    localStorage.setItem("forja_token", "expired")
    mockAdapter((cfg) => {
      throw new AxiosError("Unauthorized", "401", cfg, null, axiosResponse(cfg, 401, {
        code: 40005, message: "unauthorized", error: { code: "UNAUTHORIZED", detail: "invalid token" },
      }))
    })
    await expect(http.get("/api/v1/tasks")).rejects.toBeInstanceOf(ApiError)
    expect(localStorage.getItem("forja_token")).toBeNull()
    expect(window.location.assign).toHaveBeenCalledWith("/login?redirect=%2Ftasks")
  })

  it("登录接口本身的 401 不触发跳转", async () => {
    mockAdapter((cfg) => {
      throw new AxiosError("Unauthorized", "401", cfg, null, axiosResponse(cfg, 401, {
        code: 40005, message: "unauthorized", error: { code: "UNAUTHORIZED", detail: "invalid credentials" },
      }))
    })
    await expect(http.post("/api/v1/auth/login")).rejects.toBeInstanceOf(ApiError)
    expect(window.location.assign).not.toHaveBeenCalled()
  })

  it("S1.2：401 时静默刷新并重放原请求（新 token 写回存储）", async () => {
    localStorage.setItem("forja_token", "expired")
    localStorage.setItem("forja_refresh", "rt-1")
    mockAdapter((cfg) => {
      if (cfg.url === "/api/v1/auth/refresh") {
        return axiosResponse(cfg, 200, { code: 0, data: { token: "new-tok", refresh_token: "rt-2" } })
      }
      if (new AxiosHeaders(cfg.headers).Authorization !== "Bearer new-tok") {
        throw new AxiosError("Unauthorized", "401", cfg, null, axiosResponse(cfg, 401, {
          code: 40005, message: "unauthorized",
        }))
      }
      return axiosResponse(cfg, 200, { code: 0, data: { ok: true } })
    })
    const res = await http.get("/api/v1/tasks")
    expect(res.data).toEqual({ ok: true })
    expect(localStorage.getItem("forja_token")).toBe("new-tok")
    expect(localStorage.getItem("forja_refresh")).toBe("rt-2")
    expect(window.location.assign).not.toHaveBeenCalled()
  })

  it("S1.2：刷新失败则清凭证并跳登录", async () => {
    localStorage.setItem("forja_token", "expired")
    localStorage.setItem("forja_refresh", "rt-dead")
    mockAdapter((cfg) => {
      if (cfg.url === "/api/v1/auth/refresh") {
        throw new AxiosError("Unauthorized", "401", cfg, null, axiosResponse(cfg, 401, {
          code: 40005, message: "unauthorized", error: { code: "UNAUTHORIZED" },
        }))
      }
      throw new AxiosError("Unauthorized", "401", cfg, null, axiosResponse(cfg, 401, {
        code: 40005, message: "unauthorized",
      }))
    })
    await expect(http.get("/api/v1/tasks")).rejects.toBeInstanceOf(ApiError)
    expect(localStorage.getItem("forja_refresh")).toBeNull()
    expect(window.location.assign).toHaveBeenCalledWith("/login?redirect=%2Ftasks")
  })
})

describe("consumeRedirect", () => {
  it("仅接受站内路径", () => {
    Object.defineProperty(window, "location", {
      value: { pathname: "/login", search: "?redirect=%2Ftasks" },
      writable: true,
    })
    expect(consumeRedirect()).toBe("/tasks")

    Object.defineProperty(window, "location", {
      value: { pathname: "/login", search: "?redirect=https%3A%2F%2Fevil.com" },
      writable: true,
    })
    expect(consumeRedirect()).toBeNull()
  })
})
