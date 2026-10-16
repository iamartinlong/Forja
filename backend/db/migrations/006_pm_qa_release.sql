-- 006: PM/QA 角色 + 任务发布状态机
-- 幂等设计：先清除所有"不含 PM/QA"的旧 role CHECK（自动命名不可预知），再重建规范约束

-- 1) users.role
DO $$
DECLARE r record;
BEGIN
  FOR r IN SELECT conname FROM pg_constraint
           WHERE conrelid = 'users'::regclass AND contype = 'c'
             AND pg_get_constraintdef(oid) LIKE '%role%'
             AND pg_get_constraintdef(oid) NOT LIKE '%''PM''%'
  LOOP
    EXECUTE format('ALTER TABLE users DROP CONSTRAINT %I', r.conname);
  END LOOP;
END $$;
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_role_check;
ALTER TABLE users ADD CONSTRAINT users_role_check CHECK (role IN ('Admin','Architect','Developer','Viewer','PM','QA'));

-- 2) project_members.role
DO $$
DECLARE r record;
BEGIN
  FOR r IN SELECT conname FROM pg_constraint
           WHERE conrelid = 'project_members'::regclass AND contype = 'c'
             AND pg_get_constraintdef(oid) LIKE '%role%'
             AND pg_get_constraintdef(oid) NOT LIKE '%''PM''%'
  LOOP
    EXECUTE format('ALTER TABLE project_members DROP CONSTRAINT %I', r.conname);
  END LOOP;
END $$;
ALTER TABLE project_members DROP CONSTRAINT IF EXISTS project_members_role_check;
ALTER TABLE project_members ADD CONSTRAINT project_members_role_check CHECK (role IN ('Admin','Architect','Developer','Viewer','PM','QA'));

-- 3) tasks.status 追加 RELEASE_PENDING / RELEASED
DO $$
DECLARE r record;
BEGIN
  FOR r IN SELECT conname FROM pg_constraint
           WHERE conrelid = 'tasks'::regclass AND contype = 'c'
             AND conname = 'tasks_status_check'
             AND pg_get_constraintdef(oid) NOT LIKE '%RELEASE_PENDING%'
  LOOP
    EXECUTE format('ALTER TABLE tasks DROP CONSTRAINT %I', r.conname);
  END LOOP;
END $$;
ALTER TABLE tasks DROP CONSTRAINT IF EXISTS tasks_status_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_status_check CHECK (status IN ('PENDING','RUNNING','VALIDATING','PASSED','FAILED','FIXED','ESCALATED','RELEASE_PENDING','RELEASED'));
