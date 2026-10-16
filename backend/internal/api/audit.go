package api

import (
	"context"
	"strconv"
	"strings"
	"time"

	"forja/backend/internal/api/respond"
	"forja/backend/internal/auth"
	"forja/backend/internal/model"

	"github.com/gofiber/fiber/v2"
	idgen "forja/backend/internal/id"
)

func RegisterAuditRoutes(r fiber.Router) {
	r.Get("/audit", auth.Require("audit.read"), listAudit)
	r.Get("/audit/export", auth.Require("audit.read"), exportAudit)
	r.Get("/specs/:id/trace", auth.Require("audit.read"), getTrace)
}

// auditExportCap 导出硬上限（防单次拉爆内存；超出请缩小时间范围）
const auditExportCap = 50000

// exportAudit GET /audit/export?project_id=&target_type=&action=&from=YYYY-MM-DD&to=YYYY-MM-DD&format=csv|json
// R7 治理：审计导出（默认 CSV，UTF-8 BOM 兼容 Excel）；导出行为自身写 audit.export 留痕。
func exportAudit(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	projectID := c.Query("project_id")
	if projectID == "" {
		return respond.InvalidParam(c, "project_id", "project_id required")
	}
	if err := AuthorizeProject(c, projectID, "audit.read"); err != nil {
		return err
	}
	format := strings.ToLower(c.Query("format", "csv"))
	if format != "csv" && format != "json" {
		return respond.InvalidParam(c, "format", "format must be csv|json")
	}
	ctx := context.Background()
	where := ` WHERE project_id=$1`
	args := []any{projectID}
	if v := c.Query("target_type"); v != "" {
		args = append(args, v)
		where += ` AND target_type=$` + strconv.Itoa(len(args))
	}
	if v := c.Query("action"); v != "" {
		args = append(args, v)
		where += ` AND action=$` + strconv.Itoa(len(args))
	}
	if v := c.Query("from"); v != "" {
		if _, err := time.Parse("2006-01-02", v); err != nil {
			return respond.InvalidParam(c, "from", "from must be YYYY-MM-DD")
		}
		args = append(args, v)
		where += ` AND created_at >= $` + strconv.Itoa(len(args)) + `::date`
	}
	if v := c.Query("to"); v != "" {
		if _, err := time.Parse("2006-01-02", v); err != nil {
			return respond.InvalidParam(c, "to", "to must be YYYY-MM-DD")
		}
		args = append(args, v)
		where += ` AND created_at < ($` + strconv.Itoa(len(args)) + `::date + interval '1 day')`
	}

	rows, err := model.Pool.Query(ctx,
		`SELECT actor, action, target_type, COALESCE(target_id::text,''), COALESCE(detail::text,''), created_at
		 FROM audit_logs`+where+` ORDER BY created_at DESC LIMIT `+strconv.Itoa(auditExportCap), args...)
	if err != nil {
		return respond.DBError(c, err)
	}
	defer rows.Close()

	type auditRow struct {
		Actor, Action, TargetType, TargetID, Detail string
		At                                          time.Time
	}
	var items []auditRow
	for rows.Next() {
		var r auditRow
		if err := rows.Scan(&r.Actor, &r.Action, &r.TargetType, &r.TargetID, &r.Detail, &r.At); err != nil {
			return respond.DBError(c, err)
		}
		items = append(items, r)
	}
	if err := rows.Err(); err != nil {
		return respond.DBError(c, err)
	}

	username, _ := c.Locals("username").(string)
	logAudit(ctx, projectID, username, "audit.export", "audit", "",
		`{"format":"`+format+`","rows":`+strconv.Itoa(len(items))+`}`)

	if format == "json" {
		out := make([]fiber.Map, 0, len(items))
		for _, r := range items {
			out = append(out, fiber.Map{"actor": r.Actor, "action": r.Action, "target_type": r.TargetType,
				"target_id": r.TargetID, "detail": r.Detail, "created_at": r.At.Format(time.RFC3339)})
		}
		return respond.OK(c, fiber.Map{"items": out, "count": len(out)})
	}

	var sb strings.Builder
	sb.WriteString("\ufeff") // UTF-8 BOM：Excel 直接双击不乱码
	sb.WriteString("时间,操作者,动作,目标类型,目标ID,详情\r\n")
	for _, r := range items {
		sb.WriteString(csvEscape(r.At.Format("2006-01-02 15:04:05")))
		sb.WriteString(",")
		sb.WriteString(csvEscape(r.Actor))
		sb.WriteString(",")
		sb.WriteString(csvEscape(r.Action))
		sb.WriteString(",")
		sb.WriteString(csvEscape(r.TargetType))
		sb.WriteString(",")
		sb.WriteString(csvEscape(r.TargetID))
		sb.WriteString(",")
		sb.WriteString(csvEscape(r.Detail))
		sb.WriteString("\r\n")
	}
	c.Set("Content-Type", "text/csv; charset=utf-8")
	c.Set("Content-Disposition", `attachment; filename="audit-`+projectID[:8]+`-`+time.Now().Format("20060102-1504")+`.csv"`)
	return c.SendString(sb.String())
}

// csvEscape RFC4180：含逗号/引号/换行时包裹并转义引号
func csvEscape(s string) string {
	if strings.ContainsAny(s, ",\"\n\r") {
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	return s
}

func listAudit(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	projectID := c.Query("project_id")
	if projectID == "" {
		return respond.InvalidParam(c, "project_id", "project_id required")
	}
	if err := AuthorizeProject(c, projectID, "audit.read"); err != nil {
		return err
	}
	targetType := c.Query("target_type")
	page, pageSize := respond.ParsePage(c)
	ctx := context.Background()
	where := ` WHERE project_id=$1`
	args := []any{projectID}
	if targetType != "" {
		where += ` AND target_type=$2`
		args = append(args, targetType)
	}
	var total int64
	if err := model.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM audit_logs`+where, args...).Scan(&total); err != nil {
		return respond.DBError(c, err)
	}
	query := `SELECT id, actor, action, target_type, target_id, detail, created_at FROM audit_logs` + where +
		` ORDER BY created_at DESC LIMIT $` + strconv.Itoa(len(args)+1) + ` OFFSET $` + strconv.Itoa(len(args)+2)
	args = append(args, pageSize, (page-1)*pageSize)
	rows, err := model.Pool.Query(ctx, query, args...)
	if err != nil {
		return respond.DBError(c, err)
	}
	defer rows.Close()
	items := []fiber.Map{}
	for rows.Next() {
		var id, actor, action, ttype string
		var tid *string
		var detail []byte
		var created time.Time
		if err := rows.Scan(&id, &actor, &action, &ttype, &tid, &detail, &created); err != nil {
			return respond.DBError(c, err)
		}
		items = append(items, fiber.Map{"id": id, "actor": actor, "action": action, "target_type": ttype, "target_id": tid, "detail": string(detail), "created_at": created.Format(time.RFC3339)})
	}
	return respond.List(c, items, respond.PageMeta{Page: page, PageSize: pageSize, Total: total})
}

func getTrace(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	specID := c.Params("id")
	if !idgen.IsValid(specID) {
		return respond.InvalidParam(c, "id", "invalid spec id")
	}
	ctx := context.Background()
	var projectID string
	if err := model.Pool.QueryRow(ctx, `SELECT project_id::text FROM specs WHERE id=$1`, specID).Scan(&projectID); err != nil {
		return respond.NotFound(c, "spec")
	}
	if err := AuthorizeProject(c, projectID, "audit.read"); err != nil {
		return err
	}
	// tasks linked to spec
	rows, err := model.Pool.Query(ctx, `SELECT id, title, status FROM tasks WHERE spec_id=$1`, specID)
	if err != nil {
		return respond.DBError(c, err)
	}
	defer rows.Close()
	tasks := []fiber.Map{}
	taskIDs := []string{}
	for rows.Next() {
		var id, title, st string
		if err := rows.Scan(&id, &title, &st); err != nil {
			return respond.DBError(c, err)
		}
		tasks = append(tasks, fiber.Map{"id": id, "title": title, "status": st})
		taskIDs = append(taskIDs, id)
	}
	// code changes for those tasks
	codes := []fiber.Map{}
	if len(taskIDs) > 0 {
		// use ANY
		rows2, _ := model.Pool.Query(ctx, `SELECT task_id, file_path, change_log FROM code_changes WHERE task_id = ANY($1)`, taskIDs)
		if rows2 != nil {
			defer rows2.Close()
			for rows2.Next() {
				var tid, fp, log string
				_ = rows2.Scan(&tid, &fp, &log)
				codes = append(codes, fiber.Map{"task_id": tid, "file_path": fp, "change_log": log})
			}
		}
	}
	// requirement links
	reqs := []fiber.Map{}
	rows3, _ := model.Pool.Query(ctx, `SELECT requirement_id FROM requirement_spec_links WHERE spec_id=$1`, specID)
	if rows3 != nil {
		defer rows3.Close()
		for rows3.Next() {
			var rid string
			_ = rows3.Scan(&rid)
			reqs = append(reqs, fiber.Map{"requirement_id": rid})
		}
	}
	return respond.OK(c, fiber.Map{"spec_id": specID, "tasks": tasks, "code_changes": codes, "requirements": reqs})
}

func logAudit(ctx context.Context, projectID, actor, action, targetType, targetID string, detail string) {
	if model.Pool == nil {
		return
	}
	// 系统内部写入兜底（A6）：无项目上下文或非法值时落种子项目
	if projectID == "" {
		projectID = model.DefaultProjectID
	}
	if !idgen.IsValid(projectID) {
		projectID = model.DefaultProjectID
	}
	if targetID != "" {
		if !idgen.IsValid(targetID) {
			targetID = ""
		}
	}
	var tid any = nil
	if targetID != "" {
		tid = targetID
	}
	_, _ = model.Pool.Exec(ctx, `INSERT INTO audit_logs (id, project_id, actor, action, target_type, target_id, detail) VALUES ($1,$2,$3,$4,$5,$6,$7)`, idgen.New(), projectID, actor, action, targetType, tid, detail)
}

// LogAuditPublic worker（cmd 层）定时部署审计导出入口（S6 §5）
func LogAuditPublic(ctx context.Context, projectID, actor, action, targetType, targetID string, detail string) {
	logAudit(ctx, projectID, actor, action, targetType, targetID, detail)
}
