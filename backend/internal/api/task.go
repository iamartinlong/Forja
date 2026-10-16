package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"forja/backend/internal/agent"
	"forja/backend/internal/api/respond"
	"forja/backend/internal/auth"
	"forja/backend/internal/converge"
	"forja/backend/internal/events"
	"forja/backend/internal/furnace"
	"forja/backend/internal/gitea"
	"forja/backend/internal/gitops"
	"forja/backend/internal/metrics"
	"forja/backend/internal/model"
	"forja/backend/internal/queue"
	"forja/backend/internal/validation"
	"forja/backend/internal/vector"

	"github.com/gofiber/fiber/v2"
	idgen "forja/backend/internal/id"
	"github.com/jackc/pgx/v5"
)

type createTaskReq struct {
	ProjectID           string   `json:"project_id"`
	SpecID              string   `json:"spec_id"`
	Title               string   `json:"title"`
	Description         string   `json:"description"`
	SpecRefs            []string `json:"spec_refs"`
	DependsOn           []string `json:"depends_on"`
	EstimatedHours      *float64 `json:"estimated_hours"`
	Priority            *int     `json:"priority"`
	AssignedTo          string   `json:"assigned_to"`
	Type                string   `json:"type"` // epic/story/task
	TargetIterationID   string   `json:"target_iteration_id"`
}

func RegisterTaskRoutes(r fiber.Router) {
	r.Post("/tasks", auth.Require("task.write"), createTask)
	r.Post("/tasks/batch", auth.Require("task.write"), batchCreateTasks)
	r.Get("/tasks", auth.Require("task.read"), listTasks)
	r.Get("/tasks/:id", auth.Require("task.read"), getTask)
	r.Post("/tasks/:id/approve", auth.Require("task.approve"), approveTask)
	r.Post("/tasks/:id/release", auth.Require("task.release"), releaseTask)
	r.Post("/tasks/:id/reject", auth.Require("task.release"), rejectTask)
	r.Post("/tasks/:id/revert", auth.Require("task.release"), revertTask)
	r.Patch("/tasks/:id", auth.Require("task.write"), patchTask)
	r.Post("/tasks/:id/converge", auth.Require("task.run"), convergeTask)
	// /tasks/:id/stream 真 SSE 由 RegisterSSERoutes 注册（sse.go）
	r.Get("/projects/:id/dag", auth.Require("dag.read"), getDag)
}

func createTask(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	var req createTaskReq
	if err := c.BodyParser(&req); err != nil {
		return respond.BadRequest(c, err.Error())
	}
	if req.Title == "" {
		return respond.InvalidParam(c, "title", "title required")
	}
	if req.ProjectID == "" {
		return respond.InvalidParam(c, "project_id", "project_id required")
	}
	if err := AuthorizeProject(c, req.ProjectID, "task.write"); err != nil {
		return err
	}
	projectID := req.ProjectID
	taskID := idgen.New()
	ctx := context.Background()
	// validate spec_refs not empty for traceability (warn only)
	priority := 0
	if req.Priority != nil {
		priority = *req.Priority
	}
	var est any
	if req.EstimatedHours != nil {
		est = *req.EstimatedHours
	}
	var assigned any = nullableUUID(req.AssignedTo)
	var iterID any = nullableUUID(req.TargetIterationID)
	if req.TargetIterationID != "" {
		if !idgen.IsValid(req.TargetIterationID) {
			return respond.InvalidParam(c, "target_iteration_id", "invalid iteration id")
		}
		var cnt int
		if err := model.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM iterations WHERE id=$1 AND project_id=$2`, req.TargetIterationID, projectID).Scan(&cnt); err != nil {
			return respond.DBError(c, err)
		}
		if cnt == 0 {
			return respond.InvalidParam(c, "target_iteration_id", "iteration not found or wrong project")
		}
	}
	// insert with extended columns
	_, err := model.Pool.Exec(ctx, `INSERT INTO tasks (id, project_id, spec_id, title, description, status, priority, estimated_hours, assigned_to, target_iteration_id) VALUES ($1,$2,$3,$4,$5,'PENDING',$6,$7,$8,$9)`, taskID, projectID, nullableUUID(req.SpecID), req.Title, req.Description, priority, est, assigned, iterID)
	if err != nil {
		return respond.DBError(c, err)
	}
	// dependencies with cycle check
	if len(req.DependsOn) > 0 {
		for _, dep := range req.DependsOn {
			if !idgen.IsValid(dep) {
				return respond.InvalidParam(c, "depends_on", "invalid depends_on id: "+dep)
			}
			// verify dep exists and same project
			var cnt int
			if err := model.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM tasks WHERE id=$1 AND project_id=$2`, dep, projectID).Scan(&cnt); err != nil {
				return respond.DBError(c, err)
			}
			if cnt == 0 {
				return respond.InvalidParam(c, "depends_on", "depends_on not found or wrong project: "+dep)
			}
		}
		// cycle check: if any dep can reach taskID via existing graph, would create cycle (taskID is new, so only check if dep == taskID)
		for _, dep := range req.DependsOn {
			if dep == taskID {
				return respond.BusinessRule(c, "CYCLE", "self dependency")
			}
			if hasCycle(ctx, projectID, dep, taskID) {
				return respond.BusinessRule(c, "CYCLE", "dependency would create cycle")
			}
			if _, err := model.Pool.Exec(ctx, `INSERT INTO task_dependencies (task_id, depends_on) VALUES ($1,$2)`, taskID, dep); err != nil {
				return respond.DBError(c, err)
			}
		}
	}
	// audit log with actor
	username, _ := c.Locals("username").(string)
	if username == "" {
		username = "anonymous"
	}
	logAudit(ctx, projectID, username, "task.create", "task", taskID, fmt.Sprintf(`{"title":%q,"spec_refs":%q}`, req.Title, strings.Join(req.SpecRefs, ",")))
	for _, ref := range req.SpecRefs {
		_, _ = model.Pool.Exec(ctx, `INSERT INTO task_spec_refs (id, task_id, ref) VALUES ($1,$2,$3)`, idgen.New(), taskID, ref)
	}
	if len(req.SpecRefs) > 0 {
		_, _ = model.Pool.Exec(ctx, `INSERT INTO audit_logs (id, project_id, actor, action, target_type, target_id, detail) VALUES ($1,$2,'api','task.create.spec_refs','task',$3,$4)`, idgen.New(), projectID, taskID, fmt.Sprintf(`{"spec_refs":%q}`, strings.Join(req.SpecRefs, ",")))
	}
	return respond.OK(c, fiber.Map{"id": taskID, "status": "PENDING"})
}

func getTask(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	id := c.Params("id")
	ctx := context.Background()
	var title, status string
	var projectID string
	var specID *string
	var confidence *float64
	var actionRequired []byte
	var commitSHA *string
	var forgeAttempt int
	var branch *string
	var targetIter *string
	err := model.Pool.QueryRow(ctx, `SELECT project_id, spec_id, title, status, confidence, action_required, commit_sha, forge_attempt, branch, target_iteration_id FROM tasks WHERE id=$1`, id).Scan(&projectID, &specID, &title, &status, &confidence, &actionRequired, &commitSHA, &forgeAttempt, &branch, &targetIter)
	if err == pgx.ErrNoRows {
		return respond.NotFound(c, "task")
	}
	if err != nil {
		return respond.DBError(c, err)
	}
	if err := AuthorizeProject(c, projectID, "task.read"); err != nil {
		return err
	}
	// aggregate code_changes / test_results / agent_executions
	// 2026-09-10 收敛到"最近活跃窗"：此前全量返回，签核弹窗把历轮历 run 的
	// 150+ 行（含 P0 期污染 FORJA 源码树的旧 diff）全堆出来，无从评审。
	// 锚点 = 该任务全部产物行的最新时间，前推 10 分钟（同轮多 attempt 相隔
	// 数分钟，跨 run 相隔数小时；旧数据 STALE 但保留在库，仅展示收敛）。
	// code_changes 另按 file_path 去重取最新（整文件交付每轮重写同名文件）。
	var anchor *time.Time
	_ = model.Pool.QueryRow(ctx, `SELECT MAX(created_at) FROM (
		SELECT created_at FROM code_changes WHERE task_id=$1
		UNION ALL SELECT tc.created_at FROM test_cases tc WHERE tc.task_id=$1
	)`, id).Scan(&anchor)
	since := time.Time{}
	if anchor != nil {
		since = anchor.Add(-10 * time.Minute)
	}
	rows, err := model.Pool.Query(ctx, `SELECT file_path, diff FROM code_changes WHERE task_id=$1 AND created_at >= $2 ORDER BY created_at DESC`, id, since)
	if err != nil {
		return respond.DBError(c, err)
	}
	codeChanges := []fiber.Map{}
	seenPath := map[string]bool{}
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var fp, diff string
			if err := rows.Scan(&fp, &diff); err != nil {
				return respond.DBError(c, err)
			}
			if seenPath[fp] {
				continue
			}
			seenPath[fp] = true
			codeChanges = append(codeChanges, fiber.Map{"file_path": fp, "diff": diff})
		}
	}
	rows2, err := model.Pool.Query(ctx, `SELECT tr.status, tr.output, tc.name FROM test_results tr JOIN test_cases tc ON tc.id=tr.test_case_id WHERE tc.task_id=$1 AND tc.created_at >= $2 ORDER BY tr.created_at DESC`, id, since)
	if err != nil {
		return respond.DBError(c, err)
	}
	testResults := []fiber.Map{}
	if rows2 != nil {
		defer rows2.Close()
		for rows2.Next() {
			var s, out, name string
			if err := rows2.Scan(&s, &out, &name); err != nil {
				return respond.DBError(c, err)
			}
			testResults = append(testResults, fiber.Map{"status": s, "output": out, "name": name})
		}
	}
	rows3, err := model.Pool.Query(ctx, `SELECT agent_type, status, attempt FROM agent_executions WHERE task_id=$1 ORDER BY started_at DESC`, id)
	if err != nil {
		return respond.DBError(c, err)
	}
	execs := []fiber.Map{}
	if rows3 != nil {
		defer rows3.Close()
		for rows3.Next() {
			var at, st string
			var att int
			if err := rows3.Scan(&at, &st, &att); err != nil {
				return respond.DBError(c, err)
			}
			execs = append(execs, fiber.Map{"agent_type": at, "status": st, "attempt": att})
		}
	}
	return respond.OK(c, fiber.Map{
		"id": id, "project_id": projectID, "title": title, "status": status,
		"confidence": confidence, "action_required": string(actionRequired),
		"commit_sha": commitSHA, "forge_attempt": forgeAttempt, "branch": branch,
		"target_iteration_id": targetIter,
		"code_changes": codeChanges, "test_results": testResults, "agent_executions": execs,
	})
}

func listTasks(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	projectID := c.Query("project_id")
	if projectID == "" {
		return respond.InvalidParam(c, "project_id", "project_id required")
	}
	status := c.Query("status")
	page, pageSize := respond.ParsePage(c)
	ctx := context.Background()
	if err := AuthorizeProject(c, projectID, "task.read"); err != nil {
		return err
	}
	ReapStaleTasks(ctx, projectID) // 看板入口顺带清扫本项目的僵尸 RUNNING
	where := ` WHERE project_id=$1`
	args := []any{projectID}
	if status != "" {
		where += ` AND status=$2`
		args = append(args, strings.ToUpper(status))
	}
	var total int64
	if err := model.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM tasks`+where, args...).Scan(&total); err != nil {
		return respond.DBError(c, err)
	}
	query := `SELECT id, title, status, priority, estimated_hours, target_iteration_id FROM tasks` + where +
		fmt.Sprintf(` ORDER BY priority DESC, created_at ASC LIMIT %d OFFSET %d`, pageSize, (page-1)*pageSize)
	rows, err := model.Pool.Query(ctx, query, args...)
	if err != nil {
		return respond.DBError(c, err)
	}
	defer rows.Close()
	items := []fiber.Map{}
	for rows.Next() {
		var id, title, st string
		var prio int
		var est *float64
		var iterID *string
		if err := rows.Scan(&id, &title, &st, &prio, &est, &iterID); err != nil {
			return respond.DBError(c, err)
		}
		// deps
		deps := []string{}
		r2, _ := model.Pool.Query(ctx, `SELECT depends_on FROM task_dependencies WHERE task_id=$1`, id)
		if r2 != nil {
			for r2.Next() {
				var d string
				_ = r2.Scan(&d)
				deps = append(deps, d)
			}
			r2.Close()
		}
		items = append(items, fiber.Map{"id": id, "title": title, "status": st, "priority": prio, "estimated_hours": est, "depends_on": deps, "target_iteration_id": iterID})
	}
	return respond.List(c, items, respond.PageMeta{Page: page, PageSize: pageSize, Total: total})
}

func batchCreateTasks(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	var reqs []createTaskReq
	if err := c.BodyParser(&reqs); err != nil {
		return respond.BadRequest(c, err.Error())
	}
	if len(reqs) == 0 {
		return respond.BadRequest(c, "empty batch")
	}
	if len(reqs) > 50 {
		return respond.BusinessRule(c, "BATCH_TOO_LARGE", "batch too large (max 50)")
	}
	results := []fiber.Map{}
	for i, req := range reqs {
		if req.Title == "" {
			results = append(results, fiber.Map{"index": i, "error": fiber.Map{"code": "INVALID_PARAM", "detail": "title required"}})
			continue
		}
		if req.ProjectID == "" {
			results = append(results, fiber.Map{"index": i, "error": fiber.Map{"code": "INVALID_PARAM", "detail": "project_id required"}})
			continue
		}
		projectID := req.ProjectID
		if err := AuthorizeProject(c, projectID, "task.write"); err != nil {
			results = append(results, fiber.Map{"index": i, "error": fiber.Map{"code": "FORBIDDEN", "detail": "not a project member or role not allowed"}})
			continue
		}
		// validate depends_on
		valid := true
		for _, dep := range req.DependsOn {
			if !idgen.IsValid(dep) {
				results = append(results, fiber.Map{"index": i, "error": fiber.Map{"code": "INVALID_PARAM", "detail": "invalid depends_on: " + dep}})
				valid = false
				break
			}
			var cnt int
			if err := model.Pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM tasks WHERE id=$1 AND project_id=$2`, dep, projectID).Scan(&cnt); err != nil {
				results = append(results, fiber.Map{"index": i, "error": fiber.Map{"code": "DB_ERROR", "detail": err.Error()}})
				valid = false
				break
			}
			if cnt == 0 {
				results = append(results, fiber.Map{"index": i, "error": fiber.Map{"code": "INVALID_PARAM", "detail": "depends_on not found: " + dep}})
				valid = false
				break
			}
		}
		if !valid {
			continue
		}
		taskID := idgen.New()
		ctx := context.Background()
		priority := 0
		if req.Priority != nil {
			priority = *req.Priority
		}
		var est any
		if req.EstimatedHours != nil {
			est = *req.EstimatedHours
		}
		var iterID any = nullableUUID(req.TargetIterationID)
		if req.TargetIterationID != "" && !idgen.IsValid(req.TargetIterationID) {
			results = append(results, fiber.Map{"index": i, "error": fiber.Map{"code": "INVALID_PARAM", "detail": "invalid target_iteration_id"}})
			continue
		}
		_, err := model.Pool.Exec(ctx, `INSERT INTO tasks (id, project_id, spec_id, title, description, status, priority, estimated_hours, assigned_to, target_iteration_id) VALUES ($1,$2,$3,$4,$5,'PENDING',$6,$7,$8,$9)`, taskID, projectID, nullableUUID(req.SpecID), req.Title, req.Description, priority, est, nullableUUID(req.AssignedTo), iterID)
		if err != nil {
			results = append(results, fiber.Map{"index": i, "error": fiber.Map{"code": "DB_ERROR", "detail": err.Error()}})
			continue
		}
		for _, dep := range req.DependsOn {
			if _, err := model.Pool.Exec(ctx, `INSERT INTO task_dependencies (task_id, depends_on) VALUES ($1,$2)`, taskID, dep); err != nil {
				results = append(results, fiber.Map{"index": i, "error": fiber.Map{"code": "DB_ERROR", "detail": err.Error()}})
				break
			}
		}
		results = append(results, fiber.Map{"index": i, "id": taskID, "status": "PENDING", "title": req.Title})
	}
	return respond.OK(c, fiber.Map{"items": results})
}

func getDag(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	projectID := c.Params("id")
	if !idgen.IsValid(projectID) {
		return respond.InvalidParam(c, "id", "invalid project id")
	}
	if err := AuthorizeProject(c, projectID, "dag.read"); err != nil {
		return err
	}
	ctx := context.Background()
	rows, err := model.Pool.Query(ctx, `SELECT id, title, status FROM tasks WHERE project_id=$1`, projectID)
	if err != nil {
		return respond.DBError(c, err)
	}
	defer rows.Close()
	nodes := []fiber.Map{}
	for rows.Next() {
		var id, title, st string
		if err := rows.Scan(&id, &title, &st); err != nil {
			return respond.DBError(c, err)
		}
		nodes = append(nodes, fiber.Map{"id": id, "title": title, "status": st})
	}
	rows2, err := model.Pool.Query(ctx, `SELECT task_id, depends_on FROM task_dependencies WHERE task_id IN (SELECT id FROM tasks WHERE project_id=$1)`, projectID)
	if err != nil {
		return respond.DBError(c, err)
	}
	defer rows2.Close()
	edges := []fiber.Map{}
	for rows2.Next() {
		var tid, dep string
		if err := rows2.Scan(&tid, &dep); err != nil {
			return respond.DBError(c, err)
		}
		edges = append(edges, fiber.Map{"from": dep, "to": tid})
	}
	// compute ready: tasks with no pending deps (all deps PASSED)
	ready := []string{}
	for _, n := range nodes {
		id := n["id"].(string)
		// check if all depends_on are PASSED
		var cnt int
		_ = model.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM task_dependencies d JOIN tasks t ON t.id=d.depends_on WHERE d.task_id=$1 AND t.status NOT IN ('PASSED','RELEASE_PENDING','RELEASED')`, id).Scan(&cnt)
		if cnt == 0 {
			// also only PENDING tasks are ready
			if n["status"] == "PENDING" {
				ready = append(ready, id)
			}
		}
	}
	return respond.OK(c, fiber.Map{"nodes": nodes, "edges": edges, "ready": ready})
}

func hasCycle(ctx context.Context, projectID, from, target string) bool {
	// check if from can reach target via dependencies (DFS)
	visited := map[string]bool{}
	stack := []string{from}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if visited[cur] {
			continue
		}
		visited[cur] = true
		if cur == target {
			return true
		}
		rows, err := model.Pool.Query(ctx, `SELECT depends_on FROM task_dependencies WHERE task_id=$1`, cur)
		if err != nil {
			continue
		}
		for rows.Next() {
			var dep string
			_ = rows.Scan(&dep)
			if !visited[dep] {
				stack = append(stack, dep)
			}
		}
		rows.Close()
	}
	return false
}

func resolveBaseBranch(ctx context.Context, projectID, targetIterID string) string {
	if targetIterID != "" && idgen.IsValid(targetIterID) {
		var b *string
		_ = model.Pool.QueryRow(ctx, `SELECT branch FROM iterations WHERE id=$1`, targetIterID).Scan(&b)
		if b != nil && *b != "" {
			return *b
		}
	}
	return ProjectDefaultBranch(ctx, projectID)
}

func approveTask(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	id := c.Params("id")
	var body struct {
		Action  string `json:"action"`
		Comment string `json:"comment"`
	}
	if len(c.Body()) > 0 {
		if err := c.BodyParser(&body); err != nil {
			return respond.BadRequest(c, err.Error())
		}
	}
	if body.Action == "" {
		body.Action = "approve"
	}
	ctx := context.Background()
	var pid, curStatus string
	if err := model.Pool.QueryRow(ctx, `SELECT project_id::text, status FROM tasks WHERE id=$1`, id).Scan(&pid, &curStatus); err != nil {
		if err == pgx.ErrNoRows {
			return respond.NotFound(c, "task")
		}
		return respond.DBError(c, err)
	}
	if err := AuthorizeProject(c, pid, "task.approve"); err != nil {
		return err
	}
	// A5 审计加固：状态机守卫——仅 PENDING/FAILED/ESCALATED 可进入 RUNNING，
	// 保护 PASSED/RELEASE_PENDING/RELEASED 的发布不可变性
	switch curStatus {
	case "PENDING", "FAILED", "ESCALATED":
	default:
		return respond.BusinessRule(c, "INVALID_STATE", "cannot approve task in state "+curStatus)
	}
	// S4-0 分支：生成 forge/task-<short>-a<attempt>，每 approve 新建分支
	branch := fmt.Sprintf("forge/task-%s-a1", shortID(id))
	if _, err := model.Pool.Exec(ctx, `UPDATE tasks SET status='RUNNING', branch=$2, forge_attempt=1, updated_at=now() WHERE id=$1`, id, branch); err != nil {
		return respond.DBError(c, err)
	}
	username, _ := c.Locals("username").(string)
	if username == "" {
		username = "anonymous"
	}
	logAudit(ctx, pid, username, "task.approve", "task", id, fmt.Sprintf(`{"action":%q,"branch":%q}`, body.Action, branch))
	// P1 SSE：状态流转脏通知（Stepper ①→②）
	notifyTask(ctx, id, "status", map[string]any{"status": "RUNNING", "actor": username, "branch": branch})
	// async forge via asynq (fallback direct if redis unavailable)
	if err := queue.EnqueueForge(ctx, id); err != nil {
		go RunForge(ctx, id)
	}
	return respond.OK(c, fiber.Map{"id": id, "status": "RUNNING", "branch": branch})
}

func patchTask(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	id := c.Params("id")
	var body struct {
		Priority            *int     `json:"priority"`
		EstimatedHours      *float64 `json:"estimated_hours"`
		Status              *string  `json:"status"`
		TargetIterationID   *string  `json:"target_iteration_id"`
	}
	if err := c.BodyParser(&body); err != nil {
		return respond.BadRequest(c, err.Error())
	}
	ctx := context.Background()
	var pid string
	if err := model.Pool.QueryRow(ctx, `SELECT project_id::text FROM tasks WHERE id=$1`, id).Scan(&pid); err != nil {
		if err == pgx.ErrNoRows {
			return respond.NotFound(c, "task")
		}
		return respond.DBError(c, err)
	}
	if err := AuthorizeProject(c, pid, "task.write"); err != nil {
		return err
	}
	if body.Priority != nil {
		if _, err := model.Pool.Exec(ctx, `UPDATE tasks SET priority=$1, updated_at=now() WHERE id=$2`, *body.Priority, id); err != nil {
			return respond.DBError(c, err)
		}
	}
	if body.EstimatedHours != nil {
		if _, err := model.Pool.Exec(ctx, `UPDATE tasks SET estimated_hours=$1, updated_at=now() WHERE id=$2`, *body.EstimatedHours, id); err != nil {
			return respond.DBError(c, err)
		}
	}
	if body.TargetIterationID != nil {
		val := strings.TrimSpace(*body.TargetIterationID)
		if val == "" {
			if _, err := model.Pool.Exec(ctx, `UPDATE tasks SET target_iteration_id=NULL, updated_at=now() WHERE id=$1`, id); err != nil {
				return respond.DBError(c, err)
			}
		} else {
			if !idgen.IsValid(val) {
				return respond.InvalidParam(c, "target_iteration_id", "invalid iteration id")
			}
			// verify same project
			var cnt int
			if err := model.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM iterations WHERE id=$1 AND project_id=$2`, val, pid).Scan(&cnt); err != nil {
				return respond.DBError(c, err)
			}
			if cnt == 0 {
				return respond.InvalidParam(c, "target_iteration_id", "iteration not found or wrong project")
			}
			if _, err := model.Pool.Exec(ctx, `UPDATE tasks SET target_iteration_id=$1, updated_at=now() WHERE id=$2`, val, id); err != nil {
				return respond.DBError(c, err)
			}
		}
	}
	if body.Status != nil {
		// only allow PENDING->RUNNING via drag (which triggers forge); other manual status changes forbidden
		if *body.Status == "RUNNING" {
			var cur string
			if err := model.Pool.QueryRow(ctx, `SELECT status FROM tasks WHERE id=$1`, id).Scan(&cur); err != nil {
				return respond.DBError(c, err)
			}
			switch cur {
			case "PENDING", "FAILED", "ESCALATED":
			default:
				return respond.BusinessRule(c, "INVALID_STATE", "cannot start task in state "+cur)
			}
			branch := fmt.Sprintf("forge/task-%s-a1", shortID(id))
			if _, err := model.Pool.Exec(ctx, `UPDATE tasks SET status='RUNNING', branch=$2, forge_attempt=1, updated_at=now() WHERE id=$1`, id, branch); err != nil {
				return respond.DBError(c, err)
			}
			username, _ := c.Locals("username").(string)
			if username == "" {
				username = "anonymous"
			}
			logAudit(ctx, pid, username, "task.patch.status", "task", id, fmt.Sprintf(`{"status":"RUNNING","branch":%q}`, branch))
			notifyTask(ctx, id, "status", map[string]any{"status": "RUNNING", "actor": username, "branch": branch})
			if err := queue.EnqueueForge(ctx, id); err != nil {
				go RunForge(ctx, id)
			}
		}
	}
	return respond.OK(c, fiber.Map{"id": id})
}

func RunForge(ctx context.Context, taskID string) {
	runForge(ctx, taskID)
}

// stackConstraints 从 projects.stack（project.yaml 缓存）生成 Agent 约束（v2.1 多栈）；
// 缺失/非法时回退 go（历史仓兼容）。
func stackConstraints(ctx context.Context, taskID string) map[string]any {
	out := map[string]any{"language": "go"}
	if model.Pool == nil {
		return out
	}
	var raw []byte
	if err := model.Pool.QueryRow(ctx, `
		SELECT COALESCE(p.stack,'{}'::jsonb) FROM tasks t JOIN projects p ON p.id=t.project_id WHERE t.id=$1`,
		taskID).Scan(&raw); err != nil {
		return out
	}
	var s struct {
		Stack struct {
			Backend struct {
				Lang      string `json:"lang"`
				Framework string `json:"framework"`
				Runtime   string `json:"runtime"`
			} `json:"backend"`
			Frontend struct {
				Framework string `json:"framework"`
				Bundler   string `json:"bundler"`
			} `json:"frontend"`
		} `json:"stack"`
		Test struct {
			Unit string `json:"unit"`
		} `json:"test"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return out
	}
	if s.Stack.Backend.Lang != "" {
		out["language"] = s.Stack.Backend.Lang
	}
	if s.Stack.Backend.Framework != "" {
		out["framework"] = s.Stack.Backend.Framework
	}
	if s.Stack.Backend.Runtime != "" {
		out["runtime"] = s.Stack.Backend.Runtime
	}
	if s.Stack.Frontend.Framework != "" {
		out["frontend"] = s.Stack.Frontend.Framework
	}
	if s.Test.Unit != "" {
		out["test_unit"] = s.Test.Unit
	}
	return out
}

// releaseTask 发布签核（A5+S4-0）：RELEASE_PENDING → merge branch→ RELEASED，仅 Architect/Admin（task.release）
func releaseTask(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	id := c.Params("id")
	ctx := context.Background()
	var pid, status string
	var branch *string
	var projectRepo string
	if err := model.Pool.QueryRow(ctx, `SELECT project_id::text, status, branch FROM tasks WHERE id=$1`, id).Scan(&pid, &status, &branch); err != nil {
		if err == pgx.ErrNoRows {
			return respond.NotFound(c, "task")
		}
		return respond.DBError(c, err)
	}
	if err := AuthorizeProject(c, pid, "task.release"); err != nil {
		return err
	}
	if status != "RELEASE_PENDING" && status != "PASSED" {
		return respond.BusinessRule(c, "NOT_RELEASE_PENDING", "only RELEASE_PENDING tasks can be released (current: "+status+")")
	}
	// S4-0：若存在 forge 分支，尝试 merge 到项目集成分支（default_branch，F0.5 去 master 硬编码）
	defBranch := ProjectDefaultBranch(ctx, pid)
	var mergeSHA string
	// 回退合并的响应基底：worker 锻造成功已 merge 并把 merge sha 写入 commit_sha
	// （task.go:992）；api 侧回退段不可用时至少如实回填既有 sha（2026-09-14 实录）。
	_ = model.Pool.QueryRow(ctx, `SELECT COALESCE(commit_sha,'') FROM tasks WHERE id=$1`, id).Scan(&mergeSHA)
	if branch != nil && *branch != "" && *branch != defBranch {
		_ = model.Pool.QueryRow(ctx, `SELECT repo_full_name FROM projects WHERE id=$1`, pid).Scan(&projectRepo)
		if projectRepo != "" {
			if _, repo := forgeWorkspace(ctx, id); repo != nil {
				if sha, err := repo.MergeBranchTo(ctx, *branch, defBranch); err != nil {
					slog.Warn("release merge failed", "task_id", id, "branch", *branch, "err", err.Error())
					return respond.Upstream(c, "MERGE_FAILED", err)
				} else {
					mergeSHA = sha
					// tag forja/<task>-<shortSHA> 并推送
					tag := fmt.Sprintf("forja/%s-%s", shortID(id), shortID(sha))
					if _, err := repo.CreateTag(ctx, tag, sha); err != nil {
						slog.Warn("release tag failed", "task_id", id, "tag", tag, "err", err.Error())
					} else {
						_, _ = model.Pool.Exec(ctx, `UPDATE tasks SET commit_sha=$2 WHERE id=$1`, id, sha)
					}
				}
			}
		}
	}
	if _, err := model.Pool.Exec(ctx, `UPDATE tasks SET status='RELEASED', updated_at=now() WHERE id=$1`, id); err != nil {
		return respond.DBError(c, err)
	}
	username, _ := c.Locals("username").(string)
	branchVal := ""
	if branch != nil {
		branchVal = *branch
	}
	logAudit(ctx, pid, username, "task.release", "task", id, fmt.Sprintf(`{"branch":%q,"merge_sha":%q}`, branchVal, mergeSHA))
	return respond.OK(c, fiber.Map{"id": id, "status": "RELEASED", "branch": branchVal, "merge_sha": mergeSHA})
}

// revertTask 回滚已发布（S4-0）：RELEASED → revert merge commit → FAILED
func revertTask(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	id := c.Params("id")
	ctx := context.Background()
	var pid, status string
	var commitSHA *string
	if err := model.Pool.QueryRow(ctx, `SELECT project_id::text, status, commit_sha FROM tasks WHERE id=$1`, id).Scan(&pid, &status, &commitSHA); err != nil {
		if err == pgx.ErrNoRows {
			return respond.NotFound(c, "task")
		}
		return respond.DBError(c, err)
	}
	if err := AuthorizeProject(c, pid, "task.release"); err != nil {
		return err
	}
	if status != "RELEASED" {
		return respond.BusinessRule(c, "NOT_RELEASED", "only RELEASED tasks can be reverted (current: "+status+")")
	}
	sha := ""
	if commitSHA != nil {
		sha = *commitSHA
	}
	if sha == "" {
		return respond.BusinessRule(c, "NO_COMMIT_SHA", "no commit_sha to revert")
	}
	if _, repo := forgeWorkspace(ctx, id); repo != nil {
		if newSHA, err := repo.RevertCommitTo(ctx, sha, ProjectDefaultBranch(ctx, pid)); err != nil {
			slog.Warn("revert failed", "task_id", id, "sha", sha, "err", err.Error())
			return respond.Upstream(c, "REVERT_FAILED", err)
		} else {
			_, _ = model.Pool.Exec(ctx, `UPDATE tasks SET commit_sha=$2 WHERE id=$1`, id, newSHA)
			sha = newSHA
		}
	}
	if _, err := model.Pool.Exec(ctx, `UPDATE tasks SET status='FAILED', action_required='["reverted"]', updated_at=now() WHERE id=$1`, id); err != nil {
		return respond.DBError(c, err)
	}
	username, _ := c.Locals("username").(string)
	logAudit(ctx, pid, username, "task.revert", "task", id, fmt.Sprintf(`{"revert_sha":%q}`, sha))
	return respond.OK(c, fiber.Map{"id": id, "status": "FAILED", "revert_sha": sha})
}

// rejectTask 发布打回（A5）：RELEASE_PENDING → FAILED + 意见留痕，可重新 approve 重跑
func rejectTask(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	id := c.Params("id")
	var body struct {
		Comment string `json:"comment"`
	}
	if len(c.Body()) > 0 {
		if err := c.BodyParser(&body); err != nil {
			return respond.BadRequest(c, err.Error())
		}
	}
	ctx := context.Background()
	var pid, status string
	if err := model.Pool.QueryRow(ctx, `SELECT project_id::text, status FROM tasks WHERE id=$1`, id).Scan(&pid, &status); err != nil {
		if err == pgx.ErrNoRows {
			return respond.NotFound(c, "task")
		}
		return respond.DBError(c, err)
	}
	if err := AuthorizeProject(c, pid, "task.release"); err != nil {
		return err
	}
	if status != "RELEASE_PENDING" && status != "PASSED" {
		return respond.BusinessRule(c, "NOT_RELEASE_PENDING", "only RELEASE_PENDING tasks can be rejected (current: "+status+")")
	}
	// 缺陷修复（S1.5 覆盖率测试发现）：action_required 为 JSONB，原样写入评论文本
	// 会导致 invalid json 500；与 runForge 保持一致存为字符串数组
	ar := []byte("[]")
	if body.Comment != "" {
		if b, merr := json.Marshal([]string{body.Comment}); merr == nil {
			ar = b
		}
	}
	if _, err := model.Pool.Exec(ctx, `UPDATE tasks SET status='FAILED', action_required=$2, updated_at=now() WHERE id=$1`, id, ar); err != nil {
		return respond.DBError(c, err)
	}
	username, _ := c.Locals("username").(string)
	detail := "{}"
	if body.Comment != "" {
		detail = fmt.Sprintf(`{"comment":%q}`, body.Comment)
	}
	logAudit(ctx, pid, username, "task.reject", "task", id, detail)
	return respond.OK(c, fiber.Map{"id": id, "status": "FAILED"})
}

// runForge S3.5 真实执行链：clone 项目仓 → ≤3 轮（Agent 出码→apply→RunPipeline(产物)）→push+回写。
// OpenCode 不可达 → FAILED（执行真实性，禁止 stub）；验证耗尽 → ESCALATED；通过 → push 并 RELEASE_PENDING。
func runForge(ctx context.Context, taskID string) {
	const maxAttempts = 3
	// 选炉：capacity1 单炉单容器，找不到炉时 Delay30s 重试视为暂不可用
	var projectID, targetIterID string
	var targetIterPtr *string
	_ = model.Pool.QueryRow(ctx, `SELECT project_id::text, target_iteration_id::text FROM tasks WHERE id=$1`, taskID).Scan(&projectID, &targetIterPtr)
	if targetIterPtr != nil {
		targetIterID = *targetIterPtr
	}
	baseBranch := resolveBaseBranch(ctx, projectID, targetIterID)
	// 炉选择：原子占位 busy 计数（M-0 模型注入：BaseURL+Provider+Model）
	var furnaceID, furnaceModel, furnaceProvider string
	var furnaceRow *furnace.FurnaceRow
	if fr, err := furnace.SelectAndAcquire(ctx, taskID, projectID); err == nil && fr != nil {
		furnaceID = fr.ID
		furnaceRow = fr
		furnaceModel = fr.Model
		furnaceProvider = fr.ProviderID
	} else if err != nil && err != furnace.ErrNoCapacity {
		slog.Warn("furnace select failed", "task_id", taskID, "err", err.Error())
	}
	// 适配器：若选中非默认炉则覆 BaseURL（默认炉回落 env OPENCODE_URL，便于测试 httptest 注入）
	adapter := agent.NewOpenCodeAdapter()
	if furnaceID != "" && furnaceID != "forja-default" && furnaceRow != nil {
		if furnaceRow.BaseURL != "" {
			adapter.BaseURL = furnaceRow.BaseURL
		}
		defer func() {
			_ = furnace.ReleaseFurnace(context.Background(), taskID)
		}()
	} else if furnaceID != "" {
		defer func() {
			_ = furnace.ReleaseFurnace(context.Background(), taskID)
		}()
	}

	// 任务元信息 + 产物工作副本（S3.5.1：验证对象=锻造产物）
	var title, descr string
	_ = model.Pool.QueryRow(ctx, `SELECT title, COALESCE(description,'') FROM tasks WHERE id=$1`, taskID).Scan(&title, &descr)
	_, repo := forgeWorkspace(ctx, taskID)

	var failCtx string
	attempt := 0
	// 读取已有分支（approve 时已写入），否则首轮生成
	var currentBranch string
	_ = model.Pool.QueryRow(ctx, `SELECT COALESCE(branch,'') FROM tasks WHERE id=$1`, taskID).Scan(&currentBranch)

	for attempt < maxAttempts {
		attempt++
		// S4-0：每轮 attempt 对应分支 forge/task-<short>-a<attempt>
		branch := currentBranch
		if branch == "" || attempt > 1 {
			branch = fmt.Sprintf("forge/task-%s-a%d", shortID(taskID), attempt)
			_, _ = model.Pool.Exec(ctx, `UPDATE tasks SET branch=$2, forge_attempt=$3 WHERE id=$1`, taskID, branch, attempt)
			currentBranch = branch
		}
		start := time.Now()
		slog.Info("forge attempt begin", "task_id", taskID, "attempt", attempt, "branch", branch)
		// P1 SSE：Stepper ② 开始（每轮 attempt 递增）
		notifyTask(ctx, taskID, "forge.attempt", map[string]any{"attempt": attempt, "branch": branch})
		// 每轮从远端重置副本，消除上轮半应用残留（迭代基分支化）
		if repo != nil {
			if _, err := gitops.EnsureRepoWithBranchOnBase(ctx, getenvLocal("FORGE_WORKDIR", "/tmp/forja-work"),
				getenvLocal("GITEA_URL", "http://gitea:3000"), os.Getenv("GITEA_TOKEN"), repo.FullName, branch, baseBranch); err != nil {
				forgeFinish(ctx, taskID, attempt, "FAILED", "workspace reset: "+err.Error(), "")
				if furnaceID != "" {
					_ = furnace.ReleaseFurnace(ctx, taskID)
				}
				return
			}
		}
		in := agent.AgentInput{
			TaskID: taskID, SpecRefs: []string{},
			Context:     forgeContext(ctx, taskID, title, descr, repo),
			Constraints: stackConstraints(ctx, taskID), FailContext: failCtx, Attempt: attempt,
			ModelProvider: furnaceProvider, ModelID: furnaceModel,
		}
		// 仓库清单接地：agent 必须基于真实存在的文件出 diff（空脚手架仓幻觉
		// 修改型补丁是 git apply 失败的主因，2026-09-05 审计）
		if repo != nil {
			if tree, terr := repo.FileTree(ctx); terr == nil {
				in.Context["repo_files"] = tree
			} else {
				slog.Warn("repo file tree failed", "task_id", taskID, "err", terr.Error())
			}
			// 内容接地（2026-09-15 模板仓实录）：仅给清单时 agent 会按旧模板猜内容，
			// diff 上下文不匹配致 git apply 失败；小仓注入真实内容（限额见 FileContents）。
			if contents, cerr := repo.FileContents(ctx, 60, 12*1024, 200*1024); cerr == nil && len(contents) > 0 {
				list := make([]map[string]string, 0, len(contents))
				for _, fc := range contents {
					list = append(list, map[string]string{"path": fc.FilePath, "content": fc.Content})
				}
				in.Context["repo_files_content"] = list
			} else if cerr != nil {
				slog.Warn("repo file contents failed", "task_id", taskID, "err", cerr.Error())
			}
		}
		res, err := adapter.ExecuteDetailed(ctx, in)
		// M0 残项（2026-09-12）：失败轮次的 usage 也如实入账（上游已回传时非零；
		// 仅在拿不到上游响应——建会话失败/HTTP 非 200/解析失败——时才为 0）
		usage := execUsage{tokensIn: res.TokensIn, tokensOut: res.TokensOut, costCents: int(res.Cost*100 + 0.5)}
		if usage.costCents < 0 {
			usage.costCents = 0
		}
		if errors.Is(err, agent.ErrUnavailable) {
			// 不可达不落 execution 行（S3.5.3：不得伪造执行记录；opencode 宕机由 HealthProbe 探活熔断覆盖）
			forgeFinish(ctx, taskID, attempt, "FAILED", "agent unavailable: "+err.Error(), "")
			return
		}
		if err != nil {
			failCtx = "agent error: " + err.Error()
			// agent 侧失败（超时/空输出/解析）此前无日志，失败原因只进内存 failCtx——
			// 本轮日志是唯一的排障口（2026-09-07 留言板 attempt 1 死因不可见实录）。
			slog.Warn("agent error", "task_id", taskID, "attempt", attempt, "err", err.Error())
			recordExecutionEx(ctx, taskID, furnaceID, furnaceModel, agent.AgentOutput{}, attempt, "FAILED", time.Since(start).Milliseconds(), usage)
			continue
		}
		out := res.Output
		persistChanges(ctx, taskID, out, attempt)
		slog.Info("agent output", "task_id", taskID, "attempt", attempt,
			"files", len(out.Files), "diffs", len(out.CodeChanges), "duration_ms", time.Since(start).Milliseconds())
		notifyTask(ctx, taskID, "agent.done", map[string]any{"attempt": attempt, "files": len(out.CodeChanges)})

		dir := ""
		if repo != nil {
			var changes []gitops.Change
			for _, cc := range out.CodeChanges {
				p := cc.FilePath
				// 模型常带平台仓前缀；产物仓根即项目根，剥掉已知前缀
				for _, pref := range []string{"backend/", "frontend/", "./"} {
					if strings.HasPrefix(p, pref) {
						if _, err := os.Stat(filepath.Join(repo.Dir, p)); err != nil {
							if _, err2 := os.Stat(filepath.Join(repo.Dir, strings.TrimPrefix(p, pref))); err2 == nil {
								p = strings.TrimPrefix(p, pref)
							}
						}
					}
				}
				changes = append(changes, gitops.Change{FilePath: p, Diff: cc.Diff})
			}
			if err := repo.ApplyDiffs(ctx, changes); err != nil {
				failCtx = truncateStr("apply failed: "+err.Error(), 1500)
				slog.Warn("apply failed", "task_id", taskID, "attempt", attempt, "files", len(changes), "err", failCtx)
				recordExecutionEx(ctx, taskID, furnaceID, furnaceModel, out, attempt, "FAILED", time.Since(start).Milliseconds(), usage)
				notifyTask(ctx, taskID, "forge.error", map[string]any{"attempt": attempt, "stage": "apply", "error": failCtx})
				continue
			}
			// 整文件交付（绿地首选）：files 直写，路径经 SafeJoin 防穿越
			if len(out.Files) > 0 {
				var wf []gitops.FileContent
				for _, fc := range out.Files {
					wf = append(wf, gitops.FileContent{FilePath: fc.FilePath, Content: fc.Content})
				}
				if err := repo.WriteFiles(ctx, wf); err != nil {
					failCtx = truncateStr("write files failed: "+err.Error(), 1500)
					recordExecutionEx(ctx, taskID, furnaceID, furnaceModel, out, attempt, "FAILED", time.Since(start).Milliseconds(), usage)
					notifyTask(ctx, taskID, "forge.error", map[string]any{"attempt": attempt, "stage": "write_files", "error": failCtx})
					continue
				}
			}
			dir = repo.Dir
		}

		outFiles := map[string]string{}
		for _, fc := range out.Files {
			outFiles[fc.FilePath] = fc.Content
		}
		result := validation.RunPipeline(ctx, taskID, dir, attempt, outFiles, start)
		slog.Info("validation finished", "task_id", taskID, "attempt", attempt,
			"passed", result.Passed, "duration_ms", time.Since(start).Milliseconds())
		notifyTask(ctx, taskID, "validate.result", map[string]any{
			"attempt": attempt, "passed": result.Passed,
		})
		if !result.Passed {
			failCtx = lastFailures(ctx, taskID)
			// D2 熔断归因：validation.fail 计为该炉一次 FAILED（连续 3 次触发 unhealthy）
			recordExecutionEx(ctx, taskID, furnaceID, furnaceModel, out, attempt, "FAILED", time.Since(start).Milliseconds(), usage)
			continue
		}
		if len(out.ActionRequired) > 0 { // Agent 自陈需人工介入 → 不再自动循环
			forgeFinish(ctx, taskID, attempt, "ESCALATED", strings.Join(out.ActionRequired, "; "), dir)
			return
		}
		// 通过：推送产物（有仓时）并回写 sha（S4-0 推分支非 master）
		sha := ""
		if repo != nil {
			branchPush := currentBranch
			if branchPush == "" {
				branchPush = fmt.Sprintf("forge/task-%s-a%d", shortID(taskID), attempt)
			}
			sha, err = repo.CommitAndPushBranch(ctx, fmt.Sprintf("forge(task %s): attempt %d\n\n%s", shortID(taskID), attempt, out.ChangeLog), branchPush)
			if err != nil {
				failCtx = "push failed: " + err.Error()
				recordExecutionEx(ctx, taskID, furnaceID, furnaceModel, out, attempt, "FAILED", time.Since(start).Milliseconds(), usage)
				notifyTask(ctx, taskID, "forge.error", map[string]any{"attempt": attempt, "stage": "push", "error": failCtx})
				continue
			}
			notifyTask(ctx, taskID, "forge.pushed", map[string]any{"attempt": attempt, "sha": sha})
			// 自动合入迭代分支（或 master 兼容）：pg_advisory_lock 串行化同迭代并发 merge
			if repo != nil && baseBranch != "" {
				_, _ = model.Pool.Exec(ctx, `SELECT pg_advisory_lock(hashtext($1))`, baseBranch)
				head, err := repo.MergeBranchTo(ctx, branchPush, baseBranch)
				_, _ = model.Pool.Exec(ctx, `SELECT pg_advisory_unlock(hashtext($1))`, baseBranch)
				if err != nil {
					failCtx = "merge to " + baseBranch + " failed: " + err.Error()
					recordExecutionEx(ctx, taskID, furnaceID, furnaceModel, out, attempt, "FAILED", time.Since(start).Milliseconds(), usage)
					notifyTask(ctx, taskID, "forge.error", map[string]any{"attempt": attempt, "stage": "merge", "error": failCtx})
					continue
				} else {
					sha = head
					notifyTask(ctx, taskID, "forge.merged", map[string]any{"attempt": attempt, "base_branch": baseBranch, "sha": sha})
				}
			}
		}
		// D2：验证通过记该炉一次 SUCCESS（与 FAILED 对称，成功率=SUCC/(SUCC+FAIL)）
		recordExecutionEx(ctx, taskID, furnaceID, furnaceModel, out, attempt, "SUCCESS", time.Since(start).Milliseconds(), usage)
		ar, _ := jsonMarshal(out.ActionRequired)
		_, _ = model.Pool.Exec(ctx, `UPDATE tasks SET confidence=$1, action_required=$2, commit_sha=$3, forge_attempt=$4,
			status='RELEASE_PENDING', updated_at=now() WHERE id=$5`, out.Confidence, ar, nullStr(sha), attempt, taskID)
		// P1 SSE：Stepper ⑥ 完成 → ⑦ 待签核
		slog.Info("forge release pending", "task_id", taskID, "attempt", attempt, "sha", nullStr(sha))
		metrics.ObserveForgeTerminal("passed", time.Since(start)) // S4 #4b：成功链路时长
		notifyTask(ctx, taskID, "status", map[string]any{
			"status": "RELEASE_PENDING", "attempt": attempt, "commit_sha": nullStr(sha),
		})
		return
	}
	forgeFinish(ctx, taskID, attempt, "ESCALATED", "max attempts exhausted; last: "+failCtx, "")
}

// forgeContext 组装 Agent 上下文：任务元信息 + 从工作副本读取的 spec_refs 文件内容 + §2b spec.refs/anchors (backend/internal/api/task.go:743)
func forgeContext(ctx context.Context, taskID, title, descr string, repo *gitops.Repo) map[string]any {
	c := map[string]any{"task_title": title, "task_description": descr}
	if repo == nil {
		return c
	}
	var specs []map[string]string
	rows, err := model.Pool.Query(ctx, `SELECT ref FROM task_spec_refs WHERE task_id=$1`, taskID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var ref string
			_ = rows.Scan(&ref)
			if b, err := os.ReadFile(filepath.Join(repo.Dir, ref)); err == nil {
				specs = append(specs, map[string]string{"path": ref, "content": truncateStr(string(b), 4000)})
			}
		}
	}
	if len(specs) > 0 {
		c["spec_files"] = specs
	}
	// F1.b 项目规范注入：仓根 AGENTS.md（真相在仓），截断 4KB，优先级高于全局 rules
	if b, err := os.ReadFile(filepath.Join(repo.Dir, "AGENTS.md")); err == nil && strings.TrimSpace(string(b)) != "" {
		c["project_agents"] = truncateStr(string(b), 4096)
	}
	// §2b: task 关联的 Spec 的 refs/anchors 转译为回归约束与锚点范围
	var specID string
	if err := model.Pool.QueryRow(ctx, `SELECT spec_id::text FROM tasks WHERE id=$1`, taskID).Scan(&specID); err == nil && specID != "" {
		var refsJSON, anchorsJSON json.RawMessage
		if err := model.Pool.QueryRow(ctx, `SELECT COALESCE(refs,'[]'::jsonb), COALESCE(anchors,'[]'::jsonb) FROM specs WHERE id=$1`, specID).Scan(&refsJSON, &anchorsJSON); err == nil {
			var anchors []string
			_ = json.Unmarshal(anchorsJSON, &anchors)
			if len(anchors) > 0 {
				c["anchors"] = anchors
			}
			var refs []string
			_ = json.Unmarshal(refsJSON, &refs)
			if len(refs) > 0 {
				c["spec_refs_ids"] = refs
				regression := []string{}
				for _, refID := range refs {
					var refTitle string
					var refContent json.RawMessage
					if err := model.Pool.QueryRow(ctx, `SELECT s.title, sv.content FROM specs s JOIN spec_versions sv ON sv.spec_id=s.id WHERE s.id=$1 ORDER BY sv.created_at DESC LIMIT 1`, refID).Scan(&refTitle, &refContent); err != nil {
						continue
					}
					acs := extractSpecACs(string(refContent))
					if len(acs) == 0 {
						snippet := truncateStr(string(refContent), 400)
						regression = append(regression, fmt.Sprintf("关联Spec %s (%s): %s", refID[:8], refTitle, snippet))
					} else {
						for _, ac := range acs {
							regression = append(regression, fmt.Sprintf("关联Spec %s (%s): %s", refID[:8], refTitle, ac))
						}
					}
				}
				if len(regression) > 0 {
					c["regression_constraints"] = regression
				}
			}
		}
	}
	// 现有产物树（帮助 Agent 定位）
	if entries, err := os.ReadDir(repo.Dir); err == nil {
		var tree []string
		for _, e := range entries {
			tree = append(tree, e.Name())
		}
		c["repo_root_files"] = tree
	}
	return c
}

// extractSpecACs extracts GWT from spec content (backend/internal/api/task.go:743)
func extractSpecACs(content string) []string {
	var obj map[string]any
	if err := json.Unmarshal([]byte(content), &obj); err != nil {
		return nil
	}
	raw, ok := obj["acceptance_criteria"]
	if !ok {
		return nil
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := []string{}
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		given, _ := m["given"].(string)
		when, _ := m["when"].(string)
		then, _ := m["then"].(string)
		line := strings.TrimSpace(fmt.Sprintf("Given %s When %s Then %s", strings.TrimSpace(given), strings.TrimSpace(when), strings.TrimSpace(then)))
		if strings.TrimSpace(line) != "Given  When  Then" && line != "" {
			out = append(out, line)
		}
	}
	return out
}

// forgeWorkspace 解析任务所属项目的 Gitea 仓并准备工作副本；未配置/未建仓返回 (nil,nil) 走降级路径。
func forgeWorkspace(ctx context.Context, taskID string) (string, *gitops.Repo) {
	gc := gitea.NewFromEnv()
	if gc == nil {
		slog.Warn("forge workspace unavailable: gitea client nil", "task_id", taskID)
		return "", nil
	}
	token := os.Getenv("GITEA_TOKEN")
	if token == "" {
		slog.Warn("forge workspace unavailable: GITEA_TOKEN empty", "task_id", taskID)
		return "", nil
	}
	var fullName, defBranch string
	if err := model.Pool.QueryRow(ctx, `SELECT p.repo_full_name, COALESCE(NULLIF(p.default_branch,''),'master') FROM tasks t JOIN projects p ON p.id=t.project_id WHERE t.id=$1`, taskID).Scan(&fullName, &defBranch); err != nil || fullName == "" {
		slog.Warn("forge workspace unavailable: task has no repo", "task_id", taskID)
		return "", nil
	}
	baseURL := os.Getenv("GITEA_URL")
	if baseURL == "" {
		baseURL = "http://gitea:3000"
	}
	repo, err := gitops.EnsureRepoOnBase(ctx, getenvLocal("FORGE_WORKDIR", "/tmp/forja-work"), baseURL, token, fullName, defBranch)
	if err != nil {
		slog.Warn("forge workspace unavailable: ensure repo failed", "task_id", taskID, "repo", fullName, "err", err.Error())
		return "", nil
	}
	return repo.Dir, repo
}

// recordExecution 写真实轮次的 agent_executions（替代旧恒 SUCCESS/1）。
// 兼容入口：默认 SUCCESS、无炉归因（旧测试/调用）。
func recordExecution(ctx context.Context, taskID string, out agent.AgentOutput, attempt int) {
	recordExecutionEx(ctx, taskID, "", "", out, attempt, "SUCCESS", 0, execUsage{})
}

// execUsage 单轮用量（v1.3 token 回传）：上游 info.tokens/cost，cost 已折分为分。
type execUsage struct {
	tokensIn  int
	tokensOut int
	costCents int
}

// recordExecutionEx D2 熔断归因版：status 仅 SUCCESS/FAILED；furnaceID 为空则 furnace_id 置 NULL。
// FAILED 且 furnaceID 非空时触发 checkFurnaceBreaker（连续 3 次 FAILED→unhealthy）。
func recordExecutionEx(ctx context.Context, taskID, furnaceID, modelName string, out agent.AgentOutput, attempt int, status string, durationMs int64, usage execUsage) {
	if model.Pool == nil {
		return
	}
	if status != "SUCCESS" && status != "FAILED" {
		status = "SUCCESS"
	}
	outJSON, _ := json.Marshal(out)
	inJSON, _ := json.Marshal(map[string]any{"attempt": attempt})
	var fid any
	if furnaceID != "" {
		fid = furnaceID
	}
	var m any
	if modelName != "" {
		m = modelName
	}
	_, _ = model.Pool.Exec(ctx, `INSERT INTO agent_executions (id, task_id, furnace_id, agent_type, input, output, status, attempt, model, duration_ms, tokens_in, tokens_out, cost_cents, started_at, finished_at)
		VALUES ($1,$2,$3,'opencode',$4,$5,$6,$7,$8,$9,$10,$11,$12,now(),now())`, idgen.New(), taskID, fid, string(inJSON), string(outJSON), status, attempt, m, durationMs, usage.tokensIn, usage.tokensOut, usage.costCents)
	if status == "FAILED" && furnaceID != "" {
		checkFurnaceBreaker(ctx, furnaceID)
	}
}

// checkFurnaceBreaker D2 熔断（v1.3-D3 改走 breakerStreak 窗口）：窗口内连续 3 次 FAILED → unhealthy 降权。
// 成功 1 次即由 HealthProbe 回 healthy（pool.go），此处只降不升；forja-default 同样适用。
// 手动 recover 置 breaker_reset_at 后窗口清零，需重新攒 3 连败才再熔断。
func checkFurnaceBreaker(ctx context.Context, furnaceID string) {
	if model.Pool == nil || furnaceID == "" {
		return
	}
	if breakerStreak(ctx, furnaceID) < 3 {
		return
	}
	var cur string
	_ = model.Pool.QueryRow(ctx, `SELECT status FROM furnaces WHERE id=$1`, furnaceID).Scan(&cur)
	if cur != "healthy" {
		return
	}
	_, _ = model.Pool.Exec(ctx, `UPDATE furnaces SET status='unhealthy', updated_at=NOW() WHERE id=$1`, furnaceID)
	slog.Warn("furnace breaker tripped: 3 consecutive FAILED, marked unhealthy", "furnace_id", furnaceID)
}

// forgeFinish 终态收口：FAILED/ESCALATED 附原因（action_required 为 JSONB，需编码）与轮次。
func forgeFinish(ctx context.Context, taskID string, attempt int, status, reason, dir string) {
	ar, _ := jsonMarshal([]string{reason})
	_, _ = model.Pool.Exec(ctx, `UPDATE tasks SET status=$1, action_required=$2, forge_attempt=$3, updated_at=now() WHERE id=$4`,
		status, ar, attempt, taskID)
	slog.Warn("forge finished", "task_id", taskID, "attempt", attempt, "status", status, "reason", reason)
	metrics.ObserveForgeTerminal(strings.ToLower(status), 0) // S4 #4b：failed/escalated 计数
	// P1 SSE：终态（FAILED/ESCALATED）——Stepper 失败标记 + 重试入口可用
	notifyTask(ctx, taskID, "status", map[string]any{
		"status": status, "attempt": attempt, "reason": reason,
	})
}

// ReapStaleTasks 任务僵尸自愈：RUNNING 超时判 FAILED（对齐 pipeline_runs/
// ReapStaleDeployments 语义）。forge asynq timeout 30m（queue.TypeForge），
// tasks.updated_at 仅在启动/终态写入、无心跳，超 2× 仍无更新必属进程崩溃或
// handler 被超时杀后无人收口 → 永久 RUNNING。projectID 非空按项目隔离；
// 脏 project_id 直接拒绝，避免退化为误清全局。挂 worker 周期与看板 listTasks 入口。
func ReapStaleTasks(ctx context.Context, projectID ...string) {
	pid := ""
	if len(projectID) > 0 {
		pid = projectID[0]
	}
	if model.Pool == nil {
		return
	}
	if pid != "" && !idgen.IsValid(pid) {
		slog.Warn("tasks reap invalid project_id", "project_id", pid)
		return
	}
	ar, _ := jsonMarshal([]string{"stale RUNNING reaped (no update > 60m)"})
	var tag interface{ RowsAffected() int64 }
	var err error
	if pid != "" {
		tag, err = model.Pool.Exec(ctx, `
			UPDATE tasks SET status='FAILED', action_required=$2, updated_at=now()
			WHERE project_id=$1::text AND status='RUNNING'
			  AND updated_at < now()-interval '60 minutes'`, pid, ar)
	} else {
		// $1 必须被引用：缺失参数占位曾致 "could not determine data type of parameter $1"（42P18，生产日志实录）
		tag, err = model.Pool.Exec(ctx, `
			UPDATE tasks SET status='FAILED', action_required=$1, updated_at=now()
			WHERE status='RUNNING' AND updated_at < now()-interval '60 minutes'`, ar)
	}
	if err != nil {
		slog.Warn("tasks reap failed", "err", err.Error())
	} else if tag.RowsAffected() > 0 {
		slog.Warn("tasks reaped stale RUNNING", "count", tag.RowsAffected())
	}
}

// StartTaskReaper 10m 周期清扫全局僵尸 RUNNING 任务（worker 侧兜底，无须等看板访问）。
func StartTaskReaper(ctx context.Context) {
	go func() {
		t := time.NewTicker(10 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				ReapStaleTasks(ctx)
			}
		}
	}()
}

// persistChanges 每轮产物入库（静态扫描与详情页数据源）。
func persistChanges(ctx context.Context, taskID string, out agent.AgentOutput, attempt int) {
	for _, cc := range out.CodeChanges {
		_, _ = model.Pool.Exec(ctx, `INSERT INTO code_changes (id, task_id, file_path, diff, change_log, attempt) VALUES ($1,$2,$3,$4,$5,$6)`,
			idgen.New(), taskID, cc.FilePath, cc.Diff, out.ChangeLog, attempt)
	}
	// 整文件交付（ab671bb）：files 无 diff，但必须落 code_changes 行——
	// 否则 validation 的 hasChanges 判定盲区（只扫 code_changes 表）会跳过
	// scratch/vet/test 全部门禁并在无 go 时误判 Passed（2026-09-07 留言板实测）。
	// diff 存空串：静态门禁扫空串无命中，安全。
	for _, fc := range out.Files {
		_, _ = model.Pool.Exec(ctx, `INSERT INTO code_changes (id, task_id, file_path, diff, change_log, attempt) VALUES ($1,$2,$3,$4,$5,$6)`,
			idgen.New(), taskID, fc.FilePath, "", out.ChangeLog, attempt)
	}
}

// lastFailures 取最近失败的验证记录作为重试上下文（test_cases.name / test_results.output）。
func lastFailures(ctx context.Context, taskID string) string {
	if model.Pool == nil {
		return "validation failed (details unavailable)"
	}
	rows, err := model.Pool.Query(ctx, `SELECT tc.name||': '||COALESCE(tr.output,'') FROM test_results tr
		JOIN test_cases tc ON tc.id=tr.test_case_id
		WHERE tc.task_id=$1 AND tr.status='FAILED' ORDER BY tr.created_at DESC LIMIT 5`, taskID)
	if err != nil {
		return "validation failed (details unavailable)"
	}
	defer rows.Close()
	var parts []string
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		parts = append(parts, s)
	}
	if len(parts) == 0 {
		return "validation failed"
	}
	return truncateStr(strings.Join(parts, "\n"), 1500)
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}

// nullStr 空串转 SQL NULL（commit_sha 可空）。
func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func jsonMarshal(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("[]"), err
	}
	return b, nil
}

func nullableUUID(s string) any {
	if s == "" {
		return nil
	}
	if !idgen.IsValid(s) {
		return nil
	}
	return s
}

func convergeTask(c *fiber.Ctx) error {
	id := c.Params("id")
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	var pid string
	if err := model.Pool.QueryRow(context.Background(), `SELECT project_id::text FROM tasks WHERE id=$1`, id).Scan(&pid); err != nil {
		if err == pgx.ErrNoRows {
			return respond.NotFound(c, "task")
		}
		return respond.DBError(c, err)
	}
	if err := AuthorizeProject(c, pid, "task.run"); err != nil {
		return err
	}
	report, err := converge.Run(context.Background(), id)
	if err != nil {
		return respond.Upstream(c, "CONVERGE_FAILED", err)
	}
	// S2/B1：收敛成功即回写演进链——为关联 spec 最新版本增量向量化（异步，不阻塞响应）
	go func() {
		var svID, content string
		if err := model.Pool.QueryRow(context.Background(),
			`SELECT sv.id, sv.content::text FROM tasks t JOIN spec_versions sv ON sv.spec_id=t.spec_id
			 WHERE t.id=$1 ORDER BY sv.created_at DESC LIMIT 1`, id).Scan(&svID, &content); err == nil && svID != "" {
			_ = vector.StoreSpecEmbedding(context.Background(), svID, content)
		}
	}()
	return respond.OK(c, report)
}

// SSE 伪流已由 sse.go streamTaskLive 替换（P1 真 SSE，observability-design §4）；
// 本文件保留 notifyTask 发布辅助供状态流转埋点使用。

// notifyTask 任务事件双通道发布：task:{id}（详情页 Stepper）+ project:{pid}（徽章/看板）
func notifyTask(ctx context.Context, taskID, name string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	data["task_id"] = taskID
	var pid string
	if err := model.Pool.QueryRow(ctx, `SELECT project_id::text FROM tasks WHERE id=$1`, taskID).Scan(&pid); err == nil && pid != "" {
		events.Default.Publish("project:"+pid, name, data)
	}
	events.Default.Publish("task:"+taskID, name, data)
}
