// Package metrics S4 #4b：Prometheus 指标基座。
//
//   - HTTP：请求计数 + 时长直方图（path 取路由模板，防标签基数爆炸）
//   - Forge：attempt 终态计数、全链路时长直方图（P99>3min 告警锚点桶）
//   - Pipeline：run 时长直方图（聚合终态时观测）
//   - Queue：asynq 队列深度定时采样（worker 侧）
//
// api(:8080/metrics) 与 worker(:8081/metrics) 共用；service 标签区分来源。
package metrics

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/adaptor"
	"github.com/hibiken/asynq"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	HTTPRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "forja_http_requests_total",
		Help: "HTTP requests by method/route/status/service",
	}, []string{"method", "route", "status", "service"})

	HTTPDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "forja_http_request_duration_seconds",
		Help:    "HTTP request latency",
		Buckets: prometheus.ExponentialBuckets(0.005, 2, 12), // 5ms ~ 10s
	}, []string{"method", "route", "service"})

	ForgeAttempts = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "forja_forge_attempts_total",
		Help: "forge task terminal results (passed/escalated/failed)",
	}, []string{"result"})

	ForgeDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "forja_forge_task_duration_seconds",
		Help:    "full forge chain duration per attempt begin→terminal",
		Buckets: []float64{30, 60, 120, 180, 240, 300, 600}, // P99>3min 告警锚点
	})

	PipelineRunDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "forja_pipeline_run_duration_seconds",
		Help:    "pipeline run wall time at terminal aggregation",
		Buckets: []float64{60, 300, 600, 900, 1200, 1800, 2700},
	})

	QueueDepth = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "forja_asynq_queue_pending",
		Help: "asynq queue pending tasks (sampled)",
	}, []string{"queue"})

	QueueActive = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "forja_asynq_queue_active",
		Help: "asynq queue in-flight tasks (sampled)",
	}, []string{"queue"})

	StorageUploads = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "forja_storage_uploads_total",
		Help: "storage uploads by driver/scope",
	}, []string{"driver", "scope"})

	StorageBytes = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "forja_storage_bytes_total",
		Help: "storage uploaded bytes by driver/scope",
	}, []string{"driver", "scope"})
)

var serviceName = "api"

// SetService 常驻 service 标签（api / worker），须在中间件注册前调用。
func SetService(name string) { serviceName = name }

// FiberMiddleware 请求指标：路径用 c.Route().Path 模板（/tasks/:id），
// 未匹配路由统一记 "unmatched" 防扫描路径撑爆基数。
func FiberMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		start := time.Now()
		err := c.Next()

		route := c.Route().Path
		if route == "/" && c.Path() != "/" {
			route = "unmatched"
		}
		status := strconv.Itoa(c.Response().StatusCode())
		HTTPRequests.WithLabelValues(c.Method(), route, status, serviceName).Inc()
		HTTPDuration.WithLabelValues(c.Method(), route, serviceName).
			Observe(time.Since(start).Seconds())
		return err
	}
}

// MetricsHandler promhttp 经 fiber adaptor 桥接（fasthttp/net/http 接口差异）。
func MetricsHandler() fiber.Handler {
	return adaptor.HTTPHandler(promhttp.Handler())
}

// ObserveForgeTerminal 锻造任务终态：计数 + 全链路时长。
// duration 由 worker handler 记录 start 后传入；api 侧直调时传 0 只计数。
func ObserveForgeTerminal(result string, duration time.Duration) {
	ForgeAttempts.WithLabelValues(result).Inc()
	if duration > 0 {
		ForgeDuration.Observe(duration.Seconds())
	}
}

// ObservePipelineRun pipeline run 到达终态时的墙钟时长（秒）。
func ObservePipelineRun(durationSeconds float64) {
	PipelineRunDuration.Observe(durationSeconds)
}

// ObserveStorageUpload 存储上传计数与字节（S4-5 存储看板）。
func ObserveStorageUpload(driver, scope string, bytes int64) {
	StorageUploads.WithLabelValues(driver, scope).Inc()
	StorageBytes.WithLabelValues(driver, scope).Add(float64(bytes))
}

// StartQueueSampler worker 侧队列深度采样（30s）；Redis 断连时静默保上次值，
// 就绪探测已覆盖 Redis 硬依赖告警，此处不重复报警路径。
func StartQueueSampler(addr string) {
	if addr == "" {
		addr = "localhost:6379"
	}
	pwd := ""
	if v := os.Getenv("FORJA_REDIS_PASSWORD"); v != "" {
		pwd = v
	} else {
		pwd = os.Getenv("REDIS_PASSWORD")
	}
	insp := asynq.NewInspector(asynq.RedisClientOpt{Addr: addr, Password: pwd})
	go func() {
		for {
			for _, q := range []string{"default", "critical"} {
				info, err := insp.GetQueueInfo(q)
				if err != nil {
					// asynq 队列键惰性创建：从未入队的 critical 查不到属正常，
					// 置 0 静默（2026-09-07 前每 30s 一条 WARN 噪音）。
					if strings.Contains(err.Error(), "NOT_FOUND") {
						QueueDepth.WithLabelValues(q).Set(0)
						QueueActive.WithLabelValues(q).Set(0)
						continue
					}
					slog.Warn("queue sample failed", "queue", q, "err", err.Error())
					continue
				}
				QueueDepth.WithLabelValues(q).Set(float64(info.Pending))
				QueueActive.WithLabelValues(q).Set(float64(info.Active))
			}
			time.Sleep(30 * time.Second)
		}
	}()
}
