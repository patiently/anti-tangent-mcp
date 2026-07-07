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
		{Ts: base, Tool: "analyze_change_set", QualityGate: "passed", FilesAnalyzed: 3,
			Verdicts: Verdicts{Improved: 2, Stable: 1}, Trend: "improvement", NetPP: -1.5,
			CategoryCounts: map[string]int{"Complex Method": 1}},
		{Ts: base.Add(time.Hour), Tool: "analyze_change_set", QualityGate: "failed", FilesAnalyzed: 5,
			Verdicts: Verdicts{Degraded: 4, Stable: 1}, Trend: "regression", NetPP: 2.3,
			CategoryCounts: map[string]int{"Complex Method": 2, "Bumpy Road Ahead": 1}},
	}
	cr := computeCodescene(events)
	if cr == nil {
		t.Fatal("expected non-nil rollup")
	}
	if cr.Runs != 2 || cr.GatesPassed != 1 || cr.GatesFailed != 1 {
		t.Errorf("runs/gates = %d/%d/%d", cr.Runs, cr.GatesPassed, cr.GatesFailed)
	}
	if cr.LatestGate != "failed" || cr.LatestTrend != "regression" || cr.LatestNetPP != 2.3 {
		t.Errorf("latest = %v/%v/%v", cr.LatestGate, cr.LatestTrend, cr.LatestNetPP)
	}
	if cr.Regressions != 1 || cr.Improvements != 1 || cr.Neutral != 0 {
		t.Errorf("trend counts = %d/%d/%d", cr.Regressions, cr.Improvements, cr.Neutral)
	}
	if cr.FilesAnalyzed != 8 {
		t.Errorf("FilesAnalyzed = %d, want 8", cr.FilesAnalyzed)
	}
	if cr.NetPPP50 != -1.5 { // {-1.5,2.3}→{-150,230}; percentile(50) ceil-rank picks lower → -1.5
		t.Errorf("NetPPP50 = %v, want -1.5", cr.NetPPP50)
	}
	if cr.CategoryHistogram["Complex Method"] != 3 || cr.CategoryHistogram["Bumpy Road Ahead"] != 1 {
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
	cr := CodesceneRollup{CategoryHistogram: map[string]int{"Complex Method": 1}}
	b, err := json.Marshal(cr)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)
	for _, key := range []string{
		"runs", "gates_passed", "gates_failed", "latest_gate", "latest_trend",
		"latest_net_pp", "net_pp_p50", "regressions", "improvements", "neutral",
		"files_analyzed", "category_histogram", "window_start", "window_end",
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
