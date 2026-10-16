package api

// 可观测性 P2 端点单测：
//   - /pipelines/metrics 按日聚合（success rate / 平均时长）
//   - /specs/:id/lifecycle 生命周期聚合（observability-design §6.3）

import (
	"context"
	"os"
	"testing"

	"forja/backend/internal/api/respond"
	"forja/backend/internal/model"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newLifecycleApp(t *testing.T) *fiber.App {
	t.Helper()
	if model.Pool == nil {
		if os.Getenv("DATABASE_URL") == "" {
			os.Setenv("DATABASE_URL", "postgres://forja:forja@localhost:5432/forja?sslmode=disable")
		}
		if err := model.InitDB(context.Background()); err != nil {
			t.Skipf("DB not available: %v", err)
		}
	}
	app := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("username", "test-admin")
		c.Locals("role", "Admin")
		return c.Next()
	})
	api := app.Group("/api/v1")
	RegisterSpecRoutes(api)
	RegisterPipelineRoutes(api)
	return app
}

func mustExec(t *testing.T, sql string, args ...any) {
	t.Helper()
	_, err := model.Pool.Exec(context.Background(), sql, args...)
	require.NoError(t, err)
}

func TestPipelines_Metrics(t *testing.T) {
	app := newLifecycleApp(t)
	const seedProject = "00000000-0000-0000-0000-000000000001"

	seedRunOnDay := func(t *testing.T, status string, durationMs int64, daysAgo int) string {
		t.Helper()
		id := uuid.NewString()
		mustExec(t, `
			INSERT INTO pipeline_runs (id, project_id, repo, sha, branch, event, status, duration_ms, created_at)
			VALUES ($1, $2, $3, $4, 'master', 'push', $5, $6, date_trunc('day', now()) - make_interval(days => $7))`,
			id, seedProject, "forja-admin/metrics-"+uuid.NewString()[:8],
			(uuid.NewString() + uuid.NewString())[:64], status, durationMs, daysAgo)
		t.Cleanup(func() { _, _ = model.Pool.Exec(context.Background(), `DELETE FROM pipeline_runs WHERE id=$1`, id) })
		return id
	}

	seedRunOnDay(t, "SUCCESS", 100_000, 0)
	seedRunOnDay(t, "FAILURE", 300_000, 0)
	seedRunOnDay(t, "RUNNING", 0, 0)
	seedRunOnDay(t, "SUCCESS", 500_000, 3)

	code, out := getJSON(t, app, "/api/v1/pipelines/metrics?days=7&project_id="+seedProject)
	require.Equal(t, 200, code)
	data := out["data"].(map[string]any)
	items := data["items"].([]any)
	require.GreaterOrEqual(t, len(items), 2)

	byDay := map[string]map[string]any{}
	for _, it := range items {
		m := it.(map[string]any)
		byDay[m["day"].(string)] = m
	}

	// 今日行：total 最大的当天（同库其他套件可能残留当日数据，取宽松锚点）
	var todayKey string
	for d, m := range byDay {
		if todayKey == "" || m["total"].(float64) > byDay[todayKey]["total"].(float64) {
			todayKey = d
		}
	}
	require.NotEmpty(t, todayKey, "应存在今日聚合行")
	today := byDay[todayKey]
	assert.GreaterOrEqual(t, today["total"].(float64), float64(3))
	assert.GreaterOrEqual(t, today["success"].(float64), float64(1))
	assert.GreaterOrEqual(t, today["failure"].(float64), float64(1))
	rate := today["success_rate"].(float64)
	assert.Greater(t, rate, float64(0))
	assert.LessOrEqual(t, rate, float64(100))
	assert.NotNil(t, today["avg_duration_ms"])

	// 参数边界：越界回退默认、非法数字不 5xx
	code, _ = getJSON(t, app, "/api/v1/pipelines/metrics?days=999")
	assert.Equal(t, 200, code)
	code, _ = getJSON(t, app, "/api/v1/pipelines/metrics?days=abc")
	assert.Equal(t, 200, code)
}

func TestSpecLifecycle(t *testing.T) {
	app := newLifecycleApp(t)
	const seedProject = "00000000-0000-0000-0000-000000000001"

	specID := uuid.NewString()
	reqID := uuid.NewString()
	taskID := uuid.NewString()
	sha := uuid.NewString() + uuid.NewString()
	runID := uuid.NewString()
	repo := "forja-admin/lc-" + uuid.NewString()[:8]

	cleanup := func() {
		_, _ = model.Pool.Exec(context.Background(), `DELETE FROM pipeline_runs WHERE id=$1`, runID)
		_, _ = model.Pool.Exec(context.Background(), `DELETE FROM tasks WHERE id=$1`, taskID)
		_, _ = model.Pool.Exec(context.Background(), `DELETE FROM requirements_inbox WHERE id=$1`, reqID)
		_, _ = model.Pool.Exec(context.Background(), `DELETE FROM specs WHERE id=$1`, specID)
	}
	cleanup() // 清理历史残留
	t.Cleanup(cleanup)

	mustExec(t, `
		INSERT INTO specs (id, project_id, layer, title, status, current_version)
		VALUES ($1,$2,'BS','lc-spec','VALIDATED','v0.1.2')`, specID, seedProject)
	for _, v := range []string{"v0.1.0", "v0.1.1", "v0.1.2"} {
		mustExec(t, `
			INSERT INTO spec_versions (id, spec_id, version, content) VALUES ($1,$2,$3,'{}'::jsonb)`,
			uuid.NewString(), specID, v)
	}
	mustExec(t, `
		INSERT INTO requirements_inbox (id, project_id, source_type, raw_content)
		VALUES ($1,$2,'TEXT_PASTE','lifecycle raw')`, reqID, seedProject)
	mustExec(t, `
		INSERT INTO requirement_spec_links (requirement_id, spec_id) VALUES ($1,$2)`, reqID, specID)
	mustExec(t, `
		INSERT INTO tasks (id, project_id, spec_id, title, status, commit_sha)
		VALUES ($1,$2,$3,'lc-task','RELEASE_PENDING',$4)`, taskID, seedProject, specID, sha[:64])
	mustExec(t, `
		INSERT INTO code_changes (task_id, file_path, diff) VALUES ($1,'main.go','+ok')`, taskID)
	mustExec(t, `
		INSERT INTO pipeline_runs (id, project_id, repo, sha, branch, event, status, duration_ms)
		VALUES ($1,$2,$3,$4,'master','push','SUCCESS',42000)`, runID, seedProject, repo, sha[:64])

	code, out := getJSON(t, app, "/api/v1/specs/"+specID+"/lifecycle")
	require.Equal(t, 200, code)
	d := out["data"].(map[string]any)

	spec := d["spec"].(map[string]any)
	assert.Equal(t, "BS", spec["layer"])
	assert.Equal(t, "VALIDATED", spec["status"])
	assert.Equal(t, "v0.1.2", spec["current_version"])

	assert.Len(t, d["versions"].([]any), 3)
	reqs := d["requirements"].([]any)
	require.Len(t, reqs, 1)
	assert.Equal(t, reqID, reqs[0].(map[string]any)["id"])

	tasks := d["tasks"].([]any)
	require.Len(t, tasks, 1)
	tm := tasks[0].(map[string]any)
	assert.Equal(t, "RELEASE_PENDING", tm["status"])
	assert.NotEmpty(t, tm["commit_sha"])

	cc := d["code_changes"].([]any)
	require.Len(t, cc, 1)
	assert.Equal(t, "main.go", cc[0].(map[string]any)["file_path"])

	runs := d["ci_runs"].([]any)
	require.Len(t, runs, 1)
	rm := runs[0].(map[string]any)
	assert.Equal(t, "SUCCESS", rm["status"])
	assert.EqualValues(t, 42000, rm["duration_ms"])

	// 无任务/代码/run 的纯 spec：空数组而非 null
	bareID := uuid.NewString()
	t.Cleanup(func() { _, _ = model.Pool.Exec(context.Background(), `DELETE FROM specs WHERE id=$1`, bareID) })
	mustExec(t, `
		INSERT INTO specs (id, project_id, layer, title, status)
		VALUES ($1,$2,'DS','bare','DRAFT')`, bareID, seedProject)
	code, out = getJSON(t, app, "/api/v1/specs/"+bareID+"/lifecycle")
	require.Equal(t, 200, code)
	d = out["data"].(map[string]any)
	assert.Empty(t, d["tasks"].([]any))
	assert.Empty(t, d["ci_runs"].([]any))

	// 非法 id / 不存在
	code, _ = getJSON(t, app, "/api/v1/specs/not-a-uuid/lifecycle")
	assert.Equal(t, 400, code)
	code, _ = getJSON(t, app, "/api/v1/specs/"+uuid.NewString()+"/lifecycle")
	assert.Equal(t, 404, code)
}
