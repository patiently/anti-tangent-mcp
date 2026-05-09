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

// PlanResult is the canonical shape returned by validate_plan.
type PlanResult struct {
	PlanVerdict  Verdict          `json:"plan_verdict"`
	PlanFindings []Finding        `json:"plan_findings"`
	Tasks        []PlanTaskResult `json:"tasks"`
	NextAction   string           `json:"next_action"`
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
