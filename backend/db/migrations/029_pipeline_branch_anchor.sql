-- 029 流水线精确分支锚 + 部署溯源 + 炉归因
-- pipeline_runs 已有 branch 列（012），补唯一锚扩展至 branch 维，防 master/iteration 同 sha 互覆盖
CREATE UNIQUE INDEX IF NOT EXISTS uq_pipeline_runs_anchor_branch ON pipeline_runs(repo, sha, branch, event);
-- deployments 关联迭代与分支，便于按迭代筛选与三锚追溯
ALTER TABLE deployments ADD COLUMN IF NOT EXISTS iteration_id TEXT REFERENCES iterations(id) ON DELETE SET NULL;
ALTER TABLE deployments ADD COLUMN IF NOT EXISTS branch VARCHAR(128);
CREATE INDEX IF NOT EXISTS idx_deployments_iteration ON deployments(iteration_id, created_at DESC);
-- 炉归因：锻造执行关联炉，便于 cost 按炉聚合与熔断观测
ALTER TABLE agent_executions ADD COLUMN IF NOT EXISTS furnace_id TEXT REFERENCES furnaces(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_agent_exec_furnace ON agent_executions(furnace_id);
