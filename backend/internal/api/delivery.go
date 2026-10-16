package api

// M1.3 交付包：docs.zip 组装（delivery.json / deploy-compose / README-DEPLOY / CHANGELOG / project.yaml）
// + 镜像 tar 回传（CI → FileStore）+ 流式下载 + Release Notes 预览。
// 镜像契约：gitea:3000/<owner>/<repo>:<default_branch>-<sha8>。

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"forja/backend/internal/api/respond"
	"forja/backend/internal/auth"
	"forja/backend/internal/events"
	"forja/backend/internal/gitea"
	idgen "forja/backend/internal/id"
	"forja/backend/internal/model"
	"forja/backend/internal/storage"
	"forja/backend/internal/templates"

	"github.com/gofiber/fiber/v2"
)

const maxDeliveryArtifactDefault = int64(2) << 30 // 2 GiB

// RegisterDeliveryRoutes JWT 组下的交付包端点（组装/列表/下载/Notes）。
func RegisterDeliveryRoutes(r fiber.Router) {
	r.Post("/projects/:id/deliveries", auth.Require("project.deliver"), triggerDelivery)
	r.Get("/projects/:id/deliveries", auth.Require("task.read"), listDeliveries)
	r.Get("/projects/:id/deliveries/:did/download", auth.Require("task.read"), downloadDelivery)
	r.Get("/projects/:id/deliveries/:did/artifact", auth.Require("task.read"), downloadDeliveryArtifact)
	r.Get("/projects/:id/releases/notes", auth.Require("task.read"), releaseNotesPreview)
}

// RegisterInternalRoutes 服务令牌组（无 JWT）：CI 回传镜像 tar。
func RegisterInternalRoutes(r fiber.Router) {
	r.Post("/internal/delivery/artifact", requireInternalToken, uploadDeliveryArtifact)
}

// requireInternalToken 服务令牌鉴权（X-Forja-Token）；env 未配置 fail-closed。
func requireInternalToken(c *fiber.Ctx) error {
	want := strings.TrimSpace(os.Getenv("FORJA_INTERNAL_TOKEN"))
	if want == "" {
		return respond.ServiceUnavailable(c)
	}
	got := strings.TrimSpace(c.Get("X-Forja-Token"))
	if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
		return respond.Unauthorized(c, "invalid internal token")
	}
	return c.Next()
}

func maxDeliveryArtifactBytes() int64 {
	if v := strings.TrimSpace(os.Getenv("FORJA_MAX_ARTIFACT_BYTES")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return maxDeliveryArtifactDefault
}

func artifactTooLarge(c *fiber.Ctx, max int64) error {
	return respond.Fail(c, fiber.StatusRequestEntityTooLarge, respond.CodeUnsupported,
		"payload too large", "ARTIFACT_TOO_LARGE", fmt.Sprintf("max %d bytes", max))
}

// countingReader 统计流式读取字节数（大小护栏用）。
type countingReader struct {
	r io.Reader
	n int64
}

func (cr *countingReader) Read(p []byte) (int, error) {
	n, err := cr.r.Read(p)
	cr.n += int64(n)
	return n, err
}

// uploadDeliveryArtifact POST /internal/delivery/artifact?project_id&sha
// body 为 gzip 压缩的 docker save 镜像流；落 FileStore 后回填 project_artifacts。
func uploadDeliveryArtifact(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	projectID := c.Query("project_id")
	sha := strings.ToLower(strings.TrimSpace(c.Query("sha")))
	if !idgen.IsValid(projectID) {
		return respond.InvalidParam(c, "project_id", "invalid project_id")
	}
	if !isFullSHA(sha) {
		return respond.InvalidParam(c, "sha", "invalid sha")
	}
	ctx := c.Context()
	var repoFull, branch string
	if err := model.Pool.QueryRow(ctx,
		`SELECT COALESCE(repo_full_name,''), COALESCE(NULLIF(default_branch,''),'master') FROM projects WHERE id=$1`,
		projectID).Scan(&repoFull, &branch); err != nil || repoFull == "" {
		return respond.NotFound(c, "project")
	}
	max := maxDeliveryArtifactBytes()
	declared := int64(c.Request().Header.ContentLength())
	if declared > max {
		return artifactTooLarge(c, max)
	}
	store, err := storage.Get()
	if err != nil {
		return respond.Internal(c, err)
	}
	key := fmt.Sprintf("delivery/%s/image-%s.tar.gz", projectID, sha[:8])
	hash := sha256.New()
	var src io.Reader
	if bs := c.Request().BodyStream(); bs != nil {
		src = bs
	} else {
		// fasthttp 默认将 body 读入内存；无流句柄时回退内存字节
		src = bytes.NewReader(c.Body())
	}
	body := &countingReader{r: io.LimitReader(io.TeeReader(src, hash), max+1)}
	if err := store.Put(ctx, key, body, declared, "application/gzip"); err != nil {
		return respond.Internal(c, err)
	}
	if body.n == 0 {
		_ = store.Delete(ctx, key)
		return respond.BadRequest(c, "empty artifact body")
	}
	if body.n > max {
		_ = store.Delete(ctx, key)
		return artifactTooLarge(c, max)
	}
	digest := "sha256:" + hex.EncodeToString(hash.Sum(nil))
	tag := branch + "-" + sha[:8]
	image := "gitea:3000/" + repoFull
	if _, err := model.Pool.Exec(ctx, `
		INSERT INTO project_artifacts (project_id, sha, tag, image, digest, size_bytes, store_key, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,'uploaded')
		ON CONFLICT (project_id, sha) DO UPDATE
		  SET image=EXCLUDED.image, tag=EXCLUDED.tag, digest=EXCLUDED.digest,
		      size_bytes=EXCLUDED.size_bytes, store_key=EXCLUDED.store_key,
		      status='uploaded', updated_at=now()`,
		projectID, sha, tag, image, digest, body.n, key); err != nil {
		return respond.DBError(c, err)
	}
	events.Default.Publish("project:"+projectID, "delivery.artifact", map[string]any{
		"sha": sha, "tag": tag, "image": image, "size_bytes": body.n, "digest": digest,
	})
	logAudit(ctx, projectID, "ci", "delivery.artifact_uploaded", "artifact", "",
		`{"sha":`+quoteJSON(sha)+`,"size_bytes":`+strconv.FormatInt(body.n, 10)+`}`)
	return respond.OK(c, fiber.Map{"stored": true, "key": key, "size_bytes": body.n, "digest": digest, "tag": tag})
}

// projectMeta 交付包组装所需的项目元数据。
type projectMeta struct {
	id       string
	name     string
	slug     string
	repoFull string
	branch   string
	template string
}

// deliveryArtifact 组装引用的制品行。
type deliveryArtifact struct {
	sha       string
	tag       string
	image     string
	digest    string
	sizeBytes int64
	storeKey  string
}

func loadProjectMeta(ctx context.Context, projectID string) (projectMeta, bool) {
	var pm projectMeta
	pm.id = projectID
	err := model.Pool.QueryRow(ctx, `
		SELECT name, COALESCE(slug,''), COALESCE(repo_full_name,''),
		       COALESCE(NULLIF(default_branch,''),'master'), COALESCE(template,'')
		  FROM projects WHERE id=$1`, projectID).
		Scan(&pm.name, &pm.slug, &pm.repoFull, &pm.branch, &pm.template)
	if err != nil {
		return pm, false
	}
	return pm, true
}

// triggerDelivery POST /projects/:id/deliveries —— 组装/覆盖交付包（UNIQUE(project_id,tag) 幂等）。
func triggerDelivery(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	projectID := c.Params("id")
	if !idgen.IsValid(projectID) {
		return respond.InvalidParam(c, "id", "invalid project id")
	}
	if err := AuthorizeProject(c, projectID, "project.deliver"); err != nil {
		return err
	}
	var req struct {
		SHA string `json:"sha"`
		Tag string `json:"tag"`
	}
	_ = c.BodyParser(&req)
	req.SHA = strings.ToLower(strings.TrimSpace(req.SHA))
	if req.SHA != "" && !isFullSHA(req.SHA) {
		return respond.InvalidParam(c, "sha", "invalid sha")
	}
	ctx := context.Background()
	pm, ok := loadProjectMeta(ctx, projectID)
	if !ok {
		return respond.NotFound(c, "project")
	}
	if pm.repoFull == "" {
		return respond.BusinessRule(c, "NO_REPO", "project has no linked repo")
	}
	// 选制品：优先带 tar 的最新；可指定 sha
	query := `SELECT sha, tag, image, COALESCE(digest,''), COALESCE(size_bytes,0), COALESCE(store_key,'')
	            FROM project_artifacts WHERE project_id=$1`
	args := []any{projectID}
	if req.SHA != "" {
		query += ` AND sha=$2`
		args = append(args, req.SHA)
	}
	query += ` ORDER BY (store_key IS NOT NULL) DESC, created_at DESC LIMIT 1`
	var art deliveryArtifact
	if err := model.Pool.QueryRow(ctx, query, args...).
		Scan(&art.sha, &art.tag, &art.image, &art.digest, &art.sizeBytes, &art.storeKey); err != nil {
		return respond.BusinessRule(c, "NO_ARTIFACT", "no artifact available; trigger a build first")
	}
	tag := art.tag
	if t := strings.TrimSpace(req.Tag); t != "" {
		if len(t) > 128 || strings.Contains(t, "..") || strings.ContainsAny(t, "\x00\n\r") {
			return respond.InvalidParam(c, "tag", "invalid delivery tag")
		}
		tag = t
	}
	username, _ := c.Locals("username").(string)
	var deliveryID string
	if err := model.Pool.QueryRow(ctx, `
		INSERT INTO project_deliveries (project_id, tag, sha, status, created_by)
		VALUES ($1,$2,$3,'assembling',$4)
		ON CONFLICT (project_id, tag) DO UPDATE
		  SET sha=EXCLUDED.sha, status='assembling', error=NULL, manifest='{}'::jsonb,
		      updated_at=now(), created_by=EXCLUDED.created_by
		RETURNING id::text`, projectID, tag, art.sha, username).Scan(&deliveryID); err != nil {
		return respond.DBError(c, err)
	}
	zipBytes, manifest, err := assembleDeliveryPackage(ctx, pm, art, tag)
	if err != nil {
		slog.Warn("delivery assemble failed", "project_id", projectID, "tag", tag, "err", err.Error())
		_, _ = model.Pool.Exec(ctx,
			`UPDATE project_deliveries SET status='failed', error=$2, updated_at=now() WHERE id=$1`,
			deliveryID, err.Error())
		return respond.Internal(c, err)
	}
	store, err := storage.Get()
	if err != nil {
		return respond.Internal(c, err)
	}
	key := fmt.Sprintf("delivery/%s/docs-%s.zip", projectID, deliveryKeyTag(tag))
	if err := store.Put(ctx, key, bytes.NewReader(zipBytes), int64(len(zipBytes)), "application/zip"); err != nil {
		return respond.Internal(c, err)
	}
	mj, _ := json.Marshal(manifest)
	if _, err := model.Pool.Exec(ctx,
		`UPDATE project_deliveries SET status='ready', store_key=$2, manifest=$3::jsonb, updated_at=now() WHERE id=$1`,
		deliveryID, key, string(mj)); err != nil {
		return respond.DBError(c, err)
	}
	logAudit(ctx, projectID, username, "delivery.assembled", "delivery", deliveryID,
		`{"tag":`+quoteJSON(tag)+`,"sha":`+quoteJSON(art.sha)+`,"size_bytes":`+strconv.Itoa(len(zipBytes))+`}`)
	events.Default.Publish("project:"+projectID, "delivery.ready",
		map[string]any{"id": deliveryID, "tag": tag, "sha": art.sha})
	return respond.OK(c, fiber.Map{
		"id": deliveryID, "tag": tag, "sha": art.sha, "status": "ready",
		"store_key": key, "size_bytes": len(zipBytes), "manifest": manifest,
	})
}

// deliveryKeyTag 把 tag 规范为存储键/文件名安全片段（允许的字符外全部替换为 '-'）。
func deliveryKeyTag(tag string) string {
	var sb strings.Builder
	for _, r := range tag {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
			sb.WriteRune(r)
		default:
			sb.WriteRune('-')
		}
	}
	s := sb.String()
	if s == "" || s == "." || s == ".." {
		return "delivery"
	}
	return s
}

// assembleDeliveryPackage 生成 docs.zip 字节与 manifest。
// 内容：delivery.json / deploy/compose.yml / README-DEPLOY.md / CHANGELOG.md / project.yaml。
func assembleDeliveryPackage(ctx context.Context, pm projectMeta, art deliveryArtifact, tag string) ([]byte, map[string]any, error) {
	notes, notesCount := releaseNotesMarkdown(ctx, pm, tag, art)
	manifestYAML := repoFileText(ctx, pm, "project.yaml")
	if strings.TrimSpace(manifestYAML) == "" {
		manifestYAML = templates.StandardManifest()
	}
	composeYML := repoFileText(ctx, pm, "deploy/compose.yml")
	if strings.TrimSpace(composeYML) == "" {
		composeYML = templateFile(pm, "deploy/compose.yml")
	}
	// M5 彩排实录：模板 image 为 {{slug}}:latest，与实际 tar 标签不符；
	// 交付包内直接改写为本次交付的镜像 tag，客户 `docker load + compose up -d` 开箱即用
	composeYML = patchComposeImage(composeYML, art.image, art.tag)
	deliveryJSON, err := json.MarshalIndent(map[string]any{
		"project":       map[string]any{"id": pm.id, "name": pm.name, "slug": pm.slug, "repo": pm.repoFull},
		"template":      pm.template,
		"tag":           tag,
		"artifact":      map[string]any{"sha": art.sha, "tag": art.tag, "image": art.image, "digest": art.digest, "size_bytes": art.sizeBytes},
		"has_image_tar": art.storeKey != "",
		"generated_at":  time.Now().UTC().Format(time.RFC3339),
	}, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	files := []struct{ name, content string }{
		{"delivery.json", string(deliveryJSON)},
		{"deploy/compose.yml", composeYML},
		{"README-DEPLOY.md", renderDeployReadme(pm, art, tag)},
		{"CHANGELOG.md", notes},
		{"project.yaml", manifestYAML},
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	fileList := make([]map[string]any, 0, len(files))
	for _, f := range files {
		w, werr := zw.Create(f.name)
		if werr != nil {
			return nil, nil, werr
		}
		if _, werr := w.Write([]byte(f.content)); werr != nil {
			return nil, nil, werr
		}
		fileList = append(fileList, map[string]any{"name": f.name, "size": len(f.content)})
	}
	if err := zw.Close(); err != nil {
		return nil, nil, err
	}
	manifest := map[string]any{
		"files":        fileList,
		"delivery_tag": tag,
		"image": map[string]any{
			"name": art.image, "tag": art.tag, "digest": art.digest,
			"sha": art.sha, "size_bytes": art.sizeBytes,
		},
		"has_tar":      art.storeKey != "",
		"notes_tasks":  notesCount,
		"generated_at": time.Now().UTC().Format(time.RFC3339),
	}
	return buf.Bytes(), manifest, nil
}

// repoFileText 读取项目仓内文件（base64 解码）；失败返回空串由调用方回退模板。
func repoFileText(ctx context.Context, pm projectMeta, path string) string {
	gc := gitea.NewFromEnv()
	if gc == nil || pm.repoFull == "" {
		return ""
	}
	fc, err := gc.GetFileContents(ctx, fullName2owner(pm.repoFull), fullName2repo(pm.repoFull), path, pm.branch)
	if err != nil || fc == nil || strings.TrimSpace(fc.Content) == "" {
		return ""
	}
	b, derr := base64.StdEncoding.DecodeString(strings.ReplaceAll(strings.TrimSpace(fc.Content), "\n", ""))
	if derr != nil {
		return ""
	}
	return string(b)
}

// patchComposeImage 把客户 compose 首条 `image:` 行替换为交付包实际镜像 tag
// （模板 {{slug}}:latest 与实际 docker-load 标签不符；见 M5 彩排记录）。
func patchComposeImage(compose, image, tag string) string {
	if strings.TrimSpace(compose) == "" || image == "" || tag == "" {
		return compose
	}
	lines := strings.Split(compose, "\n")
	for i, ln := range lines {
		if strings.HasPrefix(strings.TrimSpace(ln), "image:") {
			indent := ln[:len(ln)-len(strings.TrimLeft(ln, " \t"))]
			lines[i] = indent + "image: " + image + ":" + tag
			break
		}
	}
	return strings.Join(lines, "\n")
}

// templateFile 渲染标准模板中的单文件（兜底 compose/README 等）。
func templateFile(pm projectMeta, path string) string {
	t, ok := templates.Get("standard-go-react")
	if !ok {
		return ""
	}
	files, err := t.Files(templates.Vars{Name: pm.name, Slug: pm.slug, Repo: pm.repoFull, ProjectID: pm.id})
	if err != nil {
		return ""
	}
	for _, f := range files {
		if f.Path == path {
			return f.Content
		}
	}
	return ""
}

// renderDeployReadme 客户部署手册（README-DEPLOY.md）。
func renderDeployReadme(pm projectMeta, art deliveryArtifact, tag string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# %s — 部署手册\n\n", pm.name)
	fmt.Fprintf(&sb, "> 由 FORJA 自动生成；交付标签 `%s`，镜像 `%s:%s`，提交 `%s`。\n\n", tag, art.image, art.tag, art.sha)
	sb.WriteString("## 前置要求\n\n- Docker 24+（含 compose v2）\n- Linux amd64 容器运行环境\n\n")
	sb.WriteString("## 交付物\n\n| 文件 | 说明 |\n|---|---|\n")
	fmt.Fprintf(&sb, "| `image-%s.tar.gz` | 应用镜像归档（`docker load` 导入）|\n", art.sha[:8])
	sb.WriteString("| `deploy/compose.yml` | 客户环境运行清单（镜像 tag 已指向本交付版本）|\n")
	sb.WriteString("| `delivery.json` | 版本/镜像/摘要元数据 |\n")
	sb.WriteString("| `CHANGELOG.md` | 本版本变更（Release Notes）|\n")
	sb.WriteString("| `project.yaml` | 技术栈声明（构建/测试约定）|\n\n")
	if art.storeKey == "" {
		fmt.Fprintf(&sb, "> 注意：本次未附镜像 tar，请从 registry 拉取 `%s:%s`。\n\n", art.image, art.tag)
	}
	sb.WriteString("## 部署\n\n```bash\n")
	if art.storeKey != "" {
		fmt.Fprintf(&sb, "gunzip -c image-%s.tar.gz | docker load\n", art.sha[:8])
	}
	sb.WriteString("docker compose up -d\n")
	sb.WriteString("curl -fsS http://127.0.0.1:8080/health\n```\n\n")
	sb.WriteString("## 升级 / 回滚\n\n")
	sb.WriteString("- 升级：导入新交付包镜像 tar，替换 `deploy/compose.yml` 中的镜像 tag，`docker compose up -d`。\n")
	sb.WriteString("- 回滚：保留上一交付包的镜像 tar，重新 `docker load` 后把 tag 改回旧值，`docker compose up -d`。\n")
	sb.WriteString("- 回滚只认**交付包中的镜像 tag**（`<分支>-<sha8>`），与构建记录一一对应。\n\n")
	sb.WriteString("## 常见问题\n\n")
	sb.WriteString("- `/health` 不达：检查端口占用与容器日志 `docker compose logs app`。\n")
	sb.WriteString("- 镜像加载报错：确认 Docker 版本 ≥24，且磁盘空间充足。\n")
	return sb.String()
}

// releaseNotesMarkdown 聚合项目已发布任务生成 Release Notes（按 Spec 分组，上限 100）。
func releaseNotesMarkdown(ctx context.Context, pm projectMeta, tag string, art deliveryArtifact) (string, int) {
	type row struct {
		title, commitSHA, specTitle string
		updatedAt                   time.Time
	}
	var rows []row
	if model.Pool != nil {
		r, err := model.Pool.Query(ctx, `
			SELECT t.title, COALESCE(t.commit_sha,''), COALESCE(s.title,''), t.updated_at
			  FROM tasks t LEFT JOIN specs s ON s.id=t.spec_id
			 WHERE t.project_id=$1 AND t.status='RELEASED'
			 ORDER BY t.updated_at DESC LIMIT 100`, pm.id)
		if err == nil {
			defer r.Close()
			for r.Next() {
				var x row
				if serr := r.Scan(&x.title, &x.commitSHA, &x.specTitle, &x.updatedAt); serr == nil {
					rows = append(rows, x)
				}
			}
		}
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Release Notes — %s\n\n", pm.name)
	fmt.Fprintf(&sb, "- 交付标签: `%s`\n- 镜像: `%s:%s`\n- 提交: `%s`\n- 生成时间: %s\n\n",
		tag, art.image, art.tag, art.sha, time.Now().UTC().Format("2006-01-02 15:04 UTC"))
	if len(rows) == 0 {
		sb.WriteString("_本版本暂无已发布任务记录。_\n")
		return sb.String(), 0
	}
	currentSpec := "\x00"
	for _, x := range rows {
		spec := x.specTitle
		if spec == "" {
			spec = "未关联 Spec"
		}
		if spec != currentSpec {
			fmt.Fprintf(&sb, "## %s\n\n", spec)
			currentSpec = spec
		}
		short := x.commitSHA
		if len(short) > 8 {
			short = short[:8]
		}
		if short == "" {
			short = "-"
		}
		fmt.Fprintf(&sb, "- %s（`%s`，%s）\n", x.title, short, x.updatedAt.UTC().Format("2006-01-02"))
	}
	return sb.String(), len(rows)
}

// listDeliveries GET /projects/:id/deliveries
func listDeliveries(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	projectID := c.Params("id")
	if !idgen.IsValid(projectID) {
		return respond.InvalidParam(c, "id", "invalid project id")
	}
	if err := AuthorizeProject(c, projectID, "task.read"); err != nil {
		return err
	}
	page, pageSize := respond.ParsePage(c)
	var total int64
	if err := model.Pool.QueryRow(c.Context(),
		`SELECT COUNT(*) FROM project_deliveries WHERE project_id=$1`, projectID).Scan(&total); err != nil {
		return respond.DBError(c, err)
	}
	rows, err := model.Pool.Query(c.Context(), `
		SELECT id::text, tag, sha, status, COALESCE(manifest,'{}'::jsonb), store_key IS NOT NULL,
		       COALESCE(error,''), COALESCE(created_by,''), created_at, updated_at
		  FROM project_deliveries WHERE project_id=$1
		 ORDER BY created_at DESC LIMIT $2 OFFSET $3`, projectID, pageSize, (page-1)*pageSize)
	if err != nil {
		return respond.DBError(c, err)
	}
	defer rows.Close()
	items := []fiber.Map{}
	for rows.Next() {
		var id, tag, sha, status, errText, createdBy string
		var manifest []byte
		var hasZip bool
		var createdAt, updatedAt any
		if err := rows.Scan(&id, &tag, &sha, &status, &manifest, &hasZip, &errText, &createdBy, &createdAt, &updatedAt); err != nil {
			return respond.DBError(c, err)
		}
		items = append(items, fiber.Map{
			"id": id, "tag": tag, "sha": sha, "status": status,
			"manifest": json.RawMessage(manifest), "has_zip": hasZip,
			"error": errText, "created_by": createdBy,
			"created_at": createdAt, "updated_at": updatedAt,
		})
	}
	return respond.List(c, items, respond.PageMeta{Page: page, PageSize: pageSize, Total: total})
}

func safeDownloadName(s string) string {
	s = strings.ReplaceAll(s, `"`, "")
	s = strings.ReplaceAll(s, "\\", "")
	s = strings.ReplaceAll(s, "/", "_")
	if s == "" {
		s = "download"
	}
	return s
}

// downloadDelivery GET /projects/:id/deliveries/:did/download —— docs.zip 流式下载。
func downloadDelivery(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	projectID, did := c.Params("id"), c.Params("did")
	if !idgen.IsValid(projectID) {
		return respond.InvalidParam(c, "id", "invalid project id")
	}
	if !idgen.IsValid(did) {
		return respond.InvalidParam(c, "did", "invalid delivery id")
	}
	if err := AuthorizeProject(c, projectID, "task.read"); err != nil {
		return err
	}
	var tag, key, slug string
	if err := model.Pool.QueryRow(c.Context(), `
		SELECT d.tag, COALESCE(d.store_key,''), COALESCE(p.slug,'delivery')
		  FROM project_deliveries d JOIN projects p ON p.id=d.project_id
		 WHERE d.id=$1 AND d.project_id=$2`, did, projectID).Scan(&tag, &key, &slug); err != nil {
		return respond.NotFound(c, "delivery")
	}
	if key == "" {
		return respond.BusinessRule(c, "DELIVERY_NOT_READY", "delivery package is not assembled yet")
	}
	store, err := storage.Get()
	if err != nil {
		return respond.Internal(c, err)
	}
	rc, err := store.Open(c.Context(), key)
	if err != nil {
		return respond.NotFound(c, "delivery package")
	}
	c.Set("Content-Type", "application/zip")
	c.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-%s.zip"`, safeDownloadName(slug), safeDownloadName(tag)))
	// SendStream 把读取权交给 fasthttp（响应写出后由其关闭），此处不可提前 Close
	return c.SendStream(rc)
}

// downloadDeliveryArtifact GET /projects/:id/deliveries/:did/artifact —— 镜像 tar.gz 透传下载。
func downloadDeliveryArtifact(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	projectID, did := c.Params("id"), c.Params("did")
	if !idgen.IsValid(projectID) {
		return respond.InvalidParam(c, "id", "invalid project id")
	}
	if !idgen.IsValid(did) {
		return respond.InvalidParam(c, "did", "invalid delivery id")
	}
	if err := AuthorizeProject(c, projectID, "task.read"); err != nil {
		return err
	}
	var sha, image, tag, key string
	if err := model.Pool.QueryRow(c.Context(), `
		SELECT d.sha, a.image, COALESCE(a.tag,''), COALESCE(a.store_key,'')
		  FROM project_deliveries d
		  JOIN project_artifacts a ON a.project_id=d.project_id AND a.sha=d.sha
		 WHERE d.id=$1 AND d.project_id=$2`, did, projectID).Scan(&sha, &image, &tag, &key); err != nil {
		return respond.NotFound(c, "delivery")
	}
	if key == "" {
		return respond.BusinessRule(c, "ARTIFACT_TAR_MISSING",
			"image tar not uploaded; use registry image "+image+":"+tag)
	}
	store, err := storage.Get()
	if err != nil {
		return respond.Internal(c, err)
	}
	rc, err := store.Open(c.Context(), key)
	if err != nil {
		return respond.NotFound(c, "artifact package")
	}
	c.Set("Content-Type", "application/gzip")
	c.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-%s.tar.gz"`, safeDownloadName(strings.ReplaceAll(image, "/", "_")), safeDownloadName(tag)))
	// SendStream 把读取权交给 fasthttp（响应写出后由其关闭），此处不可提前 Close
	return c.SendStream(rc)
}

// releaseNotesPreview GET /projects/:id/releases/notes?tag= —— Release Notes Markdown 预览。
func releaseNotesPreview(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	projectID := c.Params("id")
	if !idgen.IsValid(projectID) {
		return respond.InvalidParam(c, "id", "invalid project id")
	}
	if err := AuthorizeProject(c, projectID, "task.read"); err != nil {
		return err
	}
	ctx := c.Context()
	pm, ok := loadProjectMeta(ctx, projectID)
	if !ok {
		return respond.NotFound(c, "project")
	}
	tag := strings.TrimSpace(c.Query("tag"))
	query := `SELECT sha, tag, image FROM project_artifacts WHERE project_id=$1`
	args := []any{projectID}
	if tag != "" {
		query += ` AND tag=$2`
		args = append(args, tag)
	}
	query += ` ORDER BY created_at DESC LIMIT 1`
	var art deliveryArtifact
	if err := model.Pool.QueryRow(ctx, query, args...).Scan(&art.sha, &art.tag, &art.image); err != nil {
		return respond.BusinessRule(c, "NO_ARTIFACT", "no artifact available; trigger a build first")
	}
	outTag := tag
	if outTag == "" {
		outTag = art.tag
	}
	md, n := releaseNotesMarkdown(ctx, pm, outTag, art)
	return respond.OK(c, fiber.Map{"tag": outTag, "sha": art.sha, "image": art.image, "tasks": n, "notes": md})
}
