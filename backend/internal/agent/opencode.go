package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

// AgentInput / Output per architecture/api-contract.md
type AgentInput struct {
	TaskID      string            `json:"task_id"`
	SpecRefs    []string          `json:"spec_refs"`
	Context     map[string]any    `json:"context"`
	Constraints map[string]any    `json:"constraints"`
	// S3.5.2 修复循环：上一轮失败上下文（验证错误/apply 失败），非空时 Agent 携带失败原因重锻
	FailContext string `json:"fail_context,omitempty"`
	Attempt     int    `json:"attempt,omitempty"`
	// M-1 模型注入：为空时回落 forja/deepseek-v4-flash
	ModelProvider string `json:"model_provider,omitempty"`
	ModelID       string `json:"model_id,omitempty"`
}

type CodeChange struct {
	FilePath string `json:"file_path"`
	Diff     string `json:"diff"`
}

// FileChange 整文件交付（绿地首选）：全新文件输出完整内容，由 worker 直写——
// 比模型手搓 unified diff 稳健得多（corrupt patch 是 mimo 类模型的主要失败模式）。
type FileChange struct {
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
}

type TestChange struct {
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
}

type AgentOutput struct {
	CodeChanges    []CodeChange `json:"code_changes"`
	Files          []FileChange `json:"files,omitempty"`
	TestChanges    []TestChange `json:"test_changes"`
	ChangeLog      string       `json:"change_log"`
	ActionRequired []string     `json:"action_required"`
	Confidence     float64      `json:"confidence"`
}

type Adapter interface {
	Execute(ctx context.Context, in AgentInput) (AgentOutput, error)
}

type OpenCodeAdapter struct {
	BaseURL  string
	Username string
	Password string
	Client   *http.Client
}

func NewOpenCodeAdapter() *OpenCodeAdapter {
	url := os.Getenv("OPENCODE_URL")
	if url == "" {
		url = "http://localhost:4096"
	}
	user := os.Getenv("OPENCODE_USERNAME")
	if user == "" {
		user = "opencode"
	}
	pass := os.Getenv("OPENCODE_PASSWORD")
	if pass == "" {
		pass = "forja-dev"
	}
	return &OpenCodeAdapter{
		BaseURL:  url,
		Username: user,
		Password: pass,
		// 60s：session 创建/健康检查的建连+响应窗；prompt 单独用 5min client。
		// 10s 在宿主网络劣化期（DNS fake-ip 污染）间歇性打断建连，伪装成炉不可用
		Client:   &http.Client{Timeout: 60 * time.Second},
	}
}

func (a *OpenCodeAdapter) auth(req *http.Request) {
	if a.Username != "" || a.Password != "" {
		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(a.Username+":"+a.Password)))
	}
}

// health check
func (a *OpenCodeAdapter) Health(ctx context.Context) error {
	req, _ := http.NewRequestWithContext(ctx, "GET", a.BaseURL+"/global/health", nil)
	a.auth(req)
	resp, err := a.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("opencode health %d", resp.StatusCode)
	}
	return nil
}

// JSON Schema for structured output per opencode-research.md:4.5
var agentOutputSchema = map[string]any{
	"type": "object",
	"required": []string{"code_changes", "change_log", "confidence"},
	"properties": map[string]any{
		"code_changes": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object", "required": []string{"file_path", "diff"},
				"properties": map[string]any{"file_path": map[string]any{"type": "string"}, "diff": map[string]any{"type": "string"}},
			},
		},
		"test_changes": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object", "required": []string{"file_path", "content"},
				"properties": map[string]any{"file_path": map[string]any{"type": "string"}, "content": map[string]any{"type": "string"}},
			},
		},
		"change_log":      map[string]any{"type": "string"},
		"action_required": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"confidence":      map[string]any{"type": "number", "minimum": 0, "maximum": 1},
	},
}

// ErrUnavailable OpenCode 不可达（S3.5.3 执行真实性）：调用方必须置 FAILED，禁止静默 stub。
var ErrUnavailable = errors.New("opencode unavailable")

// ExecuteResult Execute 的用量版本：Output + 上游 usage（message 响应 info.tokens/cost，
// 与 SimpleChatDetailed 同端点同形状；缺失时为 0，由调用方如实入库）。
type ExecuteResult struct {
	Output    AgentOutput
	TokensIn  int
	TokensOut int
	Cost      float64
}

func (a *OpenCodeAdapter) Execute(ctx context.Context, in AgentInput) (AgentOutput, error) {
	res, err := a.ExecuteDetailed(ctx, in)
	if err != nil {
		return AgentOutput{}, err
	}
	return res.Output, nil
}

func (a *OpenCodeAdapter) ExecuteDetailed(ctx context.Context, in AgentInput) (ExecuteResult, error) {
	// 1. create session
	sessID, err := a.createSession(ctx, in.TaskID)
	if err != nil {
		return ExecuteResult{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	// 2. prompt with json_schema
	out, usage, err := a.prompt(ctx, sessID, in)
	if err != nil {
		// S3.5.2：出码失败（含无结构化输出）可重试，交由 runForge 携带上下文循环；
		// M0 残项（2026-09-12）：上游已回传的 usage 随错误返回，失败轮次也可如实计费
		return ExecuteResult{TokensIn: usage.tokensIn, TokensOut: usage.tokensOut, Cost: usage.cost}, err
	}
	return ExecuteResult{Output: out, TokensIn: usage.tokensIn, TokensOut: usage.tokensOut, Cost: usage.cost}, nil
}

// promptUsage prompt 轮次的上游用量（info.tokens/cost）。
type promptUsage struct {
	tokensIn  int
	tokensOut int
	cost      float64
}

func (a *OpenCodeAdapter) createSession(ctx context.Context, title string) (string, error) {
	body, _ := json.Marshal(map[string]string{"title": title})
	req, _ := http.NewRequestWithContext(ctx, "POST", a.BaseURL+"/session", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	a.auth(req)
	resp, err := a.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("create session %d: %s", resp.StatusCode, string(b))
	}
	var res struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		// try generic map
		var m map[string]any
		b, _ := io.ReadAll(resp.Body)
		_ = json.Unmarshal(b, &m)
		if v, ok := m["id"].(string); ok {
			return v, nil
		}
		return "", err
	}
	return res.ID, nil
}

func (a *OpenCodeAdapter) prompt(ctx context.Context, sessionID string, in AgentInput) (AgentOutput, promptUsage, error) {
	promptText := buildPrompt(in)
	// DeepSeek via Zen does not reliably support json_schema (500), use plain text and parse
	providerID := in.ModelProvider
	modelID := in.ModelID
	if providerID == "" {
		providerID = "forja"
	}
	if modelID == "" {
		modelID = "deepseek-v4-flash"
	}
	reqBody := map[string]any{
		"parts": []map[string]string{{"type": "text", "text": promptText}},
		"agent": "forja-build",
		"model": map[string]string{"providerID": providerID, "modelID": modelID},
	}
	b, _ := json.Marshal(reqBody)
	req, _ := http.NewRequestWithContext(ctx, "POST", a.BaseURL+"/session/"+sessionID+"/message", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	a.auth(req)
	// longer timeout for generation
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return AgentOutput{}, promptUsage{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		bb, _ := io.ReadAll(resp.Body)
		return AgentOutput{}, promptUsage{}, fmt.Errorf("prompt %d: %s", resp.StatusCode, string(bb))
	}
	var raw struct {
		Info struct {
			StructuredOutput AgentOutput `json:"structured_output"`
			Cost             float64     `json:"cost"`
			Tokens           struct {
				Input  int `json:"input"`
				Output int `json:"output"`
			} `json:"tokens"`
			Error *struct {
				Name string `json:"name"`
				Data struct {
					Message string `json:"message"`
				} `json:"data"`
			} `json:"error"`
		} `json:"info"`
		Parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"parts"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return AgentOutput{}, promptUsage{}, err
	}
	usage := promptUsage{tokensIn: raw.Info.Tokens.Input, tokensOut: raw.Info.Tokens.Output, cost: raw.Info.Cost}
	if raw.Info.Error != nil {
		// S3.5.2 仅 StructuredOutputError 属模型格式漂移、走修复循环重试；
		// 其余（欠费/鉴权/上游 4xx5xx）透传原始错误——吞掉会伪装成
		// "no structured output" 烧完 3 轮重试且无法排障（2026-09-05 审计实录：
		// zen 401 Insufficient balance 被吞，任务 ESCALATED 假象为格式漂移）
		if raw.Info.Error.Name == "StructuredOutputError" {
			return AgentOutput{}, usage, errors.New("agent: StructuredOutputError")
		}
		msg := raw.Info.Error.Data.Message
		if msg == "" {
			msg = raw.Info.Error.Name
		}
		return AgentOutput{}, usage, fmt.Errorf("agent error: %s", msg)
	}
	if raw.Info.StructuredOutput.ChangeLog != "" || len(raw.Info.StructuredOutput.CodeChanges) > 0 || len(raw.Info.StructuredOutput.Files) > 0 {
		return raw.Info.StructuredOutput, usage, nil
	}
	// plain text fallback: concat all text parts and try to extract JSON
	var sb strings.Builder
	for _, p := range raw.Parts {
		if p.Type == "text" && p.Text != "" {
			sb.WriteString(p.Text)
			sb.WriteString("\n")
		}
	}
	plain := sb.String()
	if plain != "" {
		if out := extractJSON(plain); out != nil {
			return *out, usage, nil
		}
	}
	// S3.5.2：无结构化输出属可重试（模型偶发格式漂移），交由 runForge 修复循环携带上下文重试
	return AgentOutput{}, usage, fmt.Errorf("agent: no structured output (raw %d bytes)", len(plain))
}

func extractJSON(s string) *AgentOutput {
	// try to find ```json ... ``` or raw { ... }
	re := regexp.MustCompile("```json\\s*([\\s\\S]*?)\\s*```")
	if m := re.FindStringSubmatch(s); len(m) > 1 {
		s = m[1]
	}
	// find first { to last }
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start >= 0 && end > start {
		candidate := s[start : end+1]
		var out AgentOutput
		if err := json.Unmarshal([]byte(candidate), &out); err == nil && (out.ChangeLog != "" || len(out.CodeChanges) > 0) {
			if out.Confidence == 0 {
				out.Confidence = 0.7
			}
			return &out
		}
	}
	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func buildPrompt(in AgentInput) string {
	// enrich context if empty: code_snippets + rules + history + §2b refs/anchors (backend/internal/agent/opencode.go:264)
	if len(in.Context) == 0 {
		if ctx, err := BuildContext(context.Background(), in.TaskID); err == nil && len(ctx) > 0 {
			if in.Context == nil {
				in.Context = ctx
			} else {
				for k, v := range ctx {
					in.Context[k] = v
				}
			}
		}
	} else {
		// partial enrich: ensure §2b regression/anchors even when caller pre-filled context (P0-2: 解嵌套，独立补全)
		needsRC := false
		needsAnchors := false
		if _, hasRC := in.Context["regression_constraints"]; !hasRC {
			needsRC = true
		}
		if _, hasAnchors := in.Context["anchors"]; !hasAnchors {
			needsAnchors = true
		}
		if needsRC || needsAnchors {
			if ctx, err := BuildContext(context.Background(), in.TaskID); err == nil {
				if needsRC {
					if rc, ok := ctx["regression_constraints"]; ok {
						in.Context["regression_constraints"] = rc
					}
				}
				if needsAnchors {
					if anc, ok := ctx["anchors"]; ok {
						in.Context["anchors"] = anc
					}
				}
				if in.Context["spec_refs_ids"] == nil {
					if refs, ok := ctx["spec_refs_ids"]; ok {
						in.Context["spec_refs_ids"] = refs
					}
				}
			}
		}
	}
	b, _ := json.Marshal(in)
	// include spec_refs expansion for better grounding
	var specDetails []string
	for _, ref := range in.SpecRefs {
		parts := strings.Split(ref, "#")
		pathPart := parts[0]
		candidates := []string{pathPart, "../" + pathPart, "/workspace/" + pathPart}
		for _, c := range candidates {
			if bb, err := os.ReadFile(c); err == nil {
				specDetails = append(specDetails, fmt.Sprintf("Spec %s:\n%s", ref, string(bb[:min(2000, len(bb))])))
				break
			}
		}
	}
	extra := ""
	if len(specDetails) > 0 {
		extra = "\nSpec 详情:\n" + strings.Join(specDetails, "\n---\n")
	}
	// §2b 三层防误改(2): 回归约束段落 — refs 关联 Spec 的 AC 转译为不得破坏清单
	if rcRaw, ok := in.Context["regression_constraints"]; ok {
		var lines []string
		switch v := rcRaw.(type) {
		case []string:
			lines = v
		case []any:
			for _, e := range v {
				if s, ok := e.(string); ok {
					lines = append(lines, s)
				}
			}
		}
		if len(lines) > 0 {
			extra += "\n\n【回归约束（不得破坏的既有行为）】\n以下为关联 Spec（refs）的验收标准，转译为不得破坏约束，任何改动不得使其失效，违背视为回归缺陷：\n"
			for _, l := range lines {
				extra += "- " + l + "\n"
			}
			extra += "提示：改动后须确保上述既有行为仍通过全量单测/e2e（S3.5 门禁）。"
		}
	}
	// §2b 三层防误改(1+2): 锚点收敛 — anchors 限定检索/修改范围，超范围需说明
	if ancRaw, ok := in.Context["anchors"]; ok {
		var anchors []string
		switch v := ancRaw.(type) {
		case []string:
			anchors = v
		case []any:
			for _, e := range v {
				if s, ok := e.(string); ok {
					anchors = append(anchors, s)
				}
			}
		}
		if len(anchors) > 0 {
			extra += "\n\n【代码锚点（允许修改范围）】\n本需求允许修改的文件范围（anchors，事前定位）:\n"
			for _, a := range anchors {
				extra += "- " + a + "\n"
			}
			extra += "要求：优先在上述文件内检索与修改；超出该范围的任何改动必须在 change_log 中单独说明理由，未说明视为误改（事中防护）。"
		}
	}
	// S3.5.2：修复轮次注入上轮失败上下文
	if in.FailContext != "" {
		extra += fmt.Sprintf("\n\n【第 %d 次重锻】上一轮验证/应用失败，必须修正以下问题后再输出:\n%s", in.Attempt, in.FailContext)
	}
	// 仓库清单接地：diff 上下文必须与真实仓状态匹配，否则 git apply 必然失败
	if rfRaw, ok := in.Context["repo_files"]; ok {
		var files []string
		switch v := rfRaw.(type) {
		case []string:
			files = v
		case []any:
			for _, e := range v {
				if s, ok := e.(string); ok {
					files = append(files, s)
				}
			}
		}
		if len(files) > 0 {
			extra += "\n\n【仓库现存文件清单（git ls-files，以此为准）】\n" + strings.Join(files, "\n") +
				"\n要求：对清单中不存在的文件，diff 必须用新建文件格式（--- /dev/null 换 +++ b/<path> 换 @@ -0,0 +1,N @@），" +
				"禁止为不存在的文件生成带上下文行的\"修改型\"补丁；对已有文件的修改须与真实内容对齐。"
			// 绿地项目提示：脚手架上没有任何 go.mod 时明确告知从零创建，
			// 阻断模型自行套用"典型 Go Web 项目"结构幻觉（2026-09-06 实录）
			hasGoMod := false
			for _, f := range files {
				if strings.HasSuffix(f, "go.mod") {
					hasGoMod = true
					break
				}
			}
			if !hasGoMod {
				extra += "\n注意：该仓库是全新绿地项目（无 go.mod、无既有代码结构）——所有文件都由你从零创建，" +
					"目录结构由你按最小可用原则设计，禁止假设存在任何未列出的既有文件或框架骨架。"
			}
		}
	}
	// F1.b 项目规范（仓内 AGENTS.md）：优先级高于全局 rules，冲突以项目为准
	if pa, ok := in.Context["project_agents"].(string); ok && strings.TrimSpace(pa) != "" {
		extra += "\n\n【项目规范（仓库 AGENTS.md，最高优先级）】\n" +
			"以下为项目级规范（由项目维护，随仓版本化），优先级高于通用规则；与之冲突时以本项目规范为准：\n" + pa
	}
	// S3.5 硬化：仓库根=项目根；禁止文件工具；只允许统一 diff；few-shot 锚定格式
	return fmt.Sprintf(`你是 FORJA 代码锻造器。对给定项目仓产出**代码交付 JSON**。

【铁律】
1. 你的最终回答必须是**一个 JSON 对象**，以 { 开头 } 结尾。禁止任何解释文字、markdown 代码块、前后缀。
2. 不要使用/提及任何文件写入工具——你无法触碰文件系统，唯一交付物是 JSON。
3. file_path 相对仓库根（如 "pkg/calc/calc.go"），绝不加 backend/ 或 frontend/ 前缀。
4. 两种交付方式，按文件性质选择：
   - **新建文件（仓库清单中不存在）→ 用 files 字段输出完整文件内容**（file_path + content 全文，最稳健，优先使用）；
   - 仅对已存在文件的修改 → 用 code_changes 的 diff（标准 unified diff，--- a/<path> 换 +++ b/<path> 换 @@ hunks）。
   - test_changes 用于测试文件（file_path + content 完整内容）。
5. 若需求包含"修复缺陷"，先依据 spec_files 中现有代码定位错误行再生成最小修改；同时补充 _test.go 测试。
6. action_required 仅在确需人工决策时非空；正常完成必须为 []。

【输出示例（结构示意）】
{"files":[{"file_path":"pkg/board/board.go","content":"package board\n\ntype Board struct{ ... }\n"}],"code_changes":[{"file_path":"main.go","diff":"--- a/main.go\n+++ b/main.go\n@@ -1,3 +1,3 @@\n-func F() int { return 0 }\n+func F() int { return 42 }\n"}],"test_changes":[],"change_log":"一句话总结","action_required":[],"confidence":0.9}

【任务输入】%s%s`, string(b), extra)
}

type ChatDetail struct {
	SessionID string `json:"session_id"`
	ModelID   string `json:"model_id"`
	ProviderID string `json:"provider_id"`
	BaseURL   string `json:"base_url"`
	Parts     []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"parts"`
	Info struct {
		StructuredOutput AgentOutput `json:"structured_output"`
		ModelID   string `json:"modelID"`
		ProviderID string `json:"providerID"`
		Cost      float64 `json:"cost"`
		Tokens    struct {
			Input  int `json:"input"`
			Output int `json:"output"`
		} `json:"tokens"`
		Error *struct {
			Name string `json:"name"`
			Data any    `json:"data"`
		} `json:"error"`
	} `json:"info"`
	RawJSON string `json:"raw_json"`
}

func (a *OpenCodeAdapter) SimpleChat(ctx context.Context, prompt, providerID, modelID string) (string, error) {
	reply, _, err := a.SimpleChatDetailed(ctx, prompt, providerID, modelID)
	return reply, err
}

func (a *OpenCodeAdapter) SimpleChatDetailed(ctx context.Context, prompt, providerID, modelID string) (string, *ChatDetail, error) {
	if providerID == "" {
		providerID = "forja"
	}
	if modelID == "" {
		modelID = "deepseek-v4-flash"
	}
	sessID, err := a.createSession(ctx, "test-chat")
	if err != nil {
		return "", nil, err
	}
	reqBody := map[string]any{
		"parts": []map[string]string{{"type": "text", "text": prompt}},
		"model": map[string]string{"providerID": providerID, "modelID": modelID},
	}
	b, _ := json.Marshal(reqBody)
	req, _ := http.NewRequestWithContext(ctx, "POST", a.BaseURL+"/session/"+sessID+"/message", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	a.auth(req)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	bb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", nil, fmt.Errorf("simple chat %d: %s", resp.StatusCode, string(bb))
	}
	var raw ChatDetail
	_ = json.Unmarshal(bb, &raw)
	raw.SessionID = sessID
	raw.ModelID = modelID
	raw.ProviderID = providerID
	raw.BaseURL = a.BaseURL
	raw.RawJSON = string(bb)
	if len(raw.RawJSON) > 2000 {
		raw.RawJSON = raw.RawJSON[:2000]
	}
	if raw.Info.StructuredOutput.ChangeLog != "" {
		return raw.Info.StructuredOutput.ChangeLog, &raw, nil
	}
	var sb strings.Builder
	for _, p := range raw.Parts {
		if p.Type == "text" && p.Text != "" {
			sb.WriteString(p.Text)
			sb.WriteString("\n")
		}
	}
	plain := strings.TrimSpace(sb.String())
	if plain == "" {
		// 保留原始 info.error 或 parts 为空时的诊断信息
		if raw.Info.Error != nil {
			plain = fmt.Sprintf("opencode error: %s", raw.Info.Error.Name)
		} else {
			plain = ""
		}
	}
	return plain, &raw, nil
}

var DefaultAdapter Adapter = NewOpenCodeAdapter()
