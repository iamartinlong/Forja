package api

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"forja/backend/internal/api/respond"
	"forja/backend/internal/auth"
	idgen "forja/backend/internal/id"
	"forja/backend/internal/model"

	"github.com/gofiber/fiber/v2"
)

func RegisterCostRoutes(r fiber.Router) {
	r.Get("/projects/:id/cost", auth.Require("task.read"), getProjectCost)
	r.Get("/cost/report", auth.Require("task.read"), costReport)
}

// costReport R7 成本分包：GET /cost/report?group=project|period&period=day|week|month
//
//	&project_id=&from=YYYY-MM-DD&to=YYYY-MM-DD&format=csv|json
// scope：Admin 全量；普通用户仅成员项目（project_members）。
func costReport(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	group := strings.ToLower(c.Query("group", "project"))
	if group != "project" && group != "period" {
		return respond.InvalidParam(c, "group", "group must be project|period")
	}
	period := strings.ToLower(c.Query("period", "day"))
	if period != "day" && period != "week" && period != "month" {
		return respond.InvalidParam(c, "period", "period must be day|week|month")
	}
	format := strings.ToLower(c.Query("format", "json"))
	if format != "csv" && format != "json" {
		return respond.InvalidParam(c, "format", "format must be csv|json")
	}
	ctx := context.Background()

	where := ` WHERE e.id IS NOT NULL`
	args := []any{}
	if !isGlobalAdmin(c) {
		uid := currentUserID(c)
		if uid == "" {
			return respond.OK(c, fiber.Map{"group": group, "items": []fiber.Map{}, "count": 0})
		}
		args = append(args, uid)
		where += ` AND t.project_id IN (SELECT project_id FROM project_members WHERE user_id=$` + strconv.Itoa(len(args)) + `)`
	}
	if pid := strings.TrimSpace(c.Query("project_id")); pid != "" {
		if !idgen.IsValid(pid) {
			return respond.InvalidParam(c, "project_id", "invalid project_id")
		}
		args = append(args, pid)
		where += ` AND t.project_id=$` + strconv.Itoa(len(args))
	}
	if v := c.Query("from"); v != "" {
		if _, err := time.Parse("2006-01-02", v); err != nil {
			return respond.InvalidParam(c, "from", "from must be YYYY-MM-DD")
		}
		args = append(args, v)
		where += ` AND COALESCE(e.started_at, e.finished_at) >= $` + strconv.Itoa(len(args)) + `::date`
	}
	if v := c.Query("to"); v != "" {
		if _, err := time.Parse("2006-01-02", v); err != nil {
			return respond.InvalidParam(c, "to", "to must be YYYY-MM-DD")
		}
		args = append(args, v)
		where += ` AND COALESCE(e.started_at, e.finished_at) < ($` + strconv.Itoa(len(args)) + `::date + interval '1 day')`
	}

	type reportRow struct {
		Key       string
		Label     string
		Execs     int
		CostCents int
		TokensIn  int
		TokensOut int
	}
	var rows []reportRow
	switch group {
	case "project":
		r, err := model.Pool.Query(ctx,
			`SELECT p.id::text, COALESCE(p.name,p.id::text), COUNT(e.id),
			        COALESCE(SUM(e.cost_cents),0), COALESCE(SUM(e.tokens_in),0), COALESCE(SUM(e.tokens_out),0)
			 FROM projects p JOIN tasks t ON t.project_id=p.id JOIN agent_executions e ON e.task_id=t.id`+where+`
			 GROUP BY p.id,p.name ORDER BY 4 DESC`, args...)
		if err != nil {
			return respond.DBError(c, err)
		}
		defer r.Close()
		for r.Next() {
			var x reportRow
			if err := r.Scan(&x.Key, &x.Label, &x.Execs, &x.CostCents, &x.TokensIn, &x.TokensOut); err != nil {
				return respond.DBError(c, err)
			}
			rows = append(rows, x)
		}
		if err := r.Err(); err != nil {
			return respond.DBError(c, err)
		}
	case "period":
		r, err := model.Pool.Query(ctx,
			fmt.Sprintf(`SELECT to_char(date_trunc('%s', COALESCE(e.started_at, e.finished_at)), 'YYYY-MM-DD'), 'period', COUNT(e.id),
			        COALESCE(SUM(e.cost_cents),0), COALESCE(SUM(e.tokens_in),0), COALESCE(SUM(e.tokens_out),0)
			 FROM agent_executions e JOIN tasks t ON t.id=e.task_id`+where+` AND COALESCE(e.started_at, e.finished_at) IS NOT NULL
			 GROUP BY date_trunc('%s', COALESCE(e.started_at, e.finished_at)) ORDER BY date_trunc('%s', COALESCE(e.started_at, e.finished_at)) DESC`, period, period, period), args...)
		if err != nil {
			return respond.DBError(c, err)
		}
		defer r.Close()
		for r.Next() {
			var x reportRow
			if err := r.Scan(&x.Key, &x.Label, &x.Execs, &x.CostCents, &x.TokensIn, &x.TokensOut); err != nil {
				return respond.DBError(c, err)
			}
			rows = append(rows, x)
		}
		if err := r.Err(); err != nil {
			return respond.DBError(c, err)
		}
	}

	if format == "csv" {
		var sb strings.Builder
		sb.WriteString("\ufeff")
		keyHead := "项目ID,项目名"
		if group == "period" {
			keyHead = "周期(" + period + ")"
		}
		sb.WriteString(keyHead + ",执行数,成本(分),输入tokens,输出tokens\r\n")
		for _, x := range rows {
			if group == "project" {
				sb.WriteString(csvEscape(x.Key) + "," + csvEscape(x.Label))
			} else {
				sb.WriteString(csvEscape(x.Key))
			}
			sb.WriteString("," + strconv.Itoa(x.Execs) + "," + strconv.Itoa(x.CostCents) +
				"," + strconv.Itoa(x.TokensIn) + "," + strconv.Itoa(x.TokensOut) + "\r\n")
		}
		c.Set("Content-Type", "text/csv; charset=utf-8")
		c.Set("Content-Disposition", `attachment; filename="cost-report-`+group+`-`+time.Now().Format("20060102-1504")+`.csv"`)
		return c.SendString(sb.String())
	}
	out := make([]fiber.Map, 0, len(rows))
	for _, x := range rows {
		out = append(out, fiber.Map{"key": x.Key, "label": x.Label, "executions": x.Execs,
			"cost_cents": x.CostCents, "tokens_in": x.TokensIn, "tokens_out": x.TokensOut})
	}
	return respond.OK(c, fiber.Map{"group": group, "period": period, "items": out, "count": len(out)})
}

func getProjectCost(c *fiber.Ctx) error {
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
	iterationID := c.Query("iteration_id")
	if iterationID != "" && !idgen.IsValid(iterationID) {
		return respond.InvalidParam(c, "iteration_id", "invalid iteration id")
	}
	groupBy := strings.ToLower(c.Query("group_by")) // assignee | model | iteration
	ctx := context.Background()

	// verify iteration belongs to project if filtered
	if iterationID != "" {
		var cnt int
		if err := model.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM iterations WHERE id=$1 AND project_id=$2`, iterationID, projectID).Scan(&cnt); err != nil {
			return respond.DBError(c, err)
		}
		if cnt == 0 {
			return respond.InvalidParam(c, "iteration_id", "iteration not found or wrong project")
		}
	}

	// base summary
	var totalTasks, totalExecs int
	var totalCost, totalIn, totalOut int
	var avgDuration *float64
	whereIter := ""
	args := []any{projectID}
	if iterationID != "" {
		whereIter = ` AND t.target_iteration_id=$2`
		args = append(args, iterationID)
	}
	// sum across agent_executions joined tasks
	q := `SELECT COUNT(DISTINCT t.id), COUNT(e.id),
	  COALESCE(SUM(e.cost_cents),0), COALESCE(SUM(e.tokens_in),0), COALESCE(SUM(e.tokens_out),0),
	  AVG(e.duration_ms)::float
	  FROM tasks t LEFT JOIN agent_executions e ON e.task_id=t.id
	  WHERE t.project_id=$1` + whereIter
	if err := model.Pool.QueryRow(ctx, q, args...).Scan(&totalTasks, &totalExecs, &totalCost, &totalIn, &totalOut, &avgDuration); err != nil {
		return respond.DBError(c, err)
	}
	summary := fiber.Map{
		"project_id": projectID, "iteration_id": iterationID,
		"total_tasks": totalTasks, "total_executions": totalExecs,
		"cost_cents": totalCost, "tokens_in": totalIn, "tokens_out": totalOut,
		"avg_duration_ms": avgDuration,
	}

	// breakdown
	var breakdown []fiber.Map
	switch groupBy {
	case "assignee":
		rows, err := model.Pool.Query(ctx, `SELECT COALESCE(t.assigned_to,'unassigned') as k, COUNT(e.id), COALESCE(SUM(e.cost_cents),0), COALESCE(SUM(e.tokens_in),0), COALESCE(SUM(e.tokens_out),0)
		  FROM tasks t LEFT JOIN agent_executions e ON e.task_id=t.id WHERE t.project_id=$1`+whereIter+` GROUP BY k`, args...)
		if err != nil {
			return respond.DBError(c, err)
		}
		defer rows.Close()
		for rows.Next() {
			var k string
			var c1, c2, c3, c4 int
			_ = rows.Scan(&k, &c1, &c2, &c3, &c4)
			breakdown = append(breakdown, fiber.Map{"key": k, "executions": c1, "cost_cents": c2, "tokens_in": c3, "tokens_out": c4})
		}
	case "model":
		rows, err := model.Pool.Query(ctx, `SELECT COALESCE(e.model,'unknown') as k, COUNT(*), COALESCE(SUM(e.cost_cents),0), COALESCE(SUM(e.tokens_in),0), COALESCE(SUM(e.tokens_out),0)
		  FROM agent_executions e JOIN tasks t ON t.id=e.task_id WHERE t.project_id=$1`+whereIter+` GROUP BY k`, args...)
		if err != nil {
			return respond.DBError(c, err)
		}
		defer rows.Close()
		for rows.Next() {
			var k string
			var c1, c2, c3, c4 int
			_ = rows.Scan(&k, &c1, &c2, &c3, &c4)
			breakdown = append(breakdown, fiber.Map{"key": k, "executions": c1, "cost_cents": c2, "tokens_in": c3, "tokens_out": c4})
		}
	case "iteration", "":
		// per iteration breakdown when no filter
		if iterationID == "" {
			rows, err := model.Pool.Query(ctx, `SELECT COALESCE(t.target_iteration_id,'__backlog__') as k, COUNT(e.id), COALESCE(SUM(e.cost_cents),0)
			  FROM tasks t LEFT JOIN agent_executions e ON e.task_id=t.id WHERE t.project_id=$1 GROUP BY k`, projectID)
			if err != nil {
				return respond.DBError(c, err)
			}
			defer rows.Close()
			for rows.Next() {
				var k string
				var c1, c2 int
				_ = rows.Scan(&k, &c1, &c2)
				label := k
				if k == "__backlog__" {
					label = "__backlog__"
				}
				breakdown = append(breakdown, fiber.Map{"key": label, "executions": c1, "cost_cents": c2})
			}
		}
	default:
		return respond.InvalidParam(c, "group_by", "group_by must be assignee|model|iteration")
	}

	return respond.OK(c, fiber.Map{"summary": summary, "breakdown": breakdown})
}
