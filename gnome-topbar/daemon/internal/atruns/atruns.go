// Package atruns reads anti-tangent's run snapshots, review outcomes and
// scorecard from the stats directory, and joins in task titles from the local
// plan ledger when it exists. Titles never leave this machine: they are for
// the local run-detail page only and are not part of any shared record.
package atruns

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/patiently/anti-tangent-mcp/scorecard"
)

type Data struct {
	Present  bool
	Lines    []scorecard.RunLine
	Outcomes []scorecard.OutcomeLine
	Skipped  int
	// Titles maps run hash -> task index -> task title, local runs only.
	Titles map[string]map[int]string
	// MinRuns is the server's configured regression gate, taken from
	// scorecard.json so the daemon's recomputed views gate the same way.
	MinRuns int
	// Revisit and RevisitGated are the server's revisit signals and the
	// comparisons too small to judge. RevisitKnown is true only when
	// scorecard.json parsed and carried the revisit key: an older server, or
	// one whose own read failed, omits it, and that must not read as "no
	// signals" or the tray would forget every ack.
	Revisit      []scorecard.Signal
	RevisitGated []scorecard.Gated
	RevisitKnown bool
}

func Read(dir string) (Data, error) {
	var d Data
	lines, skipR, err := readJSONL[scorecard.RunLine](filepath.Join(dir, "runs.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return d, nil
	}
	if err != nil {
		return d, err
	}
	outs, skipO, err := readJSONL[scorecard.OutcomeLine](filepath.Join(dir, "outcomes.jsonl"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return d, err
	}
	d.Present, d.Lines, d.Outcomes, d.Skipped = true, lines, outs, skipR+skipO
	d.Titles = readTitles(dir)
	d.MinRuns, d.Revisit, d.RevisitGated, d.RevisitKnown = readScorecard(dir)
	return d, nil
}

func readScorecard(dir string) (minRuns int, rev []scorecard.Signal, gated []scorecard.Gated, known bool) {
	b, err := os.ReadFile(filepath.Join(dir, "scorecard.json"))
	if err != nil {
		return 0, nil, nil, false
	}
	var sc struct {
		MinRuns int                 `json:"min_runs"`
		Revisit *[]scorecard.Signal `json:"revisit"`
		Gated   *[]scorecard.Gated  `json:"revisit_coverage"`
	}
	if json.Unmarshal(b, &sc) != nil {
		return 0, nil, nil, false
	}
	if sc.Revisit == nil {
		return sc.MinRuns, nil, nil, false
	}
	if sc.Gated != nil {
		gated = *sc.Gated
	}
	return sc.MinRuns, *sc.Revisit, gated, true
}

func readTitles(dir string) map[string]map[int]string {
	out := map[string]map[int]string{}
	var salt string
	if b, err := os.ReadFile(filepath.Join(dir, "state.json")); err == nil {
		var st struct {
			Salt string `json:"salt"`
		}
		if json.Unmarshal(b, &st) == nil {
			salt = st.Salt
		}
	}
	type ledgerRow struct {
		PlanRunID      string `json:"plan_run_id"`
		SessionRunHash string `json:"session_run_hash"`
		Row            struct {
			Index     int    `json:"index"`
			TaskTitle string `json:"task_title"`
		} `json:"row"`
	}
	rows, _, err := readJSONL[ledgerRow](filepath.Join(dir, "plan-runs.jsonl"))
	if err != nil {
		return out
	}
	for _, r := range rows {
		if r.SessionRunHash != "" {
			if r.Row.TaskTitle != "" {
				if out[r.SessionRunHash] == nil {
					out[r.SessionRunHash] = map[int]string{}
				}
				out[r.SessionRunHash][1] = r.Row.TaskTitle
			}
			continue
		}
		if salt == "" || r.PlanRunID == "" || r.Row.Index == 0 || r.Row.TaskTitle == "" {
			continue
		}
		h := scorecard.HashRunID(salt, r.PlanRunID)
		if out[h] == nil {
			out[h] = map[int]string{}
		}
		out[h][r.Row.Index] = r.Row.TaskTitle
	}
	return out
}

type RunSummary struct {
	Publisher         string
	RunHash           string
	Latest            time.Time
	PlanVerdict       string
	Kind              string
	TaskCount         int
	ConfiguredModels  map[string]string
	ImplementerModels []string
	Sources           []string
	Escapes           map[string]int
}

// Runs summarises every run, newest first.
func Runs(lines []scorecard.RunLine, outcomes []scorecard.OutcomeLine) []RunSummary {
	type key struct{ pub, hash string }
	byKey := map[key]*RunSummary{}
	perRun := map[key][]scorecard.RunLine{}
	get := func(k key) *RunSummary {
		s, ok := byKey[k]
		if !ok {
			s = &RunSummary{Publisher: k.pub, RunHash: k.hash, Escapes: map[string]int{}}
			byKey[k] = s
		}
		return s
	}
	for _, l := range lines {
		k := key{l.Publisher, l.RunHash}
		s := get(k)
		perRun[k] = append(perRun[k], l)
		if l.Ts.After(s.Latest) {
			s.Latest = l.Ts
		}
		if l.Header {
			s.Kind = l.Kind
			s.PlanVerdict, s.TaskCount, s.ConfiguredModels = l.PlanVerdict, l.TaskCount, l.ConfiguredModels
		}
	}
	latestOutcome := map[key]map[string]scorecard.OutcomeLine{}
	for _, o := range outcomes {
		k := key{o.Publisher, o.RunHash}
		if latestOutcome[k] == nil {
			latestOutcome[k] = map[string]scorecard.OutcomeLine{}
		}
		if prev, ok := latestOutcome[k][o.Source]; !ok || !o.Ts.Before(prev.Ts) {
			latestOutcome[k][o.Source] = o
		}
	}
	reported := map[key]map[int]bool{}
	for k, bySource := range latestOutcome {
		s := get(k)
		reported[k] = map[int]bool{}
		for _, src := range scorecard.Sources {
			o, ok := bySource[src]
			if !ok {
				continue
			}
			s.Sources = append(s.Sources, src)
			esc, _, _ := scorecard.RunEscapes(perRun[k], o)
			s.Escapes[src] = len(esc)
			for _, m := range o.ImplementerModels {
				model := scorecard.NormalizeImplementerModel(m.Model)
				if model == "" {
					continue
				}
				reported[k][m.TaskIndex] = true
				s.ImplementerModels = append(s.ImplementerModels, model)
			}
		}
	}
	// A task no outcome names a model for falls back to the model its
	// implementer reported for itself in the task's latest snapshot, the same
	// order the scorecard uses; a session run has no final review, so this is
	// its only source.
	for k, s := range byKey {
		for index, snap := range latestTaskSnapshots(perRun[k]) {
			if reported[k][index] {
				continue
			}
			if m := scorecard.NormalizeImplementerModel(snap.ImplementerModel); m != "" {
				s.ImplementerModels = append(s.ImplementerModels, m)
			}
		}
		s.ImplementerModels = dedupSorted(s.ImplementerModels)
	}
	out := make([]RunSummary, 0, len(byKey))
	for _, s := range byKey {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Latest.Equal(out[j].Latest) {
			return out[i].Latest.After(out[j].Latest)
		}
		return out[i].RunHash < out[j].RunHash
	})
	return out
}

// latestTaskSnapshots returns each task's newest snapshot, a later line
// winning a timestamp tie, as the scorecard resolves them.
func latestTaskSnapshots(lines []scorecard.RunLine) map[int]scorecard.TaskSnapshot {
	snaps := map[int]scorecard.TaskSnapshot{}
	ts := map[int]time.Time{}
	for _, l := range lines {
		if l.Header || l.Task == nil {
			continue
		}
		if prev, ok := ts[l.Task.Index]; ok && l.Ts.Before(prev) {
			continue
		}
		snaps[l.Task.Index], ts[l.Task.Index] = *l.Task, l.Ts
	}
	return snaps
}

func dedupSorted(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	sort.Strings(in)
	out := in[:1]
	for _, v := range in[1:] {
		if v != out[len(out)-1] {
			out = append(out, v)
		}
	}
	return out
}

func readJSONL[T any](path string) ([]T, int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	var out []T
	skipped := 0
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var v T
		if json.Unmarshal(line, &v) != nil {
			skipped++
			continue
		}
		out = append(out, v)
	}
	return out, skipped, sc.Err()
}
