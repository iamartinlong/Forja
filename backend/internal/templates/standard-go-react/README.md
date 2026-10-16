# {{name}}

由 FORJA 脚手架生成的可运行骨架（Go 标准库 HTTP 服务）。

## 快速开始

```bash
go run .            # 监听 :8080（PORT 可覆盖）
curl -s localhost:8080/health
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
| `main.go` | HTTP 服务与 `/health` |
| `project.yaml` | 技术栈/测试/构建/运行契约（仓内唯一真相） |
| `AGENTS.md` | 项目级 AI 规范（锻造注入） |
| `deploy/compose.yml` | 客户环境运行清单模板 |
| `.gitea/workflows/build.yml` | 构建镜像流水线（FORJA 触发） |
