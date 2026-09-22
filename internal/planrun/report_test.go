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
	assert.Contains(t, got, "open (pre: warn)")
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

// TestRender_OpenIsNotFail pins the distinction the report exists to
// make: a task with no PostVerdict is a different fact from a task that
// failed. It isolates the row for "Config plumbing" (no PostVerdict) rather
// than scanning the whole report, because the totals line legitimately
// contains the substring "fail" (e.g. "fail 0") even when no row failed.
func TestRender_OpenIsNotFail(t *testing.T) {
	got := Render(sampleRun())
	var row3 string
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "Config plumbing") {
			row3 = line
		}
	}
	require.NotEmpty(t, row3, "expected a rendered row for Config plumbing")
	assert.Contains(t, row3, "open (pre: warn)")
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

func TestTotals(t *testing.T) {
	tot := Totals(sampleRun())
	assert.Equal(t, 1, tot.Pass)
	assert.Equal(t, 1, tot.Warn)
	assert.Equal(t, 0, tot.Fail)
	assert.Equal(t, 1, tot.Incomplete)
	assert.Equal(t, 1, tot.CodesceneRan)
	assert.Equal(t, 1, tot.CodesceneSkipped)
	assert.Equal(t, 0, tot.CodesceneMissing, "an open task has not had its chance to run CodeScene")
	assert.InDelta(t, -1.5, tot.NetPP, 0.0001)
	assert.Equal(t, 0, tot.NeverDispatched)
	assert.Equal(t, 0, tot.Unmatched)
}

func TestCodesceneCellShowsEvidence(t *testing.T) {
	row := TaskRow{
		CodesceneState: StateSkipped,
		Codescene: &codescene.Digest{
			SkipReason:   "not configured",
			SkipEvidence: "MCP error: tool not found",
		},
	}
	got := codesceneCell(row)
	assert.Contains(t, got, "not configured")
	assert.Contains(t, got, "MCP error: tool not found")
}

// The evidence a cell can carry arrives capped at 2,000 runes, and the cell
// shows the head of it. Short evidence passes through either way, so the cap
// is only observable at its own boundary — and only in runes: a multi-byte
// evidence cut by byte count would come out both shorter than the boundary
// and, on an unlucky offset, invalid UTF-8.
func TestCodesceneCellCapsEvidenceAtTheRuneBoundary(t *testing.T) {
	cell := func(evidence string) string {
		return codesceneCell(TaskRow{
			CodesceneState: StateSkipped,
			Codescene: &codescene.Digest{
				SkipReason:   "not configured",
				SkipEvidence: evidence,
			},
		})
	}

	atCap := strings.Repeat("é", reportCellEvidenceRunes)
	require.Equal(t, reportCellEvidenceRunes, utf8.RuneCountInString(atCap))
	assert.Contains(t, cell(atCap), atCap,
		"evidence exactly at the cap is shown whole")

	overCap := strings.Repeat("é", reportCellEvidenceRunes+1)
	got := cell(overCap)
	assert.NotContains(t, got, overCap, "evidence over the cap must be cut")
	shown := strings.TrimPrefix(strings.TrimSuffix(got, ")"),
		"skipped (not configured: ")
	// The ellipsis is one of the retained runes, not an addition to them.
	assert.Equal(t, reportCellEvidenceRunes, utf8.RuneCountInString(shown))
	assert.True(t, strings.HasSuffix(shown, "…"),
		"a cut cell must end in the ellipsis that marks it as cut")
	assert.True(t, utf8.ValidString(shown), "the cut must fall on a rune boundary")
}

// A CodeScene skip carries the failing tool's own output verbatim, so the
// cell can receive any byte the tool printed. Two of them decide whether the
// row survives as a row.
func TestCodesceneCellStaysOnOneRow(t *testing.T) {
	cell := func(reason, evidence string) string {
		return codesceneCell(TaskRow{
			CodesceneState: StateSkipped,
			Codescene: &codescene.Digest{
				SkipReason:   reason,
				SkipEvidence: evidence,
			},
		})
	}

	for name, got := range map[string]string{
		"lf in evidence":   cell("not configured", "MCP error:\ntool not found"),
		"crlf in evidence": cell("not configured", "MCP error:\r\ntool not found"),
		"cr in evidence":   cell("not configured", "MCP error:\rtool not found"),
		"lf in reason":     cell("not\nconfigured", "MCP error"),
		"lf in both":       cell("not\nconfigured", "MCP error:\ntool not found"),
	} {
		t.Run(name, func(t *testing.T) {
			assert.NotContains(t, got, "\n", "a line break would split the row")
			assert.NotContains(t, got, "\r", "a carriage return would overwrite the row")
			assert.Contains(t, got, "MCP error", "the text itself must survive")
		})
	}

	// A category key is caller-supplied too, and reaches the other branch.
	ran := codesceneCell(TaskRow{
		CodesceneState: StateRan,
		Codescene: &codescene.Digest{
			Ran: true, QualityGate: "failed", NetPP: -2,
			CategoryCounts: map[string]int{"Complex\nMethod": 3},
		},
	})
	assert.NotContains(t, ran, "\n", "a line break in a category key would split the row too")
}

// The fold sentinel EscapeContinuationLines writes is "| ". Cell text that
// carries its own pipe must not be able to read as one this renderer put
// there.
func TestCodesceneCellMarksPipes(t *testing.T) {
	got := codesceneCell(TaskRow{
		CodesceneState: StateSkipped,
		Codescene: &codescene.Digest{
			SkipReason:   "not configured",
			SkipEvidence: "usage: tool | grep x",
		},
	})
	assert.Contains(t, got, `usage: tool \| grep x`)
}

// The two properties of the ordering: the cap counts what is DISPLAYED, so a
// line break spends one rune of it as a space rather than buying a whole
// extra row; and the pipe mark is applied after the cut, so no cell can end
// in the dangling half of one.
func TestCodesceneCellEscapesAroundTheCap(t *testing.T) {
	cell := func(evidence string) string {
		return codesceneCell(TaskRow{
			CodesceneState: StateSkipped,
			Codescene:      &codescene.Digest{SkipReason: "r", SkipEvidence: evidence},
		})
	}
	shown := func(got string) string {
		return strings.TrimPrefix(strings.TrimSuffix(got, ")"), "skipped (r: ")
	}

	overCap := shown(cell(strings.Repeat("a", 100) + "\n" + strings.Repeat("b", 200)))
	assert.Equal(t, reportCellEvidenceRunes, utf8.RuneCountInString(overCap),
		"a line break must cost one rune of the cap, not a row")
	assert.Contains(t, overCap, strings.Repeat("a", 100)+" b")

	// The last rune the cap retains is the pipe: escaped before the cut, its
	// backslash would be the rune the cut discards.
	atEdge := shown(cell(strings.Repeat("a", 198) + "|" + strings.Repeat("x", 10)))
	assert.True(t, strings.HasSuffix(atEdge, `a\|…`),
		"the pipe mark must survive the cut whole, got %q", atEdge[len(atEdge)-8:])
}

// The row count of a whole report is what a reader's eye and any line-oriented
// consumer both depend on.
func TestRenderKeepsOneRowPerTask(t *testing.T) {
	r := sampleRun()
	before := strings.Count(Render(r), "\n")
	r.Rows[1].Codescene.SkipEvidence = "MCP error:\n  at frame 1\n  at frame 2"
	assert.Equal(t, before, strings.Count(Render(r), "\n"),
		"evidence carrying line breaks must not add rows to the report")
}

func TestRender_RulingsColumnAndTotals(t *testing.T) {
	r := &Run{
		ID: "pr_rulings", CreatedAt: time.Unix(0, 0).UTC(),
		PlanVerdict: "pass", PlanQuality: "actionable", TaskCount: 4,
		Rows: []TaskRow{
			{Index: 1, TaskTitle: "both", PostVerdict: "pass", Waived: 2, Escalated: true},
			{Index: 2, TaskTitle: "waived", PostVerdict: "pass", Waived: 1},
			{Index: 3, TaskTitle: "escalated", PostVerdict: "warn", Escalated: true},
			{Index: 4, TaskTitle: "neither", PostVerdict: "pass"},
		},
	}
	got := Render(r)
	assert.Contains(t, got, "Rulings")
	assert.Contains(t, got, "2 waived, escalated")
	assert.Contains(t, got, "1 waived ")
	assert.Contains(t, got, "  rulings: 3 findings waived, 2 tasks escalated\n")

	tot := Totals(r)
	assert.Equal(t, 3, tot.Waived)
	assert.Equal(t, 2, tot.Escalated)
}

func TestRulingsCell(t *testing.T) {
	assert.Equal(t, "2 waived, escalated", rulingsCell(TaskRow{Waived: 2, Escalated: true}))
	assert.Equal(t, "1 waived", rulingsCell(TaskRow{Waived: 1}))
	assert.Equal(t, "escalated", rulingsCell(TaskRow{Escalated: true}))
	assert.Equal(t, "-", rulingsCell(TaskRow{}))
}

func TestTotals_CountsPlanTasksNotRows(t *testing.T) {
	r := &Run{
		ID: "pr_shape0000001", PlanVerdict: "pass", PlanQuality: "rigorous", TaskCount: 7,
		Tasks: []PlanTask{
			{Index: 1, Title: "Task 1: One"}, {Index: 2, Title: "Task 2: Two"}, {Index: 3, Title: "Task 3: Three"},
			{Index: 4, Title: "Task 4: Four"}, {Index: 5, Title: "Task 5: Five"}, {Index: 6, Title: "Task 6: Six"},
			{Index: 7, Title: "Task 7: Seven"},
		},
		Rows: []TaskRow{
			{Index: 1, TaskTitle: "One", PreVerdict: "warn", PostVerdict: "pass", Attempts: 3},
			{Index: 2, TaskTitle: "Two", PreVerdict: "warn", PostVerdict: "pass", Attempts: 1},
			{Index: 3, TaskTitle: "Three", PreVerdict: "warn", PostVerdict: "pass", Attempts: 1},
			{Index: 4, TaskTitle: "Task 4: Four", PostVerdict: "pass", Lite: true},
			{Index: 5, TaskTitle: "Task 5: Five", PostVerdict: "warn", Lite: true},
			{Index: 6, TaskTitle: "Task 6: Six", PostVerdict: "pass", Lite: true},
			{Index: 8, TaskTitle: "Stray", PostVerdict: "fail", Unmatched: true},
		},
	}
	tot := Totals(r)
	assert.Equal(t, 7, tot.Tasks)
	assert.Equal(t, 6, tot.Completed)
	assert.Equal(t, 5, tot.Pass)
	assert.Equal(t, 1, tot.Warn)
	assert.Equal(t, 0, tot.Fail, "an unmatched row is not a plan task")
	assert.Equal(t, 1, tot.NeverDispatched)
	assert.Equal(t, 1, tot.Unmatched)

	got := Render(r)
	assert.Contains(t, got, "tasks: 6 of 7 completed")
	assert.Contains(t, got, "never dispatched: 1\n")
	assert.Contains(t, got, "Task 7: Seven")
	assert.Contains(t, got, "unmatched: 1 row(s) named no plan task by task_index or title")
	assert.Contains(t, got, "pass (lite)")
	assert.NotContains(t, got, "rows for", "the duplicate-row note is gone")
}

func TestRender_NeverDispatchedWithoutHeadingsListsNumbers(t *testing.T) {
	r := &Run{ID: "pr_legacy000001", TaskCount: 3, Rows: []TaskRow{{Index: 1, TaskTitle: "a", PostVerdict: "pass"}}}
	got := Render(r)
	assert.Contains(t, got, "never dispatched: 2\n")
	assert.Contains(t, got, "    2  task 2\n")
	assert.Contains(t, got, "    3  task 3\n")
}

func TestTotals_BranchNetPPCountsEachBaseRefOnce(t *testing.T) {
	at := func(h int) time.Time { return time.Date(2026, 9, 22, h, 0, 0, 0, time.UTC) }
	ran := func(base string, pp float64) *codescene.Digest {
		return &codescene.Digest{Ran: true, QualityGate: "passed", NetPP: pp, BaseRef: base}
	}
	r := &Run{ID: "pr_netpp0000001", TaskCount: 5, Rows: []TaskRow{
		{Index: 1, PostVerdict: "pass", CodesceneState: StateRan, Codescene: ran("origin/main", -2), CompletedAt: at(10)},
		{Index: 2, PostVerdict: "pass", CodesceneState: StateRan, Codescene: ran("origin/main", -2), CompletedAt: at(11)},
		{Index: 3, PostVerdict: "pass", CodesceneState: StateRan, Codescene: ran("origin/main", -3), CompletedAt: at(12)},
		{Index: 4, PostVerdict: "pass", CodesceneState: StateRan, Codescene: ran("", 1), CompletedAt: at(9)},
		{Index: 5, PostVerdict: "pass", CodesceneState: StateRan, Codescene: ran("", 4), CompletedAt: at(8)},
	}}
	tot := Totals(r)
	assert.InDelta(t, -3+1, tot.NetPP, 0.0001, "latest per base ref: -3 for origin/main, +1 for the rows naming none")
	assert.Contains(t, Render(r), "branch net problem points (latest per base ref): -2.0")
}

func TestVerdictCell(t *testing.T) {
	assert.Equal(t, "pass", verdictCell(TaskRow{PostVerdict: "pass"}))
	assert.Equal(t, "warn (lite)", verdictCell(TaskRow{PostVerdict: "warn", Lite: true}))
	assert.Equal(t, "open (pre: fail)", verdictCell(TaskRow{PreVerdict: "fail"}))
	assert.Equal(t, "open", verdictCell(TaskRow{}))
}
