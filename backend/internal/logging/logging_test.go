package logging

// RequestLogger 单测：ErrResponded 哨兵剥离（审计缺陷 1）、*fiber.Error 状态兜底
// （缺陷 2）、分级（4xx Warn / 5xx Error）与 user 字段透出。

import (
	"errors"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"forja/backend/internal/api/respond"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/requestid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func captureLogs(t *testing.T, fn func()) string {
	t.Helper()
	var sb strings.Builder
	slog.SetDefault(slog.New(slog.NewJSONHandler(&sbWriter{&sb}, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(slog.New(slog.NewJSONHandler(&nopWriter{}, nil))) })
	fn()
	return sb.String()
}

type sbWriter struct{ b *strings.Builder }

func (w *sbWriter) Write(p []byte) (int, error) { return w.b.Write(p) }

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }

func newTestApp(handler fiber.Handler) *fiber.App {
	app := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	app.Use(requestid.New())
	app.Use(RequestLogger())
	app.Get("/ok", handler)
	return app
}

func TestRequestLogger_SentinelStripped(t *testing.T) {
	logs := captureLogs(t, func() {
		app := newTestApp(func(c *fiber.Ctx) error {
			c.Set("Content-Type", "application/json")
			return respond.OK(c, fiber.Map{"x": 1})
		})
		resp, err := app.Test(httptest.NewRequest("GET", "/ok", nil))
		require.NoError(t, err)
		assert.Equal(t, 200, resp.StatusCode)
	})
	assert.NotContains(t, logs, "response already written", "哨兵不得泄漏为日志 err 字段")
	assert.Contains(t, logs, `"status":200`)
	assert.Contains(t, logs, "http_request")
}

func TestRequestLogger_FiberErrorStatusFallback(t *testing.T) {
	// 未匹配路由：fiber 返回 *fiber.Error(404)，Response 尚未写——日志须兜底记 404
	logs := captureLogs(t, func() {
		app := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
		app.Use(requestid.New())
		app.Use(RequestLogger())
		resp, err := app.Test(httptest.NewRequest("GET", "/missing", nil))
		require.NoError(t, err)
		assert.Equal(t, 404, resp.StatusCode)
	})
	assert.Contains(t, logs, `"status":404`, "*fiber.Error 状态码应兜底")
	assert.Contains(t, logs, `"level":"WARN"`)
}

func TestRequestLogger_UserField(t *testing.T) {
	logs := captureLogs(t, func() {
		app := fiber.New()
		app.Use(requestid.New())
		app.Use(func(c *fiber.Ctx) error {
			c.Locals("username", "alice") // 模拟 JWT 中间件先于响应完成注入
			return c.Next()
		})
		app.Use(RequestLogger())
		app.Get("/who", func(c *fiber.Ctx) error { return c.JSON(fiber.Map{}) })
		_, _ = app.Test(httptest.NewRequest("GET", "/who", nil))
	})
	assert.Contains(t, logs, `"user":"alice"`)
}

func TestAsynqLogger_Levels(t *testing.T) {
	logs := captureLogs(t, func() {
		var l AsynqLogger
		l.Info("Starting processing")
		l.Debug("quiet detail")
		l.Error("boom", errors.New("x"))
	})
	assert.Contains(t, logs, `"level":"INFO","msg":"Starting processing"`)
	assert.Contains(t, logs, `"level":"DEBUG","msg":"quiet detail"`)
	assert.Contains(t, logs, `"level":"ERROR"`)
}
