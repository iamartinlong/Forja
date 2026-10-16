package api

// v2.2 技术债批次：furnace 容器 env 构建路径（Docker 副作用已由 TestMain
// 的 FORJA_FURNACE_DISABLE_DOCKER 关闭，保留 env 组装分支覆盖）、
// splitRepo/forgeContext/worker-ready 等小型补测。

import (
	"context"
	"testing"
	"time"

	"forja/backend/internal/gitops"
	"forja/backend/internal/model"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCovV22b_FurnaceEnvBuildPaths(t *testing.T) {
	app := covV21App(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()

	// provider 行（base_url CHECK ^https://；api_key_ref 非加密值走 Decrypt 失败兜底）
	provID := "cov22b" + uuid.NewString()[:6]
	_, err := model.Pool.Exec(ctx, `INSERT INTO model_providers (id, provider_id, display_name, base_url, api_key_ref, auth_mode)
		VALUES (gen_random_uuid(),$1,'cov22b','https://cov22b.invalid','plain-key','Bearer')`, provID)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM model_providers WHERE provider_id=$1`, provID) })

	// 1) ensureFurnaceContainerAsync：provider+model 路径
	fid := uuid.NewString()
	_, err = model.Pool.Exec(ctx, `INSERT INTO furnaces (id,name,base_url,capacity,status,model_profile)
		VALUES ($1,$2,'http://127.0.0.1:1',1,'healthy',$3::jsonb)`,
		fid, "cov22b-"+fid[:8], `{"provider_id":"`+provID+`","model":"m1"}`)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM furnaces WHERE id=$1`, fid) })
	ensureFurnaceContainerAsync(fid)

	// 2) ensureFurnaceContainerAsync：仅 model（无 provider）路径
	fid2 := uuid.NewString()
	_, err = model.Pool.Exec(ctx, `INSERT INTO furnaces (id,name,base_url,capacity,status,model_profile)
		VALUES ($1,$2,'http://127.0.0.1:1',1,'healthy',$3::jsonb)`,
		fid2, "cov22b-"+fid2[:8], `{"model":"only"}`)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM furnaces WHERE id=$1`, fid2) })
	ensureFurnaceContainerAsync(fid2)

	// 3) createFurnace：provider 路径（BaseURL 显式给，避免项目要求）
	code, out := covDo(t, app, "POST", "/api/v1/furnaces",
		`{"name":"cov22b-create-`+uuid.NewString()[:6]+`","base_url":"http://127.0.0.1:1","model_profile":{"provider_id":"`+provID+`","model":"m2"},"auth_ref":"user:pass"}`)
	require.Equal(t, 200, code, "%v", out)
	createdID, _ := covData(t, out)["id"].(string)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM furnaces WHERE id=$1`, createdID) })

	// 4) patch drained：Stop 异步分支（测试环境内部跳过副作用）
	code, out = covDo(t, app, "PATCH", "/api/v1/furnaces/"+fid, `{"status":"drained"}`)
	assert.Equal(t, 200, code, "%v", out)
	// 5) patch healthy：recover Ensure 复用路径
	code, out = covDo(t, app, "PATCH", "/api/v1/furnaces/"+fid, `{"status":"healthy"}`)
	assert.Equal(t, 200, code, "%v", out)

	// 6) delete：Remove 异步分支（测试环境内部跳过）
	code, out = covDo(t, app, "DELETE", "/api/v1/furnaces/"+fid2, "")
	assert.Equal(t, 200, code, "%v", out)

	time.Sleep(250 * time.Millisecond) // 等异步 goroutine 走完
}

func TestCovV22b_SmallHelpers(t *testing.T) {
	ensureTestDB(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	// splitRepo：无斜杠 / 正常
	a, b := splitRepo("noslash")
	assert.Equal(t, "noslash", a)
	assert.Equal(t, "", b)
	a, b = splitRepo("owner/repo")
	assert.Equal(t, "owner", a)
	assert.Equal(t, "repo", b)

	// worker ready server 启动即返回（端口 0 随机，避免占用冲突）
	StartWorkerReadyServer("127.0.0.1:0")

	// forgeContext：无 AGENTS.md 的仓 → 仅基础字段；nil repo 短路
	dir := t.TempDir()
	fctx := forgeContext(context.Background(), uuid.NewString(), "标题", "描述", &gitops.Repo{Dir: dir, FullName: "forja-admin/x"})
	assert.Equal(t, "标题", fctx["task_title"])
	_, hasAgents := fctx["project_agents"]
	assert.False(t, hasAgents)
	_, hasSpecs := fctx["spec_files"]
	assert.False(t, hasSpecs)

	short := forgeContext(context.Background(), uuid.NewString(), "t", "d", nil)
	assert.Equal(t, "t", short["task_title"])
	assert.Nil(t, short["project_agents"])
}
