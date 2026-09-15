package mcpsrv

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

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

// assignPlanIDs gives every finding and waived entry of a validate_plan
// response its display ID: plan-level findings, each task's findings in
// order, then the plan-level and each task's waived entries.
func assignPlanIDs(pr *verdict.PlanResult) {
	a := verdict.NewIDAssigner()
	a.Assign(pr.PlanFindings, "")
	for i := range pr.Tasks {
		a.Assign(pr.Tasks[i].Findings, planTaskKey(pr.Tasks[i].TaskTitle))
	}
	a.AssignWaived(pr.WaivedFindings, "")
	for i := range pr.Tasks {
		a.AssignWaived(pr.Tasks[i].WaivedFindings, planTaskKey(pr.Tasks[i].TaskTitle))
	}
}
