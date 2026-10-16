import { http, ApiError } from "./http"
import { BASE } from "./http"
import { useForjaStore } from "./store"

// API 资源对象集合。底层为 lib/http.ts 的 axios 实例：
// - envelope 解包、auth 注入、全局错误 toast/401 跳转均在拦截器统一处理
// - 表单等需要内联展示错误的调用点传 { silent: true } 关闭全局 toast

export { ApiError }
export type ApiErrorType = ApiError

// A6：project_id 后端必填；未显式传参时取全局项目上下文（AppHeader 切换器）
function currentProjectId(): string {
  return useForjaStore.getState().projectId
}

export interface PageMeta {
  page: number
  page_size: number
  total: number
}

export interface ListResult<T = any> extends PageMeta {
  items: T[]
}

export interface ApiExtra {
  /** 静默模式：不弹全局 toast */
  silent?: boolean
}

export async function api<T = any>(path: string, opts: RequestInit = {}, extra: ApiExtra = {}): Promise<T> {
  const isForm = typeof FormData !== "undefined" && opts.body instanceof FormData
  const res = await http.request<T>({
    url: path,
    method: (opts.method as any) || "GET",
    data: opts.body ?? undefined,
    headers: {
      // JSON 字符串体显式声明；FormData 交由 axios 自动设置 multipart boundary
      ...(opts.body && !isForm ? { "Content-Type": "application/json" } : {}),
      ...(opts.headers as Record<string, string> | undefined),
    },
    silent: extra.silent,
  })
  return res.data
}

export function pageQuery(page = 1, pageSize = 20) {
  return `page=${page}&page_size=${pageSize}`
}

// ---- Spec 领域类型（图纸编辑器）----

export interface SpecListItem {
  id: string
  layer: string
  title: string
  status: string
  current_version: string
  /** §2b 增量关联：指向老 Spec 的 ID 列表 (frontend/src/lib/api.ts:54) */
  refs?: string[]
  /** §2b 代码锚点：允许修改的文件范围 (frontend/src/lib/api.ts:55) */
  anchors?: string[]
}

export interface SpecDetail extends SpecListItem {
  project_id: string
  /** spec_versions 最新版本号 */
  version: string
  content: Record<string, unknown>
}

export interface GateCheck {
  rule: string
  passed: boolean
  message: string
  blocking: boolean
}

export interface GateResult {
  passed: boolean
  checks: GateCheck[]
}

export interface PactInteraction {
  description?: string
  [key: string]: unknown
}

export interface Pact {
  consumer?: { name?: string }
  provider?: { name?: string }
  interactions: PactInteraction[]
  [key: string]: unknown
}

export interface SpecMutationResult {
  id: string
  version: string
  status: string
}

// ---- Spec 生命周期（observability P2，/specs/$specId 页数据源）----

export interface LifecycleSpec {
  id: string
  project_id: string
  layer: string
  title: string
  status: string
  current_version: string
  created_at: string
  updated_at: string
  refs?: string[]
  anchors?: string[]
}

export interface LifecycleRefSpec {
  id: string
  layer: string
  title: string
  status: string
}

export interface LifecycleVersion {
  id: string
  version: string
  change_log: string
  created_at: string
}

export interface LifecycleRequirement {
  id: string
  source_type: string
  snippet: string
  status: string
  created_at: string
}

export interface LifecycleTask {
  id: string
  title: string
  status: string
  commit_sha: string
  forge_attempt: number
  created_at: string
}

export interface LifecycleCodeChange {
  task_id: string
  file_path: string
  change_log: string
  created_at: string
}

export interface SpecLifecycle {
  spec: LifecycleSpec
  ref_specs?: LifecycleRefSpec[]
  requirements: LifecycleRequirement[]
  versions: LifecycleVersion[]
  tasks: LifecycleTask[]
  code_changes: LifecycleCodeChange[]
  ci_runs: PipelineRun[]
}

export const Specs = {
  list: (layer?: string, projectId?: string, page = 1, pageSize = 20): Promise<ListResult<SpecListItem>> =>
    api(`/api/v1/specs?project_id=${projectId || currentProjectId()}${layer ? `&layer=${layer}` : ""}&${pageQuery(page, pageSize)}`),
  create: (body: { layer: string; title: string; content: unknown; refs?: string[]; anchors?: string[] }): Promise<SpecMutationResult> =>
    api(`/api/v1/specs`, { method: "POST", body: JSON.stringify({ project_id: currentProjectId(), ...body }) }),
  update: (id: string, body: { title: string; content: unknown; refs?: string[]; anchors?: string[] }): Promise<SpecMutationResult> =>
    api(`/api/v1/specs/${id}`, { method: "PUT", body: JSON.stringify(body) }),
  get: (id: string): Promise<SpecDetail> => api(`/api/v1/specs/${id}`),
  refs: (id: string): Promise<{ spec_id: string; refs: (SpecListItem & { acceptance_criteria?: unknown })[] }> =>
    api(`/api/v1/specs/${id}/refs`),
  // P2：生命周期聚合（requirements/versions/tasks/code_changes/ci_runs 一次取齐，§2b 含 refs/anchors）
  lifecycle: (id: string): Promise<SpecLifecycle> =>
    api(`/api/v1/specs/${id}/lifecycle`),
  validate: (id: string): Promise<GateResult> => api(`/api/v1/specs/${id}/validate`, { method: "POST" }),
  pact: (id: string): Promise<{ pact: Pact }> => api(`/api/v1/specs/${id}/pact`, { method: "POST" }),
  getPact: (id: string): Promise<{ pact: Pact }> => api(`/api/v1/specs/${id}/pact`),
  search: (q: string, projectId?: string) => api(`/api/v1/specs/search?project_id=${projectId || currentProjectId()}&q=${encodeURIComponent(q)}`),
}

export const Tasks = {
  create: (body: any) => api(`/api/v1/tasks`, { method: "POST", body: JSON.stringify({ project_id: currentProjectId(), ...body }) }),
  batch: (items: any[]) => api(`/api/v1/tasks/batch`, { method: "POST", body: JSON.stringify(items.map((it) => ({ project_id: currentProjectId(), ...it }))) }),
  list: (projectId?: string, status?: string, page = 1, pageSize = 20) =>
    api(`/api/v1/tasks?project_id=${projectId || currentProjectId()}${status ? `&status=${status}` : ""}&${pageQuery(page, pageSize)}`),
  get: (id: string) => api(`/api/v1/tasks/${id}`),
  approve: (id: string) => api(`/api/v1/tasks/${id}/approve`, { method: "POST", body: JSON.stringify({ action: "approve" }) }),
  release: (id: string) => api(`/api/v1/tasks/${id}/release`, { method: "POST" }),
  reject: (id: string, comment?: string) => api(`/api/v1/tasks/${id}/reject`, { method: "POST", body: JSON.stringify({ comment: comment || "" }) }),
  patch: (id: string, body: any) => api(`/api/v1/tasks/${id}`, { method: "PATCH", body: JSON.stringify(body) }),
  // P1 真 SSE：useEventSource 走 fetch + Authorization header（M2 起 URL 不带 token）
  streamURL: (id: string) => `${BASE}/api/v1/tasks/${id}/stream`,
  dag: (projectId?: string) => api(`/api/v1/projects/${projectId || currentProjectId()}/dag`),
  estimate: (body: any) => api(`/api/v1/tasks/estimate`, { method: "POST", body: JSON.stringify({ project_id: currentProjectId(), ...body }) }),
  converge: (id: string) => api(`/api/v1/tasks/${id}/converge`, { method: "POST" }),
}

export const Auth = {
  login: (username: string, password: string) => api(`/api/v1/auth/login`, { method: "POST", body: JSON.stringify({ username, password }) }, { silent: true }),
  // silent：旧密码错误属字段级错误，由表单内联展示（AGENTS.md 错误分层）
  changePassword: (body: { old_password: string; new_password: string }) => api(`/api/v1/auth/change-password`, { method: "POST", body: JSON.stringify(body) }, { silent: true }),
  me: () => api(`/api/v1/auth/me`),
  // S1.2：登出吊销 refresh token（静默；失败不阻塞本地清理）
  logout: (refresh_token?: string | null) => api(`/api/v1/auth/logout`, { method: "POST", body: JSON.stringify({ refresh_token }) }, { silent: true }).catch(() => {}),
}

export interface InboxListItem {
  id: string
  source_type: string
  snippet: string
  status: string
  created_at: string
  updated_at?: string
  content_hash?: string
  created_by?: string
  creator?: string
}

export interface InboxDetail extends InboxListItem {
  project_id: string
  raw_content: string
  content_hash: string
  spec_ids: string[]
  creator?: string
  created_by?: string
}

export const Inbox = {
  create: (body: any, extra?: ApiExtra) => api(`/api/v1/inbox`, { method: "POST", body: JSON.stringify({ project_id: currentProjectId(), ...body }) }, extra),
  createFile: (file: File, projectId?: string) => {
    const fd = new FormData()
    fd.append("file", file)
    fd.append("project_id", projectId || currentProjectId())
    return api(`/api/v1/inbox`, { method: "POST", body: fd }, { silent: true })
  },
  list: (projectId?: string, status?: string, page = 1, pageSize = 20, extra?: ApiExtra) =>
    api(`/api/v1/inbox?project_id=${projectId || currentProjectId()}${status ? `&status=${status}` : ""}&${pageQuery(page, pageSize)}`, {}, extra),
  /** §2e+ server-side filters: q/source_type/created_by/time range/sort/order + pagination 50/page (frontend/src/lib/api.ts:219) */
  listFiltered: (params: { projectId?: string; status?: string; q?: string; source_type?: string; created_by?: string; created_from?: string; created_to?: string; sort?: string; order?: string; page?: number; pageSize?: number }, extra?: ApiExtra): Promise<ListResult<InboxListItem>> => {
    const p = new URLSearchParams()
    p.set("project_id", params.projectId || currentProjectId())
    if (params.status) p.set("status", params.status)
    if (params.q) p.set("q", params.q)
    if (params.source_type) p.set("source_type", params.source_type)
    if (params.created_by) p.set("created_by", params.created_by)
    if (params.created_from) p.set("created_from", params.created_from)
    if (params.created_to) p.set("created_to", params.created_to)
    if (params.sort) p.set("sort", params.sort)
    if (params.order) p.set("order", params.order)
    p.set("page", String(params.page ?? 1))
    p.set("page_size", String(params.pageSize ?? 20))
    return api(`/api/v1/inbox?${p.toString()}`, {}, extra)
  },
  get: (id: string): Promise<InboxDetail> => api(`/api/v1/inbox/${id}`),
  update: (id: string, body: any, extra?: ApiExtra) => api(`/api/v1/inbox/${id}`, { method: "PATCH", body: JSON.stringify(body) }, extra),
  remove: (id: string, extra?: ApiExtra) => api(`/api/v1/inbox/${id}`, { method: "DELETE" }, extra),
  draft: (id: string, extra?: ApiExtra) => api(`/api/v1/inbox/${id}/draft`, { method: "POST" }, extra),
  convert: (body: any) => api(`/api/v1/inbox/convert`, { method: "POST", body: JSON.stringify({ project_id: currentProjectId(), ...body }) }),
  /** v2.2 T3：UAT 反馈一键回炉（生成修复 Task，幂等） */
  reforge: (id: string): Promise<{ id: string; status: string; dedup: boolean }> =>
    api(`/api/v1/inbox/${id}/reforge`, { method: "POST" }),
  /** M4b：原料附件列表 / 下载（anchor 直链 access_token，与交付下载同约定） */
  attachments: (id: string): Promise<ListResult<any>> => api(`/api/v1/inbox/${id}/attachments`),
  attachmentURL: (attachmentId: string) =>
    `${BASE}/api/v1/attachments/${attachmentId}/download?access_token=${localStorage.getItem("forja_token") ?? ""}`,
}

/** M4b 项目资源库（上传/分类/列表/下载/删除） */
export const Files = {
  list: (projectId: string, category?: string, page = 1, pageSize = 20, extra?: ApiExtra): Promise<ListResult<any>> =>
    api(`/api/v1/projects/${projectId}/files?${category ? `category=${encodeURIComponent(category)}&` : ""}${pageQuery(page, pageSize)}`, {}, extra),
  upload: (projectId: string, body: { file: File; category?: string; summary?: string }, extra?: ApiExtra) => {
    const fd = new FormData()
    fd.append("file", body.file)
    if (body.category) fd.append("category", body.category)
    if (body.summary) fd.append("summary", body.summary)
    return api(`/api/v1/projects/${projectId}/files`, { method: "POST", body: fd }, extra)
  },
  remove: (projectId: string, fileId: string) =>
    api(`/api/v1/projects/${projectId}/files/${fileId}`, { method: "DELETE" }),
  downloadURL: (projectId: string, fileId: string) =>
    `${BASE}/api/v1/projects/${projectId}/files/${fileId}/download?access_token=${localStorage.getItem("forja_token") ?? ""}`,
}

export const Audit = {
  list: (projectId?: string, page = 1, pageSize = 20) =>
    api(`/api/v1/audit?project_id=${projectId || currentProjectId()}&${pageQuery(page, pageSize)}`),
  // R7 治理：审计导出（CSV/JSON 直链下载；access_token 锚点下载为既有例外）
  exportURL: (projectId?: string, format: "csv" | "json" = "csv") =>
    `${BASE}/api/v1/audit/export?project_id=${projectId || currentProjectId()}&format=${format}&access_token=${localStorage.getItem("forja_token") ?? ""}`,
}

// ---- 交付流水线（observability P0）----

export interface PipelineRun {
  id: string
  project_id?: string
  repo: string
  sha: string
  branch?: string
  event: string
  status: "RUNNING" | "SUCCESS" | "FAILURE" | "CANCELLED" | "PENDING" | string
  actor?: string
  duration_ms: number
  created_at: string
  updated_at: string
  /** 详情接口附带：步骤时间线 */
  jobs?: { name: string; conclusion: string; duration_ms: number; started_at: string; created_at: string }[]
}

export interface DeployedVersion {
  /** 短 sha（master-xxxx 形式的 tag 同值） */
  sha: string
  tag: string
  by: string
  at: string
}

/** S6 §11 部署实体：状态机 scheduled/running/success/failed */
export interface Deployment {
  id: string
  sha: string
  tag: string
  status: "scheduled" | "running" | "success" | "failed" | string
  mode: "manual" | "scheduled" | string
  actor?: string
  created_at: string
  deployed_at?: string
}

export interface PipelineSummary {
  running: number
  failed_today: number
  /** S6：待构建行数（push 已登记、等人工发起构建） */
  pending_builds?: number
  /** S6 §11：当前线上版本（deployments 表最新 success——真实已上线，非指令快照） */
  deployed?: DeployedVersion | null
  /** S6 §11：在途部署数（scheduled/running），摘要条「部署中」徽章 */
  deploying?: number
  last_run: PipelineRun | null
}

/** 按日聚合行（observability P2：Metrics 真数据源） */
export interface PipelineMetricsDay {
  day: string
  total: number
  success: number
  failure: number
  /** 百分比 0-100；无已完结 run 为 null */
  success_rate: number | null
  avg_duration_ms: number | null
}

export const Pipelines = {
  list: (opts: { projectId?: string; status?: string; page?: number; pageSize?: number } = {}) => {
    const p = new URLSearchParams()
    if (opts.projectId !== undefined) p.set("project_id", opts.projectId)
    if (opts.status) p.set("status", opts.status)
    p.set("page", String(opts.page ?? 1))
    p.set("page_size", String(opts.pageSize ?? 20))
    return api<ListResult<PipelineRun>>(`/api/v1/pipelines?${p.toString()}`)
  },
  get: (id: string) => api<PipelineRun>(`/api/v1/pipelines/${id}`),
  summary: (projectId?: string) =>
    api<PipelineSummary>(`/api/v1/pipelines/summary${projectId ? `?project_id=${projectId}` : ""}`),
  // P2 真聚合：按日 success rate / 平均时长（days 默认 14，上限 90）
  metrics: (opts: { projectId?: string; days?: number } = {}) => {
    const p = new URLSearchParams()
    if (opts.projectId !== undefined) p.set("project_id", opts.projectId)
    p.set("days", String(opts.days ?? 14))
    return api<{ days: number; items: PipelineMetricsDay[] }>(`/api/v1/pipelines/metrics?${p.toString()}`)
  },
  /** S6 构建部署分离：手动构建（kind=build）/ 部署（kind=deploy，delay_seconds>0 为定时）—— P0 扩展：可选 project_id 透传以实现按项目隔离在途互斥，空时后端回退 repo 映射 */
  dispatch: (body: { kind: "build" | "deploy"; sha: string; delay_seconds?: number; project_id?: string }) => {
    const payload = { ...body } as any
    if (!payload.project_id) {
      const pid = currentProjectId()
      if (pid) payload.project_id = pid
    }
    return api<{ queued: boolean; kind: string; scheduled_in_seconds?: number }>(`/api/v1/internal/gitea/dispatch`, {
      method: "POST",
      body: JSON.stringify(payload),
    })
  },
  /** S6 §11 部署记录（deployments 实体，分页倒序）—— U-2 支持 project_id 过滤，跟随 header 切换 */
  deployments: (opts: { projectId?: string; page?: number; pageSize?: number } | number = 1, pageSize = 10) => {
    let projectId: string | undefined
    let page: number
    let ps: number
    if (typeof opts === "object" && opts !== null && !Array.isArray(opts) && ("projectId" in opts || "page" in opts || "pageSize" in opts)) {
      const o = opts as { projectId?: string; page?: number; pageSize?: number }
      projectId = o.projectId
      page = o.page ?? 1
      ps = o.pageSize ?? 10
    } else {
      page = (opts as number) ?? 1
      ps = pageSize
    }
    const p = new URLSearchParams()
    if (projectId) p.set("project_id", projectId)
    p.set("page", String(page))
    p.set("page_size", String(ps))
    return api<ListResult<Deployment>>(`/api/v1/pipelines/deployments?${p.toString()}`)
  },
}

export const Projects = {
  // S1.4 软删除：默认仅活跃项目，archived=true 查归档项目
  list: (q?: string, page = 1, pageSize = 20, archived = false) =>
    api(`/api/v1/projects?${q ? `q=${encodeURIComponent(q)}&` : ""}${archived ? "archived=true&" : ""}${pageQuery(page, pageSize)}`),
  create: (body: any) => api(`/api/v1/projects`, { method: "POST", body: JSON.stringify(body) }),
  /** v2.0 可用脚手架模板（建项 Dialog） */
  templates: () => api<{ items: any[] }>(`/api/v1/project-templates`),
  /** v2.0 项目规范（仓内 AGENTS.md，真相在仓） */
  agents: (id: string): Promise<{ exists: boolean; content: string; sha: string; path: string }> =>
    api(`/api/v1/projects/${id}/agents`),
  updateAgents: (id: string, body: { content: string; sha?: string }) =>
    api(`/api/v1/projects/${id}/agents`, { method: "PUT", body: JSON.stringify(body) }, { silent: true }),
  generateAgents: (id: string) => api(`/api/v1/projects/${id}/agents/generate`, { method: "POST" }),
  /** v2.0 M1.2 构建通道：触发项目仓 build.yml（默认分支 HEAD） */
  builds: (id: string): Promise<{ queued: boolean; sha: string; branch: string }> =>
    api(`/api/v1/projects/${id}/builds`, { method: "POST" }),
  /** v2.0 M1.2 制品台账 */
  artifacts: (id: string, page = 1, pageSize = 20): Promise<ListResult<any>> =>
    api(`/api/v1/projects/${id}/artifacts?${pageQuery(page, pageSize)}`),
  /** v2.0 M1.3 交付包台账 */
  deliveries: (id: string, page = 1, pageSize = 20): Promise<ListResult<any>> =>
    api(`/api/v1/projects/${id}/deliveries?${pageQuery(page, pageSize)}`),
  /** v2.0 M1.3 组装/覆盖交付包（tag 幂等） */
  createDelivery: (id: string, body: { sha?: string; tag?: string } = {}) =>
    api(`/api/v1/projects/${id}/deliveries`, { method: "POST", body: JSON.stringify(body) }),
  /** v2.0 M1.3 Release Notes 预览（tag 缺省=最新制品） */
  releaseNotes: (id: string, tag?: string): Promise<{ tag: string; sha: string; image: string; tasks: number; notes: string }> =>
    api(`/api/v1/projects/${id}/releases/notes${tag ? `?tag=${encodeURIComponent(tag)}` : ""}`),
  /** 交付包 docs.zip 下载（anchor 直链；JWT 走 access_token，与 SSE 同约定） */
  deliveryDownloadURL: (id: string, did: string) =>
    `${BASE}/api/v1/projects/${id}/deliveries/${did}/download?access_token=${localStorage.getItem("forja_token") ?? ""}`,
  /** 镜像 tar.gz 下载（anchor 直链） */
  deliveryArtifactURL: (id: string, did: string) =>
    `${BASE}/api/v1/projects/${id}/deliveries/${did}/artifact?access_token=${localStorage.getItem("forja_token") ?? ""}`,
  /** v2.0 M3 验收反馈（UAT→Inbox）；带 file 时走 multipart */
  submitUAT: (
    id: string,
    body: { result: "pass" | "partial" | "fail"; content: string; title?: string; env?: string; delivery_id?: string; file?: File },
  ) => {
    const { file, ...rest } = body
    if (file) {
      const fd = new FormData()
      Object.entries(rest).forEach(([k, v]) => { if (v) fd.append(k, String(v)) })
      fd.append("file", file)
      return api(`/api/v1/projects/${id}/uat`, { method: "POST", body: fd })
    }
    return api(`/api/v1/projects/${id}/uat`, { method: "POST", body: JSON.stringify(rest) })
  },
  get: (id: string) => api(`/api/v1/projects/${id}`),
  update: (id: string, body: any) => api(`/api/v1/projects/${id}`, { method: "PATCH", body: JSON.stringify(body) }),
  /** 归档项目（软删除，可恢复） */
  archive: (id: string) => api(`/api/v1/projects/${id}`, { method: "DELETE" }),
  /** 恢复归档项目 */
  restore: (id: string) => api(`/api/v1/projects/${id}/restore`, { method: "POST" }),
  /** 彻底删除：级联清除全部数据，不可恢复；仅限已归档项目 */
  purge: (id: string) => api(`/api/v1/projects/${id}?purge=true`, { method: "DELETE" }),
  members: (id: string) => api(`/api/v1/projects/${id}/members`),
  addMember: (id: string, body: { user_id: string; role?: string }) =>
    api(`/api/v1/projects/${id}/members`, { method: "POST", body: JSON.stringify(body) }),
  updateMember: (id: string, userId: string, body: { role: string }) =>
    api(`/api/v1/projects/${id}/members/${userId}`, { method: "PATCH", body: JSON.stringify(body) }),
  removeMember: (id: string, userId: string) =>
    api(`/api/v1/projects/${id}/members/${userId}`, { method: "DELETE" }),
  // P1 真 SSE：项目频道（徽章/PipelineBoard ci.* 脏通知；M2 起 URL 不带 token，hook 走 Authorization header）
  eventsURL: (id: string) => `${BASE}/api/v1/projects/${id}/events`,
}

export const Users = {
  list: (q?: string, page = 1, pageSize = 20) =>
    api(`/api/v1/admin/users${q ? `?q=${encodeURIComponent(q)}&` : "?"}${pageQuery(page, pageSize)}`),
  get: (id: string) => api(`/api/v1/admin/users/${id}`),
  create: (body: any) => api(`/api/v1/admin/users`, { method: "POST", body: JSON.stringify(body) }),
  update: (id: string, body: any) => api(`/api/v1/admin/users/${id}`, { method: "PATCH", body: JSON.stringify(body) }),
  remove: (id: string) => api(`/api/v1/admin/users/${id}`, { method: "DELETE" }),
  enable: (id: string) => api(`/api/v1/admin/users/${id}/enable`, { method: "POST" }),
  disable: (id: string) => api(`/api/v1/admin/users/${id}/disable`, { method: "POST" }),
  loginLogs: (id: string, page = 1, pageSize = 20, opts: { success?: boolean } = {}) => {
    const p = new URLSearchParams()
    p.set("page", String(page)); p.set("page_size", String(pageSize))
    if (opts.success !== undefined) p.set("success", String(opts.success))
    return api(`/api/v1/admin/users/${id}/login-logs?${p.toString()}`)
  },
}

export const Me = {
  loginLogs: (page = 1, pageSize = 20, opts: { success?: boolean } = {}) => {
    const p = new URLSearchParams()
    p.set("page", String(page)); p.set("page_size", String(pageSize))
    if (opts.success !== undefined) p.set("success", String(opts.success))
    return api(`/api/v1/auth/me/login-logs?${p.toString()}`)
  },
}

export const Iterations = {
  list: (projectId: string) => api<{ items: any[] }>(`/api/v1/projects/${projectId}/iterations`),
  create: (projectId: string, body: any) => api(`/api/v1/projects/${projectId}/iterations`, { method: "POST", body: JSON.stringify(body) }),
  patch: (projectId: string, iterId: string, body: any) => api(`/api/v1/projects/${projectId}/iterations/${iterId}`, { method: "PATCH", body: JSON.stringify(body) }),
  release: (projectId: string, iterId: string) => api(`/api/v1/projects/${projectId}/iterations/${iterId}/release`, { method: "POST" }),
}

export const Cost = {
  get: (projectId: string, opts: { iteration_id?: string; group_by?: string } = {}) => {
    const p = new URLSearchParams()
    if (opts.iteration_id) p.set("iteration_id", opts.iteration_id)
    if (opts.group_by) p.set("group_by", opts.group_by)
    const qs = p.toString() ? `?${p.toString()}` : ""
    return api<{ summary: any; breakdown: any[] }>(`/api/v1/projects/${projectId}/cost${qs}`)
  },
  // R7 成本分包：跨项目/周期聚合（Admin 全量，普通用户仅成员项目）
  report: (opts: { group?: "project" | "period"; period?: "day" | "week" | "month"; projectId?: string; from?: string; to?: string } = {}) => {
    const p = new URLSearchParams()
    if (opts.group) p.set("group", opts.group)
    if (opts.period) p.set("period", opts.period)
    if (opts.projectId) p.set("project_id", opts.projectId)
    if (opts.from) p.set("from", opts.from)
    if (opts.to) p.set("to", opts.to)
    return api<{ group: string; period?: string; items: any[]; count: number }>(`/api/v1/cost/report?${p.toString()}`)
  },
  reportURL: (opts: { group?: "project" | "period"; period?: "day" | "week" | "month"; projectId?: string; from?: string; to?: string } = {}) => {
    const p = new URLSearchParams()
    if (opts.group) p.set("group", opts.group)
    if (opts.period) p.set("period", opts.period)
    if (opts.projectId) p.set("project_id", opts.projectId)
    if (opts.from) p.set("from", opts.from)
    if (opts.to) p.set("to", opts.to)
    p.set("format", "csv")
    p.set("access_token", localStorage.getItem("forja_token") ?? "")
    return `${BASE}/api/v1/cost/report?${p.toString()}`
  },
}

export const Releases = {
  list: (projectId: string, iterationId?: string) => {
    const qs = iterationId ? `?iteration_id=${iterationId}` : ""
    return api<{ items: any[] }>(`/api/v1/projects/${projectId}/releases${qs}`)
  },
}

export const Furnaces = {
  list: () => api<{ items: any[] }>(`/api/v1/furnaces`),
  get: (id: string) => api<any>(`/api/v1/furnaces/${id}`),
  create: (body: any) => api(`/api/v1/furnaces`, { method: "POST", body: JSON.stringify(body) }),
  patch: (id: string, body: any) => api(`/api/v1/furnaces/${id}`, { method: "PATCH", body: JSON.stringify(body) }),
  remove: (id: string) => api(`/api/v1/furnaces/${id}`, { method: "DELETE" }),
  recover: (id: string) => api(`/api/v1/furnaces/${id}/recover`, { method: "POST" }),
  testChat: (id: string, body: { prompt?: string; model?: string }) => api(`/api/v1/furnaces/${id}/test-chat`, { method: "POST", body: JSON.stringify(body) }),
  testPreviewChat: (body: any) => api(`/api/v1/furnaces/test-preview-chat`, { method: "POST", body: JSON.stringify(body) }),
}
