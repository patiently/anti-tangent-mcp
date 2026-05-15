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

// handlePlanReviewErr is the ValidatePlan analog of handlePerTaskReviewErr.
// Collapses the truncation-recovery + error-propagation pattern after the
// plan reviewer call (either reviewPlanSingle or reviewPlanChunked).
//
// Returns (result, planResult, handled, err):
//   - reviewErr == nil               → handled=false; caller proceeds normally.
//   - reviewErr is a truncation err  → handled=true; result/planResult carry
//     the partial-recovery or truncated envelope with clamp applied.
//   - reviewErr is anything else     → handled=true; result/planResult are
//     zero values and err is the propagated reviewErr.
//
// Always returning handled=true on non-nil reviewErr lets the call site drop
// the residual `if err != nil` branch — just `if handled { return ... }`.
//
// `prior` carries any partial state already collected before the truncation
// point (for the chunked path: Pass-1 plan_findings plus complete chunks).
// See recoverPartialPlanFindings for the merge semantics.
func (h *handlers) handlePlanReviewErr(
	reviewErr error,
	model config.ModelRef,
	partialRaw []byte,
	clamp verdict.Finding,
	prior verdict.PlanResult,
) (*mcp.CallToolResult, verdict.PlanResult, bool, error) {
	if reviewErr == nil {
		return nil, verdict.PlanResult{}, false, nil
	}
	if !errors.Is(reviewErr, providers.ErrResponseTruncated) {
		return nil, verdict.PlanResult{}, true, reviewErr
	}
	pr, ok := recoverPartialPlanFindings(partialRaw, prior)
	if !ok {
		pr = truncatedPlanResult()
	}
	pr = prependPlanClamp(pr, clamp)
	r, p, err := planEnvelopeResult(pr, model.String(), 0)
	return r, p, true, err
}

// handlePerTaskReviewErr collapses the truncation-recovery + error-propagation
// pattern shared by ValidateTaskSpec, CheckProgress, and ValidateCompletion
// after h.review(...).
//
// Returns (result, env, handled, err):
//   - reviewErr == nil               → handled=false; caller proceeds normally.
//   - reviewErr is a truncation err  → handled=true; result/env carry the
//     partial-recovery or truncated envelope with clamp and (when sess is
//     non-nil) session-TTL fields applied.
//   - reviewErr is anything else     → handled=true; result/env are zero
//     values and err is the propagated reviewErr.
//
// Always returning handled=true on non-nil reviewErr lets the call site drop
// the residual `if err != nil` branch — just `if handled { return ... }`.
//
// Pass sess=nil for pre-session flows (ValidateTaskSpec, lightweight
// ValidateCompletion); pass the resolved *session.Session otherwise so the
// envelope carries SessionExpiresAt / SessionTTLRemainingSeconds.
func (h *handlers) handlePerTaskReviewErr(
	reviewErr error,
	sessionID string,
	model config.ModelRef,
	partialRaw []byte,
	envVar string,
	clamp verdict.Finding,
	sess *session.Session,
) (*mcp.CallToolResult, Envelope, bool, error) {
	if reviewErr == nil {
		return nil, Envelope{}, false, nil
	}
	if !errors.Is(reviewErr, providers.ErrResponseTruncated) {
		return nil, Envelope{}, true, reviewErr
	}
	env, ok := recoverPartialFindings(sessionID, model, partialRaw, envVar)
	if !ok {
		env = truncatedEnvelope(sessionID, model)
	}
	env = prependClamp(env, clamp)
	if sess != nil {
		env = h.withSessionTTL(env, sess)
	}
	r, e, err := envelopeResult(env)
	return r, e, true, err
}
