package mcpsrv

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

func TestSuppressUnverifiableCodebaseClaim_MultiSymbolClaim_SinglePathMatch(t *testing.T) {
	findings := []verdict.Finding{{
		Severity:   verdict.SeverityMinor,
		Category:   verdict.CategoryUnverifiableCodebaseClaim,
		Criterion:  "spec",
		Evidence:   `claim 'XService.findFoo at path/to/file.kt:L42 returns a sorted slice'`,
		Suggestion: "verify",
	}}
	cvr := []string{"path/to/file.kt"}
	out := suppressUnverifiableCodebaseClaim(findings, cvr)
	require.Empty(t, out, "claim suppressed by single-substring match")
}

func TestSuppressUnverifiableCodebaseClaim_NoMatch_Retained(t *testing.T) {
	findings := []verdict.Finding{{
		Severity:   verdict.SeverityMinor,
		Category:   verdict.CategoryUnverifiableCodebaseClaim,
		Criterion:  "spec",
		Evidence:   `claim 'YService.bar in elsewhere/file.kt'`,
		Suggestion: "verify",
	}}
	cvr := []string{"path/to/file.kt"}
	out := suppressUnverifiableCodebaseClaim(findings, cvr)
	require.Len(t, out, 1, "no overlap → retained")
}

func TestSuppressUnverifiableCodebaseClaim_ShortCVREntryIgnored(t *testing.T) {
	findings := []verdict.Finding{{
		Severity:   verdict.SeverityMinor,
		Category:   verdict.CategoryUnverifiableCodebaseClaim,
		Criterion:  "spec",
		Evidence:   "T.foo at file.kt",
		Suggestion: "verify",
	}}
	cvr := []string{"T", "abc"}
	out := suppressUnverifiableCodebaseClaim(findings, cvr)
	require.Len(t, out, 1, "short CVR entries not used for matching")
}

func TestSuppressUnverifiableCodebaseClaim_EmptyEvidenceAndCriterion_NotSuppressed(t *testing.T) {
	findings := []verdict.Finding{{
		Severity:   verdict.SeverityMinor,
		Category:   verdict.CategoryUnverifiableCodebaseClaim,
		Criterion:  "",
		Evidence:   "",
		Suggestion: "verify",
	}}
	out := suppressUnverifiableCodebaseClaim(findings, []string{"abcd"})
	require.Len(t, out, 1, "empty evidence + criterion not suppressed (avoid strings.Contains empty-string trap)")
}

func TestSuppressUnverifiableCodebaseClaim_NonUnverifiableUntouched(t *testing.T) {
	findings := []verdict.Finding{{
		Severity:   verdict.SeverityMinor,
		Category:   verdict.CategoryAmbiguousSpec,
		Criterion:  "spec",
		Evidence:   "path/to/file.kt is ambiguous",
		Suggestion: "rewrite",
	}}
	out := suppressUnverifiableCodebaseClaim(findings, []string{"path/to/file.kt"})
	require.Len(t, out, 1, "non-unverifiable category not affected")
}

func TestSuppressUnverifiableCodebaseClaim_BothDirections(t *testing.T) {
	findings := []verdict.Finding{{
		Severity:   verdict.SeverityMinor,
		Category:   verdict.CategoryUnverifiableCodebaseClaim,
		Criterion:  "spec",
		Evidence:   "file.kt",
		Suggestion: "verify",
	}}
	cvr := []string{"path/to/file.kt"}
	out := suppressUnverifiableCodebaseClaim(findings, cvr)
	require.Empty(t, out, "evidence-as-substring-of-CVR-entry direction also suppresses")
}

func TestSuppressUnverifiableCodebaseClaim_EmptyInputs_ShortCircuit(t *testing.T) {
	require.Nil(t, suppressUnverifiableCodebaseClaim(nil, []string{"x"}))
	some := []verdict.Finding{{Category: verdict.CategoryUnverifiableCodebaseClaim}}
	out := suppressUnverifiableCodebaseClaim(some, nil)
	require.Equal(t, len(some), len(out), "empty CVR short-circuits to input")
}
