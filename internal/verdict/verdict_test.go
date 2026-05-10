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

func TestParse_ValidJSON(t *testing.T) {
	in := []byte(`{"verdict":"pass","findings":[],"next_action":"ship it"}`)
	r, err := Parse(in)
	require.NoError(t, err)
	assert.Equal(t, VerdictPass, r.Verdict)
	assert.Equal(t, "ship it", r.NextAction)
}

func TestParse_InvalidEnum(t *testing.T) {
	// Use a valid next_action so the test asserts specifically on the verdict
	// error path, not the now-stricter next_action presence check.
	in := []byte(`{"verdict":"maybe","findings":[],"next_action":"x"}`)
	_, err := Parse(in)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "verdict")
}

func TestParse_MalformedJSON(t *testing.T) {
	_, err := Parse([]byte(`{not json`))
	require.Error(t, err)
}

func TestParse_StripsCodeFences(t *testing.T) {
	// Some providers wrap the JSON in ```json fences despite instructions.
	in := []byte("```json\n{\"verdict\":\"pass\",\"findings\":[],\"next_action\":\"ok\"}\n```")
	r, err := Parse(in)
	require.NoError(t, err)
	assert.Equal(t, VerdictPass, r.Verdict)
}

func TestParse_RejectsExtraJSON(t *testing.T) {
	in := []byte(`{"verdict":"pass","findings":[],"next_action":"a"}{"verdict":"fail","findings":[],"next_action":"b"}`)
	_, err := Parse(in)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "extra JSON")
}

func TestRetryHint(t *testing.T) {
	hint := RetryHint()
	assert.Contains(t, hint, "JSON")
}

func TestParse_RejectsMissingNextAction(t *testing.T) {
	in := []byte(`{"verdict":"pass","findings":[]}`)
	_, err := Parse(in)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "next_action")
}

func TestParse_RejectsEmptyNextAction(t *testing.T) {
	in := []byte(`{"verdict":"pass","findings":[],"next_action":""}`)
	_, err := Parse(in)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "next_action")
}
