-- 008: spec 计数器,用于 BS 层 story_id 自动生成 ({大写SLUG}-{序号})
CREATE TABLE IF NOT EXISTS spec_counters (
  project_id UUID PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
  last_seq INT NOT NULL DEFAULT 0
);
