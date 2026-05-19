package verdict

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSchemas_AcceptConventionDeviationCategory(t *testing.T) {
	// All four reviewer-output schemas must include "convention_deviation"
	// in their findings[].category enum. The test is a substring assertion;
	// JSON-Schema validation of round-trip parsing is covered by the parser
	// tests above. We assert here that the enum text is present so a future
	// schema edit cannot silently drop the value.
	schemas := map[string][]byte{
		"schema.json":                    Schema(),
		"plan_schema.json":               PlanSchema(),
		"tasks_only_schema.json":         TasksOnlySchema(),
		"plan_findings_only_schema.json": PlanFindingsOnlySchema(),
	}
	for name, body := range schemas {
		if !strings.Contains(string(body), `"convention_deviation"`) {
			t.Errorf("%s: missing \"convention_deviation\" in category enum", name)
		}
		// Sanity: additionalProperties:false is still enforced (no regression).
		if !strings.Contains(string(body), `"additionalProperties": false`) {
			t.Errorf("%s: lost additionalProperties:false", name)
		}
	}
}

func TestEdge_FindingEvidenceContainsEscapedBraces(t *testing.T) {
	// Evidence contains `\"} \"` and `{}` characters inside the string.
	// The walker must not be confused by escaped quotes and embedded braces.
	raw := []byte(`{"verdict":"warn","findings":[` +
		`{"severity":"major","category":"other","criterion":"c1","evidence":"oops \"} weird {} body","suggestion":"s1"},` +
		`{"severity":"minor","category":"other","crit`)

	got, ok := ParseResultPartial(raw)
	require.True(t, ok, "should recover the first finding even with escapes in evidence")
	assert.True(t, got.Partial)
	require.Len(t, got.Findings, 1)
	assert.Equal(t, "c1", got.Findings[0].Criterion)
	assert.Equal(t, `oops "} weird {} body`, got.Findings[0].Evidence)
}

func TestEdge_OnlyOpenedFindingsArray(t *testing.T) {
	// Input is *just* the opener — no findings, no comma, just `[`.
	raw := []byte(`{"verdict":"warn","findings":[`)
	got, ok := ParseResultPartial(raw)
	assert.False(t, ok)
	assert.Empty(t, got.Findings)
}

func TestEdge_PlanFindingsRecoveredEvenWithNoTasks(t *testing.T) {
	// plan_findings has a complete finding; tasks[] is missing entirely.
	raw := []byte(`{"plan_verdict":"warn","plan_findings":[` +
		`{"severity":"major","category":"other","criterion":"pf1","evidence":"e","suggestion":"s"},` +
		`{"severity":"min`)
	got, ok := ParsePlanResultPartial(raw)
	require.True(t, ok)
	assert.True(t, got.Partial)
	require.Len(t, got.PlanFindings, 1)
	assert.Equal(t, "pf1", got.PlanFindings[0].Criterion)
}
