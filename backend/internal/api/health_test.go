package api

// S4 #3 /ready 真探测试：硬依赖 up→200 / 任一 down→503+明细；embedding 仅报告不计分。

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"forja/backend/internal/model"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func probeReady(t *testing.T) (int, map[string]any) {
	t.Helper()
	app := fiber.New()
	app.Get("/ready", ReadinessHandler())
	resp, err := app.Test(httptest.NewRequest("GET", "/ready", nil), 15000)
	require.NoError(t, err)
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestReadiness_Up(t *testing.T) {
	newPipelineApp(t) // 初始化 model.Pool（本地 DB）
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	code, out := probeReady(t)
	assert.Equal(t, 200, code)
	assert.Equal(t, "ready", out["status"])
	checks := out["checks"].(map[string]any)
	assert.Equal(t, "up", checks["db"])
	assert.Equal(t, "up", checks["redis"])
}

func TestReadiness_DownOnDeadRedis(t *testing.T) {
	newPipelineApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	t.Setenv("REDIS_ADDR", "127.0.0.1:1") // 必然拒绝连接的端口
	code, out := probeReady(t)
	assert.Equal(t, 503, code)
	assert.Equal(t, "unavailable", out["status"])
	checks := out["checks"].(map[string]any)
	assert.Contains(t, checks["redis"], "down:")
	assert.Equal(t, "up", checks["db"]) // DB 正常不影响 redis 单项报错
}

func TestReadiness_DownOnNilPool(t *testing.T) {
	saved := model.Pool
	model.Pool = nil
	t.Cleanup(func() { model.Pool = saved })
	// 置空（走 localhost 默认分支）而非 Unsetenv：t.Setenv 测试结束自动恢复，
	// 否则进程级清掉 REDIS_ADDR 会毒化同包后续测试（run#190-192 FourProbes 503 根因）
	t.Setenv("REDIS_ADDR", "")

	app := fiber.New()
	app.Get("/ready", ReadinessHandler())
	resp, err := app.Test(httptest.NewRequest("GET", "/ready", nil), 15000)
	require.NoError(t, err)
	require.Equal(t, 503, resp.StatusCode)
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	checks := out["checks"].(map[string]any)
	assert.Contains(t, checks["db"], "pool not initialized")
}
