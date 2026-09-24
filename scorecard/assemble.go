package scorecard

import "time"

type runKey struct{ publisher, hash string }

type task struct {
	snap        TaskSnapshot
	ts          time.Time
	version     string
	everFlagged bool
}

type run struct {
	key      runKey
	header   *RunLine
	tasks    map[int]*task
	outcomes map[string]OutcomeLine
}

func isFlag(v string) bool { return v == "warn" || v == "fail" }

// assemble folds snapshot and outcome lines into runs. For each task the
// latest snapshot wins (a later line wins a timestamp tie); everFlagged
// remembers whether any snapshot ended warn/fail, which is what separates a
// catch that was fixed from a task that simply passed. For each source the
// latest outcome wins, so a re-reviewed run replaces its earlier outcome.
// publisher, when non-empty, keeps only that publisher's records.
func assemble(lines []RunLine, outcomes []OutcomeLine, publisher string) map[runKey]*run {
	runs := map[runKey]*run{}
	get := func(k runKey) *run {
		r, ok := runs[k]
		if !ok {
			r = &run{key: k, tasks: map[int]*task{}, outcomes: map[string]OutcomeLine{}}
			runs[k] = r
		}
		return r
	}
	for i := range lines {
		l := lines[i]
		if publisher != "" && l.Publisher != publisher {
			continue
		}
		r := get(runKey{l.Publisher, l.RunHash})
		if l.Header {
			if r.header == nil || !l.Ts.Before(r.header.Ts) {
				h := l
				r.header = &h
			}
			continue
		}
		if l.Task == nil {
			continue
		}
		t, ok := r.tasks[l.Task.Index]
		flagged := isFlag(l.Task.PostVerdict)
		if !ok {
			r.tasks[l.Task.Index] = &task{snap: *l.Task, ts: l.Ts, version: l.ServerVersion, everFlagged: flagged}
			continue
		}
		t.everFlagged = t.everFlagged || flagged
		if !l.Ts.Before(t.ts) {
			t.snap, t.ts, t.version = *l.Task, l.Ts, l.ServerVersion
		}
	}
	for _, o := range outcomes {
		if publisher != "" && o.Publisher != publisher {
			continue
		}
		if !ValidSource(o.Source) {
			continue
		}
		r := get(runKey{o.Publisher, o.RunHash})
		if prev, ok := r.outcomes[o.Source]; !ok || !o.Ts.Before(prev.Ts) {
			r.outcomes[o.Source] = o
		}
	}
	return runs
}

// latestCall returns the task's most recent call of tool.
func (t *task) latestCall(tool string) (ToolCall, bool) {
	for i := len(t.snap.Calls) - 1; i >= 0; i-- {
		if t.snap.Calls[i].Tool == tool {
			return t.snap.Calls[i], true
		}
	}
	return ToolCall{}, false
}

func (t *task) reviewModel() string {
	if c, ok := t.latestCall("validate_completion"); ok && c.Model != "" {
		return c.Model
	}
	return "unknown"
}

func (t *task) serverVersion() string {
	if t.version == "" {
		return "unknown"
	}
	return t.version
}

// implementerModel prefers the final review's report, because the controller
// that files it is the one that dispatched each task.
func (r *run) implementerModel(index int) string {
	for _, src := range Sources {
		for _, m := range r.outcomes[src].ImplementerModels {
			if m.TaskIndex == index && m.Model != "" {
				return m.Model
			}
		}
	}
	return "unknown"
}

// outcomeCounts returns how many critical/major and how many minor outcome
// findings o attributes to task index.
func outcomeCounts(o OutcomeLine, index int) (high, minor int) {
	for _, f := range o.Findings {
		if f.TaskIndex != index {
			continue
		}
		switch f.Severity {
		case "critical", "major":
			high++
		case "minor":
			minor++
		}
	}
	return high, minor
}
