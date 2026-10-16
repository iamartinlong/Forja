package api

// M1.2 构建通道测试：项目构建触发 / 用户项目禁 deploy / 制品登记。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"forja/backend/internal/model"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startBuildMock 拦截 branch/dispatch 的 Gitea 替身；返回 head sha 与 dispatch 计数。
func startBuildMock(t *testing.T, repoFull string) (*string, *int64) {
	t.Helper()
	head := strings.Repeat("cd", 20)
	var dispatches int64
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/user", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"login":"forja-admin"}`))
	})
	mux.HandleFunc("/api/v1/repos/"+repoFull+"/branches/master", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"master","commit":{"id":"` + head + `"}}`))
	})
	mux.HandleFunc("/api/v1/repos/"+repoFull+"/actions/workflows/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/dispatches") {
			atomic.AddInt64(&dispatches, 1)
			w.WriteHeader(http.StatusCreated)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	t.Setenv("GITEA_URL", ts.URL)
	t.Setenv("GITEA_TOKEN", "tok")
	return &head, &dispatches
}

func TestTriggerProjectBuild(t *testing.T) {
	app := covApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	repo := "forja-admin/build-proj-" + uuid.NewString()[:8]
	head, dispatches := startBuildMock(t, repo)
	ctx := context.Background()
	pid := uuid.NewString()
	_, err := model.Pool.Exec(ctx,
		`INSERT INTO projects (id,name,slug,repo_full_name,default_branch) VALUES ($1,$2,$2,$3,'master')`,
		pid, "bp-"+pid[:8], repo)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = model.Pool.Exec(ctx, `DELETE FROM pipeline_runs WHERE repo=$1`, repo)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, pid)
	})

	req := httptest.NewRequest("POST", "/api/v1/projects/"+pid+"/builds", nil)
	resp, err := app.Test(req, 15000)
	require.NoError(t, err)
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	require.Equal(t, 200, resp.StatusCode, "%v", out)
	assert.EqualValues(t, 1, atomic.LoadInt64(dispatches))
	assert.Equal(t, *head, out["data"].(map[string]any)["sha"])

	var status string
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT status FROM pipeline_runs WHERE repo=$1 AND sha=$2 AND event='manual'`, repo, *head).Scan(&status))
	assert.Equal(t, "RUNNING", status)

	// 防重复连点：同 sha 在途 → 409
	req2 := httptest.NewRequest("POST", "/api/v1/projects/"+pid+"/builds", nil)
	resp2, err := app.Test(req2, 15000)
	require.NoError(t, err)
	assert.Equal(t, 409, resp2.StatusCode)
}

func TestGiteaDispatch_UserProjectDeployRejected(t *testing.T) {
	app := newPipelineApp(t) // RegisterPipelineRoutes → RegisterDispatchRoutes
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	repo := "forja-admin/delivery-only-" + uuid.NewString()[:8]
	head, _ := startBuildMock(t, repo)
	pid := uuid.NewString()
	_, err := model.Pool.Exec(context.Background(),
		`INSERT INTO projects (id,name,slug,repo_full_name,default_branch) VALUES ($1,$2,$2,$3,'master')`,
		pid, "do-"+pid[:8], repo)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = model.Pool.Exec(context.Background(), `DELETE FROM projects WHERE id=$1`, pid) })

	body := `{"kind":"deploy","sha":"` + *head + `","project_id":"` + pid + `"}`
	req := httptest.NewRequest("POST", "/api/v1/internal/gitea/dispatch", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, 15000)
	require.NoError(t, err)
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	require.Equal(t, 400, resp.StatusCode, "%v", out)
	assert.Equal(t, "DELIVERY_ONLY", out["error"].(map[string]any)["code"])
}

// TestProjectBuildRunAggregatesSuccess M1.3 修复回归：项目单 job(build) 预播 + 完成 → run SUCCESS。
func TestProjectBuildRunAggregatesSuccess(t *testing.T) {
	ensureTestDB(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	repo := "forja-admin/agg-proj-" + uuid.NewString()[:8]
	head := strings.Repeat("ab", 20)
	ctx := context.Background()
	pid := uuid.NewString()
	_, err := model.Pool.Exec(ctx,
		`INSERT INTO projects (id,name,slug,repo_full_name,default_branch) VALUES ($1,$2,$2,$3,'master')`,
		pid, "ag-"+pid[:8], repo)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = model.Pool.Exec(ctx, `DELETE FROM pipeline_runs WHERE repo=$1`, repo)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, pid)
	})

	recordManualBuildRunWithBranch(ctx, repo, head, "master", "tester", pid)
	preseedProjectBuildJobs(ctx, repo, head, "master", pid)

	var total int
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM pipeline_jobs j JOIN pipeline_runs r ON r.id=j.run_id WHERE r.repo=$1 AND r.sha=$2`,
		repo, head).Scan(&total))
	assert.Equal(t, 1, total, "项目构建仅 build 一个 job")

	var st string
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT status FROM pipeline_runs WHERE repo=$1 AND sha=$2 AND event='manual'`, repo, head).Scan(&st))
	assert.Equal(t, "RUNNING", st)

	recordJobCompletion(ctx, repo, head, "build", "success", "", "")
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT status FROM pipeline_runs WHERE repo=$1 AND sha=$2 AND event='manual'`, repo, head).Scan(&st))
	assert.Equal(t, "SUCCESS", st)
}

func TestRegisterArtifact(t *testing.T) {
	app := covApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	_ = app
	repo := "forja-admin/artifact-proj-" + uuid.NewString()[:8]
	head := strings.Repeat("ef", 20)
	ctx := context.Background()
	pid := uuid.NewString()
	_, err := model.Pool.Exec(ctx,
		`INSERT INTO projects (id,name,slug,repo_full_name,default_branch) VALUES ($1,$2,$2,$3,'master')`,
		pid, "af-"+pid[:8], repo)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = model.Pool.Exec(ctx, `DELETE FROM project_artifacts WHERE project_id=$1`, pid)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM pipeline_runs WHERE repo=$1`, repo)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, pid)
	})
	recordManualBuildRunWithBranch(ctx, repo, head, "master", "tester", pid)

	registerArtifact(ctx, repo, head, "build")
	var tag, image, status string
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT tag, image, status FROM project_artifacts WHERE project_id=$1 AND sha=$2`, pid, head).Scan(&tag, &image, &status))
	assert.Equal(t, "master-"+head[:8], tag)
	assert.Equal(t, "gitea:3000/"+repo, image)
	assert.Equal(t, "built", status)

	// 幂等：重复登记不产生第二行
	registerArtifact(ctx, repo, head, "build")
	var cnt int
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM project_artifacts WHERE project_id=$1`, pid).Scan(&cnt))
	assert.Equal(t, 1, cnt)
}
