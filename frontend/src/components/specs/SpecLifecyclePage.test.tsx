import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { SpecLifecyclePage } from './SpecLifecyclePage'

const navigate = vi.fn()
vi.mock('@tanstack/react-router', () => ({
  useParams: () => ({ specId: '11111111-1111-1111-1111-111111111111' }),
  useNavigate: () => navigate,
}))

vi.mock('@/lib/api', () => ({
  Specs: {
    lifecycle: () =>
      Promise.resolve({
        spec: {
          id: '11111111-1111-1111-1111-111111111111',
          project_id: 'p', layer: 'BS', title: '登录功能',
          status: 'VALIDATED', current_version: 'v0.1.2',
          created_at: new Date().toISOString(), updated_at: new Date().toISOString(),
        },
        requirements: [{ id: 'r1', source_type: 'TEXT_PASTE', snippet: '支持手机号登录', status: 'CONVERTED', created_at: new Date().toISOString() }],
        versions: [
          { id: 'v3', version: 'v0.1.2', change_log: '门禁修正', created_at: new Date().toISOString() },
          { id: 'v1', version: 'v0.1.0', change_log: '', created_at: new Date().toISOString() },
        ],
        tasks: [{ id: 't1', title: '实现登录', status: 'RELEASE_PENDING', commit_sha: 'abc1234def', forge_attempt: 2, created_at: new Date().toISOString() }],
        code_changes: [{ task_id: 't1', file_path: 'src/login.ts', change_log: '', created_at: new Date().toISOString() }],
        ci_runs: [{ id: 'run1', repo: 'o/r', sha: 'abc1234def5678', event: 'push', status: 'SUCCESS', duration_ms: 62000, created_at: new Date().toISOString(), updated_at: new Date().toISOString(), branch: 'master' }],
      }),
  },
}))

describe('SpecLifecyclePage', () => {
  it('renders lifecycle stages and traceability sections', async () => {
    const qc = new QueryClient()
    render(<QueryClientProvider client={qc}><SpecLifecyclePage /></QueryClientProvider>)

    expect(await screen.findByText('登录功能')).toBeInTheDocument()
    expect(screen.getByTestId('spec-lifecycle-stepper')).toBeInTheDocument()
    // 七阶段标签（部分文案与状态徽章重复，用 getAllByText 断言存在即可）
    for (const label of ['Inbox 原料', 'DRAFT', 'VALIDATED', '任务', '代码', 'CI Run', 'RELEASED']) {
      expect(screen.getAllByText(label).length).toBeGreaterThan(0)
    }
    // 追溯区块
    expect(await screen.findByText('实现登录')).toBeInTheDocument()
    expect(screen.getByText('src/login.ts')).toBeInTheDocument()
    expect(screen.getByText(/版本演进时间线/)).toBeInTheDocument()
    expect(screen.getByText('v0.1.2')).toBeInTheDocument()
  })
})
