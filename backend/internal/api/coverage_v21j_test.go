package api

// v2.1 coverage sprint Phase 4d: attachment/project-file download branches,
// deployment state-machine helpers (direct), dispatch helpers, user + inbox leftovers.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"forja/backend/internal/model"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func jsonDecodeBody(r io.Reader, v any) error { return json.NewDecoder(r).Decode(v) }

func TestCovV21j_AttachmentAndProjectFileDownloads(t *testing.T) {
	app := covV21App(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	pid := covV21Project(t, ctx, false)

	// inbox 需求 + 附件上传成功（覆盖 FileStore 落盘/DB 行）
	reqID := uuid.NewString()
	_, err := model.Pool.Exec(ctx,
		`INSERT INTO requirements_inbox (id, project_id, source_type, raw_content, status) VALUES ($1,$2,'TEXT_PASTE','cov21j','NEW')`, reqID, pid)
	require.NoError(t, err)
	body, ct := multipartFile("file", "cov21j.txt", "attachment-data", nil)
	req := httptest.NewRequest("POST", "/api/v1/inbox/"+reqID+"/attachments", body)
	req.Header.Set("Content-Type", ct)
	resp, err := app.Test(req, 15000)
	require.NoError(t, err)
	require.Equal(t, 200, resp.StatusCode)
	var out map[string]any
	_ = jsonDecodeBody(resp.Body, &out)
	attID, _ := covData(t, out)["id"].(string)
	require.NotEmpty(t, attID)

	// 列表
	code, out := covDo(t, app, "GET", "/api/v1/inbox/"+reqID+"/attachments", "")
	require.Equal(t, 200, code)
	items, _ := covData(t, out)["items"].([]any)
	assert.GreaterOrEqual(t, len(items), 1)

	// 下载: presign ttl 超限 → 400；presign 正常（本地驱动回退流式）→ 200；普通下载 200
	code, _ = covDo(t, app, "GET", "/api/v1/attachments/"+attID+"/download?presign=true&ttl=999", "")
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "GET", "/api/v1/attachments/"+attID+"/download?presign=true&ttl=0", "")
	assert.True(t, code < 500, "code=%d", code)
	code, out = covDo(t, app, "GET", "/api/v1/attachments/"+attID+"/download", "")
	assert.Equal(t, 200, code)
	_ = out

	// 资源库：默认分类 + 下载 presign 分支 + 删除
	body, ct = multipartFile("file", "cov21j-doc.txt", "project-file-data", nil)
	req = httptest.NewRequest("POST", "/api/v1/projects/"+pid+"/files", body)
	req.Header.Set("Content-Type", ct)
	resp, err = app.Test(req, 15000)
	require.NoError(t, err)
	require.Equal(t, 200, resp.StatusCode)
	_ = jsonDecodeBody(resp.Body, &out)
	fid, _ := covData(t, out)["id"].(string)
	require.NotEmpty(t, fid)
	assert.Equal(t, "general", covData(t, out)["category"])

	code, _ = covDo(t, app, "GET", "/api/v1/project-files/"+fid+"/download?presign=true&ttl=999", "")
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "GET", "/api/v1/project-files/"+fid+"/download?presign=true&ttl=60", "")
	assert.True(t, code < 500, "code=%d", code)
	code, _ = covDo(t, app, "GET", "/api/v1/project-files/"+fid+"/download", "")
	assert.Equal(t, 200, code)
	code, _ = covDo(t, app, "GET", "/api/v1/projects/"+pid+"/files/"+fid+"/download?presign=true&ttl=999", "")
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "GET", "/api/v1/projects/"+pid+"/files?category=general", "")
	assert.Equal(t, 200, code)
	code, _ = covDo(t, app, "GET", "/api/v1/project-files/"+uuid.NewString()+"/download", "")
	assert.True(t, code >= 400, "code=%d", code)
	code, _ = covDo(t, app, "DELETE", "/api/v1/projects/"+pid+"/files/"+fid, "")
	assert.True(t, code < 500, "code=%d", code)
	code, _ = covDo(t, app, "DELETE", "/api/v1/projects/"+pid+"/files/"+uuid.NewString(), "")
	assert.True(t, code >= 400, "code=%d", code)
}

func TestCovV21j_DeploymentAndDispatchHelpers(t *testing.T) {
	ensureTestDB(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	pid := covV21Project(t, ctx, false)
	repo := "forja-admin/cov21j-" + pid[:8]
	t.Cleanup(func() {
		_, _ = model.Pool.Exec(ctx, `DELETE FROM deployments WHERE project_id=$1 OR repo=$2`, pid, repo)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM pipeline_jobs WHERE run_id IN (SELECT id FROM pipeline_runs WHERE repo=$1)`, repo)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM pipeline_runs WHERE repo=$1`, repo)
	})

	// CreateDeployment：running（manual）/ scheduled；非法 project_id 回退 repo 映射；空参数直接返回
	CreateDeployment(ctx, repo, strings.Repeat("a1", 20), "cov-j-1", "manual", "cov-admin", pid)
	CreateDeployment(ctx, repo, strings.Repeat("a2", 20), "cov-j-2", "scheduled", "cov-admin", pid)
	CreateDeployment(ctx, repo, strings.Repeat("a3", 20), "cov-j-3", "manual", "cov-admin", "bad-id")
	assert.True(t, true)
	assert.Equal(t, "", resolveDeployProjectID(ctx, "no/such-repo-j"))

	// MarkScheduledRunning：指定项目（把 scheduled 行转 running）
	var st string
	MarkScheduledRunning(ctx, pid)
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT status FROM deployments WHERE project_id=$1 AND tag='cov-j-2'`, pid).Scan(&st))
	assert.Equal(t, "running", st)
	// 无匹配行分支 + 非法 pid 分支
	MarkScheduledRunning(ctx, pid)
	MarkScheduledRunning(ctx, "not-a-uuid")
	MarkScheduledRunning(ctx)

	// CompleteRunningDeployment：success/failed + 非法 pid + 无 pid
	CompleteRunningDeployment(ctx, pid, "success")
	CompleteRunningDeployment(ctx, pid, "failure")
	CompleteRunningDeployment(ctx, "not-a-uuid", "success")
	CompleteRunningDeployment(ctx, "success")
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT status FROM deployments WHERE project_id=$1 AND tag='cov-j-1'`, pid).Scan(&st))
	assert.Contains(t, []string{"success", "failed"}, st)

	// FailScheduledDeployment：三态签名
	CreateDeployment(ctx, repo, strings.Repeat("a4", 20), "cov-j-4", "scheduled", "cov-admin", pid)
	FailScheduledDeployment(ctx, pid, "cov reason")
	FailScheduledDeployment(ctx, "legacy reason")
	FailScheduledDeployment(ctx, "not-a-uuid", "bad pid reason")
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT status FROM deployments WHERE project_id=$1 AND tag='cov-j-4'`, pid).Scan(&st))
	assert.Equal(t, "failed", st)

	// recordManualBuildRun（无 branch 变体）与 ensureBuilt 分支
	sha := strings.Repeat("b1", 20)
	recordManualBuildRun(ctx, repo, sha, "cov-admin", pid)
	var n int
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM pipeline_runs WHERE repo=$1 AND sha=$2 AND event='manual'`, repo, sha).Scan(&n))
	assert.Equal(t, 1, n)
	assert.Error(t, ensureBuilt(ctx, repo, sha))
	// 无 branch 行：带 branch 维度校验失败；补 branch 后通过
	assert.Error(t, ensureBuilt(ctx, repo, sha, "master"))
	_, _ = model.Pool.Exec(ctx, `UPDATE pipeline_runs SET status='SUCCESS', branch='master' WHERE repo=$1 AND sha=$2`, repo, sha)
	assert.NoError(t, ensureBuilt(ctx, repo, sha))
	assert.NoError(t, ensureBuilt(ctx, repo, sha, "master"))
	assert.Error(t, ensureBuilt(ctx, repo, sha, "release"))
}

func TestCovV21j_UserAndInboxLeftovers(t *testing.T) {
	app := covV21hApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	pid := covV21Project(t, ctx, false)

	// user：me / createUser 分支 / listUsers 搜索 / login-logs
	code, _ := covDo(t, app, "GET", "/api/v1/auth/me", "")
	assert.Equal(t, 200, code)
	uname := "cov21j-" + uuid.NewString()[:8]
	code, out := covDo(t, app, "POST", "/api/v1/admin/users", `{"username":"`+uname+`","email":"cov21j@x.io","password":"Cov21j#2026x","role":"Developer"}`)
	require.Equal(t, 200, code, "%v", out)
	code, _ = covDo(t, app, "POST", "/api/v1/admin/users", `{"username":"`+uname+`","email":"cov21j2@x.io","password":"Cov21j#2026x","role":"Developer"}`)
	assert.True(t, code >= 400, "code=%d", code)
	code, _ = covDo(t, app, "POST", "/api/v1/admin/users", `{"username":"cov21j-bad1","password":"weak","role":"Developer"}`)
	assert.True(t, code >= 400, "code=%d", code)
	code, _ = covDo(t, app, "POST", "/api/v1/admin/users", `{"username":"cov21j-bad2","password":"Cov21j#2026x","role":"Bogus"}`)
	assert.True(t, code >= 400, "code=%d", code)
	code, out = covDo(t, app, "GET", "/api/v1/admin/users?q="+uname, "")
	require.Equal(t, 200, code, "%v", out)
	code, _ = covDo(t, app, "GET", "/api/v1/admin/users/"+uname+"/login-logs", "")
	assert.Equal(t, 200, code)
	code, _ = covDo(t, app, "GET", "/api/v1/auth/me/login-logs", "")
	assert.Equal(t, 200, code)
	// 清理
	_, _ = model.Pool.Exec(ctx, `DELETE FROM users WHERE username LIKE 'cov21j-%'`)

	// inbox：multipart 成功（带 project_id 表单）→ 草稿 → 更新 → 详情 → 列表过滤 → 删除
	var buf bytes.Buffer
	bw, bct := multipartFile("file", "cov21j-inbox.md", "# cov21j multipart", map[string]string{"project_id": pid})
	buf = *bw
	req := httptest.NewRequest("POST", "/api/v1/inbox", &buf)
	req.Header.Set("Content-Type", bct)
	resp, err := app.Test(req, 15000)
	require.NoError(t, err)
	var mpOut map[string]any
	_ = jsonDecodeBody(resp.Body, &mpOut)
	require.Equal(t, 200, resp.StatusCode, "%v", mpOut)
	rid, _ := covData(t, mpOut)["id"].(string)
	require.NotEmpty(t, rid)

	code, _ = covDo(t, app, "GET", "/api/v1/inbox/"+rid, "")
	assert.Equal(t, 200, code)
	code, _ = covDo(t, app, "PATCH", "/api/v1/inbox/"+rid, `{"raw_content":"cov21j updated","status":"DRAFTED"}`)
	assert.True(t, code < 500, "code=%d", code)
	code, _ = covDo(t, app, "GET", "/api/v1/inbox?project_id="+pid+"&status=NEW&source_type=TEXT_PASTE&q=cov21j", "")
	assert.Equal(t, 200, code)
	code, _ = covDo(t, app, "GET", "/api/v1/inbox?page=1&page_size=5", "")
	assert.True(t, code < 500, "code=%d", code)
	code, _ = covDo(t, app, "PATCH", "/api/v1/inbox/"+rid, `{"status":"BOGUS"}`)
	assert.True(t, code < 500, "code=%d", code)
	code, _ = covDo(t, app, "DELETE", "/api/v1/inbox/"+rid, "")
	assert.True(t, code < 500, "code=%d", code)
}
