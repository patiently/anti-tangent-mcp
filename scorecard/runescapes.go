package scorecard

import "sort"

type Escape struct {
	TaskIndex          int    `json:"task_index"`
	AntiTangentVerdict string `json:"anti_tangent_verdict"`
	OutcomeSeverity    string `json:"outcome_severity"`
}

// RunEscapes scores one run's snapshot lines against one outcome: the tasks
// anti-tangent passed that the outcome found a critical or major problem in,
// how many tasks had a final verdict, and whether any snapshot exists.
func RunEscapes(lines []RunLine, o OutcomeLine) (escapes []Escape, tasksScored int, known bool) {
	escapes = []Escape{}
	if len(lines) == 0 {
		return escapes, 0, false
	}
	runs := assemble(lines, nil, "")
	for _, r := range runs {
		for _, t := range r.tasks {
			if t.snap.PostVerdict == "" {
				continue
			}
			tasksScored++
			if t.snap.PostVerdict != "pass" {
				continue
			}
			worst := ""
			for _, f := range o.Findings {
				if f.TaskIndex != t.snap.Index {
					continue
				}
				if f.Severity == "critical" || (f.Severity == "major" && worst == "") {
					worst = f.Severity
				}
			}
			if worst != "" {
				escapes = append(escapes, Escape{TaskIndex: t.snap.Index, AntiTangentVerdict: "pass", OutcomeSeverity: worst})
			}
		}
	}
	sort.Slice(escapes, func(i, j int) bool { return escapes[i].TaskIndex < escapes[j].TaskIndex })
	return escapes, tasksScored, true
}
