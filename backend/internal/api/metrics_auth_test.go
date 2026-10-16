package api

// M2 回归：/metrics 可选令牌校验（未配置放行 / 配置后 Bearer 或 X-Forja-Token）。

import (
	"net/http/httptest"
	"testing"

	"forja/backend/internal/api/respond"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMetricsAuthMiddleware(t *testing.T) {
	app := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	app.Get("/metrics", MetricsAuthMiddleware(), func(c *fiber.Ctx) error { return c.SendString("m") })

	t.Setenv("FORJA_METRICS_TOKEN", "")
	resp, err := app.Test(httptest.NewRequest("GET", "/metrics", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode, "unset token must stay open (internal-only by compose)")

	t.Setenv("FORJA_METRICS_TOKEN", "secret-metrics")
	resp, err = app.Test(httptest.NewRequest("GET", "/metrics", nil))
	require.NoError(t, err)
	assert.Equal(t, 401, resp.StatusCode)

	req := httptest.NewRequest("GET", "/metrics", nil)
	req.Header.Set("Authorization", "Bearer secret-metrics")
	resp, err = app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	req = httptest.NewRequest("GET", "/metrics", nil)
	req.Header.Set("X-Forja-Token", "secret-metrics")
	resp, err = app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	req = httptest.NewRequest("GET", "/metrics", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	resp, err = app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 401, resp.StatusCode)
}
