package scorecard

import "sort"

// ToolOrder is the display order of anti-tangent's reviewing tools.
var ToolOrder = []string{"validate_plan", "validate_task_spec", "check_progress", "validate_completion"}

type ToolModelOutcome struct {
	Source              string `json:"source"`
	EscapeRate          Rate   `json:"escape_rate"`
	UnconfirmedFlagRate Rate   `json:"unconfirmed_flag_rate"`
}

// ToolModelRow reports one validator model's work in one anti-tangent tool.
// Its operational columns cover the calls still in each task's capped call
// log; a call evicted by the cap is known only as a count, with no tool or
// model, so it cannot be placed in any row.
// The outcome columns slice escapes by the model that reviewed the task in
// this tool; a task has one outcome and up to four reviewing models, so this
// is a slice, not a causal attribution.
type ToolModelRow struct {
	Tool            string             `json:"tool"`
	Model           string             `json:"model"`
	Publisher       string             `json:"publisher,omitempty"`
	Calls           int                `json:"calls"`
	Runs            int                `json:"runs"`
	Tasks           int                `json:"tasks"`
	VerdictCounts   map[string]int     `json:"verdict_counts"`
	FindingsPerCall float64            `json:"findings_per_call"`
	MSP50           int64              `json:"ms_p50"`
	MSP95           int64              `json:"ms_p95"`
	PartialRate     float64            `json:"partial_rate"`
	Outcomes        []ToolModelOutcome `json:"outcomes,omitempty"`
}

type ModelSet struct {
	Models map[string]string `json:"models"`
	Runs   int               `json:"runs"`
}

type tmKey struct{ tool, model, publisher string }

type tmAcc struct {
	calls, findings, partial int
	verdicts                 map[string]int
	ms                       []int64
	runs                     map[runKey]bool
	tasks                    map[[2]any]bool
	out                      map[string]*[4]int // source -> passN, escNum, flagN, unconfNum
}

func toolModelRows(runs map[runKey]*run, byPublisher bool) []ToolModelRow {
	accs := map[tmKey]*tmAcc{}
	get := func(tool, model string, r *run) *tmAcc {
		k := tmKey{tool: tool, model: model}
		if byPublisher {
			k.publisher = r.key.publisher
		}
		a, ok := accs[k]
		if !ok {
			a = &tmAcc{verdicts: map[string]int{}, runs: map[runKey]bool{}, tasks: map[[2]any]bool{}, out: map[string]*[4]int{}}
			accs[k] = a
		}
		return a
	}
	addCall := func(a *tmAcc, r *run, c ToolCall) {
		a.calls++
		a.findings += c.Findings
		if c.Partial {
			a.partial++
		}
		if c.Verdict != "" {
			a.verdicts[c.Verdict]++
		}
		a.ms = append(a.ms, c.MS)
		a.runs[r.key] = true
	}
	score := func(a *tmAcc, src string, t *task, high int, flagged bool) {
		o, ok := a.out[src]
		if !ok {
			o = &[4]int{}
			a.out[src] = o
		}
		if t.snap.PostVerdict == "pass" {
			o[0]++
			if high > 0 {
				o[1]++
			}
		}
		if flagged {
			o[2]++
			if high == 0 {
				o[3]++
			}
		}
	}
	for _, r := range runs {
		if r.header != nil && r.header.PlanCall != nil {
			a := get("validate_plan", r.header.PlanCall.Model, r)
			addCall(a, r, *r.header.PlanCall)
		}
		for idx, t := range r.tasks {
			for _, c := range t.snap.Calls {
				a := get(c.Tool, c.Model, r)
				addCall(a, r, c)
				a.tasks[[2]any{r.key, idx}] = true
			}
		}
		for _, src := range Sources {
			o, ok := r.outcomes[src]
			if !ok {
				continue
			}
			for _, t := range r.tasks {
				if t.snap.PostVerdict == "" {
					continue
				}
				high, _ := outcomeCounts(o, t.snap.Index)
				for _, tool := range []string{"validate_task_spec", "validate_completion"} {
					if c, ok := t.latestCall(tool); ok {
						score(get(tool, c.Model, r), src, t, high, isFlag(c.Verdict))
					}
				}
				checkpointFlag := map[string]bool{}
				for _, c := range t.snap.Calls {
					if c.Tool == "check_progress" {
						checkpointFlag[c.Model] = checkpointFlag[c.Model] || isFlag(c.Verdict)
					}
				}
				for model, flagged := range checkpointFlag {
					score(get("check_progress", model, r), src, t, high, flagged)
				}
				if r.header != nil && r.header.PlanCall != nil {
					score(get("validate_plan", r.header.PlanCall.Model, r), src, t, high, isFlag(r.header.PlanVerdict))
				}
			}
		}
	}
	rows := make([]ToolModelRow, 0, len(accs))
	for k, a := range accs {
		row := ToolModelRow{
			Tool: k.tool, Model: k.model, Publisher: k.publisher,
			Calls: a.calls, Runs: len(a.runs), Tasks: len(a.tasks),
			VerdictCounts: a.verdicts,
			MSP50:         percentile(a.ms, 50), MSP95: percentile(a.ms, 95),
		}
		if k.tool == "validate_plan" {
			row.Tasks = 0
			for rk := range a.runs {
				if h := runs[rk].header; h != nil {
					row.Tasks += h.TaskCount
				}
			}
		}
		if a.calls > 0 {
			row.FindingsPerCall = float64(a.findings) / float64(a.calls)
			row.PartialRate = float64(a.partial) / float64(a.calls)
		}
		for _, src := range Sources {
			if o, ok := a.out[src]; ok {
				row.Outcomes = append(row.Outcomes, ToolModelOutcome{Source: src, EscapeRate: wilson(o[1], o[0]), UnconfirmedFlagRate: wilson(o[3], o[2])})
			}
		}
		rows = append(rows, row)
	}
	rank := map[string]int{}
	for i, t := range ToolOrder {
		rank[t] = i
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if rank[a.Tool] != rank[b.Tool] {
			return rank[a.Tool] < rank[b.Tool]
		}
		if a.Model != b.Model {
			return a.Model < b.Model
		}
		return a.Publisher < b.Publisher
	})
	return rows
}

func modelSets(runs map[runKey]*run) []ModelSet {
	type entry struct {
		models map[string]string
		runs   int
	}
	byKey := map[string]*entry{}
	for _, r := range runs {
		if r.header == nil || len(r.header.ConfiguredModels) == 0 {
			continue
		}
		k := modelSetKey(r.header.ConfiguredModels)
		e, ok := byKey[k]
		if !ok {
			e = &entry{models: r.header.ConfiguredModels}
			byKey[k] = e
		}
		e.runs++
	}
	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if byKey[keys[i]].runs != byKey[keys[j]].runs {
			return byKey[keys[i]].runs > byKey[keys[j]].runs
		}
		return keys[i] < keys[j]
	})
	out := make([]ModelSet, 0, len(keys))
	for _, k := range keys {
		out = append(out, ModelSet{Models: byKey[k].models, Runs: byKey[k].runs})
	}
	return out
}

func modelSetKey(m map[string]string) string {
	roles := make([]string, 0, len(m))
	for r := range m {
		roles = append(roles, r)
	}
	sort.Strings(roles)
	s := ""
	for _, r := range roles {
		s += r + "=" + m[r] + ";"
	}
	return s
}

func publishers(runs map[runKey]*run) []string {
	seen := map[string]bool{}
	for k := range runs {
		if k.publisher != "" {
			seen[k.publisher] = true
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
