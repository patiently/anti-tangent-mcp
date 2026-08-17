// Package codescene defines the CodeScene analyze_change_set digest shape
// shared by the validate_completion MCP argument and the stats subsystem's
// on-disk record. It is a leaf package: it imports nothing else from this
// repository, so both internal/stats and internal/mcpsrv can depend on it
// without an import cycle.
//
// anti-tangent never calls CodeScene. It receives a digest a caller computed
// (the same reduction examples/hooks/codescene-log.sh performs) and treats it
// as caller-attested, exactly like pinned_by.
package codescene

import "strings"

// Verdicts is the per-file verdict tally from an analyze_change_set run.
type Verdicts struct {
	Improved int `json:"improved"`
	Degraded int `json:"degraded"`
	Stable   int `json:"stable"`
}

// Digest is one analyze_change_set result reduced to counts and metadata.
// No file paths, no code, no function names — privacy parity with the rest of
// the stats subsystem.
//
// Ran and SkipReason are omitempty and absent from hook-written records; the
// hook has no notion of a deliberate skip, so a record it wrote unmarshals
// with Ran=false and is distinguished from a caller-declared skip by
// SkipReason being empty too.
type Digest struct {
	Ran            bool           `json:"ran,omitempty"`
	SkipReason     string         `json:"skip_reason,omitempty"`
	Tool           string         `json:"tool,omitempty"`
	QualityGate    string         `json:"quality_gate,omitempty"` // passed|failed
	FilesAnalyzed  int            `json:"files_analyzed,omitempty"`
	Verdicts       *Verdicts      `json:"verdicts,omitempty"`
	Trend          string         `json:"trend,omitempty"` // improvement|regression|neutral
	NetPP          float64        `json:"net_pp,omitempty"`
	CategoryCounts map[string]int `json:"category_counts,omitempty"`
}

// Trend values.
const (
	TrendImprovement = "improvement"
	TrendRegression  = "regression"
	TrendNeutral     = "neutral"
)

// Normalize derives Trend from NetPP and lowercases QualityGate. It is the
// only integrity check applied to an inbound digest: it cannot tell whether
// the numbers came from a real CodeScene run, but it does stop a caller
// reporting an improvement alongside positive problem points.
func (d *Digest) Normalize() {
	switch {
	case d.NetPP > 0:
		d.Trend = TrendRegression
	case d.NetPP < 0:
		d.Trend = TrendImprovement
	default:
		d.Trend = TrendNeutral
	}
	d.QualityGate = strings.ToLower(strings.TrimSpace(d.QualityGate))
}
