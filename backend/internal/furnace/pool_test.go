package furnace

import (
	"context"
	"testing"

	"forja/backend/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnsureDefaultOnBoot_Idempotent(t *testing.T) {
	if model.Pool == nil {
		ctx := context.Background()
		_ = model.InitDB(ctx)
		if model.Pool == nil {
			t.Skip("DB not available")
		}
	}
	ctx := context.Background()
	require.NoError(t, EnsureDefaultOnBoot(ctx))
	var cnt int
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM furnaces WHERE id='forja-default'`).Scan(&cnt))
	assert.Equal(t, 1, cnt)
	// second call idempotent
	require.NoError(t, EnsureDefaultOnBoot(ctx))
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM furnaces WHERE id='forja-default'`).Scan(&cnt))
	assert.Equal(t, 1, cnt)
}

func TestSelectAndAcquire_BusySem(t *testing.T) {
	if model.Pool == nil {
		_ = model.InitDB(context.Background())
		if model.Pool == nil {
			t.Skip("DB not available")
		}
	}
	ctx := context.Background()
	// clean tasks for this test
	_, _ = model.Pool.Exec(ctx, `DELETE FROM tasks WHERE title LIKE 'pool-test-%'`)
	taskID := "00000000-0000-0000-0000-0000000000aa"
	projectID := "00000000-0000-0000-0000-000000000001"
	_, _ = model.Pool.Exec(ctx, `INSERT INTO tasks (id, project_id, title, status) VALUES ($1,$2,'pool-test-1','PENDING') ON CONFLICT DO NOTHING`, taskID, projectID)
	defer func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM tasks WHERE id=$1`, taskID) }()
	row, err := SelectAndAcquire(ctx, taskID, projectID)
	// 仅无可用炉可 skip；其余错误（如 0A000 SQL 缺陷）必须 fail——
	// 此前 skipf 吞掉一切错误，P0 选炉缺陷在 CI 静默逃逸（2026-09-05 审计）
	if err != nil {
		if err == ErrNoCapacity {
			t.Skipf("no capacity: %v", err)
		}
		t.Fatalf("SelectAndAcquire: %v", err)
	}
	assert.NotEmpty(t, row.ID)
	// M-0 模型注入：model_profile 必须带出（forja-default 种子绑定非空）
	assert.NotEmpty(t, row.Model, "model_profile.model should be injected")
	assert.NotEmpty(t, row.ProviderID, "model_profile.provider_id should be injected")
	var furnaceID *string
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT furnace_id FROM tasks WHERE id=$1`, taskID).Scan(&furnaceID))
	require.NotNil(t, furnaceID)
	assert.Equal(t, row.ID, *furnaceID)
	require.NoError(t, ReleaseFurnace(ctx, taskID))
}
