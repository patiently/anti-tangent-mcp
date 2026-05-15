// Package mcpsrv: plan-result normalization and verdict calibration for the
// unverifiable_codebase_claim rollup path. See spec §3 / §4. No I/O.
package mcpsrv

import (
	"fmt"
	"strings"

	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

// rollupEvidencePerTaskMax bounds each per-task entry in the rolled-up
// codebase_reference_checklist evidence. Spec §3 picked 240 separately from
// summary.go's summaryEvidenceMax (120) so the human checklist has enough
// room (~2 compact lines of paths/symbols) without letting one task dominate.
const rollupEvidencePerTaskMax = 240

// splitTaskUnverifiable separates a task's findings into the ones that should
// stay attached (kept) and the evidence strings that should be rolled up to
// plan level (perTaskEvidence). The kept slice is freshly allocated so the
// caller's backing array is not aliased.
func splitTaskUnverifiable(findings []verdict.Finding) (kept []verdict.Finding, perTaskEvidence []string) {
	kept = make([]verdict.Finding, 0, len(findings))
	for _, f := range findings {
		if f.Category != verdict.CategoryUnverifiableCodebaseClaim {
			kept = append(kept, f)
			continue
		}
		perTaskEvidence = append(perTaskEvidence, f.Evidence)
	}
	return kept, perTaskEvidence
}

// normalizePlanUnverifiableFindings collects every task-level finding whose
// category is unverifiable_codebase_claim, removes them from their tasks, and
// appends ONE plan-level codebase_reference_checklist finding whose evidence
// lists the affected tasks with their original evidence text (per-task
// truncated at rollupEvidencePerTaskMax). Reviewer-emitted plan-level
// unverifiable findings are intentionally left in place — see spec §3.
//
// Pointer receiver: the helper mutates the supplied PlanResult in place.
// Each task's Findings is reassigned to a freshly-allocated slice so the
// caller's original backing array is not aliased and silently rewritten.
func normalizePlanUnverifiableFindings(pr *verdict.PlanResult) {
	var lines []string
	for i := range pr.Tasks {
		kept, perTask := splitTaskUnverifiable(pr.Tasks[i].Findings)
		pr.Tasks[i].Findings = kept
		if len(perTask) == 0 {
			continue
		}
		// Spec §3: "one compact line per affected task." Multiple
		// unverifiable findings under the same task join with "; " so the
		// human checklist shows one task once, not duplicated.
		//
		// Chunked-path defense: validateChunkIdentity checks titles/order,
		// not task_index, so a chunk-local or zero index can survive.
		// Fall back to the merged-task position when the reviewer-provided
		// index is missing or invalid.
		taskNum := pr.Tasks[i].TaskIndex
		if taskNum <= 0 {
			taskNum = i + 1
		}
		lines = append(lines, fmt.Sprintf("Task %d: %s",
			taskNum,
			truncate(strings.Join(perTask, "; "), rollupEvidencePerTaskMax)))
	}
	if len(lines) == 0 {
		return
	}
	pr.PlanFindings = append(pr.PlanFindings, verdict.Finding{
		Severity:   verdict.SeverityMinor,
		Category:   verdict.CategoryUnverifiableCodebaseClaim,
		Criterion:  "codebase_reference_checklist",
		Evidence:   strings.Join(lines, "\n"),
		Suggestion: "Pre-flight these references with grep or codebase-aware review before dispatch. Do not treat this checklist as a plan-quality defect if the references were already verified.",
	})
}

// calibratePlanVerdictForUnverifiableOnly force-passes a plan whose only
// findings are minor unverifiable_codebase_claim entries (after rollup).
// plan_quality stays at rigorous if the reviewer already emitted that;
// otherwise it lands at actionable. The next_action is rewritten to make
// the "checklist, not blocker" framing explicit. See spec §4.
//
// Pointer receiver: mutates in place (matches normalize counterpart).
func calibratePlanVerdictForUnverifiableOnly(pr *verdict.PlanResult) {
	if !allPlanFindingsAreMinorUnverifiable(*pr) {
		return
	}
	pr.PlanVerdict = verdict.VerdictPass
	if pr.PlanQuality != verdict.PlanQualityRigorous {
		pr.PlanQuality = verdict.PlanQualityActionable
	}
	pr.NextAction = "No blocking plan-quality findings remain; pre-flight the rolled-up codebase references before dispatch."
}

// isMinorUnverifiable reports whether f is a minor-severity
// unverifiable_codebase_claim finding — the only shape that the
// unverifiable-only calibration is willing to force-pass.
func isMinorUnverifiable(f verdict.Finding) bool {
	return f.Severity == verdict.SeverityMinor &&
		f.Category == verdict.CategoryUnverifiableCodebaseClaim
}

// allPlanFindingsAreMinorUnverifiable returns true iff every finding across
// pr.PlanFindings and pr.Tasks[].Findings has severity=minor AND
// category=unverifiable_codebase_claim, AND at least one such finding exists.
// (Empty input returns false — calibration only fires when there is something
// to calibrate.)
func allPlanFindingsAreMinorUnverifiable(pr verdict.PlanResult) bool {
	found := false
	for _, f := range pr.PlanFindings {
		if !isMinorUnverifiable(f) {
			return false
		}
		found = true
	}
	for _, task := range pr.Tasks {
		for _, f := range task.Findings {
			if !isMinorUnverifiable(f) {
				return false
			}
			found = true
		}
	}
	return found
}
