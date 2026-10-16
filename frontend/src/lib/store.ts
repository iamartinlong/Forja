import { create } from "zustand"

// 种子项目 forja 的固定 UUID（与后端 model.DefaultProjectID、001_init.sql 一致）。
// 仅作 localStorage 缺失/非法时的兜底；正常路径由 AppHeader 切换器写入。
export const DEFAULT_PROJECT_ID = "00000000-0000-0000-0000-000000000001"

const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

// A6 审计修复：存量 localStorage 可能残留 "ALL" 等非法值（后端已必填校验），初始化时清洗
// 2026-09-08：Header 显示读 forja_project、业务查询读 store（forja_project_id），
// 双源曾导致"Header 显示 A、看板查 B"（手动重选才好）。收敛到 store 单一真相源，
// 初始化兼容 legacy 键（双键分叉的浏览器下次加载自愈）。
function initialProjectId(): string {
  const v1 = localStorage.getItem("forja_project_id")
  if (v1 && UUID_RE.test(v1)) return v1
  const legacy = localStorage.getItem("forja_project")
  if (legacy && UUID_RE.test(legacy)) return legacy
  return DEFAULT_PROJECT_ID
}

type ForjaState = {
  projectId: string
  setProjectId: (id: string) => void
  tab: string
  setTab: (t: string) => void
}

export const useForjaStore = create<ForjaState>((set) => ({
  projectId: initialProjectId(),
  setProjectId: (projectId) => {
    localStorage.setItem("forja_project_id", projectId);
    localStorage.setItem("forja_project", projectId); // legacy
    set({ projectId });
  },
  tab: "specs",
  setTab: (tab) => set({ tab }),
}))
