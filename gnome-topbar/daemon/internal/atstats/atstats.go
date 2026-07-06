// Package atstats reads the anti-tangent v0.10.0 stats output (rollup.json +
// summary.md, with an optional codescene object) if present. Pure local file
// reads; absence is reported as Present=false with no error.
package atstats

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Stats struct {
	Present           bool            `json:"present"`
	GeneratedAt       time.Time       `json:"generated_at"`
	TotalCalls        int             `json:"total_calls"`
	PassPct           float64         `json:"pass_pct"`
	WarnPct           float64         `json:"warn_pct"`
	FailPct           float64         `json:"fail_pct"`
	TopCategory       string          `json:"top_category"`
	ReviewMSP95       int64           `json:"review_ms_p95"`
	Summary           string          `json:"summary"`
	PerTool           map[string]int  `json:"per_tool,omitempty"`
	VerdictCounts     map[string]int  `json:"verdict_counts,omitempty"`
	FindingsPerCall   float64         `json:"findings_per_call"`
	SeverityHistogram map[string]int  `json:"severity_histogram,omitempty"`
	CategoryHistogram map[string]int  `json:"category_histogram,omitempty"`
	ReviewMSP50       int64           `json:"review_ms_p50"`
	CacheHitRate      float64         `json:"cache_hit_rate"`
	PartialRate       float64         `json:"partial_rate"`
	ModelUsage        map[string]int  `json:"model_usage,omitempty"`
	WindowStart       time.Time       `json:"window_start"`
	WindowEnd         time.Time       `json:"window_end"`
	CodeScene         *CodeSceneStats `json:"codescene,omitempty"`
}

// CodeSceneStats mirrors the optional top-level "codescene" object inside
// anti-tangent's rollup.json. Contract: 2026-06-02-anti-tangent-stats-design.md.
type CodeSceneStats struct {
	Runs              int            `json:"runs"`
	LatestScore       float64        `json:"latest_score"`
	LatestDelta       float64        `json:"latest_delta"`
	LatestTrend       string         `json:"latest_trend"`
	ScoreP50          float64        `json:"score_p50"`
	Regressions       int            `json:"regressions"`
	Improvements      int            `json:"improvements"`
	Neutral           int            `json:"neutral"`
	CategoryHistogram map[string]int `json:"category_histogram"`
}

type rollup struct {
	TotalCalls        int             `json:"total_calls"`
	PerTool           map[string]int  `json:"per_tool"`
	VerdictCounts     map[string]int  `json:"verdict_counts"`
	FindingsPerCall   float64         `json:"findings_per_call"`
	SeverityHistogram map[string]int  `json:"severity_histogram"`
	CategoryHistogram map[string]int  `json:"category_histogram"`
	ReviewMSP50       int64           `json:"review_ms_p50"`
	ReviewMSP95       int64           `json:"review_ms_p95"`
	CacheHitRate      float64         `json:"cache_hit_rate"`
	PartialRate       float64         `json:"partial_rate"`
	ModelUsage        map[string]int  `json:"model_usage"`
	WindowStart       time.Time       `json:"window_start"`
	WindowEnd         time.Time       `json:"window_end"`
	GeneratedAt       time.Time       `json:"generated_at"`
	CodeScene         *CodeSceneStats `json:"codescene"`
}

// Read returns Present=false (no error) when dir is empty or rollup.json is
// absent/unreadable/unparseable.
func Read(dir string) Stats {
	if dir == "" {
		return Stats{}
	}
	b, err := os.ReadFile(filepath.Join(dir, "rollup.json"))
	if err != nil {
		return Stats{}
	}
	var r rollup
	if err := json.Unmarshal(b, &r); err != nil {
		return Stats{}
	}
	s := Stats{
		Present: true, GeneratedAt: r.GeneratedAt, TotalCalls: r.TotalCalls,
		ReviewMSP95: r.ReviewMSP95, TopCategory: topKey(r.CategoryHistogram), CodeScene: r.CodeScene,
	}
	if r.TotalCalls > 0 {
		s.PassPct = pct(r.VerdictCounts["pass"], r.TotalCalls)
		s.WarnPct = pct(r.VerdictCounts["warn"], r.TotalCalls)
		s.FailPct = pct(r.VerdictCounts["fail"], r.TotalCalls)
	}
	if sb, err := os.ReadFile(filepath.Join(dir, "summary.md")); err == nil {
		s.Summary = truncate(string(sb), summaryMaxChars)
	}
	s.PerTool = r.PerTool
	s.VerdictCounts = r.VerdictCounts
	s.FindingsPerCall = r.FindingsPerCall
	s.SeverityHistogram = r.SeverityHistogram
	s.CategoryHistogram = r.CategoryHistogram
	s.ReviewMSP50 = r.ReviewMSP50
	s.CacheHitRate = r.CacheHitRate
	s.PartialRate = r.PartialRate
	s.ModelUsage = r.ModelUsage
	s.WindowStart = r.WindowStart
	s.WindowEnd = r.WindowEnd
	return s
}

// summaryMaxChars bounds how much of summary.md we keep (a headline excerpt).
const summaryMaxChars = 600

func pct(n, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(n) / float64(total) * 100
}

// topKey returns the highest-count key, breaking ties lexicographically so the
// result is stable across refreshes (Go map iteration order is randomized).
func topKey(m map[string]int) string {
	best, bestN := "", -1
	for k, v := range m {
		if v > bestN || (v == bestN && k < best) {
			best, bestN = k, v
		}
	}
	return best
}

// truncate trims and caps to max runes (not bytes, so it never splits a
// multi-byte rune into invalid UTF-8).
func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > max {
		return string(r[:max])
	}
	return s
}
