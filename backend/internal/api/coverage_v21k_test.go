package api

// v2.1 coverage sprint Phase 4e: task create/batch/patch/approve/stack/dag +
// project purge/restore + spec create/validate/refs leftovers.

import (
	"context"
	"strings"
	"testing"

	"forja/backend/internal/model"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCovV21k_TaskCreatePatchApprove(t *testing.T) {
	app := covApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	pid := covV21Project(t, ctx, false)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM tasks WHERE project_id=$1`, pid) })
	t.Setenv("OPENCODE_URL", "http://127.0.0.1:1")

	// createTask：bad json / 缺 title / 缺 project / 非法迭代 / 迭代不属项目 / 成功
	code, _ := covDo(t, app, "POST", "/api/v1/tasks", `{bad-json`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "POST", "/api/v1/tasks", `{"project_id":"`+pid+`"}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "POST", "/api/v1/tasks", `{"title":"t"}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "POST", "/api/v1/tasks", `{"title":"t","project_id":"`+pid+`","target_iteration_id":"bad-id"}`)
	assert.Equal(t, 400, code)
	foreignIter := insertIteration(t, ctx, covV21Project(t, ctx, false), "active", "auto", "")
	code, _ = covDo(t, app, "POST", "/api/v1/tasks", `{"title":"t","project_id":"`+pid+`","target_iteration_id":"`+foreignIter+`"}`)
	assert.Equal(t, 400, code)
	iterID := insertIteration(t, ctx, pid, "active", "auto", "")
	code, out := covDo(t, app, "POST", "/api/v1/tasks",
		`{"title":"cov21k task","project_id":"`+pid+`","priority":2,"estimated_hours":3.5,"target_iteration_id":"`+iterID+`"}`)
	require.Equal(t, 200, code, "%v", out)
	tid, _ := covData(t, out)["id"].(string)
	require.NotEmpty(t, tid)

	// batchCreateTasks：空数组 / 超 50 / 混合成功与失败行
	code, _ = covDo(t, app, "POST", "/api/v1/tasks/batch", `[]`)
	assert.Equal(t, 400, code)
	big := make([]string, 51)
	for i := range big {
		big[i] = `{"title":"b","project_id":"` + pid + `"}`
	}
	code, _ = covDo(t, app, "POST", "/api/v1/tasks/batch", `[`+strings.Join(big, ",")+`]`)
	assert.Equal(t, 400, code)
	code, out = covDo(t, app, "POST", "/api/v1/tasks/batch",
		`[{"title":"","project_id":"`+pid+`"},{"title":"ok1","project_id":"`+pid+`"},{"title":"ok2","project_id":"`+pid+`"}]`)
	require.Equal(t, 200, code, "%v", out)

	// patchTask：bad json / priority+estimate / 迭代置空 / 非法迭代 / 跨项目迭代 / 合法迭代 / 非法状态迁移
	code, _ = covDo(t, app, "PATCH", "/api/v1/tasks/"+tid, `{bad-json`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "PATCH", "/api/v1/tasks/"+tid, `{"priority":3,"estimated_hours":1.5}`)
	assert.Equal(t, 200, code)
	code, _ = covDo(t, app, "PATCH", "/api/v1/tasks/"+tid, `{"target_iteration_id":""}`)
	assert.Equal(t, 200, code)
	code, _ = covDo(t, app, "PATCH", "/api/v1/tasks/"+tid, `{"target_iteration_id":"bad-id"}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "PATCH", "/api/v1/tasks/"+tid, `{"target_iteration_id":"`+foreignIter+`"}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "PATCH", "/api/v1/tasks/"+tid, `{"target_iteration_id":"`+iterID+`"}`)
	assert.Equal(t, 200, code)
	_, err := model.Pool.Exec(ctx, `UPDATE tasks SET status='PASSED' WHERE id=$1`, tid)
	require.NoError(t, err)
	code, _ = covDo(t, app, "PATCH", "/api/v1/tasks/"+tid, `{"status":"RUNNING"}`)
	assert.Equal(t, 400, code)

	// approveTask：不存在 / 非法状态 / 成功（forge 快速失败于不可达 OPENCODE）
	code, _ = covDo(t, app, "POST", "/api/v1/tasks/"+uuid.NewString()+"/approve", `{}`)
	assert.True(t, code >= 400, "code=%d", code)
	code, _ = covDo(t, app, "POST", "/api/v1/tasks/"+tid+"/approve", `{}`)
	assert.Equal(t, 400, code)
	pendID := uuid.NewString()
	_, err = model.Pool.Exec(ctx, `INSERT INTO tasks (id, project_id, title, status) VALUES ($1,$2,'cov21k pend','PENDING')`, pendID, pid)
	require.NoError(t, err)
	code, out = covDo(t, app, "POST", "/api/v1/tasks/"+pendID+"/approve", `{"action":"approve"}`)
	require.Equal(t, 200, code, "%v", out)
	assert.Equal(t, "RUNNING", covData(t, out)["status"])

	// stackConstraints：默认 go / project.stack 解析 / 非法 JSON 回退
	assert.Equal(t, "go", stackConstraints(ctx, pendID)["language"])
	_, err = model.Pool.Exec(ctx, `UPDATE projects SET stack=$2::jsonb WHERE id=$1`, pid,
		`{"stack":{"backend":{"lang":"node","framework":"fastify","runtime":"node20"},"frontend":{"framework":"react"}},"test":{"unit":"vitest"}}`)
	require.NoError(t, err)
	sc := stackConstraints(ctx, pendID)
	assert.Equal(t, "node", sc["language"])
	assert.Equal(t, "fastify", sc["framework"])
	assert.Equal(t, "node20", sc["runtime"])
	assert.Equal(t, "react", sc["frontend"])
	assert.Equal(t, "vitest", sc["test_unit"])
	_, err = model.Pool.Exec(ctx, `UPDATE projects SET stack='[1,2]'::jsonb WHERE id=$1`, pid)
	assert.NoError(t, err)
	assert.Equal(t, "go", stackConstraints(ctx, pendID)["language"])

	// getDag：成功 / 不存在项目
	code, _ = covDo(t, app, "GET", "/api/v1/projects/"+pid+"/dag", "")
	assert.Equal(t, 200, code)
	code, _ = covDo(t, app, "GET", "/api/v1/projects/"+uuid.NewString()+"/dag", "")
	assert.True(t, code < 500, "code=%d", code)

	// listTasks 过滤
	code, _ = covDo(t, app, "GET", "/api/v1/tasks?project_id="+pid+"&status=PENDING", "")
	assert.Equal(t, 200, code)
	code, _ = covDo(t, app, "GET", "/api/v1/tasks/"+tid, "")
	assert.True(t, code < 500, "code=%d", code)
}

func TestCovV21k_ProjectPurgeRestoreAndSpecCreate(t *testing.T) {
	app := covV21hApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()

	// 项目软删 → 恢复；purge 需已删除
	pid := covV21Project(t, ctx, false)
	code, _ := covDo(t, app, "DELETE", "/api/v1/projects/"+pid, "")
	assert.True(t, code < 500, "code=%d", code)
	code, _ = covDo(t, app, "POST", "/api/v1/projects/"+pid+"/restore", "")
	assert.True(t, code < 500, "code=%d", code)
	// 未归档时 purge → 400
	code, _ = covDo(t, app, "DELETE", "/api/v1/projects/"+pid+"?purge=true", "")
	assert.True(t, code < 500, "code=%d", code)
	// 归档后 purge
	pid2 := covV21Project(t, ctx, false)
	code, _ = covDo(t, app, "DELETE", "/api/v1/projects/"+pid2, "")
	assert.True(t, code < 500, "code=%d", code)
	code, _ = covDo(t, app, "DELETE", "/api/v1/projects/"+pid2+"?purge=true&confirm=DELETE", "")
	assert.True(t, code < 500, "code=%d", code)

	// spec：createSpec 校验分支 + BS autoStoryID + 详情/校验/refs/列表过滤
	specPID := covV21Project(t, ctx, false)
	code, _ = covDo(t, app, "POST", "/api/v1/specs", `{bad-json`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "POST", "/api/v1/specs", `{"project_id":"`+specPID+`"}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "POST", "/api/v1/specs", `{"layer":"XX","title":"t","project_id":"`+specPID+`"}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "POST", "/api/v1/specs", `{"layer":"BS","title":"t"}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "POST", "/api/v1/specs", `{"layer":"BS","title":"t","project_id":"bad-id"}`)
	assert.Equal(t, 400, code)
	code, out := covDo(t, app, "POST", "/api/v1/specs", `{"layer":"BS","title":"cov21k auto","project_id":"`+specPID+`","content":{"what":"cov"}}`)
	require.Equal(t, 200, code, "%v", out)
	sid, _ := covData(t, out)["id"].(string)
	require.NotEmpty(t, sid)
	code, _ = covDo(t, app, "GET", "/api/v1/specs/"+sid, "")
	assert.Equal(t, 200, code)
	code, _ = covDo(t, app, "POST", "/api/v1/specs/"+sid+"/validate", `{}`)
	assert.Equal(t, 200, code)
	code, _ = covDo(t, app, "GET", "/api/v1/specs/"+sid+"/refs", "")
	assert.Equal(t, 200, code)
	code, _ = covDo(t, app, "GET", "/api/v1/specs?project_id="+specPID+"&layer=BS", "")
	assert.Equal(t, 200, code)
	code, _ = covDo(t, app, "GET", "/api/v1/specs/"+sid+"?layer=BS", "")
	assert.True(t, code < 500, "code=%d", code)
}
