package verdict

import (
	"strings"
	"testing"
)

func TestParse_UnverifiableCodebaseClaim_SeverityFloorToMinor(t *testing.T) {
	// Reviewer emits a `major` unverifiable_codebase_claim — server should
	// silently floor to `minor` because the reviewer doesn't know if the
	// claim is wrong, only that it can't check.
	raw := []byte(`{
		"verdict": "warn",
		"findings": [{
			"severity":   "major",
			"category":   "unverifiable_codebase_claim",
			"criterion":  "plan-stated fact",
			"evidence":   "plan says: uses field StateMachineOutput.currentState",
			"suggestion": "verify against the actual code before dispatching"
		}],
		"next_action": "Address the warnings before dispatching."
	}`)

	r, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if len(r.Findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(r.Findings))
	}
	if got, want := r.Findings[0].Severity, SeverityMinor; got != want {
		t.Errorf("severity = %q, want %q (server should floor)", got, want)
	}
	if got, want := r.Findings[0].Category, CategoryUnverifiableCodebaseClaim; got != want {
		t.Errorf("category = %q, want %q", got, want)
	}
}

func TestParse_MalformedEvidenceCategory_RejectedFromReviewerOutput(t *testing.T) {
	// malformed_evidence is emitted ONLY by the server-side validate_completion
	// guard, never by the reviewer. If a reviewer somehow emits it, the
	// parser must reject it as an invalid category.
	raw := []byte(`{
		"verdict": "fail",
		"findings": [{
			"severity":   "major",
			"category":   "malformed_evidence",
			"criterion":  "evidence_shape",
			"evidence":   "final_diff contains (truncated) at offset 142",
			"suggestion": "Submit full file contents."
		}],
		"next_action": "Re-submit with complete evidence."
	}`)
	_, err := Parse(raw)
	if err == nil {
		t.Fatalf("Parse should reject reviewer-emitted malformed_evidence (server-only category)")
	}
	if !strings.Contains(err.Error(), "category") {
		t.Errorf("error should mention invalid category; got %v", err)
	}
}
