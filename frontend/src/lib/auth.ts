// 前端权限判定（A4）：与 backend/internal/auth 权限矩阵对齐
// - 全局角色存 localStorage（登录时写入），仅做 UI 显隐；真正的鉴权在后端
// - 项目内操作优先使用接口返回的 my_role（ProjectDetail 等）

export type Role = "Admin" | "Architect" | "Developer" | "Viewer" | "PM" | "QA"

export function globalRole(): Role {
  return (localStorage.getItem("forja_role") as Role) || "Viewer"
}

export function isAdmin(): boolean {
  return globalRole() === "Admin"
}

// 平台级动作 → 允许的全局角色（对应 auth.go platformPermissions）
const PLATFORM_ALLOWED: Record<string, Role[]> = {
  "project.list": ["Admin", "Architect", "Developer", "Viewer", "PM", "QA"],
  "project.create": ["Admin", "Architect"],
  "project.update": ["Admin", "Architect"],
  "project.delete": ["Admin"],
  "project.member.manage": ["Admin", "Architect"],
  "user.list": ["Admin"],
  "user.create": ["Admin"],
  "user.update": ["Admin"],
  "user.delete": ["Admin"],
  "furnace.manage": ["Admin"],
}

// 项目级动作 → 允许的项目内角色（对应 auth.go projectPermissions）
const PROJECT_ALLOWED: Record<string, Role[]> = {
  "spec.read": ["Admin", "Architect", "Developer", "Viewer", "PM", "QA"],
  "task.read": ["Admin", "Architect", "Developer", "Viewer", "PM", "QA"],
  "inbox.read": ["Admin", "Architect", "Developer", "Viewer", "PM", "QA"],
  "spec.write": ["Admin", "Architect", "Developer", "QA"],
  // task.create 为后端 legacy 别名（→ task.write，auth.go legacyAliases），UI 判定用
  "task.create": ["Admin", "Architect", "Developer"],
  "task.write": ["Admin", "Architect", "Developer"],
  "task.approve": ["Admin", "Architect", "Developer"],
  "task.run": ["Admin", "Architect", "Developer", "QA"],
  "task.release": ["Admin", "Architect"],
  "iteration.release": ["Admin", "Architect"],
  "project.deliver": ["Admin", "Architect", "PM"],
  // v2.0 M3：验收反馈提交（Viewer 仅此写入口）
  "inbox.uat": ["Admin", "Architect", "Developer", "Viewer", "PM", "QA"],
  "inbox.write": ["Admin", "Architect", "Developer", "PM"],
  "inbox.convert": ["Admin", "Architect", "Developer", "PM"],
  "inbox.delete": ["Admin", "Architect", "Developer", "PM"],
  "spec.validate": ["Admin", "Architect"],
  "dag.read": ["Admin", "Architect", "Developer", "Viewer", "PM", "QA"],
  "audit.read": ["Admin", "Architect", "Developer", "Viewer", "PM", "QA"],
  "file.read": ["Admin", "Architect", "Developer", "Viewer", "PM", "QA"],
  "file.write": ["Admin", "Architect", "Developer", "PM"],
}

/**
 * 判定当前用户是否可执行 action。
 * @param action 权限动作，如 "project.delete"、"spec.validate"
 * @param projectRole 可选，项目内生效角色（接口返回的 my_role）；传入时按项目级矩阵判定
 */
export function can(action: string, projectRole?: string | null): boolean {
  if (isAdmin()) return true
  const role = globalRole()
  if (projectRole !== undefined && projectRole !== null) {
    // 项目级判定：全局 Admin 已在上面放行，其余以项目内角色为准
    return (PROJECT_ALLOWED[action] || []).includes(projectRole as Role)
  }
  return (PLATFORM_ALLOWED[action] || []).includes(role)
}
