import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { ReleaseDialog } from './ReleaseDialog'

vi.mock('@/lib/api', () => ({
  Tasks: {
    get: vi.fn(() =>
      Promise.resolve({
        forge_attempt: 2,
        commit_sha: 'abc1234def5678',
        code_changes: [{ file_path: 'src/login.ts' }],
        test_results: [],
      })
    ),
    release: vi.fn(() => Promise.resolve({})),
  },
}))

const noop = () => {}

describe('ReleaseDialog', () => {
  it('task=null 常驻挂载时不崩溃且不渲染弹窗内容（回归：forge_attempt 空指针）', () => {
    expect(() =>
      render(<ReleaseDialog task={null} onClose={noop} onDone={noop} />)
    ).not.toThrow()
    expect(screen.queryByText('发布签核')).not.toBeInTheDocument()
  })

  it('传入任务时渲染 attempt 徽标、sha 与确认文案', async () => {
    render(
      <ReleaseDialog
        task={{ id: 't1', title: '实现登录', forge_attempt: 2, commit_sha: 'abc1234def5678' }}
        onClose={noop}
        onDone={noop}
      />
    )
    expect(await screen.findByText('attempt 2')).toBeInTheDocument()
    expect(screen.getByText('sha abc1234')).toBeInTheDocument()
    expect(screen.getByText(/实现登录/)).toBeInTheDocument()
  })
})
