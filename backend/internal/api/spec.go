package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"forja/backend/internal/api/respond"
	"forja/backend/internal/auth"
	"forja/backend/internal/model"
	specgen "forja/backend/internal/spec"
	"forja/backend/internal/validation"
	"forja/backend/internal/vector"

	"github.com/gofiber/fiber/v2"
	idgen "forja/backend/internal/id"
	"github.com/jackc/pgx/v5"
)

type createSpecReq struct {
	ProjectID string          `json:"project_id"`
	Layer     string          `json:"layer"`
	Title     string          `json:"title"`
	Content   json.RawMessage `json:"content"`
	Refs      []string        `json:"refs"`
	Anchors   []string        `json:"anchors"`
}

type updateSpecReq struct {
	Title   string          `json:"title"`
	Content json.RawMessage `json:"content"`
	Refs    *[]string       `json:"refs"`
	Anchors *[]string       `json:"anchors"`
}

// maxSpecContent single spec content size limit
const maxSpecContent = 256 << 10 // 256KB

var validLayers = map[string]bool{
	"BS": true, "IS": true, "DS": true, "TS": true,
	"AS": true, "DEPLOY": true, "CONSTITUTION": true, "CONTRACT": true,
}

func RegisterSpecRoutes(r fiber.Router) {
	r.Post("/specs", auth.Require("spec.write"), createSpec)
	r.Put("/specs/:id", auth.Require("spec.write"), updateSpec)
	r.Get("/specs/search", auth.Require("spec.read"), searchSpecs)
	r.Get("/specs/timeline", auth.Require("spec.read"), specTimeline)
	// P2 lifecycle (spec_lifecycle.go): task.read
	registerSpecLifecycleRoute(r)
	r.Get("/specs/:id/refs", auth.Require("spec.read"), getSpecRefs)
	r.Get("/specs/:id", auth.Require("spec.read"), getSpec)
	r.Get("/specs", auth.Require("spec.read"), listSpecs)
	r.Post("/specs/:id/validate", auth.Require("spec.validate"), validateSpec)
	r.Post("/specs/:id/embed", auth.Require("spec.write"), embedSpec)
	r.Post("/specs/:id/ts-draft", auth.Require("spec.write"), tsDraft)
}

// checkBSConstitution BS layer WHAT/WHY must not contain HOW keywords
func checkBSConstitution(layer string, content json.RawMessage) error {
	if layer != "BS" {
		return nil
	}
	if kw := validation.BSViolation(string(content)); kw != "" {
		return fmt.Errorf("BS must not contain HOW keyword: %s", kw)
	}
	return nil
}

// autoStoryID for BS missing story_id
func autoStoryID(ctx context.Context, tx pgx.Tx, projectID, layer string, content json.RawMessage) (json.RawMessage, error) {
	if layer != "BS" {
		return content, nil
	}
	dec := json.NewDecoder(bytes.NewReader(content))
	dec.UseNumber()
	var obj map[string]any
	if err := dec.Decode(&obj); err != nil || obj == nil {
		return content, nil
	}
	if sid, _ := obj["story_id"].(string); strings.TrimSpace(sid) != "" {
		return content, nil
	}
	var slug *string
	if err := tx.QueryRow(ctx, `SELECT slug FROM projects WHERE id=$1`, projectID).Scan(&slug); err != nil && err != pgx.ErrNoRows {
		return nil, err
	}
	prefix := strings.ToUpper(projectID[:8])
	if slug != nil && *slug != "" {
		prefix = strings.ToUpper(*slug)
	}
	var seq int
	if err := tx.QueryRow(ctx,
		`INSERT INTO spec_counters (project_id, last_seq) VALUES ($1, 1)
		 ON CONFLICT (project_id) DO UPDATE SET last_seq = spec_counters.last_seq + 1
		 RETURNING last_seq`, projectID).Scan(&seq); err != nil {
		return nil, err
	}
	obj["story_id"] = fmt.Sprintf("%s-%d", prefix, seq)
	out, err := json.Marshal(obj)
	if err != nil {
		return content, nil
	}
	return out, nil
}

func nextVersion(cur string) string {
	base := strings.SplitN(cur, "-", 2)[0]
	parts := strings.Split(strings.TrimPrefix(base, "v"), ".")
	if len(parts) == 3 {
		if n, err := strconv.Atoi(parts[2]); err == nil {
			return fmt.Sprintf("%s.%d", base[:len(base)-len(parts[2])-1], n+1)
		}
	}
	return fmt.Sprintf("v0.1.0-%d", time.Now().UnixNano())
}

func storeEmbeddingAsync(svID, content string) {
	go func() {
		defer func() { _ = recover() }()
		_ = vector.StoreSpecEmbedding(context.Background(), svID, content)
	}()
}

// anchorPathRe validates file path anchors: must be relative path with at least one slash and file extension
var anchorPathRe = regexp.MustCompile(`^[a-zA-Z0-9_.\-/]+$`)

// validateAnchors verifies anchor paths: non-empty, allowed chars, contains slash, no traversal, dedup, limit 20
func validateAnchors(anchors []string) ([]string, error) {
	if anchors == nil {
		return []string{}, nil
	}
	if len(anchors) > 20 {
		return nil, fmt.Errorf("anchors too many (max 20)")
	}
	seen := map[string]bool{}
	out := []string{}
	for _, a := range anchors {
		s := strings.TrimSpace(a)
		if s == "" {
			return nil, fmt.Errorf("anchor path cannot be empty")
		}
		if len(s) > 512 {
			return nil, fmt.Errorf("anchor path too long: %s", s)
		}
		if strings.Contains(s, "..") || strings.Contains(s, "\\") {
			return nil, fmt.Errorf("anchor path must not contain '..' or backslash: %s", s)
		}
		if strings.HasPrefix(s, "/") {
			return nil, fmt.Errorf("anchor path must be relative (no leading /): %s", s)
		}
		if !strings.Contains(s, "/") {
			return nil, fmt.Errorf("anchor path must contain '/': %s", s)
		}
		if !anchorPathRe.MatchString(s) {
			return nil, fmt.Errorf("anchor path contains invalid characters: %s", s)
		}
		// must have extension or at least dot? allow directory? require file extension for spec anchor
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	if out == nil {
		out = []string{}
	}
	return out, nil
}

// validateRefs verifies refs are valid UUIDs, exist, same project, not self, dedup, limit 20
func validateRefs(ctx context.Context, projectID string, refs []string, selfID string) ([]string, error) {
	if refs == nil {
		return []string{}, nil
	}
	if len(refs) > 20 {
		return nil, fmt.Errorf("refs too many (max 20)")
	}
	seen := map[string]bool{}
	out := []string{}
	for _, r := range refs {
		s := strings.TrimSpace(r)
		if s == "" {
			continue
		}
		if !idgen.IsValid(s) {
			return nil, fmt.Errorf("invalid refs id (must be UUID): %s", s)
		}
		if selfID != "" && s == selfID {
			return nil, fmt.Errorf("refs cannot contain self: %s", s)
		}
		if seen[s] {
			continue
		}
		seen[s] = true
		// existence + same project check
		var cnt int
		err := model.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM specs WHERE id=$1 AND project_id=$2`, s, projectID).Scan(&cnt)
		if err != nil {
			return nil, err
		}
		if cnt == 0 {
			return nil, fmt.Errorf("refs spec not found or not in same project: %s", s)
		}
		out = append(out, s)
	}
	if out == nil {
		out = []string{}
	}
	return out, nil
}

func createSpec(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	var req createSpecReq
	if err := c.BodyParser(&req); err != nil {
		return respond.BadRequest(c, err.Error())
	}
	if req.Title == "" || req.Layer == "" {
		return respond.InvalidParam(c, "title,layer", "layer and title required")
	}
	layer := strings.ToUpper(req.Layer)
	if !validLayers[layer] {
		return respond.InvalidParam(c, "layer", "invalid layer")
	}
	if req.ProjectID == "" {
		return respond.InvalidParam(c, "project_id", "project_id required")
	}
	projectID := req.ProjectID
	if !idgen.IsValid(projectID) {
		return respond.InvalidParam(c, "project_id", "invalid project_id")
	}
	if err := AuthorizeProject(c, projectID, "spec.write"); err != nil {
		return err
	}
	if len(req.Content) > maxSpecContent {
		return respond.InvalidParam(c, "content", "content too large (max 256KB)")
	}
	if req.Content == nil {
		req.Content = json.RawMessage(`{}`)
	}
	if err := checkBSConstitution(layer, req.Content); err != nil {
		return respond.BusinessRule(c, "CONSTITUTION_VIOLATION", err.Error())
	}
	// validate refs/anchors
	ctx := context.Background()
	refs, err := validateRefs(ctx, projectID, req.Refs, "")
	if err != nil {
		return respond.InvalidParam(c, "refs", err.Error())
	}
	anchors, err := validateAnchors(req.Anchors)
	if err != nil {
		return respond.InvalidParam(c, "anchors", err.Error())
	}
	refsJSON, _ := json.Marshal(refs)
	anchorsJSON, _ := json.Marshal(anchors)
	specID := idgen.New()
	version := fmt.Sprintf("v0.1.0-%d", time.Now().Unix())
	tx, err := model.Pool.Begin(ctx)
	if err != nil {
		return respond.DBError(c, err)
	}
	defer tx.Rollback(ctx)
	content := req.Content
	if gen, err := autoStoryID(ctx, tx, projectID, layer, req.Content); err != nil {
		return respond.DBError(c, err)
	} else if gen != nil {
		content = gen
	}
	svID := idgen.New()
	_, err = tx.Exec(ctx, `INSERT INTO specs (id, project_id, layer, title, status, current_version, refs, anchors) VALUES ($1,$2,$3,$4,'DRAFT',$5,$6,$7)`, specID, projectID, layer, req.Title, version, refsJSON, anchorsJSON)
	if err != nil {
		return respond.DBError(c, err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO spec_versions (id, spec_id, version, content) VALUES ($1,$2,$3,$4)`, svID, specID, version, content)
	if err != nil {
		return respond.DBError(c, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return respond.DBError(c, err)
	}
	username, _ := c.Locals("username").(string)
	if username == "" {
		username = "anonymous"
	}
	logAudit(ctx, projectID, username, "spec.create", "spec", specID, fmt.Sprintf(`{"layer":"%s","title":%q,"refs":%d,"anchors":%d}`, layer, req.Title, len(refs), len(anchors)))
	storeEmbeddingAsync(svID, string(content))
	return respond.OK(c, fiber.Map{"id": specID, "version": version, "status": "DRAFT", "refs": refs, "anchors": anchors})
}

// updateSpec 保存已有 spec 的修改：追加新版本行
func updateSpec(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	id := c.Params("id")
	if !idgen.IsValid(id) {
		return respond.InvalidParam(c, "id", "invalid id")
	}
	var req updateSpecReq
	if err := c.BodyParser(&req); err != nil {
		return respond.BadRequest(c, err.Error())
	}
	if req.Title == "" {
		return respond.InvalidParam(c, "title", "title required")
	}
	ctx := context.Background()
	var layer, projectID string
	err := model.Pool.QueryRow(ctx,
		`SELECT layer, project_id::text FROM specs WHERE id=$1`, id).
		Scan(&layer, &projectID)
	if err == pgx.ErrNoRows {
		return respond.NotFound(c, "spec")
	}
	if err != nil {
		return respond.DBError(c, err)
	}
	if err := AuthorizeProject(c, projectID, "spec.write"); err != nil {
		return err
	}
	if len(req.Content) > maxSpecContent {
		return respond.InvalidParam(c, "content", "content too large (max 256KB)")
	}
	if req.Content == nil {
		req.Content = json.RawMessage(`{}`)
	}
	if err := checkBSConstitution(layer, req.Content); err != nil {
		return respond.BusinessRule(c, "CONSTITUTION_VIOLATION", err.Error())
	}
	tx, err := model.Pool.Begin(ctx)
	if err != nil {
		return respond.DBError(c, err)
	}
	defer tx.Rollback(ctx)
	var curVer string
	var curRefsJSON, curAnchorsJSON json.RawMessage
	err = tx.QueryRow(ctx, `SELECT current_version, refs, anchors FROM specs WHERE id=$1 FOR UPDATE`, id).Scan(&curVer, &curRefsJSON, &curAnchorsJSON)
	if err != nil {
		return respond.DBError(c, err)
	}
	// determine refs/anchors to persist
	var refsJSON, anchorsJSON json.RawMessage
	var refsOut, anchorsOut []string
	if req.Refs != nil {
		validated, verr := validateRefs(ctx, projectID, *req.Refs, id)
		if verr != nil {
			return respond.InvalidParam(c, "refs", verr.Error())
		}
		refsOut = validated
		b, _ := json.Marshal(validated)
		refsJSON = b
	} else {
		// keep existing
		refsJSON = curRefsJSON
		// also unmarshal for response
		_ = json.Unmarshal(curRefsJSON, &refsOut)
		if refsOut == nil {
			refsOut = []string{}
		}
	}
	if req.Anchors != nil {
		validated, verr := validateAnchors(*req.Anchors)
		if verr != nil {
			return respond.InvalidParam(c, "anchors", verr.Error())
		}
		anchorsOut = validated
		b, _ := json.Marshal(validated)
		anchorsJSON = b
	} else {
		anchorsJSON = curAnchorsJSON
		_ = json.Unmarshal(curAnchorsJSON, &anchorsOut)
		if anchorsOut == nil {
			anchorsOut = []string{}
		}
	}
	version := nextVersion(curVer)
	svID := idgen.New()
	_, err = tx.Exec(ctx, `INSERT INTO spec_versions (id, spec_id, version, content) VALUES ($1,$2,$3,$4)`, svID, id, version, req.Content)
	if err != nil {
		return respond.DBError(c, err)
	}
	_, err = tx.Exec(ctx, `UPDATE specs SET title=$2, current_version=$3, refs=$4, anchors=$5, status='DRAFT', updated_at=now() WHERE id=$1`, id, req.Title, version, refsJSON, anchorsJSON)
	if err != nil {
		return respond.DBError(c, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return respond.DBError(c, err)
	}
	username, _ := c.Locals("username").(string)
	if username == "" {
		username = "anonymous"
	}
	logAudit(ctx, projectID, username, "spec.update", "spec", id, fmt.Sprintf(`{"version":%q,"refs":%d,"anchors":%d}`, version, len(refsOut), len(anchorsOut)))
	storeEmbeddingAsync(svID, string(req.Content))
	return respond.OK(c, fiber.Map{"id": id, "version": version, "status": "DRAFT", "refs": refsOut, "anchors": anchorsOut})
}

func getSpec(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	id := c.Params("id")
	if !idgen.IsValid(id) {
		return respond.InvalidParam(c, "id", "invalid id")
	}
	ctx := context.Background()
	var layer, title, status, curVer string
	var projectID string
	var refsJSON, anchorsJSON json.RawMessage
	err := model.Pool.QueryRow(ctx, `SELECT project_id, layer, title, status, COALESCE(current_version,'') AS current_version, COALESCE(refs,'[]'::jsonb), COALESCE(anchors,'[]'::jsonb) FROM specs WHERE id=$1`, id).Scan(&projectID, &layer, &title, &status, &curVer, &refsJSON, &anchorsJSON)
	if err == pgx.ErrNoRows {
		return respond.NotFound(c, "spec")
	}
	if err != nil {
		return respond.DBError(c, err)
	}
	if err := AuthorizeProject(c, projectID, "spec.read"); err != nil {
		return err
	}
	var content json.RawMessage
	var version string
	err = model.Pool.QueryRow(ctx, `SELECT version, content FROM spec_versions WHERE spec_id=$1 ORDER BY created_at DESC LIMIT 1`, id).Scan(&version, &content)
	if err != nil {
		return respond.DBError(c, err)
	}
	var refs, anchors []string
	_ = json.Unmarshal(refsJSON, &refs)
	_ = json.Unmarshal(anchorsJSON, &anchors)
	if refs == nil {
		refs = []string{}
	}
	if anchors == nil {
		anchors = []string{}
	}
	return respond.OK(c, fiber.Map{"id": id, "project_id": projectID, "layer": layer, "title": title, "status": status, "current_version": curVer, "version": version, "content": content, "refs": refs, "anchors": anchors})
}

func listSpecs(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	projectID := c.Query("project_id")
	if projectID == "" {
		return respond.InvalidParam(c, "project_id", "project_id required")
	}
	layer := c.Query("layer")
	page, pageSize := respond.ParsePage(c)
	ctx := context.Background()
	if err := AuthorizeProject(c, projectID, "spec.read"); err != nil {
		return err
	}
	where := ` WHERE project_id=$1`
	args := []interface{}{projectID}
	if layer != "" {
		where += ` AND layer=$2`
		args = append(args, strings.ToUpper(layer))
	}
	var total int64
	if err := model.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM specs`+where, args...).Scan(&total); err != nil {
		return respond.DBError(c, err)
	}
	query := `SELECT id, layer, title, status, COALESCE(current_version,'') AS current_version, COALESCE(refs,'[]'::jsonb), COALESCE(anchors,'[]'::jsonb) FROM specs` + where +
		fmt.Sprintf(` ORDER BY updated_at DESC LIMIT %d OFFSET %d`, pageSize, (page-1)*pageSize)
	rows, err := model.Pool.Query(ctx, query, args...)
	if err != nil {
		return respond.DBError(c, err)
	}
	defer rows.Close()
	list := []fiber.Map{}
	for rows.Next() {
		var id, l, t, s, v string
		var refsJSON, anchorsJSON json.RawMessage
		if err := rows.Scan(&id, &l, &t, &s, &v, &refsJSON, &anchorsJSON); err != nil {
			return respond.DBError(c, err)
		}
		var refs, anchors []string
		_ = json.Unmarshal(refsJSON, &refs)
		_ = json.Unmarshal(anchorsJSON, &anchors)
		if refs == nil {
			refs = []string{}
		}
		if anchors == nil {
			anchors = []string{}
		}
		list = append(list, fiber.Map{"id": id, "layer": l, "title": t, "status": s, "current_version": v, "refs": refs, "anchors": anchors})
	}
	return respond.List(c, list, respond.PageMeta{Page: page, PageSize: pageSize, Total: total})
}

// getSpecRefs fetches related specs by refs field (backend/internal/api/spec.go:45)
func getSpecRefs(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	id := c.Params("id")
	if !idgen.IsValid(id) {
		return respond.InvalidParam(c, "id", "invalid id")
	}
	ctx := context.Background()
	var projectID string
	var refsJSON json.RawMessage
	err := model.Pool.QueryRow(ctx, `SELECT project_id::text, COALESCE(refs,'[]'::jsonb) FROM specs WHERE id=$1`, id).Scan(&projectID, &refsJSON)
	if err == pgx.ErrNoRows {
		return respond.NotFound(c, "spec")
	}
	if err != nil {
		return respond.DBError(c, err)
	}
	if err := AuthorizeProject(c, projectID, "spec.read"); err != nil {
		return err
	}
	var refs []string
	_ = json.Unmarshal(refsJSON, &refs)
	if len(refs) == 0 {
		return respond.OK(c, fiber.Map{"spec_id": id, "refs": []fiber.Map{}})
	}
	// validate UUIDs and filter by same project for safety
	validIDs := []string{}
	for _, r := range refs {
		if idgen.IsValid(r) {
			validIDs = append(validIDs, r)
		}
	}
	if len(validIDs) == 0 {
		return respond.OK(c, fiber.Map{"spec_id": id, "refs": []fiber.Map{}})
	}
	rows, err := model.Pool.Query(ctx, `SELECT id::text, layer, title, status, COALESCE(current_version,''), project_id::text FROM specs WHERE id = ANY($1::text[])`, validIDs)
	if err != nil {
		return respond.DBError(c, err)
	}
	defer rows.Close()
	items := []fiber.Map{}
	for rows.Next() {
		var sid, layer, title, status, ver, pid string
		if err := rows.Scan(&sid, &layer, &title, &status, &ver, &pid); err != nil {
			return respond.DBError(c, err)
		}
		// only same project
		if pid != projectID {
			continue
		}
		items = append(items, fiber.Map{"id": sid, "layer": layer, "title": title, "status": status, "current_version": ver})
	}
	// also fetch AC snippet for regression context? Provide content AC if needed
	// For prompt usage, frontend can call getSpec for each, but we include AC excerpt
	for i, it := range items {
		sid := it["id"].(string)
		var content json.RawMessage
		if err := model.Pool.QueryRow(ctx, `SELECT content FROM spec_versions WHERE spec_id=$1 ORDER BY created_at DESC LIMIT 1`, sid).Scan(&content); err == nil {
			// extract acceptance_criteria count or raw
			var obj map[string]any
			if err := json.Unmarshal(content, &obj); err == nil {
				if ac, ok := obj["acceptance_criteria"]; ok {
					items[i]["acceptance_criteria"] = ac
				}
			}
		}
	}
	return respond.OK(c, fiber.Map{"spec_id": id, "refs": items})
}

func validateSpec(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	id := c.Params("id")
	if !idgen.IsValid(id) {
		return respond.InvalidParam(c, "id", "invalid id")
	}
	ctx := context.Background()
	var content json.RawMessage
	var layer string
	var projectID string
	err := model.Pool.QueryRow(ctx, `SELECT s.layer, s.project_id::text, sv.content FROM specs s JOIN spec_versions sv ON sv.spec_id=s.id WHERE s.id=$1 ORDER BY sv.created_at DESC LIMIT 1`, id).Scan(&layer, &projectID, &content)
	if err == pgx.ErrNoRows {
		return respond.NotFound(c, "spec")
	}
	if err != nil {
		return respond.DBError(c, err)
	}
	if err := AuthorizeProject(c, projectID, "spec.validate"); err != nil {
		return err
	}
	result := validation.RunAll(layer, content)
	if result.Passed {
		_, _ = model.Pool.Exec(ctx, `UPDATE specs SET status='VALIDATED', updated_at=now() WHERE id=$1`, id)
	}
	username, _ := c.Locals("username").(string)
	if username == "" {
		username = "anonymous"
	}
	logAudit(ctx, projectID, username, "spec.validate", "spec", id, fmt.Sprintf(`{"passed":%v}`, result.Passed))
	return respond.OK(c, result)
}

func embedSpec(c *fiber.Ctx) error {
	id := c.Params("id")
	if !idgen.IsValid(id) {
		return respond.InvalidParam(c, "id", "invalid id")
	}
	ctx := context.Background()
	var content json.RawMessage
	var svID string
	var projectID string
	err := model.Pool.QueryRow(ctx, `SELECT sv.id, sv.content, s.project_id::text FROM spec_versions sv JOIN specs s ON s.id=sv.spec_id WHERE sv.spec_id=$1 ORDER BY sv.created_at DESC LIMIT 1`, id).Scan(&svID, &content, &projectID)
	if err != nil {
		return respond.NotFound(c, "spec")
	}
	if err := AuthorizeProject(c, projectID, "spec.write"); err != nil {
		return err
	}
	if err := vector.StoreSpecEmbedding(ctx, svID, string(content)); err != nil {
		return respond.Upstream(c, "EMBED_FAILED", err)
	}
	return respond.OK(c, fiber.Map{"id": id, "embedded": true})
}

func searchSpecs(c *fiber.Ctx) error {
	projectID := c.Query("project_id")
	if projectID == "" {
		return respond.InvalidParam(c, "project_id", "project_id required")
	}
	if !idgen.IsValid(projectID) {
		return respond.InvalidParam(c, "project_id", "invalid project_id")
	}
	q := c.Query("q")
	if q == "" {
		return respond.InvalidParam(c, "q", "q required")
	}
	if err := AuthorizeProject(c, projectID, "spec.read"); err != nil {
		return err
	}
	limit := 10
	res, err := vector.SearchSpecs(context.Background(), projectID, q, limit)
	if err != nil {
		return respond.Upstream(c, "SEARCH_FAILED", err)
	}
	return respond.OK(c, fiber.Map{"items": res})
}

func tsDraft(c *fiber.Ctx) error {
	id := c.Params("id")
	if !idgen.IsValid(id) {
		return respond.InvalidParam(c, "id", "invalid id")
	}
	ctx := context.Background()
	var content json.RawMessage
	var title, projectID string
	var layer string
	err := model.Pool.QueryRow(ctx, `SELECT s.title, s.layer, s.project_id::text, sv.content FROM specs s JOIN spec_versions sv ON sv.spec_id=s.id WHERE s.id=$1 ORDER BY sv.created_at DESC LIMIT 1`, id).Scan(&title, &layer, &projectID, &content)
	if err != nil {
		return respond.NotFound(c, "spec")
	}
	if err := AuthorizeProject(c, projectID, "spec.write"); err != nil {
		return err
	}
	if layer != "BS" {
		return respond.BusinessRule(c, "LAYER_MISMATCH", "only BS can generate TS draft")
	}
	draft, err := specgen.GenerateTSDraft(content, title)
	if err != nil {
		return respond.Upstream(c, "DRAFT_FAILED", err)
	}
	if c.Query("create") == "true" {
		tsID := idgen.New()
		version := fmt.Sprintf("v0.1.0-%d", time.Now().Unix())
		draftJSON, err := json.Marshal(draft)
		if err != nil {
			return respond.Internal(c, err)
		}
		tx, err := model.Pool.Begin(ctx)
		if err != nil {
			return respond.DBError(c, err)
		}
		defer tx.Rollback(ctx)
		if _, err := tx.Exec(ctx, `INSERT INTO specs (id, project_id, layer, title, status, current_version) VALUES ($1,$2,'TS',$3,'DRAFT',$4)`, tsID, projectID, "Auto TS for "+title, version); err != nil {
			return respond.DBError(c, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO spec_versions (id, spec_id, version, content) VALUES ($1,$2,$3,$4)`, idgen.New(), tsID, version, draftJSON); err != nil {
			return respond.DBError(c, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return respond.DBError(c, err)
		}
		username, _ := c.Locals("username").(string)
		if username == "" {
			username = "anonymous"
		}
		logAudit(ctx, projectID, username, "spec.create", "spec", tsID, fmt.Sprintf(`{"layer":"TS","source":%q}`, id))
		return respond.OK(c, fiber.Map{"draft": draft, "created_id": tsID})
	}
	return respond.OK(c, fiber.Map{"draft": draft})
}

// specTimeline (backend/internal/api/spec.go:481)
func specTimeline(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	projectID := c.Query("project_id")
	if projectID == "" {
		return respond.InvalidParam(c, "project_id", "project_id required")
	}
	limit := c.QueryInt("limit", 50)
	if limit < 1 || limit > 200 {
		limit = 50
	}
	ctx := context.Background()
	where := ""
	var args []any
	switch {
	case projectID == "ALL":
		if !isGlobalAdmin(c) {
			uid := currentUserID(c)
			if uid == "" {
				return respond.List(c, []fiber.Map{}, respond.PageMeta{Page: 1, PageSize: limit, Total: 0})
			}
			where = ` WHERE s.project_id IN (SELECT project_id FROM project_members WHERE user_id=$1)`
			args = append(args, uid)
		}
	default:
		if !idgen.IsValid(projectID) {
			return respond.InvalidParam(c, "project_id", "invalid project_id")
		}
		if err := AuthorizeProject(c, projectID, "spec.read"); err != nil {
			return err
		}
		where = ` WHERE s.project_id=$1`
		args = append(args, projectID)
	}

	query := `SELECT sv.id, sv.spec_id, s.title, s.layer, s.project_id::text,
		sv.version, COALESCE(sv.change_log,''), sv.created_at
		FROM spec_versions sv JOIN specs s ON s.id=sv.spec_id` + where +
		` ORDER BY sv.created_at DESC LIMIT ` + itoa(limit)
	rows, err := model.Pool.Query(ctx, query, args...)
	if err != nil {
		return respond.DBError(c, err)
	}
	defer rows.Close()
	items := []fiber.Map{}
	for rows.Next() {
		var svID, specID, title, layer, ptext, version string
		var changeLog *string
		var createdAt time.Time
		if err := rows.Scan(&svID, &specID, &title, &layer, &ptext, &version, &changeLog, &createdAt); err != nil {
			return respond.DBError(c, err)
		}
		cl := ""
		if changeLog != nil {
			cl = *changeLog
		}
		items = append(items, fiber.Map{
			"version_id": svID, "spec_id": specID, "title": title,
			"layer": layer, "project_id": ptext, "version": version,
			"change_log": cl, "created_at": createdAt.Format(time.RFC3339),
		})
	}
	if err := rows.Err(); err != nil {
		return respond.DBError(c, err)
	}
	return respond.List(c, items, respond.PageMeta{Page: 1, PageSize: limit, Total: int64(len(items))})
}
