-- 007: 用户最后登录时间（A7 收尾）
-- 幂等设计：列不存在则添加，已存在则跳过
ALTER TABLE users ADD COLUMN IF NOT EXISTS last_login_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_users_last_login ON users(last_login_at);
