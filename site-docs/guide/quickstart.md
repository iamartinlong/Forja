# 快速开始

> 目标：走通「登录 → 建项 → 需求入堆 → Spec → 派生任务 → 锻造 → 签核 → 构建交付」主链路。
> 本文配图均来自系统实测截图（`site-docs/public/screenshots/`）。

## 1. 登录与工作台

浏览器打开站点（本地 `http://localhost:8080` 或部署地址），使用账号登录。默认演示账号为
`admin / admin`（生产请立即修改）。

登录后进入**工作台**：左侧为模块导航，顶部为项目切换器，中部为当前项目的统计、最近动态、
成本概览与交付状态。

![工作台](/screenshots/workbench.png)

> 个人资料（右上角头像）可查看登录日志、自助改密；平台级设置见「用户 / 炉池 / 模型提供商」。

## 2. 创建项目（模板 + 参数问卷）

「项目 → 新建项目」：选择模板（Go+React / Node / Python），填写项目名与描述。
带参数的模板会弹出**参数问卷**（如 Python 版本、健康检查路径、是否关闭 ReDoc），
提交后系统自动：

1. 创建 Git 仓库并写入脚手架（15 个文件，含 `project.yaml`、`Dockerfile`、CI 工作流）；
2. 写入构建 secrets 与 webhook；
3. 回填 `repo_full_name`、`stack`、`template_version`。

![项目列表](/screenshots/projects.png)

项目详情页汇总**成员 / 规格 / 任务 / 收件箱 / 交付 / 成本 / 最近动态**，是日常工作的起点。

![项目详情](/screenshots/project-detail.png)

## 3. 需求入堆（原料）

项目 →「收件箱」：粘贴文本或上传 `PDF / Word` 附件，卡片展示相对时间、创建人与状态；
`CONVERTED` 状态的卡片可跳转到由它生成的 Spec。

![收件箱](/screenshots/inbox.png)

## 4. 整理为 Spec

将入堆原料**升格为 Spec**：`BS / IS / DS / TS` 四层分层描述，支持关联既有 Spec、锚点文件、
回归约束（自动转译为"不得破坏"清单）。编辑器带路径与格式校验。

![规格详情](/screenshots/spec-detail.png)

## 5. 派生锻造任务

Spec 校验通过后，在详情页**派生锻造任务**。任务进入 `PENDING`，批准后：
- 自动切出隔离分支 `forge/task-<短id>-a<轮次>`（`master` 不直推）；
- Agent 按 Spec 生成 diff 与测试，产出结构化结果（文件清单 / 变更日志 / 置信度）。

## 6. 锻造与门禁

「锻造」页展示单任务的完整流水线：阶段步进、每轮 attempt 的产物、验证摘要与耗时。
门禁依次执行：静态检查 → 构建 → 单测/Batch → 覆盖率（lcov）→ 安全检查；
失败自动进入下一轮修复（≤3 轮），仍失败则升级为 `ESCALATED` 等待人工。

![任务看板](/screenshots/tasks.png)
![锻造面板](/screenshots/forge.png)

## 7. 签核发布

锻造通过（`RELEASE_PENDING`）后，架构师在**发布签核**弹窗核对变更文件与未通过测试，
输入任务名确认后合并 `master` 并打标签；误发可回退（`POST /tasks/:id/revert`）。

## 8. 构建与交付

「构建部署」页两个页签：

- **流水线**：push 与构建的 run/job 明细（结论、耗时、触发者），支持按结论筛选、翻页；
- **部署记录**：定时/手动部署全量台账（状态机 `scheduled → running → success/failed`），
  线上版本高亮。

点击 `SUCCESS` 行的「部署」发起滚动更新（可定时）。

![构建部署](/screenshots/pipelines.png)

构建成功后组装**交付包**（五件：`delivery.json` / `deploy/compose.yml` /
`README-DEPLOY.md` / `CHANGELOG.md` / `project.yaml`，镜像 tar 单独下载），
客户侧 `docker load + compose up -d` 即可运行。

## 9. 指标与治理

「指标」页：Spec/Task 分布、14 天流水线成功率趋势，以及**成本分包**卡片
（按项目/周期聚合执行数与 tokens，可导出 CSV）。平台级「用户 / 炉池 / 模型提供商」均支持
分页与检索；审计可按项目导出 CSV。

![指标与成本](/screenshots/metrics.png)
![用户管理](/screenshots/users.png)
![炉池](/screenshots/furnaces.png)

---

遇到不认识的词？见[概念与术语](/glossary)；角色视角的操作见[按角色使用](/guide/handbook)。
