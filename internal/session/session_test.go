package session

import (
	"encoding/json"
	"reflect"
	"strings"
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

func TestTaskSpec_DoesNotCarryProjectKnowledge(t *testing.T) {
	// Confirms spec §3.3: project_knowledge is not stored in session.TaskSpec.
	// Runtime reflection check that no TaskSpec field name matches
	// "ProjectKnowledge" (case-insensitive).
	specType := reflect.TypeOf(TaskSpec{})
	for i := 0; i < specType.NumField(); i++ {
		if name := specType.Field(i).Name; strings.EqualFold(name, "ProjectKnowledge") {
			t.Fatalf("session.TaskSpec must not carry %q — project_knowledge is per-call only per spec §3.3", name)
		}
	}
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
