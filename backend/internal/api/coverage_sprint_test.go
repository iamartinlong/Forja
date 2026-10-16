package api

// S1.5 覆盖率冲刺：补齐 0% 覆盖的 handler——
// 用户 CRUD / 成员管理 / Pact / Trace / 发布签核与打回 / runForge 升级路径 / getInbox / 工具函数。
// 依赖 Postgres，无 DB 跳过（runForge 用不可达 OpenCode 触发 stub→ESCALATED，无外部依赖）。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"forja/backend/internal/api/respond"
	"forja/backend/internal/model"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

func covApp(t *testing.T) *fiber.App {
	t.Helper()
	ensureTestDB(t)
	adminID := uuid.NewString()
	_ = model.Pool.QueryRow(context.Background(), `SELECT id FROM users WHERE username='admin'`).Scan(&adminID)
	app := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("username", "cov-admin")
		c.Locals("role", "Admin")
		c.Locals("uid", adminID)
		return c.Next()
	})
	api := app.Group("/api/v1")
	RegisterProjectRoutes(api)
	RegisterUserRoutes(api)
	RegisterSpecRoutes(api)
	RegisterPactRoutes(api)
	RegisterTaskRoutes(api)
	RegisterEstimateRoutes(api)
	RegisterInboxRoutes(api)
	RegisterAuditRoutes(api)
	return app
}

func covDo(t *testing.T, app *fiber.App, method, path, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, 30000)
	require.NoError(t, err)
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func covData(t *testing.T, out map[string]any) map[string]any {
	t.Helper()
	d, _ := out["data"].(map[string]any)
	require.NotNil(t, d, "envelope data 缺失: %v", out)
	return d
}

func errCodeOf(t *testing.T, out map[string]any) string {
	t.Helper()
	e, _ := out["error"].(map[string]any)
	c, _ := e["code"].(string)
	return c
}

// ---------- 用户 CRUD ----------

func TestCov_UsersCRUD(t *testing.T) {
	app := covApp(t)

	// 弱密码拒绝（S1.3 策略）
	code, out := covDo(t, app, "POST", "/api/v1/admin/users", `{"username":"weak-`+uuid.NewString()[:6]+`","password":"short"}`)
	assert.Equal(t, 400, code)
	assert.Equal(t, "INVALID_PARAM", errCodeOf(t, out))

	// 正常创建 + 重复冲突
	uname := "cov-" + uuid.NewString()[:8]
	uid := uuid.NewString()
	code, out = covDo(t, app, "POST", "/api/v1/admin/users",
		fmt.Sprintf(`{"username":%q,"password":"pass1234","email":"c@t.io","role":"QA"}`, uname))
	require.Equal(t, 200, code, "%v", out)
	assert.Equal(t, "QA", covData(t, out)["role"])
	code, _ = covDo(t, app, "POST", "/api/v1/admin/users", fmt.Sprintf(`{"username":%q,"password":"pass1234"}`, uname))
	assert.Equal(t, 409, code, "重复用户名应 409")

	t.Cleanup(func() { _, _ = model.Pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, uid) })

	// 列表：q 过滤 + 分页参数
	code, out = covDo(t, app, "GET", "/api/v1/admin/users?q="+uname+"&page=1&page_size=5", "")
	require.Equal(t, 200, code)
	d := covData(t, out)
	items, _ := d["items"].([]any)
	assert.NotEmpty(t, items)
	assert.GreaterOrEqual(t, d["total"], float64(1))

	// 更新 email/role；弱密码 400；强密码成功
	code, out = covDo(t, app, "PATCH", "/api/v1/admin/users/"+uname,
		`{"email":"upd@t.io","role":"Developer","password":"short"}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "PATCH", "/api/v1/admin/users/"+uname,
		`{"email":"upd@t.io","role":"Developer","password":"newpass99"}`)
	assert.Equal(t, 200, code)

	// 禁用/启用
	code, _ = covDo(t, app, "POST", "/api/v1/admin/users/"+uname+"/disable", "")
	assert.Equal(t, 200, code)
	var enabled bool
	_ = model.Pool.QueryRow(context.Background(), `SELECT enabled FROM users WHERE username=$1`, uname).Scan(&enabled)
	assert.False(t, enabled)
	code, _ = covDo(t, app, "POST", "/api/v1/admin/users/"+uname+"/enable", "")
	assert.Equal(t, 200, code)

	// 删除
	code, _ = covDo(t, app, "DELETE", "/api/v1/admin/users/"+uname, "")
	assert.Equal(t, 200, code)
	code, _ = covDo(t, app, "DELETE", "/api/v1/admin/users/"+uname, "")
	assert.Equal(t, 404, code)
}

// ---------- 项目成员管理 ----------

func TestCov_MembersCRUD(t *testing.T) {
	app := covApp(t)
	ctx := context.Background()

	// 建项目 + owner + 两个成员用户
	pid := uuid.NewString()
	_, err := model.Pool.Exec(ctx, `INSERT INTO projects (id, name, slug) VALUES ($1,'cov-mem','cov-mem')`, pid)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, pid) })
	adminID := uuid.NewString()
	_ = model.Pool.QueryRow(ctx, `SELECT id FROM users WHERE username='admin'`).Scan(&adminID)
	_, _ = model.Pool.Exec(ctx, `UPDATE projects SET owner_id=$2 WHERE id=$1`, pid, adminID)
	// owner 需在 project_members 中才会出现在成员列表
	_, _ = model.Pool.Exec(ctx, `INSERT INTO project_members (project_id, user_id, username, role) VALUES ($1,$2,'cov-admin','Admin') ON CONFLICT DO NOTHING`, pid, adminID)

	mkUser := func(tag string, enabled bool) string {
		u := uuid.NewString()
		h, _ := bcrypt.GenerateFromPassword([]byte("pass1234"), 10)
		_, err := model.Pool.Exec(ctx, `INSERT INTO users (id, username, password_hash, role, enabled) VALUES ($1,$2,$3,'Developer',$4)`,
			u, "mem-"+tag+"-"+uuid.NewString()[:6], string(h), enabled)
		require.NoError(t, err)
		return u
	}
	devA := mkUser("a", true)
	disabled := mkUser("d", false)
	t.Cleanup(func() {
		for _, u := range []string{devA, disabled} {
			_, _ = model.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, u)
		}
	})

	// addMember：坏 uuid / 非法角色 / 禁用用户 / 正常
	code, _ := covDo(t, app, "POST", "/api/v1/projects/not-a-uuid/members", `{}`)
	assert.Equal(t, 400, code)
	code, out := covDo(t, app, "POST", "/api/v1/projects/"+pid+"/members", fmt.Sprintf(`{"user_id":%q,"role":"Bogus"}`, devA))
	assert.Equal(t, 400, code)
	code, out = covDo(t, app, "POST", "/api/v1/projects/"+pid+"/members", fmt.Sprintf(`{"user_id":%q}`, disabled))
	assert.Equal(t, 400, code, "禁用用户应被拒: %v", out)
	assert.Equal(t, "USER_DISABLED", errCodeOf(t, out))
	code, out = covDo(t, app, "POST", "/api/v1/projects/"+pid+"/members", fmt.Sprintf(`{"user_id":%q,"role":"Developer"}`, devA))
	require.Equal(t, 200, code, "%v", out)
	code, _ = covDo(t, app, "POST", "/api/v1/projects/"+pid+"/members", fmt.Sprintf(`{"user_id":%q}`, uuid.NewString()))
	assert.Equal(t, 404, code, "不存在的用户应 404")

	// listMembers
	code, out = covDo(t, app, "GET", "/api/v1/projects/"+pid+"/members", "")
	require.Equal(t, 200, code)
	items, _ := covData(t, out)["items"].([]any)
	assert.GreaterOrEqual(t, len(items), 2, "owner+devA 应在列")

	// updateMember：owner 锁定 / 正常变更
	code, out = covDo(t, app, "PATCH", "/api/v1/projects/"+pid+"/members/"+adminID, `{"role":"Developer"}`)
	assert.Equal(t, 400, code)
	assert.Equal(t, "OWNER_ROLE_LOCKED", errCodeOf(t, out))
	code, _ = covDo(t, app, "PATCH", "/api/v1/projects/"+pid+"/members/"+devA, `{"role":"Viewer"}`)
	assert.Equal(t, 200, code)

	// removeMember：owner 保护 / 正常移除
	code, out = covDo(t, app, "DELETE", "/api/v1/projects/"+pid+"/members/"+adminID, "")
	assert.Equal(t, 400, code)
	assert.Equal(t, "OWNER_UNREMOVABLE", errCodeOf(t, out))
	code, _ = covDo(t, app, "DELETE", "/api/v1/projects/"+pid+"/members/"+devA, "")
	assert.Equal(t, 200, code)
}

// ---------- Pact 生成 ----------

func TestCov_Pact(t *testing.T) {
	app := covApp(t)
	ctx := context.Background()
	pid := uuid.NewString()
	_, err := model.Pool.Exec(ctx, `INSERT INTO projects (id, name, slug) VALUES ($1,'cov-pact','cov-pact')`, pid)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, pid) })

	specBody := map[string]any{
		"project_id": pid, "layer": "IS", "title": "cov-is",
		"content": map[string]any{
			"paths": map[string]any{
				"/ping": map[string]any{"get": map[string]any{}},
				"/echo": map[string]any{"post": map[string]any{}},
			},
		},
	}
	b, _ := json.Marshal(specBody)
	code, out := covDo(t, app, "POST", "/api/v1/specs", string(b))
	require.Equal(t, 200, code, "%v", out)
	sid := covData(t, out)["id"].(string)

	// POST pact：生成 interactions 并落 test_cases + 合成任务
	code, out = covDo(t, app, "POST", "/api/v1/specs/"+sid+"/pact", "")
	require.Equal(t, 200, code, "%v", out)
	pactMap, _ := covData(t, out)["pact"].(map[string]any)
	interactions, _ := pactMap["interactions"].([]any)
	assert.GreaterOrEqual(t, len(interactions), 2, "两个 path 应生成 ≥2 interactions")

	// GET pact 即时重生成（data 即 pact 对象本身）
	code, out = covDo(t, app, "GET", "/api/v1/specs/"+sid+"/pact", "")
	assert.Equal(t, 200, code)
	gi, _ := covData(t, out)["interactions"].([]any)
	assert.GreaterOrEqual(t, len(gi), 2)

	// trace：spec 关联任务与代码变更聚合
	var tid string
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT id FROM tasks WHERE spec_id=$1 LIMIT 1`, sid).Scan(&tid))
	_, _ = model.Pool.Exec(ctx, `INSERT INTO code_changes (id, task_id, file_path, diff, change_log) VALUES ($1,$2,'a.go','diff','log')`,
		uuid.NewString(), tid)
	code, out = covDo(t, app, "GET", "/api/v1/specs/"+sid+"/trace", "")
	assert.Equal(t, 200, code)
	td := covData(t, out)
	tasks, _ := td["tasks"].([]any)
	codes, _ := td["code_changes"].([]any)
	assert.Len(t, tasks, 1)
	assert.Len(t, codes, 1)
	code, _ = covDo(t, app, "GET", "/api/v1/specs/not-a-uuid/trace", "")
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "GET", "/api/v1/specs/"+uuid.NewString()+"/trace", "")
	assert.Equal(t, 404, code)
}

// ---------- 发布签核 / 打回 ----------

func seedTask(t *testing.T, pid, status string) string {
	t.Helper()
	id := uuid.NewString()
	_, err := model.Pool.Exec(context.Background(),
		`INSERT INTO tasks (id, project_id, title, status) VALUES ($1,$2,'cov-rel',$3)`, id, pid, status)
	require.NoError(t, err)
	return id
}

func TestCov_ReleaseReject(t *testing.T) {
	app := covApp(t)
	ctx := context.Background()
	pid := uuid.NewString()
	_, err := model.Pool.Exec(ctx, `INSERT INTO projects (id, name, slug) VALUES ($1,'cov-rel','cov-rel')`, pid)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, pid) })

	// RELEASE_PENDING → release
	okTask := seedTask(t, pid, "RELEASE_PENDING")
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM tasks WHERE id=$1`, okTask) })
	code, out := covDo(t, app, "POST", "/api/v1/tasks/"+okTask+"/release", "")
	require.Equal(t, 200, code, "%v", out)
	assert.Equal(t, "RELEASED", covData(t, out)["status"])
	// RELEASED 再 release/reject → 业务规则拒绝
	code, out = covDo(t, app, "POST", "/api/v1/tasks/"+okTask+"/release", "")
	assert.Equal(t, 400, code)
	assert.Equal(t, "NOT_RELEASE_PENDING", errCodeOf(t, out))

	// PASSED 兼容路径 → release
	passedTask := seedTask(t, pid, "PASSED")
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM tasks WHERE id=$1`, passedTask) })
	code, out = covDo(t, app, "POST", "/api/v1/tasks/"+passedTask+"/release", "")
	assert.Equal(t, 200, code)

	// PENDING → release/reject 均 NOT_RELEASE_PENDING
	pendTask := seedTask(t, pid, "PENDING")
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM tasks WHERE id=$1`, pendTask) })
	code, _ = covDo(t, app, "POST", "/api/v1/tasks/"+pendTask+"/release", "")
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "POST", "/api/v1/tasks/"+pendTask+"/reject", `{"comment":"nope"}`)
	assert.Equal(t, 400, code)

	// RELEASE_PENDING → reject 意见留痕
	rejTask := seedTask(t, pid, "RELEASE_PENDING")
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM tasks WHERE id=$1`, rejTask) })
	code, out = covDo(t, app, "POST", "/api/v1/tasks/"+rejTask+"/reject", `{"comment":"覆盖率不足打回"}`)
	require.Equal(t, 200, code, "%v", out)
	assert.Equal(t, "FAILED", covData(t, out)["status"])
	var ar *string
	_ = model.Pool.QueryRow(ctx, `SELECT action_required FROM tasks WHERE id=$1`, rejTask).Scan(&ar)
	require.NotNil(t, ar)
	assert.Contains(t, *ar, "覆盖率不足")

	// 不存在的任务 → 404
	code, _ = covDo(t, app, "POST", "/api/v1/tasks/"+uuid.NewString()+"/release", "")
	assert.Equal(t, 404, code)
}

// ---------- runForge：OpenCode 不可达 → stub → ESCALATED ----------

func TestCov_RunForge_EscalatedOnAgentDown(t *testing.T) {
	covApp(t) // 仅初始化 DB 与连接池
	ctx := context.Background()
	pid := uuid.NewString()
	_, err := model.Pool.Exec(ctx, `INSERT INTO projects (id, name, slug) VALUES ($1,'cov-forge','cov-forge')`, pid)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, pid) })

	tid := seedTask(t, pid, "PENDING")
	t.Cleanup(func() {
		_, _ = model.Pool.Exec(ctx, `DELETE FROM agent_executions WHERE task_id=$1`, tid)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM test_cases WHERE task_id=$1`, tid)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM test_results WHERE task_id=$1`, tid)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM tasks WHERE id=$1`, tid)
	})

	t.Setenv("OPENCODE_URL", "http://127.0.0.1:1") // 不可达
	t.Setenv("SKIP_VET", "1")
	RunForge(ctx, tid)

	// S3.5.3 执行真实性：Agent 不可达 → FAILED（不再 stub 出码假升级 ESCALATED）
	var status string
	var reason *string
	var attempt int
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT status, action_required, forge_attempt FROM tasks WHERE id=$1`, tid).Scan(&status, &reason, &attempt))
	assert.Equal(t, "FAILED", status, "agent 不可达必须真实失败")
	assert.NotNil(t, reason, "应记录失败原因")
	assert.Contains(t, *reason, "unavailable")
	assert.Equal(t, 1, attempt, "首轮即判不可达")

	// 不写假执行记录（旧 stub 会落一条 SUCCESS）
	var execCount int
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM agent_executions WHERE task_id=$1`, tid).Scan(&execCount))
	assert.Equal(t, 0, execCount, "不可达不得伪造 SUCCESS 执行记录")
}

// ---------- getInbox 与工具函数 ----------

func TestCov_GetInbox_And_Helpers(t *testing.T) {
	app := covApp(t)
	_ = app
	ctx := context.Background()
	pid := uuid.NewString()
	_, err := model.Pool.Exec(ctx, `INSERT INTO projects (id, name, slug) VALUES ($1,'cov-inbox','cov-inbox')`, pid)
	require.NoError(t, err)
	iid := uuid.NewString()
	_, err = model.Pool.Exec(ctx,
		`INSERT INTO requirements_inbox (id, project_id, source_type, raw_content, content_hash) VALUES ($1,$2,'TEXT_PASTE','cov inbox raw','h1')`,
		iid, pid)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = model.Pool.Exec(ctx, `DELETE FROM requirements_inbox WHERE id=$1`, iid)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, pid)
	})

	code, out := covDo(t, app, "GET", "/api/v1/inbox/"+iid, "")
	assert.Equal(t, 200, code)
	assert.Equal(t, "cov inbox raw", covData(t, out)["raw_content"])

	// nullableUUID / jsonMarshal 边界
	assert.Nil(t, nullableUUID(""))
	assert.NotNil(t, nullableUUID(uuid.NewString()))
	good, err := jsonMarshal(map[string]int{"a": 1})
	assert.NoError(t, err)
	assert.Contains(t, string(good), `"a"`)
	bad, err := jsonMarshal(make(chan int)) // 无法序列化 → 回退 "[]"
	assert.Error(t, err)
	assert.Equal(t, "[]", string(bad))
}
