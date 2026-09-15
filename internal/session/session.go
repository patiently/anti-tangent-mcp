// Package session defines the per-task session structures and an in-memory
// store with TTL eviction.
package session

import (
	"time"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

// HarnessShapeAttestation declares a caller-attested shape fact about a test
// harness or fixture referenced in a task spec. The reviewer treats each
// attestation as authoritative context (no independent verification) and
// flags ACs that explicitly contradict an entry as
// `attestation_contradiction` findings. See docs/protocol/authoring.md §3
// for the use case.
//
// JSON tags pin the caller-visible field names. Without them, reflection on
// ValidateTaskSpecArgs would surface capitalized Go names (Harness, Path,
// Assertions) in the generated MCP input schema instead of the documented
// lowercase ones.
type HarnessShapeAttestation struct {
	Harness    string   `json:"harness" jsonschema:"Name of the test harness or fixture. At most 240 characters."`
	Path       string   `json:"path" jsonschema:"Path of the harness or fixture file. At most 240 characters."`
	Assertions []string `json:"assertions" jsonschema:"Facts about the harness's shape that the caller attests to. At most 10 entries of at most 480 characters each."`
}

type TaskSpec struct {
	Title                        string                    `json:"title"`
	Goal                         string                    `json:"goal"`
	AcceptanceCriteria           []string                  `json:"acceptance_criteria,omitempty"`
	NonGoals                     []string                  `json:"non_goals,omitempty"`
	Context                      string                    `json:"context,omitempty"`
	PinnedBy                     []string                  `json:"pinned_by,omitempty"`
	ControllerVerifiedReferences []string                  `json:"controller_verified_references,omitempty"`
	TestStrategyNotes            []string                  `json:"test_strategy_notes,omitempty"`
	CodebaseConventions          []string                  `json:"codebase_conventions,omitempty"`
	TestabilityExtractions       []string                  `json:"testability_extractions,omitempty"`
	NormativeTestBodies          []string                  `json:"normative_test_bodies,omitempty"`
	HarnessShapeAttestations     []HarnessShapeAttestation `json:"harness_shape_attestations,omitempty"`
	Phase                        string                    `json:"phase,omitempty"`
}

type ModelDefaults struct {
	Pre, Mid, Post config.ModelRef
}

type Checkpoint struct {
	At        time.Time         `json:"at"`
	WorkingOn string            `json:"working_on"`
	FileCount int               `json:"file_count"`
	Verdict   verdict.Verdict   `json:"verdict"`
	Findings  []verdict.Finding `json:"findings,omitempty"`
}

// MaxRulings is how many ruled fingerprints one session keeps. A ruling on a
// new fingerprint past that is dropped, so a caller cannot grow a session
// without bound by ruling on every finding it is shown.
const MaxRulings = 50

// Ruling is a controller's decision on a finding. A session stores it under
// the finding's fingerprint, and it covers every later finding with that
// fingerprint. Category and Criterion describe the ruled finding when the
// session still held it at ruling time, so a reviewer prompt can say what was
// ruled on; both are empty otherwise.
type Ruling struct {
	ID        string
	Category  verdict.Category
	Criterion string
	Text      string
}

type Session struct {
	ID           string
	CreatedAt    time.Time
	LastAccessed time.Time
	Spec         TaskSpec
	PreFindings  []verdict.Finding
	Checkpoints  []Checkpoint
	// PostFindings is the reviewer's own findings from the most recent
	// validate_completion whose review completed, with the IDs that response
	// showed and without the findings a ruling waived. The next
	// validate_completion shows them to the reviewer as prior findings.
	PostFindings  []verdict.Finding
	ModelDefaults ModelDefaults
	// PlanRunID ties this session to a plan_run_id minted by validate_plan, so
	// check_progress and validate_completion can update the right planrun row
	// without new arguments. Empty for tasks not dispatched under a plan run.
	PlanRunID string
	// IssuedIDs is every finding ID a response on this session carried, from
	// any of its tools. A controller ruling must name one of them.
	IssuedIDs map[string]bool
	// Rulings holds the controller's rulings, keyed by fingerprint.
	Rulings map[string]Ruling
	// Escalated is set once any validate_completion on the session escalates,
	// and never cleared.
	Escalated bool
}
