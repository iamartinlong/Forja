package api

import (
	"bytes"
	"context"
	"encoding/json"
	"forja/backend/internal/api/respond"
	"forja/backend/internal/auth"
	"net/http/httptest"
	"testing"
	"time"

	"forja/backend/internal/model"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

func setupMoreApp(t *testing.T) *fiber.App {
	t.Helper()
	ensureTestDB(t)
	app := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("username", "test")
		c.Locals("role", "Admin")
		return c.Next()
	})
	api := app.Group("/api/v1")
	RegisterSpecRoutes(api)
	RegisterTaskRoutes(api)
	RegisterInboxRoutes(api)
	RegisterEstimateRoutes(api)
	RegisterAuditRoutes(api)
	return app
}

func TestMore_SpecLifecycle(t *testing.T) {
	app := setupMoreApp(t)
	// create BS
	body, _ := json.Marshal(map[string]any{"layer": "BS", "title": "more test", "project_id": "00000000-0000-0000-0000-000000000001", "content": map[string]any{"story_id": "S-1", "title": "t", "acceptance_criteria": []any{map[string]string{"given": "a", "when": "b", "then": "c"}}}})
	req := httptest.NewRequest("POST", "/api/v1/specs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, 5000)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	var created map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&created)
	createdData, _ := created["data"].(map[string]any)
	id, _ := createdData["id"].(string)
	require.NotEmpty(t, id)

	// get
	req2 := httptest.NewRequest("GET", "/api/v1/specs/"+id, nil)
	resp2, _ := app.Test(req2, 5000)
	assert.Equal(t, 200, resp2.StatusCode)

	// list
	req3 := httptest.NewRequest("GET", "/api/v1/specs?project_id=00000000-0000-0000-0000-000000000001", nil)
	resp3, _ := app.Test(req3, 5000)
	assert.Equal(t, 200, resp3.StatusCode)

	// validate
	req4 := httptest.NewRequest("POST", "/api/v1/specs/"+id+"/validate", nil)
	resp4, _ := app.Test(req4, 5000)
	assert.Equal(t, 200, resp4.StatusCode)

	// embed
	req5 := httptest.NewRequest("POST", "/api/v1/specs/"+id+"/embed", nil)
	resp5, _ := app.Test(req5, 5000)
	assert.Contains(t, []int{200, 500}, resp5.StatusCode)

	// ts-draft
	req6 := httptest.NewRequest("POST", "/api/v1/specs/"+id+"/ts-draft", nil)
	resp6, _ := app.Test(req6, 5000)
	assert.Equal(t, 200, resp6.StatusCode)

	// search
	req7 := httptest.NewRequest("GET", "/api/v1/specs/search?project_id=00000000-0000-0000-0000-000000000001&q=test", nil)
	resp7, _ := app.Test(req7, 5000)
	assert.Equal(t, 200, resp7.StatusCode)

	// cleanup
	_, _ = model.Pool.Exec(context.Background(), `DELETE FROM specs WHERE id=$1`, id)
}

func TestMore_TaskLifecycle(t *testing.T) {
	app := setupMoreApp(t)
	// create
	body, _ := json.Marshal(map[string]any{"title": "more task", "project_id": "00000000-0000-0000-0000-000000000001"})
	req := httptest.NewRequest("POST", "/api/v1/tasks", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := app.Test(req, 5000)
	assert.Equal(t, 200, resp.StatusCode)
	var created map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&created)
	createdData, _ := created["data"].(map[string]any)
	id, _ := createdData["id"].(string)
	require.NotEmpty(t, id)

	// get
	req2 := httptest.NewRequest("GET", "/api/v1/tasks/"+id, nil)
	resp2, _ := app.Test(req2, 5000)
	assert.Equal(t, 200, resp2.StatusCode)

	// list
	req3 := httptest.NewRequest("GET", "/api/v1/tasks?project_id=00000000-0000-0000-0000-000000000001", nil)
	resp3, _ := app.Test(req3, 5000)
	assert.Equal(t, 200, resp3.StatusCode)

	// dag
	req4 := httptest.NewRequest("GET", "/api/v1/projects/00000000-0000-0000-0000-000000000001/dag", nil)
	resp4, _ := app.Test(req4, 5000)
	assert.Equal(t, 200, resp4.StatusCode)

	// estimate
	body5, _ := json.Marshal(map[string]any{"title": "x", "project_id": "00000000-0000-0000-0000-000000000001"})
	req5 := httptest.NewRequest("POST", "/api/v1/tasks/estimate", bytes.NewReader(body5))
	req5.Header.Set("Content-Type", "application/json")
	resp5, _ := app.Test(req5, 5000)
	assert.Equal(t, 200, resp5.StatusCode)

	// patch
	body6, _ := json.Marshal(map[string]any{"priority": 5})
	req6 := httptest.NewRequest("PATCH", "/api/v1/tasks/"+id, bytes.NewReader(body6))
	req6.Header.Set("Content-Type", "application/json")
	resp6, _ := app.Test(req6, 5000)
	assert.Equal(t, 200, resp6.StatusCode)

	// stream：真 SSE 常驻流由 RegisterSSERoutes 提供（sse.go），此处不再断言单发响应

	// converge
	req8 := httptest.NewRequest("POST", "/api/v1/tasks/"+id+"/converge", nil)
	resp8, _ := app.Test(req8, 5000)
	assert.Equal(t, 200, resp8.StatusCode)

	// audit
	req9 := httptest.NewRequest("GET", "/api/v1/audit?project_id=00000000-0000-0000-0000-000000000001", nil)
	resp9, _ := app.Test(req9, 5000)
	assert.Equal(t, 200, resp9.StatusCode)

	// cleanup
	_, _ = model.Pool.Exec(context.Background(), `DELETE FROM tasks WHERE id=$1`, id)
	_ = uuid.NewString
}

func TestProjectID_Required(t *testing.T) {
	app := setupMoreApp(t)
	if model.Pool == nil {
		t.Skip("db unavailable")
	}
	// 列表端点缺 project_id -> 400 (A6)
	for _, path := range []string{"/api/v1/specs", "/api/v1/tasks", "/api/v1/inbox", "/api/v1/audit", "/api/v1/specs/search?q=x"} {
		req := httptest.NewRequest("GET", path, nil)
		resp, _ := app.Test(req, 5000)
		assert.Equal(t, 400, resp.StatusCode, path)
	}
	// 创建端点缺 project_id -> 400
	bodies := map[string][]byte{
		"/api/v1/tasks":          []byte(`{"title":"t"}`),
		"/api/v1/specs":          []byte(`{"layer":"BS","title":"t","content":{"story_id":"S"}}`),
		"/api/v1/tasks/estimate": []byte(`{"title":"t"}`),
		"/api/v1/inbox/convert":  []byte(`{"requirement_ids":["00000000-0000-0000-0000-00000000aaaa"]}`),
	}
	for path, b := range bodies {
		req := httptest.NewRequest("POST", path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		resp, _ := app.Test(req, 5000)
		assert.Equal(t, 400, resp.StatusCode, path)
	}
	// inbox 文本创建缺 project_id -> 400
	req2 := httptest.NewRequest("POST", "/api/v1/inbox", bytes.NewReader([]byte(`{"raw_content":"x","source_type":"TEXT_PASTE"}`)))
	req2.Header.Set("Content-Type", "application/json")
	resp2, _ := app.Test(req2, 5000)
	assert.Equal(t, 400, resp2.StatusCode)
}

func TestAuth_ChangePassword(t *testing.T) {
	ensureTestDB(t)
	if model.Pool == nil {
		t.Skip("db unavailable")
	}
	app := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	app.Post("/api/v1/auth/login", auth.LoginHandler)
	api := app.Group("/api/v1", auth.JWTMiddleware)
	RegisterUserRoutes(api)

	ctx := context.Background()
	// 专用测试用户，避免污染共享库 admin 密码
	uname := "cp-" + uuid.NewString()[:8]
	hash, _ := bcrypt.GenerateFromPassword([]byte("old-pass-1"), 10)
	uid := uuid.NewString()
	_, err := model.Pool.Exec(ctx, `INSERT INTO users (id, username, password_hash, role) VALUES ($1,$2,$3,'Viewer')`, uid, uname, string(hash))
	require.NoError(t, err)
	defer model.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, uname)

	login := func(pw string) int {
		b, _ := json.Marshal(map[string]string{"username": uname, "password": pw})
		req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		resp, _ := app.Test(req, 5000)
		return resp.StatusCode
	}
	require.Equal(t, 200, login("old-pass-1"))

	token, err2 := auth.GenerateToken(uname, "Viewer", uid)
	require.NoError(t, err2)
	authHdr := "Bearer " + token

	// 未认证 -> 401
	req := httptest.NewRequest("POST", "/api/v1/auth/change-password", bytes.NewReader([]byte(`{"old_password":"x","new_password":"y"}`)))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := app.Test(req, 5000)
	assert.Equal(t, 401, resp.StatusCode)

	// 旧密码错 -> 400
	b, _ := json.Marshal(map[string]string{"old_password": "wrong", "new_password": "new-pass-9"})
	req = httptest.NewRequest("POST", "/api/v1/auth/change-password", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHdr)
	resp, _ = app.Test(req, 5000)
	assert.Equal(t, 400, resp.StatusCode)

	// 正确修改 -> 200
	b, _ = json.Marshal(map[string]string{"old_password": "old-pass-1", "new_password": "new-pass-9"})
	req = httptest.NewRequest("POST", "/api/v1/auth/change-password", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHdr)
	resp, _ = app.Test(req, 5000)
	assert.Equal(t, 200, resp.StatusCode)

	// 新密码可登录、旧密码不可
	assert.Equal(t, 200, login("new-pass-9"))
	assert.Equal(t, 401, login("old-pass-1"))

	// 登录打点：last_login_at 已记录
	var ll *time.Time
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT last_login_at FROM users WHERE username=$1`, uname).Scan(&ll))
	assert.NotNil(t, ll, "last_login_at should be set after login")
}

func TestAuth_Me(t *testing.T) {
	ensureTestDB(t)
	if model.Pool == nil {
		t.Skip("db unavailable")
	}
	app := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	api := app.Group("/api/v1", auth.JWTMiddleware)
	RegisterUserRoutes(api)

	ctx := context.Background()
	uname := "me-" + uuid.NewString()[:8]
	uid := uuid.NewString()
	hash, _ := bcrypt.GenerateFromPassword([]byte("pw-123456"), 10)
	_, err := model.Pool.Exec(ctx, `INSERT INTO users (id, username, email, password_hash, role) VALUES ($1,$2,'me@test.io',$3,'QA')`, uid, uname, string(hash))
	require.NoError(t, err)
	defer model.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, uname)

	// 建两个项目并加入成员，验证 memberships 排序与角色映射
	// （先清同名残留：ON CONFLICT DO NOTHING 会让 pa/pb 成无行 uuid，随后成员表 FK 炸）
	pa := uuid.NewString()
	pb := uuid.NewString()
	_, _ = model.Pool.Exec(ctx, `DELETE FROM project_members WHERE project_id IN (SELECT id FROM projects WHERE name IN ('zz-me-test','aa-me-test'))`)
	_, _ = model.Pool.Exec(ctx, `DELETE FROM projects WHERE name IN ('zz-me-test','aa-me-test')`)
	_, err = model.Pool.Exec(ctx, `INSERT INTO projects (id, name) VALUES ($1,'zz-me-test'),($2,'aa-me-test')`, pa, pb)
	require.NoError(t, err)
	defer model.Pool.Exec(ctx, `DELETE FROM projects WHERE id IN ($1,$2)`, pa, pb)
	defer model.Pool.Exec(ctx, `DELETE FROM project_members WHERE user_id=$1`, uid)
	_, err = model.Pool.Exec(ctx, `INSERT INTO project_members (project_id, user_id, username, role) VALUES ($1,$2,$3,'PM'),($4,$5,$6,'Viewer')`, pa, uid, uname, pb, uid, uname)
	require.NoError(t, err)

	token, err := auth.GenerateToken(uname, "QA", uid)
	require.NoError(t, err)
	req := httptest.NewRequest("GET", "/api/v1/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, _ := app.Test(req, 5000)
	assert.Equal(t, 200, resp.StatusCode)

	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	data, _ := out["data"].(map[string]any)
	assert.Equal(t, uname, data["username"])
	assert.Equal(t, "QA", data["global_role"])
	assert.Equal(t, "me@test.io", data["email"])
	mem, _ := data["memberships"].([]any)
	require.Len(t, mem, 2)
	first, _ := mem[0].(map[string]any)
	second, _ := mem[1].(map[string]any)
	// 按项目名排序：aa 在前
	assert.Equal(t, "aa-me-test", first["project_name"])
	assert.Equal(t, "Viewer", first["role"])
	assert.Equal(t, "zz-me-test", second["project_name"])
	assert.Equal(t, "PM", second["role"])

	// 未认证 -> 401
	req2 := httptest.NewRequest("GET", "/api/v1/auth/me", nil)
	resp2, _ := app.Test(req2, 5000)
	assert.Equal(t, 401, resp2.StatusCode)
}
