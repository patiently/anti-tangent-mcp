package verdict

import _ "embed"

// Pick is one note recommendation produced by prime_project_knowledge.
type Pick struct {
	Permalink string   `json:"permalink"`
	Reason    string   `json:"reason"`
	Priority  Severity `json:"priority"`
}

// BMCommand is a paste-ready Basic Memory client call. Emitted only when
// the server is configured with ANTI_TANGENT_KB_STORE=basic-memory.
// ArgsJSON is the BM tool's args object encoded as a JSON string — OpenAI
// strict structured-outputs rejects freeform `object` schemas, so the wire
// format flattens args to a string and callers parse it on receipt.
type BMCommand struct {
	Tool     string `json:"tool"`
	ArgsJSON string `json:"args_json"`
}

// PrimeResult is the canonical shape returned by prime_project_knowledge.
// BMCommands is required-but-can-be-empty (OpenAI strict-mode schema
// invariant; see internal/verdict/schema_invariants_test.go).
//
// SummaryBlock and Partial are SERVER-OWNED — they are populated by the
// handler, never by the reviewer. To stop a non-OpenAI reviewer (Anthropic,
// Google, etc., which do not enforce strict-mode and so cannot block
// extra fields at the provider boundary) from spoofing these, the parser
// decodes into a separate wire-only struct that excludes them — see
// primeWire below and ParsePrime's implementation in prime_parser.go.
type PrimeResult struct {
	Verdict      Verdict     `json:"verdict"`
	Findings     []Finding   `json:"findings"`
	Picks        []Pick      `json:"picks"`
	BMCommands   []BMCommand `json:"bm_commands"`
	NextAction   string      `json:"next_action"`
	SummaryBlock string      `json:"summary_block,omitempty"`
	Partial      bool        `json:"partial,omitempty"`
}

// primeWire is the reviewer-emitted shape (no server-owned fields). The
// parser decodes raw bytes into this with DisallowUnknownFields, then
// the parser lifts the reviewer fields into a PrimeResult with the
// server-owned fields zero-valued. This closes the spoof-vector where a
// non-strict-mode provider could emit a fake `summary_block` or `partial`
// and have it round-trip through the server.
type primeWire struct {
	Verdict    Verdict     `json:"verdict"`
	Findings   []Finding   `json:"findings"`
	Picks      []Pick      `json:"picks"`
	BMCommands []BMCommand `json:"bm_commands"`
	NextAction string      `json:"next_action"`
}

//go:embed prime_schema.json
var primeSchema []byte

// PrimeSchema returns a defensive byte copy of the prime JSON schema.
func PrimeSchema() []byte {
	out := make([]byte, len(primeSchema))
	copy(out, primeSchema)
	return out
}
