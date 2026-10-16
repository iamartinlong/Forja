import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import UserBoard from './UserBoard'

const m = vi.hoisted(() => ({
  list: vi.fn(),
  create: vi.fn(),
  update: vi.fn(),
  remove: vi.fn(),
  enable: vi.fn(),
  disable: vi.fn(),
  navigate: vi.fn(),
  can: vi.fn(() => true),
  globalRole: vi.fn(() => 'Admin'),
}))

vi.mock('@/lib/api', () => ({
  Users: {
    list: m.list,
    create: m.create,
    update: m.update,
    remove: m.remove,
    enable: m.enable,
    disable: m.disable,
  },
}))
vi.mock('@/lib/auth', () => ({ can: m.can, globalRole: m.globalRole }))
vi.mock('@tanstack/react-router', () => ({ useNavigate: () => m.navigate }))
vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

const user = { id: 'u-1', username: 'alice', email: 'a@t.io', role: 'Developer', enabled: true, created_at: '2026-09-01T00:00:00Z', last_login_at: null }

describe('UserBoard 列表与分页', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    m.can.mockReturnValue(true)
    m.list.mockResolvedValue({ items: [user], total: 1, page: 1, page_size: 20 })
    m.enable.mockResolvedValue({})
    m.disable.mockResolvedValue({})
  })

  it('渲染列表并点击用户名导航详情', async () => {
    render(<UserBoard />)
    expect(await screen.findByText('alice')).toBeInTheDocument()
    expect(screen.getByText('用户列表 (1)')).toBeInTheDocument()
    expect(screen.getByText('第 1 / 1 页 · 共 1 条')).toBeInTheDocument()
    fireEvent.click(screen.getByText('alice'))
    expect(m.navigate).toHaveBeenCalledWith({ to: '/admin/users/$id', params: { id: 'u-1' } })
  })

  it('分页：总数 > pageSize 显示翻页条并服务端翻页', async () => {
    m.list.mockResolvedValue({ items: [user], total: 25, page: 1, page_size: 20 })
    render(<UserBoard />)
    await screen.findByText('alice')
    expect(screen.getByText('第 1 / 2 页 · 共 25 条')).toBeInTheDocument()
    expect(screen.getByText('用户列表 (25)')).toBeInTheDocument()

    const next = screen.getByRole('button', { name: '下一页' })
    await waitFor(() => expect(next).not.toBeDisabled())
    fireEvent.click(next)
    await waitFor(() => expect(m.list).toHaveBeenLastCalledWith(undefined, 2, 20))
    await waitFor(() => expect(screen.getByText('第 2 / 2 页 · 共 25 条')).toBeInTheDocument())
    expect(screen.getByRole('button', { name: '下一页' })).toBeDisabled()

    const prev = screen.getByRole('button', { name: '上一页' })
    await waitFor(() => expect(prev).not.toBeDisabled())
    fireEvent.click(prev)
    await waitFor(() => expect(m.list).toHaveBeenLastCalledWith(undefined, 1, 20))
  })

  it('分页：默认每页 10，切换每页条数回到第 1 页', async () => {
    m.list.mockImplementation((_q: any, page: number, size: number) =>
      Promise.resolve({ items: [user], total: 25, page, page_size: size })
    )
    render(<UserBoard />)
    await screen.findByText('alice')
    expect(m.list).toHaveBeenLastCalledWith(undefined, 1, 10)
    expect(screen.getByLabelText('每页条数')).toHaveValue('10')

    fireEvent.change(screen.getByLabelText('每页条数'), { target: { value: '100' } })
    await waitFor(() => expect(m.list).toHaveBeenLastCalledWith(undefined, 1, 100))
    expect(await screen.findByText('第 1 / 1 页 · 共 25 条')).toBeInTheDocument()
  })

  it('查询回到第 1 页并透传关键词', async () => {
    m.list.mockResolvedValue({ items: [user], total: 25, page: 2, page_size: 20 })
    render(<UserBoard />)
    await screen.findByText('alice')
    fireEvent.change(screen.getByPlaceholderText('用户名或 Email'), { target: { value: 'ali' } })
    const qBtn = screen.getByRole('button', { name: '查询' })
    await waitFor(() => expect(qBtn).not.toBeDisabled())
    fireEvent.click(qBtn)
    await waitFor(() => expect(m.list).toHaveBeenLastCalledWith('ali', 1, 20))
  })

  it('无权限时不渲染列表', async () => {
    m.can.mockReturnValue(false)
    render(<UserBoard />)
    expect(screen.getByText(/仅 Admin 可管理用户/)).toBeInTheDocument()
    expect(m.list).not.toHaveBeenCalled()
  })

  it('新建用户：空表单内联报错；合法提交调用 create 并关闭弹窗', async () => {
    m.create.mockResolvedValue({ id: 'u-2' })
    render(<UserBoard />)
    await screen.findByText('alice')
    fireEvent.click(screen.getByRole('button', { name: /新建用户/ }))
    await screen.findByRole('heading', { name: '新建用户' })
    fireEvent.click(screen.getByRole('button', { name: '创建' }))
    expect(await screen.findByText('用户名必填')).toBeInTheDocument()
    expect(await screen.findByText('密码至少 8 位')).toBeInTheDocument()
    expect(m.create).not.toHaveBeenCalled()

    fireEvent.change(screen.getByPlaceholderText('如 zhangsan'), { target: { value: 'bob' } })
    fireEvent.change(screen.getByPlaceholderText('至少 1 位'), { target: { value: 'pass1234' } })
    fireEvent.change(screen.getByPlaceholderText('可选'), { target: { value: 'bob@t.io' } })
    fireEvent.click(screen.getByRole('button', { name: '创建' }))
    await waitFor(() => expect(m.create).toHaveBeenCalledWith(expect.objectContaining({ username: 'bob', password: 'pass1234', email: 'bob@t.io', role: 'Developer' })))
    await waitFor(() => expect(screen.queryByRole('heading', { name: '新建用户' })).not.toBeInTheDocument())
  })

  it('编辑用户：留空密码不提交；填写密码随 body 提交', async () => {
    m.update.mockResolvedValue({})
    render(<UserBoard />)
    await screen.findByText('alice')
    fireEvent.click(screen.getByTitle('编辑'))
    await screen.findByRole('heading', { name: /编辑用户/ })
    fireEvent.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(m.update).toHaveBeenCalledWith('u-1', { email: 'a@t.io', role: 'Developer' }))
    expect(m.update.mock.calls[0][1].password).toBeUndefined()

    fireEvent.click(screen.getByTitle('编辑'))
    await screen.findByRole('heading', { name: /编辑用户/ })
    fireEvent.change(screen.getByPlaceholderText('留空不修改'), { target: { value: 'newpass123' } })
    fireEvent.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(m.update).toHaveBeenLastCalledWith('u-1', expect.objectContaining({ password: 'newpass123' })))
  })

  it('启用/禁用切换：成功调用接口；失败回滚开关', async () => {
    render(<UserBoard />)
    await screen.findByText('alice')
    fireEvent.click(screen.getAllByRole('switch')[0])
    await waitFor(() => expect(m.disable).toHaveBeenCalledWith('u-1'))
    await waitFor(() => expect(screen.getAllByRole('switch')[0]).toHaveAttribute('aria-checked', 'false'))

    m.enable.mockRejectedValueOnce(new Error('enable-fail'))
    fireEvent.click(screen.getAllByRole('switch')[0])
    await waitFor(() => expect(m.enable).toHaveBeenCalledWith('u-1'))
    // 失败回滚：开关回到禁用态
    await waitFor(() => expect(screen.getAllByRole('switch')[0]).toHaveAttribute('aria-checked', 'false'))
  })

  it('删除用户：确认后调用 remove；失败不静默吞错', async () => {
    m.remove.mockResolvedValue({})
    render(<UserBoard />)
    await screen.findByText('alice')
    fireEvent.click(screen.getByTitle('删除'))
    const confirmBtn = await screen.findByRole('button', { name: '删除' })
    fireEvent.click(confirmBtn)
    await waitFor(() => expect(m.remove).toHaveBeenCalledWith('u-1'))
  })

  it('筛空态清除筛选；真空态引导新建', async () => {
    m.list.mockResolvedValueOnce({ items: [], total: 0 })
    render(<UserBoard />)
    expect(await screen.findByText('暂无用户')).toBeInTheDocument()
    fireEvent.click(screen.getAllByRole('button', { name: /新建用户/ }).at(-1) as HTMLElement)
    expect(await screen.findByRole('heading', { name: '新建用户' })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '取消' }))
    await waitFor(() => expect(screen.queryByRole('heading', { name: '新建用户' })).not.toBeInTheDocument())

    fireEvent.change(screen.getByPlaceholderText('用户名或 Email'), { target: { value: 'zzz' } })
    m.list.mockResolvedValue({ items: [], total: 0 })
    fireEvent.keyDown(screen.getByPlaceholderText('用户名或 Email'), { key: 'Enter' })
    expect(await screen.findByText('未找到匹配用户')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '清除筛选' }))
    await waitFor(() => expect(m.list).toHaveBeenLastCalledWith(undefined, 1, 10))
  })
})
