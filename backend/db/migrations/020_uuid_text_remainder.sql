-- 020: convert remaining uuid columns to TEXT (follow-up to 019, fix mixed-type joins like users.id TEXT vs requirements_inbox.created_by uuid)
-- strategy: drop FKs if any, alter using ::TEXT, recreate FKs as TEXT
ALTER TABLE project_files DROP CONSTRAINT IF EXISTS project_files_created_by_fkey;
ALTER TABLE spec_versions DROP CONSTRAINT IF EXISTS spec_versions_created_by_fkey;
ALTER TABLE task_spec_refs DROP CONSTRAINT IF EXISTS task_spec_refs_spec_id_fkey;
ALTER TABLE task_dependencies DROP CONSTRAINT IF EXISTS task_dependencies_check;

DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='requirements_inbox' AND column_name='id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE requirements_inbox ALTER COLUMN id TYPE TEXT USING id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='requirements_inbox' AND column_name='created_by' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE requirements_inbox ALTER COLUMN created_by TYPE TEXT USING created_by::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='requirements_inbox' AND column_name='cluster_id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE requirements_inbox ALTER COLUMN cluster_id TYPE TEXT USING cluster_id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='audit_logs' AND column_name='id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE audit_logs ALTER COLUMN id TYPE TEXT USING id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='code_changes' AND column_name='id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE code_changes ALTER COLUMN id TYPE TEXT USING id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='code_changes' AND column_name='task_id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE code_changes ALTER COLUMN task_id TYPE TEXT USING task_id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='inbox_attachments' AND column_name='id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE inbox_attachments ALTER COLUMN id TYPE TEXT USING id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='inbox_attachments' AND column_name='project_id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE inbox_attachments ALTER COLUMN project_id TYPE TEXT USING project_id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='inbox_attachments' AND column_name='requirement_id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE inbox_attachments ALTER COLUMN requirement_id TYPE TEXT USING requirement_id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='project_files' AND column_name='id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE project_files ALTER COLUMN id TYPE TEXT USING id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='project_files' AND column_name='project_id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE project_files ALTER COLUMN project_id TYPE TEXT USING project_id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='project_files' AND column_name='created_by' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE project_files ALTER COLUMN created_by TYPE TEXT USING created_by::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='requirement_spec_links' AND column_name='requirement_id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE requirement_spec_links ALTER COLUMN requirement_id TYPE TEXT USING requirement_id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='requirement_spec_links' AND column_name='spec_id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE requirement_spec_links ALTER COLUMN spec_id TYPE TEXT USING spec_id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='rules' AND column_name='id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE rules ALTER COLUMN id TYPE TEXT USING id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='rules' AND column_name='project_id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE rules ALTER COLUMN project_id TYPE TEXT USING project_id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='spec_counters' AND column_name='project_id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE spec_counters ALTER COLUMN project_id TYPE TEXT USING project_id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='spec_versions' AND column_name='created_by' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE spec_versions ALTER COLUMN created_by TYPE TEXT USING created_by::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='task_dependencies' AND column_name='task_id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE task_dependencies ALTER COLUMN task_id TYPE TEXT USING task_id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='task_dependencies' AND column_name='depends_on' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE task_dependencies ALTER COLUMN depends_on TYPE TEXT USING depends_on::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='task_spec_refs' AND column_name='id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE task_spec_refs ALTER COLUMN id TYPE TEXT USING id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='task_spec_refs' AND column_name='task_id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE task_spec_refs ALTER COLUMN task_id TYPE TEXT USING task_id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='tasks' AND column_name='assigned_to' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE tasks ALTER COLUMN assigned_to TYPE TEXT USING assigned_to::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='test_cases' AND column_name='id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE test_cases ALTER COLUMN id TYPE TEXT USING id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='test_cases' AND column_name='task_id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE test_cases ALTER COLUMN task_id TYPE TEXT USING task_id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='test_cases' AND column_name='code_change_id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE test_cases ALTER COLUMN code_change_id TYPE TEXT USING code_change_id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='test_results' AND column_name='id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE test_results ALTER COLUMN id TYPE TEXT USING id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='test_results' AND column_name='test_case_id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE test_results ALTER COLUMN test_case_id TYPE TEXT USING test_case_id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='agent_executions' AND column_name='id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE agent_executions ALTER COLUMN id TYPE TEXT USING id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='agent_executions' AND column_name='task_id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE agent_executions ALTER COLUMN task_id TYPE TEXT USING task_id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='audit_logs' AND column_name='id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE audit_logs ALTER COLUMN id TYPE TEXT USING id::TEXT'; END IF;
END $$;

-- recreate FKs/checks as TEXT (if they existed, now TEXT->TEXT)
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='task_dependencies_check') THEN
    EXECUTE 'ALTER TABLE task_dependencies ADD CONSTRAINT task_dependencies_check CHECK (task_id <> depends_on)';
  END IF;
END $$;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='requirements_inbox_created_by_fkey') THEN
    EXECUTE 'ALTER TABLE requirements_inbox ADD CONSTRAINT requirements_inbox_created_by_fkey FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE SET NULL';
  END IF;
END $$;
