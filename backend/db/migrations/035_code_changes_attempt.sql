-- 035 v1.3-forge 校验轮次隔离：code_changes 记 attempt，静态门禁只扫当轮交付
-- 背景（2026-09-07 留言板实测）：静态门禁扫任务全部历史 diff，昨日 attempt 的
-- fmt.Println 残留把今日 files-only 交付误杀，3 轮烧空 ESCALATED 且原因不可见。
-- 每轮分支从 base 重置，历史 diff 对当前工作区无意义，必须按轮隔离。
ALTER TABLE code_changes ADD COLUMN IF NOT EXISTS attempt INT NOT NULL DEFAULT 1;
