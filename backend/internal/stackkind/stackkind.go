// Package stackkind 多栈注册表（v2.1）：按 project.yaml 的 backend lang 提供
// 门禁命令与覆盖率默认格式，避免 validation 内按语言硬编码分支。
package stackkind

// Kind 单栈能力声明。
type Kind struct {
	Lang           string
	UnitDefault    string // 契约 test.unit 缺省命令
	CoverageFormat string // 覆盖率默认格式（go|lcov|""）
	NeedsDBDefault bool   // 门禁是否默认需要隔离 DB
	VulnCmd        string // 依赖漏洞扫描命令（空=跳过）
	TidyCmd        string // 依赖整理命令（空=跳过）
	RequiresLock   bool   // 漏洞扫描/依赖整理是否要求 lock 文件存在
}

var registry = map[string]Kind{
	"go": {
		Lang:           "go",
		UnitDefault:    "go test ./...",
		CoverageFormat: "go",
		NeedsDBDefault: true,
		VulnCmd:        "govulncheck ./...",
		TidyCmd:        "go mod tidy",
	},
	"node": {
		Lang:           "node",
		UnitDefault:    "npm test",
		CoverageFormat: "lcov",
		NeedsDBDefault: false,
		VulnCmd:        "npm audit --audit-level=high",
		TidyCmd:        "npm ci --no-audit --no-fund",
		RequiresLock:   true,
	},
	// v2.2 T1：Python 栈（FastAPI + pytest + coverage.py lcov）。
	// VulnCmd 暂空（pip-audit 未烘焙，见 v2.2-proposal T1 范围裁剪），
	// 依赖安装走 requirements-dev.txt（TidyCmd），镜像源由 worker ENV PIP_INDEX_URL 兜底。
	"python": {
		Lang:           "python",
		UnitDefault:    "python3 -m pytest -q",
		CoverageFormat: "lcov",
		NeedsDBDefault: false,
		TidyCmd:        "python3 -m pip install --disable-pip-version-check -r requirements-dev.txt -q",
		RequiresLock:   true,
	},
}

// For 按 lang 取栈声明。
func For(lang string) (Kind, bool) {
	k, ok := registry[lang]
	return k, ok
}

// Supported 支持的栈列表（稳定顺序）。
func Supported() []string { return []string{"go", "node", "python"} }
