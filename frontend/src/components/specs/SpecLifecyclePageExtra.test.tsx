import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { SpecLifecyclePage } from './SpecLifecyclePage'

const m = vi.hoisted(() => ({ lifecycle: vi.fn(), navigate: vi.fn() }))

vi.mock('@tanstack/react-router', () => ({
  useParams: () => ({ specId: '11111111-1111-1111-1111-111111111111' }),
  useNavigate: () => m.navigate,
}))
vi.mock('@/lib/api', () => ({ Specs: { lifecycle: m.lifecycle } }))

const baseData = {
  spec: {
    id: '11111111-1111-1111-1111-111111111111', project_id: 'p', layer: 'BS', title: '登录功能',
    status: 'VALIDATED', current_version: 'v0.1.2',
    created_at: new Date().toISOString(), updated_at: new Date().toISOString(),
  },
  requirements: [] as any[],
  versions: [] as any[],
  tasks: [] as any[],
  code_changes: [] as any[],
  ci_runs: [] as any[],
}

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<QueryClientProvider client={qc}><SpecLifecyclePage /></QueryClientProvider>)
}

describe('SpecLifecyclePage 扩展：加载/失败/空态/分享/行跳转/CI 徽章', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    Object.assign(navigator, { clipboard: { writeText: vi.fn().mockResolvedValue(undefined) } })
  })

  it('加载中展示 loading；失败展示错误与重试（refetch 触发再次请求）', async () => {
    let resolveFirst!: (v: any) => void
    m.lifecycle.mockImplementationOnce(() => new Promise((res) => { resolveFirst = res }))
    renderPage()
    expect(screen.getByText('生命周期加载中…')).toBeInTheDocument()
    resolveFirst(baseData)

    m.lifecycle.mockRejectedValueOnce(new Error('boom'))
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const { unmount } = render(<QueryClientProvider client={qc}><SpecLifecyclePage /></QueryClientProvider>)
    expect(await screen.findByText('生命周期加载失败（规格不存在或无权访问）')).toBeInTheDocument()
    m.lifecycle.mockResolvedValue(baseData)
    fireEvent.click(screen.getByRole('button', { name: '重试' }))
    expect(await screen.findByText('登录功能')).toBeInTheDocument()
    unmount()
  })

  it('空态：版本/原料/任务/代码/CI 全部提示空', async () => {
    m.lifecycle.mockResolvedValue(baseData)
    renderPage()
    await screen.findByText('登录功能')
    expect(screen.getByText('暂无版本记录')).toBeInTheDocument()
    expect(screen.getByText('无关联 Inbox 原料')).toBeInTheDocument()
    expect(screen.getByText(/尚无任务/)).toBeInTheDocument()
    expect(screen.getByText('暂无代码变更')).toBeInTheDocument()
    expect(screen.getByText(/暂无流水线记录/)).toBeInTheDocument()
  })

  it('分享深链：成功与剪贴板失败两分支', async () => {
    m.lifecycle.mockResolvedValue(baseData)
    renderPage()
    await screen.findByText('登录功能')
    fireEvent.click(screen.getByRole('button', { name: /分享深链/ }))
    await waitFor(() => expect(navigator.clipboard.writeText).toHaveBeenCalled())

    ;(navigator.clipboard.writeText as any) = vi.fn().mockRejectedValue(new Error('denied'))
    fireEvent.click(screen.getByRole('button', { name: /分享深链/ }))
    await waitFor(() => expect(navigator.clipboard.writeText).toHaveBeenCalledTimes(1))
  })

  it('任务行点击跳 /forge；CI 行点击跳 /pipelines；状态徽章覆盖失败/取消/运行中', async () => {
    m.lifecycle.mockResolvedValue({
      ...baseData,
      versions: [{ id: 'v1', version: 'v0.1.0', change_log: 'changed', created_at: new Date().toISOString() }],
      requirements: [{ id: 'r1', source_type: 'UAT', snippet: 's', status: 'NEW', created_at: new Date().toISOString() }],
      tasks: [{ id: 't1', title: '任务一', status: 'FAILED', commit_sha: '', forge_attempt: 0, created_at: new Date().toISOString() }],
      code_changes: [{ task_id: 't1', file_path: 'src/a.ts', change_log: '', created_at: new Date().toISOString() }],
      ci_runs: [
        { id: 'r1', repo: 'o/r', sha: 'abc1234def5678', event: 'push', status: 'FAILURE', duration_ms: 1000, created_at: new Date().toISOString(), updated_at: new Date().toISOString(), branch: '' },
        { id: 'r2', repo: 'o/r', sha: 'abc1234def5678', event: 'push', status: 'CANCELLED', duration_ms: 0, created_at: new Date().toISOString(), updated_at: new Date().toISOString(), branch: 'master' },
        { id: 'r3', repo: 'o/r', sha: 'abc1234def5678', event: 'push', status: 'RUNNING', duration_ms: 0, created_at: new Date().toISOString(), updated_at: new Date().toISOString(), branch: 'master' },
      ],
    })
    renderPage()
    await screen.findByText('任务一')
    expect(screen.getByText('失败')).toBeInTheDocument()
    expect(screen.getByText('已取消')).toBeInTheDocument()
    expect(screen.getByText('运行中')).toBeInTheDocument()
    expect(screen.getByText('changed')).toBeInTheDocument()

    fireEvent.click(screen.getByText('任务一'))
    expect(m.navigate).toHaveBeenCalledWith({ to: '/forge' })
    fireEvent.click(screen.getAllByText('abc1234')[0])
    expect(m.navigate).toHaveBeenCalledWith({ to: '/pipelines' })
    fireEvent.click(screen.getByRole('button', { name: '返回列表' }))
    expect(m.navigate).toHaveBeenCalledWith({ to: '/specs' })
  })
})
