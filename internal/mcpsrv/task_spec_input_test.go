package mcpsrv

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/session"
)

func TestNormalizeHarnessShapeAttestation_HappyPath(t *testing.T) {
	in := []session.HarnessShapeAttestation{
		{Harness: " H ", Path: " p ", Assertions: []string{" a1 ", "a2"}},
	}
	out, err := normalizeHarnessShapeAttestation(in)
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Equal(t, "H", out[0].Harness, "whitespace trimmed")
	require.Equal(t, "p", out[0].Path)
	require.Equal(t, []string{"a1", "a2"}, out[0].Assertions)
}

func TestNormalizeHarnessShapeAttestation_Caps(t *testing.T) {
	t.Run("too many entries", func(t *testing.T) {
		in := make([]session.HarnessShapeAttestation, 26)
		for i := range in {
			in[i] = session.HarnessShapeAttestation{Harness: "h", Path: "p", Assertions: []string{"a"}}
		}
		for i := range in {
			in[i].Harness = "h" + string(rune('a'+i%26))
			in[i].Path = "p" + string(rune('a'+i%26))
		}
		_, err := normalizeHarnessShapeAttestation(in)
		require.Error(t, err)
		require.Contains(t, err.Error(), "at most 25 entries")
	})
	t.Run("harness too long", func(t *testing.T) {
		in := []session.HarnessShapeAttestation{{Harness: strings.Repeat("x", 241), Path: "p", Assertions: []string{"a"}}}
		_, err := normalizeHarnessShapeAttestation(in)
		require.Error(t, err)
		require.Contains(t, err.Error(), "harness")
		require.Contains(t, err.Error(), "240")
	})
	t.Run("path too long", func(t *testing.T) {
		in := []session.HarnessShapeAttestation{{Harness: "h", Path: strings.Repeat("x", 241), Assertions: []string{"a"}}}
		_, err := normalizeHarnessShapeAttestation(in)
		require.Error(t, err)
		require.Contains(t, err.Error(), "path")
		require.Contains(t, err.Error(), "240")
	})
	t.Run("too many assertions", func(t *testing.T) {
		assertions := make([]string, 11)
		for i := range assertions {
			assertions[i] = "a" + string(rune('a'+i))
		}
		in := []session.HarnessShapeAttestation{{Harness: "h", Path: "p", Assertions: assertions}}
		_, err := normalizeHarnessShapeAttestation(in)
		require.Error(t, err)
		require.Contains(t, err.Error(), "assertions")
		require.Contains(t, err.Error(), "10")
	})
	t.Run("assertion too long", func(t *testing.T) {
		in := []session.HarnessShapeAttestation{{Harness: "h", Path: "p", Assertions: []string{strings.Repeat("x", 481)}}}
		_, err := normalizeHarnessShapeAttestation(in)
		require.Error(t, err)
		require.Contains(t, err.Error(), "480")
	})
}

func TestNormalizeHarnessShapeAttestation_RejectsEmpty(t *testing.T) {
	t.Run("empty harness", func(t *testing.T) {
		in := []session.HarnessShapeAttestation{{Harness: "  ", Path: "p", Assertions: []string{"a"}}}
		_, err := normalizeHarnessShapeAttestation(in)
		require.Error(t, err)
		require.Contains(t, err.Error(), "harness")
	})
	t.Run("empty assertions array", func(t *testing.T) {
		in := []session.HarnessShapeAttestation{{Harness: "h", Path: "p", Assertions: []string{}}}
		_, err := normalizeHarnessShapeAttestation(in)
		require.Error(t, err)
		require.Contains(t, err.Error(), "assertions")
	})
	t.Run("assertion with empty string", func(t *testing.T) {
		in := []session.HarnessShapeAttestation{{Harness: "h", Path: "p", Assertions: []string{"valid", "  "}}}
		_, err := normalizeHarnessShapeAttestation(in)
		require.Error(t, err)
		require.Contains(t, err.Error(), "assertion")
	})
}

func TestNormalizeHarnessShapeAttestation_DedupCanonical(t *testing.T) {
	in := []session.HarnessShapeAttestation{
		{Harness: "h", Path: "p", Assertions: []string{"a", "b"}},
		{Harness: " h ", Path: "p", Assertions: []string{" a ", "b"}},
		{Harness: "h2", Path: "p", Assertions: []string{"a"}},
	}
	out, err := normalizeHarnessShapeAttestation(in)
	require.NoError(t, err)
	require.Len(t, out, 2, "dup collapses; distinct entry stays")
}

func TestNormalizeHarnessShapeAttestation_EmptyInputOk(t *testing.T) {
	out, err := normalizeHarnessShapeAttestation(nil)
	require.NoError(t, err)
	require.Empty(t, out)
}

func TestNormalizeHarnessShapeAttestation_DupsDoNotCountTowardEntryCap(t *testing.T) {
	// 26 inputs but 2 are duplicates — after dedup, 25 unique entries.
	// Must succeed under dedup-before-count semantics.
	in := make([]session.HarnessShapeAttestation, 26)
	for i := 0; i < 25; i++ {
		in[i] = session.HarnessShapeAttestation{Harness: "h" + string(rune('a'+i)), Path: "p", Assertions: []string{"a"}}
	}
	// Index 25 duplicates index 0 (post-trim).
	in[25] = session.HarnessShapeAttestation{Harness: "h" + string(rune('a'+0)), Path: "p", Assertions: []string{"a"}}
	out, err := normalizeHarnessShapeAttestation(in)
	require.NoError(t, err, "dup collapses → 25 unique entries → within cap")
	require.Len(t, out, 25)
}

func TestNormalizeTaskSpecInputs_Verification(t *testing.T) {
	in, err := normalizeTaskSpecInputs(ValidateTaskSpecArgs{TaskTitle: "t", Goal: "g", Verification: []string{" go test ./... ", " "}}, 1<<20)
	require.NoError(t, err)
	require.Equal(t, []string{"go test ./..."}, in.Verification)

	_, err = normalizeTaskSpecInputs(ValidateTaskSpecArgs{TaskTitle: "t", Goal: "g", Verification: []string{strings.Repeat("x", maxVerificationChars+1)}}, 1<<20)
	require.EqualError(t, err, "verification[0] must be at most 2000 characters")

	many := make([]string, maxPinnedByEntries+1)
	for i := range many {
		many[i] = "step"
	}
	_, err = normalizeTaskSpecInputs(ValidateTaskSpecArgs{TaskTitle: "t", Goal: "g", Verification: many}, 1<<20)
	require.EqualError(t, err, "verification must contain at most 50 entries")

	_, err = normalizeTaskSpecInputs(ValidateTaskSpecArgs{TaskTitle: "t", Goal: "g", Verification: []string{strings.Repeat("x", 400)}}, 300)
	require.ErrorContains(t, err, "task spec payload 402 bytes > cap 300")
}

func TestNormalizeTaskSpecInputs_VerificationHasItsOwnCap(t *testing.T) {
	args := ValidateTaskSpecArgs{TaskTitle: "T", Goal: "G", Verification: []string{strings.Repeat("a", maxVerificationChars)}}
	in, err := normalizeTaskSpecInputs(args, 1<<20)
	require.NoError(t, err, "a step of %d characters is within the verification cap", maxVerificationChars)
	require.Len(t, in.Verification, 1)

	args.Verification = []string{strings.Repeat("a", maxVerificationChars+1)}
	_, err = normalizeTaskSpecInputs(args, 1<<20)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "verification[0] must be at most 2000 characters")
}

func TestNormalizeTaskSpecInputs_PinnedByKeepsTheShorterCap(t *testing.T) {
	args := ValidateTaskSpecArgs{TaskTitle: "T", Goal: "G", PinnedBy: []string{strings.Repeat("a", maxPinnedByChars+1)}}
	_, err := normalizeTaskSpecInputs(args, 1<<20)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pinned_by[0] must be at most 500 characters")
}

func TestNormalizeTaskSpecInputs_VerifiedReferencesTakeTwoHundred(t *testing.T) {
	refs := make([]string, maxVerifiedReferenceEntries)
	for i := range refs {
		refs[i] = fmt.Sprintf("internal/pkg/file%d.go", i)
	}
	args := ValidateTaskSpecArgs{TaskTitle: "T", Goal: "G", ControllerVerifiedReferences: refs}
	in, err := normalizeTaskSpecInputs(args, 1<<20)
	require.NoError(t, err)
	assert.Len(t, in.ControllerVerifiedReferences, maxVerifiedReferenceEntries)

	args.ControllerVerifiedReferences = append(refs, "one too many")
	_, err = normalizeTaskSpecInputs(args, 1<<20)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "controller_verified_references must contain at most 200 entries")
}
