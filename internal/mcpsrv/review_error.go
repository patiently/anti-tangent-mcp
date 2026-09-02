package mcpsrv

import (
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/planparser"
	"github.com/patiently/anti-tangent-mcp/internal/planrun"
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

// planCallContext bundles everything the three validate_plan exit paths —
// the fresh-review path, the truncation-recovery path, and the cache-hit
// path — need to assemble the SAME post-review tail.
//
// It exists because that tail was hand-assembled at all three sites and the
// recovery site kept missing steps: across three review rounds it was found
// to be missing prependRepoRootUnusable, populateNormativeTestBodies, and
// the PlanRunID mint. The last of those is functional, not cosmetic —
// controller.md §5.1 tells the controller to capture plan_run_id from a
// passing validate_plan response, a truncated-but-recovered review CAN
// return pass, and plan_run_report hard-requires the id. Most of the fields
// below used to live on planReviewErrInputs solely to feed the duplicated
// tail, one field added per review round; that growth WAS the failure mode.
//
// The verdict ladder (finalizePlanVerdict) is deliberately NOT a method
// here. The cache-hit path must never re-run it on an already-finalized
// entry — normalizePlanUnverifiableFindings is not proven idempotent — so
// the ladder stays at the call sites, which is exactly where the three
// orders differ:
//
//	fresh review = applyPreLadder -> ladder -> mintPlanRunID -> store -> finish
//	recovery     = applyPreLadder -> ladder ->                            finish
//	cache hit    =                                                        finish
//
// The fresh-review path hoists the mint above store() so the cached entry
// carries the plan_run_id: a cache hit must reuse the original call's run
// rather than mint a second one (design §, and
// TestValidatePlan_CachePassingResult). finish()'s own mint is guarded on
// PlanRunID == "" and is therefore a no-op both there and on the cache-hit
// path, which reads an entry that already carries one.
//
// Two divergences on the cache-hit path are DELIBERATE, not omissions, and
// must not be "fixed" into parity:
//
//   - it never runs checkFileConsistency, so FileConsistency is nil there
//     (design §3.9). store() caches only a `pass` result (plan_cache.go),
//     and repo_root — the argument that gates the disk tier — is part of the
//     cache key, so a hit reproduces a run in which the check found nothing.
//   - it never calls store(), because the entry it is reading is the entry
//     it would write.
type planCallContext struct {
	// PlanRuns is the run store the PlanRunID mint draws from. Carried on the
	// context rather than reached through *handlers so finish() stays a method
	// on the context and all three sites construct one identical thing.
	PlanRuns *planrun.Store
	// Source is the caller's pre-rendered provenance string (planSrc.String()),
	// empty when plan_text was used. Threaded through so every envelope —
	// recovery and cache hit included — carries the same source line a
	// successful fresh review would have.
	Source string
	// ModelUsed is the provider-reported model for the reviewer call that
	// produced this result: the real identifier on the fresh path, the value
	// captured before truncation on the recovery path (falling back to the
	// configured ref when Pass 1 failed before reporting one), and the stored
	// entry's model on a cache hit.
	ModelUsed string
	// ReviewMS is the elapsed reviewer time, 0 on a cache hit (no call made).
	ReviewMS int64
	// UsedPlanText is args.PlanText != "" for THIS call, so every path gets
	// the plan_text deprecation notice — see prependPlanDeprecation.
	UsedPlanText bool
	// RepoRootUnusable is the reason a supplied repo_root could not be
	// resolved, empty when it was usable or was never supplied. Per-call, like
	// the deprecation notice, and never stored on a cache entry.
	RepoRootUnusable string
	// Clamp is the max_tokens clamp finding, zero when nothing was clamped.
	// Read by applyPreLadder only, so it is unset on the cache-hit path: the
	// clamp is already baked into the stored entry (max_tokens_override is
	// part of the cache key).
	Clamp verdict.Finding
	// ContextFiles is THIS call's attached set. Without it a truncated
	// review's summary block omitted the `context:` provenance list entirely
	// while the same call's stats counted every attached byte — the human
	// reading the envelope could not see what the reviewer had been given. It
	// is also the attached set DemoteUnattachedContradictions tests against,
	// and both paths MUST pass the same one: a divergence there is exactly how
	// the demotion would go all-or-nothing again on one path only.
	ContextFiles []fileSource
	// FileConsistency is the deterministic, reviewer-free Create/Modify
	// finding for this plan, or nil. It is computed by the CALLER and carried
	// here because it must survive truncation: the check needs no reviewer and
	// cannot itself be truncated, so a truncated reviewer response silently
	// dropping it was the one failure mode that lost a finding the server
	// already knew for certain. Nil on the cache-hit path by design — see the
	// type comment.
	FileConsistency *verdict.Finding
	// Tasks is the parsed plan, used to re-attach normative test bodies to the
	// reviewer's per-task results (populateNormativeTestBodies).
	Tasks []planparser.RawTask
}

// meta projects the context down to the summary inputs. One place, so the
// three paths cannot drift on what the `source:`/`context:`/`model_used:`
// lines say.
func (c planCallContext) meta() planSummaryMeta {
	return planSummaryMeta{
		ModelUsed:    c.ModelUsed,
		ReviewMS:     c.ReviewMS,
		Source:       c.Source,
		ContextFiles: c.ContextFiles,
	}
}

// applyPreLadder runs the post-review, PRE-verdict-ladder steps shared by the
// fresh-review and truncation-recovery paths. Everything here can change a
// verdict, which is why it must run before finalizePlanVerdict and why the
// ladder itself is not folded in (see the type comment).
func (c planCallContext) applyPreLadder(pr *verdict.PlanResult) {
	populateNormativeTestBodies(pr, c.Tasks)
	// Server-side suppression, independent of reviewer compliance: a
	// contradicted_codebase_claim that no attached file backs has nothing to
	// refute with, so the category (which deliberately carries no severity
	// floor) would let an unverifiable claim fail the gate at major or
	// critical. Per finding, not per call — with ANY file attached, a
	// contradiction about some OTHER, unattached file used to keep its
	// unfloored severity. The prompt already forbids both shapes; this is the
	// enforcement point.
	verdict.DemoteUnattachedContradictions(pr, fileSourcePaths(c.ContextFiles))
	if c.FileConsistency != nil {
		pr.PlanFindings = append(pr.PlanFindings, *c.FileConsistency)
	}
	*pr = prependPlanClamp(*pr, c.Clamp)
}

// mintPlanRunID assigns a plan_run_id when pr does not already carry one.
// Idempotent by that guard, which is what lets finish() call it
// unconditionally while the fresh-review path hoists it above store().
func (c planCallContext) mintPlanRunID(pr *verdict.PlanResult) {
	if pr.PlanRunID != "" {
		return
	}
	run := c.PlanRuns.Create(string(pr.PlanVerdict), string(pr.PlanQuality), len(pr.Tasks))
	pr.PlanRunID = run.ID
}

// finish runs the post-ladder tail every validate_plan exit path shares:
// mint the plan_run_id if none exists, add the two per-call advisories, then
// compute SummaryBlock exactly once with both of those already in place.
//
// The advisories land AFTER the ladder and after store() on purpose. Both are
// minor CategoryOther findings, and verdict.FinalizeVerdict treats a 3rd minor
// finding as a noise_cluster trigger that lifts the verdict to warn — running
// either through the ladder would let an advisory about THIS call's arguments
// flip a plan's verdict. Both also describe this call, not the plan content,
// so neither may be stored on a cache entry. Order puts deprecation first
// because it has been PlanFindings[0] since it existed.
func (c planCallContext) finish(pr *verdict.PlanResult) {
	c.mintPlanRunID(pr)
	*pr = prependRepoRootUnusable(*pr, c.RepoRootUnusable)
	*pr = prependPlanDeprecation(*pr, c.UsedPlanText)
	pr.SummaryBlock = formatPlanSummary(*pr, c.meta())
}

// planReviewErrInputs bundles the inputs to handlePlanReviewErr. Carrying
// these on a struct keeps the helper signature narrow (1 arg vs. 5) and
// matches CodeScene's "max arguments = 4" code-health threshold.
type planReviewErrInputs struct {
	Err        error
	Model      config.ModelRef
	PartialRaw []byte
	// Prior carries any partial state already collected before the truncation
	// point (for the chunked path: Pass-1 plan_findings plus complete chunks).
	// See recoverPartialPlanFindings for the merge semantics.
	Prior verdict.PlanResult
	// Call is the shared post-review tail context, built once by ValidatePlan
	// and used by BOTH the recovery path here and the fresh-review path there.
	// Call.ModelUsed may be empty when Pass 1 failed before the provider
	// reported a model; handlePlanReviewErr falls back to Model.String().
	Call planCallContext
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
	call := in.Call
	if call.ModelUsed == "" {
		// Pass 1 failed before the provider reported a model.
		call.ModelUsed = in.Model.String()
	}
	// Same tail, same order, as ValidatePlan's fresh-review path — minus the
	// store(), because a truncated result is never cached. See planCallContext.
	call.applyPreLadder(&pr)
	finalizePlanVerdict(&pr)
	call.finish(&pr)
	r, p, err := planEnvelopeResultFinalized(pr, call.meta())
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
	r, ok := recoverPartialFindings(in.PartialRaw, in.EnvVar)
	if !ok {
		r = truncatedResult()
	}
	if in.Clamp.Severity != "" {
		r.Findings = append([]verdict.Finding{in.Clamp}, r.Findings...)
	}
	r = verdict.FinalizeVerdict(r)
	env := Envelope{
		SessionID:  in.SessionID,
		Verdict:    string(r.Verdict),
		Findings:   r.Findings,
		NextAction: r.NextAction,
		ModelUsed:  in.Model.String(),
		Partial:    r.Partial,
	}
	if in.Sess != nil {
		env = h.withSessionTTL(env, in.Sess)
	}
	res, e, err := envelopeResult(env)
	return res, e, true, err
}
