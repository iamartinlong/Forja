package api

// M3 验收反馈闭环（v2.0 计划 §五 M3）：Viewer/项目成员对交付版本提交 UAT 反馈 →
// requirements_inbox(source_type='UAT')，内部团队在 Inbox 板筛选后 convert → Spec → Task。
// 设计要点：零迁移（复用 requirements_inbox）；版本上下文注入 raw_content 首行；
// multipart 可选附件一次成型（避免为 Viewer 开放 inbox.write）。

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"path/filepath"
	"strings"

	"forja/backend/internal/api/respond"
	"forja/backend/internal/auth"
	"forja/backend/internal/events"
	idgen "forja/backend/internal/id"
	"forja/backend/internal/metrics"
	"forja/backend/internal/model"
	"forja/backend/internal/storage"

	"github.com/gofiber/fiber/v2"
)

// RegisterUATRoutes 项目级验收反馈入口（权限 inbox.uat）。
func RegisterUATRoutes(r fiber.Router) {
	r.Post("/projects/:id/uat", auth.Require("inbox.uat"), submitProjectUAT)
}

// uatResults 结论白名单 → 中文标签（注入 raw_content 首行，列表/详情可读）。
var uatResults = map[string]string{"pass": "通过", "partial": "部分通过", "fail": "不通过"}

const uatContentMax = 32 * 1024 // 正文上限 32KB；附件走 20MB 扩展名白名单

// submitProjectUAT POST /projects/:id/uat
// JSON：{result, content, title?, env?, delivery_id?}；multipart：同字段 + file。
func submitProjectUAT(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	projectID := c.Params("id")
	if !idgen.IsValid(projectID) {
		return respond.InvalidParam(c, "id", "invalid project id")
	}
	if err := AuthorizeProject(c, projectID, "inbox.uat"); err != nil {
		return err
	}
	isMultipart := strings.Contains(c.Get("Content-Type"), "multipart/form-data")
	var result, content, title, env, deliveryID string
	if isMultipart {
		result = strings.TrimSpace(c.FormValue("result"))
		content = strings.TrimSpace(c.FormValue("content"))
		title = strings.TrimSpace(c.FormValue("title"))
		env = strings.TrimSpace(c.FormValue("env"))
		deliveryID = strings.TrimSpace(c.FormValue("delivery_id"))
	} else {
		var req struct {
			Result     string `json:"result"`
			Content    string `json:"content"`
			Title      string `json:"title"`
			Env        string `json:"env"`
			DeliveryID string `json:"delivery_id"`
		}
		if err := c.BodyParser(&req); err != nil {
			return respond.BadRequest(c, err.Error())
		}
		result = strings.TrimSpace(req.Result)
		content = strings.TrimSpace(req.Content)
		title = strings.TrimSpace(req.Title)
		env = strings.TrimSpace(req.Env)
		deliveryID = strings.TrimSpace(req.DeliveryID)
	}
	resultCN, ok := uatResults[strings.ToLower(result)]
	if !ok {
		return respond.InvalidParam(c, "result", "result must be pass|partial|fail")
	}
	if content == "" {
		return respond.InvalidParam(c, "content", "content required")
	}
	if len(content) > uatContentMax {
		return respond.BusinessRule(c, "UAT_CONTENT_TOO_LARGE", "content too large (max 32KB)")
	}
	ctx := context.Background()
	// 版本上下文：可选关联交付包（必须属本项目）
	tag, sha := "", ""
	if deliveryID != "" {
		if !idgen.IsValid(deliveryID) {
			return respond.InvalidParam(c, "delivery_id", "invalid delivery_id")
		}
		if err := model.Pool.QueryRow(ctx,
			`SELECT tag, sha FROM project_deliveries WHERE id=$1 AND project_id=$2`,
			deliveryID, projectID).Scan(&tag, &sha); err != nil {
			return respond.NotFound(c, "delivery")
		}
	}
	// raw_content：首行结构化上下文（零迁移方案），正文随后
	var sb strings.Builder
	if tag != "" {
		fmt.Fprintf(&sb, "【UAT｜%s｜%s｜delivery:%s】", resultCN, tag, deliveryID[:8])
	} else {
		fmt.Fprintf(&sb, "【UAT｜%s】", resultCN)
	}
	if title != "" {
		sb.WriteString("\n")
		sb.WriteString(title)
	}
	if env != "" {
		sb.WriteString("\n环境: ")
		sb.WriteString(env)
	}
	sb.WriteString("\n\n")
	sb.WriteString(content)
	raw := sb.String()
	h := sha256.Sum256([]byte(raw))
	hash := hex.EncodeToString(h[:])
	// 幂等去重（同项目同内容）
	var existing string
	if err := model.Pool.QueryRow(ctx,
		`SELECT id FROM requirements_inbox WHERE project_id=$1 AND content_hash=$2 LIMIT 1`,
		projectID, hash).Scan(&existing); err == nil {
		return respond.OK(c, fiber.Map{"id": existing, "status": "NEW", "dedup": true, "result": result, "tag": tag, "sha": sha})
	}
	createdByID := currentUserID(c)
	if createdByID != "" && !idgen.IsValid(createdByID) {
		createdByID = ""
	}
	id := idgen.New()
	var err error
	if createdByID != "" {
		_, err = model.Pool.Exec(ctx, `
			INSERT INTO requirements_inbox (id, project_id, source_type, raw_content, content_hash, status, created_by)
			VALUES ($1,$2,'UAT',$3,$4,'NEW',$5)`, id, projectID, raw, hash, createdByID)
	} else {
		_, err = model.Pool.Exec(ctx, `
			INSERT INTO requirements_inbox (id, project_id, source_type, raw_content, content_hash, status)
			VALUES ($1,$2,'UAT',$3,$4,'NEW')`, id, projectID, raw, hash)
	}
	if err != nil {
		return respond.DBError(c, err)
	}
	// 可选附件（multipart）：校验 → FileStore → inbox_attachments（失败显错，禁静默）
	attachmentID := ""
	if isMultipart {
		if file, ferr := c.FormFile("file"); ferr == nil && file != nil {
			if verr := storage.ValidateFile(file.Filename, file.Size); verr != nil {
				if strings.Contains(verr.Error(), "too large") {
					return respond.BusinessRule(c, "FILE_TOO_LARGE", verr.Error())
				}
				if strings.Contains(verr.Error(), "not allowed") {
					return respond.BusinessRule(c, "FILE_TYPE_NOT_ALLOWED", verr.Error())
				}
				return respond.InvalidParam(c, "file", verr.Error())
			}
			f, oerr := file.Open()
			if oerr != nil {
				return respond.Upstream(c, "UPLOAD_FAILED", oerr)
			}
			data, rerr := io.ReadAll(io.LimitReader(f, storage.MaxFileSize+1))
			_ = f.Close()
			if rerr != nil {
				return respond.Upstream(c, "UPLOAD_FAILED", rerr)
			}
			if int64(len(data)) > storage.MaxFileSize {
				return respond.BusinessRule(c, "FILE_TOO_LARGE", "file too large >20MB")
			}
			mimeType := file.Header.Get("Content-Type")
			if mimeType == "" {
				mimeType = mime.TypeByExtension(strings.ToLower(filepath.Ext(file.Filename)))
				if mimeType == "" {
					mimeType = "application/octet-stream"
				}
			}
			store, serr := storage.Get()
			if serr != nil {
				return respond.Upstream(c, "STORAGE_UNAVAILABLE", serr)
			}
			key := storage.BuildKey("inbox", projectID, file.Filename)
			if perr := store.Put(ctx, key, bytes.NewReader(data), int64(len(data)), mimeType); perr != nil {
				return respond.Upstream(c, "STORAGE_PUT_FAILED", perr)
			}
			metrics.ObserveStorageUpload(storage.ConfigFromEnv().Driver, "inbox", int64(len(data)))
			attachmentID = idgen.New()
			if _, aerr := model.Pool.Exec(ctx, `
				INSERT INTO inbox_attachments (id, requirement_id, project_id, filename, size, mime, storage_key)
				VALUES ($1,$2,$3,$4,$5,$6,$7)`,
				attachmentID, id, projectID, file.Filename, len(data), mimeType, key); aerr != nil {
				_ = store.Delete(context.Background(), key)
				return respond.DBError(c, aerr)
			}
		}
	}
	username, _ := c.Locals("username").(string)
	if username == "" {
		username = "anonymous"
	}
	logAudit(ctx, projectID, username, "inbox.uat_submitted", "requirement", id,
		fmt.Sprintf(`{"result":%q,"tag":%q,"sha":%q,"delivery_id":%q}`, result, tag, sha, deliveryID))
	events.Default.Publish("project:"+projectID, "inbox.created",
		map[string]any{"id": id, "source_type": "UAT", "result": result, "tag": tag})
	out := fiber.Map{"id": id, "status": "NEW", "result": result, "tag": tag, "sha": sha}
	if attachmentID != "" {
		out["attachment_id"] = attachmentID
	}
	return respond.OK(c, out)
}
