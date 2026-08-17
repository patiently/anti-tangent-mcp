// Package mcpsrv: classification of validate_completion findings into
// "the submission was incomplete" versus "the code is wrong".
package mcpsrv

import "github.com/patiently/anti-tangent-mcp/internal/verdict"

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
