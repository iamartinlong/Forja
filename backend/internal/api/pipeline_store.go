package api

// 可观测性 P0 落库辅助（observability-design.md §3.2）。
// webhook_gitea.go 的事件处理器调用本文件函数写 pipeline_runs / pipeline_jobs。

import (
	"context"
	"log/slog"
	"time"

	"forja/backend/internal/events"
	"forja/backend/internal/metrics"
	"forja/backend/internal/model"

	idgen "forja/backend/internal/id"
)

// expectedPipelineJobs 4 预期 job（build.yml 产出）——聚合 SUCCESS 与 preseed 共用。
var expectedPipelineJobs = []string{"backend", "frontend", "e2e", "build-push"}

// projectBuildJobs 用户项目 build.yml 单 job（模板约定）；聚合按实际 job 集动态判定。
var projectBuildJobs = []string{"build"}

// projectIDByRepo 仓库名 ↔ 项目映射（projects.repo_full_name）；未映射返回空串。
// ProjectIDByRepo 为 worker（cmd 层）定时部署审计导出的同一能力。
func projectIDByRepo(repo string) string { return ProjectIDByRepo(repo) }

func ProjectIDByRepo(repo string) string {
	if repo == "" || model.Pool == nil {
		return ""
	}
	var pid string
	if err := model.Pool.QueryRow(context.Background(),
		`SELECT id::text FROM projects WHERE repo_full_name=$1 LIMIT 1`, repo).Scan(&pid); err != nil {
		return ""
	}
	return pid
}

// projectIDByRun 读取既有 pipeline_runs 行的 project_id（多租户直连，避免 repo→default 漂移）
func projectIDByRun(ctx context.Context, runID string) string {
	if runID == "" || model.Pool == nil {
		return ""
	}
	if !idgen.IsValid(runID) {
		return ""
	}
	var pid *string
	if err := model.Pool.QueryRow(ctx, `SELECT project_id::text FROM pipeline_runs WHERE id=$1`, runID).Scan(&pid); err != nil {
		return ""
	}
	if pid == nil || *pid == "" {
		return ""
	}
	return *pid
}

// resolveDeployProjectID 部署回填时从 deployments 表最新 running 行推断归属项目
func resolveDeployProjectID(ctx context.Context, fallbackRepo string) string {
	if model.Pool != nil {
		var pid *string
		if err := model.Pool.QueryRow(ctx,
			`SELECT project_id::text FROM deployments WHERE status='running' ORDER BY created_at DESC LIMIT 1`).Scan(&pid); err == nil && pid != nil && *pid != "" {
			if idgen.IsValid(*pid) {
				return *pid
			}
		}
	}
	return projectIDByRepo(fallbackRepo)
}

// isZeroSHA 分支删除等场景 push payload 的 after 为全零串，不应建 run
func isZeroSHA(sha string) bool {
	if len(sha) == 0 {
		return true
	}
	for _, ch := range sha {
		if ch != '0' {
			return false
		}
	}
	return true
}

// isAllowedBranch 迭代分支化精确：仅集成分支与 iteration/* 计分（默认 master，项目可覆写）
func isAllowedBranch(branch string) bool { return isAllowedBranchFor(branch, "master") }

// isAllowedBranchFor F0.5：集成分支由项目 default_branch 决定（导入 main 仓不再误判 non-master）
func isAllowedBranchFor(branch, defaultBranch string) bool {
	if branch == "" {
		return false
	}
	if defaultBranch == "" {
		defaultBranch = "master"
	}
	return branch == defaultBranch || (len(branch) > 10 && branch[:10] == "iteration/")
}

func isNonMasterPush(ctx context.Context, repo, sha string) bool {
	if model.Pool == nil || repo == "" || isZeroSHA(sha) {
		return false
	}
	var branch *string
	if err := model.Pool.QueryRow(ctx, `SELECT branch FROM pipeline_runs WHERE repo=$1 AND sha=$2 AND event='push' LIMIT 1`, repo, sha).Scan(&branch); err != nil {
		return false
	}
	if branch != nil && *branch != "" {
		def := ""
		if pid := projectIDByRepo(repo); pid != "" {
			def = ProjectDefaultBranch(ctx, pid)
		}
		if !isAllowedBranchFor(*branch, def) {
			return true
		}
	}
	return false
}

// recordPushRun push → pipeline_runs upsert（UNIQUE 锚点 repo+sha+branch+event，031 后旧锚已删）。
// S6 起 push 不触发流水线——status=PENDING（待构建），由人工在交付页发起构建。
func recordPushRun(ctx context.Context, repo, sha, branch, actor string) {
	if model.Pool == nil || repo == "" || isZeroSHA(sha) {
		return
	}
	var pid any
	if v := projectIDByRepo(repo); v != "" {
		pid = v
	}
	var br any
	if branch != "" {
		br = branch
	}
	_, err := model.Pool.Exec(ctx, `
		INSERT INTO pipeline_runs (id, project_id, repo, sha, branch, event, status, actor)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, 'push', 'PENDING', NULLIF($5,''))
		ON CONFLICT (repo, sha, branch, event) DO UPDATE SET updated_at=now(), branch=EXCLUDED.branch`,
		pid, repo, sha, br, actor)
	if err != nil {
		slog.Warn("pipeline_runs upsert(push) failed", "err", err.Error())
	}
}

// recordJobCompletion workflow_job completed → 定位 run → upsert job → 聚合结论：
//
//	任一 job failure/cancelled ⇒ FAILURE；
//	4 预期 job（backend/frontend/e2e/build-push）全 success/skipped/neutral ⇒ SUCCESS；否则 RUNNING。
//
// run 定位：优先同 repo+sha 的 push/dispatch run；不存在则以 event='workflow_job' 锚点新建。
// U-3 防御：非 master 分支的 push 不应被 job 回流转 RUNNING，直接跳过
func recordJobCompletion(ctx context.Context, repo, sha, jobName, conclusion, startedAt, completedAt string) {
	if model.Pool == nil || repo == "" || isZeroSHA(sha) {
		return
	}
	if isNonMasterPush(ctx, repo, sha) {
		slog.Info("pipeline skip non-master branch job completion", "repo", repo, "sha", shortSHA(sha), "job", jobName)
		return
	}

	runID := locateRunID(ctx, repo, sha)
	if runID == "" {
		var pid any
		if v := projectIDByRepo(repo); v != "" {
			pid = v
		}
		err := model.Pool.QueryRow(ctx, `
			INSERT INTO pipeline_runs (id, project_id, repo, sha, branch, event, status, actor)
			VALUES (gen_random_uuid(), $1, $2, $3, '', 'workflow_job', 'RUNNING', 'gitea')
			ON CONFLICT (repo, sha, branch, event) DO UPDATE SET updated_at=now()
			RETURNING id::text`, pid, repo, sha).Scan(&runID)
		if err != nil {
			slog.Warn("pipeline_runs upsert(workflow_job) failed", "err", err.Error())
			return
		}
	}

	started := parseRFC3339(startedAt)
	var dur any
	if started != nil && completedAt != "" {
		if done := parseRFC3339(completedAt); done != nil && !done.Before(*started) {
			dur = done.Sub(*started).Milliseconds()
		}
	}

	if _, err := model.Pool.Exec(ctx, `
		INSERT INTO pipeline_jobs (run_id, name, conclusion, started_at, duration_ms)
		VALUES ($1, $2, NULLIF($3,''), $4, $5)
		ON CONFLICT (run_id, name) DO UPDATE
		  SET conclusion=EXCLUDED.conclusion,
		      duration_ms=COALESCE(EXCLUDED.duration_ms, pipeline_jobs.duration_ms),
		      created_at=now()`,
		runID, jobName, conclusion, started, dur); err != nil {
		slog.Warn("pipeline_jobs upsert failed", "job", jobName, "err", err.Error())
		return
	}
	aggregateRunStatus(ctx, runID)
}

// locateRunID 同 repo+sha 的既有 run（push / manual / workflow_dispatch / workflow_job 任一事件），取最新
// P0-2 多租户修复：显式 projectID 时优先精确匹配该项目行，缺失时回退全局匹配（兼容 webhook 无项目上下文）。
// 2026-09-17 排序加固：dispatch 行（manual/workflow_dispatch）与 RUNNING 行优先——
// push 回流行可能因 webhook 延迟而 created_at 更晚，导致 job 回流错挂 push 行（manual 滞留 RUNNING）。
func locateRunID(ctx context.Context, repo, sha string, projectID ...string) string {
	if len(projectID) > 0 && projectID[0] != "" {
		pid := projectID[0]
		if idgen.IsValid(pid) {
			var id string
			err := model.Pool.QueryRow(ctx, `
				SELECT id::text FROM pipeline_runs
				WHERE repo=$1 AND sha=$2 AND project_id=$3::text AND event IN ('push','manual','workflow_dispatch','workflow_job')
				ORDER BY (event IN ('manual','workflow_dispatch')) DESC, (status='RUNNING') DESC, created_at DESC LIMIT 1`, repo, sha, pid).Scan(&id)
			if err == nil {
				return id
			}
		}
	}
	var id string
	err := model.Pool.QueryRow(ctx, `
		SELECT id::text FROM pipeline_runs
		WHERE repo=$1 AND sha=$2 AND event IN ('push','manual','workflow_dispatch','workflow_job')
		ORDER BY (event IN ('manual','workflow_dispatch')) DESC, (status='RUNNING') DESC, created_at DESC LIMIT 1`, repo, sha).Scan(&id)
	if err != nil {
		return ""
	}
	return id
}

// ensureRunID 同 locateRunID，缺失时以 workflow_job 事件新建 RUNNING 行（供 progress/completed 路径共用）
// P0-2 多租户修复：显式 projectID 优先写入，回退 repo 映射。
// U-3 防御：非 master 分支不新建 RUNNING 行
func ensureRunID(ctx context.Context, repo, sha string, projectID ...string) string {
	if isNonMasterPush(ctx, repo, sha) {
		return ""
	}
	effective := ""
	if len(projectID) > 0 {
		effective = projectID[0]
	}
	runID := locateRunID(ctx, repo, sha, effective)
	if runID != "" {
		return runID
	}
	var pid any
	if effective != "" {
		if idgen.IsValid(effective) {
			pid = effective
		} else {
			slog.Warn("pipeline_runs ensureRunID invalid project_id", "project_id", effective)
		}
	}
	if pid == nil {
		if v := projectIDByRepo(repo); v != "" {
			pid = v
		}
	}
	err := model.Pool.QueryRow(ctx, `
			INSERT INTO pipeline_runs (id, project_id, repo, sha, branch, event, status, actor)
			VALUES (gen_random_uuid(), $1, $2, $3, '', 'workflow_job', 'RUNNING', 'gitea')
			ON CONFLICT (repo, sha, branch, event) DO UPDATE SET updated_at=now()
			RETURNING id::text`, pid, repo, sha).Scan(&runID)
	if err != nil {
		slog.Warn("pipeline_runs upsert(workflow_job) ensure failed", "err", err.Error())
		return ""
	}
	return runID
}

// preseedPipelineJobs 在 dispatch 成功后立即为该 sha 预播 4 行 running job，使 PipelineBoard 展开即见转圈。
// 幂等：ON CONFLICT DO NOTHING，不覆盖已落库的完成结论；started_at 取预播时刻，completed 回流再补时长。
// P0-2 多租户修复：显式 projectID 优先，事件总线发布亦用有效项目。
// U-3 防御：非 master 分支禁止 preseed
func preseedPipelineJobs(ctx context.Context, repo, sha string, projectID ...string) {
	effective := ""
	if len(projectID) > 0 {
		effective = projectID[0]
	}
	preseedJobsNamed(ctx, repo, sha, "", effective, expectedPipelineJobs)
}

// preseedProjectBuildJobs 用户项目构建（build.yml 单 job=build）专用预播：
// 不能复用 FORJA 四 job 预期，否则聚合永远停在 RUNNING（2026-09-12 M1.3 E2E 实录）。
func preseedProjectBuildJobs(ctx context.Context, repo, sha, branch, projectID string) {
	preseedJobsNamed(ctx, repo, sha, branch, projectID, projectBuildJobs)
}

// preseedJobsNamed 以指定 job 集预播 run/jobs（FORJA 四 job 或项目 build）。
func preseedJobsNamed(ctx context.Context, repo, sha, branch, projectID string, names []string) {
	if model.Pool == nil || repo == "" || isZeroSHA(sha) {
		return
	}
	if isNonMasterPush(ctx, repo, sha) {
		slog.Info("pipeline skip preseed non-master branch", "repo", repo, "sha", shortSHA(sha))
		return
	}
	runID := ensureRunID(ctx, repo, sha, projectID)
	if runID == "" {
		return
	}
	if branch != "" {
		_, _ = model.Pool.Exec(ctx, `UPDATE pipeline_runs SET branch=$1 WHERE id=$2`, branch, runID)
	}
	now := time.Now().UTC()
	for _, name := range names {
		_, err := model.Pool.Exec(ctx, `
			INSERT INTO pipeline_jobs (run_id, name, conclusion, started_at, duration_ms)
			VALUES ($1, $2, NULL, $3, NULL)
			ON CONFLICT (run_id, name) DO NOTHING`, runID, name, now)
		if err != nil {
			slog.Warn("pipeline_jobs preseed failed", "job", name, "err", err.Error())
		}
	}
	// 预播后聚合一次以 bump updated_at（防僵尸 reap），保持 RUNNING
	aggregateRunStatus(ctx, runID)
	// 脏通知：让订阅方立即刷新列表/详情
	pid := projectID
	if pid == "" {
		pid = projectIDByRepo(repo)
	}
	if pid != "" {
		events.Default.Publish("project:"+pid, "ci.progress",
			map[string]any{"repo": repo, "sha": sha, "preseed": true})
	}
}

// recordJobProgress 处理 queued / in_progress 的 workflow_job 回流：确保 run+job 行存在（结论为空/running），供实时明细展示。
// 与 completed 区分：结论未定，不触发 FAILURE 聚合，保持 RUNNING。
// P0-2 多租户修复：优先以 run 行的 project_id 发布，缺失时回退 repo 映射。
// U-3 防御：非 master 分支不落 RUNNING
func recordJobProgress(ctx context.Context, repo, sha, jobName, status, startedAt string) {
	if model.Pool == nil || repo == "" || isZeroSHA(sha) || jobName == "" {
		return
	}
	if jobName == "deploy" {
		return // deploy 另表跟踪，不入 pipeline_jobs（与 completed 一致）
	}
	if isNonMasterPush(ctx, repo, sha) {
		slog.Info("pipeline skip non-master branch job progress", "repo", repo, "sha", shortSHA(sha), "job", jobName)
		return
	}
	runID := ensureRunID(ctx, repo, sha)
	if runID == "" {
		return
	}
	started := parseRFC3339(startedAt)
	if started == nil {
		t := time.Now().UTC()
		started = &t
	}
	// 仅确保行存在与 started_at 回填，不覆盖已有的 terminal 结论（success/failure 等）
	_, err := model.Pool.Exec(ctx, `
		INSERT INTO pipeline_jobs (run_id, name, conclusion, started_at, duration_ms)
		VALUES ($1, $2, NULL, $3, NULL)
		ON CONFLICT (run_id, name) DO UPDATE
		  SET started_at = COALESCE(pipeline_jobs.started_at, EXCLUDED.started_at)`, runID, jobName, started)
	if err != nil {
		slog.Warn("pipeline_jobs progress upsert failed", "job", jobName, "err", err.Error())
		return
	}
	aggregateRunStatus(ctx, runID)
	pid := projectIDByRun(ctx, runID)
	if pid == "" {
		pid = projectIDByRepo(repo)
	}
	if pid != "" {
		events.Default.Publish("project:"+pid, "ci.progress",
			map[string]any{"repo": repo, "job": jobName, "status": status, "sha": sha})
	}
}

// aggregateRunStatus 按 jobs 结论聚合 run 状态并回填耗时（2026-09-12 动态化）：
//   - 存在 failure/cancelled/timed_out 等 ⇒ FAILURE
//   - 至少 1 个 job 且全部完成且均为 success/skipped/neutral ⇒ SUCCESS
//   - 其余（含 NULL 未完成结论）⇒ RUNNING
//
// 动态 job 集兼容两类流水线：FORJA 自身 4 job（preseed 四行）与用户项目单 job `build`。
func aggregateRunStatus(ctx context.Context, runID string) {
	if !idgen.IsValid(runID) {
		return
	}
	row := model.Pool.QueryRow(ctx, `
		UPDATE pipeline_runs r SET
		  status = CASE
		    WHEN j.failed > 0 THEN 'FAILURE'
		    WHEN j.total > 0 AND j.pending = 0 THEN 'SUCCESS'
		    ELSE 'RUNNING'
		  END,
		  duration_ms = CASE
		    WHEN j.failed > 0 OR (j.total > 0 AND j.pending = 0)
		      THEN (EXTRACT(EPOCH FROM (now()-r.created_at))*1000)::BIGINT
		    ELSE r.duration_ms
		  END,
		  updated_at = now()
		FROM (
		  SELECT COUNT(*) AS total,
		         COUNT(*) FILTER (WHERE conclusion IN ('failure','cancelled','timed_out','startup_failure','action_required')) AS failed,
		         COUNT(*) FILTER (WHERE COALESCE(conclusion,'') NOT IN ('success','skipped','neutral')) AS pending
		    FROM pipeline_jobs WHERE run_id=$1
		) j
		WHERE r.id=$1
		RETURNING r.status, r.duration_ms`, runID)
	var st string
	var ms *int64
	if err := row.Scan(&st, &ms); err != nil {
		// RETURNING 无行（run 不存在）或 DB 故障：聚合本身失败需留痕
		slog.Warn("pipeline_runs aggregate failed", "run_id", runID, "err", err.Error())
		return
	}
	if st != "RUNNING" && ms != nil && *ms > 0 {
		metrics.ObservePipelineRun(float64(*ms) / 1000) // S4 #4b：终态时长入直方图
	}
}

// revertFailedBuildRun 构建校验失败回滚：清理 SHA_NOT_HEAD / isFullSHA 等
// 400 路径下可能残留的 RUNNING（manual/workflow_job），回滚为 PENDING 并清 preseed jobs。
// 按 project_id 维度隔离，兼容 legacy NULL 行（repo 映射）。
func revertFailedBuildRun(ctx context.Context, repo, sha string, projectID ...string) {
	if model.Pool == nil || repo == "" || isZeroSHA(sha) {
		return
	}
	pid := ""
	if len(projectID) > 0 {
		pid = projectID[0]
	}
	if pid != "" {
		if !idgen.IsValid(pid) {
			slog.Warn("pipeline_runs revert invalid project_id", "project_id", pid)
			pid = ""
		}
	}
	if pid != "" {
		// 先清 preseed jobs，再将 RUNNING 回滚为 PENDING（保留 push PENDING 不动）
		_, _ = model.Pool.Exec(ctx, `DELETE FROM pipeline_jobs WHERE run_id IN (SELECT id FROM pipeline_runs WHERE repo=$1 AND sha=$2 AND status='RUNNING' AND event IN ('manual','workflow_job') AND (project_id=$3::text OR (project_id IS NULL AND repo=(SELECT repo_full_name FROM projects WHERE id=$3::text))))`, repo, sha, pid)
		tag, err := model.Pool.Exec(ctx, `UPDATE pipeline_runs SET status='PENDING', updated_at=now() WHERE repo=$1 AND sha=$2 AND status='RUNNING' AND event IN ('manual','workflow_job') AND (project_id=$3::text OR (project_id IS NULL AND repo=(SELECT repo_full_name FROM projects WHERE id=$3::text)))`, repo, sha, pid)
		if err != nil {
			slog.Warn("pipeline_runs revert failed", "err", err.Error())
		} else if tag.RowsAffected() > 0 {
			slog.Info("pipeline_runs reverted RUNNING->PENDING", "repo", repo, "sha", shortSHA(sha), "project_id", pid, "count", tag.RowsAffected())
		}
		return
	}
	_, _ = model.Pool.Exec(ctx, `DELETE FROM pipeline_jobs WHERE run_id IN (SELECT id FROM pipeline_runs WHERE repo=$1 AND sha=$2 AND status='RUNNING' AND event IN ('manual','workflow_job'))`, repo, sha)
	tag, err := model.Pool.Exec(ctx, `UPDATE pipeline_runs SET status='PENDING', updated_at=now() WHERE repo=$1 AND sha=$2 AND status='RUNNING' AND event IN ('manual','workflow_job')`, repo, sha)
	if err != nil {
		slog.Warn("pipeline_runs revert failed", "err", err.Error())
	} else if tag.RowsAffected() > 0 {
		slog.Info("pipeline_runs reverted RUNNING->PENDING", "repo", repo, "sha", shortSHA(sha), "count", tag.RowsAffected())
	}
}

// reapStaleRuns 僵尸 run 自愈清扫（挂在 webhook 入口，随每次事件触发，无独立定时器）：
//   - 零 job 且超时判 SKIPPED（"从未被调度"）——S6 后仅剩 dispatch 建的 manual 行
//     可能落入此况（Gitea 接受 dispatch 但 runner 未起跑）；push 行为 PENDING 永不清理
//   - runner 中途崩溃（OOM/重启）时部分 job 落库后停更 → 有 job 但停更超时判 FAILURE
//
// 阈值依据 §7 基线：tier-1 正常 1-4 分钟内起跑；单 job 最长 ~11m，30m 无更新必属异常。
// U-3 扩展：按 project_id 维度隔离（跨项目不互清），可变参数兼容旧无参调用。
func reapStaleRuns(ctx context.Context, projectID ...string) {
	if model.Pool == nil {
		return
	}
	pid := ""
	if len(projectID) > 0 {
		pid = projectID[0]
	}
	if pid != "" {
		if !idgen.IsValid(pid) {
			slog.Warn("pipeline_runs reap invalid project_id", "project_id", pid)
			pid = ""
		}
	}
	if pid != "" {
		// Per-project isolation: only reap runs belonging to this project (legacy NULL rows via repo mapping)
		tag, err := model.Pool.Exec(ctx, `
			UPDATE pipeline_runs SET status='SKIPPED', updated_at=now()
			WHERE status='RUNNING' AND event IN ('manual','workflow_dispatch','workflow_job')
			  AND (project_id=$1::text OR (project_id IS NULL AND repo=(SELECT repo_full_name FROM projects WHERE id=$1::text)))
			  AND updated_at < now()-interval '10 minutes'
			  AND NOT EXISTS (SELECT 1 FROM pipeline_jobs j WHERE j.run_id=pipeline_runs.id)`, pid)
		if err != nil {
			slog.Error("pipeline_runs reap(skipped) failed", "err", err.Error())
		} else if tag.RowsAffected() > 0 {
			slog.Info("pipeline_runs reaped", "count", tag.RowsAffected(), "to", "SKIPPED", "project_id", pid)
		}
		tag, err = model.Pool.Exec(ctx, `
			UPDATE pipeline_runs r SET status='FAILURE', duration_ms=
			  COALESCE(r.duration_ms,(EXTRACT(EPOCH FROM (now()-r.created_at))*1000)::BIGINT),
			  updated_at=now()
			WHERE r.status='RUNNING'
			  AND (r.project_id=$1::text OR (r.project_id IS NULL AND r.repo=(SELECT repo_full_name FROM projects WHERE id=$1::text)))
			  AND r.updated_at < now()-interval '30 minutes'
			  AND EXISTS (SELECT 1 FROM pipeline_jobs j WHERE j.run_id=r.id)`, pid)
		if err != nil {
			slog.Error("pipeline_runs reap(failure) failed", "err", err.Error())
		} else if tag.RowsAffected() > 0 {
			slog.Info("pipeline_runs reaped", "count", tag.RowsAffected(), "to", "FAILURE", "project_id", pid)
		}
		return
	}
	tag, err := model.Pool.Exec(ctx, `
		UPDATE pipeline_runs SET status='SKIPPED', updated_at=now()
		WHERE status='RUNNING' AND event IN ('manual','workflow_dispatch','workflow_job') AND updated_at < now()-interval '10 minutes'
		  AND NOT EXISTS (SELECT 1 FROM pipeline_jobs j WHERE j.run_id=pipeline_runs.id)`)
	if err != nil {
		slog.Error("pipeline_runs reap(skipped) failed", "err", err.Error())
	} else if tag.RowsAffected() > 0 {
		slog.Info("pipeline_runs reaped", "count", tag.RowsAffected(), "to", "SKIPPED")
	}
	tag, err = model.Pool.Exec(ctx, `
		UPDATE pipeline_runs r SET status='FAILURE', duration_ms=
		  COALESCE(r.duration_ms,(EXTRACT(EPOCH FROM (now()-r.created_at))*1000)::BIGINT),
		  updated_at=now()
		WHERE r.status='RUNNING' AND r.updated_at < now()-interval '30 minutes'
		  AND EXISTS (SELECT 1 FROM pipeline_jobs j WHERE j.run_id=r.id)`)
	if err != nil {
		slog.Error("pipeline_runs reap(failure) failed", "err", err.Error())
	} else if tag.RowsAffected() > 0 {
		slog.Info("pipeline_runs reaped", "count", tag.RowsAffected(), "to", "FAILURE")
	}
}

// linkCIToTask CI↔Task 回写（observability-design §6.2 补 G4 断链）：
// 仓库名↔repo_full_name + commit_sha↔tasks.commit_sha 定位 RELEASE_PENDING 任务，
// 以审计留痕代替状态变更（签核仍是人）；映射不到则静默跳过（手动建仓绕过约定场景）
func linkCIToTask(ctx context.Context, repo, sha, jobName, conclusion string) {
	if model.Pool == nil || isZeroSHA(sha) {
		return
	}
	pid := projectIDByRepo(repo)
	var tid string
	err := model.Pool.QueryRow(ctx, `
		SELECT t.id::text FROM tasks t JOIN projects p ON p.id=t.project_id
		WHERE p.repo_full_name=$1 AND t.commit_sha=$2 AND t.status='RELEASE_PENDING'
		LIMIT 1`, repo, sha).Scan(&tid)
	if err != nil {
		return
	}
	action := "ci.passed"
	if conclusion != "success" {
		action = "ci.failed"
	}
	detail := `{"job":` + quoteJSON(jobName) + `,"conclusion":` + quoteJSON(conclusion) +
		`,"sha":` + quoteJSON(sha) + `}`
	logAudit(ctx, pid, "gitea", action, "task", tid, detail)
	if pid != "" {
		events.Default.Publish("project:"+pid, action, map[string]any{
			"task_id": tid, "job": jobName, "conclusion": conclusion, "sha": sha,
		})
		events.Default.Publish("task:"+tid, action, map[string]any{
			"job": jobName, "conclusion": conclusion, "sha": sha,
		})
	}
}

func parseRFC3339(s string) *time.Time {
	if s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil
	}
	return &t
}
