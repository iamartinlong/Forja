# FORJA Constitution — 锻造宪法

> Version: 1.0.0 | Ratified: 2026-08-20 | Immutable — 修改需 Amendment Process (理由+维护者评审+兼容评估) | 唯一记忆源，与 `AGENTS.md` 合并

此宪法为 FORJA 一切规格、计划、实现的上位法。任何 Spec/Plan/Task 与宪法冲突时，以宪法为准。

---

## Article I — Spec 即真理 (Spec is Source of Truth)

代码服务规格，而非规格服务代码。`specs/` 是唯一事实源，代码可再生、可丢弃，规格永存。需求变更必须先改 Spec，再生代码，禁止直接 hotfix 代码而不回写 Spec。

## Article II — 意图分离 (Intent vs How)

`specify/clarify` 阶段严禁 HOW (技术栈、框架、库)。所有 `WHAT/WHY` 与 `HOW` 分离；`plan` 阶段每项技术选型必须追溯到具体用户故事与验收场景。

## Article III — 测试先行 (Test-First Imperative)

无测试不写代码。契约→集成→组件→单元 顺序，契约为 Task 0。先写并获批且确认失败 (Red)，再实现 (Green)，后重构 (Refactor)。

## Article IV — 契约优先 (Contract First)

`specs/<feature>/contracts/` 先于实现存在；每个接口先有契约与契约测试 (Pact 预留)，违反则 `analyze` 阻断。

## Article V — 可追溯 (Traceability)

每行代码可追溯到 Spec 条目与 Agent 执行记录；每个 Plan 选型自动生成 `docs/adr/ADR-###.md` 并链回 `spec.md`。全链路 `Requirement → Spec → Task → Code → Test` 统一实体 ID 双向可溯。

## Article VI — 简洁与反过度抽象 (Simplicity & Anti-Abstraction)

单功能 ≤3 projects，框架直用优于自造抽象。复杂度必须文档化 justify，LLM 的 elaborate 倾向由 `Phase -1 Gates` 拦截。

## Article VII — 图纸显性不确定 (Explicit Uncertainty)

不猜。所有模糊点显式标记 `[NEEDS CLARIFICATION: question]`，零残留方进 `plan`。辅以 Example Mapping 四色卡 (黄Story/蓝Rule/绿Example/红Question) 工作坊。

## Article VIII — 知识即资产 (Knowledge as Asset)

`constitution/templates/research/specs` 版本化；`research.md` 自动检索沉淀；`specs/` 全量索引至 pgvector 跨项目复用；运营 `FORJA Walkthroughs` 端到端案例作 onboarding 教材。

## Article IX — 安全与可观测 (Security & Observability)

安全优先；所有 `write/edit/bash` 需权限 (`ask/allow/deny`)；密钥与 PII 自动脱敏；每次执行含 `trace_id` 结构化日志；`CLI` 可观测，`--help` 完备。

---

## Amendment Process

1. 提交 `docs/adr/ADR-###.md` 说明理由与影响
2. 维护者评审 (≥2 Approve)
3. 向后兼容评估与迁移计划
4. 版本号 `major` 递增，`git tag constitution-vX`

## References

- `.specify/templates/` — 组织模板覆写
- `docs/phases/phase-0-foundation/spec-system.md` — WHAT/WHY 约束
- `docs/architecture/sdd-review.md` — 最佳实践对照
