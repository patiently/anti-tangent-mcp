package planrun

import (
	"os"
	"path/filepath"
	"strings"
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

// TestLedger_AppendSetsFileMode0600 pins the permission bits Append leaves on
// a freshly created ledger file. os.Stat.Mode().Perm() is checked directly
// rather than trusting the O_CREATE mode argument, since that argument is
// silently inert once the file already exists (see the *_test.go helpers
// above that pass 0o644 to an OpenFile call with no O_CREATE — that argument
// does nothing there, precisely the trap this test must not fall into).
func TestLedger_AppendSetsFileMode0600(t *testing.T) {
	dir := t.TempDir()
	l := &Ledger{Dir: dir}

	require.NoError(t, l.Append(&Run{ID: "pr_mode1"}, TaskRow{Index: 1, TaskTitle: "first"}))

	info, err := os.Stat(filepath.Join(dir, ledgerFile))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

// TestLedger_AppendTightensPreExistingFileMode covers the upgrade scenario
// the round-2 fix (Append's unconditional f.Chmod(0o600)) closes: an
// installation that already has plan-runs.jsonl on disk at 0o644 from a
// pre-fix binary. The file is created here with os.WriteFile (NOT via
// l.Append), simulating that pre-existing state, before the first Append
// under the fixed code runs against it.
func TestLedger_AppendTightensPreExistingFileMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ledgerFile)
	require.NoError(t, os.WriteFile(path, []byte(`{"plan_run_id":"pr_old","row":{"index":1}}`+"\n"), 0o644))

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o644), info.Mode().Perm(), "precondition: file starts world-readable")

	l := &Ledger{Dir: dir}
	require.NoError(t, l.Append(&Run{ID: "pr_new"}, TaskRow{Index: 1, TaskTitle: "second"}))

	info, err = os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "Append must tighten a pre-existing 0o644 file to 0o600")
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

// TestLedger_PruneDropsOldRows is the brief's Step 2 test, verbatim: two rows
// completed 48h and 1h ago; pruning at a 24h cutoff must keep only the
// recent one.
func TestLedger_PruneDropsOldRows(t *testing.T) {
	dir := t.TempDir()
	l := &Ledger{Dir: dir}
	run := &Run{ID: "pr_x", PlanVerdict: "pass", TaskCount: 2}
	old := time.Now().Add(-48 * time.Hour)
	recent := time.Now().Add(-1 * time.Hour)

	require.NoError(t, l.Append(run, TaskRow{Index: 1, TaskTitle: "old", CompletedAt: old}))
	require.NoError(t, l.Append(run, TaskRow{Index: 2, TaskTitle: "recent", CompletedAt: recent}))

	require.NoError(t, l.Prune(time.Now().Add(-24*time.Hour)))

	got, ok := l.Load("pr_x")
	require.True(t, ok)
	require.Len(t, got.Rows, 1)
	assert.Equal(t, "recent", got.Rows[0].TaskTitle)
}

// TestLedger_PruneWritesFileMode0600 pins the permission bits Prune's atomic
// rewrite (writeFileAtomic) leaves on the replaced ledger file, the second of
// the two write paths (Append is the other, see TestLedger_AppendSetsFileMode0600).
func TestLedger_PruneWritesFileMode0600(t *testing.T) {
	dir := t.TempDir()
	l := &Ledger{Dir: dir}
	recent := time.Now().Add(-1 * time.Hour)

	require.NoError(t, l.Append(&Run{ID: "pr_prunemode"}, TaskRow{Index: 1, TaskTitle: "keep", CompletedAt: recent}))
	require.NoError(t, l.Prune(time.Now().Add(-24*time.Hour)))

	info, err := os.Stat(filepath.Join(dir, ledgerFile))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestLedger_PruneNilAndEmptyDirAreNoops(t *testing.T) {
	var nilLedger *Ledger
	assert.NoError(t, nilLedger.Prune(time.Now()))
	assert.NoError(t, (&Ledger{}).Prune(time.Now()))
}

// TestLedger_PruneMissingFileIsNoop covers the branch TestLedger_PruneNilAndEmptyDirAreNoops
// cannot reach: a *Ledger with a real, non-empty Dir that simply has no
// plan-runs.jsonl yet (e.g. the plan ledger is enabled but no task has
// completed since the server started). Prune must not conjure an empty file
// into existence just because a retention tick fired.
func TestLedger_PruneMissingFileIsNoop(t *testing.T) {
	dir := t.TempDir()
	l := &Ledger{Dir: dir}
	require.NoError(t, l.Prune(time.Now()))
	assert.NoFileExists(t, filepath.Join(dir, ledgerFile))
}

// TestLedger_PruneRetainsZeroCompletedAt pins the deliberate zero-CompletedAt
// decision: a row written without a completion time is retained regardless
// of cutoff, because a naive Before(cutoff) comparison would otherwise treat
// "unknown" as "infinitely old" and silently drop it.
func TestLedger_PruneRetainsZeroCompletedAt(t *testing.T) {
	dir := t.TempDir()
	l := &Ledger{Dir: dir}
	run := &Run{ID: "pr_zero"}

	require.NoError(t, l.Append(run, TaskRow{Index: 1, TaskTitle: "no-timestamp"})) // CompletedAt left zero
	require.NoError(t, l.Append(run, TaskRow{Index: 2, TaskTitle: "old", CompletedAt: time.Now().Add(-48 * time.Hour)}))

	// A cutoff of "now" would drop both rows under a naive comparison; the
	// zero-CompletedAt row must survive anyway.
	require.NoError(t, l.Prune(time.Now()))

	got, ok := l.Load("pr_zero")
	require.True(t, ok)
	require.Len(t, got.Rows, 1)
	assert.Equal(t, "no-timestamp", got.Rows[0].TaskTitle)
}

// TestLedger_PruneDropsTornTrailingLineNotPromoted proves Prune's torn-line
// handling does more than "not abort": the corrupt fragment must not survive
// the rewrite. It reads the raw post-prune file (not through Load, which
// already tolerates torn lines on read and would mask a promotion bug at the
// write layer) and asserts the fragment text is gone and exactly the two
// well-formed rows remain.
func TestLedger_PruneDropsTornTrailingLineNotPromoted(t *testing.T) {
	dir := t.TempDir()
	l := &Ledger{Dir: dir}
	recent := time.Now().Add(-1 * time.Hour)

	require.NoError(t, l.Append(&Run{ID: "pr_torn2"}, TaskRow{Index: 1, TaskTitle: "First", CompletedAt: recent}))
	require.NoError(t, l.Append(&Run{ID: "pr_torn2"}, TaskRow{Index: 2, TaskTitle: "Second", CompletedAt: recent}))

	f, err := os.OpenFile(filepath.Join(dir, ledgerFile), os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString(`{"plan_run_id":"pr_torn2","row":{"index":3,"task_title":"TORN_UNIQUE_MARKER`)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	// Cutoff far in the past: nothing with a real CompletedAt should be dropped
	// for age, isolating this test to the torn-line behavior.
	require.NoError(t, l.Prune(time.Now().Add(-24*time.Hour)))

	raw, err := os.ReadFile(filepath.Join(dir, ledgerFile))
	require.NoError(t, err)
	content := string(raw)
	assert.NotContains(t, content, "TORN_UNIQUE_MARKER", "the torn trailing line must not survive the rewrite")
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	require.Len(t, lines, 2, "only the two well-formed rows should remain after Prune")
}

// TestLedger_PruneConcurrentAppendNotLost proves the mutex added to Append
// and Prune actually serializes them: a concurrent Append must not be able
// to run its critical section while Prune still holds mu, and the Append
// must land (and survive) once Prune releases it.
//
// An earlier version of this test only raced a concurrent l.Append call
// against Prune's *remaining* I/O (temp-file write + rename) and checked
// whether the row survived. That turned out to be a "green run is not
// evidence" test in exactly the sense this branch has been burned by
// before: spawning a goroutine and letting the scheduler sort out the
// timing meant Prune's own (multi-syscall) rewrite consistently outran the
// newly-spawned goroutine before it ever got scheduled, so the test passed
// 50/50 runs even with both l.mu.Lock() calls deleted -- it never actually
// exercised the lost-update window it claimed to.
//
// This version gates on afterAppendLock (a second test-only seam, fired the
// instant Append passes its own mutex gate) instead of on wall-clock speed:
// while Prune is inside afterPruneRead -- i.e. still holding mu -- a
// concurrent Append attempt must NOT be able to reach afterAppendLock within
// a generous timeout. That directly proves mutual exclusion, independent of
// how long Prune's remaining rewrite work happens to take on a given
// machine. Removing the mutex lets Append reach afterAppendLock almost
// immediately (no lock to block on), which the timeout catches
// deterministically -- see the BREAK #4 (superseding the original
// concurrency break) transcript in the task's completion evidence.
func TestLedger_PruneConcurrentAppendNotLost(t *testing.T) {
	dir := t.TempDir()
	l := &Ledger{Dir: dir}
	run := &Run{ID: "pr_race"}
	recent := time.Now().Add(-1 * time.Hour)

	require.NoError(t, l.Append(run, TaskRow{Index: 1, TaskTitle: "before", CompletedAt: recent}))

	appendEntered := make(chan struct{})
	l.afterAppendLock = func() {
		close(appendEntered) // fires the instant a concurrent Append passes the mutex gate
	}

	done := make(chan error, 1)
	dispatched := make(chan struct{})
	l.afterPruneRead = func() {
		go func() {
			close(dispatched)
			done <- l.Append(run, TaskRow{Index: 2, TaskTitle: "concurrent", CompletedAt: recent})
		}()
		<-dispatched // the goroutine has started running

		// Prune is still holding mu here (afterPruneRead runs inside Prune's
		// locked section). A correctly-serialized Append cannot pass its own
		// mutex gate yet, so appendEntered must NOT fire within this window.
		select {
		case <-appendEntered:
			t.Error("concurrent Append entered its critical section while Prune still held the lock -- mutex is not serializing Append against Prune")
		case <-time.After(50 * time.Millisecond):
			// expected: Append is blocked on l.mu.Lock()
		}
	}

	// Cutoff keeps everything; this test isolates the concurrency behavior,
	// not the cutoff comparison.
	require.NoError(t, l.Prune(time.Now().Add(-24*time.Hour)))

	// Prune has released mu; the blocked Append can now proceed.
	<-appendEntered
	require.NoError(t, <-done, "the concurrent Append must complete without error")

	got, ok := l.Load("pr_race")
	require.True(t, ok)
	require.Len(t, got.Rows, 2, "the concurrent Append must not be silently dropped by Prune's rewrite")
	titles := []string{got.Rows[0].TaskTitle, got.Rows[1].TaskTitle}
	assert.ElementsMatch(t, []string{"before", "concurrent"}, titles)
}
