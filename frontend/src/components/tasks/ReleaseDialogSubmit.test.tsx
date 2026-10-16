import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { ReleaseDialog } from './ReleaseDialog'

const m = vi.hoisted(() => ({
  get: vi.fn(() =>
    Promise.resolve({
      forge_attempt: 3,
      commit_sha: 'abcdef1234567',
      code_changes: [{ file_path: 'src/login.ts' }, { file_path: 'src/auth.ts' }],
      test_results: [{ name: 'unit-login', status: 'failed', output: 'expected 401 got 200' }],
    })
  ),
  release: vi.fn(() => Promise.resolve({})),
}))

vi.mock('@/lib/api', () => ({ Tasks: { get: m.get, release: m.release } }))

const task = { id: 't1', title: '实现登录', forge_attempt: 3, commit_sha: 'abcdef1234567' }

describe('ReleaseDialog 提交与失败测试展示', () => {
  beforeEach(() => vi.clearAllMocks())

  it('展示变更文件与未通过测试，输入任务名匹配后方可发布', async () => {
    const onDone = vi.fn()
    const onClose = vi.fn()
    render(<ReleaseDialog task={task} onClose={onClose} onDone={onDone} />)
    // 以查询结果元素为就绪信号：attempt 3 来自 task prop（首帧即有），
    // 先等它会在慢机上抢跑（CI run380 实录），必须等 Tasks.get 数据渲染后再断言。
    expect(await screen.findByText(/src\/login\.ts/)).toBeInTheDocument()
    expect(screen.getByText('attempt 3')).toBeInTheDocument()
    expect(screen.getByText(/未通过测试 1 项/)).toBeInTheDocument()
    expect(screen.getByText(/expected 401 got 200/)).toBeInTheDocument()

    const submit = screen.getByRole('button', { name: '确认发布' })
    expect(submit).toBeDisabled()

    fireEvent.change(screen.getByLabelText(/输入任务名「实现登录」确认/), { target: { value: '实现登' } })
    expect(screen.getByText('任务名不匹配')).toBeInTheDocument()
    expect(submit).toBeDisabled()

    fireEvent.change(screen.getByLabelText(/输入任务名「实现登录」确认/), { target: { value: '实现登录' } })
    expect(submit).toBeEnabled()
    fireEvent.click(submit)
    await waitFor(() => expect(m.release).toHaveBeenCalledWith('t1'))
    await waitFor(() => expect(onDone).toHaveBeenCalled())
    expect(onClose).toHaveBeenCalled()
  })

  it('接口失败时不关闭弹窗', async () => {
    m.release.mockRejectedValueOnce(new Error('release failed'))
    const onDone = vi.fn()
    const onClose = vi.fn()
    render(<ReleaseDialog task={task} onClose={onClose} onDone={onDone} />)
    await screen.findByText(/src\/login\.ts/)
    fireEvent.change(screen.getByLabelText(/输入任务名「实现登录」确认/), { target: { value: '实现登录' } })
    fireEvent.click(screen.getByRole('button', { name: '确认发布' }))
    await waitFor(() => expect(m.release).toHaveBeenCalled())
    expect(onDone).not.toHaveBeenCalled()
    expect(onClose).not.toHaveBeenCalled()
  })
})
