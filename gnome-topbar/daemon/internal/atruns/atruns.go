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
	d.MinRuns = readMinRuns(dir)
	return d, nil
}

func readMinRuns(dir string) int {
	b, err := os.ReadFile(filepath.Join(dir, "scorecard.json"))
	if err != nil {
		return 0
	}
	var sc struct {
		MinRuns int `json:"min_runs"`
	}
	if json.Unmarshal(b, &sc) != nil {
		return 0
	}
	return sc.MinRuns
}

func readTitles(dir string) map[string]map[int]string {
	out := map[string]map[int]string{}
	b, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		return out
	}
	var st struct {
		Salt string `json:"salt"`
	}
	if json.Unmarshal(b, &st) != nil || st.Salt == "" {
		return out
	}
	type ledgerRow struct {
		PlanRunID string `json:"plan_run_id"`
		Row       struct {
			Index     int    `json:"index"`
			TaskTitle string `json:"task_title"`
		} `json:"row"`
	}
	rows, _, err := readJSONL[ledgerRow](filepath.Join(dir, "plan-runs.jsonl"))
	if err != nil {
		return out
	}
	for _, r := range rows {
		if r.PlanRunID == "" || r.Row.Index == 0 || r.Row.TaskTitle == "" {
			continue
		}
		h := scorecard.HashRunID(st.Salt, r.PlanRunID)
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
	for k, bySource := range latestOutcome {
		s := get(k)
		seen := map[string]bool{}
		for _, src := range scorecard.Sources {
			o, ok := bySource[src]
			if !ok {
				continue
			}
			s.Sources = append(s.Sources, src)
			esc, _, _ := scorecard.RunEscapes(perRun[k], o)
			s.Escapes[src] = len(esc)
			for _, m := range o.ImplementerModels {
				if !seen[m.Model] {
					seen[m.Model] = true
					s.ImplementerModels = append(s.ImplementerModels, m.Model)
				}
			}
		}
		sort.Strings(s.ImplementerModels)
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
