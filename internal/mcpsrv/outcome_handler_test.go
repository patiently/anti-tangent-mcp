package mcpsrv

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/planrun"
	"github.com/patiently/anti-tangent-mcp/internal/stats"
	"github.com/patiently/anti-tangent-mcp/scorecard"
)

func outcomeHandlers(t *testing.T) (*handlers, *stats.Recorder, string) {
	t.Helper()
	dir := t.TempDir()
	rec, err := stats.New(stats.Options{Dir: dir, SummaryInterval: 24 * time.Hour, SummaryThreshold: 1000, RetentionDays: 30, Logger: slog.Default()})
	require.NoError(t, err)
	h := &handlers{deps: newDeps(t, &fakeReviewer{name: "anthropic", resp: passResp("m")})}
	h.deps.Stats = rec
	return h, rec, dir
}

func recordOutcome(t *testing.T, h *handlers, args RecordReviewOutcomeArgs) RecordReviewOutcomeResult {
	t.Helper()
	_, res, err := h.RecordReviewOutcome(context.Background(), nil, args)
	require.NoError(t, err)
	return res
}

// waitForScorecard blocks briefly for RecordOutcome's async scorecard refresh
// (internal/stats.Recorder.RecordOutcome) to finish writing scorecard.json to
// dir. That refresh runs in a goroutine which outlives the call that
// triggered it, so a caller's t.TempDir() cleanup can otherwise race it:
// RemoveAll can list dir's entries before the goroutine's atomic-rename
// creates its temp file, then fail the directory's own removal with
// "directory not empty" once that file lands. Waiting for the finished file
// rather than deleting the async refresh keeps the scorecard fresh as soon as
// an outcome lands, which is the point of making the refresh async at all.
func waitForScorecard(t *testing.T, dir string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, "scorecard.json")); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Log("scorecard.json did not appear before the wait deadline; continuing (refresh is best-effort)")
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestRecordReviewOutcome_StatsDisabled(t *testing.T) {
	h := &handlers{deps: newDeps(t, &fakeReviewer{name: "anthropic", resp: passResp("m")})}
	res := recordOutcome(t, h, RecordReviewOutcomeArgs{PlanRunID: "pr_x", Source: "final_review"})
	assert.False(t, res.Recorded)
	assert.Contains(t, res.Reason, "ANTI_TANGENT_STATS_DIR")
}

func TestRecordReviewOutcome_Validation(t *testing.T) {
	h, _, dir := outcomeHandlers(t)
	run := h.deps.PlanRuns.Create("pass", "rigorous", 2)
	cases := map[string]RecordReviewOutcomeArgs{
		"plan_run_id":        {Source: "final_review"},
		"source":             {PlanRunID: run.ID, Source: "coderabbit"},
		"severity":           {PlanRunID: run.ID, Source: "final_review", Findings: []OutcomeFindingArg{{TaskIndex: 1, Severity: "nit", Category: "x"}}},
		"task_index":         {PlanRunID: run.ID, Source: "final_review", Findings: []OutcomeFindingArg{{TaskIndex: -1, Severity: "major", Category: "x"}}},
		"exceeds":            {PlanRunID: run.ID, Source: "final_review", Findings: []OutcomeFindingArg{{TaskIndex: 3, Severity: "major", Category: "x"}}},
		"implementer_models": {PlanRunID: run.ID, Source: "final_review", ImplementerModels: []OutcomeImplementerModelArg{{TaskIndex: 0, Model: "m"}}},
	}
	many := make([]OutcomeFindingArg, maxOutcomeFindings+1)
	for i := range many {
		many[i] = OutcomeFindingArg{TaskIndex: 1, Severity: "minor", Category: "x"}
	}
	cases["at most"] = RecordReviewOutcomeArgs{PlanRunID: run.ID, Source: "final_review", Findings: many}
	for want, args := range cases {
		res := recordOutcome(t, h, args)
		assert.False(t, res.Recorded, want)
		assert.Contains(t, res.Reason, want)
	}
	_, err := os.Stat(filepath.Join(dir, "outcomes.jsonl"))
	assert.True(t, os.IsNotExist(err), "a rejected outcome must write nothing")
}

func TestRecordReviewOutcome_KnownRunReportsEscapes(t *testing.T) {
	h, rec, dir := outcomeHandlers(t)
	run := h.deps.PlanRuns.Create("pass", "rigorous", 2)
	for i, post := range []string{"pass", "warn"} {
		sid := "s" + string(rune('1'+i))
		_, ok := h.deps.PlanRuns.Attach(run.ID, sid, planrun.TaskRef{Index: i + 1}, "pass")
		require.True(t, ok)
		row, _ := h.deps.PlanRuns.UpdateRow(run.ID, sid, func(r *planrun.TaskRow) { r.PostVerdict = post })
		h.snapshotRow(run.ID, row)
	}
	res := recordOutcome(t, h, RecordReviewOutcomeArgs{
		PlanRunID: run.ID, Source: "final_review",
		Findings: []OutcomeFindingArg{
			{TaskIndex: 1, Severity: "major", Category: "  Correctness  "},
			{TaskIndex: 2, Severity: "major", Category: "tests"},
		},
	})
	require.True(t, res.Recorded, res.Reason)
	assert.True(t, res.RunKnown)
	assert.Equal(t, 2, res.TasksScored)
	assert.Equal(t, []scorecard.Escape{{TaskIndex: 1, AntiTangentVerdict: "pass", OutcomeSeverity: "major"}}, res.Escapes)
	assert.True(t, strings.HasPrefix(res.SummaryBlock, "record_review_outcome"))
	assert.Contains(t, res.SummaryBlock, "escapes: 1")

	raw, err := os.ReadFile(filepath.Join(dir, "outcomes.jsonl"))
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"category":"correctness"`)
	assert.Contains(t, string(raw), rec.RunHash(run.ID))
	assert.NotContains(t, string(raw), run.ID)
	waitForScorecard(t, dir)
}

func TestRecordReviewOutcome_UnknownRunStillRecorded(t *testing.T) {
	h, _, dir := outcomeHandlers(t)
	res := recordOutcome(t, h, RecordReviewOutcomeArgs{PlanRunID: "pr_000000000000", Source: "review_now",
		Findings: []OutcomeFindingArg{{TaskIndex: 9, Severity: "minor", Category: "docs"}}})
	assert.True(t, res.Recorded)
	assert.False(t, res.RunKnown)
	assert.Equal(t, []scorecard.Escape{}, res.Escapes)
	waitForScorecard(t, dir)
}

func TestRecordReviewOutcome_ZeroTaskKnownRunRejectsTaskIndex(t *testing.T) {
	h, _, _ := outcomeHandlers(t)
	run := h.deps.PlanRuns.Create("pass", "rigorous", 0)
	res := recordOutcome(t, h, RecordReviewOutcomeArgs{PlanRunID: run.ID, Source: "final_review",
		Findings: []OutcomeFindingArg{{TaskIndex: 1, Severity: "major", Category: "x"}}})
	assert.False(t, res.Recorded)
	assert.Contains(t, res.Reason, "exceeds")
}

func TestRecordReviewOutcome_LiveRunWithoutSnapshotsIsKnown(t *testing.T) {
	h, _, dir := outcomeHandlers(t)
	run := h.deps.PlanRuns.Create("pass", "rigorous", 1)
	res := recordOutcome(t, h, RecordReviewOutcomeArgs{PlanRunID: run.ID, Source: "final_review", Findings: []OutcomeFindingArg{}})
	assert.True(t, res.Recorded)
	assert.True(t, res.RunKnown)
	assert.Equal(t, 0, res.TasksScored)
	waitForScorecard(t, dir)
}

func TestRecordReviewOutcomeRegisteredInCatalog(t *testing.T) {
	assert.True(t, catalogHas(t, "record_review_outcome"))
}
