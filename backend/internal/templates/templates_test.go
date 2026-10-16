package templates

import (
	"strings"
	"testing"

	"forja/backend/internal/projectyaml"
)

func TestStandardTemplate_Files(t *testing.T) {
	tpl, ok := Get("standard-go-react")
	if !ok {
		t.Fatal("standard-go-react not registered")
	}
	if err := tpl.ValidateFilePaths(); err != nil {
		t.Fatal(err)
	}
	files, err := tpl.Files(Vars{Name: "演示项目", Slug: "demo-app", Repo: "forja-admin/demo-app-12345678", ProjectID: "pid-1"})
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]string{}
	for _, f := range files {
		byPath[f.Path] = f.Content
		for _, tok := range []string{"{{name}}", "{{slug}}", "{{module}}", "{{repo}}", "{{project_id}}"} {
			if strings.Contains(f.Content, tok) {
				t.Fatalf("unrendered placeholder %s in %s", tok, f.Path)
			}
		}
	}
	for _, want := range []string{"project.yaml", "AGENTS.md", "README.md", "Dockerfile", "main.go", "go.mod", ".gitignore", ".dockerignore", "deploy/compose.yml", ".gitea/workflows/build.yml"} {
		if _, ok := byPath[want]; !ok {
			t.Errorf("missing scaffold file %s", want)
		}
	}
	if _, ok := byPath["main.go.tmpl"]; ok {
		t.Error(".tmpl suffix must be stripped")
	}
	if !strings.Contains(byPath["main.go"], "演示项目") {
		t.Error("name placeholder not rendered in main.go")
	}
	if !strings.Contains(byPath["README.md"], "demo-app") {
		t.Error("slug placeholder not rendered in README.md")
	}
	if !strings.Contains(byPath[".gitea/workflows/build.yml"], "forja-admin/demo-app-12345678") {
		t.Error("repo placeholder not rendered in build.yml")
	}
	// 模板内 project.yaml 必须是合法契约
	if _, err := projectyaml.Parse([]byte(byPath["project.yaml"])); err != nil {
		t.Fatalf("scaffold project.yaml invalid: %v", err)
	}
}

func TestStandardManifestAndAgents(t *testing.T) {
	if _, err := projectyaml.Parse([]byte(StandardManifest())); err != nil {
		t.Fatalf("StandardManifest invalid: %v", err)
	}
	agents := RenderStandardAgents("我的项目")
	if !strings.Contains(agents, "我的项目") {
		t.Error("RenderStandardAgents should substitute name")
	}
}

// v2.2 T1：Python 标准模板渲染自检（契约可解析 + 关键文件齐备 + 占位符全渲染）。
func TestPythonTemplate_Files(t *testing.T) {
	tpl, ok := Get("standard-python-api")
	if !ok {
		t.Fatal("standard-python-api not registered")
	}
	if err := tpl.ValidateFilePaths(); err != nil {
		t.Fatal(err)
	}
	rd, err := ResolveOptions(tpl.Options, nil)
	if err != nil {
		t.Fatalf("resolve default options: %v", err)
	}
	files, err := tpl.Files(Vars{Name: "演示 Py 项目", Slug: "demo-py", Repo: "forja-admin/demo-py-12345678", ProjectID: "pid-1", Options: rd})
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]string{}
	for _, f := range files {
		byPath[f.Path] = f.Content
		for _, tok := range []string{"{{name}}", "{{slug}}", "{{module}}", "{{repo}}", "{{project_id}}"} {
			if strings.Contains(f.Content, tok) {
				t.Fatalf("unrendered placeholder %s in %s", tok, f.Path)
			}
		}
	}
	for _, want := range []string{
		"project.yaml", "AGENTS.md", "README.md", "Dockerfile",
		"requirements.txt", "requirements-dev.txt",
		"src/app/main.py", "test/test_health.py",
		"deploy/compose.yml", ".gitea/workflows/build.yml", ".gitignore", ".dockerignore",
	} {
		if _, ok := byPath[want]; !ok {
			t.Errorf("missing scaffold file %s", want)
		}
	}
	// 仓内契约必须合法且为 python 栈
	m, err := projectyaml.Parse([]byte(byPath["project.yaml"]))
	if err != nil {
		t.Fatalf("scaffold project.yaml invalid: %v", err)
	}
	if m.Stack.Backend.Lang != "python" || m.Test.Coverage.Format != "lcov" {
		t.Fatalf("unexpected scaffold stack: %+v", m.Stack.Backend)
	}
	if !strings.Contains(byPath["src/app/main.py"], "演示 Py 项目") {
		t.Error("name placeholder not rendered in main.py")
	}
	if !strings.Contains(byPath["deploy/compose.yml"], "demo-py:latest") {
		t.Error("slug placeholder not rendered in compose.yml")
	}
	if !strings.Contains(byPath[".gitea/workflows/build.yml"], "forja-admin/demo-py-12345678") {
		t.Error("repo placeholder not rendered in build.yml")
	}
}

// v2.2 T2：模板参数（options）渲染——默认值路径 + 非默认值联动多文件。
func TestPythonTemplate_Options(t *testing.T) {
	tpl, ok := Get("standard-python-api")
	if !ok {
		t.Fatal("standard-python-api not registered")
	}
	if err := ValidateOptionsSchema(tpl.Options); err != nil {
		t.Fatalf("options schema invalid: %v", err)
	}
	if len(tpl.Options) < 2 {
		t.Fatalf("expect >=2 options, got %d", len(tpl.Options))
	}

	// 默认值路径
	rd, err := ResolveOptions(tpl.Options, nil)
	if err != nil {
		t.Fatal(err)
	}
	files, err := tpl.Files(Vars{Name: "N", Slug: "s", Repo: "o/r", ProjectID: "p", Options: rd})
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]string{}
	for _, f := range files {
		byPath[f.Path] = f.Content
		if strings.Contains(f.Content, "{{opt.") || strings.Contains(f.Content, "{{#opt.") {
			t.Fatalf("unrendered option placeholder in %s: %s", f.Path, f.Content[:min(80, len(f.Content))])
		}
	}
	if !strings.Contains(byPath["Dockerfile"], "python:3.12-slim") {
		t.Error("default python_version not rendered in Dockerfile")
	}
	if !strings.Contains(byPath["src/app/main.py"], `@app.get("/health")`) {
		t.Error("default health_path not rendered in main.py")
	}
	if !strings.Contains(byPath["src/app/main.py"], "redoc_url=None") {
		t.Error("default disable_redoc=true should keep redoc_url=None")
	}
	if !strings.Contains(byPath["deploy/compose.yml"], "/health'") {
		t.Error("default health_path not rendered in compose healthcheck")
	}
	if !strings.Contains(byPath["test/test_health.py"], `client.get("/health")`) {
		t.Error("default health_path not rendered in test")
	}
	if !strings.Contains(byPath["project.yaml"], `runtime: "3.12"`) || !strings.Contains(byPath["project.yaml"], `health: "/health"`) {
		t.Error("default options not rendered in project.yaml")
	}

	// 非默认值：enum 与 bool 联动
	given := map[string]string{"python_version": "3.11", "health_path": "healthz", "disable_redoc": "false"}
	rv, err := ResolveOptions(tpl.Options, given)
	if err != nil {
		t.Fatal(err)
	}
	files, err = tpl.Files(Vars{Name: "N", Slug: "s", Repo: "o/r", ProjectID: "p", Options: rv})
	if err != nil {
		t.Fatal(err)
	}
	byPath = map[string]string{}
	for _, f := range files {
		byPath[f.Path] = f.Content
	}
	if !strings.Contains(byPath["Dockerfile"], "python:3.11-slim") {
		t.Error("python_version=3.11 not rendered in Dockerfile")
	}
	if !strings.Contains(byPath["src/app/main.py"], `@app.get("/healthz")`) {
		t.Error("health_path=healthz not rendered in main.py")
	}
	if strings.Contains(byPath["src/app/main.py"], "redoc_url=None") {
		t.Error("disable_redoc=false should strip the redoc block")
	}
	if !strings.Contains(byPath["deploy/compose.yml"], "/healthz'") {
		t.Error("health_path=healthz not rendered in compose healthcheck")
	}
	if !strings.Contains(byPath["project.yaml"], `runtime: "3.11"`) {
		t.Error("python_version not rendered in project.yaml")
	}

	// 校验分支：未知 key / 非法枚举 / 非法布尔 / 非法 schema
	if _, err := ResolveOptions(tpl.Options, map[string]string{"nope": "x"}); err == nil {
		t.Error("unknown option key must fail")
	}
	if _, err := ResolveOptions(tpl.Options, map[string]string{"health_path": "boom"}); err == nil {
		t.Error("invalid enum value must fail")
	}
	if _, err := ResolveOptions(tpl.Options, map[string]string{"disable_redoc": "yes"}); err == nil {
		t.Error("invalid bool value must fail")
	}
	badSchema := []Option{
		{Key: "Bad-Key", Label: "x", Type: OptionTypeBool},
		{Key: "dup", Label: "x", Type: OptionTypeBool},
		{Key: "dup", Label: "y", Type: OptionTypeBool},
		{Key: "free", Label: "z", Type: "text"},
		{Key: "many", Label: "z", Type: OptionTypeEnum, Values: []string{"A", "b"}, Default: "c"},
	}
	for i := range badSchema {
		var opts []Option
		if i == 0 {
			opts = badSchema[0:1]
		} else {
			opts = badSchema[0 : i+1]
		}
		if err := ValidateOptionsSchema(opts); err == nil {
			t.Errorf("bad schema prefix %d must fail", i+1)
		}
	}
	// 超过 6 项上限
	many := make([]Option, 0, MaxTemplateOptions+1)
	for i := 0; i <= MaxTemplateOptions; i++ {
		many = append(many, Option{Key: "k" + string(rune('a'+i)), Label: "l", Type: OptionTypeBool})
	}
	if err := ValidateOptionsSchema(many); err == nil {
		t.Error("too many options must fail")
	}
}
