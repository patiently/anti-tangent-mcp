package mcpsrv

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

// TestSummaryBlockContract pins the exact substrings that
// plugin/anti-tangent-guard/hooks/check-task-complete greps for in a
// transcript.
//
// The guard's second pass signal is the summary_block pasted into a
// subagent's DONE report — that is how a controller-side hook can tell
// validate_completion ran inside a subagent transcript it cannot see. It also
// parses the verdict from the same block to block a close that read `fail`.
//
// Both couplings are textual. Without this test a future change to
// formatEnvelopeSummary's wording would silently disarm the guard: the hook
// would find no marker, conclude the gate never ran, and start blocking every
// close — or, worse, stop recognising a fail verdict and let it through. A
// failing test here means: update the hook's grep patterns in the same commit.
func TestSummaryBlockContract(t *testing.T) {
	got := formatEnvelopeSummary(Envelope{
		SessionID:  "sess-123",
		Verdict:    string(verdict.VerdictFail),
		NextAction: "fix",
		ModelUsed:  "anthropic:claude-opus-4-7",
	})
	// Assert label and value separately, not as one string. The hook greps the
	// bare labels (session_id:, verdict:) and the values independently, so it
	// is indifferent to column alignment spacing. Pinning the exact spacing
	// would fail on cosmetic realignment — someone widening a column — while
	// the hook carries on working, disabling this tripwire.
	for _, want := range []string{"anti-tangent envelope", "session_id:"} {
		require.Contains(t, got, want, "summary_block missing required substring.\nUpdate plugin/anti-tangent-guard/hooks/check-task-complete in this commit.")
	}

	// Extra insurance: verify the session ID value is populated (hook doesn't
	// require it, but we assert it for completeness).
	assert.Contains(t, got, "sess-123", "session_id value should be present in output")

	// Mirror the guard hook's ACTUAL parser, not an approximation of it.
	// check-task-complete extracts the verdict with ^\s*verdict:\s*(\w+) in
	// MULTILINE mode. Asserting "verdict:" and "fail" independently would pass
	// even if the value moved to another line — precisely the drift that would
	// stop the guard recognising a failed close while this test stayed green.
	re := regexp.MustCompile(`(?m)^\s*verdict:\s*(\w+)`)
	m := re.FindAllStringSubmatch(got, -1)
	require.NotEmpty(t, m, "no line matches the guard's verdict pattern.\ngot:\n%s", got)
	assert.Equal(t, "fail", m[len(m)-1][1], "guard would parse wrong verdict — update plugin/anti-tangent-guard/hooks/check-task-complete in this commit")
}

// TestSummaryBlockContractLastMatchSemantics verifies that the verdict
// parser uses last-match semantics, critical for reading the final verdict
// from a concatenated transcript (check_progress + validate_completion).
// A DONE report may contain two envelopes with different verdicts; only the
// last one is authoritative. This test confirms the regex would catch a bug
// that reads m[0] (first match) instead of m[len(m)-1] (last match).
func TestSummaryBlockContractLastMatchSemantics(t *testing.T) {
	// Render two envelopes: an early check_progress with verdict warn,
	// then a final validate_completion with verdict fail. Concatenate them
	// as a DONE report would.
	first := formatEnvelopeSummary(Envelope{
		SessionID:  "sess-123",
		Verdict:    string(verdict.VerdictWarn),
		NextAction: "continue",
		ModelUsed:  "anthropic:claude-opus-4-7",
	})
	second := formatEnvelopeSummary(Envelope{
		SessionID:  "sess-123",
		Verdict:    string(verdict.VerdictFail),
		NextAction: "fix",
		ModelUsed:  "anthropic:claude-opus-4-7",
	})
	got := first + "\n\n" + second

	// Extract verdicts using the guard's exact parser.
	re := regexp.MustCompile(`(?m)^\s*verdict:\s*(\w+)`)
	m := re.FindAllStringSubmatch(got, -1)

	// Verify we found exactly two verdict lines (one per envelope).
	require.Len(t, m, 2, "expected two verdict lines in concatenated envelopes")

	// Verify the first match is warn (from check_progress).
	assert.Equal(t, "warn", m[0][1], "first envelope verdict should be warn")

	// Verify the last match is fail (from validate_completion).
	// This is the critical assertion: if the hook reads m[0] instead of
	// m[len(m)-1], it would miss the authoritative failure and let a close
	// through. This test would fail immediately if that bug were introduced.
	assert.Equal(t, "fail", m[len(m)-1][1], "last envelope verdict should be fail — hook reads last-match, not first")
}
