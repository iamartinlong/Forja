import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { ForgeTaskPicker } from './ForgePanel'
import ForgePanel from './ForgePanel'

// 副作用全部 mock：api / router / SSE（覆盖率统计图只进被测组件）
const m = vi.hoisted(() => ({
  list: vi.fn(),
  get: vi.fn(),
  pget: vi.fn(),
  approve: vi.fn(),
  converge: vi.fn(),
  navigate: vi.fn(),
}))

vi.mock('@/lib/api', () => ({
  Tasks: {
    list: m.list,
    get: m.get,
    approve: m.approve,
    converge: m.converge,
    streamURL: vi.fn(() => 'http://test/stream'),
  },
  Projects: { get: m.pget },
  Specs: { list: vi.fn(() => Promise.resolve({ items: [] })) },
  Iterations: { list: vi.fn(() => Promise.resolve({ items: [] })) },
}))
vi.mock('@tanstack/react-router', () => ({
  useParams: vi.fn(() => ({ taskId: 't-1' })),
  useNavigate: () => m.navigate,
  Link: ({ children }: any) => <a>{children}</a>,
}))
vi.mock('@/lib/useEventSource', () => ({ useEventSource: vi.fn(() => ({ connected: true })) }))

const TASK = {
  id: 't-1', project_id: 'p-1', title: '实现登录接口', status: 'RELEASE_PENDING',
  priority: 3, estimated_hours: 4, confidence: 0.5, forge_attempt: 2,
  commit_sha: 'abc1234def5678', branch: 'forge/t-1-a2',
  action_required: '["opencode not reachable", "manual review"]',
  agent_executions: [{ agent_type: 'opencode', attempt: 1, status: 'SUCCESS' }],
  test_results: [{ status: 'passed' }, { status: 'failed' }],
  code_changes: [{ file_path: 'src/login.ts' }],
}

describe('ForgeTaskPicker（/forge 任务发现）', () => {
  beforeEach(() => { vi.clearAllMocks() })

  it('渲染任务列表并按状态排序（进行中优先）', async () => {
    m.list.mockResolvedValue({ items: [
      { id: 't-p', title: '待办任务', status: 'PENDING', priority: 5, estimated_hours: 4 },
      { id: 't-r', title: '进行中任务', status: 'RUNNING', priority: 5, estimated_hours: 4 },
    ] })
    render(<ForgeTaskPicker />)
    expect(await screen.findByText('锻造流水线')).toBeInTheDocument()
    const names = screen.getAllByRole('button').map((b) => b.textContent || '')
    const running = names.findIndex((n) => n.includes('进行中任务'))
    const pending = names.findIndex((n) => n.includes('待办任务'))
    expect(running).toBeGreaterThanOrEqual(0)
    expect(running).toBeLessThan(pending)
  })

  it('点击任务行导航到 /forge/$taskId', async () => {
    m.list.mockResolvedValue({ items: [{ id: 't-9', title: '某任务', status: 'PENDING', priority: 1, estimated_hours: null }] })
    render(<ForgeTaskPicker />)
    fireEvent.click(await screen.findByText('某任务'))
    expect(m.navigate).toHaveBeenCalledWith({ to: '/forge/$taskId', params: { taskId: 't-9' } })
  })

  it('空列表渲染引导空态（ui/empty）', async () => {
    m.list.mockResolvedValue({ items: [] })
    render(<ForgeTaskPicker />)
    expect(await screen.findByText('还没有锻造任务')).toBeInTheDocument()
    expect(screen.getByText('派生首个锻造任务')).toBeInTheDocument()
  })
})

describe('ForgePanel（/forge/$taskId 单任务观测）', () => {
  beforeEach(() => { vi.clearAllMocks(); localStorage.setItem('forja_role', 'Developer') })

  it('渲染标题、回放事实行与结构化状态卡（回归：原始 JSON dump 与"等待事件…"空白）', async () => {
    m.get.mockResolvedValue(TASK)
    m.pget.mockResolvedValue({ my_role: 'Developer' })
    render(<ForgePanel />)
    expect(await screen.findByText('实现登录接口')).toBeInTheDocument()
    expect(screen.getByText(/任务当前状态 RELEASE_PENDING/)).toBeInTheDocument()
    expect(screen.getByText('锻造轮次（1）')).toBeInTheDocument()
    expect(screen.getByText(/变更文件 1 个/)).toBeInTheDocument()
    // 文案跨文本节点与 span，分段断言；事实行与状态卡各渲染一次
    expect(screen.getAllByText(/验证记录 2 条/).length).toBeGreaterThan(0)
    expect(screen.getAllByText(/1 条未通过/).length).toBeGreaterThan(0)
    // action_required JSON 数组串转人类可读
    expect(screen.getByText(/需人工介入：opencode not reachable；manual review/)).toBeInTheDocument()
    // 置信度 0.5 → 50%
    expect(screen.getByText(/置信度 50%/)).toBeInTheDocument()
  })

  it('RELEASE_PENDING 不显示 Approve（仅 PENDING/FAILED/ESCALATED），显示 Converge', async () => {
    m.get.mockResolvedValue(TASK)
    m.pget.mockResolvedValue({ my_role: 'Developer' })
    render(<ForgePanel />)
    await screen.findByText('实现登录接口')
    // CI 慢机存在 task/role 响应竞态：先等 my_role 解析后按钮渲染，再断言
    await screen.findByText('Converge')
    expect(screen.queryByText('Approve → RUNNING')).not.toBeInTheDocument()
  })

  it('QA 角色：PENDING 任务可见 Converge（task.run）但无 Approve（task.approve）', async () => {
    m.get.mockResolvedValue({ ...TASK, status: 'PENDING' })
    m.pget.mockResolvedValue({ my_role: 'QA' })
    render(<ForgePanel />)
    await screen.findByText('实现登录接口')
    await screen.findByText('Converge')
    expect(screen.queryByText('Approve → RUNNING')).not.toBeInTheDocument()
  })

  it('Developer 点击 PENDING 任务的 Approve 弹出确认框（不直接执行）', async () => {
    m.get.mockResolvedValue({ ...TASK, status: 'PENDING' })
    m.pget.mockResolvedValue({ my_role: 'Developer' })
    render(<ForgePanel />)
    // CI 慢机 task/role 响应竞态：先等任务与按钮区就绪（同文件上方面例一致模式）
    await screen.findByText('实现登录接口')
    await screen.findByText('Converge')
    const btn = await screen.findByText('Approve → RUNNING')
    // 等待 useMinLoading(400ms) 窗口结束后按钮恢复可用
    await waitFor(() => expect(btn.closest('button')).not.toBeDisabled())
    fireEvent.click(btn)
    expect(await screen.findByText('开始锻造？')).toBeInTheDocument()
    expect(m.approve).not.toHaveBeenCalled()
  })
})
