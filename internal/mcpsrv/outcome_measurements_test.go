package mcpsrv

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/scorecard"
)

func TestRecordReviewOutcome_StoresMeasurements(t *testing.T) {
	h, _, dir := outcomeHandlers(t)
	run := h.deps.PlanRuns.Create("pass", "rigorous", 2)
	res := recordOutcome(t, h, RecordReviewOutcomeArgs{
		PlanRunID: run.ID, Source: "final_review", Findings: []OutcomeFindingArg{},
		Measurements: []OutcomeMeasurementArg{
			{TaskIndex: 2, Metric: "  ZIP_Missing_Rate ", Before: 0.5, After: 0.8, N: 10},
			{TaskIndex: 0, Metric: "suite_regressions", Before: 0, After: 0},
		},
	})
	waitForScorecard(t, dir)
	require.True(t, res.Recorded, res.Reason)
	assert.Contains(t, res.SummaryBlock, "measurements: 2 stored, not scored")

	raw, err := os.ReadFile(filepath.Join(dir, "outcomes.jsonl"))
	require.NoError(t, err)
	var line scorecard.OutcomeLine
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(string(raw))), &line))
	require.Equal(t, []scorecard.Measurement{
		{TaskIndex: 2, Metric: "zip_missing_rate", Before: 0.5, After: 0.8, N: 10},
		{TaskIndex: 0, Metric: "suite_regressions", Before: 0, After: 0},
	}, line.Measurements)
}

func TestRecordReviewOutcome_MeasurementsAreNotScored(t *testing.T) {
	// Each outcome is recorded in its own stats dir and waited for there:
	// recording starts an asynchronous scorecard refresh, and a second one
	// in the same dir could still be writing when the dir is removed.
	score := func(ms []OutcomeMeasurementArg) RecordReviewOutcomeResult {
		h, _, dir := outcomeHandlers(t)
		run := h.deps.PlanRuns.Create("pass", "rigorous", 1)
		res := recordOutcome(t, h, RecordReviewOutcomeArgs{PlanRunID: run.ID, Source: "final_review", Findings: []OutcomeFindingArg{}, Measurements: ms})
		waitForScorecard(t, dir)
		require.True(t, res.Recorded, res.Reason)
		return res
	}
	without := score(nil)
	with := score([]OutcomeMeasurementArg{{TaskIndex: 1, Metric: "rate", Before: 0.1, After: 0.9}})
	require.Equal(t, without.TasksScored, with.TasksScored)
	require.Equal(t, without.Escapes, with.Escapes)
}

func TestRecordReviewOutcome_MeasurementValidation(t *testing.T) {
	h, _, dir := outcomeHandlers(t)
	run := h.deps.PlanRuns.Create("pass", "rigorous", 2)
	cases := map[string][]OutcomeMeasurementArg{
		"measurements[0].task_index must be 0":            {{TaskIndex: -1, Metric: "m"}},
		"measurements[0].task_index 3 exceeds":            {{TaskIndex: 3, Metric: "m"}},
		"measurements[0].metric is required":              {{TaskIndex: 1, Metric: "  "}},
		"measurements[0].n must not be negative":          {{TaskIndex: 1, Metric: "m", N: -1}},
		"measurements[0].metric must not contain control": {{TaskIndex: 1, Metric: "a\x1b[2Jb"}},
		"measurements has 201 entries; at most 200 are":   make([]OutcomeMeasurementArg, maxOutcomeMeasurements+1),
	}
	for want, ms := range cases {
		res := recordOutcome(t, h, RecordReviewOutcomeArgs{PlanRunID: run.ID, Source: "final_review", Findings: []OutcomeFindingArg{}, Measurements: ms})
		assert.False(t, res.Recorded, want)
		assert.Contains(t, res.Reason, want)
	}
	_, err := os.Stat(filepath.Join(dir, "outcomes.jsonl"))
	assert.True(t, os.IsNotExist(err), "a rejected outcome must write nothing")
}
