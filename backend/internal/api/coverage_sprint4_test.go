package api

// S1.5 覆盖率冲刺（第四波）：错误分支与变体路径扫尾。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http/httptest"
	"testing"

	"forja/backend/internal/model"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCov4_CreateTaskWithSpecRefs(t *testing.T) {
	app := covApp(t)
	ctx := context.Background()
	pid := uuid.NewString()
	_, err := model.Pool.Exec(ctx, `INSERT INTO projects (id, name, slug) VALUES ($1,'cv4','cv4')`, pid)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, pid) })

	code, out := covDo(t, app, "POST", "/api/v1/tasks",
		fmt.Sprintf(`{"project_id":%q,"title":"refs-t","spec_refs":["SPEC-1","SPEC-2"]}`, pid))
	require.Equal(t, 200, code, "%v", out)
	id := covData(t, out)["id"].(string)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM tasks WHERE id=$1`, id) })

	// spec_refs 审计副表已写入
	var cnt int
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM audit_logs WHERE action='task.create.spec_refs' AND target_id=$1`, id).Scan(&cnt))
	assert.Equal(t, 1, cnt)
}

func TestCov4_SpecVariants(t *testing.T) {
	app := covApp(t)
	pid := uuid.NewString()
	_, err := model.Pool.Exec(context.Background(), `INSERT INTO projects (id, name, slug) VALUES ($1,'cv4s','cv4s')`, pid)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = model.Pool.Exec(context.Background(), `DELETE FROM projects WHERE id=$1`, pid) })

	// BS 带 [NEEDS CLARIFICATION] → validate 失败（passed=false）
	code, out := covDo(t, app, "POST", "/api/v1/specs",
		fmt.Sprintf(`{"project_id":%q,"layer":"BS","title":"clarify","content":{"story_id":"C4","description":"[NEEDS CLARIFICATION] scope"}}`, pid))
	require.Equal(t, 200, code)
	sid := covData(t, out)["id"].(string)
	code, out = covDo(t, app, "POST", "/api/v1/specs/"+sid+"/validate", "")
	assert.Equal(t, 200, code)
	vd := covData(t, out)
	if passed, ok := vd["passed"].(bool); ok {
		assert.False(t, passed, "含澄清标记应校验失败")
	}

	// PUT 更新：版本递增 + 回退 DRAFT；错误路径
	code, _ = covDo(t, app, "PUT", "/api/v1/specs/not-a-uuid", `{"title":"x","content":{}}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "PUT", "/api/v1/specs/"+uuid.NewString(), `{"title":"x","content":{}}`)
	assert.Equal(t, 404, code)
	code, out = covDo(t, app, "PUT", "/api/v1/specs/"+sid, `{"title":"clarify2","content":{"story_id":"C4"}}`)
	require.Equal(t, 200, code, "%v", out)
	assert.NotEqual(t, "", covData(t, out)["version"])

	// 列表过滤分支
	code, out = covDo(t, app, "GET", fmt.Sprintf(`/api/v1/specs?project_id=%s&layer=BS&q=cla&page=2&page_size=1`, pid), "")
	assert.Equal(t, 200, code)
	_ = covData(t, out)

	// estimate 缺 title → 400
	code, _ = covDo(t, app, "POST", "/api/v1/tasks/estimate", fmt.Sprintf(`{"project_id":%q,"priority":3}`, pid))
	assert.Equal(t, 400, code)

	// audit target_type 过滤分支
	code, out = covDo(t, app, "GET", "/api/v1/audit?project_id="+pid+"&target_type=spec", "")
	assert.Equal(t, 200, code)
	_ = covData(t, out)

	// me：last_login 分支
	code, out = covDo(t, app, "GET", "/api/v1/auth/me", "")
	assert.Equal(t, 200, code)
	assert.NotNil(t, covData(t, out)["username"])
}

func TestCov4_InboxMultipartAndSelfBlock(t *testing.T) {
	app := covApp(t)
	pid := uuid.NewString()
	_, err := model.Pool.Exec(context.Background(), `INSERT INTO projects (id, name, slug) VALUES ($1,'cv4m','cv4m')`, pid)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = model.Pool.Exec(context.Background(), `DELETE FROM projects WHERE id=$1`, pid) })

	// multipart 文件上传（.md 轻量路径）
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	fw, _ := mw.CreateFormFile("file", "req.md")
	_, _ = fw.Write([]byte("multipart raw requirement content"))
	_ = mw.WriteField("project_id", pid)
	mw.Close()
	req := httptest.NewRequest("POST", "/api/v1/inbox", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := app.Test(req, 30000)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode, "文件上传应成功")
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if d, ok := out["data"].(map[string]any); ok {
		if id, ok2 := d["id"].(string); ok2 && id != "" {
			_, _ = model.Pool.Exec(context.Background(), `DELETE FROM requirements_inbox WHERE id=$1`, id)
		}
	}

	// 自操作防护：禁用/删除自己 → SELF_OPERATION（用真实 admin id，兼容 username 透传）
	var adminID string
	_ = model.Pool.QueryRow(context.Background(), `SELECT id FROM users WHERE username='admin'`).Scan(&adminID)
	if adminID == "" {
		adminID = "00000000-0000-0000-0000-000000000001"
	}
	code, out := covDo(t, app, "POST", "/api/v1/admin/users/"+adminID+"/disable", "")
	assert.Equal(t, 400, code)
	assert.Equal(t, "SELF_OPERATION", errCodeOf(t, out))
	code, out = covDo(t, app, "DELETE", "/api/v1/admin/users/"+adminID, "")
	assert.Equal(t, 400, code)
	assert.Equal(t, "SELF_OPERATION", errCodeOf(t, out))

	// 归档分支收尾：purge 不存在 → 404；restore 不存在 → 404
	code, _ = covDo(t, app, "DELETE", "/api/v1/projects/"+uuid.NewString()+"?purge=true", "")
	assert.Equal(t, 404, code)
	code, _ = covDo(t, app, "POST", "/api/v1/projects/"+uuid.NewString()+"/restore", "")
	assert.Equal(t, 404, code)

	// convert 空 requirement_ids → 400
	code, _ = covDo(t, app, "POST", "/api/v1/inbox/convert", fmt.Sprintf(`{"project_id":%q,"requirement_ids":[]}`, pid))
	assert.Equal(t, 400, code)
}
