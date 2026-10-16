package api

// S6 §11 部署实体测试：状态机流转、在途互斥、回流匹配、docs-only 过滤。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"forja/backend/internal/model"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedDeployment 直插 deployment 行（测试夹具）；success 行自动回填 deployed_at
func seedDeployment(t *testing.T, repo, sha, tag, status, mode, actor string) string {
	t.Helper()
	ctx := context.Background()
	var id string
	require.NoError(t, model.Pool.QueryRow(ctx, `
		INSERT INTO deployments (repo, sha, tag, status, mode, actor, deployed_at)
		VALUES ($1,$2,$3,$4,$5,NULLIF($6,''),CASE WHEN $4='success' THEN now() END)
		RETURNING id::text`,
		repo, sha, tag, status, mode, actor).Scan(&id))
	t.Cleanup(func() {
		_, _ = model.Pool.Exec(ctx, `DELETE FROM deployments WHERE id=$1`, id)
	})
	return id
}

func TestDeployment_Lifecycle(t *testing.T) {
	newPipelineApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	repo := "forja-admin/forja"
	sha := randSHA()

	t.Cleanup(func() {
		_, _ = model.Pool.Exec(ctx, `DELETE FROM deployments WHERE sha=$1`, sha)
	})

	// running 行建立（dispatch 后）
	CreateDeployment(ctx, repo, sha, "master-"+sha[:8], "manual", "tester")
	var status string
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT status FROM deployments WHERE sha=$1 AND mode='manual'`, sha).Scan(&status))
	assert.Equal(t, "running", status)

	// 在途互斥：running 存在时 HasInFlight=true
	assert.True(t, HasInFlightDeployment(ctx))

	// deploy job 成功回流 → success + deployed_at
	CompleteRunningDeployment(ctx, "success")
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT status FROM deployments WHERE sha=$1`, sha).Scan(&status))
	assert.Equal(t, "success", status)
	assert.False(t, HasInFlightDeployment(ctx), "success 后不应再有 in-flight")

	var dAt *time.Time
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT deployed_at FROM deployments WHERE sha=$1`, sha).Scan(&dAt))
	assert.NotNil(t, dAt, "success 行应回填 deployed_at")

	// summary.deployed 读到该行
	code, out := getJSON(t, newPipelineApp(t), "/api/v1/pipelines/summary")
	require.Equal(t, 200, code)
	dep := out["data"].(map[string]any)["deployed"]
	if dep != nil {
		assert.Equal(t, "master-"+sha[:8], dep.(map[string]any)["tag"])
	}

	_, _ = model.Pool.Exec(ctx, `DELETE FROM deployments WHERE sha=$1`, sha)
}

func TestDeployment_FailureKeepsBanner(t *testing.T) {
	newPipelineApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()

	oldSha := randSHA()
	seedDeployment(t, "forja-admin/forja", oldSha, "master-"+oldSha[:8], "success", "manual", "admin")

	// 新版本部署失败
	newSha := randSHA()
	CreateDeployment(ctx, "forja-admin/forja", newSha, "master-"+newSha[:8], "manual", "admin")
	CompleteRunningDeployment(ctx, "failure")

	var st string
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT status FROM deployments WHERE sha=$1`, newSha).Scan(&st))
	assert.Equal(t, "failed", st)

	// 横幅查询语义：failed 行永不入选（与 summary 同 SQL）
	var bannerTag string
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT tag FROM deployments WHERE status='success' ORDER BY deployed_at DESC NULLS LAST LIMIT 1`).Scan(&bannerTag))
	assert.NotEqual(t, "master-"+newSha[:8], bannerTag, "失败部署不得成为线上版本")
}

func TestDeployment_InFlightMutex409(t *testing.T) {
	app := newPipelineApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	startMockGitea(t)
	ctx := context.Background()
	head := randSHA()

	built := seedRun(t, "", "SUCCESS", [][2]string{{"backend", "success"}})
	_, err := model.Pool.Exec(ctx,
		`UPDATE pipeline_runs SET repo='forja-admin/forja', sha=$2 WHERE id=$1`, built.ID, head)
	require.NoError(t, err)

	doPost := func(body string) (int, map[string]any) {
		req := httptest.NewRequest("POST", "/api/v1/internal/gitea/dispatch", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, e := app.Test(req, 15000)
		require.NoError(t, e)
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}

	code, _ := doPost(`{"kind":"deploy","sha":"` + head + `"}`)
	require.Equal(t, 200, code, "首次部署应放行")

	code, out := doPost(`{"kind":"deploy","sha":"` + head + `"}`)
	assert.Equal(t, 409, code, "在途部署存在时应 409: %v", out)

	// 回流成功后互斥解除
	CompleteRunningDeployment(ctx, "success")
	code, _ = doPost(`{"kind":"deploy","sha":"` + head + `"}`)
	assert.Equal(t, 200, code)
	CompleteRunningDeployment(ctx, "success")

	_, _ = model.Pool.Exec(ctx,
		`DELETE FROM deployments WHERE sha=$1; DELETE FROM pipeline_runs WHERE id=$2`, head, built.ID)
}

// TestDeployment_ReapStaleRunning running 超 10 分钟判 failed——否则永久卡死互斥
func TestDeployment_ReapStaleRunning(t *testing.T) {
	newPipelineApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	stale := randSHA()
	id := seedDeployment(t, "forja-admin/forja", stale, "master-"+stale[:8], "running", "manual", "tester")
	_, err := model.Pool.Exec(ctx,
		`UPDATE deployments SET created_at=now()-interval '15 minutes' WHERE id=$1`, id)
	require.NoError(t, err)

	assert.True(t, HasInFlightDeployment(ctx) == false || true) // reap 先行，不依赖返回值
	var st string
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT status FROM deployments WHERE id=$1`, id).Scan(&st))
	assert.Equal(t, "failed", st, "僵尸 running 应被 reap 为 failed")
	assert.False(t, HasInFlightDeployment(ctx), "reap 后互斥应解除")
}

func TestWebhook_DocsOnlyPushNoPending(t *testing.T) {
	ensureTestDB(t)
	if model.Pool == nil {
		t.Skip("db unavailable")
	}
	t.Setenv("GITEA_WEBHOOK_SECRET", hookSecret)
	app := newHookApp()
	ctx := context.Background()

	docsRepo := fmt.Sprintf("forja-admin/hook-%s", uuid.NewString()[:8])
	docsSHA := uuid.NewString() + uuid.NewString()
	docsBody := fmt.Sprintf(`{"ref":"refs/heads/master","after":%q,"repository":{"full_name":%q},"commits":[{"modified":["docs/guide/x.md"],"added":["README.md"]}]}`, docsSHA[:64], docsRepo)
	code, _ := postHook(t, app, "pl-docs-"+uuid.NewString()[:8], sign(t, hookSecret, []byte(docsBody)), "push", docsBody)
	require.Equal(t, 200, code)

	time.Sleep(300 * time.Millisecond)
	var cnt int64
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM pipeline_runs WHERE repo=$1 AND sha=$2 AND event='push'`, docsRepo, docsSHA[:64]).Scan(&cnt))
	assert.Equal(t, int64(0), cnt, "docs-only push 不应建 PENDING 行")

	// 混合代码路径照常建行
	codeRepo := fmt.Sprintf("forja-admin/hook-%s", uuid.NewString()[:8])
	codeSHA := uuid.NewString() + uuid.NewString()
	mixedBody := fmt.Sprintf(`{"ref":"refs/heads/master","after":%q,"repository":{"full_name":%q},"commits":[{"modified":["docs/a.md","backend/main.go"]}]}`, codeSHA[:64], codeRepo)
	code, _ = postHook(t, app, "pl-mixed-"+uuid.NewString()[:8], sign(t, hookSecret, []byte(mixedBody)), "push", mixedBody)
	require.Equal(t, 200, code)

	time.Sleep(300 * time.Millisecond)
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM pipeline_runs WHERE repo=$1 AND sha=$2 AND event='push'`, codeRepo, codeSHA[:64]).Scan(&cnt))
	assert.Equal(t, int64(1), cnt, "含代码变更的 push 应建 PENDING 行")
}
