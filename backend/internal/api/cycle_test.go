package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"forja/backend/internal/api/respond"
	"net/http/httptest"
	"testing"

	"forja/backend/internal/model"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

// poolUsable 探活当前池：nil / 已关闭 / typed-nil（接口非 nil 内部 nil）均判定不可用。
// recover 兜底 pgxpool 对坏池操作可能产生的 panic（run#179：CI 下坏池 Exec 整包 panic）。
func poolUsable() (ok bool) {
	defer func() { _ = recover() }()
	if model.Pool == nil {
		return false
	}
	return model.Pool.Ping(context.Background()) == nil
}

// ensureTestDB 自愈式守卫：池不可用则重建；重建失败 skip 而非 panic（不拖垮整包结果）。
func ensureTestDB(t *testing.T) {
	t.Helper()
	if poolUsable() {
		return
	}
	model.Pool = nil
	if err := model.InitDB(context.Background()); err != nil {
		// 无条件输出（skip 在非 -v 下不可见）：CI 里 pg 服务不可达时给出直接证据
		fmt.Printf("[ensureTestDB] %s: db unavailable: %v\n", t.Name(), err)
		t.Skipf("db unavailable: %v", err)
	}
}

func TestHasCycle(t *testing.T) {
	ensureTestDB(t)
	ctx := context.Background()
	pid := "00000000-0000-0000-0000-000000000001"
	// create 3 tasks A->B->C
	a := uuid.NewString()
	b := uuid.NewString()
	c := uuid.NewString()
	_, _ = model.Pool.Exec(ctx, `INSERT INTO tasks (id, project_id, title, status) VALUES ($1,$2,'cycle-a','PENDING')`, a, pid)
	_, _ = model.Pool.Exec(ctx, `INSERT INTO tasks (id, project_id, title, status) VALUES ($1,$2,'cycle-b','PENDING')`, b, pid)
	_, _ = model.Pool.Exec(ctx, `INSERT INTO tasks (id, project_id, title, status) VALUES ($1,$2,'cycle-c','PENDING')`, c, pid)
	defer model.Pool.Exec(ctx, `DELETE FROM tasks WHERE id IN ($1,$2,$3)`, a, b, c)
	_, _ = model.Pool.Exec(ctx, `INSERT INTO task_dependencies (task_id, depends_on) VALUES ($1,$2)`, b, a)
	_, _ = model.Pool.Exec(ctx, `INSERT INTO task_dependencies (task_id, depends_on) VALUES ($1,$2)`, c, b)
	defer model.Pool.Exec(ctx, `DELETE FROM task_dependencies WHERE task_id IN ($1,$2)`, b, c)

	// a->b->c chain: c can reach a, so hasCycle(c,a) should be false? Actually hasCycle checks if from can reach target
	assert.False(t, hasCycle(ctx, pid, a, c))
	// b can reach a, so hasCycle(b,a) is false? Let's test c can reach a via b
	assert.True(t, hasCycle(ctx, pid, c, a))
	// self cycle
	assert.True(t, hasCycle(ctx, pid, a, a))
}

func TestBatch_Edge(t *testing.T) {
	ensureTestDB(t)
	app := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	app.Use(func(c *fiber.Ctx) error { c.Locals("role", "Admin"); return c.Next() })
	api := app.Group("/api/v1")
	RegisterTaskRoutes(api)

	// empty batch
	body, _ := json.Marshal([]any{})
	req := httptest.NewRequest("POST", "/api/v1/tasks/batch", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := app.Test(req, 5000)
	assert.Equal(t, 400, resp.StatusCode)

	// too large
	large := make([]any, 51)
	for i := range large {
		large[i] = map[string]any{"title": "x"}
	}
	body2, _ := json.Marshal(large)
	req2 := httptest.NewRequest("POST", "/api/v1/tasks/batch", bytes.NewReader(body2))
	req2.Header.Set("Content-Type", "application/json")
	resp2, _ := app.Test(req2, 5000)
	assert.Equal(t, 400, resp2.StatusCode)

	// invalid depends_on
	body3, _ := json.Marshal([]any{map[string]any{"title": "t", "project_id": "00000000-0000-0000-0000-000000000001", "depends_on": []string{"not-uuid"}}})
	req3 := httptest.NewRequest("POST", "/api/v1/tasks/batch", bytes.NewReader(body3))
	req3.Header.Set("Content-Type", "application/json")
	resp3, _ := app.Test(req3, 5000)
	assert.Equal(t, 200, resp3.StatusCode)
	var out map[string]any
	_ = json.NewDecoder(resp3.Body).Decode(&out)
	assert.Equal(t, float64(0), out["code"])
	data, _ := out["data"].(map[string]any)
	items, _ := data["items"].([]any)
	assert.Len(t, items, 1)
	m, _ := items[0].(map[string]any)
	errObj, _ := m["error"].(map[string]any)
	assert.Equal(t, "INVALID_PARAM", errObj["code"])
	assert.Contains(t, errObj["detail"], "invalid depends_on")
}

func TestListTasks_Filter(t *testing.T) {
	ensureTestDB(t)
	app := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	app.Use(func(c *fiber.Ctx) error { c.Locals("role", "Admin"); return c.Next() })
	api := app.Group("/api/v1")
	RegisterTaskRoutes(api)
	req := httptest.NewRequest("GET", "/api/v1/tasks?project_id=00000000-0000-0000-0000-000000000001&status=PENDING", nil)
	resp, _ := app.Test(req, 5000)
	assert.Equal(t, 200, resp.StatusCode)
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	assert.Equal(t, float64(0), out["code"])
	data, _ := out["data"].(map[string]any)
	_, ok := data["items"]
	assert.True(t, ok)
	_, ok = data["total"]
	assert.True(t, ok)
}

func TestGetDag_InvalidProject(t *testing.T) {
	ensureTestDB(t)
	app := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	app.Use(func(c *fiber.Ctx) error { c.Locals("role", "Admin"); return c.Next() })
	api := app.Group("/api/v1")
	RegisterTaskRoutes(api)
	req := httptest.NewRequest("GET", "/api/v1/projects/not-uuid/dag", nil)
	resp, _ := app.Test(req, 5000)
	assert.Equal(t, 400, resp.StatusCode)
}
