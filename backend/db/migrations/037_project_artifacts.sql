-- 037 项目制品台账（v2.0 M1.2）：构建成功回填（webhook workflow_job build）
-- 交付包（M1.3）消费本表；镜像契约：gitea:3000/<owner>/<repo>:master-<sha8>
-- 类型注意：projects.id / pipeline_runs.id 经 019 已为 TEXT，外键列必须同为 TEXT
CREATE TABLE IF NOT EXISTS project_artifacts (
    id           TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    project_id   TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    sha          VARCHAR(64)  NOT NULL,
    tag          VARCHAR(128) NOT NULL,
    image        VARCHAR(256) NOT NULL,
    digest       VARCHAR(128),
    size_bytes   BIGINT,
    store_key    TEXT,                                   -- FileStore 中 image.tar.gz（CI 回传，M1.3）
    build_run_id TEXT REFERENCES pipeline_runs(id) ON DELETE SET NULL,
    status       VARCHAR(16)  NOT NULL DEFAULT 'built'
        CHECK (status IN ('built','uploaded','failed')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(project_id, sha)
);
CREATE INDEX IF NOT EXISTS idx_project_artifacts_project ON project_artifacts(project_id, created_at DESC);
