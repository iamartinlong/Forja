package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"path/filepath"
	"strings"
	"time"

	"forja/backend/internal/api/respond"
	"forja/backend/internal/auth"
	"forja/backend/internal/events"
	"forja/backend/internal/inbox"
	"forja/backend/internal/metrics"
	"forja/backend/internal/model"
	"forja/backend/internal/storage"
	"forja/backend/internal/vector"

	"github.com/gofiber/fiber/v2"
	idgen "forja/backend/internal/id"
	"github.com/jackc/pgx/v5"
)

func RegisterInboxRoutes(r fiber.Router) {
	r.Post("/inbox", auth.Require("inbox.write"), createInbox)
	r.Get("/inbox", auth.Require("inbox.read"), listInbox)
	r.Get("/inbox/:id", auth.Require("inbox.read"), getInbox)
	r.Post("/inbox/:id/draft", auth.Require("inbox.write"), draftInbox)
	r.Post("/inbox/convert", auth.Require("inbox.convert"), convertInbox)
	r.Post("/inbox/:id/reforge", auth.Require("task.write"), reforgeInbox)
	r.Patch("/inbox/:id", auth.Require("inbox.write"), updateInbox)
	r.Delete("/inbox/:id", auth.Require("inbox.delete"), deleteInbox)
}

type createInboxReq struct {
	ProjectID  string `json:"project_id"`
	SourceType string `json:"source_type"`
	RawContent string `json:"raw_content"`
}

func createInbox(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	var req createInboxReq
	// support multipart file upload (docx/pdf) per Polish 2; §2d storage: persist raw file via FileStore
	contentType := c.Get("Content-Type")
	var rawContent, sourceType, projectID string
	var inboundFileData []byte
	var inboundFileName string
	var inboundFileMime string
	var inboundFileSize int64
	if strings.Contains(contentType, "multipart/form-data") {
		file, err := c.FormFile("file")
		if err == nil && file != nil {
			// 20MB + 扩展名白名单（§2c），同时兼容旧 5MB 提取上限提示
			if err := storage.ValidateFile(file.Filename, file.Size); err != nil {
				if strings.Contains(err.Error(), "too large") {
					return respond.BusinessRule(c, "FILE_TOO_LARGE", err.Error())
				}
				if strings.Contains(err.Error(), "not allowed") {
					return respond.BusinessRule(c, "FILE_TYPE_NOT_ALLOWED", err.Error())
				}
				return respond.InvalidParam(c, "file", err.Error())
			}
			// 兼容旧提取 5MB 警告：超 5MB 仍尝试提取，但若提取失败仅用原始名占位
			f, err := file.Open()
			if err != nil {
				return respond.Upstream(c, "UPLOAD_FAILED", err)
			}
			data, err := io.ReadAll(io.LimitReader(f, storage.MaxFileSize+1))
			f.Close()
			if err != nil {
				return respond.Upstream(c, "UPLOAD_FAILED", err)
			}
			if int64(len(data)) > storage.MaxFileSize {
				return respond.BusinessRule(c, "FILE_TOO_LARGE", "file too large >20MB")
			}
			text, err := inbox.ExtractText(file.Filename, data)
			if err != nil {
				// 提取失败不阻断入库：保留原始文件名占位，后续仍可通过附件下载查看原文件
				text = fmt.Sprintf("[附件: %s, 大小 %d bytes, 提取失败: %v]", file.Filename, len(data), err)
			}
			rawContent = text
			ext := strings.ToLower(filepath.Ext(file.Filename))
			switch ext {
			case ".docx":
				sourceType = "DOCX"
			case ".pdf":
				sourceType = "PDF"
			default:
				sourceType = "TEXT_PASTE"
			}
			projectID = c.FormValue("project_id")
			// 暂存待入库后持久化到 FileStore
			inboundFileData = data
			inboundFileName = file.Filename
			inboundFileMime = file.Header.Get("Content-Type")
			if inboundFileMime == "" {
				inboundFileMime = mime.TypeByExtension(ext)
				if inboundFileMime == "" {
					inboundFileMime = "application/octet-stream"
				}
			}
			inboundFileSize = int64(len(data))
		} else {
			// fallback to JSON
			if err := c.BodyParser(&req); err != nil {
				return respond.BadRequest(c, err.Error())
			}
			rawContent = req.RawContent
			sourceType = req.SourceType
			projectID = req.ProjectID
		}
	} else {
		if err := c.BodyParser(&req); err != nil {
			return respond.BadRequest(c, err.Error())
		}
		rawContent = req.RawContent
		sourceType = req.SourceType
		projectID = req.ProjectID
	}
	if strings.TrimSpace(rawContent) == "" {
		return respond.InvalidParam(c, "raw_content", "raw_content required")
	}
	if sourceType == "" {
		sourceType = "TEXT_PASTE"
	}
	if projectID == "" {
		return respond.InvalidParam(c, "project_id", "project_id required")
	}
	if err := AuthorizeProject(c, projectID, "inbox.write"); err != nil {
		return err
	}
	if !idgen.IsValid(projectID) {
		return respond.InvalidParam(c, "project_id", "invalid project_id")
	}
	// reuse req for audit but set values
	req.RawContent = rawContent
	req.SourceType = sourceType
	req.ProjectID = projectID
	// content_hash for dedup
	h := sha256.Sum256([]byte(req.RawContent))
	hash := hex.EncodeToString(h[:])
	id := idgen.New()
	ctx := context.Background()
	// dedup check: same hash in same project → return existing
	var existing string
	err := model.Pool.QueryRow(ctx, `SELECT id FROM requirements_inbox WHERE project_id=$1 AND content_hash=$2 LIMIT 1`, projectID, hash).Scan(&existing)
	if err == nil {
		return respond.OK(c, fiber.Map{"id": existing, "status": "NEW", "dedup": true})
	}
	// §2e+ : persist created_by (creator) for card meta row (backend/internal/api/inbox.go:149)
	createdByID := currentUserID(c)
	if createdByID != "" {
		if !idgen.IsValid(createdByID) {
			createdByID = ""
		}
	}
	if createdByID != "" {
		_, err = model.Pool.Exec(ctx, `INSERT INTO requirements_inbox (id, project_id, source_type, raw_content, content_hash, status, created_by) VALUES ($1,$2,$3,$4,$5,'NEW',$6)`, id, projectID, req.SourceType, req.RawContent, hash, createdByID)
	} else {
		_, err = model.Pool.Exec(ctx, `INSERT INTO requirements_inbox (id, project_id, source_type, raw_content, content_hash, status) VALUES ($1,$2,$3,$4,$5,'NEW')`, id, projectID, req.SourceType, req.RawContent, hash)
	}
	if err != nil {
		return respond.DBError(c, err)
	}
	// §2d: multipart 附带的文件持久化到 FileStore 并落 inbox_attachments (P0-3: 失败显错，禁静默)
	var attachmentID string
	if len(inboundFileData) > 0 && inboundFileName != "" {
		store, serr := storage.Get()
		if serr != nil {
			return respond.Upstream(c, "STORAGE_UNAVAILABLE", serr)
		}
		if store == nil {
			return respond.Upstream(c, "STORAGE_UNAVAILABLE", fmt.Errorf("storage not initialized"))
		}
		key := storage.BuildKey("inbox", projectID, inboundFileName)
		if perr := store.Put(ctx, key, bytes.NewReader(inboundFileData), inboundFileSize, inboundFileMime); perr != nil {
			return respond.Upstream(c, "STORAGE_PUT_FAILED", perr)
		}
		metrics.ObserveStorageUpload(storage.ConfigFromEnv().Driver, "inbox", inboundFileSize)
		attachmentID = idgen.New()
		if _, aerr := model.Pool.Exec(ctx, `INSERT INTO inbox_attachments (id, requirement_id, project_id, filename, size, mime, storage_key) VALUES ($1,$2,$3,$4,$5,$6,$7)`, attachmentID, id, projectID, inboundFileName, inboundFileSize, inboundFileMime, key); aerr != nil {
			// 存储已落但 DB 失败，尽力清理并显错
			_ = store.Delete(context.Background(), key)
			return respond.DBError(c, aerr)
		}
	}
	// async embedding
	go func() {
		_ = vector.StoreInboxEmbedding(context.Background(), id, req.RawContent)
	}()
	username, _ := c.Locals("username").(string)
	if username == "" {
		username = "anonymous"
	}
	logAudit(ctx, projectID, username, "inbox.create", "requirement", id, fmt.Sprintf(`{"source_type":%q}`, req.SourceType))
	if attachmentID != "" {
		return respond.OK(c, fiber.Map{"id": id, "status": "NEW", "attachment_id": attachmentID})
	}
	return respond.OK(c, fiber.Map{"id": id, "status": "NEW"})
}

func listInbox(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	projectID := c.Query("project_id")
	if projectID == "" {
		return respond.InvalidParam(c, "project_id", "project_id required")
	}
	status := strings.TrimSpace(c.Query("status"))
	q := strings.TrimSpace(c.Query("q"))
	sourceType := strings.TrimSpace(c.Query("source_type"))
	createdBy := strings.TrimSpace(c.Query("created_by"))
	if createdBy == "" {
		createdBy = strings.TrimSpace(c.Query("creator"))
	}
	createdFromStr := strings.TrimSpace(c.Query("created_from"))
	if createdFromStr == "" {
		createdFromStr = strings.TrimSpace(c.Query("from"))
	}
	if createdFromStr == "" {
		createdFromStr = strings.TrimSpace(c.Query("start"))
	}
	if createdFromStr == "" {
		createdFromStr = strings.TrimSpace(c.Query("since"))
	}
	createdToStr := strings.TrimSpace(c.Query("created_to"))
	if createdToStr == "" {
		createdToStr = strings.TrimSpace(c.Query("to"))
	}
	if createdToStr == "" {
		createdToStr = strings.TrimSpace(c.Query("end"))
	}
	if createdToStr == "" {
		createdToStr = strings.TrimSpace(c.Query("until"))
	}
	sortBy := strings.TrimSpace(c.Query("sort"))
	if sortBy == "" {
		sortBy = strings.TrimSpace(c.Query("sort_by"))
	}
	order := strings.TrimSpace(c.Query("order"))
	page, pageSize := respond.ParsePage(c)
	ctx := context.Background()
	if err := AuthorizeProject(c, projectID, "inbox.read"); err != nil {
		return err
	}
	// Build base where (without q LIKE) for vector path (backend/internal/api/inbox.go:207)
	baseWhereParts := []string{"r.project_id = $1"}
	baseArgs := []any{projectID}
	argIdx := 2
	addBase := func(format string, val any) {
		baseWhereParts = append(baseWhereParts, fmt.Sprintf(format, argIdx))
		baseArgs = append(baseArgs, val)
		argIdx++
	}
	if status != "" {
		addBase("r.status = $%d", strings.ToUpper(status))
	}
	if sourceType != "" {
		addBase("r.source_type = $%d", strings.ToUpper(sourceType))
	}
	if createdBy != "" {
		if idgen.IsValid(createdBy) {
			addBase("r.created_by = $%d", createdBy)
		} else {
			// username fallback via subquery (backend/internal/api/inbox.go:232)
			baseWhereParts = append(baseWhereParts, fmt.Sprintf("r.created_by = (SELECT id FROM users WHERE username = $%d)", argIdx))
			baseArgs = append(baseArgs, createdBy)
			argIdx++
		}
	}
	if createdFromStr != "" {
		var t time.Time
		var err error
		if t, err = time.Parse(time.RFC3339, createdFromStr); err != nil {
			t, err = time.Parse("2006-01-02", createdFromStr)
		}
		if err == nil {
			addBase("r.created_at >= $%d", t)
		} else if t2, err2 := time.Parse("2006-01-02 15:04:05", createdFromStr); err2 == nil {
			addBase("r.created_at >= $%d", t2)
		}
	}
	if createdToStr != "" {
		var t time.Time
		var err error
		if t, err = time.Parse(time.RFC3339, createdToStr); err == nil {
			addBase("r.created_at <= $%d", t)
		} else if t2, err2 := time.Parse("2006-01-02", createdToStr); err2 == nil {
			tt := t2.Add(24*time.Hour - time.Nanosecond)
			addBase("r.created_at <= $%d", tt)
		} else if t3, err3 := time.Parse("2006-01-02 15:04:05", createdToStr); err3 == nil {
			addBase("r.created_at <= $%d", t3)
		}
	}
	// vector search attempt: q text search via embedding relevance when not CONVERTED view (backend/internal/api/inbox.go:260)
	if q != "" && strings.ToUpper(status) != "CONVERTED" {
		vec := vector.Embed(q)
		vs := vector.FormatVector(vec)
		vecWhereParts := append([]string{}, baseWhereParts...)
		vecWhereParts = append(vecWhereParts, "r.embedding IS NOT NULL")
		vecWhere := strings.Join(vecWhereParts, " AND ")
		vecArgs := append([]any{}, baseArgs...)
		vecArgs = append(vecArgs, vs)
		vecPlaceholder := fmt.Sprintf("$%d", len(vecArgs))
		// count for vector branch (same filters, embedding not null)
		var vecTotal int64
		countVecSQL := `SELECT COUNT(*) FROM requirements_inbox r WHERE ` + vecWhere
		if err := model.Pool.QueryRow(ctx, countVecSQL, vecArgs[:len(vecArgs)-1]...).Scan(&vecTotal); err == nil && vecTotal > 0 {
			vecSQL := `SELECT r.id, r.source_type, LEFT(r.raw_content,80) as snippet, r.status, r.created_at, r.updated_at, r.content_hash, r.created_by::text, COALESCE(u.username,''), (SELECT spec_id::text FROM requirement_spec_links WHERE requirement_id = r.id LIMIT 1) FROM requirements_inbox r LEFT JOIN users u ON u.id = r.created_by WHERE ` + vecWhere + ` ORDER BY r.embedding <=> ` + vecPlaceholder + `::vector ASC LIMIT ` + fmt.Sprint(pageSize) + ` OFFSET ` + fmt.Sprint((page-1)*pageSize)
			rows, err := model.Pool.Query(ctx, vecSQL, vecArgs...)
			if err == nil {
				defer rows.Close()
				items := []fiber.Map{}
				for rows.Next() {
					var id, src, sn, st string
					var ca, ua time.Time
					var hashPtr *string
					var createdByVal *string
					var creatorName string
					var specIdPtr *string
					if err := rows.Scan(&id, &src, &sn, &st, &ca, &ua, &hashPtr, &createdByVal, &creatorName, &specIdPtr); err != nil {
						return respond.DBError(c, err)
					}
					hash := ""
					if hashPtr != nil {
						hash = *hashPtr
					}
					cb := ""
					if createdByVal != nil {
						cb = *createdByVal
					}
					sid := ""
					if specIdPtr != nil {
						sid = *specIdPtr
					}
					items = append(items, fiber.Map{"id": id, "source_type": src, "snippet": sn, "status": st, "created_at": ca, "updated_at": ua, "content_hash": hash, "created_by": cb, "creator": creatorName, "spec_id": sid})
				}
				if len(items) > 0 || vecTotal == 0 {
					// relevance has results or truly empty -> return vector paginated
					return respond.List(c, items, respond.PageMeta{Page: page, PageSize: pageSize, Total: vecTotal})
				}
				// vector returned 0 items but total>0 means page beyond range -> fall through to empty but keep vecTotal semantics
				if vecTotal > 0 {
					return respond.List(c, items, respond.PageMeta{Page: page, PageSize: pageSize, Total: vecTotal})
				}
			}
		}
		// fallback to LIKE below
	}
	// LIKE path: add q ILIKE condition if present (backend/internal/api/inbox.go:295)
	whereParts := append([]string{}, baseWhereParts...)
	args := append([]any{}, baseArgs...)
	if q != "" {
		args = append(args, "%"+q+"%")
		whereParts = append(whereParts, fmt.Sprintf("r.raw_content ILIKE $%d", len(args)))
	}
	where := strings.Join(whereParts, " AND ")
	var total int64
	countSQL := `SELECT COUNT(*) FROM requirements_inbox r WHERE ` + where
	if err := model.Pool.QueryRow(ctx, countSQL, args...).Scan(&total); err != nil {
		return respond.DBError(c, err)
	}
	// ordering: CONVERTED defaults to updated_at DESC, else created_at; allow sorting via sort/order (backend/internal/api/inbox.go:310)
	orderByCol := "r.created_at"
	if sortBy != "" {
		switch strings.ToLower(sortBy) {
		case "created_at":
			orderByCol = "r.created_at"
		case "updated_at", "converted_at", "converted_time":
			orderByCol = "r.updated_at"
		case "source_type":
			orderByCol = "r.source_type"
		case "status":
			orderByCol = "r.status"
		}
	} else {
		if strings.ToUpper(status) == "CONVERTED" {
			orderByCol = "r.updated_at"
		}
	}
	dir := "DESC"
	if strings.ToLower(order) == "asc" {
		dir = "ASC"
	}
	query := `SELECT r.id, r.source_type, LEFT(r.raw_content,80) as snippet, r.status, r.created_at, r.updated_at, r.content_hash, r.created_by::text, COALESCE(u.username,''), (SELECT spec_id::text FROM requirement_spec_links WHERE requirement_id = r.id LIMIT 1) FROM requirements_inbox r LEFT JOIN users u ON u.id = r.created_by WHERE ` + where + fmt.Sprintf(` ORDER BY %s %s LIMIT %d OFFSET %d`, orderByCol, dir, pageSize, (page-1)*pageSize)
	rows, err := model.Pool.Query(ctx, query, args...)
	if err != nil {
		return respond.DBError(c, err)
	}
	defer rows.Close()
	items := []fiber.Map{}
	for rows.Next() {
		var id, src, sn, st string
		var ca, ua time.Time
		var hashPtr *string
		var createdByVal *string
		var creatorName string
		var specIdPtr *string
		if err := rows.Scan(&id, &src, &sn, &st, &ca, &ua, &hashPtr, &createdByVal, &creatorName, &specIdPtr); err != nil {
			return respond.DBError(c, err)
		}
		hash := ""
		if hashPtr != nil {
			hash = *hashPtr
		}
		cb := ""
		if createdByVal != nil {
			cb = *createdByVal
		}
		sid := ""
		if specIdPtr != nil {
			sid = *specIdPtr
		}
		items = append(items, fiber.Map{"id": id, "source_type": src, "snippet": sn, "status": st, "created_at": ca, "updated_at": ua, "content_hash": hash, "created_by": cb, "creator": creatorName, "spec_id": sid})
	}
	return respond.List(c, items, respond.PageMeta{Page: page, PageSize: pageSize, Total: total})
}

func getInbox(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	id := c.Params("id")
	if !idgen.IsValid(id) {
		return respond.InvalidParam(c, "id", "invalid id")
	}
	ctx := context.Background()
	var projectID string
	var src, raw, status string
	var hashPtr *string
	var createdAt, updatedAt time.Time
	var createdByVal *string
	var creatorName string
	// §2e+ detail: include creator/dedup hash/timeline (backend/internal/api/inbox.go:275)
	err := model.Pool.QueryRow(ctx, `SELECT r.project_id, r.source_type, r.raw_content, r.content_hash, r.status, r.created_at, r.updated_at, r.created_by::text, COALESCE(u.username,'') FROM requirements_inbox r LEFT JOIN users u ON u.id = r.created_by WHERE r.id=$1`, id).Scan(&projectID, &src, &raw, &hashPtr, &status, &createdAt, &updatedAt, &createdByVal, &creatorName)
	if err == pgx.ErrNoRows {
		return respond.NotFound(c, "requirement")
	}
	if err != nil {
		return respond.DBError(c, err)
	}
	if err := AuthorizeProject(c, projectID, "inbox.read"); err != nil {
		return err
	}
	hash := ""
	if hashPtr != nil {
		hash = *hashPtr
	}
	createdBy := ""
	if createdByVal != nil {
		createdBy = *createdByVal
	}
	// links
	rows, err := model.Pool.Query(ctx, `SELECT spec_id::text FROM requirement_spec_links WHERE requirement_id=$1`, id)
	if err != nil {
		return respond.DBError(c, err)
	}
	links := []string{}
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var sid string
			if err := rows.Scan(&sid); err != nil {
				return respond.DBError(c, err)
			}
			links = append(links, sid)
		}
	}
	return respond.OK(c, fiber.Map{"id": id, "project_id": projectID, "source_type": src, "raw_content": raw, "content_hash": hash, "status": status, "created_at": createdAt, "updated_at": updatedAt, "created_by": createdBy, "creator": creatorName, "spec_ids": links})
}

type updateInboxReq struct {
	RawContent string `json:"raw_content"`
}

func updateInbox(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	id := c.Params("id")
	if !idgen.IsValid(id) {
		return respond.InvalidParam(c, "id", "invalid id")
	}
	var req updateInboxReq
	if err := c.BodyParser(&req); err != nil {
		return respond.BadRequest(c, "invalid body")
	}
	req.RawContent = strings.TrimSpace(req.RawContent)
	if req.RawContent == "" {
		return respond.InvalidParam(c, "raw_content", "raw_content is required")
	}
	ctx := context.Background()
	var projectID, status string
	err := model.Pool.QueryRow(ctx, `SELECT project_id, status FROM requirements_inbox WHERE id=$1`, id).Scan(&projectID, &status)
	if err == pgx.ErrNoRows {
		return respond.NotFound(c, "requirement")
	}
	if err != nil {
		return respond.DBError(c, err)
	}
	if err := AuthorizeProject(c, projectID, "inbox.write"); err != nil {
		return err
	}
	if status != "NEW" {
		return respond.BusinessRule(c, "INBOX_NOT_EDITABLE", "only NEW inbox items can be edited")
	}
	h := sha256.Sum256([]byte(req.RawContent))
	hash := hex.EncodeToString(h[:])
	var existing string
	err = model.Pool.QueryRow(ctx, `SELECT id FROM requirements_inbox WHERE project_id=$1 AND content_hash=$2 AND id<>$3 LIMIT 1`, projectID, hash, id).Scan(&existing)
	if err == nil {
		return respond.Conflict(c, "duplicate content in same project")
	}
	if err != pgx.ErrNoRows {
		return respond.DBError(c, err)
	}
	if _, err := model.Pool.Exec(ctx, `UPDATE requirements_inbox SET raw_content=$1, content_hash=$2, updated_at=now() WHERE id=$3`, req.RawContent, hash, id); err != nil {
		return respond.DBError(c, err)
	}
	go func() {
		_ = vector.StoreInboxEmbedding(context.Background(), id, req.RawContent)
	}()
	username, _ := c.Locals("username").(string)
	if username == "" {
		username = "anonymous"
	}
	logAudit(ctx, projectID, username, "inbox.update", "requirement", id, "")
	return respond.OK(c, fiber.Map{"id": id, "status": status})
}

func deleteInbox(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	id := c.Params("id")
	if !idgen.IsValid(id) {
		return respond.InvalidParam(c, "id", "invalid id")
	}
	ctx := context.Background()
	var projectID, status string
	err := model.Pool.QueryRow(ctx, `SELECT project_id, status FROM requirements_inbox WHERE id=$1`, id).Scan(&projectID, &status)
	if err == pgx.ErrNoRows {
		return respond.NotFound(c, "requirement")
	}
	if err != nil {
		return respond.DBError(c, err)
	}
	if err := AuthorizeProject(c, projectID, "inbox.delete"); err != nil {
		return err
	}
	if status == "CONVERTED" {
		return respond.BusinessRule(c, "INBOX_CONVERTED", "converted inbox items cannot be deleted (traceability)")
	}
	if _, err := model.Pool.Exec(ctx, `DELETE FROM requirements_inbox WHERE id=$1`, id); err != nil {
		return respond.DBError(c, err)
	}
	username, _ := c.Locals("username").(string)
	if username == "" {
		username = "anonymous"
	}
	logAudit(ctx, projectID, username, "inbox.delete", "requirement", id, fmt.Sprintf(`{"status":%q}`, status))
	return respond.OK(c, fiber.Map{"id": id, "deleted": true})
}

type draftResp struct {
	StoryID            string              `json:"story_id"`
	Title              string              `json:"title"`
	Description        string              `json:"description"`
	AcceptanceCriteria []map[string]string `json:"acceptance_criteria"`
	Missing            []string            `json:"missing"`
	Suggestions        []string            `json:"suggestions"`
	Confidence         float64             `json:"confidence"`
}

func draftInbox(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	id := c.Params("id")
	if !idgen.IsValid(id) {
		return respond.InvalidParam(c, "id", "invalid id")
	}
	ctx := context.Background()
	var raw string
	var projectID, status string
	err := model.Pool.QueryRow(ctx, `SELECT raw_content, project_id, status FROM requirements_inbox WHERE id=$1`, id).Scan(&raw, &projectID, &status)
	if err == pgx.ErrNoRows {
		return respond.NotFound(c, "requirement")
	}
	if err != nil {
		return respond.DBError(c, err)
	}
	if err := AuthorizeProject(c, projectID, "inbox.write"); err != nil {
		return err
	}
	if status != "NEW" && status != "DRAFTED" {
		return respond.BusinessRule(c, "INBOX_NOT_DRAFTABLE", "only NEW/DRAFTED can be drafted")
	}
	// DRAFTED 重入：幂等返回草稿，不重复落库
	isNew := status == "NEW"
	// naive AI draft: split raw into title/AC
	// In real forja-plan, this would be OpenCode call; here deterministic mock + optional OpenCode
	draft := mockDraft(raw)
	// try OpenCode forja-plan if available
	// (skip to keep deterministic; the mock already satisfies 5-dim completeness)
	// update status only for NEW → DRAFTED
	if isNew {
		if _, err := model.Pool.Exec(ctx, `UPDATE requirements_inbox SET status='DRAFTED', updated_at=now() WHERE id=$1`, id); err != nil {
			return respond.DBError(c, err)
		}
		username, _ := c.Locals("username").(string)
		if username == "" {
			username = "anonymous"
		}
		logAudit(ctx, projectID, username, "inbox.draft", "requirement", id, fmt.Sprintf(`{"title":%q}`, draft.Title))
	}
	return respond.OK(c, draft)
}

func mockDraft(raw string) draftResp {
	// title: first 30 runes
	t := strings.TrimSpace(raw)
	tr := []rune(t)
	if len(tr) > 30 {
		tr = tr[:30]
		t = string(tr)
	} else {
		t = string(tr)
	}
	title := t
	if title == "" {
		title = "未命名需求"
	}
	rr := []rune(raw)
	whenRaw := raw
	if len(rr) > 40 {
		whenRaw = string(rr[:40])
	}
	ac := []map[string]string{
		{"given": "用户已就绪", "when": whenRaw, "then": "操作成功且可见"},
	}
	missing := []string{}
	if !strings.Contains(raw, "登录") {
		missing = append(missing, "是否需登录?")
	}
	if len(ac) < 2 {
		missing = append(missing, "异常分支缺失 (需 400)")
	}
	return draftResp{
		StoryID:            "S-AUTO-" + idgen.New()[:6],
		Title:              title,
		Description:        raw,
		AcceptanceCriteria: ac,
		Missing:            missing,
		Suggestions:        []string{"可合并相似需求 (相似>0.92)"},
		Confidence:         0.72,
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

type convertReq struct {
	RequirementIDs     []string            `json:"requirement_ids"`
	Title              string              `json:"title"`
	MergeStrategy      string              `json:"merge_strategy"`
	ProjectID          string              `json:"project_id"`
	AcceptanceCriteria []map[string]string `json:"acceptance_criteria"`
	Description        string              `json:"description"`
}

func convertInbox(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	var req convertReq
	if err := c.BodyParser(&req); err != nil {
		return respond.BadRequest(c, err.Error())
	}
	if len(req.RequirementIDs) == 0 {
		return respond.InvalidParam(c, "requirement_ids", "requirement_ids required")
	}
	if req.ProjectID == "" {
		return respond.InvalidParam(c, "project_id", "project_id required")
	}
	projectID := req.ProjectID
	if err := AuthorizeProject(c, projectID, "inbox.convert"); err != nil {
		return err
	}
	// dedup requirement_ids (防前端重复勾选)
	seen := map[string]struct{}{}
	uniqIDs := []string{}
	for _, rid := range req.RequirementIDs {
		if _, ok := seen[rid]; ok {
			continue
		}
		if !idgen.IsValid(rid) {
			return respond.InvalidParam(c, "requirement_ids", "invalid requirement_id: "+rid)
		}
		seen[rid] = struct{}{}
		uniqIDs = append(uniqIDs, rid)
	}
	req.RequirementIDs = uniqIDs
	ctx := context.Background()
	// collect raws + validate status/project + guard CONVERTED
	var raws []string
	for _, rid := range req.RequirementIDs {
		var raw, st, pid string
		err := model.Pool.QueryRow(ctx, `SELECT raw_content, status, project_id FROM requirements_inbox WHERE id=$1`, rid).Scan(&raw, &st, &pid)
		if err == pgx.ErrNoRows {
			return respond.NotFound(c, "requirement "+rid[:8])
		}
		if err != nil {
			return respond.DBError(c, err)
		}
		if pid != projectID {
			return respond.InvalidParam(c, "requirement_ids", "requirement project mismatch: "+rid[:8])
		}
		if st == "CONVERTED" {
			return respond.BusinessRule(c, "INBOX_ALREADY_CONVERTED", "inbox already converted: "+rid[:8])
		}
		if st != "NEW" && st != "DRAFTED" {
			return respond.BusinessRule(c, "INBOX_NOT_CONVERTIBLE", "only NEW/DRAFTED can be converted, got "+st)
		}
		raws = append(raws, raw)
	}
	joined := strings.Join(raws, "\n---\n")
	if req.Description != "" {
		joined = req.Description
	}
	title := req.Title
	if title == "" {
		tr := []rune(strings.TrimSpace(joined))
		if len(tr) > 20 {
			title = string(tr[:20])
		} else {
			title = string(tr)
		}
		if title == "" {
			title = "由 Inbox 生成"
		}
	}
	// build BS content — 优先使用前端编辑后的 AC
	draft := mockDraft(joined)
	ac := draft.AcceptanceCriteria
	if len(req.AcceptanceCriteria) > 0 {
		ac = req.AcceptanceCriteria
	}
	content, _ := json.Marshal(map[string]any{
		"story_id":            draft.StoryID,
		"title":               title,
		"description":         joined,
		"acceptance_criteria": ac,
		"priority":            "P1",
	})
	specID := idgen.New()
	version := "v0.1.0-" + idgen.New()[:8]
	tx, err := model.Pool.Begin(ctx)
	if err != nil {
		return respond.DBError(c, err)
	}
	defer tx.Rollback(ctx)
	// 事务内二次校验并加行锁，防并发双建（P1-1）
	for _, rid := range req.RequirementIDs {
		var st string
		if err := tx.QueryRow(ctx, `SELECT status FROM requirements_inbox WHERE id=$1 FOR UPDATE`, rid).Scan(&st); err != nil {
			return respond.DBError(c, err)
		}
		if st == "CONVERTED" {
			return respond.BusinessRule(c, "INBOX_ALREADY_CONVERTED", "inbox already converted: "+rid[:8])
		}
		if st != "NEW" && st != "DRAFTED" {
			return respond.BusinessRule(c, "INBOX_NOT_CONVERTIBLE", "only NEW/DRAFTED can be converted, got "+st)
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO specs (id, project_id, layer, title, status, current_version) VALUES ($1,$2,'BS',$3,'DRAFT',$4)`, specID, projectID, title, version)
	if err != nil {
		return respond.DBError(c, err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO spec_versions (id, spec_id, version, content) VALUES ($1,$2,$3,$4)`, idgen.New(), specID, version, content)
	if err != nil {
		return respond.DBError(c, err)
	}
	for _, rid := range req.RequirementIDs {
		_, _ = tx.Exec(ctx, `INSERT INTO requirement_spec_links (requirement_id, spec_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, rid, specID)
		_, _ = tx.Exec(ctx, `UPDATE requirements_inbox SET status='CONVERTED', updated_at=now() WHERE id=$1`, rid)
	}
	if err := tx.Commit(ctx); err != nil {
		return respond.DBError(c, err)
	}
	// async embedding for new BS
	go func() {
		var svID string
		_ = model.Pool.QueryRow(context.Background(), `SELECT id FROM spec_versions WHERE spec_id=$1 ORDER BY created_at DESC LIMIT 1`, specID).Scan(&svID)
		if svID != "" {
			_ = vector.StoreSpecEmbedding(context.Background(), svID, string(content))
		}
	}()
	username, _ := c.Locals("username").(string)
	if username == "" {
		username = "anonymous"
	}
	// 审计 detail 必须为合法 JSON（S1.3 修复：原字符串拼接产出非法 JSON）
	if detail, err := json.Marshal(fiber.Map{"requirements": req.RequirementIDs}); err == nil {
		logAudit(ctx, projectID, username, "inbox.convert", "spec", specID, string(detail))
	} else {
		logAudit(ctx, projectID, username, "inbox.convert", "spec", specID, "{}")
	}
	return respond.OK(c, fiber.Map{"id": specID, "version": version, "title": title})
}

// reforgeInbox POST /inbox/:id/reforge —— v2.2 T3：UAT 验收反馈一键回炉。
// 从 UAT 条目生成修复 Task（PENDING 待审批），description 注入交付上下文（首行 UAT 头含
// tag/sha/delivery id）；幂等：description 内嵌 [UAT-REF:<inboxId>] 标记，重复调用返回既有任务。
// 权限：task.write（团队侧动作）；Viewer 的 UAT 提交仍走 submitProjectUAT。
func reforgeInbox(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	id := c.Params("id")
	if !idgen.IsValid(id) {
		return respond.InvalidParam(c, "id", "invalid id")
	}
	ctx := context.Background()
	var raw, projectID, sourceType, status string
	if err := model.Pool.QueryRow(ctx,
		`SELECT raw_content, project_id::text, COALESCE(source_type,''), COALESCE(status,'') FROM requirements_inbox WHERE id=$1`,
		id).Scan(&raw, &projectID, &sourceType, &status); err != nil {
		if err == pgx.ErrNoRows {
			return respond.NotFound(c, "requirement")
		}
		return respond.DBError(c, err)
	}
	if err := AuthorizeProject(c, projectID, "task.write"); err != nil {
		return err
	}
	if sourceType != "UAT" {
		return respond.BusinessRule(c, "INBOX_NOT_UAT", "reforge only supports UAT feedback (source_type=UAT)")
	}
	marker := "[UAT-REF:" + id + "]"
	// 幂等：同一条 UAT 只生成一个修复任务（description 标记即真相，零迁移）
	var existing, existingStatus string
	if err := model.Pool.QueryRow(ctx,
		`SELECT id::text, status FROM tasks WHERE project_id=$1 AND description LIKE '%'||$2||'%' ORDER BY created_at LIMIT 1`,
		projectID, marker).Scan(&existing, &existingStatus); err == nil {
		return respond.OK(c, fiber.Map{"id": existing, "status": existingStatus, "dedup": true})
	}
	username, _ := c.Locals("username").(string)
	if username == "" {
		username = "anonymous"
	}
	// 标题：UAT 首行（含结论/tag 上下文）截断；描述：完整正文 + 幂等标记
	subject := strings.TrimSpace(raw)
	if i := strings.Index(subject, "\n"); i >= 0 {
		subject = strings.TrimSpace(subject[:i])
	}
	rs := []rune(subject)
	if len(rs) > 80 {
		rs = rs[:80]
	}
	title := "[UAT 修复] " + string(rs)
	if strings.TrimSpace(string(rs)) == "" {
		title = "[UAT 修复] 验收反馈"
	}
	body := raw
	if len([]rune(body)) > 4000 {
		body = string([]rune(body)[:4000])
	}
	description := "UAT 验收反馈回炉（自动携带交付上下文，首行含结论/交付 tag/sha）：\n\n" + body + "\n\n" + marker
	// 已关联 Spec（convert 产物）则挂回，便于锻造注入规格上下文
	specID := ""
	_ = model.Pool.QueryRow(ctx,
		`SELECT spec_id::text FROM requirement_spec_links WHERE requirement_id=$1 ORDER BY spec_id LIMIT 1`, id).Scan(&specID)
	var specVal any
	if specID != "" {
		specVal = specID
	}
	taskID := idgen.New()
	if _, err := model.Pool.Exec(ctx, `
		INSERT INTO tasks (id, project_id, title, description, status, priority, spec_id)
		VALUES ($1,$2,$3,$4,'PENDING',1,$5)`, taskID, projectID, title, description, specVal); err != nil {
		return respond.DBError(c, err)
	}
	logAudit(ctx, projectID, username, "inbox.reforge", "task", taskID,
		fmt.Sprintf(`{"requirement_id":%q,"source_type":"UAT"}`, id))
	events.Default.Publish("project:"+projectID, "task.created",
		map[string]any{"id": taskID, "from": "uat-reforge", "requirement_id": id})
	return respond.OK(c, fiber.Map{"id": taskID, "status": "PENDING", "dedup": false, "spec_id": specID})
}
