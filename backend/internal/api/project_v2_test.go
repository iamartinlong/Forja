package api

// v2.0 项目域：模板建项 / 导入校验 / 规范（AGENTS.md）读写。

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"forja/backend/internal/model"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func postProject(t *testing.T, app *fiber.App, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/v1/projects", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, 15000)
	require.NoError(t, err)
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestCreateProject_TemplateMode(t *testing.T) {
	app := covApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	// 无 Gitea 集成：仅验证建项落库与契约字段（模板 manifest 预检内置）
	t.Setenv("GITEA_URL", "")
	t.Setenv("GITEA_TOKEN", "")
	code, out := postProject(t, app, `{"name":"v2-template-`+uuid.NewString()[:6]+`","mode":"create"}`)
	require.Equal(t, 200, code, "%v", out)
	data := out["data"].(map[string]any)
	pid := data["id"].(string)
	t.Cleanup(func() { _, _ = model.Pool.Exec(context.Background(), `DELETE FROM projects WHERE id=$1`, pid) })
	assert.Equal(t, "create", data["mode"])
	assert.Equal(t, "standard-go-react", data["template"])
	var stack string
	require.NoError(t, model.Pool.QueryRow(context.Background(),
		`SELECT stack::text FROM projects WHERE id=$1`, pid).Scan(&stack))
	assert.Contains(t, stack, `"lang": "go"`)
}

func TestCreateProject_InvalidTemplate(t *testing.T) {
	app := covApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	code, _ := postProject(t, app, `{"name":"v2-badtpl-`+uuid.NewString()[:6]+`","mode":"create","template":"nope"}`)
	assert.Equal(t, 400, code)
}

func TestCreateProject_ImportMissingYaml(t *testing.T) {
	app := covApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/user", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"login":"forja-admin"}`))
	})
	mux.HandleFunc("/api/v1/repos/forja-admin/imp-proj", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"full_name":"forja-admin/imp-proj","default_branch":"main","empty":false}`))
	})
	mux.HandleFunc("/api/v1/repos/forja-admin/imp-proj/contents/project.yaml", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	t.Setenv("GITEA_URL", ts.URL)
	t.Setenv("GITEA_TOKEN", "tok")

	code, out := postProject(t, app, `{"name":"v2-import-`+uuid.NewString()[:6]+`","mode":"import","repo_full_name":"forja-admin/imp-proj"}`)
	require.Equal(t, 400, code, "%v", out)
	env, _ := out["error"].(map[string]any)
	assert.Equal(t, "PROJECT_YAML_REQUIRED", env["code"])
}

func TestProjectAgents_GetPut(t *testing.T) {
	app := covApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	var putCalls int64
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/user", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"login":"forja-admin"}`))
	})
	mux.HandleFunc("/api/v1/repos/forja-admin/ag-proj/contents/AGENTS.md", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"sha":"sha1","content":"` +
				base64.StdEncoding.EncodeToString([]byte("# 项目规范\n")) + `"}`))
			return
		}
		atomic.AddInt64(&putCalls, 1)
		_, _ = w.Write([]byte(`{"content":{"sha":"sha2"}}`))
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	t.Setenv("GITEA_URL", ts.URL)
	t.Setenv("GITEA_TOKEN", "tok")

	pid := uuid.NewString()
	_, err := model.Pool.Exec(context.Background(),
		`INSERT INTO projects (id,name,slug,repo_full_name,default_branch) VALUES ($1,$2,$2,'forja-admin/ag-proj','main')`,
		pid, "ag-"+pid[:8])
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = model.Pool.Exec(context.Background(), `DELETE FROM projects WHERE id=$1`, pid) })

	// GET
	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/projects/"+pid+"/agents", nil), 15000)
	require.NoError(t, err)
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	require.Equal(t, 200, resp.StatusCode, "%v", out)
	data := out["data"].(map[string]any)
	assert.Equal(t, true, data["exists"])
	assert.Equal(t, "sha1", data["sha"])
	assert.Contains(t, data["content"].(string), "项目规范")

	// PUT：sha 不一致 → 400 FILE_CONFLICT，不得写
	req := httptest.NewRequest("PUT", "/api/v1/projects/"+pid+"/agents",
		strings.NewReader(`{"content":"new","sha":"stale"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err = app.Test(req, 15000)
	require.NoError(t, err)
	_ = json.NewDecoder(resp.Body).Decode(&out)
	assert.Equal(t, 400, resp.StatusCode, "%v", out)
	assert.Equal(t, "FILE_CONFLICT", out["error"].(map[string]any)["code"])
	assert.EqualValues(t, 0, atomic.LoadInt64(&putCalls))

	// PUT：sha 一致 → 写入并回写缓存
	req = httptest.NewRequest("PUT", "/api/v1/projects/"+pid+"/agents",
		strings.NewReader(`{"content":"# new","sha":"sha1"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err = app.Test(req, 15000)
	require.NoError(t, err)
	out = map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	require.Equal(t, 200, resp.StatusCode, "%v", out)
	assert.EqualValues(t, 1, atomic.LoadInt64(&putCalls))
	var cached *string
	require.NoError(t, model.Pool.QueryRow(context.Background(),
		`SELECT agents_sha FROM projects WHERE id=$1`, pid).Scan(&cached))
	assert.Equal(t, "sha2", *cached)
}
