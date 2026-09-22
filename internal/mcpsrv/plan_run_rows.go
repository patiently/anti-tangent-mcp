package mcpsrv

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/patiently/anti-tangent-mcp/internal/codescene"
	"github.com/patiently/anti-tangent-mcp/internal/planrun"
	"github.com/patiently/anti-tangent-mcp/internal/session"
	"github.com/patiently/anti-tangent-mcp/internal/stats"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

// taskIndexAdvisory explains a task_index that names no task of the plan run.
// It reports false when there is nothing to explain: no index, an index in
// range, or a run this server does not know.
func (h *handlers) taskIndexAdvisory(runID string, index int) (verdict.Finding, bool) {
	if index == 0 {
		return verdict.Finding{}, false
	}
	n, ok := h.deps.PlanRuns.PlanTaskCount(runID)
	inRange := index >= 1 && index <= n
	if !ok || inRange {
		return verdict.Finding{}, false
	}
	return verdict.Finding{
		Severity:  verdict.SeverityMinor,
		Category:  verdict.CategoryOther,
		Criterion: "task_index",
		Evidence: fmt.Sprintf("task_index %d names no task of plan run %s, which has %d; the task was looked up by its title instead, and is recorded as unmatched when no heading matches.",
			index, runID, n),
		Suggestion: "Pass the task's 1-based position in the plan, or leave task_index out.",
	}, true
}

// taskSpecPlanRunAdvisory returns the plan-run advisory for one
// validate_task_spec call: it names the latest live run when the call passed
// no plan_run_id, or flags a task_index outside the named run's plan. Kept
// out of ValidateTaskSpec, the most complex function in this package.
func (h *handlers) taskSpecPlanRunAdvisory(planRunID string, taskIndex int) (verdict.Finding, bool) {
	if planRunID == "" {
		if run, ok := h.deps.PlanRuns.Latest(); ok {
			return planRunIDAdvisory(run.ID), true
		}
		return verdict.Finding{}, false
	}
	return h.taskIndexAdvisory(planRunID, taskIndex)
}

// lightweightPlanRunAdvisory returns the plan-run advisory for one
// lightweight validate_completion call: an untargeted-task notice when
// plan_run_id named no task, or a task_index outside the run's plan. Kept
// out of ValidateCompletion, the most complex function in this package.
func (h *handlers) lightweightPlanRunAdvisory(planRunID string, taskIndex int, taskTitle string) (verdict.Finding, bool) {
	if planRunID == "" {
		return verdict.Finding{}, false
	}
	if taskIndex == 0 && strings.TrimSpace(taskTitle) == "" {
		return untargetedLitePlanRunAdvisory(), true
	}
	return h.taskIndexAdvisory(planRunID, taskIndex)
}

// untargetedLitePlanRunAdvisory explains a lightweight validate_completion
// that passed plan_run_id without naming its task, which is therefore not
// recorded in the run.
func untargetedLitePlanRunAdvisory() verdict.Finding {
	return verdict.Finding{
		Severity:   verdict.SeverityMinor,
		Category:   verdict.CategoryOther,
		Criterion:  "plan_run_id",
		Evidence:   "plan_run_id was passed without task_index or task_title, so this lightweight task is not recorded in the plan run.",
		Suggestion: "Pass the task's 1-based task_index, or its task_title, with plan_run_id.",
	}
}

// appendPlanLedger writes a changed plan-run row to the plan ledger. Best
// effort: a failure is logged and never changes a result.
func (h *handlers) appendPlanLedger(runID string, row planrun.TaskRow) {
	run, ok := h.deps.PlanRuns.Get(runID)
	if !ok {
		return
	}
	if err := h.deps.PlanLedger.Append(run, row); err != nil {
		slog.Warn("plan ledger append failed", "plan_run_id", runID, "err", err)
	}
}

// completionRowUpdate is the plan-run row write for one validate_completion
// result.
func completionRowUpdate(env Envelope, cs *codescene.Digest) func(*planrun.TaskRow) {
	sev, _, _, _ := stats.CountFindings(env.Findings)
	state := planrun.StateMissing
	if cs != nil {
		if cs.Ran {
			state = planrun.StateRan
		} else {
			state = planrun.StateSkipped
		}
	}
	completedAt := time.Now().UTC()
	return func(row *planrun.TaskRow) {
		row.PostVerdict = env.Verdict
		row.Severity = sev
		row.SubmissionOnly = env.SubmissionDefectOnly
		row.Codescene = cs
		row.CodesceneState = state
		row.CompletedAt = completedAt
		row.Waived = len(env.WaivedFindings)
		row.Escalated = row.Escalated || env.Escalate
	}
}

// recordCheckpointRow increments the plan-run row's checkpoint count for a
// session-backed check_progress call and writes the updated row to the plan
// ledger. Best effort: an unknown run or row logs a warning and never
// changes the result. A no-op when sess carries no plan run.
func (h *handlers) recordCheckpointRow(sess *session.Session) {
	if sess.PlanRunID == "" {
		return
	}
	row, ok := h.deps.PlanRuns.UpdateRow(sess.PlanRunID, sess.ID, func(row *planrun.TaskRow) {
		row.Checkpoints++
	})
	if !ok {
		slog.Warn("plan run row update failed; run or row unknown",
			"plan_run_id", sess.PlanRunID, "session_id", sess.ID)
		return
	}
	h.appendPlanLedger(sess.PlanRunID, row)
}

// recordCompletionRow updates the plan-run row for a session-backed
// validate_completion call and writes the updated row to the plan ledger.
// Best effort: an unknown run or row logs a warning and never changes the
// result. A no-op when sess carries no plan run.
func (h *handlers) recordCompletionRow(sess *session.Session, env Envelope, cs *codescene.Digest) {
	if sess.PlanRunID == "" {
		return
	}
	if row, ok := h.deps.PlanRuns.UpdateRow(sess.PlanRunID, sess.ID, completionRowUpdate(env, cs)); ok {
		h.appendPlanLedger(sess.PlanRunID, row)
	} else {
		slog.Warn("plan run row update failed; run or row unknown",
			"plan_run_id", sess.PlanRunID, "session_id", sess.ID)
	}
}

// recordLightweightCompletionRow upserts the plan-run row for a lightweight
// validate_completion call and writes the updated row to the plan ledger.
// Best effort: an unknown or expired run, or a call naming no task, logs a
// warning and never changes the result. A no-op when args carries no
// plan_run_id.
func (h *handlers) recordLightweightCompletionRow(args ValidateCompletionArgs, env Envelope) {
	if args.PlanRunID == "" {
		return
	}
	ref := planrun.TaskRef{Index: args.TaskIndex, Title: args.TaskTitle}
	if row, ok := h.deps.PlanRuns.UpsertLite(args.PlanRunID, ref, completionRowUpdate(env, args.Codescene)); ok {
		h.appendPlanLedger(args.PlanRunID, row)
	} else {
		slog.Warn("plan run lightweight update skipped; run unknown or expired, or no task named",
			"plan_run_id", args.PlanRunID)
	}
}
