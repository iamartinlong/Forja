# 功能总览

> 按"从需求到交付"的链路顺序，逐模块说明 FORJA 的能力与对应界面。

## 1. 需求与 Spec

| 能力 | 说明 |
|------|------|
| 原料入堆 | 文本粘贴 / `PDF · Word` 附件上传，卡片化看板，CONVERTED 跳链生成物 |
| 四层 Spec | `BS / IS / DS / TS` 分层（WHAT/WHY 禁 HOW），Clarify 门禁拦截含糊需求 |
| 关联与回归 | `refs` 关联既有 Spec；回归约束自动转译为"不得破坏"清单 |
| 校验 | `anchors` 文件路径校验、`checklist / analyze` 双门禁 |

![规格详情](/screenshots/spec-detail.png)

## 2. 锻造（Agent）

| 能力 | 说明 |
|------|------|
| 分支隔离 | `forge/task-<短id>-a<轮次>`，`master` 不直推 |
| 结构化产出 | diff / 文件清单 / 变更日志 / 置信度 / 人工介入标记 |
| 门禁 | 静态检查 → 构建 → 单测 → 覆盖率（lcov）→ 安全扫描 |
| 自动修复 | 失败携带上下文重锻，≤3 轮，超限升级 `ESCALATED` |

![锻造面板](/screenshots/forge.png)

## 3. 任务与编排

- 看板 4+2 列、拖拽流转、异常竖签；
- `depends_on` 依赖 DAG、批量创建（≤50）、优先级与预估工时；
- 迭代（iterations）批次管理与发布分流。

![任务看板](/screenshots/tasks.png)

## 4. 构建 / 部署 / 交付

| 能力 | 说明 |
|------|------|
| 流水线台账 | run/job 明细（结论/耗时/触发者），按结论筛选 + 分页 |
| 部署记录 | `scheduled/running/success/failed` 全量台账，线上版本高亮，支持定时部署 |
| 制品台账 | 镜像 tag / digest / 大小、tar 回传与下载 |
| 交付包 | 五件套 + 镜像 tar；compose 镜像行服务端改写为实际 tag，客户 `load + up` 即用 |
| 升级/回滚 | 只认交付包 tag；`DEPLOY_TAG` 切换即回滚 |

![构建部署](/screenshots/pipelines.png)
![部署记录](/screenshots/pipelines-deployments.png)

## 5. 验收与回炉

- Viewer 在客户环境提交 UAT（`pass / fail / partial`），附交付 tag 上下文；
- `fail` → 收件箱 → **一键生成修复任务**（幂等：重复提交返回既有任务）；
- 修复任务进入锻造流水线，形成"验收不通过 → 回炉 → 再交付"闭环。

![项目详情](/screenshots/project-detail.png)

## 6. 多栈模板与参数化

| 栈 | 模板 | 参数（enum/bool） |
|----|------|-------------------|
| Go + React | `standard-go-react` | — |
| Node/TS | `standard-node-api` | — |
| Python | `standard-python-api` | `python_version`（3.12/3.11）、`health_path`（health/healthz）、`disable_redoc` |

模板来自组织仓（`forja-admin/templates`）优先 + 内置兜底；建项 pin `template_version`，
参数选项白名单校验（≤6 项、无自由文本），非法选项直接 400。

## 7. 可观测与治理

| 能力 | 说明 |
|------|------|
| 指标 | `forja_*` Prometheus 指标、14 天成功率/时长趋势、部署横幅 |
| 审计 | 全量操作留痕（actor/action/target），按项目导出 CSV/JSON（导出自身留痕） |
| 成本 | 按项目/周期聚合执行数与 tokens，CSV 导出；`agent_executions` 台账 |
| 密钥 | 模型 key AES 加密存储；`APP_SECRET` 双钥轮换窗口；JWT/Internal Token/Webhook Secret 轮换 Runbook |
| 权限 | 平台角色（6）× 项目角色（双层），`RBAC` 细到动作 |

![指标与成本](/screenshots/metrics.png)

## 8. 平台体验细节

- 全站表格统一：检索 / 查询 / 刷新（`h-8` 工具栏）、400ms 遮罩、真空/筛空分态；
- 列表分页：服务端 `page/page_size`，每页 10/20/30/50/100，默认 10；
- 表单规范：`react-hook-form + zod` 内联校验，错误分层（字段内联 / 全局 toast）；
- 受控弹窗关闭即 reset；危险操作二次确认（输入名称匹配）。
