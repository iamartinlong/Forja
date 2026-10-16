package ratelimit

// S1.1 限流单元测试：纯内存实现，不依赖 DB/Redis。
// 覆盖：滑动窗口放行/拒绝、键隔离、窗口重置、429 envelope 与 Retry-After 头。

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"forja/backend/internal/api/respond"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoryLimiter_AllowDeny(t *testing.T) {
	m := newMemoryLimiter(Options{Limit: 3, Window: time.Minute})
	for i := 0; i < 3; i++ {
		assert.True(t, m.Allow("k1"), "第 %d 次应放行", i+1)
	}
	assert.False(t, m.Allow("k1"), "超限应拒绝")
	assert.True(t, m.Allow("k2"), "不同键互不影响")
}

func TestMemoryLimiter_WindowReset(t *testing.T) {
	m := newMemoryLimiter(Options{Limit: 2, Window: 60 * time.Millisecond})
	require.True(t, m.Allow("k"))
	require.True(t, m.Allow("k"))
	assert.False(t, m.Allow("k"))
	time.Sleep(80 * time.Millisecond)
	assert.True(t, m.Allow("k"), "窗口滑出后应重新放行")
}

func newRateApp(limit int) *fiber.App {
	Use(newMemoryLimiter(Options{Limit: limit, Window: time.Minute}), time.Minute)
	return fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
}

func postJSON(t *testing.T, app *fiber.App, path, body string) (*http.Response, string) { //nolint:revive
	t.Helper()
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	require.NoError(t, err)
	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)
	return resp, string(buf[:n])
}

func TestLoginMiddleware_RateLimited_Envelope(t *testing.T) {
	app := newRateApp(2)
	app.Post("/login", LoginMiddleware, func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"ok": true})
	})

	for i := 0; i < 2; i++ {
		resp, _ := postJSON(t, app, "/login", `{"username":"alice","password":"pw"}`)
		assert.Equal(t, 200, resp.StatusCode, "前 %d 次应放行", i+1)
	}
	resp, body := postJSON(t, app, "/login", `{"username":"alice","password":"pw"}`)
	assert.Equal(t, 429, resp.StatusCode, "同用户超限应 429")
	assert.NotEmpty(t, resp.Header.Get("Retry-After"), "必须返回 Retry-After")

	var env struct {
		Code  int    `json:"code"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &env))
	assert.Equal(t, respond.CodeRateLimited, env.Code)
	assert.Equal(t, "RATE_LIMITED", env.Error.Code)

	_, _ = postJSON(t, app, "/login", `{"username":"bob","password":"pw"}`) // bob 独立计数，不触发即可
}

func TestLoginMiddleware_FallbackIPWhenNoUsername(t *testing.T) {
	app := newRateApp(1)
	app.Post("/login", LoginMiddleware, func(c *fiber.Ctx) error { return c.JSON(fiber.Map{}) })
	resp, _ := postJSON(t, app, "/login", `{"password":"pw"}`)
	assert.Equal(t, 200, resp.StatusCode)
	resp, _ = postJSON(t, app, "/login", `{}`)
	assert.Equal(t, 429, resp.StatusCode, "无 username 时按 IP 计数应命中同一桶")
}

func TestAuthedMiddleware_KeyByUID(t *testing.T) {
	app := newRateApp(1)
	setUID := func(c *fiber.Ctx) error {
		c.Locals("uid", c.Query("uid"))
		return c.Next()
	}
	app.Post("/x", setUID, AuthedMiddleware, func(c *fiber.Ctx) error { return c.JSON(fiber.Map{}) })

	resp, _ := postJSON(t, app, "/x?uid=u1", `{}`)
	assert.Equal(t, 200, resp.StatusCode)
	resp, _ = postJSON(t, app, "/x?uid=u1", `{}`)
	assert.Equal(t, 429, resp.StatusCode, "同 uid 第二次应限流")
	resp, _ = postJSON(t, app, "/x?uid=u2", `{}`)
	assert.Equal(t, 200, resp.StatusCode, "不同 uid 不受影响")
}
