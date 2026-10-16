package api

// S4-1/S4-2：/ready 真探测（v1.1-sprint W7-W8 / compose healthcheck 数据源）。
//
//   - 硬依赖：DB、Redis —— 任一不可达返回 503 + 各项明细（部署 --wait 以此为准，
//     替代旧假 ready 导致的"DB 挂了容器仍 healthy"盲区，KI-007 关联）
//   - 软依赖：embedding / opencode / gitea —— 仅报告不计入失败（外部依赖永不阻塞门禁与检索；
//     未配置时报 disabled，已配置时 3s 超时探测 /global/health 与 /api/v1/version）
//
// api(:8080/ready) 与 worker(:8081/ready) 共用同一 Handler。

import (
	"context"
	"encoding/base64"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"forja/backend/internal/api/respond"
	"forja/backend/internal/metrics"
	"forja/backend/internal/model"

	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
)

const probeTimeout = 3 * time.Second

// probeHTTP performs a GET with context timeout, optional BasicAuth, and reports up/down.
func probeHTTP(ctx context.Context, url, username, password string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "down: " + err.Error()
	}
	if username != "" || password != "" {
		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(username+":"+password)))
	}
	client := &http.Client{Timeout: probeTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return "down: " + err.Error()
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "down: http " + http.StatusText(resp.StatusCode)
	}
	return "up"
}

// ReadinessHandler 返回就绪探测 handler；探测互相独立、单项失败快速短路累计明细。
func ReadinessHandler() fiber.Handler {
	return func(c *fiber.Ctx) error {
		ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
		defer cancel()

		checks := fiber.Map{}
		ready := true

		// DB：池未初始化（KI-007 自愈中）或 Ping 失败均判 down
		if model.Pool == nil {
			checks["db"] = "down: pool not initialized"
			ready = false
		} else if err := model.Pool.Ping(ctx); err != nil {
			checks["db"] = "down: " + err.Error()
			ready = false
		} else {
			checks["db"] = "up"
		}

		// Redis：queue/ratelimit/events 共用 REDIS_ADDR；独立短连接避免复用故障实例
		addr := os.Getenv("REDIS_ADDR")
		if addr == "" {
			addr = "localhost:6379"
		}
		pwd := os.Getenv("FORJA_REDIS_PASSWORD")
		if pwd == "" {
			pwd = os.Getenv("REDIS_PASSWORD")
		}
		rdb := redis.NewClient(&redis.Options{
			Addr: addr, Password: pwd, DialTimeout: probeTimeout, ReadTimeout: probeTimeout, WriteTimeout: probeTimeout,
			MaxRetries: 1, // 探测要快失败：默认 3 次重试+退避会拖满超时窗并刷屏连接日志
		})
		if err := rdb.Ping(ctx).Err(); err != nil {
			checks["redis"] = "down: " + err.Error()
			ready = false
		} else {
			checks["redis"] = "up"
		}
		_ = rdb.Close()

		// embedding：软依赖仅报告
		if os.Getenv("FORJA_API_KEY") == "" {
			checks["embedding"] = "mock (FORJA_API_KEY unset)"
		} else {
			checks["embedding"] = "configured"
		}

		// opencode：软依赖（FORJA 锻造引擎）；未配置报 disabled，已配置则 3s 探活
		if ocURL := strings.TrimRight(os.Getenv("OPENCODE_URL"), "/"); ocURL == "" {
			checks["opencode"] = "disabled (OPENCODE_URL unset)"
		} else {
			user := os.Getenv("OPENCODE_USERNAME")
			if user == "" {
				user = "opencode"
			}
			pass := os.Getenv("OPENCODE_PASSWORD")
			if pass == "" {
				pass = "forja-dev"
			}
			checks["opencode"] = probeHTTP(ctx, ocURL+"/global/health", user, pass)
		}

		// gitea：软依赖（代码托管）；未配置报 disabled，已配置则 3s 探活
		if gURL := strings.TrimRight(os.Getenv("GITEA_URL"), "/"); gURL == "" {
			checks["gitea"] = "disabled (GITEA_URL unset)"
		} else {
			// /api/v1/version 无需认证且始终可达；避免依赖 token 权限
			checks["gitea"] = probeHTTP(ctx, gURL+"/api/v1/version", "", "")
		}

		status := "ready"
		code := fiber.StatusOK
		if !ready {
			status = "unavailable"
			code = fiber.StatusServiceUnavailable
		}
		return c.Status(code).JSON(fiber.Map{"status": status, "checks": checks})
	}
}

// StartWorkerReadyServer worker 侧健康端点（默认 :8081）：compose healthcheck 探测口。
// 阻塞运行；监听失败仅记日志（worker 主流程不因健康口被占而拒绝锻造成单）。
func StartWorkerReadyServer(addr string) {
	if addr == "" {
		addr = ":8081"
	}
	go func() {
		// ErrorHandler：MetricsAuthMiddleware 以 respond 哨兵错误中断，
		// 无 ErrorHandler 时 Fiber 默认兜底会把 401 覆盖成 500（M2 实录）
		app := fiber.New(fiber.Config{DisableStartupMessage: true, ErrorHandler: respond.HandleError})
		app.Get("/ready", ReadinessHandler())
		app.Get("/health", func(c *fiber.Ctx) error {
			return c.JSON(fiber.Map{"status": "ok", "service": "forja-worker"})
		})
		// S4 #4b：worker 抓取端点与就绪探测同端口（M2 起可选 FORJA_METRICS_TOKEN 鉴权）
		app.Get("/metrics", MetricsAuthMiddleware(), metrics.MetricsHandler())
		if err := app.Listen(addr); err != nil {
			log.Printf("worker ready server: %v", err)
		}
	}()
}
