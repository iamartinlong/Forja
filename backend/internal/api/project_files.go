package api

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
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

func RegisterProjectFileRoutes(r fiber.Router) {
	r.Post("/projects/:projectId/files", auth.Require("file.write"), uploadProjectFile)
	r.Get("/projects/:projectId/files", auth.Require("file.read"), listProjectFiles)
	r.Get("/project-files/:id/download", auth.Require("file.read"), downloadProjectFile)
	// 兼容别名：GET /projects/:projectId/files/:id/download 也可（便于前端）
	r.Get("/projects/:projectId/files/:id/download", auth.Require("file.read"), downloadProjectFileByProject)
	// M4b：资源库删除（DB 行 + FileStore 对象，best-effort 清理）
	r.Delete("/projects/:projectId/files/:id", auth.Require("file.write"), deleteProjectFile)
}

var validCategories = map[string]bool{
	"general": true, "brand": true, "contract": true, "api": true, "design": true, "requirement": true, "other": true,
}

// uploadProjectFile POST /projects/:projectId/files
// multipart: file, category, summary
func uploadProjectFile(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	projectID := c.Params("projectId")
	if !idgen.IsValid(projectID) {
		return respond.InvalidParam(c, "projectId", "invalid project_id")
	}
	if err := AuthorizeProject(c, projectID, "file.write"); err != nil {
		return err
	}
	file, err := c.FormFile("file")
	if err != nil || file == nil {
		return respond.InvalidParam(c, "file", "file required (multipart field 'file')")
	}
	if err := storage.ValidateFile(file.Filename, file.Size); err != nil {
		if strings.Contains(err.Error(), "too large") {
			return respond.BusinessRule(c, "FILE_TOO_LARGE", err.Error())
		}
		if strings.Contains(err.Error(), "not allowed") {
			return respond.BusinessRule(c, "FILE_TYPE_NOT_ALLOWED", err.Error())
		}
		return respond.InvalidParam(c, "file", err.Error())
	}
	category := strings.ToLower(strings.TrimSpace(c.FormValue("category")))
	if category == "" {
		category = "general"
	}
	if !validCategories[category] {
		return respond.InvalidParam(c, "category", "invalid category")
	}
	summary := strings.TrimSpace(c.FormValue("summary"))
	if len(summary) > 2000 {
		return respond.InvalidParam(c, "summary", "summary too long (max 2000)")
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
	mimeType := file.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = mime.TypeByExtension(strings.ToLower(filepath.Ext(file.Filename)))
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
	}
	store, err := storage.Get()
	if err != nil {
		return respond.Internal(c, err)
	}
	key := storage.BuildKey("project", projectID, file.Filename)
	if err := store.Put(context.Background(), key, bytes.NewReader(data), int64(len(data)), mimeType); err != nil {
		return respond.Upstream(c, "STORAGE_PUT_FAILED", err)
	}
	metrics.ObserveStorageUpload(storage.ConfigFromEnv().Driver, "project", int64(len(data)))
	id := idgen.New()
	createdBy := currentUserID(c)
	var createdByVal any = nil
	if createdBy != "" {
		if idgen.IsValid(createdBy) {
			createdByVal = createdBy
		}
	}
	ctx := context.Background()
	_, err = model.Pool.Exec(ctx, `INSERT INTO project_files (id, project_id, category, filename, summary, storage_key, size, mime, created_by) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, id, projectID, category, file.Filename, summary, key, len(data), mimeType, createdByVal)
	if err != nil {
		_ = store.Delete(context.Background(), key)
		return respond.DBError(c, err)
	}
	username, _ := c.Locals("username").(string)
	if username == "" {
		username = "anonymous"
	}
	logAudit(ctx, projectID, username, "project_file.upload", "project_file", id, fmt.Sprintf(`{"filename":%q,"category":%q,"size":%d}`, file.Filename, category, len(data)))
	return respond.OK(c, fiber.Map{"id": id, "filename": file.Filename, "size": len(data), "mime": mimeType, "category": category, "storage_key": key})
}

func listProjectFiles(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	projectID := c.Params("projectId")
	if !idgen.IsValid(projectID) {
		return respond.InvalidParam(c, "projectId", "invalid project_id")
	}
	if err := AuthorizeProject(c, projectID, "file.read"); err != nil {
		return err
	}
	category := c.Query("category")
	page, pageSize := respond.ParsePage(c)
	ctx := context.Background()
	where := ` WHERE project_id=$1`
	args := []any{projectID}
	if category != "" {
		where += ` AND category=$2`
		args = append(args, strings.ToLower(category))
	}
	var total int64
	if err := model.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM project_files`+where, args...).Scan(&total); err != nil {
		return respond.DBError(c, err)
	}
	// 构造带分页的查询
	query := `SELECT id, filename, category, COALESCE(summary,''), size, mime, storage_key, created_at FROM project_files` + where + fmt.Sprintf(` ORDER BY created_at DESC LIMIT %d OFFSET %d`, pageSize, (page-1)*pageSize)
	rows, err := model.Pool.Query(ctx, query, args...)
	if err != nil {
		return respond.DBError(c, err)
	}
	defer rows.Close()
	items := []fiber.Map{}
	for rows.Next() {
		var id, filename, cat, summary, mimeType, key string
		var size int64
		var createdAt time.Time
		if err := rows.Scan(&id, &filename, &cat, &summary, &size, &mimeType, &key, &createdAt); err != nil {
			return respond.DBError(c, err)
		}
		items = append(items, fiber.Map{"id": id, "filename": filename, "category": cat, "summary": summary, "size": size, "mime": mimeType, "storage_key": key, "created_at": createdAt})
	}
	if err := rows.Err(); err != nil {
		return respond.DBError(c, err)
	}
	return respond.List(c, items, respond.PageMeta{Page: page, PageSize: pageSize, Total: total})
}

func downloadProjectFile(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	fileID := c.Params("id")
	if !idgen.IsValid(fileID) {
		return respond.InvalidParam(c, "id", "invalid id")
	}
	ctx := context.Background()
	var projectID, filename, mimeType, key string
	var size int64
	err := model.Pool.QueryRow(ctx, `SELECT project_id::text, filename, mime, storage_key, size FROM project_files WHERE id=$1`, fileID).Scan(&projectID, &filename, &mimeType, &key, &size)
	if err == pgx.ErrNoRows {
		return respond.NotFound(c, "project_file")
	}
	if err != nil {
		return respond.DBError(c, err)
	}
	if err := AuthorizeProject(c, projectID, "file.read"); err != nil {
		return err
	}
	store, err := storage.Get()
	if err != nil {
		return respond.Internal(c, err)
	}
	if c.Query("presign") == "true" {
		ttlSec := 300
		fmt.Sscan(c.Query("ttl", "300"), &ttlSec)
		if ttlSec > 300 {
			return respond.BusinessRule(c, "PRESIGN_TTL_TOO_LONG", "presign ttl must be ≤5min")
		}
		if ttlSec <= 0 {
			ttlSec = 300
		}
		ttl := time.Duration(ttlSec) * time.Second
		url, err := store.Presign(ctx, key, ttl)
		if err == nil {
			return respond.OK(c, fiber.Map{"url": url, "expires_in": ttlSec})
		}
		if !strings.Contains(err.Error(), "not supported") {
			return respond.Upstream(c, "PRESIGN_FAILED", err)
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
	c.Status(http.StatusOK)
	_, copyErr := io.Copy(c.Response().BodyWriter(), rc)
	if copyErr != nil {
		return nil
	}
	return nil
}

// downloadProjectFileByProject 兼容路径：GET /projects/:projectId/files/:id/download（校验 projectId 一致）
func downloadProjectFileByProject(c *fiber.Ctx) error {
	projectID := c.Params("projectId")
	if !idgen.IsValid(projectID) {
		return respond.InvalidParam(c, "projectId", "invalid project_id")
	}
	fileID := c.Params("id")
	if !idgen.IsValid(fileID) {
		return respond.InvalidParam(c, "id", "invalid id")
	}
	ctx := context.Background()
	var actualPID, filename, mimeType, key string
	var size int64
	err := model.Pool.QueryRow(ctx, `SELECT project_id::text, filename, mime, storage_key, size FROM project_files WHERE id=$1`, fileID).Scan(&actualPID, &filename, &mimeType, &key, &size)
	if err == pgx.ErrNoRows {
		return respond.NotFound(c, "project_file")
	}
	if err != nil {
		return respond.DBError(c, err)
	}
	if actualPID != projectID {
		return respond.InvalidParam(c, "projectId", "project mismatch")
	}
	if err := AuthorizeProject(c, projectID, "file.read"); err != nil {
		return err
	}
	store, err := storage.Get()
	if err != nil {
		return respond.Internal(c, err)
	}
	if c.Query("presign") == "true" {
		ttlSec := 300
		fmt.Sscan(c.Query("ttl", "300"), &ttlSec)
		if ttlSec > 300 {
			return respond.BusinessRule(c, "PRESIGN_TTL_TOO_LONG", "presign ttl must be ≤5min")
		}
		if ttlSec <= 0 {
			ttlSec = 300
		}
		ttl := time.Duration(ttlSec) * time.Second
		url, err := store.Presign(ctx, key, ttl)
		if err == nil {
			return respond.OK(c, fiber.Map{"url": url, "expires_in": ttlSec})
		}
		if !strings.Contains(err.Error(), "not supported") {
			return respond.Upstream(c, "PRESIGN_FAILED", err)
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
	c.Status(http.StatusOK)
	_, _ = io.Copy(c.Response().BodyWriter(), rc)
	return nil
}

// deleteProjectFile DELETE /projects/:projectId/files/:id —— 资源库文件删除（M4b）：
// 先删 DB 行再 best-effort 清 FileStore 对象（对象清理失败仅告警，不回滚）。
func deleteProjectFile(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	projectID := c.Params("projectId")
	if !idgen.IsValid(projectID) {
		return respond.InvalidParam(c, "projectId", "invalid project_id")
	}
	fileID := c.Params("id")
	if !idgen.IsValid(fileID) {
		return respond.InvalidParam(c, "id", "invalid id")
	}
	if err := AuthorizeProject(c, projectID, "file.write"); err != nil {
		return err
	}
	ctx := context.Background()
	var filename, key string
	err := model.Pool.QueryRow(ctx,
		`SELECT filename, storage_key FROM project_files WHERE id=$1 AND project_id=$2`,
		fileID, projectID).Scan(&filename, &key)
	if err == pgx.ErrNoRows {
		return respond.NotFound(c, "project_file")
	}
	if err != nil {
		return respond.DBError(c, err)
	}
	if _, err := model.Pool.Exec(ctx, `DELETE FROM project_files WHERE id=$1`, fileID); err != nil {
		return respond.DBError(c, err)
	}
	if st, serr := storage.Get(); serr == nil && key != "" {
		if derr := st.Delete(ctx, key); derr != nil {
			slog.Warn("project_file delete: object delete failed", "file_id", fileID, "key", key, "err", derr.Error())
		}
	}
	username, _ := c.Locals("username").(string)
	logAudit(ctx, projectID, username, "project_file.delete", "project_file", fileID,
		fmt.Sprintf(`{"filename":%q}`, filename))
	return respond.OK(c, fiber.Map{"id": fileID, "deleted": true})
}
