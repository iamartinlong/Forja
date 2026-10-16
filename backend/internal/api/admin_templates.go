package api

// v2.1 模板库运维：POST /admin/templates/sync 强制刷新组织库缓存（Admin/Architect）。
// 拉取失败返回 502 且保留旧缓存（Snapshot 回退语义见 templates/registry.go）。

import (
	"context"
	"fmt"
	"time"

	"forja/backend/internal/api/respond"
	"forja/backend/internal/auth"
	"forja/backend/internal/gitea"
	"forja/backend/internal/templates"

	"github.com/gofiber/fiber/v2"
)

func RegisterAdminTemplateRoutes(r fiber.Router) {
	r.Post("/admin/templates/sync", auth.Require("template.manage"), syncTemplates)
}

func syncTemplates(c *fiber.Ctx) error {
	gc := gitea.NewFromEnv()
	if gc == nil {
		return respond.ServiceUnavailable(c)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	list, err := templates.Refresh(ctx, gc)
	if err != nil {
		return respond.Upstream(c, "templates", err)
	}
	items := []fiber.Map{}
	for _, t := range list {
		items = append(items, fiber.Map{
			"id": t.ID, "name": t.Name, "version": t.Version, "source": t.Source,
		})
	}
	username, _ := c.Locals("username").(string)
	logAudit(ctx, "", username, "templates.sync", "template", "", fmt.Sprintf(`{"count":%d}`, len(items)))
	return respond.OK(c, fiber.Map{"count": len(items), "items": items})
}
