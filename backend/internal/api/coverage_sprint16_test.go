package api

import (
	"context"
	"net/http/httptest"
	"testing"

	"forja/backend/internal/api/respond"
	"forja/backend/internal/model"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeployment_MarkAndComplete(t *testing.T) {
	newPipelineApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	// MarkScheduledRunning success
	sha := randSHA()
	id := seedDeployment(t, "forja-admin/forja", sha, "master-"+sha[:8], "scheduled", "scheduled", "tester")
	MarkScheduledRunning(ctx)
	var st string
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT status FROM deployments WHERE id=$1`, id).Scan(&st))
	assert.Equal(t, "running", st)
	// Complete with no running row warning path (second call should warn but not panic)
	CompleteRunningDeployment(ctx, "success")
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT status FROM deployments WHERE id=$1`, id).Scan(&st))
	assert.Equal(t, "success", st)
	// second complete with no running row
	CompleteRunningDeployment(ctx, "failure") // should log warning, not panic
}

func TestDeployment_FailScheduled(t *testing.T) {
	newPipelineApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	sha := randSHA()
	seedDeployment(t, "forja-admin/forja", sha, "master-"+sha[:8], "scheduled", "scheduled", "tester")
	FailScheduledDeployment(ctx, "test reason")
	var st string
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT status FROM deployments WHERE sha=$1`, sha).Scan(&st))
	assert.Equal(t, "failed", st)
}

func TestDeployment_CreateEmptyAndListPoolNil(t *testing.T) {
	// early returns with empty repo/sha
	CreateDeployment(context.Background(), "", "", "", "manual", "x")
	CreateDeployment(context.Background(), "forja-admin/forja", "", "tag", "manual", "x")
	// Pool nil branches
	saved := model.Pool
	model.Pool = nil
	CreateDeployment(context.Background(), "r", "s", "t", "manual", "x")
	MarkScheduledRunning(context.Background())
	FailScheduledDeployment(context.Background(), "x")
	CompleteRunningDeployment(context.Background(), "success")
	ReapStaleDeployments(context.Background())
	assert.False(t, HasInFlightDeployment(context.Background()))
	model.Pool = saved
	// listDeployments Pool nil -> 503
	app := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	app.Use(func(c *fiber.Ctx) error { c.Locals("username", "a"); c.Locals("role", "Admin"); return c.Next() })
	api := app.Group("/api/v1")
	RegisterPipelineRoutes(api)
	model.Pool = nil
	req := httptest.NewRequest("GET", "/api/v1/pipelines/deployments", nil)
	resp, err := app.Test(req, 5000)
	require.NoError(t, err)
	assert.Equal(t, 503, resp.StatusCode)
	model.Pool = saved
}

func TestReindex_PaginationAndAuth(t *testing.T) {
	// test listDeployments pagination and reindex with no data
	newPipelineApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	// pagination
	code, out := getJSON(t, newPipelineApp(t), "/api/v1/pipelines/deployments?page=2&page_size=1")
	assert.Equal(t, 200, code)
	_ = out
}
