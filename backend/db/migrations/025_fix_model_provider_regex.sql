-- 025 fix: provider_id regex hyphen range invalid
ALTER TABLE model_providers DROP CONSTRAINT IF EXISTS model_providers_provider_id_check;
ALTER TABLE model_providers ADD CONSTRAINT model_providers_provider_id_check CHECK (provider_id ~ '^[a-z0-9_-]+$');
