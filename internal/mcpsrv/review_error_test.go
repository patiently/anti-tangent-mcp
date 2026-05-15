package mcpsrv

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/providers"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

// TestHandlePlanReviewErr_PreservesModelUsedAndReviewMS guards against the
// regression CodeRabbit caught on the v0.3.3 refactor: rebuilding the
// truncated-plan envelope must NOT overwrite the real reviewer identifier
// and elapsed time captured before the truncation. The chunked path can
// complete several reviewer calls before the truncating chunk, and those
// values should survive the recovery rather than reset to defaults.
func TestHandlePlanReviewErr_PreservesModelUsedAndReviewMS(t *testing.T) {
	h := &handlers{}

	r, _, handled, err := h.handlePlanReviewErr(planReviewErrInputs{
		Err:        providers.ErrResponseTruncated,
		Model:      config.ModelRef{Provider: "openai", Model: "gpt-5"},
		ModelUsed:  "anthropic:claude-sonnet-4-6",
		ReviewMS:   1234,
		PartialRaw: nil,
		Clamp:      verdict.Finding{},
		Prior:      verdict.PlanResult{},
	})
	require.True(t, handled)
	require.NoError(t, err)
	require.NotNil(t, r)

	got := decodeCallToolEnvelope(t, r)
	assert.Equal(t, "anthropic:claude-sonnet-4-6", got.ModelUsed,
		"recovered envelope must surface the reviewer that actually ran, not the configured fallback")
	assert.Equal(t, int64(1234), got.ReviewMS,
		"recovered envelope must surface the elapsed time observed before truncation, not zero")
}

// TestHandlePlanReviewErr_FallsBackToModelStringWhenModelUsedEmpty covers the
// pre-truncation Pass-1 case where no reviewer call completed successfully
// before the failure. The helper must fall back to the configured plan model
// rather than emitting an empty model_used field.
func TestHandlePlanReviewErr_FallsBackToModelStringWhenModelUsedEmpty(t *testing.T) {
	h := &handlers{}

	r, _, handled, err := h.handlePlanReviewErr(planReviewErrInputs{
		Err:        providers.ErrResponseTruncated,
		Model:      config.ModelRef{Provider: "openai", Model: "gpt-5"},
		ModelUsed:  "",
		ReviewMS:   0,
		PartialRaw: nil,
		Clamp:      verdict.Finding{},
		Prior:      verdict.PlanResult{},
	})
	require.True(t, handled)
	require.NoError(t, err)
	require.NotNil(t, r)

	got := decodeCallToolEnvelope(t, r)
	assert.Equal(t, "openai:gpt-5", got.ModelUsed,
		"empty ModelUsed must fall back to the configured plan model identifier")
	assert.Equal(t, int64(0), got.ReviewMS)
}

// TestHandlePlanReviewErr_PropagatesNonTruncationError verifies the contract
// that non-truncation errors return handled=true so the caller can drop the
// residual `if err != nil` branch.
func TestHandlePlanReviewErr_PropagatesNonTruncationError(t *testing.T) {
	h := &handlers{}
	want := errors.New("provider exploded")

	r, _, handled, err := h.handlePlanReviewErr(planReviewErrInputs{
		Err:   want,
		Model: config.ModelRef{Provider: "openai", Model: "gpt-5"},
	})
	require.True(t, handled, "non-truncation errors must still set handled=true")
	require.Same(t, want, err, "the helper must propagate the original error unchanged")
	require.Nil(t, r)
}

// TestHandlePlanReviewErr_NilErrorReturnsHandledFalse covers the happy path:
// no review error means handled=false so the caller continues normally.
func TestHandlePlanReviewErr_NilErrorReturnsHandledFalse(t *testing.T) {
	h := &handlers{}
	r, _, handled, err := h.handlePlanReviewErr(planReviewErrInputs{Err: nil})
	require.False(t, handled)
	require.NoError(t, err)
	require.Nil(t, r)
}

// decodeCallToolEnvelope pulls the JSON-marshaled envelope text out of a
// CallToolResult and decodes the model_used + review_ms fields the plan
// recovery path is expected to surface.
func decodeCallToolEnvelope(t *testing.T, r *mcp.CallToolResult) struct {
	ModelUsed string `json:"model_used"`
	ReviewMS  int64  `json:"review_ms"`
} {
	t.Helper()
	require.Len(t, r.Content, 1)
	tc, ok := r.Content[0].(*mcp.TextContent)
	require.True(t, ok, "CallToolResult.Content[0] must be *mcp.TextContent")
	var got struct {
		ModelUsed string `json:"model_used"`
		ReviewMS  int64  `json:"review_ms"`
	}
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &got))
	return got
}
