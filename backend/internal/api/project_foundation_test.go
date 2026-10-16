package api

// F0.5 项目域基础契约单测：仓库命名 / 项目默认分支 / 分支白名单。

import (
	"context"
	"testing"

	"forja/backend/internal/model"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRepoNameFor(t *testing.T) {
	id := "01a0704e-2bac-7d04-988b-837c3d513ea9"
	assert.Equal(t, "guest-book-3d513ea9", repoNameFor("guest-book", id))
	assert.Equal(t, "3d513ea9", repoNameFor("", id))
	assert.Equal(t, "guest-book-3d513ea9", repoNameFor("guest-book-3d513ea9", id), "已含后缀不重复拼接")
}

func TestIsAllowedBranchFor(t *testing.T) {
	assert.True(t, isAllowedBranchFor("master", ""))
	assert.True(t, isAllowedBranchFor("main", "main"))
	assert.False(t, isAllowedBranchFor("main", "master"))
	assert.True(t, isAllowedBranchFor("iteration/iter-1", "main"))
	assert.False(t, isAllowedBranchFor("", "main"))
	assert.False(t, isAllowedBranchFor("feature/x", "main"))
}

func TestProjectDefaultBranch_Fallback(t *testing.T) {
	saved := model.Pool
	model.Pool = nil
	defer func() { model.Pool = saved }()
	assert.Equal(t, "master", ProjectDefaultBranch(context.Background(), "not-valid"))
}

func TestProjectDefaultBranch_DB(t *testing.T) {
	newPipelineApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	pid := uuid.NewString()
	_, err := model.Pool.Exec(ctx,
		`INSERT INTO projects (id, name, slug, default_branch) VALUES ($1,'fd-branch','fd-branch','main')`, pid)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, pid) })
	assert.Equal(t, "main", ProjectDefaultBranch(ctx, pid))
}
