package api

// 双层 RBAC 归属校验辅助（A4）：
// 项目级动作在 handler 内通过 AuthorizeProject 判定；
// 平台级动作由 auth.Require 中间件判定。

import (
	"context"
	"strings"
	"time"

	"forja/backend/internal/api/respond"
	"forja/backend/internal/auth"
	"forja/backend/internal/model"

	"github.com/gofiber/fiber/v2"
	idgen "forja/backend/internal/id"
)

// currentUserID 当前登录用户 id：优先 JWT uid claim，回退按 username 查库
func currentUserID(c *fiber.Ctx) string {
	if uid, ok := c.Locals("uid").(string); ok && uid != "" {
		return uid
	}
	if model.Pool == nil {
		return ""
	}
	username, _ := c.Locals("username").(string)
	if username == "" || username == "anonymous" {
		return ""
	}
	var id string
	err := model.Pool.QueryRow(context.Background(), `SELECT id FROM users WHERE username=$1`, username).Scan(&id)
	if err != nil {
		return ""
	}
	return id
}

// isGlobalAdmin 当前操作者是否全局 Admin（超级管理员）
func isGlobalAdmin(c *fiber.Ctx) bool {
	role, _ := c.Locals("role").(string)
	return role == "Admin"
}

// projectRoleOf uid 在项目内的角色；非成员返回 ""
func projectRoleOf(projectID, uid string) string {
	if model.Pool == nil || uid == "" {
		return ""
	}
	var r string
	err := model.Pool.QueryRow(context.Background(), `SELECT role FROM project_members WHERE project_id=$1 AND user_id=$2`, projectID, uid).Scan(&r)
	if err != nil {
		return ""
	}
	return r
}

// effectiveRole 生效角色：全局 Admin 超级覆盖；否则取项目内角色，非成员为 ""
func effectiveRole(c *fiber.Ctx, projectID string) string {
	if isGlobalAdmin(c) {
		return "Admin"
	}
	return projectRoleOf(projectID, currentUserID(c))
}

// guardNotArchived 归档项目写保护（S1.4 软删除，KI-002）：
// 已归档项目仅允许读动作与恢复/彻底删除；写类动作一律 409 PROJECT_ARCHIVED。
// 项目不存在时放行（由调用方后续逻辑返回 404）。
func guardNotArchived(c *fiber.Ctx, projectID string) error {
	if model.Pool == nil {
		return nil
	}
	var archivedAt *time.Time
	err := model.Pool.QueryRow(context.Background(),
		`SELECT archived_at FROM projects WHERE id=$1`, projectID).Scan(&archivedAt)
	if err != nil || archivedAt == nil {
		return nil
	}
	return respond.Fail(c, fiber.StatusConflict, respond.CodeBusinessRule,
		"business rule rejected", "PROJECT_ARCHIVED",
		"project is archived; restore it before modifying")
}

// AuthorizeProject 项目级动作判定：非成员 403；角色不满足动作要求 403；
// 归档项目的非读动作 409（S1.4）
func AuthorizeProject(c *fiber.Ctx, projectID, action string) error {
	if !idgen.IsValid(projectID) {
		return respond.InvalidParam(c, "project_id", "invalid project_id")
	}
	role := effectiveRole(c, projectID)
	if role == "" {
		return respond.Forbidden(c, "not a project member")
	}
	if !auth.HasProjectPermission(role, action) {
		return respond.Forbidden(c, "project role "+role+" not allowed for "+action)
	}
	if !strings.HasSuffix(action, ".read") {
		if err := guardNotArchived(c, projectID); err != nil {
			return err
		}
	}
	return nil
}

// requireProjectAdmin 成员管理归属校验：全局 Admin 或该项目内 Admin
func requireProjectAdmin(c *fiber.Ctx, projectID string) error {
	if isGlobalAdmin(c) {
		return nil
	}
	uid := currentUserID(c)
	if uid != "" && projectRoleOf(projectID, uid) == "Admin" {
		return nil
	}
	return respond.Forbidden(c, "project admin required")
}

// requireProjectMember 读取校验：全局 Admin 或该项目任一成员
func requireProjectMember(c *fiber.Ctx, projectID string) error {
	if isGlobalAdmin(c) {
		return nil
	}
	uid := currentUserID(c)
	if uid != "" && projectRoleOf(projectID, uid) != "" {
		return nil
	}
	return respond.Forbidden(c, "not a project member")
}
