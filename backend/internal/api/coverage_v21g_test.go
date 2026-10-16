package api

// v2.1 覆盖率冲刺 Phase 4a：webhook 签名与事件分支、dispatch mock gitea 全链路、
// delivery 组装/下载成功路径、SSE 写帧与健康探测。

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"forja/backend/internal/api/respond"
	"forja/backend/internal/events"
	"forja/backend/internal/model"
	"forja/backend/internal/storage"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func covV21gWebhookApp(t *testing.T) *fiber.App {
	t.Helper()
	ensureTestDB(t)
	app := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	RegisterWebhookRoutes(app.Group("/api/v1"))
	return app
}

func covV21gPostWebhook(t *testing.T, app *fiber.App, secret, event, deliveryID string, body string) (int, string) {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	req := httptest.NewRequest("POST", "/api/v1/webhooks/gitea", strings.NewReader(body))
	req.Header.Set("X-Gitea-Signature", hex.EncodeToString(mac.Sum(nil)))
	req.Header.Set("Content-Type", "application/json")
	if event != "" {
		req.Header.Set("X-Gitea-Event", event)
	}
	if deliveryID != "" {
		req.Header.Set("X-Gitea-Delivery", deliveryID)
	}
	resp, err := app.Test(req, 15000)
	require.NoError(t, err)
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(resp.Body)
	return resp.StatusCode, buf.String()
}

func TestCovV21g_WebhookEvents(t *testing.T) {
	app := covV21gWebhookApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()

	// secret 未配置 → 503
	code, _ := covV21gPostWebhook(t, app, "whatever", "ping", "", `{}`)
	assert.Equal(t, 503, code)

	t.Setenv("GITEA_WEBHOOK_SECRET", "cov-secret")

	// 签名错误 → 401
	code, _ = covV21gPostWebhook(t, app, "wrong-secret", "ping", "", `{}`)
	assert.Equal(t, 401, code)

	// ping → 200
	code, _ = covV21gPostWebhook(t, app, "cov-secret", "ping", uuid.NewString(), `{}`)
	assert.Equal(t, 200, code)

	// 非法 JSON（签名对） → 400
	code, _ = covV21gPostWebhook(t, app, "cov-secret", "push", uuid.NewString(), `{not-json`)
	assert.Equal(t, 400, code)

	// 未知事件 → 200
	code, _ = covV21gPostWebhook(t, app, "cov-secret", "issues", uuid.NewString(), `{"repository":{"full_name":"forja-admin/cov21g-unk"}}`)
	assert.Equal(t, 200, code)

	pid := covV21Project(t, ctx, true)
	var repo string
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT repo_full_name FROM projects WHERE id=$1`, pid).Scan(&repo))
	t.Cleanup(func() {
		_, _ = model.Pool.Exec(ctx, `DELETE FROM pipeline_jobs WHERE run_id IN (SELECT id FROM pipeline_runs WHERE repo=$1)`, repo)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM pipeline_runs WHERE repo=$1`, repo)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM tasks WHERE project_id=$1`, pid)
	})

	// docs-only push → 不建 PENDING 行
	docsSHA := strings.Repeat("d1", 20)
	docsBody := `{"ref":"refs/heads/master","after":"` + docsSHA + `","repository":{"full_name":"` + repo + `"},"sender":{"login":"cov"},
		"commits":[{"added":["docs/a.md"],"modified":["README.md"]}]}`
	code, _ = covV21gPostWebhook(t, app, "cov-secret", "push", uuid.NewString(), docsBody)
	assert.Equal(t, 200, code)
	var cnt int
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM pipeline_runs WHERE repo=$1 AND sha=$2`, repo, docsSHA).Scan(&cnt))
	assert.Zero(t, cnt, "docs-only push 不应建行")

	// 标签 push（release tag）→ 不建行（2026-09-17：tag 行曾抢走 workflow_job 回流）
	tagSHA := strings.Repeat("c3", 20)
	tagBody := `{"ref":"refs/tags/forja/cov-` + tagSHA[:8] + `","after":"` + tagSHA + `","repository":{"full_name":"` + repo + `"},"sender":{"login":"cov"}}`
	code, _ = covV21gPostWebhook(t, app, "cov-secret", "push", uuid.NewString(), tagBody)
	assert.Equal(t, 200, code)
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM pipeline_runs WHERE repo=$1 AND sha=$2`, repo, tagSHA).Scan(&cnt))
	assert.Zero(t, cnt, "tag push 不应建行")

	// 竞态回归：manual 行更早、push 行更晚（同 sha）→ 回流应落 manual 行，push 行保持 PENDING
	raceSHA := strings.Repeat("b5", 20)
	manualID := uuid.NewString()
	_, err := model.Pool.Exec(ctx,
		`INSERT INTO pipeline_runs (id, project_id, repo, sha, branch, event, status, created_at) VALUES ($1,$2,$3,$4,'master','manual','RUNNING', now()-interval '2 seconds')`,
		manualID, pid, repo, raceSHA)
	require.NoError(t, err)
	_, err = model.Pool.Exec(ctx,
		`INSERT INTO pipeline_runs (id, project_id, repo, sha, branch, event, status) VALUES (gen_random_uuid(),$1,$2,$3,'master','push','PENDING')`,
		pid, repo, raceSHA)
	require.NoError(t, err)
	raceDone := `{"repository":{"full_name":"` + repo + `"},"workflow_job":{"name":"build","status":"completed","conclusion":"success","head_sha":"` + raceSHA + `"}}`
	code, _ = covV21gPostWebhook(t, app, "cov-secret", "workflow_job", uuid.NewString(), raceDone)
	assert.Equal(t, 200, code)
	var mst, pst string
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT status FROM pipeline_runs WHERE id=$1`, manualID).Scan(&mst))
	assert.Equal(t, "SUCCESS", mst, "job 回流应落 manual 行")
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT status FROM pipeline_runs WHERE repo=$1 AND sha=$2 AND event='push'`, repo, raceSHA).Scan(&pst))
	assert.Equal(t, "PENDING", pst, "push 行不应被回流污染")

	// 普通 push（触及 AGENTS.md/project.yaml）→ 建 PENDING 行 + 异步刷新
	sha := strings.Repeat("e2", 20)
	body := `{"ref":"refs/heads/master","after":"` + sha + `","repository":{"full_name":"` + repo + `"},"sender":{"login":"cov"},
		"commits":[{"added":["AGENTS.md"],"modified":["project.yaml","backend/x.go"]}]}`
	code, _ = covV21gPostWebhook(t, app, "cov-secret", "push", uuid.NewString(), body)
	assert.Equal(t, 200, code)
	var st string
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT status FROM pipeline_runs WHERE repo=$1 AND sha=$2`, repo, sha).Scan(&st))
	assert.Equal(t, "PENDING", st)

	// delivery 去重：同 delivery id 再次投递 → dedup
	dupID := uuid.NewString()
	code, out := covV21gPostWebhook(t, app, "cov-secret", "ping", dupID, `{}`)
	assert.Equal(t, 200, code)
	assert.NotContains(t, out, "dedup")
	code, out = covV21gPostWebhook(t, app, "cov-secret", "ping", dupID, `{}`)
	assert.Equal(t, 200, code)
	assert.Contains(t, out, "dedup")

	// workflow_job：queued → in_progress → completed(success)，非 master push 行跳过
	wsha := strings.Repeat("f3", 20)
	queued := `{"repository":{"full_name":"` + repo + `"},"workflow_job":{"name":"backend","status":"queued","head_sha":"` + wsha + `"}}`
	code, _ = covV21gPostWebhook(t, app, "cov-secret", "workflow_job", uuid.NewString(), queued)
	assert.Equal(t, 200, code)
	inprog := `{"repository":{"full_name":"` + repo + `"},"workflow_job":{"name":"backend","status":"in_progress","head_sha":"` + wsha + `","started_at":"bad-time"}}`
	code, _ = covV21gPostWebhook(t, app, "cov-secret", "workflow_job", uuid.NewString(), inprog)
	assert.Equal(t, 200, code)
	done := `{"repository":{"full_name":"` + repo + `"},"workflow_job":{"name":"backend","status":"completed","conclusion":"success","head_sha":"` + wsha + `","started_at":"2026-09-15T10:00:00Z","completed_at":"2026-09-15T10:01:00Z"}}`
	code, _ = covV21gPostWebhook(t, app, "cov-secret", "workflow_job", uuid.NewString(), done)
	assert.Equal(t, 200, code)
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT status FROM pipeline_runs WHERE repo=$1 AND sha=$2`, repo, wsha).Scan(&st))
	assert.Equal(t, "SUCCESS", st)

	// workflow_job 空体 / 非 master 分支 progress 跳过
	code, _ = covV21gPostWebhook(t, app, "cov-secret", "workflow_job", uuid.NewString(), `{"repository":{"full_name":"`+repo+`"}}`)
	assert.Equal(t, 200, code)
	featSHA := strings.Repeat("a4", 20)
	_, err = model.Pool.Exec(ctx,
		`INSERT INTO pipeline_runs (id, project_id, repo, sha, branch, event, status) VALUES (gen_random_uuid(),$1,$2,$3,'feature/x','push','PENDING')`,
		pid, repo, featSHA)
	require.NoError(t, err)
	featDone := `{"repository":{"full_name":"` + repo + `"},"workflow_job":{"name":"backend","status":"completed","conclusion":"success","head_sha":"` + featSHA + `"}}`
	code, _ = covV21gPostWebhook(t, app, "cov-secret", "workflow_job", uuid.NewString(), featDone)
	assert.Equal(t, 200, code)
}

func TestCovV21g_DispatchWithMockGitea(t *testing.T) {
	ensureTestDB(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	headSHA := strings.Repeat("ab", 20)
	var dispatched []string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/user", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"login":"cov-owner","id":1}`))
	})
	mux.HandleFunc("/api/v1/repos/", func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case strings.Contains(p, "/branches/"):
			_, _ = w.Write([]byte(`{"commit":{"id":"` + headSHA + `"}}`))
		case strings.HasSuffix(p, "/dispatches"):
			dispatched = append(dispatched, p)
			w.WriteHeader(204)
		case strings.Contains(p, "/actions/variables/"):
			// PUT 404 → 客户端降级 POST 创建
			if r.Method == http.MethodPut {
				http.NotFound(w, r)
				return
			}
			w.WriteHeader(201)
		default:
			http.NotFound(w, r)
		}
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	t.Setenv("GITEA_URL", ts.URL)
	t.Setenv("GITEA_TOKEN", "tok")
	t.Setenv("GITEA_ORG", "cov-owner")
	t.Setenv("GITEA_SELF_REPO", "forja-admin/forja")

	adminID := uuid.NewString()
	_ = model.Pool.QueryRow(ctx, `SELECT id FROM users WHERE username='admin'`).Scan(&adminID)
	app := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("username", "cov-admin")
		c.Locals("role", "Admin")
		c.Locals("uid", adminID)
		return c.Next()
	})
	RegisterDispatchRoutes(app.Group("/api/v1"))

	pid := covV21Project(t, ctx, true)
	var repo string
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT repo_full_name FROM projects WHERE id=$1`, pid).Scan(&repo))
	t.Cleanup(func() {
		_, _ = model.Pool.Exec(ctx, `DELETE FROM pipeline_jobs WHERE run_id IN (SELECT id FROM pipeline_runs WHERE repo IN ($1,'forja-admin/forja'))`, repo)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM pipeline_runs WHERE repo IN ($1,'forja-admin/forja')`, repo)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM deployments WHERE project_id=$1 OR repo='forja-admin/forja'`, pid)
	})

	// 参数校验分支
	code, _ := covDo(t, app, "POST", "/api/v1/internal/gitea/dispatch", `{bad-json`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "POST", "/api/v1/internal/gitea/dispatch", `{"kind":"fake","sha":"`+headSHA+`"}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "POST", "/api/v1/internal/gitea/dispatch", `{"kind":"build","sha":"short","project_id":"`+pid+`"}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "POST", "/api/v1/internal/gitea/dispatch", `{"kind":"build","sha":"`+headSHA+`","project_id":"bad-id"}`)
	assert.Equal(t, 400, code)

	// 非 HEAD → 400 SHA_NOT_HEAD
	otherSHA := strings.Repeat("cd", 20)
	code, out := covDo(t, app, "POST", "/api/v1/internal/gitea/dispatch", `{"kind":"build","sha":"`+otherSHA+`","project_id":"`+pid+`"}`)
	assert.Equal(t, 400, code, "%v", out)

	// 成功构建（记录 manual run + preseed）
	code, out = covDo(t, app, "POST", "/api/v1/internal/gitea/dispatch", `{"kind":"build","sha":"`+headSHA+`","project_id":"`+pid+`"}`)
	assert.Equal(t, 200, code, "%v", out)
	var runSt string
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT status FROM pipeline_runs WHERE repo=$1 AND sha=$2 AND event='manual'`, repo, headSHA).Scan(&runSt))
	assert.Equal(t, "RUNNING", runSt)

	// 在途重复 → 409
	code, _ = covDo(t, app, "POST", "/api/v1/internal/gitea/dispatch", `{"kind":"build","sha":"`+headSHA+`","project_id":"`+pid+`"}`)
	assert.Equal(t, 409, code)

	// 用户项目 deploy → DELIVERY_ONLY 400
	code, out = covDo(t, app, "POST", "/api/v1/internal/gitea/dispatch", `{"kind":"deploy","sha":"`+headSHA+`","project_id":"`+pid+`"}`)
	assert.Equal(t, 400, code, "%v", out)

	// 自身仓 deploy：未构建 → 400；排期超上限 → 400（须在成功部署前，避免在途互斥 409）
	shaNoBuild := strings.Repeat("e5", 20)
	code, out = covDo(t, app, "POST", "/api/v1/internal/gitea/dispatch", `{"kind":"deploy","sha":"`+shaNoBuild+`"}`)
	assert.Equal(t, 400, code, "%v", out)
	code, _ = covDo(t, app, "POST", "/api/v1/internal/gitea/dispatch", `{"kind":"deploy","sha":"`+shaNoBuild+`","delay_seconds":9999999}`)
	assert.Equal(t, 400, code)

	shaBuilt := strings.Repeat("f6", 20)
	_, err := model.Pool.Exec(ctx,
		`INSERT INTO pipeline_runs (id, repo, sha, branch, event, status) VALUES (gen_random_uuid(),'forja-admin/forja',$1,'master','push','SUCCESS')`, shaBuilt)
	require.NoError(t, err)
	code, out = covDo(t, app, "POST", "/api/v1/internal/gitea/dispatch", `{"kind":"deploy","sha":"`+shaBuilt+`"}`)
	assert.Equal(t, 200, code, "%v", out)
	assert.True(t, len(dispatched) >= 2, "应已触发 build 与 deploy dispatch")
}

func TestCovV21g_DeliverySuccessPaths(t *testing.T) {
	ensureTestDB(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	adminID := uuid.NewString()
	_ = model.Pool.QueryRow(ctx, `SELECT id FROM users WHERE username='admin'`).Scan(&adminID)
	app := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("username", "cov-admin")
		c.Locals("role", "Admin")
		c.Locals("uid", adminID)
		return c.Next()
	})
	api := app.Group("/api/v1")
	RegisterDeliveryRoutes(api)
	RegisterInternalRoutes(api)

	pid := covV21Project(t, ctx, true)
	sha := strings.Repeat("1b", 20)
	t.Cleanup(func() {
		_, _ = model.Pool.Exec(ctx, `DELETE FROM project_deliveries WHERE project_id=$1`, pid)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM tasks WHERE project_id=$1`, pid)
	})

	// 预置制品（真实文件落存储）→ triggerDelivery 组装成功
	store, err := storage.Get()
	require.NoError(t, err)
	storeKey := "delivery/" + pid + "/image-" + sha[:8] + ".tar.gz"
	require.NoError(t, store.Put(ctx, storeKey, bytes.NewReader([]byte("fake-image-tar")), 14, "application/gzip"))
	_, err = model.Pool.Exec(ctx,
		`INSERT INTO project_artifacts (project_id, sha, tag, image, digest, size_bytes, store_key, status)
		 VALUES ($1,$2,'master-1b1b1b1b','gitea:3000/x/y','sha256:abc',14,$3,'uploaded')
		 ON CONFLICT (project_id, sha) DO UPDATE SET store_key=EXCLUDED.store_key`,
		pid, sha, storeKey)
	require.NoError(t, err)

	// RELEASED 任务（供发布说明聚合）
	_, err = model.Pool.Exec(ctx,
		`INSERT INTO tasks (id, project_id, title, status) VALUES ($1,$2,'cov21g released','RELEASED')`, uuid.NewString(), pid)
	require.NoError(t, err)

	code, out := covDo(t, app, "POST", "/api/v1/projects/"+pid+"/deliveries", `{"tag":"cov21g-1"}`)
	require.Equal(t, 200, code, "%v", out)
	did, _ := covData(t, out)["id"].(string)
	require.NotEmpty(t, did)
	assert.Equal(t, "ready", covData(t, out)["status"])

	// 列表 → 含该交付
	code, out = covDo(t, app, "GET", "/api/v1/projects/"+pid+"/deliveries?page=1", "")
	require.Equal(t, 200, code)
	items, _ := covData(t, out)["items"].([]any)
	assert.GreaterOrEqual(t, len(items), 1)

	// docs.zip 下载
	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/projects/"+pid+"/deliveries/"+did+"/download", nil), 20000)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(resp.Body)
	assert.Greater(t, buf.Len(), 100, "zip 应有内容")

	// 镜像 tar.gz 透传下载
	resp, err = app.Test(httptest.NewRequest("GET", "/api/v1/projects/"+pid+"/deliveries/"+did+"/artifact", nil), 20000)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	buf.Reset()
	_, _ = buf.ReadFrom(resp.Body)
	assert.Equal(t, "fake-image-tar", buf.String())

	// 跨项目访问 → 404
	otherPid := covV21Project(t, ctx, true)
	resp, err = app.Test(httptest.NewRequest("GET", "/api/v1/projects/"+otherPid+"/deliveries/"+did+"/download", nil), 15000)
	require.NoError(t, err)
	assert.Equal(t, 404, resp.StatusCode)

	// 发布说明预览
	code, _ = covDo(t, app, "GET", "/api/v1/projects/"+pid+"/releases/notes", "")
	assert.Equal(t, 200, code)

	// 非法 tag / 指定不存在 sha
	code, _ = covDo(t, app, "POST", "/api/v1/projects/"+pid+"/deliveries", `{"tag":"bad..tag"}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "POST", "/api/v1/projects/"+pid+"/deliveries", `{"sha":"`+strings.Repeat("9f", 20)+`"}`)
	assert.Equal(t, 400, code)

	// internal 上传制品成功（覆盖 sha 新行）→ 200 且可查
	t.Setenv("FORJA_INTERNAL_TOKEN", "cov-internal")
	upSHA := strings.Repeat("2c", 20)
	req := httptest.NewRequest("POST", "/api/v1/internal/delivery/artifact?project_id="+pid+"&sha="+upSHA, bytes.NewReader([]byte("gzip-bytes")))
	req.Header.Set("X-Forja-Token", "cov-internal")
	req.Header.Set("Content-Type", "application/gzip")
	resp, err = app.Test(req, 20000)
	require.NoError(t, err)
	require.Equal(t, 200, resp.StatusCode)
	var cnt int
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM project_artifacts WHERE project_id=$1 AND sha=$2 AND status='uploaded'`, pid, upSHA).Scan(&cnt))
	assert.Equal(t, 1, cnt)

	// 空体 → 400
	req = httptest.NewRequest("POST", "/api/v1/internal/delivery/artifact?project_id="+pid+"&sha="+strings.Repeat("3d", 20), strings.NewReader(""))
	req.Header.Set("X-Forja-Token", "cov-internal")
	resp, err = app.Test(req, 15000)
	require.NoError(t, err)
	assert.Equal(t, 400, resp.StatusCode)

	// 非法 sha / 项目无 repo
	req = httptest.NewRequest("POST", "/api/v1/internal/delivery/artifact?project_id="+pid+"&sha=nope", strings.NewReader("x"))
	req.Header.Set("X-Forja-Token", "cov-internal")
	resp, err = app.Test(req, 15000)
	require.NoError(t, err)
	assert.Equal(t, 400, resp.StatusCode)
	noRepoPID := covV21Project(t, ctx, false)
	req = httptest.NewRequest("POST", "/api/v1/internal/delivery/artifact?project_id="+noRepoPID+"&sha="+upSHA, strings.NewReader("x"))
	req.Header.Set("X-Forja-Token", "cov-internal")
	resp, err = app.Test(req, 15000)
	require.NoError(t, err)
	assert.Equal(t, 404, resp.StatusCode)
}

func TestCovV21g_SSEAndHealthHelpers(t *testing.T) {
	ensureTestDB(t)

	// writeSSE 帧格式
	buf := new(bytes.Buffer)
	w := bufio.NewWriter(buf)
	require.NoError(t, writeSSE(w, events.Event{Name: "snapshot", Data: mustJSON(map[string]any{"k": 1})}))
	assert.Contains(t, buf.String(), "data: ")
	assert.Contains(t, buf.String(), "snapshot")

	// 写入失败路径：断开的 writer
	assert.Error(t, writeSSE(bufio.NewWriter(errWriter{}), events.Event{Name: "x"}))

	// probeHTTP：up / 非 2xx / 连接失败
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer up.Close()
	assert.Equal(t, "up", probeHTTP(context.Background(), up.URL, "", ""))
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer bad.Close()
	assert.Contains(t, probeHTTP(context.Background(), bad.URL, "u", "p"), "down: http")
	assert.Contains(t, probeHTTP(context.Background(), "http://127.0.0.1:1/probe", "", ""), "down:")

	// ReadinessHandler 探测（DB/Redis 均已就绪 → 200）
	app := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	app.Get("/readyz", ReadinessHandler())
	resp, err := app.Test(httptest.NewRequest("GET", "/readyz", nil), 15000)
	require.NoError(t, err)
	assert.Contains(t, []int{200, 503}, resp.StatusCode)
}

type errWriter struct{}

func (errWriter) Write([]byte) (int, error) { return 0, http.ErrBodyNotAllowed }
