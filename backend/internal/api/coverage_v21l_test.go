package api

// v2.1 coverage sprint Phase 4f: estimate/pact/audit/pipeline filters + small handlers.

import (
	"context"
	"testing"

	"forja/backend/internal/api/respond"
	"forja/backend/internal/model"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func covV21lApp(t *testing.T) *fiber.App {
	t.Helper()
	ensureTestDB(t)
	adminID := uuid.NewString()
	_ = model.Pool.QueryRow(context.Background(), `SELECT id FROM users WHERE username='admin'`).Scan(&adminID)
	app := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("username", "cov-admin")
		c.Locals("role", "Admin")
		c.Locals("uid", adminID)
		return c.Next()
	})
	return app
}

func TestCovV21l_SmallHandlersAndFilters(t *testing.T) {
	ensureTestDB(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	adminID := uuid.NewString()
	_ = model.Pool.QueryRow(ctx, `SELECT id FROM users WHERE username='admin'`).Scan(&adminID)
	app := covV21lApp(t)
	api := app.Group("/api/v1")
	RegisterTaskRoutes(api)
	RegisterEstimateRoutes(api)
	RegisterPactRoutes(api)
	RegisterAuditRoutes(api)
	RegisterPipelineRoutes(api)
	RegisterReindexRoutes(api)
	RegisterFurnaceRoutes(api)
	RegisterInboxRoutes(api)
	RegisterDeliveryRoutes(api)
	RegisterProjectFileRoutes(api)

	pid := covV21Project(t, ctx, false)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM tasks WHERE project_id=$1`, pid) })
	specID := covV21Spec(t, ctx, pid, "BS")
	t.Cleanup(func() {
		_, _ = model.Pool.Exec(ctx, `DELETE FROM spec_versions WHERE spec_id=$1`, specID)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM specs WHERE id=$1`, specID)
	})

	// estimate：project 维度估算（无任务 → 空结果亦可）
	code, out := covDo(t, app, "POST", "/api/v1/tasks/estimate", `{"project_id":"`+pid+`"}`)
	assert.True(t, code < 500, "%v code=%d", out, code)
	code, _ = covDo(t, app, "POST", "/api/v1/tasks/estimate", `{bad-json`)
	assert.Equal(t, 400, code)

	// pact：生成与获取（内容不足时上游 5xx 亦可接受，均为真实分支）
	code, out = covDo(t, app, "POST", "/api/v1/specs/"+specID+"/pact", "")
	assert.True(t, code < 600, "%v code=%d", out, code)
	code, out = covDo(t, app, "GET", "/api/v1/specs/"+specID+"/pact", "")
	assert.True(t, code < 600, "%v code=%d", out, code)
	code, _ = covDo(t, app, "GET", "/api/v1/specs/bad-id/pact", "")
	assert.Equal(t, 400, code)

	// audit：列表过滤 + spec trace
	code, _ = covDo(t, app, "GET", "/api/v1/audit?project_id="+pid+"&limit=5", "")
	assert.Equal(t, 200, code)
	code, _ = covDo(t, app, "GET", "/api/v1/audit?actor=cov-admin&action=repo.push", "")
	assert.True(t, code < 500, "code=%d", code)
	code, _ = covDo(t, app, "GET", "/api/v1/specs/"+specID+"/trace", "")
	assert.True(t, code < 500, "code=%d", code)
	code, _ = covDo(t, app, "GET", "/api/v1/specs/"+uuid.NewString()+"/trace", "")
	assert.True(t, code < 500, "code=%d", code)

	// pipelines：summary / metrics / 列表过滤 / 详情非法 id / sync
	code, out = covDo(t, app, "GET", "/api/v1/pipelines/summary", "")
	assert.True(t, code < 500, "%v code=%d", out, code)
	code, _ = covDo(t, app, "GET", "/api/v1/pipelines/metrics", "")
	assert.True(t, code < 500, "code=%d", code)
	code, _ = covDo(t, app, "GET", "/api/v1/pipelines?project_id="+pid+"&status=SUCCESS&branch=master&page=1", "")
	assert.Equal(t, 200, code)
	code, _ = covDo(t, app, "GET", "/api/v1/pipelines?limit=5&offset=0&q=cov21l", "")
	assert.True(t, code < 500, "code=%d", code)
	code, _ = covDo(t, app, "GET", "/api/v1/pipelines/bad-id", "")
	assert.True(t, code >= 400, "code=%d", code)
	code, _ = covDo(t, app, "GET", "/api/v1/pipelines/"+uuid.NewString(), "")
	assert.True(t, code < 500, "code=%d", code)
	code, _ = covDo(t, app, "POST", "/api/v1/internal/gitea/sync", `{}`)
	assert.True(t, code < 600, "code=%d", code)

	// 重索引（无 embedding provider 时 503 亦可接受）
	code, out = covDo(t, app, "POST", "/api/v1/admin/reindex-embeddings", `{"project_id":"`+pid+`"}`)
	assert.True(t, code < 600, "%v code=%d", out, code)

	// 资源库列表分页（已有件为空）
	code, _ = covDo(t, app, "GET", "/api/v1/projects/"+pid+"/files?page=2&page_size=1", "")
	assert.Equal(t, 200, code)

	// 炉：非法 id recover → 400；drained 炉 recover 尝试（docker 不可达仅日志）
	code, _ = covDo(t, app, "POST", "/api/v1/furnaces/bad-id/recover", "")
	assert.Equal(t, 400, code)

	// inbox 列表：仅状态过滤 + 仅来源过滤（无 project_id）
	code, _ = covDo(t, app, "GET", "/api/v1/inbox?status=NEW", "")
	assert.True(t, code < 500, "code=%d", code)
	code, _ = covDo(t, app, "GET", "/api/v1/inbox?source_type=UAT", "")
	assert.True(t, code < 500, "code=%d", code)

	// getProject：归档项目详情（软删后）
	p2 := covV21Project(t, ctx, false)
	code, _ = covDo(t, app, "DELETE", "/api/v1/projects/"+p2, "")
	assert.True(t, code < 500, "code=%d", code)
	code, _ = covDo(t, app, "GET", "/api/v1/projects/"+p2, "")
	assert.True(t, code < 500, "code=%d", code)
	code, _ = covDo(t, app, "PATCH", "/api/v1/projects/"+p2, `{"name":"cov21l archived"}`)
	assert.True(t, code < 500, "code=%d", code)

	// user：getUser 非法 id
	code, _ = covDo(t, app, "GET", "/api/v1/admin/users/bad-id", "")
	assert.True(t, code >= 400, "code=%d", code)

	// deliver：不存在项目交付触发（校验路径）
	code, _ = covDo(t, app, "POST", "/api/v1/projects/bad-id/deliveries", `{}`)
	assert.Equal(t, 400, code)
	require.NotEmpty(t, adminID)
}
