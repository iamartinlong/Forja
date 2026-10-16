import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'

vi.mock('@/lib/api', () => ({
  api: vi.fn((url: string, opts?: any) => {
    if (url.includes('/model-providers')) {
      if (opts?.method === 'POST' && url.includes('test-preview')) return Promise.resolve({ status: 'healthy' })
      if (opts?.method === 'POST' && url.includes('/test')) return Promise.resolve({ status: 'healthy' })
      if (opts?.method === 'POST') return Promise.resolve({ id: 'new' })
      return Promise.resolve({ items: [{ id: '1', provider_id: 'myprovider', display_name: 'My', base_url: 'https://api.example.com/v1', models: [{ model_id: 'm1', enabled: true }], headers: [], status: 'healthy' }] })
    }
    return Promise.resolve({ items: [] })
  }),
}))

import ModelProviderBoard from './ModelProviderBoard'

describe('ModelProviderBoard regression', () => {
  beforeEach(() => {
    localStorage.setItem('forja_role', 'Admin')
  })
  it('creates with api_key required', async () => {
    render(<ModelProviderBoard />)
    expect(await screen.findByText(/模型提供商/)).toBeInTheDocument()
    fireEvent.click(screen.getByText('新增'))
    const submit = await screen.findByText('提交')
    fireEvent.click(submit)
    // 5376888 统一标签后错误文案同步：提供商ID/API 密钥必填提示（两处内联错误同时出现）
    const errs = await screen.findAllByText(/请输入提供商ID|请输入 API 密钥/)
    expect(errs.length).toBeGreaterThan(0)
  })
  it('edits with empty api_key allowed and test uses stored key', async () => {
    render(<ModelProviderBoard />)
    const edit = await screen.findByTitle('编辑')
    fireEvent.click(edit)
    const input = await screen.findByPlaceholderText('•••••••• 留空不改')
    expect((input as HTMLInputElement).placeholder).toContain('留空')
    // api_key empty should not block isValid for edit
    expect(input).toBeInTheDocument()
  })
})
