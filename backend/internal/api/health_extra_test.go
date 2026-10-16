package api

import (
	"net/http/httptest"
	"testing"

	"forja/backend/internal/api/respond"
	"forja/backend/internal/model"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHealth_ReadinessBranches(t *testing.T) {
	// Pool nil -> 503
	saved := model.Pool
	model.Pool = nil
	app := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	app.Get("/ready", ReadinessHandler())
	req := httptest.NewRequest("GET", "/ready", nil)
	resp, err := app.Test(req, 3000)
	require.NoError(t, err)
	assert.Equal(t, 503, resp.StatusCode)
	model.Pool = saved

	// With DB -> should be 200 or 503 depending on DB health, but not 404
	newPipelineApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	app2 := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	app2.Get("/ready", ReadinessHandler())
	req2 := httptest.NewRequest("GET", "/ready", nil)
	resp2, err := app2.Test(req2, 5000)
	require.NoError(t, err)
	assert.NotEqual(t, 404, resp2.StatusCode)
}
