package mcpsrv

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

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
// A tail assembled by hand at each site drifts: a step added to one path is
// easily missed on another. Such a miss can be functional, not cosmetic — the
// PlanRunID mint, for one: controller.md §5.1 tells the controller to capture
// plan_run_id from a passing validate_plan response, a truncated-but-recovered
// review CAN return pass, and plan_run_report hard-requires the id. A step
// every path needs therefore goes on this type, and a value it needs becomes
// a field here rather than a parameter threaded to one site.
//
// The verdict ladder (finalizePlanVerdict) is deliberately NOT a method
// here. The cache-hit path must never re-run it on an already-finalized
// entry — its checklist is already appended, and a second ladder would count
// it toward noise_cluster — so the ladder stays at the call sites, which is
// exactly where the three orders differ:
//
//	fresh review = applyPreLadder -> ladder -> mintPlanRunID -> store -> finish
//	recovery     = applyPreLadder -> ladder ->                            finish
//	cache hit    =                                                        finish
//
// The fresh-review path hoists the mint above store() so the cached entry
// carries the plan_run_id: a cache hit must reuse the original call's run
// rather than mint a second one (TestValidatePlan_CachePassingResult).
// finish()'s own mint is guarded on PlanRunID == "" and is therefore a no-op
// both there and on the cache-hit path, which reads an entry that already
// carries one.
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
	// PlanLedger receives a header line for every freshly minted run. Nil-safe:
	// nil unless ANTI_TANGENT_STATS_DIR and ANTI_TANGENT_PLAN_LEDGER are set.
	PlanLedger *planrun.Ledger
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
	// ContextFiles is THIS call's attached set. Every path renders it as the
	// summary block's `context:` provenance list, so the human reading a
	// truncated review's envelope still sees what the reviewer was given. It
	// is also the attached set DemoteUnattachedContradictions tests against,
	// and both paths MUST pass the same one, or the demotion would differ
	// between a fresh and a recovered review of the same plan.
	ContextFiles []fileSource
	// FileConsistency is the deterministic, reviewer-free Create/Modify
	// finding for this plan, or nil. It is computed by the CALLER and carried
	// here because it must survive truncation: the check needs no reviewer and
	// cannot itself be truncated, so a truncated reviewer response must not
	// lose a finding the server already knows for certain. Nil on the
	// cache-hit path by design — see the type comment.
	FileConsistency *verdict.Finding
	// Tasks is the parsed plan. applyPreLadder re-attaches normative test
	// bodies from it (populateNormativeTestBodies), and applyPreLadder's
	// waivers and finish's display IDs both key task findings on its headings
	// (planTaskKeys). Every path sets it, the cache hit included, so a task
	// finding's ID is the same whichever path produced the response.
	Tasks []planparser.RawTask
	// Rulings are this call's controller rulings by fingerprint, which
	// applyPreLadder waives findings against. Unset on the cache-hit path: the
	// rulings are rendered into the prompts the cache key hashes, so a stored
	// entry already carries its waivers.
	Rulings map[string]session.Ruling
	// VerifiedReferences are this call's controller_verified_references, which
	// applyPreLadder suppresses unverifiable claims with.
	VerifiedReferences []string
	// MalformedRulingIDs are this call's ruling IDs without a display ID's
	// shape. Per call, like the deprecation notice, and never stored on a
	// cache entry.
	MalformedRulingIDs []string
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
	// After demotion, which can turn a contradiction into the unverifiable
	// claim a verified reference suppresses; before the ladder's rollup
	// collects what remains into the checklist.
	suppressPlanVerifiedReferences(pr, c.VerifiedReferences)
	// Before the file-consistency finding and the clamp join the list, so only
	// reviewer findings are waived.
	waivePlanFindings(pr, c.Rulings, c.Tasks)
	if c.FileConsistency != nil {
		pr.PlanFindings = append(pr.PlanFindings, *c.FileConsistency)
	}
	*pr = prependPlanClamp(*pr, c.Clamp)
}

// mintPlanRunID assigns a plan_run_id when pr does not already carry one.
// Idempotent by that guard, which is what lets finish() call it
// unconditionally while the fresh-review path hoists it above store(). On a
// freshly minted run it also appends a best-effort ledger header, recording
// the run's id, verdict, quality and the plan's task headings; the early
// return on an existing id is what keeps a cache hit from writing a second
// header.
func (c planCallContext) mintPlanRunID(pr *verdict.PlanResult) {
	if pr.PlanRunID != "" {
		return
	}
	run := c.PlanRuns.CreateWithTasks(string(pr.PlanVerdict), string(pr.PlanQuality), planRunTasks(*pr, c.Tasks))
	pr.PlanRunID = run.ID
	if err := c.PlanLedger.AppendHeader(run); err != nil {
		slog.Warn("plan ledger header append failed", "plan_run_id", run.ID, "err", err)
	}
}

// planRunTasks lists the plan's tasks for a new run: the parsed headings, in
// order, or the reviewer's task titles when the plan parsed no tasks.
func planRunTasks(pr verdict.PlanResult, tasks []planparser.RawTask) []planrun.PlanTask {
	if len(tasks) > 0 {
		out := make([]planrun.PlanTask, len(tasks))
		for i, t := range tasks {
			out[i] = planrun.PlanTask{Index: i + 1, Title: t.Title}
		}
		return out
	}
	out := make([]planrun.PlanTask, len(pr.Tasks))
	for i, t := range pr.Tasks {
		out[i] = planrun.PlanTask{Index: i + 1, Title: t.TaskTitle}
	}
	return out
}

// finish runs the post-ladder tail every validate_plan exit path shares:
// mint the plan_run_id if none exists, add the per-call advisories, assign
// display IDs, then compute SummaryBlock exactly once with all of that in
// place.
//
// The advisories land AFTER the ladder and after store() on purpose. Each is
// a minor CategoryOther finding, and verdict.FinalizeVerdict treats a 3rd
// minor finding as a noise_cluster trigger that lifts the verdict to warn —
// running one through the ladder would let an advisory about THIS call's
// arguments flip a plan's verdict. Each also describes this call, not the plan
// content, so none may be stored on a cache entry. Deprecation goes first
// because it has been PlanFindings[0] since it existed.
func (c planCallContext) finish(pr *verdict.PlanResult) {
	c.mintPlanRunID(pr)
	if len(c.MalformedRulingIDs) > 0 {
		pr.PlanFindings = append(pr.PlanFindings, malformedPlanRulingsAdvisory(c.MalformedRulingIDs))
	}
	*pr = prependRepoRootUnusable(*pr, c.RepoRootUnusable)
	*pr = prependPlanDeprecation(*pr, c.UsedPlanText)
	assignPlanIDs(pr, c.Tasks)
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

// handlePlanReviewErr handles the error from ValidatePlan's reviewer call
// (reviewPlanSingle or reviewPlanChunked). On a truncated response it
// recovers what it can and runs the whole post-review tail itself —
// applyPreLadder, the verdict ladder and finish, but never store() — so a
// truncated validate_plan response is complete when it returns.
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
	finalizePlanVerdict(&pr, call.Tasks)
	call.finish(&pr)
	r, p, err := planEnvelopeResultFinalized(pr, call.meta())
	return r, p, true, err
}

// perTaskMaxTokensEnvVar names the output budget a truncated per-task review
// tells the caller to raise.
const perTaskMaxTokensEnvVar = "ANTI_TANGENT_PER_TASK_MAX_TOKENS"

// reviewOutcome is one per-task reviewer call as a session tool's tail
// consumes it. Result holds only the findings the reviewer produced. Server
// holds the findings the server adds for a truncated response, which the tail
// places after the reviewer's own.
type reviewOutcome struct {
	Result    verdict.Result
	Server    []verdict.Finding
	ModelUsed string
	ReviewMS  int64
	Truncated bool
}

// runReview runs the reviewer call and folds a truncated response into an
// ordinary outcome, so each session tool runs one tail for both. A response
// truncated after some complete findings yields those findings, marked
// partial, and a minor marker; one truncated before any yields no reviewer
// findings and the server's major truncation notice. Any other error is
// returned.
func (h *handlers) runReview(ctx context.Context, model config.ModelRef, p prompts.Output, maxTokens int) (reviewOutcome, error) {
	result, modelUsed, ms, partialRaw, err := h.review(ctx, model, p, maxTokens)
	if err == nil {
		return reviewOutcome{Result: result, ModelUsed: modelUsed, ReviewMS: ms}, nil
	}
	if !errors.Is(err, providers.ErrResponseTruncated) {
		return reviewOutcome{}, err
	}
	out := reviewOutcome{ModelUsed: model.String(), Truncated: true}
	if recovered, marker, ok := recoverPartialFindings(partialRaw, perTaskMaxTokensEnvVar); ok {
		out.Result = recovered
		out.Server = []verdict.Finding{marker}
		return out, nil
	}
	notice := truncatedResult()
	out.Server = notice.Findings
	notice.Findings = nil
	out.Result = notice
	return out, nil
}

// withServerFindings places the max-tokens clamp before the reviewer's
// findings and the truncation findings after them.
func withServerFindings(clamp verdict.Finding, reviewer, server []verdict.Finding) []verdict.Finding {
	out := make([]verdict.Finding, 0, len(reviewer)+len(server)+1)
	if clamp.Severity != "" {
		out = append(out, clamp)
	}
	out = append(out, reviewer...)
	return append(out, server...)
}
