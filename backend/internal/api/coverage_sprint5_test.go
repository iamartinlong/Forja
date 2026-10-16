package api

// S1.5 覆盖率冲刺（第五波）：扫尾分支——归档幂等/404、任务聚合详情、pact 层门禁、
// inbox 去重、成员 404 分支、批量部分失败语义。

import (
	"context"
	"fmt"
	"testing"

	"forja/backend/internal/model"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCov5_FinalSweep(t *testing.T) {
	app := covApp(t)
	ctx := context.Background()
	pid := uuid.NewString()
	_, err := model.Pool.Exec(ctx, `INSERT INTO projects (id, name, slug) VALUES ($1,'cv5','cv5')`, pid)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, pid) })

	// ---- 归档幂等：二次 DELETE 返回 archived:true（非 404）----
	code, out := covDo(t, app, "DELETE", "/api/v1/projects/"+pid, "")
	require.Equal(t, 200, code)
	code, out = covDo(t, app, "DELETE", "/api/v1/projects/"+pid, "")
	assert.Equal(t, 200, code, "重复归档幂等: %v", out)
	assert.Equal(t, true, covData(t, out)["archived"])
	code, _ = covDo(t, app, "DELETE", "/api/v1/projects/"+uuid.NewString(), "")
	assert.Equal(t, 404, code, "归档不存在的项目应 404")
	// 恢复后清理
	code, _ = covDo(t, app, "POST", "/api/v1/projects/"+pid+"/restore", "")
	assert.Equal(t, 200, code)

	// ---- getTask 聚合分支：code_changes + test_cases/test_results + agent_executions ----
	tid := uuid.NewString()
	_, err = model.Pool.Exec(ctx, `INSERT INTO tasks (id, project_id, title, status) VALUES ($1,$2,'agg','PENDING')`, tid, pid)
	require.NoError(t, err)
	t.Cleanup(func() {
		for _, tbl := range []string{"code_changes", "test_results", "test_cases", "agent_executions"} {
			_, _ = model.Pool.Exec(ctx, fmt.Sprintf(`DELETE FROM %s WHERE task_id=$1`, tbl), tid)
		}
		_, _ = model.Pool.Exec(ctx, `DELETE FROM tasks WHERE id=$1`, tid)
	})
	_, _ = model.Pool.Exec(ctx, `INSERT INTO code_changes (id, task_id, file_path, diff, change_log) VALUES ($1,$2,'m.go','d','l')`, uuid.NewString(), tid)
	var tcID string
	require.NoError(t, model.Pool.QueryRow(ctx,
		`INSERT INTO test_cases (id, task_id, name, framework) VALUES ($1,$2,'n','go') RETURNING id`, uuid.NewString(), tid).Scan(&tcID))
	_, _ = model.Pool.Exec(ctx, `INSERT INTO test_results (id, test_case_id, status, output) VALUES ($1,$2,'PASSED','ok')`, uuid.NewString(), tcID)
	_, _ = model.Pool.Exec(ctx, `INSERT INTO agent_executions (id, task_id, agent_type, input, output, status, attempt, started_at, finished_at) VALUES ($1,$2,'opencode','{}','{}','SUCCESS',1,now(),now())`, uuid.NewString(), tid)
	code, out = covDo(t, app, "GET", "/api/v1/tasks/"+tid, "")
	require.Equal(t, 200, code)
	td := covData(t, out)
	cc, _ := td["code_changes"].([]any)
	tr, _ := td["test_results"].([]any)
	ae, _ := td["agent_executions"].([]any)
	assert.Len(t, cc, 1)
	assert.Len(t, tr, 1)
	assert.Len(t, ae, 1)

	// ---- pact 门禁：BS 层 LAYER_MISMATCH / 不存在 404 / 坏 uuid 400 ----
	sid := uuid.NewString()
	_, err = model.Pool.Exec(ctx, `INSERT INTO specs (id, project_id, layer, title) VALUES ($1,$2,'BS','cv5-bs')`, sid, pid)
	require.NoError(t, err)
	_, _ = model.Pool.Exec(ctx, `INSERT INTO spec_versions (id, spec_id, version, content) VALUES ($1,$2,'v0.1.0','{}'::jsonb)`, uuid.NewString(), sid)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM specs WHERE id=$1`, sid) })
	code, out = covDo(t, app, "POST", "/api/v1/specs/"+sid+"/pact", "")
	assert.Equal(t, 400, code)
	assert.Equal(t, "LAYER_MISMATCH", errCodeOf(t, out))
	code, _ = covDo(t, app, "POST", "/api/v1/specs/"+uuid.NewString()+"/pact", "")
	assert.Equal(t, 404, code)
	code, _ = covDo(t, app, "POST", "/api/v1/specs/not-a-uuid/pact", "")
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "GET", "/api/v1/specs/not-a-uuid/pact", "")
	assert.Equal(t, 400, code)

	// ---- inbox 去重：同内容二传 dedup=true ----
	raw := "dedup me " + uuid.NewString()[:6]
	code, out = covDo(t, app, "POST", "/api/v1/inbox",
		fmt.Sprintf(`{"project_id":%q,"source_type":"TEXT_PASTE","raw_content":%q}`, pid, raw))
	require.Equal(t, 200, code)
	i1 := covData(t, out)["id"].(string)
	code, out = covDo(t, app, "POST", "/api/v1/inbox",
		fmt.Sprintf(`{"project_id":%q,"source_type":"TEXT_PASTE","raw_content":%q}`, pid, raw))
	require.Equal(t, 200, code)
	dd := covData(t, out)["dedup"]
	if b, ok := dd.(bool); ok {
		assert.True(t, b, "同内容二传应命中去重")
	}
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM requirements_inbox WHERE project_id=$1`, pid) })
	_ = i1

	// ---- 成员 404 分支 ----
	ghost := uuid.NewString()
	code, _ = covDo(t, app, "PATCH", "/api/v1/projects/"+pid+"/members/"+ghost, `{"role":"Viewer"}`)
	assert.Equal(t, 404, code, "更新不存在成员应 404")
	code, _ = covDo(t, app, "DELETE", "/api/v1/projects/"+pid+"/members/"+ghost, "")
	assert.Equal(t, 404, code, "移除不存在成员应 404")

	// ---- enable/disable 不存在用户 → 404 ----
	code, _ = covDo(t, app, "POST", "/api/v1/admin/users/ghost-"+uuid.NewString()[:6]+"/enable", "")
	assert.Equal(t, 404, code)

	// ---- 批量创建：含非法项时逐条报错而非整体失败 ----
	code, out = covDo(t, app, "POST", "/api/v1/tasks/batch",
		fmt.Sprintf(`[{"project_id":%q,"title":""}]`, pid))
	assert.Equal(t, 200, code, "批量接口对空标题项应逐条报错")
	items, _ := covData(t, out)["items"].([]any)
	require.Len(t, items, 1)
	m, _ := items[0].(map[string]any)
	_, hasErr := m["error"]
	assert.True(t, hasErr, "空标题项应携带 error: %v", m)
}

func TestCov5_AuthorizeBadProjectID(t *testing.T) {
	app := covApp(t)
	code, _ := covDo(t, app, "GET", "/api/v1/inbox?project_id=bad-uuid", "")
	assert.Contains(t, []int{400, 403}, code)
}

// TestCov5_TaskDetailScopesToLatestActivity 2026-09-10：详情聚合只取最近活跃窗
// （旧轮残留不出镜）+ 文件去重 + 用例名关联（签核弹窗此前堆 150 行、无名失败）。
func TestCov5_TaskDetailScopesToLatestActivity(t *testing.T) {
	app := covApp(t)
	ctx := context.Background()
	pid := uuid.NewString()
	_, err := model.Pool.Exec(ctx, `INSERT INTO projects (id, name, slug) VALUES ($1,'cv5scope','cv5scope')`, pid)
	require.NoError(t, err)
	tid := uuid.NewString()
	_, err = model.Pool.Exec(ctx, `INSERT INTO tasks (id, project_id, title, status) VALUES ($1,$2,'scope','PENDING')`, tid, pid)
	require.NoError(t, err)
	t.Cleanup(func() {
		for _, tbl := range []string{"code_changes", "test_results", "test_cases", "agent_executions"} {
			_, _ = model.Pool.Exec(ctx, fmt.Sprintf(`DELETE FROM %s WHERE task_id=$1`, tbl), tid)
		}
		_, _ = model.Pool.Exec(ctx, `DELETE FROM tasks WHERE id=$1`, tid)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, pid)
	})
	// 旧轮残留：2 天前的文件行 + 失败行
	_, err = model.Pool.Exec(ctx, `INSERT INTO code_changes (id, task_id, file_path, diff, change_log, created_at) VALUES ($1,$2,'old.go','d','l', now() - interval '2 days')`, uuid.NewString(), tid)
	require.NoError(t, err)
	var oldCase string
	require.NoError(t, model.Pool.QueryRow(ctx, `INSERT INTO test_cases (id, task_id, name, created_at) VALUES ($1,$2,'old-static', now() - interval '2 days') RETURNING id`, uuid.NewString(), tid).Scan(&oldCase))
	_, err = model.Pool.Exec(ctx, `INSERT INTO test_results (test_case_id, status, output) VALUES ($1,'FAILED','old fail')`, oldCase)
	require.NoError(t, err)
	// 本轮：同名文件新旧两行（去重取新，时间戳拉开避免同秒打平无序）+ 带名失败行
	_, err = model.Pool.Exec(ctx, `INSERT INTO code_changes (id, task_id, file_path, diff, change_log, created_at) VALUES ($1,$2,'new.go','d-old','l', now() - interval '1 minute'),($3,$2,'new.go','d-new','l', now())`, uuid.NewString(), tid, uuid.NewString())
	require.NoError(t, err)
	var newCase string
	require.NoError(t, model.Pool.QueryRow(ctx, `INSERT INTO test_cases (id, task_id, name) VALUES ($1,$2,'vet') RETURNING id`, uuid.NewString(), tid).Scan(&newCase))
	_, err = model.Pool.Exec(ctx, `INSERT INTO test_results (test_case_id, status, output) VALUES ($1,'FAILED','new fail')`, newCase)
	require.NoError(t, err)

	code, out := covDo(t, app, "GET", "/api/v1/tasks/"+tid, "")
	require.Equal(t, 200, code)
	td := covData(t, out)
	cc, _ := td["code_changes"].([]any)
	assert.Len(t, cc, 1, "旧轮行过滤 + 同名去重后只剩 1 行")
	assert.Equal(t, "new.go", cc[0].(map[string]any)["file_path"])
	assert.Equal(t, "d-new", cc[0].(map[string]any)["diff"], "去重取最新")
	tr, _ := td["test_results"].([]any)
	require.Len(t, tr, 1, "旧失败行过滤")
	assert.Equal(t, "vet", tr[0].(map[string]any)["name"], "用例名必须关联")
	assert.Equal(t, "new fail", tr[0].(map[string]any)["output"])
}
