import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { InboxDetailDialog } from './InboxDetailDialog'

const m = vi.hoisted(() => ({ get: vi.fn(), attachments: vi.fn(), reforge: vi.fn(), navigate: vi.fn() }))

vi.mock('@/lib/api', () => ({
  Inbox: {
    get: m.get,
    attachments: m.attachments,
    reforge: m.reforge,
    attachmentURL: (id: string) => `/api/v1/attachments/${id}/download`,
  },
}))
vi.mock('@tanstack/react-router', () => ({ useNavigate: () => m.navigate }))

const base = {
  id: 'req-1',
  source_type: 'UAT',
  status: 'NEW',
  raw_content: '【UAT｜不通过｜v1.0.0-py】\n验收不通过',
  created_at: new Date().toISOString(),
  updated_at: new Date().toISOString(),
  content_hash: 'h1',
  spec_ids: [],
}

function renderDialog() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <InboxDetailDialog id="req-1" open onOpenChange={() => {}} />
    </QueryClientProvider>
  )
}

describe('InboxDetailDialog UAT 回炉入口（v2.2 T3）', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    m.attachments.mockResolvedValue({ items: [] })
  })

  it('UAT 条目显示「生成修复任务」，点击调用 reforge 并跳转锻造面板', async () => {
    m.get.mockResolvedValue(base)
    m.reforge.mockResolvedValue({ id: 'task-12345678', status: 'PENDING', dedup: false })
    renderDialog()
    const btn = await screen.findByRole('button', { name: /生成修复任务/ })
    fireEvent.click(btn)
    await waitFor(() => expect(m.reforge).toHaveBeenCalledWith('req-1'))
    await waitFor(() =>
      expect(m.navigate).toHaveBeenCalledWith({ to: '/forge/$taskId', params: { taskId: 'task-12345678' } })
    )
  })

  it('幂等返回 dedup 时提示不重复建单（仍跳转既有任务）', async () => {
    m.get.mockResolvedValue(base)
    m.reforge.mockResolvedValue({ id: 'task-87654321', status: 'PENDING', dedup: true })
    renderDialog()
    fireEvent.click(await screen.findByRole('button', { name: /生成修复任务/ }))
    await waitFor(() => expect(m.reforge).toHaveBeenCalled())
    await waitFor(() =>
      expect(m.navigate).toHaveBeenCalledWith({ to: '/forge/$taskId', params: { taskId: 'task-87654321' } })
    )
  })

  it('非 UAT 条目不显示回炉入口', async () => {
    m.get.mockResolvedValue({ ...base, source_type: 'TEXT_PASTE' })
    renderDialog()
    await screen.findByText('原始内容')
    expect(screen.queryByRole('button', { name: /生成修复任务/ })).not.toBeInTheDocument()
  })

  it('附件列表渲染（下载直链）与无附件态', async () => {
    m.get.mockResolvedValue(base)
    m.attachments.mockResolvedValue({
      items: [{ id: 'att-1', filename: 'evidence.log', size: 2048 }],
    })
    renderDialog()
    expect(await screen.findByText('evidence.log')).toBeInTheDocument()
    expect(screen.getByText('2.0KB')).toBeInTheDocument()
    const link = screen.getByRole('link', { name: '下载' })
    expect(link).toHaveAttribute('href', '/api/v1/attachments/att-1/download')
  })

  it('关联 Spec 列表点击直跳 + 关闭按钮', async () => {
    const onOpenChange = vi.fn()
    m.get.mockResolvedValue({ ...base, spec_ids: ['spec-abcdef12', 'spec-2'] })
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={qc}>
        <InboxDetailDialog id="req-1" open onOpenChange={onOpenChange} />
      </QueryClientProvider>
    )
    const specBtn = await screen.findByRole('button', { name: /spec-abcdef12/ })
    fireEvent.click(specBtn)
    await waitFor(() =>
      expect(m.navigate).toHaveBeenCalledWith({ to: '/specs/$specId', params: { specId: 'spec-abcdef12' } })
    )
    expect(onOpenChange).toHaveBeenCalledWith(false)

    fireEvent.click(screen.getByRole('button', { name: '关闭' }))
    expect(onOpenChange).toHaveBeenCalledTimes(2)
  })

  it('时间字段非法时回退原样展示（absoluteTime 容错）', async () => {
    m.get.mockResolvedValue({ ...base, created_at: 'not-a-date', updated_at: 'not-a-date' })
    renderDialog()
    expect(await screen.findByText('原始内容')).toBeInTheDocument()
    expect(screen.getAllByText(/not-a-date/).length).toBeGreaterThan(0)
  })
})
