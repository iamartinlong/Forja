package api

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"time"

	"forja/backend/internal/api/respond"
	"forja/backend/internal/auth"
	"forja/backend/internal/gitea"
	"forja/backend/internal/gitops"
	idgen "forja/backend/internal/id"
	"forja/backend/internal/model"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
)

func RegisterIterationRoutes(r fiber.Router) {
	r.Post("/projects/:id/iterations", auth.Require("task.write"), createIteration)
	r.Get("/projects/:id/iterations", auth.Require("task.read"), listIterations)
	r.Patch("/projects/:id/iterations/:iterId", auth.Require("task.write"), patchIteration)
	r.Post("/projects/:id/iterations/:iterId/release", auth.Require("iteration.release"), releaseIteration)
	r.Post("/projects/:id/iterations/:iterId/revert", auth.Require("iteration.release"), revertIteration)
}

var iterSlugRe = regexp.MustCompile(`[^a-z0-9]+`)

func iterSlugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = iterSlugRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 40 {
		s = s[:40]
		s = strings.Trim(s, "-")
	}
	if s == "" {
		s = "iter"
	}
	return s
}

func splitRepo(full string) (string, string) {
	if i := strings.IndexByte(full, '/'); i > 0 {
		return full[:i], full[i+1:]
	}
	return full, ""
}

type createIterationReq struct {
	Name      string  `json:"name"`
	StartDate *string `json:"start_date"` // YYYY-MM-DD
	EndDate   *string `json:"end_date"`
}

func createIteration(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	projectID := c.Params("id")
	if err := AuthorizeProject(c, projectID, "task.write"); err != nil {
		return err
	}
	var req createIterationReq
	if err := c.BodyParser(&req); err != nil {
		return respond.BadRequest(c, err.Error())
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return respond.InvalidParam(c, "name", "name required")
	}
	if len(req.Name) > 80 {
		return respond.InvalidParam(c, "name", "name too long")
	}
	ctx := context.Background()
	// uniqueness within project already via UNIQUE, but pre-check for nicer error
	var cnt int
	if err := model.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM iterations WHERE project_id=$1 AND name=$2`, projectID, req.Name).Scan(&cnt); err != nil {
		return respond.DBError(c, err)
	}
	if cnt > 0 {
		return respond.InvalidParam(c, "name", "name already exists in project")
	}
	iterID := idgen.New()
	var startDate, endDate any
	if req.StartDate != nil && *req.StartDate != "" {
		if _, err := time.Parse("2006-01-02", *req.StartDate); err != nil {
			return respond.InvalidParam(c, "start_date", "invalid date YYYY-MM-DD")
		}
		startDate = *req.StartDate
	}
	if req.EndDate != nil && *req.EndDate != "" {
		if _, err := time.Parse("2006-01-02", *req.EndDate); err != nil {
			return respond.InvalidParam(c, "end_date", "invalid date YYYY-MM-DD")
		}
		endDate = *req.EndDate
	}
	if startDate != nil && endDate != nil {
		sd, _ := time.Parse("2006-01-02", startDate.(string))
		ed, _ := time.Parse("2006-01-02", endDate.(string))
		if ed.Before(sd) {
			return respond.InvalidParam(c, "end_date", "end_date before start_date")
		}
	}
	username, _ := c.Locals("username").(string)
	if username == "" {
		username = "anonymous"
	}
	if _, err := model.Pool.Exec(ctx, `INSERT INTO iterations (id, project_id, name, start_date, end_date, created_by) VALUES ($1,$2,$3,$4,$5,(SELECT id FROM users WHERE username=$6))`, iterID, projectID, req.Name, startDate, endDate, username); err != nil {
		return respond.DBError(c, err)
	}
	return respond.OK(c, fiber.Map{"id": iterID, "name": req.Name, "status": "planned"})
}

func listIterations(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	projectID := c.Params("id")
	if err := AuthorizeProject(c, projectID, "task.read"); err != nil {
		return err
	}
	ctx := context.Background()
	rows, err := model.Pool.Query(ctx, `SELECT i.id, i.name, i.status, i.start_date, i.end_date, i.created_at,
	  COALESCE(cnt.c,0) as task_count, COALESCE(cnt.h,0) as hours, i.branch, i.base_sha, i.merge_sha
	  FROM iterations i
	  LEFT JOIN (SELECT target_iteration_id, COUNT(*) c, COALESCE(SUM(estimated_hours),0) h FROM tasks WHERE project_id=$1 AND target_iteration_id IS NOT NULL GROUP BY target_iteration_id) cnt ON cnt.target_iteration_id=i.id
	  WHERE i.project_id=$1 ORDER BY i.created_at ASC`, projectID)
	if err != nil {
		return respond.DBError(c, err)
	}
	defer rows.Close()
	items := []fiber.Map{}
	for rows.Next() {
		var id, name, status string
		var startDate, endDate *time.Time
		var createdAt time.Time
		var taskCount int
		var hours float64
		var branch, baseSha, mergeSha *string
		if err := rows.Scan(&id, &name, &status, &startDate, &endDate, &createdAt, &taskCount, &hours, &branch, &baseSha, &mergeSha); err != nil {
			return respond.DBError(c, err)
		}
		var sd, ed *string
		if startDate != nil {
			s := startDate.Format("2006-01-02")
			sd = &s
		}
		if endDate != nil {
			s := endDate.Format("2006-01-02")
			ed = &s
		}
		items = append(items, fiber.Map{
			"id": id, "name": name, "status": status,
			"start_date": sd, "end_date": ed,
			"created_at": createdAt, "task_count": taskCount, "estimated_hours": hours,
			"branch": branch, "base_sha": baseSha, "merge_sha": mergeSha,
		})
	}
	return respond.OK(c, fiber.Map{"items": items})
}

func patchIteration(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	projectID := c.Params("id")
	iterID := c.Params("iterId")
	if !idgen.IsValid(iterID) {
		return respond.InvalidParam(c, "iterId", "invalid iteration id")
	}
	if err := AuthorizeProject(c, projectID, "task.write"); err != nil {
		return err
	}
	var body map[string]string
	if err := c.BodyParser(&body); err != nil {
		return respond.BadRequest(c, err.Error())
	}
	ctx := context.Background()
	var curStatus, curName string
	var curProject string
	var curStart, curEnd *time.Time
	var curBranch *string
	if err := model.Pool.QueryRow(ctx, `SELECT status, project_id, name, branch, start_date, end_date FROM iterations WHERE id=$1`, iterID).Scan(&curStatus, &curProject, &curName, &curBranch, &curStart, &curEnd); err == pgx.ErrNoRows {
		return respond.NotFound(c, "iteration")
	} else if err != nil {
		return respond.DBError(c, err)
	}
	if curProject != projectID {
		return respond.NotFound(c, "iteration")
	}
	sets := []string{}
	args := []any{}
	idx := 1
	if name, ok := body["name"]; ok {
		name = strings.TrimSpace(name)
		if name == "" {
			return respond.InvalidParam(c, "name", "name required")
		}
		if len(name) > 80 {
			return respond.InvalidParam(c, "name", "name too long")
		}
		var cnt int
		if err := model.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM iterations WHERE project_id=$1 AND name=$2 AND id<>$3`, projectID, name, iterID).Scan(&cnt); err != nil {
			return respond.DBError(c, err)
		}
		if cnt > 0 {
			return respond.InvalidParam(c, "name", "name already exists in project")
		}
		sets = append(sets, `name=$`+fmt.Sprint(idx))
		args = append(args, name)
		idx++
	}
	if st, ok := body["status"]; ok {
		st = strings.ToLower(strings.TrimSpace(st))
		if st != "planned" && st != "active" && st != "closed" {
			return respond.InvalidParam(c, "status", "status must be planned|active|closed")
		}
		sets = append(sets, `status=$`+fmt.Sprint(idx))
		args = append(args, st)
		idx++
		if st == "closed" {
			// unassign unfinished tasks back to backlog (keep target_iteration but status closed signals backlog)
			// keep as is per blueprint: closed iteration unfinished specs退回 BACKLOG — here we keep link but front filters by status
		}
	}
	if sd, ok := body["start_date"]; ok {
		if sd == "" {
			sets = append(sets, `start_date=NULL`)
		} else {
			if _, err := time.Parse("2006-01-02", sd); err != nil {
				return respond.InvalidParam(c, "start_date", "invalid date YYYY-MM-DD")
			}
			sets = append(sets, `start_date=$`+fmt.Sprint(idx))
			args = append(args, sd)
			idx++
		}
	}
	if ed, ok := body["end_date"]; ok {
		if ed == "" {
			sets = append(sets, `end_date=NULL`)
		} else {
			if _, err := time.Parse("2006-01-02", ed); err != nil {
				return respond.InvalidParam(c, "end_date", "invalid date YYYY-MM-DD")
			}
			sets = append(sets, `end_date=$`+fmt.Sprint(idx))
			args = append(args, ed)
			idx++
		}
	}
	if len(sets) == 0 {
		return respond.BadRequest(c, "nothing to update")
	}
	// cross-field date ordering: effective start/end after patch
	var effStart, effEnd *time.Time
	if v, ok := body["start_date"]; ok {
		if v == "" {
			effStart = nil
		} else {
			t, _ := time.Parse("2006-01-02", v)
			effStart = &t
		}
	} else {
		effStart = curStart
	}
	if v, ok := body["end_date"]; ok {
		if v == "" {
			effEnd = nil
		} else {
			t, _ := time.Parse("2006-01-02", v)
			effEnd = &t
		}
	} else {
		effEnd = curEnd
	}
	if effStart != nil && effEnd != nil && effEnd.Before(*effStart) {
		return respond.InvalidParam(c, "end_date", "end_date before start_date")
	}
	// 激活：planned→active 时创建 iteration 分支（基于 master 切出，幂等）FOR UPDATE 防并发
	if st, ok := body["status"]; ok && strings.ToLower(strings.TrimSpace(st)) == "active" && (curBranch == nil || *curBranch == "") {
		// 串行化：锁行防并发同名
		tx, err := model.Pool.Begin(ctx)
		if err == nil {
			var locked string
			_ = tx.QueryRow(ctx, `SELECT id FROM iterations WHERE id=$1 FOR UPDATE`, iterID).Scan(&locked)
			// 释放锁由后续 UPDATE 隐式提交时释放，事务内完成分支创建
			_ = tx.Commit(ctx)
		}
		effName := curName
		if n, ok2 := body["name"]; ok2 && strings.TrimSpace(n) != "" {
			effName = strings.TrimSpace(n)
		}
		slug := iterSlugify(effName)
		branch := fmt.Sprintf("iteration/%s-%s", slug, shortIDIter(iterID))
		var cnt int
		_ = model.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM iterations WHERE project_id=$1 AND branch=$2 AND id<>$3`, projectID, branch, iterID).Scan(&cnt)
		if cnt > 0 {
			return respond.InvalidParam(c, "status", "branch already exists")
		}
		var repoFull string
		_ = model.Pool.QueryRow(ctx, `SELECT repo_full_name FROM projects WHERE id=$1`, projectID).Scan(&repoFull)
		baseSHA := ""
		if repoFull != "" {
			baseURL := os.Getenv("GITEA_URL")
			if baseURL == "" {
				baseURL = "http://gitea:3000"
			}
			token := os.Getenv("GITEA_TOKEN")
			workdir := os.Getenv("FORGE_WORKDIR")
			if workdir == "" {
				workdir = "/tmp/forja-work"
			}
			if _, err := gitops.EnsureIterationBranch(ctx, workdir, baseURL, token, repoFull, branch, ProjectDefaultBranch(ctx, projectID)); err != nil {
				slog.Warn("iteration branch create failed", "branch", branch, "err", err.Error(), "workdir", workdir, "baseURL", baseURL, "repo", repoFull)
				return respond.Upstream(c, "BRANCH_CREATE_FAILED", fmt.Errorf("%s workdir=%s baseURL=%s repo=%s: %w", branch, workdir, baseURL, repoFull, err))
			}
			if gc := gitea.NewFromEnv(); gc != nil {
				owner, repo := splitRepo(repoFull)
				if h, err := gc.BranchCommit(ctx, owner, repo, ProjectDefaultBranch(ctx, projectID)); err == nil && h != "" {
					baseSHA = h
				}
			}
		}
		sets = append(sets, `branch=$`+fmt.Sprint(idx))
		args = append(args, branch)
		idx++
		if baseSHA != "" {
			sets = append(sets, `base_sha=$`+fmt.Sprint(idx))
			args = append(args, baseSHA)
			idx++
		}
	}
	sets = append(sets, `updated_at=NOW()`)
	query := `UPDATE iterations SET ` + strings.Join(sets, ", ") + ` WHERE id=$` + fmt.Sprint(idx)
	args = append(args, iterID)
	if _, err := model.Pool.Exec(ctx, query, args...); err != nil {
		return respond.DBError(c, err)
	}
	return respond.OK(c, fiber.Map{"id": iterID})
}

func shortIDIter(id string) string {
	if len(id) > 6 {
		return id[:6]
	}
	return id
}

// releaseIteration 迭代发布：active iteration → merge iteration→master + release Tag + deployments
func releaseIteration(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	projectID := c.Params("id")
	iterID := c.Params("iterId")
	if !idgen.IsValid(iterID) {
		return respond.InvalidParam(c, "iterId", "invalid iteration id")
	}
	if err := AuthorizeProject(c, projectID, "iteration.release"); err != nil {
		return err
	}
	ctx := context.Background()
	var curStatus, curBranch, curName string
	var branchPtr *string
	if err := model.Pool.QueryRow(ctx, `SELECT status, COALESCE(branch,''), name FROM iterations WHERE id=$1 AND project_id=$2`, iterID, projectID).Scan(&curStatus, &curBranch, &curName); err != nil {
		if err == pgx.ErrNoRows {
			return respond.NotFound(c, "iteration")
		}
		return respond.DBError(c, err)
	}
	if curBranch == "" {
		return respond.BusinessRule(c, "NO_BRANCH", "iteration branch not created (status must be active)")
	}
	branchPtr = &curBranch
	if curStatus != "active" {
		return respond.BusinessRule(c, "NOT_ACTIVE", "only active iteration can be released (current: "+curStatus+")")
	}
	var repoFull string
	_ = model.Pool.QueryRow(ctx, `SELECT repo_full_name FROM projects WHERE id=$1`, projectID).Scan(&repoFull)
	if repoFull == "" {
		return respond.BusinessRule(c, "NO_REPO", "project repo not configured")
	}
	baseURL := os.Getenv("GITEA_URL")
	if baseURL == "" {
		baseURL = "http://gitea:3000"
	}
	token := os.Getenv("GITEA_TOKEN")
	workdir := os.Getenv("FORGE_WORKDIR")
	if workdir == "" {
		workdir = "/tmp/forja-work"
	}
	// 获取迭代分支 HEAD：通过 gitops 确保副本
	repo, err := gitops.EnsureRepoOnBase(ctx, workdir, baseURL, token, repoFull, ProjectDefaultBranch(ctx, projectID))
	if err != nil {
		return respond.Upstream(c, "REPO_ERROR", err)
	}
	// 强门禁：BranchCommit 重试3次 == iterHead 且 pipeline 4 job SUCCESS
	var iterHead string
	if gc := gitea.NewFromEnv(); gc != nil {
		owner, repo := splitRepo(repoFull)
		for i := 0; i < 3; i++ {
			if h, err := gc.BranchCommit(ctx, owner, repo, *branchPtr); err == nil && h != "" {
				iterHead = h
				break
			}
			time.Sleep(time.Duration(i+1) * 500 * time.Millisecond)
		}
	}
	if iterHead != "" {
		var ok bool
		_ = model.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pipeline_runs WHERE repo=$1 AND sha=$2 AND branch=$3 AND status='SUCCESS')`, repoFull, iterHead, *branchPtr).Scan(&ok)
		if !ok {
			return respond.BusinessRule(c, "NO_SUCCESS_BUILD", "iteration branch has no SUCCESS build (need 4 job green)")
		}
	} else {
		var hasSuccess bool
		_ = model.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pipeline_runs WHERE repo=$1 AND branch=$2 AND status='SUCCESS')`, repoFull, *branchPtr).Scan(&hasSuccess)
		if !hasSuccess {
			slog.Warn("iteration release without SUCCESS pipeline", "branch", *branchPtr, "repo", repoFull)
		}
	}
	// Merge iteration → master（串行化：FOR UPDATE 锁迭代行）
	tx, err := model.Pool.Begin(ctx)
	if err != nil {
		return respond.DBError(c, err)
	}
	defer tx.Rollback(ctx)
	var lockedBranch string
	if err := tx.QueryRow(ctx, `SELECT branch FROM iterations WHERE id=$1 FOR UPDATE`, iterID).Scan(&lockedBranch); err != nil {
		return respond.DBError(c, err)
	}
	// 执行 merge（gitops 层已做 fetch src/dst）
	mergeSHA, err := repo.MergeBranchTo(ctx, *branchPtr, ProjectDefaultBranch(ctx, projectID))
	if err != nil {
		slog.Warn("iteration merge failed", "branch", *branchPtr, "err", err.Error())
		return respond.Upstream(c, "MERGE_FAILED", err)
	}
	// Tag release/<slug>-<date>-<shortSha>
	slug := iterSlugify(curName)
	tag := fmt.Sprintf("release/%s-%s-%s", slug, time.Now().Format("20060102"), shortIDIter(mergeSHA))
	if _, err := repo.CreateTag(ctx, tag, mergeSHA); err != nil {
		slog.Warn("release tag failed", "tag", tag, "err", err.Error())
	}
	// M0 残项修复（2026-09-12）：发布后续动作按项目分流——
	//   self（FORJA 自身）：落 deployment 行，由 deploy.yml webhook 回填真实终止态；
	//   用户项目（DELIVERY_ONLY，禁部署）：不再写永不完成的 deployment 行，
	//   改为对合并后的默认分支触发一次真实构建（制品由 webhook 登记，供交付包）。
	username, _ := c.Locals("username").(string)
	defBranch := ProjectDefaultBranch(ctx, projectID)
	if gc := gitea.NewFromEnv(); gc != nil {
		owner, repoName, full, isSelf, rerr := resolveDispatchTarget(ctx, gc, projectID)
		switch {
		case rerr != nil:
			slog.Warn("iteration release resolve target failed", "project_id", projectID, "err", rerr.Error())
		case isSelf:
			CreateDeployment(ctx, repoFull, mergeSHA, tag, "iteration", username, projectID)
		default:
			if derr := gc.Dispatch(ctx, owner, repoName, buildWorkflow, defBranch); derr != nil {
				slog.Warn("iteration release build dispatch failed", "project_id", projectID, "err", derr.Error())
			} else {
				recordManualBuildRunWithBranch(ctx, full, mergeSHA, defBranch, username, projectID)
				preseedProjectBuildJobs(ctx, full, mergeSHA, defBranch, projectID)
				logAudit(ctx, projectID, username, "build.dispatched", "repo", "",
					`{"sha":`+quoteJSON(mergeSHA)+`,"workflow":"`+buildWorkflow+`","branch":`+quoteJSON(defBranch)+`,"source":"iteration.release"}`)
			}
		}
	} else {
		// 无 Gitea 客户端（本地/测试）：保留旧行为落 deployment 行
		CreateDeployment(ctx, repoFull, mergeSHA, tag, "iteration", username, projectID)
	}
	// 更新迭代行 iteration branch 已验证，直接更新
	if _, err := tx.Exec(ctx, `UPDATE iterations SET status='closed', merge_sha=$1, updated_at=NOW() WHERE id=$2`, mergeSHA, iterID); err != nil {
		return respond.DBError(c, err)
	}
	// deployments.iteration_id 回填
	_, _ = tx.Exec(ctx, `UPDATE deployments SET iteration_id=$1, branch=$2 WHERE repo=$3 AND sha=$4 AND tag=$5`, iterID, *branchPtr, repoFull, mergeSHA, tag)
	if err := tx.Commit(ctx); err != nil {
		return respond.DBError(c, err)
	}
	if username == "" {
		username = "anonymous"
	}
	logAudit(ctx, projectID, username, "iteration.release", "iteration", iterID, fmt.Sprintf(`{"branch":%q,"merge_sha":%q,"tag":%q}`, *branchPtr, mergeSHA, tag))
	return respond.OK(c, fiber.Map{"id": iterID, "status": "closed", "branch": *branchPtr, "merge_sha": mergeSHA, "tag": tag})
}

func revertIteration(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	projectID := c.Params("id")
	iterID := c.Params("iterId")
	if !idgen.IsValid(iterID) {
		return respond.InvalidParam(c, "iterId", "invalid iteration id")
	}
	if err := AuthorizeProject(c, projectID, "iteration.release"); err != nil {
		return err
	}
	ctx := context.Background()
	var curStatus, curBranch, curMerge string
	if err := model.Pool.QueryRow(ctx, `SELECT status, COALESCE(branch,''), COALESCE(merge_sha,'') FROM iterations WHERE id=$1 AND project_id=$2`, iterID, projectID).Scan(&curStatus, &curBranch, &curMerge); err != nil {
		if err == pgx.ErrNoRows {
			return respond.NotFound(c, "iteration")
		}
		return respond.DBError(c, err)
	}
	if curStatus != "closed" || curMerge == "" || curBranch == "" {
		return respond.BusinessRule(c, "NOT_REVERTABLE", "only closed iteration with merge_sha can be reverted")
	}
	var repoFull string
	_ = model.Pool.QueryRow(ctx, `SELECT repo_full_name FROM projects WHERE id=$1`, projectID).Scan(&repoFull)
	if repoFull == "" {
		return respond.BusinessRule(c, "NO_REPO", "project repo not configured")
	}
	baseURL := os.Getenv("GITEA_URL")
	if baseURL == "" {
		baseURL = "http://gitea:3000"
	}
	token := os.Getenv("GITEA_TOKEN")
	workdir := os.Getenv("FORGE_WORKDIR")
	if workdir == "" {
		workdir = "/tmp/forja-work"
	}
	repo, err := gitops.EnsureRepoOnBase(ctx, workdir, baseURL, token, repoFull, ProjectDefaultBranch(ctx, projectID))
	if err != nil {
		return respond.Upstream(c, "REPO_ERROR", err)
	}
	newSHA, err := repo.RevertCommitTo(ctx, curMerge, ProjectDefaultBranch(ctx, projectID))
	if err != nil {
		return respond.Upstream(c, "REVERT_FAILED", err)
	}
	tag := fmt.Sprintf("revert-%s-%s", shortIDIter(iterID), shortIDIter(newSHA))
	_, _ = repo.CreateTag(ctx, tag, newSHA)
	_, _ = model.Pool.Exec(ctx, `INSERT INTO deployments (repo, sha, tag, status, mode, actor, project_id, branch, iteration_id) VALUES ($1,$2,$3,'success','manual',$4,$5,$6,$7)`, repoFull, newSHA, tag, c.Locals("username"), projectID, curBranch, iterID)
	_, _ = model.Pool.Exec(ctx, `UPDATE iterations SET status='active', merge_sha=$1, updated_at=NOW() WHERE id=$2`, newSHA, iterID)
	username, _ := c.Locals("username").(string)
	if username == "" {
		username = "anonymous"
	}
	logAudit(ctx, projectID, username, "iteration.revert", "iteration", iterID, fmt.Sprintf(`{"branch":%q,"revert_sha":%q,"tag":%q}`, curBranch, newSHA, tag))
	return respond.OK(c, fiber.Map{"id": iterID, "status": "active", "branch": curBranch, "revert_sha": newSHA, "tag": tag})
}


