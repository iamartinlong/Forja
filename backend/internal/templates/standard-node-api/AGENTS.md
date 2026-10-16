# {{name}} — 项目规范

> 本文件由 FORJA 脚手架生成，是**项目级规范唯一真相**（随仓评审、版本化）。
> 锻造时会被注入 Agent 提示词，优先级高于 FORJA 全局规则；请按项目实际修改。
> 编辑入口：FORJA「项目详情 → 规范」页签，或直接在 Git 中修改（下次锻造自动生效）。

## 技术栈
- 运行时：Node.js 20（零依赖，`node:http` 标准库）
- 测试：`node:test`（`npm test`，用例在 `test/`）
- 入口：`src/server.js`（监听）→ `src/app.js`（`createServer` 路由）

## 编码约定
- CommonJS（`require` / `module.exports`）
- 错误路径必须返回 JSON 与正确状态码（400/404/500），禁止静默吞错
- 新增依赖：先在 Spec 说明，再更新 `package.json` 并在 `Dockerfile` 增加安装步骤
  （当前模板零依赖，Dockerfile 不执行 npm install；引入依赖后须补 `npm ci`）
- 密钥一律走环境变量，禁止提交任何凭据

## 测试与门禁
- 新增行为必须带对应 `node:test` 用例；提交前本地 `npm test`
- CI 门禁不过视为未完成，不得进入签核

## 提交规范
- `feat|fix|docs|chore(scope): 中文简述`
