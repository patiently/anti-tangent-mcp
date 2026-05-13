package verdict

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseResultPartial_CompleteInputMatchesStrict(t *testing.T) {
	raw := []byte(`{"verdict":"warn","findings":[{"severity":"major","category":"other","criterion":"c","evidence":"e","suggestion":"s"}],"next_action":"do thing"}`)

	got, ok := ParseResultPartial(raw)
	require.True(t, ok, "complete input should parse successfully")
	assert.False(t, got.Partial, "complete input should not be marked partial")

	var want Result
	require.NoError(t, json.Unmarshal(raw, &want))
	assert.Equal(t, want.Verdict, got.Verdict)
	assert.Equal(t, want.Findings, got.Findings)
	assert.Equal(t, want.NextAction, got.NextAction)
}

func TestParsePlanResultPartial_CompleteInputMatchesStrict(t *testing.T) {
	raw := []byte(`{"plan_verdict":"warn","plan_findings":[{"severity":"major","category":"other","criterion":"c","evidence":"e","suggestion":"s"}],"tasks":[{"task_index":0,"task_title":"T1","verdict":"pass","findings":[],"suggested_header_block":"","suggested_header_reason":""}],"next_action":"go"}`)

	got, ok := ParsePlanResultPartial(raw)
	require.True(t, ok)
	assert.False(t, got.Partial)

	var want PlanResult
	require.NoError(t, json.Unmarshal(raw, &want))
	assert.Equal(t, want.PlanVerdict, got.PlanVerdict)
	assert.Equal(t, want.PlanFindings, got.PlanFindings)
	assert.Len(t, got.Tasks, 1)
}

func TestParseResultPartial_TruncatedMidFinding(t *testing.T) {
	// Three findings; truncation hits in the middle of the 3rd object.
	raw := []byte(`{"verdict":"warn","findings":[` +
		`{"severity":"major","category":"other","criterion":"c1","evidence":"e1","suggestion":"s1"},` +
		`{"severity":"minor","category":"other","criterion":"c2","evidence":"e2","suggestion":"s2"},` +
		`{"severity":"critical","category":"other","crit`)

	got, ok := ParseResultPartial(raw)
	require.True(t, ok, "should recover the first two complete findings")
	assert.True(t, got.Partial)
	assert.Len(t, got.Findings, 2)
	assert.Equal(t, "c1", got.Findings[0].Criterion)
	assert.Equal(t, "c2", got.Findings[1].Criterion)
}

func TestParsePlanResultPartial_TruncatedMidTask(t *testing.T) {
	// Two complete tasks, truncation inside the third task's findings list.
	raw := []byte(`{"plan_verdict":"warn","plan_findings":[],"tasks":[` +
		`{"task_index":0,"task_title":"T0","verdict":"pass","findings":[],"suggested_header_block":"","suggested_header_reason":""},` +
		`{"task_index":1,"task_title":"T1","verdict":"warn","findings":[{"severity":"minor","category":"other","criterion":"c","evidence":"e","suggestion":"s"}],"suggested_header_block":"","suggested_header_reason":""},` +
		`{"task_index":2,"task_title":"T2","verdict":"warn","findings":[{"severity":"major","cat`)

	got, ok := ParsePlanResultPartial(raw)
	require.True(t, ok, "should recover the two complete tasks")
	assert.True(t, got.Partial)
	assert.Len(t, got.Tasks, 2)
	assert.Equal(t, "T0", got.Tasks[0].TaskTitle)
	assert.Equal(t, "T1", got.Tasks[1].TaskTitle)
}

func TestParseResultPartial_TruncatedBeforeAnyFinding(t *testing.T) {
	raw := []byte(`{"verdict":"warn","findings":[{"severity":"maj`)

	got, ok := ParseResultPartial(raw)
	assert.False(t, ok, "no complete finding recovered should return false")
	assert.Empty(t, got.Findings)
}

func TestParseResultPartial_TruncatedInsideStringLiteral(t *testing.T) {
	// Truncation hits inside the evidence string of the first finding.
	raw := []byte(`{"verdict":"warn","findings":[{"severity":"major","category":"other","criterion":"c1","evidence":"this is a long evidence stri`)

	got, ok := ParseResultPartial(raw)
	assert.False(t, ok, "truncation inside a string literal cannot recover")
	assert.Empty(t, got.Findings)
}

func TestParseResultPartial_TruncatedAtTrailingWhitespace(t *testing.T) {
	// Valid complete JSON followed by truncation in trailing whitespace.
	raw := []byte(`{"verdict":"pass","findings":[],"next_action":"go"}    `)

	got, ok := ParseResultPartial(raw)
	require.True(t, ok)
	assert.False(t, got.Partial, "complete JSON with trailing whitespace should not be marked partial")
	assert.Equal(t, Verdict("pass"), got.Verdict)
}

func TestParsePlanResultPartial_TruncatedInsideTaskFindings_RecoversSyntheticTask(t *testing.T) {
	// Two complete tasks; the third task has task_title and verdict parsed,
	// findings[] opened with one complete finding inside, then truncation
	// cuts the second finding. The recovery should emit a SYNTHETIC tasks[2]
	// containing the one complete finding.
	raw := []byte(`{"plan_verdict":"warn","plan_findings":[],"tasks":[` +
		`{"task_index":0,"task_title":"T0","verdict":"pass","findings":[],"suggested_header_block":"","suggested_header_reason":""},` +
		`{"task_index":1,"task_title":"T1","verdict":"pass","findings":[],"suggested_header_block":"","suggested_header_reason":""},` +
		`{"task_index":2,"task_title":"T2","verdict":"warn","findings":[` +
		`{"severity":"major","category":"other","criterion":"tf2","evidence":"e","suggestion":"s"},` +
		`{"severity":"min`)

	got, ok := ParsePlanResultPartial(raw)
	require.True(t, ok, "should recover the two complete tasks plus a synthetic third task")
	assert.True(t, got.Partial)
	require.Len(t, got.Tasks, 3, "third task should be present as synthetic with recovered finding")
	assert.Equal(t, "T2", got.Tasks[2].TaskTitle)
	require.Len(t, got.Tasks[2].Findings, 1, "synthetic task should carry its one complete finding")
	assert.Equal(t, "tf2", got.Tasks[2].Findings[0].Criterion)
}

func TestParsePlanResultPartial_TruncatedInsideTaskBeforeAnyFinding_DropsPartialTask(t *testing.T) {
	// Truncation hits inside the third task's findings[] before any complete
	// finding is emitted. The partial task should be dropped entirely.
	raw := []byte(`{"plan_verdict":"warn","plan_findings":[],"tasks":[` +
		`{"task_index":0,"task_title":"T0","verdict":"pass","findings":[],"suggested_header_block":"","suggested_header_reason":""},` +
		`{"task_index":1,"task_title":"T1","verdict":"pass","findings":[],"suggested_header_block":"","suggested_header_reason":""},` +
		`{"task_index":2,"task_title":"T2","verdict":"warn","findings":[{"severity":"maj`)

	got, ok := ParsePlanResultPartial(raw)
	require.True(t, ok, "the two complete tasks alone are enough to consider recovery successful")
	assert.True(t, got.Partial)
	require.Len(t, got.Tasks, 2, "partial task with no complete finding should be dropped")
}

func TestParsePlanResultPartial_SyntheticTaskRejectsNonIntegerTaskIndex(t *testing.T) {
	// Synthetic-task recovery must NOT silently truncate non-integer
	// task_index values like 2.7 to 2. The synthetic task should fall
	// back to the caller-assigned sentinel (-1).
	raw := []byte(`{"plan_verdict":"warn","plan_findings":[],"tasks":[` +
		`{"task_index":1,"task_title":"T1","verdict":"pass","findings":[],"suggested_header_block":"","suggested_header_reason":""},` +
		`{"task_index":2.7,"task_title":"T2","verdict":"warn","findings":[` +
		`{"severity":"major","category":"other","criterion":"tf2","evidence":"e","suggestion":"s"},` +
		`{"severity":"min`)

	got, ok := ParsePlanResultPartial(raw)
	require.True(t, ok)
	require.Len(t, got.Tasks, 2)
	// The synthetic task with task_index=2.7 must not be silently
	// coerced to 2. It falls back to the caller-assigned sentinel
	// (recoverPlanResult sets the second task's index to len(complete)=1).
	assert.NotEqual(t, 2, got.Tasks[1].TaskIndex, "non-integer task_index 2.7 must not be silently truncated to 2")
}
