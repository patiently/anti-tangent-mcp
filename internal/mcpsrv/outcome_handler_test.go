package mcpsrv

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/planrun"
	"github.com/patiently/anti-tangent-mcp/internal/session"
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

// TestRecordReviewOutcome_ModelStringValidation pins the bound on
// reviewer_model and implementer_models[].model: the global constraints say
// these are the only free text on the wire beyond category, so both a
// too-long value and one carrying a control character must be rejected
// (recorded:false) rather than written to outcomes.jsonl.
func TestRecordReviewOutcome_ModelStringValidation(t *testing.T) {
	h, _, dir := outcomeHandlers(t)
	run := h.deps.PlanRuns.Create("pass", "rigorous", 2)

	tests := []struct {
		name       string
		args       RecordReviewOutcomeArgs
		wantSubstr string
	}{
		{
			name:       "reviewer_model too long",
			args:       RecordReviewOutcomeArgs{PlanRunID: run.ID, Source: "final_review", ReviewerModel: strings.Repeat("a", scorecard.MaxModelRunes+1)},
			wantSubstr: "reviewer_model must be at most 100 characters",
		},
		{
			name:       "reviewer_model control character",
			args:       RecordReviewOutcomeArgs{PlanRunID: run.ID, Source: "final_review", ReviewerModel: "anthropic:claude\nsonnet-5"},
			wantSubstr: "reviewer_model must be at most 100 characters",
		},
		{
			name: "implementer_models[].model too long",
			args: RecordReviewOutcomeArgs{PlanRunID: run.ID, Source: "final_review",
				ImplementerModels: []OutcomeImplementerModelArg{{TaskIndex: 1, Model: strings.Repeat("b", scorecard.MaxModelRunes+1)}}},
			wantSubstr: "implementer_models[0].model must be at most 100 characters",
		},
		{
			name: "implementer_models[].model control character",
			args: RecordReviewOutcomeArgs{PlanRunID: run.ID, Source: "final_review",
				ImplementerModels: []OutcomeImplementerModelArg{{TaskIndex: 1, Model: "anthropic:claude\tsonnet-5"}}},
			wantSubstr: "implementer_models[0].model must be at most 100 characters",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := recordOutcome(t, h, tc.args)
			assert.False(t, res.Recorded)
			assert.Contains(t, res.Reason, tc.wantSubstr)
		})
	}

	t.Run("exactly 100 runes is accepted", func(t *testing.T) {
		res := recordOutcome(t, h, RecordReviewOutcomeArgs{
			PlanRunID: run.ID, Source: "final_review",
			ReviewerModel:     strings.Repeat("a", scorecard.MaxModelRunes),
			ImplementerModels: []OutcomeImplementerModelArg{{TaskIndex: 1, Model: strings.Repeat("b", scorecard.MaxModelRunes)}},
		})
		assert.True(t, res.Recorded, res.Reason)
		waitForScorecard(t, dir)
	})
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

// outcomeRun mints a run of n tasks, each with the final verdict "pass" and a
// snapshot line, and returns its id.
func outcomeRun(t *testing.T, h *handlers, n int) string {
	t.Helper()
	run := h.deps.PlanRuns.Create("pass", "rigorous", n)
	for i := 0; i < n; i++ {
		sid := "s" + string(rune('1'+i))
		_, ok := h.deps.PlanRuns.Attach(run.ID, sid, planrun.TaskRef{Index: i + 1}, "pass")
		require.True(t, ok)
		row, _ := h.deps.PlanRuns.UpdateRow(run.ID, sid, func(r *planrun.TaskRow) { r.PostVerdict = "pass" })
		h.snapshotRow(run.ID, row)
	}
	return run.ID
}

func TestRecordReviewOutcome_ListsTasksMissingAnImplementerModel(t *testing.T) {
	h, _, dir := outcomeHandlers(t)
	runID := outcomeRun(t, h, 3)
	res := recordOutcome(t, h, RecordReviewOutcomeArgs{
		PlanRunID: runID, Source: "final_review",
		ImplementerModels: []OutcomeImplementerModelArg{{TaskIndex: 2, Model: "anthropic:claude-sonnet-5"}},
		Findings:          []OutcomeFindingArg{},
	})
	require.True(t, res.Recorded, res.Reason)
	assert.Equal(t, []int{1, 3}, res.MissingImplementerModels)
	assert.Contains(t, res.SummaryBlock, "implementer model missing for tasks: 1, 3")
	waitForScorecard(t, dir)
}

func TestRecordReviewOutcome_NoMissingModelsLeavesSummaryQuiet(t *testing.T) {
	h, _, dir := outcomeHandlers(t)
	runID := outcomeRun(t, h, 2)
	res := recordOutcome(t, h, RecordReviewOutcomeArgs{
		PlanRunID: runID, Source: "final_review",
		ImplementerModels: []OutcomeImplementerModelArg{
			{TaskIndex: 1, Model: "anthropic:claude-sonnet-5"},
			{TaskIndex: 2, Model: "anthropic:claude-sonnet-5"},
		},
		Findings: []OutcomeFindingArg{},
	})
	require.True(t, res.Recorded, res.Reason)
	assert.Equal(t, []int{}, res.MissingImplementerModels)
	assert.NotContains(t, res.SummaryBlock, "implementer model missing")
	waitForScorecard(t, dir)
}

func TestRecordReviewOutcome_MissingModelsIsAnArrayWhenNotRecorded(t *testing.T) {
	h := &handlers{deps: newDeps(t, &fakeReviewer{name: "anthropic", resp: passResp("m")})}
	res := recordOutcome(t, h, RecordReviewOutcomeArgs{PlanRunID: "pr_x", Source: "final_review"})
	require.False(t, res.Recorded)
	assert.NotNil(t, res.MissingImplementerModels)
}

func TestRecordReviewOutcome_ReviewNowDoesNotListMissingModels(t *testing.T) {
	h, _, dir := outcomeHandlers(t)
	runID := outcomeRun(t, h, 2)
	res := recordOutcome(t, h, RecordReviewOutcomeArgs{
		PlanRunID: runID, Source: "review_now",
		Findings: []OutcomeFindingArg{},
	})
	require.True(t, res.Recorded, res.Reason)
	assert.Equal(t, []int{}, res.MissingImplementerModels)
	assert.NotContains(t, res.SummaryBlock, "implementer model missing")
	waitForScorecard(t, dir)
}

func TestRecordReviewOutcome_SessionRuns(t *testing.T) {
	finding := func(idx int) []OutcomeFindingArg {
		return []OutcomeFindingArg{{TaskIndex: idx, Severity: "major", Category: "correctness"}}
	}
	cases := []struct {
		name       string
		evict      bool
		args       func(sessionID, planRunID string) RecordReviewOutcomeArgs
		wantReason []string
		wantIndex  int
	}{
		{name: "live session, index 0 stored as 1", wantIndex: 1,
			args: func(s, _ string) RecordReviewOutcomeArgs {
				return RecordReviewOutcomeArgs{SessionID: s, Source: "final_review", Findings: finding(0)}
			}},
		{name: "evicted session is still recorded", evict: true, wantIndex: 1,
			args: func(s, _ string) RecordReviewOutcomeArgs {
				return RecordReviewOutcomeArgs{SessionID: s, Source: "final_review", Findings: finding(0)}
			}},
		{name: "index above 1 rejected", wantReason: []string{"task_index"},
			args: func(s, _ string) RecordReviewOutcomeArgs {
				return RecordReviewOutcomeArgs{SessionID: s, Source: "final_review", Findings: finding(2)}
			}},
		{name: "implementer model index 0 stored as 1", wantIndex: 1,
			args: func(s, _ string) RecordReviewOutcomeArgs {
				return RecordReviewOutcomeArgs{SessionID: s, Source: "final_review", ImplementerModels: []OutcomeImplementerModelArg{{TaskIndex: 0, Model: "m"}}}
			}},
		{name: "implementer model index above 1 rejected", wantReason: []string{"implementer_models"},
			args: func(s, _ string) RecordReviewOutcomeArgs {
				return RecordReviewOutcomeArgs{SessionID: s, Source: "final_review", ImplementerModels: []OutcomeImplementerModelArg{{TaskIndex: 2, Model: "m"}}}
			}},
		{name: "measurement index 0 stored as 1", wantIndex: 1,
			args: func(s, _ string) RecordReviewOutcomeArgs {
				return RecordReviewOutcomeArgs{SessionID: s, Source: "final_review", Measurements: []OutcomeMeasurementArg{{TaskIndex: 0, Metric: "m"}}}
			}},
		{name: "measurement index above 1 rejected", wantReason: []string{"measurements"},
			args: func(s, _ string) RecordReviewOutcomeArgs {
				return RecordReviewOutcomeArgs{SessionID: s, Source: "final_review", Measurements: []OutcomeMeasurementArg{{TaskIndex: 2, Metric: "m"}}}
			}},
		{name: "both ids rejected", wantReason: []string{"plan_run_id", "session_id"},
			args: func(s, p string) RecordReviewOutcomeArgs {
				return RecordReviewOutcomeArgs{SessionID: s, PlanRunID: p, Source: "final_review"}
			}},
		{name: "neither id rejected", wantReason: []string{"plan_run_id", "session_id"},
			args: func(string, string) RecordReviewOutcomeArgs { return RecordReviewOutcomeArgs{Source: "final_review"} }},
		{name: "finishup source accepted", wantIndex: 1,
			args: func(s, _ string) RecordReviewOutcomeArgs {
				return RecordReviewOutcomeArgs{SessionID: s, Source: "finishup", Findings: finding(1)}
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, rec, dir := outcomeHandlers(t)
			sess := h.deps.Sessions.Create(session.TaskSpec{Title: "t", Goal: "g"}, "")
			run := h.deps.PlanRuns.Create("pass", "rigorous", 2)
			if tc.evict {
				h.deps.Sessions.EvictExpired(time.Now().Add(24 * time.Hour))
			}
			res := recordOutcome(t, h, tc.args(sess.ID, run.ID))
			assert.Equal(t, len(tc.wantReason) == 0, res.Recorded, res.Reason)
			for _, w := range tc.wantReason {
				assert.Contains(t, res.Reason, w)
			}
			if !res.Recorded {
				return
			}
			waitForScorecard(t, dir)
			raw, err := os.ReadFile(filepath.Join(dir, "outcomes.jsonl"))
			require.NoError(t, err)
			var last scorecard.OutcomeLine
			rows := strings.Split(strings.TrimSpace(string(raw)), "\n")
			require.NoError(t, json.Unmarshal([]byte(rows[len(rows)-1]), &last))
			assert.Equal(t, rec.SessionRunHash(sess.ID), last.RunHash)
			var got []int
			for _, f := range last.Findings {
				got = append(got, f.TaskIndex)
			}
			for _, m := range last.ImplementerModels {
				got = append(got, m.TaskIndex)
			}
			for _, m := range last.Measurements {
				got = append(got, m.TaskIndex)
			}
			require.NotEmpty(t, got)
			for _, idx := range got {
				assert.Equal(t, tc.wantIndex, idx)
			}
		})
	}
}
