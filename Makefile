# 本地二进制快验（日常开发）—— 不走 CI/CD 镜像流水线
# 约定：infra 仅 pgvector/redis/gitea 已在 docker compose 中健康；api/worker/web 本地直连 localhost
# 例：make dev        # 后端 :8080 + 前端 :5173 并行热重载
#     make test       # 前后端单测
#     make build      # 本地快检（go vet/build + vite build）

.PHONY: dev dev-api dev-web test build vet

dev:
	@echo "== infra check ==" && docker compose ps postgres redis | grep -q healthy || (echo "hint: docker compose up -d postgres redis gitea" && exit 1)
	@echo "== dev: api :8080 + web :5173 ==" && (cd backend && go run ./cmd/server) & (cd frontend && npm run dev) & wait

dev-api:
	cd backend && go run ./cmd/server

dev-web:
	cd frontend && npm run dev

test:
	cd backend && go vet -mod=vendor ./... && go test -mod=vendor ./... -count=1 -p 1
	cd frontend && npm run lint && npm test

build:
	cd backend && go vet -mod=vendor ./... && go build -mod=vendor -ldflags="-s -w" -trimpath -o /tmp/forja-api ./cmd/server && echo "backend -> /tmp/forja-api"
	cd frontend && npm run build && echo "frontend -> dist/"

vet:
	cd backend && go vet -mod=vendor ./...
	cd frontend && npm run lint
