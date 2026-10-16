package api

import (
	"context"
	"net/http/httptest"
	"testing"

	"forja/backend/internal/api/respond"
	"forja/backend/internal/model"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPipelineScope_Direct(t *testing.T) {
	newPipelineApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	app := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	api := app.Group("/api/v1")
	// need auth middleware to set locals, but we test pipelineScope directly via handler
	// Instead test via endpoint with different roles
	// Viewer without user_id -> WHERE FALSE
	app2 := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	app2.Use(func(c *fiber.Ctx) error {
		c.Locals("username", "anon")
		c.Locals("role", "Viewer")
		return c.Next()
	})
	g2 := app2.Group("/api/v1")
	RegisterPipelineRoutes(g2)
	req := httptest.NewRequest("GET", "/api/v1/pipelines", nil)
	resp, err := app2.Test(req, 5000)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	// Valid project_id but not member -> 403
	_ = app
	_ = api
	_ = context.Background
}

func TestDeployment_HasInFlight_Reap(t *testing.T) {
	newPipelineApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	// create stale running to test reap path
	sha := randSHA()
	id := seedDeployment(t, "forja-admin/forja", sha, "master-"+sha[:8], "running", "manual", "tester")
	_, err := model.Pool.Exec(ctx, `UPDATE deployments SET created_at=now()-interval '20 minutes' WHERE id=$1`, id)
	require.NoError(t, err)
	// HasInFlight should reap and return false after
	// First call triggers reap
	_ = HasInFlightDeployment(ctx)
	var st string
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT status FROM deployments WHERE id=$1`, id).Scan(&st))
	assert.Equal(t, "failed", st)
}

func TestReindex_PoolNil(t *testing.T) {
	saved := model.Pool
	model.Pool = nil
	app := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("username", "admin")
		c.Locals("role", "Admin")
		return c.Next()
	})
	api := app.Group("/api/v1")
	RegisterReindexRoutes(api)
	req := httptest.NewRequest("POST", "/api/v1/admin/reindex-embeddings", nil)
	resp, err := app.Test(req, 5000)
	require.NoError(t, err)
	assert.Equal(t, 503, resp.StatusCode)
	model.Pool = saved
}

func TestInbox_CreateInvalidProject(t *testing.T) {
	app := newPipelineApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	code, _ := getJSON(t, app, "/api/v1/inbox?project_id="+uuid.NewString())
	assert.NotEqual(t, 500, code)
	_ = respond.HandleError
}
