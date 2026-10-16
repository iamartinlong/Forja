-- S6 §11 部署实体化：deployments 表——部署状态机（scheduled/running/success/failed）
-- 与部署历史记录；"当前线上"唯一真相 = 最新 success 行（横幅/summary 数据源）。
-- 审计溯源仍在 audit_logs，本表只做状态追踪。
CREATE TABLE IF NOT EXISTS deployments (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  repo TEXT NOT NULL,
  sha TEXT NOT NULL,
  tag TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('scheduled','running','success','failed')),
  mode TEXT NOT NULL DEFAULT 'manual',
  actor TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  deployed_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_deployments_status ON deployments (status, created_at DESC);
