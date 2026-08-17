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

import (
	"sort"
	"strings"
)

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

// qualityGateUnrecognized is what Normalize maps QualityGate to when it is
// non-empty but not one of the recognized values. QualityGate is caller-
// supplied free text with only a comment ("// passed|failed") pinning its
// shape; without this, an out-of-range value renders as bare, unvalidated
// prose in both the reviewer prompt and the plan-run report table.
const qualityGateUnrecognized = "unrecognized"

// codesceneSkipReasonMaxRunes bounds SkipReason's length in Normalize.
// SkipReason is free text with no request-level cap, and lands verbatim in
// the reviewer prompt and plan-runs.jsonl; a few hundred runes is generous
// for a one-line reason.
const codesceneSkipReasonMaxRunes = 300

// codesceneCategoryCountsMax bounds how many CategoryCounts entries
// Normalize retains. CategoryCounts is an unbounded caller-supplied map that,
// like SkipReason, is excluded from the request-level payload cap
// (totalCompletionBytes) and lands verbatim in plan-runs.jsonl.
const codesceneCategoryCountsMax = 20

// codesceneCategoryKeyMaxRunes bounds each CategoryCounts key's length in
// Normalize. Entry count alone isn't enough: a caller can stay under
// codesceneCategoryCountsMax while sending a handful of enormous keys, which
// would still ride the entry-count cap straight into the reviewer prompt and
// plan-runs.jsonl. Real CodeScene category names are short fixed labels
// (e.g. "Complex Method"); this cap exists only to bound caller-attested
// input, not to accommodate legitimate long names.
const codesceneCategoryKeyMaxRunes = 100

// Normalize derives Trend from NetPP, lowercases and validates QualityGate,
// and bounds the two caller-supplied free-form fields (SkipReason,
// CategoryCounts). It is the only integrity/size check applied to an inbound
// digest: it cannot tell whether the numbers came from a real CodeScene run,
// but it does stop a caller reporting an improvement alongside positive
// problem points, an unrecognized quality-gate string, or an unbounded
// payload riding along in fields the request-level cap doesn't cover.
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
	if d.QualityGate != "" && d.QualityGate != "passed" && d.QualityGate != "failed" {
		d.QualityGate = qualityGateUnrecognized
	}
	d.SkipReason = truncateRunes(strings.TrimSpace(d.SkipReason), codesceneSkipReasonMaxRunes)
	d.CategoryCounts = capCategoryCounts(d.CategoryCounts, codesceneCategoryCountsMax, codesceneCategoryKeyMaxRunes)
}

// truncateRunes returns s if its rune count is at or below max; otherwise the
// first max runes followed by a single UTF-8 ellipsis. Rune-based truncation
// avoids splitting multi-byte UTF-8 characters mid-codepoint. Duplicated from
// (rather than sharing) internal/mcpsrv/summary.go's truncate: codescene is a
// leaf package (see the package doc) and must not import internal/mcpsrv.
func truncateRunes(s string, max int) string {
	if s == "" || max <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}

// capCategoryCounts truncates every key to at most maxKeyRunes, then bounds
// the result to at most maxEntries entries, keeping the highest-count
// categories and breaking ties alphabetically so which entries survive is
// deterministic rather than depending on Go's randomized map iteration
// order. Key truncation runs unconditionally — including when the map is
// already at or under maxEntries — because entry count and key length are
// independent knobs a caller controls separately. Truncated keys that
// collide have their counts summed rather than one silently overwriting the
// other. nil maps pass through unchanged (nil stays nil so json omitempty
// keeps behaving the same way for callers who never set the field).
func capCategoryCounts(counts map[string]int, maxEntries, maxKeyRunes int) map[string]int {
	if counts == nil {
		return nil
	}
	truncated := make(map[string]int, len(counts))
	for k, v := range counts {
		truncated[truncateRunes(k, maxKeyRunes)] += v
	}
	if len(truncated) <= maxEntries {
		return truncated
	}
	type kv struct {
		k string
		v int
	}
	pairs := make([]kv, 0, len(truncated))
	for k, v := range truncated {
		pairs = append(pairs, kv{k, v})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].v != pairs[j].v {
			return pairs[i].v > pairs[j].v
		}
		return pairs[i].k < pairs[j].k
	})
	out := make(map[string]int, maxEntries)
	for _, p := range pairs[:maxEntries] {
		out[p.k] = p.v
	}
	return out
}
