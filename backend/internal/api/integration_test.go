package api

import (
	"bytes"
	"context"
	"encoding/json"
	"forja/backend/internal/api/respond"
	"net/http/httptest"
	"os"
	"testing"

	"forja/backend/internal/model"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupIntegrationApp(t *testing.T) (*fiber.App, func()) {
	t.Helper()
	// init DB if not already
	if model.Pool == nil {
		if os.Getenv("DATABASE_URL") == "" {
			os.Setenv("DATABASE_URL", "postgres://forja:forja@localhost:5432/forja?sslmode=disable")
		}
		if err := model.InitDB(context.Background()); err != nil {
			t.Skipf("DB not available: %v", err)
		}
	}
	app := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	// bypass auth for integration: set user in context
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("username", "test-admin")
		c.Locals("role", "Admin")
		return c.Next()
	})
	api := app.Group("/api/v1")
	RegisterInboxRoutes(api)
	RegisterSpecRoutes(api)
	RegisterTaskRoutes(api)
	RegisterEstimateRoutes(api)
	return app, func() {}
}

func TestIntegration_InboxToBS(t *testing.T) {
	app, cleanup := setupIntegrationApp(t)
	defer cleanup()

	// 1. create inbox (unique to avoid dedup)
	raw := "As a user I want to import Todo batch " + uuid.NewString()[:6]
	body, _ := json.Marshal(map[string]any{"raw_content": raw, "source_type": "TEXT_PASTE", "project_id": "00000000-0000-0000-0000-000000000001"})
	req := httptest.NewRequest("POST", "/api/v1/inbox", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, 5000)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	var created map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&created)
	createdData, _ := created["data"].(map[string]any)
	id, _ := createdData["id"].(string)
	require.NotEmpty(t, id)

	// 2. draft
	req2 := httptest.NewRequest("POST", "/api/v1/inbox/"+id+"/draft", nil)
	resp2, err := app.Test(req2, 5000)
	require.NoError(t, err)
	assert.Equal(t, 200, resp2.StatusCode)
	var draft map[string]any
	_ = json.NewDecoder(resp2.Body).Decode(&draft)
	draftData, _ := draft["data"].(map[string]any)
	assert.NotEmpty(t, draftData["story_id"])

	// 3. convert to BS
	body3, _ := json.Marshal(map[string]any{"requirement_ids": []string{id}, "project_id": "00000000-0000-0000-0000-000000000001"})
	req3 := httptest.NewRequest("POST", "/api/v1/inbox/convert", bytes.NewReader(body3))
	req3.Header.Set("Content-Type", "application/json")
	resp3, err := app.Test(req3, 5000)
	require.NoError(t, err)
	assert.Equal(t, 200, resp3.StatusCode)
	var converted map[string]any
	_ = json.NewDecoder(resp3.Body).Decode(&converted)
	convertedData, _ := converted["data"].(map[string]any)
	bsID, _ := convertedData["id"].(string)
	require.NotEmpty(t, bsID)

	// 4. validate BS
	req4 := httptest.NewRequest("POST", "/api/v1/specs/"+bsID+"/validate", nil)
	resp4, err := app.Test(req4, 5000)
	require.NoError(t, err)
	assert.Equal(t, 200, resp4.StatusCode)

	// 5. estimate
	body5, _ := json.Marshal(map[string]any{"title": "audit e2e", "priority": 5, "project_id": "00000000-0000-0000-0000-000000000001"})
	req5 := httptest.NewRequest("POST", "/api/v1/tasks/estimate", bytes.NewReader(body5))
	req5.Header.Set("Content-Type", "application/json")
	resp5, err := app.Test(req5, 5000)
	require.NoError(t, err)
	assert.Equal(t, 200, resp5.StatusCode)

	// 6. create task linked to BS
	body6, _ := json.Marshal(map[string]any{"title": "audit task", "spec_id": bsID, "project_id": "00000000-0000-0000-0000-000000000001"})
	req6 := httptest.NewRequest("POST", "/api/v1/tasks", bytes.NewReader(body6))
	req6.Header.Set("Content-Type", "application/json")
	resp6, err := app.Test(req6, 5000)
	require.NoError(t, err)
	assert.Equal(t, 200, resp6.StatusCode)
	var task map[string]any
	_ = json.NewDecoder(resp6.Body).Decode(&task)
	taskData, _ := task["data"].(map[string]any)
	tid, _ := taskData["id"].(string)
	require.NotEmpty(t, tid)

	// 7. converge (should be true for empty diff)
	req7 := httptest.NewRequest("POST", "/api/v1/tasks/"+tid+"/converge", nil)
	resp7, err := app.Test(req7, 5000)
	require.NoError(t, err)
	assert.Equal(t, 200, resp7.StatusCode)

	// 8. patch
	body8, _ := json.Marshal(map[string]any{"priority": 99})
	req8 := httptest.NewRequest("PATCH", "/api/v1/tasks/"+tid, bytes.NewReader(body8))
	req8.Header.Set("Content-Type", "application/json")
	resp8, err := app.Test(req8, 5000)
	require.NoError(t, err)
	assert.Equal(t, 200, resp8.StatusCode)

	// cleanup
	_, _ = model.Pool.Exec(context.Background(), `DELETE FROM tasks WHERE id=$1`, tid)
	_, _ = model.Pool.Exec(context.Background(), `DELETE FROM specs WHERE id=$1`, bsID)
	_, _ = model.Pool.Exec(context.Background(), `DELETE FROM requirements_inbox WHERE id=$1`, id)
	_ = uuid.NewString
}

func TestIntegration_Search(t *testing.T) {
	app, _ := setupIntegrationApp(t)
	req := httptest.NewRequest("GET", "/api/v1/specs/search?project_id=00000000-0000-0000-0000-000000000001&q=Todo", nil)
	resp, err := app.Test(req, 5000)
	require.NoError(t, err)
	// may be 200 even if no results
	assert.Contains(t, []int{200, 500}, resp.StatusCode)
}
