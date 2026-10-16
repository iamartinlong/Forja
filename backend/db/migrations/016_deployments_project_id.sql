-- U-2: deployments 项目过滤修复（PipelineBoard header projectId 跟随）
-- 补 project_id 列，使部署记录可按项目精确过滤（历史行 repo 映射兜底由查询层 OR 兼容）
ALTER TABLE deployments ADD COLUMN IF NOT EXISTS project_id UUID REFERENCES projects(id);
CREATE INDEX IF NOT EXISTS idx_deployments_project ON deployments(project_id, created_at DESC);
-- 回填存量：按 repo=projects.repo_full_name 关联推导
UPDATE deployments d SET project_id = p.id
FROM projects p WHERE d.project_id IS NULL AND d.repo = p.repo_full_name AND p.repo_full_name IS NOT NULL;
