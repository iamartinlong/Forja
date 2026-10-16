import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import ProjectBoard from './ProjectBoard'

const m = vi.hoisted(() => ({
  templates: vi.fn(),
  list: vi.fn(),
  create: vi.fn(),
  update: vi.fn(),
  archive: vi.fn(),
  restore: vi.fn(),
  purge: vi.fn(),
  navigate: vi.fn(),
  toastError: vi.fn(),
  toastSuccess: vi.fn(),
}))

vi.mock('@/lib/api', () => ({
  Projects: {
    templates: m.templates,
    list: m.list,
    create: m.create,
    update: m.update,
    archive: m.archive,
    restore: m.restore,
    purge: m.purge,
  },
}))
vi.mock('@tanstack/react-router', () => ({ useNavigate: () => m.navigate }))
vi.mock('sonner', () => ({ toast: { success: m.toastSuccess, error: m.toastError } }))

const goTpl = { id: 'standard-go-react', name: 'FORJA 标准栈（Go + React）', description: 'Go 骨架', version: '1.0.0', source: 'builtin', stack: {}, options: [] }
const proj = { id: 'p-1', name: 'Alpha', slug: 'alpha', description: '演示', owner: 'admin', member_count: 2 }

const openCreate = async () => {
  const openBtn = screen.getAllByRole('button', { name: /新建/ })[0]
  await waitFor(() => expect(openBtn).not.toBeDisabled())
  fireEvent.click(openBtn)
  await screen.findByRole('heading', { name: '新建项目' })
  await waitFor(() => expect(m.templates).toHaveBeenCalled())
}

const switchArchived = async () => {
  const tab = screen.getByRole('tab', { name: '已归档' })
  fireEvent.focus(tab)
  fireEvent.click(tab)
  await waitFor(() => expect(m.list.mock.calls.some((c) => c[3] === true)).toBe(true), { timeout: 3000 })
}

describe('ProjectBoard 补充分支（覆盖率余量）', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    localStorage.clear()
    localStorage.setItem('forja_role', 'Admin')
    m.list.mockResolvedValue({ items: [] })
    m.templates.mockResolvedValue({ items: [goTpl] })
    m.create.mockResolvedValue({ id: 'p-2' })
    m.update.mockResolvedValue({})
    m.archive.mockResolvedValue({})
    m.restore.mockResolvedValue({})
    m.purge.mockResolvedValue({})
  })

  it('空态引导新建 + 模式切换 + 取消关闭（重置表单）', async () => {
    render(<ProjectBoard />)
    expect(await screen.findByText('暂无项目')).toBeInTheDocument()

    fireEvent.click(screen.getAllByRole('button', { name: /新建项目/ }).at(-1) as HTMLElement)
    await screen.findByRole('heading', { name: '新建项目' })
    await waitFor(() => expect(m.templates).toHaveBeenCalled())

    fireEvent.click(screen.getByRole('button', { name: '导入仓库' }))
    fireEvent.click(screen.getByRole('button', { name: '新建模板' }))
    fireEvent.click(screen.getByRole('button', { name: '取消' }))
    await waitFor(() => expect(screen.queryByRole('heading', { name: '新建项目' })).not.toBeInTheDocument())

    await openCreate()
    expect(screen.getByPlaceholderText('如 team-alpha')).toHaveValue('')
    // Escape 关闭走 Radix onOpenChange（覆盖受控 Dialog 关闭重置分支）
    fireEvent.keyDown(document, { key: 'Escape', code: 'Escape' })
    await waitFor(() => expect(screen.queryByRole('heading', { name: '新建项目' })).not.toBeInTheDocument())
  })

  it('建项成功但 Gitea 集成失败 → toast.error 提示', async () => {
    m.create.mockResolvedValue({ id: 'p-3', gitea: { error: 'repo already exists' } })
    render(<ProjectBoard />)
    await openCreate()
    fireEvent.change(screen.getByPlaceholderText('如 team-alpha'), { target: { value: 'gitea-fail' } })
    fireEvent.click(screen.getByRole('button', { name: '创建' }))
    await waitFor(() => expect(m.create).toHaveBeenCalled())
    await waitFor(() => expect(m.toastError).toHaveBeenCalledWith(expect.stringContaining('repo already exists')))
  })

  it('编辑弹窗取消关闭 + Escape 关闭', async () => {
    m.list.mockResolvedValue({ items: [proj] })
    render(<ProjectBoard />)
    await screen.findByText('Alpha')
    fireEvent.click(screen.getByTitle('编辑'))
    await screen.findByRole('heading', { name: '编辑项目' })
    fireEvent.click(screen.getByRole('button', { name: '取消' }))
    await waitFor(() => expect(screen.queryByRole('heading', { name: '编辑项目' })).not.toBeInTheDocument())

    fireEvent.click(screen.getByTitle('编辑'))
    await screen.findByRole('heading', { name: '编辑项目' })
    fireEvent.keyDown(document, { key: 'Escape', code: 'Escape' })
    await waitFor(() => expect(screen.queryByRole('heading', { name: '编辑项目' })).not.toBeInTheDocument())
  })

  it('查询按钮重新加载 + 名称点击导航', async () => {
    m.list.mockResolvedValue({ items: [proj] })
    render(<ProjectBoard />)
    await screen.findByText('Alpha')
    const qBtn = screen.getByRole('button', { name: '查询' })
    await waitFor(() => expect(qBtn).not.toBeDisabled())
    const before = m.list.mock.calls.length
    fireEvent.click(qBtn)
    await waitFor(() => expect(m.list.mock.calls.length).toBeGreaterThan(before))
    fireEvent.click(screen.getByText('Alpha'))
    expect(m.navigate).toHaveBeenCalledWith({ to: '/projects/$projectId', params: { projectId: 'p-1' } })
  })

  it('分页：页码/总数展示 + 下一页/上一页服务端翻页', async () => {
    m.list.mockResolvedValue({ items: [proj], total: 45, page: 1, page_size: 10 })
    render(<ProjectBoard />)
    await screen.findByText('Alpha')
    expect(m.list).toHaveBeenLastCalledWith(undefined, 1, 10, false)
    expect(screen.getByText('第 1 / 5 页 · 共 45 条')).toBeInTheDocument()
    expect(screen.getByText('项目列表 (45)')).toBeInTheDocument()

    const next = screen.getByRole('button', { name: '下一页' })
    await waitFor(() => expect(next).not.toBeDisabled())
    fireEvent.click(next)
    await waitFor(() => expect(m.list).toHaveBeenLastCalledWith(undefined, 2, 10, false))
    await waitFor(() => expect(screen.getByText('第 2 / 5 页 · 共 45 条')).toBeInTheDocument())

    const prev = screen.getByRole('button', { name: '上一页' })
    await waitFor(() => expect(prev).not.toBeDisabled())
    fireEvent.click(prev)
    await waitFor(() => expect(m.list).toHaveBeenLastCalledWith(undefined, 1, 10, false))
    await waitFor(() => expect(screen.getByText('第 1 / 5 页 · 共 45 条')).toBeInTheDocument())
  })

  it('分页：切换每页条数（默认 10）回到第 1 页并按新 size 请求', async () => {
    m.list.mockImplementation((_q: any, page: number, size: number) =>
      Promise.resolve({ items: [proj], total: 45, page, page_size: size })
    )
    render(<ProjectBoard />)
    await screen.findByText('Alpha')
    expect(m.list).toHaveBeenLastCalledWith(undefined, 1, 10, false)

    fireEvent.change(screen.getByLabelText('每页条数'), { target: { value: '50' } })
    await waitFor(() => expect(m.list).toHaveBeenLastCalledWith(undefined, 1, 50, false))
    expect(await screen.findByText('第 1 / 1 页 · 共 45 条')).toBeInTheDocument()

    fireEvent.change(screen.getByLabelText('每页条数'), { target: { value: '100' } })
    await waitFor(() => expect(m.list).toHaveBeenLastCalledWith(undefined, 1, 100, false))
  })

  it('分页：单页时仍显示页码总数（翻页按钮禁用）', async () => {
    m.list.mockResolvedValue({ items: [proj], total: 3, page: 1, page_size: 20 })
    render(<ProjectBoard />)
    await screen.findByText('Alpha')
    expect(screen.getByText('第 1 / 1 页 · 共 3 条')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '上一页' })).toBeDisabled()
    expect(screen.getByRole('button', { name: '下一页' })).toBeDisabled()
  })

  it('分页：无数据时不渲染翻页条', async () => {
    m.list.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20 })
    render(<ProjectBoard />)
    expect(await screen.findByText('暂无项目')).toBeInTheDocument()
    expect(screen.queryByText(/第 1 \/ 1 页/)).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '下一页' })).not.toBeInTheDocument()
  })

  it('归档 confirm 拒绝 + 归档/恢复接口失败 msg 分支', async () => {
    m.list.mockResolvedValue({ items: [proj] })
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(false)
    render(<ProjectBoard />)
    await screen.findByText('Alpha')
    fireEvent.click(screen.getByTitle('归档（可恢复）'))
    expect(m.archive).not.toHaveBeenCalled()

    confirmSpy.mockReturnValue(true)
    m.archive.mockRejectedValueOnce(new Error('archive-fail'))
    fireEvent.click(screen.getByTitle('归档（可恢复）'))
    await waitFor(() => expect(m.archive).toHaveBeenCalledWith('p-1'))
    expect(await screen.findByText('archive-fail')).toBeInTheDocument()
    confirmSpy.mockRestore()

    await switchArchived()
    m.restore.mockRejectedValueOnce(new Error('restore-fail'))
    fireEvent.click(await screen.findByTitle('恢复项目'))
    await waitFor(() => expect(m.restore).toHaveBeenCalledWith('p-1'))
    expect(await screen.findByText('restore-fail')).toBeInTheDocument()
  })

  it('彻底删除：取消关闭 + 名称不匹配提交被拦截 + 接口失败内联报错', async () => {
    m.list.mockResolvedValue({ items: [proj] })
    render(<ProjectBoard />)
    await screen.findByText('Alpha')
    await switchArchived()

    fireEvent.click(await screen.findByTitle('彻底删除（不可恢复）'))
    await screen.findByRole('heading', { name: /彻底删除/ })
    fireEvent.click(screen.getByRole('button', { name: '取消' }))
    await waitFor(() => expect(screen.queryByRole('heading', { name: /彻底删除/ })).not.toBeInTheDocument())

    fireEvent.click(await screen.findByTitle('彻底删除（不可恢复）'))
    await screen.findByRole('heading', { name: /彻底删除/ })
    // 非空但错误的名字：绕过 zod，直接提交命中「名称不匹配即返回」分支
    fireEvent.change(screen.getByPlaceholderText('Alpha'), { target: { value: 'Beta' } })
    const purgeFormEl = document.querySelector('[role="dialog"][data-state="open"] form') as HTMLFormElement
    fireEvent.submit(purgeFormEl)
    expect(m.purge).not.toHaveBeenCalled()

    m.purge.mockRejectedValueOnce(new Error('purge-fail'))
    fireEvent.change(screen.getByPlaceholderText('Alpha'), { target: { value: 'Alpha' } })
    const purgeBtn = screen.getByRole('button', { name: '彻底删除' })
    await waitFor(() => expect(purgeBtn).not.toBeDisabled())
    fireEvent.click(purgeBtn)
    await waitFor(() => expect(m.purge).toHaveBeenCalledWith('p-1'))
    expect((await screen.findAllByText('purge-fail')).length).toBeGreaterThan(0)
  })
})
