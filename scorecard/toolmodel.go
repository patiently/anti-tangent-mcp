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

// toolModelRows builds one ToolModelRow per (tool, model[, publisher]) seen
// across runs: an operational pass over every call, then an outcome pass
// scoring each finished task against every source that reviewed its run.
func toolModelRows(runs map[runKey]*run, byPublisher bool) []ToolModelRow {
	accs := map[tmKey]*tmAcc{}
	for _, r := range runs {
		c := &tmCtx{accs: accs, byPublisher: byPublisher, r: r}
		c.collectRunCalls()
		c.scoreRunOutcomes()
	}
	rows := buildToolModelRows(accs, runs)
	sortToolModelRows(rows)
	return rows
}

// tmCtx threads what every scoring step needs to find or create an
// accumulator — the map being built, whether it is split by publisher, and
// the run currently being folded in — so the per-call and per-task helpers
// below take only the arguments specific to their own job.
type tmCtx struct {
	accs        map[tmKey]*tmAcc
	byPublisher bool
	r           *run
}

// acc returns c.r's accumulator for (tool, model), creating it on first use.
// The publisher is folded into the key only in per-publisher views, so the
// pooled view's accumulators merge every publisher's calls for the same tool
// and model.
func (c *tmCtx) acc(tool, model string) *tmAcc {
	k := tmKey{tool: tool, model: model}
	if c.byPublisher {
		k.publisher = c.r.key.publisher
	}
	a, ok := c.accs[k]
	if !ok {
		a = &tmAcc{verdicts: map[string]int{}, runs: map[runKey]bool{}, tasks: map[[2]any]bool{}, out: map[string]*[4]int{}}
		c.accs[k] = a
	}
	return a
}

// addCall folds one call into its accumulator's operational counters.
func addCall(a *tmAcc, r *run, call ToolCall) {
	a.calls++
	a.findings += call.Findings
	if call.Partial {
		a.partial++
	}
	if call.Verdict != "" {
		a.verdicts[call.Verdict]++
	}
	a.ms = append(a.ms, call.MS)
	a.runs[r.key] = true
}

// collectRunCalls folds c.r's validate_plan call and every task's call log
// into their accumulators' operational counters (calls, verdicts, latency,
// the run and task sets a row's Calls/Runs/Tasks columns are sized from).
func (c *tmCtx) collectRunCalls() {
	r := c.r
	if r.header != nil && r.header.PlanCall != nil {
		addCall(c.acc("validate_plan", r.header.PlanCall.Model), r, *r.header.PlanCall)
	}
	for idx, t := range r.tasks {
		for _, call := range t.snap.Calls {
			a := c.acc(call.Tool, call.Model)
			addCall(a, r, call)
			a.tasks[[2]any{r.key, idx}] = true
		}
	}
}

// taskScore is what scoreOutcome needs about one task's outcome for one
// source: bundled so the function takes an accumulator and a task rather
// than a run of scalar parameters.
type taskScore struct {
	src     string
	high    int
	flagged bool
}

// scoreOutcome folds one task's outcome into an accumulator's per-source
// pass/escape/flag/unconfirmed-flag counts.
func scoreOutcome(a *tmAcc, t *task, s taskScore) {
	o, ok := a.out[s.src]
	if !ok {
		o = &[4]int{}
		a.out[s.src] = o
	}
	pass := t.snap.PostVerdict == "pass"
	if pass {
		o[0]++
	}
	if pass && s.high > 0 {
		o[1]++
	}
	if s.flagged {
		o[2]++
	}
	if s.flagged && s.high == 0 {
		o[3]++
	}
}

// scoreRunOutcomes scores c.r's finished tasks against every source that
// reviewed it. A run with no outcome for a source contributes nothing to
// that source's counts.
func (c *tmCtx) scoreRunOutcomes() {
	for _, src := range Sources {
		o, ok := c.r.outcomes[src]
		if !ok {
			continue
		}
		for _, t := range c.r.tasks {
			c.scoreTaskOutcome(src, o, t)
		}
	}
}

// scoreTaskOutcome scores one task, for one source, against every tool that
// touched it: the tool's own reviewing model for validate_task_spec and
// validate_completion, every model that ever checkpointed it for
// check_progress, and the run's plan model for validate_plan.
func (c *tmCtx) scoreTaskOutcome(src string, o OutcomeLine, t *task) {
	if t.snap.PostVerdict == "" {
		return
	}
	high, _ := outcomeCounts(o, t.snap.Index)
	c.scoreDirectCalls(src, t, high)
	c.scoreCheckpoints(src, t, high)
	c.scorePlanCall(src, t, high)
}

// scoreDirectCalls scores t's own validate_task_spec and validate_completion
// calls, each against the model that last called it.
func (c *tmCtx) scoreDirectCalls(src string, t *task, high int) {
	for _, tool := range []string{"validate_task_spec", "validate_completion"} {
		if call, ok := t.latestCall(tool); ok {
			scoreOutcome(c.acc(tool, call.Model), t, taskScore{src: src, high: high, flagged: isFlag(call.Verdict)})
		}
	}
}

// scoreCheckpoints scores every model that ever ran check_progress on t: a
// model is flagged for the task if any of its checkpoints flagged it, even
// when a later checkpoint from the same model did not.
func (c *tmCtx) scoreCheckpoints(src string, t *task, high int) {
	checkpointFlag := map[string]bool{}
	for _, call := range t.snap.Calls {
		if call.Tool == "check_progress" {
			checkpointFlag[call.Model] = checkpointFlag[call.Model] || isFlag(call.Verdict)
		}
	}
	for model, flagged := range checkpointFlag {
		scoreOutcome(c.acc("check_progress", model), t, taskScore{src: src, high: high, flagged: flagged})
	}
}

// scorePlanCall scores the run's validate_plan call against t: the whole run
// shares one plan verdict, so every task in it scores the same plan call.
func (c *tmCtx) scorePlanCall(src string, t *task, high int) {
	if c.r.header != nil && c.r.header.PlanCall != nil {
		scoreOutcome(c.acc("validate_plan", c.r.header.PlanCall.Model), t, taskScore{src: src, high: high, flagged: isFlag(c.r.header.PlanVerdict)})
	}
}

// buildToolModelRows renders every accumulator into its row.
func buildToolModelRows(accs map[tmKey]*tmAcc, runs map[runKey]*run) []ToolModelRow {
	rows := make([]ToolModelRow, 0, len(accs))
	for k, a := range accs {
		rows = append(rows, buildToolModelRow(k, a, runs))
	}
	return rows
}

// planTaskCount sums the plan task count of every run a's calls came from.
// validate_plan's Tasks column uses this instead of len(a.tasks): a plan call
// is scored once per run, not once per task like every other tool.
func planTaskCount(a *tmAcc, runs map[runKey]*run) int {
	n := 0
	for rk := range a.runs {
		if h := runs[rk].header; h != nil {
			n += h.TaskCount
		}
	}
	return n
}

// toolModelOutcomes renders a's per-source counts into a row's Outcomes.
func toolModelOutcomes(a *tmAcc) []ToolModelOutcome {
	var out []ToolModelOutcome
	for _, src := range Sources {
		if o, ok := a.out[src]; ok {
			out = append(out, ToolModelOutcome{Source: src, EscapeRate: wilson(o[1], o[0]), UnconfirmedFlagRate: wilson(o[3], o[2])})
		}
	}
	return out
}

// buildToolModelRow renders one accumulator into its row.
func buildToolModelRow(k tmKey, a *tmAcc, runs map[runKey]*run) ToolModelRow {
	row := ToolModelRow{
		Tool: k.tool, Model: k.model, Publisher: k.publisher,
		Calls: a.calls, Runs: len(a.runs), Tasks: len(a.tasks),
		VerdictCounts: a.verdicts,
		MSP50:         percentile(a.ms, 50), MSP95: percentile(a.ms, 95),
	}
	if k.tool == "validate_plan" {
		row.Tasks = planTaskCount(a, runs)
	}
	if a.calls > 0 {
		row.FindingsPerCall = float64(a.findings) / float64(a.calls)
		row.PartialRate = float64(a.partial) / float64(a.calls)
	}
	row.Outcomes = toolModelOutcomes(a)
	return row
}

// sortToolModelRows orders rows by ToolOrder, then model, then publisher.
func sortToolModelRows(rows []ToolModelRow) {
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
