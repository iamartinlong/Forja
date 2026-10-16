package api

// M2 生产暴露收口（v2.0 F7）：/metrics 访问控制。
// 设 FORJA_METRICS_TOKEN 时强制令牌（Prometheus credentials_file / X-Forja-Token）；
// 未设置时放行——端口收口后仅 compose 内网可达（宿主仅 web:80 与 api:18080 运维口）。

import (
	"crypto/subtle"
	"os"
	"strings"

	"forja/backend/internal/api/respond"

	"github.com/gofiber/fiber/v2"
)

// MetricsAuthMiddleware 可选令牌校验；constant-time 比较。
func MetricsAuthMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		want := strings.TrimSpace(os.Getenv("FORJA_METRICS_TOKEN"))
		if want == "" {
			return c.Next()
		}
		got := strings.TrimSpace(c.Get("X-Forja-Token"))
		if got == "" {
			if auth := strings.TrimSpace(c.Get("Authorization")); len(auth) > 7 && strings.EqualFold(auth[:7], "Bearer ") {
				got = strings.TrimSpace(auth[7:])
			}
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			return respond.Unauthorized(c, "invalid metrics token")
		}
		return c.Next()
	}
}
