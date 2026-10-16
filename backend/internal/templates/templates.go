// Package templates 项目脚手架模板注册表（v2.1：组织级模板库 + 内置兜底）。
//
// 来源：
//   - builtin：go:embed 内置模板（离线/新装可用，随代码评审）
//   - registry：组织仓（FORJA_TEMPLATES_REPO，如 forja-admin/templates）热更新，
//     目录 `templates/<id>/`，manifest `template.yaml`（id/name/version/stack）
//
// 渲染变量：{{name}} 项目名 / {{slug}} slug(亦作 module) / {{repo}} owner/name / {{project_id}}。
// .tmpl 后缀文件渲染后剥离后缀（避免 Go 源文件被主模块编译）。
package templates

import (
	"embed"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"
)

//go:embed all:standard-go-react
var stdGoReact embed.FS

//go:embed all:standard-node-api
var stdNodeAPI embed.FS

//go:embed all:standard-python-api
var stdPythonAPI embed.FS

// Vars 模板渲染变量。
type Vars struct {
	Name      string
	Slug      string
	Repo      string
	ProjectID string
	// Options 模板参数（v2.2 T2）：{{opt.<key>}} 直插 + {{#opt.<key>}}…{{/opt.<key>}} 条件块。
	Options map[string]string
}

// Option 模板参数声明（v2.2 T2）：仅 enum/bool，无自由文本注入面。
type Option struct {
	Key     string   `json:"key" yaml:"key"`
	Label   string   `json:"label" yaml:"label"`
	Type    string   `json:"type" yaml:"type"` // enum | bool
	Values  []string `json:"values,omitempty" yaml:"values,omitempty"`
	Default string   `json:"default,omitempty" yaml:"default,omitempty"`
}

const (
	// MaxTemplateOptions 单模板参数上限（防止"万能模板"化）。
	MaxTemplateOptions = 6
	OptionTypeEnum     = "enum"
	OptionTypeBool     = "bool"
)

var optionKeyRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

// optionValueRe 枚举值允许版本号形态（如 3.12），仍禁空格/斜杠/模板语法，杜绝注入面。
var optionValueRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,31}$`)

// ValidateOptionsSchema 校验模板参数声明（值本身亦限制为短标识，避免注入路径）。
func ValidateOptionsSchema(opts []Option) error {
	if len(opts) > MaxTemplateOptions {
		return fmt.Errorf("too many options: %d (max %d)", len(opts), MaxTemplateOptions)
	}
	seen := map[string]bool{}
	for _, o := range opts {
		if !optionKeyRe.MatchString(o.Key) {
			return fmt.Errorf("invalid option key %q", o.Key)
		}
		if seen[o.Key] {
			return fmt.Errorf("duplicate option key %q", o.Key)
		}
		seen[o.Key] = true
		if strings.TrimSpace(o.Label) == "" {
			return fmt.Errorf("option %s: label required", o.Key)
		}
		switch o.Type {
		case OptionTypeBool:
			if o.Default != "" && o.Default != "true" && o.Default != "false" {
				return fmt.Errorf("option %s: bool default must be true/false", o.Key)
			}
		case OptionTypeEnum:
			if len(o.Values) == 0 || len(o.Values) > 10 {
				return fmt.Errorf("option %s: enum needs 1..10 values", o.Key)
			}
			for _, v := range o.Values {
				if !optionValueRe.MatchString(v) {
					return fmt.Errorf("option %s: invalid enum value %q (short id only)", o.Key, v)
				}
			}
			if o.Default != "" {
				ok := false
				for _, v := range o.Values {
					if v == o.Default {
						ok = true
					}
				}
				if !ok {
					return fmt.Errorf("option %s: default %q not in values", o.Key, o.Default)
				}
			}
		default:
			return fmt.Errorf("option %s: unsupported type %q (want enum|bool)", o.Key, o.Type)
		}
	}
	return nil
}

// ResolveOptions 校验用户输入并补默认值，返回渲染用最终值。
func ResolveOptions(declared []Option, given map[string]string) (map[string]string, error) {
	if err := ValidateOptionsSchema(declared); err != nil {
		return nil, err
	}
	byKey := map[string]Option{}
	out := map[string]string{}
	for _, o := range declared {
		byKey[o.Key] = o
		if o.Default != "" {
			out[o.Key] = o.Default
		} else if o.Type == OptionTypeBool {
			out[o.Key] = "false"
		}
	}
	for k, v := range given {
		o, ok := byKey[k]
		if !ok {
			return nil, fmt.Errorf("unknown option %q", k)
		}
		v = strings.TrimSpace(v)
		switch o.Type {
		case OptionTypeBool:
			if v != "true" && v != "false" {
				return nil, fmt.Errorf("option %s: must be true/false", k)
			}
		case OptionTypeEnum:
			okv := false
			for _, x := range o.Values {
				if x == v {
					okv = true
				}
			}
			if !okv {
				return nil, fmt.Errorf("option %s: invalid value %q", k, v)
			}
		}
		out[k] = v
	}
	return out, nil
}

// File 渲染后的仓内文件。
type File struct {
	Path    string
	Content string
}

// 模板来源。
const (
	SourceBuiltin  = "builtin"
	SourceRegistry = "registry"
)

// 内置模板默认版本（manifest 版本从 v2.1 起记录；内置视为 1.0.0）。
const builtinVersion = "1.0.0"

// Template 模板元数据与文件源。
type Template struct {
	ID          string
	Name        string
	Description string
	Version     string
	Source      string // builtin | registry
	Stack       map[string]any
	// Options 模板参数声明（v2.2 T2，可为空）。
	Options []Option

	root string
	fsys fs.FS
	// raw 组织库模板原始文件（含 {{vars}} 占位）；非 nil 时优先于 embed。
	raw map[string]string
}

func render(s string, v Vars) string {
	out := renderOptionBlocks(s, v.Options)
	out = strings.NewReplacer(
		"{{name}}", v.Name,
		"{{slug}}", v.Slug,
		"{{module}}", v.Slug,
		"{{repo}}", v.Repo,
		"{{project_id}}", v.ProjectID,
	).Replace(out)
	for k, val := range v.Options {
		out = strings.ReplaceAll(out, "{{opt."+k+"}}", val)
	}
	return out
}

// renderOptionBlocks 条件块：{{#opt.key}}body{{/opt.key}} —— 值为真时保留 body，否则整体剔除。
// 仅支持非嵌套块（模板参数只做小范围开关，保持解析简单可审计）。
func renderOptionBlocks(s string, opts map[string]string) string {
	const open = "{{#opt."
	const closePfx = "{{/opt."
	for {
		i := strings.Index(s, open)
		if i < 0 {
			return s
		}
		rest := s[i+len(open):]
		j := strings.Index(rest, "}}")
		if j < 0 {
			return s // 畸形块：原样保留，交由测试/评审兜底
		}
		key := rest[:j]
		bodyStart := i + len(open) + j + 2
		end := closePfx + key + "}}"
		k := strings.Index(s[bodyStart:], end)
		if k < 0 {
			return s
		}
		body := s[bodyStart : bodyStart+k]
		repl := ""
		if optionTruthy(opts[key]) {
			repl = body
		}
		s = s[:i] + repl + s[bodyStart+k+len(end):]
	}
}

// optionTruthy 布尔语义：非空且非 false/0。
func optionTruthy(v string) bool {
	return v != "" && v != "false" && v != "0"
}

// Files 渲染全部文件（路径稳定排序）。
func (t *Template) Files(v Vars) ([]File, error) {
	if t.raw != nil {
		paths := make([]string, 0, len(t.raw))
		for p := range t.raw {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		out := make([]File, 0, len(paths))
		for _, p := range paths {
			rel := strings.TrimSuffix(p, ".tmpl")
			out = append(out, File{Path: rel, Content: render(t.raw[p], v)})
		}
		return out, nil
	}
	var out []File
	err := fs.WalkDir(t.fsys, t.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, rerr := fs.ReadFile(t.fsys, p)
		if rerr != nil {
			return rerr
		}
		rel := strings.TrimSuffix(strings.TrimPrefix(p, t.root+"/"), ".tmpl")
		out = append(out, File{Path: rel, Content: render(string(b), v)})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// Paths 模板文件路径清单（不含内容，供模板列表展示）。
func (t *Template) Paths() []string {
	files, err := t.Files(Vars{})
	if err != nil {
		return nil
	}
	paths := make([]string, 0, len(files))
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	return paths
}

// RawFile 读取模板内原始文件（未渲染），如 project.yaml / AGENTS.md 原文。
func (t *Template) RawFile(path string) string {
	if t.raw != nil {
		return t.raw[path]
	}
	b, err := fs.ReadFile(t.fsys, t.root+"/"+path)
	if err != nil {
		return ""
	}
	return string(b)
}

// builtinRegistry 内置模板（随代码评审）。
var builtinRegistry = map[string]*Template{
	"standard-go-react": {
		ID:          "standard-go-react",
		Name:        "FORJA 标准栈（Go + React）",
		Description: "Go 标准库 HTTP 可运行骨架 + project.yaml + AGENTS.md + Dockerfile + 客户运行清单",
		Version:     builtinVersion,
		Source:      SourceBuiltin,
		Stack: map[string]any{
			"backend":  map[string]any{"lang": "go", "framework": "net/http"},
			"frontend": map[string]any{"framework": "react", "bundler": "vite"},
		},
		root: "standard-go-react",
		fsys: stdGoReact,
	},
	"standard-node-api": {
		ID:          "standard-node-api",
		Name:        "FORJA 标准栈（Node/TS API）",
		Description: "Node 20 HTTP 可运行骨架（零依赖 runtime）+ vitest + Dockerfile + 客户运行清单",
		Version:     builtinVersion,
		Source:      SourceBuiltin,
		Stack: map[string]any{
			"backend": map[string]any{"lang": "node", "framework": "node:http", "runtime": "20"},
		},
		root: "standard-node-api",
		fsys: stdNodeAPI,
	},
	"standard-python-api": {
		ID:          "standard-python-api",
		Name:        "FORJA 标准栈（Python API）",
		Description: "Python + FastAPI 可运行骨架 + pytest/coverage(lcov) + Dockerfile + 客户运行清单",
		Version:     builtinVersion,
		Source:      SourceBuiltin,
		Stack: map[string]any{
			"backend": map[string]any{"lang": "python", "framework": "fastapi", "runtime": "3.12"},
		},
		Options: []Option{
			{Key: "python_version", Label: "Python 版本", Type: OptionTypeEnum, Values: []string{"3.12", "3.11"}, Default: "3.12"},
			{Key: "health_path", Label: "健康检查路径", Type: OptionTypeEnum, Values: []string{"health", "healthz"}, Default: "health"},
			{Key: "disable_redoc", Label: "关闭 ReDoc 文档", Type: OptionTypeBool, Default: "true"},
		},
		root: "standard-python-api",
		fsys: stdPythonAPI,
	},
}

// builtinList 内置模板列表（按 ID 排序）。
func builtinList() []*Template {
	ids := make([]string, 0, len(builtinRegistry))
	for id := range builtinRegistry {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]*Template, 0, len(ids))
	for _, id := range ids {
		out = append(out, builtinRegistry[id])
	}
	return out
}

// StandardManifest 标准模板的 project.yaml 原文（import 缺失时引导补交）。
func StandardManifest() string {
	t, ok := builtinRegistry["standard-go-react"]
	if !ok {
		return ""
	}
	return t.RawFile("project.yaml")
}

// StandardAgents 标准模板的 AGENTS.md 原文（生成初版规范）。
func StandardAgents() string {
	t, ok := builtinRegistry["standard-go-react"]
	if !ok {
		return ""
	}
	return t.RawFile("AGENTS.md")
}

// RenderStandardAgents 渲染标准规范骨架（项目名占位替换）。
func RenderStandardAgents(name string) string {
	return render(StandardAgents(), Vars{Name: name})
}

// ValidateFilePaths 渲染结果自检（调试/单测用）。
func (t *Template) ValidateFilePaths() error {
	files, err := t.Files(Vars{Name: "n", Slug: "s", Repo: "o/r", ProjectID: "p"})
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("template %s: no files", t.ID)
	}
	return nil
}
