-- 015: 2b incremental Spec relations -- refs/anchors
-- refs: associated spec IDs (incremental refs); anchors: code anchor file paths. GIN indexed.
ALTER TABLE specs ADD COLUMN IF NOT EXISTS refs JSONB NOT NULL DEFAULT '[]';
ALTER TABLE specs ADD COLUMN IF NOT EXISTS anchors JSONB NOT NULL DEFAULT '[]';
CREATE INDEX IF NOT EXISTS idx_specs_refs ON specs USING GIN (refs);
CREATE INDEX IF NOT EXISTS idx_specs_anchors ON specs USING GIN (anchors);
ALTER TABLE specs DROP CONSTRAINT IF EXISTS chk_specs_refs_is_array;
ALTER TABLE specs ADD CONSTRAINT chk_specs_refs_is_array CHECK (jsonb_typeof(refs) = 'array');
ALTER TABLE specs DROP CONSTRAINT IF EXISTS chk_specs_anchors_is_array;
ALTER TABLE specs ADD CONSTRAINT chk_specs_anchors_is_array CHECK (jsonb_typeof(anchors) = 'array');
