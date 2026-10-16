-- S4-0 分支策略：tasks.branch 持 forge/task-<id>-a<attempt>，隔离锻造输出
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS branch VARCHAR(128);
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS base_sha VARCHAR(64);
CREATE INDEX IF NOT EXISTS idx_tasks_branch ON tasks(branch);
