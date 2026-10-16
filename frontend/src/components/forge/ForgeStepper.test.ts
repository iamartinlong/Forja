import { describe, it, expect, vi } from 'vitest'
import { deriveStage, reduceForgeEvent, readableActionRequired, FORGE_STAGES } from './ForgeStepper'

// reducer 为纯函数测试：mock 掉副作用模块（同时避免把真实 lib/api 拖入覆盖率统计图）
vi.mock('@/lib/api', () => ({ Tasks: { streamURL: vi.fn(() => 'http://test/stream') } }))
vi.mock('@/lib/useEventSource', () => ({ useEventSource: vi.fn(() => ({ connected: false })) }))

// 回归 audit-2026-08-29：Stepper 阶段与事件错位（CI ✓失真、forge.merged/error 被静默丢弃）
describe('reduceForgeEvent', () => {
  it('阶段④命名对齐真实事件源：合并推送而非 CI 构建', () => {
    expect(FORGE_STAGES[3]).toBe('合并推送')
  })

  it('推送不标记阶段④完成，仅置 CI 待构建；forge.merged 才推进（回归 CI ✓失真）', () => {
    let s = deriveStage('RUNNING')
    s = reduceForgeEvent(s, { name: 'forge.attempt', data: { attempt: 1 } }).state
    s = reduceForgeEvent(s, { name: 'agent.done', data: { files: 2 } }).state
    s = reduceForgeEvent(s, { name: 'validate.result', data: { passed: true, attempt: 1 } }).state
    expect(s.done).toBe(2)
    const pushed = reduceForgeEvent(s, { name: 'forge.pushed', data: { sha: 'abc1234def' } }).state
    expect(pushed.done).toBe(2)
    expect(pushed.ci).toBe('pending')
    expect(pushed.sha).toBe('abc1234def')
    const merged = reduceForgeEvent(pushed, { name: 'forge.merged', data: { base_branch: 'master' } })
    expect(merged.state.done).toBe(3)
    expect(merged.log).toContain('master')
  })

  it('ci.passed/ci.failed 驱动 CI 徽章（task 频道双发事件）', () => {
    let s = deriveStage('RELEASE_PENDING')
    const passed = reduceForgeEvent(s, { name: 'ci.passed', data: { job: 'build-push', sha: 'x' } })
    expect(passed.state.ci).toBe('passed')
    const failed = reduceForgeEvent(passed.state, { name: 'ci.failed', data: { job: 'e2e' } })
    expect(failed.state.ci).toBe('failed')
    expect(failed.log).toContain('e2e')
  })

  it('门禁失败后 ESCALATED 保留 failAt=2（门禁校验），不笼统回落 Agent 锻造', () => {
    let s = deriveStage('RUNNING')
    s = reduceForgeEvent(s, { name: 'validate.result', data: { passed: false } }).state
    expect(s.failAt).toBe(2)
    s = reduceForgeEvent(s, { name: 'status', data: { status: 'ESCALATED', reason: 'max attempts exhausted' } }).state
    expect(s.status).toBe('ESCALATED')
    expect(s.failAt).toBe(2)
    expect(s.reason).toContain('max attempts')
  })

  it('无门禁失败的 ESCALATED 落在当前活动阶段', () => {
    let s = deriveStage('RUNNING')
    s = reduceForgeEvent(s, { name: 'forge.attempt', data: { attempt: 1 } }).state
    s = reduceForgeEvent(s, { name: 'status', data: { status: 'ESCALATED' } }).state
    expect(s.failAt).toBe(1)
  })

  it('snapshot 不覆盖本地进度，仅补 attempt/sha/status', () => {
    let s = deriveStage('RUNNING')
    s = reduceForgeEvent(s, { name: 'forge.attempt', data: { attempt: 2 } }).state
    s = reduceForgeEvent(s, { name: 'snapshot', data: { status: 'RUNNING', forge_attempt: 2 } }).state
    expect(s.attempt).toBe(2)
    expect(s.active).toBe(1)
  })

  it('forge.error 过程性异常只记日志，不置终态红叉', () => {
    const s = deriveStage('RUNNING')
    const r = reduceForgeEvent(s, { name: 'forge.error', data: { attempt: 1, stage: 'push', error: 'boom' } })
    expect(r.state).toEqual(s)
    expect(r.log).toContain('push')
    expect(r.log).toContain('boom')
  })

  it('forge.attempt 重开新一轮时清除上一轮失败标记', () => {
    let s = deriveStage('RUNNING')
    s = reduceForgeEvent(s, { name: 'validate.result', data: { passed: false } }).state
    expect(s.failAt).toBe(2)
    s = reduceForgeEvent(s, { name: 'forge.attempt', data: { attempt: 2 } }).state
    expect(s.failAt).toBe(-1)
    expect(s.attempt).toBe(2)
  })
})

describe('readableActionRequired', () => {
  it('JSON 数组字符串转人类可读（回归：原样输出 ["opencode not reachable..."]）', () => {
    expect(readableActionRequired('["opencode not reachable", "manual review"]')).toBe('opencode not reachable；manual review')
  })
  it('普通字符串原样、空值返回空串', () => {
    expect(readableActionRequired('please review')).toBe('please review')
    expect(readableActionRequired('')).toBe('')
    expect(readableActionRequired(null)).toBe('')
  })
})
