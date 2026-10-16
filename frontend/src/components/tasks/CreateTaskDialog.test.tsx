import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { CreateTaskDialog } from './CreateTaskDialog'
import { useForjaStore } from '@/lib/store'

const m = vi.hoisted(() => ({
  create: vi.fn(() => Promise.resolve({ id: 't-1' })),
  get: vi.fn(() => Promise.resolve({ my_role: 'Developer' })),
  listSpecs: vi.fn(() =>
    Promise.resolve({
      items: [
        { id: 's1', title: 'BS 登录', status: 'VALIDATED', file_path: 'docs/bs-login.md' },
        { id: 's2', title: '草稿规格', status: 'DRAFT' },
      ],
    })
  ),
  listIters: vi.fn(() => Promise.resolve({ items: [{ id: 'i1', name: 'Sprint 1', status: 'active' }] })),
}))

vi.mock('@/lib/api', () => ({
  Tasks: { create: m.create },
  Projects: { get: m.get },
  Specs: { list: m.listSpecs },
  Iterations: { list: m.listIters },
}))

describe('CreateTaskDialog', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useForjaStore.setState({ projectId: 'p1' })
  })

  it('仅展示已验证 Spec，空表单提交时三项内联校验', async () => {
    render(<CreateTaskDialog open onClose={() => {}} onDone={() => {}} />)
    expect(await screen.findByText(/BS 登录/)).toBeInTheDocument()
    expect(screen.queryByText(/草稿规格/)).not.toBeInTheDocument()
    expect(await screen.findByText(/Sprint 1/)).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: '创建' }))
    expect(await screen.findByText('标题至少 2 个字')).toBeInTheDocument()
    expect(screen.getByText('描述至少 5 个字')).toBeInTheDocument()
    expect(screen.getByText('请选择 Spec')).toBeInTheDocument()
    expect(m.create).not.toHaveBeenCalled()
  })

  it('选择 Spec 自动回填产物路径，填写后提交成功回调', async () => {
    const onDone = vi.fn()
    const onClose = vi.fn()
    render(<CreateTaskDialog open onClose={onClose} onDone={onDone} />)
    await screen.findByText(/BS 登录/)

    fireEvent.change(screen.getByLabelText('来源 Spec（已验证）'), { target: { value: 's1' } })
    const refs = screen.getByLabelText('产物路径（逗号分隔，可留空）') as HTMLInputElement
    expect(refs.value).toBe('docs/bs-login.md')

    fireEvent.change(screen.getByLabelText('标题'), { target: { value: '实现登录接口' } })
    fireEvent.change(screen.getByLabelText('描述'), { target: { value: '覆盖登录与鉴权流程' } })
    fireEvent.change(screen.getByLabelText('迭代（可选）'), { target: { value: 'i1' } })
    fireEvent.click(screen.getByRole('button', { name: '创建' }))

    await waitFor(() => expect(m.create).toHaveBeenCalledTimes(1))
    expect(m.create).toHaveBeenCalledWith(
      expect.objectContaining({
        title: '实现登录接口',
        description: '覆盖登录与鉴权流程',
        spec_id: 's1',
        target_iteration_id: 'i1',
      })
    )
    await waitFor(() => expect(onDone).toHaveBeenCalledWith('t-1'))
    expect(onClose).toHaveBeenCalled()
  })

  it('无 task.write 权限的角色渲染为空（不出现表单）', async () => {
    m.get.mockResolvedValueOnce({ my_role: 'Viewer' })
    render(<CreateTaskDialog open onClose={() => {}} onDone={() => {}} />)
    await waitFor(() => expect(screen.queryByText('派生锻造任务')).not.toBeInTheDocument())
  })
})
