package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"forja/backend/internal/model"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunForge_SuccessEmptyChanges(t *testing.T) {
	taskID := seedForgeTask(t, "PENDING")
	ctx := context.Background()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/session" && r.Method == "POST":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"sess-ok2"}`)
		case r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/session/") && strings.HasSuffix(r.URL.Path, "/message"):
			out := map[string]any{
				"info": map[string]any{
					"structured_output": map[string]any{
						"change_log":      "empty ok",
						"confidence":      0.95,
						"code_changes":    []map[string]string{},
						"action_required": []string{},
					},
				},
				"parts": []map[string]any{{"type": "text", "text": "done"}},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(out)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("OPENCODE_URL", srv.URL)
	t.Setenv("GITEA_TOKEN", "")
	RunForge(ctx, taskID)
	var status string
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT status FROM tasks WHERE id=$1`, taskID).Scan(&status))
	assert.Equal(t, "RELEASE_PENDING", status)
}

func TestRunForge_ActionRequiredEscalated(t *testing.T) {
	taskID := seedForgeTask(t, "PENDING")
	ctx := context.Background()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/session" && r.Method == "POST":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"sess-ar"}`)
		case r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/session/") && strings.HasSuffix(r.URL.Path, "/message"):
			out := map[string]any{
				"info": map[string]any{
					"structured_output": map[string]any{
						"change_log":      "needs human",
						"confidence":      0.8,
						"code_changes":    []map[string]string{},
						"action_required": []string{"need human review"},
					},
				},
				"parts": []map[string]any{{"type": "text", "text": "done"}},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(out)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("OPENCODE_URL", srv.URL)
	t.Setenv("GITEA_TOKEN", "")
	RunForge(ctx, taskID)
	var status string
	require.NoError(t, model.Pool.QueryRow(ctx, `SELECT status FROM tasks WHERE id=$1`, taskID).Scan(&status))
	assert.Equal(t, "ESCALATED", status)
	_ = uuid.NewString
}
