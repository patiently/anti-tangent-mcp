package atstats

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadAbsentIsNotPresent(t *testing.T) {
	if Read(filepath.Join(t.TempDir(), "nope")).Present {
		t.Fatal("expected not present")
	}
	if Read("").Present {
		t.Fatal("empty dir must be not present")
	}
}

func TestReadParsesRollupSummaryAndCodescene(t *testing.T) {
	dir := t.TempDir()
	rollup := `{"total_calls":10,"verdict_counts":{"pass":7,"warn":2,"fail":1},
	  "category_histogram":{"ambiguous_spec":5},"review_ms_p95":1800,"generated_at":"2026-06-02T08:00:00Z",
	  "per_tool":{"validate_completion":6},"findings_per_call":3.2,"severity_histogram":{"major":4},
	  "review_ms_p50":900,"cache_hit_rate":0,"partial_rate":0,"model_usage":{"openai:gpt-5.5":10},
	  "window_start":"2026-05-26T08:00:00Z","window_end":"2026-06-02T08:00:00Z",
	  "codescene":{"runs":12,"latest_score":8.4,"latest_delta":-0.3,"latest_trend":"regression",
	  "score_p50":8.6,"regressions":3,"improvements":7,"neutral":2,"category_histogram":{"complex-method":5}}}`
	_ = os.WriteFile(filepath.Join(dir, "rollup.json"), []byte(rollup), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "summary.md"), []byte("All healthy."), 0o600)
	s := Read(dir)
	if !s.Present || s.TotalCalls != 10 || s.ReviewMSP95 != 1800 {
		t.Fatalf("bad: %+v", s)
	}
	if s.PassPct != 70 || s.WarnPct != 20 || s.FailPct != 10 {
		t.Fatalf("pct: %+v", s)
	}
	if s.TopCategory != "ambiguous_spec" || s.Summary != "All healthy." {
		t.Fatalf("cat/summary: %+v", s)
	}
	if s.CodeScene == nil || s.CodeScene.Runs != 12 || s.CodeScene.LatestTrend != "regression" {
		t.Fatalf("codescene: %+v", s.CodeScene)
	}
	if s.PerTool["validate_completion"] != 6 || s.ReviewMSP50 != 900 || s.ModelUsage["openai:gpt-5.5"] != 10 {
		t.Errorf("full rollup decode: got PerTool=%v p50=%d model=%v", s.PerTool, s.ReviewMSP50, s.ModelUsage)
	}
	if s.WindowStart.IsZero() || s.WindowEnd.IsZero() || !s.WindowStart.Before(s.WindowEnd) {
		t.Errorf("window: got start=%v end=%v", s.WindowStart, s.WindowEnd)
	}
}

func TestReadCodesceneAbsentIsNil(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "rollup.json"), []byte(`{"total_calls":1,"verdict_counts":{"pass":1},"generated_at":"2026-06-02T08:00:00Z"}`), 0o600)
	if Read(dir).CodeScene != nil {
		t.Fatal("expected nil CodeScene")
	}
}
