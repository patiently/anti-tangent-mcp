package planrun

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/codescene"
)

func sampleRun() *Run {
	return &Run{
		ID: "pr_8f21c4a90b3e", CreatedAt: time.Unix(0, 0).UTC(),
		PlanVerdict: "pass", PlanQuality: "rigorous", TaskCount: 3,
		Rows: []TaskRow{
			{Index: 1, TaskTitle: "Add /healthz endpoint", PreVerdict: "pass", PostVerdict: "pass",
				CodesceneState: StateRan,
				Codescene:      &codescene.Digest{Ran: true, QualityGate: "passed", NetPP: -1.5}},
			{Index: 2, TaskTitle: "Retry backoff", PreVerdict: "pass", PostVerdict: "warn",
				CodesceneState: StateSkipped,
				Codescene:      &codescene.Digest{SkipReason: "docs-only task"}},
			{Index: 3, TaskTitle: "Config plumbing", PreVerdict: "warn"},
		},
	}
}

func TestRender_Deterministic(t *testing.T) {
	r := sampleRun()
	assert.Equal(t, Render(r), Render(r))
}

func TestRender_Contents(t *testing.T) {
	got := Render(sampleRun())
	assert.Contains(t, got, "pr_8f21c4a90b3e")
	assert.Contains(t, got, "pass / rigorous")
	assert.Contains(t, got, "Add /healthz endpoint")
	assert.Contains(t, got, "skipped (docs-only task)")
	assert.Contains(t, got, "not run")
	assert.Contains(t, got, "incomplete")
}

// TestRender_DeterministicWithCategoryCounts exists because sampleRun's rows
// carry no CategoryCounts (the one row that does have a Codescene digest
// leaves CategoryCounts nil), so TestRender_Deterministic above never
// actually exercises the map-iteration path in topCategoriesList. Without a
// row that has a real, multi-entry CategoryCounts map, a missing sort could
// slip through unnoticed. Ten same-count entries maximizes the chance an
// unsorted map.Iteration reorders between calls; rendering 50 times makes a
// false pass by accidental ordering implausible (Go's map order is
// randomized per-range, not per-process).
func TestRender_DeterministicWithCategoryCounts(t *testing.T) {
	r := &Run{
		ID: "pr_cats", CreatedAt: time.Unix(0, 0).UTC(),
		PlanVerdict: "pass", PlanQuality: "rigorous", TaskCount: 1,
		Rows: []TaskRow{
			{Index: 1, TaskTitle: "Many categories", PostVerdict: "warn",
				CodesceneState: StateRan,
				Codescene: &codescene.Digest{Ran: true, QualityGate: "failed", NetPP: 4.0,
					CategoryCounts: map[string]int{
						"alpha": 1, "bravo": 1, "charlie": 1, "delta": 1, "echo": 1,
						"foxtrot": 1, "golf": 1, "hotel": 1, "india": 1, "juliet": 1,
					}},
			},
		},
	}
	first := Render(r)
	for i := 0; i < 50; i++ {
		require.Equal(t, first, Render(r), "iteration %d diverged from the first render", i)
	}
}

// TestRender_IncompleteIsNotFail pins the distinction the report exists to
// make: a task with no PostVerdict is a different fact from a task that
// failed. It isolates the row for "Config plumbing" (no PostVerdict) rather
// than scanning the whole report, because the totals line legitimately
// contains the substring "fail" (e.g. "fail 0") even when no row failed.
func TestRender_IncompleteIsNotFail(t *testing.T) {
	got := Render(sampleRun())
	var row3 string
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "Config plumbing") {
			row3 = line
		}
	}
	require.NotEmpty(t, row3, "expected a rendered row for Config plumbing")
	assert.Contains(t, row3, "incomplete")
	assert.NotContains(t, row3, "fail")
}

// TestRender_MultiByteTitleTruncatesOnRuneBoundary pins the fix for a
// byte-based truncation bug: title lengths are compared and sliced in runes,
// not bytes, so a title full of multi-byte UTF-8 characters (each "é" here is
// 2 bytes) that crosses the 40-rune cap truncates cleanly instead of being
// sliced mid-codepoint. A byte-based slice at width-1=39 bytes would land
// inside the 20th "é" (39 is odd; every "é" starts on an even byte offset),
// producing an invalid UTF-8 tail — utf8.ValidString on the byte-sliced
// version fails; on the rune-sliced version it must pass.
func TestRender_MultiByteTitleTruncatesOnRuneBoundary(t *testing.T) {
	title := strings.Repeat("é", 50) // 50 runes, 100 bytes — exceeds the 40-rune cap
	r := &Run{
		ID: "pr_utf8", CreatedAt: time.Unix(0, 0).UTC(),
		PlanVerdict: "pass", PlanQuality: "rigorous", TaskCount: 1,
		Rows: []TaskRow{
			{Index: 1, TaskTitle: title, PostVerdict: "pass"},
		},
	}
	got := Render(r)
	require.True(t, utf8.ValidString(got), "rendered report must be valid UTF-8, got: %q", got)
	assert.Contains(t, got, "…")

	// The truncated title cell must be exactly 39 "é" runes plus the ellipsis
	// — width-1 runes, not width-1 bytes.
	wantCell := strings.Repeat("é", 39) + "…"
	assert.Contains(t, got, wantCell)
}

// TestRender_OverDispatchedRows pins the over-dispatch case: len(Rows) >
// TaskCount is reachable when a task is re-dispatched after its first
// subagent died (validate_task_spec called again with the same
// plan_run_id), producing two rows for one task slot. Render's
// under-dispatch branch (n := TaskCount - len(Rows)) goes negative and is
// silently skipped in that case, so this pins the sibling branch that makes
// the anomaly visible instead of leaving "tasks: 4 of 3 completed" (or
// similar) unexplained.
func TestRender_OverDispatchedRows(t *testing.T) {
	r := &Run{
		ID: "pr_over00000001", CreatedAt: time.Unix(0, 0).UTC(),
		PlanVerdict: "warn", PlanQuality: "rigorous", TaskCount: 1,
		Rows: []TaskRow{
			{Index: 1, TaskTitle: "Flaky task", PostVerdict: "fail"},
			{Index: 2, TaskTitle: "Flaky task (retry)", PostVerdict: "pass"},
		},
	}
	got := Render(r)
	assert.Contains(t, got, "2 rows for 1 tasks — includes re-dispatched or duplicate attempts")
}

func TestTotals(t *testing.T) {
	tot := Totals(sampleRun())
	assert.Equal(t, 1, tot.Pass)
	assert.Equal(t, 1, tot.Warn)
	assert.Equal(t, 0, tot.Fail)
	assert.Equal(t, 1, tot.Incomplete)
	assert.Equal(t, 1, tot.CodesceneRan)
	assert.Equal(t, 1, tot.CodesceneSkipped)
	assert.Equal(t, 1, tot.CodesceneMissing)
	assert.InDelta(t, -1.5, tot.NetPP, 0.0001)
}
