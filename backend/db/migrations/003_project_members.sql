-- Project协作：成员 + 知识库演进标记
CREATE TABLE IF NOT EXISTS project_members (
  project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  user_id UUID NOT NULL,
  username VARCHAR(128) NOT NULL,
  role VARCHAR(32) NOT NULL CHECK (role IN ('Admin','Architect','Developer','Viewer')),
  joined_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (project_id, username)
);
CREATE INDEX IF NOT EXISTS idx_project_members_project ON project_members(project_id);

-- 知识库演进：spec_versions 已有 embedding，向 task/code 扩展 (可选)
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS embedding VECTOR(1536);
CREATE INDEX IF NOT EXISTS idx_tasks_embedding ON tasks USING ivfflat (embedding vector_cosine_ops) WITH (lists = 50);
