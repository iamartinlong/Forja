package api

// M1.3 交付包测试：镜像 tar 回传（服务令牌）/ docs.zip 组装幂等下载 / Release Notes 预览。

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
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

func deliveryApp(t *testing.T) *fiber.App {
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
	RegisterDeliveryRoutes(api)
	return app
}

func testStorage(t *testing.T) *storage.LocalDriver {
	t.Helper()
	st, err := storage.NewLocal(t.TempDir())
	require.NoError(t, err)
	storage.SetForTest(st)
	t.Cleanup(storage.Reset)
	return st
}

func TestUploadDeliveryArtifact(t *testing.T) {
	ensureTestDB(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	t.Setenv("FORJA_INTERNAL_TOKEN", "test-internal-token")
	t.Setenv("GITEA_URL", "")
	t.Setenv("GITEA_TOKEN", "")
	st := testStorage(t)

	repo := "forja-admin/upload-proj-" + uuid.NewString()[:8]
	pid := uuid.NewString()
	ctx := context.Background()
	_, err := model.Pool.Exec(ctx,
		`INSERT INTO projects (id,name,slug,repo_full_name,default_branch) VALUES ($1,$2,$2,$3,'main')`,
		pid, "up-"+pid[:8], repo)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = model.Pool.Exec(ctx, `DELETE FROM project_artifacts WHERE project_id=$1`, pid)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, pid)
	})

	sha := strings.Repeat("ab", 20)
	body := strings.Repeat("PKGZ", 256)
	app := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	api := app.Group("/api/v1")
	RegisterInternalRoutes(api)

	// 无令牌 → 401
	req := httptest.NewRequest("POST",
		"/api/v1/internal/delivery/artifact?project_id="+pid+"&sha="+sha, strings.NewReader(body))
	resp, err := app.Test(req, 15000)
	require.NoError(t, err)
	assert.Equal(t, 401, resp.StatusCode)

	// 带令牌 → 200 并落盘/落库
	req = httptest.NewRequest("POST",
		"/api/v1/internal/delivery/artifact?project_id="+pid+"&sha="+sha, strings.NewReader(body))
	req.Header.Set("X-Forja-Token", "test-internal-token")
	resp, err = app.Test(req, 15000)
	require.NoError(t, err)
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	require.Equal(t, 200, resp.StatusCode, "%v", out)
	data := out["data"].(map[string]any)
	assert.EqualValues(t, len(body), data["size_bytes"])
	assert.Equal(t, "main-"+sha[:8], data["tag"])

	var status, key, digest string
	var size int64
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT status, COALESCE(store_key,''), COALESCE(digest,''), COALESCE(size_bytes,0)
		   FROM project_artifacts WHERE project_id=$1 AND sha=$2`, pid, sha).Scan(&status, &key, &digest, &size))
	assert.Equal(t, "uploaded", status)
	assert.Equal(t, "delivery/"+pid+"/image-"+sha[:8]+".tar.gz", key)
	assert.Contains(t, digest, "sha256:")
	assert.EqualValues(t, len(body), size)

	rc, err := st.Open(ctx, key)
	require.NoError(t, err)
	got, _ := io.ReadAll(rc)
	_ = rc.Close()
	assert.Equal(t, body, string(got))

	// 幂等重传：仍单行
	req = httptest.NewRequest("POST",
		"/api/v1/internal/delivery/artifact?project_id="+pid+"&sha="+sha, strings.NewReader(body))
	req.Header.Set("X-Forja-Token", "test-internal-token")
	resp, err = app.Test(req, 15000)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	var cnt int
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM project_artifacts WHERE project_id=$1`, pid).Scan(&cnt))
	assert.Equal(t, 1, cnt)
}

func TestDeliveryAssembleAndDownload(t *testing.T) {
	ensureTestDB(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	t.Setenv("GITEA_URL", "")
	t.Setenv("GITEA_TOKEN", "")
	st := testStorage(t)
	app := deliveryApp(t)

	repo := "forja-admin/deliv-proj-" + uuid.NewString()[:8]
	pid := uuid.NewString()
	ctx := context.Background()
	_, err := model.Pool.Exec(ctx,
		`INSERT INTO projects (id,name,slug,repo_full_name,default_branch) VALUES ($1,$2,$2,$3,'master')`,
		pid, "dv-"+pid[:8], repo)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = model.Pool.Exec(ctx, `DELETE FROM project_deliveries WHERE project_id=$1`, pid)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM project_artifacts WHERE project_id=$1`, pid)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, pid)
	})

	sha := strings.Repeat("cd", 20)
	tarKey := "delivery/" + pid + "/image-" + sha[:8] + ".tar.gz"
	require.NoError(t, st.Put(ctx, tarKey, strings.NewReader("FAKETAR"), 7, "application/gzip"))
	_, err = model.Pool.Exec(ctx, `
		INSERT INTO project_artifacts (project_id, sha, tag, image, status, store_key, size_bytes)
		VALUES ($1,$2,$3,$4,'uploaded',$5,7)`,
		pid, sha, "master-"+sha[:8], "gitea:3000/"+repo, tarKey)
	require.NoError(t, err)

	// 组装
	req := httptest.NewRequest("POST", "/api/v1/projects/"+pid+"/deliveries", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, 15000)
	require.NoError(t, err)
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	require.Equal(t, 200, resp.StatusCode, "%v", out)
	data := out["data"].(map[string]any)
	did := data["id"].(string)
	assert.Equal(t, "master-"+sha[:8], data["tag"])
	assert.Equal(t, "ready", data["status"])

	var status, zipKey string
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT status, COALESCE(store_key,'') FROM project_deliveries WHERE project_id=$1 AND tag=$2`,
		pid, "master-"+sha[:8]).Scan(&status, &zipKey))
	assert.Equal(t, "ready", status)

	// zip 内容：5 个文件齐备
	rc, err := st.Open(ctx, zipKey)
	require.NoError(t, err)
	zb, _ := io.ReadAll(rc)
	_ = rc.Close()
	zr, err := zip.NewReader(bytes.NewReader(zb), int64(len(zb)))
	require.NoError(t, err)
	names := map[string]bool{}
	contents := map[string]string{}
	for _, f := range zr.File {
		names[f.Name] = true
		rcf, rerr := f.Open()
		if rerr == nil {
			b, _ := io.ReadAll(rcf)
			_ = rcf.Close()
			contents[f.Name] = string(b)
		}
	}
	for _, want := range []string{"delivery.json", "deploy/compose.yml", "README-DEPLOY.md", "CHANGELOG.md", "project.yaml"} {
		assert.True(t, names[want], "missing %s", want)
	}
	// M5 彩排修复回归：交付包 compose 的 image 行已指向实际交付 tag（非模板占位）
	assert.Contains(t, contents["deploy/compose.yml"], "image: gitea:3000/"+repo+":master-"+sha[:8])
	assert.NotContains(t, contents["deploy/compose.yml"], ":latest")

	// 列表
	resp, err = app.Test(httptest.NewRequest("GET", "/api/v1/projects/"+pid+"/deliveries", nil), 15000)
	require.NoError(t, err)
	var listOut map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&listOut)
	require.Equal(t, 200, resp.StatusCode)
	listData := listOut["data"].(map[string]any)
	items := listData["items"].([]any)
	require.Len(t, items, 1)
	assert.Equal(t, did, items[0].(map[string]any)["id"])

	// 下载 docs.zip
	resp, err = app.Test(httptest.NewRequest("GET", "/api/v1/projects/"+pid+"/deliveries/"+did+"/download", nil), 15000)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "application/zip")
	dl, _ := io.ReadAll(resp.Body)
	assert.Equal(t, zb, dl)

	// 下载镜像 tar
	resp, err = app.Test(httptest.NewRequest("GET", "/api/v1/projects/"+pid+"/deliveries/"+did+"/artifact", nil), 15000)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	dl, _ = io.ReadAll(resp.Body)
	assert.Equal(t, "FAKETAR", string(dl))

	// Notes 预览
	resp, err = app.Test(httptest.NewRequest("GET", "/api/v1/projects/"+pid+"/releases/notes", nil), 15000)
	require.NoError(t, err)
	var notesOut map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&notesOut)
	require.Equal(t, 200, resp.StatusCode, "%v", notesOut)
	notes := notesOut["data"].(map[string]any)["notes"].(string)
	assert.Contains(t, notes, "Release Notes")
	assert.Contains(t, notes, "dv-"+pid[:8])
}
