// Package projectyaml 解析并校验项目仓根 project.yaml（v2.0 项目域契约，v2.1 多栈扩展）。
//
// 版本：1（v2.0 首期，仅 Go）与 2（v2.1：多栈 + 声明式测试/覆盖率）均接受；
// 未知字段拒绝；校验错误带字段路径，便于前端/API 直接展示。
package projectyaml

import (
	"bytes"
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"
)

const (
	// Version 当前最高支持的契约版本。
	Version = 2
	// MinVersion 仍接受的最低版本（v1 仓无需迁移）。
	MinVersion = 1
)

// Manifest project.yaml 结构（仓内为唯一真相，DB 仅缓存）。
type Manifest struct {
	Version      int       `json:"version" yaml:"version"`
	Type         string    `json:"type" yaml:"type"` // fullstack | backend | frontend
	Stack        Stack     `json:"stack" yaml:"stack"`
	Test         TestSpec  `json:"test" yaml:"test"`
	Build        BuildSpec `json:"build" yaml:"build"`
	Run          RunSpec   `json:"run" yaml:"run"`
	CoverageGate float64   `json:"coverage_gate" yaml:"coverage_gate"`
}

// Stack 技术栈声明。
type Stack struct {
	Backend  LangSpec `json:"backend" yaml:"backend"`
	Frontend LangSpec `json:"frontend" yaml:"frontend"`
}

// LangSpec 单端技术栈。
type LangSpec struct {
	Lang      string `json:"lang,omitempty" yaml:"lang,omitempty"`
	Framework string `json:"framework,omitempty" yaml:"framework,omitempty"`
	Bundler   string `json:"bundler,omitempty" yaml:"bundler,omitempty"`
	Runtime   string `json:"runtime,omitempty" yaml:"runtime,omitempty"` // v2：语言运行时版本提示
}

// TestSpec 测试命令约定（门禁按此分派）。
type TestSpec struct {
	Unit     string       `json:"unit,omitempty" yaml:"unit,omitempty"`
	Frontend string       `json:"frontend,omitempty" yaml:"frontend,omitempty"`
	NeedsDB  bool         `json:"needs_db,omitempty" yaml:"needs_db,omitempty"` // v2：门禁需隔离 DB
	Coverage CoverageSpec `json:"coverage,omitempty" yaml:"coverage,omitempty"` // v2：覆盖率声明
}

// CoverageSpec 覆盖率数据位置与格式（v2）。
type CoverageSpec struct {
	File   string `json:"file,omitempty" yaml:"file,omitempty"`
	Format string `json:"format,omitempty" yaml:"format,omitempty"` // go | lcov（空=按栈推断）
}

// BuildSpec 构建约定。
type BuildSpec struct {
	Dockerfile string `json:"dockerfile,omitempty" yaml:"dockerfile,omitempty"`
	Context    string `json:"context,omitempty" yaml:"context,omitempty"`
	Image      string `json:"image,omitempty" yaml:"image,omitempty"`
}

// RunSpec 运行约定。
type RunSpec struct {
	Port   int    `json:"port,omitempty" yaml:"port,omitempty"`
	Health string `json:"health,omitempty" yaml:"health,omitempty"`
}

var validTypes = map[string]bool{"fullstack": true, "backend": true, "frontend": true}

// validBackendLangs v2.1 支持的后端栈（新增栈需同步 stackkind 注册表；v2.2 增 python）。
var validBackendLangs = map[string]bool{"go": true, "node": true, "python": true}

// validCoverageFormats 覆盖率解析格式白名单。
var validCoverageFormats = map[string]bool{"go": true, "lcov": true}

// Parse 严格解析 + 校验；未知字段（KnownFields）与非法值均返回错误。
func Parse(b []byte) (*Manifest, error) {
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("project.yaml: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// Validate 契约校验。
func (m *Manifest) Validate() error {
	if m.Version < MinVersion || m.Version > Version {
		return fmt.Errorf("project.yaml: unsupported version %d (want %d..%d)", m.Version, MinVersion, Version)
	}
	if !validTypes[m.Type] {
		return fmt.Errorf("project.yaml: invalid type %q (want fullstack|backend|frontend)", m.Type)
	}
	// frontend-only 项目允许无后端；其余必须为受支持栈（v2.2：go|node|python）
	if m.Type != "frontend" && !validBackendLangs[m.Stack.Backend.Lang] {
		return fmt.Errorf("project.yaml: unsupported stack.backend.lang %q (want go|node|python)", m.Stack.Backend.Lang)
	}
	if m.CoverageGate < 0 || m.CoverageGate > 100 {
		return fmt.Errorf("project.yaml: coverage_gate out of range: %v", m.CoverageGate)
	}
	if f := m.Test.Coverage.Format; f != "" && !validCoverageFormats[f] {
		return fmt.Errorf("project.yaml: unsupported test.coverage.format %q (want go|lcov)", f)
	}
	return nil
}

// JSON 序列化为 projects.stack 缓存值。
func (m *Manifest) JSON() ([]byte, error) { return json.Marshal(m) }
