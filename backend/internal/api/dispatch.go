package api

// S6 构建部署分离（S6-build-deploy-separation.md §3.1/§9）：
//   POST /internal/gitea/dispatch  Admin 代理触发构建/部署
//     kind=build            → 校验 sha==master HEAD（Gitea dispatch ref 不接受 commit
//                             sha，实测 404）→ dispatch build.yml(ref=master)
//     kind=deploy           → 写 Variable DEPLOY_TAG → dispatch deploy.yml(ref=master)
//     kind=deploy + delay>0 → asynq 定时任务，worker 到点执行同一链路
//
// 全程审计留痕（actor=操作者）；deploy 前置校验该 sha 存在 SUCCESS 构建（无制品不部署）。

import (
	"context"
	"encoding/hex"
	"log/slog"
	"os"
	"strings"
	"time"

	"forja/backend/internal/api/respond"
	"forja/backend/internal/auth"
	"forja/backend/internal/gitea"
	"forja/backend/internal/model"
	"forja/backend/internal/queue"

	"github.com/gofiber/fiber/v2"
	idgen "forja/backend/internal/id"
)

const (
	deployWorkflow = "deploy.yml"
	buildWorkflow  = "build.yml"
	deployVarKey   = "DEPLOY_TAG"
)

func RegisterDispatchRoutes(r fiber.Router) {
	r.Post("/internal/gitea/dispatch", auth.Require("user.update"), giteaDispatch)
}

type dispatchReq struct {
	Kind         string `json:"kind"`
	SHA          string `json:"sha"`
	Branch       string `json:"branch"`
	DelaySeconds int    `json:"delay_seconds"`
	ProjectID    string `json:"project_id"`
}

// selfRepo dispatch 目标仓（FORJA 自身）；env 可覆盖，默认 <owner>/forja。
// SelfRepo 为 worker（cmd 层）定时部署导出的同一能力。
func selfRepo(ctx context.Context, client *gitea.Client) (string, string, error) { return SelfRepo(ctx, client) }

func SelfRepo(ctx context.Context, client *gitea.Client) (owner, repo string, err error) {
	owner, err = client.Owner(ctx)
	if err != nil {
		return "", "", err
	}
	if r := envOrDefault("GITEA_SELF_REPO", ""); r != "" {
		for i := len(r) - 1; i >= 0; i-- {
			if r[i] == '/' {
				return r[:i], r[i+1:], nil
			}
		}
		return owner, r, nil
	}
	return owner, "forja", nil
}

func envOrDefault(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// resolveDispatchTarget 解析 dispatch 目标仓（v2.0 M1.2 项目化）：
// project_id 指向已绑定 repo_full_name 的项目时构建该项目仓（isSelf=false）；
// 否则回退 FORJA self repo（自构建路径不回归）。
func resolveDispatchTarget(ctx context.Context, client *gitea.Client, projectID string) (owner, repo, fullRepo string, isSelf bool, err error) {
	if projectID != "" && idgen.IsValid(projectID) && model.Pool != nil {
		var full string
		if qerr := model.Pool.QueryRow(ctx, `SELECT COALESCE(repo_full_name,'') FROM projects WHERE id=$1`, projectID).Scan(&full); qerr == nil && strings.Contains(full, "/") {
			o, r := fullName2owner(full), fullName2repo(full)
			so, sr, serr := SelfRepo(ctx, client)
			if serr != nil {
				return o, r, full, false, nil
			}
			return o, r, full, full == so+"/"+sr, nil
		}
	}
	o, r, serr := selfRepo(ctx, client)
	if serr != nil {
		return "", "", "", false, serr
	}
	return o, r, o + "/" + r, true, nil
}

func giteaDispatch(c *fiber.Ctx) error {
	var req dispatchReq
	if err := c.BodyParser(&req); err != nil {
		return respond.BadRequest(c, "invalid body")
	}
	if req.Kind != "build" && req.Kind != "deploy" {
		return respond.InvalidParam(c, "kind", "must be build or deploy")
	}
	// U-3 扩展：isFullSHA 失败亦需保证不残留 RUNNING——需先解析 repo 再回滚
	client := gitea.NewFromEnv()
	if client == nil {
		return respond.ServiceUnavailable(c)
	}
	ctx := context.Background()
	owner, repo, fullRepo, isSelf, err := resolveDispatchTarget(ctx, client, req.ProjectID)
	if err != nil {
		return respond.Upstream(c, "gitea", err)
	}
	if !isFullSHA(req.SHA) {
		// 防御：无效 sha 不应已建 RUNNING，但仍尝试按项目维度回滚
		if req.Kind == "build" {
			effPID := req.ProjectID
			if effPID == "" {
				effPID = projectIDByRepo(fullRepo)
			}
			revertFailedBuildRun(ctx, fullRepo, req.SHA, effPID)
		}
		return respond.InvalidParam(c, "sha", "must be a full commit sha")
	}
	username := c.Locals("username").(string)

	switch req.Kind {
	case "build":
		return dispatchBuild(c, ctx, client, owner, repo, fullRepo, req.SHA, username, req.ProjectID, req.Branch)
	default:
		// v2.0：用户项目只交付（构建制品+交付包），不在 FORJA 宿主部署
		if !isSelf {
			return respond.BusinessRule(c, "DELIVERY_ONLY", "user projects are deliver-only; deploy is disabled (build an artifact and use the delivery package)")
		}
		return dispatchDeploy(c, ctx, client, owner, repo, fullRepo, req.SHA, username, req.DelaySeconds, req.ProjectID)
	}
}

func dispatchBuild(c *fiber.Ctx, ctx context.Context, client *gitea.Client, owner, repo, fullRepo, sha, username, reqProjectID, reqBranch string) error {
	// P0 可选 project_id：用于构建审计归属，空时回退 repo 映射；build 本身无部署互斥
	// U-3 扩展：effectivePID 提前解析，按项目维度校验/回滚，避免跨项目污染
	effectivePID := reqProjectID
	if effectivePID != "" {
		if !idgen.IsValid(effectivePID) {
			return respond.InvalidParam(c, "project_id", "invalid project_id")
		}
		if err := AuthorizeProject(c, effectivePID, "task.read"); err != nil {
			return err
		}
	} else {
		effectivePID = projectIDByRepo(fullRepo)
	}
	defBranch := ProjectDefaultBranch(ctx, effectivePID)
	branch := reqBranch
	if branch == "" {
		branch = defBranch
	}
	if branch != "master" && branch != defBranch && !(len(branch) > 10 && branch[:10] == "iteration/") {
		return respond.InvalidParam(c, "branch", "branch must be "+defBranch+", master or iteration/*")
	}
	head, err := client.BranchCommit(ctx, owner, repo, branch)
	if err != nil {
		return respond.Upstream(c, "gitea", err)
	}
	if head != sha {
		revertFailedBuildRun(ctx, fullRepo, sha, effectivePID)
		return respond.BusinessRule(c, "SHA_NOT_HEAD", "only "+branch+" head can be built (requested "+shortSHA(sha)+", head "+shortSHA(head)+") — refresh the list")
	}
	if model.Pool != nil {
		var dup bool
		// 防重复连点：同 sha 已有在途构建则拒绝（S6 §3.1）—— U-3 按 repo+project 维度隔离，避免跨项目误判
		if effectivePID != "" {
			if idgen.IsValid(effectivePID) {
				err = model.Pool.QueryRow(ctx,
					`SELECT EXISTS (SELECT 1 FROM pipeline_runs WHERE repo=$1 AND sha=$2 AND status='RUNNING' AND (project_id=$3::text OR (project_id IS NULL AND repo=(SELECT repo_full_name FROM projects WHERE id=$3::text))))`, fullRepo, sha, effectivePID).Scan(&dup)
			} else {
				err = model.Pool.QueryRow(ctx,
					`SELECT EXISTS (SELECT 1 FROM pipeline_runs WHERE repo=$1 AND sha=$2 AND status='RUNNING')`, fullRepo, sha).Scan(&dup)
			}
		} else {
			err = model.Pool.QueryRow(ctx,
				`SELECT EXISTS (SELECT 1 FROM pipeline_runs WHERE repo=$1 AND sha=$2 AND status='RUNNING')`, fullRepo, sha).Scan(&dup)
		}
		if err == nil && dup {
			return respond.Conflict(c, "build already running for this sha")
		}
	}
	if err := client.Dispatch(ctx, owner, repo, buildWorkflow, branch); err != nil {
		slog.Warn("dispatch build failed", "sha", shortSHA(sha), "branch", branch, "err", err.Error())
		revertFailedBuildRun(ctx, fullRepo, sha, effectivePID)
		return respond.Upstream(c, "gitea", err)
	}
	recordManualBuildRunWithBranch(ctx, fullRepo, sha, branch, username, effectivePID)
	preseedPipelineJobsWithBranch(ctx, fullRepo, sha, branch, effectivePID)
	pid := effectivePID
	logAudit(ctx, pid, username, "build.dispatched", "repo", "",
		`{"sha":`+quoteJSON(sha)+`,"workflow":"`+buildWorkflow+`"}`)
	return respond.OK(c, fiber.Map{"queued": true, "kind": "build"})
}

func dispatchDeploy(c *fiber.Ctx, ctx context.Context, client *gitea.Client, owner, repo, fullRepo, sha, username string, delaySeconds int, reqProjectID string) error {
	// P0 修复：按 project_id 维度隔离在途互斥。reqProjectID 可选，空时回退 ProjectIDByRepo(fullRepo)。
	effectivePID := reqProjectID
	if effectivePID != "" {
		if !idgen.IsValid(effectivePID) {
			return respond.InvalidParam(c, "project_id", "invalid project_id")
		}
		if err := AuthorizeProject(c, effectivePID, "task.read"); err != nil {
			return err
		}
	} else {
		effectivePID = projectIDByRepo(fullRepo)
	}
	// §11.3 在途互斥：同一项目内至多一条 scheduled/running（跨项目不互堵）
	if HasInFlightDeployment(ctx, effectivePID) {
		return respond.Conflict(c, "another deployment is in flight")
	}
	tag := "master-" + shortSHA(sha)
	if delaySeconds > 0 {
		if delaySeconds > 7*24*3600 {
			return respond.InvalidParam(c, "delay_seconds", "max 604800")
		}
		// 排期前同样校验制品存在，避免定时到期才发现空转
		if err := ensureBuilt(ctx, fullRepo, sha); err != nil {
			if isDeployValidation(err) {
				return respond.BadRequest(c, err.Error())
			}
			return respond.Upstream(c, "db", err)
		}
		if err := queue.EnqueueDeployScheduled(ctx, sha, username, time.Duration(delaySeconds)*time.Second, effectivePID, fullRepo); err != nil {
			return respond.Upstream(c, "asynq", err)
		}
		CreateDeployment(ctx, fullRepo, sha, tag, "scheduled", username, effectivePID)
		pid := effectivePID
		if pid == "" {
			pid = projectIDByRepo(fullRepo)
		}
		logAudit(ctx, pid, username, "deploy.scheduled", "repo", "",
			`{"sha":`+quoteJSON(sha)+`,"in_seconds":`+itoa(delaySeconds)+`}`)
		return respond.OK(c, fiber.Map{"queued": true, "kind": "deploy", "scheduled_in_seconds": delaySeconds})
	}
	if err := ExecuteDeploy(ctx, client, owner, repo, sha, ProjectDefaultBranch(ctx, effectivePID)); err != nil {
		if isDeployValidation(err) {
			return respond.BadRequest(c, err.Error())
		}
		return respond.Upstream(c, "gitea", err)
	}
	// 先执行后建行：ExecuteDeploy 失败不留脏行；回流竞态可忽略（job 至少数秒后才开始）
	CreateDeployment(ctx, fullRepo, sha, tag, "manual", username, effectivePID)
	pid := effectivePID
	if pid == "" {
		pid = projectIDByRepo(fullRepo)
	}
	logAudit(ctx, pid, username, "deploy.dispatched", "repo", "",
		`{"sha":`+quoteJSON(sha)+`,"tag":"`+tag+`"}`)
	return respond.OK(c, fiber.Map{"queued": true, "kind": "deploy"})
}

// ExecuteDeploy 实际部署链路：写 DEPLOY_TAG → dispatch deploy.yml(master)。
// 立即部署与 asynq 到期执行共用；校验失败返回可识别错误（端点层转 400）。
func ExecuteDeploy(ctx context.Context, client *gitea.Client, owner, repo string, sha string, branch string) error {
	if !isFullSHA(sha) {
		return errDeployValidation("invalid sha")
	}
	// pipeline_runs.repo 落的是 full_name（projects.repo_full_name 同源）
	if err := ensureBuilt(ctx, owner+"/"+repo, sha); err != nil {
		return err
	}
	if branch == "" {
		branch = "master"
	}
	tag := "master-" + shortSHA(sha)
	if err := client.SetVariable(ctx, owner, repo, deployVarKey, tag); err != nil {
		return err
	}
	return client.Dispatch(ctx, owner, repo, deployWorkflow, branch)
}

type deployValidationError struct{ msg string }

func (e deployValidationError) Error() string { return e.msg }
func isDeployValidation(err error) bool {
	_, ok := err.(deployValidationError)
	return ok
}
func errDeployValidation(msg string) error { return deployValidationError{msg} }

// ensureBuilt 部署安全网：目标 sha 必须存在 SUCCESS 构建产物（DoD #6——门禁不过不出制品、
// 无制品不可部署）。Pool 不可用时放行（本地裸跑场景）。
// branch 精确：若提供 branch 则校验该分支的 SUCCESS，避免跨分支误放行
func ensureBuilt(ctx context.Context, repo, sha string, branch ...string) error {
	if model.Pool == nil {
		return nil
	}
	var ok bool
	var err error
	if len(branch) > 0 && branch[0] != "" {
		err = model.Pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM pipeline_runs WHERE repo=$1 AND sha=$2 AND branch=$3 AND status='SUCCESS')`,
			repo, sha, branch[0]).Scan(&ok)
	} else {
		err = model.Pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM pipeline_runs WHERE repo=$1 AND sha=$2 AND status='SUCCESS')`,
			repo, sha).Scan(&ok)
	}
	if err != nil {
		return errDeployValidation("build check failed")
	}
	if !ok {
		return errDeployValidation("no successful build for this sha")
	}
	return nil
}

func recordManualBuildRunWithBranch(ctx context.Context, repo, sha, branch, actor string, projectID ...string) {
	if model.Pool == nil || repo == "" || isZeroSHA(sha) {
		return
	}
	var pid any
	effective := ""
	if len(projectID) > 0 {
		effective = projectID[0]
	}
	if effective != "" && idgen.IsValid(effective) {
		pid = effective
	}
	if pid == nil {
		if v := projectIDByRepo(repo); v != "" {
			pid = v
		}
	}
	var br any
	if branch != "" {
		br = branch
	}
	_, err := model.Pool.Exec(ctx, `
		INSERT INTO pipeline_runs (id, project_id, repo, sha, branch, event, status, actor)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, 'manual', 'RUNNING', NULLIF($5,''))
		ON CONFLICT (repo, sha, branch, event) DO UPDATE SET status='RUNNING', updated_at=now()`,
		pid, repo, sha, br, actor)
	if err != nil {
		slog.Warn("pipeline_runs upsert(manual+branch) failed", "err", err.Error())
	}
}

func preseedPipelineJobsWithBranch(ctx context.Context, repo, sha, branch string, projectID ...string) {
	// FORJA 自身四 job 预播（branch 透传）
	effective := ""
	if len(projectID) > 0 {
		effective = projectID[0]
	}
	preseedJobsNamed(ctx, repo, sha, branch, effective, expectedPipelineJobs)
}

// recordManualBuildRun 手动构建建行（event=manual，与 push 锚点隔离）
// P0-2 多租户修复：显式 projectID 优先于 repo 映射，回退兼容旧三参与历史调用。
func recordManualBuildRun(ctx context.Context, repo, sha, actor string, projectID ...string) {
	if model.Pool == nil || repo == "" || isZeroSHA(sha) {
		return
	}
	var pid any
	effective := ""
	if len(projectID) > 0 {
		effective = projectID[0]
	}
	if effective != "" {
		if idgen.IsValid(effective) {
			pid = effective
		} else {
			slog.Warn("pipeline_runs manual invalid project_id", "project_id", effective)
		}
	}
	if pid == nil {
		if v := projectIDByRepo(repo); v != "" {
			pid = v
		}
	}
	_, err := model.Pool.Exec(ctx, `
		INSERT INTO pipeline_runs (id, project_id, repo, sha, branch, event, status, actor)
		VALUES (gen_random_uuid(), $1, $2, $3, '', 'manual', 'RUNNING', NULLIF($4,''))
		ON CONFLICT (repo, sha, branch, event) DO UPDATE SET status='RUNNING', updated_at=now()`,
		pid, repo, sha, actor)
	if err != nil {
		slog.Warn("pipeline_runs upsert(manual) failed", "err", err.Error())
	}
}

// isFullSHA 40 位十六进制 commit sha
func isFullSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
