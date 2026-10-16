package api

// S1.5 覆盖率冲刺（第六波）：故障池注入 —— 用指向不可达端口的连接池替换
// model.Pool，使各 handler 的首查询失败，批量点亮 DBError 分支；结束后恢复。

import (
	"context"
	"testing"

	"forja/backend/internal/model"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCov6_DBErrorBranches(t *testing.T) {
	app := covApp(t)
	if model.Pool == nil {
		t.Skip("db unavailable")
	}
	oldPool := model.Pool
	broken, err := pgxpool.New(context.Background(), "postgres://forja:forja@127.0.0.1:1/forja?sslmode=disable&connect_timeout=1")
	require.NoError(t, err)
	t.Cleanup(func() {
		model.Pool = oldPool
		broken.Close()
	})

	restore := func() { model.Pool = oldPool }

	fresh := uuid.NewString()

	type req struct{ m, p, b string }
	cases := []req{
		{"GET", "/api/v1/projects", ""},
		{"POST", "/api/v1/projects", `{"name":"boom"}`},
		{"GET", "/api/v1/projects/" + fresh + "/members", ""},
		{"GET", "/api/v1/admin/users", ""},
		{"POST", "/api/v1/admin/users", `{"username":"u","password":"pass1234"}`},
		{"GET", "/api/v1/inbox?project_id=" + fresh, ""},
		{"POST", "/api/v1/inbox", `{"project_id":"` + fresh + `","source_type":"TEXT_PASTE","raw_content":"x"}`},
		{"GET", "/api/v1/inbox/" + fresh, ""},
		{"PATCH", "/api/v1/inbox/" + fresh, `{"raw_content":"y"}`},
		{"DELETE", "/api/v1/inbox/" + fresh, ""},
		{"POST", "/api/v1/inbox/" + fresh + "/draft", ""},
		{"GET", "/api/v1/tasks?project_id=" + fresh, ""},
		{"GET", "/api/v1/tasks/" + fresh, ""},
		{"POST", "/api/v1/tasks", `{"project_id":"` + fresh + `","title":"t"}`},
		{"POST", "/api/v1/tasks/batch", `[{"project_id":"` + fresh + `","title":"t"}]`},
		{"POST", "/api/v1/tasks/" + fresh + "/approve", `{"action":"approve"}`},
		{"POST", "/api/v1/tasks/" + fresh + "/release", ""},
		{"POST", "/api/v1/tasks/" + fresh + "/reject", `{}`},
		{"POST", "/api/v1/tasks/" + fresh + "/converge", ""},
		{"GET", "/api/v1/tasks/" + fresh + "/stream", ""},
		{"PATCH", "/api/v1/tasks/" + fresh, `{"priority":1}`},
		{"GET", "/api/v1/specs?project_id=" + fresh, ""},
		{"POST", "/api/v1/specs", `{"project_id":"` + fresh + `","layer":"BS","title":"t","content":{}}`},
		{"PUT", "/api/v1/specs/" + fresh, `{"title":"t","content":{}}`},
		{"GET", "/api/v1/specs/" + fresh, ""},
		{"POST", "/api/v1/specs/" + fresh + "/validate", ""},
		{"POST", "/api/v1/specs/" + fresh + "/pact", ""},
		{"GET", "/api/v1/specs/" + fresh + "/pact", ""},
		{"POST", "/api/v1/specs/" + fresh + "/ts-draft", ""},
		{"GET", "/api/v1/audit?project_id=" + fresh, ""},
		{"GET", "/api/v1/specs/" + fresh + "/trace", ""},
	}
	for i, c := range cases {
		model.Pool = broken
		code, out := covDo(t, app, c.m, c.p, c.b)
		if i == 14 { // batch 接口契约：逐条报错而非整体失败（分支已覆盖）
			continue
		}
		assert.NotEqual(t, 200, code, "case %d %s %s 应命中错误分支而非成功: %v", i, c.m, c.p, out)
	}
	restore()
}

func TestCov6_HelperNilPoolGuards(t *testing.T) {
	if model.Pool == nil {
		t.Skip("db unavailable")
	}
	oldPool := model.Pool
	t.Cleanup(func() { model.Pool = oldPool })

	model.Pool = nil
	assert.Equal(t, "", projectRoleOf(uuid.NewString(), uuid.NewString()), "Pool 为空应安全返回空角色")
	var nilStr *string
	assert.Equal(t, "", deref(nilStr))
}
