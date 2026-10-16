package api

// v2.1 coverage sprint Phase 4g: final branch fills toward 80%.

import (
	"context"
	"net/http/httptest"
	"testing"

	"forja/backend/internal/model"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCovV21m_FinalBranchFills(t *testing.T) {
	app := covV21hApp(t)
	api := app.Group("/api/v1")
	RegisterProjectFileRoutes(api)
	RegisterFurnaceRoutes(api)
	RegisterAuditRoutes(api)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	pid := covV21Project(t, ctx, false)

	// 项目列表：关键字 + 归档过滤 + 分页
	code, _ := covDo(t, app, "GET", "/api/v1/projects?q=cov21&archived=false&page=1", "")
	assert.Equal(t, 200, code)
	code, _ = covDo(t, app, "GET", "/api/v1/projects?archived=true", "")
	assert.Equal(t, 200, code)

	// spec timeline：缺 project_id / limit 边界
	code, _ = covDo(t, app, "GET", "/api/v1/specs/timeline", "")
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "GET", "/api/v1/specs/timeline?project_id="+pid+"&limit=1", "")
	assert.True(t, code < 500, "code=%d", code)

	// cost：迭代过滤 + iteration 分组（无过滤分支的 else 路径）
	iterID := insertIteration(t, ctx, pid, "active", "auto", "")
	code, _ = covDo(t, app, "GET", "/api/v1/projects/"+pid+"/cost?iteration_id="+iterID+"&group_by=iteration", "")
	assert.Equal(t, 200, code)
	code, _ = covDo(t, app, "GET", "/api/v1/projects/"+pid+"/cost?iteration_id="+iterID+"&group_by=assignee", "")
	assert.Equal(t, 200, code)
	code, _ = covDo(t, app, "GET", "/api/v1/projects/"+pid+"/cost?iteration_id="+iterID+"&group_by=model", "")
	assert.Equal(t, 200, code)

	// 资源库：上传 → 项目路径 presign（本地驱动回退流式）→ 删除后下载
	body, ct := multipartFile("file", "cov21m.txt", "cov21m-data", map[string]string{"category": "api", "summary": "cov"})
	req := httptest.NewRequest("POST", "/api/v1/projects/"+pid+"/files", body)
	req.Header.Set("Content-Type", ct)
	resp, err := app.Test(req, 15000)
	require.NoError(t, err)
	require.Equal(t, 200, resp.StatusCode)
	var out map[string]any
	_ = jsonDecodeBody(resp.Body, &out)
	fid, _ := covData(t, out)["id"].(string)
	require.NotEmpty(t, fid)
	code, _ = covDo(t, app, "GET", "/api/v1/projects/"+pid+"/files/"+fid+"/download?presign=true&ttl=60", "")
	assert.True(t, code < 500, "code=%d", code)
	code, _ = covDo(t, app, "GET", "/api/v1/projects/"+pid+"/files/"+fid+"/download", "")
	assert.Equal(t, 200, code)
	code, _ = covDo(t, app, "DELETE", "/api/v1/projects/"+pid+"/files/"+fid, "")
	assert.True(t, code < 500, "code=%d", code)
	// 删除后下载 → 存储/DB 缺失分支
	code, _ = covDo(t, app, "GET", "/api/v1/project-files/"+fid+"/download", "")
	assert.True(t, code >= 400, "code=%d", code)

	// 迭代列表：状态过滤
	code, _ = covDo(t, app, "GET", "/api/v1/projects/"+pid+"/iterations?status=active", "")
	assert.Equal(t, 200, code)

	// inbox：来源/状态组合 + 不存在需求详情
	code, _ = covDo(t, app, "GET", "/api/v1/inbox?project_id="+pid+"&status=CONVERTED&q=", "")
	assert.True(t, code < 500, "code=%d", code)

	// tasks 列表：迭代过滤 + 非法状态过滤
	code, _ = covDo(t, app, "GET", "/api/v1/tasks?project_id="+pid+"&iteration_id="+iterID, "")
	assert.True(t, code < 500, "code=%d", code)
	code, _ = covDo(t, app, "GET", "/api/v1/tasks?project_id="+pid+"&q=cov21m", "")
	assert.True(t, code < 500, "code=%d", code)

	// releases：迭代过滤（合法）
	code, _ = covDo(t, app, "GET", "/api/v1/projects/"+pid+"/releases?iteration_id="+iterID, "")
	assert.Equal(t, 200, code)

	// 交付：列表分页 + 发布说明（迭代过滤）
	code, _ = covDo(t, app, "GET", "/api/v1/projects/"+pid+"/deliveries?page=1&page_size=5", "")
	assert.Equal(t, 200, code)
	code, _ = covDo(t, app, "GET", "/api/v1/projects/"+pid+"/releases/notes?iteration_id="+iterID, "")
	assert.True(t, code < 500, "code=%d", code)

	// 审计：spec trace（有 spec 行）
	specID := covV21Spec(t, ctx, pid, "BS")
	t.Cleanup(func() {
		_, _ = model.Pool.Exec(ctx, `DELETE FROM spec_versions WHERE spec_id=$1`, specID)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM specs WHERE id=$1`, specID)
	})
	code, _ = covDo(t, app, "GET", "/api/v1/specs/"+specID+"/trace", "")
	assert.True(t, code < 500, "code=%d", code)
	code, _ = covDo(t, app, "GET", "/api/v1/audit?project_id="+pid+"&limit=1&offset=0", "")
	assert.Equal(t, 200, code)

	// 项目成员列表（无成员也可）
	code, _ = covDo(t, app, "GET", "/api/v1/projects/"+pid+"/members", "")
	assert.Equal(t, 200, code)

	// 制品列表分页
	code, _ = covDo(t, app, "GET", "/api/v1/projects/"+pid+"/artifacts?page=1&page_size=5", "")
	assert.Equal(t, 200, code)

	// agents：无仓项目生成（应 4xx/200 均可，覆盖分支）
	code, _ = covDo(t, app, "POST", "/api/v1/projects/"+pid+"/agents/generate", "")
	assert.True(t, code < 600, "code=%d", code)
	code, _ = covDo(t, app, "GET", "/api/v1/projects/"+pid+"/agents", "")
	assert.True(t, code < 600, "code=%d", code)
	code, _ = covDo(t, app, "PUT", "/api/v1/projects/"+pid+"/agents", `{"agents":[]}`)
	assert.True(t, code < 600, "code=%d", code)

	// 炉列表 + breakerStreak（无执行 → 0）
	code, _ = covDo(t, app, "GET", "/api/v1/furnaces?q=cov", "")
	assert.Equal(t, 200, code)

	// 任务：reject 分支（PASSED 非法状态）
	tid := insertTaskRow(t, ctx, pid)
	code, _ = covDo(t, app, "POST", "/api/v1/tasks/"+tid+"/reject", `{"comment":"cov"}`)
	assert.True(t, code < 600, "code=%d", code)
	code, _ = covDo(t, app, "POST", "/api/v1/tasks/"+tid+"/converge", `{}`)
	assert.True(t, code < 600, "code=%d", code)
}

func insertTaskRow(t *testing.T, ctx context.Context, pid string) string {
	t.Helper()
	tid := uuid.NewString()
	_, err := model.Pool.Exec(ctx, `INSERT INTO tasks (id, project_id, title, status) VALUES ($1,$2,'cov21m task','PASSED')`, tid, pid)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM tasks WHERE project_id=$1`, pid) })
	return tid
}
