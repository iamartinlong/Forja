package api

// v2.2 T2/T3：模板参数化（options 校验/透出）+ UAT 一键回炉（幂等）用例。

import (
	"context"
	"strings"
	"testing"

	"forja/backend/internal/model"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCovV22_TemplateOptions(t *testing.T) {
	app := covV21hApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()

	// 模板列表透出 options（python 模板声明 3 项）
	code, out := covDo(t, app, "GET", "/api/v1/project-templates", "")
	require.Equal(t, 200, code)
	items, _ := covData(t, out)["items"].([]any)
	var pyOpts []any
	for _, it := range items {
		m, _ := it.(map[string]any)
		if m["id"] == "standard-python-api" {
			pyOpts, _ = m["options"].([]any)
		}
	}
	assert.GreaterOrEqual(t, len(pyOpts), 3, "python 模板应透出 options 问卷")

	// 未知模板 / 未知参数 key / 非法枚举 / 非法布尔 → 400
	code, _ = covDo(t, app, "POST", "/api/v1/projects", `{"mode":"create","name":"cov22-a","template":"no-such-tpl"}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "POST", "/api/v1/projects",
		`{"mode":"create","name":"cov22-b","template":"standard-python-api","options":{"nope":"1"}}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "POST", "/api/v1/projects",
		`{"mode":"create","name":"cov22-c","template":"standard-python-api","options":{"health_path":"boom"}}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "POST", "/api/v1/projects",
		`{"mode":"create","name":"cov22-d","template":"standard-python-api","options":{"disable_redoc":"yes"}}`)
	assert.Equal(t, 400, code)

	// 合法建项（非默认参数；Gitea 不可达时仍应落库返回 200）
	code, out = covDo(t, app, "POST", "/api/v1/projects",
		`{"mode":"create","name":"cov22-opt-`+uuid.NewString()[:8]+`","template":"standard-python-api","options":{"python_version":"3.11","health_path":"healthz","disable_redoc":"false"}}`)
	require.Equal(t, 200, code, "%v", out)
	pid, _ := covData(t, out)["id"].(string)
	require.NotEmpty(t, pid)
	assert.Equal(t, "standard-python-api", covData(t, out)["template"])
	t.Cleanup(func() {
		_, _ = model.Pool.Exec(ctx, `DELETE FROM project_members WHERE project_id=$1`, pid)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, pid)
	})
}

func TestCovV22_InboxReforge(t *testing.T) {
	app := covV21hApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	pid := covV21Project(t, ctx, false)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM tasks WHERE project_id=$1`, pid) })

	uatID := uuid.NewString()
	raw := "【UAT｜不通过｜v1.0.0-py｜delivery:abcd1234】\n客户验收不通过\n环境: staging\n\n版本接口返回缺少 python 字段"
	_, err := model.Pool.Exec(ctx,
		`INSERT INTO requirements_inbox (id, project_id, source_type, raw_content, status) VALUES ($1,$2,'UAT',$3,'NEW')`,
		uatID, pid, raw)
	require.NoError(t, err)

	// 参数校验：非法 id / 不存在 / 非 UAT
	code, _ := covDo(t, app, "POST", "/api/v1/inbox/bad-id/reforge", "")
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "POST", "/api/v1/inbox/"+uuid.NewString()+"/reforge", "")
	assert.Equal(t, 404, code)
	otherID := uuid.NewString()
	_, err = model.Pool.Exec(ctx,
		`INSERT INTO requirements_inbox (id, project_id, source_type, raw_content, status) VALUES ($1,$2,'TEXT_PASTE','x','NEW')`,
		otherID, pid)
	require.NoError(t, err)
	code, _ = covDo(t, app, "POST", "/api/v1/inbox/"+otherID+"/reforge", "")
	assert.Equal(t, 400, code)

	// 首次回炉：生成 PENDING 任务 + 上下文注入 + 幂等标记
	code, out := covDo(t, app, "POST", "/api/v1/inbox/"+uatID+"/reforge", "")
	require.Equal(t, 200, code, "%v", out)
	tid, _ := covData(t, out)["id"].(string)
	require.NotEmpty(t, tid)
	assert.Equal(t, false, covData(t, out)["dedup"])
	assert.Equal(t, "PENDING", covData(t, out)["status"])
	var title, description, status string
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT title, description, status FROM tasks WHERE id=$1`, tid).Scan(&title, &description, &status))
	assert.Equal(t, "PENDING", status)
	assert.True(t, strings.HasPrefix(title, "[UAT 修复] "), "title=%s", title)
	assert.Contains(t, description, "【UAT｜不通过")
	assert.Contains(t, description, "[UAT-REF:"+uatID+"]")

	// 重复回炉：幂等返回既有任务，不重复建单
	code, out = covDo(t, app, "POST", "/api/v1/inbox/"+uatID+"/reforge", "")
	require.Equal(t, 200, code, "%v", out)
	assert.Equal(t, true, covData(t, out)["dedup"])
	assert.Equal(t, tid, covData(t, out)["id"])
	var cnt int
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM tasks WHERE project_id=$1 AND description LIKE '%'||$2||'%'`, pid, "[UAT-REF:"+uatID+"]").Scan(&cnt))
	assert.Equal(t, 1, cnt)
}
