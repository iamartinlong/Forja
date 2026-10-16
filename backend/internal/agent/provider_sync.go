package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"forja/backend/internal/crypto"
	"forja/backend/internal/model"
)

// SyncOpencodeProviders 将 model_providers 表的启用模型同步到 opencode 的 provider 配置
// 采用 PATCH /config 动态注入，避免 opencode.json 静态文件与炉池选择脱节
// 合理方式：炉池选择的模型即 provider_id/model_id，opencode 需按需具备该 provider 的 baseURL/apiKey
// 本函数在 model_provider CRUD 后及服务启动时调用，失败仅日志不阻塞主流程
func SyncOpencodeProviders(ctx context.Context) {
	if model.Pool == nil {
		return
	}
	// 读取所有 provider
	rows, err := model.Pool.Query(ctx, `SELECT provider_id, display_name, base_url, auth_mode, COALESCE(api_key_ref,''), models::text, COALESCE(headers::text,'[]') FROM model_providers`)
	if err != nil {
		log.Printf("sync opencode providers: query failed: %v", err)
		return
	}
	defer rows.Close()
	type providerCfg struct {
		NPM     string            `json:"npm"`
		Name    string            `json:"name"`
		Options map[string]string `json:"options"`
		Models  map[string]any    `json:"models"`
	}
	providersAny := map[string]any{}
	// 保留 forja 默认（若 DB 未覆盖则文件已有，此处补齐以防 PATCH 覆盖丢失）
	// 读取现有 opencode 配置中的 forja 以防被覆盖（最佳是全量 PATCH 合并语义，缺失的会保留）
	for rows.Next() {
		var pid, displayName, baseURL, authMode, apiKeyRef, modelsText, headersText string
		if err := rows.Scan(&pid, &displayName, &baseURL, &authMode, &apiKeyRef, &modelsText, &headersText); err != nil {
			continue
		}
		// 解密
		if apiKeyRef != "" {
			if dec, decErr := crypto.Decrypt(apiKeyRef); decErr == nil && dec != "" {
				apiKeyRef = dec
			}
		}
		var models []struct {
			ModelID     string `json:"model_id"`
			DisplayName string `json:"display_name"`
			Enabled     *bool  `json:"enabled"`
		}
		_ = json.Unmarshal([]byte(modelsText), &models)
		// 仅同步启用模型，无模型则跳过（防止空 provider）
		modelMap := map[string]any{}
		for _, m := range models {
			if m.Enabled != nil && !*m.Enabled {
				continue
			}
			if strings.TrimSpace(m.ModelID) == "" {
				continue
			}
			modelMap[m.ModelID] = map[string]any{"name": m.DisplayName}
			if modelMap[m.ModelID].(map[string]any)["name"] == "" {
				modelMap[m.ModelID] = map[string]any{"name": m.ModelID}
			}
		}
		if len(modelMap) == 0 {
			// 若无启用模型仍需注册 provider 以便测试探活，但模型列表空会导致调用 UnknownError
			// 仍注册空 models，opencode 会在调用时 500，提示配置问题优于静默
		}
		opts := map[string]string{}
		// baseURL 优先来自 DB，否则 fallback
		if strings.TrimSpace(baseURL) != "" {
			opts["baseURL"] = baseURL
		}
		// apiKey 仅 Bearer 需要，Header/None 下可空或走 headers
		if authMode == "Bearer" && strings.TrimSpace(apiKeyRef) != "" {
			opts["apiKey"] = apiKeyRef
		} else if authMode == "Bearer" && strings.TrimSpace(apiKeyRef) == "" {
			// 无 key 的 Bearer 仍注册，但调用会 401，状态 healthy 探活会标 unhealthy
			opts["apiKey"] = ""
		}
		// headers 暂不透传到 opencode options（opencode 插件可注入），保留扩展点
		_ = headersText
		providersAny[pid] = providerCfg{
			NPM:     "@ai-sdk/openai-compatible",
			Name:    displayName,
			Options: opts,
			Models:  modelMap,
		}
	}
	if len(providersAny) == 0 {
		return
	}
	// 先 GET 现有 provider 以做合并（避免 PATCH 覆盖丢失 forja 等静态 provider）
	baseURL := os.Getenv("OPENCODE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:4096"
	}
	baseURL = strings.TrimSuffix(baseURL, "/")
	user := os.Getenv("OPENCODE_USERNAME")
	if user == "" {
		user = "opencode"
	}
	pass := os.Getenv("OPENCODE_PASSWORD")
	if pass == "" {
		pass = os.Getenv("OPENCODE_SERVER_PASSWORD")
	}
	if pass == "" {
		pass = "forja-dev"
	}
	// 合并现有 provider（GET /config）再 PATCH，避免覆盖静态 forja
	// 删除语义：DB 中已删除的 provider（除 forja 外）需从 opencode 中剔除，否则 PATCH 合并会残留
	merged := map[string]any{}
	// 先取现有
	existingProvider := map[string]any{}
	{
		reqGet, _ := http.NewRequestWithContext(ctx, "GET", baseURL+"/config", nil)
		reqGet.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(user+":"+pass)))
		clientGet := &http.Client{Timeout: 5 * time.Second}
		if respGet, errGet := clientGet.Do(reqGet); errGet == nil {
			var cfg struct {
				Provider map[string]any `json:"provider"`
			}
			_ = json.NewDecoder(respGet.Body).Decode(&cfg)
			respGet.Body.Close()
			for k, v := range cfg.Provider {
				existingProvider[k] = v
				merged[k] = v
			}
		}
	}
	for k, v := range providersAny {
		merged[k] = v
	}
	// 删除已不在 DB 的 provider（保留 forja 静态）
	for k := range existingProvider {
		if k == "forja" {
			continue
		}
		if _, ok := providersAny[k]; !ok {
			delete(merged, k)
		}
	}
	body, _ := json.Marshal(map[string]any{"provider": merged})
	req, _ := http.NewRequestWithContext(ctx, "PATCH", baseURL+"/config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(user+":"+pass)))
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("sync opencode providers: PATCH failed: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		log.Printf("sync opencode providers: PATCH status %d", resp.StatusCode)
		return
	}
	log.Printf("sync opencode providers: synced %d providers", len(providersAny))
	// 同步写回 opencode.json 文件以便重启后保持
	if err := syncOpencodeJSONFile(merged); err != nil {
		log.Printf("sync opencode providers: file sync skipped: %v", err)
	} else {
		// 文件已更新，重启 opencode 使配置生效（配置为文件驱动，PATCH 在 :ro 时不持久）
		go restartAndVerifyOpencode(baseURL, user, pass)
	}
}

func restartOpencode() {
	host := os.Getenv("DOCKER_HOST")
	if host == "" {
		host = "http://socket-proxy:2375"
	}
	host = strings.TrimSuffix(host, "/")
	// 部署抖动期 opencode 容器可能正被重建：轮询重试（2026-09-07 实测：
	// api 启动同步时 opencode 尚不存在，单次尝试 "no container found" 后
	// 配置写回无人重启，旧进程读新挂载 ENODATA，锻造链断 1h+ 才被发现）。
	for attempt := 0; attempt < 6; attempt++ {
		// 尝试常见容器名
		for _, name := range []string{"superpm-opencode-1", "opencode", "superpm_opencode_1"} {
			req, _ := http.NewRequest("POST", host+"/containers/"+name+"/restart?t=5", nil)
			client := &http.Client{Timeout: 5 * time.Second}
			if resp, err := client.Do(req); err == nil {
				resp.Body.Close()
				if resp.StatusCode < 300 {
					log.Printf("opencode restarted via %s", name)
					return
				}
			}
		}
		time.Sleep(10 * time.Second)
	}
	log.Printf("opencode restart: no container found")
}

// restartAndVerifyOpencode 重启后用真探针验收 session 创建（ENODATA 闭环）：
// 文件写回 + 容器重建存在竞态——写后启动的 bun 进程读单文件挂载可能 ENODATA，
// 此时 /global/health 仍报 healthy，只有建 session 触发 InstanceBootstrap→
// Config.getGlobal→readFile 才会 500（2026-09-07 锻造链静默断 1h+ 实录）。
// 验收失败打 ERROR（进日志告警）而非静默；探针 session 建后即删，不留垃圾。
func restartAndVerifyOpencode(baseURL, user, pass string) {
	restartOpencode()
	baseURL = strings.TrimSuffix(baseURL, "/")
	auth := "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		if verifyOpencodeSession(baseURL, auth) {
			log.Printf("sync opencode providers: session creation verified")
			return
		}
		time.Sleep(10 * time.Second)
	}
	log.Printf("ERROR sync opencode providers: session creation failing 120s after sync+restart (likely ENODATA mount race) — manual fix: docker restart superpm-opencode-1")
}

func verifyOpencodeSession(baseURL, auth string) bool {
	body, _ := json.Marshal(map[string]string{"title": "forja-config-verify"})
	req, _ := http.NewRequest("POST", baseURL+"/session", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", auth)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		return false
	}
	var res struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&res)
	if res.ID != "" {
		dreq, _ := http.NewRequest("DELETE", baseURL+"/session/"+res.ID, nil)
		dreq.Header.Set("Authorization", auth)
		if dresp, derr := client.Do(dreq); derr == nil {
			dresp.Body.Close()
		}
	}
	return true
}

func syncOpencodeJSONFile(providers map[string]any) error {
	// 尝试定位宿主 opencode.json 路径
	candidates := []string{
		"opencode.json",
		"./opencode.json",
		"../opencode.json",
		"/workspace/opencode.json",
		"D:/opencode/SuperPM/opencode.json",
	}
	var path string
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			path = p
			break
		}
	}
	if path == "" {
		return fmt.Errorf("opencode.json not found")
	}
	// 读取现有文件
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var cfg map[string]any
	if err := json.Unmarshal(b, &cfg); err != nil {
		return err
	}
	// 以 providers 为权威（已含删除），重建 file 的 provider 段，保留 forja 若缺失
	existing, _ := cfg["provider"].(map[string]any)
	if existing == nil {
		existing = map[string]any{}
	}
	// 先以 providers 重建（已正确删除）
	newProvider := map[string]any{}
	for k, v := range providers {
		b2, _ := json.Marshal(v)
		var m map[string]any
		_ = json.Unmarshal(b2, &m)
		newProvider[k] = m
	}
	// 保留 forja 若 DB 未覆盖但文件已有（providers 已含 forja 的静态回退，此处兜底）
	if _, ok := newProvider["forja"]; !ok {
		if fj, ok := existing["forja"]; ok {
			newProvider["forja"] = fj
		}
	}
	cfg["provider"] = newProvider
	// 机密脱敏（2026-09-07）：newProvider 来自 PATCH 合并后的实时配置，含 DB 解密
	// 出来的 live key——直接落盘会把真密钥写进被跟踪的 opencode.json，抵消 key
	// 中性化（5778be4e），且改写与容器启动竞态曾致 ENODATA（09-07 晨）。
	// 文件只是重启 fallback：live key 每次启动由 DB 经 PATCH 重注。落盘规则：
	// live key 与某已知 env 值一致 → 还原为 {env:VAR} 占位符（与提交态一致，
	// 使改写后文件字节稳定、无 M 噪音）；否则清空（永不落 live key）。
	envKeys := map[string]string{
		"FORJA_API_KEY":     os.Getenv("FORJA_API_KEY"),
		"OPENAI_API_KEY":    os.Getenv("OPENAI_API_KEY"),
		"ANTHROPIC_API_KEY": os.Getenv("ANTHROPIC_API_KEY"),
	}
	for _, v := range newProvider {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		opts, ok := m["options"].(map[string]any)
		if !ok {
			continue
		}
		k, _ := opts["apiKey"].(string)
		if k == "" || strings.HasPrefix(k, "{env:") {
			continue
		}
		placeholder := ""
		for env, val := range envKeys {
			if val != "" && k == val {
				placeholder = "{env:" + env + "}"
				break
			}
		}
		opts["apiKey"] = placeholder
	}
	out, _ := json.MarshalIndent(cfg, "", "  ")
	// 写回（宿主为 :ro 时会失败，忽略）
	return os.WriteFile(path, out, 0644)
}
