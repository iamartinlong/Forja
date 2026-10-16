import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { ConfirmDialog } from './ConfirmDialog'

describe('ConfirmDialog', () => {
  it('state=null 时不渲染', () => {
    const { container } = render(<ConfirmDialog state={null} onClose={() => {}} />)
    expect(container).toBeEmptyDOMElement()
  })

  it('requireTextMatch：输入不一致提示，匹配后可执行 action 并关闭', async () => {
    const action = vi.fn(() => Promise.resolve())
    const onClose = vi.fn()
    render(
      <ConfirmDialog
        state={{
          title: '删除项目',
          description: '不可恢复',
          confirmText: '删除',
          danger: true,
          requireTextMatch: 'project-x',
          action,
        }}
        onClose={onClose}
      />
    )
    expect(screen.getByText('删除项目')).toBeInTheDocument()
    expect(screen.getByText('不可恢复')).toBeInTheDocument()
    const btn = screen.getByRole('button', { name: '删除' })
    expect(btn).toBeDisabled()

    fireEvent.change(screen.getByLabelText(/输入「project-x」以确认/), { target: { value: 'wrong' } })
    expect(screen.getByText('输入不一致')).toBeInTheDocument()
    expect(btn).toBeDisabled()

    fireEvent.change(screen.getByLabelText(/输入「project-x」以确认/), { target: { value: 'project-x' } })
    expect(btn).toBeEnabled()
    fireEvent.click(btn)
    await waitFor(() => expect(action).toHaveBeenCalledTimes(1))
    await waitFor(() => expect(onClose).toHaveBeenCalled())
  })

  it('无 requireTextMatch 时按钮默认可点，busy 期间显示处理中', async () => {
    let release!: () => void
    const action = vi.fn(
      () =>
        new Promise<void>((res) => {
          release = res
        })
    )
    const onClose = vi.fn()
    render(<ConfirmDialog state={{ title: '确认', confirmText: '执行', action }} onClose={onClose} />)
    const btn = screen.getByRole('button', { name: '执行' })
    fireEvent.click(btn)
    expect(await screen.findByRole('button', { name: '处理中…' })).toBeDisabled()
    release()
    await waitFor(() => expect(onClose).toHaveBeenCalled())
  })

  it('取消按钮触发 onClose', () => {
    const onClose = vi.fn()
    render(<ConfirmDialog state={{ title: 't', action: vi.fn() }} onClose={onClose} />)
    fireEvent.click(screen.getByRole('button', { name: '取消' }))
    expect(onClose).toHaveBeenCalled()
  })
})
