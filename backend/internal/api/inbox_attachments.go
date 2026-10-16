package api

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"forja/backend/internal/api/respond"
	"forja/backend/internal/auth"
	"forja/backend/internal/metrics"
	"forja/backend/internal/model"
	"forja/backend/internal/storage"

	"github.com/gofiber/fiber/v2"
	idgen "forja/backend/internal/id"
	"github.com/jackc/pgx/v5"
)

func RegisterInboxAttachmentRoutes(r fiber.Router) {
	r.Post("/inbox/:id/attachments", auth.Require("inbox.write"), uploadInboxAttachment)
	r.Get("/inbox/:id/attachments", auth.Require("inbox.read"), listInboxAttachments)
	r.Get("/attachments/:id/download", auth.Require("inbox.read"), downloadInboxAttachment)
}

// uploadInboxAttachment POST /inbox/:id/attachments — 上传需求附件（multipart file）。
func uploadInboxAttachment(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	reqID := c.Params("id")
	if !idgen.IsValid(reqID) {
		return respond.InvalidParam(c, "id", "invalid id")
	}
	ctx := context.Background()
	var projectID string
	var status string
	err := model.Pool.QueryRow(ctx, `SELECT project_id::text, status FROM requirements_inbox WHERE id=$1`, reqID).Scan(&projectID, &status)
	if err == pgx.ErrNoRows {
		return respond.NotFound(c, "requirement")
	}
	if err != nil {
		return respond.DBError(c, err)
	}
	if err := AuthorizeProject(c, projectID, "inbox.write"); err != nil {
		return err
	}
	if status == "DISCARDED" {
		return respond.BusinessRule(c, "INBOX_DISCARDED", "discarded inbox cannot add attachments")
	}
	// 解析文件
	file, err := c.FormFile("file")
	if err != nil || file == nil {
		return respond.InvalidParam(c, "file", "file required (multipart field 'file')")
	}
	if err := storage.ValidateFile(file.Filename, file.Size); err != nil {
		// 统一 400 业务码
		if strings.Contains(err.Error(), "too large") {
			return respond.BusinessRule(c, "FILE_TOO_LARGE", err.Error())
		}
		if strings.Contains(err.Error(), "not allowed") {
			return respond.BusinessRule(c, "FILE_TYPE_NOT_ALLOWED", err.Error())
		}
		return respond.InvalidParam(c, "file", err.Error())
	}
	f, err := file.Open()
	if err != nil {
		return respond.Upstream(c, "UPLOAD_FAILED", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, storage.MaxFileSize+1))
	if err != nil {
		return respond.Upstream(c, "UPLOAD_FAILED", err)
	}
	if int64(len(data)) > storage.MaxFileSize {
		return respond.BusinessRule(c, "FILE_TOO_LARGE", "file too large >20MB")
	}
	// mime
	mimeType := file.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = mime.TypeByExtension(strings.ToLower(filepath.Ext(file.Filename)))
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
	}
	// storage
	store, err := storage.Get()
	if err != nil {
		return respond.Internal(c, err)
	}
	key := storage.BuildKey("inbox", projectID, file.Filename)
	if err := store.Put(ctx, key, bytes.NewReader(data), int64(len(data)), mimeType); err != nil {
		return respond.Upstream(c, "STORAGE_PUT_FAILED", err)
	}
	metrics.ObserveStorageUpload(storage.ConfigFromEnv().Driver, "inbox", int64(len(data)))
	id := idgen.New()
	_, err = model.Pool.Exec(ctx, `INSERT INTO inbox_attachments (id, requirement_id, project_id, filename, size, mime, storage_key) VALUES ($1,$2,$3,$4,$5,$6,$7)`, id, reqID, projectID, file.Filename, len(data), mimeType, key)
	if err != nil {
		// 回滚存储（尽力）
		_ = store.Delete(context.Background(), key)
		return respond.DBError(c, err)
	}
	username, _ := c.Locals("username").(string)
	if username == "" {
		username = "anonymous"
	}
	logAudit(ctx, projectID, username, "inbox.attachment.upload", "inbox_attachment", id, fmt.Sprintf(`{"requirement_id":%q,"filename":%q,"size":%d}`, reqID, file.Filename, len(data)))
	return respond.OK(c, fiber.Map{"id": id, "filename": file.Filename, "size": len(data), "mime": mimeType, "storage_key": key})
}

// listInboxAttachments GET /inbox/:id/attachments — 列出需求附件。
func listInboxAttachments(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	reqID := c.Params("id")
	if !idgen.IsValid(reqID) {
		return respond.InvalidParam(c, "id", "invalid id")
	}
	ctx := context.Background()
	var projectID string
	err := model.Pool.QueryRow(ctx, `SELECT project_id::text FROM requirements_inbox WHERE id=$1`, reqID).Scan(&projectID)
	if err == pgx.ErrNoRows {
		return respond.NotFound(c, "requirement")
	}
	if err != nil {
		return respond.DBError(c, err)
	}
	if err := AuthorizeProject(c, projectID, "inbox.read"); err != nil {
		return err
	}
	rows, err := model.Pool.Query(ctx, `SELECT id, filename, size, mime, storage_key, created_at FROM inbox_attachments WHERE requirement_id=$1 ORDER BY created_at DESC`, reqID)
	if err != nil {
		return respond.DBError(c, err)
	}
	defer rows.Close()
	items := []fiber.Map{}
	for rows.Next() {
		var id, filename, mimeType, key string
		var size int64
		var createdAt time.Time
		if err := rows.Scan(&id, &filename, &size, &mimeType, &key, &createdAt); err != nil {
			return respond.DBError(c, err)
		}
		items = append(items, fiber.Map{"id": id, "filename": filename, "size": size, "mime": mimeType, "storage_key": key, "created_at": createdAt})
	}
	if err := rows.Err(); err != nil {
		return respond.DBError(c, err)
	}
	return respond.OK(c, fiber.Map{"items": items})
}

// downloadInboxAttachment GET /attachments/:id/download — 鉴权后透传后端流式下载；大文件可选 presign。
func downloadInboxAttachment(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	attID := c.Params("id")
	if !idgen.IsValid(attID) {
		return respond.InvalidParam(c, "id", "invalid id")
	}
	ctx := context.Background()
	var projectID, filename, mimeType, key string
	var size int64
	err := model.Pool.QueryRow(ctx, `SELECT project_id::text, filename, mime, storage_key, size FROM inbox_attachments WHERE id=$1`, attID).Scan(&projectID, &filename, &mimeType, &key, &size)
	if err == pgx.ErrNoRows {
		return respond.NotFound(c, "attachment")
	}
	if err != nil {
		return respond.DBError(c, err)
	}
	if err := AuthorizeProject(c, projectID, "inbox.read"); err != nil {
		return err
	}
	store, err := storage.Get()
	if err != nil {
		return respond.Internal(c, err)
	}
	// Presign 仅大文件短 TTL ≤5min：?presign=true & size>1MB 演示（策略：鉴权后签）
	if c.Query("presign") == "true" {
		ttlStr := c.Query("ttl", "300") // 默认 5min
		// ttl 限制
		ttlSec := 300
		fmt.Sscan(ttlStr, &ttlSec)
		if ttlSec <= 0 {
			ttlSec = 300
		}
		if ttlSec > 300 {
			return respond.BusinessRule(c, "PRESIGN_TTL_TOO_LONG", "presign ttl must be ≤5min")
		}
		ttl := time.Duration(ttlSec) * time.Second
		url, err := store.Presign(ctx, key, ttl)
		if err != nil {
			// 本地驱动不支持 presign，回退到流式
			if strings.Contains(err.Error(), "not supported") {
				// fallback to streaming below
			} else {
				return respond.Upstream(c, "PRESIGN_FAILED", err)
			}
		} else {
			return respond.OK(c, fiber.Map{"url": url, "expires_in": ttlSec})
		}
	}
	rc, err := store.Open(ctx, key)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return respond.NotFound(c, "file")
		}
		return respond.Upstream(c, "STORAGE_OPEN_FAILED", err)
	}
	defer rc.Close()
	c.Set("Content-Type", mimeType)
	if mimeType == "" {
		c.Set("Content-Type", "application/octet-stream")
	}
	c.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, sanitizeDisposition(filename)))
	c.Set("Content-Length", fmt.Sprint(size))
	// Fiber 流式
	c.Status(http.StatusOK)
	_, copyErr := io.Copy(c.Response().BodyWriter(), rc)
	if copyErr != nil {
		// 连接已写，无法再返回 JSON
		return nil
	}
	return nil
}

func sanitizeDisposition(name string) string {
	// 简易防头部注入
	name = strings.ReplaceAll(name, `"`, "_")
	name = strings.ReplaceAll(name, "\r", "")
	name = strings.ReplaceAll(name, "\n", "")
	if name == "" {
		return "file"
	}
	return name
}
