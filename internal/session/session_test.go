package session

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHarnessShapeAttestation_JSONRoundTrip(t *testing.T) {
	in := HarnessShapeAttestation{
		Harness:    "TestHarnessX",
		Path:       "test/path/foo.kt:L100-L200",
		Assertions: []string{"records emitted spans", "does not stub the validator"},
	}
	b, err := json.Marshal(in)
	require.NoError(t, err)
	require.Contains(t, string(b), `"harness"`)
	require.Contains(t, string(b), `"path"`)
	require.Contains(t, string(b), `"assertions"`)
	var out HarnessShapeAttestation
	require.NoError(t, json.Unmarshal(b, &out))
	require.Equal(t, in, out)
}

func TestTaskSpec_HarnessShapeAttestations_OmittedWhenEmpty(t *testing.T) {
	spec := TaskSpec{Title: "t", Goal: "g"}
	b, err := json.Marshal(spec)
	require.NoError(t, err)
	require.NotContains(t, string(b), "harness_shape_attestations", "must be omitempty")
}

func TestTaskSpec_HarnessShapeAttestations_SerializesWhenSet(t *testing.T) {
	spec := TaskSpec{
		Title: "t", Goal: "g",
		HarnessShapeAttestations: []HarnessShapeAttestation{
			{Harness: "H", Path: "p", Assertions: []string{"a"}},
		},
	}
	b, err := json.Marshal(spec)
	require.NoError(t, err)
	require.Contains(t, string(b), `"harness_shape_attestations":[`)
}
