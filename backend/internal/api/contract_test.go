package api

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"forja/backend/internal/api/respond"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/requestid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupApp() *fiber.App {
	app := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	app.Use(requestid.New())
	app.Post("/api/v1/auth/login", func(c *fiber.Ctx) error {
		return respond.OK(c, fiber.Map{"token": "test-token", "role": "Admin"})
	})
	// mock auth middleware: allow all for contract test
	api := app.Group("/api/v1")
	// bypass auth for contract: directly register
	// we register a minimal version that checks layer/title without DB
	api.Post("/specs", func(c *fiber.Ctx) error {
		var body map[string]any
		if err := json.Unmarshal(c.Body(), &body); err != nil {
			return respond.BadRequest(c, err.Error())
		}
		if body["layer"] == nil || body["title"] == nil {
			return respond.InvalidParam(c, "layer,title", "layer and title required")
		}
		layer, _ := body["layer"].(string)
		if layer != "BS" && layer != "IS" && layer != "DS" && layer != "TS" {
			return respond.InvalidParam(c, "layer", "invalid layer")
		}
		return respond.OK(c, fiber.Map{"id": "test-id", "status": "DRAFT"})
	})
	api.Post("/tasks", func(c *fiber.Ctx) error {
		var body map[string]any
		if err := json.Unmarshal(c.Body(), &body); err != nil {
			return respond.BadRequest(c, err.Error())
		}
		if body["title"] == nil || body["title"] == "" {
			return respond.InvalidParam(c, "title", "title required")
		}
		return respond.OK(c, fiber.Map{"id": "task-id", "status": "PENDING"})
	})
	api.Post("/inbox", func(c *fiber.Ctx) error {
		var body map[string]any
		_ = json.Unmarshal(c.Body(), &body)
		if body["raw_content"] == nil || body["raw_content"] == "" {
			return respond.InvalidParam(c, "raw_content", "raw_content required")
		}
		return respond.OK(c, fiber.Map{"id": "inbox-id", "status": "NEW"})
	})
	api.Post("/tasks/estimate", func(c *fiber.Ctx) error {
		var body map[string]any
		_ = json.Unmarshal(c.Body(), &body)
		if body["title"] == nil || body["title"] == "" {
			return respond.InvalidParam(c, "title", "title required")
		}
		return respond.OK(c, fiber.Map{"estimated_hours": 3.5, "deviation": 0.15})
	})
	return app
}

func TestContract_SpecCreate(t *testing.T) {
	app := setupApp()
	// valid
	body, _ := json.Marshal(map[string]any{"layer": "BS", "title": "t", "content": map[string]any{"story_id": "S-1", "title": "t", "acceptance_criteria": []any{map[string]string{"given": "a", "when": "b", "then": "c"}}}})
	req := httptest.NewRequest("POST", "/api/v1/specs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, 5000)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	assert.Equal(t, float64(0), out["code"])
	assert.NotEmpty(t, out["trace_id"])
	data, _ := out["data"].(map[string]any)
	assert.Equal(t, "test-id", data["id"])
	// invalid layer
	body2, _ := json.Marshal(map[string]any{"layer": "XX", "title": "t"})
	req2 := httptest.NewRequest("POST", "/api/v1/specs", bytes.NewReader(body2))
	req2.Header.Set("Content-Type", "application/json")
	resp2, _ := app.Test(req2, 5000)
	assert.Equal(t, 400, resp2.StatusCode)
	var errOut map[string]any
	_ = json.NewDecoder(resp2.Body).Decode(&errOut)
	assert.Equal(t, float64(respond.CodeInvalidParam), errOut["code"])
	errObj, _ := errOut["error"].(map[string]any)
	assert.Equal(t, "INVALID_PARAM", errObj["code"])
}

func TestContract_TaskCreate(t *testing.T) {
	app := setupApp()
	body, _ := json.Marshal(map[string]any{"title": "my task"})
	req := httptest.NewRequest("POST", "/api/v1/tasks", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := app.Test(req, 5000)
	assert.Equal(t, 200, resp.StatusCode)
	// missing title
	body2, _ := json.Marshal(map[string]any{})
	req2 := httptest.NewRequest("POST", "/api/v1/tasks", bytes.NewReader(body2))
	req2.Header.Set("Content-Type", "application/json")
	resp2, _ := app.Test(req2, 5000)
	assert.Equal(t, 400, resp2.StatusCode)
}

func TestContract_InboxCreate(t *testing.T) {
	app := setupApp()
	body, _ := json.Marshal(map[string]any{"raw_content": "hello"})
	req := httptest.NewRequest("POST", "/api/v1/inbox", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := app.Test(req, 5000)
	assert.Equal(t, 200, resp.StatusCode)
	body2, _ := json.Marshal(map[string]any{})
	req2 := httptest.NewRequest("POST", "/api/v1/inbox", bytes.NewReader(body2))
	req2.Header.Set("Content-Type", "application/json")
	resp2, _ := app.Test(req2, 5000)
	assert.Equal(t, 400, resp2.StatusCode)
}

func TestContract_Estimate(t *testing.T) {
	app := setupApp()
	body, _ := json.Marshal(map[string]any{"title": "x"})
	req := httptest.NewRequest("POST", "/api/v1/tasks/estimate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := app.Test(req, 5000)
	assert.Equal(t, 200, resp.StatusCode)
}
