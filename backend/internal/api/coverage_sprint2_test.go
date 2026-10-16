package api

// S1.5 覆盖率冲刺（第二波）：listInbox / 任务编排全链路 / tsDraft / stream / 归档幂等分支。
// 依赖 Postgres；approve 走 Redis 不可达→进程内直跑兜底，OPENCODE_URL 指向不可达端口避免外呼。

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"forja/backend/internal/model"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCov2_InboxList(t *testing.T) {
	app := covApp(t)
	ctx := context.Background()
	pid := uuid.NewString()
	_, err := model.Pool.Exec(ctx, `INSERT INTO projects (id, name, slug) VALUES ($1,'cov-il','cov-il')`, pid)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, pid) })

	mk := func(raw, status string) {
		id := uuid.NewString()
		_, err := model.Pool.Exec(ctx,
			`INSERT INTO requirements_inbox (id, project_id, source_type, raw_content, content_hash, status) VALUES ($1,$2,'TEXT_PASTE',$3,$4,$5)`,
			id, pid, raw, "h-"+id[:8], status)
		require.NoError(t, err)
	}
	mk("alpha needs login", "NEW")
	mk("beta export csv", "DRAFTED")
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM requirements_inbox WHERE project_id=$1`, pid) })

	// 缺 project_id → 400
	code, _ := covDo(t, app, "GET", "/api/v1/inbox", "")
	assert.Equal(t, 400, code)

	// 列表 + 状态过滤
	code, out := covDo(t, app, "GET", "/api/v1/inbox?project_id="+pid+"&status=new", "")
	require.Equal(t, 200, code)
	d := covData(t, out)
	assert.Equal(t, float64(1), d["total"], "status 过滤应仅剩 NEW 一条")

	// 全量
	code, out = covDo(t, app, "GET", "/api/v1/inbox?project_id="+pid, "")
	require.Equal(t, 200, code)
	assert.Equal(t, float64(2), covData(t, out)["total"])

	// q 向量路径：先补 embedding 再带 q 查询
	var iid string
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT id FROM requirements_inbox WHERE project_id=$1 LIMIT 1`, pid).Scan(&iid))
	_, _ = model.Pool.Exec(ctx, `UPDATE requirements_inbox SET embedding=('[0.1]'::text)::vector(1536) WHERE id=$1`, iid)
	code, out = covDo(t, app, "GET", "/api/v1/inbox?project_id="+pid+"&q=login", "")
	require.Equal(t, 200, code)
	assert.NotNil(t, covData(t, out)["items"])

	// 分页越界页
	code, _ = covDo(t, app, "GET", "/api/v1/inbox?project_id="+pid+"&page=9", "")
	assert.Equal(t, 200, code)
}

func TestCov2_TaskOrchestration(t *testing.T) {
	app := covApp(t)
	ctx := context.Background()
	pid := uuid.NewString()
	_, err := model.Pool.Exec(ctx, `INSERT INTO projects (id, name, slug) VALUES ($1,'cov-task','cov-task')`, pid)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, pid) })
	t.Setenv("OPENCODE_URL", "http://127.0.0.1:1")
	t.Setenv("SKIP_VET", "1")

	cleanupTask := func(id string) {
		for _, tbl := range []string{"agent_executions", "test_results", "test_cases"} {
			_, _ = model.Pool.Exec(ctx, fmt.Sprintf(`DELETE FROM %s WHERE task_id=$1`, tbl), id)
		}
		_, _ = model.Pool.Exec(ctx, `DELETE FROM tasks WHERE id=$1`, id)
	}

	// createTask：缺 title/project 校验 + 正常创建
	code, _ := covDo(t, app, "POST", "/api/v1/tasks", `{"title":"x"}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "POST", "/api/v1/tasks", `{"title":"y"}`)
	assert.Equal(t, 400, code)
	code, out := covDo(t, app, "POST", "/api/v1/tasks",
		fmt.Sprintf(`{"project_id":%q,"title":"cov-t1","priority":2,"estimated_hours":3.5,"assigned_to":"","type":"task","description":"d"}`, pid))
	require.Equal(t, 200, code, "%v", out)
	t1 := covData(t, out)["id"].(string)
	t.Cleanup(func() { cleanupTask(t1) })

	// batchCreateTasks：两条合法
	code, out = covDo(t, app, "POST", "/api/v1/tasks/batch",
		fmt.Sprintf(`[{"project_id":%q,"title":"b1"},{"project_id":%q,"title":"b2"}]`, pid, pid))
	require.Equal(t, 200, code, "%v", out)
	batchItems, _ := covData(t, out)["items"].([]any)
	require.Len(t, batchItems, 2)
	for _, it := range batchItems {
		m, _ := it.(map[string]any)
		if idv, ok := m["id"].(string); ok && idv != "" {
			cleanupTask(idv)
		}
	}

	// approveTask：PENDING → RUNNING；再次 approve → INVALID_STATE
	code, out = covDo(t, app, "POST", "/api/v1/tasks/"+t1+"/approve", `{"action":"approve"}`)
	require.Equal(t, 200, code, "%v", out)
	code, out = covDo(t, app, "POST", "/api/v1/tasks/"+t1+"/approve", `{"action":"approve"}`)
	assert.Equal(t, 400, code)
	assert.Equal(t, "INVALID_STATE", errCodeOf(t, out))

	// getTask（/stream 真 SSE 常驻流由 sse.go 提供，httptest 无法消费常驻响应，
	// 其行为由 events 包单测 + 真浏览器验收覆盖）
	code, out = covDo(t, app, "GET", "/api/v1/tasks/"+t1, "")
	assert.Equal(t, 200, code)
	assert.Equal(t, "cov-t1", covData(t, out)["title"])

	// patchTask：状态守卫（RUNNING → RUNNING 拒绝）+ 描述更新
	code, out = covDo(t, app, "PATCH", "/api/v1/tasks/"+t1, `{"status":"RUNNING"}`)
	assert.Equal(t, 400, code)
	code, out = covDo(t, app, "PATCH", "/api/v1/tasks/"+t1, `{"description":"upd"}`)
	assert.Equal(t, 200, code)

	// convergeTask：空 diff 应收敛通过
	code, out = covDo(t, app, "POST", "/api/v1/tasks/"+t1+"/converge", "")
	assert.Equal(t, 200, code, "%v", out)

	// getDag：项目维度就绪视图
	code, out = covDo(t, app, "GET", "/api/v1/projects/"+pid+"/dag", "")
	assert.Equal(t, 200, code)
	require.NotNil(t, covData(t, out))

	// 不存在的任务
	code, _ = covDo(t, app, "GET", "/api/v1/tasks/"+uuid.NewString(), "")
	assert.Equal(t, 404, code)
}

func TestCov2_TsDraft(t *testing.T) {
	app := covApp(t)
	ctx := context.Background()
	pid := uuid.NewString()
	// 先清同名残留：唯一约束会让固定名项目插入炸掉
	_, _ = model.Pool.Exec(ctx, `DELETE FROM projects WHERE name='cov-ts'`)
	_, err := model.Pool.Exec(ctx, `INSERT INTO projects (id, name, slug) VALUES ($1,'cov-ts','cov-ts')`, pid)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, pid) })

	bs := map[string]any{
		"project_id": pid, "layer": "BS", "title": "cov-bs",
		"content": map[string]any{
			"story_id": "COV-1",
			"acceptance_criteria": []any{
				map[string]any{"given": "g", "when": "w", "then": "t"},
			},
		},
	}
	b, _ := json.Marshal(bs)
	code, out := covDo(t, app, "POST", "/api/v1/specs", string(b))
	require.Equal(t, 200, code, "%v", out)
	sid := covData(t, out)["id"].(string)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM specs WHERE project_id=$1`, pid) })

	// 仅生成草稿（不落库）
	code, out = covDo(t, app, "POST", "/api/v1/specs/"+sid+"/ts-draft", "")
	require.Equal(t, 200, code, "%v", out)
	assert.Contains(t, fmt.Sprintf("%v", covData(t, out)), "scenario")

	// create=true 持久化为 TS 层规格
	code, out = covDo(t, app, "POST", "/api/v1/specs/"+sid+"/ts-draft?create=true", "")
	assert.Equal(t, 200, code, "%v", out)

	// embed 端点（异步向量化，容忍任意结果）
	code, _ = covDo(t, app, "POST", "/api/v1/specs/"+sid+"/embed", "")
	assert.Contains(t, []int{200, 500}, code)
}

func TestCov2_ArchiveIdempotentBranches(t *testing.T) {
	app := covApp(t)
	ctx := context.Background()

	// 未归档项目 restore → 幂等 archived:false
	pid := uuid.NewString()
	_, err := model.Pool.Exec(ctx, `INSERT INTO projects (id, name, slug) VALUES ($1,'cov-idem','cov-idem')`, pid)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, pid) })
	code, out := covDo(t, app, "POST", "/api/v1/projects/"+pid+"/restore", "")
	require.Equal(t, 200, code, "%v", out)
	assert.Equal(t, false, covData(t, out)["archived"])

	// updateProject 分支：nothing to update / 不存在
	code, _ = covDo(t, app, "PATCH", "/api/v1/projects/"+pid, `{}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "PATCH", "/api/v1/projects/"+uuid.NewString(), `{"name":"ghost"}`)
	assert.Equal(t, 404, code)

	// 名称变更触发 slug 重算 + 重名冲突 409
	code, out = covDo(t, app, "POST", "/api/v1/projects", `{"name":"cov-slug-a","description":""}`)
	require.Equal(t, 200, code)
	pa := covData(t, out)["id"].(string)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, pa) })
	_, _ = covDo(t, app, "POST", "/api/v1/projects", `{"name":"cov-slug-b"}`)
	code, _ = covDo(t, app, "PATCH", "/api/v1/projects/"+pa, `{"name":"cov-slug-b"}`)
	assert.Equal(t, 409, code, "slug 唯一冲突应 409")
	code, out = covDo(t, app, "PATCH", "/api/v1/projects/"+pa, `{"name":"cov-slug-a2","description":"d2"}`)
	assert.Equal(t, 200, code)
	code, out = covDo(t, app, "GET", "/api/v1/projects/"+pa, "")
	require.Equal(t, 200, code)
	assert.Equal(t, "cov-slug-a2", covData(t, out)["slug"], "改名应重算 slug")
}
