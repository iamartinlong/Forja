package model

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"forja/backend/db/sqlc"

	"github.com/jackc/pgx/v5/pgxpool"
)

var Pool *pgxpool.Pool
var Q *sqlc.Queries

// DefaultProjectID 种子项目 forja 的固定 UUID（001_init.sql）。
// 仅供系统内部写入兜底（新用户自动绑入、无项目上下文的审计事件等）；
// API 层的 project_id 参数一律必填，缺失返回 400（A6 决策）。
const DefaultProjectID = "00000000-0000-0000-0000-000000000001"

func InitDB(ctx context.Context) error {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "postgres://forja:forja@localhost:5432/forja?sslmode=disable"
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return fmt.Errorf("parse db url: %w", err)
	}
	cfg.MaxConns = 10
	// 保底暖连接：/ready 探针（3s 窗）在 DinD/冷 IO 下新建连接可能超时，MinConns=2
	// 保证池内常驻可用连接（run#190/191 FourProbes 503 教训）
	cfg.MinConns = 2
	// 空闲连接回收：pgxpool 默认永不回收闲置连接——2026-09-05 曾观测 prod api/worker
	// 合计占满 max_connections=100（连保留槽耗尽，psql 均无法进入），定性未复现（KI-011）；
	// 此配置保证池只常驻 MinConns，多余连接 15min 无活动即释放
	cfg.MaxConnIdleTime = 15 * time.Minute
	// 重入先关旧池：InitDB 可被启动重试/后台自愈反复调用，直接覆盖 Pool 会丢弃
	// 旧池对象（其 MinConns 常驻连接永久泄漏）。2026-09-09 KI-011 根因：
	// 失败路径未关池 × 无限重试 = 每次失败 +2 永久连接，打满 150（见下）。
	if Pool != nil {
		Pool.Close()
		Pool = nil
		Q = nil
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return fmt.Errorf("create pool: %w", err)
	}
	// 失败即关池（KI-011 根因修复）：NewWithConfig 成功后后台 MinConns 已开始建连，
	// 此后任何失败返回（ping 不通/迁移失败）都必须先 Close，否则每个泄漏池永久
	// 占用 MinConns=2 个连接；叠加启动无限重试即打满 max_connections 死亡螺旋。
	// （migrate 分支此前已有关闭，此处补 ping 分支并统一收口）
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return fmt.Errorf("ping db: %w", err)
	}
	Pool = pool
	Q = sqlc.New(pool)
	// auto migrate: 版本表加固（Stage 4 #1）——schema_migrations 精确跟踪，
	// 每个迁移文件仅执行一次；失败快速中止，不再静默吞错。
	// 手动兜底仍可用：migrate -path db/migrations -database $DATABASE_URL up
	if err := runMigrations(ctx, pool); err != nil {
		// 迁移失败不得带残缺 schema 服务：弃用连接池回退未就绪态
		// （server 降级 503 / worker log.Fatalf）
		pool.Close()
		Pool = nil
		Q = nil
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

// resolveMigrationsDir 按 候选路径 定位迁移目录（工作目录/二进制/包目录兼容）。
func resolveMigrationsDir() string {
	candidates := []string{"db/migrations", "../db/migrations", "../../db/migrations", "backend/db/migrations", "./backend/db/migrations"}
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && fi.IsDir() {
			return c
		}
	}
	return ""
}

// migrationLockKey 会话级咨询锁键（"forja" 的 hex 截断）：
// api 与 worker 双进程并发启动 / 未来 C1 多副本滚动时串行化迁移。
const migrationLockKey = int64(0x666f726a61)

// runMigrations 版本化迁移执行器：
//
//  1. 会话咨询锁防并发双跑；
//  2. 确保 schema_migrations 跟踪表存在；
//  3. 遗留库基线补戳：库内已有 FORJA 表但跟踪表为空（升级首个启动实例），
//     视为旧「全量重放」方案已收敛出全部对象，逐文件登记不重放——
//     避免对存量库重放非幂等语句（如 001 的裸 CREATE INDEX）；
//  4. 其余场景按序执行缺失版本：单文件事务内 执行+登记，任一失败立即中止返回错误。
func runMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	dir := resolveMigrationsDir()
	if dir == "" {
		return nil
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil || len(files) == 0 {
		return nil
	}
	sort.Strings(files)

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire conn: %w", err)
	}
	defer conn.Release()

	// 会话级锁：持锁连接释放前其他实例的迁移等待排队
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationLockKey); err != nil {
		return fmt.Errorf("migration advisory lock: %w", err)
	}
	// 解锁用独立背景上下文：调用方 ctx 取消（停机）时仍能释放会话锁
	defer func() { _, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", migrationLockKey) }()

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
	    version    VARCHAR(64) PRIMARY KEY,
	    applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		return fmt.Errorf("ensure schema_migrations: %w", err)
	}

	var tracked int64
	if err := conn.QueryRow(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&tracked); err != nil {
		return fmt.Errorf("count schema_migrations: %w", err)
	}

	// 遗留库基线：有业务表但零跟踪记录 → 全量补戳不执行
	// 探测用非限定名走 search_path：与应用内所有查询的表解析方式一致，
	// 非 public schema 部署时不误判为全新库
	var hasProjects bool
	if err := conn.QueryRow(ctx,
		`SELECT to_regclass('projects') IS NOT NULL`).Scan(&hasProjects); err != nil {
		return fmt.Errorf("probe projects table: %w", err)
	}
	if tracked == 0 && hasProjects {
		tx, err := conn.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin baseline tx: %w", err)
		}
		for _, f := range files {
			if _, err := tx.Exec(ctx,
				`INSERT INTO schema_migrations (version) VALUES ($1) ON CONFLICT DO NOTHING`,
				filepath.Base(f)); err != nil {
				_ = tx.Rollback(ctx)
				return fmt.Errorf("baseline stamp %s: %w", filepath.Base(f), err)
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit baseline: %w", err)
		}
		fmt.Printf("migrate: legacy database baselined (%d files stamped)\n", len(files))
		return nil
	}

	for _, f := range files {
		version := filepath.Base(f)
		var done bool
		if err := conn.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, version).Scan(&done); err != nil {
			return fmt.Errorf("lookup %s: %w", version, err)
		}
		if done {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			return fmt.Errorf("read %s: %w", version, err)
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin %s: %w", version, err)
		}
		if _, err := tx.Exec(ctx, string(b)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply %s: %w", version, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("stamp %s: %w", version, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit %s: %w", version, err)
		}
		fmt.Printf("migrated %s\n", version)
	}
	return nil
}

func CloseDB() {
	if Pool != nil {
		Pool.Close()
	}
}
