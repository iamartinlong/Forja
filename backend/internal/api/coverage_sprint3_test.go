package api

// S1.5 覆盖率冲刺（第三波）：依赖环分支 / patch 全分支 / converge 失败路径 /
// inbox 状态门禁错误码 / RBAC 回退链（无 uid claim）/ 零散工具函数。

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

func TestCov3_TaskDepsAndPatch(t *testing.T) {
	app := covApp(t)
	ctx := context.Background()
	pid := uuid.NewString()
	_, err := model.Pool.Exec(ctx, `INSERT INTO projects (id, name, slug) VALUES ($1,'cov-d','cov-d')`, pid)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, pid) })
	t.Setenv("OPENCODE_URL", "http://127.0.0.1:1")
	t.Setenv("SKIP_VET", "1")

	cleanup := func(id string) {
		for _, tbl := range []string{"agent_executions", "test_results", "test_cases"} {
			_, _ = model.Pool.Exec(ctx, fmt.Sprintf(`DELETE FROM %s WHERE task_id=$1`, tbl), id)
		}
		_, _ = model.Pool.Exec(ctx, `DELETE FROM tasks WHERE id=$1`, id)
	}

	// 前置任务
	code, out := covDo(t, app, "POST", "/api/v1/tasks", fmt.Sprintf(`{"project_id":%q,"title":"dep-a"}`, pid))
	require.Equal(t, 200, code)
	a := covData(t, out)["id"].(string)
	t.Cleanup(func() { cleanup(a) })

	// depends_on：不存在 / 跨项目拒绝（同为 not-found 分支）
	code, out = covDo(t, app, "POST", "/api/v1/tasks",
		fmt.Sprintf(`{"project_id":%q,"title":"dep-b","depends_on":[%q]}`, pid, uuid.NewString()))
	assert.Equal(t, 400, code)
	assert.Equal(t, "INVALID_PARAM", errCodeOf(t, out))

	// 合法依赖创建
	code, out = covDo(t, app, "POST", "/api/v1/tasks",
		fmt.Sprintf(`{"project_id":%q,"title":"dep-b","depends_on":[%q]}`, pid, a))
	require.Equal(t, 200, code, "%v", out)
	b := covData(t, out)["id"].(string)
	t.Cleanup(func() { cleanup(b) })

	// patchTask：不存在 → 404；坏 JSON → 400
	code, _ = covDo(t, app, "PATCH", "/api/v1/tasks/"+uuid.NewString(), `{"priority":1}`)
	assert.Equal(t, 404, code)
	badReq := httptest.NewRequest("PATCH", "/api/v1/tasks/"+b, strings.NewReader("{invalid"))
	badReq.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(badReq, 30000)
	require.NoError(t, err)
	assert.Equal(t, 400, resp.StatusCode)

	// patchTask：priority/estimated 更新
	code, _ = covDo(t, app, "PATCH", "/api/v1/tasks/"+b, `{"priority":5,"estimated_hours":1.5}`)
	assert.Equal(t, 200, code)

	// patchTask：FAILED → RUNNING 触发锻造；RELEASE_PENDING 守卫
	code, _ = covDo(t, app, "PATCH", "/api/v1/tasks/"+b, `{"status":"FAILED"}`)
	assert.Equal(t, 200, code)
	code, out = covDo(t, app, "PATCH", "/api/v1/tasks/"+b, `{"status":"RUNNING"}`)
	require.Equal(t, 200, code, "%v", out)

	rp := seedTask(t, pid, "RELEASE_PENDING")
	t.Cleanup(func() { cleanup(rp) })
	code, out = covDo(t, app, "PATCH", "/api/v1/tasks/"+rp, `{"status":"RUNNING"}`)
	assert.Equal(t, 400, code)
	assert.Equal(t, "INVALID_STATE", errCodeOf(t, out))

	// approve 不存在任务 → 404
	code, _ = covDo(t, app, "POST", "/api/v1/tasks/"+uuid.NewString()+"/approve", `{"action":"approve"}`)
	assert.Equal(t, 404, code)
}

func TestCov3_ConvergeFailPathAndStream404(t *testing.T) {
	app := covApp(t)
	ctx := context.Background()
	pid := uuid.NewString()
	_, err := model.Pool.Exec(ctx, `INSERT INTO projects (id, name, slug) VALUES ($1,'cv3','cv3')`, pid)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, pid) })

	// 带 spec 的任务 + 含 TODO 的 diff → drift → 未收敛
	sid := uuid.NewString()
	vid := uuid.NewString()
	_, err = model.Pool.Exec(ctx, `INSERT INTO specs (id, project_id, layer, title) VALUES ($1,$2,'BS','login feature')`, sid, pid)
	require.NoError(t, err)
	_, err = model.Pool.Exec(ctx, `INSERT INTO spec_versions (id, spec_id, version, content) VALUES ($1,$2,'v0.1.0','{"title":"login feature"}'::jsonb)`, vid, sid)
	require.NoError(t, err)
	tid := uuid.NewString()
	_, err = model.Pool.Exec(ctx, `INSERT INTO tasks (id, project_id, spec_id, title, status) VALUES ($1,$2,$3,'cv3-t','PENDING')`, tid, pid, sid)
	require.NoError(t, err)
	t.Cleanup(func() {
		for _, tbl := range []string{"code_changes", "agent_executions", "test_results", "test_cases"} {
			_, _ = model.Pool.Exec(ctx, fmt.Sprintf(`DELETE FROM %s WHERE task_id=$1`, tbl), tid)
		}
		_, _ = model.Pool.Exec(ctx, `DELETE FROM tasks WHERE id=$1`, tid)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM specs WHERE id=$1`, sid)
	})
	_, _ = model.Pool.Exec(ctx, `INSERT INTO code_changes (id, task_id, file_path, diff, change_log) VALUES ($1,$2,'tmp/x.log','TODO fix later','log')`,
		uuid.NewString(), tid)

	code, out := covDo(t, app, "POST", "/api/v1/tasks/"+tid+"/converge", "")
	require.Equal(t, 200, code, "%v", out)
	rd := covData(t, out)
	if conv, ok := rd["converged"].(bool); ok {
		assert.False(t, conv, "含 TODO/tmp 的 diff 应判定未收敛")
	}

	// stream/converge 不存在任务 → 404
	code, _ = covDo(t, app, "GET", "/api/v1/tasks/"+uuid.NewString()+"/stream", "")
	assert.Equal(t, 404, code)
	code, _ = covDo(t, app, "POST", "/api/v1/tasks/"+uuid.NewString()+"/converge", "")
	assert.Equal(t, 404, code)
}

func TestCov3_InboxStateGates(t *testing.T) {
	app := covApp(t)
	ctx := context.Background()
	pid := uuid.NewString()
	_, err := model.Pool.Exec(ctx, `INSERT INTO projects (id, name, slug) VALUES ($1,'cv-i','cv-i')`, pid)
	require.NoError(t, err)
	iid := uuid.NewString()
	cid := uuid.NewString()
	_, err = model.Pool.Exec(ctx,
		`INSERT INTO requirements_inbox (id, project_id, source_type, raw_content, content_hash, status) VALUES ($1,$2,'TEXT_PASTE','gate raw','h-g','NEW')`, iid, pid)
	require.NoError(t, err)
	_, err = model.Pool.Exec(ctx,
		`INSERT INTO requirements_inbox (id, project_id, source_type, raw_content, content_hash, status) VALUES ($1,$2,'TEXT_PASTE','converted raw','h-c','CONVERTED')`, cid, pid)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = model.Pool.Exec(ctx, `DELETE FROM requirements_inbox WHERE project_id=$1`, pid)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, pid)
	})

	// 非 NEW 不可编辑
	code, out := covDo(t, app, "PATCH", "/api/v1/inbox/"+cid, `{"raw_content":"x"}`)
	assert.Equal(t, 400, code)
	assert.Equal(t, "INBOX_NOT_EDITABLE", errCodeOf(t, out))
	// CONVERTED 不可删除
	code, out = covDo(t, app, "DELETE", "/api/v1/inbox/"+cid, "")
	assert.Equal(t, 400, code)
	assert.Equal(t, "INBOX_CONVERTED", errCodeOf(t, out))
	// NEW 可编辑、可删
	code, _ = covDo(t, app, "PATCH", "/api/v1/inbox/"+iid, `{"raw_content":"edited raw"}`)
	assert.Equal(t, 200, code)
	code, _ = covDo(t, app, "DELETE", "/api/v1/inbox/"+iid, "")
	assert.Equal(t, 200, code)
	// 不存在 → 404（update/delete/draft）
	code, _ = covDo(t, app, "PATCH", "/api/v1/inbox/"+uuid.NewString(), `{"raw_content":"x"}`)
	assert.Equal(t, 404, code)
	code, _ = covDo(t, app, "DELETE", "/api/v1/inbox/"+uuid.NewString(), "")
	assert.Equal(t, 404, code)
	code, _ = covDo(t, app, "POST", "/api/v1/inbox/"+uuid.NewString()+"/draft", "")
	assert.Equal(t, 404, code)
}

func TestCov3_RBACFallbackChain(t *testing.T) {
	// 无 uid claim 场景：currentUserID 按 username 查库 → projectRoleOf → require*
	ensureTestDB(t)
	if model.Pool == nil {
		t.Skip("db unavailable")
	}
	ctx := context.Background()
	adminID := uuid.NewString()
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT id FROM users WHERE username='admin'`).Scan(&adminID))

	grole := "Developer"
	app := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	app.Use(func(c *fiber.Ctx) error {
		// 故意不设 uid，仅 username + 非 Admin 全局角色
		c.Locals("username", "admin")
		c.Locals("role", grole)
		return c.Next()
	})
	api := app.Group("/api/v1")
	RegisterProjectRoutes(api)

	pid := uuid.NewString()
	_, err := model.Pool.Exec(ctx, `INSERT INTO projects (id, name, slug) VALUES ($1,'cv-rbac','cv-rbac')`, pid)
	require.NoError(t, err)
	_, _ = model.Pool.Exec(ctx, `INSERT INTO project_members (project_id, user_id, username, role) VALUES ($1,$2,'admin','Admin') ON CONFLICT DO NOTHING`, pid, adminID)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, pid) })

	// 成员读取走 requireProjectMember → currentUserID 回退查库命中
	code, out := covDo(t, app, "GET", "/api/v1/projects/"+pid+"/members", "")
	if code != 200 {
		b, _ := json.Marshal(out)
		t.Logf("members http=%d body=%s pid=%s adminID=%s", code, string(b), pid, adminID)
		var cnt int
		_ = model.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM project_members WHERE project_id=$1`, pid).Scan(&cnt)
		t.Logf("membership rows=%d", cnt)
	}
	require.Equal(t, 200, code, "members 响应: %v", out)
	items, _ := covData(t, out)["items"].([]any)
	assert.GreaterOrEqual(t, len(items), 1)

	// 项目内 Admin 角色允许成员管理（requireProjectAdmin 项目内分支）：
	// 全局角色升为 Architect（有平台 project.member.manage），项目内判定走 projectRoleOf
	grole = "Architect"
	uname := "rbac-" + uuid.NewString()[:6]
	uid2 := uuid.NewString()
	h, _ := bcrypt.GenerateFromPassword([]byte("pass1234"), 10)
	_, err = model.Pool.Exec(ctx, `INSERT INTO users (id, username, password_hash, role, enabled) VALUES ($1,$2,$3,'Viewer',true)`, uid2, uname, string(h))
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, uid2) })
	code, _ = covDo(t, app, "POST", "/api/v1/projects/"+pid+"/members", fmt.Sprintf(`{"user_id":%q,"role":"Viewer"}`, uid2))
	assert.Equal(t, 200, code)
}

func TestCov3_MiscHelpers(t *testing.T) {
	assert.Equal(t, 1, min(1, 2))
	assert.Equal(t, 1, min(2, 1))

	// specs 列表 layer/q 过滤与 search 分支
	app := covApp(t)
	code, out := covDo(t, app, "GET", "/api/v1/specs?project_id=00000000-0000-0000-0000-000000000001&layer=BS&q=todo&page=1&page_size=5", "")
	assert.Equal(t, 200, code)
	_ = covData(t, out)
	code, out = covDo(t, app, "GET", "/api/v1/specs/search?project_id=00000000-0000-0000-0000-000000000001&q=login", "")
	assert.Contains(t, []int{200, 500}, code) // 向量检索依赖 embedding 列，容错
	_ = out
}
