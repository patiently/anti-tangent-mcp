// Package stats records compact, counts-only statistics about anti-tangent hook
// calls and periodically asks a reviewer LLM for a prose performance summary.
// Everything is opt-in via ANTI_TANGENT_STATS_DIR and best-effort: a stats
// failure never affects a hook's result or latency.
//
// Import direction: stats imports internal/verdict and internal/providers only.
// internal/mcpsrv imports stats, never the reverse, so there is no import cycle.
package stats

import (
	"strings"
	"time"

	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

// Event is one counts-only record appended per hook call. It deliberately holds
// NO finding text, plan/spec content, or raw session id (SessionHash is a salted
// digest, never the raw id).
type Event struct {
	Ts              time.Time      `json:"ts"`
	Tool            string         `json:"tool"`
	Verdict         string         `json:"verdict,omitempty"`
	FindingsTotal   int            `json:"findings_total"`
	SeverityCounts  map[string]int `json:"severity_counts,omitempty"`
	CategoryCounts  map[string]int `json:"category_counts,omitempty"`
	CriterionCounts map[string]int `json:"criterion_counts,omitempty"`
	ReviewMS        int64          `json:"review_ms"`
	Model           string         `json:"model,omitempty"`
	Cached          bool           `json:"cached,omitempty"`
	Partial         bool           `json:"partial,omitempty"`
	PayloadBytes    int            `json:"payload_bytes,omitempty"`
	SessionHash     string         `json:"session_hash,omitempty"`

	// TasksTotal and TasksWithHeader are set only on validate_plan events:
	// how many tasks the plan parsed into, and how many carried a structured
	// Goal / Acceptance criteria header. Plan-header adoption telemetry.
	TasksTotal      int `json:"tasks_total,omitempty"`
	TasksWithHeader int `json:"tasks_with_header,omitempty"`

	// InputTokens / OutputTokens are set only by the I/O-delegation tools
	// (bulk_read, code_write). They are what makes "tokens kept out of the
	// implementer's context" computable. omitempty so no existing event shape
	// changes.
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
}

// countedCriteria bounds what the ledger records. Criterion is free-form
// reviewer text — the pre-hook prompt instructs the reviewer to quote verbatim
// acceptance-criterion text into it — so counting every distinct value would
// give the histogram one key per acceptance criterion ever reviewed and write
// specification text into the ledger, which otherwise holds no free text at all.
// Only these server-recognised sentinels are counted; everything else is dropped.
// Keys are lower-case, and lookups normalise the reviewer's text to match:
// Criterion is free text on the wire, so a reviewer writing "Comment_Hygiene"
// or " comment_hygiene" means the same sentinel and must land in the same
// bucket rather than vanishing from the metric.
var countedCriteria = map[string]bool{
	"comment_hygiene":              true,
	"comment_policy_absent":        true,
	"test_evidence":                true,
	"codescene_adoption":           true,
	"noise_cluster":                true,
	"codebase_reference_checklist": true,
	"codebase_convention":          true,
	"exit_contract":                true,
	"spec":                         true,
	"structure":                    true,
	"max_tokens_override":          true,
}

// CountFindings builds severity, category, and criterion histograms (and the total) from a
// finding slice. Returns nil maps when there are no findings so empty Events
// serialize without empty objects.
func CountFindings(findings []verdict.Finding) (severity, category, criterion map[string]int, total int) {
	if len(findings) == 0 {
		return nil, nil, nil, 0
	}
	severity = make(map[string]int)
	category = make(map[string]int)
	for _, f := range findings {
		severity[string(f.Severity)]++
		category[string(f.Category)]++
		key := strings.ToLower(strings.TrimSpace(f.Criterion))
		if countedCriteria[key] {
			if criterion == nil {
				criterion = make(map[string]int)
			}
			criterion[key]++
		}
	}
	return severity, category, criterion, len(findings)
}
