// 系统截图采集（运营/文档用）：登录 → 逐页截图
// 用法：SHOT_BASE=http://localhost:8080 node frontend/scripts/capture-screenshots.mjs
// 产物：site-docs/public/screenshots/*.png（1440x900 @2x）
import { chromium } from '@playwright/test'
import { mkdirSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..', '..')
const OUT = join(ROOT, 'site-docs', 'public', 'screenshots')
const BASE = process.env.SHOT_BASE || 'http://localhost:8080'

const PROJECT_ID = '01a0a9ae-82dc-7044-8627-eae034a3da30'
const SPEC_ID = '01a09f14-f424-75f0-b6fd-e139b393e949'
const TASK_ID = '01a0a9b1-0bed-7f16-9be2-28ae4726be8f'

const shots = [
  ['workbench', '/', null],
  ['projects', '/projects', null],
  ['project-detail', `/projects/${PROJECT_ID}`, null],
  ['inbox', '/inbox', null],
  ['specs', '/specs', null],
  ['spec-detail', `/specs/${SPEC_ID}`, null],
  ['tasks', '/tasks', null],
  ['forge', `/forge/${TASK_ID}`, null],
  ['pipelines', '/pipelines', null],
  ['pipelines-deployments', '/pipelines', '部署记录'],
  ['metrics', '/metrics', null],
  ['users', '/admin/users', null],
  ['furnaces', '/admin/furnaces', null],
]

const wait = (ms) => new Promise((r) => setTimeout(r, ms))

async function main() {
  mkdirSync(OUT, { recursive: true })
  const browser = await chromium.launch()
  const page = await browser.newPage({ viewport: { width: 1440, height: 900 }, deviceScaleFactor: 2 })

  await page.goto(`${BASE}/login`, { waitUntil: 'domcontentloaded' })
  await page.locator('#username').fill('admin')
  await page.locator('#password').fill('admin')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await page.waitForSelector('text=工作台', { timeout: 20000 })
  await wait(2500)

  for (const [name, path, tabText] of shots) {
    await page.goto(`${BASE}${path}`, { waitUntil: 'domcontentloaded' })
    if (tabText) {
      const tab = page.getByRole('tab', { name: tabText })
      if (await tab.count()) {
        await tab.first().click()
        await wait(1200)
      }
    }
    await wait(2600)
    await page.screenshot({ path: join(OUT, `${name}.png`) })
    console.log('captured', name)
  }
  await browser.close()
  console.log('done ->', OUT)
}

main().catch((e) => {
  console.error('screenshot failed:', e)
  process.exit(1)
})
