# 按角色使用

> 同一套系统，不同角色的关注点与常用路径。建议先读[快速开始](/guide/quickstart)。

## PM / 需求方：从原料到 Spec

- **收件箱**：粘贴文本 / 上传 `PDF · Word`；卡片带相对时间与创建人，`CONVERTED` 可跳转生成物。
- **Spec 四层**：`BS`（业务故事）/ `IS`（接口契约）/ `DS`（数据模型）/ `TS`（测试规格），
  `refs` 关联既有 Spec、`anchors` 锚定改动范围，回归约束自动注入。
- **项目资源库**：按分类管理交付物与参考资料，可被锻造提示词引用。

![收件箱](/screenshots/inbox.png)
![规格详情](/screenshots/spec-detail.png)

## 架构师：门禁 · 编排 · 签核

- **门禁策略**：静态检查 / 构建 / 单测 / 覆盖率（lcov）/ 安全扫描；覆盖率阈值可配，
  低于阈值直接判失败以倒逼补测。
- **任务编排**：从 `VALIDATED` Spec 派生任务，`priority` 与依赖（`depends_on`）驱动 DAG；
  迭代（iterations）用于批次管理。
- **发布签核**：核对 diff、测试与变更文件后输入任务名确认；合并走 `--no-ff` 并打标签。

![任务看板](/screenshots/tasks.png)

## 开发者：看板 · 锻造 · 分支

- **看板**：4+2 列（4 主状态 + 2 异常列），`FAILED/ESCALATED` 竖签标识，支持拖拽流转。
- **锻造**：分支隔离 `forge/task-<短id>-a<轮次>`；每轮产出结构化 diff 与测试；
  失败按 `failCtx` 上下文重锻（≤3 轮）。
- **交付**：构建产物回传 registry + 制品台账，组装交付包（compose/手册/Release Notes）。

![锻造面板](/screenshots/forge.png)
![构建部署](/screenshots/pipelines.png)

## QA：契约 · 回归 · 验收

- **契约**：`contracts/` 优先，`IS` 生成 OpenAPI 与 Pact 交互，`TestGeneratePact` 为必绿项。
- **回归**：Spec `refs` 自动转译为"不得破坏"清单，进入锻造提示词。
- **验收（UAT）**：在客户环境按交付包部署后提交 `pass / fail / partial`；
  `fail` 会携带交付 tag 进入收件箱，可**一键生成修复任务**（幂等，重复提交不重复建单）。

![项目详情](/screenshots/project-detail.png)

## 管理员：用户 · 炉池 · 模型 · 治理

- **用户**：新建/编辑/禁用/删除，角色（Admin/Architect/Developer/Viewer/PM/QA）与项目成员
  双层权限；列表支持检索与每页条数（10/20/30/50/100）。
- **炉池**：Furnace 是 Agent 执行单元（每项目一个容器），支持启停/排水、健康探测与熔断恢复。
- **模型提供商**：多 Provider 管理（base_url / api_key / 模型），用于锻造与聊天。
- **治理**：审计按项目导出 CSV（导出行为自身留痕）；成本分包按项目/周期聚合导出。

![用户管理](/screenshots/users.png)
![炉池](/screenshots/furnaces.png)
![指标与成本](/screenshots/metrics.png)
