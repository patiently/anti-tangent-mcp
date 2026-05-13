package verdict

import _ "embed"

//go:embed plan_schema.json
var planSchema []byte

// PlanSchema returns a defensive byte copy of the plan-level JSON schema.
// Providers are instructed to produce output matching this shape.
func PlanSchema() []byte {
	out := make([]byte, len(planSchema))
	copy(out, planSchema)
	return out
}

//go:embed plan_findings_only_schema.json
var planFindingsOnlySchema []byte

// PlanFindingsOnlySchema returns a defensive byte copy of the plan-findings-only
// JSON schema (used by validate_plan's chunking fallback Pass 1).
func PlanFindingsOnlySchema() []byte {
	out := make([]byte, len(planFindingsOnlySchema))
	copy(out, planFindingsOnlySchema)
	return out
}

// PlanQuality is a separate axis from PlanVerdict, indicating how close
// the plan is to ship-ready independent of whether it's dispatchable.
//
//	rough      — implementer cannot start; missing pieces / contradictions.
//	actionable — dispatchable but with gaps an implementer might have to
//	             ask about; some quality issues that risk rework.
//	rigorous   — ready to hand to a fresh implementer with high confidence;
//	             remaining findings are stylistic.
type PlanQuality string

const (
	PlanQualityRough      PlanQuality = "rough"
	PlanQualityActionable PlanQuality = "actionable"
	PlanQualityRigorous   PlanQuality = "rigorous"
)

// PlanFindingsOnly is the Pass-1 response shape during chunked plan review.
// Carries cross-cutting findings and next_action; no per-task data.
type PlanFindingsOnly struct {
	PlanVerdict  Verdict     `json:"plan_verdict"`
	PlanFindings []Finding   `json:"plan_findings"`
	NextAction   string      `json:"next_action"`
	PlanQuality  PlanQuality `json:"plan_quality"`
}

// PlanResult is the canonical shape returned by validate_plan.
type PlanResult struct {
	PlanVerdict  Verdict          `json:"plan_verdict"`
	PlanFindings []Finding        `json:"plan_findings"`
	Tasks        []PlanTaskResult `json:"tasks"`
	NextAction   string           `json:"next_action"`
	PlanQuality  PlanQuality      `json:"plan_quality"`
	SummaryBlock string           `json:"summary_block,omitempty"`
	Partial      bool             `json:"partial,omitempty"`
}

// PlanTaskResult is the per-task analysis carried inside PlanResult.Tasks.
type PlanTaskResult struct {
	TaskIndex             int       `json:"task_index"`
	TaskTitle             string    `json:"task_title"`
	Verdict               Verdict   `json:"verdict"`
	Findings              []Finding `json:"findings"`
	SuggestedHeaderBlock  string    `json:"suggested_header_block"`
	SuggestedHeaderReason string    `json:"suggested_header_reason"`
}

//go:embed tasks_only_schema.json
var tasksOnlySchema []byte

// TasksOnlySchema returns a defensive byte copy of the per-chunk reviewer
// response schema (used by validate_plan's chunking fallback Passes 2..K+1).
func TasksOnlySchema() []byte {
	out := make([]byte, len(tasksOnlySchema))
	copy(out, tasksOnlySchema)
	return out
}

// TasksOnly is the per-chunk response shape during chunked plan review.
type TasksOnly struct {
	Tasks []PlanTaskResult `json:"tasks"`
}
