-- A1 用户/项目协作：users 表 (bcrypt)
CREATE TABLE IF NOT EXISTS users (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  username VARCHAR(128) UNIQUE NOT NULL,
  email VARCHAR(256),
  password_hash VARCHAR(256) NOT NULL,
  role VARCHAR(32) NOT NULL CHECK (role IN ('Admin','Architect','Developer','Viewer')),
  enabled BOOLEAN NOT NULL DEFAULT true,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_users_username ON users(username);
CREATE INDEX IF NOT EXISTS idx_users_role ON users(role);

-- projects 加 owner/开放标记 (已存在 name)
ALTER TABLE projects ADD COLUMN IF NOT EXISTS owner_id UUID REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE projects ADD COLUMN IF NOT EXISTS slug VARCHAR(128);
UPDATE projects SET slug = lower(regexp_replace(name, '[^a-zA-Z0-9]+', '-', 'g')) WHERE slug IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_projects_slug ON projects(slug);

-- 补充 project_members 的 user_id FK (若旧数据 username 孤立则保留)
-- 兼容：user_id 已存在但非 FK，补 FK 约束需先清孤儿，暂不强制 FK，仅加 index 已在 003

-- seed 默认用户 (bcrypt 10)
INSERT INTO users (username, email, password_hash, role) VALUES
  ('admin', 'admin@forja.local', '$2a$10$79L.j7W/QztdGQcxRrsQ9uAlubjgbr2jD3YzGJIB4ouvLYCm7vjUu', 'Admin'),
  ('architect', 'architect@forja.local', '$2a$10$JCpnI6SnRIHbKQHJGdC/D.glwIb2OG8KWbbrkpXrZNPkwLeH6R2Yy', 'Architect'),
  ('dev', 'dev@forja.local', '$2a$10$NtziKhVMzKOHWKo2tiKjZeDw.GSmli6keGRqhxWUB6u3T1cUEPMPe', 'Developer'),
  ('viewer', 'viewer@forja.local', '$2a$10$qEDC2cVVopjL1nBq/ZoLluq1M6Fzc7piK5n2g1okC2BpTtLTyVF3K', 'Viewer')
ON CONFLICT (username) DO NOTHING;

-- 默认项目成员绑定 (forja)
INSERT INTO project_members (project_id, user_id, username, role)
SELECT '00000000-0000-0000-0000-000000000001', u.id, u.username, u.role FROM users u
ON CONFLICT (project_id, username) DO UPDATE SET role = EXCLUDED.role;
