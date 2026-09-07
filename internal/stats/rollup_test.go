package stats

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		Codescene:         &CodesceneRollup{Runs: 1, CategoryHistogram: map[string]int{}},
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
		"codescene",
	} {
		if !strings.Contains(s, `"`+key+`"`) {
			t.Errorf("missing json key %q in marshaled Rollup", key)
		}
	}
}

func TestComputeRollup_PlanHeaders(t *testing.T) {
	now := time.Now().UTC()
	events := []Event{
		{Ts: now, Tool: "validate_plan", TasksTotal: 10, TasksWithHeader: 8},
		{Ts: now, Tool: "validate_plan", TasksTotal: 10, TasksWithHeader: 10},
		{Ts: now, Tool: "validate_task_spec"},
	}
	r := computeRollup(events, now)
	require.NotNil(t, r.PlanHeaders)
	assert.Equal(t, 20, r.PlanHeaders.TasksTotal)
	assert.Equal(t, 18, r.PlanHeaders.TasksWithHeader)
	assert.InDelta(t, 0.9, r.PlanHeaders.Adoption, 0.0001)
}

func TestComputeRollup_PlanHeaders_AbsentWithoutPlanEvents(t *testing.T) {
	now := time.Now().UTC()
	r := computeRollup([]Event{{Ts: now, Tool: "validate_completion"}}, now)
	assert.Nil(t, r.PlanHeaders, "absence must mean no plan events, not zero adoption")

	b, err := json.Marshal(r)
	require.NoError(t, err)
	assert.NotContains(t, string(b), "plan_headers")
}

func TestComputeRollup_PlanHeaders_ZeroTasksNoDivideByZero(t *testing.T) {
	now := time.Now().UTC()
	r := computeRollup([]Event{{Ts: now, Tool: "validate_plan", TasksTotal: 0}}, now)
	require.NotNil(t, r.PlanHeaders)
	assert.Equal(t, 0.0, r.PlanHeaders.Adoption)
}

func TestRollupWorkerAggregates(t *testing.T) {
	now := time.Now()
	events := []Event{
		{Ts: now, Tool: "bulk_read", InputTokens: 100, OutputTokens: 10},
		{Ts: now, Tool: "bulk_read", InputTokens: 200, OutputTokens: 20},
		{Ts: now, Tool: "code_write", InputTokens: 50, OutputTokens: 300},
		{Ts: now, Tool: "validate_completion", Verdict: "pass"},
	}
	r := computeRollup(events, now)
	if r.Worker == nil {
		t.Fatal("Worker rollup must be present when worker events exist")
	}
	if r.Worker.InputTokens != 350 || r.Worker.OutputTokens != 330 {
		t.Errorf("tokens = %d/%d, want 350/330", r.Worker.InputTokens, r.Worker.OutputTokens)
	}
	if r.Worker.PerTool["bulk_read"] != 2 || r.Worker.PerTool["code_write"] != 1 {
		t.Errorf("PerTool = %v", r.Worker.PerTool)
	}
}

func TestRollupWorkerAbsentWithoutWorkerEvents(t *testing.T) {
	now := time.Now()
	r := computeRollup([]Event{{Ts: now, Tool: "check_progress"}}, now)
	if r.Worker != nil {
		t.Error("Worker must be nil when the window holds no worker events — absence means no data")
	}
}

func TestRollupExistingKeysUnchanged(t *testing.T) {
	now := time.Now()
	b, err := json.Marshal(computeRollup([]Event{{Ts: now, Tool: "bulk_read", InputTokens: 1}}, now))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	// The gnome-topbar consumer reads these by exact name. Adding a key is
	// safe; renaming or dropping one is a breaking change.
	for _, k := range []string{
		"window_start", "window_end", "total_calls", "per_tool", "verdict_counts",
		"findings_per_call", "severity_histogram", "category_histogram",
		"review_ms_p50", "review_ms_p95", "cache_hit_rate", "partial_rate",
		"model_usage", "generated_at",
	} {
		if _, ok := m[k]; !ok {
			t.Errorf("rollup.json lost the load-bearing key %q", k)
		}
	}
}
