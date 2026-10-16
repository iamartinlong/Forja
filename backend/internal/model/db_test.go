package model

// Stage 4 #1：schema_migrations 版本表迁移加固的行为测试
//   - 全量登记 = 迁移文件数
//   - 重复执行幂等（快速路径零重放）
//   - 遗留库基线补戳（有业务表 + 跟踪表空 → stamp 不执行）
//   - 缺失版本选择性补应用（012 为全 IF NOT EXISTS，可安全重放验证）

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestInitDB_FailureClosesPool KI-011 根因回归：InitDB 失败必须关闭已建池。
// 2026-09-09 实录：NewWithConfig 成功后 Ping 失败直接 return，泄漏的池各持
// MinConns=2 常驻连接；叠加启动无限重试（server background heal / worker 无界
// 循环），每次失败 +2 永久连接，打满 max_connections=150 死亡螺旋。
// 观测口：失败后全局 Pool 必须为 nil，且进程 goroutine 不得随失败次数增长
// （泄漏池的后台建连/healthcheck goroutine 会残留）。
func TestInitDB_FailureClosesPool(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://forja:forja@127.0.0.1:1/forja?sslmode=disable&connect_timeout=1")
	runtime.GC()
	time.Sleep(200 * time.Millisecond)
	base := runtime.NumGoroutine()
	for i := 0; i < 5; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := InitDB(ctx)
		cancel()
		require.Error(t, err, "unreachable DB must fail")
		assert.Nil(t, Pool, "failed InitDB must not leave a global pool")
	}
	time.Sleep(500 * time.Millisecond)
	runtime.GC()
	time.Sleep(200 * time.Millisecond)
	assert.Less(t, runtime.NumGoroutine()-base, 5,
		"5 failed InitDB attempts must not leak pool goroutines (KI-011 death spiral)")
}

func TestRunMigrationsVersioned(t *testing.T) {
	if Pool == nil {
		if os.Getenv("DATABASE_URL") == "" {
			os.Setenv("DATABASE_URL", "postgres://forja:forja@localhost:5432/forja?sslmode=disable")
		}
		if err := InitDB(context.Background()); err != nil {
			t.Skipf("DB not available: %v", err)
		}
	}
	ctx := context.Background()

	dir := resolveMigrationsDir()
	require.NotEmpty(t, dir, "migrations dir must resolve from package dir")
	files, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	require.NoError(t, err)
	require.NotEmpty(t, files)

	trackedCount := func() int64 {
		var n int64
		require.NoError(t, Pool.QueryRow(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&n))
		return n
	}

	// InitDB 内部已跑过一次：登记数应等于文件数
	assert.EqualValues(t, len(files), trackedCount(), "all migration files should be tracked")

	// 幂等：再次 runMigrations 零重放且无错
	require.NoError(t, runMigrations(ctx, Pool))
	assert.EqualValues(t, len(files), trackedCount())

	// 选择性补应用：删掉幂等的 033 后重跑，仅它被补上（012 会重建旧锚，改用 033）
	const reapplyable = "033_furnaces_default.sql"
	_, err = Pool.Exec(ctx, `DELETE FROM schema_migrations WHERE version=$1`, reapplyable)
	require.NoError(t, err)
	require.NoError(t, runMigrations(ctx, Pool), "re-applying idempotent 033 should succeed")
	assert.EqualValues(t, len(files), trackedCount())

	var exists bool
	require.NoError(t, Pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, reapplyable).Scan(&exists))
	assert.True(t, exists)

	// 遗留库基线：跟踪表清空后重跑 → 全量补戳不执行（库内对象仍在）
	_, err = Pool.Exec(ctx, `DROP TABLE schema_migrations`)
	require.NoError(t, err)
	require.NoError(t, runMigrations(ctx, Pool), "legacy baseline path must not replay SQL")
	assert.EqualValues(t, len(files), trackedCount(), "baseline stamps every file")
}
