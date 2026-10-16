# API 参考

> 基址 `/api/v1`，Bearer JWT 认证（`POST /auth/login` 获取）。
> 端点行为以 `backend/internal/api/*_test.go` 契约测试为准；
> 完整 OpenAPI 见仓内 `docs/openapi.yaml`。

## 认证

| 方法 | 路径 | 说明 |
| ---- | ---- | ---- |
| POST | `/auth/login` | 用户名+密码登录，返回 access/refresh token |
| POST | `/auth/refresh` | 静默续期（前端并发 401 只刷一次） |
| POST | `/auth/logout` | 登出，作废 refresh 凭证 |
| GET | `/auth/me` | 当前用户 |
| POST | `/auth/change-password` | 改密码（ account 自助） |

## 项目 / 成员

| 方法 | 路径 | 说明 |
| ---- | ---- | ---- |
| GET/POST | `/projects` | 列表（`page/q`）/ 新建 |
| GET/PATCH/DELETE | `/projects/:id` | 详情 / 改名 / 删除（软删） |
| POST | `/projects/:id/restore` | 恢复已删项目 |
| GET/POST | `/projects/:id/members` | 成员列表 / 加成员 |
| PATCH/DELETE | `/projects/:id/members/:userId` | 改角色 / 移除 |
| GET | `/projects/:id/cost` | 项目 token/费用聚合（v1.3） |
| GET | `/projects/:id/dag` | 任务依赖 DAG |
| GET | `/projects/:id/releases` | 发布记录 |

## 入堆 / 规格

| 方法 | 路径 | 说明 |
| ---- | ---- | ---- |
| GET/POST | `/inbox` | 列表 / 新建（文本或文件） |
| POST | `/inbox/:id/draft` | 起草 Spec |
| POST | `/inbox/convert` | 转为规格 |
| GET | `/specs` `/specs/search` `/specs/timeline` | 列表 / 全文搜索 / 版本时间线 |
| POST/PUT | `/specs` `/specs/:id` | 新建 / 更新（产生新版本） |
| POST | `/specs/:id/validate` `/specs/:id/pact` | 门禁校验 / 生成契约 |
| GET | `/specs/:id/lifecycle` | 生命周期状态机 |

## 任务 / 锻造

| 方法 | 路径 | 说明 |
| ---- | ---- | ---- |
| GET/POST | `/tasks` | 列表 / 新建（含批量 `/tasks/batch`） |
| POST | `/tasks/estimate` | 工时估算 |
| POST | `/tasks/:id/approve` | 批准（PIApp 级可配） |
| POST | `/tasks/:id/converge` | 触发锻造（最多 3 轮） |
| POST | `/tasks/:id/release` `/reject` `/revert` | 签核 / 驳回 / 回退 |
| GET | `/tasks/:id/stream` | SSE 实时锻造日志 |
| GET/POST | `/projects/:id/iterations…` | 迭代 CRUD + release/revert |

## 炉 / 模型 / 流水线

| 方法 | 路径 | 说明 |
| ---- | ---- | ---- |
| GET/POST | `/furnaces` | 列表（含成功率/token 聚合）/ 新建（空 base_url 自动建容器） |
| POST | `/furnaces/:id/test-chat` | 炉连通性试聊 |
| POST | `/furnaces/:id/recover` | 手动恢复：清零熔断计数，unhealthy→healthy 并拉起容器 |
| GET/POST | `/model-providers` | 供应商列表 / 新建 |
| POST | `/model-providers/:id/test` | 供应商试聊 |
| GET | `/pipelines/summary` `/pipelines/metrics` `/pipelines/deployments` | 流水线总览 / 指标 / 部署记录 |
| GET | `/audit` `/specs/:id/trace` | 审计日志 / 规格溯源 |

## 系统

| 方法 | 路径 | 说明 |
| ---- | ---- | ---- |
| GET | `/ready` `/health` `/metrics` | 就绪探活 / 存活 / Prometheus 指标 |
| GET | `/projects/:id/events` | 项目事件 SSE |

## 前端调用约定（`lib/http.ts`）

- **401 单飞续期**：业务请求 401 先静默 `refresh` 后重放一次；刷新失败才跳登录。
  主动登出有静默窗口，窗口期内 401 不弹 toast、不重复跳转。
- **`silent: true`**：不弹全局 toast，用于"表单旁内联展示 API 错误"的场景；
  其他调用一律走拦截器统一 toast，调用点不要自己再弹。
- **错误分层**：客户端校验失败 → 字段内联错误（不 toast）；
  API 业务错误 → 全局 toast；网络错误 → toast 提示检查网络。
