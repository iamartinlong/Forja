package api

// 可观测性 P2：Spec 生命周期聚合端点（observability-design.md §6.3）
//
//	GET /specs/:id/lifecycle
//
// 单次往返返回生命周期页全部数据源，Stepper 状态由前端聚合推导，
// 不新增状态字段（避免状态漂移）。授权走 task.read——页面本质是
// Spec↔Task↔CI 跨域视图，与 /pipelines 查询口径一致。
//
//	{
//	  "spec":         {...specs 行},
//	  "requirements": [...requirement_spec_links 反查的 Inbox 原料],
//	  "versions":     [...spec_versions 演进时间线（倒序）],
//	  "tasks":        [...spec_id 关联任务（含 commit_sha）],
//	  "code_changes": [...任务产出的代码变更],
//	  "ci_runs":      [...tasks.commit_sha 锚定的流水线 run]
//	}

import (
	"context"
	"encoding/json"
	"time"

	"forja/backend/internal/api/respond"
	"forja/backend/internal/auth"
	"forja/backend/internal/model"

	"github.com/gofiber/fiber/v2"
	idgen "forja/backend/internal/id"
	"github.com/jackc/pgx/v5"
)

func registerSpecLifecycleRoute(r fiber.Router) {
	// 字面量段 lifecycle 先于 /specs/:id 注册语义由 fiber 路由静态优先保证，
	// 与 /specs/search、/specs/timeline 同级放置
	r.Get("/specs/:id/lifecycle", auth.Require("task.read"), getSpecLifecycle)
}

func getSpecLifecycle(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	id := c.Params("id")
	if !idgen.IsValid(id) {
		return respond.InvalidParam(c, "id", "invalid spec id")
	}
	ctx := context.Background()
	var projectID string
	err := model.Pool.QueryRow(ctx,
		`SELECT project_id::text FROM specs WHERE id=$1`, id).Scan(&projectID)
	if err == pgx.ErrNoRows {
		return respond.NotFound(c, "spec")
	}
	if err != nil {
		return respond.DBError(c, err)
	}
	if err := AuthorizeProject(c, projectID, "task.read"); err != nil {
		return err
	}

	var spec struct {
		ID             string    `json:"id"`
		ProjectID      string    `json:"project_id"`
		Layer          string    `json:"layer"`
		Title          string    `json:"title"`
		Status         string    `json:"status"`
		CurrentVersion string    `json:"current_version"`
		CreatedAt      time.Time `json:"created_at"`
		UpdatedAt      time.Time `json:"updated_at"`
		Refs           []string  `json:"refs"`
		Anchors        []string  `json:"anchors"`
	}
	var refsJSON, anchorsJSON json.RawMessage
	if err := model.Pool.QueryRow(ctx, `
		SELECT id::text, project_id::text, layer, title, status,
		       COALESCE(current_version,''), created_at, updated_at,
		       COALESCE(refs,'[]'::jsonb), COALESCE(anchors,'[]'::jsonb)
		FROM specs WHERE id=$1`, id).
		Scan(&spec.ID, &spec.ProjectID, &spec.Layer, &spec.Title,
			&spec.Status, &spec.CurrentVersion, &spec.CreatedAt, &spec.UpdatedAt, &refsJSON, &anchorsJSON); err != nil {
		return respond.DBError(c, err)
	}
	spec.CreatedAt = spec.CreatedAt.UTC()
	spec.UpdatedAt = spec.UpdatedAt.UTC()
	_ = json.Unmarshal(refsJSON, &spec.Refs)
	_ = json.Unmarshal(anchorsJSON, &spec.Anchors)
	if spec.Refs == nil {
		spec.Refs = []string{}
	}
	if spec.Anchors == nil {
		spec.Anchors = []string{}
	}
	// resolved refs for UI (backend/internal/api/spec_lifecycle.go:61)
	refSpecs := []fiber.Map{}
	if len(spec.Refs) > 0 {
		validIDs := []string{}
		for _, r := range spec.Refs {
			if idgen.IsValid(r) {
				validIDs = append(validIDs, r)
			}
		}
		if len(validIDs) > 0 {
			rows, err := model.Pool.Query(ctx, `SELECT id::text, layer, title, status FROM specs WHERE id = ANY($1::text[]) AND project_id=$2`, validIDs, spec.ProjectID)
			if err == nil {
				defer rows.Close()
				for rows.Next() {
					var sid, layer, title, st string
					if err := rows.Scan(&sid, &layer, &title, &st); err == nil {
						refSpecs = append(refSpecs, fiber.Map{"id": sid, "layer": layer, "title": title, "status": st})
					}
				}
			}
		}
	}

	// Inbox 原料（requirement_spec_links 反向追溯）
	requirements := []fiber.Map{}
	reqRows, err := model.Pool.Query(ctx, `
		SELECT r.id::text, r.source_type, LEFT(r.raw_content,80), r.status, r.created_at
		FROM requirement_spec_links l JOIN requirements_inbox r ON r.id=l.requirement_id
		WHERE l.spec_id=$1 ORDER BY r.created_at`, id)
	if err != nil {
		return respond.DBError(c, err)
	}
	for reqRows.Next() {
		var rid, src, snippet, st string
		var ca time.Time
		if err := reqRows.Scan(&rid, &src, &snippet, &st, &ca); err != nil {
			reqRows.Close()
			return respond.DBError(c, err)
		}
		requirements = append(requirements, fiber.Map{
			"id": rid, "source_type": src, "snippet": snippet,
			"status": st, "created_at": ca.UTC().Format(time.RFC3339),
		})
	}
	reqRows.Close()

	// 版本演进时间线（倒序，上限 50）
	versions := []fiber.Map{}
	verRows, err := model.Pool.Query(ctx, `
		SELECT id::text, version, COALESCE(change_log,''), created_at
		FROM spec_versions WHERE spec_id=$1 ORDER BY created_at DESC LIMIT 50`, id)
	if err != nil {
		return respond.DBError(c, err)
	}
	for verRows.Next() {
		var vid, version, changeLog string
		var ca time.Time
		if err := verRows.Scan(&vid, &version, &changeLog, &ca); err != nil {
			verRows.Close()
			return respond.DBError(c, err)
		}
		versions = append(versions, fiber.Map{
			"id": vid, "version": version,
			"change_log": changeLog, "created_at": ca.UTC().Format(time.RFC3339),
		})
	}
	verRows.Close()

	// 关联任务（含锻造产物锚点）
	tasks := []fiber.Map{}
	commitSHAs := []string{}
	taskRows, err := model.Pool.Query(ctx, `
		SELECT id::text, title, status, COALESCE(commit_sha,''), forge_attempt, created_at
		FROM tasks WHERE spec_id=$1 ORDER BY created_at`, id)
	if err != nil {
		return respond.DBError(c, err)
	}
	for taskRows.Next() {
		var tid, title, st, sha string
		var attempt int
		var ca time.Time
		if err := taskRows.Scan(&tid, &title, &st, &sha, &attempt, &ca); err != nil {
			taskRows.Close()
			return respond.DBError(c, err)
		}
		tasks = append(tasks, fiber.Map{
			"id": tid, "title": title, "status": st,
			"commit_sha": sha, "forge_attempt": attempt,
			"created_at": ca.UTC().Format(time.RFC3339),
		})
		if sha != "" {
			commitSHAs = append(commitSHAs, sha)
		}
	}
	taskRows.Close()

	// 任务产出代码变更（上限 100 条防炸页）
	codeChanges := []fiber.Map{}
	if len(tasks) > 0 {
		ccRows, err := model.Pool.Query(ctx, `
			SELECT cc.task_id::text, cc.file_path, COALESCE(cc.change_log,''), cc.created_at
			FROM code_changes cc
			WHERE cc.task_id IN (SELECT id FROM tasks WHERE spec_id=$1)
			ORDER BY cc.created_at DESC LIMIT 100`, id)
		if err != nil {
			return respond.DBError(c, err)
		}
		for ccRows.Next() {
			var tid, fp, log string
			var ca time.Time
			if err := ccRows.Scan(&tid, &fp, &log, &ca); err != nil {
				ccRows.Close()
				return respond.DBError(c, err)
			}
			codeChanges = append(codeChanges, fiber.Map{
				"task_id": tid, "file_path": fp,
				"change_log": log, "created_at": ca.UTC().Format(time.RFC3339),
			})
		}
		ccRows.Close()
	}

	// CI Run：tasks.commit_sha ↔ pipeline_runs.sha 锚定（P0 UNIQUE 锚点保证同 sha 唯一事件行）
	ciRuns := []fiber.Map{}
	if len(commitSHAs) > 0 {
		runRows, err := model.Pool.Query(ctx, `
			SELECT id::text, sha, status, COALESCE(duration_ms,0), event, COALESCE(branch,''),
			       created_at::text
			FROM pipeline_runs
			WHERE sha = ANY($1) AND project_id::text=$2
			ORDER BY created_at DESC LIMIT 20`, commitSHAs, projectID)
		if err != nil {
			return respond.DBError(c, err)
		}
		for runRows.Next() {
			var rid, sha, st, ev, br, ca string
			var dur int64
			if err := runRows.Scan(&rid, &sha, &st, &dur, &ev, &br, &ca); err != nil {
				runRows.Close()
				return respond.DBError(c, err)
			}
			ciRuns = append(ciRuns, fiber.Map{
				"id": rid, "sha": sha, "status": st, "duration_ms": dur,
				"event": ev, "branch": br, "created_at": ca,
			})
		}
		runRows.Close()
	}

	return respond.OK(c, fiber.Map{
		"spec":         spec,
		"ref_specs":    refSpecs,
		"requirements": requirements,
		"versions":     versions,
		"tasks":        tasks,
		"code_changes": codeChanges,
		"ci_runs":      ciRuns,
	})
}
