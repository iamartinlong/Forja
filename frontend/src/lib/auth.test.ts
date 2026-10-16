import { describe, it, expect, beforeEach } from 'vitest'
import { can } from './auth'

// 回归：task.create 曾不在前端权限矩阵，导致 CreateTaskDialog 对非 Admin 渲染 null、
// /forge 与 /tasks 的「派生锻造任务」按钮点击无反应（audit-2026-08-29 A1）
describe('can() 项目级权限矩阵', () => {
  beforeEach(() => { localStorage.clear() })

  it('Developer/Architect 具备 task.create（legacy 别名 → task.write）', () => {
    localStorage.setItem('forja_role', 'Developer')
    expect(can('task.create', 'Developer')).toBe(true)
    expect(can('task.write', 'Developer')).toBe(true)
    localStorage.setItem('forja_role', 'Architect')
    expect(can('task.create', 'Architect')).toBe(true)
  })

  it('PM/QA/Viewer 不可建任务', () => {
    localStorage.setItem('forja_role', 'PM')
    expect(can('task.create', 'PM')).toBe(false)
    localStorage.setItem('forja_role', 'QA')
    expect(can('task.create', 'QA')).toBe(false)
    localStorage.setItem('forja_role', 'Viewer')
    expect(can('task.create', 'Viewer')).toBe(false)
  })

  it('Admin 短路放行（与项目内角色无关）', () => {
    localStorage.setItem('forja_role', 'Admin')
    expect(can('task.create', 'Viewer')).toBe(true)
    expect(can('task.release', 'Viewer')).toBe(true)
  })

  it('QA 可 task.run 重跑验证，但不可 approve/release（编写者与放行者分离）', () => {
    localStorage.setItem('forja_role', 'QA')
    expect(can('task.run', 'QA')).toBe(true)
    expect(can('task.approve', 'QA')).toBe(false)
    expect(can('task.release', 'QA')).toBe(false)
  })
})
