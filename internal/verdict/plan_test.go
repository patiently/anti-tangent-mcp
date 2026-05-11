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

func TestParsePlan_Valid(t *testing.T) {
	in := []byte(`{
		"plan_verdict":"pass",
		"plan_findings":[],
		"tasks":[
			{"task_index":0,"task_title":"T1","verdict":"pass","findings":[],"suggested_header_block":"","suggested_header_reason":""}
		],
		"next_action":"go"
	}`)
	r, err := ParsePlan(in)
	require.NoError(t, err)
	assert.Equal(t, VerdictPass, r.PlanVerdict)
	require.Len(t, r.Tasks, 1)
	assert.Equal(t, "T1", r.Tasks[0].TaskTitle)
}

func TestParsePlan_Malformed(t *testing.T) {
	_, err := ParsePlan([]byte(`{not json`))
	require.Error(t, err)
}

func TestParsePlan_InvalidPlanVerdict(t *testing.T) {
	in := []byte(`{"plan_verdict":"maybe","plan_findings":[],"tasks":[],"next_action":"x"}`)
	_, err := ParsePlan(in)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "plan_verdict")
}

func TestParsePlan_InvalidTaskVerdict(t *testing.T) {
	in := []byte(`{
		"plan_verdict":"pass",
		"plan_findings":[],
		"tasks":[{"task_index":0,"task_title":"T","verdict":"meh","findings":[],"suggested_header_block":"","suggested_header_reason":""}],
		"next_action":"x"
	}`)
	_, err := ParsePlan(in)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "task[0]")
}

func TestParsePlan_InvalidFindingSeverity(t *testing.T) {
	in := []byte(`{
		"plan_verdict":"warn",
		"plan_findings":[{"severity":"oops","category":"other","criterion":"c","evidence":"e","suggestion":"s"}],
		"tasks":[],
		"next_action":"x"
	}`)
	_, err := ParsePlan(in)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "severity")
}

func TestParsePlan_InvalidFindingCategory(t *testing.T) {
	in := []byte(`{
		"plan_verdict":"warn",
		"plan_findings":[{"severity":"major","category":"made_up","criterion":"c","evidence":"e","suggestion":"s"}],
		"tasks":[],
		"next_action":"x"
	}`)
	_, err := ParsePlan(in)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "category")
}

func TestParsePlan_RejectsExtraJSON(t *testing.T) {
	in := []byte(`{"plan_verdict":"pass","plan_findings":[],"tasks":[],"next_action":"a"}{"plan_verdict":"fail","plan_findings":[],"tasks":[],"next_action":"b"}`)
	_, err := ParsePlan(in)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "extra JSON")
}

func TestParsePlan_StripsCodeFences(t *testing.T) {
	in := []byte("```json\n{\"plan_verdict\":\"pass\",\"plan_findings\":[],\"tasks\":[],\"next_action\":\"ok\"}\n```")
	r, err := ParsePlan(in)
	require.NoError(t, err)
	assert.Equal(t, VerdictPass, r.PlanVerdict)
}

// ---------------------------------------------------------------------------
// PlanFindingsOnly tests
// ---------------------------------------------------------------------------

func TestPlanFindingsOnlySchema_IsValidJSON(t *testing.T) {
	var schema map[string]any
	require.NoError(t, json.Unmarshal(PlanFindingsOnlySchema(), &schema))
	assert.Equal(t, "object", schema["type"])
	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, props, "plan_verdict")
	assert.Contains(t, props, "plan_findings")
	assert.Contains(t, props, "next_action")
	assert.NotContains(t, props, "tasks")
}

func TestPlanFindingsOnlySchema_DefensiveCopy(t *testing.T) {
	a := PlanFindingsOnlySchema()
	b := PlanFindingsOnlySchema()
	require.Equal(t, a, b)
	// Mutate a; b must remain unchanged (proving Schema() returns a copy).
	a[0] = 'X'
	assert.NotEqual(t, a[0], b[0])
}

func TestParsePlanFindingsOnly_Valid(t *testing.T) {
	in := []byte(`{
		"plan_verdict": "warn",
		"plan_findings": [
			{"severity":"major","category":"ambiguous_spec","criterion":"c","evidence":"e","suggestion":"s"}
		],
		"next_action": "clarify before dispatch"
	}`)
	r, err := ParsePlanFindingsOnly(in)
	require.NoError(t, err)
	assert.Equal(t, VerdictWarn, r.PlanVerdict)
	require.Len(t, r.PlanFindings, 1)
	assert.NotEmpty(t, r.NextAction)
}

func TestParsePlanFindingsOnly_RejectsInvalid(t *testing.T) {
	t.Run("missing plan_verdict", func(t *testing.T) {
		in := []byte(`{"plan_findings":[],"next_action":"x"}`)
		_, err := ParsePlanFindingsOnly(in)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "plan_verdict")
	})
	t.Run("invalid verdict enum", func(t *testing.T) {
		in := []byte(`{"plan_verdict":"maybe","plan_findings":[],"next_action":"x"}`)
		_, err := ParsePlanFindingsOnly(in)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "plan_verdict")
	})
	t.Run("empty next_action", func(t *testing.T) {
		in := []byte(`{"plan_verdict":"pass","plan_findings":[],"next_action":""}`)
		_, err := ParsePlanFindingsOnly(in)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "next_action")
	})
	t.Run("missing plan_findings", func(t *testing.T) {
		in := []byte(`{"plan_verdict":"pass","next_action":"x"}`)
		_, err := ParsePlanFindingsOnly(in)
		require.Error(t, err)
	})
	t.Run("tasks field rejected", func(t *testing.T) {
		in := []byte(`{"plan_verdict":"pass","plan_findings":[],"next_action":"x","tasks":[]}`)
		_, err := ParsePlanFindingsOnly(in)
		require.Error(t, err)
	})
}
