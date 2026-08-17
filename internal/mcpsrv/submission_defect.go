// Package mcpsrv: classification of validate_completion findings into
// "the submission was incomplete" versus "the code is wrong".
package mcpsrv

import (
	"fmt"
	"strings"

	"github.com/patiently/anti-tangent-mcp/internal/codescene"
	"github.com/patiently/anti-tangent-mcp/internal/planrun"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

// submissionDefectCategories are the findings that describe what the
// implementer sent rather than what they built. Fixing one means attaching
// more evidence, not changing code.
var submissionDefectCategories = map[verdict.Category]bool{
	verdict.CategoryInsufficientEvidence: true,
	verdict.CategoryMalformedEvidence:    true,
	verdict.CategoryCodesceneNotRun:      true,
}

// resubmitNextAction is prefixed onto next_action when the only blocking
// findings are submission defects.
const resubmitNextAction = "Re-submit with the missing evidence — every blocking finding is " +
	"about the submission, not the code; no rework is implied. Then: "

// isSubmissionDefectOnly reports whether the envelope is blocked solely by
// submission defects. Minor findings are ignored: they never blocked DONE, so
// an envelope carrying only minors has nothing to excuse and returns false.
func isSubmissionDefectOnly(findings []verdict.Finding) bool {
	blocking := 0
	for _, f := range findings {
		if f.Severity != verdict.SeverityCritical && f.Severity != verdict.SeverityMajor {
			continue
		}
		blocking++
		if !submissionDefectCategories[f.Category] {
			return false
		}
	}
	return blocking > 0
}

// codesceneFindings returns the findings implied by an inbound digest.
// mode is cfg.Codescene: "" disables the adoption check entirely, "required"
// makes a missing or undeclared-skipped run observable. The regression finding
// fires regardless of mode, because a supplied digest is signal whether or not
// the operator opted into enforcement.
//
// d is normalized by the caller before this runs.
func codesceneFindings(mode string, d *codescene.Digest) []verdict.Finding {
	var out []verdict.Finding

	if mode == "required" {
		switch {
		case d == nil:
			out = append(out, verdict.Finding{
				Severity:  verdict.SeverityMajor,
				Category:  verdict.CategoryCodesceneNotRun,
				Criterion: "codescene_adoption",
				Evidence: "ANTI_TANGENT_CODESCENE=required, but this validate_completion call " +
					"carried no `codescene` argument.",
				Suggestion: "Run CodeScene `analyze_change_set` and re-submit with the result as " +
					"the `codescene` argument, or pass {\"ran\": false, \"skip_reason\": \"…\"} if " +
					"the skip is deliberate. This is a submission defect — no code rework is implied.",
			})
		case !d.Ran && strings.TrimSpace(d.SkipReason) == "":
			out = append(out, verdict.Finding{
				Severity:  verdict.SeverityMajor,
				Category:  verdict.CategoryCodesceneNotRun,
				Criterion: "codescene_adoption",
				Evidence:  "`codescene.ran` is false and no `skip_reason` was given.",
				Suggestion: "State why CodeScene was skipped in `skip_reason`, or run " +
					"`analyze_change_set` and re-submit the result.",
			})
		case !d.Ran:
			out = append(out, verdict.Finding{
				Severity:   verdict.SeverityMinor,
				Category:   verdict.CategoryCodesceneSkipped,
				Criterion:  "codescene_adoption",
				Evidence:   "CodeScene deliberately skipped: " + strings.TrimSpace(d.SkipReason),
				Suggestion: "No action needed if the reason holds; the skip is recorded in the plan-run ledger.",
			})
		}
	}

	if d != nil && d.Ran && d.Trend == codescene.TrendRegression {
		out = append(out, verdict.Finding{
			Severity:  verdict.SeverityMinor,
			Category:  verdict.CategoryQuality,
			Criterion: "code_health_regression",
			Evidence: fmt.Sprintf("CodeScene reports a Code Health regression: net problem points %+.1f%s.",
				d.NetPP, planrun.TopCategories(d.CategoryCounts, 3)),
			Suggestion: "Consider addressing the flagged functions before reporting DONE. " +
				"Advisory only — anti-tangent never fails a verdict on CodeScene.",
		})
	}
	return out
}
