package mcpsrv

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/planrun"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

func titledRun(h *handlers, titles ...string) *planrun.Run {
	tasks := make([]planrun.PlanTask, len(titles))
	for i, title := range titles {
		tasks[i] = planrun.PlanTask{Index: i + 1, Title: title}
	}
	return h.deps.PlanRuns.CreateWithTasks("pass", "actionable", tasks)
}

// alphaBetaRun returns handlers wired with a passing reviewer and a run of
// two titled tasks, the fixture shared by the row-resolution tests below.
func alphaBetaRun(t *testing.T) (*handlers, *planrun.Run) {
	t.Helper()
	h := &handlers{deps: newDeps(t, &fakeReviewer{name: "anthropic", resp: passResp("m")})}
	return h, titledRun(h, "Task 1: Alpha", "Task 2: Beta")
}

func specFor(t *testing.T, h *handlers, runID string, ref planrun.TaskRef) Envelope {
	t.Helper()
	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: ref.Title, Goal: "G", AcceptanceCriteria: []string{"AC"}, PlanRunID: runID, TaskIndex: ref.Index,
	})
	require.NoError(t, err)
	require.NotEmpty(t, env.SessionID)
	return env
}

func completeSession(t *testing.T, h *handlers, sessionID string) Envelope {
	t.Helper()
	_, env, err := h.ValidateCompletion(context.Background(), nil, completionCallArgs(sessionID))
	require.NoError(t, err)
	return env
}

func completeLite(t *testing.T, h *handlers, runID string, ref planrun.TaskRef) Envelope {
	t.Helper()
	args := completionCallArgs("")
	args.PlanRunID, args.TaskIndex, args.TaskTitle = runID, ref.Index, ref.Title
	_, env, err := h.ValidateCompletion(context.Background(), nil, args)
	require.NoError(t, err)
	return env
}

func rowsOf(t *testing.T, h *handlers, runID string) []planrun.TaskRow {
	t.Helper()
	snap, ok := h.deps.PlanRuns.Snapshot(runID)
	require.True(t, ok)
	return snap.Rows
}

func findingsWithCriterion(fs []verdict.Finding, criterion string) []verdict.Finding {
	var out []verdict.Finding
	for _, f := range fs {
		if f.Criterion == criterion {
			out = append(out, f)
		}
	}
	return out
}

func TestValidateTaskSpec_ReValidationUpdatesOneRow(t *testing.T) {
	h, run := alphaBetaRun(t)
	for i := 0; i < 3; i++ {
		specFor(t, h, run.ID, planrun.TaskRef{Title: "Task 1: Alpha"})
	}
	rows := rowsOf(t, h, run.ID)
	require.Len(t, rows, 1)
	assert.Equal(t, 1, rows[0].Index)
	assert.Equal(t, 3, rows[0].Attempts)
}

func TestValidateTaskSpec_TaskIndexNamesTheTask(t *testing.T) {
	h, run := alphaBetaRun(t)
	specFor(t, h, run.ID, planrun.TaskRef{Title: "a title matching nothing", Index: 2})
	rows := rowsOf(t, h, run.ID)
	require.Len(t, rows, 1)
	assert.Equal(t, 2, rows[0].Index)
	assert.False(t, rows[0].Unmatched)
}

func TestValidateTaskSpec_OutOfRangeTaskIndexIsAdvisedNotFatal(t *testing.T) {
	baseline := &handlers{deps: newDeps(t, &fakeReviewer{name: "anthropic", resp: twoMinorsResp()})}
	want := specFor(t, baseline, titledRun(baseline, "Task 1: Alpha", "Task 2: Beta").ID, planrun.TaskRef{Title: "Beta"})

	h := &handlers{deps: newDeps(t, &fakeReviewer{name: "anthropic", resp: twoMinorsResp()})}
	run := titledRun(h, "Task 1: Alpha", "Task 2: Beta")
	env := specFor(t, h, run.ID, planrun.TaskRef{Title: "Beta", Index: 9})

	got := findingsWithCriterion(env.Findings, "task_index")
	require.Len(t, got, 1)
	assert.Equal(t, verdict.SeverityMinor, got[0].Severity)
	assert.NotEmpty(t, got[0].ID)
	assert.Equal(t, want.Verdict, env.Verdict, "the advisory must not change the verdict")
	assert.Equal(t, 2, rowsOf(t, h, run.ID)[0].Index, "the title still finds the task")
}

func TestValidateCompletion_AnEarlierSessionStillUpdatesItsTask(t *testing.T) {
	h := &handlers{deps: newDeps(t, &fakeReviewer{name: "anthropic", resp: passResp("m")})}
	run := titledRun(h, "Task 1: Alpha")
	first := specFor(t, h, run.ID, planrun.TaskRef{Title: "Alpha"})
	specFor(t, h, run.ID, planrun.TaskRef{Title: "Alpha"})
	completeSession(t, h, first.SessionID)

	rows := rowsOf(t, h, run.ID)
	require.Len(t, rows, 1)
	assert.Equal(t, "pass", rows[0].PostVerdict)
}

func TestValidateCompletion_LightweightRecordsItsTask(t *testing.T) {
	h := &handlers{deps: newDeps(t, &fakeReviewer{name: "anthropic", resp: passResp("m")})}
	run := titledRun(h, "Task 1: One", "Task 2: Two", "Task 3: Three")
	env := completeLite(t, h, run.ID, planrun.TaskRef{Index: 3})
	assert.True(t, env.Lightweight)

	rows := rowsOf(t, h, run.ID)
	require.Len(t, rows, 1)
	assert.Equal(t, 3, rows[0].Index)
	assert.True(t, rows[0].Lite)
	assert.Equal(t, "Task 3: Three", rows[0].TaskTitle)
	assert.Equal(t, env.Verdict, rows[0].PostVerdict)
}

func TestValidateCompletion_LightweightWithoutATaskIsAdvised(t *testing.T) {
	h := &handlers{deps: newDeps(t, &fakeReviewer{name: "anthropic", resp: passResp("m")})}
	run := titledRun(h, "Task 1: One")
	env := completeLite(t, h, run.ID, planrun.TaskRef{})

	got := findingsWithCriterion(env.Findings, "plan_run_id")
	require.Len(t, got, 1)
	assert.Equal(t, verdict.SeverityMinor, got[0].Severity)
	assert.Empty(t, rowsOf(t, h, run.ID))
}

func TestValidateCompletion_ASessionCallIgnoresTheTaskFields(t *testing.T) {
	h := &handlers{deps: newDeps(t, &fakeReviewer{name: "anthropic", resp: passResp("m")})}
	run := titledRun(h, "Task 1: One", "Task 2: Two")
	pre := specFor(t, h, run.ID, planrun.TaskRef{Title: "One"})
	args := completionCallArgs(pre.SessionID)
	args.PlanRunID, args.TaskIndex = run.ID, 2
	_, _, err := h.ValidateCompletion(context.Background(), nil, args)
	require.NoError(t, err)

	rows := rowsOf(t, h, run.ID)
	require.Len(t, rows, 1)
	assert.Equal(t, 1, rows[0].Index)
	assert.Equal(t, "pass", rows[0].PostVerdict)
}

func TestPlanRunReport_CountsTasksForARunMixingModes(t *testing.T) {
	h := &handlers{deps: newDeps(t, &fakeReviewer{name: "anthropic", resp: passResp("m")})}
	run := titledRun(h, "Task 1: One", "Task 2: Two", "Task 3: Three", "Task 4: Four",
		"Task 5: Five", "Task 6: Six", "Task 7: Seven")
	var last Envelope
	for i := 0; i < 3; i++ {
		last = specFor(t, h, run.ID, planrun.TaskRef{Title: "Task 1: One"})
	}
	completeSession(t, h, last.SessionID)
	completeSession(t, h, specFor(t, h, run.ID, planrun.TaskRef{Title: "Two"}).SessionID)
	completeSession(t, h, specFor(t, h, run.ID, planrun.TaskRef{Title: "Task 3: Three"}).SessionID)
	for idx := 4; idx <= 6; idx++ {
		completeLite(t, h, run.ID, planrun.TaskRef{Index: idx})
	}

	_, res, err := h.PlanRunReport(context.Background(), nil, PlanRunReportArgs{PlanRunID: run.ID})
	require.NoError(t, err)
	assert.Len(t, res.Tasks, 6)
	assert.Equal(t, 6, res.Totals.Completed)
	assert.Equal(t, 1, res.Totals.NeverDispatched)
	assert.Equal(t, 0, res.Totals.Unmatched)
	assert.Equal(t, 3, res.Tasks[0].Attempts)
	assert.Contains(t, res.SummaryBlock, "never dispatched: 1")
	assert.Contains(t, res.SummaryBlock, "Task 7: Seven")
	assert.Contains(t, res.SummaryBlock, "pass (lite)")
}

func TestPlanLedger_RecoveredReportMatchesTheLiveOne(t *testing.T) {
	ledger := &planrun.Ledger{Dir: t.TempDir()}
	store := planrun.NewStore(time.Hour)
	h := &handlers{deps: planLedgerTestDeps(t, ledger, store)}
	run := store.CreateWithTasks("pass", "actionable", []planrun.PlanTask{{Index: 1, Title: "Task 1: One"}, {Index: 2, Title: "Task 2: Two"}})
	require.NoError(t, ledger.AppendHeader(run))
	completeSession(t, h, specFor(t, h, run.ID, planrun.TaskRef{Title: "One"}).SessionID)
	specFor(t, h, run.ID, planrun.TaskRef{Title: "Two"})

	_, live, err := h.PlanRunReport(context.Background(), nil, PlanRunReportArgs{PlanRunID: run.ID})
	require.NoError(t, err)
	restarted := &handlers{deps: planLedgerTestDeps(t, ledger, planrun.NewStore(time.Hour))}
	_, recovered, err := restarted.PlanRunReport(context.Background(), nil, PlanRunReportArgs{PlanRunID: run.ID})
	require.NoError(t, err)

	assert.Equal(t, live.Totals, recovered.Totals)
	require.Len(t, recovered.Tasks, 2)
	assert.Empty(t, recovered.Tasks[1].PostVerdict, "the open task survives the restart")
	assert.Equal(t, "pass", recovered.Tasks[1].PreVerdict)
}

// TestCheckProgress_ChecksArePersistedToTheLedger pins that a checkpoint
// increment is written to the plan ledger too, not only validate_task_spec's
// attach and validate_completion's row update: every plan-run row change
// must survive a restart.
func TestCheckProgress_ChecksArePersistedToTheLedger(t *testing.T) {
	ledger := &planrun.Ledger{Dir: t.TempDir()}
	store := planrun.NewStore(time.Hour)
	h := &handlers{deps: planLedgerTestDeps(t, ledger, store)}
	run := store.CreateWithTasks("pass", "actionable", []planrun.PlanTask{{Index: 1, Title: "Task 1: One"}})
	env := specFor(t, h, run.ID, planrun.TaskRef{Title: "One"})

	_, _, err := h.CheckProgress(context.Background(), nil, CheckProgressArgs{
		SessionID: env.SessionID, WorkingOn: "implementing",
	})
	require.NoError(t, err)

	restarted := &handlers{deps: planLedgerTestDeps(t, ledger, planrun.NewStore(time.Hour))}
	_, res, err := restarted.PlanRunReport(context.Background(), nil, PlanRunReportArgs{PlanRunID: run.ID})
	require.NoError(t, err)
	require.Len(t, res.Tasks, 1)
	assert.Equal(t, 1, res.Tasks[0].Checkpoints, "the checkpoint increment must survive a restart via the ledger")
}

func TestValidatePlan_RunKnowsItsTaskHeadings(t *testing.T) {
	h := newTestPlanHandlers(t)
	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: buildPlanWithNTasks(2)})
	require.NoError(t, err)
	snap, ok := h.deps.PlanRuns.Snapshot(pr.PlanRunID)
	require.True(t, ok)
	require.Len(t, snap.Tasks, 2)
	assert.Equal(t, 1, snap.Tasks[0].Index)
	assert.Regexp(t, `^Task 1:`, snap.Tasks[0].Title)
	assert.Equal(t, 2, snap.TaskCount)
}
