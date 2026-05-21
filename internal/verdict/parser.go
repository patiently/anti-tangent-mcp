package verdict

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Parse decodes provider output into a Result and validates enum fields.
// It tolerates a ```json ... ``` wrapper and surrounding whitespace.
func Parse(raw []byte) (Result, error) {
	body := stripFences(bytes.TrimSpace(raw))
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var r Result
	if err := dec.Decode(&r); err != nil {
		return Result{}, fmt.Errorf("decode result: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return Result{}, fmt.Errorf("decode result: extra JSON after document")
	}
	switch r.Verdict {
	case VerdictPass, VerdictWarn, VerdictFail:
	default:
		return Result{}, fmt.Errorf("invalid verdict %q", r.Verdict)
	}
	if r.NextAction == "" {
		return Result{}, fmt.Errorf("decode result: next_action is required")
	}
	for i := range r.Findings {
		f := r.Findings[i]
		switch f.Severity {
		case SeverityCritical, SeverityMajor, SeverityMinor:
		default:
			return Result{}, fmt.Errorf("finding[%d]: invalid severity %q", i, f.Severity)
		}
		if !validCategory(f.Category) {
			return Result{}, fmt.Errorf("finding[%d]: invalid category %q", i, f.Category)
		}
		// Severity floor: see applySeverityFloor in parser_partial.go for the
		// list of categories that get capped at minor (the reviewer can't
		// know if the claim/deviation is wrong).
		r.Findings[i] = applySeverityFloor(r.Findings[i])
		if err := validateFindingStrings(r.Findings[i], fmt.Sprintf("finding[%d]", i)); err != nil {
			return Result{}, err
		}
	}
	return r, nil
}

// validCategory recognizes every category a reviewer is allowed to emit.
// CategoryMalformedEvidence is intentionally absent: it is server-only,
// emitted by the validate_completion evidence-shape guard which builds
// the envelope directly (never round-tripping through Parse()).
func validCategory(c Category) bool {
	switch c {
	case CategoryMissingAC, CategoryScopeDrift, CategoryAmbiguousSpec,
		CategoryUnaddressed, CategoryQuality, CategorySessionMissing,
		CategoryTooLarge, CategoryUnverifiableCodebaseClaim,
		CategoryConventionDeviation, CategoryAttestationContradiction,
		CategoryKBGap, CategoryAmbiguousPick, CategoryMissingIndexEntry,
		CategoryInsufficientEvidence, CategoryRedundantProposal, CategoryContradictsExisting,
		CategoryOther:
		return true
	}
	return false
}

// validateFindingStrings rejects findings whose criterion/evidence/suggestion
// are empty strings. The reviewer-output JSON schemas enforce this via
// minLength:1 today, but the parser is the durable enforcement point and
// the schemas are belt-and-braces. Called from Parse and from
// validateFinding (used by ParsePlan / ParseTasksOnly); ParsePrime and
// ParseExtract (v0.6.0) will call this from their own loops.
func validateFindingStrings(f Finding, where string) error {
	if f.Criterion == "" {
		return fmt.Errorf("%s: criterion is required", where)
	}
	if f.Evidence == "" {
		return fmt.Errorf("%s: evidence is required", where)
	}
	if f.Suggestion == "" {
		return fmt.Errorf("%s: suggestion is required", where)
	}
	return nil
}

func stripFences(b []byte) []byte {
	s := string(b)
	if !strings.HasPrefix(s, "```") {
		return b
	}
	// strip leading fence (with optional language tag)
	if nl := strings.IndexByte(s, '\n'); nl != -1 {
		s = s[nl+1:]
	}
	s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	return []byte(strings.TrimSpace(s))
}

// RetryHint is the system-side instruction we append when reissuing
// the prompt after a failed parse.
func RetryHint() string {
	return "Your previous response was not valid JSON matching the schema. " +
		"Respond with ONLY the JSON object, no commentary, no code fences."
}
