-- 036 项目域基础契约（v2.0 F0.5）
--  1) 基础列：mode/template/stack/default_branch/agents_sha/agents_updated_at
--  2) repo_full_name 非空唯一（防双绑；含两处存量修复）
--  3) 存量回填：历史项目 template='legacy'（无 manifest/规范），default_branch='master'
-- 设计详见 docs/phases/phase-v1-production/v2.0-project-module-design.md §二·五

ALTER TABLE projects ADD COLUMN IF NOT EXISTS mode VARCHAR(16) NOT NULL DEFAULT 'create'
    CHECK (mode IN ('create','import'));
ALTER TABLE projects ADD COLUMN IF NOT EXISTS template VARCHAR(64) NOT NULL DEFAULT 'standard-go-react';
ALTER TABLE projects ADD COLUMN IF NOT EXISTS stack JSONB NOT NULL DEFAULT '{}'::jsonb;
-- default_branch：唯一分支来源（gitops/dispatch/iteration/release 全链路读它；导入 main 仓的前提）
ALTER TABLE projects ADD COLUMN IF NOT EXISTS default_branch VARCHAR(128) NOT NULL DEFAULT 'master';
ALTER TABLE projects ADD COLUMN IF NOT EXISTS agents_sha VARCHAR(64);
ALTER TABLE projects ADD COLUMN IF NOT EXISTS agents_updated_at TIMESTAMPTZ;

-- 存量：历史项目均无 manifest/规范，标 legacy 避免被误认为模板项目
UPDATE projects SET template = 'legacy';

-- 数据修复 1：自举项目仓库指针被早期集成测试串写（S3.5 回填），纠正回 self repo
UPDATE projects SET repo_full_name = 'forja-admin/forja'
 WHERE id = '00000000-0000-0000-0000-000000000001';

-- 数据修复 2：重复 repo 绑定（历史测试项目残留）保留最早，其余解绑
UPDATE projects SET repo_full_name = NULL
 WHERE repo_full_name IS NOT NULL
   AND id NOT IN (
     SELECT DISTINCT ON (repo_full_name) id
       FROM projects
      WHERE repo_full_name IS NOT NULL
      ORDER BY repo_full_name, created_at ASC, id ASC
   );

-- 防双绑：非空唯一
CREATE UNIQUE INDEX IF NOT EXISTS uq_projects_repo_full_name
    ON projects(repo_full_name) WHERE repo_full_name IS NOT NULL;
