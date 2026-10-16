-- 038 项目交付包（v2.0 M1.3）：docs.zip 台账（delivery.json/compose/README-DEPLOY/CHANGELOG/project.yaml）
-- 镜像 tar 存 project_artifacts.store_key（CI 回传），本表只引用 sha；UNIQUE(project_id,tag) 支持幂等重生成。
-- 类型注意：projects.id 经 019 已为 TEXT，外键列必须同为 TEXT。
CREATE TABLE IF NOT EXISTS project_deliveries (
    id          TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    project_id  TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    tag         VARCHAR(128) NOT NULL,
    sha         VARCHAR(64)  NOT NULL,
    status      VARCHAR(16)  NOT NULL DEFAULT 'assembling'
        CHECK (status IN ('assembling','ready','failed')),
    manifest    JSONB NOT NULL DEFAULT '{}'::jsonb,   -- {files:[],image:{},notes:{},has_tar}
    store_key   TEXT,                                 -- FileStore 中 docs-<tag>.zip
    error       TEXT,                                 -- status=failed 时的原因
    created_by  TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(project_id, tag)
);
CREATE INDEX IF NOT EXISTS idx_project_deliveries_project ON project_deliveries(project_id, created_at DESC);
