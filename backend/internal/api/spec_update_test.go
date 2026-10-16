package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"forja/backend/internal/model"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testProjectID = "00000000-0000-0000-0000-000000000001"

// createTestSpec 走 API 创建一个 spec 并返回 id。
func createTestSpec(t *testing.T, app *fiber.App, layer, title string, content map[string]any) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"layer": layer, "title": title, "project_id": testProjectID, "content": content})
	req := httptest.NewRequest("POST", "/api/v1/specs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, 5000)
	require.NoError(t, err)
	require.Equal(t, 200, resp.StatusCode, "create spec failed")
	var env struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&env))
	id, _ := env.Data["id"].(string)
	require.NotEmpty(t, id)
	return id
}

func TestIntegration_UpdateSpec(t *testing.T) {
	app, _ := setupIntegrationApp(t)
	specID := createTestSpec(t, app, "BS", "update me", map[string]any{"story_id": "S-T1", "title": "t"})
	defer func() { _, _ = model.Pool.Exec(context.Background(), `DELETE FROM specs WHERE id=$1`, specID) }()

	// 1. update: 追加版本 + 状态回 DRAFT
	newContent := map[string]any{"story_id": "S-T1", "title": "t2", "acceptance_criteria": []any{map[string]string{"given": "a", "when": "b", "then": "c"}}}
	body, _ := json.Marshal(map[string]any{"title": "updated title", "content": newContent})
	req := httptest.NewRequest("PUT", "/api/v1/specs/"+specID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, 5000)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	var env struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&env))
	version, _ := env.Data["version"].(string)
	assert.NotEmpty(t, version)

	// 2. get: content 已更新、版本号推进
	req2 := httptest.NewRequest("GET", "/api/v1/specs/"+specID, nil)
	resp2, err := app.Test(req2, 5000)
	require.NoError(t, err)
	var got struct {
		Data struct {
			Title          string          `json:"title"`
			Status         string          `json:"status"`
			CurrentVersion string          `json:"current_version"`
			Version        string          `json:"version"`
			Content        json.RawMessage `json:"content"`
		} `json:"data"`
	}
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&got))
	assert.Equal(t, "updated title", got.Data.Title)
	assert.Equal(t, version, got.Data.Version)
	assert.Equal(t, version, got.Data.CurrentVersion)
	assert.Contains(t, string(got.Data.Content), `"t2"`)

	// 3. 再更新一次，版本号严格递增
	body3, _ := json.Marshal(map[string]any{"title": "v3", "content": newContent})
	req3 := httptest.NewRequest("PUT", "/api/v1/specs/"+specID, bytes.NewReader(body3))
	req3.Header.Set("Content-Type", "application/json")
	resp3, err := app.Test(req3, 5000)
	require.NoError(t, err)
	require.Equal(t, 200, resp3.StatusCode)
	var env3 struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.NewDecoder(resp3.Body).Decode(&env3))
	v2, _ := env.Data["version"].(string)
	v3, _ := env3.Data["version"].(string)
	assert.NotEqual(t, v2, v3)
	assert.Regexp(t, `^v\d+\.\d+\.\d+$`, v2, "version must be clean semver, no double dots")
	assert.Regexp(t, `^v\d+\.\d+\.\d+$`, v3)

	// 4. 校验失败场景：BS 含 HOW 关键词被拒
	bad, _ := json.Marshal(map[string]any{"title": "bad", "content": map[string]any{"story_id": "S-T1", "stack": "React"}})
	req4 := httptest.NewRequest("PUT", "/api/v1/specs/"+specID, bytes.NewReader(bad))
	req4.Header.Set("Content-Type", "application/json")
	resp4, err := app.Test(req4, 5000)
	require.NoError(t, err)
	assert.Equal(t, 400, resp4.StatusCode)
}

func TestIntegration_StoryIDAutoGen(t *testing.T) {
	app, _ := setupIntegrationApp(t)

	// story_id 缺失：自动生成 {SLUG}-{seq}
	specID := createTestSpec(t, app, "BS", "auto story", map[string]any{"title": "no story id"})
	defer func() { _, _ = model.Pool.Exec(context.Background(), `DELETE FROM specs WHERE id=$1`, specID) }()

	req := httptest.NewRequest("GET", "/api/v1/specs/"+specID, nil)
	resp, err := app.Test(req, 5000)
	require.NoError(t, err)
	var got struct {
		Data struct {
			Content struct {
				StoryID string `json:"story_id"`
			} `json:"content"`
		} `json:"data"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	sid := got.Data.Content.StoryID
	require.NotEmpty(t, sid, "story_id should be auto generated")
	assert.Regexp(t, `^[A-Z0-9_-]+-\d+$`, sid)

	// story_id 已填写：不覆盖
	specID2 := createTestSpec(t, app, "BS", "manual story", map[string]any{"story_id": "S-MANUAL", "title": "t"})
	defer func() { _, _ = model.Pool.Exec(context.Background(), `DELETE FROM specs WHERE id=$1`, specID2) }()
	req2 := httptest.NewRequest("GET", "/api/v1/specs/"+specID2, nil)
	resp2, err := app.Test(req2, 5000)
	require.NoError(t, err)
	var got2 struct {
		Data struct {
			Content struct {
				StoryID string `json:"story_id"`
			} `json:"content"`
		} `json:"data"`
	}
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&got2))
	assert.Equal(t, "S-MANUAL", got2.Data.Content.StoryID)

	// 非 BS 层：不生成
	specID3 := createTestSpec(t, app, "IS", "is layer", map[string]any{"title": "api"})
	defer func() { _, _ = model.Pool.Exec(context.Background(), `DELETE FROM specs WHERE id=$1`, specID3) }()
	req3 := httptest.NewRequest("GET", "/api/v1/specs/"+specID3, nil)
	resp3, err := app.Test(req3, 5000)
	require.NoError(t, err)
	var got3 struct {
		Data struct {
			Content map[string]any `json:"content"`
		} `json:"data"`
	}
	require.NoError(t, json.NewDecoder(resp3.Body).Decode(&got3))
	_, has := got3.Data.Content["story_id"]
	assert.False(t, has, "IS layer should not get auto story_id")
}

func TestIntegration_StoryIDCounterConcurrent(t *testing.T) {
	app, _ := setupIntegrationApp(t)

	// KI-008：app.Test 无并发安全保证（goroutine 并发调用会偶发丢响应→空 id），
	// 改真监听保留并发语义；业务断言目标不变——
	// 并发创建 BS 时 spec_counters 行锁分配的 story_id 不重不空
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = app.Listener(ln) }()
	t.Cleanup(func() { _ = app.ShutdownWithTimeout(3 * time.Second) })
	baseURL := "http://" + ln.Addr().String()
	// 带超时客户端：防服务端异常时 http.Post 无限挂起拖死整个套件
	httpClient := &http.Client{Timeout: 10 * time.Second}
	// 断言不在 worker goroutine 内做（t.FailNow 仅限测试主 goroutine）：
	// 返回 (id, err)，主 goroutine 统一判定
	postSpec := func(i int) (string, error) {
		body, _ := json.Marshal(map[string]any{
			"layer": "BS", "title": fmt.Sprintf("conc %d", i),
			"project_id": testProjectID,
			"content":    map[string]any{"title": "concurrent"},
		})
		resp, err := httpClient.Post(baseURL+"/api/v1/specs", "application/json", bytes.NewReader(body))
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return "", fmt.Errorf("create spec: HTTP %d", resp.StatusCode)
		}
		var env struct {
			Data map[string]any `json:"data"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
			return "", err
		}
		id, _ := env.Data["id"].(string)
		return id, nil
	}

	const n = 8
	type result struct {
		id  string
		err error
	}
	results := make([]result, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id, err := postSpec(i)
			results[i] = result{id: id, err: err}
		}(i)
	}
	wg.Wait()
	ids := make([]string, n)
	for i, r := range results {
		require.NoError(t, r.err, "worker %d create failed", i)
		ids[i] = r.id
	}
	t.Cleanup(func() {
		for _, id := range ids {
			if id != "" {
				_, _ = model.Pool.Exec(context.Background(), `DELETE FROM specs WHERE id=$1`, id)
			}
		}
	})

	seen := map[string]bool{}
	for _, id := range ids {
		require.NotEmpty(t, id)
		req := httptest.NewRequest("GET", "/api/v1/specs/"+id, nil)
		resp, err := app.Test(req, 5000)
		require.NoError(t, err)
		var got struct {
			Data struct {
				Content struct {
					StoryID string `json:"story_id"`
				} `json:"content"`
			} `json:"data"`
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
		sid := got.Data.Content.StoryID
		require.NotEmpty(t, sid)
		assert.False(t, seen[sid], "duplicated story_id %s", sid)
		seen[sid] = true
	}
}
