-- 005: project_members 重构为 user_id 外键模型
-- 主键 (project_id, user_id)，username 冗余仅作展示；旧数据按 username 回填真实 users.id
CREATE TABLE IF NOT EXISTS project_members_new (
  project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  username VARCHAR(128) NOT NULL,
  role VARCHAR(32) NOT NULL CHECK (role IN ('Admin','Architect','Developer','Viewer')),
  joined_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (project_id, user_id)
);

INSERT INTO project_members_new (project_id, user_id, username, role, joined_at)
SELECT m.project_id, u.id, m.username, COALESCE(u.role, m.role), m.joined_at
FROM project_members m
JOIN users u ON u.username = m.username
ON CONFLICT (project_id, user_id) DO NOTHING;

DROP TABLE IF EXISTS project_members;
ALTER TABLE project_members_new RENAME TO project_members;

CREATE INDEX IF NOT EXISTS idx_project_members_project ON project_members(project_id);
CREATE INDEX IF NOT EXISTS idx_project_members_user ON project_members(user_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_project_members_username ON project_members(project_id, username);

-- 存量项目回填 owner_id：取最早加入的 Admin 成员
UPDATE projects p SET owner_id = t.user_id
FROM (
  SELECT DISTINCT ON (project_id) project_id, user_id
  FROM project_members
  WHERE role = 'Admin'
  ORDER BY project_id, joined_at
) t
WHERE p.id = t.project_id AND p.owner_id IS NULL;
