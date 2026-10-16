package api

// B1 第五波：integrateGitea 全链（mock Gitea 五步）+ giteaScaffold + SelfRepo env 分支。

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"forja/backend/internal/gitea"
	"forja/backend/internal/model"
	"forja/backend/internal/templates"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startFullGiteaMock 覆盖 CreateRepo/CommitScaffold/EnsureWebhook/Dispatch 的完整替身
func startFullGiteaMock(t *testing.T, failStage string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/user", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"login":"forja-admin"}`))
	})
	mux.HandleFunc("/api/v1/user/repos", func(w http.ResponseWriter, r *http.Request) {
		if failStage == "create_repo" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"full_name":"forja-admin/cov-slug","html_url":"http://x/cov-slug"}`))
	})
	mux.HandleFunc("/api/v1/repos/forja-admin/cov-slug/contents", func(w http.ResponseWriter, r *http.Request) {
		if failStage == "scaffold" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
	})
	mux.HandleFunc("/api/v1/repos/forja-admin/cov-slug/hooks", func(w http.ResponseWriter, r *http.Request) {
		if failStage == "webhook" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
	})
	mux.HandleFunc("/api/v1/repos/forja-admin/cov-slug/actions/workflows/", func(w http.ResponseWriter, r *http.Request) {
		if failStage == "dispatch" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	t.Setenv("GITEA_URL", ts.URL)
	t.Setenv("GITEA_TOKEN", "tok")
	t.Setenv("GITEA_ORG", "forja-admin")
	return ts
}

// TestIntegrateGitea_Stages 每个失败阶段返回对应 error 前缀；全通则脚手架齐活
func TestIntegrateGitea_Stages(t *testing.T) {
	newPipelineApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	pid := uuid.NewString()
	_, err := model.Pool.Exec(ctx, `INSERT INTO projects (id, name) VALUES ($1, $2)`, pid, "cov-int-"+pid[:8])
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, pid) })

	tpl, ok := templates.Get("standard-go-react")
	require.True(t, ok)
	wantFiles, err := tpl.Files(templates.Vars{Name: "cov", Slug: "cov-slug", Repo: "forja-admin/cov-slug", ProjectID: pid})
	require.NoError(t, err)

	cases := []struct{ stage, errPrefix string }{
		{"create_repo", "create_repo:"},
		{"scaffold", "scaffold"},
		{"webhook", "webhook:"},
	}
	for _, tc := range cases {
		startFullGiteaMock(t, tc.stage)
		gc := gitea.NewFromEnv()
		require.NotNil(t, gc)
		out := integrateGiteaCreate(gc, ctx, pid, "cov-slug", "cov-slug", "cov", "desc", tpl, nil)
		assert.Contains(t, out["error"], tc.errPrefix, "stage=%s out=%v", tc.stage, out)
	}

	// 全通路径：repo 回填 + 模板脚手架文件数 + webhook
	startFullGiteaMock(t, "")
	gc := gitea.NewFromEnv()
	require.NotNil(t, gc)
	out := integrateGiteaCreate(gc, ctx, pid, "cov-slug", "cov-slug", "cov", "desc", tpl, nil)
	assert.Nil(t, out["error"], "全通不应有 error: %v", out)
	assert.Equal(t, true, out["webhook"])
	assert.Equal(t, len(wantFiles), out["scaffold_files"])
	var repoBack *string
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT repo_full_name FROM projects WHERE id=$1`, pid).Scan(&repoBack))
	assert.Equal(t, "forja-admin/cov-slug", *repoBack)
}

// TestSelfRepo_EnvBranch GITEA_SELF_REPO 的三种形态
func TestSelfRepo_EnvBranch(t *testing.T) {
	client := &gitea.Client{BaseURL: "http://127.0.0.1:1", Token: "t", HTTP: &http.Client{}}

	t.Setenv("GITEA_ORG", "forja-admin") // 跳过 resolveOwner 网络调用
	t.Setenv("GITEA_SELF_REPO", "")
	o, r, err := SelfRepo(context.Background(), client)
	require.NoError(t, err)
	assert.Equal(t, "forja-admin", o)
	assert.Equal(t, "forja", r)

	t.Setenv("GITEA_SELF_REPO", "custom/repo")
	o, r, err = SelfRepo(context.Background(), client)
	require.NoError(t, err)
	assert.Equal(t, "custom", o)
	assert.Equal(t, "repo", r)

	t.Setenv("GITEA_SELF_REPO", "bare-name")
	_, r, err = SelfRepo(context.Background(), client)
	require.NoError(t, err)
	assert.Equal(t, "bare-name", r)
}
