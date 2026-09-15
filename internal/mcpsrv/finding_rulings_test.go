package mcpsrv

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/prompts"
	"github.com/patiently/anti-tangent-mcp/internal/session"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

func TestNormalizeFindingResponses_TrimsDropsEmptyAndEnforcesLimits(t *testing.T) {
	got, err := normalizeFindingResponses([]FindingResponseArg{
		{FindingID: " f_0123abcd ", Response: " answered "},
		{FindingID: "", Response: "no id"},
		{FindingID: "f_89abcdef", Response: "   "},
	})
	require.NoError(t, err)
	assert.Equal(t, []FindingResponseArg{{FindingID: "f_0123abcd", Response: "answered"}}, got)

	_, err = normalizeFindingResponses([]FindingResponseArg{{FindingID: "f_0123abcd", Response: strings.Repeat("x", maxFindingResponseChars+1)}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "finding_responses[0].response must be at most 2000 characters")

	many := make([]FindingResponseArg, maxFindingResponseEntries+1)
	for i := range many {
		many[i] = FindingResponseArg{FindingID: "f_0123abcd", Response: "a"}
	}
	_, err = normalizeFindingResponses(many)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "finding_responses must contain at most 50 entries")
}

func TestNormalizeControllerRulings_EnforcesLimits(t *testing.T) {
	_, err := normalizeControllerRulings([]ControllerRulingArg{{FindingID: "f_0123abcd", Ruling: strings.Repeat("x", maxControllerRulingChars+1)}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "controller_rulings[0].ruling must be at most 2000 characters")
}

func TestWaiveRuled_MatchesTheFingerprintOrAShownSameAs(t *testing.T) {
	fp := verdict.Fingerprint(verdict.CategoryScopeDrift, "", "AC 1")
	rulings := map[string]session.Ruling{fp: {ID: fp + "-2", Text: "ruled"}}
	other := "f_ffffffff"
	fs := []verdict.Finding{
		{Severity: verdict.SeverityMajor, Category: verdict.CategoryScopeDrift, Criterion: "ac 1", Evidence: "by fingerprint"},
		{Severity: verdict.SeverityMajor, Category: verdict.CategoryMissingAC, Criterion: "moved", Evidence: "by same_as", SameAs: strPtr(fp)},
		{Severity: verdict.SeverityMajor, Category: verdict.CategoryMissingAC, Criterion: "moved", Evidence: "unshown same_as", SameAs: &other},
	}
	kept, waived := waiveRuled(fs, "", rulings, map[string]bool{fp: true})
	require.Len(t, kept, 1)
	assert.Equal(t, "unshown same_as", kept[0].Evidence)
	require.Len(t, waived, 2)
	assert.Equal(t, "ruled", waived[0].Ruling)
	assert.Equal(t, "by same_as", waived[1].Evidence)
}

func TestMarkRepeats_EscalatesOnlyAnAnsweredCriticalOrMajorRepeat(t *testing.T) {
	majorID := verdict.Fingerprint(verdict.CategoryScopeDrift, "", "AC 1")
	minorID := verdict.Fingerprint(verdict.CategoryQuality, "", "nit")
	prior := []prompts.PriorFinding{
		{Finding: verdict.Finding{ID: majorID, Severity: verdict.SeverityMajor, Category: verdict.CategoryScopeDrift, Criterion: "AC 1"}, Response: "answered"},
		{Finding: verdict.Finding{ID: minorID, Severity: verdict.SeverityMinor, Category: verdict.CategoryQuality, Criterion: "nit"}, Response: "answered"},
	}
	fs := []verdict.Finding{
		{Severity: verdict.SeverityMinor, Category: verdict.CategoryQuality, Criterion: "nit"},
		{Severity: verdict.SeverityMajor, Category: verdict.CategoryMissingAC, Criterion: "elsewhere", SameAs: strPtr(majorID)},
	}
	escalate := markRepeats(fs, prior, map[string]bool{majorID: true, minorID: true})
	assert.Equal(t, minorID, fs[0].RepeatOf)
	assert.Equal(t, majorID, fs[1].RepeatOf)
	assert.Nil(t, fs[1].SameAs, "same_as is cleared once read")
	assert.Equal(t, []string{majorID}, escalate)
}

func TestBuildCompletionReview_AdvisesOnUnknownIDsAndOmitsRuledFindings(t *testing.T) {
	ruled := verdict.Finding{ID: verdict.Fingerprint(verdict.CategoryScopeDrift, "", "AC 1"), Severity: verdict.SeverityMajor, Category: verdict.CategoryScopeDrift, Criterion: "AC 1"}
	open := verdict.Finding{ID: verdict.Fingerprint(verdict.CategoryQuality, "", "nit"), Severity: verdict.SeverityMinor, Category: verdict.CategoryQuality, Criterion: "nit"}
	state := session.ReviewState{
		PriorFindings: []verdict.Finding{ruled, open},
		IssuedIDs:     map[string]bool{ruled.ID: true, open.ID: true},
	}
	cr := buildCompletionReview(state, nil, state.PriorFindings,
		[]FindingResponseArg{{FindingID: open.ID, Response: "first"}, {FindingID: open.ID, Response: "second"}, {FindingID: "f_00000000", Response: "?"}},
		[]ControllerRulingArg{{FindingID: ruled.ID, Ruling: "Task 7 owns it"}, {FindingID: "f_11111111", Ruling: "?"}})

	require.Len(t, cr.prior, 1, "a ruled prior finding leaves the prompt")
	assert.Equal(t, "second", cr.prior[0].Response, "the last answer to an id wins")
	require.Contains(t, cr.newRulings, ruled.ID)
	assert.Equal(t, verdict.CategoryScopeDrift, cr.newRulings[ruled.ID].Category, "a ruling names what it rules on")
	assert.True(t, cr.shown[ruled.ID], "a ruling's ID counts as shown")

	var criteria []string
	for _, a := range cr.advisories {
		criteria = append(criteria, a.Criterion)
		assert.Equal(t, verdict.SeverityMinor, a.Severity)
	}
	assert.ElementsMatch(t, []string{"finding_responses", "controller_rulings"}, criteria)
}
