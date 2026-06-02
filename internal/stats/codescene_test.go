package stats

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestComputeCodescene(t *testing.T) {
	base := time.Unix(1700000000, 0).UTC()
	events := []CodesceneEvent{
		{Ts: base, Tool: "analyze_change_set", ScoreBefore: 8.0, ScoreAfter: 8.5, Delta: 0.5, Trend: "improvement",
			FilesAnalyzed: 3, CategoryCounts: map[string]int{"complex-method": 1}},
		{Ts: base.Add(time.Hour), Tool: "analyze_change_set", ScoreBefore: 8.5, ScoreAfter: 8.2, Delta: -0.3, Trend: "regression",
			FilesAnalyzed: 5, CategoryCounts: map[string]int{"complex-method": 2, "bumpy-road": 1}},
	}
	cr := computeCodescene(events)
	if cr == nil {
		t.Fatal("expected non-nil rollup")
	}
	if cr.Runs != 2 {
		t.Errorf("Runs = %d, want 2", cr.Runs)
	}
	if cr.LatestScore != 8.2 || cr.LatestDelta != -0.3 || cr.LatestTrend != "regression" {
		t.Errorf("latest = %v/%v/%v", cr.LatestScore, cr.LatestDelta, cr.LatestTrend)
	}
	if cr.Regressions != 1 || cr.Improvements != 1 || cr.Neutral != 0 {
		t.Errorf("trend counts = %d/%d/%d", cr.Regressions, cr.Improvements, cr.Neutral)
	}
	if cr.CategoryHistogram["complex-method"] != 3 || cr.CategoryHistogram["bumpy-road"] != 1 {
		t.Errorf("category histogram = %v", cr.CategoryHistogram)
	}
	if !cr.WindowStart.Equal(base) || !cr.WindowEnd.Equal(base.Add(time.Hour)) {
		t.Errorf("window = %v..%v", cr.WindowStart, cr.WindowEnd)
	}
}

func TestComputeCodesceneEmptyIsNil(t *testing.T) {
	if cr := computeCodescene(nil); cr != nil {
		t.Errorf("empty input must yield nil (omitted key), got %+v", cr)
	}
}

func TestCodesceneRollupJSONContract(t *testing.T) {
	cr := CodesceneRollup{
		Runs:              1,
		LatestScore:       8.5,
		LatestDelta:       0.3,
		LatestTrend:       "improvement",
		ScoreP50:          8.2,
		Regressions:       0,
		Improvements:      1,
		Neutral:           0,
		CategoryHistogram: map[string]int{"complex-method": 1},
		WindowStart:       time.Unix(1700000000, 0).UTC(),
		WindowEnd:         time.Unix(1700003600, 0).UTC(),
	}
	b, err := json.Marshal(cr)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)
	for _, key := range []string{
		"runs", "latest_score", "latest_delta", "latest_trend", "score_p50",
		"regressions", "improvements", "neutral", "category_histogram",
		"window_start", "window_end",
	} {
		if !strings.Contains(s, `"`+key+`"`) {
			t.Errorf("missing json key %q in marshaled CodesceneRollup", key)
		}
	}
}

func TestPruneCodescene(t *testing.T) {
	dir := t.TempDir()
	base := time.Unix(1700000000, 0).UTC()
	old := CodesceneEvent{Ts: base.Add(-48 * time.Hour), Tool: "analyze_change_set"}
	fresh := CodesceneEvent{Ts: base, Tool: "analyze_change_set"}
	if err := appendJSONL(dir, codesceneFile, old); err != nil {
		t.Fatal(err)
	}
	if err := appendJSONL(dir, codesceneFile, fresh); err != nil {
		t.Fatal(err)
	}
	if err := pruneCodescene(dir, base.Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, _ := readCodescene(dir)
	if len(got) != 1 || !got[0].Ts.Equal(base) {
		t.Fatalf("after prune got %d events, want 1 (the fresh one)", len(got))
	}
}
