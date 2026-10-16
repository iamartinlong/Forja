package api

// S6 构建部署分离端点测试（S6-build-deploy-separation.md §3.5）：
// mock Gitea httptest 覆盖 build/deploy 全链路与校验分支。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"forja/backend/internal/model"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// randSHA 每次运行随机的 40 位纯 hex sha——避免跨轮残留行触发查重/制品校验
func randSHA() string {
	u := strings.ReplaceAll(uuid.NewString(), "-", "")
	return (u + u)[:40]
}

// startMockGitea 拦截 BranchCommit / SetVariable / Dispatches 的 Gitea 替身。
// headSHA 可被用例改写；返回计数指针供断言调用次数。
func startMockGitea(t *testing.T) (*int64, *int64, *string) {
	t.Helper()
	var varWrites, dispatches int64
	head := strings.Repeat("ab", 20)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/user", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"login":"forja-admin"}`))
	})
	mux.HandleFunc("/api/v1/repos/forja-admin/forja/branches/master", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"master","commit":{"id":"` + head + `"}}`))
	})
	mux.HandleFunc("/api/v1/repos/forja-admin/forja/actions/variables/DEPLOY_TAG", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&varWrites, 1)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/api/v1/repos/forja-admin/forja/actions/workflows/", func(w http.ResponseWriter, r *http.Request) {
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
	t.Setenv("GITEA_TOKEN", "test-token")
	return &varWrites, &dispatches, &head
}

// TestGiteaDispatch_BuildAndDeploy 主链路：build 建行+审计；无制品 sha 部署被拒（400）；
// 有制品 deploy 写变量+dispatch+审计；定时入队+审计。
func TestGiteaDispatch_BuildAndDeploy(t *testing.T) {
	app := newPipelineApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	varWrites, dispatches, head := startMockGitea(t)
	ctx := context.Background()

	doPost := func(body string) (int, map[string]any) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/internal/gitea/dispatch", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req, 15000)
		require.NoError(t, err)
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}

	sha := *head // mock master HEAD——构建仅允许 HEAD（Gitea ref 不接受 commit sha）

	// 参数校验：坏 kind / 非 HEAD sha
	code, _ := doPost(`{"kind":"x","sha":"` + sha + `"}`)
	assert.Equal(t, 400, code)
	notHead := randSHA()
	code, out := doPost(`{"kind":"build","sha":"` + notHead + `"}`)
	assert.Equal(t, 400, code, "非 HEAD 构建应被拒: %v", out)

	// build：mock 接受 → 建 manual 行 RUNNING + 审计
	code, out = doPost(`{"kind":"build","sha":"` + sha + `"}`)
	require.Equal(t, 200, code, "%v", out)
	assert.Equal(t, int64(1), atomic.LoadInt64(dispatches))
	var status string
	require.Eventually(t, func() bool {
		return model.Pool.QueryRow(ctx,
			`SELECT status FROM pipeline_runs WHERE repo='forja-admin/forja' AND sha=$1 AND event='manual'`, sha).Scan(&status) == nil
	}, 3*time.Second, 100*time.Millisecond, "build dispatch 应建 manual 行")
	assert.Equal(t, "RUNNING", status)
	t.Cleanup(func() {
		_, _ = model.Pool.Exec(ctx, `DELETE FROM pipeline_runs WHERE repo='forja-admin/forja' AND sha=$1`, sha)
	})
	var cnt int64
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM audit_logs WHERE action='build.dispatched' AND detail::text LIKE '%`+sha[:16]+`%'`).Scan(&cnt))
	assert.Equal(t, int64(1), cnt)

	// deploy 未构建 sha：400（无制品不部署）
	unbuilt := randSHA()
	code, _ = doPost(`{"kind":"deploy","sha":"` + unbuilt + `"}`)
	assert.Equal(t, 400, code)

	// 先造 SUCCESS 构建行再 deploy → 放行（变量写入 + 第二次 dispatch）。
	// event 用默认 push：与 build 建的 manual 行不撞 UNIQUE(repo,sha,event)
	okRow := seedRun(t, "", "SUCCESS", [][2]string{{"backend", "success"}})
	_, err2 := model.Pool.Exec(ctx,
		`UPDATE pipeline_runs SET repo='forja-admin/forja', sha=$2 WHERE id=$1`, okRow.ID, sha)
	require.NoError(t, err2)

	code, out = doPost(`{"kind":"deploy","sha":"` + sha + `"}`)
	require.Equal(t, 200, code, "%v", out)
	assert.Equal(t, int64(1), atomic.LoadInt64(varWrites), "DEPLOY_TAG 应被写入")
	assert.Equal(t, int64(2), atomic.LoadInt64(dispatches))
	// §11 实体行应已建立（running），模拟 webhook 回流转 success
	CompleteRunningDeployment(ctx, "success")
	var depStatus string
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT status FROM deployments WHERE sha=$1 AND mode='manual'`, sha).Scan(&depStatus))
	assert.Equal(t, "success", depStatus)
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM audit_logs WHERE action='deploy.dispatched' AND detail::text LIKE '%master-`+sha[:8]+`%'`).Scan(&cnt))
	assert.Equal(t, int64(1), cnt)

	// 定时部署：制品已存在 → 入队成功并审计 scheduled
	code, out = doPost(`{"kind":"deploy","sha":"` + sha + `","delay_seconds":3600}`)
	require.Equal(t, 200, code, "%v", out)
	assert.Equal(t, float64(3600), out["data"].(map[string]any)["scheduled_in_seconds"])
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM audit_logs WHERE action='deploy.scheduled' AND detail::text LIKE '%`+sha[:16]+`%'`).Scan(&cnt))
	assert.Equal(t, int64(1), cnt)

	// 清理审计与实体痕迹避免污染其他用例断言
	_, _ = model.Pool.Exec(ctx,
		`DELETE FROM audit_logs WHERE action IN ('build.dispatched','deploy.dispatched','deploy.scheduled') AND detail::text LIKE '%`+sha[:16]+`%'`)
	_, _ = model.Pool.Exec(ctx, `DELETE FROM pipeline_runs WHERE id=$1`, okRow.ID)
	_, _ = model.Pool.Exec(ctx,
		`DELETE FROM deployments WHERE repo='forja-admin/forja' OR actor IN ('test-admin','admin') AND created_at > now()-interval '1 hour'`)
}

// TestWebhook_DeployJobNotAggregated S6 审计修正：deploy job 回流不得污染 HEAD 行
// （head_sha 是 master HEAD 而非目标 TAG commit——聚合会给未构建 commit 伪造 SUCCESS）
func TestWebhook_DeployJobNotAggregated(t *testing.T) {
	ensureTestDB(t)
	if model.Pool == nil {
		t.Skip("db unavailable")
	}
	t.Setenv("GITEA_WEBHOOK_SECRET", hookSecret)
	app := newHookApp()
	ctx := context.Background()

	repo := fmt.Sprintf("forja-admin/hook-%s", uuid.NewString()[:8])
	headSHA := strings.Repeat("12", 20)

	// HEAD 行处于 FAILURE（从未构建通过）
	row := seedRun(t, "", "FAILURE", [][2]string{{"backend", "failure"}})
	_, err := model.Pool.Exec(ctx,
		`UPDATE pipeline_runs SET repo=$2, sha=$3 WHERE id=$1`, row.ID, repo, headSHA)
	require.NoError(t, err)

	wf := fmt.Sprintf(`{"repository":{"full_name":%q},"workflow_job":{"name":"deploy","status":"completed","conclusion":"success","head_sha":%q,"started_at":"2026-08-26T00:00:00Z","completed_at":"2026-08-26T00:02:00Z"}}`, repo, headSHA)
	// delivery ID 必须随机：webhook_deliveries 持久化去重会吞掉固定 ID 的重放
	code, _ := postHook(t, app, "wf-deploy-"+uuid.NewString()[:8], sign(t, hookSecret, []byte(wf)), "workflow_job", wf)
	require.Equal(t, 200, code)

	time.Sleep(300 * time.Millisecond)
	var jobs, status string
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT COALESCE(string_agg(name,','),''), r.status FROM pipeline_runs r
		 LEFT JOIN pipeline_jobs j ON j.run_id=r.id
		 WHERE r.id=$1 GROUP BY r.status`, row.ID).Scan(&jobs, &status))
	assert.NotContains(t, jobs, "deploy", "deploy job 不得落 pipeline_jobs")
	assert.Equal(t, "FAILURE", status, "HEAD 行结论不得被 deploy 回流改写")

	var cnt int64
	require.NoError(t, model.Pool.QueryRow(ctx,
		// webhook ci.completed 审计 detail 为 jsonb text（冒号带空格），按 job 名值匹配近期窗口
		`SELECT COUNT(*) FROM audit_logs WHERE action='ci.completed' AND detail::text LIKE '%deploy%' AND created_at > now()-interval '1 minute'`).Scan(&cnt))
	assert.Equal(t, int64(1), cnt, "审计仍应留痕")

	_, _ = model.Pool.Exec(ctx, `DELETE FROM audit_logs WHERE action='ci.completed' AND detail::text LIKE '%`+headSHA[:16]+`%'`)
}

// TestGiteaDispatch_BuildDuplicate 同 sha 在途构建 → 409
func TestGiteaDispatch_BuildDuplicate(t *testing.T) {
	app := newPipelineApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	_, _, head := startMockGitea(t)
	ctx := context.Background()

	dup := seedRun(t, "", "RUNNING", nil, "manual")
	defer func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM pipeline_runs WHERE id=$1`, dup.ID) }()
	_, err := model.Pool.Exec(ctx, `UPDATE pipeline_runs SET repo='forja-admin/forja', sha=$2 WHERE id=$1`, dup.ID, *head)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/internal/gitea/dispatch",
		strings.NewReader(`{"kind":"build","sha":"`+*head+`"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err2 := app.Test(req, 15000)
	require.NoError(t, err2)
	assert.Equal(t, 409, resp.StatusCode)
}

// TestSummary_DeployedBanner summary.deployed 读 deployments 表最新 success 行（§11）
func TestSummary_DeployedBanner(t *testing.T) {
	app := newPipelineApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	sha := randSHA()
	seedDeployment(t, "forja-admin/forja", sha, "master-"+sha[:8], "success", "manual", "banner-admin")

	code, out := getJSON(t, app, "/api/v1/pipelines/summary")
	require.Equal(t, 200, code)
	sum := out["data"].(map[string]any)
	assert.Contains(t, sum, "pending_builds")
	dep, ok := sum["deployed"].(map[string]any)
	require.True(t, ok, "deployed banner 应存在: %v", sum)
	assert.Equal(t, "master-"+sha[:8], dep["tag"])
	assert.Equal(t, "banner-admin", dep["by"])
}
