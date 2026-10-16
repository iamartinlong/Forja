package api

// S6 §11 部署实体化：deployments 表状态机（scheduled/running/success/failed）。
// "当前线上"唯一真相 = 最新 status='success' 行（横幅/summary 数据源）；
// 审计溯源仍在 audit_logs，本表只做状态追踪。
//
// 回流匹配策略（§11.4）：deploy.yml 的 workflow_job.head_sha 是 master HEAD 而非目标
// TAG commit，故不做 sha 匹配——更新"当前唯一 in-flight 行"，前提是 dispatch 端点的
// 在途互斥（同一时刻至多一条 scheduled/running）。

import (
	"context"
	"log/slog"
	"strconv"

	"forja/backend/internal/api/respond"
	"forja/backend/internal/model"

	"github.com/gofiber/fiber/v2"
	idgen "forja/backend/internal/id"
)

const (
	deployStatusScheduled = "scheduled"
	deployStatusRunning   = "running"
	deployStatusSuccess   = "success"
	deployStatusFailed    = "failed"
)

// 路由注册：GET /pipelines/deployments 由 pipeline.go 的 RegisterPipelineRoutes
// 统一挂载——必须先于 /pipelines/:id 注册（Fiber 注册顺序优先），故不放本文件。

// HasInFlightDeployment 在途互斥检查（§11.3）：存在 scheduled/running 时拒绝新部署。
// 先 reap 僵尸：deploy 实测 ~51s 完成，running 超 10 分钟必属异常（runner 崩溃/
// UpdateTask 上报丢失），不清扫会永久卡死所有后续部署。
// P0 修复：按 project_id 维度隔离（同一项目内互斥，跨项目不互堵）；兼容 legacy 行
// (project_id IS NULL) 通过 repo=(SELECT repo_full_name FROM projects WHERE id=$1) 兜底。
func HasInFlightDeployment(ctx context.Context, projectID ...string) bool {
	pid := ""
	if len(projectID) > 0 {
		pid = projectID[0]
	}
	if model.Pool == nil {
		return false
	}
	ReapStaleDeployments(ctx, pid)
	var exists bool
	var err error
	if pid != "" {
		if !idgen.IsValid(pid) {
			slog.Warn("deployments inflight invalid project_id", "project_id", pid)
			err = model.Pool.QueryRow(ctx,
				`SELECT EXISTS (SELECT 1 FROM deployments WHERE status IN ('scheduled','running'))`).Scan(&exists)
		} else {
			err = model.Pool.QueryRow(ctx,
				`SELECT EXISTS (SELECT 1 FROM deployments WHERE (project_id=$1::text OR (project_id IS NULL AND repo=(SELECT repo_full_name FROM projects WHERE id=$1::text))) AND status IN ('scheduled','running'))`, pid).Scan(&exists)
		}
	} else {
		err = model.Pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM deployments WHERE status IN ('scheduled','running'))`).Scan(&exists)
	}
	if err != nil {
		slog.Warn("deployments inflight check failed", "err", err.Error())
		return false // 查询失败不阻塞主流程（宽松侧），回流匹配仍按唯一行兜底
	}
	return exists
}

// ReapStaleDeployments 部署僵尸自愈：running 超 10 分钟判 failed（正常 <2min；
// runner 崩溃/上报丢失兜底）。scheduled 合法期 = 用户 delay（≤7 天）+缓冲，超 8 天
// 仍 scheduled 必为任务丢失，一并清理。挂 HasInFlightDeployment 与 webhook deploy
// 回流入口顺带执行，无独立定时器。
// P0 修复：按 project_id 维度隔离，僵尸清扫仅影响目标项目。
func ReapStaleDeployments(ctx context.Context, projectID ...string) {
	pid := ""
	if len(projectID) > 0 {
		pid = projectID[0]
	}
	if model.Pool == nil {
		return
	}
	var tag interface {
		RowsAffected() int64
	}
	var err error
	if pid != "" {
		if !idgen.IsValid(pid) {
			slog.Warn("deployments reap invalid project_id", "project_id", pid)
			tag, err = model.Pool.Exec(ctx, `
				UPDATE deployments SET status='failed'
				WHERE (status='running' AND created_at < now()-interval '10 minutes')
				   OR (status='scheduled' AND created_at < now()-interval '8 days')`)
		} else {
			tag, err = model.Pool.Exec(ctx, `
				UPDATE deployments SET status='failed'
				WHERE (project_id=$1::text OR (project_id IS NULL AND repo=(SELECT repo_full_name FROM projects WHERE id=$1::text)))
				  AND ((status='running' AND created_at < now()-interval '10 minutes')
				    OR (status='scheduled' AND created_at < now()-interval '8 days'))`, pid)
		}
	} else {
		tag, err = model.Pool.Exec(ctx, `
			UPDATE deployments SET status='failed'
			WHERE (status='running' AND created_at < now()-interval '10 minutes')
			   OR (status='scheduled' AND created_at < now()-interval '8 days')`)
	}
	if err != nil {
		slog.Warn("deployments reap failed", "err", err.Error())
	} else if tag.RowsAffected() > 0 {
		slog.Warn("deployments reaped stale rows", "count", tag.RowsAffected())
	}
}

// deploymentScope U-2 项目过滤：显式 project_id 则授权并按 project_id 精确过滤
// （兼容存量 project_id IS NULL 的 legacy 行，回退按 repo=projects.repo_full_name 映射）；
// 无参时 Admin 全量，其余限成员项目（与 pipelineScope 一致）。
func deploymentScope(c *fiber.Ctx) (string, []any, error) {
	projectID := c.Query("project_id")
	if projectID != "" {
		if !idgen.IsValid(projectID) {
			return "", nil, respond.InvalidParam(c, "project_id", "invalid project_id")
		}
		if err := AuthorizeProject(c, projectID, "task.read"); err != nil {
			return "", nil, err
		}
		// project_id 列在 016 迁移后存在；存量 NULL 行用 repo 映射兜底
		return ` WHERE (project_id=$1 OR (project_id IS NULL AND repo=(SELECT repo_full_name FROM projects WHERE id=$1)))`, []any{projectID}, nil
	}
	if isGlobalAdmin(c) {
		return "", []any{}, nil
	}
	uid := currentUserID(c)
	if uid == "" {
		return ` WHERE FALSE`, []any{}, nil
	}
	return ` WHERE (project_id IN (SELECT project_id FROM project_members WHERE user_id=$1) OR (project_id IS NULL AND repo IN (SELECT repo_full_name FROM projects WHERE id IN (SELECT project_id FROM project_members WHERE user_id=$1))))`, []any{uid}, nil
}

// CreateDeployment dispatch 成功后落实体行（mode=manual|scheduled）—— U-2 写入 project_id 以支持按项目过滤
// P0 扩展：显式 projectID 参数（来自 dispatch body），为空时回退 ProjectIDByRepo(repo) 兼容旧链路。
// 支持可变参数以兼容旧 6 参调用（测试/过渡期）：CreateDeployment(ctx, repo, sha, tag, mode, actor) 仍可用。
func CreateDeployment(ctx context.Context, repo, sha, tag, mode, actor string, projectID ...string) {
	if model.Pool == nil || repo == "" || sha == "" {
		return
	}
	status := deployStatusRunning
	if mode == "scheduled" {
		status = deployStatusScheduled
	}
	pidParam := ""
	if len(projectID) > 0 {
		pidParam = projectID[0]
	}
	var pid any
	if pidParam != "" {
		if idgen.IsValid(pidParam) {
			pid = pidParam
		} else {
			slog.Warn("deployments create invalid project_id", "project_id", pidParam)
		}
	}
	if pid == nil {
		if v := ProjectIDByRepo(repo); v != "" {
			if idgen.IsValid(v) {
				pid = v
			}
		}
	}
	_, err := model.Pool.Exec(ctx, `
		INSERT INTO deployments (repo, sha, tag, status, mode, actor, project_id)
		VALUES ($1,$2,$3,$4,$5,NULLIF($6,''), $7::text)`, repo, sha, tag, status, mode, actor, pid)
	if err != nil {
		// 回退：列不存在或类型不匹配（迁移尚未执行时）按旧 schema 插入，保证服务不中断
		if _, err2 := model.Pool.Exec(ctx, `
			INSERT INTO deployments (repo, sha, tag, status, mode, actor)
			VALUES ($1,$2,$3,$4,$5,NULLIF($6,''))`, repo, sha, tag, status, mode, actor); err2 != nil {
			slog.Warn("deployments insert failed", "tag", tag, "err", err.Error())
		}
	}
}

// MarkScheduledRunning worker 定时到期：最新 scheduled 行转 running（互斥保证唯一）
// P0 修复：按 project_id 维度隔离（可变参数兼容旧无参调用）
func MarkScheduledRunning(ctx context.Context, projectID ...string) {
	pid := ""
	if len(projectID) > 0 {
		pid = projectID[0]
	}
	if model.Pool == nil {
		return
	}
	var tag interface {
		RowsAffected() int64
	}
	var err error
	if pid != "" {
		if !idgen.IsValid(pid) {
			slog.Warn("deployments scheduled->running invalid project_id", "project_id", pid)
			tag, err = model.Pool.Exec(ctx, `
				UPDATE deployments SET status='running'
				WHERE status='scheduled'
				  AND id=(SELECT id FROM deployments WHERE status='scheduled' ORDER BY created_at DESC LIMIT 1)`)
		} else {
			tag, err = model.Pool.Exec(ctx, `
				UPDATE deployments SET status='running'
				WHERE (project_id=$1::text OR (project_id IS NULL AND repo=(SELECT repo_full_name FROM projects WHERE id=$1::text)))
				  AND status='scheduled'
				  AND id=(SELECT id FROM deployments WHERE (project_id=$1::text OR (project_id IS NULL AND repo=(SELECT repo_full_name FROM projects WHERE id=$1::text))) AND status='scheduled' ORDER BY created_at DESC LIMIT 1)`, pid)
		}
	} else {
		tag, err = model.Pool.Exec(ctx, `
			UPDATE deployments SET status='running'
			WHERE status='scheduled'
			  AND id=(SELECT id FROM deployments WHERE status='scheduled' ORDER BY created_at DESC LIMIT 1)`)
	}
	if err != nil {
		slog.Warn("deployments scheduled->running failed", "err", err.Error())
	} else if tag.RowsAffected() > 0 {
		slog.Info("deployments scheduled -> running", "project_id", pid)
	}
}

// FailScheduledDeployment 定时任务遇不可恢复错误（配置缺失等）时终止：
// 行标 failed 且调用方不再重试（重试无法自愈配置问题，只会堆积 archived 任务）
// P0 修复：按 project_id 维度隔离。签名兼容旧 2 参调用：FailScheduledDeployment(ctx, reason) 与 新 3 参 FailScheduledDeployment(ctx, projectID, reason)
func FailScheduledDeployment(ctx context.Context, a string, b ...string) {
	var pid, reason string
	if len(b) == 0 {
		reason = a
		pid = ""
	} else {
		pid = a
		reason = b[0]
	}
	if model.Pool == nil {
		slog.Warn("scheduled deployment aborted", "reason", reason, "project_id", pid)
		return
	}
	var err error
	if pid != "" {
		if !idgen.IsValid(pid) {
			slog.Warn("deployments fail-scheduled invalid project_id", "project_id", pid)
			_, err = model.Pool.Exec(ctx, `
				UPDATE deployments SET status='failed'
				WHERE status='scheduled'
				  AND id=(SELECT id FROM deployments WHERE status='scheduled' ORDER BY created_at DESC LIMIT 1)`)
		} else {
			_, err = model.Pool.Exec(ctx, `
				UPDATE deployments SET status='failed'
				WHERE (project_id=$1::text OR (project_id IS NULL AND repo=(SELECT repo_full_name FROM projects WHERE id=$1::text)))
				  AND status='scheduled'
				  AND id=(SELECT id FROM deployments WHERE (project_id=$1::text OR (project_id IS NULL AND repo=(SELECT repo_full_name FROM projects WHERE id=$1::text))) AND status='scheduled' ORDER BY created_at DESC LIMIT 1)`, pid)
		}
	} else {
		_, err = model.Pool.Exec(ctx, `
			UPDATE deployments SET status='failed'
			WHERE status='scheduled'
			  AND id=(SELECT id FROM deployments WHERE status='scheduled' ORDER BY created_at DESC LIMIT 1)`)
	}
	if err != nil {
		slog.Warn("deployments fail-scheduled failed", "err", err.Error())
	}
	slog.Warn("scheduled deployment aborted", "reason", reason, "project_id", pid)
}

// CompleteRunningDeployment webhook deploy 结论回填唯一 running 行（§11.4）。
// 无匹配行仅告警（历史脏数据容错）；conclusion 非 success 一律 failed。
// P0 修复：按 project_id 维度隔离。签名兼容旧 2 参 CompleteRunningDeployment(ctx, conclusion) 与 新 3 参 CompleteRunningDeployment(ctx, projectID, conclusion)
func CompleteRunningDeployment(ctx context.Context, a string, b ...string) {
	var pid, conclusion string
	if len(b) == 0 {
		conclusion = a
		pid = ""
	} else {
		pid = a
		conclusion = b[0]
	}
	if model.Pool == nil {
		return
	}
	status := deployStatusFailed
	if conclusion == "success" {
		status = deployStatusSuccess
	}
	var tag interface {
		RowsAffected() int64
	}
	var err error
	if pid != "" {
		if !idgen.IsValid(pid) {
			slog.Warn("deployments complete invalid project_id", "project_id", pid)
			tag, err = model.Pool.Exec(ctx, `
				UPDATE deployments SET status=$1,
				       deployed_at=CASE WHEN $1='success' THEN now() ELSE deployed_at END
				WHERE status='running'
				  AND id=(SELECT id FROM deployments WHERE status='running' ORDER BY created_at DESC LIMIT 1)`,
				status)
		} else {
			tag, err = model.Pool.Exec(ctx, `
				UPDATE deployments SET status=$1,
				       deployed_at=CASE WHEN $1='success' THEN now() ELSE deployed_at END
				WHERE (project_id=$2::text OR (project_id IS NULL AND repo=(SELECT repo_full_name FROM projects WHERE id=$2::text)))
				  AND status='running'
				  AND id=(SELECT id FROM deployments WHERE (project_id=$2::text OR (project_id IS NULL AND repo=(SELECT repo_full_name FROM projects WHERE id=$2::text))) AND status='running' ORDER BY created_at DESC LIMIT 1)`,
				status, pid)
		}
	} else {
		tag, err = model.Pool.Exec(ctx, `
			UPDATE deployments SET status=$1,
			       deployed_at=CASE WHEN $1='success' THEN now() ELSE deployed_at END
			WHERE status='running'
			  AND id=(SELECT id FROM deployments WHERE status='running' ORDER BY created_at DESC LIMIT 1)`,
			status)
	}
	if err != nil {
		slog.Warn("deployments complete failed", "conclusion", conclusion, "err", err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		slog.Warn("deployments complete matched no running row", "conclusion", conclusion)
	}
}

type deploymentRow struct {
	ID        string `json:"id"`
	SHA       string `json:"sha"`
	Tag       string `json:"tag"`
	Status    string `json:"status"`
	Mode      string `json:"mode"`
	Actor     string `json:"actor,omitempty"`
	CreatedAt string `json:"created_at"`
	DeployedAt string `json:"deployed_at,omitempty"`
}

func listDeployments(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	where, args, err := deploymentScope(c)
	if err != nil {
		return err
	}
	page, pageSize := respond.ParsePage(c)
	ctx := context.Background()
	var total int64
	if err := model.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM deployments`+where, args...).Scan(&total); err != nil {
		return respond.DBError(c, err)
	}
	args = append(args, pageSize, (page-1)*pageSize)
	rows, err := model.Pool.Query(ctx, `
		SELECT id::text, sha, tag, status, mode, COALESCE(actor,''),
		       created_at::text, COALESCE(deployed_at::text,'')
		FROM deployments`+where+` ORDER BY created_at DESC
		LIMIT $`+strconv.Itoa(len(args)-1)+` OFFSET $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return respond.DBError(c, err)
	}
	defer rows.Close()
	items := []*deploymentRow{}
	for rows.Next() {
		var d deploymentRow
		if err := rows.Scan(&d.ID, &d.SHA, &d.Tag, &d.Status, &d.Mode, &d.Actor,
			&d.CreatedAt, &d.DeployedAt); err != nil {
			return respond.DBError(c, err)
		}
		items = append(items, &d)
	}
	if err := rows.Err(); err != nil {
		return respond.DBError(c, err)
	}
	return respond.List(c, items, respond.PageMeta{Page: page, PageSize: pageSize, Total: total})
}
