package mcpsrv

import (
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/prompts"
	"github.com/patiently/anti-tangent-mcp/internal/providers"
	"github.com/patiently/anti-tangent-mcp/internal/session"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

// preCallContext bundles the per-call setup values shared by the three
// stateful handlers (ValidateTaskSpec, CheckProgress, ValidateCompletion).
// Carrying these together lets a single helper return one error instead of
// three (effectiveMaxTokens, resolveModel, prompts.Render*).
type preCallContext struct {
	MaxTokens int
	Clamp     verdict.Finding
	Model     config.ModelRef
	Rendered  prompts.Output
}

// resolvePreCallContext bundles effectiveMaxTokens + resolveModel + prompt
// rendering so handlers have a single error-return point instead of three.
// The renderFn closure lets each handler use its own prompt template
// (RenderPre / RenderMid / RenderPost); renderErrMsg is the prefix used to
// wrap a render failure so handler error strings stay identical to the
// inline form previously emitted from each handler.
func (h *handlers) resolvePreCallContext(
	overrideTokens int,
	defaultTokens int,
	modelOverride string,
	fallbackModel config.ModelRef,
	renderFn func() (prompts.Output, error),
	renderErrMsg string,
) (preCallContext, error) {
	maxTokens, clamp, err := effectiveMaxTokens(overrideTokens, defaultTokens, h.deps.Cfg.MaxTokensCeiling)
	if err != nil {
		return preCallContext{}, err
	}
	model, err := h.resolveModel(modelOverride, fallbackModel)
	if err != nil {
		return preCallContext{}, err
	}
	rendered, err := renderFn()
	if err != nil {
		return preCallContext{}, fmt.Errorf("%s: %w", renderErrMsg, err)
	}
	return preCallContext{MaxTokens: maxTokens, Clamp: clamp, Model: model, Rendered: rendered}, nil
}

// resolveModelAndRender bundles resolveModel + prompt rendering for handlers
// whose pre-render work (session lookup, payload cap) requires effectiveMaxTokens
// to be split out earlier. Returns one error-return point instead of two so
// the caller's branch count drops by one. renderErrMsg is the prefix used to
// wrap a render failure (matches the inline `fmt.Errorf` form previously used
// at each call site).
func (h *handlers) resolveModelAndRender(
	modelOverride string,
	fallbackModel config.ModelRef,
	renderFn func() (prompts.Output, error),
	renderErrMsg string,
) (config.ModelRef, prompts.Output, error) {
	model, err := h.resolveModel(modelOverride, fallbackModel)
	if err != nil {
		return config.ModelRef{}, prompts.Output{}, err
	}
	rendered, err := renderFn()
	if err != nil {
		return config.ModelRef{}, prompts.Output{}, fmt.Errorf("%s: %w", renderErrMsg, err)
	}
	return model, rendered, nil
}

// planReviewErrInputs bundles the inputs to handlePlanReviewErr. Carrying
// these on a struct keeps the helper signature narrow (1 arg vs. 5) and
// matches CodeScene's "max arguments = 4" code-health threshold.
type planReviewErrInputs struct {
	Err        error
	Model      config.ModelRef
	PartialRaw []byte
	Clamp      verdict.Finding
	// ModelUsed and ReviewMS preserve the real reviewer identifier and elapsed
	// time captured before the truncation. The chunked path can record these
	// from earlier successful reviewer calls (Pass-1 + completed chunks); when
	// non-empty they survive the recovery envelope. Pre-truncation Pass-1
	// failures leave them empty/zero and the helper falls back to Model.String().
	ModelUsed string
	ReviewMS  int64
	// Prior carries any partial state already collected before the truncation
	// point (for the chunked path: Pass-1 plan_findings plus complete chunks).
	// See recoverPartialPlanFindings for the merge semantics.
	Prior verdict.PlanResult
}

// handlePlanReviewErr is the ValidatePlan analog of handlePerTaskReviewErr.
// Collapses the truncation-recovery + error-propagation pattern after the
// plan reviewer call (either reviewPlanSingle or reviewPlanChunked).
//
// Returns (result, planResult, handled, err):
//   - in.Err == nil               → handled=false; caller proceeds normally.
//   - in.Err is a truncation err  → handled=true; result/planResult carry
//     the partial-recovery or truncated envelope with clamp applied.
//   - in.Err is anything else     → handled=true; result/planResult are
//     zero values and err is the propagated in.Err.
//
// Always returning handled=true on non-nil in.Err lets the call site drop
// the residual `if err != nil` branch — just `if handled { return ... }`.
func (h *handlers) handlePlanReviewErr(in planReviewErrInputs) (*mcp.CallToolResult, verdict.PlanResult, bool, error) {
	if in.Err == nil {
		return nil, verdict.PlanResult{}, false, nil
	}
	if !errors.Is(in.Err, providers.ErrResponseTruncated) {
		return nil, verdict.PlanResult{}, true, in.Err
	}
	pr, ok := recoverPartialPlanFindings(in.PartialRaw, in.Prior)
	if !ok {
		pr = truncatedPlanResult()
	}
	pr = prependPlanClamp(pr, in.Clamp)
	modelUsed := in.ModelUsed
	if modelUsed == "" {
		modelUsed = in.Model.String()
	}
	r, p, err := planEnvelopeResult(pr, modelUsed, in.ReviewMS)
	return r, p, true, err
}

// perTaskReviewErrInputs bundles the inputs to handlePerTaskReviewErr.
// Carrying these on a struct keeps the helper signature narrow (1 arg vs. 7)
// and matches CodeScene's "max arguments = 4" code-health threshold.
type perTaskReviewErrInputs struct {
	Err        error
	SessionID  string
	Model      config.ModelRef
	PartialRaw []byte
	EnvVar     string
	Clamp      verdict.Finding
	// Sess is nil for pre-session flows (ValidateTaskSpec, lightweight
	// ValidateCompletion); otherwise the resolved *session.Session so the
	// envelope carries SessionExpiresAt / SessionTTLRemainingSeconds.
	Sess *session.Session
}

// handlePerTaskReviewErr collapses the truncation-recovery + error-propagation
// pattern shared by ValidateTaskSpec, CheckProgress, and ValidateCompletion
// after h.review(...).
//
// Returns (result, env, handled, err):
//   - in.Err == nil               → handled=false; caller proceeds normally.
//   - in.Err is a truncation err  → handled=true; result/env carry the
//     partial-recovery or truncated envelope with clamp and (when in.Sess is
//     non-nil) session-TTL fields applied.
//   - in.Err is anything else     → handled=true; result/env are zero
//     values and err is the propagated in.Err.
//
// Always returning handled=true on non-nil in.Err lets the call site drop
// the residual `if err != nil` branch — just `if handled { return ... }`.
func (h *handlers) handlePerTaskReviewErr(in perTaskReviewErrInputs) (*mcp.CallToolResult, Envelope, bool, error) {
	if in.Err == nil {
		return nil, Envelope{}, false, nil
	}
	if !errors.Is(in.Err, providers.ErrResponseTruncated) {
		return nil, Envelope{}, true, in.Err
	}
	env, ok := recoverPartialFindings(in.SessionID, in.Model, in.PartialRaw, in.EnvVar)
	if !ok {
		env = truncatedEnvelope(in.SessionID, in.Model)
	}
	env = prependClamp(env, in.Clamp)
	if in.Sess != nil {
		env = h.withSessionTTL(env, in.Sess)
	}
	r, e, err := envelopeResult(env)
	return r, e, true, err
}
