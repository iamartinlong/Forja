package main

import (
	"context"
	"log"
	"os"
	"strings"
	"time"

	"forja/backend/internal/agent"
	specapi "forja/backend/internal/api"
	"forja/backend/internal/api/respond"
	"forja/backend/internal/auth"
	"forja/backend/internal/furnace"
	"forja/backend/internal/logging"
	"forja/backend/internal/metrics"
	"forja/backend/internal/model"
	"forja/backend/internal/queue"
	"forja/backend/internal/ratelimit"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/fiber/v2/middleware/requestid"
)

// dbInitTimeout 单次 InitDB 的超时（覆盖"全新库全量迁移"窗口）。
// 默认 60s；资源饥饿环境（e2e 冷启动 + security 并行）可由 FORJA_DBINIT_TIMEOUT 放宽。
func dbInitTimeout() time.Duration {
	if v := os.Getenv("FORJA_DBINIT_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 60 * time.Second
}

// tryInitDB 带间隔的 InitDB 重试；attempts<=0 表示无限重试直到成功。
// 每次尝试给独立 ctx（覆盖单次迁移全量窗口，pg 就绪等待交给重试次数）。
func tryInitDB(attempts int, every time.Duration) bool {
	for i := 0; attempts <= 0 || i < attempts; i++ {
		if i > 0 {
			time.Sleep(every)
		}
		ctx, cancel := context.WithTimeout(context.Background(), dbInitTimeout())
		err := model.InitDB(ctx)
		cancel()
		if err == nil {
			return true
		}
		log.Printf("DB init attempt %d failed: %v", i+1, err)
	}
	return false
}

// enforceProductionPolicy 生产环境安全基线强制项（S1.3 / KI-005）：
// 弱 JWT_SECRET 或未收紧的 CORS 在 FORJA_ENV=production 时拒绝启动。
func enforceProductionPolicy() {
	if os.Getenv("FORJA_ENV") != "production" {
		return
	}
	s := os.Getenv("JWT_SECRET")
	if s == "" || s == auth.LegacyDevSecret || len(s) < 32 {
		log.Fatal("production requires a strong JWT_SECRET (>=32 random chars); refusing to start")
	}
	if strings.TrimSpace(os.Getenv("FORJA_CORS_ORIGINS")) == "" {
		log.Fatal("production requires FORJA_CORS_ORIGINS whitelist; refusing to start")
	}
	sec := os.Getenv("APP_SECRET")
	if sec == "" {
		sec = os.Getenv("FORJA_APP_SECRET")
	}
	if len(sec) < 32 {
		log.Fatal("production requires a strong APP_SECRET (>=32 chars) for AES-GCM; refusing to start")
	}
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// DB init with retry：迁移加固后逐文件真实执行（不再吞错快过），
	// pgvector 服务容器冷启动 initdb 可达分钟级——15s 一次性尝试会被
	// context deadline 杀掉且旧代码并无"retry on request"，请求将永久 503。
	// 策略：启动期重试至多 ~2min；仍未就绪则降级启动，由后台 goroutine 持续自愈，
	// 就绪瞬间 Pool 恢复、503 自然消失（无需重启进程）。
	dbReady := tryInitDB(24, 5*time.Second)
	if !dbReady {
		log.Println("DB still not ready after startup retries; serving 503 until background heal succeeds")
		go func() {
			tryInitDB(0, 15*time.Second) // 无限自愈
			log.Println("DB healed in background")
		}()
	} else {
		defer model.CloseDB()
		log.Println("DB connected")
	}
	queue.Init()
	_ = furnace.EnsureDefaultOnBoot(context.Background())
	furnace.StartReaper(context.Background())
	furnace.StartHealthProbe(context.Background())
	// 炉池驱动 opencode：启动时将 model_providers 同步为 opencode provider 配置（避免静态 opencode.json 与炉池选择脱节）
	go func() {
		// 等待 DB 就绪后重试同步（最多 1 分钟）
		for i := 0; i < 12; i++ {
			if model.Pool != nil {
				agent.SyncOpencodeProviders(context.Background())
				break
			}
			time.Sleep(5 * time.Second)
		}
	}()
	enforceProductionPolicy()

	app := fiber.New(fiber.Config{
		AppName:      "FORJA API v0.1.0",
		ErrorHandler: respond.HandleError,
		// M1.3：项目镜像 tar 回传常超 Fiber 默认 4MB 上限（run364 实录 413）；
		// BodyLimit 以下缓冲，超限请求体转流式（上传端读 Request.BodyStream）。
		BodyLimit:         64 << 20,
		StreamRequestBody: true,
	})

	app.Use(recover.New())
	app.Use(requestid.New())
	logging.Setup("api")
	// S4 #4b：Prometheus 请求指标（路由模板在后续注册中定型，中间件先挂不影响）
	metrics.SetService("api")
	app.Use(metrics.FiberMiddleware())
	// S4 #4a：结构化请求日志（JSON，含 request_id/user/latency；4xx Warn / 5xx Error）
	app.Use(logging.RequestLogger())
	// CORS 白名单（S1.3 / KI-005）：FORJA_CORS_ORIGINS 逗号分隔；
	// 未设置时开发环境放行全部并告警，生产启动已被 enforceProductionPolicy 拦截。
	corsOrigins := os.Getenv("FORJA_CORS_ORIGINS")
	if corsOrigins == "" {
		corsOrigins = "*"
		log.Println("WARN: FORJA_CORS_ORIGINS unset, allowing all origins (dev only)")
	}
	app.Use(cors.New(cors.Config{
		AllowOrigins: corsOrigins,
		AllowHeaders: "Origin, Content-Type, Accept, Authorization",
		AllowMethods: "GET,POST,PUT,PATCH,DELETE,OPTIONS",
	}))

	// Health — Phase 0.1
	app.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok", "service": "forja-api", "version": "0.1.0"})
	})
	// Ready — S4 #3 真探测：DB/Redis 硬依赖任一不可达即 503（compose healthcheck 数据源）
	app.Get("/ready", specapi.ReadinessHandler())
	// S4 #4b：Prometheus 抓取端点（compose 内网；M2 起可选 FORJA_METRICS_TOKEN 鉴权）
	app.Get("/metrics", specapi.MetricsAuthMiddleware(), metrics.MetricsHandler())

	// public auth（S1.1：登录限流 100/min/用户，超限 429）
	app.Post("/api/v1/auth/login", ratelimit.LoginMiddleware, auth.LoginHandler)
	// S1.2：刷新/登出（refresh 按 IP 限流防滥用）
	app.Post("/api/v1/auth/refresh", ratelimit.IPMiddleware("refresh"), auth.RefreshHandler)
	app.Post("/api/v1/auth/logout", ratelimit.IPMiddleware("logout"), auth.LogoutHandler)

	// M-CICD-4b：Gitea webhook 公开回调（HMAC 签名自鉴权）
	pub := app.Group("/api/v1")
	specapi.RegisterWebhookRoutes(pub)
	// M1.3：CI 服务令牌组（X-Forja-Token，无 JWT）——镜像 tar 回传
	specapi.RegisterInternalRoutes(pub)

	api := app.Group("/api/v1")
	api.Use(auth.JWTMiddleware)
	specapi.RegisterSpecRoutes(api)
	specapi.RegisterTaskRoutes(api)
	specapi.RegisterAuditRoutes(api)
	specapi.RegisterEstimateRoutes(api)
	specapi.RegisterInboxRoutes(api)
	specapi.RegisterInboxAttachmentRoutes(api)
	specapi.RegisterProjectFileRoutes(api)
	specapi.RegisterPactRoutes(api)
	specapi.RegisterProjectRoutes(api)
	specapi.RegisterDeliveryRoutes(api)
	specapi.RegisterUATRoutes(api)
	specapi.RegisterUserRoutes(api)
	specapi.RegisterReindexRoutes(api)
	specapi.RegisterPipelineRoutes(api)
	specapi.RegisterIterationRoutes(api)
	specapi.RegisterCostRoutes(api)
	specapi.RegisterFurnaceRoutes(api)
	specapi.RegisterReleaseRoutes(api)
	specapi.RegisterModelProviderRoutes(api)
	specapi.RegisterAdminTemplateRoutes(api)
	specapi.RegisterSSERoutes(api)

	log.Printf("FORJA API listening on :%s", port)
	if err := app.Listen(":" + port); err != nil {
		log.Fatal(err)
	}
}
