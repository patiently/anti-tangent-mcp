package planrun

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/codescene"
)

func TestLedger_AppendAndLoad(t *testing.T) {
	dir := t.TempDir()
	l := &Ledger{Dir: dir}

	run := &Run{ID: "pr_abc123abc123", PlanVerdict: "pass", PlanQuality: "rigorous", TaskCount: 2}
	require.NoError(t, l.Append(run, TaskRow{
		Index: 1, TaskTitle: "Add endpoint", PostVerdict: "pass",
		CodesceneState: StateRan, CompletedAt: time.Unix(0, 0).UTC(),
		Codescene: &codescene.Digest{Ran: true, QualityGate: "passed", NetPP: -1},
	}))
	require.NoError(t, l.Append(run, TaskRow{
		Index: 2, TaskTitle: "Wire router", PostVerdict: "warn", CodesceneState: StateMissing,
	}))

	got, ok := l.Load("pr_abc123abc123")
	require.True(t, ok)
	assert.Equal(t, "pass", got.PlanVerdict)
	assert.Equal(t, 2, got.TaskCount)
	require.Len(t, got.Rows, 2)
	assert.Equal(t, "Add endpoint", got.Rows[0].TaskTitle)
	assert.Equal(t, "Wire router", got.Rows[1].TaskTitle)

	_, ok = l.Load("pr_000000000000")
	assert.False(t, ok)

	assert.FileExists(t, filepath.Join(dir, "plan-runs.jsonl"))
}

// TestLedger_ToleratesTornTrailingLine pins the global constraint that Load
// must survive a partial write from a killed process: two well-formed lines
// followed by a truncated, non-JSON trailing line (no closing brace, no
// newline — exactly what os.OpenFile|O_APPEND leaves behind if the process
// dies mid-write). Load must still return the two good rows rather than
// failing the whole read.
func TestLedger_ToleratesTornTrailingLine(t *testing.T) {
	dir := t.TempDir()
	l := &Ledger{Dir: dir}

	require.NoError(t, l.Append(&Run{ID: "pr_torn0000001"}, TaskRow{Index: 1, TaskTitle: "First"}))
	require.NoError(t, l.Append(&Run{ID: "pr_torn0000001"}, TaskRow{Index: 2, TaskTitle: "Second"}))

	// Append a torn line by hand: valid JSON prefix, no closing brace/newline.
	f, err := os.OpenFile(filepath.Join(dir, ledgerFile), os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString(`{"plan_run_id":"pr_torn0000001","row":{"index":3,"task_ti`)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	got, ok := l.Load("pr_torn0000001")
	require.True(t, ok, "a torn trailing line must not fail the whole read")
	require.Len(t, got.Rows, 2, "only the two well-formed rows should survive")
	assert.Equal(t, "First", got.Rows[0].TaskTitle)
	assert.Equal(t, "Second", got.Rows[1].TaskTitle)
}

func TestLedger_DisabledIsNoop(t *testing.T) {
	var l *Ledger
	assert.NoError(t, l.Append(&Run{ID: "x"}, TaskRow{}))
	_, ok := l.Load("x")
	assert.False(t, ok)
}

// TestLedger_EmptyDirIsNoop covers the branch TestLedger_DisabledIsNoop
// cannot reach: a non-nil *Ledger whose Dir is empty. This is NOT the shape
// production actually produces today — main.go (main.go:110-113) only ever
// constructs a live *Ledger when both ANTI_TANGENT_STATS_DIR and
// ANTI_TANGENT_PLAN_LEDGER are set, and leaves the pointer nil otherwise, so
// ANTI_TANGENT_PLAN_LEDGER=1 alone never reaches this branch in the current
// wiring. This test is belt-and-suspenders: `l == nil || l.Dir == ""` is a
// genuine two-clause guard, and the second clause deserves its own cover in
// case a future refactor of the gate (e.g. constructing the Ledger earlier,
// before cfg.StatsDir is known) starts producing a live pointer with an
// empty Dir. If the `|| l.Dir == ""` half of Append/Load's guard were ever
// dropped, this test is what would catch a relative "plan-runs.jsonl" being
// written into the process's working directory instead of silently doing
// nothing.
func TestLedger_EmptyDirIsNoop(t *testing.T) {
	l := &Ledger{Dir: ""}
	assert.NoError(t, l.Append(&Run{ID: "x"}, TaskRow{TaskTitle: "t"}))
	_, ok := l.Load("x")
	assert.False(t, ok)
	assert.NoFileExists(t, "plan-runs.jsonl", "an empty Dir must never write relative to the working directory")
}
