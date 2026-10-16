import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import PipelineBoard from './PipelineBoard'

const m = vi.hoisted(() => ({
  list: vi.fn(),
  summary: vi.fn(),
  deployments: vi.fn(),
  dispatch: vi.fn(),
  get: vi.fn(),
  canFlag: { value: false },
  toastSuccess: vi.fn(),
  toastError: vi.fn(),
}))

vi.mock('@/lib/api', () => ({
  Pipelines: {
    list: m.list,
    summary: m.summary,
    deployments: m.deployments,
    dispatch: m.dispatch,
    get: m.get,
  },
  Projects: { eventsURL: () => '/api/v1/projects/p1/events' },
}))
vi.mock('@/lib/useEventSource', () => ({ useEventSource: vi.fn() }))
vi.mock('@/lib/auth', () => ({ can: () => m.canFlag.value }))
vi.mock('@/lib/store', () => {
  const useForjaStore: any = (fn: any) => fn({ projectId: 'p1' })
  useForjaStore.getState = () => ({ projectId: 'p1' })
  return { useForjaStore }
})
vi.mock('sonner', () => ({ toast: { success: m.toastSuccess, error: m.toastError } }))

const run = {
  id: 'run-1',
  repo: 'forja-admin/forja',
  sha: 'abcdef1234567890abcdef1234567890abcdef12',
  branch: 'master',
  event: 'manual',
  status: 'SUCCESS',
  actor: 'admin',
  duration_ms: 120000,
  created_at: '2026-09-18T00:00:00Z',
  updated_at: '2026-09-18T00:02:00Z',
}

const deployment = {
  id: 'dep-1',
  tag: 'master-deadbeef',
  status: 'success',
  mode: 'manual',
  actor: 'admin',
  created_at: '2026-09-18T00:00:00Z',
  deployed_at: '2026-09-18T00:01:00Z',
}

function renderBoard() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <PipelineBoard />
    </QueryClientProvider>
  )
}

describe('PipelineBoard 分页（构建部署页）', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    m.canFlag.value = false
    m.list.mockResolvedValue({ items: [run], total: 45, page: 1, page_size: 10 })
    m.summary.mockResolvedValue({ deployed: null, deploying: 0 })
    m.deployments.mockResolvedValue({ items: [deployment], total: 25, page: 1, page_size: 10 })
  })

  it('流水线分页：默认每页 10 + 翻页 + 切换每页条数回第 1 页', async () => {
    renderBoard()
    expect(await screen.findByText('第 1 / 5 页 · 共 45 条')).toBeInTheDocument()
    await waitFor(() => expect(m.list).toHaveBeenCalledWith({ status: undefined, page: 1, pageSize: 10 }))
    expect(screen.getByLabelText('流水线每页条数')).toHaveValue('10')

    const next = screen.getByRole('button', { name: '下一页' })
    await waitFor(() => expect(next).not.toBeDisabled())
    fireEvent.click(next)
    await waitFor(() => expect(m.list).toHaveBeenCalledWith({ status: undefined, page: 2, pageSize: 10 }))
    expect(await screen.findByText('第 2 / 5 页 · 共 45 条')).toBeInTheDocument()

    fireEvent.change(screen.getByLabelText('流水线每页条数'), { target: { value: '50' } })
    await waitFor(() => expect(m.list).toHaveBeenCalledWith({ status: undefined, page: 1, pageSize: 50 }))
    expect(await screen.findByText('第 1 / 1 页 · 共 45 条')).toBeInTheDocument()
  })

  it('流水线空态：不渲染分页条', async () => {
    m.list.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 10 })
    renderBoard()
    expect(await screen.findByText(/暂无流水线记录/)).toBeInTheDocument()
    expect(screen.queryByText(/第 1 \/ 1 页/)).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '下一页' })).not.toBeInTheDocument()
  })

  it('部署记录页签：分页展示 + 切换每页条数', async () => {
    renderBoard()
    await screen.findByText('第 1 / 5 页 · 共 45 条')
    const tab = screen.getByRole('tab', { name: '部署记录' })
    fireEvent.focus(tab)
    fireEvent.click(tab)
    expect(await screen.findByText('第 1 / 3 页 · 共 25 条')).toBeInTheDocument()
    await waitFor(() => expect(m.deployments).toHaveBeenCalledWith({ page: 1, pageSize: 10 }))
    expect(screen.getByLabelText('部署记录每页条数')).toHaveValue('10')

    fireEvent.change(screen.getByLabelText('部署记录每页条数'), { target: { value: '100' } })
    await waitFor(() => expect(m.deployments).toHaveBeenCalledWith({ page: 1, pageSize: 100 }))
    expect(await screen.findByText('第 1 / 1 页 · 共 25 条')).toBeInTheDocument()
  })
})

describe('PipelineBoard 行操作与部署弹窗', () => {
  const pending = { ...run, id: 'run-p', sha: 'p'.repeat(40), status: 'PENDING' }
  const failure = { ...run, id: 'run-f', sha: 'f'.repeat(40), status: 'FAILURE' }
  const running = { ...run, id: 'run-r', sha: 'r'.repeat(40), status: 'RUNNING' }

  beforeEach(() => {
    vi.clearAllMocks()
    m.canFlag.value = true
    m.list.mockResolvedValue({ items: [pending, run, failure, running], total: 4, page: 1, page_size: 10 })
    m.summary.mockResolvedValue({ deployed: { tag: 'master-deadbeef', at: '2026-09-18T00:01:00Z', by: 'admin' }, deploying: 0 })
    m.deployments.mockResolvedValue({ items: [deployment], total: 1, page: 1, page_size: 10 })
    m.get.mockResolvedValue({
      ...run,
      jobs: [
        { name: 'backend', conclusion: 'success', duration_ms: 1000 },
        { name: 'e2e', conclusion: 'failure', duration_ms: 2000 },
        { name: 'web', conclusion: 'running', duration_ms: 3000 },
      ],
    })
    m.dispatch.mockResolvedValue({})
  })

  it('状态徽章 + 展开 job 明细（成功/失败/运行中）', async () => {
    renderBoard()
    expect(await screen.findByText('待构建')).toBeInTheDocument()
    expect(screen.getByText('失败')).toBeInTheDocument()
    expect(screen.getByText('运行中')).toBeInTheDocument()
    expect(screen.getByTestId('deployed-banner')).toBeInTheDocument()
    expect(screen.getByText('master-deadbeef')).toBeInTheDocument()

    fireEvent.click(screen.getByTestId(`pipeline-row-${run.sha.slice(0, 7)}`))
    await waitFor(() => expect(m.get).toHaveBeenCalledWith(run.id))
    expect(await screen.findByText('backend')).toBeInTheDocument()
    expect(screen.getByText('e2e')).toBeInTheDocument()
    expect(screen.getByText('web')).toBeInTheDocument()
  })

  it('待构建行：构建派发 + 状态筛选', async () => {
    renderBoard()
    await screen.findByText('待构建')
    fireEvent.click(screen.getByRole('button', { name: /构建/ }))
    await waitFor(() =>
      expect(m.dispatch).toHaveBeenCalledWith({ kind: 'build', sha: pending.sha, project_id: 'p1' })
    )

    fireEvent.change(screen.getByLabelText('按结论筛选'), { target: { value: 'FAILURE' } })
    await waitFor(() => expect(m.list).toHaveBeenCalledWith({ status: 'FAILURE', page: 1, pageSize: 10 }))
  })

  it('部署弹窗：定时部署带 delay_seconds；立即部署不带', async () => {
    renderBoard()
    await screen.findByText('第 1 / 1 页 · 共 4 条')
    const deployBtns = screen.getAllByRole('button', { name: '部署' })
    fireEvent.click(deployBtns[0])
    expect(await screen.findByRole('heading', { name: '部署确认' })).toBeInTheDocument()
    expect(screen.getAllByText('当前线上').length).toBeGreaterThan(1)

    fireEvent.click(screen.getByRole('radio', { name: /定时部署/ }))
    fireEvent.change(screen.getByPlaceholderText('分钟'), { target: { value: '60' } })
    fireEvent.click(screen.getByRole('button', { name: '排期部署' }))
    await waitFor(() =>
      expect(m.dispatch).toHaveBeenCalledWith({ kind: 'deploy', sha: run.sha, delay_seconds: 3600, project_id: 'p1' })
    )
    expect(m.toastSuccess).toHaveBeenCalled()

    fireEvent.click(screen.getAllByRole('button', { name: '部署' })[0])
    await screen.findByRole('heading', { name: '部署确认' })
    fireEvent.click(screen.getByRole('button', { name: '确认部署' }))
    await waitFor(() =>
      expect(m.dispatch).toHaveBeenLastCalledWith({ kind: 'deploy', sha: run.sha, project_id: 'p1' })
    )
  })
})
