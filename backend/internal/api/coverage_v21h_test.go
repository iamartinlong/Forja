package api

// v2.1 覆盖率冲刺 Phase 4b：validateAnchors/Refs 直测、cost 分组、UAT 缺口分支、
// spec 深分支与 lifecycle 聚合、inbox 转换/草拟、iteration patch 全分支、
// user 管理分支、项目构建（mock gitea）。

import (
	"context"
	"encoding/json"
	"net/http"
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

func covV21hApp(t *testing.T) *fiber.App {
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
	RegisterUserRoutes(api)
	RegisterProjectRoutes(api)
	RegisterSpecRoutes(api)
	RegisterInboxRoutes(api)
	RegisterIterationRoutes(api)
	RegisterCostRoutes(api)
	RegisterUATRoutes(api)
	RegisterDeliveryRoutes(api)
	RegisterReleaseRoutes(api)
	return app
}

func TestCovV21h_ValidateHelpers(t *testing.T) {
	ensureTestDB(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()

	// validateAnchors
	out, err := validateAnchors(nil)
	require.NoError(t, err)
	assert.Empty(t, out)
	_, err = validateAnchors(make([]string, 21))
	assert.Error(t, err)
	_, err = validateAnchors([]string{"  "})
	assert.Error(t, err)
	_, err = validateAnchors([]string{"a/" + strings.Repeat("b", 600)})
	assert.Error(t, err)
	_, err = validateAnchors([]string{"a/../b.go"})
	assert.Error(t, err)
	_, err = validateAnchors([]string{"a\\b.go"})
	assert.Error(t, err)
	_, err = validateAnchors([]string{"/a/b.go"})
	assert.Error(t, err)
	_, err = validateAnchors([]string{"nofile"})
	assert.Error(t, err)
	_, err = validateAnchors([]string{"a/b c.go"})
	assert.Error(t, err)
	out, err = validateAnchors([]string{"a/b.go", "a/b.go", "c/d.go"})
	require.NoError(t, err)
	assert.Equal(t, []string{"a/b.go", "c/d.go"}, out)

	// validateRefs
	pid := covV21Project(t, ctx, false)
	refs, err := validateRefs(ctx, pid, nil, "")
	require.NoError(t, err)
	assert.Empty(t, refs)
	_, err = validateRefs(ctx, pid, make([]string, 21), "")
	assert.Error(t, err)
	refs, err = validateRefs(ctx, pid, []string{"", " "}, "")
	require.NoError(t, err)
	assert.Empty(t, refs)
	_, err = validateRefs(ctx, pid, []string{"not-uuid"}, "")
	assert.Error(t, err)
	selfSpec := uuid.NewString()
	_, err = validateRefs(ctx, pid, []string{selfSpec}, selfSpec)
	assert.Error(t, err)
	_, err = validateRefs(ctx, pid, []string{uuid.NewString()}, "")
	assert.Error(t, err)
	// 同项目存在的 spec
	other := covV21Spec(t, ctx, pid, "BS")
	outRefs, err := validateRefs(ctx, pid, []string{other, other}, "self")
	require.NoError(t, err)
	assert.Equal(t, []string{other}, outRefs)
}

func TestCovV21h_CostGroupingAndUATGaps(t *testing.T) {
	app := covV21hApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	pid := covV21Project(t, ctx, false)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM tasks WHERE project_id=$1`, pid) })

	// cost：assignee / model / 非法分组 / 迭代不属项目
	taskID := uuid.NewString()
	_, err := model.Pool.Exec(ctx,
		`INSERT INTO tasks (id, project_id, title, status, assigned_to) VALUES ($1,$2,'cov21h cost','PASSED','dev-cov')`, taskID, pid)
	require.NoError(t, err)
	_, err = model.Pool.Exec(ctx,
		`INSERT INTO agent_executions (id, task_id, agent_type, model, cost_cents, tokens_in, tokens_out, duration_ms) VALUES (gen_random_uuid(),$1,'forge','cov-model',5,10,20,100)`, taskID)
	require.NoError(t, err)
	code, out := covDo(t, app, "GET", "/api/v1/projects/"+pid+"/cost?group_by=assignee", "")
	require.Equal(t, 200, code, "%v", out)
	code, out = covDo(t, app, "GET", "/api/v1/projects/"+pid+"/cost?group_by=model", "")
	require.Equal(t, 200, code, "%v", out)
	code, _ = covDo(t, app, "GET", "/api/v1/projects/"+pid+"/cost?group_by=bogus", "")
	assert.Equal(t, 400, code)
	otherIter := insertIteration(t, ctx, covV21Project(t, ctx, false), "active", "auto", "")
	code, _ = covDo(t, app, "GET", "/api/v1/projects/"+pid+"/cost?iteration_id="+otherIter, "")
	assert.Equal(t, 400, code)

	// UAT：项目 id 非法 / content 超限 / delivery_id 非法 / 附件非法
	code, _ = covDo(t, app, "POST", "/api/v1/projects/bad-id/uat", `{"result":"pass","content":"x"}`)
	assert.Equal(t, 400, code)
	big := strings.Repeat("x", 33*1024)
	code, _ = covDo(t, app, "POST", "/api/v1/projects/"+pid+"/uat", `{"result":"pass","content":"`+big+`"}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "POST", "/api/v1/projects/"+pid+"/uat", `{"result":"pass","content":"cov21h uat","delivery_id":"bad-id"}`)
	assert.Equal(t, 400, code)

	// UAT multipart：非法附件扩展名
	body, ct := multipartFile("file", "evil.exe", "MZ", map[string]string{"result": "pass", "content": "cov21h uat mp"})
	req := httptest.NewRequest("POST", "/api/v1/projects/"+pid+"/uat", body)
	req.Header.Set("Content-Type", ct)
	resp, err := app.Test(req, 15000)
	require.NoError(t, err)
	assert.Equal(t, 400, resp.StatusCode)

	// UAT multipart 成功（无附件）：走 FormValue 路径
	body, ct = multipartFile("file", "note.md", "# ok", map[string]string{"result": "partial", "content": "cov21h uat partial", "title": "t", "env": "staging"})
	req = httptest.NewRequest("POST", "/api/v1/projects/"+pid+"/uat", body)
	req.Header.Set("Content-Type", ct)
	resp, err = app.Test(req, 15000)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
}

func TestCovV21h_SpecDeepAndLifecycle(t *testing.T) {
	app := covV21hApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	pid := covV21Project(t, ctx, false)
	t.Cleanup(func() {
		_, _ = model.Pool.Exec(ctx, `DELETE FROM code_changes WHERE task_id IN (SELECT id FROM tasks WHERE project_id=$1)`, pid)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM tasks WHERE project_id=$1`, pid)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM requirement_spec_links WHERE spec_id IN (SELECT id FROM specs WHERE project_id=$1)`, pid)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM spec_versions WHERE spec_id IN (SELECT id FROM specs WHERE project_id=$1)`, pid)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM specs WHERE project_id=$1`, pid)
	})

	bsA := covV21Spec(t, ctx, pid, "BS")
	bsB := covV21Spec(t, ctx, pid, "BS")
	tsSpec := covV21Spec(t, ctx, pid, "TS")

	// updateSpec：成功路径（refs + anchors 持久化）
	code, out := covDo(t, app, "PUT", "/api/v1/specs/"+bsA,
		`{"title":"cov21h A","content":{"story_id":"S-1","title":"A"},"refs":["`+bsB+`"],"anchors":["src/a.go"]}`)
	require.Equal(t, 200, code, "%v", out)

	// updateSpec：非法 refs / 非法 anchors / 内容超限
	code, _ = covDo(t, app, "PUT", "/api/v1/specs/"+bsA, `{"title":"x","refs":["`+uuid.NewString()+`"]}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "PUT", "/api/v1/specs/"+bsA, `{"title":"x","anchors":["nofile"]}`)
	assert.Equal(t, 400, code)
	huge := `{"title":"x","content":{"pad":"` + strings.Repeat("p", 300*1024) + `"}}`
	code, _ = covDo(t, app, "PUT", "/api/v1/specs/"+bsA, huge)
	assert.Equal(t, 400, code)

	// tsDraft：非 BS 层 → 400；BS → 草稿；create=true → 建 TS
	code, _ = covDo(t, app, "POST", "/api/v1/specs/"+tsSpec+"/ts-draft", "")
	assert.Equal(t, 400, code)
	code, out = covDo(t, app, "POST", "/api/v1/specs/"+bsA+"/ts-draft", "")
	require.Equal(t, 200, code, "%v", out)
	code, out = covDo(t, app, "POST", "/api/v1/specs/"+bsA+"/ts-draft?create=true", "")
	require.Equal(t, 200, code, "%v", out)
	assert.NotEmpty(t, covData(t, out)["created_id"])

	// embedSpec：存在 spec 时尝试向量化（mock/无 provider 均允许）
	code, _ = covDo(t, app, "POST", "/api/v1/specs/"+bsA+"/embed", "")
	assert.Contains(t, []int{200, 502}, code)

	// searchSpecs：缺 project_id / 缺 q / 正常
	code, _ = covDo(t, app, "GET", "/api/v1/specs/search?q=x", "")
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "GET", "/api/v1/specs/search?project_id="+pid, "")
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "GET", "/api/v1/specs/search?project_id="+pid+"&q=cov", "")
	assert.Contains(t, []int{200, 502}, code)

	// specTimeline：limit 越界 / 正常
	code, _ = covDo(t, app, "GET", "/api/v1/specs/timeline?project_id="+pid+"&limit=999", "")
	assert.Contains(t, []int{200, 400}, code)
	code, _ = covDo(t, app, "GET", "/api/v1/specs/timeline?project_id="+pid, "")
	assert.Equal(t, 200, code)

	// lifecycle：refs 解析 + 需求反查 + 任务/变更/CI 聚合
	reqID := uuid.NewString()
	_, err := model.Pool.Exec(ctx,
		`INSERT INTO requirements_inbox (id, project_id, source_type, raw_content, status) VALUES ($1,$2,'TEXT_PASTE','cov21h raw','CONVERTED')`, reqID, pid)
	require.NoError(t, err)
	_, err = model.Pool.Exec(ctx, `INSERT INTO requirement_spec_links (requirement_id, spec_id) VALUES ($1,$2)`, reqID, bsA)
	require.NoError(t, err)
	sha := strings.Repeat("5a", 20)
	tid := uuid.NewString()
	_, err = model.Pool.Exec(ctx,
		`INSERT INTO tasks (id, project_id, spec_id, title, status, commit_sha, forge_attempt) VALUES ($1,$2,$3,'cov21h task','PASSED',$4,1)`,
		tid, pid, bsA, sha)
	require.NoError(t, err)
	_, err = model.Pool.Exec(ctx,
		`INSERT INTO code_changes (task_id, file_path, change_log, diff) VALUES ($1,'src/a.go','cov change','')`, tid)
	require.NoError(t, err)
	_, err = model.Pool.Exec(ctx,
		`INSERT INTO pipeline_runs (id, project_id, repo, sha, branch, event, status, duration_ms) VALUES (gen_random_uuid(),$1,'x/y',$2,'master','push','SUCCESS',1234)`,
		pid, sha)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM pipeline_runs WHERE project_id=$1`, pid) })

	code, out = covDo(t, app, "GET", "/api/v1/specs/"+bsA+"/lifecycle", "")
	require.Equal(t, 200, code, "%v", out)
	data := covData(t, out)
	refSpecs, _ := data["ref_specs"].([]any)
	assert.GreaterOrEqual(t, len(refSpecs), 1)
	reqs, _ := data["requirements"].([]any)
	assert.GreaterOrEqual(t, len(reqs), 1)
	tasksAny, _ := data["tasks"].([]any)
	assert.GreaterOrEqual(t, len(tasksAny), 1)
	cc, _ := data["code_changes"].([]any)
	assert.GreaterOrEqual(t, len(cc), 1)
	ci, _ := data["ci_runs"].([]any)
	assert.GreaterOrEqual(t, len(ci), 1)

	// lifecycle：非法 id / 不存在
	code, _ = covDo(t, app, "GET", "/api/v1/specs/bad-id/lifecycle", "")
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "GET", "/api/v1/specs/"+uuid.NewString()+"/lifecycle", "")
	assert.Equal(t, 404, code)

	// releases 列表（含 iteration 过滤非法值）
	code, _ = covDo(t, app, "GET", "/api/v1/projects/"+pid+"/releases?iteration_id=bad-id", "")
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "GET", "/api/v1/projects/"+pid+"/releases", "")
	assert.Equal(t, 200, code)
}

func TestCovV21h_InboxConvertDraft(t *testing.T) {
	app := covV21hApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	pid := covV21Project(t, ctx, false)
	t.Cleanup(func() {
		_, _ = model.Pool.Exec(ctx, `DELETE FROM requirement_spec_links WHERE spec_id IN (SELECT id FROM specs WHERE project_id=$1)`, pid)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM spec_versions WHERE spec_id IN (SELECT id FROM specs WHERE project_id=$1)`, pid)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM specs WHERE project_id=$1`, pid)
	})

	// createInbox：multipart 文本成功 / 非法扩展名 / 缺 project_id / JSON 非法
	body, ct := multipartFile("file", "note.md", "# cov21h inbox", nil)
	req := httptest.NewRequest("POST", "/api/v1/inbox", body)
	req.Header.Set("Content-Type", ct)
	resp, err := app.Test(req, 15000)
	require.NoError(t, err)
	assert.Equal(t, 400, resp.StatusCode, "multipart 缺 project_id → 400")

	body, ct = multipartFile("file", "evil.exe", "MZ", nil)
	req = httptest.NewRequest("POST", "/api/v1/inbox?project_id="+pid, body)
	req.Header.Set("Content-Type", ct)
	resp, err = app.Test(req, 15000)
	require.NoError(t, err)
	assert.Equal(t, 400, resp.StatusCode)

	// JSON 非法
	code, _ := covDo(t, app, "POST", "/api/v1/inbox", `{bad-json`)
	assert.Equal(t, 400, code)

	// 正常新建（JSON）
	code, out := covDo(t, app, "POST", "/api/v1/inbox", `{"project_id":"`+pid+`","source_type":"TEXT_PASTE","raw_content":"cov21h convert source"}`)
	require.Equal(t, 200, code, "%v", out)
	rid, _ := covData(t, out)["id"].(string)
	require.NotEmpty(t, rid)

	// draft：NEW → 200；重复 → 200 幂等
	code, out = covDo(t, app, "POST", "/api/v1/inbox/"+rid+"/draft", "")
	require.Equal(t, 200, code, "%v", out)
	code, _ = covDo(t, app, "POST", "/api/v1/inbox/"+rid+"/draft", "")
	assert.Equal(t, 200, code)

	// draft：非法 id / 不存在 / 不可草拟状态
	code, _ = covDo(t, app, "POST", "/api/v1/inbox/bad-id/draft", "")
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "POST", "/api/v1/inbox/"+uuid.NewString()+"/draft", "")
	assert.Equal(t, 404, code)
	discID := uuid.NewString()
	_, err = model.Pool.Exec(ctx,
		`INSERT INTO requirements_inbox (id, project_id, source_type, raw_content, status) VALUES ($1,$2,'TEXT_PASTE','x','DISCARDED')`, discID, pid)
	require.NoError(t, err)
	code, _ = covDo(t, app, "POST", "/api/v1/inbox/"+discID+"/draft", "")
	assert.Equal(t, 400, code)

	// convert：缺 ids / 缺 project / 非法 id / 不存在 / 项目不匹配 / DISCARDED / 成功
	code, _ = covDo(t, app, "POST", "/api/v1/inbox/convert", `{"project_id":"`+pid+`"}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "POST", "/api/v1/inbox/convert", `{"requirement_ids":["`+rid+`"]}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "POST", "/api/v1/inbox/convert", `{"requirement_ids":["bad-id"],"project_id":"`+pid+`"}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "POST", "/api/v1/inbox/convert", `{"requirement_ids":["`+uuid.NewString()+`"],"project_id":"`+pid+`"}`)
	assert.Equal(t, 404, code)
	otherPID := covV21Project(t, ctx, false)
	code, _ = covDo(t, app, "POST", "/api/v1/inbox/convert", `{"requirement_ids":["`+rid+`"],"project_id":"`+otherPID+`"}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "POST", "/api/v1/inbox/convert", `{"requirement_ids":["`+discID+`"],"project_id":"`+pid+`"}`)
	assert.Equal(t, 400, code)

	// 成功转换（title/AC/description 覆盖分支）
	code, out = covDo(t, app, "POST", "/api/v1/inbox/convert",
		`{"requirement_ids":["`+rid+`"],"project_id":"`+pid+`","title":"cov21h BS","description":"cov21h desc","acceptance_criteria":[{"given":"g","when":"w","then":"t"}]}`)
	require.Equal(t, 200, code, "%v", out)
	specID, _ := covData(t, out)["id"].(string)
	assert.NotEmpty(t, specID)
	// 已转换再转 → 400
	code, _ = covDo(t, app, "POST", "/api/v1/inbox/convert", `{"requirement_ids":["`+rid+`"],"project_id":"`+pid+`"}`)
	assert.Equal(t, 400, code)
}

func TestCovV21h_IterationPatchBranches(t *testing.T) {
	app := covV21hApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	pid := covV21Project(t, ctx, false)
	pid2 := covV21Project(t, ctx, false)

	itID := insertIteration(t, ctx, pid, "planned", "", "")
	otherID := insertIteration(t, ctx, pid, "planned", "", "")
	foreignID := insertIteration(t, ctx, pid2, "planned", "", "")

	code, _ := covDo(t, app, "PATCH", "/api/v1/projects/"+pid+"/iterations/bad-id", `{"name":"x"}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "PATCH", "/api/v1/projects/"+pid+"/iterations/"+uuid.NewString(), `{"name":"x"}`)
	assert.Equal(t, 404, code)
	code, _ = covDo(t, app, "PATCH", "/api/v1/projects/"+pid+"/iterations/"+foreignID, `{"name":"x"}`)
	assert.Equal(t, 404, code)
	code, _ = covDo(t, app, "PATCH", "/api/v1/projects/"+pid+"/iterations/"+itID, `{bad-json`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "PATCH", "/api/v1/projects/"+pid+"/iterations/"+itID, `{}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "PATCH", "/api/v1/projects/"+pid+"/iterations/"+itID, `{"name":"  "}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "PATCH", "/api/v1/projects/"+pid+"/iterations/"+itID, `{"name":"`+strings.Repeat("n", 81)+`"}`)
	assert.Equal(t, 400, code)
	var otherName string
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT name FROM iterations WHERE id=$1`, otherID).Scan(&otherName))
	code, _ = covDo(t, app, "PATCH", "/api/v1/projects/"+pid+"/iterations/"+itID, `{"name":"`+otherName+`"}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "PATCH", "/api/v1/projects/"+pid+"/iterations/"+itID, `{"status":"bogus"}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "PATCH", "/api/v1/projects/"+pid+"/iterations/"+itID, `{"start_date":"not-a-date"}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "PATCH", "/api/v1/projects/"+pid+"/iterations/"+itID, `{"start_date":"2026-09-10","end_date":"2026-09-01"}`)
	assert.Equal(t, 400, code)

	// 正常：改日期/清空日期/改名/关迭代
	code, _ = covDo(t, app, "PATCH", "/api/v1/projects/"+pid+"/iterations/"+itID, `{"name":"cov21h-it","start_date":"2026-09-01","end_date":"2026-09-30"}`)
	assert.Equal(t, 200, code)
	code, _ = covDo(t, app, "PATCH", "/api/v1/projects/"+pid+"/iterations/"+itID, `{"start_date":"","end_date":""}`)
	assert.Equal(t, 200, code)
	code, _ = covDo(t, app, "PATCH", "/api/v1/projects/"+pid+"/iterations/"+itID, `{"status":"closed"}`)
	assert.Equal(t, 200, code)

	// 激活：已有分支 → 跳过创建路径
	withBranch := insertIteration(t, ctx, pid, "planned", "auto", "")
	code, _ = covDo(t, app, "PATCH", "/api/v1/projects/"+pid+"/iterations/"+withBranch, `{"status":"active"}`)
	assert.Equal(t, 200, code)
}

func TestCovV21h_UserAdminBranches(t *testing.T) {
	app := covV21hApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()

	// 造一个真实用户（bcrypt），覆盖 update/change-password/disable/enable/delete
	uid := uuid.NewString()
	uname := "cov21h-" + uid[:8]
	hash, err := bcrypt.GenerateFromPassword([]byte("OldPass#2026"), 10)
	require.NoError(t, err)
	_, err = model.Pool.Exec(ctx,
		`INSERT INTO users (id, username, password_hash, role, enabled) VALUES ($1,$2,$3,'Developer',true)`, uid, uname, string(hash))
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, uid) })

	// getUser 成功
	code, out := covDo(t, app, "GET", "/api/v1/admin/users/"+uid, "")
	require.Equal(t, 200, code, "%v", out)

	// updateUser：非法角色 → 400；弱密码 → 400；正常更新（email/role/password）
	code, _ = covDo(t, app, "PATCH", "/api/v1/admin/users/"+uid, `{"role":"Bogus"}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "PATCH", "/api/v1/admin/users/"+uid, `{"password":"weak"}`)
	assert.Equal(t, 400, code)
	code, out = covDo(t, app, "PATCH", "/api/v1/admin/users/"+uid, `{"email":"cov21h@x.io","role":"QA","password":"NewPass#2026"}`)
	require.Equal(t, 200, code, "%v", out)

	// changePassword：字段缺失 / 弱密码 / 旧密码错误 / 成功
	code, _ = covDo(t, app, "POST", "/api/v1/auth/change-password", `{"old_password":"x"}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "POST", "/api/v1/auth/change-password", `{"old_password":"NewPass#2026","new_password":"weak"}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "POST", "/api/v1/auth/change-password", `{"old_password":"WrongOld#1","new_password":"Another#2026"}`)
	assert.Equal(t, 400, code)

	// 切换身份到该用户执行改密成功路径
	app2 := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	app2.Use(func(c *fiber.Ctx) error {
		c.Locals("username", uname)
		c.Locals("role", "Developer")
		c.Locals("uid", uid)
		return c.Next()
	})
	RegisterUserRoutes(app2.Group("/api/v1"))
	code, out = covDo(t, app2, "POST", "/api/v1/auth/change-password", `{"old_password":"NewPass#2026","new_password":"Third#2026x"}`)
	require.Equal(t, 200, code, "%v", out)

	// disable → enable → delete
	code, _ = covDo(t, app, "POST", "/api/v1/admin/users/"+uid+"/disable", "")
	assert.Equal(t, 200, code)
	code, _ = covDo(t, app, "POST", "/api/v1/admin/users/"+uid+"/enable", "")
	assert.Equal(t, 200, code)
	code, _ = covDo(t, app, "DELETE", "/api/v1/admin/users/"+uid, "")
	assert.Equal(t, 200, code)
	code, _ = covDo(t, app, "POST", "/api/v1/admin/users/"+uuid.NewString()+"/disable", "")
	assert.True(t, code >= 400, "code=%d", code)
}

func TestCovV21h_MemberAndProjectBranches(t *testing.T) {
	app := covV21hApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	pid := covV21Project(t, ctx, false)

	// getProject 非法 id
	code, _ := covDo(t, app, "GET", "/api/v1/projects/bad-id", "")
	assert.Equal(t, 400, code)
	// provision 非法 id
	code, _ = covDo(t, app, "POST", "/api/v1/projects/bad-id/provision", "")
	assert.Equal(t, 400, code)

	// 成员：u 不存在 → 404；非法角色 → 400；成功更新；移除
	uid := uuid.NewString()
	_, err := model.Pool.Exec(ctx,
		`INSERT INTO users (id, username, password_hash, role, enabled) VALUES ($1,$2,'x','Developer',true)`,
		uid, "cov21h-m-"+uid[:8])
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, uid) })

	code, _ = covDo(t, app, "PATCH", "/api/v1/projects/"+pid+"/members/"+uid, `{"role":"QA"}`)
	assert.True(t, code >= 400, "code=%d", code)
	code, out := covDo(t, app, "POST", "/api/v1/projects/"+pid+"/members", `{"user_id":"`+uid+`","role":"Developer"}`)
	require.Equal(t, 200, code, "%v", out)
	code, _ = covDo(t, app, "PATCH", "/api/v1/projects/"+pid+"/members/"+uid, `{"role":"Bogus"}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "PATCH", "/api/v1/projects/"+pid+"/members/"+uid, `{"role":"QA"}`)
	assert.Equal(t, 200, code)
	code, _ = covDo(t, app, "DELETE", "/api/v1/projects/"+pid+"/members/"+uid, "")
	assert.Equal(t, 200, code)

	// restore 非法 id
	code, _ = covDo(t, app, "POST", "/api/v1/projects/bad-id/restore", "")
	assert.Equal(t, 400, code)
}

func TestCovV21h_ProjectBuildWithMockGitea(t *testing.T) {
	ensureTestDB(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	headSHA := strings.Repeat("7c", 20)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/repos/", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/branches/") {
			_, _ = w.Write([]byte(`{"commit":{"id":"` + headSHA + `"}}`))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/dispatches") {
			w.WriteHeader(204)
			return
		}
		http.NotFound(w, r)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	t.Setenv("GITEA_URL", ts.URL)
	t.Setenv("GITEA_TOKEN", "tok")
	t.Setenv("GITEA_ORG", "cov-owner")

	adminID := uuid.NewString()
	_ = model.Pool.QueryRow(ctx, `SELECT id FROM users WHERE username='admin'`).Scan(&adminID)
	app := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("username", "cov-admin")
		c.Locals("role", "Admin")
		c.Locals("uid", adminID)
		return c.Next()
	})
	RegisterProjectRoutes(app.Group("/api/v1"))

	pid := covV21Project(t, ctx, true)
	var repo string
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT repo_full_name FROM projects WHERE id=$1`, pid).Scan(&repo))
	t.Cleanup(func() {
		_, _ = model.Pool.Exec(ctx, `DELETE FROM pipeline_jobs WHERE run_id IN (SELECT id FROM pipeline_runs WHERE repo=$1)`, repo)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM pipeline_runs WHERE repo=$1`, repo)
	})

	// 无仓项目 → NO_REPO 400
	noRepo := covV21Project(t, ctx, false)
	code, _ := covDo(t, app, "POST", "/api/v1/projects/"+noRepo+"/builds", `{}`)
	assert.Equal(t, 400, code)

	// 成功构建（BranchCommit + Dispatch + 单 job 预播）
	code, out := covDo(t, app, "POST", "/api/v1/projects/"+pid+"/builds", `{}`)
	require.Equal(t, 200, code, "%v", out)
	assert.Equal(t, headSHA, covData(t, out)["sha"])

	// 在途重复 → 409
	code, _ = covDo(t, app, "POST", "/api/v1/projects/"+pid+"/builds", `{}`)
	assert.Equal(t, 409, code)

	// 制品列表
	code, _ = covDo(t, app, "GET", "/api/v1/projects/"+pid+"/artifacts", "")
	assert.Equal(t, 200, code)
	code, _ = covDo(t, app, "GET", "/api/v1/projects/bad-id/artifacts", "")
	assert.Equal(t, 400, code)
}

// covV21Spec 直接建 spec + 首版本，便于 spec 相关用例编排。
func covV21Spec(t *testing.T, ctx context.Context, pid, layer string) string {
	t.Helper()
	id := uuid.NewString()
	_, err := model.Pool.Exec(ctx,
		`INSERT INTO specs (id, project_id, layer, title, status, current_version) VALUES ($1,$2,$3,$4,'DRAFT','v0.1.0')`,
		id, pid, layer, "cov21h "+layer)
	require.NoError(t, err)
	content, _ := json.Marshal(map[string]any{"title": "cov21h " + layer, "story_id": "S-COV"})
	_, err = model.Pool.Exec(ctx,
		`INSERT INTO spec_versions (id, spec_id, version, content) VALUES ($1,$2,'v0.1.0',$3)`, uuid.NewString(), id, string(content))
	require.NoError(t, err)
	return id
}
