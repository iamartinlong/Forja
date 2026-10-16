// Package gitops 提供 Forge 产物落库所需的最小 git 操作集（S3.5.1 串行 apply）。
// 全部经 exec git CLI 实现（go-git 不支持 apply）；凭据仅内嵌于 remote URL，不落日志。
package gitops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Change 与 code_changes 表行对齐（file_path + 统一 diff 文本）。
type Change struct {
	FilePath string
	Diff     string
}

// ErrApplyFailed 任一补丁应用失败（携带文件级上下文，供 Agent 重试喂回）。
var ErrApplyFailed = errors.New("gitops: apply diffs failed")

// Repo 返回一个已就绪的工作副本句柄。
type Repo struct {
	Dir      string // 工作副本绝对路径
	FullName string // owner/name
	token    string
	baseURL  string
}

// EnsureRepo 克隆或复用工作副本，并同步到远端 master 最新。
// workdir 为工作区根目录；副本位于 <workdir>/<owner>_<name>。
func EnsureRepo(ctx context.Context, workdir, baseURL, token, fullName string) (*Repo, error) {
	return EnsureRepoWithBranch(ctx, workdir, baseURL, token, fullName, "")
}

// EnsureRepoWithBranch 在 EnsureRepo 基础上支持锻造分支隔离（S4-0）：
// branch 为空或 master 时等同 EnsureRepo；否则在 master 最新上建/切到 branch。
func EnsureRepoWithBranch(ctx context.Context, workdir, baseURL, token, fullName, branch string) (*Repo, error) {
	return EnsureRepoWithBranchOnBase(ctx, workdir, baseURL, token, fullName, branch, "master")
}

// EnsureRepoOnBase 同 EnsureRepo，但集成分支由调用方指定（项目 default_branch，F0.5）。
func EnsureRepoOnBase(ctx context.Context, workdir, baseURL, token, fullName, baseBranch string) (*Repo, error) {
	if baseBranch == "" {
		baseBranch = "master"
	}
	return EnsureRepoWithBranchOnBase(ctx, workdir, baseURL, token, fullName, baseBranch, baseBranch)
}

// EnsureRepoWithBranchOnBase 支持指定基分支（iteration 分支化 027），避免 reset 污染已合 iteration。
func EnsureRepoWithBranchOnBase(ctx context.Context, workdir, baseURL, token, fullName, branch, baseBranch string) (*Repo, error) {
	if baseBranch == "" {
		baseBranch = "master"
	}
	if fullName == "" || !strings.Contains(fullName, "/") {
		return nil, fmt.Errorf("gitops: invalid repo full name %q", fullName)
	}
	r := &Repo{
		FullName: fullName,
		token:    token,
		baseURL:  strings.TrimSuffix(baseURL, "/"),
		Dir:      filepath.Join(workdir, strings.ReplaceAll(fullName, "/", "_")),
	}
	if _, err := os.Stat(filepath.Join(r.Dir, ".git")); err != nil {
		if err := os.MkdirAll(workdir, 0o755); err != nil {
			return nil, err
		}
		if err := r.git(ctx, "", "clone", "--depth", "50", r.authedURL(), r.Dir); err != nil {
			return nil, err
		}
	}
	// 同步基分支
	if err := r.git(ctx, r.Dir, "fetch", "origin", baseBranch); err != nil {
		return nil, err
	}
	if branch == "" || branch == baseBranch {
		if err := r.git(ctx, r.Dir, "checkout", baseBranch); err != nil {
			_ = r.git(ctx, r.Dir, "checkout", "-B", baseBranch, "origin/"+baseBranch)
		}
		if err := r.git(ctx, r.Dir, "reset", "--hard", "origin/"+baseBranch); err != nil {
			return nil, err
		}
		if err := r.git(ctx, r.Dir, "clean", "-fd"); err != nil {
			return nil, err
		}
		return r, nil
	}
	// 复用副本：拉齐基分支并清理
	if err := r.git(ctx, r.Dir, "reset", "--hard", "origin/"+baseBranch); err != nil {
		return nil, err
	}
	if err := r.git(ctx, r.Dir, "clean", "-fd"); err != nil {
		return nil, err
	}
	// 目标分支：若远端已存在则跟踪，否则基于 baseBranch 新建，避免 reset --hard master 污染 iteration 已合
	_ = r.git(ctx, r.Dir, "fetch", "origin", branch)
	if err := r.git(ctx, r.Dir, "checkout", "-B", branch, "origin/"+branch); err != nil {
		if err2 := r.git(ctx, r.Dir, "checkout", "-B", branch, "origin/"+baseBranch); err2 != nil {
			// 远端无 branch 也无 baseBranch 时本地新建
			if err3 := r.git(ctx, r.Dir, "checkout", "-b", branch); err3 != nil {
				return nil, err3
			}
		}
	}
	// 已跟踪远端分支不强制 reset 到 baseBranch，保留 iteration 已合并历史
	return r, nil
}

// EnsureIterationBranch 确保迭代分支存在（基于项目集成分支切出并推送，幂等 409 复用）
func EnsureIterationBranch(ctx context.Context, workdir, baseURL, token, fullName, iterBranch, baseBranch string) (*Repo, error) {
	r, err := EnsureRepoWithBranchOnBase(ctx, workdir, baseURL, token, fullName, iterBranch, baseBranch)
	if err != nil {
		return nil, err
	}
	// 推送迭代分支（若已存在远端则幂等）
	_ = r.git(ctx, r.Dir, "push", "-u", "origin", iterBranch)
	return r, nil
}

// ApplyDiffs 应用补丁：先对全部补丁做 --check 预检（all-or-nothing），
// 预检全过再实际应用——避免第 N 个文件失败时前 N-1 个已半应用污染工作副本。
func (r *Repo) ApplyDiffs(ctx context.Context, changes []Change) error {
	var patches []string
	defer func() {
		for _, p := range patches {
			_ = os.Remove(p)
		}
	}()
	type prepared struct {
		patch string
		mode  []string
	}
	var ready []prepared
	var failed []string
	for _, ch := range changes {
		if strings.TrimSpace(ch.Diff) == "" {
			continue
		}
		patch := filepath.Join(os.TempDir(), "forja-"+strings.ReplaceAll(ch.FilePath, "/", "_")+".patch")
		if err := os.WriteFile(patch, []byte(ch.Diff), 0o644); err != nil {
			return err
		}
		patches = append(patches, patch)
		// 预检：不落盘校验，corrupt/上下文不匹配在此暴露；LLM diff 的高频缺陷是
		// @@ hunk 计数不准（内容正确仍被标准 apply 拒绝），用 --recount 放宽级别预检
		// （2026-09-16 python 模板实录：三轮均因计数错误烧空）。
		mode, err := r.applyCheck(ctx, patch)
		if err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", ch.FilePath, err))
			break // 首个失败即停：修复循环携带失败上下文整体重建
		}
		ready = append(ready, prepared{patch: patch, mode: mode})
	}
	if len(failed) > 0 {
		return fmt.Errorf("%w: %s", ErrApplyFailed, strings.Join(failed, "; "))
	}
	for _, p := range ready {
		args := append(append([]string{}, p.mode...), p.patch)
		if err := r.git(ctx, r.Dir, args...); err != nil {
			// 预检已过仍失败（竞态极小）：保持旧语义中止并报告
			return fmt.Errorf("%w: %s: %v", ErrApplyFailed, p.patch, err)
		}
	}
	return nil
}

// applyModes git apply 放宽级别（依次尝试）：
//  1. 标准（历史路径）
//  2. --recount：按实际内容重算 hunk 计数（LLM 生成 diff 的高频缺陷）
//  3. + --ignore-whitespace：上下文仅空白差异
func applyModes() [][]string {
	return [][]string{
		{"apply", "--whitespace=nowarn"},
		{"apply", "--recount", "--whitespace=nowarn"},
		{"apply", "--recount", "--ignore-whitespace", "--whitespace=nowarn"},
	}
}

// applyCheck 以首个通过的放宽级别做 --check，返回该级别供实际应用复用。
func (r *Repo) applyCheck(ctx context.Context, patch string) ([]string, error) {
	var last error
	for _, mode := range applyModes() {
		args := append(append([]string{}, mode...), "--check", patch)
		if err := r.git(ctx, r.Dir, args...); err != nil {
			last = err
			continue
		}
		return mode, nil
	}
	return nil, last
}

// FileTree 返回工作副本现存文件清单（git ls-files），供 Agent 接地：
// 没有真实清单时模型会对不存在的文件编造"修改型 diff"，git apply 必然失败
// （2026-09-05 审计实录：空脚手架仓上 agent 幻觉出 backend/internal/... 修改补丁）。
func (r *Repo) FileTree(ctx context.Context) ([]string, error) {
	out, err := r.gitOut(ctx, r.Dir, "ls-files")
	if err != nil {
		return nil, err
	}
	files := strings.Split(strings.TrimSpace(out), "\n")
	paths := files[:0]
	for _, f := range files {
		if f = strings.TrimSpace(f); f != "" {
			paths = append(paths, f)
		}
	}
	return paths, nil
}

// FileContent 整文件交付（绿地首选）：file_path 相对仓库根，content 为完整文件内容。
type FileContent struct {
	FilePath string
	Content  string
}

// FileContents 读取工作区文本文件内容（受上限约束，供 forge 提示词接地）。
// 跳过 .git/vendor/node_modules、二进制（含 NUL）与超大文件；单文件按 maxFileBytes 截断。
func (r *Repo) FileContents(ctx context.Context, maxFiles, maxFileBytes, maxTotal int) ([]FileContent, error) {
	paths, err := r.FileTree(ctx)
	if err != nil {
		return nil, err
	}
	var out []FileContent
	total := 0
	for _, p := range paths {
		if len(out) >= maxFiles || total >= maxTotal {
			break
		}
		if strings.HasPrefix(p, ".git/") || strings.Contains(p, "node_modules/") ||
			strings.HasPrefix(p, "vendor/") || strings.HasPrefix(p, "dist/") {
			continue
		}
		abs, jerr := SafeJoin(r.Dir, p)
		if jerr != nil {
			continue
		}
		b, rerr := os.ReadFile(abs)
		if rerr != nil || len(b) == 0 {
			continue
		}
		if len(b) > maxFileBytes {
			b = b[:maxFileBytes]
		}
		if strings.ContainsRune(string(b), 0) {
			continue // 二进制
		}
		total += len(b)
		out = append(out, FileContent{FilePath: p, Content: string(b)})
	}
	return out, nil
}

// SafeJoin 校验相对路径并拼接到仓库根：拒绝绝对路径、.. 穿越与指向根自身的路径。
// 判定用可移植写法（Windows 宿主下 /x 与 C:x 都不是 filepath.IsAbs 的绝对路径）。
func SafeJoin(base, p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", fmt.Errorf("empty file path")
	}
	drive := len(p) >= 2 && p[1] == ':'
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, "\\") || drive || strings.Contains(p, "..") {
		return "", fmt.Errorf("unsafe path %q", p)
	}
	cleanBase := filepath.Clean(base)
	full := filepath.Clean(filepath.Join(base, p))
	if full == cleanBase || !strings.HasPrefix(full, cleanBase+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes repo: %q", p)
	}
	return full, nil
}

// WriteFiles 整文件直写（绿地交付）：file_path 相对仓库根，自动建父目录。
// agent 输出整文件内容比手搓 unified diff 稳健（corrupt patch 是模型主要失败模式）。
func (r *Repo) WriteFiles(ctx context.Context, files []FileContent) error {
	var failed []string
	for _, f := range files {
		if strings.TrimSpace(f.Content) == "" {
			continue
		}
		full, err := SafeJoin(r.Dir, f.FilePath)
		if err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", f.FilePath, err))
			break
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", f.FilePath, err))
			break
		}
		if err := os.WriteFile(full, []byte(f.Content), 0o644); err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", f.FilePath, err))
			break
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("%w: %s", ErrApplyFailed, strings.Join(failed, "; "))
	}
	return nil
}

// CommitAndPush 提交全部变更并推送 master，返回新 commit sha。
func (r *Repo) CommitAndPush(ctx context.Context, message string) (string, error) {
	return r.CommitAndPushBranch(ctx, message, "master")
}

// CommitAndPushBranch 提交并推送指定分支（S4-0），返回新 sha。
func (r *Repo) CommitAndPushBranch(ctx context.Context, message, branch string) (string, error) {
	if branch == "" {
		branch = "master"
	}
	if err := r.git(ctx, r.Dir, "add", "-A"); err != nil {
		return "", err
	}
	if err := r.git(ctx, r.Dir, "-c", "user.email=forge@forja.local", "-c", "user.name=FORJA Forge",
		"commit", "-m", message); err != nil {
		if !strings.Contains(err.Error(), "nothing to commit") {
			return "", err
		}
	}
	// 非 master 分支用 --force：forge/* 是按轮从 base 重置的 scratch 分支，
	// 重跑任务会改写同名分支历史，普通 push 必 non-fast-forward 被拒
	// （2026-09-07 留言板重跑 attempt-1 验证通过却推不上去实录）。
	// master 永远不用 force（签核发布的人工安全线）。
	pushArgs := []string{"push", "origin", branch}
	if branch != "master" {
		pushArgs = []string{"push", "--force", "origin", branch}
	}
	if err := r.git(ctx, r.Dir, pushArgs...); err != nil {
		return "", err
	}
	out, err := exec.CommandContext(ctx, "git", "-C", r.Dir, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// MergeBranch 将 branch 合入 master 并推送（S4-0 签核 Release），返回 merge commit sha。
func (r *Repo) MergeBranch(ctx context.Context, branch string) (string, error) {
	return r.MergeBranchTo(ctx, branch, "master")
}

// MergeBranchTo 将 src 合入 dst 并推送 dst，返回 merge commit sha（迭代分支化参数化）。
func (r *Repo) MergeBranchTo(ctx context.Context, src, dst string) (string, error) {
	if src == "" || src == dst {
		return "", fmt.Errorf("gitops: invalid merge src %q dst %q", src, dst)
	}
	if err := r.git(ctx, r.Dir, "fetch", "origin", src); err != nil {
		return "", err
	}
	// 立即固化 src 顶点：后续 fetch dst 会覆盖 FETCH_HEAD；且新克隆（clone --depth
	// 隐含 single-branch）无本地 src 分支，直接 merge src 报 "not something we can
	// merge"（2026-09-14 release 回退合并实录）。按 sha 合并对两处调用都正确。
	tipOut, err := exec.CommandContext(ctx, "git", "-C", r.Dir, "rev-parse", "FETCH_HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("gitops: rev-parse FETCH_HEAD: %w", err)
	}
	srcTip := strings.TrimSpace(string(tipOut))
	if srcTip == "" {
		return "", fmt.Errorf("gitops: empty FETCH_HEAD for %q", src)
	}
	if err := r.git(ctx, r.Dir, "fetch", "origin", dst); err != nil {
		// dst 远端不存在时忽略（新迭代分支刚切出）
	}
	if err := r.git(ctx, r.Dir, "checkout", dst); err != nil {
		_ = r.git(ctx, r.Dir, "checkout", "-B", dst, "origin/"+dst)
	}
	if err := r.git(ctx, r.Dir, "reset", "--hard", "origin/"+dst); err != nil {
		// dst 为新分支时 origin/dst 不存在，忽略
	}
	// merge 提交须带 -c 身份：worker 容器无全局 git 身份，commit 靠内联 -c，
	// merge 忘带会 Committer identity unknown（2026-09-08 留言板 attempt-3 实录）。
	if err := r.git(ctx, r.Dir, "-c", "user.email=forge@forja.local", "-c", "user.name=FORJA Forge",
		"merge", "--no-ff", srcTip, "-m", "merge "+src+" into "+dst); err != nil {
		return "", err
	}
	if err := r.git(ctx, r.Dir, "push", "origin", dst); err != nil {
		return "", err
	}
	out, err := exec.CommandContext(ctx, "git", "-C", r.Dir, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// RevertCommit 回退指定 commit（S4-0 回滚），推送 master。
func (r *Repo) RevertCommit(ctx context.Context, sha string) (string, error) {
	return r.RevertCommitTo(ctx, sha, "master")
}

// RevertCommitTo 回退指定 commit 到 dst 分支并推送 dst。
func (r *Repo) RevertCommitTo(ctx context.Context, sha, dst string) (string, error) {
	if dst == "" {
		dst = "master"
	}
	if err := r.git(ctx, r.Dir, "fetch", "origin", dst); err != nil {
		return "", err
	}
	if err := r.git(ctx, r.Dir, "checkout", dst); err != nil {
		return "", err
	}
	if err := r.git(ctx, r.Dir, "reset", "--hard", "origin/"+dst); err != nil {
		return "", err
	}
	if err := r.git(ctx, r.Dir, "revert", "--no-edit", sha); err != nil {
		return "", err
	}
	if err := r.git(ctx, r.Dir, "push", "origin", dst); err != nil {
		return "", err
	}
	out, err := exec.CommandContext(ctx, "git", "-C", r.Dir, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// CreateTag 打标签并推送（S4-0 release）。
func (r *Repo) CreateTag(ctx context.Context, tag, sha string) (string, error) {
	if err := r.git(ctx, r.Dir, "tag", tag, sha); err != nil {
		return "", err
	}
	if err := r.git(ctx, r.Dir, "push", "origin", tag); err != nil {
		return "", err
	}
	return tag, nil
}

func (r *Repo) authedURL() string {
	// baseURL 形如 http://gitea:3000 → http://user:token@gitea:3000
	return strings.Replace(r.baseURL, "://", "://forja-admin:"+r.token+"@", 1) + "/" + r.FullName + ".git"
}

func (r *Repo) git(ctx context.Context, dir string, args ...string) error {
	_, err := r.gitOut(ctx, dir, args...)
	return err
}

func (r *Repo) gitOut(ctx context.Context, dir string, args ...string) (string, error) {
	full := append([]string{"-C", dir}, args...)
	if dir == "" {
		full = args
	}
	cmd := exec.CommandContext(ctx, "git", full...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// 不回显 authed URL：只报参数与截断输出
		return string(out), fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, truncate(string(out), 400))
	}
	return string(out), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}
