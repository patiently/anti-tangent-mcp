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
	"encoding/json"
	"math"
	"sort"
	"strings"
)

// Verdicts is the per-file verdict tally from an analyze_change_set run.
type Verdicts struct {
	Improved int `json:"improved" jsonschema:"Files whose Code Health improved."`
	Degraded int `json:"degraded" jsonschema:"Files whose Code Health degraded."`
	Stable   int `json:"stable" jsonschema:"Files whose Code Health did not change."`
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
	Ran            bool           `json:"ran,omitempty" jsonschema:"True when a CodeScene analysis of the task's changes actually ran."`
	SkipReason     string         `json:"skip_reason,omitempty" jsonschema:"Why the analysis did not run, when ran is false. The first 300 characters are kept."`
	SkipEvidence   string         `json:"skip_evidence,omitempty" jsonschema:"The failing tool's own error text, when ran is false. Without it a skip is graded like no analysis at all. The first 2000 characters are kept."`
	Tool           string         `json:"tool,omitempty" jsonschema:"The CodeScene tool that produced the result, normally analyze_change_set."`
	QualityGate    string         `json:"quality_gate,omitempty" jsonschema:"passed or failed; analyze_change_set reports it as quality_gates."` // passed|failed
	FilesAnalyzed  int            `json:"files_analyzed,omitempty" jsonschema:"Number of files analysed: the length of analyze_change_set's results."`
	Verdicts       *Verdicts      `json:"verdicts,omitempty" jsonschema:"Per-file verdict counts."`
	Trend          string         `json:"trend,omitempty" jsonschema:"Ignored on input; the server derives it from net_pp."` // improvement|regression|neutral
	NetPP          float64        `json:"net_pp,omitempty" jsonschema:"Net change in problem points: the sum of new-pp minus old-pp over every finding. Positive means worse."`
	CategoryCounts map[string]int `json:"category_counts,omitempty" jsonschema:"Number of findings per CodeScene category, such as Complex Method. The 20 largest are kept."`
}

// Trend values.
const (
	TrendImprovement = "improvement"
	TrendRegression  = "regression"
	TrendNeutral     = "neutral"
)

// rawChangeSetResult is one file entry of CodeScene's raw analyze_change_set
// output: its verdict, and the findings whose problem points and categories
// reduce into a Digest.
type rawChangeSetResult struct {
	Verdict  string `json:"verdict"`
	Findings []struct {
		Category string  `json:"category"`
		NewPP    float64 `json:"new-pp"`
		OldPP    float64 `json:"old-pp"`
	} `json:"findings"`
}

// reduceChangeSetResults tallies rawChangeSetResult.Verdict into a Verdicts,
// sums each finding's new-pp minus old-pp into a net problem-points delta,
// and counts findings per non-empty category. Split out of UnmarshalJSON so
// that function's own branching (whether each field is caller-present) stays
// separate from this one's (how the raw results reduce).
func reduceChangeSetResults(results []rawChangeSetResult) (verdicts Verdicts, netPP float64, categoryCounts map[string]int) {
	categoryCounts = map[string]int{}
	for _, r := range results {
		switch r.Verdict {
		case "improved":
			verdicts.Improved++
		case "degraded":
			verdicts.Degraded++
		case "stable":
			verdicts.Stable++
		}
		for _, f := range r.Findings {
			netPP += f.NewPP - f.OldPP
			if f.Category != "" {
				categoryCounts[f.Category]++
			}
		}
	}
	return verdicts, netPP, categoryCounts
}

// UnmarshalJSON accepts both the digest shape and CodeScene's raw
// analyze_change_set output, reducing quality_gates and results[] the way
// examples/hooks/codescene-log.sh does. A digest field present in the input
// always wins over the value derived from raw keys. Unknown keys are ignored,
// and each raw key is decoded on its own, so one of the wrong type is ignored
// without disturbing the other: the argument is optional, and a malformed side
// field must not cost the caller the whole call or the rest of the reduction.
// A struct that embeds Digest anonymously inherits this method by Go's
// promotion rules, which makes it the struct's own UnmarshalJSON — its other
// fields are then never decoded unless that struct defines its own
// UnmarshalJSON that delegates to this one.
func (d *Digest) UnmarshalJSON(b []byte) error {
	type plainDigest Digest
	var p plainDigest
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*d = Digest(p)

	var present map[string]json.RawMessage
	if err := json.Unmarshal(b, &present); err != nil {
		return nil
	}
	has := func(key string) bool { _, ok := present[key]; return ok }

	var gate string
	gateOK := has("quality_gates") && json.Unmarshal(present["quality_gates"], &gate) == nil
	var results []rawChangeSetResult
	resultsOK := has("results") && json.Unmarshal(present["results"], &results) == nil && results != nil
	if !gateOK && !resultsOK {
		return nil
	}

	if !has("ran") {
		d.Ran = true
	}
	if !has("tool") {
		d.Tool = "analyze_change_set"
	}
	if gateOK && !has("quality_gate") {
		d.QualityGate = gate
	}
	if !resultsOK {
		return nil
	}
	verdicts, netPP, counts := reduceChangeSetResults(results)
	if !has("files_analyzed") {
		d.FilesAnalyzed = len(results)
	}
	if !has("verdicts") {
		d.Verdicts = &verdicts
	}
	if !has("net_pp") {
		d.NetPP = netPP
	}
	if !has("category_counts") && len(counts) > 0 {
		d.CategoryCounts = counts
	}
	return nil
}

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

// codesceneSkipEvidenceMaxRunes bounds SkipEvidence's length in Normalize.
// It is an order of magnitude above the SkipReason cap because the two hold
// different things: a reason is a sentence a caller composes, while evidence
// is a tool's own error output pasted verbatim, and a useful stack or MCP
// error easily runs past a few hundred runes. Like SkipReason it is outside
// the request-level payload cap and lands in plan-runs.jsonl, so it needs a
// bound of its own.
const codesceneSkipEvidenceMaxRunes = 2000

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
	d.SkipEvidence = truncateRunes(strings.TrimSpace(d.SkipEvidence), codesceneSkipEvidenceMaxRunes)
	d.CategoryCounts = capCategoryCounts(d.CategoryCounts, codesceneCategoryCountsMax, codesceneCategoryKeyMaxRunes)
}

// truncateRunes returns s if its rune count is at or below max; otherwise the
// first max runes followed by a single UTF-8 ellipsis -- max counts the
// RETAINED runes, so a truncated result is max+1 runes long. internal/planrun
// has a fitRunes whose bound covers the whole result instead, so a cap moved
// between the two shifts by one rune. Rune-based truncation
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

// saturatingAdd returns a+b, clamped to the int range instead of wrapping.
// Both operands are caller-supplied: CategoryCounts arrives straight off the
// wire, and key truncation can make two distinct keys collide so their counts
// are summed. A plain + could wrap to a negative, which would then reorder or
// silently drop entries in the top-N cap below.
func saturatingAdd(a, b int) int {
	if b > 0 && a > math.MaxInt-b {
		return math.MaxInt
	}
	if b < 0 && a < math.MinInt-b {
		return math.MinInt
	}
	return a + b
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
		key := truncateRunes(k, maxKeyRunes)
		truncated[key] = saturatingAdd(truncated[key], v)
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
