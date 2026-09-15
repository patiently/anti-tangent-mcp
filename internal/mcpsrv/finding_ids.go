package mcpsrv

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/patiently/anti-tangent-mcp/internal/planparser"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

// assignEnvelopeIDs gives every finding and waived entry of a session-tool
// response its display ID, findings first, and clears same_as, which is the
// reviewer's claim and is never echoed. Call it once the list is final and
// before the session is written, so the IDs stored are the IDs shown.
func assignEnvelopeIDs(env *Envelope) {
	a := verdict.NewIDAssigner()
	a.Assign(env.Findings, "")
	a.AssignWaived(env.WaivedFindings, "")
	for i := range env.Findings {
		env.Findings[i].SameAs = nil
	}
}

// envelopeIDs lists every display ID a response carries.
func envelopeIDs(env Envelope) []string {
	ids := make([]string, 0, len(env.Findings)+len(env.WaivedFindings))
	for _, f := range env.Findings {
		ids = append(ids, f.ID)
	}
	for _, w := range env.WaivedFindings {
		ids = append(ids, w.ID)
	}
	return ids
}

// rejectionEnvelopeResult renders a response that never reached the reviewer.
// Such a response writes nothing to the session, so its IDs are assigned here
// rather than before a session write. The findings are copied first: a
// malformed-evidence rejection shares its slices with the evidence-rejection
// cache entry, which concurrent calls read and render outside the cache lock.
func rejectionEnvelopeResult(env Envelope) (*mcp.CallToolResult, Envelope, error) {
	env.Findings = append([]verdict.Finding(nil), env.Findings...)
	env.WaivedFindings = append([]verdict.WaivedFinding(nil), env.WaivedFindings...)
	assignEnvelopeIDs(&env)
	return envelopeResult(env)
}

// planTaskKey is the task key a validate_plan task finding is fingerprinted
// with: the title without the "Task N:" prefix that renumbering changes.
func planTaskKey(title string) string {
	return normalizeTaskTitle(title)
}

// planTaskKeys returns the task key for each of pr's task results. The title
// is the plan's own heading for the parsed task the result reports on (see
// parsedTaskIndexes), not the reviewer's task_title: a reviewer that restates
// a title differently from one round to the next would otherwise change every
// fingerprint on that task, and a ruling on one would stop waiving it. A
// result that names no parsed task falls back to the reviewer's task_title.
func planTaskKeys(pr verdict.PlanResult, tasks []planparser.RawTask) []string {
	keys := make([]string, len(pr.Tasks))
	for i, idx := range parsedTaskIndexes(pr.Tasks, tasks) {
		title := pr.Tasks[i].TaskTitle
		if idx >= 0 {
			title = tasks[idx].Title
		}
		keys[i] = planTaskKey(title)
	}
	return keys
}

// assignPlanIDs gives every finding and waived entry of a validate_plan
// response its display ID: plan-level findings, each task's findings in
// order, then the plan-level and each task's waived entries. tasks is the
// parsed plan the task keys come from; see planTaskKeys.
func assignPlanIDs(pr *verdict.PlanResult, tasks []planparser.RawTask) {
	keys := planTaskKeys(*pr, tasks)
	a := verdict.NewIDAssigner()
	a.Assign(pr.PlanFindings, "")
	for i := range pr.Tasks {
		a.Assign(pr.Tasks[i].Findings, keys[i])
	}
	a.AssignWaived(pr.WaivedFindings, "")
	for i := range pr.Tasks {
		a.AssignWaived(pr.Tasks[i].WaivedFindings, keys[i])
	}
}
