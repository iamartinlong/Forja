-- 039 v2.1 模板库：建项 pin 模板版本（组织库热更新仅影响新项目，存量不漂移）
ALTER TABLE projects ADD COLUMN IF NOT EXISTS template_version VARCHAR(32);
