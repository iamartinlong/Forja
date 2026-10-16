package api

import (
	"forja/backend/internal/api/respond"
	"forja/backend/internal/auth"
	"forja/backend/internal/estimate"

	"github.com/gofiber/fiber/v2"
)

func RegisterEstimateRoutes(r fiber.Router) {
	r.Post("/tasks/estimate", auth.Require("task.write"), estimateTask)
}

func estimateTask(c *fiber.Ctx) error {
	var body struct {
		ProjectID string `json:"project_id"`
		Title     string `json:"title"`
		Priority  *int   `json:"priority"`
	}
	if err := c.BodyParser(&body); err != nil {
		return respond.BadRequest(c, err.Error())
	}
	if body.Title == "" {
		return respond.InvalidParam(c, "title", "title required")
	}
	if body.ProjectID == "" {
		return respond.InvalidParam(c, "project_id", "project_id required")
	}
	pid := body.ProjectID
	if err := AuthorizeProject(c, pid, "task.write"); err != nil {
		return err
	}
	prio := 5
	if body.Priority != nil {
		prio = *body.Priority
	}
	hours, deviation, err := estimate.Estimate(c.Context(), pid, body.Title, prio)
	if err != nil {
		return respond.Upstream(c, "ESTIMATE_FAILED", err)
	}
	return respond.OK(c, fiber.Map{"estimated_hours": hours, "deviation": deviation, "model": "history_avg"})
}
