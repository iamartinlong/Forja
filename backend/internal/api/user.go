package api

import (
	"context"
	"strconv"
	"strings"
	"time"

	"forja/backend/internal/api/respond"
	"forja/backend/internal/auth"
	idgen "forja/backend/internal/id"
	"forja/backend/internal/model"
	"forja/backend/internal/ratelimit"

	"github.com/gofiber/fiber/v2"
	"golang.org/x/crypto/bcrypt"
)

func RegisterUserRoutes(r fiber.Router) {
	r.Get("/admin/users", auth.Require("user.list"), listUsers)
	r.Get("/admin/users/:id", auth.Require("user.list"), getUser)
	r.Get("/admin/users/:id/login-logs", auth.Require("user.list"), listLoginLogs)
	r.Get("/auth/me/login-logs", listMyLoginLogs)
	r.Post("/admin/users", auth.Require("user.create"), createUser)
	r.Patch("/admin/users/:id", auth.Require("user.update"), updateUser)
	r.Delete("/admin/users/:id", auth.Require("user.delete"), deleteUser)
	r.Post("/admin/users/:id/disable", auth.Require("user.update"), disableUser)
	r.Post("/admin/users/:id/enable", auth.Require("user.update"), enableUser)
	// S1.1：改密限流（含旧密码试错防爆破，KI-005），按 uid 计数
	r.Post("/auth/change-password", ratelimit.AuthedMiddleware, changePassword)
	r.Get("/auth/me", me)
}

// me 个人中心：当前用户基本信息 + 全部项目成员身份（A7）
func me(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	uid := currentUserID(c)
	if uid == "" {
		return respond.Unauthorized(c, "missing user identity")
	}
	ctx := context.Background()
	var username, email, role string
	var createdAt time.Time
	var lastLogin *time.Time
	if err := model.Pool.QueryRow(ctx,
		`SELECT username, COALESCE(email,''), role, created_at, last_login_at FROM users WHERE id=$1`, uid).
		Scan(&username, &email, &role, &createdAt, &lastLogin); err != nil {
		return respond.DBError(c, err)
	}
	rows, err := model.Pool.Query(ctx,
		`SELECT pm.project_id, p.name, pm.role FROM project_members pm
		 JOIN projects p ON p.id = pm.project_id
		 WHERE pm.user_id=$1 ORDER BY p.name`, uid)
	if err != nil {
		return respond.DBError(c, err)
	}
	defer rows.Close()
	memberships := []fiber.Map{}
	for rows.Next() {
		var pid, pname, prole string
		if err := rows.Scan(&pid, &pname, &prole); err != nil {
			return respond.DBError(c, err)
		}
		memberships = append(memberships, fiber.Map{"project_id": pid, "project_name": pname, "role": prole})
	}
	resp := fiber.Map{
		"username":    username,
		"email":       email,
		"global_role": role,
		"created_at":  createdAt.Format(time.RFC3339),
		"memberships": memberships,
	}
	if lastLogin != nil {
		resp["last_login_at"] = lastLogin.Format(time.RFC3339)
	} else {
		resp["last_login_at"] = nil
	}
	return respond.OK(c, resp)
}

// resolveUserParam 兼容 :id（UUIDv7/ULID 36/26）与旧 username 透传：
// IsValid→按 id 查，否则按 username 查，返回 id 与 username。
func resolveUserParam(ctx context.Context, param string) (string, string, bool) {
	if idgen.IsValid(param) {
		var uname string
		if err := model.Pool.QueryRow(ctx, `SELECT username FROM users WHERE id=$1`, param).Scan(&uname); err == nil {
			return param, uname, true
		}
	}
	// fallback username
	var id, uname string
	if err := model.Pool.QueryRow(ctx, `SELECT id, username FROM users WHERE username=$1`, param).Scan(&id, &uname); err == nil {
		return id, uname, true
	}
	return "", "", false
}

// getUser 单用户明细 + 成员项目（Admin 专属，U-6）— RESTful :id（兼容 username）
func getUser(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	param := c.Params("id")
	ctx := context.Background()
	idStr, _, ok := resolveUserParam(ctx, param)
	if !ok {
		if !idgen.IsValid(param) {
			var exists bool
			_ = model.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE username=$1)`, param).Scan(&exists)
			if !exists {
				return respond.InvalidParam(c, "id", "invalid user id")
			}
		}
		return respond.NotFound(c, "user")
	}
	var id string
	var username string
	var emailPtr *string
	var role string
	var enabled bool
	var createdAt, updatedAt time.Time
	var lastLogin *time.Time
	err := model.Pool.QueryRow(ctx,
		`SELECT id, username, COALESCE(email,''), role, enabled, created_at, updated_at, last_login_at FROM users WHERE id=$1`, idStr).
		Scan(&id, &username, &emailPtr, &role, &enabled, &createdAt, &updatedAt, &lastLogin)
	if err != nil {
		return respond.NotFound(c, "user")
	}
	email := ""
	if emailPtr != nil {
		email = *emailPtr
	}
	var lastLoginStr any
	if lastLogin != nil {
		lastLoginStr = lastLogin.Format(time.RFC3339)
	}
	// 成员项目
	rows, err := model.Pool.Query(ctx,
		`SELECT pm.project_id, p.name, pm.role, pm.joined_at FROM project_members pm JOIN projects p ON p.id=pm.project_id WHERE pm.user_id=$1 ORDER BY p.name`, id)
	if err != nil {
		return respond.DBError(c, err)
	}
	defer rows.Close()
	memberships := []fiber.Map{}
	for rows.Next() {
		var pid, pname, prole string
		var joinedAt time.Time
		if err := rows.Scan(&pid, &pname, &prole, &joinedAt); err != nil {
			return respond.DBError(c, err)
		}
		memberships = append(memberships, fiber.Map{"project_id": pid, "project_name": pname, "role": prole, "joined_at": joinedAt.Format(time.RFC3339)})
	}
	// 近期审计（actor=username）
	aRows, _ := model.Pool.Query(ctx,
		`SELECT action, target_type, detail, created_at FROM audit_logs WHERE actor=$1 ORDER BY created_at DESC LIMIT 10`, username)
	var audits []fiber.Map
	if aRows != nil {
		defer aRows.Close()
		for aRows.Next() {
			var action, targetType, detail string
			var at time.Time
			if err := aRows.Scan(&action, &targetType, &detail, &at); err == nil {
				audits = append(audits, fiber.Map{"action": action, "target_type": targetType, "detail": detail, "created_at": at.Format(time.RFC3339)})
			}
		}
	}
	return respond.OK(c, fiber.Map{
		"id": id, "username": username, "email": email, "role": role, "enabled": enabled,
		"created_at": createdAt.Format(time.RFC3339), "updated_at": updatedAt.Format(time.RFC3339), "last_login_at": lastLoginStr,
		"memberships": memberships, "recent_audits": audits,
	})
}

// listLoginLogs 指定用户的登录日志（Admin，分页，success 过滤，去 IP，仅 UA）— RESTful :id（兼容 username）
func listLoginLogs(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	param := c.Params("id")
	idStr, _, ok := resolveUserParam(context.Background(), param)
	if !ok {
		return respond.NotFound(c, "user")
	}
	page, pageSize := respond.ParsePage(c)
	successFilter := c.Query("success")
	where := ` WHERE user_id=$1`
	args := []interface{}{idStr}
	idx := 2
	if successFilter == "true" || successFilter == "false" {
		where += ` AND success=$` + strconv.Itoa(idx)
		args = append(args, successFilter == "true")
		idx++
	}
	var total int64
	if err := model.Pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM login_logs`+where, args...).Scan(&total); err != nil {
		return respond.DBError(c, err)
	}
	query := `SELECT id, user_id, username, user_agent, success, failure_reason, created_at FROM login_logs` + where + ` ORDER BY created_at DESC LIMIT $` + strconv.Itoa(idx) + ` OFFSET $` + strconv.Itoa(idx+1)
	args = append(args, pageSize, (page-1)*pageSize)
	rows, err := model.Pool.Query(context.Background(), query, args...)
	if err != nil {
		return respond.DBError(c, err)
	}
	defer rows.Close()
	items := []fiber.Map{}
	for rows.Next() {
		var id, uid *string
		var uname, ua, reason *string
		var success bool
		var at time.Time
		if err := rows.Scan(&id, &uid, &uname, &ua, &success, &reason, &at); err != nil {
			return respond.DBError(c, err)
		}
		uaStr := ""
		if ua != nil {
			uaStr = *ua
		}
		reasonStr := ""
		if reason != nil {
			reasonStr = *reason
		}
		items = append(items, fiber.Map{"id": *id, "user_id": uid, "username": *uname, "user_agent": uaStr, "success": success, "failure_reason": reasonStr, "created_at": at.Format(time.RFC3339)})
	}
	return respond.List(c, items, respond.PageMeta{Page: page, PageSize: pageSize, Total: total})
}

// listMyLoginLogs 本人登录日志（/auth/me/login-logs，免 Admin）
func listMyLoginLogs(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	uid := currentUserID(c)
	if uid == "" {
		return respond.Unauthorized(c, "missing user identity")
	}
	page, pageSize := respond.ParsePage(c)
	successFilter := c.Query("success")
	where := ` WHERE user_id=$1`
	args := []interface{}{uid}
	idx := 2
	if successFilter == "true" || successFilter == "false" {
		where += ` AND success=$` + strconv.Itoa(idx)
		args = append(args, successFilter == "true")
		idx++
	}
	var total int64
	if err := model.Pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM login_logs`+where, args...).Scan(&total); err != nil {
		return respond.DBError(c, err)
	}
	query := `SELECT id, user_id, username, user_agent, success, failure_reason, created_at FROM login_logs` + where + ` ORDER BY created_at DESC LIMIT $` + strconv.Itoa(idx) + ` OFFSET $` + strconv.Itoa(idx+1)
	args = append(args, pageSize, (page-1)*pageSize)
	rows, err := model.Pool.Query(context.Background(), query, args...)
	if err != nil {
		return respond.DBError(c, err)
	}
	defer rows.Close()
	items := []fiber.Map{}
	for rows.Next() {
		var id, uid2, uname, ua, reason *string
		var success bool
		var at time.Time
		if err := rows.Scan(&id, &uid2, &uname, &ua, &success, &reason, &at); err != nil {
			return respond.DBError(c, err)
		}
		uaStr := ""
		if ua != nil {
			uaStr = *ua
		}
		reasonStr := ""
		if reason != nil {
			reasonStr = *reason
		}
		items = append(items, fiber.Map{"id": *id, "user_id": uid2, "username": *uname, "user_agent": uaStr, "success": success, "failure_reason": reasonStr, "created_at": at.Format(time.RFC3339)})
	}
	return respond.List(c, items, respond.PageMeta{Page: page, PageSize: pageSize, Total: total})
}

// changePassword 自助修改密码（A7）：验证旧密码后更新，审计留痕
func changePassword(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	var body struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := c.BodyParser(&body); err != nil {
		return respond.BadRequest(c, err.Error())
	}
	if body.OldPassword == "" || body.NewPassword == "" {
		return respond.InvalidParam(c, "password", "old_password/new_password required")
	}
	// 密码策略基线（S1.3 / KI-005）
	if err := auth.ValidatePasswordStrength(body.NewPassword); err != nil {
		return respond.InvalidParam(c, "password", err.Error())
	}
	uid := currentUserID(c)
	if uid == "" {
		return respond.Unauthorized(c, "missing user identity")
	}
	username, _ := c.Locals("username").(string)
	ctx := context.Background()
	var hash string
	if err := model.Pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE id=$1`, uid).Scan(&hash); err != nil {
		return respond.DBError(c, err)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(body.OldPassword)) != nil {
		return respond.InvalidParam(c, "old_password", "old password incorrect")
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(body.NewPassword), 10)
	if err != nil {
		return respond.Internal(c, err)
	}
	if _, err := model.Pool.Exec(ctx, `UPDATE users SET password_hash=$2, updated_at=now() WHERE id=$1`, uid, string(newHash)); err != nil {
		return respond.DBError(c, err)
	}
	// S1.2：改密后全量吊销 refresh token（其他设备强制重登；当前设备 access 到期后同样需重登）
	auth.RevokeUserRefreshTokens(ctx, uid)
	logAudit(ctx, model.DefaultProjectID, username, "auth.change_password", "user", uid, "{}")
	return respond.OK(c, fiber.Map{"changed": true})
}

func listUsers(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	q := strings.TrimSpace(c.Query("q"))
	page, pageSize := respond.ParsePage(c)
	query := `SELECT id, username, email, role, enabled, created_at, last_login_at FROM users`
	where := ``
	var args []interface{}
	if q != "" {
		where = ` WHERE username ILIKE $1 OR email ILIKE $1`
		args = append(args, "%"+q+"%")
	}
	var total int64
	if err := model.Pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM users`+where, args...).Scan(&total); err != nil {
		return respond.DBError(c, err)
	}
	query += where + ` ORDER BY created_at, username LIMIT $` + strconv.Itoa(len(args)+1) + ` OFFSET $` + strconv.Itoa(len(args)+2)
	args = append(args, pageSize, (page-1)*pageSize)
	rows, err := model.Pool.Query(context.Background(), query, args...)
	if err != nil {
		return respond.DBError(c, err)
	}
	defer rows.Close()
	items := []fiber.Map{}
	for rows.Next() {
		var id string
		var username string
		var emailPtr *string
		var urole string
		var enabled bool
		var createdAt time.Time
		var lastLogin *time.Time
		if err := rows.Scan(&id, &username, &emailPtr, &urole, &enabled, &createdAt, &lastLogin); err != nil {
			return respond.DBError(c, err)
		}
		email := ""
		if emailPtr != nil {
			email = *emailPtr
		}
		var lastLoginStr any
		if lastLogin != nil {
			lastLoginStr = lastLogin.Format(time.RFC3339)
		}
		items = append(items, fiber.Map{"id": id, "username": username, "email": email, "role": urole, "enabled": enabled, "created_at": createdAt.Format(time.RFC3339), "last_login_at": lastLoginStr})
	}
	if err := rows.Err(); err != nil {
		return respond.DBError(c, err)
	}
	return respond.List(c, items, respond.PageMeta{Page: page, PageSize: pageSize, Total: total})
}

func createUser(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Email    string `json:"email"`
		Role     string `json:"role"`
	}
	if err := c.BodyParser(&body); err != nil {
		return respond.BadRequest(c, err.Error())
	}
	body.Username = strings.TrimSpace(body.Username)
	body.Role = strings.TrimSpace(body.Role)
	if body.Username == "" || body.Password == "" {
		return respond.InvalidParam(c, "username,password", "username/password required")
	}
	// 密码策略基线（S1.3 / KI-005）
	if err := auth.ValidatePasswordStrength(body.Password); err != nil {
		return respond.InvalidParam(c, "password", err.Error())
	}
	if body.Role == "" {
		body.Role = "Developer"
	}
	if !map[string]bool{"Admin": true, "Architect": true, "Developer": true, "Viewer": true, "PM": true, "QA": true}[body.Role] {
		return respond.InvalidParam(c, "role", "invalid role")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(body.Password), 10)
	if err != nil {
		return respond.Internal(c, err)
	}
	id := idgen.New()
	_, err = model.Pool.Exec(context.Background(), `INSERT INTO users (id, username, email, password_hash, role) VALUES ($1,$2,$3,$4,$5)`, id, body.Username, body.Email, string(hash), body.Role)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate") || strings.Contains(err.Error(), "unique") {
			return respond.Conflict(c, "username exists")
		}
		return respond.DBError(c, err)
	}
	// auto add to default project as Viewer? Let admin decide via project_members; but add to forja as role
	_, _ = model.Pool.Exec(context.Background(), `INSERT INTO project_members (project_id, user_id, username, role) VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING`, model.DefaultProjectID, id, body.Username, body.Role)
	return respond.OK(c, fiber.Map{"id": id, "username": body.Username, "role": body.Role})
}

func disableUser(c *fiber.Ctx) error {
	param := c.Params("id")
	idStr, _, ok := resolveUserParam(context.Background(), param)
	if !ok {
		return respond.NotFound(c, "user")
	}
	uid := currentUserID(c)
	if idStr == uid {
		return respond.BusinessRule(c, "SELF_OPERATION", "cannot disable yourself")
	}
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	ct, err := model.Pool.Exec(context.Background(), `UPDATE users SET enabled=false, updated_at=now() WHERE id=$1`, idStr)
	if err != nil {
		return respond.DBError(c, err)
	}
	if ct.RowsAffected() == 0 {
		return respond.NotFound(c, "user")
	}
	auth.RevokeUserRefreshTokens(context.Background(), idStr)
	var username string
	_ = model.Pool.QueryRow(context.Background(), `SELECT username FROM users WHERE id=$1`, idStr).Scan(&username)
	return respond.OK(c, fiber.Map{"id": idStr, "username": username, "enabled": false})
}

func enableUser(c *fiber.Ctx) error {
	param := c.Params("id")
	idStr, _, ok := resolveUserParam(context.Background(), param)
	if !ok {
		return respond.NotFound(c, "user")
	}
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	ct, err := model.Pool.Exec(context.Background(), `UPDATE users SET enabled=true, updated_at=now() WHERE id=$1`, idStr)
	if err != nil {
		return respond.DBError(c, err)
	}
	if ct.RowsAffected() == 0 {
		return respond.NotFound(c, "user")
	}
	var username string
	_ = model.Pool.QueryRow(context.Background(), `SELECT username FROM users WHERE id=$1`, idStr).Scan(&username)
	return respond.OK(c, fiber.Map{"id": idStr, "username": username, "enabled": true})
}

func updateUser(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	param := c.Params("id")
	idStr, _, ok := resolveUserParam(context.Background(), param)
	if !ok {
		return respond.NotFound(c, "user")
	}
	var body struct {
		Email    *string `json:"email"`
		Role     *string `json:"role"`
		Password *string `json:"password"`
	}
	if err := c.BodyParser(&body); err != nil {
		return respond.BadRequest(c, err.Error())
	}
	if body.Role != nil && !map[string]bool{"Admin": true, "Architect": true, "Developer": true, "Viewer": true, "PM": true, "QA": true}[*body.Role] {
		return respond.InvalidParam(c, "role", "invalid role")
	}
	// 校验存在并取 username 供审计/返回
	var username string
	if err := model.Pool.QueryRow(context.Background(), `SELECT username FROM users WHERE id=$1`, idStr).Scan(&username); err != nil {
		return respond.NotFound(c, "user")
	}
	if body.Email != nil {
		if _, err := model.Pool.Exec(context.Background(), `UPDATE users SET email=$2, updated_at=now() WHERE id=$1`, idStr, *body.Email); err != nil {
			return respond.DBError(c, err)
		}
	}
	if body.Role != nil {
		if _, err := model.Pool.Exec(context.Background(), `UPDATE users SET role=$2, updated_at=now() WHERE id=$1`, idStr, *body.Role); err != nil {
			return respond.DBError(c, err)
		}
		if _, err := model.Pool.Exec(context.Background(), `UPDATE project_members SET role=$1 WHERE user_id=$2`, *body.Role, idStr); err != nil {
			return respond.DBError(c, err)
		}
	}
	if body.Password != nil && *body.Password != "" {
		if err := auth.ValidatePasswordStrength(*body.Password); err != nil {
			return respond.InvalidParam(c, "password", err.Error())
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(*body.Password), 10)
		if err != nil {
			return respond.Internal(c, err)
		}
		if _, err := model.Pool.Exec(context.Background(), `UPDATE users SET password_hash=$2, updated_at=now() WHERE id=$1`, idStr, string(hash)); err != nil {
			return respond.DBError(c, err)
		}
		auth.RevokeUserRefreshTokens(context.Background(), idStr)
	}
	return respond.OK(c, fiber.Map{"id": idStr, "username": username})
}

func deleteUser(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	param := c.Params("id")
	idStr, _, ok := resolveUserParam(context.Background(), param)
	if !ok {
		return respond.NotFound(c, "user")
	}
	uid := currentUserID(c)
	if idStr == uid {
		return respond.BusinessRule(c, "SELF_OPERATION", "cannot delete yourself")
	}
	ct, err := model.Pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, idStr)
	if err != nil {
		return respond.DBError(c, err)
	}
	if ct.RowsAffected() == 0 {
		return respond.NotFound(c, "user")
	}
	return respond.OK(c, fiber.Map{"id": idStr})
}
