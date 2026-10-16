import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, act, fireEvent, waitFor } from '@testing-library/react'
import { TasksBoard } from './TasksBoard'
import { useForjaStore } from '@/lib/store'

const m = vi.hoisted(() => ({
  list: vi.fn(),
  dag: vi.fn(),
  patch: vi.fn(() => Promise.resolve({})),
  approve: vi.fn(() => Promise.resolve({})),
  converge: vi.fn(() => Promise.resolve({})),
  get: vi.fn(() => Promise.resolve({})),
  release: vi.fn(() => Promise.resolve({})),
  reject: vi.fn(() => Promise.resolve({})),
  create: vi.fn(() => Promise.resolve({ id: 'new-1' })),
  navigate: vi.fn(),
}))
const dnd = vi.hoisted(() => ({ props: null as any }))

vi.mock('@/lib/api', () => ({
  Tasks: {
    list: m.list, dag: m.dag, patch: m.patch, approve: m.approve, converge: m.converge,
    get: m.get, release: m.release, reject: m.reject, create: m.create,
  },
  Projects: { get: vi.fn(() => Promise.resolve({ my_role: 'Admin' })) },
  Specs: { list: vi.fn(() => Promise.resolve({ items: [] })) },
  Iterations: { list: vi.fn(() => Promise.resolve({ items: [] })) },
}))
vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => m.navigate,
  Link: ({ children }: any) => <a>{children}</a>,
}))
vi.mock('@/lib/useEventSource', () => ({ useEventSource: vi.fn(() => ({ connected: true })) }))
vi.mock('@dnd-kit/core', () => ({
  DndContext: (p: any) => {
    dnd.props = p
    return <>{p.children}</>
  },
  closestCenter: vi.fn(),
  DragOverlay: () => null,
  useDroppable: () => ({ setNodeRef: () => {}, isOver: false }),
}))
vi.mock('@dnd-kit/sortable', () => ({
  SortableContext: ({ children }: any) => <>{children}</>,
  useSortable: () => ({ attributes: {}, listeners: {}, setNodeRef: () => {}, transform: null, transition: null }),
  verticalListSortingStrategy: {},
}))
vi.mock('@dnd-kit/utilities', () => ({ CSS: { Transform: { toString: () => undefined } } }))

const P = '00000000-0000-0000-0000-000000000001'

function setWidth(w: number) {
  Object.defineProperty(window, 'innerWidth', { value: w, writable: true, configurable: true })
}
function deferred<T>() {
  let resolve!: (v: T) => void
  const promise = new Promise<T>((res) => { resolve = res })
  return { promise, resolve }
}

describe('TasksBoard 扩展：XL 折叠、拖拽回调、移动端与行内操作', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    m.list.mockReset()
    m.navigate.mockReset()
    localStorage.clear()
    localStorage.setItem('forja_role', 'Admin')
    act(() => { useForjaStore.getState().setProjectId(P) })
    setWidth(1400)
  })

  it('XL：异常列默认折叠为竖签，点击/键盘展开，拖入自动展开', async () => {
    m.list.mockResolvedValue({ items: [
      { id: 'f1', title: 'FailedTask-Unique', status: 'FAILED', priority: 0 },
      { id: 'e1', title: 'Esc-Unique', status: 'ESCALATED', priority: 0 },
    ] })
    m.dag.mockResolvedValue({})
    render(<TasksBoard />)
    await act(async () => {})
    fireEvent(window, new Event('resize'))

    // 折叠态：竖签存在，卡片不渲染
    const failTab = await screen.findByLabelText('FAILED 1 项，点击展开')
    expect(screen.queryByText('FailedTask-Unique')).not.toBeInTheDocument()

    // 点击展开 → 列头与卡片出现
    fireEvent.click(failTab)
    expect(await screen.findByText('FAILED (1)')).toBeInTheDocument()
    expect(screen.getByText('FailedTask-Unique')).toBeInTheDocument()

    // 键盘展开 ESCALATED
    fireEvent.keyDown(screen.getByLabelText('ESCALATED 1 项，点击展开'), { key: 'Enter' })
    expect(await screen.findByText('ESCALATED (1)')).toBeInTheDocument()

    // 折叠按钮回竖签
    fireEvent.click(screen.getByLabelText('折叠 FAILED'))
    await waitFor(() => expect(screen.queryByText('FailedTask-Unique')).not.toBeInTheDocument())

    // 拖入折叠列（onDragOver）自动展开
    await act(async () => { dnd.props.onDragOver({ over: { id: 'FAILED' } }) })
    expect(await screen.findByText('FailedTask-Unique')).toBeInTheDocument()

    // onDragStart 设置 DragOverlay 数据 + over=null 直接返回
    await act(async () => { dnd.props.onDragStart({ active: { data: { current: { task: { id: 'f1', title: 'f' } } } } }) })
    await act(async () => { dnd.props.onDragEnd({ active: { id: 'f1', data: { current: { col: 'FAILED', task: { id: 'f1' } } } }, over: null }) })
  })

  it('拖拽 PENDING→RUNNING 触发 approve；PENDING 内重排改 priority；异列仅提示', async () => {
    m.list.mockResolvedValue({ items: [
      { id: 'p1', title: 'Pending-One', status: 'PENDING', priority: 2 },
      { id: 'p2', title: 'Pending-Two', status: 'PENDING', priority: 1 },
    ] })
    m.dag.mockResolvedValue({})
    render(<TasksBoard />)
    expect(await screen.findByText('Pending-One')).toBeInTheDocument()

    await act(async () => {
      dnd.props.onDragEnd({ active: { id: 'p1', data: { current: { col: 'PENDING', task: { id: 'p1' } } } }, over: { id: 'RUNNING' } })
    })
    expect(m.approve).toHaveBeenCalledWith('p1')

    await act(async () => {
      dnd.props.onDragEnd({ active: { id: 'p1', data: { current: { col: 'PENDING', task: { id: 'p1' } } } }, over: { id: 'p2' } })
    })
    expect(m.patch).toHaveBeenCalledWith('p1', { priority: 1 })

    const before = m.approve.mock.calls.length
    await act(async () => {
      dnd.props.onDragEnd({ active: { id: 'p1', data: { current: { col: 'PENDING', task: { id: 'p1' } } } }, over: { id: 'RELEASED' } })
    })
    expect(m.approve.mock.calls.length).toBe(before)
  })

  it('移动端(<1024)：顶部列选择器切换列，异常列徽章高亮', async () => {
    setWidth(600)
    m.list.mockResolvedValue({ items: [
      { id: 'e1', title: 'Esc-Unique', status: 'ESCALATED', priority: 0 },
      { id: 'p1', title: 'Pending-Unique', status: 'PENDING', priority: 1 },
    ] })
    m.dag.mockResolvedValue({})
    render(<TasksBoard />)
    expect(await screen.findByText('PENDING (1)')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /RUNNING/ }))
    expect(await screen.findByText('RUNNING (0)')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /ESCALATED/ }))
    expect(await screen.findByText('Esc-Unique')).toBeInTheDocument()
  })

  it('空态：提示 + 刷新触发重载与遮罩；派生首个任务打开弹窗', async () => {
    m.list.mockResolvedValueOnce({ items: [] })
    m.dag.mockResolvedValue({})
    render(<TasksBoard />)
    expect(await screen.findByText('看板还是空的')).toBeInTheDocument()

    // 刷新：等最小 loading 窗结束后点击，慢响应期间出现遮罩文案
    await waitFor(() => expect(screen.getByRole('button', { name: /刷新/ })).toBeEnabled())
    const def = deferred<any>()
    m.list.mockImplementationOnce(() => def.promise)
    fireEvent.click(screen.getByRole('button', { name: /刷新/ }))
    expect(await screen.findByText('正在刷新…', {}, { timeout: 3000 })).toBeInTheDocument()
    def.resolve({ items: [] })
    await waitFor(() => expect(screen.queryByText('正在刷新…')).not.toBeInTheDocument())
    expect(m.list).toHaveBeenCalledTimes(2)

    // 空态入口打开派生弹窗（Radix modal 会 aria-hidden 背景，放最后断言）
    fireEvent.click(screen.getByRole('button', { name: '派生首个锻造任务' }))
    expect(await screen.findByText('从已验证的 Spec 派生任务，进入看板后拖入 RUNNING 开始锻造。')).toBeInTheDocument()
  })

  it('行内操作：流水线跳转 / FAILED 重跑 / PASSED 归入待发布 / deps 展示', async () => {
    m.list.mockResolvedValue({ items: [
      { id: 'p1', title: 'Pending-Unique', status: 'PENDING', priority: 1, depends_on: ['abcdef123456'] },
      { id: 'f1', title: 'Failed-Unique', status: 'FAILED', priority: 0 },
      { id: 'x1', title: 'Passed-Unique', status: 'PASSED', priority: 0 },
    ] })
    m.dag.mockResolvedValue({ ready: [{ id: 'p1' }] })
    render(<TasksBoard />)
    expect(await screen.findByText('Pending-Unique')).toBeInTheDocument()
    expect(screen.getByText(/deps: abcdef/)).toBeInTheDocument()
    expect(screen.getByText('并行就绪 1 个任务')).toBeInTheDocument()

    fireEvent.click(screen.getAllByRole('button', { name: /流水线/ })[0])
    expect(m.navigate).toHaveBeenCalledWith({ to: '/forge/$taskId', params: { taskId: 'p1' } })

    // FAILED 列默认折叠：展开后才有重跑按钮
    fireEvent.click(await screen.findByLabelText('FAILED 1 项，点击展开'))
    expect(await screen.findByText('Failed-Unique')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '重跑验证' }))
    expect(m.converge).toHaveBeenCalledWith('f1')

    // PASSED 归入 RELEASE_PENDING 列（A5 前存量兼容）
    expect(screen.getByText('Passed-Unique')).toBeInTheDocument()
  })
})
