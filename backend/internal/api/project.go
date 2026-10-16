package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"forja/backend/internal/api/respond"
	"forja/backend/internal/auth"
	"forja/backend/internal/gitea"
	idgen "forja/backend/internal/id"
	"forja/backend/internal/model"
	"forja/backend/internal/projectyaml"
	"forja/backend/internal/storage"
	"forja/backend/internal/templates"

	"github.com/gofiber/fiber/v2"
)

func RegisterProjectRoutes(r fiber.Router) {
	r.Get("/project-templates", auth.Require("project.list"), listProjectTemplates)
	r.Get("/projects", auth.Require("project.list"), listProjects)
	r.Post("/projects", auth.Require("project.create"), createProject)
	r.Get("/projects/:id", auth.Require("project.list"), getProject)
	r.Patch("/projects/:id", auth.Require("project.update"), updateProject)
	r.Delete("/projects/:id", auth.Require("project.delete"), deleteProject)
	r.Post("/projects/:id/restore", auth.Require("project.update"), restoreProject)
	r.Get("/projects/:id/agents", auth.Require("project.list"), getProjectAgents)
	r.Put("/projects/:id/agents", auth.Require("project.update"), putProjectAgents)
	r.Post("/projects/:id/agents/generate", auth.Require("project.update"), generateProjectAgents)
	r.Post("/projects/:id/builds", auth.Require("task.run"), triggerProjectBuild)
	r.Get("/projects/:id/artifacts", auth.Require("task.read"), listProjectArtifacts)
	// 存量项目回填项目仓 build secrets（M5+：老项目无建项注入链路）
	r.Post("/projects/:id/provision", auth.Require("project.update"), provisionProject)
	r.Get("/projects/:id/members", auth.Require("project.list"), listMembers)
	r.Post("/projects/:id/members", auth.Require("project.member.manage"), addMember)
	r.Patch("/projects/:id/members/:userId", auth.Require("project.member.manage"), updateMember)
	r.Delete("/projects/:id/members/:userId", auth.Require("project.member.manage"), removeMember)
}

var validRoles = map[string]bool{"Admin": true, "Architect": true, "Developer": true, "Viewer": true, "PM": true, "QA": true}

// ProjectDefaultBranch 项目集成分支的单一来源（F0.5 基础契约）：
// 创建/导入时从 Gitea 读入并落库；gitops/dispatch/iteration/release/pipeline 一律读它，
// 不再硬编码 master（导入默认 main 仓的前提）。缺失/非法 id 回落 master。
func ProjectDefaultBranch(ctx context.Context, projectID string) string {
	b := ""
	if model.Pool != nil && idgen.IsValid(projectID) {
		_ = model.Pool.QueryRow(ctx,
			`SELECT COALESCE(NULLIF(default_branch,''),'master') FROM projects WHERE id=$1`, projectID).Scan(&b)
	}
	if b == "" {
		b = "master"
	}
	return b
}

// refreshProjectAgents F0.5 缓存刷新（D13）：push 命中 AGENTS.md 时重读仓内规范，
// 只刷新 projects.agents_sha（内容真相仍在仓内，DB 仅缓存/展示；锻造时另 clone 读取）。
func refreshProjectAgents(ctx context.Context, projectID string) {
	gc := gitea.NewFromEnv()
	if gc == nil || model.Pool == nil {
		return
	}
	var repoFull, defBranch string
	if err := model.Pool.QueryRow(ctx,
		`SELECT COALESCE(repo_full_name,''), COALESCE(NULLIF(default_branch,''),'master') FROM projects WHERE id=$1`,
		projectID).Scan(&repoFull, &defBranch); err != nil || repoFull == "" {
		return
	}
	fc, err := gc.GetFileContents(ctx, fullName2owner(repoFull), fullName2repo(repoFull), "AGENTS.md", defBranch)
	if err != nil {
		slog.Warn("refresh project agents failed", "project_id", projectID, "err", err.Error())
		return
	}
	sha := ""
	if fc != nil {
		sha = fc.SHA
	}
	_, _ = model.Pool.Exec(ctx,
		`UPDATE projects SET agents_sha=NULLIF($2,''), agents_updated_at=now() WHERE id=$1`, projectID, sha)
}

// refreshProjectManifest F0.5 D13：push 命中 project.yaml 时重解析并刷新 stack 缓存
// （真相在仓；解析失败保持旧缓存并告警，不阻塞）。
func refreshProjectManifest(ctx context.Context, projectID string) {
	gc := gitea.NewFromEnv()
	if gc == nil || model.Pool == nil {
		return
	}
	repoFull, defBranch, ok := projectRepoRef(ctx, projectID)
	if !ok || repoFull == "" {
		return
	}
	fc, err := gc.GetFileContents(ctx, fullName2owner(repoFull), fullName2repo(repoFull), "project.yaml", defBranch)
	if err != nil || fc == nil {
		return
	}
	raw, derr := base64.StdEncoding.DecodeString(strings.TrimSpace(fc.Content))
	if derr != nil {
		return
	}
	m, perr := projectyaml.Parse(raw)
	if perr != nil {
		slog.Warn("project.yaml invalid on push; stack cache unchanged", "project_id", projectID, "err", perr.Error())
		return
	}
	stackJSON, _ := m.JSON()
	_, _ = model.Pool.Exec(ctx, `UPDATE projects SET stack=$2, updated_at=now() WHERE id=$1`, projectID, string(stackJSON))
}

func slugify(name, fallback string) string {
	s := strings.ToLower(regexp.MustCompile(`[^a-zA-Z0-9]+`).ReplaceAllString(name, "-"))
	s = strings.Trim(s, "-")
	if s == "" {
		return fallback
	}
	return s
}

// repoNameFor F0.5 基础契约：仓库名 = <slug>-<id8>（id 末 8 位随机段），
// 保证同一 owner 命名空间下唯一，避免 CreateRepo 幂等路径静默复用他项目/残留仓库。
func repoNameFor(slug, id string) string {
	id8 := id
	if len(id) > 8 {
		id8 = id[len(id)-8:]
	}
	if slug == "" {
		return id8
	}
	if strings.HasSuffix(slug, "-"+id8) {
		return slug
	}
	return slug + "-" + id8
}

func listProjects(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	q := strings.TrimSpace(c.Query("q"))
	archivedOnly := c.Query("archived") == "true" // S1.4：默认仅活跃项目，?archived=true 仅归档项目
	page, pageSize := respond.ParsePage(c)

	where := ``
	var args []interface{}
	if !isGlobalAdmin(c) {
		uid := currentUserID(c)
		if uid == "" {
			return respond.List(c, []fiber.Map{}, respond.PageMeta{Page: page, PageSize: pageSize, Total: 0})
		}
		where = ` JOIN project_members m ON m.project_id=p.id AND m.user_id=$1`
		args = append(args, uid)
	}
	// 归档过滤（S1.4）+ 搜索条件
	cond := ` WHERE p.archived_at IS `
	if archivedOnly {
		cond += `NOT NULL`
	} else {
		cond += `NULL`
	}
	if q != "" {
		n := len(args) + 1
		cond += ` AND (p.name ILIKE $` + itoa(n) + ` OR p.description ILIKE $` + itoa(n) + `)`
		args = append(args, "%"+q+"%")
	}
	var total int64
	if err := model.Pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM projects p`+where+cond, args...).Scan(&total); err != nil {
		return respond.DBError(c, err)
	}
	limitIdx := len(args) + 1
	args = append(args, pageSize, (page-1)*pageSize)
	rows, err := model.Pool.Query(context.Background(),
		`SELECT p.id, p.name, p.description, p.slug, u.username, (SELECT COUNT(*) FROM project_members m2 WHERE m2.project_id=p.id), p.archived_at FROM projects p LEFT JOIN users u ON u.id=p.owner_id`+where+cond+` ORDER BY p.created_at DESC LIMIT $`+itoa(limitIdx)+` OFFSET $`+itoa(limitIdx+1), args...)
	if err != nil {
		return respond.DBError(c, err)
	}
	defer rows.Close()
	items := []fiber.Map{}
	for rows.Next() {
		var id string
		var name string
		var descPtr, slugPtr, ownerPtr *string
		var memberCount int64
		var archivedAt *time.Time
		if err := rows.Scan(&id, &name, &descPtr, &slugPtr, &ownerPtr, &memberCount, &archivedAt); err != nil {
			return respond.DBError(c, err)
		}
		item := fiber.Map{
			"id": id, "name": name,
			"description": deref(descPtr), "slug": deref(slugPtr),
			"owner": deref(ownerPtr), "member_count": memberCount,
			"archived": archivedAt != nil,
		}
		if archivedAt != nil {
			item["archived_at"] = archivedAt.Format(time.RFC3339)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return respond.DBError(c, err)
	}
	return respond.List(c, items, respond.PageMeta{Page: page, PageSize: pageSize, Total: total})
}

// createProject 建项（v2.0）：mode=create 走模板脚手架；mode=import 接入既有仓。
func createProject(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	var body struct {
		Name         string            `json:"name"`
		Description  string            `json:"description"`
		Mode         string            `json:"mode"`
		Template     string            `json:"template"`
		RepoFullName string            `json:"repo_full_name"`
		MirrorURL    string            `json:"mirror_url"`
		Options      map[string]string `json:"options"` // v2.2 T2：模板参数（值受模板声明约束）
	}
	if err := c.BodyParser(&body); err != nil {
		return respond.BadRequest(c, err.Error())
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" {
		return respond.InvalidParam(c, "name", "name required")
	}
	mode := body.Mode
	if mode == "" {
		mode = "create"
	}
	if mode != "create" && mode != "import" {
		return respond.InvalidParam(c, "mode", "mode must be create or import")
	}
	id := idgen.New()
	slug := slugify(body.Name, id[len(id)-8:])
	repoName := repoNameFor(slug, id)
	uid := currentUserID(c)
	username, _ := c.Locals("username").(string)
	ctx := context.Background()

	if mode == "create" {
		return createProjectFromTemplate(c, ctx, id, slug, repoName, uid, username, body.Name, body.Description, body.Template, body.Options)
	}
	return createProjectFromImport(c, ctx, id, slug, repoName, uid, username, body.Name, body.Description, body.RepoFullName, body.MirrorURL)
}

// isUniqueViolation 唯一约束冲突（name/slug/repo_full_name）。
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "duplicate key") || strings.Contains(s, "unique")
}

// insertProjectRow 建项落库（含 v2.0 基础契约列）。repoFull/defBranch/agentsSHA 可空。
func insertProjectRow(ctx context.Context, id, name, description, slug, uid, mode, template, templateVersion string, stackJSON []byte, repoFull, defBranch, agentsSHA string) error {
	var ownerID any
	if uid != "" {
		ownerID = uid
	}
	stack := string(stackJSON)
	if stack == "" {
		stack = "{}"
	}
	_, err := model.Pool.Exec(ctx, `
		INSERT INTO projects (id, name, description, slug, owner_id, mode, template, template_version, stack,
		                      repo_full_name, default_branch, agents_sha, agents_updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),$9,
		        NULLIF($10,''), COALESCE(NULLIF($11,''),'master'), NULLIF($12,''),
		        CASE WHEN NULLIF($12,'') IS NULL THEN NULL ELSE now() END)`,
		id, name, description, slug, ownerID, mode, template, templateVersion, stack, repoFull, defBranch, agentsSHA)
	return err
}

// createProjectFromTemplate mode=create：模板预检 → 落库 → Gitea 建仓+脚手架首提。
// 模板 manifest 随代码评审（预检失败属实现缺陷，不产生半成品项目）。
func createProjectFromTemplate(c *fiber.Ctx, ctx context.Context, id, slug, repoName, uid, username, name, description, tplID string, opts map[string]string) error {
	if tplID == "" {
		tplID = "standard-go-react"
	}
	tpl, ok := templates.Get(tplID)
	if !ok {
		return respond.InvalidParam(c, "template", "unknown template: "+tplID)
	}
	// v2.2 T2：模板参数校验 + 默认值补齐（未知 key/非法枚举/非法布尔一律 400）
	resolved, rerr := templates.ResolveOptions(tpl.Options, opts)
	if rerr != nil {
		return respond.InvalidParam(c, "options", rerr.Error())
	}
	pre, err := tpl.Files(templates.Vars{Name: name, Slug: slug, Repo: "precheck/precheck", ProjectID: id, Options: resolved})
	if err != nil {
		return respond.Internal(c, err)
	}
	manifestYAML := ""
	for _, f := range pre {
		if f.Path == "project.yaml" {
			manifestYAML = f.Content
		}
	}
	manifest, err := projectyaml.Parse([]byte(manifestYAML))
	if err != nil {
		slog.Error("scaffold manifest invalid", "template", tplID, "err", err.Error())
		return respond.BusinessRule(c, "TEMPLATE_INVALID", err.Error())
	}
	stackJSON, _ := manifest.JSON()
	if err := insertProjectRow(ctx, id, name, description, slug, uid, "create", tplID, tpl.Version, stackJSON, "", "", ""); err != nil {
		if isUniqueViolation(err) {
			return respond.Conflict(c, "project name or slug exists")
		}
		return respond.DBError(c, err)
	}
	if uid != "" {
		if _, err := model.Pool.Exec(ctx, `INSERT INTO project_members (project_id, user_id, username, role) VALUES ($1,$2,$3,'Admin') ON CONFLICT DO NOTHING`, id, uid, username); err != nil {
			return respond.DBError(c, err)
		}
	}
	logAudit(ctx, id, username, "project.create", "project", id,
		`{"name":`+quoteJSON(name)+`,"mode":"create","template":`+quoteJSON(tplID)+`}`)

	resp := fiber.Map{"id": id, "name": name, "description": description, "slug": slug,
		"mode": "create", "template": tplID, "template_version": tpl.Version, "default_branch": "master"}
	if gc := gitea.NewFromEnv(); gc != nil {
		gctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		gi := integrateGiteaCreate(gc, gctx, id, slug, repoName, name, description, tpl, resolved)
		resp["gitea"] = gi
		if db, ok := gi["default_branch"].(string); ok && db != "" {
			resp["default_branch"] = db
		}
		detailJSON, _ := json.Marshal(gi)
		logAudit(gctx, id, username, "project.gitea_integration", "project", id, string(detailJSON))
	}
	return respond.OK(c, resp)
}

// createProjectFromImport mode=import：同实例仓直接引用 / 外网仓 MigrateRepo 镜像；
// 契约校验（project.yaml 必须存在且合法）先于落库，避免半成品项目。
func createProjectFromImport(c *fiber.Ctx, ctx context.Context, id, slug, repoName, uid, username, name, description, repoFullIn, mirrorURL string) error {
	gc := gitea.NewFromEnv()
	if gc == nil {
		return respond.ServiceUnavailable(c)
	}
	var owner, repo string
	switch {
	case repoFullIn != "":
		owner, repo = fullName2owner(repoFullIn), fullName2repo(repoFullIn)
		if owner == repoFullIn || repo == "" {
			return respond.InvalidParam(c, "repo_full_name", "must be owner/repo")
		}
	case mirrorURL != "":
		fn, _, err := gc.MigrateRepo(ctx, mirrorURL, repoName, description)
		if err != nil {
			return respond.Upstream(c, "gitea", err)
		}
		owner, repo = fullName2owner(fn), fullName2repo(fn)
	default:
		return respond.InvalidParam(c, "repo_full_name", "repo_full_name (same instance) or mirror_url (external) required")
	}
	fullName := owner + "/" + repo
	var bound bool
	_ = model.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE repo_full_name=$1)`, fullName).Scan(&bound)
	if bound {
		return respond.BusinessRule(c, "REPO_ALREADY_LINKED", "repo already linked to another project: "+fullName)
	}
	info, err := gc.GetRepo(ctx, owner, repo)
	if err != nil {
		return respond.Upstream(c, "gitea", err)
	}
	if info == nil {
		return respond.NotFound(c, "repo")
	}
	defBranch := info.DefaultBranch
	if defBranch == "" {
		defBranch = "master"
	}
	fcYaml, err := gc.GetFileContents(ctx, owner, repo, "project.yaml", defBranch)
	if err != nil {
		return respond.Upstream(c, "gitea", err)
	}
	if fcYaml == nil {
		return respond.BusinessRule(c, "PROJECT_YAML_REQUIRED",
			"import requires project.yaml at repo root. Template:\n"+templates.StandardManifest())
	}
	raw, derr := base64.StdEncoding.DecodeString(strings.TrimSpace(fcYaml.Content))
	if derr != nil {
		return respond.InvalidParam(c, "project.yaml", "invalid file content encoding")
	}
	manifest, perr := projectyaml.Parse(raw)
	if perr != nil {
		return respond.BusinessRule(c, "PROJECT_YAML_INVALID", perr.Error())
	}
	stackJSON, _ := manifest.JSON()
	agentsSHA := ""
	if fcA, e := gc.GetFileContents(ctx, owner, repo, "AGENTS.md", defBranch); e == nil && fcA != nil {
		agentsSHA = fcA.SHA
	}
	if err := insertProjectRow(ctx, id, name, description, slug, uid, "import", "import", "", stackJSON, fullName, defBranch, agentsSHA); err != nil {
		if isUniqueViolation(err) {
			return respond.Conflict(c, "project name or slug exists")
		}
		return respond.DBError(c, err)
	}
	if uid != "" {
		if _, err := model.Pool.Exec(ctx, `INSERT INTO project_members (project_id, user_id, username, role) VALUES ($1,$2,$3,'Admin') ON CONFLICT DO NOTHING`, id, uid, username); err != nil {
			return respond.DBError(c, err)
		}
	}
	logAudit(ctx, id, username, "project.import", "project", id,
		fmt.Sprintf(`{"name":%s,"repo":%s}`, quoteJSON(name), quoteJSON(fullName)))

	out := fiber.Map{"enabled": true, "mode": "import", "repo": fullName, "default_branch": defBranch}
	hookURL := strings.TrimSuffix(getenvLocal("GITEA_HOOK_URL", "http://api:8080/api/v1/webhooks/gitea"), "/")
	if err := gc.EnsureWebhook(ctx, owner, repo, hookURL, getenvLocal("GITEA_WEBHOOK_SECRET", "")); err != nil {
		out["error"] = "webhook: " + err.Error()
	} else {
		out["webhook"] = true
	}
	provisionRepoSecrets(gc, ctx, owner, repo, out)
	return respond.OK(c, fiber.Map{"id": id, "name": name, "description": description, "slug": slug,
		"mode": "import", "template": "import", "repo_full_name": fullName,
		"default_branch": defBranch, "stack": json.RawMessage(stackJSON), "gitea": out})
}

// integrateGiteaCreate mode=create 的 Gitea 闭环：建仓 → 模板脚手架首提（含
// project.yaml/AGENTS.md）→ agents_sha 基线 → webhook → repo secrets。
// 任一步失败即止并返回错误摘要（不 panic、不重试）；集成失败不阻塞建项主流程。
func integrateGiteaCreate(gc *gitea.Client, ctx context.Context, projectID, slug, repoName, displayName, description string, tpl *templates.Template, opts map[string]string) fiber.Map {
	out := fiber.Map{"enabled": true, "mode": "create"}
	fullName, htmlURL, err := gc.CreateRepo(ctx, repoName, description)
	if err != nil {
		out["error"] = "create_repo: " + err.Error()
		return out
	}
	if fullName2repo(fullName) != repoName {
		out["error"] = fmt.Sprintf("repo name mismatch: got %s want %s", fullName, repoName)
		return out
	}
	// 防双绑：同名仓已被其他项目绑定则拒绝（unique index 兜底；显式检查给可读错误）
	if model.Pool != nil {
		var bound bool
		_ = model.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE repo_full_name=$1 AND id<>$2)`, fullName, projectID).Scan(&bound)
		if bound {
			out["error"] = "repo already linked to another project: " + fullName
			return out
		}
	}
	owner, repo := fullName2owner(fullName), fullName2repo(fullName)
	defBranch := "master"
	if info, ierr := gc.GetRepo(ctx, owner, repo); ierr == nil && info != nil && info.DefaultBranch != "" {
		defBranch = info.DefaultBranch
	}
	out["repo"] = fullName
	out["repo_url"] = htmlURL
	out["default_branch"] = defBranch
	// 回填仓库名/默认分支，runForge 据此 clone；脚手架渲染需真实 repo/project_id
	if _, err := model.Pool.Exec(ctx, `UPDATE projects SET repo_full_name=$1, default_branch=$3 WHERE id=$2`, fullName, projectID, defBranch); err != nil {
		out["error"] = "backfill repo_full_name: " + err.Error()
		return out
	}

	files, err := tpl.Files(templates.Vars{Name: displayName, Slug: slug, Repo: fullName, ProjectID: projectID, Options: opts})
	if err != nil {
		out["error"] = "render: " + err.Error()
		return out
	}
	scaffold := make([]gitea.ScaffoldFile, 0, len(files))
	for _, f := range files {
		scaffold = append(scaffold, gitea.ScaffoldFile{Path: f.Path, Content: f.Content})
	}
	n, err := gc.CommitScaffold(ctx, owner, repo, defBranch, scaffold)
	if err != nil {
		out["error"] = fmt.Sprintf("scaffold(%d): %v", n, err)
		return out
	}
	out["scaffold_files"] = n
	// AGENTS.md 基线 sha（内容真相在仓；后续 push 由 webhook 自动刷新）
	if fc, e := gc.GetFileContents(ctx, owner, repo, "AGENTS.md", defBranch); e == nil && fc != nil {
		_, _ = model.Pool.Exec(ctx, `UPDATE projects SET agents_sha=$2, agents_updated_at=now() WHERE id=$1`, projectID, fc.SHA)
		out["agents_sha"] = fc.SHA
	}

	hookURL := strings.TrimSuffix(getenvLocal("GITEA_HOOK_URL", "http://api:8080/api/v1/webhooks/gitea"), "/")
	if err := gc.EnsureWebhook(ctx, owner, repo, hookURL, getenvLocal("GITEA_WEBHOOK_SECRET", "")); err != nil {
		out["error"] = "webhook: " + err.Error()
		return out
	}
	out["webhook"] = true
	provisionRepoSecrets(gc, ctx, owner, repo, out)
	return out
}

// provisionProject POST /projects/:id/provision —— 存量项目回填项目仓 build secrets
// （REGISTRY_USER/REGISTRY_TOKEN/FORJA_INTERNAL_TOKEN）；幂等覆盖。平台门 project.update，
// 项目内要求 Admin（requireProjectAdmin）。只读 env 取值，不落跟踪文件（AGENTS #1）。
func provisionProject(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	projectID := c.Params("id")
	if !idgen.IsValid(projectID) {
		return respond.InvalidParam(c, "id", "invalid project id")
	}
	if err := requireProjectAdmin(c, projectID); err != nil {
		return err
	}
	if err := guardNotArchived(c, projectID); err != nil {
		return err
	}
	ctx := context.Background()
	var repoFull string
	if err := model.Pool.QueryRow(ctx, `SELECT COALESCE(repo_full_name,'') FROM projects WHERE id=$1`, projectID).Scan(&repoFull); err != nil {
		return respond.NotFound(c, "project")
	}
	if repoFull == "" {
		return respond.BusinessRule(c, "NO_REPO", "project has no linked repo")
	}
	gc := gitea.NewFromEnv()
	if gc == nil {
		return respond.ServiceUnavailable(c)
	}
	out := fiber.Map{}
	provisionRepoSecrets(gc, ctx, fullName2owner(repoFull), fullName2repo(repoFull), out)
	n, _ := out["secrets"].(int)
	username, _ := c.Locals("username").(string)
	logAudit(ctx, projectID, username, "project.secrets_provisioned", "project", projectID,
		fmt.Sprintf(`{"repo":%q,"secrets":%d}`, repoFull, n))
	return respond.OK(c, fiber.Map{"repo": repoFull, "secrets": n})
}

// provisionRepoSecrets 注入项目仓构建 secrets（env 存在才写；缺失仅告警不阻塞——
// 构建触发时再补）。凭据只存 runner.envs/宿主 env，禁入跟踪文件（AGENTS #1）。
func provisionRepoSecrets(gc *gitea.Client, ctx context.Context, owner, repo string, out fiber.Map) {
	pairs := map[string]string{
		"REGISTRY_USER":        getenvLocal("FORJA_REGISTRY_USER", owner),
		"REGISTRY_TOKEN":       os.Getenv("FORJA_REGISTRY_TOKEN"),
		"FORJA_INTERNAL_TOKEN": os.Getenv("FORJA_INTERNAL_TOKEN"),
	}
	ok := 0
	for k, v := range pairs {
		if v == "" {
			continue
		}
		if err := gc.SetSecret(ctx, owner, repo, k, v); err != nil {
			slog.Warn("provision repo secret failed", "repo", owner+"/"+repo, "key", k, "err", err.Error())
			continue
		}
		ok++
	}
	out["secrets"] = ok
}

func fullName2owner(fn string) string {
	if i := strings.IndexByte(fn, '/'); i > 0 {
		return fn[:i]
	}
	return fn
}

func fullName2repo(fn string) string {
	if i := strings.IndexByte(fn, '/'); i >= 0 && i+1 < len(fn) {
		return fn[i+1:]
	}
	return fn
}

func getenvLocal(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func getProject(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	id := c.Params("id")
	if !idgen.IsValid(id) {
		return respond.InvalidParam(c, "id", "invalid project id")
	}
	var name string
	var descPtr, slugPtr *string
	var ownerID *string
	var createdAt interface{}
	var archivedAt *time.Time
	var repoFull, mode, template, defBranch string
	err := model.Pool.QueryRow(context.Background(),
		`SELECT p.name, p.description, p.slug, p.owner_id, p.created_at, p.archived_at,
		        COALESCE(p.repo_full_name,''), COALESCE(p.mode,'create'), COALESCE(p.template,'legacy'),
		        COALESCE(NULLIF(p.default_branch,''),'master')
		   FROM projects p WHERE p.id=$1`, id).Scan(&name, &descPtr, &slugPtr, &ownerID, &createdAt, &archivedAt,
		&repoFull, &mode, &template, &defBranch)
	if err != nil {
		return respond.NotFound(c, "project")
	}
	myRole, _ := c.Locals("role").(string)
	if myRole != "Admin" {
		uid := currentUserID(c)
		if uid == "" {
			return respond.NotFound(c, "project")
		}
		r := projectRoleOf(id, uid)
		if r == "" {
			return respond.Forbidden(c, "not a project member")
		}
		myRole = r
	}
	var memberCount int64
	_ = model.Pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM project_members WHERE project_id=$1`, id).Scan(&memberCount)

	resp := fiber.Map{
		"id": id, "name": name,
		"description": deref(descPtr), "slug": deref(slugPtr),
		"owner_id": deref(ownerID), "member_count": memberCount,
		"created_at": createdAt, "my_role": myRole,
		"archived": archivedAt != nil, // S1.4
		// v2.0 项目域基础契约/交付上下文
		"repo_full_name": repoFull, "default_branch": defBranch,
		"mode": mode, "template": template,
	}
	if archivedAt != nil {
		resp["archived_at"] = archivedAt.Format(time.RFC3339)
	}
	return respond.OK(c, resp)
}

func updateProject(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	id := c.Params("id")
	if !idgen.IsValid(id) {
		return respond.InvalidParam(c, "id", "invalid project id")
	}
	if err := requireProjectAdmin(c, id); err != nil {
		return err
	}
	if err := guardNotArchived(c, id); err != nil {
		return err
	}
	var body struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
	}
	if err := c.BodyParser(&body); err != nil {
		return respond.BadRequest(c, err.Error())
	}
	if body.Name != nil {
		*body.Name = strings.TrimSpace(*body.Name)
		if *body.Name == "" {
			return respond.InvalidParam(c, "name", "name required")
		}
	}
	sets := []string{}
	args := []interface{}{}
	if body.Name != nil {
		args = append(args, *body.Name)
		sets = append(sets, `name=$`+itoa(len(args)), `slug=$`+itoa(len(args)+1))
		args = append(args, slugify(*body.Name, id[:8]))
	}
	if body.Description != nil {
		args = append(args, *body.Description)
		sets = append(sets, `description=$`+itoa(len(args)))
	}
	if len(sets) == 0 {
		return respond.BadRequest(c, "nothing to update")
	}
	sets = append(sets, `updated_at=now()`)
	args = append(args, id)
	ct, err := model.Pool.Exec(context.Background(), `UPDATE projects SET `+strings.Join(sets, ", ")+` WHERE id=$`+itoa(len(args)), args...)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate") || strings.Contains(err.Error(), "unique") {
			return respond.Conflict(c, "project name or slug exists")
		}
		return respond.DBError(c, err)
	}
	if ct.RowsAffected() == 0 {
		return respond.NotFound(c, "project")
	}
	logAudit(context.Background(), id, c.Locals("username").(string), "project.update", "project", id, `{}`)
	return respond.OK(c, fiber.Map{"id": id})
}

// deleteProject S1.4 软删除（KI-002 决策 S1）：
//   - DELETE /projects/:id            → 归档（可恢复，幂等）
//   - DELETE /projects/:id?purge=true → 物理级联删除，仅限已归档项目
func deleteProject(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	id := c.Params("id")
	if !idgen.IsValid(id) {
		return respond.InvalidParam(c, "id", "invalid project id")
	}
	if c.Query("purge") == "true" {
		return purgeProject(c, id)
	}
	ct, err := model.Pool.Exec(context.Background(),
		`UPDATE projects SET archived_at=now(), updated_at=now() WHERE id=$1 AND archived_at IS NULL`, id)
	if err != nil {
		return respond.DBError(c, err)
	}
	if ct.RowsAffected() == 0 {
		var exists bool
		if err := model.Pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM projects WHERE id=$1)`, id).Scan(&exists); err != nil {
			return respond.DBError(c, err)
		}
		if !exists {
			return respond.NotFound(c, "project")
		}
		return respond.OK(c, fiber.Map{"id": id, "archived": true}) // 已归档，幂等返回
	}
	logAudit(context.Background(), "", c.Locals("username").(string), "project.archive", "project", id, `{}`)
	return respond.OK(c, fiber.Map{"id": id, "archived": true})
}

// restoreProject 恢复归档项目（S1.4）。权限：平台 project.update + 项目管理员。
func restoreProject(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	id := c.Params("id")
	if !idgen.IsValid(id) {
		return respond.InvalidParam(c, "id", "invalid project id")
	}
	if err := requireProjectAdmin(c, id); err != nil {
		return err
	}
	ct, err := model.Pool.Exec(context.Background(),
		`UPDATE projects SET archived_at=NULL, updated_at=now() WHERE id=$1 AND archived_at IS NOT NULL`, id)
	if err != nil {
		return respond.DBError(c, err)
	}
	if ct.RowsAffected() == 0 {
		var exists bool
		var archivedAt *time.Time
		if err := model.Pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM projects WHERE id=$1), (SELECT archived_at FROM projects WHERE id=$1)`, id).Scan(&exists, &archivedAt); err != nil {
			return respond.DBError(c, err)
		}
		if !exists {
			return respond.NotFound(c, "project")
		}
		return respond.OK(c, fiber.Map{"id": id, "archived": false}) // 未归档，幂等返回
	}
	logAudit(context.Background(), id, c.Locals("username").(string), "project.restore", "project", id, `{}`)
	return respond.OK(c, fiber.Map{"id": id, "archived": false})
}

// purgeProject 物理级联删除（不可恢复）。仅限已归档项目；Admin 专属（路由层 project.delete）。
func purgeProject(c *fiber.Ctx, id string) error {
	var archivedAt *time.Time
	err := model.Pool.QueryRow(context.Background(), `SELECT archived_at FROM projects WHERE id=$1`, id).Scan(&archivedAt)
	if err != nil {
		return respond.NotFound(c, "project")
	}
	if archivedAt == nil {
		return respond.Fail(c, fiber.StatusConflict, respond.CodeBusinessRule,
			"business rule rejected", "PROJECT_NOT_ARCHIVED",
			"only archived projects can be purged; archive it first")
	}
	// 审计留痕用计数（purge 后随级联消失）
	var specCount, taskCount int64
	_ = model.Pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM specs WHERE project_id=$1`, id).Scan(&specCount)
	_ = model.Pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM tasks WHERE project_id=$1`, id).Scan(&taskCount)
	// F0.5 生命周期联动：先收集外部资源引用（DB 级联删除后不可考）
	var repoFull string
	_ = model.Pool.QueryRow(context.Background(), `SELECT COALESCE(repo_full_name,'') FROM projects WHERE id=$1`, id).Scan(&repoFull)
	var storeKeys []string
	if rows, qerr := model.Pool.Query(context.Background(),
		`SELECT storage_key FROM inbox_attachments WHERE project_id=$1
		 UNION ALL SELECT storage_key FROM project_files WHERE project_id=$1
		 UNION ALL SELECT store_key FROM project_deliveries WHERE project_id=$1 AND store_key IS NOT NULL
		 UNION ALL SELECT store_key FROM project_artifacts WHERE project_id=$1 AND store_key IS NOT NULL`, id); qerr == nil {
		for rows.Next() {
			var k string
			if serr := rows.Scan(&k); serr == nil {
				storeKeys = append(storeKeys, k)
			}
		}
		rows.Close()
	}
	ct, err := model.Pool.Exec(context.Background(), `DELETE FROM projects WHERE id=$1`, id)
	if err != nil {
		return respond.DBError(c, err)
	}
	if ct.RowsAffected() == 0 {
		return respond.NotFound(c, "project")
	}
	// 外部清理 best-effort：失败仅告警，不回滚已删的 DB（幂等重试安全）
	if store, serr := storage.Get(); serr == nil {
		for _, k := range storeKeys {
			if derr := store.Delete(context.Background(), k); derr != nil {
				slog.Warn("project purge: delete object failed", "project_id", id, "key", k, "err", derr.Error())
			}
		}
	}
	if repoFull != "" {
		if gc := gitea.NewFromEnv(); gc != nil {
			if derr := gc.DeleteRepo(context.Background(), fullName2owner(repoFull), fullName2repo(repoFull)); derr != nil {
				slog.Warn("project purge: delete repo failed", "project_id", id, "repo", repoFull, "err", derr.Error())
			}
		}
	}
	detail := fmt.Sprintf(`{"purge":true,"specs":%d,"tasks":%d,"repo":%q,"objects":%d}`, specCount, taskCount, repoFull, len(storeKeys))
	logAudit(context.Background(), "", c.Locals("username").(string), "project.purge", "project", id, detail)
	return respond.OK(c, fiber.Map{"id": id, "purged": true})
}

func listMembers(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	id := c.Params("id")
	if !idgen.IsValid(id) {
		return respond.InvalidParam(c, "id", "invalid project id")
	}
	if err := requireProjectMember(c, id); err != nil {
		return err
	}
	rows, err := model.Pool.Query(context.Background(),
		`SELECT m.user_id, m.username, COALESCE(u.email,''), COALESCE(u.enabled,false), m.role, m.joined_at FROM project_members m LEFT JOIN users u ON u.id=m.user_id WHERE m.project_id=$1 ORDER BY m.joined_at`, id)
	if err != nil {
		return respond.DBError(c, err)
	}
	defer rows.Close()
	items := []fiber.Map{}
	for rows.Next() {
		var uid, username, email, role string
		var enabled bool
		var joined interface{}
		if err := rows.Scan(&uid, &username, &email, &enabled, &role, &joined); err != nil {
			return respond.DBError(c, err)
		}
		items = append(items, fiber.Map{"user_id": uid, "username": username, "email": email, "enabled": enabled, "role": role, "joined_at": joined})
	}
	if err := rows.Err(); err != nil {
		return respond.DBError(c, err)
	}
	return respond.OK(c, fiber.Map{"items": items})
}

func addMember(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	id := c.Params("id")
	if !idgen.IsValid(id) {
		return respond.InvalidParam(c, "id", "invalid project id")
	}
	if err := requireProjectAdmin(c, id); err != nil {
		return err
	}
	if err := guardNotArchived(c, id); err != nil {
		return err
	}
	var body struct {
		UserID string `json:"user_id"`
		Role   string `json:"role"`
	}
	if err := c.BodyParser(&body); err != nil {
		return respond.BadRequest(c, err.Error())
	}
	if !idgen.IsValid(body.UserID) {
		return respond.InvalidParam(c, "user_id", "valid user_id required")
	}
	if body.Role == "" {
		body.Role = "Developer"
	}
	if !validRoles[body.Role] {
		return respond.InvalidParam(c, "role", "invalid role")
	}
	var username string
	var enabled bool
	err := model.Pool.QueryRow(context.Background(), `SELECT username, enabled FROM users WHERE id=$1`, body.UserID).Scan(&username, &enabled)
	if err != nil {
		return respond.NotFound(c, "user")
	}
	if !enabled {
		return respond.BusinessRule(c, "USER_DISABLED", "user is disabled")
	}
	_, err = model.Pool.Exec(context.Background(), `INSERT INTO project_members (project_id, user_id, username, role) VALUES ($1,$2,$3,$4) ON CONFLICT (project_id, user_id) DO NOTHING`, id, body.UserID, username, body.Role)
	if err != nil {
		return respond.DBError(c, err)
	}
	logAudit(context.Background(), id, c.Locals("username").(string), "project.member.add", "project", id, `{"user":"`+username+`","role":"`+body.Role+`"}`)
	return respond.OK(c, fiber.Map{"project_id": id, "user_id": body.UserID, "username": username, "role": body.Role})
}

func updateMember(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	id := c.Params("id")
	uid := c.Params("userId")
	if !idgen.IsValid(id) {
		return respond.InvalidParam(c, "id", "invalid project id")
	}
	if !idgen.IsValid(uid) {
		return respond.InvalidParam(c, "userId", "invalid user id")
	}
	if err := requireProjectAdmin(c, id); err != nil {
		return err
	}
	if err := guardNotArchived(c, id); err != nil {
		return err
	}
	var body struct {
		Role string `json:"role"`
	}
	if err := c.BodyParser(&body); err != nil {
		return respond.BadRequest(c, err.Error())
	}
	if !validRoles[body.Role] {
		return respond.InvalidParam(c, "role", "invalid role")
	}
	// owner 角色不可降级
	var ownerID *string
	if err := model.Pool.QueryRow(context.Background(), `SELECT owner_id FROM projects WHERE id=$1`, id).Scan(&ownerID); err != nil {
		return respond.NotFound(c, "project")
	}
	if ownerID != nil && *ownerID == uid && body.Role != "Admin" {
		return respond.BusinessRule(c, "OWNER_ROLE_LOCKED", "cannot change owner role")
	}
	ct, err := model.Pool.Exec(context.Background(), `UPDATE project_members SET role=$3 WHERE project_id=$1 AND user_id=$2`, id, uid, body.Role)
	if err != nil {
		return respond.DBError(c, err)
	}
	if ct.RowsAffected() == 0 {
		return respond.NotFound(c, "member")
	}
	logAudit(context.Background(), id, c.Locals("username").(string), "project.member.update", "project", id, `{"role":"`+body.Role+`"}`)
	return respond.OK(c, fiber.Map{"project_id": id, "user_id": uid, "role": body.Role})
}

func removeMember(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	id := c.Params("id")
	uid := c.Params("userId")
	if !idgen.IsValid(id) {
		return respond.InvalidParam(c, "id", "invalid project id")
	}
	if !idgen.IsValid(uid) {
		return respond.InvalidParam(c, "userId", "invalid user id")
	}
	if err := requireProjectAdmin(c, id); err != nil {
		return err
	}
	if err := guardNotArchived(c, id); err != nil {
		return err
	}
	var ownerID *string
	if err := model.Pool.QueryRow(context.Background(), `SELECT owner_id FROM projects WHERE id=$1`, id).Scan(&ownerID); err != nil {
		return respond.NotFound(c, "project")
	}
	if ownerID != nil && *ownerID == uid {
		return respond.BusinessRule(c, "OWNER_UNREMOVABLE", "cannot remove project owner")
	}
	ct, err := model.Pool.Exec(context.Background(), `DELETE FROM project_members WHERE project_id=$1 AND user_id=$2`, id, uid)
	if err != nil {
		return respond.DBError(c, err)
	}
	if ct.RowsAffected() == 0 {
		return respond.NotFound(c, "member")
	}
	logAudit(context.Background(), id, c.Locals("username").(string), "project.member.remove", "project", id, `{}`)
	return respond.OK(c, fiber.Map{"project_id": id, "user_id": uid})
}

func itoa(n int) string {
	return strconv.Itoa(n)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
