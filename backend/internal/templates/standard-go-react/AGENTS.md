# {{name}} — 项目规范

> 本文件由 FORJA 脚手架生成，是**项目级规范唯一真相**（随仓评审、版本化）。
> 锻造时会被注入 Agent 提示词，优先级高于 FORJA 全局规则；请按项目实际修改。
> 编辑入口：FORJA「项目详情 → 规范」页签，或直接在 Git 中修改（下次锻造自动生效）。

## 技术栈
- 后端：Go 标准库 `net/http`（`main.go`，`/health` 健康端点）
- 前端：React + Vite（`frontend/`，由锻造迭代生成）
- 测试：`go test ./...`；前端 `npm test --prefix frontend`
- 如确需引入 Web 框架/依赖（如 Fiber），在 Spec 中声明并由锻造添加

## 编码约定
- 错误处理：`fmt.Errorf("...: %w", err)`，禁止静默吞错
- 分层：handler 只做参数校验与响应，业务逻辑下沉内部包
- 命名：Go 标准；HTTP 路径 kebab-case；JSON 字段 snake_case
- 禁止：提交任何密钥/token；改动 `deploy/` 与 `.gitea/` 需在变更说明中解释

## 测试与门禁
- 新增行为必须带对应单测；覆盖率不低于 `project.yaml` 的 `coverage_gate`
- 提交前本地：`go vet ./... && go test ./...`
- CI 门禁不过视为未完成，不得进入签核

## 提交规范
- `feat|fix|docs|chore(scope): 中文简述`
