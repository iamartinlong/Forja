package api

// S2/B1 存量向量回填：为 embedding 为空的 spec_versions / requirements_inbox
// 批量补算向量（真实 API 或 mock 降级，由 vector 包决定）。Admin 专属。
// 单次调用有上限保护，避免长事务阻塞；不足部分可重复调用直至 skipped=0。

import (
	"context"

	"forja/backend/internal/api/respond"
	"forja/backend/internal/auth"
	"forja/backend/internal/model"
	"forja/backend/internal/vector"

	"github.com/gofiber/fiber/v2"
)

const reindexBatchLimit = 500

func RegisterReindexRoutes(r fiber.Router) {
	r.Post("/admin/reindex-embeddings", auth.Require("user.update"), reindexEmbeddings)
}

func reindexEmbeddings(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	ctx := context.Background()
	var specs, inbox int

	// spec_versions：取最新版本内容做文本源（与增量链路一致）
	rows, err := model.Pool.Query(ctx, `
		SELECT DISTINCT ON (sv.id) sv.id, sv.content::text
		FROM spec_versions sv
		WHERE sv.embedding IS NULL
		ORDER BY sv.id, sv.created_at DESC
		LIMIT $1`, reindexBatchLimit)
	if err != nil {
		return respond.DBError(c, err)
	}
	type item struct{ id, text string }
	var specItems []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.id, &it.text); err == nil {
			specItems = append(specItems, it)
		}
	}
	rows.Close()
	for _, it := range specItems {
		if err := vector.StoreSpecEmbedding(ctx, it.id, it.text); err == nil {
			specs++
		}
	}

	// requirements_inbox
	rows2, err := model.Pool.Query(ctx, `
		SELECT id, raw_content FROM requirements_inbox
		WHERE embedding IS NULL LIMIT $1`, reindexBatchLimit)
	if err != nil {
		return respond.DBError(c, err)
	}
	var inboxItems []item
	for rows2.Next() {
		var it item
		if err := rows2.Scan(&it.id, &it.text); err == nil {
			inboxItems = append(inboxItems, it)
		}
	}
	rows2.Close()
	for _, it := range inboxItems {
		if err := vector.StoreInboxEmbedding(ctx, it.id, it.text); err == nil {
			inbox++
		}
	}

	username, _ := c.Locals("username").(string)
	logAudit(ctx, "", username, "admin.reindex_embeddings", "system", "", `{}`)
	return respond.OK(c, fiber.Map{
		"spec_versions": specs,
		"inbox":         inbox,
		"remaining_hint": "重复调用直到两项均为 0（每轮上限 " +
			reindexBatchLimitLabel() + "）",
	})
}

func reindexBatchLimitLabel() string { return "500" }
