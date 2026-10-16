package validation

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"forja/backend/internal/model"
	"forja/backend/internal/stackkind"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type PipelineResult struct {
	Passed  bool
	Attempt int
}

// RunPipeline 对 dir（锻造产物工作副本）执行静态扫描+vet+test；dir 为空时退回旧探测
// （兼容单测/历史调用），S3.5.1 起生产路径必须传产物目录。
// attempt 轮次隔离（035）：静态门禁只扫本轮 code_changes + 本轮 files 内容——
// 每轮分支从 base 重置，历史 diff 对当前工作区无意义，扫全量会把旧轮残留
// （如昨日 fmt.Println）误判到新轮交付上（2026-09-07 留言板 3 轮烧空实录）。
// since 本轮 forge 启动时刻：attempt 编号在每次重跑后从 1 复用，且 035 之前
// 的历史行 attempt 全为默认值 1——仅凭 attempt 仍会跨 run 误杀，必须再按
// created_at >= since 限定到本次 run（worker 与 pg 同宿主时钟）。
func RunPipeline(ctx context.Context, taskID, dir string, attempt int, files map[string]string, since time.Time) PipelineResult {
	// gate already handles contracts; pipeline handles static+unit
	if model.Pool == nil {
		return PipelineResult{Passed: true, Attempt: 1}
	}
	// 1. static forbidden check
	rows, err := model.Pool.Query(ctx, `SELECT file_path, diff FROM code_changes WHERE task_id=$1 AND attempt=$2 AND created_at >= $3`, taskID, attempt, since)
	if err != nil {
		return PipelineResult{Passed: true, Attempt: 1}
	}
	defer rows.Close()
	hasChanges := false
	for rows.Next() {
		var path, diff string
		_ = rows.Scan(&path, &diff)
		hasChanges = true
		lower := strings.ToLower(diff)
		// main 包放行 fmt.Println（2026-09-07 决策）：绿地 main.go 打印启动信息
		// 属合理用法，禁令本意只拦调试残留；硬编码密钥检查不限包。
		if !isMainPackage(path, diff) && strings.Contains(lower, "fmt.println") {
			recordTest(ctx, taskID, "static", "FAILED", "forbidden fmt.Println")
			return PipelineResult{Passed: false, Attempt: 1}
		}
		if strings.Contains(diff, "sk-") && strings.Contains(lower, "api") {
			recordTest(ctx, taskID, "static", "FAILED", "hardcoded secret")
			return PipelineResult{Passed: false, Attempt: 1}
		}
	}
	rows.Close()
	// 整文件交付无 diff：内容即交付物，同规则直扫（否则 files 模式静态门禁真空）。
	for path, content := range files {
		hasChanges = true
		lower := strings.ToLower(content)
		if !isMainPackage(path, content) && strings.Contains(lower, "fmt.println") {
			recordTest(ctx, taskID, "static", "FAILED", "forbidden fmt.Println in "+path)
			return PipelineResult{Passed: false, Attempt: 1}
		}
		if strings.Contains(content, "sk-") && strings.Contains(lower, "api") {
			recordTest(ctx, taskID, "static", "FAILED", "hardcoded secret in "+path)
			return PipelineResult{Passed: false, Attempt: 1}
		}
	}
	// 2. go vet (if go binary available)
	// DB 隔离（2026-09-06 事故）：workspace 测试此前继承 worker 的产库 DATABASE_URL，
	// agent 生成的迁移/测试会直接对产库执行——改为每任务一次性 scratch 库，跑完即删。
	// scratch 创建失败时 fail-closed（宁可校验失败也不污染产库）。
	// v2.1 多栈分派：工作区 project.yaml 声明优先；缺失/非法回退 go（历史仓兼容）
	manifest, hasManifest := resolveStack(dir)
	lang := stackLang(manifest, hasManifest)
	kind, _ := stackkind.For(lang)

	var dbEnv []string
	var dropScratch func()
	if hasChanges && needsDB(manifest, hasManifest, lang) && hasGo() {
		sURL, cleanup, serr := scratchDatabase(ctx, taskID)
		if serr != nil {
			recordTest(ctx, taskID, "unit", "FAILED", "scratch db unavailable (fail-closed): "+serr.Error())
			return PipelineResult{Passed: false, Attempt: 1}
		}
		dbEnv = append(os.Environ(), "DATABASE_URL="+sURL)
		dropScratch = cleanup
		defer dropScratch()
	}
	if lang == "go" && hasGo() {
		// 1b. go mod tidy（2026-09-08）：agent 只出 go.mod 常缺 go.sum 条目，
		// vet/test 遇到外部依赖直接判死。tidy 自动补齐（需 GOMODCACHE 可写，
		// 见 compose worker 环境；网络抖动则如实失败进修复循环）。
		if ok, out := runGoTidy(ctx, dir, dbEnv); !ok {
			recordTest(ctx, taskID, "tidy", "FAILED", out)
			return PipelineResult{Passed: false, Attempt: 1}
		}
		if ok, out := runGoVet(ctx, dir, dbEnv); !ok {
			recordTest(ctx, taskID, "vet", "FAILED", out)
			return PipelineResult{Passed: false, Attempt: 1}
		}
	}
	// 2b. P0 轻量供应链门禁（Q-1）：govulncheck + npm audit 高危 — fail 进入修复循环（`forge_attempt`）
	// SKIP_VET=1 时一并跳过：该 env 在单测中表示"只验静态判定"，外部重门（网络依赖、
	// 冷缓存耗时）会让单测不可控（run318 实录）。生产路径从不设置此 env。
	if hasChanges && os.Getenv("SKIP_VET") != "1" {
		if lang == "go" && hasGo() {
			if ok, out := runGovulncheck(ctx, dir); !ok {
				recordTest(ctx, taskID, "govulncheck", "FAILED", out)
				return PipelineResult{Passed: false, Attempt: 1}
			}
			if ok, out := runNpmAudit(ctx, dir); !ok {
				recordTest(ctx, taskID, "npm_audit", "FAILED", out)
				return PipelineResult{Passed: false, Attempt: 1}
			}
		}
		if lang == "node" && hasNode() && kind.RequiresLock && hasLockFile(dir) {
			if ok, out := runShellCommand(ctx, dir, kind.VulnCmd, 120*time.Second); !ok {
				recordTest(ctx, taskID, "npm_audit", "FAILED", out)
				return PipelineResult{Passed: false, Attempt: 1}
			}
		}
		// v2.2 T1：python 栈漏洞扫描窗口（VulnCmd 非空才启用；当前 pip-audit 未烘焙 → 跳过）
		if lang == "python" && kind.VulnCmd != "" && hasPython() && hasDepsFile(dir, lang) {
			if ok, out := runShellCommand(ctx, dir, kind.VulnCmd, 120*time.Second); !ok {
				recordTest(ctx, taskID, "deps_audit", "FAILED", out)
				return PipelineResult{Passed: false, Attempt: 1}
			}
		}
	}
	// 3. 单元测试 + 覆盖率门禁（v2.1 多栈：go 走既有路径；node 走契约命令）
	if hasChanges {
		switch lang {
		case "go":
			if hasGo() {
				ok, out, coverage := runGoTest(ctx, dir, dbEnv)
				if !ok {
					recordTest(ctx, taskID, "unit", "FAILED", out)
					return PipelineResult{Passed: false, Attempt: 1}
				}
				recordTest(ctx, taskID, "unit", "PASSED", "go test passed")
				// 4. 覆盖率门禁（A5）：低于阈值 → FAILED → 进修复循环
				if coverage >= 0 {
					min := coverageMin()
					pct := coverage * 100
					if pct < min {
						recordTest(ctx, taskID, "coverage", "FAILED", strings.TrimSpace(strings.Join([]string{"line", trimf(pct), "<", trimf(min)}, " "))+"%")
						return PipelineResult{Passed: false, Attempt: 1}
					}
					recordTest(ctx, taskID, "coverage", "PASSED", "line "+trimf(pct)+"% >= "+trimf(min)+"%")
				} else if os.Getenv("SKIP_VET") != "1" {
					// M5+ 门禁收紧（2026-09-14 guestbook 实录）：仓库有 Go 源码但 0 个 _test.go 时，
					// "no test files" 不再静默放行——AC 要求补单测的任务必须真正落地测试文件。
					if src, tests := countGoFiles(resolveWorkDir(dir)); src > 0 && tests == 0 {
						recordTest(ctx, taskID, "coverage", "FAILED", "no *_test.go in repo while Go sources present")
						return PipelineResult{Passed: false, Attempt: 1}
					}
				}
			}
		case "node", "python":
			if !hasToolchain(lang) {
				recordTest(ctx, taskID, "unit", "FAILED", lang+" toolchain unavailable in worker")
				return PipelineResult{Passed: false, Attempt: 1}
			}
			// 依赖安装（node: package-lock.json / python: requirements*.txt；零依赖模板直接跳过）
			if kind.RequiresLock && hasDepsFile(dir, lang) {
				if ok, out := runShellCommand(ctx, dir, kind.TidyCmd, 300*time.Second); !ok {
					recordTest(ctx, taskID, "tidy", "FAILED", out)
					return PipelineResult{Passed: false, Attempt: 1}
				}
			}
			unit := kind.UnitDefault
			if hasManifest && strings.TrimSpace(manifest.Test.Unit) != "" {
				unit = manifest.Test.Unit
			}
			if ok, out := runShellCommand(ctx, dir, unit, 300*time.Second); !ok {
				recordTest(ctx, taskID, "unit", "FAILED", out)
				return PipelineResult{Passed: false, Attempt: 1}
			}
			recordTest(ctx, taskID, "unit", "PASSED", "command passed")
			// 覆盖率：声明格式优先，无数据跳过（不阻断）
			format := kind.CoverageFormat
			if hasManifest && manifest.Test.Coverage.Format != "" {
				format = manifest.Test.Coverage.Format
			}
			if format == "lcov" {
				file := ""
				if hasManifest {
					file = manifest.Test.Coverage.File
				}
				if cov := parseLcov(resolveCoveragePath(dir, file)); cov >= 0 {
					min := coverageMin()
					pct := cov * 100
					if pct < min {
						recordTest(ctx, taskID, "coverage", "FAILED", "line "+trimf(pct)+"% < "+trimf(min)+"%")
						return PipelineResult{Passed: false, Attempt: 1}
					}
					recordTest(ctx, taskID, "coverage", "PASSED", "line "+trimf(pct)+"% >= "+trimf(min)+"%")
				}
			}
		}
	}
	if !hasChanges {
		return PipelineResult{Passed: true, Attempt: 1}
	}
	return PipelineResult{Passed: true, Attempt: 1}
}

func hasGo() bool {
	_, err := exec.LookPath("go")
	return err == nil
}

// isMainPackage 启发式判定 main 包文件（fmt.Println 禁令放行用）：
// 路径惯例 main.go，或文本中出现 package main 子句行（含 diff 的 +/-/空格前缀）。
// 注释/字符串里提及 package main 会误放行——禁令本就是启发式，宽松方向可接受。
func isMainPackage(path, text string) bool {
	if strings.HasSuffix(path, "/main.go") || path == "main.go" {
		return true
	}
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "+"))
		if strings.HasPrefix(t, "package main") {
			if rest := strings.TrimPrefix(t, "package main"); rest == "" || rest[0] == ' ' || rest[0] == '\t' {
				return true
			}
		}
	}
	return false
}

// scratchDatabase 为锻造校验创建一次性隔离库（DROP→CREATE），返回其 DATABASE_URL 与删除函数。
// 背景（2026-09-06 事故）：workspace 测试进程继承 worker 的产库 DATABASE_URL，agent 生成的
// 迁移/测试会直接对产库执行——幻觉迁移 035 曾因此打产库。隔离库与产库同实例（forja 用户
// 即实例超管，有 CREATEDB 权限），命名按 taskID 前缀便于排障。
func scratchDatabase(ctx context.Context, taskID string) (string, func(), error) {
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		// 与 model.InitDB 的本地默认一致（测试进程通常不显式设置该 env）
		base = "postgres://forja:forja@localhost:5432/forja?sslmode=disable"
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", func() {}, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	// 维护库连 postgres；库名只留字母数字（taskID 是 UUID/ULID，去连字符取前 16 位）
	u.Path = "/postgres"
	adminURL := u.String()
	safe := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, taskID)
	if len(safe) > 16 {
		safe = safe[:16]
	}
	name := "forja_val_" + strings.ToLower(safe)
	conn, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return "", func() {}, fmt.Errorf("connect maintenance db: %w", err)
	}
	defer conn.Close(ctx)
	// 残留清理 + 建库（标识符不可参数化，safe 已过滤为字母数字）
	drop := func() {
		dctx, dcancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer dcancel()
		dc, derr := pgx.Connect(dctx, adminURL)
		if derr != nil {
			return
		}
		defer dc.Close(dctx)
		_, _ = dc.Exec(dctx, fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, name))
	}
	if _, err := conn.Exec(ctx, fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, name)); err != nil {
		return "", func() {}, fmt.Errorf("drop stale scratch: %w", err)
	}
	if _, err := conn.Exec(ctx, `CREATE DATABASE `+name+` TEMPLATE template0`); err != nil {
		return "", func() {}, fmt.Errorf("create scratch: %w", err)
	}
	su, serr := url.Parse(base)
	if serr != nil {
		drop()
		return "", func() {}, fmt.Errorf("re-parse DATABASE_URL: %w", serr)
	}
	su.Path = "/" + name
	return su.String(), drop, nil
}

// runGoTidy 补齐模块依赖（agent 常只出 go.mod 而缺 go.sum 条目）：
// 有 go.mod 才跑；无则跳过（vet 会给出明确的缺模块错误）。
// 注意：会改写工作区 go.mod/go.sum——这是期望行为（机器自动修依赖，
// agent 不必手搓 sum）。SKIP_VET=1 时一并跳过（单测/轻量路径）。
// dir 为空时一律跳过：空 dir 是单测兼容路径，此时相对 CWD 的 go.mod
// 极可能是 FORJA backend 自身模块——tidy 它会污染仓库 + 拖慢单测
// （2026-09-08 run299 后复盘：CI 后端 job 行为异常）。
func runGoTidy(ctx context.Context, dir string, extraEnv []string) (bool, string) {
	if os.Getenv("SKIP_VET") == "1" {
		return true, "skipped"
	}
	if dir == "" && os.Getenv("VET_TEST_DIR") == "" {
		return true, "go mod tidy skipped (no workspace dir)"
	}
	mod := "go.mod"
	if dir != "" {
		mod = filepath.Join(dir, "go.mod")
	} else if legacy := os.Getenv("VET_TEST_DIR"); legacy != "" {
		mod = filepath.Join(legacy, "go.mod")
	}
	if _, err := os.Stat(mod); err != nil {
		return true, "go mod tidy skipped (no go.mod)"
	}
	c, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(c, "go", "mod", "tidy")
	if len(extraEnv) > 0 {
		cmd.Env = extraEnv
	}
	if dir != "" {
		cmd.Dir = dir
	} else if legacy := os.Getenv("VET_TEST_DIR"); legacy != "" {
		cmd.Dir = legacy
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		out := buf.String()
		if len(out) > 1500 {
			out = out[:1500]
		}
		if out == "" {
			out = err.Error()
		}
		log.Printf("go mod tidy failed: %v %s", err, out)
		return false, out
	}
	return true, "go mod tidy ok"
}

func runGoVet(ctx context.Context, dir string, extraEnv []string) (bool, string) {
	if os.Getenv("SKIP_VET") == "1" {
		return true, "skipped"
	}
	c, cancel := context.WithTimeout(ctx, 180*time.Second)
	defer cancel()
	cmd := exec.CommandContext(c, "go", "vet", "./...")
	if len(extraEnv) > 0 {
		cmd.Env = extraEnv // 含隔离 DATABASE_URL，阻断 workspace 测试继承产库连接
	}
	// workdir: 产物目录优先；否则 VET_TEST_DIR / 历史探测（兼容旧调用）
	if dir != "" {
		cmd.Dir = dir
	} else if legacy := os.Getenv("VET_TEST_DIR"); legacy != "" {
		cmd.Dir = legacy
	} else if _, err := os.Stat("/app/go.mod"); err == nil {
		cmd.Dir = "/app"
	} else if _, err := os.Stat("go.mod"); err == nil {
		cmd.Dir = "."
	} else if _, err := os.Stat("/workspace/backend/go.mod"); err == nil {
		cmd.Dir = "/workspace/backend"
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	out := buf.String()
	if out == "" {
		out = "go vet ok"
	}
	if err != nil {
		// no packages in /app means no source copied — treat as pass (pipeline static already checked)
		if strings.Contains(out, "matched no packages") || strings.Contains(out, "no packages") {
			log.Printf("go vet skipped (no packages): %s", out)
			return true, out
		}
		log.Printf("go vet failed: %v %s", err, out)
		return false, out
	}
	return true, out
}

func runGoTest(ctx context.Context, dir string, extraEnv []string) (bool, string, float64) {
	if os.Getenv("SKIP_VET") == "1" {
		return true, "skipped", -1
	}
	c, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	coverFile := filepath.Join(os.TempDir(), "forja_cover_"+uuid.NewString()+".out")
	defer os.Remove(coverFile)
	cmd := exec.CommandContext(c, "go", "test", "./...", "-run", "^Test", "-count=1", "-json", "-coverprofile="+coverFile)
	if len(extraEnv) > 0 {
		cmd.Env = extraEnv // 含隔离 DATABASE_URL，阻断 workspace 测试继承产库连接
	}
	if wd := resolveWorkDir(dir); wd != "" {
		cmd.Dir = wd
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	out := buf.String()
	if len(out) > 2000 {
		out = out[:2000]
	}
	if err != nil {
		log.Printf("go test failed: %v %s", err, out)
		// 放行分支（与 runGoVet 的 no-packages 语义对齐，pipeline.go:119）：
		//   no test files            —— 目录有代码但无测试文件，go 历史版本 exit 0
		//   matched no packages      —— 空模块警告（如瘦身后回退命中无源码的 /app）
		//   no packages to test      —— 同上场景 go 1.24 的输出（且 exit 1）
		// 2026-09-07 修正：放行条件必须先排除真实失败——此前任一包 "no test files"
		// 即可为整轮失败开脱（留言板 attempt 2：handler/store 测试 FAIL 却判通过）。
		hasFail := strings.Contains(out, `"Action":"fail"`) ||
			strings.Contains(out, "\nFAIL") || strings.HasPrefix(out, "FAIL")
		if !hasFail && (strings.Contains(out, "no test files") ||
			strings.Contains(out, "matched no packages") ||
			strings.Contains(out, "no packages to test")) {
			return true, out, -1
		}
		return false, out, -1
	}
	return true, out, parseCoverage(coverFile)
}

// resolveWorkDir 目录解析（runGoTest 与覆盖率门禁共用）：显式 dir > VET_TEST_DIR > /app > /workspace/backend。
func resolveWorkDir(dir string) string {
	if dir != "" {
		return dir
	}
	if legacy := os.Getenv("VET_TEST_DIR"); legacy != "" {
		return legacy
	}
	if _, err := os.Stat("/app/go.mod"); err == nil {
		return "/app"
	}
	if _, err := os.Stat("/workspace/backend/go.mod"); err == nil {
		return "/workspace/backend"
	}
	return ""
}

// countGoFiles 统计目录下 Go 源码与测试文件数（跳过 .git/vendor/node_modules）。
func countGoFiles(root string) (sources, tests int) {
	if root == "" {
		root = "."
	}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor", "node_modules":
				return fs.SkipDir
			}
			return nil
		}
		name := d.Name()
		switch {
		case strings.HasSuffix(name, "_test.go"):
			tests++
		case strings.HasSuffix(name, ".go"):
			sources++
		}
		return nil
	})
	return sources, tests
}

// parseCoverage 从 coverprofile 解析 total 覆盖率（0~1）；解析失败返回 -1（不阻断）
func parseCoverage(profile string) float64 {
	data, err := os.ReadFile(profile)
	if err != nil {
		return -1
	}
	re := regexp.MustCompile(`^total:\s+\(statements\)\s+([\d.]+)%`)
	for _, line := range strings.Split(string(data), "\n") {
		if m := re.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			v, err := strconv.ParseFloat(m[1], 64)
			if err != nil {
				return -1
			}
			return v / 100
		}
	}
	return -1
}

// coverageMin 阈值：env COVERAGE_MIN > rules/testing.yaml coverage.line > 默认 80
func coverageMin() float64 {
	if s := os.Getenv("COVERAGE_MIN"); s != "" {
		if v, err := strconv.ParseFloat(s, 64); err == nil && v >= 0 && v <= 100 {
			return v
		}
	}
	if v := coverageFromYaml(); v > 0 {
		return v
	}
	return 80
}

func coverageFromYaml() float64 {
	for _, p := range []string{"rules/testing.yaml", "../rules/testing.yaml", "/app/rules/testing.yaml"} {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		re := regexp.MustCompile(`coverage:\s*\{\s*line:\s*([\d.]+)\s*\}`)
		if m := re.FindStringSubmatch(string(data)); m != nil {
			v, err := strconv.ParseFloat(m[1], 64)
			if err == nil {
				return v
			}
		}
	}
	return 0
}

func trimf(v float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.1f", v), "0"), ".")
}

func runGovulncheck(ctx context.Context, dir string) (bool, string) {
	if _, err := exec.LookPath("govulncheck"); err != nil {
		return true, "govulncheck not installed, skipped"
	}
	c, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(c, "govulncheck", "./...")
	if dir != "" {
		cmd.Dir = dir
	} else if _, err := os.Stat("/app/go.mod"); err == nil {
		cmd.Dir = "/app"
	} else if _, err := os.Stat("/workspace/backend/go.mod"); err == nil {
		cmd.Dir = "/workspace/backend"
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		out := buf.String()
		if len(out) > 1500 {
			out = out[:1500]
		}
		return false, out
	}
	return true, "govulncheck ok"
}

func runNpmAudit(ctx context.Context, dir string) (bool, string) {
	// 仅当存在 frontend/package.json（或产物目录含前端）时执行
	candidates := []string{"frontend/package.json", "../frontend/package.json", "/app/../frontend/package.json"}
	found := false
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			found = true
			break
		}
	}
	if !found && dir != "" {
		if _, err := os.Stat(filepath.Join(dir, "..", "frontend", "package.json")); err == nil {
			found = true
		}
	}
	if !found {
		return true, "npm audit skipped (no frontend)"
	}
	c, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(c, "npm", "audit", "--audit-level=high", "--json")
	// 尝试定位 frontend 目录
	for _, p := range []string{"frontend", "../frontend", "/app/../frontend"} {
		if _, err := os.Stat(filepath.Join(p, "package.json")); err == nil {
			cmd.Dir = p
			break
		}
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		out := buf.String()
		if len(out) > 1500 {
			out = out[:1500]
		}
		// npm audit 非 0 仅高危才判失败（json 含 vulnerabilities 高危计数）
		if strings.Contains(out, `"severity":"high"`) || strings.Contains(out, `"severity":"critical"`) {
			return false, out
		}
	}
	return true, "npm audit ok"
}

func recordTest(ctx context.Context, taskID, name, status, output string) {
	// ensure test_cases + test_results exist; create ephemeral case
	// （2026-09-06 修复：原 INSERT 带 test_cases 不存在的 type 列，静默失败——
	//  校验失败详情全部丢失，锻造失败原因无从排查）
	// （2026-09-07 追修：test_results 根本没有 started_at/finished_at 列
	//  （实为 duration_ms/created_at），INSERT 常年报错被 `_, _` 吞掉——
	//  改按真实 schema 落行，失败打日志不再静默）
	tcID := uuid.NewString()
	if _, err := model.Pool.Exec(ctx, `INSERT INTO test_cases (id, task_id, name) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, tcID, taskID, name); err != nil {
		log.Printf("recordTest case failed: %v", err)
		return
	}
	if _, err := model.Pool.Exec(ctx, `INSERT INTO test_results (test_case_id, status, output) VALUES ($1,$2,$3)`, tcID, status, output); err != nil {
		log.Printf("recordTest result failed: %v", err)
	}
}
