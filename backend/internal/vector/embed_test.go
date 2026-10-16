package vector

// S2/B1 真 embedding 单测：OpenAI 兼容端点成功 / 降级（500、维度不符、不可达）/ 请求形态。

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func dimServer(t *testing.T, status int, dims int) (*httptest.Server, *[]map[string]any) {
	t.Helper()
	var got []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		got = append(got, req)
		w.Header().Set("Content-Type", "application/json")
		if status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":"boom"}`))
			return
		}
		vec := make([]float64, dims)
		for i := range vec {
			vec[i] = float64(i%10) / 10
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"embedding": vec, "index": 0}}})
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func TestEmbed_RealAPI_Success(t *testing.T) {
	srv, got := dimServer(t, http.StatusOK, Dim)
	t.Setenv("FORJA_API_KEY", "sk-test")
	t.Setenv("FORJA_EMBEDDING_URL", srv.URL)
	t.Setenv("FORJA_EMBEDDING_MODEL", "test-model")
	// CI 置 FORJA_EMBEDDING_MOCK=1 强制 mock；本用例验 live 路径，必须显式关掉
	t.Setenv("FORJA_EMBEDDING_MOCK", "0")

	v := Embed("semantic search query")
	require.Len(t, v, Dim)
	assert.InDelta(t, 0, float64(v[0]), 0.0001, "应返回服务端向量首元素 0")
	assert.Equal(t, "test-model", (*got)[0]["model"], "请求应携带模型名")
	assert.Equal(t, "semantic search query", (*got)[0]["input"])
}

func TestEmbed_FallbackOn500(t *testing.T) {
	srv, _ := dimServer(t, http.StatusInternalServerError, Dim)
	t.Setenv("FORJA_API_KEY", "sk-test")
	t.Setenv("FORJA_EMBEDDING_URL", srv.URL)

	v := Embed("fallback please")
	assert.Equal(t, mockEmbed("fallback please"), v, "5xx 应降级确定性 mock")
}

func TestEmbed_FallbackOnDimMismatch(t *testing.T) {
	srv, _ := dimServer(t, http.StatusOK, 8)
	t.Setenv("FORJA_API_KEY", "sk-test")
	t.Setenv("FORJA_EMBEDDING_URL", srv.URL)

	v := Embed("wrong dims")
	assert.Equal(t, mockEmbed("wrong dims"), v, "维度≠1536 应降级")
}

func TestEmbed_FallbackOnUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // 立即关闭制造不可达
	t.Setenv("FORJA_API_KEY", "sk-test")
	t.Setenv("FORJA_EMBEDDING_URL", url)

	v := Embed("unreachable")
	assert.Equal(t, mockEmbed("unreachable"), v)
}

func TestEmbed_NoKeyMeansMock(t *testing.T) {
	srv, got := dimServer(t, http.StatusOK, Dim)
	t.Setenv("FORJA_API_KEY", "")
	t.Setenv("FORJA_EMBEDDING_URL", srv.URL)

	v := Embed("no key")
	assert.Equal(t, mockEmbed("no key"), v)
	assert.Empty(t, *got, "未配置密钥不应发起外呼")
}

// TestEmbed_MockSwitchOverridesKey 2026-09-09：FORJA_EMBEDDING_MOCK=1 即使有 key
// 也不外呼（CI 确定性：runner envs 给全 job 注入 live key，单测打 live API 在
// DinD DNS 抖动下烧掉 httptest 预算，run319/320 实录）。
func TestEmbed_MockSwitchOverridesKey(t *testing.T) {
	srv, got := dimServer(t, http.StatusOK, Dim)
	t.Setenv("FORJA_API_KEY", "sk-live")
	t.Setenv("FORJA_EMBEDDING_URL", srv.URL)
	t.Setenv("FORJA_EMBEDDING_MOCK", "1")

	v := Embed("mock forced")
	assert.Equal(t, mockEmbed("mock forced"), v)
	assert.Empty(t, *got, "MOCK 开关下不应发起外呼")
}
