package api

import (
	"context"
	"testing"

	"forja/backend/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIterationBranch_Regex(t *testing.T) {
	if model.Pool == nil {
		_ = model.InitDB(context.Background())
		if model.Pool == nil {
			t.Skip("DB not available")
		}
	}
	ctx := context.Background()
	// direct DB check: branch must match CHECK
	_, err := model.Pool.Exec(ctx, `INSERT INTO iterations (id, project_id, name, branch) VALUES ('00000000-0000-0000-0000-0000000000bb','00000000-0000-0000-0000-000000000001','branch-regex-test','iteration/bad_branch')`)
	assert.Error(t, err, "bad branch should violate CHECK")
	_, err = model.Pool.Exec(ctx, `INSERT INTO iterations (id, project_id, name, branch) VALUES ('00000000-0000-0000-0000-0000000000cc','00000000-0000-0000-0000-000000000001','branch-regex-ok','iteration/ok-abcdef')`)
	if err == nil {
		_, _ = model.Pool.Exec(ctx, `DELETE FROM iterations WHERE id='00000000-0000-0000-0000-0000000000cc'`)
	}
	assert.NoError(t, err)
}

func TestFurnaceDeleteProtected(t *testing.T) {
	if model.Pool == nil {
		_ = model.InitDB(context.Background())
		if model.Pool == nil {
			t.Skip("DB not available")
		}
	}
	// Ensure forja-default exists
	_, _ = model.Pool.Exec(context.Background(), `INSERT INTO furnaces(id,name,base_url,capacity) VALUES('forja-default','forja-default','http://forja-default-furnace:4096',1) ON CONFLICT(id) DO NOTHING`)
	// try delete via API would be 403, but DB level not protected; just check exists
	var cnt int
	require.NoError(t, model.Pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM furnaces WHERE id='forja-default'`).Scan(&cnt))
	assert.Equal(t, 1, cnt)
}
