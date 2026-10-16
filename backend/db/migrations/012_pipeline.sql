-- 012: 交付可观测性 P0（observability-design.md §3.1）
-- pipeline_runs/jobs: 流水线实体表（webhook 落库，页面刷新不依赖 Gitea 存活）
-- webhook_deliveries: 持久化去重（替换进程内 LRU，重启不丢）

CREATE TABLE IF NOT EXISTS pipeline_runs (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id  UUID REFERENCES projects(id),
    repo        VARCHAR(256) NOT NULL,            -- owner/name
    sha         VARCHAR(64)  NOT NULL,
    branch      VARCHAR(128),
    event       VARCHAR(32)  NOT NULL,            -- push / workflow_dispatch / workflow_job
    status      VARCHAR(16)  NOT NULL DEFAULT 'RUNNING', -- RUNNING/SUCCESS/FAILURE/CANCELLED
    actor       VARCHAR(128),                     -- 触发者（人 / FORJA Agent）
    duration_ms BIGINT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- webhook 幂等 upsert 锚点
CREATE UNIQUE INDEX IF NOT EXISTS uq_pipeline_runs_anchor ON pipeline_runs(repo, sha, event);
CREATE INDEX IF NOT EXISTS idx_pipeline_runs_project ON pipeline_runs(project_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_pipeline_runs_status ON pipeline_runs(status, created_at DESC);

CREATE TABLE IF NOT EXISTS pipeline_jobs (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id      UUID NOT NULL REFERENCES pipeline_runs(id) ON DELETE CASCADE,
    name        VARCHAR(128) NOT NULL,            -- job 名（backend/frontend/deploy）
    conclusion  VARCHAR(16),                      -- success/failure/cancelled/skipped
    started_at  TIMESTAMPTZ,
    duration_ms BIGINT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_pipeline_jobs_run_name ON pipeline_jobs(run_id, name);
CREATE INDEX IF NOT EXISTS idx_pipeline_jobs_run ON pipeline_jobs(run_id);

CREATE TABLE IF NOT EXISTS webhook_deliveries (
    delivery_id VARCHAR(64) PRIMARY KEY,
    event       VARCHAR(32),
    received_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
