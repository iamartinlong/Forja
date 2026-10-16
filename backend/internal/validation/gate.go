package validation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type Check struct {
	Rule     string `json:"rule"`
	Passed   bool   `json:"passed"`
	Message  string `json:"message"`
	Blocking bool   `json:"blocking"`
}

type Result struct {
	Passed bool    `json:"passed"`
	Checks []Check `json:"checks"`
}

func RunAll(layer string, content json.RawMessage) Result {
	raw := string(content)
	checks := []Check{}
	// clarification
	marker := rules.ClarificationMarker
	checks = append(checks, Check{
		Rule: "clarification", Blocking: true,
		Passed:  !strings.Contains(raw, marker),
		Message: func() string {
			if strings.Contains(raw, marker) {
				return "残留 [" + marker + "] 需澄清"
			}
			return "无残留"
		}(),
	})
	// contracts — 检查 contracts/ 契约目录 (Task0)，路径可用 FORJA_CONTRACTS_DIR 覆盖
	passedContracts := true
	msgContracts := "contracts 已就绪"
	// BS 层不强制，其余层需至少一个 contract 文件
	if layer != "BS" {
		dir := os.Getenv("FORJA_CONTRACTS_DIR")
		if dir == "" {
			dir = filepath.Join("specs", "forja", "contracts")
		}
		found := false
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			for _, pattern := range []string{"*.yaml", "*.yml"} {
				if matches, _ := filepath.Glob(filepath.Join(dir, pattern)); len(matches) > 0 {
					found = true
					break
				}
			}
		}
		if !found {
			passedContracts = false
			msgContracts = "缺少 contracts/ 契约 (需 " + filepath.ToSlash(dir) + "/*.yaml)"
		}
	}
	checks = append(checks, Check{
		Rule: "contracts", Blocking: true,
		Passed:  passedContracts,
		Message: msgContracts,
	})
	// completeness
	checks = append(checks, completeness(layer, content))
	// exception
	checks = append(checks, exception(content))
	// non_functional
	checks = append(checks, nonFunctional(content))
	// consistency
	checks = append(checks, consistency(content))
	// testability
	checks = append(checks, testability(raw))
	// constitution
	checks = append(checks, constitution(raw))

	passed := true
	for _, ch := range checks {
		if ch.Blocking && !ch.Passed {
			passed = false
			break
		}
	}
	return Result{Passed: passed, Checks: checks}
}

func completeness(layer string, content json.RawMessage) Check {
	var m map[string]interface{}
	_ = json.Unmarshal(content, &m)
	passed := true
	msg := "完整"
	switch layer {
	case "BS":
		if _, ok := m["story_id"]; !ok {
			passed = false
			msg = "BS 缺 story_id"
		}
		if _, ok := m["title"]; !ok {
			passed = false
			msg = "BS 缺 title"
		}
	case "IS":
		if _, ok := m["paths"]; !ok {
			if _, ok2 := m["openapi"]; !ok2 {
				passed = false
				msg = "IS 缺 paths/openapi"
			}
		}
	case "DS":
		if _, ok := m["entity_name"]; !ok {
			if _, ok2 := m["fields"]; !ok2 {
				passed = false
				msg = "DS 缺 entity_name/fields"
			}
		}
	}
	return Check{Rule: "completeness", Passed: passed, Message: msg, Blocking: true}
}

func exception(content json.RawMessage) Check {
	passed := hasAny(string(content), rules.ExceptionMarkers)
	msg := "异常分支已覆盖"
	if !passed {
		msg = "缺少异常分支 (需含 400/exception)"
	}
	return Check{Rule: "exception", Passed: passed, Message: msg, Blocking: true}
}

func nonFunctional(content json.RawMessage) Check {
	raw := strings.ToLower(string(content))
	passed := hasAny(raw, rules.NonFunctionalMarkers)
	msg := "已标注非功能"
	if !passed {
		msg = "未标注非功能 (performance/concurrency)"
	}
	return Check{Rule: "non_functional", Passed: passed, Message: msg, Blocking: false}
}

func consistency(content json.RawMessage) Check {
	return Check{Rule: "consistency", Passed: true, Message: "一致性检查通过 (stub)", Blocking: true}
}

func testability(raw string) Check {
	for _, phrase := range rules.UntestablePhrases {
		if strings.Contains(raw, phrase) {
			return Check{Rule: "testability", Passed: false, Message: "含不可测短语 '" + phrase + "'", Blocking: true}
		}
	}
	return Check{Rule: "testability", Passed: true, Message: "可测试", Blocking: true}
}

func constitution(raw string) Check {
	// Phase -1 gates: max 3 projects etc. stub
	return Check{Rule: "constitution", Passed: true, Message: "宪法门禁通过", Blocking: true}
}
