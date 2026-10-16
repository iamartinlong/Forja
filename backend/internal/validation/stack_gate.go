package validation

// 多栈门禁辅助：从工作区 project.yaml 解析栈；node/python 工具链与命令执行；
// lcov 覆盖率解析。Go 路径保持既有实现（零回归），本文件只服务新增栈（v2.2 增 python）。

import (
	"context"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"forja/backend/internal/projectyaml"
	"forja/backend/internal/stackkind"
)

// resolveStack 从工作区 project.yaml 解析栈声明；缺失/非法时返回 (零值, false)
// 由调用方按 Go 兼容路径处理（历史仓无 project.yaml）。
func resolveStack(dir string) (projectyaml.Manifest, bool) {
	wd := resolveWorkDir(dir)
	if wd == "" {
		wd = "."
	}
	raw, err := os.ReadFile(filepath.Join(wd, "project.yaml"))
	if err != nil {
		return projectyaml.Manifest{}, false
	}
	m, perr := projectyaml.Parse(raw)
	if perr != nil {
		log.Printf("pipeline: project.yaml invalid, fallback go: %v", perr)
		return projectyaml.Manifest{}, false
	}
	return *m, true
}

// stackLang 生效栈：契约声明优先，逾期回退 go（兼容）。
func stackLang(m projectyaml.Manifest, ok bool) string {
	if ok {
		if lang := strings.TrimSpace(m.Stack.Backend.Lang); lang != "" {
			return lang
		}
	}
	return "go"
}

// needsDB 门禁是否需要隔离 DB：契约声明优先，缺省按栈默认。
func needsDB(m projectyaml.Manifest, ok bool, lang string) bool {
	if ok && m.Test.NeedsDB {
		return true
	}
	if ok && m.Version >= 2 {
		// v2 显式声明（含 false）以契约为准
		return m.Test.NeedsDB
	}
	if k, found := stackkind.For(lang); found {
		return k.NeedsDBDefault
	}
	return false
}

func hasNode() bool {
	if _, err := exec.LookPath("node"); err != nil {
		return false
	}
	if _, err := exec.LookPath("npm"); err != nil {
		return false
	}
	return true
}

// hasPython 判断 worker 是否具备 Python 门禁工具链（v2.2 T1）。
func hasPython() bool {
	if _, err := exec.LookPath("python3"); err != nil {
		return false
	}
	return true
}

// hasToolchain 按栈判断门禁工具链可用性（node/python；go 走 hasGo 历史路径）。
func hasToolchain(lang string) bool {
	switch lang {
	case "node":
		return hasNode()
	case "python":
		return hasPython()
	}
	return false
}

// runShellCommand 通用命令门禁执行（sh -c）；返回 (通过, 截断输出)。
func runShellCommand(ctx context.Context, dir, command string, timeout time.Duration) (bool, string) {
	if strings.TrimSpace(command) == "" {
		return true, "skipped (empty command)"
	}
	c, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(c, "sh", "-c", command)
	if wd := resolveWorkDir(dir); wd != "" {
		cmd.Dir = wd
	}
	var buf strings.Builder
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	out := buf.String()
	if len(out) > 2000 {
		out = out[:2000]
	}
	if err != nil {
		return false, out
	}
	return true, out
}

// hasLockFile 工作区是否存在依赖锁文件（node 栈的 npm ci/audit 前置）。
func hasLockFile(dir string) bool {
	wd := resolveWorkDir(dir)
	if wd == "" {
		wd = "."
	}
	if _, err := os.Stat(filepath.Join(wd, "package-lock.json")); err == nil {
		return true
	}
	return false
}

// hasDepsFile 按栈判断依赖声明文件是否存在（依赖安装/漏洞扫描前置；v2.2 起 node/python 共用）。
func hasDepsFile(dir, lang string) bool {
	wd := resolveWorkDir(dir)
	if wd == "" {
		wd = "."
	}
	switch lang {
	case "node":
		return hasLockFile(dir)
	case "python":
		for _, f := range []string{"requirements-dev.txt", "requirements.txt"} {
			if _, err := os.Stat(filepath.Join(wd, f)); err == nil {
				return true
			}
		}
	}
	return false
}

// parseLcov 从 lcov.info 解析行覆盖率（LF/LH 汇总，0~1）；无数据返回 -1（不阻断）。
func parseLcov(path string) float64 {
	raw, err := os.ReadFile(path)
	if err != nil {
		return -1
	}
	var lf, lh float64
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "LF:"):
			if v, perr := strconv.ParseFloat(strings.TrimPrefix(line, "LF:"), 64); perr == nil {
				lf += v
			}
		case strings.HasPrefix(line, "LH:"):
			if v, perr := strconv.ParseFloat(strings.TrimPrefix(line, "LH:"), 64); perr == nil {
				lh += v
			}
		}
	}
	if lf <= 0 {
		return -1
	}
	return lh / lf
}

// resolveCoveragePath lcov 覆盖率文件路径（相对工作区）。
func resolveCoveragePath(dir, file string) string {
	wd := resolveWorkDir(dir)
	if wd == "" {
		wd = "."
	}
	if file == "" {
		file = "coverage/lcov.info"
	}
	if filepath.IsAbs(file) {
		return file
	}
	return filepath.Join(wd, file)
}
