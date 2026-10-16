import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { ForgeTaskPicker, default as ForgePanel } from './ForgePanel'

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
  id: 't-1', project_id: 'p-1', title: '实现登录接口', status: 'PENDING',
  priority: 3, estimated_hours: 4, confidence: 0.5, forge_attempt: 2,
  commit_sha: 'abc1234def5678', branch: 'forge/t-1-a2',
  action_required: '',
  agent_executions: [], test_results: [], code_changes: [],
}

describe('ForgeTaskPicker 扩展：刷新与派生入口', () => {
  beforeEach(() => { vi.clearAllMocks() })

  it('刷新按钮触发重载', async () => {
    m.list.mockResolvedValue({ items: [{ id: 't-1', title: '任务A', status: 'PENDING', priority: 1, estimated_hours: 1 }] })
    render(<ForgeTaskPicker />)
    expect(await screen.findByText('任务A')).toBeInTheDocument()
    const refresh = screen.getByRole('button', { name: '刷新' })
    await waitFor(() => expect(refresh).not.toBeDisabled())
    fireEvent.click(refresh)
    await waitFor(() => expect(m.list).toHaveBeenCalledTimes(2))
  })

  it('派生锻造任务打开弹窗', async () => {
    m.list.mockResolvedValue({ items: [] })
    m.pget.mockResolvedValue({ my_role: 'Developer' })
    render(<ForgeTaskPicker />)
    await screen.findByText('还没有锻造任务')
    fireEvent.click(screen.getByText('派生锻造任务'))
    expect(await screen.findByText('从已验证的 Spec 派生任务，进入看板后拖入 RUNNING 开始锻造。')).toBeInTheDocument()
  })
})

describe('ForgePanel 扩展：失败态/重试/审批执行/Converge/角色降级', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    localStorage.setItem('forja_role', 'Developer')
  })

  it('加载失败渲染失败态，重试恢复，可返回任务列表', async () => {
    m.get.mockRejectedValueOnce(new Error('gone'))
    m.pget.mockResolvedValue({ my_role: 'Developer' })
    render(<ForgePanel />)
    expect(await screen.findByText('任务不存在或无权访问')).toBeInTheDocument()
    expect(screen.getByText(/task id：t-1/)).toBeInTheDocument()

    m.get.mockResolvedValue(TASK)
    fireEvent.click(screen.getByRole('button', { name: '重试' }))
    expect(await screen.findByText('实现登录接口')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /任务列表/ }))
    expect(m.navigate).toHaveBeenCalledWith({ to: '/forge' })
  })

  it('Approve 确认后执行审批并重载任务', async () => {
    m.get.mockResolvedValue(TASK)
    m.pget.mockResolvedValue({ my_role: 'Developer' })
    render(<ForgePanel />)
    await screen.findByText('实现登录接口')
    const btn = await screen.findByText('Approve → RUNNING')
    await waitFor(() => expect(btn.closest('button')).not.toBeDisabled())
    fireEvent.click(btn)
    fireEvent.click(await screen.findByRole('button', { name: '开始锻造' }))
    await waitFor(() => expect(m.approve).toHaveBeenCalledWith('t-1'))
    await waitFor(() => expect(m.get.mock.calls.length).toBeGreaterThanOrEqual(2))
  })

  it('Converge 执行并渲染收敛报告（收敛/漂移两态）', async () => {
    m.get.mockResolvedValue({ ...TASK, status: 'RUNNING' })
    m.pget.mockResolvedValue({ my_role: 'Developer' })
    m.converge.mockResolvedValue({ converged: true, similarity: 0.92, missing: [], drift: [] })
    render(<ForgePanel />)
    await screen.findByText('实现登录接口')
    const convBtn = await screen.findByText('Converge')
    await waitFor(() => expect(convBtn.closest('button')).not.toBeDisabled())
    fireEvent.click(convBtn)
    expect(await screen.findByText('Converge 报告')).toBeInTheDocument()
    expect(screen.getByText('收敛')).toBeInTheDocument()

    vi.clearAllMocks()
    m.get.mockResolvedValue({ ...TASK, status: 'RUNNING' })
    m.pget.mockResolvedValue({ my_role: 'Developer' })
    m.converge.mockResolvedValue({ converged: false, similarity: 0.4, missing: ['a'], drift: ['b'] })
    render(<ForgePanel />)
    await screen.findByText('实现登录接口')
    const convBtn2 = await screen.findByText('Converge')
    await waitFor(() => expect(convBtn2.closest('button')).not.toBeDisabled())
    fireEvent.click(convBtn2)
    expect(await screen.findByText('漂移')).toBeInTheDocument()
    expect(screen.getByText(/缺失: a 漂移: b/)).toBeInTheDocument()
  })

  it('角色获取失败时放行审批（后端兜底 403）', async () => {
    m.get.mockResolvedValue(TASK)
    m.pget.mockRejectedValue(new Error('role fail'))
    render(<ForgePanel />)
    await screen.findByText('实现登录接口')
    expect(await screen.findByText('Approve → RUNNING')).toBeInTheDocument()
  })

  it('无执行/产物记录时展示空提示（StatusCards 分支）', async () => {
    m.get.mockResolvedValue(TASK)
    m.pget.mockResolvedValue({ my_role: 'Developer' })
    render(<ForgePanel />)
    await screen.findByText('实现登录接口')
    expect(screen.getByText('尚无 Agent 执行记录')).toBeInTheDocument()
    expect(screen.getByText('暂无产物记录')).toBeInTheDocument()
    expect(screen.getByText(/置信度 50%/)).toBeInTheDocument()
  })
})
