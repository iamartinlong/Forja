package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"forja/backend/internal/api/respond"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"forja/backend/internal/model"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/requestid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = model.InitDB(ctx)
	os.Exit(m.Run())
}

func requireDB(t *testing.T) {
	t.Helper()
	if model.Pool == nil {
		t.Skip("database unavailable")
	}
}

func TestGenerateToken(t *testing.T) {
	token, err := GenerateToken("alice", "Admin", "00000000-0000-0000-0000-00000000aaaa")
	require.NoError(t, err)
	assert.NotEmpty(t, token)
}

func TestLoginHandler_Success(t *testing.T) {
	requireDB(t)
	app := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	app.Use(requestid.New())
	app.Post("/login", LoginHandler)
	body, _ := json.Marshal(map[string]string{"username": "admin", "password": "admin"})
	req := httptest.NewRequest("POST", "/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, 5000)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	assert.Equal(t, float64(0), out["code"])
	data, _ := out["data"].(map[string]any)
	assert.Equal(t, "Admin", data["role"])
	assert.NotEmpty(t, out["trace_id"])
}

func TestLoginHandler_Invalid(t *testing.T) {
	requireDB(t)
	app := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	app.Post("/login", LoginHandler)
	body, _ := json.Marshal(map[string]string{"username": "admin", "password": "wrong"})
	req := httptest.NewRequest("POST", "/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := app.Test(req, 5000)
	assert.Equal(t, 401, resp.StatusCode)
}

func TestJWTMiddleware_Valid(t *testing.T) {
	token, _ := GenerateToken("bob", "Developer", "00000000-0000-0000-0000-00000000bbbb")
	app := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	app.Use(JWTMiddleware)
	app.Get("/protected", func(c *fiber.Ctx) error {
		role, _ := c.Locals("role").(string)
		uid, _ := c.Locals("uid").(string)
		return c.JSON(fiber.Map{"role": role, "uid": uid})
	})
	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, _ := app.Test(req, 5000)
	assert.Equal(t, 200, resp.StatusCode)
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	assert.Equal(t, "Developer", out["role"])
	assert.Equal(t, "00000000-0000-0000-0000-00000000bbbb", out["uid"])
}

func TestJWTMiddleware_NoToken_Unauthorized(t *testing.T) {
	app := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	app.Use(JWTMiddleware)
	app.Get("/protected", func(c *fiber.Ctx) error {
		return c.SendString("ok")
	})
	req := httptest.NewRequest("GET", "/protected", nil)
	resp, _ := app.Test(req, 5000)
	assert.Equal(t, 401, resp.StatusCode)
}

// 平台级动作：全局角色判定（A4 矩阵）
func TestHasPermission_Platform(t *testing.T) {
	// project.*
	assert.True(t, HasPermission("Admin", "project.delete"))
	assert.False(t, HasPermission("Architect", "project.delete"))
	assert.True(t, HasPermission("Architect", "project.create"))
	assert.False(t, HasPermission("Developer", "project.create"))
	// user.* 仅 Admin
	for _, r := range []string{"Architect", "Developer", "Viewer"} {
		assert.False(t, HasPermission(r, "user.list"), r)
		assert.False(t, HasPermission(r, "user.create"), r)
	}
	assert.True(t, HasPermission("Admin", "user.delete"))
	// 未知角色
	assert.False(t, HasPermission("Unknown", "project.list"))
}

// 项目级动作：Require 仅做认证门，任何合法角色通过，细粒度由 HasProjectPermission 判定
func TestHasPermission_ProjectAction_AuthGateOnly(t *testing.T) {
	assert.True(t, HasPermission("Viewer", "spec.read"))
	assert.True(t, HasPermission("Developer", "spec.validate")) // handler 内收紧为 Architect+
}

// 项目级矩阵（A4 定稿）
func TestHasProjectPermission(t *testing.T) {
	// 读：全员（成员内）
	for _, r := range []string{"Viewer", "Developer", "Architect", "Admin"} {
		assert.True(t, HasProjectPermission(r, "spec.read"), r)
		assert.True(t, HasProjectPermission(r, "task.read"), r)
	}
	// spec.validate 收紧 Architect+
	assert.True(t, HasProjectPermission("Architect", "spec.validate"))
	assert.True(t, HasProjectPermission("Admin", "spec.validate"))
	assert.False(t, HasProjectPermission("Developer", "spec.validate"))
	assert.False(t, HasProjectPermission("Viewer", "spec.validate"))
	// task.approve 保持 Developer+
	assert.True(t, HasProjectPermission("Developer", "task.approve"))
	assert.False(t, HasProjectPermission("Viewer", "task.approve"))
	// 写动作 Viewer 一律拒绝
	for _, a := range []string{"spec.write", "task.write", "inbox.write", "inbox.convert", "inbox.delete"} {
		assert.False(t, HasProjectPermission("Viewer", a), a)
		assert.True(t, HasProjectPermission("Developer", a), a)
	}
	// 别名兼容
	assert.True(t, HasProjectPermission("Developer", "spec.create"))
	assert.True(t, HasProjectPermission("Viewer", "task.list"))
}

func TestRequire_Forbidden(t *testing.T) {
	app := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("role", "Viewer")
		return c.Next()
	})
	app.Get("/admin", Require("user.list"), func(c *fiber.Ctx) error {
		return c.SendString("ok")
	})
	req := httptest.NewRequest("GET", "/admin", nil)
	resp, _ := app.Test(req, 5000)
	assert.Equal(t, 403, resp.StatusCode)
}

func TestRequire_Allowed(t *testing.T) {
	app := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("role", "Admin")
		return c.Next()
	})
	app.Get("/admin", Require("user.list"), func(c *fiber.Ctx) error {
		return c.SendString("ok")
	})
	req := httptest.NewRequest("GET", "/admin", nil)
	resp, _ := app.Test(req, 5000)
	assert.Equal(t, 200, resp.StatusCode)
}
