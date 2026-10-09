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
	// PlanRunID is server-set, never reviewer-emitted: it is absent from
	// plan_schema.json, exactly like SummaryBlock. Controllers thread it into
	// each validate_task_spec call so plan_run_report can assemble the run.
	PlanRunID string `json:"plan_run_id,omitempty"`
	// WaivedFindings holds the plan-level findings a controller ruling
	// covered. Server-set, like PlanRunID.
	WaivedFindings []WaivedFinding `json:"waived_findings,omitempty"`
	// CodebaseReferenceChecklist lists, one entry per affected task, the
	// codebase references the reviewer could not verify. Server-set. It is a
	// to-do list for the controller, not a list of findings, and does not
	// count toward PlanVerdict.
	CodebaseReferenceChecklist []string `json:"codebase_reference_checklist,omitempty"`
	// ReviewScope reports what this round sent to the reviewer. Server-set;
	// absent on a response that reviewed nothing in full, such as a rejected
	// or truncated call.
	ReviewScope *PlanReviewScope `json:"review_scope,omitempty"`
}

// PlanReviewScope is how much of a plan one validate_plan round reviewed. A
// round that names an earlier round's plan_run_id reviews only the tasks
// whose text changed and carries the rest.
type PlanReviewScope struct {
	// Revision numbers the rounds on the plan run, starting at 1.
	Revision int `json:"revision"`
	// TasksReviewed is how many tasks were sent to the reviewer this round,
	// and TasksCarried how many kept the earlier round's result.
	TasksReviewed int `json:"tasks_reviewed"`
	TasksCarried  int `json:"tasks_carried"`
	// PlanLevelReviewed is true when the plan-level pass ran this round.
	PlanLevelReviewed bool `json:"plan_level_reviewed"`
}

// PlanTaskResult is the per-task analysis carried inside PlanResult.Tasks.
type PlanTaskResult struct {
	TaskIndex             int       `json:"task_index"`
	TaskTitle             string    `json:"task_title"`
	Verdict               Verdict   `json:"verdict"`
	Findings              []Finding `json:"findings"`
	SuggestedHeaderBlock  string    `json:"suggested_header_block"`
	SuggestedHeaderReason string    `json:"suggested_header_reason"`
	LightweightEligible   bool      `json:"lightweight_eligible,omitempty"`
	LightweightReason     string    `json:"lightweight_reason,omitempty"`
	ExitContracts         []string  `json:"exit_contracts,omitempty"`
	ExitContractsInferred bool      `json:"exit_contracts_inferred,omitempty"`
	NormativeTestBodies   []string  `json:"normative_test_bodies,omitempty"`
	// WaivedFindings holds this task's findings a controller ruling
	// covered. Server-set.
	WaivedFindings []WaivedFinding `json:"waived_findings,omitempty"`
	// TaskKind, Rung and PlanKind are server-set from the plan's
	// **Kind:**, **Rung:** and **Plan kind:** headers, for the controller to
	// pass to the task's per-task calls.
	TaskKind string `json:"task_kind,omitempty"`
	Rung     string `json:"rung,omitempty"`
	PlanKind string `json:"plan_kind,omitempty"`
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
