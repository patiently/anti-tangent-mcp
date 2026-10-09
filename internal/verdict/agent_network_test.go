package verdict

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParse_BoundaryViolation_AcceptedAndNotFloored(t *testing.T) {
	raw := []byte(`{
		"verdict":"fail",
		"findings":[{
			"severity":"major",
			"category":"boundary_violation",
			"criterion":"boundary:1",
			"evidence":"+ if (reply.contains(\"zip\")) {",
			"suggestion":"give the model the ZIP field and let it ask",
			"same_as":null
		}],
		"next_action":"move the check"
	}`)
	r, err := Parse(raw)
	require.NoError(t, err)
	require.Len(t, r.Findings, 1)
	require.Equal(t, CategoryBoundaryViolation, r.Findings[0].Category)
	require.Equal(t, SeverityMajor, r.Findings[0].Severity)
}

func TestParsePlanFindingsOnly_AcceptsBoundaryViolation(t *testing.T) {
	in := []byte(`{
		"plan_verdict": "warn",
		"plan_quality": "actionable",
		"plan_findings": [
			{"severity":"major","category":"boundary_violation","criterion":"fix_ladder","evidence":"every fix task is rung commitment","suggestion":"add a facts or prompt rung task"}
		],
		"next_action": "add a lower rung"
	}`)
	r, err := ParsePlanFindingsOnly(in)
	require.NoError(t, err)
	require.Equal(t, CategoryBoundaryViolation, r.PlanFindings[0].Category)
	require.Equal(t, SeverityMajor, r.PlanFindings[0].Severity)
}

func TestParse_AgentNetworkServerCategories_RejectedFromReviewerOutput(t *testing.T) {
	for _, cat := range []Category{
		CategoryBoundaryRulesMissing, CategoryPlanKindMissing, CategoryKindConflict,
		CategoryUnknownKind, CategoryRungMissing, CategoryDiffRequired, CategoryBoundaryUnchecked,
	} {
		raw := []byte(`{"verdict":"warn","findings":[{"severity":"minor","category":"` + string(cat) +
			`","criterion":"c","evidence":"e","suggestion":"s","same_as":null}],"next_action":"x"}`)
		_, err := Parse(raw)
		require.Error(t, err, "%s must be server-only", cat)
		for _, s := range [][]byte{Schema(), PlanSchema(), TasksOnlySchema(), PlanFindingsOnlySchema(), PrimeSchema(), ExtractSchema()} {
			require.False(t, strings.Contains(string(s), `"`+string(cat)+`"`), "%s must be in no schema", cat)
		}
	}
}

func TestSchemas_ListBoundaryViolation(t *testing.T) {
	for _, s := range [][]byte{Schema(), PlanSchema(), TasksOnlySchema(), PlanFindingsOnlySchema()} {
		require.Contains(t, string(s), `"boundary_violation"`)
	}
}

func TestParsePlan_ClearsServerSetKinds(t *testing.T) {
	raw := []byte(`{"plan_verdict":"pass","plan_quality":"rigorous","plan_findings":[],"tasks":[` +
		`{"task_index":1,"task_title":"Task 1: x","verdict":"pass","findings":[],"suggested_header_block":"","suggested_header_reason":"",` +
		`"task_kind":"build","rung":"prompt","plan_kind":"agent-network"}],"next_action":"go"}`)
	pr, err := ParsePlan(raw)
	require.NoError(t, err)
	require.Empty(t, pr.Tasks[0].TaskKind, "a reviewer cannot set the task kind: the plan's own header decides it")
	require.Empty(t, pr.Tasks[0].Rung)
	require.Empty(t, pr.Tasks[0].PlanKind)
}
