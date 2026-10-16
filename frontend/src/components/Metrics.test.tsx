import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { Metrics } from './Metrics'

const m = vi.hoisted(() => ({
  report: vi.fn(),
}))

vi.mock('@/lib/api', () => ({
  Specs: { list: () => Promise.resolve({ items: [{ layer: 'BS', title: 't', status: 'DRAFT' }] }) },
  Tasks: { list: () => Promise.resolve({ items: [{ status: 'PENDING' }, { status: 'PASSED' }] }) },
  Pipelines: {
    // P2 真聚合数据源
    metrics: () =>
      Promise.resolve({
        days: 14,
        items: [
          { day: '2026-08-24', total: 3, success: 2, failure: 1, success_rate: 66.7, avg_duration_ms: 120000 },
          { day: '2026-08-25', total: 1, success: 1, failure: 0, success_rate: 100, avg_duration_ms: 90000 },
        ],
      }),
  },
  Cost: {
    report: m.report,
    reportURL: (opts: any) => `/api/v1/cost/report?group=${opts?.group ?? 'project'}&format=csv`,
  },
}))

function renderMetrics() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<QueryClientProvider client={qc}><Metrics /></QueryClientProvider>)
}

describe('Metrics', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    m.report.mockResolvedValue({
      group: 'project',
      items: [{ key: 'p-1', label: 'Alpha', executions: 2, cost_cents: 200, tokens_in: 1600, tokens_out: 700 }],
      count: 1,
    })
  })

  it('renders charts with real pipeline metrics', async () => {
    renderMetrics()
    expect(await screen.findByText(/Spec 分层/)).toBeInTheDocument()
    expect(await screen.findByText(/Task 状态/)).toBeInTheDocument()
    expect(await screen.findByText(/流水线趋势/)).toBeInTheDocument()
  })

  it('成本分包卡片：渲染分包行 + 切换分组/周期 + CSV 直链', async () => {
    renderMetrics()
    expect(await screen.findByText('成本分包 · agent_executions 聚合（Admin 全量 / 普通用户仅成员项目）')).toBeInTheDocument()
    await waitFor(() => expect(m.report).toHaveBeenCalledWith({ group: 'project', period: undefined }))
    expect(await screen.findByText('Alpha')).toBeInTheDocument()
    expect(screen.getByText('200')).toBeInTheDocument()
    expect(screen.getByText('1600')).toBeInTheDocument()
    expect(screen.getByText('700')).toBeInTheDocument()

    const exportLink = screen.getByRole('link', { name: '导出 CSV' })
    expect(exportLink).toHaveAttribute('href', '/api/v1/cost/report?group=project&format=csv')

    fireEvent.change(screen.getByLabelText('成本分组'), { target: { value: 'period' } })
    await waitFor(() => expect(m.report).toHaveBeenCalledWith({ group: 'period', period: 'day' }))
    fireEvent.change(screen.getByLabelText('周期粒度'), { target: { value: 'month' } })
    await waitFor(() => expect(m.report).toHaveBeenCalledWith({ group: 'period', period: 'month' }))
  })

  it('成本分包卡片：无数据态', async () => {
    m.report.mockResolvedValue({ group: 'project', items: [], count: 0 })
    renderMetrics()
    expect(await screen.findByText(/暂无成本数据/)).toBeInTheDocument()
  })
})
