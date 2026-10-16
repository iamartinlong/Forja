-- 033 修正 furnaces 默认容量 2→1，与 forja-default 种子一致
ALTER TABLE furnaces ALTER COLUMN capacity SET DEFAULT 1;
