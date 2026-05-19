package mcpsrv

import (
	"strings"
	"testing"

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
