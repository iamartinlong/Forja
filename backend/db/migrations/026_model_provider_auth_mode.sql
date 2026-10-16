-- 026 模型提供商认证模式 动态必填
ALTER TABLE model_providers ADD COLUMN IF NOT EXISTS auth_mode TEXT NOT NULL DEFAULT 'Bearer' CHECK (auth_mode IN ('Bearer','Header','None'));
UPDATE model_providers SET auth_mode='Header' WHERE auth_mode='Bearer' AND (api_key_ref IS NULL OR api_key_ref='');
