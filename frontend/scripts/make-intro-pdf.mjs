// 由 HTML 生成「项目介绍 PDF」（Chromium 打印，A4）
// 用法：node frontend/scripts/make-intro-pdf.mjs
// 产物：site-docs/public/downloads/FORJA-项目介绍.pdf（源：docs/assets/forja-intro.html）
import { chromium } from '@playwright/test'
import { mkdirSync, copyFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..', '..')
const SRC = join(ROOT, 'docs', 'assets', 'forja-intro.html')
const SHOTS = join(ROOT, 'site-docs', 'public', 'screenshots')
const OUTDIR = join(ROOT, 'site-docs', 'public', 'downloads')
const OUT = join(OUTDIR, 'FORJA-项目介绍.pdf')

mkdirSync(OUTDIR, { recursive: true })

async function main() {
  const browser = await chromium.launch()
  const page = await browser.newPage()
  await page.goto(`file:///${SRC.replace(/\\/g, '/')}`, { waitUntil: 'load' })
  await page.waitForTimeout(1500)
  await page.pdf({
    path: OUT,
    format: 'A4',
    printBackground: true,
    margin: { top: '18mm', bottom: '16mm', left: '16mm', right: '16mm' },
  })
  await browser.close()
  console.log('pdf ->', OUT)
}

main().catch((e) => {
  console.error('pdf failed:', e)
  process.exit(1)
})
