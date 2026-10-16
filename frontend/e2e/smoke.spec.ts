import { test, expect } from '@playwright/test'

// S2 CI 冒烟（当前 Router 版 UI）：登录 → 侧边栏导航 → 甘特面板渲染（含 KI-001 回归）
test('登录 → Inbox → 甘特首击 SPA 渲染', async ({ page }) => {
  await page.goto('/login')
  await expect(page.getByRole('heading', { name: '登录 FORJA' })).toBeVisible({ timeout: 10000 })

  await page.locator('#username').fill('admin')
  await page.locator('#password').fill('admin')
  await page.getByRole('button', { name: '登录', exact: true }).click()

  // 登录成功进入工作台：侧边栏出现原料堆入口
  // getByText 会同时命中主区 Tab 标题（strict mode violation）——锚定侧边栏按钮
  await expect(page.getByRole('button', { name: 'Inbox 原料堆' })).toBeVisible({ timeout: 10000 })

  // KI-001 回归：甘特首击应为 SPA 瞬切（无整页 reload），面板出现刷新按钮
  await page.getByRole('button', { name: '甘特', exact: true }).click()
  await expect(page.getByRole('button', { name: /刷新甘特/ })).toBeVisible({ timeout: 10000 })

  // 同类回归（2026-08-23 反馈）：Specs 首击同样必须 SPA 渲染
  await page.getByText('Spec 编辑器').click()
  await expect(page.getByPlaceholder('给这个规格起个名字')).toBeVisible({ timeout: 10000 })
})
