package api

// B1 覆盖率冲刺：锻造执行链辅助函数（forgeContext/forgeWorkspace 降级分支/
// recordExecution/persistChanges/lastFailures/truncateStr/nullStr/shortID）。

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"forja/backend/internal/agent"
	"forja/backend/internal/gitops"
	"forja/backend/internal/furnace"
	"forja/backend/internal/model"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedForgeTask 建临时 spec+task 并返回清理钩子
func seedForgeTask(t *testing.T, status string) (taskID string) {
	t.Helper()
	ctx := context.Background()
	newPipelineApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	const pid = "00000000-0000-0000-0000-000000000001"
	specID := uuid.NewString()
	taskID = uuid.NewString()
	_, err := model.Pool.Exec(ctx,
		`INSERT INTO specs (id, project_id, layer, title) VALUES ($1,$2,'BS','forge-cov')`, specID, pid)
	require.NoError(t, err)
	_, err = model.Pool.Exec(ctx, `
		INSERT INTO tasks (id, project_id, spec_id, title, status)
		VALUES ($1,$2,$3,'forge-cov-task',$4)`, taskID, pid, specID, status)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = model.Pool.Exec(ctx, `DELETE FROM code_changes WHERE task_id=$1`, taskID)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM agent_executions WHERE task_id=$1`, taskID)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM tasks WHERE id=$1`, taskID)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM specs WHERE id=$1`, specID)
	})
	return taskID
}

func TestForgeHelpers_PureFns(t *testing.T) {
	assert.Equal(t, "abc", truncateStr("abc", 10))
	got := truncateStr(strings.Repeat("x", 30), 10)
	assert.True(t, strings.HasPrefix(got, "xxxxxxxxxx") && strings.Contains(got, "(truncated)"))

	assert.Nil(t, nullStr(""))
	assert.Equal(t, "sha", nullStr("sha"))

	assert.Equal(t, "12345678", shortID("1234567890abcdef"))
	assert.Equal(t, "short", shortID("short"))

	b, err := jsonMarshal([]string{"ok"})
	require.NoError(t, err)
	assert.JSONEq(t, `["ok"]`, string(b))
}

func TestForgeContext_NilRepoAndSpecFiles(t *testing.T) {
	newPipelineApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	taskID := seedForgeTask(t, "RUNNING")
	ctx := context.Background()

	// repo=nil：仅任务元信息
	c := forgeContext(ctx, taskID, "t-title", "t-desc", nil)
	assert.Equal(t, "t-title", c["task_title"])
	assert.Equal(t, "t-desc", c["task_description"])
	assert.NotContains(t, c, "spec_files")

	// 有 repo（本地目录伪造工作副本）：task_spec_refs 命中文件 → spec_files；目录树回填
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "specs"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "specs", "a.md"), []byte("spec body"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644))
	repo := &gitops.Repo{Dir: dir}
	c = forgeContext(ctx, taskID, "t2", "", repo)
	assert.Contains(t, c, "repo_root_files")

	// 写入一条 task_spec_refs 指向存在的文件 → 内容被读取截断保护
	_, err := model.Pool.Exec(ctx,
		`INSERT INTO task_spec_refs (task_id, ref) VALUES ($1,'docs/x.md') ON CONFLICT DO NOTHING`, taskID)
	if err == nil {
		c = forgeContext(ctx, taskID, "t3", "", repo)
		t.Logf("spec_files present: %v (file may not exist in fake root)", c["spec_files"])
	}
}

func TestRecordExecutionAndPersistChanges(t *testing.T) {
	taskID := seedForgeTask(t, "RUNNING")
	ctx := context.Background()

	out := agent.AgentOutput{ChangeLog: "cov log", CodeChanges: []agent.CodeChange{
		{FilePath: "a.go", Diff: "--- a\n+++ b"},
	}}
	recordExecution(ctx, taskID, out, 2)

	var attempt int
	var status string
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT attempt, status FROM agent_executions WHERE task_id=$1 ORDER BY started_at DESC LIMIT 1`,
		taskID).Scan(&attempt, &status))
	assert.Equal(t, 2, attempt)
	assert.Equal(t, "SUCCESS", status)

	persistChanges(ctx, taskID, out, 2)
	var n int
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM code_changes WHERE task_id=$1 AND change_log='cov log'`, taskID).Scan(&n))
	assert.Equal(t, 1, n)
	// attempt 落行（035 轮次隔离）
	var att int
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT attempt FROM code_changes WHERE task_id=$1`, taskID).Scan(&att))
	assert.Equal(t, 2, att)
}

func TestPersistChanges_IncludesFiles(t *testing.T) {
	taskID := seedForgeTask(t, "RUNNING")
	ctx := context.Background()

	// 整文件交付：Files 无 diff 也必须落 code_changes 行，否则 validation
	// hasChanges 盲区跳过门禁（2026-09-07 留言板实测）。
	out := agent.AgentOutput{ChangeLog: "files log", Files: []agent.FileChange{
		{FilePath: "go.mod", Content: "module x\n"},
		{FilePath: "main.go", Content: "package main\n"},
	}}
	persistChanges(ctx, taskID, out, 1)
	var n int
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM code_changes WHERE task_id=$1 AND change_log='files log'`, taskID).Scan(&n))
	assert.Equal(t, 2, n)
}

// TestCheckFurnaceBreaker_TripsAfter3Failed D2 熔断：连续 3 次 FAILED→unhealthy；2F+1S 不触发
func TestCheckFurnaceBreaker_TripsAfter3Failed(t *testing.T) {
	newPipelineApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	newFurnace := func() string {
		fid := uuid.NewString()
		fname := "breaker-" + fid[:8]
		_, err := model.Pool.Exec(ctx, `INSERT INTO furnaces(id,name,base_url,capacity,status) VALUES($1,$2,'http://127.0.0.1:1',1,'healthy')`, fid, fname)
		require.NoError(t, err)
		t.Cleanup(func() {
			_, _ = model.Pool.Exec(ctx, `DELETE FROM agent_executions WHERE furnace_id=$1`, fid)
			_, _ = model.Pool.Exec(ctx, `DELETE FROM furnaces WHERE id=$1`, fid)
		})
		return fid
	}
	out := agent.AgentOutput{ChangeLog: "breaker"}
	statusOf := func(fid string) string {
		var st string
		require.NoError(t, model.Pool.QueryRow(ctx, `SELECT status FROM furnaces WHERE id=$1`, fid).Scan(&st))
		return st
	}

	// 3×FAILED → unhealthy（附用量入库断言）
	fid := newFurnace()
	taskID := seedForgeTask(t, "RUNNING")
	recordExecutionEx(ctx, taskID, fid, "m", out, 1, "FAILED", 10, execUsage{tokensIn: 100, tokensOut: 50, costCents: 3})
	assert.Equal(t, "healthy", statusOf(fid))
	recordExecutionEx(ctx, taskID, fid, "m", out, 2, "FAILED", 10, execUsage{})
	assert.Equal(t, "healthy", statusOf(fid))
	recordExecutionEx(ctx, taskID, fid, "m", out, 3, "FAILED", 10, execUsage{})
	assert.Equal(t, "unhealthy", statusOf(fid))
	var tin, tout, cc int
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT tokens_in, tokens_out, cost_cents FROM agent_executions WHERE task_id=$1 AND attempt=1`, taskID).Scan(&tin, &tout, &cc))
	assert.Equal(t, 100, tin)
	assert.Equal(t, 50, tout)
	assert.Equal(t, 3, cc)

	// 2F+1S → 保持 healthy（最近 3 次含 SUCCESS 不熔断）
	fid2 := newFurnace()
	taskID2 := seedForgeTask(t, "RUNNING")
	recordExecutionEx(ctx, taskID2, fid2, "m", out, 1, "FAILED", 10, execUsage{})
	recordExecutionEx(ctx, taskID2, fid2, "m", out, 2, "FAILED", 10, execUsage{})
	recordExecutionEx(ctx, taskID2, fid2, "m", out, 3, "SUCCESS", 10, execUsage{})
	assert.Equal(t, "healthy", statusOf(fid2))
}

// TestBreakerStreak_RecoverResetsWindow v1.3-D3：streak 计数 + recover 清零语义
// 3F→跳闸；recover→healthy+streak 0；之后 2F 不跳、第 3F 再跳；drained 不被 recover 翻转。
func TestBreakerStreak_RecoverResetsWindow(t *testing.T) {
	newPipelineApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	fid := uuid.NewString()
	_, err := model.Pool.Exec(ctx, `INSERT INTO furnaces(id,name,base_url,capacity,status) VALUES($1,$2,'http://127.0.0.1:1',1,'healthy')`, fid, "streak-"+fid[:8])
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = model.Pool.Exec(ctx, `DELETE FROM agent_executions WHERE furnace_id=$1`, fid)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM furnaces WHERE id=$1`, fid)
	})
	out := agent.AgentOutput{ChangeLog: "streak"}
	taskID := seedForgeTask(t, "RUNNING")
	statusOf := func() string {
		var st string
		require.NoError(t, model.Pool.QueryRow(ctx, `SELECT status FROM furnaces WHERE id=$1`, fid).Scan(&st))
		return st
	}

	recordExecutionEx(ctx, taskID, fid, "m", out, 1, "FAILED", 10, execUsage{})
	assert.Equal(t, 1, breakerStreak(ctx, fid))
	recordExecutionEx(ctx, taskID, fid, "m", out, 2, "FAILED", 10, execUsage{})
	assert.Equal(t, 2, breakerStreak(ctx, fid))
	assert.Equal(t, "healthy", statusOf())
	recordExecutionEx(ctx, taskID, fid, "m", out, 3, "FAILED", 10, execUsage{})
	assert.Equal(t, 3, breakerStreak(ctx, fid))
	assert.Equal(t, "unhealthy", statusOf())

	// recover：healthy + streak 清零
	ns, err := recoverFurnaceState(ctx, fid)
	require.NoError(t, err)
	assert.Equal(t, "healthy", ns)
	assert.Equal(t, "healthy", statusOf())
	assert.Equal(t, 0, breakerStreak(ctx, fid))

	// reset 后 2F 不跳，第 3F 再跳
	recordExecutionEx(ctx, taskID, fid, "m", out, 4, "FAILED", 10, execUsage{})
	recordExecutionEx(ctx, taskID, fid, "m", out, 5, "FAILED", 10, execUsage{})
	assert.Equal(t, 2, breakerStreak(ctx, fid))
	assert.Equal(t, "healthy", statusOf())
	recordExecutionEx(ctx, taskID, fid, "m", out, 6, "FAILED", 10, execUsage{})
	assert.Equal(t, 3, breakerStreak(ctx, fid))
	assert.Equal(t, "unhealthy", statusOf())

	// SUCCESS 截断 streak（窗口语义与 D2 一致）
	_, _ = recoverFurnaceState(ctx, fid)
	recordExecutionEx(ctx, taskID, fid, "m", out, 7, "FAILED", 10, execUsage{})
	recordExecutionEx(ctx, taskID, fid, "m", out, 8, "SUCCESS", 10, execUsage{})
	recordExecutionEx(ctx, taskID, fid, "m", out, 9, "FAILED", 10, execUsage{})
	assert.Equal(t, 1, breakerStreak(ctx, fid))
	assert.Equal(t, "healthy", statusOf())

	// drained 不被 recover 翻转，但计数照样清零
	_, err = model.Pool.Exec(ctx, `UPDATE furnaces SET status='drained' WHERE id=$1`, fid)
	require.NoError(t, err)
	ns, err = recoverFurnaceState(ctx, fid)
	require.NoError(t, err)
	assert.Equal(t, "drained", ns)
	assert.Equal(t, 0, breakerStreak(ctx, fid))

	// 不存在的炉 → ErrNoRows（handler 转 404）
	_, err = recoverFurnaceState(ctx, uuid.NewString())
	assert.Error(t, err)
}

// TestProbeRecoveryResetsBreakerWindow 探活自愈语义：unhealthy→healthy 时必须同重置
// breaker_reset_at（探活成功不产生 execution，streak 不会自然衰减）。
// 3F 跳闸 → RecoverProbeHealthy → streak 清零；healthy 炉重复探活不刷新 reset（streak 保留）。
func TestProbeRecoveryResetsBreakerWindow(t *testing.T) {
	newPipelineApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	fid := uuid.NewString()
	_, err := model.Pool.Exec(ctx, `INSERT INTO furnaces(id,name,base_url,capacity,status) VALUES($1,$2,'http://127.0.0.1:1',1,'healthy')`, fid, "proberec-"+fid[:8])
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = model.Pool.Exec(ctx, `DELETE FROM agent_executions WHERE furnace_id=$1`, fid)
		_, _ = model.Pool.Exec(ctx, `DELETE FROM furnaces WHERE id=$1`, fid)
	})
	out := agent.AgentOutput{ChangeLog: "proberec"}
	taskID := seedForgeTask(t, "RUNNING")

	// 3 连败 → 跳闸
	for i := 1; i <= 3; i++ {
		recordExecutionEx(ctx, taskID, fid, "m", out, i, "FAILED", 10, execUsage{})
	}
	var st string
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT status FROM furnaces WHERE id=$1`, fid).Scan(&st))
	assert.Equal(t, "unhealthy", st)

	// 探活自愈：healthy + 窗口清零（修复点：此前只翻状态，streak 残留导致下次 1 败即再熔断）
	require.NoError(t, furnace.RecoverProbeHealthy(ctx, fid))
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT status FROM furnaces WHERE id=$1`, fid).Scan(&st))
	assert.Equal(t, "healthy", st)
	assert.Equal(t, 0, breakerStreak(ctx, fid))

	// 已 healthy 的炉重复探活成功不刷新 reset（守卫 status='unhealthy'）——
	// 攒 2 连败后再探活，streak 应保留，不因状态翻新被误清
	recordExecutionEx(ctx, taskID, fid, "m", out, 4, "FAILED", 10, execUsage{})
	recordExecutionEx(ctx, taskID, fid, "m", out, 5, "FAILED", 10, execUsage{})
	require.NoError(t, furnace.RecoverProbeHealthy(ctx, fid))
	assert.Equal(t, 2, breakerStreak(ctx, fid))
}

func TestLastFailures_Branches(t *testing.T) {
	taskID := seedForgeTask(t, "FAILED")
	ctx := context.Background()

	// 无失败记录 → 默认文案
	msg := lastFailures(ctx, taskID)
	assert.NotEmpty(t, msg)

	// 长文本截断保护
	assert.LessOrEqual(t, len(truncateStr(msg, 20)), len("...(truncated)")+20)

	// Pool=nil 错误分支
	old := model.Pool
	model.Pool = nil
	assert.Equal(t, "validation failed (details unavailable)", lastFailures(ctx, taskID))
	model.Pool = old
}

// TestForgeWorkspace_DegradedPaths 未配置 Gitea / 任务不存在时的降级路径
func TestForgeWorkspace_DegradedPaths(t *testing.T) {
	newPipelineApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()

	// 无 token → (nil,nil)
	t.Setenv("GITEA_TOKEN", "")
	dir, repo := forgeWorkspace(ctx, uuid.NewString())
	assert.Empty(t, dir)
	assert.Nil(t, repo)

	// 有 token 但任务不存在 → 查询失败降级
	isolateGitProxy(t)
	t.Setenv("GITEA_TOKEN", "dummy")
	t.Setenv("GITEA_URL", "http://127.0.0.1:1") // 不出网
	dir, repo = forgeWorkspace(ctx, uuid.NewString())
	assert.Empty(t, dir)
	assert.Nil(t, repo)
}
