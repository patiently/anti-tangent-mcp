package stats

import (
	"time"

	"github.com/patiently/anti-tangent-mcp/scorecard"
)

// RunHash returns the salted digest of a plan_run_id. It is keyed differently
// from HashSession so a run id and a session id that happen to share a value
// never share a digest. The raw plan_run_id is never written to runs.jsonl or
// outcomes.jsonl; this digest is the only join key between them.
func (r *Recorder) RunHash(planRunID string) string {
	if r == nil || planRunID == "" {
		return ""
	}
	return scorecard.HashRunID(r.state.Salt, planRunID)
}

// RecordRunLine appends one content-free run snapshot. Best-effort.
func (r *Recorder) RecordRunLine(l scorecard.RunLine) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := appendJSONL(r.dir, runsFile, l); err != nil {
		r.logger.Warn("stats run snapshot append failed", "err", err)
	}
}

// RecordOutcome appends one outcome and schedules a scorecard refresh, so the
// scorecard reflects an outcome as soon as it lands rather than at the next
// compaction. The error is returned because the tool reports it to its caller.
func (r *Recorder) RecordOutcome(o scorecard.OutcomeLine) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	err := appendJSONL(r.dir, outcomesFile, o)
	r.mu.Unlock()
	if err != nil {
		r.logger.Warn("stats outcome append failed", "err", err)
		return err
	}
	if r.scoring.CompareAndSwap(false, true) {
		go func() {
			defer r.scoring.Store(false)
			defer func() {
				if v := recover(); v != nil {
					r.logger.Warn("stats scorecard refresh panicked", "panic", v)
				}
			}()
			r.WriteScorecard()
		}()
	}
	return nil
}

// RunLines returns runs.jsonl's lines for one run hash.
func (r *Recorder) RunLines(runHash string) ([]scorecard.RunLine, error) {
	if r == nil {
		return nil, nil
	}
	r.mu.Lock()
	all, err := readJSONL[scorecard.RunLine](r.dir, runsFile)
	r.mu.Unlock()
	if err != nil {
		return nil, err
	}
	var out []scorecard.RunLine
	for _, l := range all {
		if l.RunHash == runHash {
			out = append(out, l)
		}
	}
	return out, nil
}

// WriteScorecard recomputes scorecard.json from runs.jsonl and
// outcomes.jsonl and returns what it wrote. Best-effort: a write failure is
// logged and the computed scorecard is still returned.
func (r *Recorder) WriteScorecard() scorecard.Scorecard {
	r.mu.Lock()
	runs, skipR, errR := readJSONLCounted[scorecard.RunLine](r.dir, runsFile)
	outs, skipO, errO := readJSONLCounted[scorecard.OutcomeLine](r.dir, outcomesFile)
	r.mu.Unlock()
	if errR != nil || errO != nil {
		r.logger.Warn("stats scorecard read failed", "runs_err", errR, "outcomes_err", errO)
	}
	sc := scorecard.Compute(runs, outs, scorecard.Options{MinRuns: r.minRuns, Now: r.clock()})
	sc.SkippedLines = skipR + skipO
	if err := writeJSON(r.dir, scorecardFile, sc); err != nil {
		r.logger.Warn("stats scorecard write failed", "err", err)
	}
	return sc
}

// pruneRuns drops a run's lines only when the run's newest line is older than
// cutoff. Pruning line by line would strip a long-lived run's header while its
// later task lines survive, leaving the run without its models and verdict.
func pruneRuns(dir string, cutoff time.Time) error {
	lines, err := readJSONL[scorecard.RunLine](dir, runsFile)
	if err != nil || lines == nil {
		return err
	}
	newest := map[string]time.Time{}
	for _, l := range lines {
		if l.Ts.After(newest[l.RunHash]) {
			newest[l.RunHash] = l.Ts
		}
	}
	kept := lines[:0]
	for _, l := range lines {
		if !newest[l.RunHash].Before(cutoff) {
			kept = append(kept, l)
		}
	}
	return rewriteJSONL(dir, runsFile, kept)
}

func pruneOutcomes(dir string, cutoff time.Time) error {
	outs, err := readJSONL[scorecard.OutcomeLine](dir, outcomesFile)
	if err != nil || outs == nil {
		return err
	}
	kept := outs[:0]
	for _, o := range outs {
		if !o.Ts.Before(cutoff) {
			kept = append(kept, o)
		}
	}
	return rewriteJSONL(dir, outcomesFile, kept)
}
