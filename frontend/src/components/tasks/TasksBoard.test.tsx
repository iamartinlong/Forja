import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, act, waitFor, fireEvent } from '@testing-library/react'
import { TasksBoard } from './TasksBoard'
import { useForjaStore } from '@/lib/store'

// Tasks.list/dag + router/SSE/dnd 全部 mock，只保留看板真实的 load/竞态逻辑
const m = vi.hoisted(() => ({
  list: vi.fn(),
  dag: vi.fn(),
  get: vi.fn(),
  navigate: vi.fn(),
}))

vi.mock('@/lib/api', () => ({
  Tasks: {
    list: m.list,
    dag: m.dag,
    patch: vi.fn(),
    approve: vi.fn(),
    converge: vi.fn(),
    get: m.get,
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
  DndContext: ({ children }: any) => <>{children}</>,
  closestCenter: vi.fn(),
  DragOverlay: () => null,
  useDroppable: () => ({ setNodeRef: () => {}, isOver: false }),
}))
vi.mock('@dnd-kit/sortable', () => ({
  SortableContext: ({ children }: any) => <>{children}</>,
  useSortable: () => ({ attributes: {}, listeners: {}, setNodeRef: () => {}, transform: null, transition: null }),
  verticalListSortingStrategy: {},
}))
vi.mock('@dnd-kit/utilities', () => ({
  CSS: { Transform: { toString: () => undefined } },
}))

function deferred<T>() {
  let resolve!: (v: T) => void
  let reject!: (e: any) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

const P = '00000000-0000-0000-0000-000000000001'
const A = '01a0704b-276e-79df-b5e7-7b72cdd44079'
const B = '11111111-1111-1111-1111-111111111111'

describe('TasksBoard 项目切换竞态', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    localStorage.clear()
    act(() => { useForjaStore.getState().setProjectId(P) })
  })

  it('加载中切换项目：新请求必须发出，旧响应不得覆盖（2026-09-08 实录）', async () => {
    const defP = deferred<any>()
    const defA = deferred<any>()
    m.list.mockImplementation((pid: string) => (pid === P ? defP.promise : defA.promise))
    m.dag.mockResolvedValue({})

    render(<TasksBoard />)
    // 首轮 P 请求已发出（加载中）
    await waitFor(() => expect(m.list).toHaveBeenCalledWith(P))

    // 加载中途切换到 A：旧代码被 refreshing 守卫吞掉，新请求发不出
    act(() => { useForjaStore.getState().setProjectId(A) })
    await waitFor(() => expect(m.list).toHaveBeenCalledWith(A))

    // 乱序回包：旧 P 后回、A 先回——最终必须是 A 的数据
    defA.resolve({ items: [{ id: 'ta', title: 'ATask-Unique', status: 'PENDING', priority: 1 }] })
    defP.resolve({ items: [{ id: 'tp', title: 'PTask-Unique', status: 'PENDING', priority: 1 }] })

    expect(await screen.findByText('ATask-Unique')).toBeInTheDocument()
    expect(screen.queryByText('PTask-Unique')).not.toBeInTheDocument()
  })

  it('两级竞态：dag 回包也按代际丢弃（list 过、新 dag 旧）', async () => {
    const defP = deferred<any>()
    const defA = deferred<any>()
    const defB = deferred<any>()
    const dagDefs: { resolve: (v: any) => void }[] = []
    m.list.mockImplementation((pid: string) =>
      pid === P ? defP.promise : pid === A ? defA.promise : defB.promise)
    m.dag.mockImplementation(() => { const d = deferred<any>(); dagDefs.push(d); return d.promise })

    render(<TasksBoard />)
    await waitFor(() => expect(m.list).toHaveBeenCalledWith(P))

    act(() => { useForjaStore.getState().setProjectId(A) })
    await waitFor(() => expect(m.list).toHaveBeenCalledWith(A))
    // A 的 list 回来，dag 发出后又切到 B
    defA.resolve({ items: [{ id: 'ta', title: 'ATask-Unique', status: 'PENDING', priority: 1 }] })
    await waitFor(() => expect(m.dag).toHaveBeenCalledTimes(1))
    act(() => { useForjaStore.getState().setProjectId(B) })
    await waitFor(() => expect(m.list).toHaveBeenCalledWith(B))
    defB.resolve({ items: [{ id: 'tb', title: 'BTask-Unique', status: 'PENDING', priority: 1 }] })
    await waitFor(() => expect(m.dag).toHaveBeenCalledTimes(2))
    dagDefs[1].resolve({})
    expect(await screen.findByText('BTask-Unique')).toBeInTheDocument()

    // 旧 dag（A）与旧 list（P）乱序后回——一律丢弃（含 catch 内 stale 分支）
    dagDefs[0].resolve({})
    defP.reject(new Error('stale boom'))
    await act(async () => {})
    expect(screen.queryByText('ATask-Unique')).not.toBeInTheDocument()
    expect(screen.queryByText('PTask-Unique')).not.toBeInTheDocument()
    expect(screen.getByText('BTask-Unique')).toBeInTheDocument()
  })
})

describe('TasksBoard 发布签核按钮（2026-09-09 实录：RELEASE_PENDING 点发布/打回无反应）', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    localStorage.clear()
    localStorage.setItem('forja_role', 'Admin')
    act(() => { useForjaStore.getState().setProjectId(P) })
  })

  it('点击发布打开签核弹窗', async () => {
    m.list.mockResolvedValue({ items: [
      { id: 't-rel', title: 'ReleaseMe-Unique', status: 'RELEASE_PENDING', priority: 1 },
    ] })
    m.dag.mockResolvedValue({})
    m.get.mockResolvedValue({ id: 't-rel', title: 'ReleaseMe-Unique' })

    render(<TasksBoard />)
    expect(await screen.findByText('ReleaseMe-Unique')).toBeInTheDocument()
    const btn = await screen.findByText('发布')
    // 真机 pointerdown 先行（dnd 截停逻辑覆盖；合成 click 不触发 pointerdown）
    fireEvent.pointerDown(btn)
    fireEvent.click(btn)
    expect(await screen.findByText('发布签核')).toBeInTheDocument()
  })

  it('点击打回打开打回弹窗', async () => {
    m.list.mockResolvedValue({ items: [
      { id: 't-rel', title: 'ReleaseMe-Unique', status: 'RELEASE_PENDING', priority: 1 },
    ] })
    m.dag.mockResolvedValue({})

    render(<TasksBoard />)
    expect(await screen.findByText('ReleaseMe-Unique')).toBeInTheDocument()
    const btn = await screen.findByText('打回')
    fireEvent.pointerDown(btn)
    fireEvent.click(btn)
    expect(await screen.findByText('打回任务')).toBeInTheDocument()
  })
})
