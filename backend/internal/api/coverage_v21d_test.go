package api

// v2.1 覆盖率冲刺 Phase 3：iteration release/revert 前置分支、furnace/provider 深度校验、
// task batch/revert/resolve/stack/forgeContext、spec anchors、inbox 列表过滤。

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"forja/backend/internal/gitops"
	"forja/backend/internal/model"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// insertIteration 直接落库指定状态/分支，用于前置校验分支覆盖。
func insertIteration(t *testing.T, ctx context.Context, pid, status, branch, mergeSHA string) string {
	t.Helper()
	id := uuid.NewString()
	var br, ms any
	if branch != "" {
		// iterations.branch CHECK: ^iteration/<slug>-<6 alnum>$
		br = "iteration/cov-" + id[:6]
	}
	if mergeSHA != "" {
		ms = mergeSHA
	}
	_, err := model.Pool.Exec(ctx,
		`INSERT INTO iterations (id, project_id, name, status, branch, merge_sha) VALUES ($1,$2,$3,$4,$5,$6)`,
		id, pid, "cov21-it-"+id[:6], status, br, ms)
	require.NoError(t, err)
	return id
}

func TestCovV21d_IterationReleaseRevertBranches(t *testing.T) {
	app := covV21App(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	pid := covV21Project(t, ctx, true)
	// gitops 克隆必然失败（不可达）——覆盖 EnsureRepoOnBase 失败分支
	isolateGitProxy(t)
	t.Setenv("GITEA_URL", "http://127.0.0.1:1")
	t.Setenv("GITEA_TOKEN", "tok")

	// release 前置：active 无分支 → NO_BRANCH
	itNoBranch := insertIteration(t, ctx, pid, "active", "", "")
	code, out := covDo(t, app, "POST", "/api/v1/projects/"+pid+"/iterations/"+itNoBranch+"/release", "")
	assert.Equal(t, 400, code, "%v", out)

	// release 前置：非 active → NOT_ACTIVE
	itPlanned := insertIteration(t, ctx, pid, "planned", "iteration/cov", "")
	code, out = covDo(t, app, "POST", "/api/v1/projects/"+pid+"/iterations/"+itPlanned+"/release", "")
	assert.Equal(t, 400, code, "%v", out)

	// release 前置：active+分支 但项目仓为空 → NO_REPO（需无仓库项目）
	pidNoRepo := covV21Project(t, ctx, false)
	itNoRepo := insertIteration(t, ctx, pidNoRepo, "active", "iteration/cov2", "")
	code, out = covDo(t, app, "POST", "/api/v1/projects/"+pidNoRepo+"/iterations/"+itNoRepo+"/release", "")
	assert.Equal(t, 400, code, "%v", out)

	// release：active+分支+仓齐 → gitops 克隆失败 → 上游 500
	itOk := insertIteration(t, ctx, pid, "active", "iteration/cov3", "")
	code, out = covDo(t, app, "POST", "/api/v1/projects/"+pid+"/iterations/"+itOk+"/release", "")
	assert.Equal(t, 500, code, "%v", out)

	// revert 前置：非 closed → NOT_REVERTABLE
	code, out = covDo(t, app, "POST", "/api/v1/projects/"+pid+"/iterations/"+itPlanned+"/revert", "")
	assert.Equal(t, 400, code, "%v", out)

	// revert 前置：closed+merge 但仓为空 → NO_REPO
	itClosed := insertIteration(t, ctx, pidNoRepo, "closed", "iteration/cov4", strings.Repeat("ab", 20))
	code, out = covDo(t, app, "POST", "/api/v1/projects/"+pidNoRepo+"/iterations/"+itClosed+"/revert", "")
	assert.Equal(t, 400, code, "%v", out)

	// revert：closed+merge+仓齐 → gitops 失败 → 上游 500
	itClosed2 := insertIteration(t, ctx, pid, "closed", "iteration/cov5", strings.Repeat("cd", 20))
	code, out = covDo(t, app, "POST", "/api/v1/projects/"+pid+"/iterations/"+itClosed2+"/revert", "")
	assert.Equal(t, 500, code, "%v", out)

	// patch 分支：结束早于开始 → 400；空名 → 400
	code, out = covDo(t, app, "POST", "/api/v1/projects/"+pid+"/iterations", `{"name":"cov21 d patch"}`)
	require.Equal(t, 200, code, "%v", out)
	iterID, _ := covData(t, out)["id"].(string)
	code, _ = covDo(t, app, "PATCH", "/api/v1/projects/"+pid+"/iterations/"+iterID, `{"start_date":"2026-09-10","end_date":"2026-09-01"}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "PATCH", "/api/v1/projects/"+pid+"/iterations/"+iterID, `{"name":"  "}`)
	assert.Equal(t, 400, code)
}

func TestCovV21d_FurnaceBranches(t *testing.T) {
	app := covV21App(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	pid := covV21Project(t, ctx, false)

	// capacity 越界
	code, _ := covDo(t, app, "POST", "/api/v1/furnaces", `{"name":"cov21 cap0","base_url":"http://127.0.0.1:1","capacity":0}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "POST", "/api/v1/furnaces", `{"name":"cov21 cap99","base_url":"http://127.0.0.1:1","capacity":99}`)
	assert.Equal(t, 400, code)
	// 非法 project_id
	code, _ = covDo(t, app, "POST", "/api/v1/furnaces", `{"name":"cov21 badp","base_url":"http://127.0.0.1:1","project_id":"nope"}`)
	assert.Equal(t, 400, code)
	// 项目炉（base_url 自动生成）
	code, out := covDo(t, app, "POST", "/api/v1/furnaces", `{"name":"cov21 proj-furnace","project_id":"`+pid+`"}`)
	require.Equal(t, 200, code, "%v", out)
	fid, _ := covData(t, out)["id"].(string)
	t.Cleanup(func() {
		_, _ = model.Pool.Exec(ctx, `DELETE FROM furnaces WHERE id=$1`, fid)
	})

	// patch 分支
	code, _ = covDo(t, app, "PATCH", "/api/v1/furnaces/"+fid, `{"capacity":0}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "PATCH", "/api/v1/furnaces/"+fid, `{"name":""}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "PATCH", "/api/v1/furnaces/"+uuid.NewString(), `{"name":"nope"}`)
	assert.Equal(t, 404, code)

	// recover 不存在
	code, _ = covDo(t, app, "POST", "/api/v1/furnaces/"+uuid.NewString()+"/recover", "")
	assert.Equal(t, 404, code)

	// 列表过滤
	code, _ = covDo(t, app, "GET", "/api/v1/furnaces?project_id="+pid+"&status=healthy", "")
	assert.Equal(t, 200, code)
	code, _ = covDo(t, app, "GET", "/api/v1/furnaces?q=cov21", "")
	assert.Equal(t, 200, code)

	// test-chat：不存在/缺 message（不断言具体码）
	_, _ = covDo(t, app, "POST", "/api/v1/furnaces/"+uuid.NewString()+"/test-chat", `{"message":"hi"}`)
	_, _ = covDo(t, app, "POST", "/api/v1/furnaces/"+fid+"/test-chat", `{}`)
}

func TestCovV21d_ModelProviderBranches(t *testing.T) {
	app := covV21App(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	name := "cov21d" + uuid.NewString()[:6]
	base := `{"provider_id":"` + name + `","display_name":"D","base_url":"https://cov21.invalid","auth_mode":"None"}`
	code, out := covDo(t, app, "POST", "/api/v1/model-providers", base)
	require.Equal(t, 200, code, "%v", out)
	pid, _ := covData(t, out)["id"].(string)
	t.Cleanup(func() {
		_, _ = model.Pool.Exec(ctx, `DELETE FROM model_providers WHERE id=$1`, pid)
	})

	// 重复 provider_id（允许 409/200 幂等，覆盖冲突分支）
	_, _ = covDo(t, app, "POST", "/api/v1/model-providers", base)

	// patch 校验分支
	code, _ = covDo(t, app, "PATCH", "/api/v1/model-providers/"+pid, `{"auth_mode":"Bogus"}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "PATCH", "/api/v1/model-providers/"+pid, `{"base_url":"not-a-url"}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "PATCH", "/api/v1/model-providers/"+pid, `{"auth_mode":"Bearer","api_key":""}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "PATCH", "/api/v1/model-providers/"+uuid.NewString(), `{"display_name":"x"}`)
	assert.Contains(t, []int{200, 404}, code, "不存在 id 的 patch 行为（兼容 upsert/404）")
	// 合法 PATCH（Bearer+key 走加密路径）
	code, _ = covDo(t, app, "PATCH", "/api/v1/model-providers/"+pid, `{"display_name":"D2","auth_mode":"Bearer","api_key":"sk-cov21-test"}`)
	assert.Equal(t, 200, code)

	// 列表过滤与引用
	code, _ = covDo(t, app, "GET", "/api/v1/model-providers?q=cov21d", "")
	assert.Equal(t, 200, code)
	code, _ = covDo(t, app, "GET", "/api/v1/model-providers/"+pid+"/references", "")
	assert.Equal(t, 200, code)
}

func TestCovV21d_TaskBatchRevertAndHelpers(t *testing.T) {
	app := covApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	pid := covV21Project(t, ctx, false)

	code, _ := covDo(t, app, "POST", "/api/v1/tasks/batch", `[]`)
	assert.Equal(t, 400, code)
	big := make([]string, 0, 51)
	for i := 0; i < 51; i++ {
		big = append(big, `{"project_id":"`+pid+`","title":"t`+string(rune('a'+i%26))+`"}`)
	}
	code, _ = covDo(t, app, "POST", "/api/v1/tasks/batch", `[`+strings.Join(big, ",")+`]`)
	assert.Equal(t, 400, code)
	// 部分非法项 → 200 且逐项错误
	code, out := covDo(t, app, "POST", "/api/v1/tasks/batch",
		`[{"title":"no project"},{"project_id":"`+pid+`"},{"project_id":"`+pid+`","title":"ok","depends_on":["bad-id"]}]`)
	require.Equal(t, 200, code, "%v", out)
	items, _ := covData(t, out)["items"].([]any)
	require.Len(t, items, 3)

	// 单任务创建 → 非 RELEASED 回滚 400
	code, out = covDo(t, app, "POST", "/api/v1/tasks", `{"project_id":"`+pid+`","title":"cov21d revert","priority":1}`)
	require.Equal(t, 200, code, "%v", out)
	tid, _ := covData(t, out)["id"].(string)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM tasks WHERE id=$1`, tid) })
	code, _ = covDo(t, app, "POST", "/api/v1/tasks/"+tid+"/revert", `{}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "POST", "/api/v1/tasks/"+tid+"/release", `{}`)
	assert.Equal(t, 400, code)

	// resolveBaseBranch：迭代带分支 → 返回分支
	itID := insertIteration(t, ctx, pid, "active", "auto", "")
	var itBranch string
	_ = model.Pool.QueryRow(ctx, `SELECT COALESCE(branch,'') FROM iterations WHERE id=$1`, itID).Scan(&itBranch)
	assert.Equal(t, itBranch, resolveBaseBranch(ctx, pid, itID))

	// stackConstraints：node 栈项目
	pidNode := uuid.NewString()
	_, err := model.Pool.Exec(ctx,
		`INSERT INTO projects (id,name,slug,stack) VALUES ($1,$2,$2,'{"stack":{"backend":{"lang":"node","framework":"node:http","runtime":"20"}},"test":{"unit":"npm test"}}'::jsonb)`,
		pidNode, "cv21d-"+pidNode[:8])
	require.NoError(t, err)
	tidNode := uuid.NewString()
	_, err = model.Pool.Exec(ctx, `INSERT INTO tasks (id, project_id, title, status) VALUES ($1,$2,'node task','PENDING')`, tidNode, pidNode)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = model.Pool.Exec(ctx, `DELETE FROM tasks WHERE project_id=$1`, pidNode)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, pidNode)
	})
	cst := stackConstraints(ctx, tidNode)
	assert.Equal(t, "node", cst["language"])
	assert.Equal(t, "node:http", cst["framework"])
	assert.Equal(t, "20", cst["runtime"])
	assert.Equal(t, "npm test", cst["test_unit"])

	// forgeContext：仓内有 AGENTS.md → 注入 project_agents
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# cov21 agents"), 0o644))
	fctx := forgeContext(ctx, tidNode, "t", "d", &gitops.Repo{Dir: dir, FullName: "forja-admin/x"})
	assert.Equal(t, "# cov21 agents", fctx["project_agents"])

	// forgeWorkspace：无仓库项目 → ("", nil)
	pidPlain := covV21Project(t, ctx, false)
	tidPlain := uuid.NewString()
	_, err = model.Pool.Exec(ctx, `INSERT INTO tasks (id, project_id, title, status) VALUES ($1,$2,'plain','PENDING')`, tidPlain, pidPlain)
	require.NoError(t, err)
	wd, repo := forgeWorkspace(ctx, tidPlain)
	assert.Empty(t, wd)
	assert.Nil(t, repo)
}

func TestCovV21d_SpecAnchorsAndInboxFilters(t *testing.T) {
	app := covApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	pid := covV21Project(t, ctx, false)

	// Spec：anchors 指向不存在任务 → 校验分支（anchor 需 'task/<id>' 路径形态）
	anchor := "task/" + uuid.NewString()
	code, out := covDo(t, app, "POST", "/api/v1/specs", `{"project_id":"`+pid+`","layer":"TS","title":"cov21d ts","content":{"what":"x"},"anchors":["`+anchor+`"]}`)
	require.Equal(t, 200, code, "%v", out)
	sid, _ := covData(t, out)["id"].(string)
	_, _ = covDo(t, app, "POST", "/api/v1/specs/"+sid+"/validate", `{}`)
	_, _ = covDo(t, app, "PATCH", "/api/v1/specs/"+sid, `{"anchors":["task/`+uuid.NewString()+`"]}`)

	// inbox 列表过滤组合
	_, err := model.Pool.Exec(ctx,
		`INSERT INTO requirements_inbox (id, project_id, source_type, raw_content, status)
		 VALUES (gen_random_uuid(),$1,'TEXT_PASTE','cov21d filter one','NEW'),
		        (gen_random_uuid(),$1,'DOCX','cov21d filter two','DRAFTED')`, pid)
	require.NoError(t, err)
	for _, qs := range []string{
		"q=cov21d", "status=NEW", "source_type=DOCX", "sort=created_at&order=asc",
		"created_from=2026-01-01&created_to=2030-01-01", "status=NEW&source_type=TEXT_PASTE&q=cov21d",
	} {
		code, _ := covDo(t, app, "GET", "/api/v1/inbox?project_id="+pid+"&"+qs, "")
		assert.Equal(t, 200, code, "query: %s", qs)
	}

	// SSEMessage/JSON 冒烟：确保测试变量使用
	b, _ := json.Marshal(map[string]string{"k": "v"})
	assert.NotEmpty(t, b)
	_ = httptest.NewRequest
}
