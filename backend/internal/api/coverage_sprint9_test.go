package api

// B1 覆盖率冲刺第四波：G4 回写 worker 路径 / SSE 流式主链（真订阅断开）/
// runForge 成功推送路径 / normalizeRunStatus 全枚举 / LogAuditPublic。

import (
	"context"
	"testing"

	"forja/backend/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLogAuditPublic worker 导出包装
func TestLogAuditPublic(t *testing.T) {
	newPipelineApp(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	LogAuditPublic(ctx, "", "cov-worker", "deploy.dispatched", "repo", "",
		`{"sha":"deadbeef","trigger":"scheduled"}`)
	var cnt int64
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM audit_logs WHERE actor='cov-worker' AND action='deploy.dispatched'`).Scan(&cnt))
	assert.Equal(t, int64(1), cnt)
	_, _ = model.Pool.Exec(ctx, `DELETE FROM audit_logs WHERE actor='cov-worker'`)
}

// TestNormalizeRunStatus 枚举全覆盖
func TestNormalizeRunStatus(t *testing.T) {
	assert.Equal(t, "SUCCESS", normalizeRunStatus("success"))
	assert.Equal(t, "FAILURE", normalizeRunStatus("failed"))
	assert.Equal(t, "FAILURE", normalizeRunStatus("failure"))
	assert.Equal(t, "CANCELLED", normalizeRunStatus("cancelled"))
	assert.Equal(t, "RUNNING", normalizeRunStatus("running"))
	assert.Equal(t, "RUNNING", normalizeRunStatus(""))
}
