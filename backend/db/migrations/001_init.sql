-- FORJA Phase 0 — 10+2 tables per architecture/data-model.md + inbox-ingest.md
-- Requires pgvector
CREATE EXTENSION IF NOT EXISTS "pgcrypto";
CREATE EXTENSION IF NOT EXISTS vector;

-- projects
CREATE TABLE IF NOT EXISTS projects (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name VARCHAR(128) UNIQUE NOT NULL,
  description TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- specs
CREATE TABLE IF NOT EXISTS specs (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  layer VARCHAR(16) NOT NULL CHECK (layer IN ('BS','IS','DS','TS','AS','DEPLOY','CONSTITUTION','CONTRACT')),
  title VARCHAR(256) NOT NULL,
  status VARCHAR(16) NOT NULL DEFAULT 'DRAFT' CHECK (status IN ('DRAFT','VALIDATED','DEPRECATED')),
  current_version VARCHAR(32),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_specs_project ON specs(project_id);
CREATE INDEX idx_specs_layer ON specs(layer);

-- spec_versions
CREATE TABLE IF NOT EXISTS spec_versions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  spec_id UUID NOT NULL REFERENCES specs(id) ON DELETE CASCADE,
  version VARCHAR(32) NOT NULL,
  content JSONB NOT NULL,
  embedding VECTOR(1536),
  change_log TEXT,
  created_by UUID,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(spec_id, version)
);
CREATE INDEX idx_spec_versions_spec ON spec_versions(spec_id);
CREATE INDEX idx_spec_versions_content ON spec_versions USING GIN (content);
-- ivfflat for embedding created in Phase 3 when enough rows

-- requirements_inbox (Inbox 原料堆)
CREATE TABLE IF NOT EXISTS requirements_inbox (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  source_type VARCHAR(32) NOT NULL,
  raw_content TEXT NOT NULL,
  content_hash VARCHAR(64),
  embedding VECTOR(1536),
  status VARCHAR(16) NOT NULL DEFAULT 'NEW' CHECK (status IN ('NEW','CLUSTERED','DRAFTED','CONVERTED','DISCARDED')),
  cluster_id UUID,
  created_by UUID,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_inbox_project ON requirements_inbox(project_id);
CREATE INDEX idx_inbox_status ON requirements_inbox(status);

CREATE TABLE IF NOT EXISTS requirement_spec_links (
  requirement_id UUID NOT NULL REFERENCES requirements_inbox(id) ON DELETE CASCADE,
  spec_id UUID NOT NULL REFERENCES specs(id) ON DELETE CASCADE,
  PRIMARY KEY (requirement_id, spec_id)
);

-- tasks
CREATE TABLE IF NOT EXISTS tasks (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  spec_id UUID REFERENCES specs(id) ON DELETE SET NULL,
  spec_version VARCHAR(32),
  title VARCHAR(256) NOT NULL,
  description TEXT,
  status VARCHAR(16) NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING','RUNNING','VALIDATING','PASSED','FAILED','FIXED','ESCALATED')),
  priority INT NOT NULL DEFAULT 0,
  estimated_hours NUMERIC,
  assigned_to UUID,
  confidence FLOAT CHECK (confidence IS NULL OR (confidence >=0 AND confidence <=1)),
  action_required JSONB,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_tasks_project ON tasks(project_id);
CREATE INDEX idx_tasks_spec ON tasks(spec_id);
CREATE INDEX idx_tasks_status ON tasks(status);

CREATE TABLE IF NOT EXISTS task_dependencies (
  task_id UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  depends_on UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  PRIMARY KEY (task_id, depends_on),
  CHECK (task_id <> depends_on)
);

CREATE TABLE IF NOT EXISTS code_changes (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  task_id UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  file_path VARCHAR(512) NOT NULL,
  diff TEXT NOT NULL,
  change_log TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_code_changes_task ON code_changes(task_id);

CREATE TABLE IF NOT EXISTS test_cases (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  task_id UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  code_change_id UUID REFERENCES code_changes(id) ON DELETE SET NULL,
  name VARCHAR(256) NOT NULL,
  assertion TEXT,
  framework VARCHAR(32),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_test_cases_task ON test_cases(task_id);

CREATE TABLE IF NOT EXISTS test_results (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  test_case_id UUID NOT NULL REFERENCES test_cases(id) ON DELETE CASCADE,
  status VARCHAR(16) NOT NULL CHECK (status IN ('PASSED','FAILED')),
  output TEXT,
  duration_ms BIGINT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_test_results_case ON test_results(test_case_id);

CREATE TABLE IF NOT EXISTS agent_executions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  task_id UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  agent_type VARCHAR(32) NOT NULL,
  input JSONB,
  output JSONB,
  status VARCHAR(16) NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING','RUNNING','SUCCESS','FAILED')),
  attempt INT NOT NULL DEFAULT 1,
  started_at TIMESTAMPTZ,
  finished_at TIMESTAMPTZ
);
CREATE INDEX idx_agent_exec_task ON agent_executions(task_id);

CREATE TABLE IF NOT EXISTS rules (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name VARCHAR(128) NOT NULL,
  content JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(project_id, name)
);

CREATE TABLE IF NOT EXISTS audit_logs (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  actor VARCHAR(128) NOT NULL,
  action VARCHAR(64) NOT NULL,
  target_type VARCHAR(64) NOT NULL,
  target_id UUID,
  detail JSONB,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_audit_project ON audit_logs(project_id);
CREATE INDEX idx_audit_target ON audit_logs(target_type, target_id);
CREATE INDEX idx_audit_created ON audit_logs(created_at);

-- seed: default project forja
INSERT INTO projects (id, name, description) VALUES ('00000000-0000-0000-0000-000000000001', 'forja', 'FORJA 锻造坊') ON CONFLICT (name) DO NOTHING;
