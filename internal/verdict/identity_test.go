package verdict

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFingerprint_KnownValue(t *testing.T) {
	sum := sha256.Sum256([]byte("scope_drift\x1f\x1fac 1"))
	assert.Equal(t, "f_"+hex.EncodeToString(sum[:])[:8], Fingerprint(CategoryScopeDrift, "", "AC 1"))
}

func TestFingerprint_StableAcrossCaseWhitespaceAndClosingPunctuation(t *testing.T) {
	want := Fingerprint(CategoryScopeDrift, "", "Returns 200 OK with body ok")
	for _, criterion := range []string{
		"returns 200 ok with body ok",
		"  Returns   200\tOK with\nbody ok  ",
		"Returns 200 OK with body ok.",
		"Returns 200 OK with body ok ;:, .",
	} {
		assert.Equal(t, want, Fingerprint(CategoryScopeDrift, "", criterion), "criterion %q", criterion)
	}
}

func TestFingerprint_DistinguishesCategoryTaskAndCriterion(t *testing.T) {
	base := Fingerprint(CategoryScopeDrift, "", "AC 1")
	assert.NotEqual(t, base, Fingerprint(CategoryQuality, "", "AC 1"))
	assert.NotEqual(t, base, Fingerprint(CategoryScopeDrift, "add parser", "AC 1"))
	assert.NotEqual(t, base, Fingerprint(CategoryScopeDrift, "", "AC 2"))
}

func TestFingerprint_NormalizesTheTaskKey(t *testing.T) {
	assert.Equal(t,
		Fingerprint(CategoryQuality, "add parser", "spec"),
		Fingerprint(CategoryQuality, "  Add   Parser.", "spec"))
}

func TestIDAssigner_SuffixesLaterDuplicatesInOrder(t *testing.T) {
	fs := []Finding{
		{Category: CategoryQuality, Criterion: "comment_hygiene"},
		{Category: CategoryScopeDrift, Criterion: "AC 1"},
		{Category: CategoryQuality, Criterion: "Comment_Hygiene"},
		{Category: CategoryQuality, Criterion: "comment_hygiene"},
	}
	NewIDAssigner().Assign(fs, "")

	hygiene := Fingerprint(CategoryQuality, "", "comment_hygiene")
	assert.Equal(t, hygiene, fs[0].ID)
	assert.Equal(t, Fingerprint(CategoryScopeDrift, "", "AC 1"), fs[1].ID)
	assert.Equal(t, hygiene+"-2", fs[2].ID)
	assert.Equal(t, hygiene+"-3", fs[3].ID)
}

func TestIDAssigner_CountsAcrossListsAndWaivedEntries(t *testing.T) {
	a := NewIDAssigner()
	fs := []Finding{{Category: CategoryScopeDrift, Criterion: "AC 1"}}
	ws := []WaivedFinding{{Category: CategoryScopeDrift, Criterion: "AC 1"}}
	a.Assign(fs, "")
	a.AssignWaived(ws, "")
	assert.Equal(t, fs[0].ID+"-2", ws[0].ID)
}

func TestIDAssigner_AssigningAgainGivesTheSameIDs(t *testing.T) {
	fs := []Finding{
		{Category: CategoryQuality, Criterion: "x"},
		{Category: CategoryQuality, Criterion: "x"},
	}
	NewIDAssigner().Assign(fs, "")
	first := []string{fs[0].ID, fs[1].ID}
	NewIDAssigner().Assign(fs, "")
	assert.Equal(t, first, []string{fs[0].ID, fs[1].ID})
}

func TestBaseID(t *testing.T) {
	assert.Equal(t, "f_0123abcd", BaseID("f_0123abcd"))
	assert.Equal(t, "f_0123abcd", BaseID("f_0123abcd-3"))
}

func TestValidDisplayID(t *testing.T) {
	for _, id := range []string{"f_0123abcd", "f_0123abcd-2", "f_0123abcd-10"} {
		assert.True(t, ValidDisplayID(id), id)
	}
	for _, id := range []string{"", "f_0123abc", "f_0123ABCD", "g_0123abcd", "f_0123abcd-1", "f_0123abcd-0", "f_0123abcd-", "f_0123abcd-2x", "F#3"} {
		assert.False(t, ValidDisplayID(id), id)
	}
}

func TestSchema_SameAsIsRequiredAndNullable(t *testing.T) {
	var s map[string]any
	require.NoError(t, json.Unmarshal(Schema(), &s))
	findings := s["properties"].(map[string]any)["findings"].(map[string]any)
	items := findings["items"].(map[string]any)
	assert.Contains(t, items["required"], "same_as")
	sameAs := items["properties"].(map[string]any)["same_as"].(map[string]any)
	assert.ElementsMatch(t, []any{"string", "null"}, sameAs["type"])
}

func TestParse_DecodesSameAs(t *testing.T) {
	r, err := Parse([]byte(`{"verdict":"warn","findings":[
		{"severity":"major","category":"quality","criterion":"a","evidence":"e","suggestion":"s","same_as":"f_0123abcd"},
		{"severity":"minor","category":"quality","criterion":"b","evidence":"e","suggestion":"s","same_as":null},
		{"severity":"minor","category":"quality","criterion":"c","evidence":"e","suggestion":"s"}
	],"next_action":"n"}`))
	require.NoError(t, err)
	require.Len(t, r.Findings, 3)
	require.NotNil(t, r.Findings[0].SameAs)
	assert.Equal(t, "f_0123abcd", *r.Findings[0].SameAs)
	assert.Nil(t, r.Findings[1].SameAs)
	assert.Nil(t, r.Findings[2].SameAs)
}

func TestParse_ClearsServerSetFindingFields(t *testing.T) {
	r, err := Parse([]byte(`{"verdict":"warn","findings":[
		{"severity":"major","category":"quality","criterion":"a","evidence":"e","suggestion":"s","same_as":null,"id":"f_deadbeef","repeat_of":"f_deadbeef"}
	],"next_action":"n"}`))
	require.NoError(t, err)
	assert.Empty(t, r.Findings[0].ID, "only the server assigns an id")
	assert.Empty(t, r.Findings[0].RepeatOf, "only the server marks a repeat")
}

func TestParseResultPartial_DecodesSameAsAndClearsServerSetFields(t *testing.T) {
	r, ok := ParseResultPartial([]byte(`{"verdict":"warn","findings":[` +
		`{"severity":"major","category":"quality","criterion":"a","evidence":"e","suggestion":"s","same_as":"f_0123abcd","id":"f_deadbeef"},` +
		`{"severity":"minor","cat`))
	require.True(t, ok)
	require.Len(t, r.Findings, 1)
	require.NotNil(t, r.Findings[0].SameAs)
	assert.Equal(t, "f_0123abcd", *r.Findings[0].SameAs)
	assert.Empty(t, r.Findings[0].ID)
}

func TestParsePlan_ClearsSameAsAndServerSetFields(t *testing.T) {
	r, err := ParsePlan([]byte(`{
		"plan_verdict":"warn",
		"plan_findings":[{"severity":"minor","category":"quality","criterion":"c","evidence":"e","suggestion":"s","id":"f_0123abcd","repeat_of":"f_0123abcd","same_as":"f_0123abcd"}],
		"tasks":[{"task_index":1,"task_title":"T1","verdict":"pass","findings":[{"severity":"minor","category":"quality","criterion":"c","evidence":"e","suggestion":"s","same_as":"f_0123abcd"}],"suggested_header_block":"","suggested_header_reason":""}],
		"next_action":"go"
	}`))
	require.NoError(t, err)
	assert.Empty(t, r.PlanFindings[0].ID)
	assert.Empty(t, r.PlanFindings[0].RepeatOf)
	assert.Nil(t, r.PlanFindings[0].SameAs, "plan schemas have no same_as")
	assert.Nil(t, r.Tasks[0].Findings[0].SameAs)
}

func TestParsePlanResultPartial_ClearsServerSetFields(t *testing.T) {
	pr, ok := ParsePlanResultPartial([]byte(`{"plan_verdict":"warn","plan_quality":"actionable","plan_findings":[{"severity":"minor","category":"quality","criterion":"c","evidence":"e","suggestion":"s","id":"f_0123abcd","same_as":"f_0123abcd"}],"tasks":[],"next_action":"go"}`))
	require.True(t, ok)
	assert.Empty(t, pr.PlanFindings[0].ID)
	assert.Nil(t, pr.PlanFindings[0].SameAs)
}
