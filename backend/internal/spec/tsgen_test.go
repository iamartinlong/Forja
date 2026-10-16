package spec

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateTSDraft_Basic(t *testing.T) {
	bs, _ := json.Marshal(map[string]any{
		"story_id": "S-1",
		"title":    "my feature",
		"acceptance_criteria": []any{
			map[string]any{"given": "a", "when": "b", "then": "c"},
		},
	})
	draft, err := GenerateTSDraft(bs, "my feature")
	require.NoError(t, err)
	assert.Equal(t, "my feature - Auto", draft["test_suite"])
	scenarios, _ := draft["scenarios"].([]map[string]string)
	assert.Len(t, scenarios, 2) // 1 AC + 1 exception
	assert.Equal(t, "AC-1: c", scenarios[0]["name"])
}

func TestGenerateTSDraft_HasException_NoAdd(t *testing.T) {
	bs, _ := json.Marshal(map[string]any{
		"acceptance_criteria": []any{
			map[string]any{"given": "a", "when": "b", "then": "400 error"},
		},
	})
	draft, err := GenerateTSDraft(bs, "t")
	require.NoError(t, err)
	scenarios, _ := draft["scenarios"].([]map[string]string)
	assert.Len(t, scenarios, 1)
}

func TestGenerateTSDraft_InvalidJSON(t *testing.T) {
	_, err := GenerateTSDraft([]byte("not json"), "t")
	assert.Error(t, err)
}
