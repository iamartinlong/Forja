# {{name}}

由 FORJA 脚手架生成的 Node.js 可运行骨架（零依赖 HTTP 服务）。

## 快速开始

```bash
node src/server.js     # 监听 :8080（PORT 可覆盖）
curl -s localhost:8080/health
npm test               # node:test 用例
```

## 构建与运行（容器）

```bash
docker build -t {{slug}} .
docker run --rm -p 8080:8080 {{slug}}
```

客户环境部署请使用交付包中的 `deploy/compose.yml` 与 `README-DEPLOY.md`。

## 目录

| 路径 | 说明 |
|------|------|
| `src/app.js` | `createServer` 路由（`/health`、`/`） |
| `src/server.js` | 入口（监听端口） |
| `test/` | `node:test` 用例 |
| `project.yaml` | 技术栈/测试/构建/运行契约（仓内唯一真相） |
| `AGENTS.md` | 项目级 AI 规范（锻造注入） |
| `Dockerfile` | 运行镜像（非 root） |
| `deploy/compose.yml` | 客户环境运行清单模板 |
| `.gitea/workflows/build.yml` | 构建镜像流水线（FORJA 触发） |
