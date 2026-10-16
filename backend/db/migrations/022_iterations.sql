-- W1 iterations 域：项目迭代 + 任务归属
CREATE TABLE IF NOT EXISTS iterations (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  start_date DATE,
  end_date DATE,
  status TEXT NOT NULL DEFAULT 'planned' CHECK (status IN ('planned','active','closed')),
  created_by TEXT REFERENCES users(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE(project_id, name)
);
CREATE INDEX IF NOT EXISTS idx_iterations_project ON iterations(project_id);

ALTER TABLE tasks ADD COLUMN IF NOT EXISTS target_iteration_id TEXT REFERENCES iterations(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_tasks_iteration ON tasks(target_iteration_id);
