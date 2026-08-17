package mcpsrv

import (
	"context"
	"os"
	"path/filepath"
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
		FinalFiles: []FileArg{{Path: "x.go", Content: "package x\n"}},
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
