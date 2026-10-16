package api

import (
	"context"
	"testing"

	"forja/backend/internal/model"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskBranch_S4_0(t *testing.T) {
	app := covApp(t)
	ctx := context.Background()
	pid := uuid.NewString()
	_, err := model.Pool.Exec(ctx, `INSERT INTO projects (id, name, slug) VALUES ($1,'branch-ut','branch-ut')`, pid)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, pid) })

	// 创建任务 PENDING
	code, out := covDo(t, app, "POST", "/api/v1/tasks", `{"project_id":"`+pid+`","title":"branch task"}`)
	require.Equal(t, 200, code, "%v", out)
	tid := covData(t, out)["id"].(string)

	// approve → branch forge/task-<short>-a1
	code, out = covDo(t, app, "POST", "/api/v1/tasks/"+tid+"/approve", `{"action":"approve"}`)
	require.Equal(t, 200, code, "%v", out)
	branch := covData(t, out)["branch"].(string)
	assert.Contains(t, branch, "forge/task-")
	assert.Contains(t, branch, "-a1")

	// get → branch 回显
	code, out = covDo(t, app, "GET", "/api/v1/tasks/"+tid, "")
	require.Equal(t, 200, code)
	assert.Equal(t, branch, covData(t, out)["branch"])

	// 手动置 RELEASE_PENDING（模拟 forge 成功推分支后）
	_, err = model.Pool.Exec(ctx, `UPDATE tasks SET status='RELEASE_PENDING', commit_sha='`+uuid.NewString()+`' WHERE id=$1`, tid)
	require.NoError(t, err)

	// release → merge（无 Gitea 时跳过合并直接 RELEASED）+ tag 尝试
	code, out = covDo(t, app, "POST", "/api/v1/tasks/"+tid+"/release", "")
	require.Equal(t, 200, code, "%v", out)
	assert.Equal(t, "RELEASED", covData(t, out)["status"])

	// revert → RELEASED→FAILED
	code, out = covDo(t, app, "POST", "/api/v1/tasks/"+tid+"/revert", "")
	// 无真实 commit_sha（或 Gitea 缺失）时可能 400 NO_COMMIT_SHA 或 200
	if code == 200 {
		assert.Equal(t, "FAILED", covData(t, out)["status"])
	} else {
		assert.Contains(t, []int{400, 500}, code)
	}

	// 清理
	_, _ = model.Pool.Exec(ctx, `DELETE FROM tasks WHERE id=$1`, tid)
}

func TestTaskBranch_RevertNotReleased(t *testing.T) {
	app := covApp(t)
	ctx := context.Background()
	pid := uuid.NewString()
	_, err := model.Pool.Exec(ctx, `INSERT INTO projects (id, name, slug) VALUES ($1,'branch-revert','branch-revert')`, pid)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, pid) })
	code, out := covDo(t, app, "POST", "/api/v1/tasks", `{"project_id":"`+pid+`","title":"revert not released"}`)
	require.Equal(t, 200, code)
	tid := covData(t, out)["id"].(string)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM tasks WHERE id=$1`, tid) })

	// 未 RELEASED 时 revert → 400 NOT_RELEASED
	code, out = covDo(t, app, "POST", "/api/v1/tasks/"+tid+"/revert", "")
	assert.Equal(t, 400, code)
	assert.Contains(t, out["error"].(map[string]any)["code"], "NOT_RELEASED")

	// 不存在的任务
	code, _ = covDo(t, app, "POST", "/api/v1/tasks/"+uuid.NewString()+"/revert", "")
	assert.Equal(t, 404, code)
}

func TestHealth_FourProbes_S4_2(t *testing.T) {
	// DB/Redis 硬依赖已在 health_test.go 覆盖，此处补 Opencode/Gitea 软报告
	ensureTestDB(t)
	// 默认 env 未设 → disabled
	t.Setenv("GITEA_URL", "")
	t.Setenv("OPENCODE_URL", "")
	code, out := probeReady(t)
	// CI 排障：503 时 checks 体直接可见（db/redis 哪个 down）
	t.Logf("ready checks: %v", out["checks"])
	assert.Equal(t, 200, code)
	checks := out["checks"].(map[string]any)
	assert.Contains(t, checks["gitea"], "disabled")
	assert.Contains(t, checks["opencode"], "disabled")

	// 设无效地址 → down 但仍 ready（软依赖）
	t.Setenv("GITEA_URL", "http://127.0.0.1:1")
	t.Setenv("OPENCODE_URL", "http://127.0.0.1:1")
	code, out = probeReady(t)
	assert.Equal(t, 200, code)
	checks = out["checks"].(map[string]any)
	assert.Contains(t, checks["gitea"], "down:")
	assert.Contains(t, checks["opencode"], "down:")
}
