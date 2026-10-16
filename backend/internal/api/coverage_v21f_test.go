package api

// v2.1 覆盖率冲刺 Phase 3c：pipeline_store 直测（run/job 状态机、僵尸清理、回滚）+
// 用户/项目/规格/收件箱/交付负面分支。

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"forja/backend/internal/api/respond"
	"forja/backend/internal/model"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCovV21f_PipelineStoreStateMachine(t *testing.T) {
	ensureTestDB(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	pid := covV21Project(t, ctx, false)
	repo := "forja-admin/cov21f-" + pid[:8]
	t.Cleanup(func() {
		_, _ = model.Pool.Exec(ctx, `DELETE FROM pipeline_jobs WHERE run_id IN (SELECT id FROM pipeline_runs WHERE repo=$1)`, repo)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM pipeline_runs WHERE repo=$1`, repo)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM deployments WHERE project_id=$1`, pid)
	})

	// projectIDByRun：空/非法/不存在/命中
	assert.Equal(t, "", projectIDByRun(ctx, ""))
	assert.Equal(t, "", projectIDByRun(ctx, "bad-id"))
	assert.Equal(t, "", projectIDByRun(ctx, uuid.NewString()))
	sha := strings.Repeat("1a", 20)
	runID := uuid.NewString()
	_, err := model.Pool.Exec(ctx,
		`INSERT INTO pipeline_runs (id, project_id, repo, sha, branch, event, status) VALUES ($1,$2,$3,$4,'master','push','PENDING')`,
		runID, pid, repo, sha)
	require.NoError(t, err)
	assert.Equal(t, pid, projectIDByRun(ctx, runID))

	// resolveDeployProjectID：running 部署优先，否则回退 repo 映射
	_, err = model.Pool.Exec(ctx,
		`INSERT INTO deployments (id, project_id, repo, sha, tag, status) VALUES (gen_random_uuid(),$1,$2,$3,'cov-tag','running')`,
		pid, repo, strings.Repeat("aa", 20))
	require.NoError(t, err)
	assert.Equal(t, pid, resolveDeployProjectID(ctx, "no/such-repo"))

	// recordPushRun 幂等 upsert（同锚点更新）+ 空分支（NULL branch 独立行）
	recordPushRun(ctx, repo, sha, "master", "cov")
	recordPushRun(ctx, repo, sha, "master", "")
	recordPushRun(ctx, repo, strings.Repeat("aa", 20), "", "")
	// push 行存在时 workflow_job 回流不新建
	recordJobCompletion(ctx, repo, sha, "backend", "success", time.Now().UTC().Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339))
	var st string
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT status FROM pipeline_runs WHERE id=$1`, runID).Scan(&st))
	assert.Equal(t, "SUCCESS", st, "动态 job 集：单 job success 即 SUCCESS（与用户项目单 job 构建兼容）")

	// progress：deploy 跳过 / 正常 job（无效 startedAt 走 now 回退）
	recordJobProgress(ctx, repo, sha, "deploy", "in_progress", "bad-time")
	recordJobProgress(ctx, repo, sha, "frontend", "in_progress", "bad-time")
	recordJobProgress(ctx, repo, sha, "frontend", "in_progress", time.Now().UTC().Format(time.RFC3339))

	// 全 4 job success → SUCCESS
	for _, name := range []string{"backend", "frontend", "e2e", "build-push"} {
		recordJobCompletion(ctx, repo, sha, name, "success", "", "")
	}
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT status FROM pipeline_runs WHERE id=$1`, runID).Scan(&st))
	assert.Equal(t, "SUCCESS", st)

	// 失败聚合：任一 job failure → FAILURE
	sha2 := strings.Repeat("2b", 20)
	recordJobCompletion(ctx, repo, sha2, "build", "failure", "", "")
	var st2 string
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT status FROM pipeline_runs WHERE repo=$1 AND sha=$2 AND event='workflow_job'`, repo, sha2).Scan(&st2))
	assert.Equal(t, "FAILURE", st2)

	// ensureRunID：非法 project_id → 告警回退 repo 映射；非 master push → ""
	sha3 := strings.Repeat("3c", 20)
	got := ensureRunID(ctx, repo, sha3, "not-a-uuid")
	require.NotEmpty(t, got)
	var pid3 *string
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT project_id::text FROM pipeline_runs WHERE id=$1`, got).Scan(&pid3))
	assert.Nil(t, pid3, "非法 project_id 不应写入")

	sha4 := strings.Repeat("4d", 20)
	_, err = model.Pool.Exec(ctx,
		`INSERT INTO pipeline_runs (id, project_id, repo, sha, branch, event, status) VALUES (gen_random_uuid(),$1,$2,$3,'feature/x','push','PENDING')`,
		pid, repo, sha4)
	require.NoError(t, err)
	assert.True(t, isNonMasterPush(ctx, repo, sha4))
	assert.Equal(t, "", ensureRunID(ctx, repo, sha4))
	preseedJobsNamed(ctx, repo, sha4, "feature/x", pid, []string{"build"})
	revertFailedBuildRun(ctx, repo, sha4)

	// preseed + 带 branch 回填 + 空 projectID 的 event 发布分支
	sha5 := strings.Repeat("5e", 20)
	preseedJobsNamed(ctx, repo, sha5, "master", pid, expectedPipelineJobs)
	var cnt int
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM pipeline_jobs j JOIN pipeline_runs r ON r.id=j.run_id WHERE r.repo=$1 AND r.sha=$2`, repo, sha5).Scan(&cnt))
	assert.Equal(t, len(expectedPipelineJobs), cnt)

	// revertFailedBuildRun：有效 pid 清理 RUNNING manual 行；非法 pid 走 legacy 分支
	sha6 := strings.Repeat("6f", 20)
	manID := uuid.NewString()
	_, err = model.Pool.Exec(ctx,
		`INSERT INTO pipeline_runs (id, project_id, repo, sha, branch, event, status) VALUES ($1,$2,$3,$4,'master','manual','RUNNING')`,
		manID, pid, repo, sha6)
	require.NoError(t, err)
	_, err = model.Pool.Exec(ctx,
		`INSERT INTO pipeline_jobs (run_id, name, conclusion) VALUES ($1,'build',NULL)`, manID)
	require.NoError(t, err)
	revertFailedBuildRun(ctx, repo, sha6, pid)
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT status FROM pipeline_runs WHERE id=$1`, manID).Scan(&st))
	assert.Equal(t, "PENDING", st)

	sha7 := strings.Repeat("7a", 20)
	manID2 := uuid.NewString()
	_, err = model.Pool.Exec(ctx,
		`INSERT INTO pipeline_runs (id, project_id, repo, sha, branch, event, status) VALUES ($1,$2,$3,$4,'master','manual','RUNNING')`,
		manID2, pid, repo, sha7)
	require.NoError(t, err)
	revertFailedBuildRun(ctx, repo, sha7, "not-a-uuid")
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT status FROM pipeline_runs WHERE id=$1`, manID2).Scan(&st))
	assert.Equal(t, "PENDING", st)

	// reapStaleRuns：无 job 超 10min → SKIPPED；有 job 超 30min → FAILURE；非法 pid 告警
	sha8 := strings.Repeat("8b", 20)
	oldID := uuid.NewString()
	_, err = model.Pool.Exec(ctx,
		`INSERT INTO pipeline_runs (id, project_id, repo, sha, branch, event, status, updated_at) VALUES ($1,$2,$3,$4,'master','manual','RUNNING', now()-interval '20 minutes')`,
		oldID, pid, repo, sha8)
	require.NoError(t, err)
	sha9 := strings.Repeat("9c", 20)
	staleID := uuid.NewString()
	_, err = model.Pool.Exec(ctx,
		`INSERT INTO pipeline_runs (id, project_id, repo, sha, branch, event, status, updated_at) VALUES ($1,$2,$3,$4,'master','manual','RUNNING', now()-interval '40 minutes')`,
		staleID, pid, repo, sha9)
	require.NoError(t, err)
	_, err = model.Pool.Exec(ctx, `INSERT INTO pipeline_jobs (run_id, name, conclusion) VALUES ($1,'build','success')`, staleID)
	require.NoError(t, err)
	reapStaleRuns(ctx, pid)
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT status FROM pipeline_runs WHERE id=$1`, oldID).Scan(&st))
	assert.Equal(t, "SKIPPED", st)
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT status FROM pipeline_runs WHERE id=$1`, staleID).Scan(&st))
	assert.Equal(t, "FAILURE", st)
	reapStaleRuns(ctx, "not-a-uuid")
	reapStaleRuns(ctx)

	// aggregateRunStatus：非法 id / 不存在 run（告警分支）
	aggregateRunStatus(ctx, "bad-id")
	aggregateRunStatus(ctx, uuid.NewString())

	// resolveDeployProjectID：无 running 部署时回退 repo 映射（本项目无 repo 映射 → ""）
	_, err = model.Pool.Exec(ctx, `UPDATE deployments SET status='success' WHERE project_id=$1`, pid)
	require.NoError(t, err)
	assert.Equal(t, "", resolveDeployProjectID(ctx, repo))

	assert.True(t, isZeroSHA(""))
	assert.True(t, isZeroSHA(strings.Repeat("0", 40)))
	assert.False(t, isZeroSHA("abc"))
}

func TestCovV21f_UserProjectSpecInboxNegatives(t *testing.T) {
	app := covApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()

	// 用户：不存在 404 / 非法角色 4xx / 改密错误 4xx
	code, _ := covDo(t, app, "GET", "/api/v1/admin/users/"+uuid.NewString(), "")
	assert.True(t, code >= 400, "code=%d", code)
	code, _ = covDo(t, app, "PATCH", "/api/v1/admin/users/"+uuid.NewString(), `{"role":"Bogus"}`)
	assert.True(t, code >= 400, "code=%d", code)
	code, _ = covDo(t, app, "POST", "/api/v1/auth/change-password", `{"current_password":"wrong-current","new_password":"NewPass#12345"}`)
	assert.True(t, code >= 400, "code=%d", code)
	code, _ = covDo(t, app, "GET", "/api/v1/auth/me/login-logs", "")
	assert.Equal(t, 200, code)

	// 项目：不存在 404 / 成员管理 4xx / provision 与 restore 4xx
	code, _ = covDo(t, app, "GET", "/api/v1/projects/"+uuid.NewString(), "")
	assert.True(t, code >= 400, "code=%d", code)
	code, _ = covDo(t, app, "DELETE", "/api/v1/projects/"+uuid.NewString(), "")
	assert.True(t, code >= 400, "code=%d", code)
	code, _ = covDo(t, app, "POST", "/api/v1/projects/"+uuid.NewString()+"/restore", "")
	assert.True(t, code >= 400, "code=%d", code)
	code, _ = covDo(t, app, "POST", "/api/v1/projects/"+uuid.NewString()+"/provision", "")
	assert.True(t, code >= 400, "code=%d", code)

	pid := covV21Project(t, ctx, false)
	code, _ = covDo(t, app, "PATCH", "/api/v1/projects/"+pid+"/members/"+uuid.NewString(), `{"role":"Bogus"}`)
	assert.True(t, code >= 400, "code=%d", code)
	code, _ = covDo(t, app, "DELETE", "/api/v1/projects/"+pid+"/members/"+uuid.NewString(), "")
	assert.True(t, code >= 400, "code=%d", code)
	code, _ = covDo(t, app, "POST", "/api/v1/projects/"+pid+"/provision", "")
	assert.True(t, code < 500, "code=%d", code)

	// 规格：不存在 404 / 列表与检索 200
	code, _ = covDo(t, app, "GET", "/api/v1/specs/"+uuid.NewString(), "")
	assert.True(t, code >= 400, "code=%d", code)
	code, _ = covDo(t, app, "PUT", "/api/v1/specs/"+uuid.NewString(), `{"title":"x"}`)
	assert.True(t, code >= 400, "code=%d", code)
	code, _ = covDo(t, app, "GET", "/api/v1/specs?project_id="+pid, "")
	assert.Equal(t, 200, code)
	code, _ = covDo(t, app, "GET", "/api/v1/specs/search?q=cov&project_id="+pid, "")
	assert.True(t, code < 500, "code=%d", code)
	code, _ = covDo(t, app, "GET", "/api/v1/specs/timeline?project_id="+pid, "")
	assert.Equal(t, 200, code)
	code, _ = covDo(t, app, "POST", "/api/v1/specs/"+uuid.NewString()+"/validate", "")
	assert.True(t, code >= 400, "code=%d", code)
	code, _ = covDo(t, app, "POST", "/api/v1/specs/"+uuid.NewString()+"/embed", "")
	assert.True(t, code >= 400, "code=%d", code)
	code, _ = covDo(t, app, "POST", "/api/v1/specs/"+uuid.NewString()+"/ts-draft", "")
	assert.True(t, code >= 400, "code=%d", code)
	code, _ = covDo(t, app, "GET", "/api/v1/specs/"+uuid.NewString()+"/refs", "")
	assert.True(t, code >= 400, "code=%d", code)

	// 收件箱：非法来源 4xx / 空内容 4xx / 不存在 4xx / convert 不存在 4xx
	code, _ = covDo(t, app, "POST", "/api/v1/inbox", `{"project_id":"`+pid+`","source_type":"BOGUS","raw_content":"x"}`)
	assert.True(t, code < 500, "code=%d", code)
	code, _ = covDo(t, app, "POST", "/api/v1/inbox", `{"project_id":"`+pid+`","source_type":"TEXT_PASTE","raw_content":"   "}`)
	assert.True(t, code >= 400, "code=%d", code)
	code, _ = covDo(t, app, "POST", "/api/v1/inbox", `{"project_id":"`+uuid.NewString()+`","source_type":"TEXT_PASTE","raw_content":"x"}`)
	assert.True(t, code >= 400, "code=%d", code)
	code, _ = covDo(t, app, "GET", "/api/v1/inbox/"+uuid.NewString(), "")
	assert.True(t, code >= 400, "code=%d", code)
	code, _ = covDo(t, app, "PATCH", "/api/v1/inbox/"+uuid.NewString(), `{"raw_content":"y"}`)
	assert.True(t, code >= 400, "code=%d", code)
	code, _ = covDo(t, app, "DELETE", "/api/v1/inbox/"+uuid.NewString(), "")
	assert.True(t, code >= 400, "code=%d", code)
	code, _ = covDo(t, app, "POST", "/api/v1/inbox/convert", `{"inbox_ids":["`+uuid.NewString()+`"],"project_id":"`+pid+`"}`)
	assert.True(t, code >= 400, "code=%d", code)
	code, _ = covDo(t, app, "POST", "/api/v1/inbox/"+uuid.NewString()+"/draft", "")
	assert.True(t, code >= 400, "code=%d", code)
}

func TestCovV21f_DeliveryNegativesAndInternalToken(t *testing.T) {
	ensureTestDB(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	app := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("username", "cov-admin")
		c.Locals("role", "Admin")
		c.Locals("uid", uuid.NewString())
		return c.Next()
	})
	api := app.Group("/api/v1")
	RegisterDeliveryRoutes(api)
	RegisterInternalRoutes(api)

	pid := covV21Project(t, ctx, false)

	// 触发交付：项目不存在 / 无 RELEASED 任务
	code, _ := covDo(t, app, "POST", "/api/v1/projects/"+uuid.NewString()+"/deliveries", `{}`)
	assert.True(t, code >= 400, "code=%d", code)
	code, _ = covDo(t, app, "POST", "/api/v1/projects/"+pid+"/deliveries", `{}`)
	assert.True(t, code >= 400, "code=%d", code)

	// 下载/制品/发布说明：不存在 delivery
	code, _ = covDo(t, app, "GET", "/api/v1/projects/"+pid+"/deliveries/"+uuid.NewString()+"/download", "")
	assert.True(t, code >= 400, "code=%d", code)
	code, _ = covDo(t, app, "GET", "/api/v1/projects/"+pid+"/deliveries/"+uuid.NewString()+"/artifact", "")
	assert.True(t, code >= 400, "code=%d", code)
	code, _ = covDo(t, app, "GET", "/api/v1/projects/"+pid+"/releases/notes", "")
	assert.True(t, code < 500, "code=%d", code)
	code, _ = covDo(t, app, "GET", "/api/v1/projects/"+pid+"/deliveries?page=1", "")
	assert.Equal(t, 200, code)

	// internal 上传：未带令牌 → 401/503（CI 注入 FORJA_INTERNAL_TOKEN 时为 401）；错令牌 → 401；正确令牌但 body 非法 → 4xx
	req := httptest.NewRequest("POST", "/api/v1/internal/delivery/artifact", strings.NewReader("x"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, 15000)
	require.NoError(t, err)
	assert.Contains(t, []int{401, 503}, resp.StatusCode)

	t.Setenv("FORJA_INTERNAL_TOKEN", "cov-internal")
	req = httptest.NewRequest("POST", "/api/v1/internal/delivery/artifact", strings.NewReader("x"))
	req.Header.Set("Content-Type", "application/json")
	resp, err = app.Test(req, 15000)
	require.NoError(t, err)
	assert.Equal(t, 401, resp.StatusCode)

	req = httptest.NewRequest("POST", "/api/v1/internal/delivery/artifact", strings.NewReader("not-json"))
	req.Header.Set("X-Forja-Token", "cov-internal")
	req.Header.Set("Content-Type", "application/json")
	resp, err = app.Test(req, 15000)
	require.NoError(t, err)
	assert.True(t, resp.StatusCode >= 400 && resp.StatusCode != 401, "code=%d", resp.StatusCode)
}
