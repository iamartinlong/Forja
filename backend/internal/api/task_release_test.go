package api

// M5+ 回归：release 响应 merge_sha 以 worker 已写入的 commit_sha 为基底回填
// （api 无 git 时回退合并静默跳过的实录修正）。

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"forja/backend/internal/model"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReleaseTask_ReturnsMergeSHAFromCommit(t *testing.T) {
	app := covApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	pid := uuid.NewString()
	tid := uuid.NewString()
	sha := strings.Repeat("ab", 20)
	_, err := model.Pool.Exec(ctx,
		`INSERT INTO projects (id,name,slug,default_branch) VALUES ($1,$2,$2,'master')`,
		pid, "rel-"+pid[:8])
	require.NoError(t, err)
	_, err = model.Pool.Exec(ctx,
		`INSERT INTO tasks (id, project_id, title, status, branch, commit_sha) VALUES ($1,$2,'rel-probe','RELEASE_PENDING','master',$3)`,
		tid, pid, sha)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = model.Pool.Exec(ctx, `DELETE FROM tasks WHERE id=$1`, tid)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, pid)
	})

	req := httptest.NewRequest("POST", "/api/v1/tasks/"+tid+"/release", strings.NewReader(`{"action":"release"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, 15000)
	require.NoError(t, err)
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	require.Equal(t, 200, resp.StatusCode, "%v", out)
	data := out["data"].(map[string]any)
	assert.Equal(t, "RELEASED", data["status"])
	assert.Equal(t, sha, data["merge_sha"], "merge_sha 应回填既有 commit_sha")
}
