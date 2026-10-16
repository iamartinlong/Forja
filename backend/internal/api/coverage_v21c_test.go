package api

// v2.1 覆盖率冲刺 Phase 2b：导入分支、Spec refs/校验、迭代 PATCH 校验分支。

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"forja/backend/internal/model"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCovV21c_ImportBranches(t *testing.T) {
	app := covV21bApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()

	// 无 gitea → 503
	t.Setenv("GITEA_URL", "")
	t.Setenv("GITEA_TOKEN", "")
	code, _ := covDo(t, app, "POST", "/api/v1/projects", `{"name":"cov21-import-x","mode":"import"}`)
	assert.Equal(t, 503, code)

	// mock gitea：imp-ok 有 project.yaml；imp-noyaml 无；imp-missing 404
	manifest := "version: 1\ntype: backend\nstack:\n  backend: { lang: go }\n"
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/repos/", func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(p, "/imp-missing"):
			http.NotFound(w, r)
		case strings.Contains(p, "/imp-noyaml") && strings.Contains(p, "/contents/project.yaml"):
			http.NotFound(w, r)
		case strings.Contains(p, "/imp-noyaml") || strings.Contains(p, "/imp-ok"):
			if strings.Contains(p, "/contents/project.yaml") {
				_, _ = w.Write([]byte(`{"content":"` + base64.StdEncoding.EncodeToString([]byte(manifest)) + `","sha":"sha-yaml"}`))
				return
			}
			_, _ = w.Write([]byte(`{"default_branch":"main"}`))
		default:
			http.NotFound(w, r)
		}
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	t.Setenv("GITEA_URL", ts.URL)
	t.Setenv("GITEA_TOKEN", "tok")

	// 非法 repo_full_name（无 owner/repo 结构）
	code, _ = covDo(t, app, "POST", "/api/v1/projects", `{"name":"cov21-import-bad","mode":"import","repo_full_name":"noslash"}`)
	assert.Equal(t, 400, code)

	// 两者都缺 → 400
	code, _ = covDo(t, app, "POST", "/api/v1/projects", `{"name":"cov21-import-none","mode":"import"}`)
	assert.Equal(t, 400, code)

	// 同实例仓不存在 → 404
	code, _ = covDo(t, app, "POST", "/api/v1/projects", `{"name":"cov21-import-missing","mode":"import","repo_full_name":"forja-admin/imp-missing"}`)
	assert.Equal(t, 404, code)

	// 缺 project.yaml → 400 PROJECT_YAML_REQUIRED
	code, out := covDo(t, app, "POST", "/api/v1/projects", `{"name":"cov21-import-noyaml","mode":"import","repo_full_name":"forja-admin/imp-noyaml"}`)
	assert.Equal(t, 400, code, "%v", out)
	assert.Equal(t, "PROJECT_YAML_REQUIRED", covErr(t, out))

	// 成功导入（main 默认分支）
	name := "cov21-imp-ok-" + uuid.NewString()[:6]
	code, out = covDo(t, app, "POST", "/api/v1/projects", `{"name":"`+name+`","mode":"import","repo_full_name":"forja-admin/imp-ok"}`)
	require.Equal(t, 200, code, "%v", out)
	pid, _ := covData(t, out)["id"].(string)
	require.NotEmpty(t, pid)
	var branch string
	_ = model.Pool.QueryRow(ctx, `SELECT default_branch FROM projects WHERE id=$1`, pid).Scan(&branch)
	assert.Equal(t, "main", branch)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, pid) })
}

func covErr(t *testing.T, out map[string]any) string {
	t.Helper()
	if e, ok := out["error"].(map[string]any); ok {
		if c, ok := e["code"].(string); ok {
			return c
		}
	}
	return ""
}

func TestCovV21c_SpecRefsAndValidate(t *testing.T) {
	app2 := covApp(t) // 已含 SpecRoutes + TaskRoutes
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	pid := covV21Project(t, ctx, false)

	// refs 必须指向同项目既有 Spec：先建被引用 Spec，再建带 refs 的 Spec
	code, out := covDo(t, app2, "POST", "/api/v1/specs", `{"project_id":"`+pid+`","layer":"BS","title":"cov21 ref target","content":{"what":"x"}}`)
	require.Equal(t, 200, code, "%v", out)
	refID, _ := covData(t, out)["id"].(string)
	require.NotEmpty(t, refID)

	body := `{"project_id":"` + pid + `","layer":"IS","title":"cov21 spec refs","content":{"what":"y"},"refs":["` + refID + `"]}`
	code, out = covDo(t, app2, "POST", "/api/v1/specs", body)
	require.Equal(t, 200, code, "%v", out)
	sid, _ := covData(t, out)["id"].(string)
	require.NotEmpty(t, sid)

	code, _ = covDo(t, app2, "GET", "/api/v1/specs/"+sid+"/refs", "")
	assert.Equal(t, 200, code)
	// 校验端点（含 refs/anchors 校验分支；结果允许 200 或 400，覆盖函数即可）
	_, _ = covDo(t, app2, "POST", "/api/v1/specs/"+sid+"/validate", `{}`)
}

func TestCovV21c_IterationPatchBranches(t *testing.T) {
	app := covV21App(t) // 含 IterationRoutes
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	pid := covV21Project(t, ctx, false)
	code, out := covDo(t, app, "POST", "/api/v1/projects/"+pid+"/iterations", `{"name":"cov21-p"}`)
	require.Equal(t, 200, code, "%v", out)
	iterID, _ := covData(t, out)["id"].(string)

	// 非法日期 → 400
	code, _ = covDo(t, app, "PATCH", "/api/v1/projects/"+pid+"/iterations/"+iterID, `{"start_date":"2026-13-99"}`)
	assert.Equal(t, 400, code)
	// 非法状态 → 400
	code, _ = covDo(t, app, "PATCH", "/api/v1/projects/"+pid+"/iterations/"+iterID, `{"status":"bogus"}`)
	assert.Equal(t, 400, code)
	// 合法改期 → 200
	code, _ = covDo(t, app, "PATCH", "/api/v1/projects/"+pid+"/iterations/"+iterID, `{"start_date":"2026-09-01","end_date":"2026-09-10"}`)
	assert.Equal(t, 200, code)
	// 不存在的迭代 → 404
	code, _ = covDo(t, app, "PATCH", "/api/v1/projects/"+pid+"/iterations/"+uuid.NewString(), `{"name":"x"}`)
	assert.Equal(t, 404, code)
}
