package api

// F1.b 项目规范（AGENTS.md）读写：仓内为唯一真相（Git 版本化），
// FORJA 只做读写通道 + projects.agents_sha 缓存；锻造注入见 agent/context。

import (
	"context"
	"encoding/base64"
	"log/slog"
	"strings"

	"forja/backend/internal/api/respond"
	"forja/backend/internal/gitea"
	idgen "forja/backend/internal/id"
	"forja/backend/internal/model"
	"forja/backend/internal/templates"

	"github.com/gofiber/fiber/v2"
)

// listProjectTemplates 可用脚手架模板（建项 Dialog 用；v2.1 带 source/version；v2.2 带队 options 问卷）。
func listProjectTemplates(c *fiber.Ctx) error {
	items := []fiber.Map{}
	for _, t := range templates.List() {
		opts := t.Options
		if opts == nil {
			opts = []templates.Option{}
		}
		items = append(items, fiber.Map{
			"id": t.ID, "name": t.Name, "description": t.Description,
			"version": t.Version, "source": t.Source,
			"stack": t.Stack, "files": t.Paths(),
			"options": opts,
		})
	}
	return respond.OK(c, fiber.Map{"items": items})
}

// projectRepoRef 项目仓库引用（repo_full_name + 默认分支）。ok=false 表示项目不存在。
func projectRepoRef(ctx context.Context, projectID string) (repoFull, defBranch string, ok bool) {
	var db *string
	if err := model.Pool.QueryRow(ctx,
		`SELECT COALESCE(repo_full_name,''), default_branch FROM projects WHERE id=$1`, projectID).Scan(&repoFull, &db); err != nil {
		return "", "", false
	}
	if db != nil && *db != "" {
		defBranch = *db
	} else {
		defBranch = "master"
	}
	return repoFull, defBranch, true
}

// getProjectAgents 读取仓内 AGENTS.md（exists=false 表示缺失）。
func getProjectAgents(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	id := c.Params("id")
	if !idgen.IsValid(id) {
		return respond.InvalidParam(c, "id", "invalid project id")
	}
	if err := AuthorizeProject(c, id, "task.read"); err != nil {
		return err
	}
	repoFull, defBranch, ok := projectRepoRef(c.Context(), id)
	if !ok {
		return respond.NotFound(c, "project")
	}
	if repoFull == "" {
		return respond.BusinessRule(c, "NO_REPO", "project has no linked repo")
	}
	gc := gitea.NewFromEnv()
	if gc == nil {
		return respond.ServiceUnavailable(c)
	}
	owner, repo := fullName2owner(repoFull), fullName2repo(repoFull)
	fc, err := gc.GetFileContents(c.Context(), owner, repo, "AGENTS.md", defBranch)
	if err != nil {
		return respond.Upstream(c, "gitea", err)
	}
	if fc == nil {
		return respond.OK(c, fiber.Map{"exists": false, "content": "", "sha": "", "path": "AGENTS.md"})
	}
	raw, derr := base64.StdEncoding.DecodeString(strings.TrimSpace(fc.Content))
	if derr != nil {
		return respond.Upstream(c, "gitea", derr)
	}
	return respond.OK(c, fiber.Map{"exists": true, "content": string(raw), "sha": fc.SHA, "path": "AGENTS.md"})
}

// putProjectAgents 保存规范（乐观并发：带 sha 时校验一致，冲突 409 FILE_CONFLICT）。
func putProjectAgents(c *fiber.Ctx) error {
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
		Content string `json:"content"`
		SHA     string `json:"sha"`
	}
	if err := c.BodyParser(&body); err != nil {
		return respond.BadRequest(c, err.Error())
	}
	if strings.TrimSpace(body.Content) == "" {
		return respond.InvalidParam(c, "content", "content required")
	}
	if len(body.Content) > 64*1024 {
		return respond.InvalidParam(c, "content", "content too large (max 64KB)")
	}
	repoFull, defBranch, ok := projectRepoRef(c.Context(), id)
	if !ok {
		return respond.NotFound(c, "project")
	}
	if repoFull == "" {
		return respond.BusinessRule(c, "NO_REPO", "project has no linked repo")
	}
	gc := gitea.NewFromEnv()
	if gc == nil {
		return respond.ServiceUnavailable(c)
	}
	owner, repo := fullName2owner(repoFull), fullName2repo(repoFull)
	if body.SHA != "" {
		fc, err := gc.GetFileContents(c.Context(), owner, repo, "AGENTS.md", defBranch)
		if err != nil {
			return respond.Upstream(c, "gitea", err)
		}
		if fc == nil || fc.SHA != body.SHA {
			return respond.BusinessRule(c, "FILE_CONFLICT", "AGENTS.md changed since load; reload and merge")
		}
	}
	newSHA, err := gc.UpsertFile(c.Context(), owner, repo, defBranch, "AGENTS.md", body.Content,
		"docs(agents): update project conventions")
	if err != nil {
		return respond.Upstream(c, "gitea", err)
	}
	_, _ = model.Pool.Exec(c.Context(),
		`UPDATE projects SET agents_sha=$2, agents_updated_at=now() WHERE id=$1`, id, newSHA)
	username, _ := c.Locals("username").(string)
	logAudit(c.Context(), id, username, "project.agents.update", "project", id, `{"sha":`+quoteJSON(newSHA)+`}`)
	return respond.OK(c, fiber.Map{"sha": newSHA, "path": "AGENTS.md"})
}

// generateProjectAgents 生成初版规范（仅当仓内缺失；已存在时 409 FILE_EXISTS，防覆盖）。
func generateProjectAgents(c *fiber.Ctx) error {
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
	var name string
	if err := model.Pool.QueryRow(c.Context(), `SELECT name FROM projects WHERE id=$1`, id).Scan(&name); err != nil {
		return respond.NotFound(c, "project")
	}
	repoFull, defBranch, ok := projectRepoRef(c.Context(), id)
	if !ok {
		return respond.NotFound(c, "project")
	}
	if repoFull == "" {
		return respond.BusinessRule(c, "NO_REPO", "project has no linked repo")
	}
	gc := gitea.NewFromEnv()
	if gc == nil {
		return respond.ServiceUnavailable(c)
	}
	owner, repo := fullName2owner(repoFull), fullName2repo(repoFull)
	existing, err := gc.GetFileContents(c.Context(), owner, repo, "AGENTS.md", defBranch)
	if err != nil {
		return respond.Upstream(c, "gitea", err)
	}
	if existing != nil {
		return respond.BusinessRule(c, "FILE_EXISTS", "AGENTS.md already exists; edit it instead")
	}
	content := templates.RenderStandardAgents(name)
	newSHA, err := gc.UpsertFile(c.Context(), owner, repo, defBranch, "AGENTS.md", content,
		"docs(agents): generate project conventions from template")
	if err != nil {
		return respond.Upstream(c, "gitea", err)
	}
	_, _ = model.Pool.Exec(c.Context(),
		`UPDATE projects SET agents_sha=$2, agents_updated_at=now() WHERE id=$1`, id, newSHA)
	username, _ := c.Locals("username").(string)
	logAudit(c.Context(), id, username, "project.agents.generate", "project", id, `{"sha":`+quoteJSON(newSHA)+`}`)
	slog.Info("project agents generated", "project_id", id, "sha", newSHA)
	return respond.OK(c, fiber.Map{"sha": newSHA, "path": "AGENTS.md"})
}
