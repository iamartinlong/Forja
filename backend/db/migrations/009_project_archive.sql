-- 009: 项目软删除（KI-002 决策 S1）— projects 归档标记
-- archived_at IS NULL 视为活跃；非空即已归档（只读），可恢复；purge 物理删除仅限已归档项目。
ALTER TABLE projects ADD COLUMN IF NOT EXISTS archived_at TIMESTAMPTZ;
