package api

import (
	"context"
	"testing"

	"forja/backend/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestE2E_IterationBranch_FurnacePipeline(t *testing.T) {
	if model.Pool == nil {
		_ = model.InitDB(context.Background())
		if model.Pool == nil {
			t.Skip("DB not available")
		}
	}
	ctx := context.Background()
	// 使用现有测试项目
	const pid = "00000000-0000-0000-0000-000000000001"
	// 创建迭代
	iterID := "00000000-0000-0000-0000-0000000000dd"
	_, _ = model.Pool.Exec(ctx, `DELETE FROM iterations WHERE id=$1`, iterID)
	_, err := model.Pool.Exec(ctx, `INSERT INTO iterations (id, project_id, name, status) VALUES ($1,$2,'e2e-iter','planned')`, iterID, pid)
	require.NoError(t, err)
	defer func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM iterations WHERE id=$1`, iterID) }()
	// 模拟 PATCH active 生成分支
	branch := "iteration/e2e-iter-000000"
	_, err = model.Pool.Exec(ctx, `UPDATE iterations SET branch=$1, status='active' WHERE id=$2`, branch, iterID)
	require.NoError(t, err)
	var gotBranch string
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT branch FROM iterations WHERE id=$1`, iterID).Scan(&gotBranch))
	assert.Equal(t, branch, gotBranch)
	// pipeline branch 锚：插入两行同 sha 不同分支应成功
	sha := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	_, err = model.Pool.Exec(ctx, `INSERT INTO pipeline_runs (repo, sha, branch, event, status) VALUES ('forja-admin/forja',$1,'master','push','PENDING') ON CONFLICT (repo, sha, branch, event) DO NOTHING`, sha)
	require.NoError(t, err)
	_, err = model.Pool.Exec(ctx, `INSERT INTO pipeline_runs (repo, sha, branch, event, status) VALUES ('forja-admin/forja',$1,'iteration/e2e-iter-000000','push','PENDING') ON CONFLICT (repo, sha, branch, event) DO NOTHING`, sha)
	require.NoError(t, err)
	var cnt int
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM pipeline_runs WHERE sha=$1`, sha).Scan(&cnt))
	assert.Equal(t, 2, cnt, "同 sha 双分支应各1行")
	// 清理
	_, _ = model.Pool.Exec(ctx, `DELETE FROM pipeline_runs WHERE sha=$1`, sha)
	// furnace 默认炉存在
	var fcnt int
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM furnaces WHERE id='forja-default'`).Scan(&fcnt))
	assert.Equal(t, 1, fcnt)
}
