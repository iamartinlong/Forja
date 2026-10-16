package api

// S1.2 Refresh Token 全流程测试（依赖 Postgres，无 DB 跳过）：
// 登录签发 → 刷新轮换 → 旧 token 重放拒绝 → 篡改拒绝 →
// 禁用用户刷新被拒 → 改密全量吊销 → 登出吊销。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"

	"forja/backend/internal/api/respond"
	"forja/backend/internal/auth"
	"forja/backend/internal/model"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

func newRefreshApp() *fiber.App {
	app := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	app.Post("/api/v1/auth/login", auth.LoginHandler)
	app.Post("/api/v1/auth/refresh", auth.RefreshHandler)
	app.Post("/api/v1/auth/logout", auth.LogoutHandler)
	api := app.Group("/api/v1", auth.JWTMiddleware)
	RegisterUserRoutes(api)
	api.Get("/probe", func(c *fiber.Ctx) error { return c.JSON(fiber.Map{"ok": true}) })
	return app
}

func refreshDo(t *testing.T, app *fiber.App, method, path, token, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := app.Test(req, 30000)
	require.NoError(t, err)
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func dataOf(t *testing.T, out map[string]any) map[string]any {
	t.Helper()
	d, _ := out["data"].(map[string]any)
	require.NotNil(t, d, "envelope data 缺失: %v", out)
	return d
}

func TestRefresh_FullFlow(t *testing.T) {
	ensureTestDB(t)
	if model.Pool == nil {
		t.Skip("db unavailable")
	}
	app := newRefreshApp()
	ctx := context.Background()

	// 专用用户，避免污染共享账号
	uname := "rf-" + uuid.NewString()[:8]
	hash, _ := bcrypt.GenerateFromPassword([]byte("old-pass-1"), 10)
	uid := uuid.NewString()
	_, err := model.Pool.Exec(ctx, `INSERT INTO users (id, username, password_hash, role) VALUES ($1,$2,$3,'Viewer')`, uid, uname, string(hash))
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, uid) })

	// 1. 登录 → 拿到 access + refresh
	code, out := refreshDo(t, app, "POST", "/api/v1/auth/login", "", fmt.Sprintf(`{"username":%q,"password":"old-pass-1"}`, uname))
	require.Equal(t, 200, code, "%v", out)
	loginData := dataOf(t, out)
	rt1, _ := loginData["refresh_token"].(string)
	at1, _ := loginData["token"].(string)
	require.NotEmpty(t, rt1, "登录必须下发 refresh_token")
	assert.NotEmpty(t, at1)
	assert.InDelta(t, float64(auth.AccessTokenTTL().Seconds()), loginData["expires_in"], 1)

	// access token 可访问受保护端点
	code, _ = refreshDo(t, app, "GET", "/api/v1/probe", at1, "")
	assert.Equal(t, 200, code)

	// 2. 刷新 → 新对；旧 refresh 重放必须失败（轮换语义）
	code, out = refreshDo(t, app, "POST", "/api/v1/auth/refresh", "", fmt.Sprintf(`{"refresh_token":%q}`, rt1))
	require.Equal(t, 200, code, "首次刷新应成功: %v", out)
	rd := dataOf(t, out)
	rt2, _ := rd["refresh_token"].(string)
	at2, _ := rd["token"].(string)
	require.NotEmpty(t, rt2)
	assert.NotEqual(t, rt1, rt2, "刷新必须轮换出新 token")
	assert.Equal(t, uname, rd["username"])

	// 新 access 可用、旧 refresh 重放 401
	code, _ = refreshDo(t, app, "GET", "/api/v1/probe", at2, "")
	assert.Equal(t, 200, code)
	code, _ = refreshDo(t, app, "POST", "/api/v1/auth/refresh", "", fmt.Sprintf(`{"refresh_token":%q}`, rt1))
	assert.Equal(t, 401, code, "旧 refresh token 重放必须 401")

	// 3. 篡改的 raw token 401
	bad := rt2[:len(rt2)-4] + "AAAA"
	code, _ = refreshDo(t, app, "POST", "/api/v1/auth/refresh", "", fmt.Sprintf(`{"refresh_token":%q}`, bad))
	assert.Equal(t, 401, code)

	// 4. 禁用用户 → 刷新立即失效（即使 refresh token 本身未过期）
	_, err = model.Pool.Exec(ctx, `UPDATE users SET enabled=false WHERE id=$1`, uid)
	require.NoError(t, err)
	code, _ = refreshDo(t, app, "POST", "/api/v1/auth/refresh", "", fmt.Sprintf(`{"refresh_token":%q}`, rt2))
	assert.Equal(t, 401, code, "禁用用户的 refresh 必须被拒")

	// 重新启用后：rt2 已在禁用分支被全量吊销，仍应 401
	_, _ = model.Pool.Exec(ctx, `UPDATE users SET enabled=true WHERE id=$1`, uid)
	code, _ = refreshDo(t, app, "POST", "/api/v1/auth/refresh", "", fmt.Sprintf(`{"refresh_token":%q}`, rt2))
	assert.Equal(t, 401, code, "已吊销的 refresh 即使重新启用也必须 401")

	// 5. 重新登录 → 改密 → 全量吊销
	code, out = refreshDo(t, app, "POST", "/api/v1/auth/login", "", fmt.Sprintf(`{"username":%q,"password":"old-pass-1"}`, uname))
	require.Equal(t, 200, code)
	rt3 := dataOf(t, out)["refresh_token"].(string)
	newTok := dataOf(t, out)["token"].(string)

	// 自助改密（新密码符合 S1.3 策略）
	code, _ = refreshDo(t, app, "POST", "/api/v1/auth/change-password", newTok,
		`{"old_password":"old-pass-1","new_password":"new-pass-9"}`)
	require.Equal(t, 200, code)

	code, _ = refreshDo(t, app, "POST", "/api/v1/auth/refresh", "", fmt.Sprintf(`{"refresh_token":%q}`, rt3))
	assert.Equal(t, 401, code, "改密后 refresh 必须全量吊销")

	// 6. 再次登录 → 登出吊销 → 刷新 401
	code, out = refreshDo(t, app, "POST", "/api/v1/auth/login", "", fmt.Sprintf(`{"username":%q,"password":"new-pass-9"}`, uname))
	require.Equal(t, 200, code, "新密码可登录: %v", out)
	rt4 := dataOf(t, out)["refresh_token"].(string)
	code, _ = refreshDo(t, app, "POST", "/api/v1/auth/logout", "", fmt.Sprintf(`{"refresh_token":%q}`, rt4))
	assert.Equal(t, 200, code)
	code, _ = refreshDo(t, app, "POST", "/api/v1/auth/refresh", "", fmt.Sprintf(`{"refresh_token":%q}`, rt4))
	assert.Equal(t, 401, code, "登出后的 refresh 必须失效")
}

func TestRefresh_UnknownToken(t *testing.T) {
	ensureTestDB(t)
	if model.Pool == nil {
		t.Skip("db unavailable")
	}
	app := newRefreshApp()
	raw, err := auth.GenerateRawToken()
	require.NoError(t, err)
	code, _ := refreshDo(t, app, "POST", "/api/v1/auth/refresh", "", fmt.Sprintf(`{"refresh_token":%q}`, raw))
	assert.Equal(t, 401, code)
	code, _ = refreshDo(t, app, "POST", "/api/v1/auth/refresh", "", `{}`)
	assert.Equal(t, 400, code, "缺参应 400")
}
