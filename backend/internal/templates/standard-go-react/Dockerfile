# 多阶段构建；运行阶段非 root、无包管理器
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/app .

FROM alpine:3.20
RUN adduser -D -H -u 10001 app
COPY --from=build /out/app /app
USER app
EXPOSE 8080
ENTRYPOINT ["/app"]
