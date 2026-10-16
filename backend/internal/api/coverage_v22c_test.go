package api

// v2.2 R7 治理：审计导出（CSV/JSON + 自留痕）与成本分包（project/period + CSV + 成员范围）。

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"forja/backend/internal/api/respond"
	"forja/backend/internal/model"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCovV22c_AuditExport(t *testing.T) {
	app := covApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	pid := covV21Project(t, ctx, false)
	act := "cov22c.action." + uuid.NewString()[:6]
	// detail 含逗号+引号：验证 CSV 转义
	_, err := model.Pool.Exec(ctx, `INSERT INTO audit_logs (id, project_id, actor, action, target_type, detail)
		VALUES (gen_random_uuid(),$1,'cov-admin',$2,'task',$3::jsonb)`, pid, act, `{"k":"v,with,comma"}`)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM audit_logs WHERE action=$1`, act) })

	// CSV：含 BOM、表头、转义与筛选命中
	url := "/api/v1/audit/export?project_id=" + pid + "&action=" + act + "&format=csv"
	resp, err := app.Test(httptest.NewRequest("GET", url, nil), 15000)
	require.NoError(t, err)
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(resp.Body)
	body := buf.String()
	assert.Equal(t, 200, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/csv")
	assert.Contains(t, resp.Header.Get("Content-Disposition"), "attachment")
	assert.True(t, strings.HasPrefix(body, "\ufeff"), "CSV 需 UTF-8 BOM")
	assert.Contains(t, body, "时间,操作者,动作")
	assert.Contains(t, body, act)
	assert.Contains(t, body, `""k""`+"", "detail 含逗号应被引号包裹并转义")

	// JSON 形态
	code, out := covDo(t, app, "GET", "/api/v1/audit/export?project_id="+pid+"&action="+act+"&format=json", "")
	require.Equal(t, 200, code, "%v", out)
	d := covData(t, out)
	assert.Equal(t, float64(1), d["count"])

	// 导出自身留痕
	var selfAudit int
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM audit_logs WHERE project_id=$1 AND action='audit.export'`, pid).Scan(&selfAudit))
	assert.GreaterOrEqual(t, selfAudit, 1)

	// 参数校验
	for _, bad := range []string{
		"/api/v1/audit/export",
		"/api/v1/audit/export?project_id=" + pid + "&format=xml",
		"/api/v1/audit/export?project_id=" + pid + "&from=2026-13-99",
		"/api/v1/audit/export?project_id=" + pid + "&to=bad",
	} {
		code, _ = covDo(t, app, "GET", bad, "")
		assert.Equal(t, 400, code, "url: %s", bad)
	}
}

func TestCovV22c_CostReport(t *testing.T) {
	app := covV21bApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	pid := covV21Project(t, ctx, false)
	tid := uuid.NewString()
	_, err := model.Pool.Exec(ctx, `INSERT INTO tasks (id, project_id, title, status) VALUES ($1,$2,'cov22c cost','PENDING')`, tid, pid)
	require.NoError(t, err)
	_, err = model.Pool.Exec(ctx, `INSERT INTO agent_executions (id, task_id, agent_type, status, model, cost_cents, tokens_in, tokens_out, started_at, finished_at)
		VALUES (gen_random_uuid(),$1,'forge','SUCCESS','m1',120,1000,500, now()-interval '1 day', now()-interval '1 day'),
		       (gen_random_uuid(),$1,'forge','SUCCESS','m2',80,600,200, now(), now())`, tid)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = model.Pool.Exec(ctx, `DELETE FROM agent_executions WHERE task_id=$1`, tid)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM tasks WHERE id=$1`, tid)
	})

	// group=project：本项目成本 200 分
	code, out := covDo(t, app, "GET", "/api/v1/cost/report?group=project&project_id="+pid, "")
	require.Equal(t, 200, code, "%v", out)
	d := covData(t, out)
	items, _ := d["items"].([]any)
	found := false
	for _, it := range items {
		m, _ := it.(map[string]any)
		if m["key"] == pid {
			found = true
			assert.Equal(t, float64(200), m["cost_cents"])
			assert.Equal(t, float64(2), m["executions"])
		}
	}
	require.True(t, found, "应包含本项目分包行: %v", items)

	// group=period&period=day：两天各一行
	code, out = covDo(t, app, "GET", "/api/v1/cost/report?group=period&period=day&project_id="+pid, "")
	require.Equal(t, 200, code, "%v", out)
	items, _ = covData(t, out)["items"].([]any)
	assert.Len(t, items, 2)

	// CSV 导出
	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/cost/report?group=project&project_id="+pid+"&format=csv", nil), 15000)
	require.NoError(t, err)
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(resp.Body)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Contains(t, buf.String(), "项目ID,项目名,执行数,成本(分)")
	assert.Contains(t, buf.String(), pid)

	// 参数校验
	for _, bad := range []string{
		"/api/v1/cost/report?group=bogus",
		"/api/v1/cost/report?group=period&period=year",
		"/api/v1/cost/report?format=xml",
		"/api/v1/cost/report?project_id=bad-id",
		"/api/v1/cost/report?from=2026-99-01",
	} {
		code, _ = covDo(t, app, "GET", bad, "")
		assert.Equal(t, 400, code, "url: %s", bad)
	}
}

// 非管理员范围：仅成员项目可见（非成员 → 0 行）。
func TestCovV22c_CostReportScope(t *testing.T) {
	ensureTestDB(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	app := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("username", "cov-viewer")
		c.Locals("role", "Viewer")
		c.Locals("uid", uuid.NewString()) // 随机非成员
		return c.Next()
	})
	api := app.Group("/api/v1")
	RegisterCostRoutes(api)
	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/cost/report?group=project", nil), 15000)
	require.NoError(t, err)
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(resp.Body)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Contains(t, buf.String(), `"count":0`)
}
