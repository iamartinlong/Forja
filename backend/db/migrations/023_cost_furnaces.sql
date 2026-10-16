-- W2 成本报表 + 炉池基础
ALTER TABLE agent_executions ADD COLUMN IF NOT EXISTS tokens_in INT;
ALTER TABLE agent_executions ADD COLUMN IF NOT EXISTS tokens_out INT;
ALTER TABLE agent_executions ADD COLUMN IF NOT EXISTS cost_cents INT;
ALTER TABLE agent_executions ADD COLUMN IF NOT EXISTS model VARCHAR(128);
ALTER TABLE agent_executions ADD COLUMN IF NOT EXISTS duration_ms BIGINT;

CREATE TABLE IF NOT EXISTS furnaces (
  id TEXT PRIMARY KEY,
  name VARCHAR(128) UNIQUE NOT NULL,
  base_url TEXT NOT NULL,
  auth_ref TEXT,
  model_profile JSONB,
  capacity INT NOT NULL DEFAULT 2,
  labels JSONB,
  project_id TEXT REFERENCES projects(id) ON DELETE SET NULL,
  status VARCHAR(16) NOT NULL DEFAULT 'healthy' CHECK (status IN ('healthy','busy','unhealthy','drained')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_furnaces_project ON furnaces(project_id);
