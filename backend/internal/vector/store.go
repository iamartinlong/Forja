package vector

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"sync"
	"time"

	"forja/backend/internal/model"

	"github.com/google/uuid"
)

// Embedding dim 1536 per data-model
const Dim = 1536

// embedEnv 真实 embedding 配置（S2/B1）：
//   - FORJA_API_KEY          API 密钥（必填才启用）
//   - FORJA_EMBEDDING_URL    OpenAI 兼容 embeddings 端点，默认 opencode zen
//   - FORJA_EMBEDDING_MODEL  模型名，默认 text-embedding-3-small
//
// 任一条件不满足 / 调用失败 / 返回维度≠Dim 时自动降级 mock（确定性 hash 向量），
// 保证门禁与检索链路永不被外部依赖阻塞。
func embedEnabled() (url, key, model string, ok bool) {
	// CI  determinism 开关（2026-09-09）：runner envs 给全 job 注入 FORJA_API_KEY 后，
	// CI 后端单测 trafic 也会打 live embedding——DinD DNS 抖动时 DNS 超时吃掉
	// httptest 5s 预算，连挂 TestIntegration_Search（run319/320 实录）。
	// CI 中显式置 1，强制 mock（单测只验行为，不验向量质量；生产不用此开关）。
	if os.Getenv("FORJA_EMBEDDING_MOCK") == "1" {
		return "", "", "", false
	}
	key = os.Getenv("FORJA_API_KEY")
	if key == "" {
		return "", "", "", false
	}
	url = os.Getenv("FORJA_EMBEDDING_URL")
	if url == "" {
		url = "https://opencode.ai/zen/go/v1/embeddings"
	}
	model = os.Getenv("FORJA_EMBEDDING_MODEL")
	if model == "" {
		model = "text-embedding-3-small"
	}
	return url, key, model, true
}

var (
	httpClient     = &http.Client{Timeout: 15 * time.Second}
	warnOnce       sync.Once
	lastWarnAt     sync.Map // url -> time.Time，失败日志限频
)

// realEmbed 调用 OpenAI 兼容 embeddings 端点。任何失败返回 error（调用方降级 mock）。
func realEmbed(url, key, model, text string) ([]float32, error) {
	payload, _ := json.Marshal(map[string]any{"model": model, "input": text})
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embeddings http %d: %s", resp.StatusCode, truncateLog(body))
	}
	var out struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
			Index     int       `json:"index"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	if len(out.Data) == 0 || len(out.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("embeddings empty data")
	}
	vec := make([]float32, len(out.Data[0].Embedding))
	for i, v := range out.Data[0].Embedding {
		vec[i] = float32(v)
	}
	// 列维度固定 VECTOR(1536)：不匹配即视为配置错误，降级并告警
	if len(vec) != Dim {
		return nil, fmt.Errorf("embedding dim %d != expected %d (check FORJA_EMBEDDING_MODEL)", len(vec), Dim)
	}
	return vec, nil
}

func truncateLog(b []byte) string {
	s := string(b)
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// warnFallback 失败降级限频告警：同一 URL 每分钟至多一条
func warnFallback(reason string) {
	now := time.Now()
	if v, ok := lastWarnAt.Load(reason); ok && now.Sub(v.(time.Time)) < time.Minute {
		return
	}
	lastWarnAt.Store(reason, now)
	warnOnce.Do(func() {})
	log.Printf("vector: embedding fallback to mock (%s)", reason)
}

// Embed 返回文本向量：优先真实 API（FORJA_API_KEY+URL），否则确定性 mock。
func Embed(text string) []float32 {
	if url, key, model, ok := embedEnabled(); ok {
		if vec, err := realEmbed(url, key, model, text); err == nil {
			return vec
		} else {
			warnFallback(err.Error())
		}
	}
	return mockEmbed(text)
}

func mockEmbed(text string) []float32 {
	h := sha256.Sum256([]byte(text))
	vec := make([]float32, Dim)
	// expand hash deterministically
	for i := 0; i < Dim; i++ {
		b := h[i%32]
		// use binary to spread
		v := float32(b)/255.0*2 - 1 // [-1,1]
		// add position factor to avoid uniform
		v = float32(math.Sin(float64(v)*float64(i%7+1) + float64(i)*0.01))
		vec[i] = v
	}
	// normalize
	var norm float32
	for _, v := range vec {
		norm += v * v
	}
	norm = float32(math.Sqrt(float64(norm)))
	if norm > 0 {
		for i := range vec {
			vec[i] /= norm
		}
	}
	return vec
}

func FormatVector(vec []float32) string {
	b, _ := json.Marshal(vec)
	return string(b)
}

func StoreSpecEmbedding(ctx context.Context, specVersionID string, text string) error {
	if model.Pool == nil {
		return fmt.Errorf("db not ready")
	}
	vec := Embed(text)
	vs := FormatVector(vec)
	// pgvector expects '[0.1,0.2]' string cast to vector
	_, err := model.Pool.Exec(ctx, `UPDATE spec_versions SET embedding = $1::vector WHERE id = $2`, vs, specVersionID)
	return err
}

func StoreInboxEmbedding(ctx context.Context, inboxID string, text string) error {
	if model.Pool == nil {
		return fmt.Errorf("db not ready")
	}
	vec := Embed(text)
	vs := FormatVector(vec)
	_, err := model.Pool.Exec(ctx, `UPDATE requirements_inbox SET embedding = $1::vector WHERE id = $2`, vs, inboxID)
	return err
}

type SearchResult struct {
	ID       string  `json:"id"`
	Title    string  `json:"title"`
	Distance float64 `json:"distance"`
}

// SearchSpecs cosine distance (pgvector <=> is distance); projectID="ALL" for cross-project
func SearchSpecs(ctx context.Context, projectID string, query string, limit int) ([]SearchResult, error) {
	if model.Pool == nil {
		return nil, fmt.Errorf("db not ready")
	}
	vec := Embed(query)
	vs := FormatVector(vec)
	querySQL := `SELECT s.id, s.title, sv.embedding <=> $1::vector AS distance
		FROM specs s JOIN spec_versions sv ON sv.spec_id = s.id
		WHERE sv.embedding IS NOT NULL`
	args := []any{vs}
	if projectID != "" && projectID != "ALL" {
		querySQL += ` AND s.project_id = $2`
		args = append(args, projectID)
		querySQL += ` ORDER BY distance ASC LIMIT $3`
		args = append(args, limit)
	} else {
		querySQL += ` ORDER BY distance ASC LIMIT $2`
		args = append(args, limit)
	}
	rows, err := model.Pool.Query(ctx, querySQL, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var res []SearchResult
	for rows.Next() {
		var id uuid.UUID
		var title string
		var dist float64
		if err := rows.Scan(&id, &title, &dist); err != nil {
			return nil, err
		}
		res = append(res, SearchResult{ID: id.String(), Title: title, Distance: dist})
	}
	return res, rows.Err()
}
