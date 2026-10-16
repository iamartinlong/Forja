package api

import (
	"context"
	"encoding/json"

	"forja/backend/internal/api/respond"
	"forja/backend/internal/auth"
	"forja/backend/internal/model"
	"forja/backend/internal/testgen"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	idgen "forja/backend/internal/id"
)

func RegisterPactRoutes(r fiber.Router) {
	r.Post("/specs/:id/pact", auth.Require("spec.write"), generatePact)
	r.Get("/specs/:id/pact", auth.Require("spec.read"), getPact)
}

func generatePact(c *fiber.Ctx) error {
	id := c.Params("id")
	if !idgen.IsValid(id) {
		return respond.InvalidParam(c, "id", "invalid id")
	}
	ctx := context.Background()
	var content json.RawMessage
	var title, layer, projectID string
	err := model.Pool.QueryRow(ctx, `SELECT s.title, s.layer, s.project_id::text, sv.content FROM specs s JOIN spec_versions sv ON sv.spec_id=s.id WHERE s.id=$1 ORDER BY sv.created_at DESC LIMIT 1`, id).Scan(&title, &layer, &projectID, &content)
	if err != nil {
		return respond.NotFound(c, "spec")
	}
	if err := AuthorizeProject(c, projectID, "spec.write"); err != nil {
		return err
	}
	if layer != "IS" {
		return respond.BusinessRule(c, "LAYER_MISMATCH", "only IS can generate pact")
	}
	pact, err := testgen.GeneratePact(content, title)
	if err != nil {
		return respond.Upstream(c, "PACT_FAILED", err)
	}
	// persist as test_cases for contract
	taskID := uuid.NewString()
	// create a synthetic task to hold contract tests
	if _, err := model.Pool.Exec(ctx, `INSERT INTO tasks (id, project_id, spec_id, title, status) VALUES ($1,(SELECT project_id FROM specs WHERE id=$2),$2,$3,'PENDING')`, taskID, id, "Pact for "+title); err != nil {
		return respond.DBError(c, err)
	}
	for _, tc := range testgen.ToTestCases(pact) {
		name, _ := tc["name"].(string)
		if _, err := model.Pool.Exec(ctx, `INSERT INTO test_cases (id, task_id, name, framework) VALUES ($1,$2,$3,'pact')`, uuid.NewString(), taskID, name); err != nil {
			return respond.DBError(c, err)
		}
	}
	return respond.OK(c, fiber.Map{"pact": pact, "task_id": taskID})
}

func getPact(c *fiber.Ctx) error {
	id := c.Params("id")
	if !idgen.IsValid(id) {
		return respond.InvalidParam(c, "id", "invalid id")
	}
	ctx := context.Background()
	var content json.RawMessage
	var projectID string
	err := model.Pool.QueryRow(ctx, `SELECT s.project_id::text, sv.content FROM specs s JOIN spec_versions sv ON sv.spec_id=s.id WHERE s.id=$1 ORDER BY sv.created_at DESC LIMIT 1`, id).Scan(&projectID, &content)
	if err != nil {
		return respond.NotFound(c, "spec")
	}
	if err := AuthorizeProject(c, projectID, "spec.read"); err != nil {
		return err
	}
	pact, err := testgen.GeneratePact(content, "")
	if err != nil {
		return respond.Upstream(c, "PACT_FAILED", err)
	}
	return respond.OK(c, pact)
}
