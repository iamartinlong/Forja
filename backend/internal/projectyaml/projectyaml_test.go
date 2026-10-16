package projectyaml

import (
	"strings"
	"testing"
)

const validManifest = `version: 1
type: fullstack
stack:
  backend:  { lang: go, framework: net/http }
  frontend: { framework: react, bundler: vite }
test:
  unit: "go test ./..."
build:
  dockerfile: "Dockerfile"
run:
  port: 8080
  health: "/health"
coverage_gate: 70
`

func TestParse_Valid(t *testing.T) {
	m, err := Parse([]byte(validManifest))
	if err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
	if m.Stack.Backend.Lang != "go" || m.Run.Port != 8080 || m.CoverageGate != 70 {
		t.Fatalf("unexpected parse result: %+v", m)
	}
	if _, err := m.JSON(); err != nil {
		t.Fatalf("json cache failed: %v", err)
	}
}

const validManifestV2Node = `version: 2
type: backend
stack:
  backend: { lang: node, framework: "node:http", runtime: "20" }
test:
  unit: "npm test"
  needs_db: false
build:
  dockerfile: "Dockerfile"
run:
  port: 8080
  health: "/health"
coverage_gate: 0
`

func TestParse_ValidV2Node(t *testing.T) {
	m, err := Parse([]byte(validManifestV2Node))
	if err != nil {
		t.Fatalf("valid v2 node manifest rejected: %v", err)
	}
	if m.Stack.Backend.Lang != "node" || m.Stack.Backend.Runtime != "20" {
		t.Fatalf("unexpected parse result: %+v", m)
	}
}

const validManifestV2Python = `version: 2
type: backend
stack:
  backend: { lang: python, framework: "fastapi", runtime: "3.12" }
test:
  unit: "python3 -m pytest -q --cov=src --cov-report=lcov:coverage/lcov.info"
  needs_db: false
  coverage: { file: "coverage/lcov.info", format: lcov }
build:
  dockerfile: "Dockerfile"
run:
  port: 8080
  health: "/health"
coverage_gate: 0
`

// v2.2 T1：python 栈契约解析（lang 白名单 + lcov 覆盖率 + needs_db 显式 false）。
func TestParse_ValidV2Python(t *testing.T) {
	m, err := Parse([]byte(validManifestV2Python))
	if err != nil {
		t.Fatalf("valid v2 python manifest rejected: %v", err)
	}
	if m.Stack.Backend.Lang != "python" || m.Test.Coverage.Format != "lcov" || m.Test.NeedsDB {
		t.Fatalf("unexpected parse result: %+v", m)
	}
}

func TestParse_Rejections(t *testing.T) {
	cases := map[string]string{
		"unknown field":     "version: 1\ntype: fullstack\nstack:\n  backend: { lang: go }\nbogus: 1\n",
		"bad version":       "version: 99\ntype: fullstack\nstack:\n  backend: { lang: go }\n",
		"bad type":          "version: 1\ntype: microservice\nstack:\n  backend: { lang: go }\n",
		"unsupported stack": "version: 1\ntype: backend\nstack:\n  backend: { lang: rust }\n",
		"bad coverage fmt":  "version: 2\ntype: backend\nstack:\n  backend: { lang: node }\ntest:\n  coverage: { format: jacoco }\n",
		"gate range":        "version: 1\ntype: backend\nstack:\n  backend: { lang: go }\ncoverage_gate: 130\n",
	}
	for name, yml := range cases {
		if _, err := Parse([]byte(yml)); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		} else if !strings.Contains(err.Error(), "project.yaml") {
			t.Errorf("%s: error should mention project.yaml: %v", name, err)
		}
	}
}

func TestParse_FrontendOnly(t *testing.T) {
	_, err := Parse([]byte("version: 1\ntype: frontend\nstack:\n  frontend: { framework: react }\n"))
	if err != nil {
		t.Fatalf("frontend-only manifest should be allowed: %v", err)
	}
}
