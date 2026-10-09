package mcpsrv

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/patiently/anti-tangent-mcp/internal/codescene"
	"github.com/patiently/anti-tangent-mcp/internal/planrun"
	"github.com/patiently/anti-tangent-mcp/internal/ratedigest"
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

// taskSpecPlanRun returns the plan run a validate_task_spec call belongs to:
// the one it names, else the server's single live run when the task's title
// matches one of that run's headings. byTitle reports the second case.
func (h *handlers) taskSpecPlanRun(args ValidateTaskSpecArgs) (runID string, byTitle bool) {
	if args.PlanRunID != "" {
		return args.PlanRunID, false
	}
	return h.deps.PlanRuns.SoleLiveByTitle(args.TaskTitle)
}

// taskSpecPlanRunAdvisory returns the plan-run advisory for one
// validate_task_spec call. A call that named no run is told which run it was
// attached to by title, or else which live run it could have named; a call
// that named one is told when its task_index is outside that run's plan.
// Kept out of ValidateTaskSpec so its branch count stays down.
func (h *handlers) taskSpecPlanRunAdvisory(planRunID string, byTitle bool, taskIndex int) (verdict.Finding, bool) {
	if byTitle {
		return attachedByTitleAdvisory(planRunID), true
	}
	if planRunID == "" {
		if run, ok := h.deps.PlanRuns.Latest(); ok {
			return planRunIDAdvisory(run.ID), true
		}
		return verdict.Finding{}, false
	}
	return h.taskIndexAdvisory(planRunID, taskIndex)
}

// attachedByTitleAdvisory tells a validate_task_spec caller that passed no
// plan_run_id which run its task was attached to. It describes the call's
// arguments, not the task, so it is appended after the verdict is finalized.
func attachedByTitleAdvisory(runID string) verdict.Finding {
	return verdict.Finding{
		Severity:  verdict.SeverityMinor,
		Category:  verdict.CategoryOther,
		Criterion: "plan_run_id",
		Evidence: fmt.Sprintf("This call passed no plan_run_id. Its task_title matches one task of the only live plan run "+
			"on this server, %s, so the task was attached to that run.", runID),
		Suggestion: fmt.Sprintf("Pass plan_run_id=%s on validate_task_spec: a server holding two live runs, or a title "+
			"that matches no plan heading, attaches nothing. Ignore this if the task is not part of that plan.", runID),
	}
}

// withdrawAttachedByTitle rewrites the attached-by-title advisory, env's last
// finding, into the plain plan_run_id advisory once the attach it announced
// has failed: the run was evicted between the title lookup and the attach.
// The finding keeps its place and its id, which the session has already
// recorded as issued.
func withdrawAttachedByTitle(env *Envelope, runID string) {
	if len(env.Findings) == 0 {
		return
	}
	last := &env.Findings[len(env.Findings)-1]
	if last.Criterion != "plan_run_id" {
		return
	}
	plain := planRunIDAdvisory(runID)
	last.Evidence, last.Suggestion = plain.Evidence, plain.Suggestion
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

// appendPlanLedger writes a changed plan-run row to the plan ledger and the
// run snapshot. Best effort: a failure is logged and never changes a result.
func (h *handlers) appendPlanLedger(runID string, row planrun.TaskRow) {
	run, ok := h.deps.PlanRuns.Get(runID)
	if !ok {
		return
	}
	h.snapshotRow(runID, row)
	if err := h.deps.PlanLedger.Append(run, row); err != nil {
		slog.Warn("plan ledger append failed", "plan_run_id", runID, "err", err)
	}
}

// hunkSpan returns how many old and new lines the hunk a header line opens
// covers, and false when line is not a hunk header.
func hunkSpan(line string) (oldLines, newLines int, ok bool) {
	m := hunkHeaderRe.FindStringSubmatch(line)
	if m == nil {
		return 0, 0, false
	}
	oldLines, newLines = 1, 1
	if m[2] != "" {
		oldLines, _ = strconv.Atoi(m[2])
	}
	if m[4] != "" {
		newLines, _ = strconv.Atoi(m[4])
	}
	return oldLines, newLines, true
}

// diffLineCounts returns how many lines a unified diff adds and removes. It
// counts only inside hunks, and a hunk ends when the line counts its header
// declares are used up. That is what tells a file header from a changed line
// that looks like one: a removed line whose text begins with "-- " is counted,
// while the "--- " and "+++ " lines that open the next file are not, with or
// without a "diff --git" line between files.
func diffLineCounts(diff string) (added, removed int) {
	oldLeft, newLeft := 0, 0
	for _, line := range strings.Split(diff, "\n") {
		if o, n, ok := hunkSpan(line); ok {
			oldLeft, newLeft = o, n
			continue
		}
		if oldLeft <= 0 && newLeft <= 0 {
			continue
		}
		switch {
		case strings.HasPrefix(line, "+"):
			added++
			newLeft--
		case strings.HasPrefix(line, "-"):
			removed++
			oldLeft--
		case strings.HasPrefix(line, "\\"):
		default:
			oldLeft--
			newLeft--
		}
	}
	return added, removed
}

// completionRowUpdate is the plan-run row write for one validate_completion
// result. finalDiff is the diff the call submitted, or "" when it sent none.
func completionRowUpdate(env Envelope, cs *codescene.Digest, finalDiff string) func(*planrun.TaskRow) {
	sev, cats, _, _ := stats.CountFindings(env.Findings)
	added, removed := diffLineCounts(finalDiff)
	state := planrun.StateMissing
	if cs != nil {
		if cs.Ran {
			state = planrun.StateRan
		} else {
			state = planrun.StateSkipped
		}
	}
	completedAt := time.Now().UTC()
	call := callFromEnvelope("validate_completion", env)
	return func(row *planrun.TaskRow) {
		row.PostVerdict = env.Verdict
		row.Severity = sev
		for c, n := range cats {
			if row.Categories == nil {
				row.Categories = map[string]int{}
			}
			row.Categories[c] += n
		}
		if finalDiff != "" {
			row.LinesAdded, row.LinesRemoved = added, removed
		}
		row.SubmissionOnly = env.SubmissionDefectOnly
		row.Codescene = cs
		row.CodesceneState = state
		row.CompletedAt = completedAt
		row.Waived = len(env.WaivedFindings)
		row.Escalated = row.Escalated || env.Escalate
		row.AppendCall(call)
	}
}

// countOverBuildingRuled wraps a row update so it also counts a call whose
// over_building finding was settled by an answer or a ruling.
func countOverBuildingRuled(update func(*planrun.TaskRow), ruled bool) func(*planrun.TaskRow) {
	if !ruled {
		return update
	}
	return func(row *planrun.TaskRow) {
		update(row)
		row.OverBuildingRuled++
	}
}

// recordCheckpointRow increments the plan-run row's checkpoint count for a
// session-backed check_progress call and writes the updated row to the plan
// ledger. Best effort: an unknown run or row logs a warning and never
// changes the result. A no-op when sess carries no plan run.
func (h *handlers) recordCheckpointRow(sess *session.Session, env Envelope) {
	if sess.PlanRunID == "" {
		return
	}
	row, ok := h.deps.PlanRuns.UpdateRow(sess.PlanRunID, sess.ID, func(row *planrun.TaskRow) {
		row.Checkpoints++
		row.AppendCall(callFromEnvelope("check_progress", env))
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
func (h *handlers) recordCompletionRow(sess *session.Session, env Envelope, cs *codescene.Digest, finalDiff string, overBuildingRuled bool, rd *ratedigest.Digest) {
	if sess.PlanRunID == "" {
		return
	}
	update := withRateDigest(countOverBuildingRuled(completionRowUpdate(env, cs, finalDiff), overBuildingRuled), rd)
	if row, ok := h.deps.PlanRuns.UpdateRow(sess.PlanRunID, sess.ID, update); ok {
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
func (h *handlers) recordLightweightCompletionRow(args ValidateCompletionArgs, env Envelope, overBuildingRuled bool, rd *ratedigest.Digest) {
	if args.PlanRunID == "" {
		return
	}
	ref := planrun.TaskRef{Index: args.TaskIndex, Title: args.TaskTitle}
	update := withRateDigest(countOverBuildingRuled(completionRowUpdate(env, args.Codescene, args.FinalDiff), overBuildingRuled), rd)
	if row, ok := h.deps.PlanRuns.UpsertLite(args.PlanRunID, ref, update); ok {
		h.appendPlanLedger(args.PlanRunID, row)
	} else {
		slog.Warn("plan run lightweight update skipped; run unknown or expired, or no task named",
			"plan_run_id", args.PlanRunID)
	}
}

// withRateDigest wraps a row update so it also keeps the call's rate digest,
// without the eval's path. A call that sends none leaves the row's last one.
func withRateDigest(update func(*planrun.TaskRow), rd *ratedigest.Digest) func(*planrun.TaskRow) {
	if rd == nil {
		return update
	}
	return func(row *planrun.TaskRow) {
		update(row)
		row.RateDigest = rd.ForRecord()
	}
}
