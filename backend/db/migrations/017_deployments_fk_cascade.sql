-- 017: deployments project_id FK 加 ON DELETE CASCADE
-- 016 创建的外键无 CASCADE，项目 purge 时 deployments 残留导致 FK 冲突；重建为 CASCADE。
-- 兼容存量库：无论约束名是否为默认 deployments_project_id_fkey 均尝试清理后重建。
DO $$
DECLARE
  r RECORD;
BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.table_constraints
             WHERE table_name='deployments' AND constraint_type='FOREIGN KEY'
               AND constraint_name='deployments_project_id_fkey') THEN
    ALTER TABLE deployments DROP CONSTRAINT deployments_project_id_fkey;
  ELSE
    FOR r IN SELECT conname FROM pg_constraint
              WHERE conrelid='deployments'::regclass AND contype='f'
                AND conkey @> ARRAY[(SELECT attnum FROM pg_attribute WHERE attrelid='deployments'::regclass AND attname='project_id')]
    LOOP
      EXECUTE format('ALTER TABLE deployments DROP CONSTRAINT %I', r.conname);
    END LOOP;
  END IF;
END $$;

-- 重建为 CASCADE；列已在 016 存在，此处仅重建约束（若已存在则跳过）
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM information_schema.table_constraints
                 WHERE table_name='deployments' AND constraint_name='deployments_project_id_fkey') THEN
    ALTER TABLE deployments ADD CONSTRAINT deployments_project_id_fkey
      FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE;
  END IF;
END $$;
