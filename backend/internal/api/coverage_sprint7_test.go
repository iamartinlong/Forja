package api

// B1 覆盖率冲刺（v1.1-sprint B1）：G4 回写 / 部署记录端点 / sync 对账 /
// project 纯函数 / SSE 辅助 —— 核心交付链路的缺口补测。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"forja/backend/internal/model"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLinkCIToTask G4 回写：RELEASE_PENDING 任务按 repo+sha 命中 → ci.passed/ci.failed
// 审计 + SSE 事件；未映射仓库静默跳过。
func TestLinkCIToTask(t *testing.T) {
	newPipelineApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()

	pid := uuid.NewString()
	repo := "forja-admin/linkci-" + uuid.NewString()[:8]
	sha := randSHA()
	specID := uuid.NewString()
	taskID := uuid.NewString()

	_, err := model.Pool.Exec(ctx, `INSERT INTO projects (id, name, repo_full_name) VALUES ($1,$2,$3)`, pid, "linkci-"+pid[:8], repo)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, pid) })
	_, err = model.Pool.Exec(ctx,
		`INSERT INTO specs (id, project_id, layer, title) VALUES ($1,$2,'BS','linkci-spec')`, specID, pid)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = model.Pool.Exec(ctx, `DELETE FROM audit_logs WHERE target_id=$1 AND action LIKE 'ci.%'`, taskID)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM tasks WHERE id=$1`, taskID)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM specs WHERE id=$1`, specID)
	})
	_, err = model.Pool.Exec(ctx, `
		INSERT INTO tasks (id, project_id, spec_id, title, status, commit_sha)
		VALUES ($1,$2,$3,'linkci-task','RELEASE_PENDING',$4)`, taskID, pid, specID, sha)
	require.NoError(t, err)

	linkCIToTask(ctx, repo, sha, "build-push", "success")
	var cnt int64
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM audit_logs WHERE action='ci.passed' AND target_id=$1`, taskID).Scan(&cnt))
	assert.Equal(t, int64(1), cnt, "success 结论应留 ci.passed 审计")

	linkCIToTask(ctx, repo, sha, "e2e", "failure")
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM audit_logs WHERE action='ci.failed' AND target_id=$1`, taskID).Scan(&cnt))
	assert.Equal(t, int64(1), cnt, "failure 结论应留 ci.failed 审计")

	// 非 RELEASE_PENDING 状态不命中
	_, _ = model.Pool.Exec(ctx, `UPDATE tasks SET status='RUNNING' WHERE id=$1`, taskID)
	linkCIToTask(ctx, repo, sha, "build-push", "failure")
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM audit_logs WHERE action LIKE 'ci.%' AND target_id=$1`, taskID).Scan(&cnt))
	assert.Equal(t, int64(2), cnt, "非 RELEASE_PENDING 不应新增回写审计")

	// 未映射仓库静默
	before := cnt
	linkCIToTask(ctx, "unknown/repo", sha, "e2e", "success")
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM audit_logs WHERE action LIKE 'ci.%' AND target_id=$1`, taskID).Scan(&cnt))
	assert.Equal(t, before, cnt, "未映射仓库应静默跳过")
}

func TestListDeploymentsEndpoint(t *testing.T) {
	app := newPipelineApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	sha := randSHA()
	seedDeployment(t, "forja-admin/forja", sha, "master-"+sha[:8], "success", "manual", "cov-admin")

	code, out := getJSON(t, app, "/api/v1/pipelines/deployments?page=1&page_size=5")
	require.Equal(t, 200, code)
	data := out["data"].(map[string]any)
	items := data["items"].([]any)
	assert.GreaterOrEqual(t, len(items), 1)
	found := false
	for _, it := range items {
		m := it.(map[string]any)
		if m["tag"] == "master-"+sha[:8] {
			found = true
			assert.Equal(t, "success", m["status"])
			assert.Equal(t, "manual", m["mode"])
			assert.NotEmpty(t, m["deployed_at"], "success 行应有上线时间")
		}
	}
	assert.True(t, found, "部署记录应包含 seeded 行")

	// 非法分页参数容错
	code, _ = getJSON(t, app, "/api/v1/pipelines/deployments?page=0&page_size=9999")
	assert.Equal(t, 200, code)
}

// TestSyncGiteaRuns mock Gitea ListActionRuns → upsert 补漏。
// 用独立临时项目锚定 forja-admin/forja，避免与共享种子项目的 repo 字段互踩。
func TestSyncGiteaRuns(t *testing.T) {
	app := newPipelineApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	var calls int64
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/user", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"login":"forja-admin"}`))
	})
	var lastBody string
	mux.HandleFunc("/api/v1/repos/forja-admin/forja/actions/runs", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		sha := randSHA()
		lastBody = `{"total_count":2,"workflow_runs":[` +
			`{"id":901,"name":"Build","head_branch":"master","head_sha":"` + sha + `","event":"manual","status":"success"},` +
			`{"id":902,"name":"Deploy","head_branch":"master","head_sha":"` + randSHA() + `","event":"workflow_dispatch","status":"success"}]}`
		_, _ = w.Write([]byte(lastBody))
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	t.Setenv("GITEA_URL", ts.URL)
	t.Setenv("GITEA_TOKEN", "tok")

	ctx := context.Background()
	// F0.5：repo_full_name 全局唯一——复用既有 self 仓绑定项目（种子项目），不再插入重复绑定
	var projID string
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT id::text FROM projects WHERE repo_full_name='forja-admin/forja'`).Scan(&projID))
	t.Cleanup(func() {
		_, _ = model.Pool.Exec(ctx, `DELETE FROM pipeline_runs WHERE event IN ('manual','workflow_dispatch') AND actor='gitea'`)
	})

	req := httptest.NewRequest("POST", "/api/v1/internal/gitea/sync", nil)
	resp, e := app.Test(req, 15000)
	require.NoError(t, e)
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	assert.Equal(t, 200, resp.StatusCode, "%v", out)
	data := out["data"].(map[string]any)
	t.Logf("sync response: %v | mock body tail: %s", out["data"], lastBody[len(lastBody)-120:])
	assert.GreaterOrEqual(t, data["synced_runs"].(float64), float64(2), "%v", out)

	// 归一化断言：manual→workflow_dispatch 已入表
	var cnt int64
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM pipeline_runs WHERE actor='gitea' AND status IN ('SUCCESS','FAILURE')`).Scan(&cnt))
	assert.GreaterOrEqual(t, cnt, int64(2))
}

// TestProjectPureFns fullName 拆分与环境读取的纯函数分支
func TestProjectPureFns(t *testing.T) {
	assert.Equal(t, "forja-admin", fullName2owner("forja-admin/forja"))
	assert.Equal(t, "forja", fullName2repo("forja-admin/forja"))
	// 无 '/' 时两者都回退为原名（实现语义）
	assert.Equal(t, "solo", fullName2owner("solo"))
	assert.Equal(t, "solo", fullName2repo("solo"))
	// 边界：'/' 在首位时 owner 条件 i>0 不成立，均回退原文
	assert.Equal(t, "/root-only", fullName2owner("/root-only"))
	assert.Equal(t, "/", fullName2repo("/"))

	t.Setenv("FORJA_TEST_KV", "v1")
	assert.Equal(t, "v1", getenvLocal("FORJA_TEST_KV", "dft"))
	assert.Equal(t, "dft", getenvLocal("FORJA_TEST_MISSING", "dft"))
}
