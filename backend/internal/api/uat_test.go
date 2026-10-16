package api

// M3 回归：UAT 提交（版本上下文注入 / Viewer 成员可提交 / 非成员 403 / 附件 multipart / 幂等）。

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"

	"forja/backend/internal/api/respond"
	"forja/backend/internal/model"
	"forja/backend/internal/storage"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func uatApp(t *testing.T, role, uid string) *fiber.App {
	t.Helper()
	app := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("username", "uat-"+role)
		c.Locals("role", role)
		c.Locals("uid", uid)
		return c.Next()
	})
	api := app.Group("/api/v1")
	RegisterUATRoutes(api)
	return app
}

func TestSubmitProjectUAT(t *testing.T) {
	ensureTestDB(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	pid := uuid.NewString()
	repo := "forja-admin/uat-proj-" + uuid.NewString()[:8]
	_, err := model.Pool.Exec(ctx,
		`INSERT INTO projects (id,name,slug,repo_full_name,default_branch) VALUES ($1,$2,$2,$3,'master')`,
		pid, "uat-"+pid[:8], repo)
	require.NoError(t, err)
	sha := strings.Repeat("ab", 20)
	did := uuid.NewString()
	_, err = model.Pool.Exec(ctx,
		`INSERT INTO project_deliveries (id, project_id, tag, sha, status) VALUES ($1,$2,$3,$4,'ready')`,
		did, pid, "master-"+sha[:8], sha)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = model.Pool.Exec(ctx, `DELETE FROM project_deliveries WHERE project_id=$1`, pid)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM requirements_inbox WHERE project_id=$1`, pid)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, pid)
	})

	adminUID := uuid.NewString()
	_ = model.Pool.QueryRow(ctx, `SELECT id FROM users WHERE username='admin'`).Scan(&adminUID)
	app := uatApp(t, "Admin", adminUID)
	body := `{"result":"fail","content":"验收不通过：导出报错","delivery_id":"` + did + `","env":"客户测试环境"}`
	req := httptest.NewRequest("POST", "/api/v1/projects/"+pid+"/uat", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, 15000)
	require.NoError(t, err)
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	require.Equal(t, 200, resp.StatusCode, "%v", out)
	data := out["data"].(map[string]any)
	id := data["id"].(string)
	assert.Equal(t, "master-"+sha[:8], data["tag"])

	var sourceType, raw, status string
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT source_type, raw_content, status FROM requirements_inbox WHERE id=$1`, id).
		Scan(&sourceType, &raw, &status))
	assert.Equal(t, "UAT", sourceType)
	assert.Equal(t, "NEW", status)
	assert.Contains(t, raw, "【UAT｜不通过｜master-"+sha[:8]+"｜delivery:"+did[:8]+"】")
	assert.Contains(t, raw, "客户测试环境")
	assert.Contains(t, raw, "验收不通过")

	// 幂等：同内容重复提交 → dedup
	req2 := httptest.NewRequest("POST", "/api/v1/projects/"+pid+"/uat", strings.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	resp, err = app.Test(req2, 15000)
	require.NoError(t, err)
	var out2 map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out2)
	require.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, true, out2["data"].(map[string]any)["dedup"])

	// 缺 content → 400
	req3 := httptest.NewRequest("POST", "/api/v1/projects/"+pid+"/uat", strings.NewReader(`{"result":"pass"}`))
	req3.Header.Set("Content-Type", "application/json")
	resp, err = app.Test(req3, 15000)
	require.NoError(t, err)
	assert.Equal(t, 400, resp.StatusCode)

	// 非法 result → 400
	req4 := httptest.NewRequest("POST", "/api/v1/projects/"+pid+"/uat", strings.NewReader(`{"result":"maybe","content":"x"}`))
	req4.Header.Set("Content-Type", "application/json")
	resp, err = app.Test(req4, 15000)
	require.NoError(t, err)
	assert.Equal(t, 400, resp.StatusCode)

	// 跨项目 delivery → 404
	pid2 := uuid.NewString()
	_, err = model.Pool.Exec(ctx,
		`INSERT INTO projects (id,name,slug,repo_full_name,default_branch) VALUES ($1,$2,$2,$3,'master')`,
		pid2, "uat2-"+pid2[:8], "forja-admin/uat2-"+pid2[:8])
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, pid2) })
	bodyCross := `{"result":"pass","content":"x","delivery_id":"` + did + `"}`
	req5 := httptest.NewRequest("POST", "/api/v1/projects/"+pid2+"/uat", strings.NewReader(bodyCross))
	req5.Header.Set("Content-Type", "application/json")
	resp, err = app.Test(req5, 15000)
	require.NoError(t, err)
	assert.Equal(t, 404, resp.StatusCode)

	// 非成员 Viewer → 403
	nonMember := uatApp(t, "Viewer", uuid.NewString())
	resp, err = nonMember.Test(httptest.NewRequest("POST", "/api/v1/projects/"+pid+"/uat", strings.NewReader(`{"result":"pass","content":"x"}`)), 15000)
	require.NoError(t, err)
	assert.Equal(t, 403, resp.StatusCode)

	// 项目内 Viewer 成员 → 200（附附件 multipart）
	_, err = model.Pool.Exec(ctx,
		`INSERT INTO project_members (project_id, user_id, username, role) VALUES ($1,$2,$3,'Viewer')`,
		pid, adminUID, "uat-viewer-member")
	require.NoError(t, err)
	viewerApp := uatApp(t, "Viewer", adminUID)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("result", "partial")
	_ = mw.WriteField("content", "验收部分通过，附验收记录")
	fw, _ := mw.CreateFormFile("file", "uat-report.md")
	_, _ = fw.Write([]byte("# 验收记录\n- 项1 OK"))
	_ = mw.Close()
	req6 := httptest.NewRequest("POST", "/api/v1/projects/"+pid+"/uat", &buf)
	req6.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err = viewerApp.Test(req6, 15000)
	require.NoError(t, err)
	var out6 map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out6)
	require.Equal(t, 200, resp.StatusCode, "%v", out6)
	vdata := out6["data"].(map[string]any)
	viewerReqID := vdata["id"].(string)
	attachID, _ := vdata["attachment_id"].(string)
	assert.NotEmpty(t, attachID)

	var fileName, storeKey string
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT filename, storage_key FROM inbox_attachments WHERE id=$1`, attachID).Scan(&fileName, &storeKey))
	assert.Equal(t, "uat-report.md", fileName)
	// 清理附件对象（FileStore）
	if st, serr := storage.Get(); serr == nil {
		_ = st.Delete(ctx, storeKey)
	}
	_ = viewerReqID
}
