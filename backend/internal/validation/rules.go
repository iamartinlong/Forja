package validation

import (
	_ "embed"
	"encoding/json"
	"log"
	"strings"
)

// ruleConfig 门禁规则参数，外置于 rules.json（go:embed）。
// 调整关键词/阈值改配置即可，无需改代码；解析失败时退回内置默认保证门禁可用。
type ruleConfig struct {
	ClarificationMarker  string   `json:"clarification_marker"`
	BSHowKeywords        []string `json:"bs_how_keywords"`
	UntestablePhrases    []string `json:"untestable_phrases"`
	ExceptionMarkers     []string `json:"exception_markers"`
	NonFunctionalMarkers []string `json:"non_functional_markers"`
}

//go:embed rules.json
var rulesJSON []byte

var rules = loadRules()

func defaultRules() ruleConfig {
	return ruleConfig{
		ClarificationMarker:  "NEEDS CLARIFICATION",
		BSHowKeywords:        []string{"React", "Fiber", "Redis", "Postgres", "pgvector"},
		UntestablePhrases:    []string{"体验良好"},
		ExceptionMarkers:     []string{"400", "exception", "异常", "TITLE_REQUIRED"},
		NonFunctionalMarkers: []string{"performance", "concurrency", "并发", "性能"},
	}
}

func loadRules() ruleConfig {
	var cfg ruleConfig
	if err := json.Unmarshal(rulesJSON, &cfg); err != nil {
		log.Printf("validation: rules.json 解析失败，使用内置默认规则: %v", err)
		return defaultRules()
	}
	def := defaultRules()
	if cfg.ClarificationMarker == "" {
		cfg.ClarificationMarker = def.ClarificationMarker
	}
	if len(cfg.BSHowKeywords) == 0 {
		cfg.BSHowKeywords = def.BSHowKeywords
	}
	if len(cfg.UntestablePhrases) == 0 {
		cfg.UntestablePhrases = def.UntestablePhrases
	}
	if len(cfg.ExceptionMarkers) == 0 {
		cfg.ExceptionMarkers = def.ExceptionMarkers
	}
	if len(cfg.NonFunctionalMarkers) == 0 {
		cfg.NonFunctionalMarkers = def.NonFunctionalMarkers
	}
	return cfg
}

// BSViolation 返回 BS 内容中出现的第一个 HOW 关键词（宪法 WHAT/WHY 不含 HOW），
// 无违规时返回空串。供 API 层 create/update 前置校验复用。
func BSViolation(content string) string {
	for _, kw := range rules.BSHowKeywords {
		if kw != "" && strings.Contains(content, kw) {
			return kw
		}
	}
	return ""
}

func hasAny(raw string, markers []string) bool {
	for _, m := range markers {
		if m != "" && strings.Contains(raw, m) {
			return true
		}
	}
	return false
}
