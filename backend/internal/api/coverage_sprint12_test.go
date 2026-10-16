package api

// B1 冲刺补测：pipelineScope 全分支 / recordJobCompletion 新建行与时长分支 /
// lastFailures 有数据 / reindex inbox 批量分支。

import (
	"context"
	"net/http/httptest"
	"testing"

	"forja/backend/internal/model"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPipelineScope_Branches 覆盖 project_id 非法、成员项目授权、Admin 全量、匿名成员过滤
func TestPipelineScope_Branches(t *testing.T) {
	// 非法 uuid → InvalidParam（无需 DB）
	app := newPipelineApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	// 非法 project_id
	code, _ := getJSON(t, app, "/api/v1/pipelines?project_id=not-a-uuid")
	assert.Equal(t, 400, code)

	// 合法但未授权项目（随机 id 非成员也不异常）→ 403 或空列表（AuthorizeProject 拒绝）
	// 这里用 Admin 上下文（newPipelineApp 默认 Admin），AuthorizeProject 放行
	code, out := getJSON(t, app, "/api/v1/pipelines?project_id=00000000-0000-0000-0000-000000000001")
	assert.Equal(t, 200, code)
	_ = out

	// 无 project_id + 非 Admin 限成员项目分支已由常规列表用例覆盖
}

// TestRecordJobCompletion_NewRunAndDuration 覆盖 locateRunID 新建行 + parseRFC3339 时长
func TestRecordJobCompletion_NewRunAndDuration(t *testing.T) {
	newPipelineApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	repo := "forja-admin/cov-rec-" + uuid.NewString()[:8]
	sha := randSHA()

	// 首次出现：无 push/manual，自动建 workflow_job 行 + job 时长入库
	// 2026-09-12 动态聚合：job 集全部完成且成功 ⇒ SUCCESS（兼容项目单 job）
	recordJobCompletion(ctx, repo, sha, "backend", "success", "2026-08-26T00:00:00Z", "2026-08-26T00:02:30Z")
	var status string
	var duration int64
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT status FROM pipeline_runs WHERE repo=$1 AND sha=$2`, repo, sha).Scan(&status))
	assert.Equal(t, "SUCCESS", status)
	require.NoError(t, model.Pool.QueryRow(ctx, `
		SELECT COALESCE(duration_ms,0) FROM pipeline_jobs
		WHERE run_id=(SELECT id FROM pipeline_runs WHERE repo=$1 AND sha=$2) AND name='backend'`, repo, sha).Scan(&duration))
	assert.Equal(t, int64(150_000), duration)
	// 补齐剩余 3 预期 job → SUCCESS
	recordJobCompletion(ctx, repo, sha, "frontend", "success", "2026-08-26T00:02:30Z", "2026-08-26T00:03:00Z")
	recordJobCompletion(ctx, repo, sha, "e2e", "success", "2026-08-26T00:03:00Z", "2026-08-26T00:03:10Z")
	recordJobCompletion(ctx, repo, sha, "build-push", "success", "2026-08-26T00:03:10Z", "2026-08-26T00:04:00Z")
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT status FROM pipeline_runs WHERE repo=$1 AND sha=$2`, repo, sha).Scan(&status))
	assert.Equal(t, "SUCCESS", status)

	// 再加入 failure job → 聚合 FAILURE（与 77% 覆盖互斥分支）
	// U-3: 覆盖 failure 优先于 4 job 成功（覆写 e2e 为 failure 触发 FAILURE）
	recordJobCompletion(ctx, repo, sha, "e2e", "failure", "2026-08-26T00:04:00Z", "2026-08-26T00:04:10Z")
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT status FROM pipeline_runs WHERE repo=$1 AND sha=$2`, repo, sha).Scan(&status))
	assert.Equal(t, "FAILURE", status)

	// 非法时间格式应不崩
	recordJobCompletion(ctx, repo, sha, "frontend", "success", "bad-time", "")
	_ = status
}

func TestLastFailures_WithData(t *testing.T) {
	newPipelineApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	const pid = "00000000-0000-0000-0000-000000000001"
	taskID := uuid.NewString()
	specID := uuid.NewString()
	_, err := model.Pool.Exec(ctx,
		`INSERT INTO specs (id, project_id, layer, title) VALUES ($1,$2,'BS','lf-spec')`, specID, pid)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM specs WHERE id=$1`, specID) })
	_, err = model.Pool.Exec(ctx, `
		INSERT INTO tasks (id, project_id, spec_id, title, status) VALUES ($1,$2,$3,'lf-task','FAILED')`, taskID, pid, specID)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM tasks WHERE id=$1`, taskID) })

	// 创建失败的 test_case + test_result
	caseID := uuid.NewString()
	_, err = model.Pool.Exec(ctx, `
		INSERT INTO test_cases (id, task_id, name) VALUES ($1,$2,'case-lf')`, caseID, taskID)
	require.NoError(t, err)
	_, err = model.Pool.Exec(ctx, `
		INSERT INTO test_results (id, test_case_id, status, output) VALUES ($1,$2,'FAILED','out-lf')`, uuid.NewString(), caseID)
	require.NoError(t, err)

	msg := lastFailures(ctx, taskID)
	assert.Contains(t, msg, "case-lf")
	assert.Contains(t, msg, "out-lf")
}

func TestReindexInboxBranch(t *testing.T) {
	app := newCovAdminApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	// 注入一条 inbox 无向量记录
	ctx := context.Background()
	inboxID := uuid.NewString()
	_, err := model.Pool.Exec(ctx, `
		INSERT INTO requirements_inbox (id, project_id, source_type, raw_content)
		VALUES ($1,'00000000-0000-0000-0000-000000000001','manual','reindex-coverage-test')`, inboxID)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM requirements_inbox WHERE id=$1`, inboxID) })

	req := httptest.NewRequest("POST", "/api/v1/admin/reindex-embeddings", nil)
	resp, e := app.Test(req, 30000)
	require.NoError(t, e)
	assert.Equal(t, 200, resp.StatusCode)
}
