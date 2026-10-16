package api

import (
	"bufio"
	"context"
	"net/http/httptest"
	"testing"

	"forja/backend/internal/api/respond"
	"forja/backend/internal/events"
	"forja/backend/internal/model"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPipelineScope_NonAdmin(t *testing.T) {
	newPipelineApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	// Viewer app: should hit WHERE FALSE / IN (...) branches
	app := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("username", "viewer-cov")
		c.Locals("role", "Viewer")
		c.Locals("user_id", uuid.NewString())
		return c.Next()
	})
	api := app.Group("/api/v1")
	RegisterPipelineRoutes(api)

	req := httptest.NewRequest("GET", "/api/v1/pipelines", nil)
	resp, err := app.Test(req, 5000)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
}

func TestRecordJobCompletion_ExistingRun(t *testing.T) {
	newPipelineApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	repo := "forja-admin/cov-rec2-" + uuid.NewString()[:6]
	sha := randSHA()
	// pre-create PENDING run
	_, err := model.Pool.Exec(ctx, `INSERT INTO pipeline_runs (id, repo, sha, event, status) VALUES (gen_random_uuid(), $1,$2,'push','PENDING')`, repo, sha)
	require.NoError(t, err)
	// existing run path + bad time (parse fails -> dur nil)
	// 2026-09-12 动态聚合：单 job 完成且成功 ⇒ SUCCESS
	recordJobCompletion(ctx, repo, sha, "backend", "success", "bad-time", "")
	var st string
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT status FROM pipeline_runs WHERE repo=$1 AND sha=$2`, repo, sha).Scan(&st))
	assert.Equal(t, "SUCCESS", st)
}

func TestWriteSSE_Error(t *testing.T) {
	// writer that fails on Flush
	bad := &failWriter{}
	w := bufio.NewWriter(bad)
	err := writeSSE(w, events.Event{Name: "x", Data: mustJSON(map[string]string{"a": "b"})})
	assert.Error(t, err)
}

type failWriter struct{}

func (f *failWriter) Write(p []byte) (int, error) { return 0, assert.AnError }

func TestShortSHA(t *testing.T) {
	assert.Equal(t, "abc", shortSHA("abc"))
	assert.Equal(t, "12345678", shortSHA("123456789012"))
	assert.Equal(t, "", shortSHA(""))
}
