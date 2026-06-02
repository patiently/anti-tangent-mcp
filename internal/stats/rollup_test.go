package stats

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestComputeRollup(t *testing.T) {
	base := time.Unix(1700000000, 0).UTC()
	events := []Event{
		{Ts: base, Tool: "validate_task_spec", Verdict: "pass", FindingsTotal: 0, ReviewMS: 100, Model: "anthropic:m"},
		{Ts: base.Add(time.Minute), Tool: "validate_completion", Verdict: "warn", FindingsTotal: 2,
			SeverityCounts: map[string]int{"major": 1, "minor": 1}, CategoryCounts: map[string]int{"scope_drift": 2},
			ReviewMS: 300, Model: "anthropic:m", Partial: true},
		{Ts: base.Add(2 * time.Minute), Tool: "validate_plan", Verdict: "fail", FindingsTotal: 1,
			SeverityCounts: map[string]int{"critical": 1}, CategoryCounts: map[string]int{"missing_acceptance_criterion": 1},
			ReviewMS: 500, Model: "openai:n", Cached: true},
	}
	r := computeRollup(events, base.Add(time.Hour))

	if r.TotalCalls != 3 {
		t.Errorf("TotalCalls = %d, want 3", r.TotalCalls)
	}
	if r.PerTool["validate_plan"] != 1 || r.PerTool["validate_task_spec"] != 1 {
		t.Errorf("PerTool = %v", r.PerTool)
	}
	if r.VerdictCounts["pass"] != 1 || r.VerdictCounts["warn"] != 1 || r.VerdictCounts["fail"] != 1 {
		t.Errorf("VerdictCounts = %v", r.VerdictCounts)
	}
	if r.FindingsPerCall != 1.0 {
		t.Errorf("FindingsPerCall = %v, want 1.0", r.FindingsPerCall)
	}
	if r.SeverityHistogram["major"] != 1 || r.SeverityHistogram["critical"] != 1 {
		t.Errorf("SeverityHistogram = %v", r.SeverityHistogram)
	}
	if r.CategoryHistogram["scope_drift"] != 2 {
		t.Errorf("CategoryHistogram = %v", r.CategoryHistogram)
	}
	if r.CacheHitRate <= 0.33 || r.CacheHitRate >= 0.34 {
		t.Errorf("CacheHitRate = %v, want ~0.333", r.CacheHitRate)
	}
	if r.PartialRate <= 0.33 || r.PartialRate >= 0.34 {
		t.Errorf("PartialRate = %v, want ~0.333", r.PartialRate)
	}
	if r.ReviewMSP50 != 300 || r.ReviewMSP95 != 500 {
		t.Errorf("p50/p95 = %d/%d, want 300/500", r.ReviewMSP50, r.ReviewMSP95)
	}
	if r.ModelUsage["anthropic:m"] != 2 || r.ModelUsage["openai:n"] != 1 {
		t.Errorf("ModelUsage = %v", r.ModelUsage)
	}
	if !r.WindowStart.Equal(base) || !r.WindowEnd.Equal(base.Add(2*time.Minute)) {
		t.Errorf("window = %v..%v", r.WindowStart, r.WindowEnd)
	}
}

func TestComputeRollupEmpty(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	r := computeRollup(nil, now)
	if r.TotalCalls != 0 {
		t.Errorf("TotalCalls = %d, want 0", r.TotalCalls)
	}
	if !r.WindowStart.Equal(now) || !r.WindowEnd.Equal(now) || !r.GeneratedAt.Equal(now) {
		t.Errorf("empty rollup windows = %v..%v gen %v", r.WindowStart, r.WindowEnd, r.GeneratedAt)
	}
}

func TestRollupJSONContract(t *testing.T) {
	r := Rollup{
		TotalCalls:        1,
		PerTool:           map[string]int{"validate_task_spec": 1},
		VerdictCounts:     map[string]int{"pass": 1},
		SeverityHistogram: map[string]int{},
		CategoryHistogram: map[string]int{},
		ModelUsage:        map[string]int{"anthropic:m": 1},
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)
	for _, key := range []string{
		"window_start", "window_end", "total_calls", "per_tool",
		"verdict_counts", "findings_per_call", "severity_histogram",
		"category_histogram", "review_ms_p50", "review_ms_p95",
		"cache_hit_rate", "partial_rate", "model_usage", "generated_at",
	} {
		if !strings.Contains(s, `"`+key+`"`) {
			t.Errorf("missing json key %q in marshaled Rollup", key)
		}
	}
}
