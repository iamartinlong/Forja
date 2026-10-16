package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"forja/backend/internal/model"
)

// BuildContext packs code_snippets + rules + history for AgentInput
func BuildContext(ctx context.Context, taskID string) (map[string]any, error) {
	out := map[string]any{}
	// 1. spec_refs' content
	var specID string
	_ = model.Pool.QueryRow(ctx, `SELECT spec_id FROM tasks WHERE id=$1`, taskID).Scan(&specID)
	if specID != "" {
		var content json.RawMessage
		err := model.Pool.QueryRow(ctx, `SELECT content FROM spec_versions WHERE spec_id=$1 ORDER BY created_at DESC LIMIT 1`, specID).Scan(&content)
		if err == nil {
			out["spec_content"] = string(content)
		}
	}
	// 2. code_snippets: 只取最近一次 SUCCESS 执行之后的产物 diff。
	// 此前无过滤——失败轮的幻觉 diff（如对不存在文件打补丁）会回灌给下一轮，
	// 教模型坚持错误路径（2026-09-06 自中毒实录：连续多轮 patch 同一不存在文件）
	var projectID string
	_ = model.Pool.QueryRow(ctx, `SELECT project_id FROM tasks WHERE id=$1`, taskID).Scan(&projectID)
	if projectID != "" {
		rows, err := model.Pool.Query(ctx, `SELECT cc.file_path, LEFT(cc.diff,500) FROM code_changes cc
			WHERE cc.task_id IN (SELECT id FROM tasks WHERE project_id=$1)
			AND cc.created_at >= COALESCE((
				SELECT MAX(ae.started_at) FROM agent_executions ae
				WHERE ae.task_id IN (SELECT id FROM tasks WHERE project_id=$1) AND ae.status='SUCCESS'
			), to_timestamp(0))
			ORDER BY cc.created_at DESC LIMIT 3`, projectID)
		if err == nil {
			defer rows.Close()
			snippets := []map[string]string{}
			for rows.Next() {
				var fp, diff string
				_ = rows.Scan(&fp, &diff)
				snippets = append(snippets, map[string]string{"path": fp, "content": diff})
			}
			if len(snippets) > 0 {
				out["code_snippets"] = snippets
			}
		}
	}
	// 3. rules 6件
	rules := loadRules()
	if len(rules) > 0 {
		out["rules"] = rules
	}
	// 4. history: recent agent_executions for same spec
	if specID != "" {
		rows, err := model.Pool.Query(ctx, `SELECT output FROM agent_executions WHERE task_id IN (SELECT id FROM tasks WHERE spec_id=$1) ORDER BY started_at DESC LIMIT 2`, specID)
		if err == nil {
			defer rows.Close()
			hist := []string{}
			for rows.Next() {
				var outp string
				_ = rows.Scan(&outp)
				if outp != "" {
					n := len(outp)
					if n > 300 {
						n = 300
					}
					hist = append(hist, outp[:n])
				}
			}
			if len(hist) > 0 {
				out["history"] = hist
			}
		}
	}
	// 5. §2b incremental Spec relations: refs(回归约束) + anchors(检索收敛)
	if specID != "" && model.Pool != nil {
		var refsJSON, anchorsJSON json.RawMessage
		if err := model.Pool.QueryRow(ctx, `SELECT COALESCE(refs,'[]'::jsonb), COALESCE(anchors,'[]'::jsonb) FROM specs WHERE id=$1`, specID).Scan(&refsJSON, &anchorsJSON); err == nil {
			var refs []string
			_ = json.Unmarshal(refsJSON, &refs)
			var anchors []string
			_ = json.Unmarshal(anchorsJSON, &anchors)
			if len(anchors) > 0 {
				out["anchors"] = anchors
			}
			if len(refs) > 0 {
				regression := []string{}
				for _, refID := range refs {
					refID = strings.TrimSpace(refID)
					if refID == "" {
						continue
					}
					var refContent json.RawMessage
					var refTitle string
					err := model.Pool.QueryRow(ctx, `SELECT s.title, sv.content FROM specs s JOIN spec_versions sv ON sv.spec_id=s.id WHERE s.id=$1 ORDER BY sv.created_at DESC LIMIT 1`, refID).Scan(&refTitle, &refContent)
					if err != nil {
						continue
					}
					acs := extractACs(string(refContent))
					if len(acs) == 0 {
						// fallback to raw content snippet if no structured AC
						snippet := string(refContent)
						if len(snippet) > 400 {
							snippet = snippet[:400] + "..."
						}
						regression = append(regression, fmt.Sprintf("关联Spec %s (%s): %s", refID[:8], refTitle, snippet))
					} else {
						for _, ac := range acs {
							regression = append(regression, fmt.Sprintf("关联Spec %s (%s): %s", refID[:8], refTitle, ac))
						}
					}
				}
				if len(regression) > 0 {
					out["regression_constraints"] = regression
				}
				// also keep raw refs for prompt scope
				out["spec_refs_ids"] = refs
			}
		}
	}
	return out, nil
}

// extractACs parses spec content JSON and returns GWT strings (backend/internal/agent/context.go:74)
func extractACs(content string) []string {
	var obj map[string]any
	if err := json.Unmarshal([]byte(content), &obj); err != nil {
		return nil
	}
	raw, ok := obj["acceptance_criteria"]
	if !ok {
		return nil
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := []string{}
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		given, _ := m["given"].(string)
		when, _ := m["when"].(string)
		then, _ := m["then"].(string)
		// support alternative keys ac / criteria string?
		if then == "" {
			if t, ok := m["then"].(string); ok {
				then = t
			} else if t, ok := m["criteria"].(string); ok {
				then = t
			} else {
				continue
			}
		}
		line := strings.TrimSpace(fmt.Sprintf("Given %s When %s Then %s", strings.TrimSpace(given), strings.TrimSpace(when), strings.TrimSpace(then)))
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func loadRules() []string {
	candidates := []string{"rules", "../rules", "/workspace/rules", "backend/rules"}
	var dir string
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			dir = c
			break
		}
	}
	if dir == "" {
		return nil
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "*.yaml"))
	var out []string
	for _, m := range matches {
		b, err := os.ReadFile(m)
		if err == nil {
			n := len(b)
			if n > 800 {
				n = 800
			}
			out = append(out, fmt.Sprintf("%s:\n%s", filepath.Base(m), string(b[:n])))
		}
		if len(out) >= 6 {
			break
		}
	}
	return out
}


