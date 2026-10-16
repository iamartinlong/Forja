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

const pythonTpl = {
  id: 'standard-python-api',
  name: 'FORJA 标准栈（Python API）',
  description: 'FastAPI 骨架',
  version: '1.0.0',
  source: 'registry',
  stack: { backend: { lang: 'python' } },
  options: [
    { key: 'python_version', label: 'Python 版本', type: 'enum', values: ['3.12', '3.11'], default: '3.12' },
    { key: 'health_path', label: '健康检查路径', type: 'enum', values: ['health', 'healthz'], default: 'health' },
    { key: 'disable_redoc', label: '关闭 ReDoc 文档', type: 'bool', default: 'true' },
  ],
}
const goTpl = { id: 'standard-go-react', name: 'FORJA 标准栈（Go + React）', description: 'Go 骨架', version: '1.0.0', source: 'builtin', stack: {}, options: [] }

const proj = { id: 'p-1', name: 'Alpha', slug: 'alpha', description: '演示', owner: 'admin', member_count: 2 }

describe('ProjectBoard 模板参数问卷（v2.2 T2）', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    localStorage.clear()
    localStorage.setItem('forja_role', 'Admin')
    m.list.mockResolvedValue({ items: [] })
    m.templates.mockResolvedValue({ items: [goTpl, pythonTpl] })
    m.create.mockResolvedValue({ id: 'p-1' })
  })

  it('选中带 options 的模板时渲染参数控件并按默认值提交', async () => {
    render(<ProjectBoard />)
    const openBtn = screen.getAllByRole('button', { name: /新建/ })[0]
    await waitFor(() => expect(openBtn).not.toBeDisabled())
    fireEvent.click(openBtn)
    await screen.findByRole('heading', { name: '新建项目' })
    await waitFor(() => expect(m.templates).toHaveBeenCalled())

    // 切换到 python 模板（模板下拉是第一个 combobox）
    const selects = document.querySelectorAll('select')
    fireEvent.change(selects[0], { target: { value: 'standard-python-api' } })

    expect(await screen.findByText('模板参数')).toBeInTheDocument()
    expect(screen.getByText('Python 版本')).toBeInTheDocument()
    expect(screen.getByText('健康检查路径')).toBeInTheDocument()
    expect(screen.getByText('关闭 ReDoc 文档')).toBeInTheDocument()

    // 修改枚举 + 切换布尔
    const paramSelects = Array.from(document.querySelectorAll('select')).slice(1)
    fireEvent.change(paramSelects[0], { target: { value: '3.11' } })
    fireEvent.change(paramSelects[1], { target: { value: 'healthz' } })
    fireEvent.click(screen.getAllByRole('switch')[0])
    await waitFor(() => expect(screen.getAllByRole('switch')[0]).toHaveAttribute('aria-checked', 'false'))

    fireEvent.change(screen.getByPlaceholderText('如 team-alpha'), { target: { value: 'cov-t2' } })
    fireEvent.click(screen.getByRole('button', { name: '创建' }))

    await waitFor(() => expect(m.create).toHaveBeenCalled())
    const payload = m.create.mock.calls[0][0]
    expect(payload.template).toBe('standard-python-api')
    expect(payload.options).toEqual({ python_version: '3.11', health_path: 'healthz', disable_redoc: 'false' })
  })

  it('无 options 的模板不渲染参数区', async () => {
    render(<ProjectBoard />)
    const openBtn = screen.getAllByRole('button', { name: /新建/ })[0]
    await waitFor(() => expect(openBtn).not.toBeDisabled())
    fireEvent.click(openBtn)
    await screen.findByRole('heading', { name: '新建项目' })
    await waitFor(() => expect(m.templates).toHaveBeenCalled())
    expect(screen.queryByText('模板参数')).not.toBeInTheDocument()
  })
})

describe('ProjectBoard 列表与管理操作', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    localStorage.clear()
    localStorage.setItem('forja_role', 'Admin')
    m.templates.mockResolvedValue({ items: [goTpl] })
    m.create.mockResolvedValue({ id: 'p-2' })
    m.update.mockResolvedValue({})
    m.archive.mockResolvedValue({})
    m.restore.mockResolvedValue({})
    m.purge.mockResolvedValue({})
  })

  it('列表渲染 + 详情/名称导航', async () => {
    m.list.mockResolvedValue({ items: [proj] })
    render(<ProjectBoard />)
    expect(await screen.findByText('Alpha')).toBeInTheDocument()
    expect(screen.getByText('alpha')).toBeInTheDocument()
    expect(screen.getByText('项目列表 (1)')).toBeInTheDocument()

    fireEvent.click(screen.getByTitle('详情'))
    expect(m.navigate).toHaveBeenCalledWith({ to: '/projects/$projectId', params: { projectId: 'p-1' } })
  })

  it('归档（confirm 通过）+ 已归档视图恢复/彻底删除（名称确认）', async () => {
    m.list.mockResolvedValue({ items: [proj] })
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true)
    render(<ProjectBoard />)
    await screen.findByText('Alpha')
    fireEvent.click(screen.getByTitle('归档（可恢复）'))
    await waitFor(() => expect(m.archive).toHaveBeenCalledWith('p-1'))
    confirmSpy.mockRestore()

    // 切换已归档视图（Radix Tabs 自动激活需 focus）
    const archiveTab = screen.getByRole('tab', { name: '已归档' })
    fireEvent.focus(archiveTab)
    fireEvent.click(archiveTab)
    await waitFor(() => expect(m.list.mock.calls.some((c) => c[3] === true)).toBe(true), { timeout: 3000 })
    fireEvent.click(await screen.findByTitle('恢复项目'))
    await waitFor(() => expect(m.restore).toHaveBeenCalledWith('p-1'))

    // 彻底删除：输入完整项目名才提交
    fireEvent.click(await screen.findByTitle('彻底删除（不可恢复）'))
    expect(await screen.findByRole('heading', { name: /彻底删除/ })).toBeInTheDocument()
    fireEvent.change(screen.getByPlaceholderText('Alpha'), { target: { value: 'Alpha' } })
    fireEvent.click(screen.getByRole('button', { name: '彻底删除' }))
    await waitFor(() => expect(m.purge).toHaveBeenCalledWith('p-1'))
  })

  it('筛空态：关键词无结果 → 清除筛选；真空态引导新建', async () => {
    m.list.mockResolvedValueOnce({ items: [] }) // 初始为空 → 真空态
    render(<ProjectBoard />)
    expect(await screen.findByText('暂无项目')).toBeInTheDocument()
    expect(screen.getByText('点击「新建项目」创建首个项目')).toBeInTheDocument()

    // 输入关键词回车 → 筛空态
    m.list.mockResolvedValue({ items: [] })
    fireEvent.change(screen.getByPlaceholderText('项目名或描述'), { target: { value: 'zzz' } })
    fireEvent.keyDown(screen.getByPlaceholderText('项目名或描述'), { key: 'Enter' })
    expect(await screen.findByText('未找到匹配项目')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '清除筛选' }))
    await waitFor(() => expect(m.list).toHaveBeenCalledTimes(3))
  })

  it('导入模式：缺仓库地址内联报错；填写后按 import 提交', async () => {
    m.list.mockResolvedValue({ items: [] })
    render(<ProjectBoard />)
    const openBtn = screen.getAllByRole('button', { name: /新建/ })[0]
    await waitFor(() => expect(openBtn).not.toBeDisabled())
    fireEvent.click(openBtn)
    await screen.findByRole('heading', { name: '新建项目' })

    fireEvent.click(screen.getByRole('button', { name: '导入仓库' }))
    fireEvent.change(screen.getByPlaceholderText('如 team-alpha'), { target: { value: 'imported' } })
    fireEvent.click(screen.getByRole('button', { name: '导入' }))
    expect(await screen.findByText('导入需填同实例仓 owner/repo，或外网镜像地址')).toBeInTheDocument()
    expect(m.create).not.toHaveBeenCalled()

    fireEvent.change(screen.getByPlaceholderText('forja-admin/my-project'), { target: { value: 'forja-admin/legacy' } })
    fireEvent.click(screen.getByRole('button', { name: '导入' }))
    await waitFor(() => expect(m.create).toHaveBeenCalled())
    const payload = m.create.mock.calls[0][0]
    expect(payload.mode).toBe('import')
    expect(payload.repo_full_name).toBe('forja-admin/legacy')
    expect(payload.options).toBeUndefined()
  })

  it('编辑保存与接口错误提示（msg 分支）', async () => {
    m.list.mockResolvedValueOnce({ items: [proj] })
    render(<ProjectBoard />)
    await screen.findByText('Alpha')
    fireEvent.click(screen.getByTitle('编辑'))
    await screen.findByRole('heading', { name: '编辑项目' })
    const editDlg = document.querySelector('[role="dialog"][data-state="open"]') as HTMLElement
    const nameInput = editDlg.querySelector('input') as HTMLInputElement
    fireEvent.change(nameInput, { target: { value: 'Alpha2' } })
    fireEvent.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(m.update).toHaveBeenCalledWith('p-1', expect.objectContaining({ name: 'Alpha2' })))

    // list 失败 → msg 分支展示（等刷新按钮脱离 min-loading 窗口）
    const refreshBtn = screen.getByTitle('刷新')
    await waitFor(() => expect(refreshBtn).not.toBeDisabled())
    const before = m.list.mock.calls.length
    m.list.mockRejectedValueOnce(new Error('boom-list'))
    fireEvent.click(refreshBtn)
    await waitFor(() => expect(m.list.mock.calls.length).toBeGreaterThan(before))
    expect(await screen.findByText('boom-list')).toBeInTheDocument()
  })
})
