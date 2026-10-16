import { describe, it, expect, vi } from 'vitest'
import { readableActionRequired, statusVariant } from './ForgeStepper'

// 纯函数用例：模块级副作用（api/sse/router）全部 mock，避免真实依赖进入覆盖率分母
vi.mock('@/lib/api', () => ({ Tasks: { streamURL: vi.fn(() => 'http://test/stream') } }))
vi.mock('@/lib/useEventSource', () => ({ useEventSource: vi.fn(() => ({ connected: true })) }))
vi.mock('@tanstack/react-router', () => ({ Link: (p: any) => p.children }))

describe('ForgeStepper 纯函数', () => {
  it('statusVariant 全状态映射', () => {
    expect(statusVariant('RELEASED')).toBe('success')
    expect(statusVariant('PASSED')).toBe('success')
    expect(statusVariant('RUNNING')).toBe('forge')
    expect(statusVariant('RELEASE_PENDING')).toBe('forge')
    expect(statusVariant('FAILED')).toBe('destructive')
    expect(statusVariant('ESCALATED')).toBe('destructive')
    expect(statusVariant('PENDING')).toBe('secondary')
  })

  it('readableActionRequired：空值/数组/普通串/JSON 串/非法 JSON/非串', () => {
    expect(readableActionRequired(null)).toBe('')
    expect(readableActionRequired('')).toBe('')
    expect(readableActionRequired(['a', 'b'])).toBe('a；b')
    expect(readableActionRequired('plain text')).toBe('plain text')
    expect(readableActionRequired('["x","y"]')).toBe('x；y')
    expect(readableActionRequired('[not-json')).toBe('[not-json')
    expect(readableActionRequired(42)).toBe('42')
    expect(readableActionRequired('  spaced  ')).toBe('spaced')
  })
})
