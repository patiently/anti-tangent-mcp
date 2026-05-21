package verdict

import _ "embed"

type ProposalAction string

const (
	ProposalActionCreate    ProposalAction = "create"
	ProposalActionUpdate    ProposalAction = "update"
	ProposalActionSupersede ProposalAction = "supersede"
)

type ProposalType string

const (
	ProposalTypeDecision ProposalType = "decision"
	ProposalTypeModule   ProposalType = "module"
	ProposalTypeFeature  ProposalType = "feature"
	ProposalTypeGlossary ProposalType = "glossary"
	ProposalTypeEpic     ProposalType = "epic"
)

// Proposal is the canonical shape of one extract proposal. All fields use
// non-omitempty JSON tags because the reviewer-output schema requires every
// property to appear in the response (strict-mode invariant). The reviewer
// emits placeholders (`""` / `[]`) when a field is unused; the parser runs
// the action-conditional checks against those placeholders.
//
// FrontmatterJSON is a JSON-encoded string (NOT a nested object) — OpenAI
// strict structured-outputs rejects freeform object schemas, and note
// frontmatter has variable per-type keys (decision uses `decided_at`,
// `supersedes`, etc.; module uses `last_changed_in`, `relates_features`;
// epic uses `owners`, `plan_refs`) we cannot enumerate in the schema.
// Callers parse FrontmatterJSON via `json.Unmarshal` after receiving the
// envelope; see Task 10's handler for the parse-and-route path.
type Proposal struct {
	Action          ProposalAction `json:"action"`
	Type            ProposalType   `json:"type"`
	Permalink       string         `json:"permalink"`
	Title           string         `json:"title"`
	FrontmatterJSON string         `json:"frontmatter_json"`
	Body            string         `json:"body"`
	BodyPatch       string         `json:"body_patch"`
	Rationale       string         `json:"rationale"`
	EvidenceRefs    []string       `json:"evidence_refs"`
	Supersedes      []string       `json:"supersedes"`
}

// ExtractResult is the canonical shape returned by extract_project_knowledge.
// BMCommands is required-but-can-be-empty (OpenAI strict-mode schema
// invariant; see internal/verdict/schema_invariants_test.go).
//
// SummaryBlock and Partial are SERVER-OWNED — same posture as PrimeResult.
// ParseExtract decodes into the wire-only `extractWire` struct below to
// reject any reviewer-emitted server-owned field.
type ExtractResult struct {
	Verdict      Verdict     `json:"verdict"`
	Findings     []Finding   `json:"findings"`
	Proposals    []Proposal  `json:"proposals"`
	BMCommands   []BMCommand `json:"bm_commands"`
	NextAction   string      `json:"next_action"`
	SummaryBlock string      `json:"summary_block,omitempty"`
	Partial      bool        `json:"partial,omitempty"`
}

// extractWire is the reviewer-emitted shape — no server-owned fields, and
// per-proposal optional strings use *string so the parser can distinguish
// "field missing" from "field present-but-empty". The strict-mode schema
// requires `body`, `body_patch`, `frontmatter_json`, and `supersedes` to be
// present in every proposal (with empty placeholders when unused). Decoding
// `body`/`body_patch` directly into a plain string would lose that
// distinction — both missing and present-as-"" decode to "", and the parser
// could not reject an action=supersede proposal that omitted body/body_patch
// entirely (which the schema requires) versus one that legitimately emitted
// "" placeholders.
//
// ParseExtract decodes into extractWire with DisallowUnknownFields, then
// converts proposalWire entries into Proposal values after presence checks.
type extractWire struct {
	Verdict    Verdict        `json:"verdict"`
	Findings   []Finding      `json:"findings"`
	Proposals  []proposalWire `json:"proposals"`
	BMCommands []BMCommand    `json:"bm_commands"`
	NextAction string         `json:"next_action"`
}

// proposalWire mirrors Proposal but with *string for the four
// required-but-can-be-empty placeholders, so the parser can detect a
// missing field (pointer == nil) versus a present-but-empty one
// (pointer != nil, *pointer == "").
type proposalWire struct {
	Action          ProposalAction `json:"action"`
	Type            ProposalType   `json:"type"`
	Permalink       string         `json:"permalink"`
	Title           string         `json:"title"`
	FrontmatterJSON *string        `json:"frontmatter_json"`
	Body            *string        `json:"body"`
	BodyPatch       *string        `json:"body_patch"`
	Rationale       string         `json:"rationale"`
	EvidenceRefs    []string       `json:"evidence_refs"`
	Supersedes      []string       `json:"supersedes"`
}

//go:embed extract_schema.json
var extractSchema []byte

// ExtractSchema returns a defensive byte copy of the extract JSON schema.
func ExtractSchema() []byte {
	out := make([]byte, len(extractSchema))
	copy(out, extractSchema)
	return out
}
