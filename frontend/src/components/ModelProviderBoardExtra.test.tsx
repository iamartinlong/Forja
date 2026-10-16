import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'

const m = vi.hoisted(() => ({ api: vi.fn(), navigate: vi.fn() }))

vi.mock('@/lib/api', () => ({ api: m.api }))
vi.mock('@tanstack/react-router', () => ({ useNavigate: () => m.navigate }))

import ModelProviderBoard from './ModelProviderBoard'

const provider = {
  id: 'p-1',
  provider_id: 'myprovider',
  display_name: 'My Provider',
  base_url: 'https://api.example.com/v1',
  models: [{ model_id: 'm1', enabled: true }, { model_id: 'm2', enabled: false }],
  headers: [],
  status: 'healthy',
}

function setupApi(overrides: Record<string, any> = {}) {
  m.api.mockImplementation((url: string, opts?: any) => {
    if (url.includes('/references')) return Promise.resolve(overrides.references ?? { furnaces: [] })
    if (url.includes('test-preview')) return Promise.resolve(overrides.preview ?? { status: 'healthy' })
    if (url.endsWith('/test')) return Promise.resolve(overrides.test ?? { status: 'healthy' })
    if (opts?.method === 'POST') return Promise.resolve({ id: 'new-1' })
    if (opts?.method === 'PATCH') return Promise.resolve({})
    if (opts?.method === 'DELETE') return Promise.resolve({})
    return Promise.resolve({ items: overrides.items ?? [provider] })
  })
}

async function openCreate() {
  fireEvent.click(screen.getByRole('button', { name: /新增/ }))
  return screen.findByText('新增提供商')
}

async function fillForm(fields: { provider_id?: string; display_name?: string; base_url?: string; api_key?: string }) {
  if (fields.provider_id !== undefined) fireEvent.change(screen.getByPlaceholderText('myprovider'), { target: { value: fields.provider_id } })
  if (fields.display_name !== undefined) fireEvent.change(screen.getByPlaceholderText('我的AI'), { target: { value: fields.display_name } })
  if (fields.base_url !== undefined) fireEvent.change(screen.getByPlaceholderText('https://api.myprovider.com/v1'), { target: { value: fields.base_url } })
  if (fields.api_key !== undefined) fireEvent.change(screen.getByPlaceholderText('sk-...'), { target: { value: fields.api_key } })
}

describe('ModelProviderBoard 扩展：搜索/表单/测试/删除', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    localStorage.setItem('forja_role', 'Admin')
    setupApi()
  })

  it('列表渲染与行内跳转：含停用徽标、状态徽标、provider_id 点击导航', async () => {
    render(<ModelProviderBoard />)
    expect(await screen.findByText('myprovider')).toBeInTheDocument()
    expect(screen.getByText('My Provider')).toBeInTheDocument()
    expect(screen.getByText('含停用')).toBeInTheDocument()
    expect(screen.getByText('模型提供商 (1)')).toBeInTheDocument()

    fireEvent.click(screen.getByText('myprovider'))
    expect(m.navigate).toHaveBeenCalledWith({ to: '/admin/model-providers/$id', params: { id: 'p-1' } })
  })

  it('搜索过滤（Enter 触发）：命中/筛空/清除筛选', async () => {
    setupApi({ items: [provider, { ...provider, id: 'p-2', provider_id: 'beta', display_name: 'Beta' }] })
    render(<ModelProviderBoard />)
    expect(await screen.findByText('beta')).toBeInTheDocument()

    const search = screen.getByPlaceholderText('ID或名称')
    fireEvent.change(search, { target: { value: 'myprov' } })
    fireEvent.keyDown(search, { key: 'Enter' })
    await waitFor(() => expect(screen.queryByText('beta')).not.toBeInTheDocument())
    expect(screen.getByText('myprovider')).toBeInTheDocument()

    fireEvent.change(search, { target: { value: 'zzz' } })
    fireEvent.keyDown(search, { key: 'Enter' })
    expect(await screen.findByText('未找到匹配提供商')).toBeInTheDocument()
    expect(screen.getByText(/关键词 “zzz” 无结果/)).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: '清除筛选' }))
    expect(await screen.findByText('myprovider')).toBeInTheDocument()
  })

  it('空态：暂无提供商 + 新增提供商入口', async () => {
    setupApi({ items: [] })
    render(<ModelProviderBoard />)
    expect(await screen.findByText('暂无提供商')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /新增提供商/ }))
    expect(await screen.findByPlaceholderText('myprovider')).toBeInTheDocument()
  })

  it('新增：填表提交 POST（含请求头）', async () => {
    render(<ModelProviderBoard />)
    await openCreate()
    await fillForm({ provider_id: 'cov-prov', display_name: 'Cov AI', base_url: 'https://api.cov.example.com/v1', api_key: 'sk-cov' })
    fireEvent.change(screen.getByPlaceholderText('model-id'), { target: { value: 'm-cov' } })
    fireEvent.click(screen.getByRole('button', { name: '+ 添加请求头' }))
    fireEvent.change(screen.getByPlaceholderText('Header-Name'), { target: { value: 'X-Org' } })
    fireEvent.change(screen.getByPlaceholderText('value'), { target: { value: 'cov' } })

    fireEvent.click(screen.getByRole('button', { name: '提交' }))
    await waitFor(() => expect(m.api).toHaveBeenCalledWith(
      '/api/v1/model-providers',
      expect.objectContaining({ method: 'POST' })
    ))
    const postCall = m.api.mock.calls.find(c => c[1]?.method === 'POST')!
    const body = JSON.parse(postCall[1].body)
    expect(body.provider_id).toBe('cov-prov')
    expect(body.api_key).toBe('sk-cov')
    expect(body.models[0].model_id).toBe('m-cov')
    expect(body.headers[0]).toEqual({ name: 'X-Org', value: 'cov' })
    await waitFor(() => expect(m.api.mock.calls.some(c => !c[1] || !c[1].method)).toBe(true))
  })

  it('字段数组：模型行增删、Switch 启停、请求头增删', async () => {
    render(<ModelProviderBoard />)
    await openCreate()
    fireEvent.click(screen.getByRole('button', { name: '+ 添加模型' }))
    expect(screen.getAllByPlaceholderText('model-id')).toHaveLength(2)
    // 删除最后一行模型（remove 按钮为 h-6 w-6 图标按钮）
    const rowRemovers = screen.getAllByRole('button').filter(b => (b.className || '').includes('h-6 w-6'))
    fireEvent.click(rowRemovers[rowRemovers.length - 1])
    await waitFor(() => expect(screen.getAllByPlaceholderText('model-id')).toHaveLength(1))

    // Switch 切换停用文案
    const sw = screen.getAllByRole('switch')[0]
    fireEvent.click(sw)
    expect(await screen.findByText('停用')).toBeInTheDocument()

    // 请求头增删
    fireEvent.click(screen.getByRole('button', { name: '+ 添加请求头' }))
    expect(screen.getAllByPlaceholderText('Header-Name')).toHaveLength(1)
    const headerRemovers = screen.getAllByRole('button').filter(b => (b.className || '').includes('h-6 w-6'))
    fireEvent.click(headerRemovers[headerRemovers.length - 1])
    await waitFor(() => expect(screen.queryByPlaceholderText('Header-Name')).not.toBeInTheDocument())
  })

  it('新增 Bearer 缺密钥：内联错误且不发请求', async () => {
    render(<ModelProviderBoard />)
    await openCreate()
    await fillForm({ provider_id: 'cov-prov', display_name: 'Cov AI', base_url: 'https://api.cov.example.com/v1' })
    fireEvent.click(screen.getByRole('button', { name: '提交' }))
    expect(await screen.findByText('请输入 API 密钥')).toBeInTheDocument()
    expect(m.api.mock.calls.filter(c => c[1]?.method === 'POST')).toHaveLength(0)
  })

  it('编辑回填并提交 PATCH：留空密钥不发送 api_key', async () => {
    render(<ModelProviderBoard />)
    fireEvent.click(await screen.findByTitle('编辑'))
    expect(await screen.findByText('编辑提供商')).toBeInTheDocument()
    const providerInput = screen.getByPlaceholderText('myprovider') as HTMLInputElement
    expect(providerInput.value).toBe('myprovider')

    fireEvent.change(screen.getByPlaceholderText('我的AI'), { target: { value: 'Renamed' } })
    fireEvent.click(screen.getByRole('button', { name: '提交' }))
    await waitFor(() => expect(m.api).toHaveBeenCalledWith(
      '/api/v1/model-providers/p-1',
      expect.objectContaining({ method: 'PATCH' })
    ))
    const patchCall = m.api.mock.calls.find(c => c[1]?.method === 'PATCH')!
    const body = JSON.parse(patchCall[1].body)
    expect(body.display_name).toBe('Renamed')
    expect('api_key' in body).toBe(false)
  })

  it('行内测试按钮：healthy 调 test 并回列表', async () => {
    render(<ModelProviderBoard />)
    fireEvent.click(await screen.findByTitle('测试'))
    await waitFor(() => expect(m.api).toHaveBeenCalledWith('/api/v1/model-providers/p-1/test', { method: 'POST' }))
  })

  it('弹窗测试：缺 URL / 新建 Bearer 缺密钥 / 编辑留空走存量测试', async () => {
    render(<ModelProviderBoard />)

    // 缺 URL：清空 base_url 后点测试
    await openCreate()
    fireEvent.change(screen.getByPlaceholderText('https://api.myprovider.com/v1'), { target: { value: '' } })
    fireEvent.click(screen.getByRole('button', { name: '测试' }))
    const previews0 = m.api.mock.calls.filter(c => String(c[0]).includes('test-preview'))
    expect(previews0).toHaveLength(0)

    // 新建 Bearer 缺密钥 → 不调 preview（base_url 补回）
    fireEvent.change(screen.getByPlaceholderText('https://api.myprovider.com/v1'), { target: { value: 'https://api.cov.example.com/v1' } })
    fireEvent.change(screen.getByPlaceholderText('myprovider'), { target: { value: 'cov-prov' } })
    fireEvent.change(screen.getByPlaceholderText('我的AI'), { target: { value: 'Cov' } })
    fireEvent.click(screen.getByRole('button', { name: '测试' }))
    expect(m.api.mock.calls.filter(c => String(c[0]).includes('test-preview'))).toHaveLength(0)

    // 关闭弹窗，编辑态留空密钥 → 走存量 /test
    fireEvent.click(screen.getByRole('button', { name: '取消' }))
    fireEvent.click(await screen.findByTitle('编辑'))
    fireEvent.click(await screen.findByRole('button', { name: '测试' }))
    await waitFor(() => expect(m.api).toHaveBeenCalledWith('/api/v1/model-providers/p-1/test', { method: 'POST' }))
  })

  it('删除：无引用走直删确认；有引用走强制删除（prompt 匹配后 force=true）', async () => {
    render(<ModelProviderBoard />)
    fireEvent.click(await screen.findByTitle('删除'))
    expect(await screen.findByText('删除提供商 myprovider')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '删除' }))
    await waitFor(() => expect(m.api).toHaveBeenCalledWith('/api/v1/model-providers/p-1', { method: 'DELETE' }))

    // 有引用：确认框切换为强制删除
    vi.clearAllMocks()
    setupApi({ references: { furnaces: [{ id: 'f1', name: '炉一' }] } })
    render(<ModelProviderBoard />)
    fireEvent.click((await screen.findAllByTitle('删除'))[0])
    expect(await screen.findByText('无法删除 myprovider')).toBeInTheDocument()
    expect(screen.getByText(/被 1 个炉引用/)).toBeInTheDocument()
    const promptSpy = vi.spyOn(window, 'prompt').mockReturnValue('myprovider')
    fireEvent.click(screen.getByRole('button', { name: '仍要强制删除' }))
    await waitFor(() => expect(m.api).toHaveBeenCalledWith('/api/v1/model-providers/p-1?force=true', { method: 'DELETE' }))
    promptSpy.mockRestore()
  })
})
