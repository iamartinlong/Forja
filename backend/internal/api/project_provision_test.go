package api

// M5+ 回归：存量项目 secrets 回填端点（POST /projects/:id/provision）。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"forja/backend/internal/model"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProvisionProjectSecrets(t *testing.T) {
	app := covApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	t.Setenv("FORJA_REGISTRY_TOKEN", "tok-registry")
	t.Setenv("FORJA_INTERNAL_TOKEN", "tok-internal")
	var mu sync.Mutex
	var got []string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/user", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"login":"forja-admin"}`))
	})
	mux.HandleFunc("/api/v1/repos/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/actions/secrets/") {
			name := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			mu.Lock()
			got = append(got, name)
			mu.Unlock()
			w.WriteHeader(http.StatusCreated)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	t.Setenv("GITEA_URL", ts.URL)
	t.Setenv("GITEA_TOKEN", "tok")

	repo := "forja-admin/prov-proj-" + uuid.NewString()[:8]
	pid := uuid.NewString()
	ctx := context.Background()
	_, err := model.Pool.Exec(ctx,
		`INSERT INTO projects (id,name,slug,repo_full_name,default_branch) VALUES ($1,$2,$2,$3,'master')`,
		pid, "pv-"+pid[:8], repo)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, pid) })

	req := httptest.NewRequest("POST", "/api/v1/projects/"+pid+"/provision", nil)
	resp, err := app.Test(req, 15000)
	require.NoError(t, err)
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	require.Equal(t, 200, resp.StatusCode, "%v", out)
	assert.EqualValues(t, 3, out["data"].(map[string]any)["secrets"])

	mu.Lock()
	defer mu.Unlock()
	assert.ElementsMatch(t, []string{"REGISTRY_USER", "REGISTRY_TOKEN", "FORJA_INTERNAL_TOKEN"}, got)
}
