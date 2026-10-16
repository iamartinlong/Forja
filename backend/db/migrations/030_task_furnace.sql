-- 030 任务关联炉：runForge 原子预约 busy 计数
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS furnace_id TEXT REFERENCES furnaces(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_tasks_furnace ON tasks(furnace_id, status);
