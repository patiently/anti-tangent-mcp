package mcpsrv

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/planrun"
	"github.com/patiently/anti-tangent-mcp/internal/providers"
	"github.com/patiently/anti-tangent-mcp/internal/session"
)

// planLedgerTestDeps builds Deps wired with a real *planrun.Store and the
// given *planrun.Ledger (nil is fine — nil-safe).
func planLedgerTestDeps(t *testing.T, ledger *planrun.Ledger, store *planrun.Store) Deps {
	t.Helper()
	cfg, err := config.Load(func(k string) string {
		if k == "ANTHROPIC_API_KEY" {
			return "k"
		}
		return ""
	})
	require.NoError(t, err)
	return Deps{
		Cfg:        cfg,
		Sessions:   session.NewStore(1 * time.Hour),
		Reviews:    providers.Registry{"anthropic": &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}},
		PlanRuns:   store,
		PlanLedger: ledger,
	}
}

// warnResp is a fixed reviewer response used together with the package's
// existing scriptedReviewer (handlers_helpers_test.go) to drive two
// validate_completion calls for the *same session* to two genuinely
// different verdicts, the way a real submission-defect resubmit does —
// fakeReviewer's single fixed resp can't do that. It carries a single MAJOR
// finding deliberately: the server-derived severity ladder
// (major >= 1 -> warn) is what actually decides the verdict, since the
// reviewer's own "verdict" field is overwritten by FinalizeVerdict, and a
// minor-only finding would calibrate right back down to "pass", defeating
// the point of this fixture.
func warnResp(model string) providers.Response {
	return providers.Response{
		RawJSON: []byte(`{"verdict":"warn","findings":[{"severity":"major","category":"quality",` +
			`"criterion":"clarity","evidence":"needs polish","suggestion":"polish and resubmit"}],` +
			`"next_action":"fix and resubmit"}`),
		Model:       model,
		InputTokens: 3, OutputTokens: 2,
	}
}

// driveOneTaskToCompletion runs validate_task_spec then validate_completion
// for one task under runID, returning the completion Envelope.
func driveOneTaskToCompletion(t *testing.T, h *handlers, runID, title string) Envelope {
	t.Helper()
	_, taskEnv, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:          title,
		Goal:               "goal for " + title,
		AcceptanceCriteria: []string{"AC1"},
		PlanRunID:          runID,
	})
	require.NoError(t, err)
	require.NotEmpty(t, taskEnv.SessionID)

	_, compEnv, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID:  taskEnv.SessionID,
		Summary:    "implemented " + title,
		FinalFiles: []CompletionFileArg{{Path: "x.go", Content: strPtr("package x\n")}},
	})
	require.NoError(t, err)
	return compEnv
}

// TestValidateCompletion_AppendsLedgerLine pins AC "with both set, each
// completed task appends one line to plan-runs.jsonl in the stats dir", and
// the privacy AC: the line carries task_title but never a session id (the
// SessionID field's json:"-" tag must survive marshalling through the
// ledger, not just on TaskRow's direct JSON encoding elsewhere).
func TestValidateCompletion_AppendsLedgerLine(t *testing.T) {
	dir := t.TempDir()
	store := planrun.NewStore(1 * time.Hour)
	ledger := &planrun.Ledger{Dir: dir}
	h := &handlers{deps: planLedgerTestDeps(t, ledger, store)}

	run := store.Create("pass", "rigorous", 1)
	env := driveOneTaskToCompletion(t, h, run.ID, "Add endpoint")
	assert.Equal(t, "pass", env.Verdict)

	ledgerPath := filepath.Join(dir, "plan-runs.jsonl")
	require.FileExists(t, ledgerPath)
	b, err := os.ReadFile(ledgerPath)
	require.NoError(t, err)
	line := string(b)

	assert.Contains(t, line, `"task_title":"Add endpoint"`)
	assert.NotContains(t, line, "session_id", "ledger line must never carry the in-process session-id join key")
	t.Logf("sample plan-runs.jsonl line: %s", line)
}

// TestValidateCompletion_LedgerWriteFailure_DoesNotChangeResult pins the
// best-effort AC: a failing ledger write never changes validate_completion's
// result. The failure is genuinely simulated (a Ledger.Dir that does not
// exist, so the underlying os.OpenFile fails), not merely asserted in a
// comment. The result is compared against a control run with no ledger at
// all to prove the two are indistinguishable to the caller.
func TestValidateCompletion_LedgerWriteFailure_DoesNotChangeResult(t *testing.T) {
	badDir := filepath.Join(t.TempDir(), "does-not-exist")
	store := planrun.NewStore(1 * time.Hour)
	ledger := &planrun.Ledger{Dir: badDir}
	h := &handlers{deps: planLedgerTestDeps(t, ledger, store)}

	run := store.Create("pass", "rigorous", 1)
	env := driveOneTaskToCompletion(t, h, run.ID, "Add endpoint")

	controlStore := planrun.NewStore(1 * time.Hour)
	hControl := &handlers{deps: planLedgerTestDeps(t, nil, controlStore)}
	controlRun := controlStore.Create("pass", "rigorous", 1)
	controlEnv := driveOneTaskToCompletion(t, hControl, controlRun.ID, "Add endpoint")

	assert.Equal(t, controlEnv.Verdict, env.Verdict)
	assert.Equal(t, controlEnv.Findings, env.Findings)
	assert.Equal(t, controlEnv.NextAction, env.NextAction)
	assert.Equal(t, "pass", env.Verdict)

	assert.NoDirExists(t, badDir, "the failed write must not have created the target directory")
}

// TestPlanRunReport_RestartFallsBackToLedger pins the AC "plan_run_report
// reconstructs a run from the ledger when the in-memory run is gone". A
// *fresh* planrun.Store simulates a restart (the old store, with the row
// still in it, is discarded entirely — this is not merely a deleted map
// entry). Only the ledger, which is a separate object built from a directory
// on disk, survives across that boundary, exactly as it would across a real
// process restart.
func TestPlanRunReport_RestartFallsBackToLedger(t *testing.T) {
	dir := t.TempDir()
	ledger := &planrun.Ledger{Dir: dir}
	preRestartStore := planrun.NewStore(1 * time.Hour)
	h := &handlers{deps: planLedgerTestDeps(t, ledger, preRestartStore)}

	run := preRestartStore.Create("pass", "rigorous", 1)
	env := driveOneTaskToCompletion(t, h, run.ID, "Add endpoint")
	require.Equal(t, "pass", env.Verdict)

	// Simulate a restart: a brand new store that has never heard of run.ID,
	// paired with the same ledger (the only thing that persisted).
	freshStore := planrun.NewStore(1 * time.Hour)
	hAfterRestart := &handlers{deps: planLedgerTestDeps(t, ledger, freshStore)}

	_, res, err := hAfterRestart.PlanRunReport(context.Background(), nil, PlanRunReportArgs{PlanRunID: run.ID})
	require.NoError(t, err)
	require.Len(t, res.Tasks, 1)
	assert.Equal(t, "Add endpoint", res.Tasks[0].TaskTitle)
	assert.Equal(t, "pass", res.Tasks[0].PostVerdict)
	assert.Empty(t, res.Findings, "a ledger-recovered run must report normally, not as unknown/expired")
}

// TestValidateCompletion_ResubmitDedupesLedgerRow pins the fix-round-1
// finding: validate_completion may legitimately run more than once for the
// same session (the submission-defect re-submit loop this release
// introduces — isSubmissionDefectOnly / resubmitNextAction). Append stays
// dumb: both calls really do write a line each. The dedup — keep the last
// line per Row.Index — happens in Load, which this test proves by driving
// the resubmit through the real handler and then recovering through a fresh
// store (so the only source of truth left is the ledger file itself).
func TestValidateCompletion_ResubmitDedupesLedgerRow(t *testing.T) {
	dir := t.TempDir()
	store := planrun.NewStore(1 * time.Hour)
	ledger := &planrun.Ledger{Dir: dir}

	// Three calls total: validate_task_spec's own pre-review consumes the
	// first response before either validate_completion call runs.
	rv := &scriptedReviewer{responses: []providers.Response{
		passResp("claude-sonnet-4-6"), // validate_task_spec's pre-review
		warnResp("claude-sonnet-4-6"), // 1st validate_completion: warn
		passResp("claude-sonnet-4-6"), // 2nd validate_completion (resubmit): pass
	}}
	cfg, err := config.Load(func(k string) string {
		if k == "ANTHROPIC_API_KEY" {
			return "k"
		}
		return ""
	})
	require.NoError(t, err)
	deps := Deps{
		Cfg:        cfg,
		Sessions:   session.NewStore(1 * time.Hour),
		Reviews:    providers.Registry{"anthropic": rv},
		PlanRuns:   store,
		PlanLedger: ledger,
	}
	h := &handlers{deps: deps}

	run := store.Create("pass", "rigorous", 1)
	_, taskEnv, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "Add endpoint", Goal: "expose a health endpoint",
		AcceptanceCriteria: []string{"AC1"}, PlanRunID: run.ID,
	})
	require.NoError(t, err)
	require.NotEmpty(t, taskEnv.SessionID)

	completionArgs := ValidateCompletionArgs{
		SessionID:  taskEnv.SessionID,
		Summary:    "attempt",
		FinalFiles: []CompletionFileArg{{Path: "x.go", Content: strPtr("package x\n")}},
	}

	// First call: warn — in the real flow, isSubmissionDefectOnly/
	// resubmitNextAction would send the implementer back to fix and resubmit.
	_, env1, err := h.ValidateCompletion(context.Background(), nil, completionArgs)
	require.NoError(t, err)
	require.Equal(t, "warn", env1.Verdict)

	// Second call, same session_id — the resubmit.
	_, env2, err := h.ValidateCompletion(context.Background(), nil, completionArgs)
	require.NoError(t, err)
	require.Equal(t, "pass", env2.Verdict)

	// Both calls really did write to the ledger — Append is dumb and never
	// suppresses the earlier write. Confirmed directly on the file, not
	// inferred from the recovered report.
	raw, err := os.ReadFile(filepath.Join(dir, "plan-runs.jsonl"))
	require.NoError(t, err)
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	require.Len(t, lines, 2, "Append must write one line per completion call, with no suppression")

	// Simulate a restart: only the ledger survives, so the recovered report
	// can only be right if Load itself deduped.
	fresh := planrun.NewStore(1 * time.Hour)
	hAfterRestart := &handlers{deps: Deps{
		Cfg: cfg, Sessions: session.NewStore(1 * time.Hour),
		Reviews: providers.Registry{"anthropic": rv}, PlanRuns: fresh, PlanLedger: ledger,
	}}

	_, res, err := hAfterRestart.PlanRunReport(context.Background(), nil, PlanRunReportArgs{PlanRunID: run.ID})
	require.NoError(t, err)
	require.Len(t, res.Tasks, 1, "a resubmitted task must dedupe to exactly one recovered row, not one per completion call")
	assert.Equal(t, "pass", res.Tasks[0].PostVerdict, "the recovered row must carry the FINAL post_verdict (the resubmit), not the first")

	// Totals must not double-count the resubmit: Completed/Pass reflect one
	// task, not two, and Completed never exceeds TaskCount.
	assert.Equal(t, 1, res.Totals.Tasks)
	assert.Equal(t, 1, res.Totals.Completed)
	assert.Equal(t, 1, res.Totals.Pass)
	assert.Equal(t, 0, res.Totals.Warn)
	assert.LessOrEqual(t, res.Totals.Completed, res.Totals.Tasks, "a resubmission must never make Completed exceed TaskCount")
}

// TestPlanRunReport_LedgerRecovery_MultiTaskOrderMatchesDispatch pins the
// second half of the fix-round-1 finding: a ledger-recovered run must order
// its rows by Row.Index (dispatch order), not by the order tasks happened to
// finish in. Every ledger-backed test before this one used exactly one task,
// so ordering was untestable; this drives three tasks, completing them in a
// different order than they were dispatched.
func TestPlanRunReport_LedgerRecovery_MultiTaskOrderMatchesDispatch(t *testing.T) {
	dir := t.TempDir()
	store := planrun.NewStore(1 * time.Hour)
	ledger := &planrun.Ledger{Dir: dir}
	h := &handlers{deps: planLedgerTestDeps(t, ledger, store)}

	run := store.Create("pass", "rigorous", 3)

	spec := func(title string) Envelope {
		_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
			TaskTitle: title, Goal: "goal for " + title,
			AcceptanceCriteria: []string{"AC1"}, PlanRunID: run.ID,
		})
		require.NoError(t, err)
		require.NotEmpty(t, env.SessionID)
		return env
	}
	// Dispatched in order A (Index 1), B (Index 2), C (Index 3).
	envA := spec("Task A")
	envB := spec("Task B")
	envC := spec("Task C")

	complete := func(sessID, title string) {
		_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
			SessionID: sessID, Summary: "done " + title,
			FinalFiles: []CompletionFileArg{{Path: "x.go", Content: strPtr("package x\n")}},
		})
		require.NoError(t, err)
		require.Equal(t, "pass", env.Verdict)
	}
	// Completed out of dispatch order: C, then A, then B.
	complete(envC.SessionID, "C")
	complete(envA.SessionID, "A")
	complete(envB.SessionID, "B")

	fresh := planrun.NewStore(1 * time.Hour)
	hAfterRestart := &handlers{deps: planLedgerTestDeps(t, ledger, fresh)}
	_, res, err := hAfterRestart.PlanRunReport(context.Background(), nil, PlanRunReportArgs{PlanRunID: run.ID})
	require.NoError(t, err)
	require.Len(t, res.Tasks, 3)

	gotTitles := []string{res.Tasks[0].TaskTitle, res.Tasks[1].TaskTitle, res.Tasks[2].TaskTitle}
	assert.Equal(t, []string{"Task A", "Task B", "Task C"}, gotTitles,
		"recovered rows must be ordered by Index (dispatch order), not by completion order")
	assert.Equal(t, 1, res.Tasks[0].Index)
	assert.Equal(t, 2, res.Tasks[1].Index)
	assert.Equal(t, 3, res.Tasks[2].Index)
}
