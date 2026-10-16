-- 011: S3.5 锻造真实性（串行 apply / Attempt 贯通）
-- projects.repo_full_name: 建仓成功后回填 owner/name，runForge 据此 clone（此前只存于审计日志）
-- tasks.commit_sha:        锻造产物 push 后的 commit sha 回写
-- tasks.forge_attempt:     真实尝试轮次（≤3），FAILED→重试循环递增
ALTER TABLE projects ADD COLUMN IF NOT EXISTS repo_full_name VARCHAR(256);
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS commit_sha VARCHAR(64);
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS forge_attempt INT NOT NULL DEFAULT 0;

-- 任务↔规格文件引用（runForge 从工作副本读取内容注入 Agent 上下文）
CREATE TABLE IF NOT EXISTS task_spec_refs (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id    UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    ref        VARCHAR(512) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_task_spec_refs_task ON task_spec_refs(task_id);
