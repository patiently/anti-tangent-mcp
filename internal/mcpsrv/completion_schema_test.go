package mcpsrv

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateCompletionInputSchema_OpensUnknownKeysAndKeepsRequired(t *testing.T) {
	s := validateCompletionInputSchema()
	assert.ElementsMatch(t, []string{"session_id", "summary"}, s.Required)

	cs, ok := s.Properties["codescene"]
	require.True(t, ok)
	assert.Nil(t, cs.AdditionalProperties, "codescene must accept unknown keys")
	assert.Empty(t, cs.Required, "every digest field is optional")

	v, ok := cs.Properties["verdicts"]
	require.True(t, ok)
	assert.Nil(t, v.AdditionalProperties, "verdicts must accept unknown keys")
	assert.ElementsMatch(t, []string{"improved", "degraded", "stable"}, v.Required)
}
