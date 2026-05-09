package verdict

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlanSchema_IsValidJSON(t *testing.T) {
	var schema map[string]any
	require.NoError(t, json.Unmarshal(PlanSchema(), &schema))
	assert.Equal(t, "object", schema["type"])
	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, props, "plan_verdict")
	assert.Contains(t, props, "plan_findings")
	assert.Contains(t, props, "tasks")
	assert.Contains(t, props, "next_action")
}

func TestPlanResult_RoundTripsJSON(t *testing.T) {
	r := PlanResult{
		PlanVerdict:  VerdictWarn,
		PlanFindings: []Finding{},
		Tasks: []PlanTaskResult{{
			TaskIndex:             0,
			TaskTitle:             "Task 1: Bootstrap",
			Verdict:               VerdictWarn,
			Findings:              []Finding{{Severity: SeverityMajor, Category: CategoryAmbiguousSpec, Criterion: "header", Evidence: "no Goal", Suggestion: "add Goal"}},
			SuggestedHeaderBlock:  "**Goal:** Initialize repo.\n",
			SuggestedHeaderReason: "task lacks Goal/AC structure",
		}},
		NextAction: "Adopt suggested header for Task 1.",
	}
	b, err := json.Marshal(r)
	require.NoError(t, err)
	var back PlanResult
	require.NoError(t, json.Unmarshal(b, &back))
	assert.Equal(t, r, back)
}

func TestPlanSchema_DefensiveCopy(t *testing.T) {
	a := PlanSchema()
	b := PlanSchema()
	require.Equal(t, a, b)
	// Mutate a; b must remain unchanged (proving Schema() returns a copy).
	a[0] = 'X'
	assert.NotEqual(t, a[0], b[0])
}
