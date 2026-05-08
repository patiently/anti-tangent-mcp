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
	CategoryOther          Category = "other"
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
}

//go:embed schema.json
var schema []byte

// Schema returns the JSON Schema (draft-07-compatible subset) describing Result.
// Providers are instructed to produce output matching this shape.
func Schema() []byte { return schema }
