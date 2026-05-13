// Package verdict defines the canonical shape of reviewer output and the
// JSON schema used to constrain provider responses.
package verdict

import _ "embed"

type Verdict string

const (
	VerdictPass Verdict = "pass"
	VerdictWarn Verdict = "warn"
	VerdictFail Verdict = "fail"
)

type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityMajor    Severity = "major"
	SeverityMinor    Severity = "minor"
)

type Category string

const (
	CategoryMissingAC      Category = "missing_acceptance_criterion"
	CategoryScopeDrift     Category = "scope_drift"
	CategoryAmbiguousSpec  Category = "ambiguous_spec"
	CategoryUnaddressed    Category = "unaddressed_finding"
	CategoryQuality        Category = "quality"
	CategorySessionMissing Category = "session_not_found"
	CategoryTooLarge       Category = "payload_too_large"
	// CategoryUnverifiableCodebaseClaim is emitted by the reviewer when a
	// plan or task-spec statement asserts a codebase fact (field name,
	// signature, file existence, repo convention) that cannot be verified
	// from text alone. Parser-side severity floor (see Parse) forces these
	// findings to SeverityMinor — the reviewer can't know if the claim is
	// wrong, only that it can't check.
	CategoryUnverifiableCodebaseClaim Category = "unverifiable_codebase_claim"
	// CategoryMalformedEvidence is server-only. It is emitted exclusively
	// by the validate_completion evidence-shape guard, which constructs
	// the envelope directly without round-tripping through Parse(). It is
	// intentionally NOT included in validCategory and NOT included in any
	// JSON schema, so a reviewer cannot emit it.
	CategoryMalformedEvidence Category = "malformed_evidence"
	CategoryOther             Category = "other"
)

type Finding struct {
	Severity   Severity `json:"severity"`
	Category   Category `json:"category"`
	Criterion  string   `json:"criterion"`
	Evidence   string   `json:"evidence"`
	Suggestion string   `json:"suggestion"`
}

type Result struct {
	Verdict    Verdict   `json:"verdict"`
	Findings   []Finding `json:"findings"`
	NextAction string    `json:"next_action"`
	Partial    bool      `json:"partial,omitempty"`
}

//go:embed schema.json
var schema []byte

// Schema returns the JSON Schema (draft-07-compatible subset) describing Result.
// Providers are instructed to produce output matching this shape.
func Schema() []byte {
	out := make([]byte, len(schema))
	copy(out, schema)
	return out
}
