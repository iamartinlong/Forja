package api

// M4b 回归：资源库文件删除（DB 行 + FileStore best-effort）。

import (
	"context"
	"net/http/httptest"
	"testing"

	"forja/backend/internal/api/respond"
	"forja/backend/internal/model"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeleteProjectFile(t *testing.T) {
	ensureTestDB(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	app := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("username", "cov-admin")
		c.Locals("role", "Admin")
		c.Locals("uid", uuid.NewString())
		return c.Next()
	})
	api := app.Group("/api/v1")
	RegisterProjectFileRoutes(api)

	ctx := context.Background()
	pid := uuid.NewString()
	fid := uuid.NewString()
	_, err := model.Pool.Exec(ctx,
		`INSERT INTO projects (id,name,slug) VALUES ($1,$2,$2)`, pid, "pf-"+pid[:8])
	require.NoError(t, err)
	_, err = model.Pool.Exec(ctx,
		`INSERT INTO project_files (id, project_id, category, filename, storage_key, size, mime) VALUES ($1,$2,'general','doc.txt',$3,3,'text/plain')`,
		fid, pid, "project/"+pid+"/doc.txt")
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = model.Pool.Exec(ctx, `DELETE FROM project_files WHERE project_id=$1`, pid)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, pid)
	})

	resp, err := app.Test(httptest.NewRequest("DELETE", "/api/v1/projects/"+pid+"/files/"+fid, nil), 15000)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	var cnt int
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM project_files WHERE id=$1`, fid).Scan(&cnt))
	assert.Equal(t, 0, cnt, "行应已删除")

	// 重复删除 → 404
	resp, err = app.Test(httptest.NewRequest("DELETE", "/api/v1/projects/"+pid+"/files/"+fid, nil), 15000)
	require.NoError(t, err)
	assert.Equal(t, 404, resp.StatusCode)
}
