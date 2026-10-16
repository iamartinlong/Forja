package api

// v2.1 覆盖率冲刺 Phase 3b：inbox/资源库上传下载分支、task patch/release/revert 无仓路径、
// 成本迭代过滤、部署可见域、模板 sync 成功路径。

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"forja/backend/internal/api/respond"
	"forja/backend/internal/model"
	"forja/backend/internal/templates"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCovV21e_InboxAttachmentAndProjectFileBranches(t *testing.T) {
	app := covV21App(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	pid := covV21Project(t, ctx, false)

	// Inbox 附件：需求不存在 → 404
	body, ct := multipartFile("file", "a.md", "x", nil)
	req := httptest.NewRequest("POST", "/api/v1/inbox/"+uuid.NewString()+"/attachments", body)
	req.Header.Set("Content-Type", ct)
	resp, err := app.Test(req, 15000)
	require.NoError(t, err)
	assert.Equal(t, 404, resp.StatusCode)

	// 需求已 DISCARDED → 400
	discID := uuid.NewString()
	_, err = model.Pool.Exec(ctx,
		`INSERT INTO requirements_inbox (id, project_id, source_type, raw_content, status) VALUES ($1,$2,'TEXT_PASTE','x','DISCARDED')`,
		discID, pid)
	require.NoError(t, err)
	body, ct = multipartFile("file", "a.md", "x", nil)
	req = httptest.NewRequest("POST", "/api/v1/inbox/"+discID+"/attachments", body)
	req.Header.Set("Content-Type", ct)
	resp, err = app.Test(req, 15000)
	require.NoError(t, err)
	assert.Equal(t, 400, resp.StatusCode)

	// 空 multipart（无文件）→ 400
	reqID := uuid.NewString()
	_, err = model.Pool.Exec(ctx,
		`INSERT INTO requirements_inbox (id, project_id, source_type, raw_content, status) VALUES ($1,$2,'TEXT_PASTE','y','NEW')`,
		reqID, pid)
	require.NoError(t, err)
	req = httptest.NewRequest("POST", "/api/v1/inbox/"+reqID+"/attachments", strings.NewReader(""))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=zzz")
	resp, err = app.Test(req, 15000)
	require.NoError(t, err)
	assert.Equal(t, 400, resp.StatusCode)

	// 非法扩展名 → 400
	body, ct = multipartFile("file", "evil.exe", "MZ", nil)
	req = httptest.NewRequest("POST", "/api/v1/inbox/"+reqID+"/attachments", body)
	req.Header.Set("Content-Type", ct)
	resp, err = app.Test(req, 15000)
	require.NoError(t, err)
	assert.Equal(t, 400, resp.StatusCode)

	// 附件下载：不存在（uuid 合法但无行）→ 4xx / 非法 id 400
	code, _ := covDo(t, app, "GET", "/api/v1/attachments/"+uuid.NewString()+"/download", "")
	assert.Contains(t, []int{400, 404}, code)
	code, _ = covDo(t, app, "GET", "/api/v1/attachments/bad-id/download", "")
	assert.Equal(t, 400, code)

	// 资源库：缺文件 → 400
	req = httptest.NewRequest("POST", "/api/v1/projects/"+pid+"/files", strings.NewReader(""))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=zzz")
	resp, err = app.Test(req, 15000)
	require.NoError(t, err)
	assert.Equal(t, 400, resp.StatusCode)

	// 资源库：摘要超长 → 400
	long := strings.Repeat("s", 2001)
	body, ct = multipartFile("file", "ok.txt", "data", map[string]string{"category": "general", "summary": long})
	req = httptest.NewRequest("POST", "/api/v1/projects/"+pid+"/files", body)
	req.Header.Set("Content-Type", ct)
	resp, err = app.Test(req, 15000)
	require.NoError(t, err)
	assert.Equal(t, 400, resp.StatusCode)

	// 正常上传 + 三个下载分支
	body, ct = multipartFile("file", "ok.txt", "data2", map[string]string{"category": "api"})
	req = httptest.NewRequest("POST", "/api/v1/projects/"+pid+"/files", body)
	req.Header.Set("Content-Type", ct)
	resp, err = app.Test(req, 15000)
	require.NoError(t, err)
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	require.Equal(t, 200, resp.StatusCode, "%v", out)
	fid, _ := covData(t, out)["id"].(string)
	require.NotEmpty(t, fid)

	resp, err = app.Test(httptest.NewRequest("GET", "/api/v1/projects/"+pid+"/files/"+fid+"/download", nil), 15000)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	resp, err = app.Test(httptest.NewRequest("GET", "/api/v1/projects/"+uuid.NewString()+"/files/"+fid+"/download", nil), 15000)
	require.NoError(t, err)
	assert.Contains(t, []int{400, 404}, resp.StatusCode)
	code, _ = covDo(t, app, "GET", "/api/v1/projects/"+pid+"/files/bad-id/download", "")
	assert.Equal(t, 400, code)
}

func TestCovV21e_TaskPatchReleaseRevertNoRepo(t *testing.T) {
	app := covApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	pid := covV21Project(t, ctx, false) // 无仓项目：release/revert 走 forgeWorkspace=nil 路径
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM tasks WHERE project_id=$1`, pid) })

	// patchTask：未知状态静默忽略（200）/ 合法状态 200 / 不存在 404
	tid := uuid.NewString()
	_, err := model.Pool.Exec(ctx, `INSERT INTO tasks (id, project_id, title, status) VALUES ($1,$2,'cov21e patch','PENDING')`, tid, pid)
	require.NoError(t, err)
	code, _ := covDo(t, app, "PATCH", "/api/v1/tasks/"+tid, `{"status":"BOGUS"}`)
	assert.Equal(t, 200, code)
	code, _ = covDo(t, app, "PATCH", "/api/v1/tasks/"+tid, `{"status":"FAILED"}`)
	assert.Equal(t, 200, code)
	code, _ = covDo(t, app, "PATCH", "/api/v1/tasks/"+uuid.NewString(), `{"status":"PENDING"}`)
	assert.Equal(t, 404, code)

	// releaseTask：RELEASE_PENDING + branch=默认分支 → 跳过合并，RELEASED
	tid2 := uuid.NewString()
	_, err = model.Pool.Exec(ctx,
		`INSERT INTO tasks (id, project_id, title, status, branch, commit_sha) VALUES ($1,$2,'cov21e rel','RELEASE_PENDING','master',$3)`,
		tid2, pid, strings.Repeat("ab", 20))
	require.NoError(t, err)
	code, out := covDo(t, app, "POST", "/api/v1/tasks/"+tid2+"/release", `{}`)
	assert.Equal(t, 200, code, "%v", out)

	// releaseTask：RELEASE_PENDING + 非默认分支但无仓 → 跳过合并仍 RELEASED
	tid3 := uuid.NewString()
	_, err = model.Pool.Exec(ctx,
		`INSERT INTO tasks (id, project_id, title, status, branch) VALUES ($1,$2,'cov21e rel2','RELEASE_PENDING','forge/task-x-a1')`,
		tid3, pid)
	require.NoError(t, err)
	code, out = covDo(t, app, "POST", "/api/v1/tasks/"+tid3+"/release", `{}`)
	assert.Equal(t, 200, code, "%v", out)

	// revertTask：RELEASED 无 commit_sha → 400；RELEASED 有 sha 无仓 → 200
	tid4 := uuid.NewString()
	_, err = model.Pool.Exec(ctx, `INSERT INTO tasks (id, project_id, title, status) VALUES ($1,$2,'cov21e rev','RELEASED')`, tid4, pid)
	require.NoError(t, err)
	code, _ = covDo(t, app, "POST", "/api/v1/tasks/"+tid4+"/revert", `{}`)
	assert.Equal(t, 400, code)

	tid5 := uuid.NewString()
	_, err = model.Pool.Exec(ctx,
		`INSERT INTO tasks (id, project_id, title, status, commit_sha) VALUES ($1,$2,'cov21e rev2','RELEASED',$3)`,
		tid5, pid, strings.Repeat("cd", 20))
	require.NoError(t, err)
	code, out = covDo(t, app, "POST", "/api/v1/tasks/"+tid5+"/revert", `{}`)
	assert.Equal(t, 200, code, "%v", out)

	// forgeWorkspace：有仓但不可达 → 空工作区
	pidRepo := covV21Project(t, ctx, true)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM tasks WHERE project_id=$1`, pidRepo) })
	tid6 := uuid.NewString()
	_, err = model.Pool.Exec(ctx, `INSERT INTO tasks (id, project_id, title, status) VALUES ($1,$2,'cov21e ws','PENDING')`, tid6, pidRepo)
	require.NoError(t, err)
	isolateGitProxy(t)
	t.Setenv("GITEA_URL", "http://127.0.0.1:1")
	t.Setenv("GITEA_TOKEN", "tok")
	wd, repo := forgeWorkspace(ctx, tid6)
	assert.Empty(t, wd)
	assert.Nil(t, repo)
}

func TestCovV21e_CostIterationFilter(t *testing.T) {
	app := covV21bApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	pid := covV21Project(t, ctx, false)
	iterID := insertIteration(t, ctx, pid, "active", "auto", "")
	code, _ := covDo(t, app, "GET", "/api/v1/projects/"+pid+"/cost?iteration_id="+iterID, "")
	assert.Equal(t, 200, code)
	code, _ = covDo(t, app, "GET", "/api/v1/projects/"+pid+"/cost?iteration_id=bad-id", "")
	assert.Equal(t, 400, code)
}

func TestCovV21e_DeploymentScope(t *testing.T) {
	ensureTestDB(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	app := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("username", "cov-viewer")
		c.Locals("role", "Viewer")
		c.Locals("uid", uuid.NewString())
		return c.Next()
	})
	api := app.Group("/api/v1")
	RegisterPipelineRoutes(api)
	// 全局部署列表放行；带 project_id 且非成员 → 项目级鉴权拒绝
	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/pipelines/deployments", nil), 15000)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	resp, err = app.Test(httptest.NewRequest("GET", "/api/v1/pipelines/deployments?project_id="+uuid.NewString(), nil), 15000)
	require.NoError(t, err)
	assert.Equal(t, 403, resp.StatusCode)
}

func TestCovV21e_TemplatesSyncSuccess(t *testing.T) {
	app := covV21App(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	manifest := "id: std-x\nname: Cov Template\nversion: 1.0.0\nstack:\n  backend: { lang: go }\n"
	files := map[string]string{
		"templates/std-x/template.yaml":              manifest,
		"templates/std-x/project.yaml":               "version: 1\ntype: backend\nstack:\n  backend: { lang: go }\n",
		"templates/std-x/Dockerfile":                 "FROM alpine:3.20\n",
		"templates/std-x/.gitea/workflows/build.yml": "name: Build\n",
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/repos/", func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(p, "/contents/templates") {
			_, _ = w.Write([]byte(`[{"name":"std-x","path":"templates/std-x","type":"dir"}]`))
			return
		}
		if strings.HasSuffix(p, "/contents/templates/std-x") {
			arr := []string{}
			for path := range files {
				arr = append(arr, fmt.Sprintf(`{"name":%q,"path":%q,"type":"file","size":64}`, path, path))
			}
			_, _ = w.Write([]byte("[" + strings.Join(arr, ",") + "]"))
			return
		}
		if strings.Contains(p, "/contents/templates/std-x/") {
			for path, content := range files {
				if strings.HasSuffix(p, "/contents/"+path) {
					_, _ = w.Write([]byte(`{"content":"` + base64.StdEncoding.EncodeToString([]byte(content)) + `","sha":"s"}`))
					return
				}
			}
		}
		http.NotFound(w, r)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	t.Setenv("GITEA_URL", ts.URL)
	t.Setenv("GITEA_TOKEN", "tok")
	t.Setenv("FORJA_TEMPLATES_REPO", "forja-admin/templates")
	t.Cleanup(templates.ResetCacheForTest)

	code, out := covDo(t, app, "POST", "/api/v1/admin/templates/sync", "")
	require.Equal(t, 200, code, "%v", out)
	assert.GreaterOrEqual(t, int(covData(t, out)["count"].(float64)), 1)

	code, out = covDo(t, app, "GET", "/api/v1/project-templates", "")
	require.Equal(t, 200, code)
	items, _ := covData(t, out)["items"].([]any)
	found := false
	for _, it := range items {
		if m, ok := it.(map[string]any); ok && m["id"] == "std-x" {
			found = true
		}
	}
	assert.True(t, found, "registry 模板应出现在列表")
}
