package stats

import (
	"math"
	"time"
)

const codesceneFile = "codescene-events.jsonl"

// Verdicts is the per-file verdict tally from an analyze_change_set run.
type Verdicts struct {
	Improved int `json:"improved"`
	Degraded int `json:"degraded"`
	Stable   int `json:"stable"`
}

// CodesceneEvent is the per-run record the hook appends (see
// docs/team-setup/codescene-stats.md). anti-tangent only READS this file; it
// never writes it. Counts + metadata only — no file paths. analyze_change_set is
// categorical (verdicts / quality-gate / problem-points), not a 1-10 score.
type CodesceneEvent struct {
	Ts             time.Time      `json:"ts"`
	Tool           string         `json:"tool"`
	QualityGate    string         `json:"quality_gate"` // passed|failed
	FilesAnalyzed  int            `json:"files_analyzed"`
	Verdicts       Verdicts       `json:"verdicts"`
	Trend          string         `json:"trend"` // improvement|regression|neutral
	NetPP          float64        `json:"net_pp"`
	CategoryCounts map[string]int `json:"category_counts,omitempty"`
}

// CodesceneRollup is the nested `codescene` block in rollup.json.
type CodesceneRollup struct {
	Runs              int            `json:"runs"`
	GatesPassed       int            `json:"gates_passed"`
	GatesFailed       int            `json:"gates_failed"`
	LatestGate        string         `json:"latest_gate"`
	LatestTrend       string         `json:"latest_trend"`
	LatestNetPP       float64        `json:"latest_net_pp"`
	NetPPP50          float64        `json:"net_pp_p50"`
	Regressions       int            `json:"regressions"`
	Improvements      int            `json:"improvements"`
	Neutral           int            `json:"neutral"`
	FilesAnalyzed     int            `json:"files_analyzed"`
	CategoryHistogram map[string]int `json:"category_histogram"`
	WindowStart       time.Time      `json:"window_start"`
	WindowEnd         time.Time      `json:"window_end"`
}

func readCodescene(dir string) ([]CodesceneEvent, error) {
	return readJSONL[CodesceneEvent](dir, codesceneFile)
}

// pruneCodescene rewrites codescene-events.jsonl keeping only records at/after cutoff.
func pruneCodescene(dir string, cutoff time.Time) error {
	events, err := readCodescene(dir)
	if err != nil {
		return err
	}
	kept := events[:0]
	for _, e := range events {
		if !e.Ts.Before(cutoff) {
			kept = append(kept, e)
		}
	}
	return rewriteJSONL(dir, codesceneFile, kept)
}

// computeCodescene aggregates per-run records. Returns nil when there are none,
// so the rollup's `codescene` key is omitted entirely (absence == no data).
func computeCodescene(events []CodesceneEvent) *CodesceneRollup {
	if len(events) == 0 {
		return nil
	}
	cr := &CodesceneRollup{
		CategoryHistogram: map[string]int{},
		WindowStart:       events[0].Ts,
		WindowEnd:         events[0].Ts,
		Runs:              len(events),
	}
	nps := make([]int64, 0, len(events)) // net_pp*100 as int64 for percentile()
	latest := events[0]
	for _, e := range events {
		if e.Ts.Before(cr.WindowStart) {
			cr.WindowStart = e.Ts
		}
		if !e.Ts.Before(cr.WindowEnd) {
			cr.WindowEnd = e.Ts
			latest = e
		}
		switch e.QualityGate {
		case "passed":
			cr.GatesPassed++
		case "failed":
			cr.GatesFailed++
		}
		switch e.Trend {
		case "regression":
			cr.Regressions++
		case "improvement":
			cr.Improvements++
		default:
			cr.Neutral++
		}
		cr.FilesAnalyzed += e.FilesAnalyzed
		for k, v := range e.CategoryCounts {
			cr.CategoryHistogram[k] += v
		}
		nps = append(nps, int64(math.Round(e.NetPP*100)))
	}
	cr.LatestGate = latest.QualityGate
	cr.LatestTrend = latest.Trend
	cr.LatestNetPP = latest.NetPP
	cr.NetPPP50 = float64(percentile(nps, 50)) / 100
	return cr
}
