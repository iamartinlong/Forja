-- 031 修正 029 分支锚遗留：旧唯一锚 (repo,sha,event) 与新 (repo,sha,branch,event) 共存致同 sha 跨分支互覆盖，删旧留新
DROP INDEX IF EXISTS uq_pipeline_runs_anchor;
CREATE UNIQUE INDEX IF NOT EXISTS uq_pipeline_runs_anchor_branch ON pipeline_runs(repo, sha, branch, event);
-- 补 029 漏：若 029 因 agent_exec 列名已回滚，需确保 index 仍存在（幂等）
