package api

import (
	"forja/backend/internal/api/respond"
	"forja/backend/internal/auth"
	idgen "forja/backend/internal/id"
	"forja/backend/internal/model"

	"github.com/gofiber/fiber/v2"
)

func RegisterReleaseRoutes(r fiber.Router) {
	r.Get("/projects/:id/releases", auth.Require("task.read"), listReleases)
}

func listReleases(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	projectID := c.Params("id")
	if !idgen.IsValid(projectID) {
		return respond.InvalidParam(c, "id", "invalid project id")
	}
	if err := AuthorizeProject(c, projectID, "task.read"); err != nil {
		return err
	}
	iterationID := c.Query("iteration_id")
	args := []any{projectID}
	where := ` WHERE t.project_id=$1 AND t.status='RELEASED'`
	if iterationID != "" {
		if !idgen.IsValid(iterationID) {
			return respond.InvalidParam(c, "iteration_id", "invalid iteration id")
		}
		where += ` AND t.target_iteration_id=$2`
		args = append(args, iterationID)
	}
	rows, err := model.Pool.Query(c.Context(), `SELECT t.id, t.title, t.commit_sha, t.updated_at, t.spec_id, COALESCE(s.title,'') as spec_title
	  FROM tasks t LEFT JOIN specs s ON s.id=t.spec_id`+where+` ORDER BY t.updated_at DESC LIMIT 50`, args...)
	if err != nil {
		return respond.DBError(c, err)
	}
	defer rows.Close()
	items := []fiber.Map{}
	for rows.Next() {
		var id, title string
		var commitSHA *string
		var updatedAt any
		var specID *string
		var specTitle string
		_ = rows.Scan(&id, &title, &commitSHA, &updatedAt, &specID, &specTitle)
		tag := ""
		if commitSHA != nil && *commitSHA != "" {
			tag = "forja/" + shortID(id) + "-" + shortID(*commitSHA)
		}
		items = append(items, fiber.Map{"id": id, "title": title, "commit_sha": commitSHA, "tag": tag, "released_at": updatedAt, "spec_id": specID, "spec_title": specTitle})
	}
	return respond.OK(c, fiber.Map{"items": items})
}
