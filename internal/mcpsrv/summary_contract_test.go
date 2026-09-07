package mcpsrv

import (
	"regexp"
	"strings"
	"testing"

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
	for _, want := range []string{"anti-tangent envelope", "session_id:", "sess-123"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary_block no longer contains %q — the guard hook greps for it.\n"+
				"Update plugin/anti-tangent-guard/hooks/check-task-complete in this commit.\ngot:\n%s", want, got)
		}
	}

	// Mirror the guard hook's ACTUAL parser, not an approximation of it.
	// check-task-complete extracts the verdict with ^\s*verdict:\s*(\w+) in
	// MULTILINE mode. Asserting "verdict:" and "fail" independently would pass
	// even if the value moved to another line — precisely the drift that would
	// stop the guard recognising a failed close while this test stayed green.
	re := regexp.MustCompile(`(?m)^\s*verdict:\s*(\w+)`)
	m := re.FindAllStringSubmatch(got, -1)
	if len(m) == 0 {
		t.Fatalf("no line matches the guard's verdict pattern.\ngot:\n%s", got)
	}
	if last := m[len(m)-1][1]; last != "fail" {
		t.Errorf("guard would parse verdict %q, want \"fail\"", last)
	}
}
