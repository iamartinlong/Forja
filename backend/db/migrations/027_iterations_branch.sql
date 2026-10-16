-- 027 iterations 分支化：迭代分支为集成/交付对象，master 仅 iteration→master 可写
ALTER TABLE iterations ADD COLUMN IF NOT EXISTS branch VARCHAR(128) CHECK (branch IS NULL OR branch ~ '^iteration/[a-z0-9-]+-[a-z0-9]{6}$');
ALTER TABLE iterations ADD COLUMN IF NOT EXISTS base_sha VARCHAR(64);
ALTER TABLE iterations ADD COLUMN IF NOT EXISTS merge_sha VARCHAR(64);
CREATE UNIQUE INDEX IF NOT EXISTS idx_iterations_branch ON iterations(project_id, branch) WHERE branch IS NOT NULL;
