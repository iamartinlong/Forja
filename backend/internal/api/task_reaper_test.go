package api

// S5.4 任务僵尸自愈单测：RUNNING 超 60m 判 FAILED，新鲜 RUNNING 不动，按项目隔离。

import (
	"context"
	"testing"

	"forja/backend/internal/model"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReapStaleTasks(t *testing.T) {
	newPipelineApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	pid := uuid.NewString()
	_, err := model.Pool.Exec(ctx, `INSERT INTO projects (id, name, slug) VALUES ($1,'s54reap','s54reap')`, pid)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = model.Pool.Exec(ctx, `DELETE FROM tasks WHERE project_id=$1`, pid)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, pid)
	})

	stale := uuid.NewString()
	fresh := uuid.NewString()
	_, err = model.Pool.Exec(ctx,
		`INSERT INTO tasks (id, project_id, title, status) VALUES ($1,$2,'stale','RUNNING'),($3,$2,'fresh','RUNNING')`,
		stale, pid, fresh)
	require.NoError(t, err)
	_, err = model.Pool.Exec(ctx, `UPDATE tasks SET updated_at=now()-interval '2 hours' WHERE id=$1`, stale)
	require.NoError(t, err)

	ReapStaleTasks(ctx, pid)

	var st, fr string
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT status FROM tasks WHERE id=$1`, stale).Scan(&st))
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT status FROM tasks WHERE id=$1`, fresh).Scan(&fr))
	assert.Equal(t, "FAILED", st, "超时 RUNNING 应被 reap 为 FAILED")
	assert.Equal(t, "RUNNING", fr, "新鲜 RUNNING 不应被误清")
}

// 全局分支回归（无 project_id）：$1 参数占位缺失曾致 42P18，生产日志实录。
func TestReapStaleTasks_GlobalPath(t *testing.T) {
	newPipelineApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	pid := uuid.NewString()
	taskID := uuid.NewString()
	_, err := model.Pool.Exec(ctx, `INSERT INTO projects (id, name, slug) VALUES ($1,'s54glob','s54glob')`, pid)
	require.NoError(t, err)
	_, err = model.Pool.Exec(ctx,
		`INSERT INTO tasks (id, project_id, title, status, updated_at) VALUES ($1,$2,'stale-global','RUNNING', now()-interval '2 hours')`,
		taskID, pid)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = model.Pool.Exec(ctx, `DELETE FROM tasks WHERE project_id=$1`, pid)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, pid)
	})

	ReapStaleTasks(ctx) // 全局路径

	var st string
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT status FROM tasks WHERE id=$1`, taskID).Scan(&st))
	assert.Equal(t, "FAILED", st, "全局路径也应正确 reap 超时 RUNNING")
}

// 脏 project_id 必须拒绝（不得退化为全局清扫）。
func TestReapStaleTasks_InvalidProjectID(t *testing.T) {
	newPipelineApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	pid := uuid.NewString()
	taskID := uuid.NewString()
	_, err := model.Pool.Exec(ctx, `INSERT INTO projects (id, name, slug) VALUES ($1,'s54bad','s54bad')`, pid)
	require.NoError(t, err)
	_, err = model.Pool.Exec(ctx,
		`INSERT INTO tasks (id, project_id, title, status, updated_at) VALUES ($1,$2,'stale','RUNNING', now()-interval '2 hours')`,
		taskID, pid)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = model.Pool.Exec(ctx, `DELETE FROM tasks WHERE project_id=$1`, pid)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, pid)
	})

	ReapStaleTasks(ctx, "not-a-valid-id")

	var st string
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT status FROM tasks WHERE id=$1`, taskID).Scan(&st))
	assert.Equal(t, "RUNNING", st, "非法 project_id 不得触发全局清扫")
}
