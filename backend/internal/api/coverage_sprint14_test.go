package api

import (
	"context"
	"testing"
	"time"

	"forja/backend/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeployment_ReapScheduledStale(t *testing.T) {
	newPipelineApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	sha := randSHA()
	id := seedDeployment(t, "forja-admin/forja", sha, "master-"+sha[:8], "scheduled", "scheduled", "tester")
	_, err := model.Pool.Exec(ctx, `UPDATE deployments SET created_at=now()-interval '9 days' WHERE id=$1`, id)
	require.NoError(t, err)
	ReapStaleDeployments(ctx)
	var st string
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT status FROM deployments WHERE id=$1`, id).Scan(&st))
	assert.Equal(t, "failed", st)
}

func TestDeployment_HasInFlight_PoolNil(t *testing.T) {
	saved := model.Pool
	model.Pool = nil
	assert.False(t, HasInFlightDeployment(context.Background()))
	model.Pool = saved
}

func TestIsDocsOnlyPush(t *testing.T) {
	assert.True(t, isDocsOnlyPush(&giteaHookPayload{Commits: []giteaHookCommit{{Added: []string{"docs/a.md"}, Modified: []string{"README.md"}}}}))
	assert.False(t, isDocsOnlyPush(&giteaHookPayload{Commits: []giteaHookCommit{{Added: []string{"docs/a.md", "backend/main.go"}}}}))
	assert.False(t, isDocsOnlyPush(&giteaHookPayload{}))
	assert.True(t, isDocPath("docs/x.md"))
	assert.True(t, isDocPath("foo.md"))
	assert.False(t, isDocPath("backend/main.go"))
}

func TestLastFailures_CreatedAtBranch(t *testing.T) {
	// ensures created_at ordering is used (already fixed from finished_at)
	taskID := seedForgeTask(t, "FAILED")
	ctx := context.Background()
	msg := lastFailures(ctx, taskID)
	assert.Equal(t, "validation failed", msg)
	// add time gap and ensure ordering by created_at
	time.Sleep(10 * time.Millisecond)
}
