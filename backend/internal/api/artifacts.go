package api

// M1.2 制品台账：构建成功（webhook workflow_job build=success）→ project_artifacts 登记。
// 交付包（M1.3）与前端交付页消费本表；镜像契约 gitea:3000/<owner>/<repo>:master-<sha8>。

import (
	"context"
	"log/slog"

	"forja/backend/internal/events"
	"forja/backend/internal/model"
)

// registerArtifact 项目仓构建成功回填（幂等 upsert）。
// 仅处理用户项目脚手架作业名 `build`（FORJA 自身 build.yml 的 build-push 不在此列）。
func registerArtifact(ctx context.Context, repo, sha, job string) {
	if model.Pool == nil || repo == "" || !isFullSHA(sha) {
		return
	}
	pid := projectIDByRepo(repo)
	if pid == "" {
		return
	}
	image := "gitea:3000/" + repo
	branch := "master"
	_ = model.Pool.QueryRow(ctx,
		`SELECT COALESCE(NULLIF(default_branch,''),'master') FROM projects WHERE id=$1`, pid).Scan(&branch)
	if branch == "" {
		branch = "master"
	}
	tag := branch + "-" + shortSHA(sha)
	var runID *string
	_ = model.Pool.QueryRow(ctx,
		`SELECT id::text FROM pipeline_runs WHERE repo=$1 AND sha=$2 ORDER BY created_at DESC LIMIT 1`, repo, sha).Scan(&runID)
	var rid any
	if runID != nil {
		rid = *runID
	}
	_, err := model.Pool.Exec(ctx, `
		INSERT INTO project_artifacts (project_id, sha, tag, image, status, build_run_id)
		VALUES ($1,$2,$3,$4,'built',$5)
		ON CONFLICT (project_id, sha) DO UPDATE
		  SET tag=EXCLUDED.tag, image=EXCLUDED.image,
		      status=CASE WHEN project_artifacts.store_key IS NOT NULL THEN 'uploaded' ELSE 'built' END,
		      build_run_id=EXCLUDED.build_run_id, updated_at=now()`,
		pid, sha, tag, image, rid)
	if err != nil {
		slog.Warn("artifact register failed", "project_id", pid, "sha", shortSHA(sha), "err", err.Error())
		return
	}
	events.Default.Publish("project:"+pid, "delivery.built",
		map[string]any{"sha": sha, "tag": tag, "image": image, "job": job})
}
