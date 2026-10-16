package gitops

// 整文件交付（绿地）：路径安全 + 直写回归。
import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"strings"
)

// TestApplyDiffs_RecountFallback 2026-09-16 回归：LLM diff 的 @@ hunk 计数不准
// （内容正确）时，标准 git apply 拒绝——放宽级别 --recount 必须兜底成功。
func TestApplyDiffs_RecountFallback(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	dir := t.TempDir()
	gitRun := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, string(out))
	}
	gitRun("init", "-q")
	gitRun("config", "user.email", "t@t")
	gitRun("config", "user.name", "t")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "src"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "src", "app.py"), []byte("line1\nline2\nline3\n"), 0o644))
	gitRun("add", ".")

	// 故意写错 hunk 计数（声明 5 行上下文，实际 3 行）
	badDiff := "--- a/src/app.py\n+++ b/src/app.py\n@@ -1,5 +1,6 @@\n line1\n line2\n+inserted\n line3\n"
	r := &Repo{Dir: dir}
	require.NoError(t, r.ApplyDiffs(ctx, []Change{{FilePath: "src/app.py", Diff: badDiff}}))
	b, err := os.ReadFile(filepath.Join(dir, "src", "app.py"))
	require.NoError(t, err)
	assert.Contains(t, string(b), "inserted")

	// 内容与上下文都不匹配的脏 diff 仍必须失败（放宽不等于放水）
	err = r.ApplyDiffs(ctx, []Change{{FilePath: "src/app.py", Diff: "--- a/src/app.py\n+++ b/src/app.py\n@@ -1,3 +1,4 @@\n nope1\n nope2\n+x\n nope3\n"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "apply diffs failed")
}

func TestSafeJoin(t *testing.T) {
	base := filepath.Join(string(filepath.Separator), "repo")
	ok, err := SafeJoin(base, "pkg/a/a.go")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(ok, filepath.Clean(base)+string(filepath.Separator)), "joined path should resolve under base: %s", ok)

	for _, bad := range []string{"", "/abs/a.go", "../escape.go", "a/../../escape.go", "."} {
		_, err := SafeJoin(base, bad)
		assert.Error(t, err, "path %q must be rejected", bad)
	}
}

func TestWriteFiles(t *testing.T) {
	dir := t.TempDir()
	r := &Repo{Dir: dir}
	err := r.WriteFiles(context.Background(), []FileContent{
		{FilePath: "pkg/board/board.go", Content: "package board\n"},
		{FilePath: "main.go", Content: "package main\n"},
		{FilePath: "empty.go", Content: "  "}, // 空内容跳过
		{FilePath: "../escape.go", Content: "evil"},
	})
	require.Error(t, err, "escape path must fail")
	assert.Contains(t, err.Error(), "escape.go")

	b, err := os.ReadFile(filepath.Join(dir, "pkg", "board", "board.go"))
	require.NoError(t, err)
	assert.Equal(t, "package board\n", string(b))

	b, err = os.ReadFile(filepath.Join(dir, "main.go"))
	require.NoError(t, err)
	assert.Equal(t, "package main\n", string(b))

	_, err = os.Stat(filepath.Join(dir, "empty.go"))
	assert.True(t, os.IsNotExist(err))
}

// TestMergeBranchTo_NewRemoteBranch 2026-09-07 回归：`fetch origin src`（无 refspec）
// 只写 FETCH_HEAD，不建 origin/src 跟踪分支——merge 必须用本地 src，
// 否则 "not something we can merge"（首个验证通过的 forge 轮在合 master 时挂）。
func TestMergeBranchTo_NewRemoteBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	remote := t.TempDir()
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
			"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}
	run(remote, "init", "--bare", "-b", "master", ".")
	work := t.TempDir()
	// 与生产一致用浅克隆（EnsureRepo --depth 50）：跟踪分支行为与全克隆不同
	run(work, "clone", "--depth", "50", remote, ".")
	// 剥离 git 身份（模拟 worker 容器）：merge 提交必须靠 MergeBranchTo 内联 -c，
	// 不能依赖全局/仓内身份（2026-09-08 Committer identity unknown 实录）。
	// run helper 的建仓提交走 GIT_AUTHOR/COMMITTER 环境，不受影响。
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	run(work, "checkout", "-b", "master")
	require.NoError(t, os.WriteFile(filepath.Join(work, "a.txt"), []byte("a\n"), 0o644))
	run(work, "add", "-A")
	run(work, "commit", "-m", "init")
	run(work, "push", "origin", "master")
	// 模拟 forge：切特性分支提交并推送（远端新分支，本地 clone 从未通配 fetch 过它）
	run(work, "checkout", "-b", "forge/task-x-a1")
	require.NoError(t, os.WriteFile(filepath.Join(work, "b.txt"), []byte("b\n"), 0o644))
	r := &Repo{Dir: work}
	_, err := r.CommitAndPushBranch(ctx, "forge", "forge/task-x-a1")
	require.NoError(t, err)
	sha, err := r.MergeBranchTo(ctx, "forge/task-x-a1", "master")
	require.NoError(t, err, "merge 新远端分支必须成功")
	assert.NotEmpty(t, sha)
	b, err := os.ReadFile(filepath.Join(work, "b.txt"))
	require.NoError(t, err)
	assert.Equal(t, "b", strings.TrimSpace(string(b)))
}

// TestCommitAndPushBranch_ForcePushScratch 2026-09-07 回归：forge/* scratch 分支
// 每轮从 base 重置，任务重跑推同名分支时普通 push 必 non-fast-forward 被拒——
// 非 master 分支必须 --force（master 永不用 force）。
func TestCommitAndPushBranch_ForcePushScratch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	remote := t.TempDir()
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
			"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}
	run(remote, "init", "--bare", "-b", "master", ".")
	work := t.TempDir()
	run(work, "clone", "--depth", "50", remote, ".")
	run(work, "config", "user.email", "t@t")
	run(work, "config", "user.name", "t")
	run(work, "checkout", "-b", "master")
	require.NoError(t, os.WriteFile(filepath.Join(work, "a.txt"), []byte("a\n"), 0o644))
	run(work, "add", "-A")
	run(work, "commit", "-m", "init")
	run(work, "push", "origin", "master")

	r := &Repo{Dir: work}
	// 首轮：新分支推送
	run(work, "checkout", "-b", "forge/task-r-a1")
	require.NoError(t, os.WriteFile(filepath.Join(work, "b.txt"), []byte("v1\n"), 0o644))
	_, err := r.CommitAndPushBranch(ctx, "run1", "forge/task-r-a1")
	require.NoError(t, err)
	// 重跑：分支从 base 重置改写历史后再推同名分支（普通 push 在此必拒）
	run(work, "checkout", "-B", "forge/task-r-a1", "master")
	require.NoError(t, os.WriteFile(filepath.Join(work, "b.txt"), []byte("v2\n"), 0o644))
	_, err = r.CommitAndPushBranch(ctx, "run2", "forge/task-r-a1")
	require.NoError(t, err, "重跑同名 scratch 分支必须 --force 推上")
	// 远端内容应为 v2
	check := t.TempDir()
	run(check, "clone", "--depth", "50", "--branch", "forge/task-r-a1", remote, ".")
	b, err := os.ReadFile(filepath.Join(check, "b.txt"))
	require.NoError(t, err)
	assert.Equal(t, "v2", strings.TrimSpace(string(b)))
}
