# 概念与术语

> 单一事实源：产品内 tooltip 的「详见文档」链接指向本页各词条锚点；
> 前端词表 `frontend/src/lib/glossary.ts` 与本页逐条对齐。

| 术语 | 英文 | 一句话解释 |
| ---- | ---- | ---------- |
| [入堆](#inbox) | Inbox | 原始需求的收集池（文本/PDF/Word） |
| [规格](#spec) | Spec | 结构化、分层的需求数据，锻造的唯一依据 |
| [收敛](#converge) | converge | 多版本 Spec 合并出最新权威版本的过程 |
| [门禁](#gate) | gate | Spec 进入下一状态的评审关卡 |
| [契约](#pact) | pact | 接口行为约定，前后端共同遵守 |
| [锻造](#forge) | forge | Agent 依据 Spec 生成代码与测试的过程 |
| [轮次](#attempt) | attempt | 锻造的重试序号，最多 3 轮 |
| [待签核](#release_pending) | RELEASE_PENDING | 锻造完成等待人工发布的终审状态 |

## 入堆 Inbox {#inbox}

原始需求的收集池。粘贴文本或上传 PDF/Word 均可入堆，入堆内容尚未结构化，
需经「起草 Spec」转为规格后才能进入锻造链路。附件走对象存储（MinIO/S3），
按入堆条目隔离。

## 规格 Spec {#spec}

结构化、分层的需求数据，是锻造的**唯一依据**——Agent 不读入堆原文，只读 Spec。
Spec 有版本（v1/v2…）与生命周期状态（草稿→评审→已批准→已发布），每次编辑产生
新版本，时间线页可回看任意版本 diff。

## 收敛 converge {#converge}

多版本 Spec 合并出最新权威版本的过程。当同一需求存在多个草稿版本时，
由架构师发起收敛：逐节确认取舍，产出唯一的权威版本；被废弃版本保留备查。
实现与规格不一致时称为**漂移**，需回到 Spec 修正而非直接改代码。

## 门禁 gate {#gate}

Spec 进入下一状态的评审关卡，例如 `go vet + go test + 覆盖率达标`。
门禁是自动执行的：锻造每轮产出 diff 后都会过门禁，不过则进入下一轮修复；
三轮均不过则任务升级（ESCALATED），转人工介入。

## 契约 pact {#pact}

接口行为约定，前后端共同遵守并由契约测试守护。Spec 中涉及接口变更的章节
可生成 pact（`POST /specs/:id/pact`），前后端实现都必须满足 pact 断言，
契约测试失败同样会计入锻造轮次。

## 锻造 forge {#forge}

Agent 依据已批准的 Spec 生成代码与测试的过程。一次锻造最多 **3 轮**自动修复：
每轮产出 diff → 跑门禁 → 失败则把失败上下文回喂下一轮。锻造在炉（Furnace）
里执行——炉是绑定的 opencode Agent 容器，探活失败会自动标为不可达，
不伪造执行记录。

## 轮次 attempt {#attempt}

锻造的重试序号（第 1/2/3 轮）。轮次记录在 `agent_executions` 表中，
含每轮的 token 用量（v1.3 起回传）与成败；炉面板的成功率与 token 列
即按轮次记录聚合。熔断规则看最近 3 个轮次：全失败则炉标为不健康；
管理员可在炉面板/详情页手动恢复（状态回 healthy、计数清零、尝试拉起容器）。

## 待签核 RELEASE_PENDING {#release_pending}

锻造完成、门禁已过，等待人工发布的终审状态。仅架构师/管理员可签核
（`POST /tasks/:id/release`）或驳回（`reject`）；签核后 commit 回流项目仓，
任务进入 RELEASED。驳回需写理由，任务退回待办重新锻造。
