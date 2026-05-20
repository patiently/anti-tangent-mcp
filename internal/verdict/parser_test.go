package verdict

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
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

func TestParse_ConventionDeviation_SeverityFloorToMinor(t *testing.T) {
	// Reviewer emits a `major` convention_deviation — server should floor to
	// `minor`, matching the unverifiable_codebase_claim pattern.
	raw := []byte(`{
		"verdict": "warn",
		"findings": [{
			"severity":   "major",
			"category":   "convention_deviation",
			"criterion":  "codebase_convention",
			"evidence":   "spec serializes the id as String at the in-memory layer",
			"suggestion": "Use UUID in memory; serialize to String only at the persistence boundary."
		}],
		"next_action": "Address the convention before dispatch."
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
	if got, want := r.Findings[0].Category, CategoryConventionDeviation; got != want {
		t.Errorf("category = %q, want %q", got, want)
	}
}

func TestParse_AttestationContradiction_AcceptedAndNotFloored(t *testing.T) {
	raw := []byte(`{
		"verdict":"warn",
		"findings":[{
			"severity":"major",
			"category":"attestation_contradiction",
			"criterion":"records emitted spans",
			"evidence":"AC asserts no spans recorded",
			"suggestion":"Revise AC or harness"
		}],
		"next_action":"address contradiction"
	}`)
	r, err := Parse(raw)
	require.NoError(t, err, "attestation_contradiction must be a valid category")
	require.Len(t, r.Findings, 1)
	require.Equal(t, SeverityMajor, r.Findings[0].Severity, "attestation_contradiction must NOT be floored to minor")
	require.Equal(t, CategoryAttestationContradiction, r.Findings[0].Category)
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

func TestParse_DemotedAmbiguousSpec_PassesThroughUnchanged(t *testing.T) {
	raw := []byte(`{
		"verdict":"warn",
		"findings":[{
			"severity":"minor",
			"category":"ambiguous_spec",
			"criterion":"AC1: emits spans",
			"evidence":"AC reads ambiguous about span count",
			"suggestion":"Pin count in the AC. (resolved-by-normative-body: Test fun emits_spans pins exactly 2 calls)"
		}],
		"next_action":"address as minor"
	}`)
	r, err := Parse(raw)
	require.NoError(t, err)
	require.Len(t, r.Findings, 1)
	require.Equal(t, SeverityMinor, r.Findings[0].Severity, "demoted finding stays minor; parser does not re-derive")
	require.Equal(t, CategoryAmbiguousSpec, r.Findings[0].Category)
	require.Contains(t, r.Findings[0].Suggestion, "(resolved-by-normative-body:")
}

func TestParse_AcceptsProjectKnowledgeCategories(t *testing.T) {
	// Six new categories added in v0.6.0 for prime_project_knowledge and
	// extract_project_knowledge. Asserts each is accepted by Parse when a
	// reviewer emits it on a finding.
	categories := []Category{
		CategoryKBGap,
		CategoryAmbiguousPick,
		CategoryMissingIndexEntry,
		CategoryInsufficientEvidence,
		CategoryRedundantProposal,
		CategoryContradictsExisting,
	}
	for _, c := range categories {
		c := c
		t.Run(string(c), func(t *testing.T) {
			raw := []byte(`{"verdict":"warn","findings":[{"severity":"minor","category":"` + string(c) + `","criterion":"x","evidence":"y","suggestion":"z"}],"next_action":"go"}`)
			r, err := Parse(raw)
			require.NoError(t, err)
			require.Len(t, r.Findings, 1)
			require.Equal(t, c, r.Findings[0].Category)
		})
	}
}

func TestParse_RejectsEmptyFindingStrings(t *testing.T) {
	// Parser-side belt-and-braces enforcement of non-empty criterion /
	// evidence / suggestion. Schemas enforce this via minLength:1 today, but
	// the parser is the durable enforcement point.
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "empty criterion",
			raw:  `{"verdict":"warn","findings":[{"severity":"minor","category":"quality","criterion":"","evidence":"e","suggestion":"s"}],"next_action":"go"}`,
			want: "criterion is required",
		},
		{
			name: "empty evidence",
			raw:  `{"verdict":"warn","findings":[{"severity":"minor","category":"quality","criterion":"c","evidence":"","suggestion":"s"}],"next_action":"go"}`,
			want: "evidence is required",
		},
		{
			name: "empty suggestion",
			raw:  `{"verdict":"warn","findings":[{"severity":"minor","category":"quality","criterion":"c","evidence":"e","suggestion":""}],"next_action":"go"}`,
			want: "suggestion is required",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.raw))
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.want)
		})
	}
}
