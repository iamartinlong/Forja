package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"forja/backend/internal/agent"
	"forja/backend/internal/api/respond"
	"forja/backend/internal/auth"
	"forja/backend/internal/crypto"
	idgen "forja/backend/internal/id"
	"forja/backend/internal/model"

	"github.com/gofiber/fiber/v2"
)

var baseURLRe = regexp.MustCompile(`^https://[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}(:\d+)?(/.*)?$`)

func isValidBaseURL(s string) bool {
	if !baseURLRe.MatchString(s) {
		return false
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || !strings.Contains(u.Host, ".") {
		return false
	}
	return true
}

func RegisterModelProviderRoutes(r fiber.Router) {
	r.Get("/model-providers", auth.Require("user.list"), listModelProviders)
	r.Post("/model-providers", auth.Require("user.create"), createModelProvider)
	r.Post("/model-providers/test-preview", auth.Require("user.create"), testPreview)
	r.Get("/model-providers/:id", auth.Require("user.list"), getModelProvider)
	r.Get("/model-providers/:id/references", auth.Require("user.list"), getModelProviderReferences)
	r.Patch("/model-providers/:id", auth.Require("user.create"), patchModelProvider)
	r.Delete("/model-providers/:id", auth.Require("user.create"), deleteModelProvider)
	r.Post("/model-providers/:id/test", auth.Require("user.create"), testModelProvider)
}

type modelEntry struct {
	ModelID     string `json:"model_id"`
	DisplayName string `json:"display_name"`
	Enabled     *bool  `json:"enabled"`
}
type headerEntry struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func listModelProviders(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	rows, err := model.Pool.Query(c.Context(), `SELECT id, provider_id, display_name, base_url, auth_mode, models, headers, status, updated_at FROM model_providers ORDER BY created_at ASC`)
	if err != nil {
		return respond.DBError(c, err)
	}
	defer rows.Close()
	items := []fiber.Map{}
	for rows.Next() {
		var id, providerID, displayName, baseURL, status, authMode string
		var modelsJ, headersJ []byte
		var updatedAt time.Time
		_ = rows.Scan(&id, &providerID, &displayName, &baseURL, &authMode, &modelsJ, &headersJ, &status, &updatedAt)
		var models, headers any
		_ = json.Unmarshal(modelsJ, &models)
		_ = json.Unmarshal(headersJ, &headers)
		items = append(items, fiber.Map{"id": id, "provider_id": providerID, "display_name": displayName, "base_url": baseURL, "auth_mode": authMode, "models": models, "headers": headers, "status": status, "updated_at": updatedAt})
	}
	return respond.OK(c, fiber.Map{"items": items})
}

func getModelProvider(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	id := c.Params("id")
	if !idgen.IsValid(id) {
		return respond.InvalidParam(c, "id", "invalid id")
	}
	var pid, displayName, baseURL, authMode, status string
	var modelsJ, headersJ []byte
	var createdAt, updatedAt time.Time
	if err := model.Pool.QueryRow(c.Context(), `SELECT id, provider_id, display_name, base_url, auth_mode, models, headers, status, created_at, updated_at FROM model_providers WHERE id=$1`, id).Scan(&id, &pid, &displayName, &baseURL, &authMode, &modelsJ, &headersJ, &status, &createdAt, &updatedAt); err != nil {
		return respond.NotFound(c, "model_provider")
	}
	var models, headers any
	_ = json.Unmarshal(modelsJ, &models)
	_ = json.Unmarshal(headersJ, &headers)
	// mask api_key
	return respond.OK(c, fiber.Map{"id": id, "provider_id": pid, "display_name": displayName, "base_url": baseURL, "auth_mode": authMode, "models": models, "headers": headers, "status": status, "created_at": createdAt, "updated_at": updatedAt, "api_key_masked": "***"})
}

func createModelProvider(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	var body struct {
		ProviderID  string        `json:"provider_id"`
		DisplayName string        `json:"display_name"`
		BaseURL     string        `json:"base_url"`
		ApiKey      string        `json:"api_key"`
		AuthMode    string        `json:"auth_mode"`
		Models      []modelEntry  `json:"models"`
		Headers     []headerEntry `json:"headers"`
	}
	if err := c.BodyParser(&body); err != nil {
		return respond.BadRequest(c, err.Error())
	}
	body.ProviderID = strings.ToLower(strings.TrimSpace(body.ProviderID))
	if body.ProviderID == "" {
		return respond.InvalidParam(c, "provider_id", "请输入提供商ID")
	}
	body.AuthMode = strings.TrimSpace(body.AuthMode)
	if body.AuthMode == "" {
		body.AuthMode = "Bearer"
	}
	if body.AuthMode != "Bearer" && body.AuthMode != "Header" && body.AuthMode != "None" {
		return respond.InvalidParam(c, "auth_mode", "must be Bearer|Header|None")
	}
	if body.AuthMode == "Bearer" && strings.TrimSpace(body.ApiKey) == "" {
		return respond.InvalidParam(c, "api_key", "请输入 API 密钥")
	}
	if body.BaseURL == "" || !isValidBaseURL(body.BaseURL) {
		return respond.InvalidParam(c, "base_url", "请输入正确的 https:// 域名")
	}
	// normalize models: default enabled true
	for i := range body.Models {
		if body.Models[i].Enabled == nil {
			t := true
			body.Models[i].Enabled = &t
		}
	}
	modelsB, _ := json.Marshal(body.Models)
	headersB, _ := json.Marshal(body.Headers)
	id := idgen.New()
	encKey := ""
	if strings.TrimSpace(body.ApiKey) != "" {
		if e, err := crypto.Encrypt(body.ApiKey); err == nil {
			encKey = e
		} else {
			encKey = body.ApiKey
		}
	}
	_, err := model.Pool.Exec(c.Context(), `INSERT INTO model_providers (id, provider_id, display_name, base_url, auth_mode, api_key_ref, models, headers) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, id, body.ProviderID, body.DisplayName, body.BaseURL, body.AuthMode, nullableStr(encKey), modelsB, headersB)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate") || strings.Contains(err.Error(), "23505") {
			return respond.InvalidParam(c, "provider_id", "provider_id already exists (case-insensitive)")
		}
		return respond.DBError(c, err)
	}
	go agent.SyncOpencodeProviders(context.Background())
	return respond.OK(c, fiber.Map{"id": id})
}

func getModelProviderReferences(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	id := c.Params("id")
	if !idgen.IsValid(id) {
		return respond.InvalidParam(c, "id", "invalid id")
	}
	var providerID string
	if err := model.Pool.QueryRow(c.Context(), `SELECT provider_id FROM model_providers WHERE id=$1`, id).Scan(&providerID); err != nil {
		return respond.NotFound(c, "model_provider")
	}
	rows, err := model.Pool.Query(c.Context(), `SELECT id, name, base_url FROM furnaces WHERE model_profile->>'provider_id'=$1`, providerID)
	if err != nil {
		return respond.DBError(c, err)
	}
	defer rows.Close()
	furnaces := []fiber.Map{}
	for rows.Next() {
		var fid, fname, furl string
		_ = rows.Scan(&fid, &fname, &furl)
		furnaces = append(furnaces, fiber.Map{"id": fid, "name": fname, "base_url": furl})
	}
	return respond.OK(c, fiber.Map{"provider_id": providerID, "furnaces": furnaces})
}

func patchModelProvider(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	id := c.Params("id")
	if !idgen.IsValid(id) {
		return respond.InvalidParam(c, "id", "invalid id")
	}
	var body map[string]json.RawMessage
	if err := c.BodyParser(&body); err != nil {
		return respond.BadRequest(c, err.Error())
	}
	if len(body) == 0 {
		return respond.BadRequest(c, "nothing to update")
	}
	// build dynamic updates - handle known fields
	ctx := c.Context()
	if raw, ok := body["provider_id"]; ok {
		var v string
		_ = json.Unmarshal(raw, &v)
		v = strings.ToLower(strings.TrimSpace(v))
		if v == "" {
			return respond.InvalidParam(c, "provider_id", "请输入提供商ID")
		}
		if _, err := model.Pool.Exec(ctx, `UPDATE model_providers SET provider_id=$1, updated_at=NOW() WHERE id=$2`, v, id); err != nil {
			if strings.Contains(err.Error(), "duplicate") {
				return respond.InvalidParam(c, "provider_id", "already exists")
			}
			return respond.DBError(c, err)
		}
	}
	if raw, ok := body["display_name"]; ok {
		var v string
		_ = json.Unmarshal(raw, &v)
		_, _ = model.Pool.Exec(ctx, `UPDATE model_providers SET display_name=$1, updated_at=NOW() WHERE id=$2`, v, id)
	}
	if raw, ok := body["base_url"]; ok {
		var v string
		_ = json.Unmarshal(raw, &v)
		if v != "" && !isValidBaseURL(v) {
			return respond.InvalidParam(c, "base_url", "请输入正确的 https:// 域名格式")
		}
		_, _ = model.Pool.Exec(ctx, `UPDATE model_providers SET base_url=$1, updated_at=NOW() WHERE id=$2`, v, id)
	}
	if raw, ok := body["auth_mode"]; ok {
		var v string
		_ = json.Unmarshal(raw, &v)
		v = strings.TrimSpace(v)
		if v != "Bearer" && v != "Header" && v != "None" {
			return respond.InvalidParam(c, "auth_mode", "must be Bearer|Header|None")
		}
		_, _ = model.Pool.Exec(ctx, `UPDATE model_providers SET auth_mode=$1, updated_at=NOW() WHERE id=$2`, v, id)
	}
	if raw, ok := body["api_key"]; ok {
		var v string
		_ = json.Unmarshal(raw, &v)
		// if auth_mode is Bearer, key required unless explicitly clearing via None?
		var curMode string
		_ = model.Pool.QueryRow(ctx, `SELECT auth_mode FROM model_providers WHERE id=$1`, id).Scan(&curMode)
		if curMode == "Bearer" && strings.TrimSpace(v) == "" {
			// allow empty on PATCH to keep existing, but if explicitly empty and Bearer, require? keep existing if empty string means not updating
			// empty string via PATCH is ambiguous; we treat empty as not changing, so skip update if empty and was Bearer with existing key
			// check if existing key exists
			var existingKey string
			_ = model.Pool.QueryRow(ctx, `SELECT COALESCE(api_key_ref,'') FROM model_providers WHERE id=$1`, id).Scan(&existingKey)
			if existingKey == "" {
				return respond.InvalidParam(c, "api_key", "api_key required for Bearer")
			}
			// skip update (keep existing)
		} else {
			enc := ""
			if strings.TrimSpace(v) != "" {
				if e, err := crypto.Encrypt(v); err == nil {
					enc = e
				} else {
					enc = v
				}
			}
			_, _ = model.Pool.Exec(ctx, `UPDATE model_providers SET api_key_ref=$1, updated_at=NOW() WHERE id=$2`, nullableStr(enc), id)
		}
	}
	if raw, ok := body["models"]; ok {
		// check removed models still referenced
		var newModels []modelEntry
		_ = json.Unmarshal(raw, &newModels)
		var oldModelsB []byte
		var providerID string
		_ = model.Pool.QueryRow(ctx, `SELECT models::text, provider_id FROM model_providers WHERE id=$1`, id).Scan(&oldModelsB, &providerID)
		var oldModels []modelEntry
		_ = json.Unmarshal(oldModelsB, &oldModels)
		oldSet := map[string]bool{}
		for _, m := range oldModels {
			oldSet[strings.ToLower(m.ModelID)] = true
		}
		newSet := map[string]bool{}
		for _, m := range newModels {
			newSet[strings.ToLower(m.ModelID)] = true
		}
		for _, m := range oldModels {
			if !newSet[strings.ToLower(m.ModelID)] {
				// removed -> check furnaces referencing this model
				var cnt int
				_ = model.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM furnaces WHERE model_profile->>'provider_id'=$1 AND model_profile->>'model'=$2`, providerID, m.ModelID).Scan(&cnt)
				if cnt > 0 {
					return respond.BusinessRule(c, "REFERENCED", "model "+m.ModelID+" still referenced, disable instead")
				}
			}
		}
		_, _ = model.Pool.Exec(ctx, `UPDATE model_providers SET models=$1, updated_at=NOW() WHERE id=$2`, string(raw), id)
	}
	if raw, ok := body["headers"]; ok {
		_, _ = model.Pool.Exec(ctx, `UPDATE model_providers SET headers=$1, updated_at=NOW() WHERE id=$2`, string(raw), id)
	}
	go agent.SyncOpencodeProviders(context.Background())
	return respond.OK(c, fiber.Map{"id": id})
}

func deleteModelProvider(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	id := c.Params("id")
	if !idgen.IsValid(id) {
		return respond.InvalidParam(c, "id", "invalid id")
	}
	var providerID string
	if err := model.Pool.QueryRow(c.Context(), `SELECT provider_id FROM model_providers WHERE id=$1`, id).Scan(&providerID); err != nil {
		return respond.NotFound(c, "model_provider")
	}
	force := c.Query("force") == "true"
	var cnt int
	_ = model.Pool.QueryRow(c.Context(), `SELECT COUNT(*) FROM furnaces WHERE model_profile->>'provider_id'=$1`, providerID).Scan(&cnt)
	if cnt > 0 && !force {
		return respond.BusinessRule(c, "REFERENCED", "referenced by furnaces, disable instead or use ?force=true with provider_id confirmation")
	}
	if force && cnt > 0 {
		_, _ = model.Pool.Exec(c.Context(), `UPDATE furnaces SET model_profile=NULL, updated_at=NOW() WHERE model_profile->>'provider_id'=$1`, providerID)
	}
	ct, err := model.Pool.Exec(c.Context(), `DELETE FROM model_providers WHERE id=$1`, id)
	if err != nil {
		return respond.DBError(c, err)
	}
	if ct.RowsAffected() == 0 {
		return respond.NotFound(c, "model_provider")
	}
	go agent.SyncOpencodeProviders(context.Background())
	return respond.OK(c, fiber.Map{"id": id})
}

func testModelProvider(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	id := c.Params("id")
	if !idgen.IsValid(id) {
		return respond.InvalidParam(c, "id", "invalid id")
	}
	var baseURL, apiKeyRef string
	var headersB []byte
	if err := model.Pool.QueryRow(c.Context(), `SELECT base_url, COALESCE(api_key_ref,''), headers FROM model_providers WHERE id=$1`, id).Scan(&baseURL, &apiKeyRef, &headersB); err != nil {
		return respond.NotFound(c, "model_provider")
	}
	if dec, err := crypto.Decrypt(apiKeyRef); err == nil && dec != "" {
		apiKeyRef = dec
	}
	var headers []headerEntry
	_ = json.Unmarshal(headersB, &headers)
	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()
	url := strings.TrimSuffix(baseURL, "/")
	if strings.HasSuffix(url, "/v1") {
		url = url + "/models"
	} else {
		url = url + "/v1/models"
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	if apiKeyRef != "" {
		req.Header.Set("Authorization", "Bearer "+apiKeyRef)
	}
	for _, h := range headers {
		if h.Name != "" {
			req.Header.Set(h.Name, h.Value)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		_, _ = model.Pool.Exec(c.Context(), `UPDATE model_providers SET status='unhealthy', updated_at=NOW() WHERE id=$1`, id)
		return respond.OK(c, fiber.Map{"id": id, "status": "unhealthy", "detail": err.Error()})
	}
	defer resp.Body.Close()
	status := "healthy"
	if resp.StatusCode >= 400 {
		status = "unhealthy"
	}
	_, _ = model.Pool.Exec(c.Context(), `UPDATE model_providers SET status=$1, updated_at=NOW() WHERE id=$2`, status, id)
	detail := ""
	if status == "unhealthy" {
		detail = "status " + resp.Status
	}
	return respond.OK(c, fiber.Map{"id": id, "status": status, "detail": detail})
}

func testPreview(c *fiber.Ctx) error {
	var body struct {
		BaseURL  string        `json:"base_url"`
		ApiKey   string        `json:"api_key"`
		AuthMode string        `json:"auth_mode"`
		Headers  []headerEntry `json:"headers"`
	}
	if err := c.BodyParser(&body); err != nil {
		return respond.BadRequest(c, err.Error())
	}
	if body.BaseURL == "" || !isValidBaseURL(body.BaseURL) {
		return respond.InvalidParam(c, "base_url", "请输入正确的 https:// 域名格式")
	}
	if body.AuthMode == "" {
		body.AuthMode = "Bearer"
	}
	if body.AuthMode == "Bearer" && strings.TrimSpace(body.ApiKey) == "" {
		return respond.InvalidParam(c, "api_key", "请输入 API 密钥")
	}
	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()
	url := strings.TrimSuffix(body.BaseURL, "/")
	if strings.HasSuffix(url, "/v1") {
		url = url + "/models"
	} else {
		url = url + "/v1/models"
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	if strings.TrimSpace(body.ApiKey) != "" {
		req.Header.Set("Authorization", "Bearer "+body.ApiKey)
	}
	for _, h := range body.Headers {
		if h.Name != "" {
			req.Header.Set(h.Name, h.Value)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return respond.OK(c, fiber.Map{"status": "unhealthy", "detail": err.Error()})
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return respond.OK(c, fiber.Map{"status": "unhealthy", "detail": "status " + resp.Status})
	}
	return respond.OK(c, fiber.Map{"status": "healthy"})
}
