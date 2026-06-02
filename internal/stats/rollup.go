package stats

import (
	"math"
	"sort"
	"time"
)

// Rollup is the deterministic aggregate written to rollup.json. The json tags
// are a LOAD-BEARING cross-component contract — the gnome-topbar consumer
// (branch feat/gnome-topbar, Task 17) reads these exact snake_case keys. Go
// marshals PascalCase by default, which would silently break that consumer, so
// every field is tagged. Changing/dropping a key is a breaking change.
//
// The Codescene field (added in Task 9) is populated when the agent appends
// CodeScene per-run records to codescene-events.jsonl.
type Rollup struct {
	WindowStart       time.Time        `json:"window_start"`
	WindowEnd         time.Time        `json:"window_end"`
	TotalCalls        int              `json:"total_calls"`
	PerTool           map[string]int   `json:"per_tool"`
	VerdictCounts     map[string]int   `json:"verdict_counts"`
	FindingsPerCall   float64          `json:"findings_per_call"`
	SeverityHistogram map[string]int   `json:"severity_histogram"`
	CategoryHistogram map[string]int   `json:"category_histogram"`
	ReviewMSP50       int64            `json:"review_ms_p50"`
	ReviewMSP95       int64            `json:"review_ms_p95"`
	CacheHitRate      float64          `json:"cache_hit_rate"`
	PartialRate       float64          `json:"partial_rate"`
	ModelUsage        map[string]int   `json:"model_usage"`
	GeneratedAt       time.Time        `json:"generated_at"`
	Codescene         *CodesceneRollup `json:"codescene,omitempty"`
}

// computeRollup aggregates events into a Rollup. now stamps GeneratedAt (and the
// window for an empty event set).
func computeRollup(events []Event, now time.Time) Rollup {
	r := Rollup{
		PerTool:           map[string]int{},
		VerdictCounts:     map[string]int{},
		SeverityHistogram: map[string]int{},
		CategoryHistogram: map[string]int{},
		ModelUsage:        map[string]int{},
		GeneratedAt:       now,
		TotalCalls:        len(events),
	}
	if len(events) == 0 {
		r.WindowStart, r.WindowEnd = now, now
		return r
	}
	var totalFindings, cached, partial int
	latencies := make([]int64, 0, len(events))
	r.WindowStart, r.WindowEnd = events[0].Ts, events[0].Ts
	for _, e := range events {
		if e.Ts.Before(r.WindowStart) {
			r.WindowStart = e.Ts
		}
		if e.Ts.After(r.WindowEnd) {
			r.WindowEnd = e.Ts
		}
		r.PerTool[e.Tool]++
		if e.Verdict != "" {
			r.VerdictCounts[e.Verdict]++
		}
		totalFindings += e.FindingsTotal
		for k, v := range e.SeverityCounts {
			r.SeverityHistogram[k] += v
		}
		for k, v := range e.CategoryCounts {
			r.CategoryHistogram[k] += v
		}
		if e.Model != "" {
			r.ModelUsage[e.Model]++
		}
		if e.Cached {
			cached++
		}
		if e.Partial {
			partial++
		}
		latencies = append(latencies, e.ReviewMS)
	}
	n := float64(len(events))
	r.FindingsPerCall = float64(totalFindings) / n
	r.CacheHitRate = float64(cached) / n
	r.PartialRate = float64(partial) / n
	r.ReviewMSP50 = percentile(latencies, 50)
	r.ReviewMSP95 = percentile(latencies, 95)
	return r
}

// percentile returns the nearest-rank p-th percentile of xs (p in 1..100).
func percentile(xs []int64, p int) int64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]int64(nil), xs...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	rank := int(math.Ceil(float64(p)/100*float64(len(s)))) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(s) {
		rank = len(s) - 1
	}
	return s[rank]
}
