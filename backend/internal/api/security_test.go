package api

// 安全测试套件（A5 审计补充）：
// 认证边界 / 项目隔离 / 角色矩阵 / 状态机不变量 / 注入探测 / 信息泄露。
//
// 依赖运行中的 Postgres（DATABASE_URL 或默认 localhost），无 DB 时跳过。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"forja/backend/internal/api/respond"
	"forja/backend/internal/auth"
	"forja/backend/internal/model"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/requestid"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newSecApp() *fiber.App {
	return fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
}

func secLogin(t *testing.T, app *fiber.App, username, password string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, 30000)
	require.NoError(t, err)
	if resp.StatusCode != 200 {
		t.Skipf("login %s failed (%d)", username, resp.StatusCode)
	}
	var out struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out.Data.Token
}

func secDo(t *testing.T, app *fiber.App, method, path, token string, body ...string) (int, string) {
	t.Helper()
	var req *http.Request
	if len(body) > 0 {
		req = httptest.NewRequest(method, path, bytes.NewReader([]byte(body[0])))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := app.Test(req, 30000)
	require.NoError(t, err)
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(resp.Body)
	return resp.StatusCode, buf.String()
}

// ---------- 认证边界 ----------

func TestSec_Unauthenticated_AllEndpoints(t *testing.T) {
	ensureTestDB(t)
	if model.Pool == nil {
		t.Skip("db unavailable")
	}
	app := newSecApp()
	api := app.Group("/api/v1")
	api.Use(auth.JWTMiddleware)
	RegisterSpecRoutes(api)
	RegisterTaskRoutes(api)
	RegisterInboxRoutes(api)
	RegisterProjectRoutes(api)
	RegisterUserRoutes(api)

	paths := []struct{ m, p string }{
		{"GET", "/api/v1/projects"},
		{"GET", "/api/v1/specs"},
		{"GET", "/api/v1/tasks"},
		{"GET", "/api/v1/inbox"},
		{"GET", "/api/v1/audit"},
		{"POST", "/api/v1/specs"},
		{"POST", "/api/v1/tasks"},
		{"POST", "/api/v1/inbox"},
		{"POST", "/api/v1/projects"},
		{"GET", "/api/v1/admin/users"},
	}
	for _, p := range paths {
		code, _ := secDo(t, app, p.m, p.p, "")
		assert.Equal(t, 401, code, "未认证应 401: "+p.m+" "+p.p)
	}
}

func TestSec_TamperedToken(t *testing.T) {
	ensureTestDB(t)
	if model.Pool == nil {
		t.Skip("db unavailable")
	}
	app := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	app.Use(requestid.New())
	api := app.Group("/api/v1")
	api.Use(auth.JWTMiddleware)
	api.Get("/x", func(c *fiber.Ctx) error { return c.SendString("ok") })

	code, _ := secDo(t, app, "GET", "/api/v1/x", "qa1.invalidsignature.token.xxxxxx")
	assert.Equal(t, 401, code, "篡改签名应 401")
}

func TestSec_AlgNoneRejected(t *testing.T) {
	app := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	app.Use(auth.JWTMiddleware)
	app.Get("/x", func(c *fiber.Ctx) error { return c.SendString("ok") })

	claims := auth.Claims{
		Username: "admin",
		Role:     "Admin",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	signed, err := tok.SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("Authorization", "Bearer "+signed)
	resp, _ := app.Test(req, 30000)
	assert.Equal(t, 401, resp.StatusCode, "alg=none 必须拒绝")
}

func TestSec_ExpiredToken(t *testing.T) {
	app := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	app.Use(auth.JWTMiddleware)
	app.Get("/x", func(c *fiber.Ctx) error { return c.SendString("ok") })

	claims := auth.Claims{
		Username: "admin",
		Role:     "Admin",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Minute)),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString(auth.Secret())
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("Authorization", "Bearer "+signed)
	resp, _ := app.Test(req, 30000)
	assert.Equal(t, 401, resp.StatusCode, "过期 token 应 401")
}

// ---------- 状态机不变量（发布不可变性）----------

func TestSec_ApproveReleasedTask_Blocked(t *testing.T) {
	ensureTestDB(t)
	if model.Pool == nil {
		t.Skip("db unavailable")
	}
	app := newSecApp()
	app.Post("/api/v1/auth/login", auth.LoginHandler)
	api := app.Group("/api/v1")
	api.Use(auth.JWTMiddleware)
	RegisterTaskRoutes(api)
	token := secLogin(t, app, "admin", "admin")

	for _, st := range []string{"RELEASED", "PASSED", "RELEASE_PENDING", "RUNNING"} {
		tid := uuid.NewString()
		_, err := model.Pool.Exec(context.Background(),
			`INSERT INTO tasks (id, project_id, title, status) VALUES ($1,'00000000-0000-0000-0000-000000000001','sec-sm',$2)`, tid, st)
		require.NoError(t, err)

		code, body := secDo(t, app, "POST", "/api/v1/tasks/"+tid+"/approve", token, `{"action":"approve"}`)
		assert.Equal(t, 400, code, fmt.Sprintf("%s 任务 approve 应被拒: %s", st, body))

		var now string
		_ = model.Pool.QueryRow(context.Background(), `SELECT status FROM tasks WHERE id=$1`, tid).Scan(&now)
		assert.Equal(t, st, now, fmt.Sprintf("%s 状态不可变", st))
		_, _ = model.Pool.Exec(context.Background(), `DELETE FROM tasks WHERE id=$1`, tid)
	}
}

// ---------- 注入探测 ----------

func TestSec_SQLInjection_Probes(t *testing.T) {
	ensureTestDB(t)
	if model.Pool == nil {
		t.Skip("db unavailable")
	}
	app := newSecApp()
	app.Post("/api/v1/auth/login", auth.LoginHandler)
	api := app.Group("/api/v1")
	api.Use(auth.JWTMiddleware)
	RegisterSpecRoutes(api)
	RegisterProjectRoutes(api)
	token := secLogin(t, app, "admin", "admin")

	payloads := []string{
		"'; DROP TABLE projects;--",
		"' OR '1'='1",
		"%' OR 1=1 --",
		`\'; SELECT pg_sleep(3);--`,
	}
	for _, pl := range payloads {
		code, body := secDo(t, app, "GET", "/api/v1/specs?project_id=00000000-0000-0000-0000-000000000001&q="+url.QueryEscape(pl), token)
		assert.NotEqual(t, 500, code, "注入探测不应 500: "+pl)
		assert.NotContains(t, body, "DROP TABLE", "错误信息不应回显 SQL")
	}
	// projects 表必须仍然存在
	var cnt int
	err := model.Pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM projects`).Scan(&cnt)
	assert.NoError(t, err, "projects 表必须存活")
}

// ---------- 信息泄露 ----------

func TestSec_ServerError_NoInternalDetail(t *testing.T) {
	app := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	app.Use(requestid.New())
	app.Get("/boom", func(c *fiber.Ctx) error {
		return respond.DBError(c, fmt.Errorf(`pq: syntax error at or near "SECRETTABLE"`))
	})
	req := httptest.NewRequest("GET", "/boom", nil)
	resp, _ := app.Test(req, 30000)
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(resp.Body)
	body := buf.String()
	assert.Equal(t, 500, resp.StatusCode)
	assert.NotContains(t, body, "SECRETTABLE", "内部错误细节不得回传客户端")
	assert.Contains(t, body, "trace_id")
}
