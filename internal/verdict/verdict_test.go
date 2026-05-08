package verdict

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSchema_IsValidJSON(t *testing.T) {
	var schema map[string]any
	require.NoError(t, json.Unmarshal(Schema(), &schema))
	assert.Equal(t, "object", schema["type"])
	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, props, "verdict")
	assert.Contains(t, props, "findings")
	assert.Contains(t, props, "next_action")
}

func TestVerdictConstants(t *testing.T) {
	assert.Equal(t, Verdict("pass"), VerdictPass)
	assert.Equal(t, Verdict("warn"), VerdictWarn)
	assert.Equal(t, Verdict("fail"), VerdictFail)
}

func TestResult_RoundTripsJSON(t *testing.T) {
	r := Result{
		Verdict: VerdictWarn,
		Findings: []Finding{{
			Severity:   SeverityMajor,
			Category:   CategoryScopeDrift,
			Criterion:  "AC #2: must reject empty payloads",
			Evidence:   "handler.go: no length check",
			Suggestion: "Add `if len(body) == 0 { return errEmpty }` at line 42",
		}},
		NextAction: "Add the length check and re-run check_progress.",
	}
	b, err := json.Marshal(r)
	require.NoError(t, err)
	var back Result
	require.NoError(t, json.Unmarshal(b, &back))
	assert.Equal(t, r, back)
}
