package vector

// S1.5 覆盖率冲刺：StoreSpecEmbedding / StoreInboxEmbedding / SearchSpecs 全路径。
// 依赖 pgvector Postgres，无 DB 跳过。

import (
	"context"
	"testing"

	"forja/backend/internal/model"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func requireDB(t *testing.T) {
	t.Helper()
	if model.Pool == nil {
		_ = model.InitDB(context.Background())
	}
	if model.Pool == nil {
		t.Skip("db unavailable")
	}
}

func TestEmbed_WithAPIKeyStillMock(t *testing.T) {
	t.Setenv("FORJA_API_KEY", "sk-test-dummy")
	v := Embed("keyed")
	assert.Len(t, v, Dim)
}

func TestStoreAndSearch_SpecEmbedding(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	specID := uuid.NewString()
	verID := uuid.NewString()
	// 隔离项目：种子项目里历史 embedding 残留会把目标行挤出 top-K（共享 DB 数据量敏感）
	seedProject := "00000000-0000-0000-0000-000000000001"
	isoProject := uuid.NewString()
	_, err := model.Pool.Exec(ctx,
		`INSERT INTO projects (id, name) VALUES ($1,$2)`, isoProject, "vec-iso-"+uuid.NewString()[:8])
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, isoProject) })
	_, err = model.Pool.Exec(ctx,
		`INSERT INTO specs (id, project_id, layer, title) VALUES ($1,$2,'BS','vec-cov-spec')`, specID, isoProject)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM specs WHERE id=$1`, specID) })
	_, err = model.Pool.Exec(ctx,
		`INSERT INTO spec_versions (id, spec_id, version, content) VALUES ($1,$2,'v0.1.0','{"story_id":"S-VEC"}'::jsonb)`, verID, specID)
	require.NoError(t, err)
	_ = seedProject

	// 存向量
	require.NoError(t, StoreSpecEmbedding(ctx, verID, "forge pipeline quality gate"))
	var notNull bool
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT embedding IS NOT NULL FROM spec_versions WHERE id=$1`, verID).Scan(&notNull))
	assert.True(t, notNull, "embedding 应已写入")

	// 未就绪 DB 报错路径（Pool=nil 分支）
	oldPool := model.Pool
	model.Pool = nil
	assert.Error(t, StoreSpecEmbedding(ctx, verID, "x"))
	model.Pool = oldPool

	// 项目内检索命中自身（隔离项目内仅此一行 embedding）
	res, err := SearchSpecs(ctx, isoProject, "forge pipeline", 10)
	require.NoError(t, err)
	found := false
	for _, r := range res {
		if r.ID == specID {
			found = true
			assert.GreaterOrEqual(t, r.Distance, float64(0))
		}
	}
	assert.True(t, found, "项目内语义检索应命中刚写入的 spec")

	// ALL 跨项目路径可执行
	allRes, err := SearchSpecs(ctx, "ALL", "forge pipeline", 10)
	require.NoError(t, err)
	_ = allRes

	// 无关项目返回空集（$2/$3 参数化分支）
	empty, err := SearchSpecs(ctx, uuid.NewString(), "forge pipeline", 10)
	require.NoError(t, err)
	assert.Empty(t, empty)

	// DB 未就绪
	model.Pool = nil
	_, err = SearchSpecs(ctx, seedProject, "x", 5)
	assert.Error(t, err)
	model.Pool = oldPool
}

func TestStoreInboxEmbedding(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	id := uuid.NewString()
	seedProject := "00000000-0000-0000-0000-000000000001"
	_, err := model.Pool.Exec(ctx,
		`INSERT INTO requirements_inbox (id, project_id, source_type, raw_content, content_hash) VALUES ($1,$2,'TEXT_PASTE','vec cov raw','hash-vec')`,
		id, seedProject)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM requirements_inbox WHERE id=$1`, id) })

	require.NoError(t, StoreInboxEmbedding(ctx, id, "raw text for embedding"))
	var notNull bool
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT embedding IS NOT NULL FROM requirements_inbox WHERE id=$1`, id).Scan(&notNull))
	assert.True(t, notNull)

	oldPool := model.Pool
	model.Pool = nil
	assert.Error(t, StoreInboxEmbedding(ctx, id, "x"))
	model.Pool = oldPool
}
