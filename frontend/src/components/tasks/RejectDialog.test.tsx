import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { RejectDialog } from './RejectDialog'

const m = vi.hoisted(() => ({ reject: vi.fn(() => Promise.resolve({})) }))

vi.mock('@/lib/api', () => ({ Tasks: { reject: m.reject } }))

describe('RejectDialog', () => {
  beforeEach(() => vi.clearAllMocks())

  it('task=null 常驻挂载不渲染弹窗', () => {
    render(<RejectDialog task={null} onClose={() => {}} onDone={() => {}} />)
    expect(screen.queryByText('打回任务')).not.toBeInTheDocument()
  })

  it('理由少于 5 字内联报错，合法理由提交回调', async () => {
    const onDone = vi.fn()
    const onClose = vi.fn()
    render(
      <RejectDialog task={{ id: 't1', title: '实现登录' }} onClose={onClose} onDone={onDone} />
    )
    fireEvent.click(screen.getByRole('button', { name: '确认打回' }))
    expect(await screen.findByText('打回理由至少 5 个字')).toBeInTheDocument()
    expect(m.reject).not.toHaveBeenCalled()

    fireEvent.change(screen.getByLabelText('打回理由'), { target: { value: '缺少异常分支处理' } })
    fireEvent.click(screen.getByRole('button', { name: '确认打回' }))
    await waitFor(() => expect(m.reject).toHaveBeenCalledWith('t1', '缺少异常分支处理'))
    await waitFor(() => expect(onDone).toHaveBeenCalled())
    expect(onClose).toHaveBeenCalled()
  })

  it('接口失败时不关闭弹窗（toast 报错）', async () => {
    m.reject.mockRejectedValueOnce(new Error('reject failed'))
    const onDone = vi.fn()
    const onClose = vi.fn()
    render(
      <RejectDialog task={{ id: 't2', title: 'x' }} onClose={onClose} onDone={onDone} />
    )
    fireEvent.change(screen.getByLabelText('打回理由'), { target: { value: '接口失败用例' } })
    fireEvent.click(screen.getByRole('button', { name: '确认打回' }))
    await waitFor(() => expect(m.reject).toHaveBeenCalled())
    expect(onDone).not.toHaveBeenCalled()
    expect(onClose).not.toHaveBeenCalled()
  })
})
