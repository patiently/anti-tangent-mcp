package mcpsrv

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/planrun"
	"github.com/patiently/anti-tangent-mcp/internal/providers"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

// NOTE: planCallContext.PlanRuns is set EXPLICITLY in every test below, never
// defaulted by a helper. A helper that filled it in whenever it was nil
// supplied the very wiring ValidatePlan is supposed to pin: deleting
// `PlanRuns: h.deps.PlanRuns` from the production call site left every test
// here green, because the helper handed the recovery path a store the handler
// had failed to pass. Tests construct `&handlers{}` with no deps, so the store
// has to come from the test, but it has to come from the test VISIBLY.

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
		PartialRaw: nil,
		Prior:      verdict.PlanResult{},
		Call: planCallContext{
			ModelUsed: "anthropic:claude-sonnet-4-6",
			ReviewMS:  1234,
			Clamp:     verdict.Finding{},
			PlanRuns:  planrun.NewStore(time.Minute),
		},
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
		PartialRaw: nil,
		Prior:      verdict.PlanResult{},
		Call: planCallContext{
			ModelUsed: "",
			ReviewMS:  0,
			Clamp:     verdict.Finding{},
			PlanRuns:  planrun.NewStore(time.Minute),
		},
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

// TestHandlePlanReviewErr_AppliesPlanTextDeprecation is the direct unit-level
// regression test for the fix-round bug: prependPlanDeprecation's doc
// comment claims it "survives every early-exit path", but the
// truncation-recovery path used to return before it was ever applied. A
// plan_text caller whose reviewer response truncates must still get the
// deprecation notice, leading PlanFindings (see prependPlanDeprecation).
func TestHandlePlanReviewErr_AppliesPlanTextDeprecation(t *testing.T) {
	h := &handlers{}

	r, pr, handled, err := h.handlePlanReviewErr(planReviewErrInputs{
		Err:   providers.ErrResponseTruncated,
		Model: config.ModelRef{Provider: "openai", Model: "gpt-5"},
		Call:  planCallContext{UsedPlanText: true, PlanRuns: planrun.NewStore(time.Minute)},
	})
	require.True(t, handled)
	require.NoError(t, err)
	require.NotNil(t, r)

	require.NotEmpty(t, pr.PlanFindings)
	assert.Equal(t, "input", pr.PlanFindings[0].Criterion,
		"plan_text deprecation notice must lead PlanFindings even on the truncation-recovery path")
	assert.Equal(t, verdict.SeverityMinor, pr.PlanFindings[0].Severity)
	assert.Contains(t, pr.SummaryBlock, "1.0.0",
		"SummaryBlock must be (re)computed after the deprecation prepend, not before it")
}

// TestHandlePlanReviewErr_NoDeprecationWhenPlanPathUsed pins the other side:
// a plan_path caller's truncation-recovery envelope must NOT gain the
// plan_text deprecation notice.
func TestHandlePlanReviewErr_NoDeprecationWhenPlanPathUsed(t *testing.T) {
	h := &handlers{}

	_, pr, handled, err := h.handlePlanReviewErr(planReviewErrInputs{
		Err:   providers.ErrResponseTruncated,
		Model: config.ModelRef{Provider: "openai", Model: "gpt-5"},
		Call:  planCallContext{UsedPlanText: false, PlanRuns: planrun.NewStore(time.Minute)},
	})
	require.True(t, handled)
	require.NoError(t, err)

	for _, f := range pr.PlanFindings {
		assert.NotEqual(t, "input", f.Criterion, "plan_path callers must not see the plan_text deprecation notice")
	}
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
