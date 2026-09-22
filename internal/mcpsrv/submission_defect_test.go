package mcpsrv

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

func f(sev verdict.Severity, cat verdict.Category) verdict.Finding {
	return verdict.Finding{Severity: sev, Category: cat, Criterion: "c", Evidence: "e", Suggestion: "s"}
}

func TestIsSubmissionDefectOnly_AllEvidenceCategories(t *testing.T) {
	got := isSubmissionDefectOnly([]verdict.Finding{
		f(verdict.SeverityMajor, verdict.CategoryInsufficientEvidence),
		f(verdict.SeverityCritical, verdict.CategoryMalformedEvidence),
		f(verdict.SeverityMinor, verdict.CategoryQuality), // minors are ignored
	})
	assert.True(t, got)
}

func TestIsSubmissionDefectOnly_MixedWithGenuineMajor(t *testing.T) {
	got := isSubmissionDefectOnly([]verdict.Finding{
		f(verdict.SeverityMajor, verdict.CategoryInsufficientEvidence),
		f(verdict.SeverityMajor, verdict.CategoryMissingAC),
	})
	assert.False(t, got, "a real code finding disqualifies the whole envelope")
}

func TestIsSubmissionDefectOnly_MinorOnly(t *testing.T) {
	got := isSubmissionDefectOnly([]verdict.Finding{
		f(verdict.SeverityMinor, verdict.CategoryInsufficientEvidence),
	})
	assert.False(t, got, "minors never blocked DONE, so there is nothing to excuse")
}

func TestIsSubmissionDefectOnly_NoFindings(t *testing.T) {
	assert.False(t, isSubmissionDefectOnly(nil))
}

func TestIsSubmissionDefectOnly_CodesceneNotRun(t *testing.T) {
	got := isSubmissionDefectOnly([]verdict.Finding{
		f(verdict.SeverityMajor, verdict.CategoryCodesceneNotRun),
	})
	assert.True(t, got)
}

// TestBlockingCodeFindingIDs_ExcludesReviewerResponseTruncation pins that the
// server's own truncation marker (major CategoryOther, criterion
// reviewer_response — see truncatedResult) never blocks DONE the way a real
// code finding does: there is no code fix for a call that ran out of output
// tokens, only a re-call with a larger budget.
func TestBlockingCodeFindingIDs_ExcludesReviewerResponseTruncation(t *testing.T) {
	truncation := f(verdict.SeverityMajor, verdict.CategoryOther)
	truncation.ID = "trunc-1"
	truncation.Criterion = reviewerResponseCriterion

	codeFinding := f(verdict.SeverityMajor, verdict.CategoryMissingAC)
	codeFinding.ID = "code-1"

	got := blockingCodeFindingIDs([]verdict.Finding{truncation, codeFinding})
	assert.Equal(t, []string{"code-1"}, got, "only the genuine code finding blocks DONE")
}

// Control: an ordinary CategoryOther finding whose criterion is NOT
// reviewer_response still blocks, so the exclusion above is keyed on the
// criterion and not on the category alone.
func TestBlockingCodeFindingIDs_OtherCategoryStillBlocksWhenNotReviewerResponse(t *testing.T) {
	finding := f(verdict.SeverityMajor, verdict.CategoryOther)
	finding.ID = "other-1"

	got := blockingCodeFindingIDs([]verdict.Finding{finding})
	assert.Equal(t, []string{"other-1"}, got)
}
