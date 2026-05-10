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
