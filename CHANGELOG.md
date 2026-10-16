# Changelog

## 2026-09-19 — R7 治理增强 + R8 前端打磨（P2 提前收口）

### R7 治理
- **审计导出**：`GET /audit/export`（CSV 默认 UTF-8 BOM 兼容 Excel、`format=json` 兜底，
  `target_type/action/from/to` 过滤，5 万行上限；导出行为自身写 `audit.export` 审计）；
  ProjectDetail「最近动态」卡新增下载按钮（access_token 直链）。
- **成本分包**：`GET /cost/report?group=project|period&period=day|week|month`（Admin 全量、
  普通用户仅成员项目；`from/to` 过滤；`format=csv` 导出）；Metrics 页新增「成本分包」卡片
  （分组/周期切换 + CSV 直链 + 表格）。
- **密钥轮换**：`crypto` 双钥兼容（主钥加密 + `*_PREVIOUS` 历史钥解密，支持多把逗号分隔，
  `TestDecrypt_KeyRotationPreviousKey` 覆盖）；运维 `OPS-key-rotation.md`
  （APP_SECRET/JWT/INTERNAL_TOKEN/WEBHOOK 四类轮换步骤 + 季度演练清单）。

### R8 前端打磨
- 抽取共享 `ui/pagination` 组件（每页 10/20/30/50/100 + 页码/总数 + 翻页），
  ProjectBoard/UserBoard/PipelineBoard 三处统一（消除重复 ~90 行）。
- Users/Pipelines/Deployments 时间列补 `title` 绝对时间悬浮。

### 测试与验收
- 后端新增 `coverage_v22c_test.go`（审计导出 CSV/JSON/自留痕/参数校验、成本分包
  project/period/CSV/成员范围）；`crypto` 轮换用例。
- 前端 `Metrics.test.tsx` 增成本卡 3 例；分页组件 100% 覆盖；**lines 92.7%（132 用例，gate 90）**。
- CI **run385 五 job 全绿**；deploy **run386 成功** → 生产 `master-307f5199` healthy（孤儿 = 0）。
- 生产实测（admin）：审计导出 CSV（BOM + 中文表头 + JSON 详情引号转义）/ JSON（21 行）/
  自留痕 `audit.export` 2 条；成本分包 `group=project`（4 行）/`group=period`（8 行）/CSV 均正常。

## 2026-09-19 — 技术债收口（furnace 孤儿容器 + 后端覆盖率 80.3%）

### 修复
- **furnace 孤儿容器泄漏（根因定位）**：`internal/api` 测试调用 `createFurnace/patch/delete`
  会异步触发 `furnace.Ensure/Stop/Remove`，CI backend job 与本地 `go test` 均注入 `DOCKER_HOST`
  → 真在宿主 Docker 起容器；测试清理只删 DB 行 → 每次 CI 派发残留 1 个 `forja-furnace-*`
  （09-18 四个创建时间与 run374/376/378/380 一一对应）。修复：`TestMain` 注入
  `FORJA_FURNACE_DISABLE_DOCKER=1` + `furnaceDockerEnabled()` 短路全部副作用（env 构建路径保留覆盖）；
  残留已清理，AGENTS 本地开发 #2 记录巡检口径。
- **覆盖率余量**：api 本地 **79.8% → 80.3%**（furnace 容器 env 构建双路径、`splitRepo`/
  `forgeContext` 空仓/worker-ready 冒烟，新增 `coverage_v22b_test.go`）。

### 验收
- CI **run383 五 job 全绿**（backend 实测 **api 80.0%**，target 80 达成；**CI 期间宿主 forja-furnace 孤儿 = 0**，
  修复实证）；deploy **run384 成功** → 生产 `master-b2280398` healthy。

## 2026-09-18 — 列表分页（项目 / 用户 / 构建部署）

### 优化
- **`/projects` 项目列表**：服务端分页接线（`page`/`page_size`），新增「每页 10/20/30/50/100 条（默认 **10**）」
  选择器 + 「第 x / y 页 · 共 n 条」+ 上一页/下一页（有数据即显示，单页时按钮禁用）；
  查询/回车/切换活跃-归档页签/切换每页条数回到第 1 页，刷新保持当前页；标题计数改用 `total`。
- **`/admin/users` 用户列表**：同款分页与每页条数选择器；无 `user.list` 权限时不发列表请求（消除 403 噪音）。
- **`/pipelines` 构建部署页**：流水线 / 部署记录双页签同款（每页条数选择器、有数据即显示、请求中禁用按钮，
  切换条数回第 1 页）。
- 后端无需改动（`listProjects`/`listUsers`/`listPipelines` 已支持 `page/page_size` 并返回 `PageMeta.total`）。

### 测试
- 新增 `UserBoard.test.tsx`（9 例）、`PipelineBoard.test.tsx`（6 例：分页/空态/双页签条数切换/
  状态徽章与明细展开/构建派发与筛选/部署弹窗定时与立即）、`ProjectBoardExtra` 增分页 3 例。
- **稳定性**：`ReleaseDialogSubmit` 改以查询结果元素为就绪信号（CI run380 慢机抢跑实录）。
- 覆盖率 lines **92.43%**（130 用例，gate 90）。

### 验收
- CI **run374/378/381 五 job 全绿**；deploy **run375/377/379/382 成功**
  → 生产最终 `master-e684b173` healthy（含 `/pipelines` 分页）。

## v2.2.0 — 2026-09-17（通用栈 · 模板参数化 · UAT 回炉闭环）

> Release：`docs/phases/phase-v1-production/RELEASE-v2.2.0.md`（v2.1 未单独发版，并入本版说明）；
> 生产镜像 `master-3a8d85af`（run370）→ 技术债收口后 `master-58af54f2`（run373）。

### 交付
- **T1 Python 第三栈**：`stackkind` python（pytest / lcov / pip 依赖）+ `standard-python-api` 模板
  （内置+组织仓双源）；worker/api 烘焙 python3/py3-pip + apk 阿里云镜像 + 只读 rootfs 下 pip 落盘通道；
  全链路实证 `py-34a3da30`（锻造→门禁 PASSED→构建 51MB→交付包 `v1.0.0-py`→客户部署 `/health 200`）。
- **T2 模板参数化**：`templates.Option`（enum/bool ≤6 白名单）+ `{{opt.x}}` / `{{#opt.x}}…{{/opt.x}}`
  渲染 + registry/API/前端建项问卷；生产验收 `py-opt-e2e-222cae06`（3.11 + `/healthz`）六处联动、非法值 400。
- **T3 UAT 一键回炉**：`POST /inbox/:id/reforge`（幂等 `[UAT-REF]` 标记、注入交付上下文、Spec 关联、审计）；
  前端 Inbox 详情入口；实测 UAT fail → PENDING 修复任务、重复调用 dedup。
- **T4 验收发版**：Go/Node 模板零回归（建项渲染零残留后 purge）；参数化模板全链路彩排
  （模拟客户环境 Healthy + `/version` python 3.11.16）；Tag `v2.2.0`。

### 修复与加固（2026-09-16/18）
- PEP668 / worker 只读 rootfs pip / apk 境外源 / LLM diff `--recount` / furnace fallback 顺序 /
  opencode provider key / CI×opencode 内存红线 / 建项模板列表加载 / RHF 动态路径 / 门禁用例确定性化。
- **workflow_job 回流错位**（`58af54f`）：tag push 不建 pipeline 行 + `locateRunID` 调度行优先，
  修复 manual 行滞留 RUNNING（含竞态回归用例）。
- `isolateGitProxy` 隔离宿主代理，消除不可达地址用例偶发 flake。
- 覆盖率：api **79.7%**（gate 78）/ 前端 **93.54%**（gate 90，110 用例），CI run371 五 job 全绿。

### 部署
- `DEPLOY_TAG` **8 位短 sha 铁律**再次实录（run372 误用 7 位 → compose 触发 pull 失败；run373 成功），
  已写入 `AGENTS.md` #5 与交接坑录。
- 生产三件套 `master-58af54f2` healthy；`/ready` db/gitea/redis up（opencode 按需启动）。

## v2.1 — 2026-09-15（组织级模板库 + Go/Node 多栈）

> 未单独发版，内容并入 v2.2.0 Release Notes。要点：组织仓 `forja-admin/templates` 优先 + 内置兜底、
> 模板 `source/version` 透出、建项 pin `template_version`（迁移 039）、Node 全链路验收、Go 链路零回归。
> 详见 `docs/phases/phase-v1-production/v2.1-templates-multistack-plan.md`。

## v2.0.1 — 2026-09-15（M4b 交付底线补齐）

> 异地备份归档 sidecar（minio/mc，默认关）+ 资源库/附件 UI；E2E：归档同步 6 对象；
> 部署镜像 `master-04834837`。详见 `RELEASE-v2.0.0.md` 附节。

## v2.0.0 — 2026-09-14（交付通道生产化）

> M0–M5：模板/导入 → 技术栈契约 → 锻造 → 构建镜像 → 交付包（compose/部署手册/Release Notes/镜像）
> → 客户环境自部署 → UAT 反馈 → 回炉，全链路生产实测；guestbook 回炉实录。
> 详见 `docs/phases/phase-v1-production/RELEASE-v2.0.0.md` + `v2.0-delivery-production-plan.md`。

## UX 修复 — 2026-08-30 (/forge 锻造流水线生产化)

> 依据 `docs/reviews/audit-2026-08-29-full.md` /forge 专项研究：入口断链、阶段失真、无回放、主按钮失效四断点

### 修复
- **A1 活 bug**：`task.create` 补入前端项目级权限矩阵（legacy 别名 → task.write），
  `CreateTaskDialog` 改按项目内 `my_role` 判定（仅确认无权限才隐藏）——修复非 Admin 角色
  「派生锻造任务」按钮点击无反应（/forge 与 /tasks 双页面）。
- **A2 CI ✓失真**：Stepper 阶段④改名「合并推送」由 `forge.pushed/forge.merged` 驱动；
  CI 结论由后端 `ci.passed/ci.failed`（task 频道双发）驱动独立徽章（待构建/通过/失败），
  不再用推送冒充 CI 通过。
- **A3 事件补齐**：新增 `forge.merged`/`forge.error` 分支；失败位置保留精确阶段
  （门禁 failAt=2 不再笼统回落「Agent 锻造」）。
- **A4 操作反馈**：Approve/Converge/刷新加 loading+disabled 防重复；去掉组件层重复 toast
  （统一走 http 拦截器）；Approve 仅对 PENDING/FAILED/ESCALATED 显示且需 ConfirmDialog。
- **A5 基础回放**：SSE 无历史的历史任务落地任务事实行（状态/attempt/sha/人工介入/验证摘要），
  告别「等待事件…」空白；SSE 增加实时/离线连接指示。

### 整合
- **B1 路由参数化**：`/forge`（任务发现，按状态排序）+ `/forge/$taskId`（深链可分享），
  消灭手粘 UUID。
- **B3 看板互通**：任务卡片新增「流水线」入口跳 `/forge/:id`；详情页可返回列表/看板。
- **B4 结构收尾**：删除 631 行 App.tsx（SpecsPanel/TasksBoard/GanttPanel/ForgePanel 全部
  直连组件并懒加载，主 bundle 减负）；此前无引用的孤儿 `components/tasks/TasksBoard.tsx`
  （useMinLoading/h-8/遮罩增强版）正式接线；看板调试文案（真拖拽已启用+UUID dump）清零，
  DAG 原始 JSON 卡改统计摘要。
- **B5 结构化状态卡**：原始 JSON dump 三卡替换为 状态（置信度/分支/sha/人工介入人类可读）、
  锻造轮次（attempt 列表）、产物与验证（文件数/验证通过率）。
- **B6 规范对齐**：h-8 工具栏、useMinLoading+遮罩、`ui/empty` 空态（含派生引导）、
  项目级权限门（approve=task.approve / converge=task.run）。
- 新增测试：`lib/auth.test.ts`（权限矩阵回归）、`components/forge/ForgeStepper.test.ts`
  （事件 reducer 10 项）；AGENTS.md 范式样板路径同步（App.tsx → SpecsPanel.tsx）。

## Security 修复 — 2026-08-29 (audit-2026-08-29-full.md P0)

> P0-1 生产安全基线失效 + P0-2 CORS 缺 PUT，详见 `docs/reviews/audit-2026-08-29-full.md`

### 修复
- **P0-1 生产清单绕过安全基线**：`deploy/compose.app.yml` 注入 `FORJA_ENV=production`，
  `JWT_SECRET`/`APP_SECRET`/`FORJA_CORS_ORIGINS`/`GITEA_WEBHOOK_SECRET`/`DATABASE_URL`/
  `OPENCODE_PASSWORD` 全部改为 `${VAR:?}` 强制注入（api+worker，worker 需 APP_SECRET 解密
  model provider API Key），pg-backup 密码同步外部化；生产凭据经 runner.envs 与根 `.env`
  注入（gitignored），新密钥已生成并轮换 Gitea 侧 4 个 webhook 的 hook secret。
  顺带修复 pg-backup `depends_on: postgres` 跨文件引用致 compose config 校验失败。
- **P0-2 CORS 缺 PUT**：`main.go` AllowMethods 追加 `PUT`，修复跨源部署下 UI 编辑 spec
  （PUT /specs/:id）预检被拒。
- 根 compose 凭据漂移：opencode/api/worker 的 OPENCODE 密码改 `${OPENCODE_PASSWORD:-forja-dev}`
  引用（.env 的 `OPENCODE_SERVER_PASSWORD` 从此生效可覆盖）。

## v1.0 — 2026-08-26 (生产可用收口)

> `S6 构建部署分离 + S6-11 部署实体 + B1 80.0% + S4 规模化` 全闭环，`api 80.0%` `15/15 绿` `8d6c23f7` healthy

### 交付
- **S6/S6-11**：push 零流水线、`build.yml`/`deploy.yml` 双 workflow、`DEPLOY_TAG` Variable、`deployments` 实体表（scheduled/running/success/failed）、真横幅 `success-only`、部署历史表、自适应轮询 10s、在途互斥 409、docs-only 过滤；`HEAD-only` 构建（Gitea ref 仅分支）、`PUT` Variable
- **S4**：`schema_migrations` 版本表、Dockerfile alpine 瘦身、Helm Chart `values-dev/prod`、slog/Prometheus/Grafana、`GF_SECURITY_ADMIN_PASSWORD` 生产化（`grafana.local` + `runner-config.yaml`）
- **可观测**：`forja_*` 指标、`FORJA 概览` 8 panels、P99 告警、部署状态机 `reap 10m/8d`

### 质量
- **B1**：`api 68.8%→80.0%` 15 文件 8 波（G4 回写、部署实体、SSE、forge 3 轮、integrateGitea 等）；修复 `lastFailures finished_at→created_at`、`current_version` NULL 扫描、`vector` 隔离；`go vet 0` `15/15 绿`
- **审计**：`release-audit-v1.0.md` 全量通过；`roadmap`/`coverage`/`v1.1-sprint` 已回写；`docs/README` badge 接入

### 部署
- 镜像 `api/worker/web` `master-8d6c23f7` 三件套 healthy；`deploy/compose.app.yml` `--env-file` 生产凭据
- `HEAD` `3451f03` (B1) 待下轮 `build→deploy` 上线即完整 v1.0

## v0.3.0 — 2026-08-20 (FORJA 锻造坊首版)

> `规格入炉，交付出炉` — P0 奠基 → P3 淬火 全闭环交付，`go vet 0` `vite 877kB` `docker 5 Up 66 handlers` `5173 7 Tabs` 联调通过

### 产品定位
- FORJA 为管理任意新项目的产品 (单仓 `Go Fiber+React+pgvector+Compose` 脚手架可复制)，`000000...0001` 仅示例，可自举

### P0 奠基
- Constitution 9条 + Spec 体系 (BS/IS/DS/TS 4层，BS 表单/Gherkin PM零JSON，IS Swagger，DS ERD，TS Gherkin) + Inbox 轻量设计 + Rules 6件 + DB 14表 (`001_init`)

### P1 炉芯
- Monaco + RHF/zod + 8 门禁 (`gate.go`) + OpenCode `forja-build` + `BuildContext` 上下文注入 + `pipeline 180s vet` + SSE 锻造可视化

### P2 锻打
- 任务拆解 `depends_on/priority/est` `hasCycle` `batch≤50` `DAG ready` + `asynq Concurrency8` + `RBAC 4角色 JWT 2h` + `audit trace` + `PATCH` + `dnd-kit` 真拖拽 + `InboxBoard` 三列

### P3 淬火
- **P3.1 基础**：`sqlc 7查询 + migrate/v4` `002_vector ivfflat` `vector mock 1536` `estimate 15%→8%` `converge 0.7` `TS 自动草稿`
- **P3.2 交互**：`shadcn 9件` `GanttView` `TanStack Query+Zustand` `Recharts 4指标` `ChatPanel forja-plan` `RHF/zod`
- **P3 测试**：`contract 4` `gate 12` `vector 3` `spec 86%` `converge 3` `estimate 2` `integration 2` `vitest 1` `playwright 1` `CI .github/workflows/ci.yml` `coverage 94% frontend`

### 部署
- `Dockerfile golang:1.24` `GOTOOLCHAIN=auto` `vet 180s` `66 handlers` `5 services healthy` `preview 5173 877kB`

### 审计
- `p0-p1-audit` 有条件 → `p2-audit` 有条件 → `p3-audit` 通过 → `audit-full` 通过 (`specs 20 tasks 21 inbox 2`)

### 下一步
- P3 剩余 `集成测试生成/知识库跨项目/Helm` 按需

## v0.3.1 — 2026-08-20 (覆盖率冲刺)

> `api 54%` `validation 81%` 达标，`Inbox` 7 Tabs 881kB 全绿

### 测试
- `api 27→60%`：`cycle_test.go` `hasCycle` `batch` 边界 4 用例 + `more_test.go` 全生命周期 2 用例
- `auth 88%` `queue 65%`：`auth_test.go 7` `queue_test.go 2`
- `validation 68→81%`：`pipeline_vet_test.go` `VET_TEST_DIR` 真 `vet` + `pipeline_integration` 3 分支
- `spec 86%` `frontend 94%` 已稳，`CI` 已串 `go vet/test/build` + `npm build/test`

### 修复
- `pipeline.go` `VET_TEST_DIR` 支持 + `SKIP_VET` mock + `truncate []rune`
- `api/cycle_test.go` 去重 `require` 未用
- `docs/coverage.md` 更新 `api 59.9%` `validation 81.2%` 徽章

## v0.4.0 — 2026-08-20 (P3 闭环 + 知识库 + Pact)

> `8 Tabs 884kB` `68 handlers` `Pact 1` `知识库 3` `全量审计通过`

### P3 增量
- **Pact**：`testgen/pact.go 68%` `POST /specs/:id/pact` IS→Pact `interactions` 落库 `test_cases pact` + `SpecsPanel` 生成按钮
- **知识库**：`vector ALL` 跨项目 `SearchBoard` 8th Tab `GET /specs/search?project_id=ALL` `3` + `ivfflat` 推荐
- **审计**：`audit-2026-08-20-v04.md` 全量 `P0-3 8 Tabs` 通过，`specs 20+ tasks 27 inbox 3`

### 部署
- `68 handlers` (`pact` 2 新增) `5 Up` `preview 884kB (HW-4CJSY.js)`

## v0.5.0 — 2026-08-21 (Polish 1-3 抛光)

> `BS 表单 PM 零 JSON + IS Swagger + DS ERD + 443kB 拆分 + Inbox docx`

### Polish
- **BS 表单**：`App.tsx:98 BSForm` `story_id/title/description` + `AC Gherkin 三列可增行` → `JSON.stringify` 自动生成 `BS JSON` (PM 零 JSON) + `IS Swagger` `ERD` 占位升级真可视化
- **IS Swagger**：`components/SwaggerEditor.tsx:1` `swagger-ui-react 1.3M (lazy)` + `js-yaml` 渲染 `OpenAPI` + `App.tsx:199` `Editor 200px` + `Suspense`
- **DS ERD**：`components/ERDEditor.tsx:1` `reactflow` `entity→fields` 3 节点 `Background/Controls/MiniMap`
- **拆分**：`App.tsx:20 lazy 7` `Gantt/Metrics/Chat/Inbox/Search/Swagger/ERD` → `884kB → 443kB (BRpuGbLr.js) + 393kB Metrics + 36kB Gantt + 1.3M Swagger lazy` `preview 200` `8 Tabs`
- **Inbox docx**：`internal/inbox/parser.go:8 docx zip→<w:t> + pdf placeholder MaxUpload 5MB` `api/inbox.go:34 multipart` `frontend/InboxBoard accept .docx/.pdf` `47.5%→84.7%`

### 部署
- `go vet 0` `go build 0` `vite 443kB` `docker 5 Up` `8 Tabs` 全绿

## Unreleased — 2026-08-22 (A5 安全加固 + A6 项目上下文 + 表单规范)

> 详细决策与审计记录见 docs/phases/phase-v1-production/{A5-qa-release,A6-project-context}.md 与 AGENTS.md

### A5 交付质量与安全
- PM/QA 角色 + 覆盖率门禁 + 发布状态机；respond 哨兵修复存量鉴权缺陷、approve 状态机守卫、错误脱敏、JWT HS256 强制、安全测试套件 7 用例

### A6 项目上下文
- project_id 后端必填(400)、前端全页面接线项目切换器、ALL 假功能移除、store 非法值清洗

### 表单规范 (AGENTS.md)
- 新立前端表单规范：RHF+zod 内联校验，UserBoard/ProjectBoard 四个弹窗迁移完成，清除 setMsg 式隐藏失败路径
- 自助修改密码：POST /auth/change-password（验旧密码+审计）+ 头部「改密」弹窗（AGENTS 规范）；表单间距统一 gap-2 对齐 shadcn FormItem
- 个人中心页：GET /auth/me + Profile 三卡（账户信息/项目身份/改密）；账户块收拢 sidebar footer（NavUser 模式，折叠态收缩头像），头部用户区移除
- 最后登录时间：迁移 007 users.last_login_at + 登录打点，用户列表/个人中心展示
