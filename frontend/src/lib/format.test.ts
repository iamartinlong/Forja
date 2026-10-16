import { describe, it, expect } from 'vitest'
import { formatDuration, relativeTime } from './format'

describe('formatDuration', () => {
  it('空值与非法值返回占位符', () => {
    expect(formatDuration(0)).toBe('—')
    expect(formatDuration(-1)).toBe('—')
    expect(formatDuration(NaN)).toBe('—')
  })

  it('秒/分/分秒组合', () => {
    expect(formatDuration(5000)).toBe('5s')
    expect(formatDuration(60_000)).toBe('1m')
    expect(formatDuration(61_000)).toBe('1m1s')
    expect(formatDuration(185_000)).toBe('3m5s')
  })
})

describe('relativeTime', () => {
  it('非法时间原样返回', () => {
    expect(relativeTime('not-a-date')).toBe('not-a-date')
  })

  it('刚刚 / 分钟 / 小时 / 日期降级', () => {
    const now = Date.now()
    expect(relativeTime(new Date(now - 10_000).toISOString())).toBe('刚刚')
    expect(relativeTime(new Date(now - 5 * 60_000).toISOString())).toBe('5 分钟前')
    expect(relativeTime(new Date(now - 3 * 3_600_000).toISOString())).toBe('3 小时前')
    const old = new Date(now - 5 * 86_400_000)
    expect(relativeTime(old.toISOString())).toBe(old.toLocaleDateString())
  })
})
