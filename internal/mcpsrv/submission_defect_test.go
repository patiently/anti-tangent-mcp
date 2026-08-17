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
