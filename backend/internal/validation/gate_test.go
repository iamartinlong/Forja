package validation

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGate_Completeness_BS_MissingTitle(t *testing.T) {
	content, _ := json.Marshal(map[string]any{"story_id": "S-1", "acceptance_criteria": []any{map[string]string{"given": "a", "when": "b", "then": "c"}}})
	res := RunAll("BS", content)
	var found bool
	for _, c := range res.Checks {
		if c.Rule == "completeness" {
			found = true
			assert.False(t, c.Passed)
			assert.True(t, c.Blocking)
		}
	}
	assert.True(t, found)
	assert.False(t, res.Passed)
}

func TestGate_Completeness_BS_OK(t *testing.T) {
	content, _ := json.Marshal(map[string]any{"story_id": "S-1", "title": "t", "acceptance_criteria": []any{map[string]string{"given": "a", "when": "b", "then": "c"}}, "exception": "400", "performance": "ok"})
	// need completeness + exception + non_functional to pass blocking
	res := RunAll("BS", content)
	// BS should have completeness passed
	for _, c := range res.Checks {
		if c.Rule == "completeness" {
			assert.True(t, c.Passed)
		}
	}
}

func TestGate_Exception_Missing(t *testing.T) {
	content, _ := json.Marshal(map[string]any{"story_id": "S-1", "title": "t"})
	res := RunAll("BS", content)
	for _, c := range res.Checks {
		if c.Rule == "exception" {
			assert.False(t, c.Passed)
			assert.Contains(t, c.Message, "400")
		}
	}
}

func TestGate_Exception_OK(t *testing.T) {
	content := json.RawMessage(`{"msg":"exception 400"}`)
	res := RunAll("BS", content)
	for _, c := range res.Checks {
		if c.Rule == "exception" {
			assert.True(t, c.Passed)
		}
	}
}

func TestGate_Clarification_Blocking(t *testing.T) {
	content := json.RawMessage(`{"note":"[NEEDS CLARIFICATION: what?] "}`)
	res := RunAll("BS", content)
	for _, c := range res.Checks {
		if c.Rule == "clarification" {
			assert.False(t, c.Passed)
			assert.True(t, c.Blocking)
		}
	}
	assert.False(t, res.Passed)
}

func TestGate_Testability_BadPhrase(t *testing.T) {
	content := json.RawMessage(`{"desc":"体验良好"}`)
	res := RunAll("BS", content)
	for _, c := range res.Checks {
		if c.Rule == "testability" {
			assert.False(t, c.Passed)
		}
	}
}

func TestGate_NonFunctional_WarningOnly(t *testing.T) {
	content := json.RawMessage(`{"x":1}`)
	res := RunAll("BS", content)
	for _, c := range res.Checks {
		if c.Rule == "non_functional" {
			assert.False(t, c.Passed)
			assert.False(t, c.Blocking)
		}
	}
	// should not block overall if only non_functional fails and others pass? But completeness etc fail, so overall still false; test isolated
}

func TestGate_Contracts_BS_NoRequire(t *testing.T) {
	content, _ := json.Marshal(map[string]any{"story_id": "S-1", "title": "t"})
	res := RunAll("BS", content)
	for _, c := range res.Checks {
		if c.Rule == "contracts" {
			assert.True(t, c.Passed, "BS should not require contracts")
		}
	}
}

func TestGate_Completeness_IS_Missing(t *testing.T) {
	content, _ := json.Marshal(map[string]any{"info": "no paths"})
	res := RunAll("IS", content)
	for _, c := range res.Checks {
		if c.Rule == "completeness" {
			assert.False(t, c.Passed)
		}
	}
}

func TestGate_Completeness_IS_OK(t *testing.T) {
	content, _ := json.Marshal(map[string]any{"paths": map[string]any{"/a": map[string]any{"get": map[string]any{}}}, "exception": "400", "performance": "ok"})
	res := RunAll("IS", content)
	for _, c := range res.Checks {
		if c.Rule == "completeness" {
			assert.True(t, c.Passed)
		}
		if c.Rule == "exception" {
			assert.True(t, c.Passed)
		}
	}
}

func TestGate_Completeness_DS_OK(t *testing.T) {
	content, _ := json.Marshal(map[string]any{"entity_name": "todo", "fields": []any{map[string]any{"name": "id", "type": "uuid"}}, "exception": "400"})
	res := RunAll("DS", content)
	for _, c := range res.Checks {
		if c.Rule == "completeness" {
			assert.True(t, c.Passed)
		}
	}
}

func TestGate_Consistency_AlwaysPass(t *testing.T) {
	content := json.RawMessage(`{"a":1}`)
	res := RunAll("BS", content)
	for _, c := range res.Checks {
		if c.Rule == "consistency" {
			assert.True(t, c.Passed)
		}
		if c.Rule == "constitution" {
			assert.True(t, c.Passed)
		}
	}
}

func TestGate_AllPass(t *testing.T) {
	content, _ := json.Marshal(map[string]any{
		"story_id": "S-1", "title": "t",
		"acceptance_criteria": []any{map[string]string{"given": "a", "when": "b", "then": "c"}},
		"exception": "400 TITLE_REQUIRED", "performance": "concurrency 50",
	})
	res := RunAll("BS", content)
	assert.True(t, res.Passed)
	for _, c := range res.Checks {
		if c.Blocking {
			assert.True(t, c.Passed, "blocking %s should pass", c.Rule)
		}
	}
}

func TestGate_NonFunctional_Pass(t *testing.T) {
	content := json.RawMessage(`{"performance":"ok","concurrency":50}`)
	res := RunAll("BS", content)
	for _, c := range res.Checks {
		if c.Rule == "non_functional" {
			assert.True(t, c.Passed)
			assert.False(t, c.Blocking)
		}
	}
}

func TestGate_Testability_Pass(t *testing.T) {
	content := json.RawMessage(`{"desc":"可量化"}`)
	res := RunAll("BS", content)
	for _, c := range res.Checks {
		if c.Rule == "testability" {
			assert.True(t, c.Passed)
		}
	}
}

func TestGate_Clarification_Pass(t *testing.T) {
	content := json.RawMessage(`{"note":"no clarification"}`)
	res := RunAll("BS", content)
	for _, c := range res.Checks {
		if c.Rule == "clarification" {
			assert.True(t, c.Passed)
		}
	}
}

func TestGate_Completeness_DS_Missing(t *testing.T) {
	content, _ := json.Marshal(map[string]any{"x": 1})
	res := RunAll("DS", content)
	for _, c := range res.Checks {
		if c.Rule == "completeness" {
			assert.False(t, c.Passed)
		}
	}
}

func TestGate_Completeness_IS_OpenAPI(t *testing.T) {
	content, _ := json.Marshal(map[string]any{"openapi": "3.0", "exception": "400"})
	res := RunAll("IS", content)
	for _, c := range res.Checks {
		if c.Rule == "completeness" {
			assert.True(t, c.Passed)
		}
	}
}
