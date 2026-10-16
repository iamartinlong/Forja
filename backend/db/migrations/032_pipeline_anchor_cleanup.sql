-- 032 清理旧分支锚：仅保留 branch 维唯一，防同 sha 跨 master/iteration 互覆盖（R-1）
DROP INDEX IF EXISTS uq_pipeline_runs_anchor;
CREATE UNIQUE INDEX IF NOT EXISTS uq_pipeline_runs_anchor_branch ON pipeline_runs(repo, sha, branch, event);
