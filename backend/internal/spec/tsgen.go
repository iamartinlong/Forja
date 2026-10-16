package spec

import (
	"encoding/json"
	"fmt"
	"strings"
)

// GenerateTSDraft from BS content (AC) to TS yaml struct
func GenerateTSDraft(bsContent json.RawMessage, title string) (map[string]any, error) {
	var bs map[string]any
	if err := json.Unmarshal(bsContent, &bs); err != nil {
		return nil, fmt.Errorf("invalid BS json: %w", err)
	}
	ac, _ := bs["acceptance_criteria"].([]any)
	scenarios := []map[string]string{}
	for i, item := range ac {
		m, _ := item.(map[string]any)
		given, _ := m["given"].(string)
		when, _ := m["when"].(string)
		then, _ := m["then"].(string)
		if given == "" {
			given = "用户已就绪"
		}
		if when == "" {
			when = fmt.Sprintf("执行 %s", title)
		}
		if then == "" {
			then = "操作成功"
		}
		scenarios = append(scenarios, map[string]string{
			"name":  fmt.Sprintf("AC-%d: %s", i+1, truncate(then, 20)),
			"given": given,
			"when":  when,
			"then":  then,
		})
	}
	// ensure exception coverage: if no 400 scenario, add one
	hasException := false
	for _, s := range scenarios {
		if strings.Contains(s["then"], "400") || strings.Contains(s["then"], "失败") {
			hasException = true
			break
		}
	}
	if !hasException {
		scenarios = append(scenarios, map[string]string{
			"name":  "异常: 参数缺失",
			"given": "用户已登录",
			"when":  fmt.Sprintf("POST /api with invalid %s", title),
			"then":  "status 400 且 返回错误码",
		})
	}
	return map[string]any{
		"test_suite": fmt.Sprintf("%s - Auto", title),
		"scenarios":  scenarios,
		"assertions": []map[string]any{{"type": "status_code", "expected": 200}},
		"coverage":   map[string]int{"line": 80},
	}, nil
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
