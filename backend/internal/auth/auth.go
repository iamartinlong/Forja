package auth

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"forja/backend/internal/api/respond"
	"forja/backend/internal/model"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

// LegacyDevSecret 历史开发默认密钥。生产环境（FORJA_ENV=production）检测到
// JWT_SECRET 为空、等于该值或长度不足 32 时拒绝启动（cmd/server enforceProductionPolicy）。
const LegacyDevSecret = "forja-dev-secret-please-change"

var jwtSecret = func() []byte {
	s := os.Getenv("JWT_SECRET")
	if s == "" {
		log.Println("WARN: JWT_SECRET unset, falling back to legacy dev secret; production startup will be refused")
		s = LegacyDevSecret
	}
	return []byte(s)
}()

// Secret 暴露签名密钥供测试使用
func Secret() []byte { return jwtSecret }

type Claims struct {
	Username string `json:"username"`
	Role     string `json:"role"`
	UserID   string `json:"uid,omitempty"`
	jwt.RegisteredClaims
}

func GenerateToken(username, role, uid string) (string, error) {
	claims := Claims{
		Username: username,
		Role:     role,
		UserID:   uid,
		RegisteredClaims: jwt.RegisteredClaims{
			// S1.2：访问令牌默认 15min（env FORJA_ACCESS_TOKEN_TTL_MINUTES），
			// 配合 refresh 静默续期，将禁用用户的残余有效窗压缩到分钟级
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(AccessTokenTTL())),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "forja",
		},
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return t.SignedString(jwtSecret)
}

func logLoginAttempt(username, userAgent string, uid *string, success bool, reason string) {
	// UA 排障，去 IP；失败不阻主流程
	if model.Pool == nil {
		return
	}
	// 异步不阻塞登录（已在 60s ctx 内，直接同步小开销可接受；失败静默）
	_, _ = model.Pool.Exec(context.Background(),
		`INSERT INTO login_logs (user_id, username, user_agent, success, failure_reason) VALUES ($1,$2,$3,$4,$5)`,
		uid, strings.TrimSpace(username), userAgent, success, reason)
}

func LoginHandler(c *fiber.Ctx) error {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.BodyParser(&body); err != nil {
		return respond.BadRequest(c, err.Error())
	}
	// DB only — 硬编码明文密码回退已移除
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	ua := c.Get("User-Agent")
	uname := strings.TrimSpace(body.Username)
	var uid, hash, role string
	var enabled bool
	err := model.Pool.QueryRow(context.Background(), `SELECT id, password_hash, role, enabled FROM users WHERE username=$1`, uname).Scan(&uid, &hash, &role, &enabled)
	if err != nil {
		logLoginAttempt(uname, ua, nil, false, "INVALID")
		return respond.Unauthorized(c, "invalid credentials")
	}
	// 密码优先校验 → 再判禁用（兼顾枚举防护与体验）：
	// 错密与不存在统一 "invalid credentials"；仅密码正确但禁用时返回 ACCOUNT_DISABLED，
	// 使"账户被禁用"需先破密才能探测，面收敛至已破密子集。
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(body.Password)) != nil {
		logLoginAttempt(uname, ua, &uid, false, "INVALID")
		return respond.Unauthorized(c, "invalid credentials")
	}
	if !enabled {
		log.Printf("login rejected: account %q is disabled (auth passed)", uname)
		logLoginAttempt(uname, ua, &uid, false, "ACCOUNT_DISABLED")
		return respond.Fail(c, fiber.StatusForbidden, respond.CodeForbidden, "forbidden", "ACCOUNT_DISABLED", "account disabled")
	}
	// 登录打点（A7 收尾）：记录最后登录时间；失败不影响登录主流程
	_, _ = model.Pool.Exec(context.Background(), `UPDATE users SET last_login_at=now() WHERE id=$1`, uid)
	logLoginAttempt(uname, ua, &uid, true, "")
	token, err := GenerateToken(body.Username, role, uid)
	if err != nil {
		return respond.Internal(c, err)
	}
	// S1.2：签发 refresh token（AES-256-GCM 落库，7 天轮换）
	refresh := ""
	if rt, rerr := IssueRefreshToken(context.Background(), uid); rerr == nil {
		refresh = rt
	} else {
		log.Printf("refresh: issue failed for %q: %v", body.Username, rerr)
	}
	return respond.OK(c, fiber.Map{
		"token": token, "role": role, "username": body.Username,
		"refresh_token": refresh,
		"expires_in":    int(AccessTokenTTL().Seconds()),
	})
}

// RefreshHandler 刷新访问令牌（S1.2）：校验+轮换 refresh token，重放即失效。
// 用户被禁用 → 全量吊销并 401。公开端点，IP 限流由 ratelimit.IPMiddleware 承担。
func RefreshHandler(c *fiber.Ctx) error {
	var body struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := c.BodyParser(&body); err != nil || body.RefreshToken == "" {
		return respond.InvalidParam(c, "refresh_token", "refresh_token required")
	}
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	newRaw, uid, err := RotateRefreshToken(context.Background(), body.RefreshToken)
	if err != nil {
		if !errors.Is(err, ErrRefreshInvalid) {
			log.Printf("refresh: rotate error: %v", err)
		}
		return respond.Unauthorized(c, "invalid refresh token")
	}
	var username, role string
	var enabled bool
	err = model.Pool.QueryRow(context.Background(),
		`SELECT username, role, enabled FROM users WHERE id=$1`, uid).Scan(&username, &role, &enabled)
	if err != nil || !enabled {
		RevokeUserRefreshTokens(context.Background(), uid)
		log.Printf("refresh: user %s missing or disabled", uid)
		return respond.Unauthorized(c, "invalid refresh token")
	}
	token, err := GenerateToken(username, role, uid)
	if err != nil {
		return respond.Internal(c, err)
	}
	return respond.OK(c, fiber.Map{
		"token": token, "refresh_token": newRaw, "role": role, "username": username,
		"expires_in": int(AccessTokenTTL().Seconds()),
	})
}

// LogoutHandler 登出：吊销传入的 refresh token（幂等，始终 200）。
func LogoutHandler(c *fiber.Ctx) error {
	var body struct {
		RefreshToken string `json:"refresh_token"`
	}
	_ = c.BodyParser(&body)
	if body.RefreshToken != "" {
		RevokeSingle(context.Background(), body.RefreshToken)
	}
	return respond.OK(c, fiber.Map{"logged_out": true})
}

func JWTMiddleware(c *fiber.Ctx) error {
	auth := c.Get("Authorization")
	if auth == "" {
		// SSE 专用：EventSource 无法携带自定义 header，允许 ?access_token= 查询参数。
		// token 进 URL 会被代理日志记录——TTL 已压至 15min（S1.2），可接受
		if qt := c.Query("access_token"); qt != "" {
			auth = "Bearer " + qt
		}
	}
	if auth == "" {
		return respond.Unauthorized(c, "missing token")
	}
	parts := strings.SplitN(auth, " ", 2)
	var tokenStr string
	if len(parts) == 2 && strings.ToLower(parts[0]) == "bearer" {
		tokenStr = parts[1]
	} else {
		tokenStr = auth
	}
	t, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		// 防算法混淆：仅接受 HS256
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return jwtSecret, nil
	})
	if err != nil || !t.Valid {
		return respond.Unauthorized(c, "invalid token")
	}
	claims, ok := t.Claims.(*Claims)
	if !ok {
		return respond.Unauthorized(c, "invalid claims")
	}
	c.Locals("role", claims.Role)
	c.Locals("username", claims.Username)
	c.Locals("uid", claims.UserID)
	return c.Next()
}

// ---------- 双层 RBAC ----------
//
// 平台级动作：按 JWT 全局角色判定（项目治理 + 用户管理）
var platformPermissions = map[string][]string{
	"Admin":     {"project.list", "project.create", "project.update", "project.delete", "project.member.manage", "user.list", "user.create", "user.update", "user.delete", "template.manage"},
	"Architect": {"project.list", "project.create", "project.update", "project.member.manage", "template.manage"},
	"Developer": {"project.list"},
	"Viewer":    {"project.list"},
	"PM":        {"project.list"},
	"QA":        {"project.list"},
}

// 项目级动作：生效角色 = 项目内角色(project_members.role)，全局 Admin 超级覆盖；
// 实际判定在 handler 内通过 api.AuthorizeProject 执行，Require 仅做认证门
var projectPermissions = map[string][]string{
	"Admin":     {"spec.read", "spec.write", "spec.validate", "task.read", "task.write", "task.approve", "task.run", "task.release", "iteration.release", "project.deliver", "inbox.read", "inbox.write", "inbox.convert", "inbox.delete", "inbox.uat", "dag.read", "audit.read", "file.read", "file.write"},
	"Architect": {"spec.read", "spec.write", "spec.validate", "task.read", "task.write", "task.approve", "task.run", "task.release", "iteration.release", "project.deliver", "inbox.read", "inbox.write", "inbox.convert", "inbox.delete", "inbox.uat", "dag.read", "audit.read", "file.read", "file.write"},
	"Developer": {"spec.read", "spec.write", "task.read", "task.write", "task.approve", "task.run", "inbox.read", "inbox.write", "inbox.convert", "inbox.delete", "inbox.uat", "dag.read", "audit.read", "file.read", "file.write"},
	"Viewer":    {"spec.read", "task.read", "inbox.read", "inbox.uat", "dag.read", "audit.read", "file.read"},
	// PM：需求录入→草拟→转换 BS；CONVERTED 删除由既有状态门禁拦截；项目资源库可读写；可生成交付包
	"PM": {"spec.read", "task.read", "project.deliver", "inbox.read", "inbox.write", "inbox.convert", "inbox.delete", "inbox.uat", "dag.read", "audit.read", "file.read", "file.write"},
	// QA：补 TS 场景断言 + 重跑验证；编写者与放行者分离（不可 approve/release/inbox 写），资源库只读
	"QA": {"spec.read", "spec.write", "task.read", "task.run", "inbox.uat", "dag.read", "audit.read", "file.read"},
}

// 旧动作别名 → 新动作（过渡兼容，路由已全部迁移后可移除）
var legacyAliases = map[string]string{
	"task.list":   "task.read",
	"task.create": "task.write",
	"spec.create": "spec.write",
}

func resolveAction(action string) string {
	if a, ok := legacyAliases[action]; ok {
		return a
	}
	return action
}

func isProjectAction(action string) bool {
	for _, allowed := range projectPermissions {
		for _, a := range allowed {
			if a == action {
				return true
			}
		}
	}
	return false
}

func isValidRole(role string) bool {
	_, ok := platformPermissions[role]
	return ok
}

// HasPermission 平台级权限判定；项目级动作此处仅要求合法角色，细粒度在 handler 内判
func HasPermission(role, action string) bool {
	a := resolveAction(action)
	if isProjectAction(a) {
		return isValidRole(role)
	}
	allowed, ok := platformPermissions[role]
	if !ok {
		return false
	}
	for _, x := range allowed {
		if x == a {
			return true
		}
	}
	return false
}

// HasProjectPermission 项目级权限判定（role 为项目内生效角色）
func HasProjectPermission(role, action string) bool {
	a := resolveAction(action)
	allowed, ok := projectPermissions[role]
	if !ok {
		return false
	}
	for _, x := range allowed {
		if x == a {
			return true
		}
	}
	return false
}

func Require(action string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		role, _ := c.Locals("role").(string)
		if role == "" {
			role = "Viewer"
		}
		if !HasPermission(role, action) {
			return respond.Forbidden(c, "role "+role+" not allowed for "+action)
		}
		return c.Next()
	}
}
