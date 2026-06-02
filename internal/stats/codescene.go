package stats

import "time"

const codesceneFile = "codescene-events.jsonl"

// CodesceneEvent is the per-run record the AGENT appends (see INTEGRATION.md and
// docs/team-setup/codescene-stats.md). anti-tangent only ever READS this file;
// it never writes it. Counts + metadata only — no file paths.
type CodesceneEvent struct {
	Ts             time.Time      `json:"ts"`
	Tool           string         `json:"tool"`
	ScoreBefore    float64        `json:"score_before"`
	ScoreAfter     float64        `json:"score_after"`
	Delta          float64        `json:"delta"`
	Trend          string         `json:"trend"`
	FilesAnalyzed  int            `json:"files_analyzed"`
	CategoryCounts map[string]int `json:"category_counts,omitempty"`
}

// CodesceneRollup is the nested `codescene` block in rollup.json. snake_case
// json tags are part of the same cross-component contract as Rollup (§12.4).
type CodesceneRollup struct {
	Runs              int            `json:"runs"`
	LatestScore       float64        `json:"latest_score"`
	LatestDelta       float64        `json:"latest_delta"`
	LatestTrend       string         `json:"latest_trend"`
	ScoreP50          float64        `json:"score_p50"`
	Regressions       int            `json:"regressions"`
	Improvements      int            `json:"improvements"`
	Neutral           int            `json:"neutral"`
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
func computeCodescene(events []CodesceneEvent, now time.Time) *CodesceneRollup {
	if len(events) == 0 {
		return nil
	}
	cr := &CodesceneRollup{
		CategoryHistogram: map[string]int{},
		WindowStart:       events[0].Ts,
		WindowEnd:         events[0].Ts,
		Runs:              len(events),
	}
	scores := make([]int64, 0, len(events)) // score*100 so we can reuse percentile (int64)
	latest := events[0]
	for _, e := range events {
		if e.Ts.Before(cr.WindowStart) {
			cr.WindowStart = e.Ts
		}
		if !e.Ts.Before(cr.WindowEnd) {
			cr.WindowEnd = e.Ts
			latest = e
		}
		switch e.Trend {
		case "regression":
			cr.Regressions++
		case "improvement":
			cr.Improvements++
		default:
			cr.Neutral++
		}
		for k, v := range e.CategoryCounts {
			cr.CategoryHistogram[k] += v
		}
		scores = append(scores, int64(e.ScoreAfter*100))
	}
	cr.LatestScore = latest.ScoreAfter
	cr.LatestDelta = latest.Delta
	cr.LatestTrend = latest.Trend
	cr.ScoreP50 = float64(percentile(scores, 50)) / 100
	return cr
}
