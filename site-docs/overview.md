# 项目介绍

> 一页看懂 FORJA：做什么、怎么做的、用在哪里、新在哪。完整内容见 PDF 版。

📄 **下载 PDF**：[FORJA-项目介绍.pdf](/downloads/FORJA-项目介绍.pdf)（约 8 页，含界面截图）

## 视频演示

<video controls playsinline preload="metadata" style="width:100%;border-radius:8px;border:1px solid #d8dee4">
  <source src="/作品视频演示.mp4" type="video/mp4" />
  你的浏览器不支持内嵌视频，请<a href="/作品视频演示.mp4">下载观看</a>。
</video>

## 项目背景

软件团队的需求、规格、实现、测试、交付长期分散在多个工具中，结果是**知识随人员流失、
质量靠事后补救、交付靠手工拼装**。FORJA 把这条链路收敛为一个平台：以 **Spec 为唯一事实源**、
以 **Agent 为执行引擎**、以 **门禁与签核为质量闸口**，实现
「需求入堆 → 结构化规格 → AI 锻造 → 构建镜像 → 交付包 → 客户部署 → 验收回炉」的全闭环。

## 技术架构

| 层 | 组成 |
|----|------|
| 前端 | React 19 + TypeScript + Vite；TanStack Query/Router；Tailwind + shadcn/ui |
| 后端 | Go 1.24 + Fiber；sqlc/pgx；asynq + Redis 队列；api / worker / storage 三入口 |
| 数据 | PostgreSQL 16（pgvector）；Redis 7（会话/队列/缓存） |
| 流水线 | Gitea + Gitea Actions（DinD runner + socket-proxy） |
| Agent | OpenCode Server（OpenAI 兼容 Provider），按项目隔离的「炉」容器 |

核心链路：
`Inbox 原料 → Spec（四层 + refs/anchors） → Task（分支隔离） → Agent Execution →
Run/Job（门禁回流） → Artifact（镜像） → Delivery（交付包） → UAT → Reforge（幂等回炉）`

## 应用场景

- **企业内部研发提效**：脚手架、单测、门禁、打包交给平台，人力集中在判断与设计。
- **外包 / 项目制交付**：一客户一项目仓 + 一交付包；UAT 回炉闭环，成本按项目/周期分包。
- **多技术栈团队**：Go / Node / Python 统一流程与语义，模板参数化复用。
- **自举（Dogfooding）**：平台自身即用 FORJA 管理，规格/锻造/门禁/交付/审计全链路自管理。

## 创新点

1. **Spec 即契约的锻造闭环**：四层 Spec + Clarify 门禁 + 回归约束自动注入，产出可校验可追溯。
2. **失败自愈的锻造流水线**：门禁失败带上下文自动重锻（≤3 轮），升级人工保留每轮产物。
3. **模板参数化 + 多栈统一**：组织级模板仓（版本 pin）+ enum/bool 问卷渲染。
4. **交付包级运维**：五件套 + compose 镜像行服务端改写；回滚只认 registry tag。
5. **验收回炉幂等闭环**：UAT fail 一键生成修复任务，重复提交不重复建单。
6. **治理内建**：审计导出（自身留痕）、成本分包、密钥双钥轮换。

## 质量与工程实践

- 覆盖率门禁：后端 api ≥78（实测 80%+）、前端行覆盖 ≥90（实测 92%+）；
- CI 五 job（backend ∥ frontend ∥ e2e → security → build-push → deploy）；
- 迁移版本表 + 每日备份 + 异地归档；`forja_*` 指标与 trace_id 全链路可观测。
