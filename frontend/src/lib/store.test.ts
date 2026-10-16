import { describe, it, expect, beforeEach, vi } from 'vitest'
import { DEFAULT_PROJECT_ID } from './store'

// 回归（2026-09-08）：Header 显示读 forja_project、业务查询读 store（forja_project_id），
// 双键分叉时"Header 显示 A、看板查 B"，进看板列表为空，必须手动重选才好。
// 修：store 单一真相源 + 初始化兼容 legacy 键自愈；Header 直绑 store。
describe('useForjaStore 项目单源', () => {
  const AUDIT = '01a0704b-276e-79df-b5e7-7b72cdd44079'
  beforeEach(() => {
    localStorage.clear()
    vi.resetModules()
  })

  it('forja_project_id 有效时采用', async () => {
    localStorage.setItem('forja_project_id', AUDIT)
    const { useForjaStore } = await import('./store')
    expect(useForjaStore.getState().projectId).toBe(AUDIT)
  })

  it('双键分叉时 legacy 键自愈（Header 显示即查询）', async () => {
    localStorage.setItem('forja_project', AUDIT)
    const { useForjaStore } = await import('./store')
    expect(useForjaStore.getState().projectId).toBe(AUDIT)
  })

  it('双键皆无/非法时回退默认项目', async () => {
    const { useForjaStore } = await import('./store')
    expect(useForjaStore.getState().projectId).toBe(DEFAULT_PROJECT_ID)
    localStorage.setItem('forja_project_id', 'ALL')
    localStorage.setItem('forja_project', 'not-a-uuid')
    vi.resetModules()
    const m2 = await import('./store')
    expect(m2.useForjaStore.getState().projectId).toBe(DEFAULT_PROJECT_ID)
  })

  it('setProjectId 双键同写（不再分叉）', async () => {
    const { useForjaStore } = await import('./store')
    useForjaStore.getState().setProjectId(AUDIT)
    expect(localStorage.getItem('forja_project_id')).toBe(AUDIT)
    expect(localStorage.getItem('forja_project')).toBe(AUDIT)
    expect(useForjaStore.getState().projectId).toBe(AUDIT)
  })
})
