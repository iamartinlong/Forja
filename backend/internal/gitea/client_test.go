package gitea

// M-CICD-4 客户端单测：httptest 模拟 Gitea API，全路径不依赖外部实例。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := &Client{BaseURL: srv.URL, Token: "test-token", HTTP: srv.Client()}
	return c, srv
}

func TestCreateRepo_CreatedAndConflictIdempotent(t *testing.T) {
	var calls int32
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/user":
			_ = json.NewEncoder(w).Encode(map[string]any{"login": "forja-admin"})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/user/repos":
			if atomic.AddInt32(&calls, 1) == 1 {
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(map[string]any{"full_name": "forja-admin/demo", "html_url": "http://x/demo"})
				return
			}
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"message":"The repository with the same name already exists."}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/forja-admin/demo":
			_ = json.NewEncoder(w).Encode(map[string]any{"full_name": "forja-admin/demo", "html_url": "http://x/demo"})
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	})

	fn, url, err := c.CreateRepo(context.Background(), "demo", "first")
	require.NoError(t, err)
	assert.Equal(t, "forja-admin/demo", fn)
	assert.NotEmpty(t, url)

	fn2, _, err := c.CreateRepo(context.Background(), "demo", "second")
	require.NoError(t, err, "已存在应幂等返回既有仓库")
	assert.Equal(t, fn, fn2)
}

func TestCommitScaffold_BatchContents(t *testing.T) {
	var method, path string
	var lastBody map[string]any
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&lastBody)
		w.WriteHeader(http.StatusCreated)
	})
	files := []ScaffoldFile{
		{"README.md", "# demo"},
		{".gitea/workflows/ci.yml", "name: ci"},
	}
	n, err := c.CommitScaffold(context.Background(), "o", "r", "master", files)
	require.NoError(t, err)
	assert.Equal(t, len(files), n)
	assert.Equal(t, http.MethodPost, method)
	assert.Equal(t, "/api/v1/repos/o/r/contents", path)
	fs, _ := lastBody["files"].([]any)
	assert.Len(t, fs, len(files))
	assert.Equal(t, "master", lastBody["branch"])
}

func TestDispatch_Accepts204(t *testing.T) {
	var path, body string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		b := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(b)
		body = string(b)
		w.WriteHeader(http.StatusNoContent)
	})
	require.NoError(t, c.Dispatch(context.Background(), "o", "r", "ci.yml", "master"))
	assert.Contains(t, path, "/actions/workflows/ci.yml/dispatches")
	assert.Contains(t, body, `"ref":"master"`)
}

func TestEnsureWebhook_DedupByURL(t *testing.T) {
	var posts int32
	existing := `[{"config":{"url":"http://api:8080/api/v1/webhooks/gitea"}}]`
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(existing))
		case http.MethodPost:
			atomic.AddInt32(&posts, 1)
			w.WriteHeader(http.StatusCreated)
		}
	})
	require.NoError(t, c.EnsureWebhook(context.Background(), "o", "r", "http://api:8080/api/v1/webhooks/gitea", "sec"))
	assert.EqualValues(t, 0, posts, "URL 已注册不应重复建钩子")
}

func TestNewFromEnv_Gating(t *testing.T) {
	t.Setenv("GITEA_URL", "")
	t.Setenv("GITEA_TOKEN", "")
	assert.Nil(t, NewFromEnv(), "未配置应返回 nil")

	t.Setenv("GITEA_URL", "http://g:3000/")
	t.Setenv("GITEA_TOKEN", "t")
	c := NewFromEnv()
	require.NotNil(t, c)
	assert.Equal(t, "http://g:3000", c.BaseURL, "尾斜杠应去除")
}
