package api

import (
	"net/http/httptest"
	"testing"
	"time"

	"forja/backend/internal/api/respond"
	"forja/backend/internal/model"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPipelineScope_WhereFalse(t *testing.T) {
	newPipelineApp(t)
	if modelPoolNil() {
		t.Skip("DB not available")
	}
	// Viewer without user_id -> WHERE FALSE branch
	app := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("username", "anon")
		c.Locals("role", "Viewer")
		return c.Next()
	})
	api := app.Group("/api/v1")
	RegisterPipelineRoutes(api)
	req := httptest.NewRequest("GET", "/api/v1/pipelines", nil)
	resp, err := app.Test(req, 5000)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	_ = uuid.NewString
}

func TestHelpers_Coverage(t *testing.T) {
	assert.True(t, isZeroSHA(""))
	assert.True(t, isZeroSHA("0000"))
	assert.False(t, isZeroSHA("abc"))
	assert.False(t, isZeroSHA("0001"))
	assert.Nil(t, parseRFC3339(""))
	assert.Nil(t, parseRFC3339("bad"))
	now := time.Now().UTC().Format(time.RFC3339)
	assert.NotNil(t, parseRFC3339(now))
	assert.Equal(t, "workflow_dispatch", normalizeRunEvent("manual"))
	assert.Equal(t, "workflow_job", normalizeRunEvent(""))
	assert.Equal(t, "push", normalizeRunEvent("push"))
	assert.Equal(t, "RUNNING", normalizeRunStatus("unknown"))
	assert.Equal(t, "", shortSHA(""))
	assert.Equal(t, "abc", shortSHA("abc"))
}

func modelPoolNil() bool { return model.Pool == nil }

// isolateGitProxy 隔离宿主代理：指向不可达地址的用例（127.0.0.1:1）在宿主配置
// http_proxy/ALL_PROXY 时 git 会走代理挂起，击穿 app.Test 超时造成偶发失败
// （TestCovV21d 曾偶发一次，2026-09-17 定位）。
func isolateGitProxy(t *testing.T) {
	t.Helper()
	for _, k := range []string{"http_proxy", "https_proxy", "all_proxy", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY"} {
		t.Setenv(k, "")
	}
}
