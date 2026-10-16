package testgen

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeneratePact_Basic(t *testing.T) {
	content, _ := json.Marshal(map[string]any{
		"openapi": "3.0.3",
		"paths": map[string]any{
			"/todos": map[string]any{
				"post": map[string]any{
					"summary": "Create Todo",
					"responses": map[string]any{"201": map[string]any{}},
				},
				"get": map[string]any{
					"summary": "List",
					"responses": map[string]any{"200": map[string]any{}},
				},
			},
		},
	})
	pact, err := GeneratePact(content, "Todo API")
	require.NoError(t, err)
	assert.Len(t, pact.Interactions, 2)
	assert.Equal(t, "Todo API", pact.Consumer)
}

func TestGeneratePact_NoPaths(t *testing.T) {
	content, _ := json.Marshal(map[string]any{"openapi": "3.0"})
	_, err := GeneratePact(content, "t")
	assert.Error(t, err)
}

func TestGeneratePact_YAML(t *testing.T) {
	yml := []byte("openapi: 3.0.3\npaths:\n  /todos:\n    post:\n      summary: Create\n      responses:\n        '201': {}\n")
	pact, err := GeneratePact(yml, "t")
	require.NoError(t, err)
	assert.Len(t, pact.Interactions, 1)
	assert.Equal(t, "POST", pact.Interactions[0].Request.Method)
}
