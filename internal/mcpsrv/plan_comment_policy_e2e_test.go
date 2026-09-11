//go:build e2e

package mcpsrv

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/planrun"
	"github.com/patiently/anti-tangent-mcp/internal/providers"
	"github.com/patiently/anti-tangent-mcp/internal/session"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

// runValidatePlanE2E issues a live validate_plan call the same way
// plan_caching_e2e_test.go does — a direct h.ValidatePlan call rather than
// going through the MCP transport — pinned to realAnthropicReviewer so this
// file and its neighbour move together instead of drifting to different
// providers.
func runValidatePlanE2E(t *testing.T, planText string) verdict.PlanResult {
	t.Helper()
	cfg, err := config.Load(os.Getenv)
	require.NoError(t, err)
	if cfg.PlanModel.Provider != "anthropic" {
		t.Skipf("comment-policy e2e pins the anthropic plan model; resolved plan model provider is %q", cfg.PlanModel.Provider)
	}

	d := Deps{
		Cfg:       cfg,
		Sessions:  session.NewStore(1 * time.Hour),
		Reviews:   providers.Registry{"anthropic": realAnthropicReviewer(t)},
		planCache: newPlanPassCache(),
		PlanRuns:  planrun.NewStore(1 * time.Hour),
	}
	h := &handlers{deps: d}

	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: planText})
	require.NoError(t, err)
	return pr
}

// The task heading is assembled rather than written literally. A bare
// "### Task 1:" at the start of a line is a heading to anything scanning the
// document this fixture lives in, that document's own plan parser included.
const planBody = "# P\n\n" + "###" + " Task 1: Add a helper\n\n" +
	"**Goal:** add one function.\n\n" +
	"**Acceptance criteria:**\n- [ ] Helper() exists\n\n" +
	"**Steps:**\n\n- [ ] Write it\n"

// The canonical pointer line from plan_rules.tmpl, byte for byte. The
// template also accepts equivalent wordings, but a fixture asserting the
// canonical form is what pins the form the documentation tells authors to
// paste.
const policyPointer = "## Global Constraints\n\n" +
	"Comments: anti-tangent-protocol implementer.md §4.4\n\n"

// A plan naming the policy pointer must draw no policy finding; the same plan
// without it must draw exactly one, at major. The severity is the load-bearing
// assertion: the obvious category for this finding carries a parser-side floor
// that would reduce it to minor and the gate would never fire.
func TestCommentPolicyFindingE2E(t *testing.T) {
	res := runValidatePlanE2E(t, policyPointer+planBody)
	for _, f := range res.PlanFindings {
		assert.NotEqual(t, "comment_policy_absent", f.Criterion,
			"a plan carrying the pointer must draw no policy finding")
	}

	res = runValidatePlanE2E(t, planBody)
	var got []string
	for _, f := range res.PlanFindings {
		if f.Criterion == "comment_policy_absent" {
			got = append(got, string(f.Severity)+"/"+string(f.Category))
		}
	}
	require.Len(t, got, 1, "expected exactly one policy finding, got %v", got)
	assert.Equal(t, "major/other", got[0],
		"a floored category would arrive as minor and never gate")
}

// The fence half of the rule needs its own coverage: a violating normative
// fence must draw exactly ONE consolidated minor however many fences carry a
// comment, and the three exemptions must stay silent. Consolidation is the
// assertion that matters -- one finding per fence would push a three-fence
// plan to warn on count alone, which is the behaviour the template text was
// written to avoid.
func TestCommentHygieneFenceFindingE2E(t *testing.T) {
	fence := func(body string) string {
		return "```" + "go\n" + body + "\n```" + "\n\n"
	}
	violating := policyPointer + "# P\n\n" + "###" + " Task 1: Add helpers\n\n" +
		"**Goal:** add three helpers.\n\n**Acceptance criteria:**\n- [ ] they exist\n\n" +
		"**Steps:**\n\n" +
		fence("// fixes task-42\nfunc A() {}") +
		fence("// see issue #7\nfunc B() {}") +
		fence("// added in v0.5.0\nfunc C() {}")

	res := runValidatePlanE2E(t, violating)
	var hygiene int
	for _, f := range res.PlanFindings {
		if f.Criterion == "comment_hygiene" {
			hygiene++
			assert.Equal(t, "minor", string(f.Severity))
		}
	}
	assert.Equal(t, 1, hygiene,
		"three violating fences must consolidate into ONE finding, not three")

	// The exemptions: a diff fence REMOVING a bad comment, an expected-output
	// fence, and a test-fixture fence. None is transcribed into the codebase.
	exempt := policyPointer + "# P\n\n" + "###" + " Task 1: Clean up\n\n" +
		"**Goal:** remove a bad comment and add a fixture.\n\n" +
		"**Acceptance criteria:**\n- [ ] it is gone\n\n**Steps:**\n\n" +
		"```" + "diff\n-// fixes task-42\n+// explains the invariant\n```" + "\n\n" +
		"Expected output:\n\n```" + "text\n// fixes task-42\n```" + "\n\n" +
		"Test fixture:\n\n```" + "json\n{\"content\": \"// fixes task-42\"}\n```" + "\n\n"

	res = runValidatePlanE2E(t, exempt)
	for _, f := range res.PlanFindings {
		assert.NotEqual(t, "comment_hygiene", f.Criterion,
			"diff, expected-output and fixture fences are exempt: %s", f.Evidence)
	}
}
