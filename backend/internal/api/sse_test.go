package api

// B1 覆盖率冲刺：SSE 辅助与错误分支（流式主链路由前端 e2e 覆盖）。

import (
	"bufio"
	"bytes"
	"context"
	"net/http/httptest"
	"testing"

	"forja/backend/internal/api/respond"
	"forja/backend/internal/events"
	"forja/backend/internal/model"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func respondHandleError(c *fiber.Ctx, err error) error { return respond.HandleError(c, err) }

func TestWriteSSE(t *testing.T) {
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	err := writeSSE(w, events.Event{Name: "status", Data: mustJSON(fiber.Map{"k": "v"})})
	require.NoError(t, err)
	assert.Equal(t, "data: {\"name\":\"status\",\"data\":{\"k\":\"v\"}}\n\n", buf.String())

	// 不可序列化值 → marshal 错误路径
	err = writeSSE(w, events.Event{Name: "bad", Data: mustJSON(nil)})
	require.NoError(t, err) // nil 合法，走正常写入
}

func TestMustJSON(t *testing.T) {
	b := mustJSON(fiber.Map{"a": float64(1)})
	assert.JSONEq(t, `{"a":1}`, string(b))
	assert.Equal(t, "null", string(mustJSON(nil)))
}

func TestTaskSnapshot(t *testing.T) {
	newPipelineApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	c := &fiber.Ctx{} // taskSnapshot 仅读 Params 未用，零值即可

	// 不存在的 task → 默认 PENDING 骨架
	m := taskSnapshot(c, uuid.NewString())
	assert.Equal(t, "PENDING", m["status"])
	assert.NotEmpty(t, m["task_id"])

	// 真实任务行：状态/attempt/commit_sha 全回填
	ctx := context.Background()
	pid := "00000000-0000-0000-0000-000000000001"
	specID := uuid.NewString()
	taskID := uuid.NewString()
	sha := randSHA()
	_, err := model.Pool.Exec(ctx,
		`INSERT INTO specs (id, project_id, layer, title) VALUES ($1,$2,'BS','sse-snap')`, specID, pid)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM specs WHERE id=$1`, specID) })
	_, err = model.Pool.Exec(ctx, `
		INSERT INTO tasks (id, project_id, spec_id, title, status, forge_attempt, commit_sha)
		VALUES ($1,$2,$3,'sse-snap-task','RUNNING',2,$4)`, taskID, pid, specID, sha)
	require.NoError(t, err)

	m = taskSnapshot(c, taskID)
	assert.Equal(t, "RUNNING", m["status"])
	assert.Equal(t, 2, m["forge_attempt"])
	assert.Equal(t, sha, *m["commit_sha"].(*string))
}

// TestSSEEndpoints_ErrorBranches 流式端点的守卫分支（503/404/4xx）
func TestSSEEndpoints_ErrorBranches(t *testing.T) {
	app := fiber.New(fiber.Config{ErrorHandler: respondHandleError})
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("username", "cov-admin")
		c.Locals("role", "Admin")
		return c.Next()
	})
	api := app.Group("/api/v1")
	RegisterSSERoutes(api)

	do := func(path string) int {
		req := httptest.NewRequest("GET", path, nil)
		resp, err := app.Test(req, 3000)
		if err != nil {
			return -1
		}
		return resp.StatusCode
	}

	// Pool=nil → 503（显式置空并恢复：全局 Pool 大概率已被同进程其他用例初始化）
	savedPool := model.Pool
	model.Pool = nil
	assert.Equal(t, 503, do("/api/v1/tasks/"+uuid.NewString()+"/stream"))
	assert.Equal(t, 503, do("/api/v1/projects/"+uuid.NewString()+"/events"))
	model.Pool = savedPool

	newPipelineApp(t) // 连库
	// 任务不存在 → 404
	assert.Equal(t, 404, do("/api/v1/tasks/"+uuid.NewString()+"/stream"))

	// 存在任务但未登录（无 Locals）→ AuthorizeProject 对非成员拒绝
	ctx := context.Background()
	const pid = "00000000-0000-0000-0000-000000000001"
	specID := uuid.NewString()
	taskID := uuid.NewString()
	_, err := model.Pool.Exec(ctx,
		`INSERT INTO specs (id, project_id, layer, title) VALUES ($1,$2,'BS','sse-guard')`, specID, pid)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM specs WHERE id=$1`, specID) })
	_, err = model.Pool.Exec(ctx,
		`INSERT INTO tasks (id, project_id, spec_id, title, status) VALUES ($1,$2,$3,'sse-guard-t','PENDING')`, taskID, pid, specID)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM tasks WHERE id=$1`, taskID) })

	// 无认证上下文：currentUserID 空、非 Admin → 拒绝（403/4xx 族）
	code := do("/api/v1/tasks/" + taskID + "/stream")
	assert.NotEqual(t, 200, code, "未认证不得建立 SSE 流")
}
