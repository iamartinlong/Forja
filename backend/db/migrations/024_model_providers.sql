-- 024 模型提供商 主从：provider + models/headers JSONB + enabled 维
CREATE EXTENSION IF NOT EXISTS "citext";
CREATE TABLE IF NOT EXISTS model_providers (
  id TEXT PRIMARY KEY,
  provider_id CITEXT UNIQUE NOT NULL CHECK (provider_id ~ '^[a-z0-9_-]+$'),
  display_name VARCHAR(128),
  base_url TEXT NOT NULL CHECK (base_url ~ '^https://'),
  api_key_ref TEXT,
  models JSONB NOT NULL DEFAULT '[]'::jsonb,
  headers JSONB NOT NULL DEFAULT '[]'::jsonb,
  status TEXT NOT NULL DEFAULT 'healthy' CHECK (status IN ('healthy','unhealthy')),
  created_by TEXT REFERENCES users(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_model_providers_provider_id ON model_providers(provider_id);
