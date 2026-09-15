package mcpsrv

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateTaskSpec_VerificationReachesThePreAndPostPrompts(t *testing.T) {
	const section = "Verification (the task's steps and verify commands):\n- go vet ./... reports no new warnings\n"
	h, rv := newRulingsHandlers(t)
	rv.resp = passResp("claude-sonnet-4-6")
	_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC 1"},
		NonGoals:     []string{"Fixing existing lint warnings"},
		Verification: []string{"  go vet ./... reports no new warnings  ", " "},
	})
	require.NoError(t, err)
	assert.Contains(t, rv.LastRequest.User, section)

	completeWith(t, h, rv, completionCallArgs(pre.SessionID), passResp("claude-opus-4-7"))
	assert.Contains(t, rv.LastRequest.User, section)
}
