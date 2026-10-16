-- 034 v1.3-D3 炉熔断手动恢复：breaker_reset_at 截断熔断计数窗口
-- 语义：breakerStreak 只计 reset 之后的连续 FAILED；recover 将 reset 置 now()
-- 且仅 unhealthy→healthy（drained 保持，由排水/恢复开关管理）
ALTER TABLE furnaces ADD COLUMN IF NOT EXISTS breaker_reset_at TIMESTAMPTZ;
