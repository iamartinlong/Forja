package furnace

import (
	"context"
	"fmt"
	"time"

	"forja/backend/internal/crypto"
	"forja/backend/internal/model"

	"github.com/jackc/pgx/v5"
)

// ErrNoCapacity 无可用炉（healthy && busy < capacity）
var ErrNoCapacity = fmt.Errorf("furnace: no capacity")

// FurnaceRow 最小炉视图，SelectFurnace 返回（M-0 扩展模型注入）
type FurnaceRow struct {
	ID                string
	Name              string
	BaseURL           string
	Capacity          int
	Model             string
	ProviderID        string
	ProviderBaseURL   string
	ApiKey            string
	Headers           string // JSON array string
	AuthMode          string
}

// EnsureDefaultOnBoot 确保 forja-default 存在且容器自愈（幂等，缺则 028 已种，容器侧容错）
func EnsureDefaultOnBoot(ctx context.Context) error {
	if model.Pool == nil {
		return nil
	}
	// 028 已 INSERT ON CONFLICT，本函数仅补 030 前任务 busy 归零自检；容器自愈由 docker Ensure 触发（若可达）
	var cnt int
	_ = model.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM furnaces WHERE id='forja-default'`).Scan(&cnt)
	if cnt == 0 {
		_, _ = model.Pool.Exec(ctx, `INSERT INTO furnaces(id,name,base_url,capacity,status,labels,model_profile) VALUES('forja-default','forja-default','http://opencode:4096',1,'healthy','[]','{"provider_id":"forja","model":"deepseek-v4-flash"}') ON CONFLICT(id) DO NOTHING`)
	} else {
		// 补默认模型绑定（存量空模型时）
		_, _ = model.Pool.Exec(ctx, `UPDATE furnaces SET model_profile='{"provider_id":"forja","model":"deepseek-v4-flash"}'::jsonb WHERE id='forja-default' AND (model_profile IS NULL OR model_profile::text='{}' OR model_profile->>'model' IS NULL OR model_profile->>'model'='')`)
		// 修历史 base_url：forja-default 曾为 forja-default-furnace:4096（per-furnace 容器名），但默认炉复用共享 opencode，需指向 opencode:4096 以使健康探活可达
		_, _ = model.Pool.Exec(ctx, `UPDATE furnaces SET base_url='http://opencode:4096', updated_at=NOW() WHERE id='forja-default' AND base_url='http://forja-default-furnace:4096'`)
	}
	return nil
}

// SelectFurnace 按专炉优先→共享池负载最小 选择 healthy 且 busy<capacity 的炉；无可用返回 ErrNoCapacity
// busy 定义为 tasks 处于 RUNNING 且 furnace_id 为该炉的计数（任务级并发），容量即信号量
// 并发安全：FOR UPDATE OF f SKIP LOCKED 防超售（PG 禁止对外连接 nullable 侧整体
// FOR UPDATE，SQLSTATE 0A000——不带 OF f 会连 model_providers 一起锁而必然报错），
// 需在事务内由调用方 AcquireFurnace 原子占位
func SelectFurnace(ctx context.Context, projectID string) (*FurnaceRow, error) {
	if model.Pool == nil {
		return nil, ErrNoCapacity
	}
	row := &FurnaceRow{}
	// 1. 专炉优先（SKIP LOCKED 防多事务同抢同炉超 capacity）+ 模型注入 JOIN
	err := model.Pool.QueryRow(ctx, `
		SELECT f.id, f.name, f.base_url, f.capacity,
		       COALESCE(f.model_profile->>'model',''), COALESCE(f.model_profile->>'provider_id',''),
		       COALESCE(p.base_url,''), COALESCE(p.api_key_ref,''), COALESCE(p.headers::text,'[]'), COALESCE(p.auth_mode,'Bearer')
		FROM furnaces f LEFT JOIN model_providers p ON p.provider_id = f.model_profile->>'provider_id'
		WHERE f.status='healthy' AND f.project_id=$1
		  AND (SELECT COUNT(*) FROM tasks t WHERE t.furnace_id=f.id AND t.status='RUNNING') < f.capacity
		ORDER BY (SELECT COUNT(*) FROM tasks t WHERE t.furnace_id=f.id AND t.status='RUNNING')::float / f.capacity ASC, random()
		LIMIT 1 FOR UPDATE OF f SKIP LOCKED`, projectID).Scan(&row.ID, &row.Name, &row.BaseURL, &row.Capacity, &row.Model, &row.ProviderID, &row.ProviderBaseURL, &row.ApiKey, &row.Headers, &row.AuthMode)
	if err == nil {
		if row.ApiKey != "" {
			if dec, decErr := crypto.Decrypt(row.ApiKey); decErr == nil {
				row.ApiKey = dec
			}
		}
		return row, nil
	}
	if err != pgx.ErrNoRows {
		return nil, err
	}
	// 2. 共享池
	err = model.Pool.QueryRow(ctx, `
		SELECT f.id, f.name, f.base_url, f.capacity,
		       COALESCE(f.model_profile->>'model',''), COALESCE(f.model_profile->>'provider_id',''),
		       COALESCE(p.base_url,''), COALESCE(p.api_key_ref,''), COALESCE(p.headers::text,'[]'), COALESCE(p.auth_mode,'Bearer')
		FROM furnaces f LEFT JOIN model_providers p ON p.provider_id = f.model_profile->>'provider_id'
		WHERE f.status='healthy' AND f.project_id IS NULL
		  AND (SELECT COUNT(*) FROM tasks t WHERE t.furnace_id=f.id AND t.status='RUNNING') < f.capacity
		ORDER BY (SELECT COUNT(*) FROM tasks t WHERE t.furnace_id=f.id AND t.status='RUNNING')::float / f.capacity ASC, random()
		LIMIT 1 FOR UPDATE OF f SKIP LOCKED`).Scan(&row.ID, &row.Name, &row.BaseURL, &row.Capacity, &row.Model, &row.ProviderID, &row.ProviderBaseURL, &row.ApiKey, &row.Headers, &row.AuthMode)
	if err == nil {
		if row.ApiKey != "" {
			if dec, decErr := crypto.Decrypt(row.ApiKey); decErr == nil {
				row.ApiKey = dec
			}
		}
		return row, nil
	}
	if err == pgx.ErrNoRows {
		return nil, ErrNoCapacity
	}
	return nil, err
}

// SelectAndAcquire 原子选炉并占位：事务内 SELECT FOR UPDATE SKIP LOCKED + UPDATE tasks.furnace_id
func SelectAndAcquire(ctx context.Context, taskID, projectID string) (*FurnaceRow, error) {
	if model.Pool == nil {
		return nil, ErrNoCapacity
	}
	tx, err := model.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	row := &FurnaceRow{}
	err = tx.QueryRow(ctx, `
		SELECT f.id, f.name, f.base_url, f.capacity,
		       COALESCE(f.model_profile->>'model',''), COALESCE(f.model_profile->>'provider_id',''),
		       COALESCE(p.base_url,''), COALESCE(p.api_key_ref,''), COALESCE(p.headers::text,'[]'), COALESCE(p.auth_mode,'Bearer')
		FROM furnaces f LEFT JOIN model_providers p ON p.provider_id = f.model_profile->>'provider_id'
		WHERE f.status='healthy' AND (f.project_id=$1 OR f.project_id IS NULL)
		  AND (SELECT COUNT(*) FROM tasks t WHERE t.furnace_id=f.id AND t.status='RUNNING') < f.capacity
		ORDER BY (f.project_id IS NULL) ASC, (SELECT COUNT(*) FROM tasks t WHERE t.furnace_id=f.id AND t.status='RUNNING')::float / f.capacity ASC, random()
		LIMIT 1 FOR UPDATE OF f SKIP LOCKED`, projectID).Scan(&row.ID, &row.Name, &row.BaseURL, &row.Capacity, &row.Model, &row.ProviderID, &row.ProviderBaseURL, &row.ApiKey, &row.Headers, &row.AuthMode)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrNoCapacity
		}
		return nil, err
	}
	if row.ApiKey != "" {
		if dec, decErr := crypto.Decrypt(row.ApiKey); decErr == nil {
			row.ApiKey = dec
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE tasks SET furnace_id=$1 WHERE id=$2`, row.ID, taskID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return row, nil
}

// AcquireFurnace 原子预约：将 tasks.furnace_id 置为选中炉（供 busy 计数）
func AcquireFurnace(ctx context.Context, taskID, furnaceID string) error {
	if model.Pool == nil {
		return nil
	}
	_, err := model.Pool.Exec(ctx, `UPDATE tasks SET furnace_id=$1 WHERE id=$2`, furnaceID, taskID)
	return err
}

// ReleaseFurnace 释放：tasks 完成时清 furnace_id（由调用方在 terminal 状态置 FAILED/RELEASE_PENDING 后清或保留归档）
func ReleaseFurnace(ctx context.Context, taskID string) error {
	if model.Pool == nil {
		return nil
	}
	_, err := model.Pool.Exec(ctx, `UPDATE tasks SET furnace_id=NULL WHERE id=$1`, taskID)
	return err
}

// StartReaper 5m 清孤儿容器（label=forja.furnace.managed）
func StartReaper(ctx context.Context) {
	go func() {
		t := time.NewTicker(5 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				_ = ReapOrphans(ctx)
			}
		}
	}()
}

func ReapOrphans(ctx context.Context) error {
	if model.Pool == nil {
		return nil
	}
	ids, err := ListManaged(ctx)
	if err != nil {
		return nil
	}
	// DB 存在的 managed 炉 id 集合
	rows, err := model.Pool.Query(ctx, `SELECT id FROM furnaces`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	dbIDs := map[string]bool{}
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		dbIDs[id] = true
		dbIDs["forja-furnace-"+id[:8]] = true // 容器名
	}
	for _, cid := range ids {
		// 容器名或标签含 furnace id，若 DB 无则为孤儿
		isOrphan := true
		for k := range dbIDs {
			if cid == k {
				isOrphan = false
				break
			}
		}
		if isOrphan {
			_ = Remove(context.Background(), cid)
		}
	}
	return nil
}

// RecoverProbeHealthy 探活自愈：unhealthy→healthy 并重置熔断计数窗口（breaker_reset_at=NOW）。
// 探活成功不产生 execution，streak 不会自然衰减——若只翻状态，恢复后 streak 仍 ≥3，
// 下一次 FAILED 会立即再熔断且 UI 显示自相矛盾，故此处必须同 reset。
func RecoverProbeHealthy(ctx context.Context, id string) error {
	_, err := model.Pool.Exec(ctx, `UPDATE furnaces SET status='healthy', breaker_reset_at=NOW(), updated_at=NOW() WHERE id=$1 AND status='unhealthy'`, id)
	return err
}

// StartHealthProbe 30s 探活 3次失败标 unhealthy，1次成功回 healthy
func StartHealthProbe(ctx context.Context) {
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		fails := map[string]int{}
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if model.Pool == nil {
					continue
				}
				rows, err := model.Pool.Query(ctx, `SELECT id, base_url, status FROM furnaces`)
				if err != nil {
					continue
				}
				for rows.Next() {
					var id, url, st string
					_ = rows.Scan(&id, &url, &st)
					healthy := HealthProbe(ctx, url)
					if !healthy {
						fails[id]++
						if fails[id] >= 3 && st == "healthy" {
							_, _ = model.Pool.Exec(ctx, `UPDATE furnaces SET status='unhealthy', updated_at=NOW() WHERE id=$1`, id)
							delete(fails, id)
						}
					} else {
						if st == "unhealthy" {
							_ = RecoverProbeHealthy(ctx, id)
						}
						delete(fails, id)
					}
				}
				rows.Close()
			}
		}
	}()
}
