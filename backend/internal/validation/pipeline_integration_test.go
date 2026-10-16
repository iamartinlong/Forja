package validation

import (
	"context"
	"testing"
	"time"

	"forja/backend/internal/model"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunPipeline_WithDB_StaticFail(t *testing.T) {
	if model.Pool == nil {
		// try init DB
		if err := model.InitDB(context.Background()); err != nil {
			t.Skip("DB not available")
		}
	}
	ctx := context.Background()
	taskID := uuid.NewString()
	projectID := "00000000-0000-0000-0000-000000000001"
	_, err := model.Pool.Exec(ctx, `INSERT INTO tasks (id, project_id, title, status) VALUES ($1,$2,'pipeline-test', 'PENDING')`, taskID, projectID)
	require.NoError(t, err)
	defer model.Pool.Exec(ctx, `DELETE FROM tasks WHERE id=$1`, taskID)

	// case 1: forbidden fmt.Println
	_, err = model.Pool.Exec(ctx, `INSERT INTO code_changes (id, task_id, file_path, diff) VALUES ($1,$2,'a.go','fmt.Println("x")')`, uuid.NewString(), taskID)
	require.NoError(t, err)
	res := RunPipeline(ctx, taskID, "", 1, nil, time.Now().Add(-time.Hour))
	assert.False(t, res.Passed)
	// cleanup diff
	_, _ = model.Pool.Exec(ctx, `DELETE FROM code_changes WHERE task_id=$1`, taskID)

	// case 2: no changes → passed
	res2 := RunPipeline(ctx, taskID, "", 1, nil, time.Now().Add(-time.Hour))
	assert.True(t, res2.Passed)

	// case 3: hasChanges + vet/test mocked → passed via recordTest
	t.Setenv("SKIP_VET", "1")
	_, err = model.Pool.Exec(ctx, `INSERT INTO code_changes (id, task_id, file_path, diff) VALUES ($1,$2,'b.go','func Foo(){}')`, uuid.NewString(), taskID)
	require.NoError(t, err)
	res3 := RunPipeline(ctx, taskID, "", 1, nil, time.Now().Add(-time.Hour))
	assert.True(t, res3.Passed)
	_, _ = model.Pool.Exec(ctx, `DELETE FROM code_changes WHERE task_id=$1`, taskID)
	t.Setenv("SKIP_VET", "")


}
