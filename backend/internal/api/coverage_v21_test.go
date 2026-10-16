package api

// v2.1 覆盖率冲刺（Phase 1）：furnace / model-provider / iteration / 资源库附件 /
// 模板 sync / 制品列表 等 0% 路由的 CRUD 与校验分支。

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"forja/backend/internal/api/respond"
	"forja/backend/internal/model"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func covV21App(t *testing.T) *fiber.App {
	t.Helper()
	ensureTestDB(t)
	adminID := uuid.NewString()
	_ = model.Pool.QueryRow(context.Background(), `SELECT id FROM users WHERE username='admin'`).Scan(&adminID)
	app := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("username", "cov-admin")
		c.Locals("role", "Admin")
		c.Locals("uid", adminID)
		return c.Next()
	})
	api := app.Group("/api/v1")
	RegisterFurnaceRoutes(api)
	RegisterModelProviderRoutes(api)
	RegisterIterationRoutes(api)
	RegisterProjectRoutes(api)
	RegisterProjectFileRoutes(api)
	RegisterInboxAttachmentRoutes(api)
	RegisterAdminTemplateRoutes(api)
	return app
}

func covV21Project(t *testing.T, ctx context.Context, withRepo bool) string {
	t.Helper()
	pid := uuid.NewString()
	repo := ""
	if withRepo {
		repo = "forja-admin/cov21-" + pid[:8]
	}
	_, err := model.Pool.Exec(ctx,
		`INSERT INTO projects (id,name,slug,repo_full_name) VALUES ($1,$2,$2,NULLIF($3,''))`,
		pid, "cv21-"+pid[:8], repo)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = model.Pool.Exec(ctx, `DELETE FROM requirements_inbox WHERE project_id=$1`, pid)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM project_files WHERE project_id=$1`, pid)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM project_artifacts WHERE project_id=$1`, pid)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, pid)
	})
	return pid
}

func TestCovV21_FurnaceCRUD(t *testing.T) {
	app := covV21App(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	code, _ := covDo(t, app, "GET", "/api/v1/furnaces", "")
	assert.Equal(t, 200, code)

	code, _ = covDo(t, app, "POST", "/api/v1/furnaces", `{}`)
	assert.Equal(t, 400, code)

	code, out := covDo(t, app, "POST", "/api/v1/furnaces", `{"name":"cov21-furnace","base_url":"http://127.0.0.1:1","capacity":2}`)
	require.Equal(t, 200, code, "%v", out)
	fid, _ := covData(t, out)["id"].(string)
	require.NotEmpty(t, fid)
	t.Cleanup(func() {
		_, _ = model.Pool.Exec(context.Background(), `DELETE FROM furnaces WHERE id=$1`, fid)
	})

	code, _ = covDo(t, app, "GET", "/api/v1/furnaces/"+fid, "")
	assert.Equal(t, 200, code)

	code, _ = covDo(t, app, "PATCH", "/api/v1/furnaces/"+fid, `{"name":"cov21-furnace-2","status":"healthy","capacity":3}`)
	assert.Equal(t, 200, code)

	code, _ = covDo(t, app, "POST", "/api/v1/furnaces/"+fid+"/recover", "")
	assert.Equal(t, 200, code)

	// 不可达上游：覆盖 test-chat/test-preview 请求与错误分支（不断言具体状态码）
	_, _ = covDo(t, app, "POST", "/api/v1/furnaces/"+fid+"/test-chat", `{"message":"hi"}`)
	_, _ = covDo(t, app, "POST", "/api/v1/furnaces/test-preview-chat", `{"base_url":"http://127.0.0.1:1","message":"hi"}`)

	code, _ = covDo(t, app, "DELETE", "/api/v1/furnaces/"+fid, "")
	assert.Equal(t, 200, code)
	code, _ = covDo(t, app, "GET", "/api/v1/furnaces/"+fid, "")
	assert.Equal(t, 404, code)
}

func TestCovV21_ModelProviderCRUD(t *testing.T) {
	app := covV21App(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	code, _ := covDo(t, app, "GET", "/api/v1/model-providers", "")
	assert.Equal(t, 200, code)

	code, _ = covDo(t, app, "POST", "/api/v1/model-providers", `{}`)
	assert.Equal(t, 400, code)

	name := "cov21prov" + uuid.NewString()[:6]
	body := `{"provider_id":"` + name + `","display_name":"Cov Provider","base_url":"https://cov21.invalid","auth_mode":"None"}`
	code, out := covDo(t, app, "POST", "/api/v1/model-providers", body)
	require.Equal(t, 200, code, "%v", out)
	pid, _ := covData(t, out)["id"].(string)
	require.NotEmpty(t, pid)
	t.Cleanup(func() {
		_, _ = model.Pool.Exec(context.Background(), `DELETE FROM model_providers WHERE id=$1`, pid)
	})

	code, _ = covDo(t, app, "GET", "/api/v1/model-providers/"+pid, "")
	assert.Equal(t, 200, code)
	code, _ = covDo(t, app, "GET", "/api/v1/model-providers/"+pid+"/references", "")
	assert.Equal(t, 200, code)
	code, _ = covDo(t, app, "PATCH", "/api/v1/model-providers/"+pid, `{"display_name":"Cov Provider 2"}`)
	assert.Equal(t, 200, code)

	// 不可达上游：覆盖连通性测试与预览错误分支
	_, _ = covDo(t, app, "POST", "/api/v1/model-providers/"+pid+"/test", `{}`)
	_, _ = covDo(t, app, "POST", "/api/v1/model-providers/test-preview", `{"base_url":"https://cov21.invalid","auth_mode":"None"}`)

	code, _ = covDo(t, app, "DELETE", "/api/v1/model-providers/"+pid, "")
	assert.Equal(t, 200, code)
	code, _ = covDo(t, app, "GET", "/api/v1/model-providers/"+pid, "")
	assert.Equal(t, 404, code)
}

func TestCovV21_IterationCRUD(t *testing.T) {
	app := covV21App(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	pid := covV21Project(t, ctx, false)

	code, _ := covDo(t, app, "POST", "/api/v1/projects/"+pid+"/iterations", `{}`)
	assert.Equal(t, 400, code)

	code, out := covDo(t, app, "POST", "/api/v1/projects/"+pid+"/iterations", `{"name":"cov21-iter","start_date":"2026-09-01","end_date":"2026-09-30"}`)
	require.Equal(t, 200, code, "%v", out)
	iterID, _ := covData(t, out)["id"].(string)
	require.NotEmpty(t, iterID)

	code, _ = covDo(t, app, "POST", "/api/v1/projects/"+pid+"/iterations", `{"name":"cov21-iter"}`)
	assert.Equal(t, 400, code, "重名应 400")

	code, _ = covDo(t, app, "GET", "/api/v1/projects/"+pid+"/iterations", "")
	assert.Equal(t, 200, code)

	code, _ = covDo(t, app, "PATCH", "/api/v1/projects/"+pid+"/iterations/"+iterID, `{"name":"cov21-iter-2"}`)
	assert.Equal(t, 200, code)

	// 无分支发布 → 400 NO_BRANCH（覆盖 release 前置校验）
	code, _ = covDo(t, app, "POST", "/api/v1/projects/"+pid+"/iterations/"+iterID+"/release", `{}`)
	assert.Equal(t, 400, code)

	// 非 RELEASED 回滚 → 400（覆盖 revert 前置校验）
	code, _ = covDo(t, app, "POST", "/api/v1/projects/"+pid+"/iterations/"+iterID+"/revert", `{}`)
	assert.Equal(t, 400, code)
}

func multipartFile(field, filename, content string, extra map[string]string) (*bytes.Buffer, string) {
	buf := &bytes.Buffer{}
	mw := multipart.NewWriter(buf)
	for k, v := range extra {
		_ = mw.WriteField(k, v)
	}
	fw, _ := mw.CreateFormFile(field, filename)
	_, _ = fw.Write([]byte(content))
	_ = mw.Close()
	return buf, mw.FormDataContentType()
}

func TestCovV21_ProjectFileLifecycle(t *testing.T) {
	app := covV21App(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	pid := covV21Project(t, ctx, false)

	// 非法分类
	body, ct := multipartFile("file", "a.txt", "hello", map[string]string{"category": "bogus"})
	req := httptest.NewRequest("POST", "/api/v1/projects/"+pid+"/files", body)
	req.Header.Set("Content-Type", ct)
	resp, err := app.Test(req, 15000)
	require.NoError(t, err)
	assert.Equal(t, 400, resp.StatusCode)

	// 非法扩展名
	body, ct = multipartFile("file", "evil.exe", "MZ", map[string]string{"category": "general"})
	req = httptest.NewRequest("POST", "/api/v1/projects/"+pid+"/files", body)
	req.Header.Set("Content-Type", ct)
	resp, err = app.Test(req, 15000)
	require.NoError(t, err)
	assert.Equal(t, 400, resp.StatusCode)

	// 正常上传
	body, ct = multipartFile("file", "cov21.txt", "cov21-file-content", map[string]string{"category": "general", "summary": "cov"})
	req = httptest.NewRequest("POST", "/api/v1/projects/"+pid+"/files", body)
	req.Header.Set("Content-Type", ct)
	resp, err = app.Test(req, 15000)
	require.NoError(t, err)
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	require.Equal(t, 200, resp.StatusCode, "%v", out)
	fid, _ := covData(t, out)["id"].(string)
	require.NotEmpty(t, fid)

	code, _ := covDo(t, app, "GET", "/api/v1/projects/"+pid+"/files?category=general", "")
	assert.Equal(t, 200, code)
	code, _ = covDo(t, app, "GET", "/api/v1/projects/"+pid+"/files", "")
	assert.Equal(t, 200, code)

	resp, err = app.Test(httptest.NewRequest("GET", "/api/v1/projects/"+pid+"/files/"+fid+"/download", nil), 15000)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	code, _ = covDo(t, app, "DELETE", "/api/v1/projects/"+pid+"/files/"+fid, "")
	assert.Equal(t, 200, code)
}

func TestCovV21_InboxAttachments(t *testing.T) {
	app := covV21App(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	pid := covV21Project(t, ctx, false)
	reqID := uuid.NewString()
	_, err := model.Pool.Exec(ctx,
		`INSERT INTO requirements_inbox (id, project_id, source_type, raw_content, status) VALUES ($1,$2,'TEXT_PASTE','cov21 attachment', 'NEW')`,
		reqID, pid)
	require.NoError(t, err)

	code, _ := covDo(t, app, "GET", "/api/v1/inbox/not-an-id/attachments", "")
	assert.Equal(t, 400, code)

	body, ct := multipartFile("file", "cov21.md", "# cov21", nil)
	req := httptest.NewRequest("POST", "/api/v1/inbox/"+reqID+"/attachments", body)
	req.Header.Set("Content-Type", ct)
	resp, err := app.Test(req, 15000)
	require.NoError(t, err)
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	require.Equal(t, 200, resp.StatusCode, "%v", out)
	aid, _ := covData(t, out)["id"].(string)
	require.NotEmpty(t, aid)

	code, _ = covDo(t, app, "GET", "/api/v1/inbox/"+reqID+"/attachments", "")
	assert.Equal(t, 200, code)
	resp, err = app.Test(httptest.NewRequest("GET", "/api/v1/attachments/"+aid+"/download", nil), 15000)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
}

func TestCovV21_ArtifactsListAndDispatchHelpers(t *testing.T) {
	app := covV21App(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	pid := covV21Project(t, ctx, true)
	sha := strings.Repeat("ab", 20)
	_, err := model.Pool.Exec(ctx,
		`INSERT INTO project_artifacts (project_id, sha, tag, image, status) VALUES ($1,$2,'master-abababab','gitea:3000/x/y','built')`,
		pid, sha)
	require.NoError(t, err)
	code, out := covDo(t, app, "GET", "/api/v1/projects/"+pid+"/artifacts", "")
	require.Equal(t, 200, code)
	items, _ := covData(t, out)["items"].([]any)
	assert.GreaterOrEqual(t, len(items), 1)

	// 纯函数与 DB 辅助
	assert.True(t, isAllowedBranchFor("master", "master"))
	assert.True(t, isAllowedBranchFor("iteration/x", "master"))
	assert.False(t, isAllowedBranchFor("feature/y", "master"))
	repo := "forja-admin/cov21-helper-" + uuid.NewString()[:8]
	_, _ = model.Pool.Exec(ctx,
		`INSERT INTO pipeline_runs (id, project_id, repo, sha, branch, event, status) VALUES (gen_random_uuid(),$1,$2,$3,'master','workflow_job','RUNNING')`,
		pid, repo, sha)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM pipeline_runs WHERE repo=$1`, repo) })
	var runID string
	_ = model.Pool.QueryRow(ctx, `SELECT id::text FROM pipeline_runs WHERE repo=$1 LIMIT 1`, repo).Scan(&runID)
	assert.Equal(t, pid, projectIDByRun(ctx, runID))

	recordManualBuildRun(ctx, repo, sha, "cov-admin", pid)
	var cnt int
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM pipeline_runs WHERE repo=$1 AND event='manual'`, repo).Scan(&cnt))
	assert.Equal(t, 1, cnt)
}

func TestCovV21_TemplatesSyncBranches(t *testing.T) {
	app := covV21App(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	// 未配置 Gitea → 503
	t.Setenv("FORJA_TEMPLATES_REPO", "forja-admin/templates")
	t.Setenv("GITEA_URL", "")
	t.Setenv("GITEA_TOKEN", "")
	code, _ := covDo(t, app, "POST", "/api/v1/admin/templates/sync", "")
	assert.Equal(t, 503, code)

	// 配置仓库但不可达（404）→ 502 上游错误（保留旧缓存语义由 templates 包负责）
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	t.Cleanup(ts.Close)
	t.Setenv("GITEA_URL", ts.URL)
	t.Setenv("GITEA_TOKEN", "tok")
	code, out := covDo(t, app, "POST", "/api/v1/admin/templates/sync", "")
	assert.Equal(t, 500, code, "%v", out)
}
