-- 014: §2c/2d 存储抽象 — 需求附件与项目资源库
-- inbox_attachments: 需求原始凭证；project_files: 背景资料/品牌手册等
-- 存储键规范 {scope}/{projectID}/{yyyymm}/{uuid}-{origName}，支持按项目清理导出

CREATE TABLE IF NOT EXISTS inbox_attachments (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    requirement_id UUID NOT NULL REFERENCES requirements_inbox(id) ON DELETE CASCADE,
    project_id     UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    filename       VARCHAR(256) NOT NULL,
    size           BIGINT NOT NULL CHECK (size >= 0 AND size <= 20971520),
    mime           VARCHAR(128) NOT NULL DEFAULT 'application/octet-stream',
    storage_key    VARCHAR(512) NOT NULL UNIQUE,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_inbox_attachments_requirement ON inbox_attachments(requirement_id);
CREATE INDEX IF NOT EXISTS idx_inbox_attachments_project ON inbox_attachments(project_id);
CREATE INDEX IF NOT EXISTS idx_inbox_attachments_key ON inbox_attachments(storage_key);

CREATE TABLE IF NOT EXISTS project_files (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id  UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    category    VARCHAR(64) NOT NULL DEFAULT 'general',
    filename    VARCHAR(256) NOT NULL,
    summary     TEXT,
    storage_key VARCHAR(512) NOT NULL UNIQUE,
    size        BIGINT NOT NULL CHECK (size >= 0 AND size <= 20971520),
    mime        VARCHAR(128) NOT NULL DEFAULT 'application/octet-stream',
    created_by  UUID,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_project_files_project ON project_files(project_id);
CREATE INDEX IF NOT EXISTS idx_project_files_category ON project_files(project_id, category);
CREATE INDEX IF NOT EXISTS idx_project_files_key ON project_files(storage_key);
