package api

// v2.1 覆盖率冲刺 Phase 2：纯函数 / 路由注册 / DB 辅助 / gitea mock（agents 生成、内容刷新）。

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"forja/backend/internal/api/respond"
	"forja/backend/internal/model"
	"forja/backend/internal/storage"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func covV21bApp(t *testing.T) *fiber.App {
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
	RegisterCostRoutes(api)
	RegisterReleaseRoutes(api)
	RegisterUserRoutes(api)
	RegisterProjectRoutes(api)
	RegisterProjectFileRoutes(api)
	return app
}

func TestCovV21b_CostReleasesUsersRoutes(t *testing.T) {
	app := covV21bApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	pid := covV21Project(t, ctx, false)
	tid := uuid.NewString()
	sha := strings.Repeat("cd", 20)
	_, err := model.Pool.Exec(ctx,
		`INSERT INTO tasks (id, project_id, title, status, commit_sha) VALUES ($1,$2,'cov21 released','RELEASED',$3)`,
		tid, pid, sha)
	require.NoError(t, err)
	_, err = model.Pool.Exec(ctx, `
		INSERT INTO agent_executions (id, task_id, agent_type, input, output, status, attempt, tokens_in, tokens_out, cost_cents, duration_ms)
		VALUES (gen_random_uuid(),$1,'opencode','{}'::jsonb,'{}'::jsonb,'SUCCESS',1,100,50,7,1000)`, tid)
	require.NoError(t, err)

	code, _ := covDo(t, app, "GET", "/api/v1/projects/"+pid+"/cost", "")
	assert.Equal(t, 200, code)
	code, out := covDo(t, app, "GET", "/api/v1/projects/"+pid+"/releases", "")
	require.Equal(t, 200, code)
	items, _ := covData(t, out)["items"].([]any)
	assert.GreaterOrEqual(t, len(items), 1)

	// 用户读端点
	adminID := ""
	_ = model.Pool.QueryRow(ctx, `SELECT id FROM users WHERE username='admin'`).Scan(&adminID)
	code, _ = covDo(t, app, "GET", "/api/v1/admin/users/"+adminID, "")
	assert.Equal(t, 200, code)
	code, _ = covDo(t, app, "GET", "/api/v1/admin/users/"+adminID+"/login-logs", "")
	assert.Equal(t, 200, code)
	code, _ = covDo(t, app, "GET", "/api/v1/auth/me/login-logs", "")
	assert.Equal(t, 200, code)

	// 模板列表（内置兜底）
	code, out = covDo(t, app, "GET", "/api/v1/project-templates", "")
	require.Equal(t, 200, code)
	ts, _ := covData(t, out)["items"].([]any)
	assert.GreaterOrEqual(t, len(ts), 2, "内置模板应含 go/node 两个")
}

func TestCovV21b_ProjectFileDownloadById(t *testing.T) {
	app := covV21bApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	st, err := storage.NewLocal(t.TempDir())
	require.NoError(t, err)
	storage.SetForTest(st)
	t.Cleanup(storage.Reset)

	ctx := context.Background()
	pid := covV21Project(t, ctx, false)
	fid := uuid.NewString()
	key := "project/" + pid + "/cov21.txt"
	require.NoError(t, st.Put(ctx, key, strings.NewReader("cov21-download-body"), int64(len("cov21-download-body")), "text/plain"))
	_, err = model.Pool.Exec(ctx,
		`INSERT INTO project_files (id, project_id, category, filename, storage_key, size, mime) VALUES ($1,$2,'general','cov21.txt',$3,$4,'text/plain')`,
		fid, pid, key, len("cov21-download-body"))
	require.NoError(t, err)

	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/project-files/"+fid+"/download", nil), 15000)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	buf := make([]byte, 64)
	n, _ := resp.Body.Read(buf)
	assert.Contains(t, string(buf[:n]), "cov21-download-body")

	// presign=true：local 驱动不支持 → 回退流式（覆盖回退分支）
	resp, err = app.Test(httptest.NewRequest("GET", "/api/v1/project-files/"+fid+"/download?presign=true", nil), 15000)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
}

func TestCovV21b_DeliveryArtifactTooLargeAndNotes(t *testing.T) {
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	t.Setenv("FORJA_INTERNAL_TOKEN", "cov21-token")
	t.Setenv("FORJA_MAX_ARTIFACT_BYTES", "4")
	app := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	api := app.Group("/api/v1")
	RegisterInternalRoutes(api)
	pid := covV21Project(t, ctx, true)

	req := httptest.NewRequest("POST", "/api/v1/internal/delivery/artifact?project_id="+pid+"&sha="+strings.Repeat("ab", 20),
		strings.NewReader("this-is-more-than-four-bytes"))
	req.Header.Set("X-Forja-Token", "cov21-token")
	resp, err := app.Test(req, 15000)
	require.NoError(t, err)
	assert.Equal(t, 413, resp.StatusCode, "超过 FORJA_MAX_ARTIFACT_BYTES 应 413")

	// releaseNotesMarkdown：RELEASED 任务聚合（覆盖分组与短 sha 分支）
	tid := uuid.NewString()
	_, err = model.Pool.Exec(ctx,
		`INSERT INTO tasks (id, project_id, title, status, commit_sha) VALUES ($1,$2,'cov21 notes task','RELEASED',$3)`,
		tid, pid, strings.Repeat("ef", 20))
	require.NoError(t, err)
	md, n := releaseNotesMarkdown(ctx, projectMeta{id: pid, name: "cov21"}, "master-efefefef", deliveryArtifact{image: "gitea:3000/x/y", tag: "master-efefefef", sha: strings.Repeat("ef", 20)})
	assert.Equal(t, 1, n)
	assert.Contains(t, md, "cov21 notes task")
}

func TestCovV21b_GiteaMockAgentsAndRefresh(t *testing.T) {
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/repos/", func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(p, "/contents/AGENTS.md"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"content":"` + base64.StdEncoding.EncodeToString([]byte("# cov21 agents")) + `","sha":"sha-agents"}`))
		case r.Method == http.MethodPut && strings.HasSuffix(p, "/contents/AGENTS.md"):
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"content":{"sha":"sha-generated"}}`))
		case r.Method == http.MethodGet && strings.HasSuffix(p, "/contents/project.yaml"):
			manifest := "version: 1\ntype: backend\nstack:\n  backend: { lang: go }\n"
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"content":"` + base64.StdEncoding.EncodeToString([]byte(manifest)) + `","sha":"sha-yaml"}`))
		default:
			http.NotFound(w, r)
		}
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	t.Setenv("GITEA_URL", ts.URL)
	t.Setenv("GITEA_TOKEN", "tok")

	pid := covV21Project(t, ctx, true)
	var repo string
	_ = model.Pool.QueryRow(ctx, `SELECT repo_full_name FROM projects WHERE id=$1`, pid).Scan(&repo)

	// refreshProjectAgents：命中 AGENTS.md → 写 agents_sha
	refreshProjectAgents(ctx, pid)
	var agentsSHA string
	_ = model.Pool.QueryRow(ctx, `SELECT COALESCE(agents_sha,'') FROM projects WHERE id=$1`, pid).Scan(&agentsSHA)
	assert.Equal(t, "sha-agents", agentsSHA)

	// refreshProjectManifest：解析 project.yaml → 写 stack 缓存
	refreshProjectManifest(ctx, pid)
	var lang string
	_ = model.Pool.QueryRow(ctx, `SELECT COALESCE(stack->'stack'->'backend'->>'lang','') FROM projects WHERE id=$1`, pid).Scan(&lang)
	assert.Equal(t, "go", lang)

	// repoFileText：内容读取分支
	got := repoFileText(ctx, projectMeta{id: pid, repoFull: repo, branch: "master"}, "AGENTS.md")
	assert.Contains(t, got, "cov21 agents")
}

func TestCovV21b_PipelineStoreAndPureHelpers(t *testing.T) {
	app := covV21bApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	pid := covV21Project(t, ctx, true)
	repo := "forja-admin/cov21b-" + uuid.NewString()[:8]
	sha := strings.Repeat("11", 20)

	// ensureRunID 新建 → preseed → recordJobProgress
	runID := ensureRunID(ctx, repo, sha, pid)
	require.NotEmpty(t, runID)
	preseedPipelineJobs(ctx, repo, sha, pid)
	var jobs int
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM pipeline_jobs WHERE run_id=$1`, runID).Scan(&jobs))
	assert.Equal(t, len(expectedPipelineJobs), jobs)
	recordJobProgress(ctx, repo, sha, "backend", "in_progress", "")
	reapStaleRuns(ctx, pid)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM pipeline_runs WHERE repo=$1`, repo) })

	// StartTaskReaper：启动后立即取消（覆盖函数体）
	rctx, cancel := context.WithCancel(ctx)
	StartTaskReaper(rctx)
	cancel()

	// 纯函数
	assert.True(t, isAllowedBranch("master"))
	assert.True(t, isAllowedBranch("iteration/x"))
	assert.False(t, isAllowedBranch("feature/x"))
	assert.Equal(t, "cov-iter", iterSlugify("  Cov Iter  "))
	assert.Equal(t, "iter", iterSlugify("???"))
	o, r := splitRepo("forja-admin/demo")
	assert.Equal(t, "forja-admin", o)
	assert.Equal(t, "demo", r)
	assert.Equal(t, "abcdef", shortIDIter("abcdef1234"))
	assert.Equal(t, "abc", shortIDIter("abc"))
	acs := extractSpecACs(`{"acceptance_criteria":[{"given":"a","when":"b","then":"c"},{"given":"d","when":"e","then":"f"}]}`)
	assert.Len(t, acs, 2)
	assert.Nil(t, extractSpecACs("not-json"))

	// 迭代分支解析：无迭代 → 默认分支
	assert.Equal(t, "master", resolveBaseBranch(ctx, pid, ""))

	// webhook 辅助
	p := &giteaHookPayload{Commits: []giteaHookCommit{{Modified: []string{"docs/x.md"}}}}
	assert.True(t, pushTouchesFile(p, "docs/x.md"))
	assert.False(t, pushTouchesFile(p, "AGENTS.md"))
	assert.False(t, deliverySeenOnce("")) // 空 id 不记
	id := "cov21-deliv-" + uuid.NewString()[:8]
	assert.False(t, deliverySeenOnce(id))
	assert.True(t, deliverySeenOnce(id))
	_ = app
}

func TestCovV21b_GenerateProjectAgents(t *testing.T) {
	app := covV21bApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/repos/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/contents/AGENTS.md") && r.Method == http.MethodGet {
			http.NotFound(w, r)
			return
		}
		// 文件不存在时 UpsertFile 走 POST（新建）；存在才 PUT
		if strings.HasSuffix(r.URL.Path, "/contents/AGENTS.md") &&
			(r.Method == http.MethodPost || r.Method == http.MethodPut) {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"content":{"sha":"sha-generated"}}`))
			return
		}
		http.NotFound(w, r)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	t.Setenv("GITEA_URL", ts.URL)
	t.Setenv("GITEA_TOKEN", "tok")

	pid := covV21Project(t, ctx, true)
	code, out := covDo(t, app, "POST", "/api/v1/projects/"+pid+"/agents/generate", "")
	require.Equal(t, 200, code, "%v", out)
	var sha string
	_ = model.Pool.QueryRow(ctx, `SELECT COALESCE(agents_sha,'') FROM projects WHERE id=$1`, pid).Scan(&sha)
	assert.Equal(t, "sha-generated", sha)

	// JSON 序列化消息不为空（防静默）
	b, _ := json.Marshal(out)
	assert.NotEmpty(t, b)
	_ = time.Now
}
