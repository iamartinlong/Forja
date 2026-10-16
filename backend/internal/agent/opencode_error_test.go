package agent

// v1.3 审计回归（2026-09-05）：上游 APIError（欠费/鉴权/4xx5xx）必须透传，
// 不得吞成 "no structured output" 烧完重试轮次（zen 401 Insufficient balance 实录）。

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenCodeAdapter_APIErrorPropagation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/session" && r.Method == http.MethodPost:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"ses_test"}`))
		case strings.HasPrefix(r.URL.Path, "/session/ses_test/message"):
			w.Header().Set("Content-Type", "application/json")
			// 200 + info.error：opencode 对上游失败的真实形态（模型未消耗 token）
			_, _ = w.Write([]byte(`{"info":{"error":{"name":"APIError","data":{"message":"Insufficient balance. Manage your billing.","statusCode":401}},"tokens":{"input":0,"output":0}},"parts":[]}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	a := &OpenCodeAdapter{BaseURL: srv.URL, Client: srv.Client()}
	// Context 预填齐三个增强键，避免 buildPrompt 走 BuildContext（需 DB）
	ctxMap := map[string]any{"regression_constraints": []string{}, "anchors": []string{}, "spec_refs_ids": []string{}}
	_, _, err := a.prompt(context.Background(), "ses_test", AgentInput{TaskID: "t1", Context: ctxMap})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if strings.Contains(err.Error(), "no structured output") {
		t.Fatalf("APIError must not be swallowed as no-structured-output, got: %v", err)
	}
	if !strings.Contains(err.Error(), "Insufficient balance") {
		t.Fatalf("original upstream message should be propagated, got: %v", err)
	}
}

func TestOpenCodeAdapter_StructuredOutputErrorStillRetryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"info":{"error":{"name":"StructuredOutputError"},"tokens":{"input":10,"output":0}},"parts":[]}`))
	}))
	defer srv.Close()

	a := &OpenCodeAdapter{BaseURL: srv.URL, Client: srv.Client()}
	ctxMap := map[string]any{"regression_constraints": []string{}, "anchors": []string{}, "spec_refs_ids": []string{}}
	_, _, err := a.prompt(context.Background(), "ses_test", AgentInput{TaskID: "t1", Context: ctxMap})
	if err == nil || err.Error() != "agent: StructuredOutputError" {
		t.Fatalf("StructuredOutputError semantics changed, got: %v", err)
	}
}

// M0 残项回归（2026-09-12）：prompt 失败时上游已回传的 usage 必须随 ExecuteDetailed 返回
// （失败轮次也要如实计费，task.go 落 execution 行）。
func TestOpenCodeAdapter_ExecuteDetailedKeepsUsageOnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/session" && r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`{"id":"ses_usage"}`))
		case r.URL.Path == "/session/ses_usage/message":
			_, _ = w.Write([]byte(`{"info":{"error":{"name":"StructuredOutputError"},"tokens":{"input":120,"output":30},"cost":0.05},"parts":[]}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	a := &OpenCodeAdapter{BaseURL: srv.URL, Client: srv.Client()}
	ctxMap := map[string]any{"regression_constraints": []string{}, "anchors": []string{}, "spec_refs_ids": []string{}}
	res, err := a.ExecuteDetailed(context.Background(), AgentInput{TaskID: "t1", Context: ctxMap})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if res.TokensIn != 120 || res.TokensOut != 30 || res.Cost != 0.05 {
		t.Fatalf("usage must survive failure, got in=%d out=%d cost=%v (err=%v)", res.TokensIn, res.TokensOut, res.Cost, err)
	}
}
