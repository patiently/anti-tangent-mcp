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
	// CategoryConventionDeviation is emitted by the reviewer when a caller-
	// supplied codebase_conventions entry conflicts with the spec text — for
	// example, when the spec implies a type or identifier choice that
	// contradicts a stated module convention. Parser-side severity floor (see
	// Parse / validateFinding) forces these findings to SeverityMinor — the
	// reviewer can't know whether the implementation will actually deviate,
	// only that the spec suggests it might.
	CategoryConventionDeviation Category = "convention_deviation"
	// CategoryAttestationContradiction is emitted by the reviewer when an
	// acceptance criterion explicitly contradicts a caller-attested harness
	// shape (see HarnessShapeAttestation on TaskSpec). Distinct from
	// convention_deviation: attestations are caller-attested shape facts, so
	// a reviewer-detected contradiction is a hard finding, not "can't
	// verify." Intentionally NOT in applySeverityFloor's list — the
	// reviewer's chosen severity (typically major) is preserved.
	CategoryAttestationContradiction Category = "attestation_contradiction"
	// CategoryContradictedCodebaseClaim is emitted by the reviewer when a
	// plan statement is refuted by the contents of a file supplied via
	// validate_plan's context_paths. Distinct from
	// unverifiable_codebase_claim: an attached file is ground truth read
	// from disk by the server, so a contradiction is a hard finding, not
	// "can't verify." Distinct from attestation_contradiction, which is a
	// conflict with a caller-ASSERTED harness shape rather than with bytes
	// the server read. Intentionally NOT in applySeverityFloor's list — the
	// reviewer's chosen severity (typically major) is preserved. Only valid
	// for claims about files that were actually attached; the ground rules
	// forbid emitting it about anything outside the attached set.
	CategoryContradictedCodebaseClaim Category = "contradicted_codebase_claim"
	// CategoryMalformedEvidence is server-only. It is emitted exclusively
	// by the validate_completion evidence-shape guard, which constructs
	// the envelope directly without round-tripping through Parse(). It is
	// intentionally NOT included in validCategory and NOT included in any
	// JSON schema, so a reviewer cannot emit it.
	CategoryMalformedEvidence Category = "malformed_evidence"
	// CategoryCodesceneNotRun and CategoryCodesceneSkipped are server-only,
	// like CategoryMalformedEvidence: emitted by the validate_completion
	// CodeScene check when ANTI_TANGENT_CODESCENE=required, never by a
	// reviewer. Both are intentionally absent from validCategory.
	CategoryCodesceneNotRun  Category = "codescene_not_run"
	CategoryCodesceneSkipped Category = "codescene_skipped"

	// Categories emitted by prime_project_knowledge (v0.6.0).
	CategoryKBGap             Category = "kb_gap"
	CategoryAmbiguousPick     Category = "ambiguous_pick"
	CategoryMissingIndexEntry Category = "missing_index_entry"

	// Categories emitted by extract_project_knowledge (v0.6.0).
	CategoryInsufficientEvidence Category = "insufficient_evidence"
	CategoryRedundantProposal    Category = "redundant_proposal"
	CategoryContradictsExisting  Category = "contradicts_existing"

	CategoryOther Category = "other"
)

type Finding struct {
	// ID is server-assigned: the finding's fingerprint, with a "-n" suffix when
	// an earlier finding in the same response shares it. See Fingerprint.
	ID         string   `json:"id,omitempty" jsonschema:"Server-assigned identifier: f_ and eight hex digits, with a -n suffix when an earlier finding in the same response shares them."`
	Severity   Severity `json:"severity" jsonschema:"critical, major or minor."`
	Category   Category `json:"category" jsonschema:"The finding's category, such as missing_acceptance_criterion or scope_drift."`
	Criterion  string   `json:"criterion" jsonschema:"The acceptance criterion or spec field the finding is about."`
	Evidence   string   `json:"evidence" jsonschema:"What the reviewer saw that supports the finding."`
	Suggestion string   `json:"suggestion" jsonschema:"The concrete next action that would resolve the finding."`
	// RepeatOf is server-set on validate_completion: the ID of a prior finding
	// the implementer answered and the reviewer raised again.
	RepeatOf string `json:"repeat_of,omitempty" jsonschema:"Server-set: the id of an earlier finding the implementer answered that this finding raises again."`
	// SameAs is the reviewer's claim that this finding raises again one its
	// prompt showed. The server reads it and clears it before responding.
	SameAs *string `json:"same_as,omitempty" jsonschema:"Reviewer-set: the id of an earlier finding shown in the prompt that this finding raises again, or null."`
}

// WaivedFinding is a reviewer finding a controller ruling covered. It does not
// count toward the verdict and is reported with the ruling that waived it, so
// the controller reading the summary block sees what each ruling covered.
type WaivedFinding struct {
	ID        string   `json:"id"`
	Severity  Severity `json:"severity"`
	Category  Category `json:"category"`
	Criterion string   `json:"criterion"`
	Evidence  string   `json:"evidence"`
	Ruling    string   `json:"ruling"`
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
