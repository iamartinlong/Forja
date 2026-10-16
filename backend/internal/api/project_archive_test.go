package api

// S1.4 项目软删除测试（KI-002 决策 S1）：
// 归档 → 默认列表隐藏/归档列表可见 → 归档期写操作 409 PROJECT_ARCHIVED
// → 恢复 → purge（仅限已归档）→ 级联清除。
// 依赖运行中的 Postgres，无 DB 时跳过。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"

	"forja/backend/internal/api/respond"
	"forja/backend/internal/model"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupArchiveApp(t *testing.T) *fiber.App {
	t.Helper()
	ensureTestDB(t)
	app := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	// 使用真实存在的用户 id，避免 projects.owner_id 外键违约
	adminID := uuid.NewString()
	_ = model.Pool.QueryRow(context.Background(), `SELECT id FROM users WHERE username='admin'`).Scan(&adminID)
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("username", "arch-admin")
		c.Locals("role", "Admin")
		c.Locals("uid", adminID)
		return c.Next()
	})
	api := app.Group("/api/v1")
	RegisterProjectRoutes(api)
	RegisterSpecRoutes(api)
	return app
}

func archDo(t *testing.T, app *fiber.App, method, path, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, 30000)
	require.NoError(t, err)
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func listContains(t *testing.T, app *fiber.App, archived bool, id string) bool {
	t.Helper()
	q := "/api/v1/projects?archived=false&page_size=100"
	if archived {
		q = "/api/v1/projects?archived=true&page_size=100"
	}
	code, out := archDo(t, app, "GET", q, "")
	require.Equal(t, 200, code)
	data, _ := out["data"].(map[string]any)
	items, _ := data["items"].([]any)
	for _, it := range items {
		m, _ := it.(map[string]any)
		if m["id"] == id {
			return true
		}
	}
	return false
}

func TestArchive_ProjectLifecycle(t *testing.T) {
	app := setupArchiveApp(t)

	// 建项目
	name := "arch-" + uuid.NewString()[:8]
	code, out := archDo(t, app, "POST", "/api/v1/projects", fmt.Sprintf(`{"name":%q,"description":"s1.4"}`, name))
	require.Equal(t, 200, code, "创建项目失败: %v", out)
	data, _ := out["data"].(map[string]any)
	pid, _ := data["id"].(string)
	require.NotEmpty(t, pid)

	// 活跃列表可见、归档列表不可见
	assert.True(t, listContains(t, app, false, pid))
	assert.False(t, listContains(t, app, true, pid))

	// 未归档项目不允许 purge
	code, out = archDo(t, app, "DELETE", "/api/v1/projects/"+pid+"?purge=true", "")
	assert.Equal(t, 409, code)
	errObj, _ := out["error"].(map[string]any)
	errCode, _ := errObj["code"].(string)
	assert.Equal(t, "PROJECT_NOT_ARCHIVED", errCode)

	// 归档（DELETE 默认语义）
	code, out = archDo(t, app, "DELETE", "/api/v1/projects/"+pid, "")
	assert.Equal(t, 200, code, "%v", out)
	d, _ := out["data"].(map[string]any)
	assert.Equal(t, true, d["archived"])

	// 默认列表隐藏、归档列表可见；详情带 archived 标记
	assert.False(t, listContains(t, app, false, pid))
	assert.True(t, listContains(t, app, true, pid))
	code, out = archDo(t, app, "GET", "/api/v1/projects/"+pid, "")
	assert.Equal(t, 200, code)
	d, _ = out["data"].(map[string]any)
	assert.Equal(t, true, d["archived"])

	// 归档期间写操作被拒：建规格 409 ARCHIVED
	specBody := `{"project_id":"` + pid + `","layer":"BS","title":"t","content":{"story_id":"S-1"}}`
	code, out = archDo(t, app, "POST", "/api/v1/specs", specBody)
	assert.Equal(t, 409, code)
	errObj, _ = out["error"].(map[string]any)
	errCode, _ = errObj["code"].(string)
	assert.Equal(t, "PROJECT_ARCHIVED", errCode)

	// 归档期间项目更新被拒
	code, _ = archDo(t, app, "PATCH", "/api/v1/projects/"+pid, `{"description":"x"}`)
	assert.Equal(t, 409, code)

	// 恢复
	code, out = archDo(t, app, "POST", "/api/v1/projects/"+pid+"/restore", "")
	assert.Equal(t, 200, code, "%v", out)
	assert.True(t, listContains(t, app, false, pid))
	assert.False(t, listContains(t, app, true, pid))

	// 恢复后可正常写入
	code, out = archDo(t, app, "POST", "/api/v1/specs", specBody)
	assert.Equal(t, 200, code, "恢复后建规格应成功: %v", out)

	// 再次归档后 purge → 两列表均消失，详情 404
	_, _ = archDo(t, app, "DELETE", "/api/v1/projects/"+pid, "")
	code, out = archDo(t, app, "DELETE", "/api/v1/projects/"+pid+"?purge=true", "")
	assert.Equal(t, 200, code, "已归档项目应可 purge: %v", out)
	d, _ = out["data"].(map[string]any)
	assert.Equal(t, true, d["purged"])
	assert.False(t, listContains(t, app, false, pid))
	assert.False(t, listContains(t, app, true, pid))
	code, _ = archDo(t, app, "GET", "/api/v1/projects/"+pid, "")
	assert.Equal(t, 404, code)
}
