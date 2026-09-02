// Package mcpsrv: characterization tests pinning that the rollup and
// calibration guards in plan_normalize.go never absorb or force-pass a
// contradicted_codebase_claim finding. See spec §5.2.
package mcpsrv

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

func TestNormalizePlanUnverifiableFindings_LeavesContradictionsAttached(t *testing.T) {
	pr := verdict.PlanResult{Tasks: []verdict.PlanTaskResult{{
		TaskIndex: 1,
		Findings: []verdict.Finding{
			{Severity: verdict.SeverityMinor, Category: verdict.CategoryUnverifiableCodebaseClaim,
				Criterion: "c1", Evidence: "unverifiable-evidence", Suggestion: "s"},
			{Severity: verdict.SeverityMajor, Category: verdict.CategoryContradictedCodebaseClaim,
				Criterion: "c2", Evidence: "contradiction-evidence", Suggestion: "s"},
		},
	}}}
	normalizePlanUnverifiableFindings(&pr)

	require.Len(t, pr.Tasks[0].Findings, 1)
	assert.Equal(t, verdict.CategoryContradictedCodebaseClaim, pr.Tasks[0].Findings[0].Category,
		"the contradiction stays on its task")

	require.Len(t, pr.PlanFindings, 1)
	assert.Equal(t, "codebase_reference_checklist", pr.PlanFindings[0].Criterion)
	assert.Contains(t, pr.PlanFindings[0].Evidence, "unverifiable-evidence")
	assert.NotContains(t, pr.PlanFindings[0].Evidence, "contradiction-evidence",
		"a hard contradiction must never be rolled into the go-grep-it-yourself checklist")
}

func TestCalibratePlanVerdict_DoesNotForcePassWithAContradiction(t *testing.T) {
	pr := verdict.PlanResult{
		PlanVerdict: verdict.VerdictWarn,
		PlanFindings: []verdict.Finding{
			{Severity: verdict.SeverityMinor, Category: verdict.CategoryUnverifiableCodebaseClaim,
				Criterion: "c1", Evidence: "e", Suggestion: "s"},
			{Severity: verdict.SeverityMajor, Category: verdict.CategoryContradictedCodebaseClaim,
				Criterion: "c2", Evidence: "e", Suggestion: "s"},
		},
	}
	calibratePlanVerdictForUnverifiableOnly(&pr)
	assert.Equal(t, verdict.VerdictWarn, pr.PlanVerdict, "must not be force-passed")
}

// Control: without the contradiction, the same shape DOES force-pass. Without
// this, the test above would pass even if calibration were broken outright.
func TestCalibratePlanVerdict_StillForcePassesUnverifiableOnly(t *testing.T) {
	pr := verdict.PlanResult{
		PlanVerdict: verdict.VerdictWarn,
		PlanFindings: []verdict.Finding{
			{Severity: verdict.SeverityMinor, Category: verdict.CategoryUnverifiableCodebaseClaim,
				Criterion: "c1", Evidence: "e", Suggestion: "s"},
		},
	}
	calibratePlanVerdictForUnverifiableOnly(&pr)
	assert.Equal(t, verdict.VerdictPass, pr.PlanVerdict)
}
