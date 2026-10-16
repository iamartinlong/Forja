-- P3.1 A-2: pgvector IVFFLAT for spec_versions + requirements_inbox
-- Requires at least 100 rows for IVFFLAT to be useful; create concurrently to avoid lock

-- spec_versions embedding index
CREATE INDEX IF NOT EXISTS idx_spec_versions_embedding ON spec_versions USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100);
-- requirements_inbox embedding index
CREATE INDEX IF NOT EXISTS idx_inbox_embedding ON requirements_inbox USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100);
