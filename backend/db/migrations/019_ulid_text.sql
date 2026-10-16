-- U-6+ 存量不动，新 Id 切 ULID（26 Crockford），存量 UUID 仍 TEXT 可容
-- 策略：先丢外键，再改列类型，再重建外键；避免 UUID→TEXT 时的 FK 类型不匹配
-- 0) 预丢弃全部相关外键（IF EXISTS 容错）
-- 0-pre) task_dependencies_check 比较 task_id<>depends_on：ALTER TYPE 重校验时
--   两列类型暂不一致会报 text <> uuid（42883）——先移除，文件末尾以 TEXT 语义重建
ALTER TABLE task_dependencies DROP CONSTRAINT IF EXISTS task_dependencies_check;
ALTER TABLE project_members DROP CONSTRAINT IF EXISTS project_members_project_id_fkey;
ALTER TABLE project_members DROP CONSTRAINT IF EXISTS project_members_new_project_id_fkey;
ALTER TABLE project_members DROP CONSTRAINT IF EXISTS project_members_user_id_fkey;
ALTER TABLE project_members DROP CONSTRAINT IF EXISTS project_members_new_user_id_fkey;
ALTER TABLE projects DROP CONSTRAINT IF EXISTS projects_owner_id_fkey;
ALTER TABLE specs DROP CONSTRAINT IF EXISTS specs_project_id_fkey;
ALTER TABLE spec_versions DROP CONSTRAINT IF EXISTS spec_versions_spec_id_fkey;
ALTER TABLE tasks DROP CONSTRAINT IF EXISTS tasks_project_id_fkey;
ALTER TABLE tasks DROP CONSTRAINT IF EXISTS tasks_spec_id_fkey;
ALTER TABLE audit_logs DROP CONSTRAINT IF EXISTS audit_logs_project_id_fkey;
ALTER TABLE audit_logs DROP CONSTRAINT IF EXISTS audit_logs_target_id_fkey;
ALTER TABLE requirements_inbox DROP CONSTRAINT IF EXISTS requirements_inbox_project_id_fkey;
ALTER TABLE login_logs DROP CONSTRAINT IF EXISTS login_logs_user_id_fkey;
ALTER TABLE refresh_tokens DROP CONSTRAINT IF EXISTS refresh_tokens_user_id_fkey;
ALTER TABLE rules DROP CONSTRAINT IF EXISTS rules_project_id_fkey;
ALTER TABLE spec_counters DROP CONSTRAINT IF EXISTS spec_counters_project_id_fkey;
ALTER TABLE code_changes DROP CONSTRAINT IF EXISTS code_changes_task_id_fkey;
ALTER TABLE task_dependencies DROP CONSTRAINT IF EXISTS task_dependencies_task_id_fkey;
ALTER TABLE task_dependencies DROP CONSTRAINT IF EXISTS task_dependencies_depends_on_fkey;
ALTER TABLE task_spec_refs DROP CONSTRAINT IF EXISTS task_spec_refs_task_id_fkey;
ALTER TABLE test_cases DROP CONSTRAINT IF EXISTS test_cases_task_id_fkey;
ALTER TABLE test_cases DROP CONSTRAINT IF EXISTS test_cases_code_change_id_fkey;
ALTER TABLE test_results DROP CONSTRAINT IF EXISTS test_results_test_case_id_fkey;
ALTER TABLE agent_executions DROP CONSTRAINT IF EXISTS agent_executions_task_id_fkey;
ALTER TABLE pipeline_runs DROP CONSTRAINT IF EXISTS pipeline_runs_project_id_fkey;
ALTER TABLE pipeline_jobs DROP CONSTRAINT IF EXISTS pipeline_jobs_run_id_fkey;
ALTER TABLE deployments DROP CONSTRAINT IF EXISTS deployments_project_id_fkey;
ALTER TABLE inbox_attachments DROP CONSTRAINT IF EXISTS inbox_attachments_project_id_fkey;
ALTER TABLE inbox_attachments DROP CONSTRAINT IF EXISTS inbox_attachments_requirement_id_fkey;
ALTER TABLE project_files DROP CONSTRAINT IF EXISTS project_files_project_id_fkey;
ALTER TABLE requirement_spec_links DROP CONSTRAINT IF EXISTS requirement_spec_links_requirement_id_fkey;
ALTER TABLE requirement_spec_links DROP CONSTRAINT IF EXISTS requirement_spec_links_spec_id_fkey;
-- 1) 子表 FK 列
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='project_members' AND column_name='project_id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE project_members ALTER COLUMN project_id TYPE TEXT USING project_id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='project_members' AND column_name='user_id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE project_members ALTER COLUMN user_id TYPE TEXT USING user_id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='projects' AND column_name='owner_id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE projects ALTER COLUMN owner_id TYPE TEXT USING owner_id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='specs' AND column_name='project_id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE specs ALTER COLUMN project_id TYPE TEXT USING project_id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='spec_versions' AND column_name='spec_id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE spec_versions ALTER COLUMN spec_id TYPE TEXT USING spec_id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='tasks' AND column_name='project_id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE tasks ALTER COLUMN project_id TYPE TEXT USING project_id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='tasks' AND column_name='spec_id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE tasks ALTER COLUMN spec_id TYPE TEXT USING spec_id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='login_logs' AND column_name='user_id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE login_logs ALTER COLUMN user_id TYPE TEXT USING user_id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='refresh_tokens' AND column_name='user_id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE refresh_tokens ALTER COLUMN user_id TYPE TEXT USING user_id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='requirements_inbox' AND column_name='project_id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE requirements_inbox ALTER COLUMN project_id TYPE TEXT USING project_id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='audit_logs' AND column_name='project_id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE audit_logs ALTER COLUMN project_id TYPE TEXT USING project_id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='audit_logs' AND column_name='target_id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE audit_logs ALTER COLUMN target_id TYPE TEXT USING target_id::TEXT'; END IF;
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
  PERFORM 1 FROM information_schema.columns WHERE table_name='code_changes' AND column_name='task_id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE code_changes ALTER COLUMN task_id TYPE TEXT USING task_id::TEXT'; END IF;
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
  PERFORM 1 FROM information_schema.columns WHERE table_name='task_spec_refs' AND column_name='task_id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE task_spec_refs ALTER COLUMN task_id TYPE TEXT USING task_id::TEXT'; END IF;
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
  PERFORM 1 FROM information_schema.columns WHERE table_name='test_results' AND column_name='test_case_id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE test_results ALTER COLUMN test_case_id TYPE TEXT USING test_case_id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='agent_executions' AND column_name='task_id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE agent_executions ALTER COLUMN task_id TYPE TEXT USING task_id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='pipeline_runs' AND column_name='project_id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE pipeline_runs ALTER COLUMN project_id TYPE TEXT USING project_id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='pipeline_jobs' AND column_name='run_id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE pipeline_jobs ALTER COLUMN run_id TYPE TEXT USING run_id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='deployments' AND column_name='project_id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE deployments ALTER COLUMN project_id TYPE TEXT USING project_id::TEXT'; END IF;
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
  PERFORM 1 FROM information_schema.columns WHERE table_name='project_files' AND column_name='project_id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE project_files ALTER COLUMN project_id TYPE TEXT USING project_id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='requirement_spec_links' AND column_name='requirement_id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE requirement_spec_links ALTER COLUMN requirement_id TYPE TEXT USING requirement_id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='requirement_spec_links' AND column_name='spec_id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE requirement_spec_links ALTER COLUMN spec_id TYPE TEXT USING spec_id::TEXT'; END IF;
END $$;
-- 2) 父表 PK 列
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='users' AND column_name='id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE users ALTER COLUMN id TYPE TEXT USING id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='projects' AND column_name='id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE projects ALTER COLUMN id TYPE TEXT USING id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='specs' AND column_name='id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE specs ALTER COLUMN id TYPE TEXT USING id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='spec_versions' AND column_name='id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE spec_versions ALTER COLUMN id TYPE TEXT USING id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='tasks' AND column_name='id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE tasks ALTER COLUMN id TYPE TEXT USING id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='login_logs' AND column_name='id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE login_logs ALTER COLUMN id TYPE TEXT USING id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='refresh_tokens' AND column_name='id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE refresh_tokens ALTER COLUMN id TYPE TEXT USING id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='pipeline_runs' AND column_name='id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE pipeline_runs ALTER COLUMN id TYPE TEXT USING id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='rules' AND column_name='id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE rules ALTER COLUMN id TYPE TEXT USING id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='code_changes' AND column_name='id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE code_changes ALTER COLUMN id TYPE TEXT USING id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='test_cases' AND column_name='id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE test_cases ALTER COLUMN id TYPE TEXT USING id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='test_results' AND column_name='id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE test_results ALTER COLUMN id TYPE TEXT USING id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='agent_executions' AND column_name='id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE agent_executions ALTER COLUMN id TYPE TEXT USING id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='pipeline_jobs' AND column_name='id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE pipeline_jobs ALTER COLUMN id TYPE TEXT USING id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='deployments' AND column_name='id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE deployments ALTER COLUMN id TYPE TEXT USING id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='inbox_attachments' AND column_name='id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE inbox_attachments ALTER COLUMN id TYPE TEXT USING id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='project_files' AND column_name='id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE project_files ALTER COLUMN id TYPE TEXT USING id::TEXT'; END IF;
END $$;
DO $$ BEGIN
  PERFORM 1 FROM information_schema.columns WHERE table_name='requirement_spec_links' AND column_name='requirement_id' AND data_type='uuid';
  IF FOUND THEN EXECUTE 'ALTER TABLE requirement_spec_links ALTER COLUMN requirement_id TYPE TEXT USING requirement_id::TEXT'; END IF;
END $$;
-- 3) 重建外键（TEXT→TEXT）
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='project_members_project_id_fkey') THEN
    EXECUTE 'ALTER TABLE project_members ADD CONSTRAINT project_members_project_id_fkey FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE';
  END IF;
END $$;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='project_members_user_id_fkey') THEN
    EXECUTE 'ALTER TABLE project_members ADD CONSTRAINT project_members_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE';
  END IF;
END $$;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='projects_owner_id_fkey') THEN
    EXECUTE 'ALTER TABLE projects ADD CONSTRAINT projects_owner_id_fkey FOREIGN KEY (owner_id) REFERENCES users(id) ON DELETE SET NULL';
  END IF;
END $$;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='specs_project_id_fkey') THEN
    EXECUTE 'ALTER TABLE specs ADD CONSTRAINT specs_project_id_fkey FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE';
  END IF;
END $$;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='spec_versions_spec_id_fkey') THEN
    EXECUTE 'ALTER TABLE spec_versions ADD CONSTRAINT spec_versions_spec_id_fkey FOREIGN KEY (spec_id) REFERENCES specs(id) ON DELETE CASCADE';
  END IF;
END $$;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='tasks_project_id_fkey') THEN
    EXECUTE 'ALTER TABLE tasks ADD CONSTRAINT tasks_project_id_fkey FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE';
  END IF;
END $$;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='tasks_spec_id_fkey') THEN
    EXECUTE 'ALTER TABLE tasks ADD CONSTRAINT tasks_spec_id_fkey FOREIGN KEY (spec_id) REFERENCES specs(id) ON DELETE SET NULL';
  END IF;
END $$;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='audit_logs_project_id_fkey') THEN
    EXECUTE 'ALTER TABLE audit_logs ADD CONSTRAINT audit_logs_project_id_fkey FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE';
  END IF;
END $$;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='requirements_inbox_project_id_fkey') THEN
    EXECUTE 'ALTER TABLE requirements_inbox ADD CONSTRAINT requirements_inbox_project_id_fkey FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE';
  END IF;
END $$;

-- 3.5) 重建业务 CHECK（此时 task_id/depends_on 均已 TEXT，text <> text 成立）
ALTER TABLE task_dependencies ADD CONSTRAINT task_dependencies_check CHECK (task_id <> depends_on);
