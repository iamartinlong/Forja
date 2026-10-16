
# Stage 4 #2 镜像瘦身（双 target 拆分，替代原单一 golang:alpine 运行时）：
#   --target api    纯 alpine + 静态二进制（~20MB 级）
#   --target worker alpine + go 工具链 + git + nodejs/npm（门禁引擎不可裁；v2.1 多栈扩 node）
#
# 保留项与依据：
#   db/migrations —— schema_migrations 版本化迁移从磁盘读取（Stage4 #1），
#                    缺失会导致全新库初始化被静默跳过；
#   worker 的 go 工具链 —— validation.RunPipeline 的 hasGo() 是 vet/test/覆盖率
#                    门禁的执行前提（pipeline.go），纯 alpine 会静默退化为仅静态扫描；
#   worker 的 go.mod/go.sum —— 门禁回退链探测 /app/go.mod（pipeline.go:102/:142），
#                    命中后按 "matched no packages"/"no test files" 分支放行；
#   git —— S3.5 runForge 在工作副本执行 clone/apply/push。
# 移除项：runtime 不再烤入 internal/cmd 源码树（历史为 /app vet 回退准备，
#         生产路径恒传产物目录，S3.5.1）。

FROM golang:1.25-alpine AS builder
ENV GOTOOLCHAIN=local
ENV GOPROXY=https://goproxy.cn,direct
WORKDIR /app
COPY go.mod go.sum ./
COPY vendor ./vendor
COPY . .
RUN CGO_ENABLED=0 go build -mod=vendor -ldflags="-s -w" -trimpath -o /out/api ./cmd/server && CGO_ENABLED=0 go build -mod=vendor -ldflags="-s -w" -trimpath -o /out/worker ./cmd/worker

# ---- api：极简运行时 ----
FROM alpine:3.20 AS api
# M5+（2026-09-14 release merge_sha 实录）：release 的回退合并需 git（此前 api 无 git，
# forgeWorkspace 失败被静默跳过）；fork/子进程隔离语义不变，仅补二进制
RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.aliyun.com/' /etc/apk/repositories && apk add --no-cache ca-certificates git && adduser -D -g '' forja
WORKDIR /app
COPY --from=builder /out/api /app/api
COPY --from=builder /app/db/migrations /app/db/migrations
RUN chown -R forja:forja /app
USER forja
EXPOSE 8080
CMD ["/app/api"]

# ---- worker：锻造 + 门禁运行时 ----
# 不删 GOROOT src：门禁需在 workspace 里 go vet/test（vet 从源码类型检查，
# Go 1.20+ std 无预编译产物——瘦身曾致门禁只能走"无测试放行"分支空转，2026-09-06）
# v2.1 多栈：nodejs+npm 供 node 栈门禁（npm test / npm audit，见 validation/stack_gate.go）
# v2.2 T1：python3+py3-pip 供 python 栈门禁（pytest / coverage lcov）；PIP_INDEX_URL 默认
#   阿里云镜像（PyPI 直连超时实录），运行时可用 compose env 覆盖。
FROM golang:1.25-alpine AS worker
ENV GOTOOLCHAIN=local \
    PIP_DISABLE_PIP_VERSION_CHECK=1 \
    PIP_INDEX_URL=https://mirrors.aliyun.com/pypi/simple/ \
    PIP_TRUSTED_HOST=mirrors.aliyun.com \
    PIP_BREAK_SYSTEM_PACKAGES=1
RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.aliyun.com/' /etc/apk/repositories && apk add --no-cache ca-certificates git nodejs npm python3 py3-pip && adduser -D -g '' forja && mkdir -p /tmp/forja-work /home/forja/.cache && chown -R forja:forja /tmp/forja-work /home/forja
WORKDIR /app
COPY --from=builder /out/worker /app/worker
COPY --from=builder /app/db/migrations /app/db/migrations
COPY --from=builder /app/go.mod /app/go.mod
COPY --from=builder /app/go.sum /app/go.sum
RUN chown -R forja:forja /app
USER forja
CMD ["/app/worker"]
