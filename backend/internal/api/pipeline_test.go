package api

// 可观测性 P0 端点单测：列表 / 详情 / summary（observability-design §3.3 验收）

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"testing"

	"forja/backend/internal/api/respond"
	"forja/backend/internal/model"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newPipelineApp(t *testing.T) *fiber.App {
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
	RegisterPipelineRoutes(api)
	return app
}

func seedRun(t *testing.T, projectID any, status string, jobs [][2]string, event ...string) runRow {
	t.Helper()
	ctx := context.Background()
	ev := "push"
	if len(event) > 0 && event[0] != "" {
		ev = event[0]
	}
	repo := fmt.Sprintf("forja-admin/pipeline-test-%s", uuid.NewString()[:8])
	sha := uuid.NewString() + uuid.NewString()
	var pid any
	if v, ok := projectID.(string); ok && v != "" {
		pid = v
	}
	var id, createdAt, updatedAt string
	require.NoError(t, model.Pool.QueryRow(ctx, `
		INSERT INTO pipeline_runs (id, project_id, repo, sha, branch, event, status, actor)
		VALUES (gen_random_uuid(), $1, $2, $3, 'master', $5, $4, 'tester')
		RETURNING id::text, created_at::text, updated_at::text`,
		pid, repo, sha[:64], status, ev).Scan(&id, &createdAt, &updatedAt))
	for _, j := range jobs {
		_, err := model.Pool.Exec(ctx, `
			INSERT INTO pipeline_jobs (run_id, name, conclusion) VALUES ($1, $2, $3)`, id, j[0], j[1])
		require.NoError(t, err)
	}
	t.Cleanup(func() {
		_, _ = model.Pool.Exec(ctx, `DELETE FROM pipeline_runs WHERE id=$1`, id)
	})
	m := runRow{ID: id, ProjectID: "", Repo: repo, SHA: sha[:64], Branch: "master",
		Event: ev, Status: status, Actor: "tester",
		CreatedAt: createdAt, UpdatedAt: updatedAt}
	return m
}

func getJSON(t *testing.T, app *fiber.App, path string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	resp, err := app.Test(req, 15000)
	require.NoError(t, err)
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestPipelines_ListDetailSummary(t *testing.T) {
	app := newPipelineApp(t)
	const seedProject = "00000000-0000-0000-0000-000000000001"

	ok := seedRun(t, seedProject, "SUCCESS", [][2]string{{"backend", "success"}, {"frontend", "success"}})
	fail := seedRun(t, seedProject, "FAILURE", [][2]string{{"backend", "failure"}})
	orphan := seedRun(t, nil, "RUNNING", nil) // 未映射仓库

	// 列表：按项目筛选包含两条；orphan 不属于该项目
	code, out := getJSON(t, app, "/api/v1/pipelines?project_id="+seedProject)
	require.Equal(t, 200, code)
	data := out["data"].(map[string]any)
	items := data["items"].([]any)
	assert.GreaterOrEqual(t, len(items), 2)

	// status 筛选命中 seeded FAILURE 行
	code, out = getJSON(t, app, "/api/v1/pipelines?project_id="+seedProject+"&status=FAILURE")
	require.Equal(t, 200, code)
	items = out["data"].(map[string]any)["items"].([]any)
	var foundFail bool
	for _, it := range items {
		m := it.(map[string]any)
		assert.Equal(t, "FAILURE", m["status"])
		if m["id"] == fail.ID {
			foundFail = true
		}
	}
	assert.True(t, foundFail, "FAILURE 筛选应包含 seeded 行")

	// 详情聚合 jobs
	code, out = getJSON(t, app, "/api/v1/pipelines/"+ok.ID)
	require.Equal(t, 200, code)
	detail := out["data"].(map[string]any)
	jobs := detail["jobs"].([]any)
	assert.Len(t, jobs, 2)

	// 未映射仓库详情：Admin 可见（仅认证门），project_id 为空
	code, out = getJSON(t, app, "/api/v1/pipelines/"+orphan.ID)
	require.Equal(t, 200, code)
	assert.Empty(t, out["data"].(map[string]any)["project_id"])

	// summary：running/failed_today 计数与 last_run
	code, out = getJSON(t, app, "/api/v1/pipelines/summary")
	require.Equal(t, 200, code)
	sum := out["data"].(map[string]any)
	assert.GreaterOrEqual(t, sum["running"].(float64), float64(1))
	assert.GreaterOrEqual(t, sum["failed_today"].(float64), float64(1))
	lastRun := sum["last_run"].(map[string]any)
	assert.NotEmpty(t, lastRun["id"])

	// 非法参数
	code, _ = getJSON(t, app, "/api/v1/pipelines/not-a-uuid")
	assert.Equal(t, 400, code)
}

// reapStaleRuns 僵尸清扫（P0 补丁）：零 job 超时→SKIPPED；有 job 停更超时→FAILURE。
// S6：push PENDING 行永不清理；仅 manual/workflow 类 RUNNING 行参与 reap。
func TestReapStaleRuns(t *testing.T) {
	newPipelineApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()

	never := seedRun(t, "", "RUNNING", nil, "manual")
	stale, err := model.Pool.Exec(ctx,
		`UPDATE pipeline_runs SET updated_at=now()-interval '15 minutes' WHERE id=$1`, never.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1), stale.RowsAffected())

	fresh := seedRun(t, "", "PENDING", nil) // push 待构建：永不被扫
	partial := seedRun(t, "", "RUNNING", [][2]string{{"backend", "success"}}, "manual")
	_, err = model.Pool.Exec(ctx,
		`UPDATE pipeline_runs SET updated_at=now()-interval '40 minutes' WHERE id=$1`, partial.ID)
	require.NoError(t, err)

	reapStaleRuns(ctx)

	var s string
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT status FROM pipeline_runs WHERE id=$1`, never.ID).Scan(&s))
	assert.Equal(t, "SKIPPED", s)
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT status FROM pipeline_runs WHERE id=$1`, fresh.ID).Scan(&s))
	assert.Equal(t, "PENDING", s)
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT status FROM pipeline_runs WHERE id=$1`, partial.ID).Scan(&s))
	assert.Equal(t, "FAILURE", s)
}
