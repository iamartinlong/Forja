package api

import (
	"context"
	"encoding/json"
	"log"
	"log/slog"
	"os"
	"strings"
	"time"

	"forja/backend/internal/agent"

	"forja/backend/internal/api/respond"
	"forja/backend/internal/auth"
	"forja/backend/internal/crypto"
	"forja/backend/internal/furnace"
	idgen "forja/backend/internal/id"
	"forja/backend/internal/model"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
)

func RegisterFurnaceRoutes(r fiber.Router) {
	r.Get("/furnaces", auth.Require("user.list"), listFurnaces)
	r.Get("/furnaces/:id", auth.Require("user.list"), getFurnace)
	r.Post("/furnaces", auth.Require("user.create"), createFurnace)
	r.Patch("/furnaces/:id", auth.Require("user.create"), patchFurnace)
	r.Delete("/furnaces/:id", auth.Require("user.create"), deleteFurnace)
	r.Post("/furnaces/:id/recover", auth.Require("user.create"), recoverFurnace)
	r.Post("/furnaces/:id/test-chat", auth.Require("user.create"), testFurnaceChat)
	r.Post("/furnaces/test-preview-chat", auth.Require("user.create"), testFurnaceChatPreview)
}

type createFurnaceReq struct {
	Name         string  `json:"name"`
	BaseURL      string  `json:"base_url"`
	AuthRef      string  `json:"auth_ref"`
	Capacity     *int    `json:"capacity"`
	ProjectID    string  `json:"project_id"`
	Labels       []string `json:"labels"`
	ModelProfile any     `json:"model_profile"`
	Status       string  `json:"status"`
}

func listFurnaces(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	// D2 炉视图聚合：busy（在锻 RUNNING 计数）+ exec 成功率 + token 汇总。
	// 归因：e.furnace_id 为主（recordExecutionEx 写入）；e.furnace_id IS NULL 时回退 tasks.furnace_id（在途任务）。
	rows, err := model.Pool.Query(c.Context(), `SELECT id, name, base_url, capacity, status, project_id, COALESCE(labels::text,'[]'), COALESCE(model_profile::text,'{}'),
		(SELECT COUNT(*) FROM tasks t WHERE t.furnace_id=furnaces.id AND t.status='RUNNING'),
		(SELECT COUNT(*) FROM agent_executions e LEFT JOIN tasks t ON t.id=e.task_id WHERE e.furnace_id=furnaces.id OR (e.furnace_id IS NULL AND t.furnace_id=furnaces.id)),
		(SELECT COUNT(*) FROM agent_executions e LEFT JOIN tasks t ON t.id=e.task_id WHERE (e.furnace_id=furnaces.id OR (e.furnace_id IS NULL AND t.furnace_id=furnaces.id)) AND e.status='SUCCESS'),
		(SELECT COALESCE(SUM(e.tokens_in),0) FROM agent_executions e LEFT JOIN tasks t ON t.id=e.task_id WHERE e.furnace_id=furnaces.id OR (e.furnace_id IS NULL AND t.furnace_id=furnaces.id)),
		(SELECT COALESCE(SUM(e.tokens_out),0) FROM agent_executions e LEFT JOIN tasks t ON t.id=e.task_id WHERE e.furnace_id=furnaces.id OR (e.furnace_id IS NULL AND t.furnace_id=furnaces.id)),
		created_at, updated_at FROM furnaces ORDER BY created_at ASC`)
	if err != nil {
		return respond.DBError(c, err)
	}
	defer rows.Close()
	items := []fiber.Map{}
	for rows.Next() {
		var id, name, baseURL, status string
		var capacity int
		var projectID *string
		var labelsText, modelProfileText string
		var busy, execTotal, execSuccess, tokensIn, tokensOut int
		var createdAt, updatedAt *string
		_ = rows.Scan(&id, &name, &baseURL, &capacity, &status, &projectID, &labelsText, &modelProfileText, &busy, &execTotal, &execSuccess, &tokensIn, &tokensOut, &createdAt, &updatedAt)
		var labels any
		_ = json.Unmarshal([]byte(labelsText), &labels)
		var modelProfile any
		_ = json.Unmarshal([]byte(modelProfileText), &modelProfile)
		// forja-default 默认模型回落（存量空时前端不空）
		if id == "forja-default" {
			if m, ok := modelProfile.(map[string]interface{}); !ok || m["model"] == nil || m["model"] == "" {
				modelProfile = map[string]interface{}{"provider_id": "forja", "model": "deepseek-v4-flash"}
			}
		}
		var successRate any
		if execTotal > 0 {
			successRate = float64(execSuccess) / float64(execTotal)
		}
		// v1.3-D3 熔断可视化：窗口内连续 FAILED 数（炉数少，逐行查可接受）
		streak := breakerStreak(c.Context(), id)
		items = append(items, fiber.Map{"id": id, "name": name, "base_url": baseURL, "capacity": capacity, "status": status, "project_id": projectID, "labels": labels, "model_profile": modelProfile, "busy": busy, "exec_total": execTotal, "exec_success": execSuccess, "success_rate": successRate, "tokens_in": tokensIn, "tokens_out": tokensOut, "consecutive_failures": streak, "breaker_tripped": streak >= 3})
	}
	return respond.OK(c, fiber.Map{"items": items})
}

// breakerStreak v1.3-D3 熔断计数窗口：breaker_reset_at 之后的轮次记录中，
// 自最近一次 SUCCESS 起的连续 FAILED 数（归因同 D2：e.furnace_id 为主，NULL 回退 tasks.furnace_id）。
// 手动 recover 置 reset=now() 即清零；无记录时为 0。
func breakerStreak(ctx context.Context, furnaceID string) int {
	if model.Pool == nil || furnaceID == "" {
		return 0
	}
	rows, err := model.Pool.Query(ctx, `SELECT e.status FROM agent_executions e LEFT JOIN tasks t ON t.id=e.task_id
		WHERE (e.furnace_id=$1 OR (e.furnace_id IS NULL AND t.furnace_id=$1))
		AND e.started_at > COALESCE((SELECT breaker_reset_at FROM furnaces WHERE id=$1), '-infinity')
		ORDER BY e.started_at DESC, e.finished_at DESC LIMIT 10`, furnaceID)
	if err != nil {
		// 静默返回 0 会连熔断防线一起失效，至少落日志可查
		slog.Warn("breaker streak query failed", "furnace_id", furnaceID, "err", err)
		return 0
	}
	defer rows.Close()
	streak := 0
	for rows.Next() {
		var st string
		_ = rows.Scan(&st)
		if st != "FAILED" {
			break
		}
		streak++
	}
	return streak
}

// recoverFurnaceState v1.3-D3 手动恢复（可单测）：breaker_reset_at=now() 清零计数；
// 仅 unhealthy→healthy（drained 由排水开关管理，不动；healthy 保持）。
// 单条语句原子完成翻转判断，避免与熔断跳闸并发时用旧状态覆盖回去；
// 返回恢复后状态；炉不存在时 err 为 pgx.ErrNoRows。
func recoverFurnaceState(ctx context.Context, furnaceID string) (string, error) {
	var newStatus string
	err := model.Pool.QueryRow(ctx, `UPDATE furnaces
		SET status=CASE WHEN status='unhealthy' THEN 'healthy' ELSE status END,
		    breaker_reset_at=NOW(), updated_at=NOW()
		WHERE id=$1
		RETURNING status`, furnaceID).Scan(&newStatus)
	if err != nil {
		return "", err
	}
	return newStatus, nil
}

// furnaceDockerEnabled 测试环境（FORJA_FURNACE_DISABLE_DOCKER=1，见 main_test.go）关闭容器副作用：
// go test / CI backend job 均注入 DOCKER_HOST，真 Ensure/Stop/Remove 会在宿主残留
// forja-furnace-* 容器（测试清理只删 DB 行）——2026-09-18/19 孤儿实录。
func furnaceDockerEnabled() bool {
	return os.Getenv("FORJA_FURNACE_DISABLE_DOCKER") != "1"
}

// ensureFurnaceContainerAsync v1.3-D3：按当前 model_profile 异步 Ensure 炉容器
//（从 patch healthy 分支提取，recover 复用；失败仅日志，不阻断 DB）。
func ensureFurnaceContainerAsync(fid string) {
	if fid == "forja-default" {
		return
	}
	cname := "forja-furnace-" + fid[:8]
	go func(n, id string) {
		var mpJSON []byte
		_ = model.Pool.QueryRow(context.Background(), `SELECT COALESCE(model_profile::text,'{}') FROM furnaces WHERE id=$1`, id).Scan(&mpJSON)
		var mp map[string]interface{}
		_ = json.Unmarshal(mpJSON, &mp)
		var pid, mid string
		if v, ok := mp["provider_id"].(string); ok {
			pid = v
		}
		if v, ok := mp["model"].(string); ok {
			mid = v
		}
		env := []string{}
		if pid != "" {
			var pBaseURL, pApiKey string
			_ = model.Pool.QueryRow(context.Background(), `SELECT base_url, COALESCE(api_key_ref,'') FROM model_providers WHERE provider_id=$1`, pid).Scan(&pBaseURL, &pApiKey)
			if pApiKey != "" {
				if dec, err := crypto.Decrypt(pApiKey); err == nil {
					pApiKey = dec
				}
			}
			if pBaseURL != "" {
				env = append(env, "PROVIDER_BASE_URL="+pBaseURL)
			}
			if pApiKey != "" {
				env = append(env, "FORJA_API_KEY="+pApiKey)
			}
			if mid != "" {
				env = append(env, "FORJA_MODEL="+mid)
			}
			env = append(env, "FORJA_PROVIDER="+pid)
		} else if mid != "" {
			env = append(env, "FORJA_MODEL="+mid)
		}
		if !furnaceDockerEnabled() { // 测试环境：env 构建路径保留覆盖，跳过容器副作用
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := furnace.Ensure(ctx, n, "ghcr.io/anomalyco/opencode:latest", env); err != nil {
			log.Printf("furnace ensure %s failed: %v", n, err)
		}
	}(cname, fid)
}

// recoverFurnace POST /furnaces/:id/recover：手动恢复熔断（清零计数 + unhealthy→healthy + 尝试拉起容器）。
func recoverFurnace(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	id := c.Params("id")
	if id != "forja-default" && !idgen.IsValid(id) {
		return respond.InvalidParam(c, "id", "invalid id")
	}
	newStatus, err := recoverFurnaceState(c.Context(), id)
	if err != nil {
		if err == pgx.ErrNoRows {
			return respond.NotFound(c, "furnace")
		}
		return respond.DBError(c, err)
	}
	ensureFurnaceContainerAsync(id)
	username, _ := c.Locals("username").(string)
	slog.Info("furnace recovered", "furnace_id", id, "operator", username, "status", newStatus)
	return respond.OK(c, fiber.Map{"id": id, "status": newStatus, "breaker_reset": true})
}

func getFurnace(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	id := c.Params("id")
	if id != "forja-default" && !idgen.IsValid(id) {
		return respond.InvalidParam(c, "id", "invalid id")
	}
	var fid, name, baseURL, status, authRef string
	var capacity int
	var projectID *string
	var labelsText, modelProfileText string
	var createdAt, updatedAt *string
	err := model.Pool.QueryRow(c.Context(), `SELECT id, name, base_url, COALESCE(auth_ref,''), capacity, status, project_id, COALESCE(labels::text,'[]'), COALESCE(model_profile::text,'{}'), created_at::text, updated_at::text FROM furnaces WHERE id=$1`, id).Scan(&fid, &name, &baseURL, &authRef, &capacity, &status, &projectID, &labelsText, &modelProfileText, &createdAt, &updatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return respond.NotFound(c, "furnace")
		}
		return respond.DBError(c, err)
	}
	var labels any
	_ = json.Unmarshal([]byte(labelsText), &labels)
	var modelProfile any
	_ = json.Unmarshal([]byte(modelProfileText), &modelProfile)
	if fid == "forja-default" {
		if m, ok := modelProfile.(map[string]interface{}); !ok || m["model"] == nil || m["model"] == "" {
			modelProfile = map[string]interface{}{"provider_id": "forja", "model": "deepseek-v4-flash"}
		}
	}
	var busy, execTotal, execSuccess, tokensIn, tokensOut int
	_ = model.Pool.QueryRow(c.Context(), `SELECT COUNT(*) FROM tasks WHERE furnace_id=$1 AND status='RUNNING'`, id).Scan(&busy)
	_ = model.Pool.QueryRow(c.Context(), `SELECT COUNT(*), COUNT(*) FILTER (WHERE e.status='SUCCESS'), COALESCE(SUM(e.tokens_in),0), COALESCE(SUM(e.tokens_out),0)
		FROM agent_executions e LEFT JOIN tasks t ON t.id=e.task_id
		WHERE e.furnace_id=$1 OR (e.furnace_id IS NULL AND t.furnace_id=$1)`, id).Scan(&execTotal, &execSuccess, &tokensIn, &tokensOut)
	masked := ""
	if authRef != "" {
		masked = "***"
	}
	var successRate any
	if execTotal > 0 {
		successRate = float64(execSuccess) / float64(execTotal)
	}
	streak := breakerStreak(c.Context(), id)
	return respond.OK(c, fiber.Map{"id": fid, "name": name, "base_url": baseURL, "auth_ref_masked": masked, "capacity": capacity, "status": status, "project_id": projectID, "labels": labels, "model_profile": modelProfile, "busy": busy, "exec_total": execTotal, "exec_success": execSuccess, "success_rate": successRate, "tokens_in": tokensIn, "tokens_out": tokensOut, "consecutive_failures": streak, "breaker_tripped": streak >= 3, "created_at": createdAt, "updated_at": updatedAt})
}

func createFurnace(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	var req createFurnaceReq
	if err := c.BodyParser(&req); err != nil {
		return respond.BadRequest(c, err.Error())
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return respond.InvalidParam(c, "name", "name required")
	}
	// v1.2 炉即容器：per-project 炉可自动生成 base_url 为 http://forja-furnace-<id8>:4096
	// 共享池炉仍需显式 base_url（默认 http://opencode:4096）
	if req.BaseURL == "" {
		if req.ProjectID != "" {
			// 延迟到 id 生成后赋值
		} else {
			return respond.InvalidParam(c, "base_url", "base_url required")
		}
	}
	// role already Admin via user.create
	if req.ProjectID != "" && !idgen.IsValid(req.ProjectID) {
		return respond.InvalidParam(c, "project_id", "invalid project_id")
	}
	capacity := 2
	if req.Capacity != nil {
		if *req.Capacity < 1 || *req.Capacity > 20 {
			return respond.InvalidParam(c, "capacity", "capacity 1..20")
		}
		capacity = *req.Capacity
	}
	status := req.Status
	if status == "" {
		status = "healthy"
	}
	id := idgen.New()
	if req.BaseURL == "" && req.ProjectID != "" {
		req.BaseURL = "http://forja-furnace-" + id[:8] + ":4096"
	}
	encAuth := ""
	if strings.TrimSpace(req.AuthRef) != "" {
		if e, err := crypto.Encrypt(req.AuthRef); err == nil {
			encAuth = e
		} else {
			encAuth = req.AuthRef
		}
	}
	_, err := model.Pool.Exec(c.Context(), `INSERT INTO furnaces (id, name, base_url, auth_ref, capacity, status, project_id, model_profile, labels) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		id, req.Name, req.BaseURL, nullableStr(encAuth), capacity, status, nullableUUID(req.ProjectID), toJSONB(req.ModelProfile), toJSONB(req.Labels))
	if err != nil {
		if strings.Contains(err.Error(), "duplicate") || strings.Contains(err.Error(), "23505") {
			return respond.InvalidParam(c, "name", "name already exists")
		}
		return respond.DBError(c, err)
	}
	// 动态启停：healthy 时尝试 Ensure 容器（失败不阻断 DB，已落库可后续重试）；drained/unhealthy 仅落库
	if status == "healthy" && id != "forja-default" {
		var providerID, modelID string
		if mp, ok := req.ModelProfile.(map[string]interface{}); ok {
			if v, ok := mp["provider_id"].(string); ok {
				providerID = v
			}
			if v, ok := mp["model"].(string); ok {
				modelID = v
			}
		}
		go func(fid, fname, pid, mid string) {
			env := []string{}
			if pid != "" {
				var pBaseURL, pApiKey, pHeaders, authMode string
				_ = model.Pool.QueryRow(context.Background(), `SELECT base_url, COALESCE(api_key_ref,''), COALESCE(headers::text,'[]'), COALESCE(auth_mode,'Bearer') FROM model_providers WHERE provider_id=$1`, pid).Scan(&pBaseURL, &pApiKey, &pHeaders, &authMode)
				if pApiKey != "" {
					if dec, err := crypto.Decrypt(pApiKey); err == nil {
						pApiKey = dec
					}
				}
				if pBaseURL != "" {
					env = append(env, "PROVIDER_BASE_URL="+pBaseURL)
				}
				if pApiKey != "" {
					env = append(env, "FORJA_API_KEY="+pApiKey)
				}
				if mid != "" {
					env = append(env, "FORJA_MODEL="+mid)
				}
				env = append(env, "FORJA_PROVIDER="+pid)
				_ = pHeaders
				_ = authMode
			} else if mid != "" {
				env = append(env, "FORJA_MODEL="+mid)
			}
			if !furnaceDockerEnabled() { // 测试环境：env 构建路径保留覆盖，跳过容器副作用
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cname := "forja-furnace-" + fid[:8]
			if _, err := furnace.Ensure(ctx, cname, "ghcr.io/anomalyco/opencode:latest", env); err != nil {
				log.Printf("furnace ensure %s (%s) failed: %v", fid, cname, err)
			} else {
				log.Printf("furnace ensure %s (%s) ok", fid, cname)
			}
			_ = fname
		}(id, req.Name, providerID, modelID)
	}
	return respond.OK(c, fiber.Map{"id": id})
}

func patchFurnace(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	id := c.Params("id")
	if id != "forja-default" && !idgen.IsValid(id) {
		return respond.InvalidParam(c, "id", "invalid id")
	}
	var body map[string]interface{}
	if err := c.BodyParser(&body); err != nil {
		return respond.BadRequest(c, err.Error())
	}
	if len(body) == 0 {
		return respond.BadRequest(c, "nothing to update")
	}
	// check exists
	var cnt int
	if err := model.Pool.QueryRow(c.Context(), `SELECT COUNT(*) FROM furnaces WHERE id=$1`, id).Scan(&cnt); err != nil {
		return respond.DBError(c, err)
	}
	if cnt == 0 {
		return respond.NotFound(c, "furnace")
	}
	// simple updaters
	if v, ok := body["name"]; ok {
		if s, ok2 := v.(string); ok2 {
			s = strings.TrimSpace(s)
			if s == "" {
				return respond.InvalidParam(c, "name", "name required")
			}
			if _, err := model.Pool.Exec(c.Context(), `UPDATE furnaces SET name=$1, updated_at=NOW() WHERE id=$2`, s, id); err != nil {
				if strings.Contains(err.Error(), "duplicate") {
					return respond.InvalidParam(c, "name", "name already exists")
				}
				return respond.DBError(c, err)
			}
		}
	}
	if v, ok := body["capacity"]; ok {
		if f, ok2 := v.(float64); ok2 {
			ci := int(f)
			if ci < 1 || ci > 20 {
				return respond.InvalidParam(c, "capacity", "capacity 1..20")
			}
			if _, err := model.Pool.Exec(c.Context(), `UPDATE furnaces SET capacity=$1, updated_at=NOW() WHERE id=$2`, ci, id); err != nil {
				return respond.DBError(c, err)
			}
		}
	}
	if v, ok := body["status"]; ok {
		if s, ok2 := v.(string); ok2 {
			s = strings.ToLower(strings.TrimSpace(s))
			if s != "healthy" && s != "busy" && s != "unhealthy" && s != "drained" {
				return respond.InvalidParam(c, "status", "status must be healthy|busy|unhealthy|drained")
			}
			if _, err := model.Pool.Exec(c.Context(), `UPDATE furnaces SET status=$1, updated_at=NOW() WHERE id=$2`, s, id); err != nil {
				return respond.DBError(c, err)
			}
			// 动态启停：healthy→Start，drained→Stop（异步，失败仅日志）
			if id != "forja-default" {
				cname := "forja-furnace-" + id[:8]
				if s == "drained" {
					go func(n string) {
						if !furnaceDockerEnabled() {
							return
						}
						ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
						defer cancel()
						if err := furnace.Stop(ctx, n); err != nil {
							log.Printf("furnace stop %s failed: %v", n, err)
						}
					}(cname)
				} else if s == "healthy" {
					ensureFurnaceContainerAsync(id)
				}
			}
		}
	}
	if v, ok := body["auth_ref"]; ok {
		if s, ok2 := v.(string); ok2 {
			enc := ""
			if strings.TrimSpace(s) != "" {
				if e, err := crypto.Encrypt(s); err == nil {
					enc = e
				} else {
					enc = s
				}
			}
			if _, err := model.Pool.Exec(c.Context(), `UPDATE furnaces SET auth_ref=$1, updated_at=NOW() WHERE id=$2`, nullableStr(enc), id); err != nil {
				return respond.DBError(c, err)
			}
		}
	}
	if v, ok := body["base_url"]; ok {
		if s, ok2 := v.(string); ok2 {
			s = strings.TrimSpace(s)
			if s == "" {
				return respond.InvalidParam(c, "base_url", "base_url required")
			}
			if _, err := model.Pool.Exec(c.Context(), `UPDATE furnaces SET base_url=$1, updated_at=NOW() WHERE id=$2`, s, id); err != nil {
				return respond.DBError(c, err)
			}
		}
	}
	if v, ok := body["project_id"]; ok {
		// allow null/empty to move to shared pool
		if s, ok2 := v.(string); ok2 {
			s = strings.TrimSpace(s)
			if s == "" {
				if _, err := model.Pool.Exec(c.Context(), `UPDATE furnaces SET project_id=NULL, updated_at=NOW() WHERE id=$1`, id); err != nil {
					return respond.DBError(c, err)
				}
			} else {
				if !idgen.IsValid(s) {
					return respond.InvalidParam(c, "project_id", "invalid project_id")
				}
				if _, err := model.Pool.Exec(c.Context(), `UPDATE furnaces SET project_id=$1, updated_at=NOW() WHERE id=$2`, s, id); err != nil {
					return respond.DBError(c, err)
				}
			}
		} else if v == nil {
			if _, err := model.Pool.Exec(c.Context(), `UPDATE furnaces SET project_id=NULL, updated_at=NOW() WHERE id=$1`, id); err != nil {
				return respond.DBError(c, err)
			}
		}
	}
	if v, ok := body["labels"]; ok {
		if _, err := model.Pool.Exec(c.Context(), `UPDATE furnaces SET labels=$1::jsonb, updated_at=NOW() WHERE id=$2`, toJSONB(v), id); err != nil {
			return respond.DBError(c, err)
		}
	}
	if v, ok := body["model_profile"]; ok {
		if _, err := model.Pool.Exec(c.Context(), `UPDATE furnaces SET model_profile=$1::jsonb, updated_at=NOW() WHERE id=$2`, toJSONB(v), id); err != nil {
			return respond.DBError(c, err)
		}
	}
	return respond.OK(c, fiber.Map{"id": id})
}

func deleteFurnace(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	id := c.Params("id")
	// id 可能是 UUIDv7 文本，forja-default 为固定种子，需放行校验
	if id != "forja-default" && !idgen.IsValid(id) {
		return respond.InvalidParam(c, "id", "invalid id")
	}
	if id == "forja-default" {
		return respond.BusinessRule(c, "FURNACE_PROTECTED", "默认炉禁止删除")
	}
	var name string
	_ = model.Pool.QueryRow(c.Context(), `SELECT name FROM furnaces WHERE id=$1`, id).Scan(&name)
	if name == "forja-default" {
		return respond.BusinessRule(c, "FURNACE_PROTECTED", "默认炉禁止删除")
	}
	ct, err := model.Pool.Exec(c.Context(), `DELETE FROM furnaces WHERE id=$1`, id)
	if err != nil {
		return respond.DBError(c, err)
	}
	if ct.RowsAffected() == 0 {
		return respond.NotFound(c, "furnace")
	}
	_ = pgx.ErrNoRows
	// 动态移除容器（异步，失败仅日志；forja-default 已在上游拦截；测试环境内部跳过）
	go func(fid string) {
		cname := "forja-furnace-" + fid[:8]
		if !furnaceDockerEnabled() {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := furnace.Remove(ctx, cname); err != nil {
			log.Printf("furnace remove %s failed: %v", cname, err)
		} else {
			log.Printf("furnace remove %s ok", cname)
		}
	}(id)
	return respond.OK(c, fiber.Map{"id": id, "deleted": true})
}

func testFurnaceChat(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	id := c.Params("id")
	if id != "forja-default" && !idgen.IsValid(id) {
		return respond.InvalidParam(c, "id", "invalid id")
	}
	var body struct {
		Prompt string `json:"prompt"`
		Model  string `json:"model"`
	}
	_ = c.BodyParser(&body)
	prompt := strings.TrimSpace(body.Prompt)
	if prompt == "" {
		prompt = "ping"
	}
	if len(prompt) > 500 {
		prompt = prompt[:500]
	}
	var baseURL, mpText, authRef, status string
	err := model.Pool.QueryRow(c.Context(), `SELECT base_url, COALESCE(model_profile::text,'{}'), COALESCE(auth_ref,''), status FROM furnaces WHERE id=$1`, id).Scan(&baseURL, &mpText, &authRef, &status)
	if err != nil {
		if err == pgx.ErrNoRows {
			return respond.NotFound(c, "furnace")
		}
		return respond.DBError(c, err)
	}
	var mp map[string]interface{}
	_ = json.Unmarshal([]byte(mpText), &mp)
	var providerID, modelID string
	if v, ok := mp["provider_id"].(string); ok {
		providerID = v
	}
	if v, ok := mp["model"].(string); ok {
		modelID = v
	}
	if body.Model != "" {
		modelID = body.Model
	}
	adapter := agent.NewOpenCodeAdapter()
	if baseURL != "" {
		adapter.BaseURL = baseURL
	}
	if authRef != "" {
		if dec, err := crypto.Decrypt(authRef); err == nil && dec != "" {
			// auth_ref may be "user:pass" or just token; assume password
			if strings.Contains(dec, ":") {
				parts := strings.SplitN(dec, ":", 2)
				adapter.Username = parts[0]
				adapter.Password = parts[1]
			} else {
				adapter.Username = "opencode"
				adapter.Password = dec
			}
		}
	}
	// 30s 超时试跑（含宿主 fallback：forja-default-furnace 仅 DinD 可达，宿主走 localhost）
	ctx, cancel := context.WithTimeout(c.Context(), 30*time.Second)
	defer cancel()
	start := time.Now()
	reply, detail, err := adapter.SimpleChatDetailed(ctx, prompt, providerID, modelID)
	usedBaseURL := adapter.BaseURL
	// fallback：宿主 DNS 不可达时重试本机 opencode。
	// 2026-09-16 run359 实录：显式 OPENCODE_URL 必须排首位——排在 localhost/host.docker.internal
	// 之后时，CI 容器内 host.docker.internal:4096 连接挂起会把 30s 用例超时打穿（探测顺序即优先级）。
	if err != nil && (strings.Contains(err.Error(), "no such host") || strings.Contains(err.Error(), "dial tcp")) {
		fallbacks := []string{}
		if envURL := os.Getenv("OPENCODE_URL"); envURL != "" && envURL != baseURL {
			fallbacks = append(fallbacks, envURL)
		}
		fallbacks = append(fallbacks, "http://localhost:4096", "http://127.0.0.1:4096", "http://host.docker.internal:4096")
		for _, fb := range fallbacks {
			if fb == baseURL || fb == "" {
				continue
			}
			adapter.BaseURL = fb
			usedBaseURL = fb
			reply, detail, err = adapter.SimpleChatDetailed(ctx, prompt, providerID, modelID)
			if err == nil {
				break
			}
			if !strings.Contains(err.Error(), "no such host") && !strings.Contains(err.Error(), "dial tcp") {
				break
			}
		}
	}
	latency := time.Since(start).Milliseconds()
	if err != nil {
		_, _ = model.Pool.Exec(context.Background(), `UPDATE furnaces SET status='unhealthy', updated_at=NOW() WHERE id=$1`, id)
		return respond.OK(c, fiber.Map{"id": id, "status": "unhealthy", "detail": err.Error(), "latency_ms": latency, "model": modelID, "provider": providerID, "base_url": usedBaseURL, "raw": detail})
	}
	_, _ = model.Pool.Exec(context.Background(), `UPDATE furnaces SET status='healthy', updated_at=NOW() WHERE id=$1`, id)
	if len(reply) > 500 {
		reply = reply[:500]
	}
	if reply == "" {
		reply = "pong"
	}
	// 详细回显：parts/info/raw 供前端诊断（prompt 回显 + 上游模型实际返回）
	respMap := fiber.Map{"id": id, "status": "healthy", "reply": reply, "latency_ms": latency, "model": modelID, "provider": providerID, "base_url": usedBaseURL, "prompt": prompt}
	if detail != nil {
		respMap["raw"] = detail.RawJSON
		respMap["parts"] = detail.Parts
		respMap["info"] = detail.Info
		respMap["session_id"] = detail.SessionID
		if detail.Info.Error != nil {
			respMap["upstream_error"] = detail.Info.Error
		}
	}
	return respond.OK(c, respMap)
}

func testFurnaceChatPreview(c *fiber.Ctx) error {
	var body struct {
		BaseURL    string `json:"base_url"`
		ProviderID string `json:"provider_id"`
		Model      string `json:"model"`
		Prompt     string `json:"prompt"`
		AuthRef    string `json:"auth_ref"`
	}
	_ = c.BodyParser(&body)
	prompt := strings.TrimSpace(body.Prompt)
	if prompt == "" {
		prompt = "ping"
	}
	if len(prompt) > 500 {
		prompt = prompt[:500]
	}
	baseURL := strings.TrimSpace(body.BaseURL)
	if baseURL == "" {
		return respond.InvalidParam(c, "base_url", "base_url required")
	}
	providerID := body.ProviderID
	modelID := body.Model
	adapter := agent.NewOpenCodeAdapter()
	adapter.BaseURL = baseURL
	if body.AuthRef != "" {
		// auth_ref may be plain for preview
		if strings.Contains(body.AuthRef, ":") {
			parts := strings.SplitN(body.AuthRef, ":", 2)
			adapter.Username = parts[0]
			adapter.Password = parts[1]
		} else {
			adapter.Username = "opencode"
			adapter.Password = body.AuthRef
		}
	}
	ctx, cancel := context.WithTimeout(c.Context(), 30*time.Second)
	defer cancel()
	start := time.Now()
	reply, detail, err := adapter.SimpleChatDetailed(ctx, prompt, providerID, modelID)
	usedBaseURL := adapter.BaseURL
	if err != nil && (strings.Contains(err.Error(), "no such host") || strings.Contains(err.Error(), "dial tcp")) {
		// 与 testFurnaceChat 同序：显式 OPENCODE_URL 优先（run359 探测顺序超时实录）
		fallbacks := []string{}
		if envURL := os.Getenv("OPENCODE_URL"); envURL != "" && envURL != baseURL {
			fallbacks = append(fallbacks, envURL)
		}
		fallbacks = append(fallbacks, "http://localhost:4096", "http://127.0.0.1:4096", "http://host.docker.internal:4096")
		for _, fb := range fallbacks {
			if fb == baseURL || fb == "" {
				continue
			}
			adapter.BaseURL = fb
			usedBaseURL = fb
			reply, detail, err = adapter.SimpleChatDetailed(ctx, prompt, providerID, modelID)
			if err == nil {
				break
			}
			if !strings.Contains(err.Error(), "no such host") && !strings.Contains(err.Error(), "dial tcp") {
				break
			}
		}
	}
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return respond.OK(c, fiber.Map{"status": "unhealthy", "detail": err.Error(), "latency_ms": latency, "base_url": usedBaseURL, "raw": detail})
	}
	if len(reply) > 500 {
		reply = reply[:500]
	}
	if reply == "" {
		reply = "pong"
	}
	respMap := fiber.Map{"status": "healthy", "reply": reply, "latency_ms": latency, "model": modelID, "provider": providerID, "base_url": usedBaseURL, "prompt": prompt}
	if detail != nil {
		respMap["raw"] = detail.RawJSON
		respMap["parts"] = detail.Parts
		respMap["info"] = detail.Info
		respMap["session_id"] = detail.SessionID
		if detail.Info.Error != nil {
			respMap["upstream_error"] = detail.Info.Error
		}
	}
	return respond.OK(c, respMap)
}

func nullableStr(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

func toJSONB(v any) any {
	if v == nil {
		return nil
	}
	return v
}
