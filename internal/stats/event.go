// Package stats records compact, counts-only statistics about anti-tangent hook
// calls and periodically asks a reviewer LLM for a prose performance summary.
// Everything is opt-in via ANTI_TANGENT_STATS_DIR and best-effort: a stats
// failure never affects a hook's result or latency.
//
// Import direction: stats imports internal/verdict and internal/providers only.
// internal/mcpsrv imports stats, never the reverse, so there is no import cycle.
package stats

import (
	"time"

	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

// Event is one counts-only record appended per hook call. It deliberately holds
// NO finding text, plan/spec content, or raw session id (SessionHash is a salted
// digest, never the raw id).
type Event struct {
	Ts             time.Time      `json:"ts"`
	Tool           string         `json:"tool"`
	Verdict        string         `json:"verdict,omitempty"`
	FindingsTotal  int            `json:"findings_total"`
	SeverityCounts map[string]int `json:"severity_counts,omitempty"`
	CategoryCounts map[string]int `json:"category_counts,omitempty"`
	ReviewMS       int64          `json:"review_ms"`
	Model          string         `json:"model,omitempty"`
	Cached         bool           `json:"cached,omitempty"`
	Partial        bool           `json:"partial,omitempty"`
	PayloadBytes   int            `json:"payload_bytes,omitempty"`
	SessionHash    string         `json:"session_hash,omitempty"`
}

// CountFindings builds severity and category histograms (and the total) from a
// finding slice. Returns nil maps when there are no findings so empty Events
// serialize without empty objects.
func CountFindings(findings []verdict.Finding) (severity, category map[string]int, total int) {
	if len(findings) == 0 {
		return nil, nil, 0
	}
	severity = make(map[string]int)
	category = make(map[string]int)
	for _, f := range findings {
		severity[string(f.Severity)]++
		category[string(f.Category)]++
	}
	return severity, category, len(findings)
}
