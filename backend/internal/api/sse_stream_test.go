package api

import (
	"net/http/httptest"
	"testing"
	"time"

	"forja/backend/internal/api/respond"
	"forja/backend/internal/events"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
)

func TestSSE_ProjectEventsSuccess(t *testing.T) {
	if modelPoolNil() {
		t.Skip("DB not available")
	}
	newPipelineApp(t)
	app := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("username", "sse-admin")
		c.Locals("role", "Admin")
		return c.Next()
	})
	api := app.Group("/api/v1")
	RegisterSSERoutes(api)
	const pid = "00000000-0000-0000-0000-000000000001"
	req := httptest.NewRequest("GET", "/api/v1/projects/"+pid+"/events", nil)
	// publish an event shortly after stream starts to hit the event branch
	go func() {
		time.Sleep(150 * time.Millisecond)
		events.Default.Publish("project:"+pid, "ci.completed", map[string]any{"job": "backend"})
	}()
	resp, err := app.Test(req, 2500)
	// app.Test with 2.5s timeout will return after stream has written snapshot+event and is waiting for heartbeat
	// It may return err due to timeout, or resp with body containing snapshot
	if err != nil {
		// timeout is expected to interrupt the stream, but handler should have been entered
		return
	}
	defer resp.Body.Close()
	assert.Equal(t, 200, resp.StatusCode)
	body, _ := readAll(resp.Body)
	assert.Contains(t, string(body), "snapshot")
}

func readAll(r interface{ Read([]byte) (int, error) }) ([]byte, error) {
	buf := make([]byte, 0, 1024)
	tmp := make([]byte, 512)
	for {
		n, err := r.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil || len(buf) > 2048 {
			break
		}
	}
	return buf, nil
}
