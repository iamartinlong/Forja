package api

// M1.2 项目构建通道：手动触发项目仓 build.yml（master HEAD 锚定），
// 制品登记由 webhook workflow_job(build, success) 回填 project_artifacts。

import (
	"context"

	"forja/backend/internal/api/respond"
	"forja/backend/internal/gitea"
	idgen "forja/backend/internal/id"
	"forja/backend/internal/model"

	"github.com/gofiber/fiber/v2"
)

// triggerProjectBuild POST /projects/:id/builds —— 服务端解析默认分支 HEAD 并 dispatch build.yml。
func triggerProjectBuild(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	projectID := c.Params("id")
	if !idgen.IsValid(projectID) {
		return respond.InvalidParam(c, "id", "invalid project id")
	}
	if err := AuthorizeProject(c, projectID, "task.run"); err != nil {
		return err
	}
	if err := guardNotArchived(c, projectID); err != nil {
		return err
	}
	ctx := context.Background()
	var repoFull, defBranch string
	if err := model.Pool.QueryRow(ctx,
		`SELECT COALESCE(repo_full_name,''), COALESCE(NULLIF(default_branch,''),'master') FROM projects WHERE id=$1`,
		projectID).Scan(&repoFull, &defBranch); err != nil {
		return respond.NotFound(c, "project")
	}
	if repoFull == "" {
		return respond.BusinessRule(c, "NO_REPO", "project has no linked repo")
	}
	client := gitea.NewFromEnv()
	if client == nil {
		return respond.ServiceUnavailable(c)
	}
	owner, repo := fullName2owner(repoFull), fullName2repo(repoFull)
	sha, err := client.BranchCommit(ctx, owner, repo, defBranch)
	if err != nil {
		return respond.Upstream(c, "gitea", err)
	}
	var dup bool
	_ = model.Pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM pipeline_runs WHERE repo=$1 AND sha=$2 AND status='RUNNING')`, repoFull, sha).Scan(&dup)
	if dup {
		return respond.Conflict(c, "build already running for this sha")
	}
	if err := client.Dispatch(ctx, owner, repo, buildWorkflow, defBranch); err != nil {
		return respond.Upstream(c, "gitea", err)
	}
	username, _ := c.Locals("username").(string)
	recordManualBuildRunWithBranch(ctx, repoFull, sha, defBranch, username, projectID)
	// 项目 build.yml 仅一个 job `build`：不能用 FORJA 四 job 预播（聚合会永停 RUNNING）
	preseedProjectBuildJobs(ctx, repoFull, sha, defBranch, projectID)
	logAudit(ctx, projectID, username, "build.dispatched", "repo", "",
		`{"sha":`+quoteJSON(sha)+`,"workflow":"`+buildWorkflow+`","branch":`+quoteJSON(defBranch)+`,"source":"project"}`)
	return respond.OK(c, fiber.Map{"queued": true, "sha": sha, "branch": defBranch})
}

// listProjectArtifacts GET /projects/:id/artifacts —— 制品台账（最新在前，M1.3/前端交付页消费）。
func listProjectArtifacts(c *fiber.Ctx) error {
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
	if err := model.Pool.QueryRow(c.Context(), `SELECT COUNT(*) FROM project_artifacts WHERE project_id=$1`, projectID).Scan(&total); err != nil {
		return respond.DBError(c, err)
	}
	rows, err := model.Pool.Query(c.Context(), `
		SELECT sha, tag, image, COALESCE(digest,''), COALESCE(size_bytes,0), status, store_key IS NOT NULL, created_at
		  FROM project_artifacts WHERE project_id=$1
		 ORDER BY created_at DESC LIMIT $2 OFFSET $3`, projectID, pageSize, (page-1)*pageSize)
	if err != nil {
		return respond.DBError(c, err)
	}
	defer rows.Close()
	items := []fiber.Map{}
	for rows.Next() {
		var sha, tag, image, digest, status string
		var size int64
		var hasTar bool
		var createdAt any
		if err := rows.Scan(&sha, &tag, &image, &digest, &size, &status, &hasTar, &createdAt); err != nil {
			return respond.DBError(c, err)
		}
		items = append(items, fiber.Map{
			"sha": sha, "tag": tag, "image": image, "digest": digest,
			"size_bytes": size, "status": status, "has_tar": hasTar, "created_at": createdAt,
		})
	}
	return respond.List(c, items, respond.PageMeta{Page: page, PageSize: pageSize, Total: total})
}
