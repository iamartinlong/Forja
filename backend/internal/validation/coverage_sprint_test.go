package validation

// S2 前置补测（v0.6 审计移交项）：A5 覆盖率门禁代码 + 规则加载/宪法词表单测。

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"forja/backend/internal/model"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTrimf(t *testing.T) {
	assert.Equal(t, "80", trimf(80.0))
	assert.Equal(t, "79.5", trimf(79.5))
	assert.Equal(t, "33.3", trimf(100.0/3.0), "保留一位小数并去尾零")
}

func TestCoverageMin_EnvPriority(t *testing.T) {
	t.Setenv("COVERAGE_MIN", "65")
	assert.Equal(t, 65.0, coverageMin())

	t.Setenv("COVERAGE_MIN", "not-a-number")
	assert.Equal(t, 80.0, coverageMin(), "非法 env 应回退默认 80")
}

func TestCoverageFromYaml(t *testing.T) {
	oldPath := "rules/testing.yaml"
	var bak string
	hasOld := false
	if b, err := os.ReadFile(oldPath); err == nil {
		bak, hasOld = string(b), true
	}
	setFile := func(content string) {
		if content == "" {
			_ = os.Remove(oldPath)
			return
		}
		_ = os.MkdirAll(filepath.Dir(oldPath), 0o755)
		require.NoError(t, os.WriteFile(oldPath, []byte(content), 0o644))
	}
	t.Cleanup(func() {
		if hasOld {
			setFile(bak)
		} else {
			_ = os.Remove(oldPath)
		}
	})

	setFile("coverage: { line: 66 }")
	assert.Equal(t, 66.0, coverageFromYaml(), "应从 yaml 解析 line 阈值")

	setFile("")
	assert.Equal(t, 0.0, coverageFromYaml(), "无任何 yaml 应返回 0")

	setFile("coverage: broken {{{")
	assert.Equal(t, 0.0, coverageFromYaml(), "畸形 yaml 应返回 0")
}

func TestParseCoverage(t *testing.T) {
	f := filepath.Join(t.TempDir(), "cover.out")

	assert.Equal(t, -1.0, parseCoverage(f), "文件不存在")

	require.NoError(t, os.WriteFile(f, []byte("mode: set\npkg/x.go:1.2,3.4 1 0\ntotal:\t(statements)\t82.5%\n"), 0o644))
	assert.InDelta(t, 0.825, parseCoverage(f), 0.0001)

	require.NoError(t, os.WriteFile(f, []byte("mode: set\n"), 0o644))
	assert.Equal(t, -1.0, parseCoverage(f), "无 total 行")

	require.NoError(t, os.WriteFile(f, []byte("total:\t(statements)\tabc%\n"), 0o644))
	assert.Equal(t, -1.0, parseCoverage(f), "数字畸形")
}

func TestBSViolation(t *testing.T) {
	assert.Equal(t, "React", BSViolation("前端采用 React 渲染"))
	assert.Equal(t, "pgvector", BSViolation("存储用 pgvector"))
	assert.Empty(t, BSViolation("用户提交登录表单后收到邮件通知"), "纯 WHAT 描述不应违规")
	assert.Empty(t, BSViolation(""))
}

func TestRunPipeline_StaticAndClean(t *testing.T) {
	if model.Pool == nil {
		_ = model.InitDB(context.Background())
	}
	if model.Pool == nil {
		t.Skip("db unavailable")
	}
	ctx := context.Background()
	pid := uuid.New()
	_, err := model.Pool.Exec(ctx, `INSERT INTO projects (id, name, slug) VALUES ($1,$2,$3)`, pid, "val-st", "val-st")
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, pid) })

	tid := uuid.New()
	_, err = model.Pool.Exec(ctx, `INSERT INTO tasks (id, project_id, title, status) VALUES ($1,$2,$3,'PENDING')`, tid, pid, "val")
	require.NoError(t, err)
	t.Cleanup(func() {
		for _, tbl := range []string{"code_changes", "test_results", "test_cases"} {
			_, _ = model.Pool.Exec(ctx, fmt.Sprintf(`DELETE FROM %s WHERE task_id=$1`, tbl), tid)
		}
		_, _ = model.Pool.Exec(ctx, `DELETE FROM tasks WHERE id=$1`, tid)
	})

	mkChange := func(diff string) {
		_, err := model.Pool.Exec(ctx,
			`INSERT INTO code_changes (id, task_id, file_path, diff, change_log) VALUES ($1,$2,'a.go',$3,'log')`,
			uuid.New(), tid, diff)
		require.NoError(t, err)
	}
	clearChanges := func() {
		_, _ = model.Pool.Exec(ctx, `DELETE FROM code_changes WHERE task_id=$1`, tid)
	}

	// fmt.Println → static FAILED
	mkChange("package main\nfunc main() { fmt.Println(\"hi\") }")
	res := RunPipeline(ctx, tid.String(), "", 1, nil, time.Now().Add(-time.Hour))
	assert.False(t, res.Passed, "含 fmt.Println 应静态失败")

	// 硬编码密钥（sk- 且上下文含 api）→ FAILED
	clearChanges()
	mkChange(`key := "sk-abc123456789xyz" // api key`)
	res = RunPipeline(ctx, tid.String(), "", 1, map[string]string{"clean.go": "package main\n"}, time.Now().Add(-time.Hour))
	assert.False(t, res.Passed, "硬编码密钥应失败（且 files 参数不干扰 diff 判定）")

	// 干净变更 + SKIP_VET=1（runGoTest 早退）→ 通过
	clearChanges()
	mkChange("clean change only")
	t.Setenv("SKIP_VET", "1")
	res = RunPipeline(ctx, tid.String(), "", 1, nil, time.Now().Add(-time.Hour))
	assert.True(t, res.Passed, "干净变更+跳过 vet+无测试应通过")
}

// TestRunPipeline_AttemptIsolation 035 轮次隔离：旧轮脏 diff 不得误杀新轮交付；
// files 内容直扫补上整文件交付的静态门禁真空。
func TestRunPipeline_AttemptIsolation(t *testing.T) {
	ctx := context.Background()
	if model.Pool == nil {
		if err := model.InitDB(ctx); err != nil {
			t.Skip("DB not available")
		}
	}
	tid := uuid.NewString()
	_, err := model.Pool.Exec(ctx, `INSERT INTO tasks (id, project_id, title, status) VALUES ($1,$2,'attempt-iso','PENDING')`,
		tid, "00000000-0000-0000-0000-000000000001")
	require.NoError(t, err)
	defer model.Pool.Exec(ctx, `DELETE FROM tasks WHERE id=$1`, tid)
	defer model.Pool.Exec(ctx, `DELETE FROM code_changes WHERE task_id=$1`, tid)
	defer model.Pool.Exec(ctx, `DELETE FROM test_cases WHERE task_id=$1`, tid)

	// 旧轮（attempt 1）残留 fmt.Println 脏 diff
	_, err = model.Pool.Exec(ctx, `INSERT INTO code_changes (id, task_id, file_path, diff, attempt) VALUES ($1,$2,'a.go','fmt.Println("x")',1)`,
		uuid.NewString(), tid)
	require.NoError(t, err)
	// 新轮（attempt 2）无 diff、无 files → 不得被旧轮误杀
	t.Setenv("SKIP_VET", "1")
	res := RunPipeline(ctx, tid, "", 2, nil, time.Now().Add(-time.Hour))
	assert.True(t, res.Passed, "旧轮脏 diff 不得误杀新轮")
	// 新轮 files 内容含 fmt.Println → 必须命中（非 main 包；main 包放行见 TestRunPipeline_MainPackageExempt）
	res = RunPipeline(ctx, tid, "", 2, map[string]string{"pkg/util.go": "package pkg\nfunc f(){ fmt.Println(1) }\n"}, time.Now().Add(-time.Hour))
	assert.False(t, res.Passed, "files 内容 fmt.Println 应静态失败")
	// 跨 run 同 attempt 旧行（attempt 默认值 1 的昨日残留）→ since 窗外不得误杀
	_, err = model.Pool.Exec(ctx, `INSERT INTO code_changes (id, task_id, file_path, diff, attempt, created_at) VALUES ($1,$2,'old.go','fmt.Println("stale")',2, now() - interval '2 days')`,
		uuid.NewString(), tid)
	require.NoError(t, err)
	res = RunPipeline(ctx, tid, "", 2, nil, time.Now().Add(-time.Hour))
	assert.True(t, res.Passed, "跨 run 同 attempt 旧行（since 窗外）不得误杀")
}

// TestRunPipeline_MainPackageExempt 2026-09-07 决策：main 包放行 fmt.Println
// （绿地启动信息打印属合理用法），其他包照拦，密钥检查不限包。
func TestRunPipeline_MainPackageExempt(t *testing.T) {
	ctx := context.Background()
	if model.Pool == nil {
		if err := model.InitDB(ctx); err != nil {
			t.Skip("DB not available")
		}
	}
	// 静态豁免单测只关心门禁判定：外部重门（vet/govulncheck/npm）一律跳过，
	// 否则 CI 冷缓存下 vet 跑验证包自身耗时且结果不可控（run318 实录 83s 失败）。
	t.Setenv("SKIP_VET", "1")
	tid := uuid.NewString()
	_, err := model.Pool.Exec(ctx, `INSERT INTO tasks (id, project_id, title, status) VALUES ($1,$2,'main-exempt','PENDING')`,
		tid, "00000000-0000-0000-0000-000000000001")
	require.NoError(t, err)
	defer model.Pool.Exec(ctx, `DELETE FROM tasks WHERE id=$1`, tid)
	defer model.Pool.Exec(ctx, `DELETE FROM code_changes WHERE task_id=$1`, tid)
	defer model.Pool.Exec(ctx, `DELETE FROM test_cases WHERE task_id=$1`, tid)
	t.Setenv("SKIP_VET", "1")

	// main.go 内容含 fmt.Println → 放行
	res := RunPipeline(ctx, tid, "", 1, map[string]string{
		"main.go": "package main\nimport \"fmt\"\nfunc main(){ fmt.Println(\"listening\") }\n",
	}, time.Now().Add(-time.Hour))
	assert.True(t, res.Passed, "main 包 fmt.Println 应放行")
	// 非 main 包含 fmt.Println → 照拦
	res = RunPipeline(ctx, tid, "", 1, map[string]string{
		"pkg/board/board.go": "package board\nimport \"fmt\"\nfunc Debug(){ fmt.Println(\"x\") }\n",
	}, time.Now().Add(-time.Hour))
	assert.False(t, res.Passed, "非 main 包 fmt.Println 应拦截")
}
