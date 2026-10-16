package api

// M-CICD-4b webhook 单测：HMAC 验签 / 幂等 / 事件审计。

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"forja/backend/internal/api/respond"
	"forja/backend/internal/model"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const hookSecret = "test-hook-secret"

func sign(t *testing.T, secret string, body []byte) string {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func newHookApp() *fiber.App {
	app := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	pub := app.Group("/api/v1")
	RegisterWebhookRoutes(pub)
	return app
}

func postHook(t *testing.T, app *fiber.App, delivery, sig, event, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/v1/webhooks/gitea", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	if delivery != "" {
		req.Header.Set("X-Gitea-Delivery", delivery)
	}
	if sig != "" {
		req.Header.Set("X-Gitea-Signature", sig)
	}
	if event != "" {
		req.Header.Set("X-Gitea-Event", event)
	}
	resp, err := app.Test(req, 15000)
	require.NoError(t, err)
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestWebhook_SignatureAndDedup(t *testing.T) {
	ensureTestDB(t)
	if model.Pool == nil {
		t.Skip("db unavailable")
	}
	t.Setenv("GITEA_WEBHOOK_SECRET", hookSecret)
	app := newHookApp()

	// 唯一仓库标记：共享库跨运行累积，断言按本轮唯一值过滤
	repo := fmt.Sprintf("forja-admin/hook-%s", uuid.NewString()[:8])
	pushBody := fmt.Sprintf(`{"ref":"refs/heads/master","repository":{"full_name":%q},"sender":{"login":"forja-admin"}}`, repo)
	marker := `%` + repo + `%`
	sig := sign(t, hookSecret, []byte(pushBody))

	// 投递去重已持久化（webhook_deliveries）：id 必须每轮唯一，否则上一轮残留会误判重放
	d1, d2, d3 := "d1-"+repo[len(repo)-8:], "d2-"+repo[len(repo)-8:], "d3-"+repo[len(repo)-8:]
	t.Cleanup(func() {
		_, _ = model.Pool.Exec(context.Background(),
			`DELETE FROM webhook_deliveries WHERE delivery_id = ANY($1)`, []string{d1, d2, d3})
	})

	code, _ := postHook(t, app, d1, "deadbeef", "push", pushBody)
	assert.Equal(t, 401, code)

	code, out := postHook(t, app, d2, sig, "push", pushBody)
	require.Equal(t, 200, code, "%v", out)
	var cnt int
	require.NoError(t, model.Pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM audit_logs WHERE action='repo.push' AND detail::text LIKE $1`, marker).Scan(&cnt))
	assert.Equal(t, 1, cnt)

	code, out = postHook(t, app, d2, sig, "push", pushBody)
	require.Equal(t, 200, code)
	if d, ok := out["data"].(map[string]any); ok {
		assert.Equal(t, true, d["dedup"], "重放应返回 dedup 标记")
	}
	require.NoError(t, model.Pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM audit_logs WHERE action='repo.push' AND detail::text LIKE $1`, marker).Scan(&cnt))
	assert.Equal(t, 1, cnt, "replay should not duplicate audit")

	wf := fmt.Sprintf(`{"repository":{"full_name":%q},"workflow_job":{"name":"build","status":"completed","conclusion":"success"}}`, repo)
	sig2 := sign(t, hookSecret, []byte(wf))
	code, _ = postHook(t, app, d3, sig2, "workflow_job", wf)
	require.Equal(t, 200, code)
	require.NoError(t, model.Pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM audit_logs WHERE action='ci.completed' AND detail::text LIKE $1 AND detail::text LIKE '%success%'`,
		marker).Scan(&cnt))
	assert.Equal(t, 1, cnt)
}

func TestWebhook_PipelineRecords(t *testing.T) {
	ensureTestDB(t)
	if model.Pool == nil {
		t.Skip("db unavailable")
	}
	t.Setenv("GITEA_WEBHOOK_SECRET", hookSecret)
	app := newHookApp()
	ctx := context.Background()

	repo := fmt.Sprintf("forja-admin/hook-%s", uuid.NewString()[:8])
	sha := uuid.NewString() + uuid.NewString() // 64 hex-ish chars
	sha = sha[:64]

	// push → run PENDING（S6：push 不触发流水线，仅登记待构建）
	pushBody := fmt.Sprintf(`{"ref":"refs/heads/master","after":%q,"repository":{"full_name":%q},"sender":{"login":"ci-bot"}}`, sha, repo)
	code, out := postHook(t, app, "pl-"+sha[:8], sign(t, hookSecret, []byte(pushBody)), "push", pushBody)
	require.Equal(t, 200, code, "%v", out)

	var runID string
	var status string
	require.Eventually(t, func() bool {
		return model.Pool.QueryRow(ctx,
			`SELECT id::text, status FROM pipeline_runs WHERE repo=$1 AND sha=$2 AND event='push'`,
			repo, sha).Scan(&runID, &status) == nil
	}, 3*time.Second, 100*time.Millisecond, "push 应建 pipeline_runs 行")
	assert.Equal(t, "PENDING", status)

	// workflow_job success ×4 → 聚合 SUCCESS (U-3: 需 4 预期 job 全绿才 SUCCESS)
	for _, job := range []string{"backend", "frontend", "e2e", "build-push"} {
		wf := fmt.Sprintf(`{"repository":{"full_name":%q},"workflow_job":{"name":%q,"status":"completed","conclusion":"success","head_sha":%q,"started_at":"2026-08-25T00:00:00Z","completed_at":"2026-08-25T00:05:00Z"}}`, repo, job, sha)
		code, _ = postHook(t, app, "wf-"+job+"-"+sha[:8], sign(t, hookSecret, []byte(wf)), "workflow_job", wf)
		require.Equal(t, 200, code)
	}
	require.Eventually(t, func() bool {
		return model.Pool.QueryRow(ctx, `SELECT status FROM pipeline_runs WHERE id=$1`, runID).Scan(&status) == nil && status == "SUCCESS"
	}, 3*time.Second, 100*time.Millisecond, "全 job success 应聚合 SUCCESS")
	var duration int64
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT COALESCE(duration_ms,0) FROM pipeline_jobs WHERE run_id=$1 AND name='backend'`, runID).Scan(&duration))
	assert.Equal(t, int64(5*60*1000), duration, "job 时长应由 started_at/completed_at 推得")

	// 追加 failure job → 聚合 FAILURE (U-3: 4 全绿后任一 failure 仍 FAILURE；需新 delivery_id 避免 dedup)
	wf := fmt.Sprintf(`{"repository":{"full_name":%q},"workflow_job":{"name":"e2e","status":"completed","conclusion":"failure","head_sha":%q}}`, repo, sha)
	code, _ = postHook(t, app, "wf-e2e-fail-"+sha[:8], sign(t, hookSecret, []byte(wf)), "workflow_job", wf)
	require.Equal(t, 200, code)
	require.Eventually(t, func() bool {
		return model.Pool.QueryRow(ctx, `SELECT status FROM pipeline_runs WHERE id=$1`, runID).Scan(&status) == nil && status == "FAILURE"
	}, 3*time.Second, 100*time.Millisecond, "任一 failure 应聚合 FAILURE")

	// 同 delivery 重放不重复落 job
	wfReplay := fmt.Sprintf(`{"repository":{"full_name":%q},"workflow_job":{"name":"e2e","status":"completed","conclusion":"failure","head_sha":%q}}`, repo, sha)
	code, out = postHook(t, app, "wf-e2e-fail-"+sha[:8], sign(t, hookSecret, []byte(wfReplay)), "workflow_job", wfReplay)
	require.Equal(t, 200, code)
	if d, ok := out["data"].(map[string]any); ok {
		assert.Equal(t, true, d["dedup"], "重放应命中持久化去重")
	}
	var jobCnt int
	require.NoError(t, model.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM pipeline_jobs WHERE run_id=$1`, runID).Scan(&jobCnt))
	assert.Equal(t, 4, jobCnt, "重放不应新增 job 行 (U-3: 4 预期 job)")

	_, _ = model.Pool.Exec(ctx, `DELETE FROM pipeline_runs WHERE id=$1`, runID)
	_, _ = model.Pool.Exec(ctx, `DELETE FROM webhook_deliveries WHERE delivery_id IN ($1,$2,$3,$4,$5,$6)`,
		"pl-"+sha[:8], "wf-backend-"+sha[:8], "wf-frontend-"+sha[:8], "wf-e2e-"+sha[:8], "wf-build-push-"+sha[:8], "wf-e2e-fail-"+sha[:8])
}

func TestWebhook_PingNoAudit(t *testing.T) {
	ensureTestDB(t)
	if model.Pool == nil {
		t.Skip("db unavailable")
	}
	t.Setenv("GITEA_WEBHOOK_SECRET", hookSecret)
	app := newHookApp()
	body := `{"zen":"keep it simple"}`
	d := fmt.Sprintf("ping-%d", time.Now().UnixNano())
	code, out := postHook(t, app, d, sign(t, hookSecret, []byte(body)), "ping", body)
	require.Equal(t, 200, code, "%v", out)
	assert.NotNil(t, out["data"])
}
