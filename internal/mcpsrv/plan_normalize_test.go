// Package mcpsrv: characterization tests pinning that the rollup and
// calibration guards in plan_normalize.go never absorb or force-pass a
// contradicted_codebase_claim finding. See spec §5.2.
package mcpsrv

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/planparser"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

func TestStripTaskUnverifiableFindings_LeavesContradictionsAttached(t *testing.T) {
	pr := verdict.PlanResult{Tasks: []verdict.PlanTaskResult{{
		TaskIndex: 1,
		Findings: []verdict.Finding{
			{Severity: verdict.SeverityMinor, Category: verdict.CategoryUnverifiableCodebaseClaim,
				Criterion: "c1", Evidence: "unverifiable-evidence", Suggestion: "s"},
			{Severity: verdict.SeverityMajor, Category: verdict.CategoryContradictedCodebaseClaim,
				Criterion: "c2", Evidence: "contradiction-evidence", Suggestion: "s"},
		},
	}}}
	appendCodebaseReferenceChecklist(&pr, stripTaskUnverifiableFindings(&pr, nil))

	require.Len(t, pr.Tasks[0].Findings, 1)
	assert.Equal(t, verdict.CategoryContradictedCodebaseClaim, pr.Tasks[0].Findings[0].Category,
		"the contradiction stays on its task")

	require.Len(t, pr.PlanFindings, 1)
	assert.Equal(t, "codebase_reference_checklist", pr.PlanFindings[0].Criterion)
	assert.Contains(t, pr.PlanFindings[0].Evidence, "unverifiable-evidence")
	assert.NotContains(t, pr.PlanFindings[0].Evidence, "contradiction-evidence",
		"a hard contradiction must never be rolled into the go-grep-it-yourself checklist")
}

// TestStripTaskUnverifiableFindings_LabelsByParsedPositionNotReviewerIndex
// covers a chunked plan review: validateChunkIdentity checks a chunk's
// titles and order but not task_index, so a chunk-local index survives into
// the merged response. The checklist label must come from the parsed
// position (parsedTaskIndexes), not from the reviewer's task_index directly.
func TestStripTaskUnverifiableFindings_LabelsByParsedPositionNotReviewerIndex(t *testing.T) {
	tasks := []planparser.RawTask{
		{Title: "Task 1: A"},
		{Title: "Task 2: B"},
		{Title: "Task 3: C"},
	}
	pr := verdict.PlanResult{Tasks: []verdict.PlanTaskResult{
		{TaskIndex: 1, TaskTitle: "Task 1: A"},
		{TaskIndex: 2, TaskTitle: "Task 2: B"},
		// Second chunk's first task: a chunk-local task_index of 1, but its
		// title names the third parsed task.
		{TaskIndex: 1, TaskTitle: "Task 3: C", Findings: []verdict.Finding{
			{Severity: verdict.SeverityMinor, Category: verdict.CategoryUnverifiableCodebaseClaim,
				Criterion: "c", Evidence: "e", Suggestion: "s"},
		}},
	}}
	lines := stripTaskUnverifiableFindings(&pr, tasks)
	require.Len(t, lines, 1)
	assert.Equal(t, "Task 3: e", lines[0], "label must come from the parsed position, not the reviewer's chunk-local task_index")
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
	calibratePlanVerdictForUnverifiableOnly(&pr, false)
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
	calibratePlanVerdictForUnverifiableOnly(&pr, false)
	assert.Equal(t, verdict.VerdictPass, pr.PlanVerdict)
}

// A plan whose task-level unverifiable claims were stripped for the checklist
// has no findings left when the calibration runs; the stripped checklist still
// counts as the one unverifiable finding.
func TestCalibratePlanVerdict_CountsAStrippedChecklist(t *testing.T) {
	pr := verdict.PlanResult{PlanVerdict: verdict.VerdictWarn, PlanQuality: verdict.PlanQualityRough}
	calibratePlanVerdictForUnverifiableOnly(&pr, true)
	assert.Equal(t, verdict.PlanQualityActionable, pr.PlanQuality)
	assert.True(t, strings.HasPrefix(pr.NextAction, "Plan passes: dispatch."))

	empty := verdict.PlanResult{PlanVerdict: verdict.VerdictWarn, NextAction: "reviewer text"}
	calibratePlanVerdictForUnverifiableOnly(&empty, false)
	assert.Equal(t, "reviewer text", empty.NextAction, "nothing to calibrate without a stripped or remaining claim")
}
