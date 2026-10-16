# site-docs — FORJA 文档站（VitePress）

仅本地预览，不进 CI/CD。

```powershell
cd site-docs
npm install            # 首次；国内可用 --registry=https://registry.npmmirror.com
npm run docs:dev       # 开发预览 http://localhost:5174
npm run docs:build     # 产物构建 + 死链检查
npm run docs:preview   # 预览构建产物
```

信息架构与批次规划见 `docs/phases/phase-v1-production/v1.1-site-docs-plan.md`。
术语表（glossary.md）是产品内 tooltip 的单一事实源。
