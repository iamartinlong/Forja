package api

// 可观测性 P0/P2 查询端点（observability-design.md §3.3、§5.3）：
//   GET  /pipelines            分页列表（project_id / status 筛选）
//   GET  /pipelines/summary    徽章专用聚合（running / failed_today / last_run）
//   GET  /pipelines/metrics    按日真聚合（success rate / 平均时长，P2 删 mock）
//   GET  /pipelines/:id        详情聚合 jobs
//   POST /internal/gitea/sync  Admin 手动对账（webhook 丢失补漏，拉 Gitea Actions runs）
//
// RBAC：查询挂 task.read（Require 认证门 + handler 内 AuthorizeProject /
// 非 Admin 限成员项目）；写入仅 webhook 内部路径与 Admin sync。

import (
	"context"
	"log"
	"strconv"
	"strings"
	"time"

	"forja/backend/internal/api/respond"
	"forja/backend/internal/auth"
	"forja/backend/internal/gitea"
	"forja/backend/internal/model"

	"github.com/gofiber/fiber/v2"
	idgen "forja/backend/internal/id"
)

func RegisterPipelineRoutes(r fiber.Router) {
	// summary / metrics / deployments 先于 :id 注册（Fiber 注册顺序优先），避免路径参数吞掉字面量段
	r.Get("/pipelines/summary", auth.Require("task.read"), pipelinesSummary)
	r.Get("/pipelines/metrics", auth.Require("task.read"), pipelinesMetrics)
	r.Get("/pipelines/deployments", auth.Require("task.read"), listDeployments)
	r.Get("/pipelines", auth.Require("task.read"), listPipelines)
	r.Get("/pipelines/:id", auth.Require("task.read"), getPipeline)
	r.Post("/internal/gitea/sync", auth.Require("user.update"), syncGiteaRuns)
	RegisterDispatchRoutes(r) // S6：POST /internal/gitea/dispatch（Admin 构建部署代理）
}

// pipelineScope 解析查询授权范围：返回 where 片段（含 WHERE 关键字或空）与参数。
// project_id 显式给定时走 AuthorizeProject；未给定时 Admin 全量、其余限成员项目。
func pipelineScope(c *fiber.Ctx) (string, []any, error) {
	projectID := c.Query("project_id")
	if projectID != "" {
		if !idgen.IsValid(projectID) {
			return "", nil, respond.InvalidParam(c, "project_id", "invalid project_id")
		}
		if err := AuthorizeProject(c, projectID, "task.read"); err != nil {
			return "", nil, err
		}
		return ` WHERE r.project_id=$1`, []any{projectID}, nil
	}
	if isGlobalAdmin(c) {
		return "", []any{}, nil
	}
	uid := currentUserID(c)
	if uid == "" {
		return ` WHERE FALSE`, []any{}, nil
	}
	return ` WHERE r.project_id IN (SELECT project_id FROM project_members WHERE user_id=$1)`, []any{uid}, nil
}

const pipelineCols = `r.id::text, COALESCE(r.project_id::text,''), r.repo, r.sha,
	COALESCE(r.branch,''), r.event, r.status, COALESCE(r.actor,''),
	COALESCE(r.duration_ms,0), r.created_at, r.updated_at`

type runRow struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Repo      string `json:"repo"`
	SHA       string `json:"sha"`
	Branch    string `json:"branch,omitempty"`
	Event     string `json:"event"`
	Status    string `json:"status"`
	Actor     string `json:"actor,omitempty"`
	Duration  int64  `json:"duration_ms"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// pgxRow 单行结果的最小接口（兼容 *pgxpool.Row 与测试桩）
type pgxRow interface {
	Scan(dest ...any) error
}

func scanPipelineRow(row pgxRow) (*runRow, error) {
	var m runRow
	var pid, branch, actor *string
	var createdAt, updatedAt time.Time
	if err := row.Scan(&m.ID, &pid, &m.Repo, &m.SHA, &branch, &m.Event,
		&m.Status, &actor, &m.Duration, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	m.ProjectID = deref(pid)
	m.Branch = deref(branch)
	m.Actor = deref(actor)
	m.CreatedAt = createdAt.Format(time.RFC3339)
	m.UpdatedAt = updatedAt.Format(time.RFC3339)
	return &m, nil
}

func appendStatusFilter(where string, args []any, status string) (string, []any) {
	if where == "" {
		where = " WHERE"
	} else {
		where += " AND"
	}
	where += ` r.status=$` + strconv.Itoa(len(args)+1)
	return where, append(args, status)
}

func listPipelines(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	where, args, err := pipelineScope(c)
	if err != nil {
		return err
	}
	if status := c.Query("status"); status != "" {
		where, args = appendStatusFilter(where, args, status)
	}
	page, pageSize := respond.ParsePage(c)
	ctx := context.Background()
	var total int64
	if err := model.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM pipeline_runs r`+where, args...).Scan(&total); err != nil {
		return respond.DBError(c, err)
	}
	query := `SELECT ` + pipelineCols + ` FROM pipeline_runs r` + where +
		` ORDER BY r.created_at DESC LIMIT $` + strconv.Itoa(len(args)+1) +
		` OFFSET $` + strconv.Itoa(len(args)+2)
	args = append(args, pageSize, (page-1)*pageSize)
	rows, err := model.Pool.Query(ctx, query, args...)
	if err != nil {
		return respond.DBError(c, err)
	}
	defer rows.Close()
	items := []*runRow{}
	for rows.Next() {
		m, err := scanPipelineRow(rows)
		if err != nil {
			return respond.DBError(c, err)
		}
		items = append(items, m)
	}
	if err := rows.Err(); err != nil {
		return respond.DBError(c, err)
	}
	return respond.List(c, items, respond.PageMeta{Page: page, PageSize: pageSize, Total: total})
}

func getPipeline(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	id := c.Params("id")
	if !idgen.IsValid(id) {
		return respond.InvalidParam(c, "id", "invalid pipeline id")
	}
	ctx := context.Background()
	run, err := scanPipelineRow(model.Pool.QueryRow(ctx,
		`SELECT `+pipelineCols+` FROM pipeline_runs r WHERE r.id=$1`, id))
	if err != nil {
		return respond.NotFound(c, "pipeline")
	}
	// 映射到项目的 run 走项目级授权；未映射仓库仅认证门兜底
	if run.ProjectID != "" {
		if err := AuthorizeProject(c, run.ProjectID, "task.read"); err != nil {
			return err
		}
	}
	jobRows, err := model.Pool.Query(ctx, `
		SELECT name, COALESCE(conclusion,''), COALESCE(duration_ms,0),
		       COALESCE(started_at::text,''), created_at::text
		FROM pipeline_jobs WHERE run_id=$1 ORDER BY created_at`, id)
	if err != nil {
		return respond.DBError(c, err)
	}
	defer jobRows.Close()
	jobs := []fiber.Map{}
	for jobRows.Next() {
		var name, conclusion, startedAt, created string
		var duration int64
		if err := jobRows.Scan(&name, &conclusion, &duration, &startedAt, &created); err != nil {
			return respond.DBError(c, err)
		}
		jobs = append(jobs, fiber.Map{
			"name": name, "conclusion": conclusion, "duration_ms": duration,
			"started_at": startedAt, "created_at": created,
		})
	}
	return respond.OK(c, fiber.Map{
		"id": run.ID, "project_id": run.ProjectID, "repo": run.Repo, "sha": run.SHA,
		"branch": run.Branch, "event": run.Event, "status": run.Status,
		"actor": run.Actor, "duration_ms": run.Duration,
		"created_at": run.CreatedAt, "updated_at": run.UpdatedAt, "jobs": jobs,
	})
}

func pipelinesSummary(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	where, args, err := pipelineScope(c)
	if err != nil {
		return err
	}
	cond := "TRUE"
	if where != "" {
		cond = strings.TrimPrefix(where, " WHERE ")
	}
	base := func(extra string) string {
		if cond == "TRUE" {
			return extra
		}
		return cond + " AND " + extra
	}
	ctx := context.Background()
	var running, failedToday, pending int64
	if err := model.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM pipeline_runs r WHERE `+base(`r.status='RUNNING'`), args...).Scan(&running); err != nil {
		return respond.DBError(c, err)
	}
	if err := model.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM pipeline_runs r WHERE `+
			base(`r.status='FAILURE' AND r.updated_at >= date_trunc('day', now())`), args...).Scan(&failedToday); err != nil {
		return respond.DBError(c, err)
	}
	if err := model.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM pipeline_runs r WHERE `+base(`r.status='PENDING'`), args...).Scan(&pending); err != nil {
		return respond.DBError(c, err)
	}
	var last any
	row := model.Pool.QueryRow(ctx,
		`SELECT `+pipelineCols+` FROM pipeline_runs r WHERE `+base(`TRUE`)+
			` ORDER BY r.created_at DESC LIMIT 1`, args...)
	if m, err := scanPipelineRow(row); err == nil {
		last = m
	}
	// S6 §11 当前线上版本：deployments 表最新 success 行（真实"已上线"，非指令快照）—— U-2 按 projectId 过滤
	var deployed any
	deployWhere, deployArgs, errScope := deploymentScope(c)
	if errScope != nil {
		return errScope
	}
	if model.Pool != nil {
		var dTag, dSHA, dBy string
		var dAt time.Time
		var scanErr error
		if deployWhere == "" {
			scanErr = model.Pool.QueryRow(ctx, `
			SELECT tag, sha, COALESCE(actor,''), deployed_at FROM deployments
			WHERE status='success' ORDER BY deployed_at DESC NULLS LAST LIMIT 1`).Scan(&dTag, &dSHA, &dBy, &dAt)
		} else {
			scanErr = model.Pool.QueryRow(ctx, `
			SELECT tag, sha, COALESCE(actor,''), deployed_at FROM deployments`+deployWhere+` AND status='success' ORDER BY deployed_at DESC NULLS LAST LIMIT 1`, deployArgs...).Scan(&dTag, &dSHA, &dBy, &dAt)
		}
		if scanErr == nil {
			deployed = fiber.Map{"sha": shortSHA(dSHA), "tag": dTag, "by": dBy,
				"at": dAt.Format(time.RFC3339)}
		}
		// 在途部署计数（摘要条「部署中」徽章）—— 同样按项目过滤
		var deploying int64
		var depErr error
		if deployWhere == "" {
			depErr = model.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM deployments WHERE status IN ('scheduled','running')`).Scan(&deploying)
		} else {
			depErr = model.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM deployments`+deployWhere+` AND status IN ('scheduled','running')`, deployArgs...).Scan(&deploying)
		}
		if depErr == nil && deploying > 0 {
			return respond.OK(c, fiber.Map{
				"running": running, "failed_today": failedToday, "pending_builds": pending,
				"last_run": last, "deployed": deployed, "deploying": deploying,
			})
		}
	}
	return respond.OK(c, fiber.Map{
		"running": running, "failed_today": failedToday, "pending_builds": pending,
		"last_run": last, "deployed": deployed,
	})
}

// metricsDay 单日聚合行（observability P2：Metrics 页真数据源）
type metricsDay struct {
	Day           string   `json:"day"`           // YYYY-MM-DD
	Total         int64    `json:"total"`         // 当日 run 总数
	Success       int64    `json:"success"`       // 结论 SUCCESS
	Failure       int64    `json:"failure"`       // 结论 FAILURE
	SuccessRate   *float64 `json:"success_rate"`  // 百分比 0-100（无已完结 run 时为 null）
	AvgDurationMs *float64 `json:"avg_duration_ms"` // 已完结 run 平均耗时（无则为 null）
}

// pipelinesMetrics 按日聚合 pipeline_runs（success rate / 平均时长）。
// days 参数默认 14、上限 90；授权范围与列表一致（Admin 全量，其余限成员项目）。
func pipelinesMetrics(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	days := c.QueryInt("days", 14)
	if days < 1 || days > 90 {
		days = 14
	}
	where, args, err := pipelineScope(c)
	if err != nil {
		return err
	}
	if where == "" {
		where = " WHERE"
	} else {
		where += " AND"
	}
	where += ` r.created_at >= date_trunc('day', now()) - make_interval(days => $` + strconv.Itoa(len(args)+1) + `)`
	args = append(args, days)
	ctx := context.Background()
	rows, err := model.Pool.Query(ctx, `
		SELECT to_char(date_trunc('day', r.created_at), 'YYYY-MM-DD'),
		       COUNT(*),
		       COUNT(*) FILTER (WHERE r.status='SUCCESS'),
		       COUNT(*) FILTER (WHERE r.status='FAILURE'),
		       AVG(r.duration_ms) FILTER (WHERE r.status IN ('SUCCESS','FAILURE'))
		FROM pipeline_runs r`+where+`
		GROUP BY 1 ORDER BY 1`, args...)
	if err != nil {
		return respond.DBError(c, err)
	}
	defer rows.Close()
	items := []*metricsDay{}
	for rows.Next() {
		var d metricsDay
		if err := rows.Scan(&d.Day, &d.Total, &d.Success, &d.Failure, &d.AvgDurationMs); err != nil {
			return respond.DBError(c, err)
		}
		finished := d.Success + d.Failure
		if finished > 0 {
			rate := float64(d.Success) * 100 / float64(finished)
			d.SuccessRate = &rate
		}
		items = append(items, &d)
	}
	if err := rows.Err(); err != nil {
		return respond.DBError(c, err)
	}
	return respond.OK(c, fiber.Map{"days": days, "items": items})
}

// syncGiteaRuns Admin 手动对账：拉各映射项目仓最近的 Actions runs 补 webhook 缺口。
// 容错设计：单仓失败仅告警不中断；Gitea 未配置时返回 503。
func syncGiteaRuns(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	client := gitea.NewFromEnv()
	if client == nil {
		return respond.ServiceUnavailable(c)
	}
	ctx := context.Background()
	rows, err := model.Pool.Query(ctx, `SELECT repo_full_name FROM projects WHERE repo_full_name IS NOT NULL`)
	if err != nil {
		return respond.DBError(c, err)
	}
	defer rows.Close()
	repos := []string{}
	for rows.Next() {
		var repo string
		if err := rows.Scan(&repo); err != nil {
			return respond.DBError(c, err)
		}
		repos = append(repos, repo)
	}
	synced := 0
	errs := []string{}
	for _, repo := range repos {
		runs, err := client.ListActionRuns(ctx, repo, 20)
		if err != nil {
			log.Printf("gitea sync %s: %v", repo, err)
			errs = append(errs, repo+": "+err.Error())
			continue
		}
		for _, r := range runs {
			if isZeroSHA(r.HeadSHA) {
				continue
			}
			var pid any
			if v := projectIDByRepo(repo); v != "" {
				pid = v
			}
			if _, err := model.Pool.Exec(ctx, `
				INSERT INTO pipeline_runs (id, project_id, repo, sha, branch, event, status, actor)
				VALUES (gen_random_uuid(), $1, $2, $3, NULLIF($4,''), NULLIF($5,''), $6, 'gitea')
				ON CONFLICT (repo, sha, branch, event) DO UPDATE SET
				  status=EXCLUDED.status, updated_at=now()`,
				pid, repo, r.HeadSHA, r.HeadBranch, normalizeRunEvent(r.Event), normalizeRunStatus(r.Status)); err != nil {
				log.Printf("gitea sync upsert %s@%s: %v", repo, r.HeadSHA[:8], err)
				continue
			}
			synced++
		}
	}
	return respond.OK(c, fiber.Map{"repos": len(repos), "synced_runs": synced, "errors": errs})
}

func shortSHA(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// normalizeRunEvent Gitea run event → 本表枚举（push/workflow_dispatch/schedule 原样保留）
func normalizeRunEvent(ev string) string {
	switch ev {
	case "":
		return "workflow_job"
	case "manual":
		return "workflow_dispatch"
	default:
		return ev
	}
}

// normalizeRunStatus Gitea Actions 状态 → RUNNING/SUCCESS/FAILURE/CANCELLED
func normalizeRunStatus(st string) string {
	switch st {
	case "success":
		return "SUCCESS"
	case "failed", "failure":
		return "FAILURE"
	case "cancelled":
		return "CANCELLED"
	default:
		return "RUNNING"
	}
}
