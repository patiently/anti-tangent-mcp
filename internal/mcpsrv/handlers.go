package mcpsrv

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/planparser"
	"github.com/patiently/anti-tangent-mcp/internal/prompts"
	"github.com/patiently/anti-tangent-mcp/internal/providers"
	"github.com/patiently/anti-tangent-mcp/internal/session"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

// Envelope is the JSON returned to the subagent for every hook.
type Envelope struct {
	SessionID                  string            `json:"session_id"`
	Verdict                    string            `json:"verdict"`
	Findings                   []verdict.Finding `json:"findings"`
	NextAction                 string            `json:"next_action"`
	ModelUsed                  string            `json:"model_used"`
	ReviewMS                   int64             `json:"review_ms"`
	Partial                    bool              `json:"partial,omitempty"`
	SessionExpiresAt           *time.Time        `json:"session_expires_at,omitempty"`
	SessionTTLRemainingSeconds *int              `json:"session_ttl_remaining_seconds,omitempty"`
	SummaryBlock               string            `json:"summary_block,omitempty"`
}

// ValidateTaskSpecArgs is the input schema for the pre-hook.
type ValidateTaskSpecArgs struct {
	TaskTitle          string   `json:"task_title"           jsonschema:"required"`
	Goal               string   `json:"goal"                 jsonschema:"required"`
	AcceptanceCriteria []string `json:"acceptance_criteria,omitempty"`
	NonGoals           []string `json:"non_goals,omitempty"`
	Context            string   `json:"context,omitempty"`
	PinnedBy           []string `json:"pinned_by,omitempty"`
	Phase              string   `json:"phase,omitempty"`
	ModelOverride      string   `json:"model_override,omitempty"`
	MaxTokensOverride  int      `json:"max_tokens_override,omitempty"`
}

type handlers struct {
	deps Deps
}

func validateTaskSpecTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "validate_task_spec",
		Description: "Validate that a task specification is clear and implementable BEFORE you start coding. " +
			"Returns findings on missing/ambiguous goals, weak acceptance criteria, and unstated assumptions. " +
			"Call this once at the start of every task.",
	}
}

func (h *handlers) ValidateTaskSpec(ctx context.Context, _ *mcp.CallToolRequest, args ValidateTaskSpecArgs) (*mcp.CallToolResult, Envelope, error) {
	if args.TaskTitle == "" || args.Goal == "" {
		return nil, Envelope{}, errors.New("task_title and goal are required")
	}

	phase, err := normalizePhase(args.Phase)
	if err != nil {
		return nil, Envelope{}, err
	}
	pinnedBy, err := normalizePinnedBy(args.PinnedBy)
	if err != nil {
		return nil, Envelope{}, err
	}

	maxTokens, clamp, err := effectiveMaxTokens(args.MaxTokensOverride, h.deps.Cfg.PerTaskMaxTokens, h.deps.Cfg.MaxTokensCeiling)
	if err != nil {
		return nil, Envelope{}, err
	}

	model, err := h.resolveModel(args.ModelOverride, h.deps.Cfg.PreModel)
	if err != nil {
		return nil, Envelope{}, err
	}

	spec := session.TaskSpec{
		Title:              args.TaskTitle,
		Goal:               args.Goal,
		AcceptanceCriteria: args.AcceptanceCriteria,
		NonGoals:           args.NonGoals,
		Context:            args.Context,
		PinnedBy:           pinnedBy,
		Phase:              phase,
	}

	rendered, err := prompts.RenderPre(prompts.PreInput{Spec: spec})
	if err != nil {
		return nil, Envelope{}, fmt.Errorf("render pre prompt: %w", err)
	}

	result, modelUsed, ms, partialRaw, err := h.review(ctx, model, rendered, maxTokens)
	if err != nil {
		if errors.Is(err, providers.ErrResponseTruncated) {
			if env, ok := recoverPartialFindings("", model, partialRaw, "ANTI_TANGENT_PER_TASK_MAX_TOKENS"); ok {
				env = prependClamp(env, clamp)
				return envelopeResult(env)
			}
			env := truncatedEnvelope("", model)
			env = prependClamp(env, clamp)
			return envelopeResult(env)
		}
		return nil, Envelope{}, err
	}

	// Create the session only after the review succeeds so failed reviews
	// don't leave orphan sessions in the store waiting for TTL eviction.
	sess := h.deps.Sessions.Create(spec)
	h.deps.Sessions.SetPreFindings(sess.ID, result.Findings)
	// Re-fetch after SetPreFindings so LastAccessed reflects the final mutation.
	if refreshed, ok := h.deps.Sessions.Get(sess.ID); ok {
		sess = refreshed
	}

	env := Envelope{
		SessionID:  sess.ID,
		Verdict:    string(result.Verdict),
		Findings:   result.Findings,
		NextAction: result.NextAction,
		ModelUsed:  modelUsed,
		ReviewMS:   ms,
	}
	env = prependClamp(env, clamp)
	env = h.withSessionTTL(env, sess)
	return envelopeResult(env)
}

// review runs a single reviewer call with one parse-retry on malformed JSON.
// On ErrResponseTruncated, the returned []byte carries the partial response
// bytes (possibly empty if the provider returned none) so the caller can
// attempt partial-findings recovery via recoverPartialFindings.
//
// maxTokens is the per-call max-tokens value (computed by effectiveMaxTokens
// from the configured default and any caller-supplied override). Passing it
// in lets the four per-task handlers share a single review() while still
// honoring per-call overrides.
func (h *handlers) review(ctx context.Context, model config.ModelRef, p prompts.Output, maxTokens int) (verdict.Result, string, int64, []byte, error) {
	rv, err := h.deps.Reviews.Get(model.Provider)
	if err != nil {
		return verdict.Result{}, "", 0, nil, err
	}
	start := time.Now()

	req := providers.Request{
		Model:      model.Model,
		System:     p.System,
		User:       p.User,
		MaxTokens:  maxTokens,
		JSONSchema: verdict.Schema(),
	}
	resp, err := rv.Review(ctx, req)
	if err != nil {
		if errors.Is(err, providers.ErrResponseTruncated) {
			return verdict.Result{}, "", 0, resp.RawJSON, err
		}
		return verdict.Result{}, "", 0, nil, err
	}
	r, err := verdict.Parse(resp.RawJSON)
	if err != nil {
		// One retry with explicit reminder.
		req.User = p.User + "\n\n" + verdict.RetryHint()
		resp, err = rv.Review(ctx, req)
		if err != nil {
			if errors.Is(err, providers.ErrResponseTruncated) {
				return verdict.Result{}, "", 0, resp.RawJSON, err
			}
			return verdict.Result{}, "", 0, nil, err
		}
		r, err = verdict.Parse(resp.RawJSON)
		if err != nil {
			return verdict.Result{}, "", 0, nil, fmt.Errorf("provider response failed schema after retry: %w", err)
		}
	}

	modelUsed := model.Provider + ":" + resp.Model
	if resp.Model == "" {
		modelUsed = model.String()
	}
	return r, modelUsed, time.Since(start).Milliseconds(), nil, nil
}

func (h *handlers) resolveModel(override string, fallback config.ModelRef) (config.ModelRef, error) {
	if override == "" {
		return fallback, nil
	}
	mr, err := config.ParseModelRef(override)
	if err != nil {
		return config.ModelRef{}, err
	}
	if err := providers.ValidateModel(mr); err != nil {
		return config.ModelRef{}, err
	}
	return mr, nil
}

// withSessionTTL populates the session expiry fields on env using the session's
// LastAccessed time and the store's configured idle TTL. Call this AFTER all
// store mutations that refresh LastAccessed (e.g. Get, AppendCheckpoint,
// SetPostFindings) so the surfaced expiry reflects the post-operation state.
// Returns env unchanged if sess is nil (e.g. not-found / truncation paths).
func (h *handlers) withSessionTTL(env Envelope, sess *session.Session) Envelope {
	if sess == nil || h.deps.Sessions == nil {
		return env
	}
	expiresAt := sess.LastAccessed.Add(h.deps.Sessions.TTL())
	remaining := int(time.Until(expiresAt).Seconds())
	if remaining < 0 {
		remaining = 0
	}
	env.SessionExpiresAt = &expiresAt
	env.SessionTTLRemainingSeconds = &remaining
	return env
}

func envelopeResult(env Envelope) (*mcp.CallToolResult, Envelope, error) {
	env.SummaryBlock = formatEnvelopeSummary(env)
	body, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return nil, Envelope{}, err
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(body)}},
	}, env, nil
}

func checkProgressTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "check_progress",
		Description: "Check that your in-progress work is staying aligned with the task spec. " +
			"Call this at natural checkpoints — after a meaningful chunk of code is written, " +
			"before moving to a new sub-area, or whenever you're unsure whether you're drifting.",
	}
}

type FileArg struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type CheckProgressArgs struct {
	SessionID         string    `json:"session_id"     jsonschema:"required"`
	WorkingOn         string    `json:"working_on"     jsonschema:"required"`
	ChangedFiles      []FileArg `json:"changed_files,omitempty"`
	Questions         []string  `json:"questions,omitempty"`
	ModelOverride     string    `json:"model_override,omitempty"`
	MaxTokensOverride int       `json:"max_tokens_override,omitempty"`
}

func (h *handlers) CheckProgress(ctx context.Context, _ *mcp.CallToolRequest, args CheckProgressArgs) (*mcp.CallToolResult, Envelope, error) {
	if args.SessionID == "" || args.WorkingOn == "" {
		return nil, Envelope{}, errors.New("session_id and working_on are required")
	}

	maxTokens, clamp, err := effectiveMaxTokens(args.MaxTokensOverride, h.deps.Cfg.PerTaskMaxTokens, h.deps.Cfg.MaxTokensCeiling)
	if err != nil {
		return nil, Envelope{}, err
	}

	sess, ok := h.deps.Sessions.Get(args.SessionID)
	if !ok {
		return envelopeResult(prependClamp(notFoundEnvelope(args.SessionID, h.deps.Cfg.MidModel), clamp))
	}

	if size := totalBytes(args.ChangedFiles); size > h.deps.Cfg.MaxPayloadBytes {
		return envelopeResult(prependClamp(tooLargeEnvelope(sess.ID, h.deps.Cfg.MidModel, size, h.deps.Cfg.MaxPayloadBytes,
			"Send a smaller changed_files set, or split the checkpoint into smaller chunks."), clamp))
	}

	model, err := h.resolveModel(args.ModelOverride, h.deps.Cfg.MidModel)
	if err != nil {
		return nil, Envelope{}, err
	}

	rendered, err := prompts.RenderMid(prompts.MidInput{
		Spec:          sess.Spec,
		PriorFindings: priorFindings(sess),
		WorkingOn:     args.WorkingOn,
		Files:         toPromptFiles(args.ChangedFiles),
		Questions:     args.Questions,
	})
	if err != nil {
		return nil, Envelope{}, fmt.Errorf("render mid prompt: %w", err)
	}

	result, modelUsed, ms, partialRaw, err := h.review(ctx, model, rendered, maxTokens)
	if err != nil {
		if errors.Is(err, providers.ErrResponseTruncated) {
			if env, ok := recoverPartialFindings(sess.ID, model, partialRaw, "ANTI_TANGENT_PER_TASK_MAX_TOKENS"); ok {
				env = prependClamp(env, clamp)
				env = h.withSessionTTL(env, sess)
				return envelopeResult(env)
			}
			env := truncatedEnvelope(sess.ID, model)
			env = prependClamp(env, clamp)
			env = h.withSessionTTL(env, sess)
			return envelopeResult(env)
		}
		return nil, Envelope{}, err
	}

	h.deps.Sessions.AppendCheckpoint(sess.ID, session.Checkpoint{
		At:        time.Now(),
		WorkingOn: args.WorkingOn,
		FileCount: len(args.ChangedFiles),
		Verdict:   result.Verdict,
		Findings:  result.Findings,
	})
	// Re-fetch after AppendCheckpoint so LastAccessed reflects the final mutation.
	if refreshed, ok := h.deps.Sessions.Get(sess.ID); ok {
		sess = refreshed
	}

	env := Envelope{
		SessionID:  sess.ID,
		Verdict:    string(result.Verdict),
		Findings:   result.Findings,
		NextAction: result.NextAction,
		ModelUsed:  modelUsed,
		ReviewMS:   ms,
	}
	env = prependClamp(env, clamp)
	env = h.withSessionTTL(env, sess)
	return envelopeResult(env)
}

func totalBytes(files []FileArg) int {
	n := 0
	for _, f := range files {
		n += len(f.Content) + len(f.Path)
	}
	return n
}

func toPromptFiles(files []FileArg) []prompts.File {
	out := make([]prompts.File, len(files))
	for i, f := range files {
		out[i] = prompts.File{Path: f.Path, Content: f.Content}
	}
	return out
}

func priorFindings(s *session.Session) []verdict.Finding {
	out := append([]verdict.Finding{}, s.PreFindings...)
	for _, cp := range s.Checkpoints {
		out = append(out, cp.Findings...)
	}
	return out
}

func notFoundEnvelope(id string, model config.ModelRef) Envelope {
	return Envelope{
		SessionID: id,
		Verdict:   string(verdict.VerdictFail),
		Findings: []verdict.Finding{{
			Severity:   verdict.SeverityCritical,
			Category:   verdict.CategorySessionMissing,
			Criterion:  "session",
			Evidence:   "session_id " + id + " not found or expired",
			Suggestion: "Call validate_task_spec first and use the returned session_id.",
		}},
		NextAction: "Call validate_task_spec first.",
		ModelUsed:  model.String(),
	}
}

func truncatedEnvelope(id string, model config.ModelRef) Envelope {
	return Envelope{
		SessionID: id,
		Verdict:   string(verdict.VerdictWarn),
		Findings: []verdict.Finding{{
			Severity:   verdict.SeverityMinor,
			Category:   verdict.CategoryOther,
			Criterion:  "reviewer_response",
			Evidence:   providers.ErrResponseTruncated.Error(),
			Suggestion: "Raise ANTI_TANGENT_PER_TASK_MAX_TOKENS or pass max_tokens_override and retry.",
		}},
		NextAction: "Retry with a higher max_tokens_override (or raise the configured max-tokens cap).",
		ModelUsed:  model.String(),
	}
}

const (
	maxPinnedByEntries = 50
	maxPinnedByChars   = 500
)

func normalizePhase(phase string) (string, error) {
	phase = strings.TrimSpace(phase)
	switch phase {
	case "", "pre":
		return "pre", nil
	case "post":
		return "post", nil
	default:
		return "", errors.New(`phase must be "pre" or "post"`)
	}
}

func normalizePinnedBy(entries []string) ([]string, error) {
	out := make([]string, 0, len(entries))
	for i, entry := range entries {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			continue
		}
		if len([]rune(trimmed)) > maxPinnedByChars {
			return nil, fmt.Errorf("pinned_by[%d] must be at most %d characters", i, maxPinnedByChars)
		}
		out = append(out, trimmed)
		if len(out) > maxPinnedByEntries {
			return nil, fmt.Errorf("pinned_by must contain at most %d entries", maxPinnedByEntries)
		}
	}
	return out, nil
}

// effectiveMaxTokens returns the max-tokens value to send to the provider,
// the optional clamp finding (zero value if no clamp occurred), and an
// error if the override is invalid.
//
//	override < 0          → return error (rejected at handler boundary)
//	override == 0         → use defaultMaxTokens; no clamp finding
//	override <= ceiling   → use override; no clamp finding
//	override > ceiling    → use ceiling; emit minor clamp finding
//
// Configured defaults are passed through unchanged when override==0 even if
// they exceed the ceiling — the ceiling only constrains caller-supplied
// override values for this single call.
func effectiveMaxTokens(override, defaultMaxTokens, ceiling int) (int, verdict.Finding, error) {
	if override < 0 {
		return 0, verdict.Finding{}, errors.New("max_tokens_override must be ≥ 0")
	}
	if override == 0 {
		return defaultMaxTokens, verdict.Finding{}, nil
	}
	if override <= ceiling {
		return override, verdict.Finding{}, nil
	}
	finding := verdict.Finding{
		Severity:   verdict.SeverityMinor,
		Category:   verdict.CategoryOther,
		Criterion:  "max_tokens_override",
		Evidence:   fmt.Sprintf("max_tokens_override (%d) exceeds ceiling (%d); used %d", override, ceiling, ceiling),
		Suggestion: "Raise ANTI_TANGENT_MAX_TOKENS_CEILING if you need a larger budget.",
	}
	return ceiling, finding, nil
}

// Adaptive default plan budget: base + per-task increment, bounded by the
// configured PlanMaxTokens floor and MaxTokensCeiling cap. Plan output scales
// roughly with task count (one block per task plus plan-level findings and
// summary), so a single 4096-token default fits small plans but truncates
// large ones. Constants are sourced from design §1.
const (
	planAdaptiveBase        = 2000
	planAdaptivePerTask     = 800
)

// adaptivePlanMaxTokens returns the max-tokens value for a validate_plan
// reviewer call WHEN no caller-supplied max_tokens_override is set. The
// formula is max(cfg.PlanMaxTokens, min(cfg.MaxTokensCeiling, base + per*tasks)).
// Adaptive bumps do not emit a clamp finding because they are not caller
// errors — callers asking for explicit overrides still route through
// effectiveMaxTokens at the ValidatePlan boundary.
func adaptivePlanMaxTokens(cfg config.Config, taskCount int) int {
	floor := planAdaptiveBase + planAdaptivePerTask*taskCount
	if floor > cfg.MaxTokensCeiling {
		floor = cfg.MaxTokensCeiling
	}
	if floor < cfg.PlanMaxTokens {
		floor = cfg.PlanMaxTokens
	}
	return floor
}

// prependClamp inserts the clamp finding at the head of the envelope's
// findings list if clamp is non-zero. Idempotent on the empty-clamp case.
// Centralises the clamp-composition rule so every handler flow (success,
// partial recovery, legacy truncation) treats it identically.
func prependClamp(env Envelope, clamp verdict.Finding) Envelope {
	if clamp.Severity == "" {
		return env
	}
	env.Findings = append([]verdict.Finding{clamp}, env.Findings...)
	return env
}

// prependPlanClamp is the PlanResult counterpart of prependClamp: it inserts
// the clamp finding at the head of pr.PlanFindings when clamp is non-zero.
func prependPlanClamp(pr verdict.PlanResult, clamp verdict.Finding) verdict.PlanResult {
	if clamp.Severity == "" {
		return pr
	}
	pr.PlanFindings = append([]verdict.Finding{clamp}, pr.PlanFindings...)
	return pr
}

// recoverPartialFindings attempts to extract complete findings from a
// truncated reviewer response. Returns (envelope, true) when at least one
// finding was recovered; (zero, false) when the caller should fall back to
// truncatedEnvelope.
//
// The returned envelope has Verdict="warn", Findings = recovered list plus a
// single minor "truncation marker" finding noting the count and referencing
// both envVar and max_tokens_override mitigations, Partial=true, and
// NextAction = the parsed result's next_action when non-empty, otherwise a
// generic fallback that points the caller at max_tokens_override.
func recoverPartialFindings(id string, model config.ModelRef, rawJSON []byte, envVar string) (Envelope, bool) {
	if len(rawJSON) == 0 {
		return Envelope{}, false
	}
	r, ok := verdict.ParseResultPartial(rawJSON)
	if !ok || len(r.Findings) == 0 {
		return Envelope{}, false
	}
	marker := verdict.Finding{
		Severity:   verdict.SeverityMinor,
		Category:   verdict.CategoryOther,
		Criterion:  "reviewer_response",
		Evidence:   fmt.Sprintf("reviewer output truncated at the max_tokens cap; %d complete findings recovered", len(r.Findings)),
		Suggestion: "Raise " + envVar + " or pass max_tokens_override on the next call to capture more.",
	}
	findings := append([]verdict.Finding{}, r.Findings...)
	findings = append(findings, marker)
	// AC: next_action MUST mention re-running with max_tokens_override. If the
	// reviewer returned a NextAction that already mentions it, preserve it;
	// otherwise append the mitigation hint (or supply a fallback if empty).
	next := r.NextAction
	switch {
	case next == "":
		next = "Address recovered findings; reviewer output was truncated. Re-call with a higher max_tokens_override (or raise " + envVar + ") to capture the full review."
	case !strings.Contains(next, "max_tokens_override"):
		next = next + " Reviewer output was truncated; re-call with a higher max_tokens_override (or raise " + envVar + ") to capture the full review."
	}
	return Envelope{
		SessionID:  id,
		Verdict:    string(verdict.VerdictWarn),
		Findings:   findings,
		NextAction: next,
		ModelUsed:  model.String(),
		Partial:    true,
	}, true
}

// truncatedPlanResult builds the synthetic PlanResult returned when a
// validate_plan reviewer call truncates AND no usable findings/tasks could be
// recovered from the partial bytes (the "no-analysis" path).
//
// Severity is major (not minor) because the caller received zero plan analysis
// and would otherwise mistake the result for a cosmetic concern. PlanQuality
// is set explicitly to rough so that ApplyPlanQualitySanity — which otherwise
// defaults a Warn verdict to actionable — does not silently upgrade a
// no-analysis response. The Suggestion and NextAction name all three retry
// knobs so the caller can self-diagnose without rereading docs.
//
// Partial-recovery truncation markers (emitted by recoverPartialPlanFindings)
// remain minor on purpose: those callers received at least some review signal.
func truncatedPlanResult() verdict.PlanResult {
	return verdict.PlanResult{
		PlanVerdict: verdict.VerdictWarn,
		PlanQuality: verdict.PlanQualityRough,
		PlanFindings: []verdict.Finding{{
			Severity:   verdict.SeverityMajor,
			Category:   verdict.CategoryOther,
			Criterion:  "reviewer_response",
			Evidence:   providers.ErrResponseTruncated.Error(),
			Suggestion: "Retry with max_tokens_override >= 16000, set ANTI_TANGENT_PLAN_MAX_TOKENS in the MCP server env, or raise ANTI_TANGENT_MAX_TOKENS_CEILING if overrides are clamped.",
		}},
		Tasks:      []verdict.PlanTaskResult{},
		NextAction: "Retry with max_tokens_override >= 16000, or set ANTI_TANGENT_PLAN_MAX_TOKENS in the MCP server env. If overrides are clamped, raise ANTI_TANGENT_MAX_TOKENS_CEILING.",
	}
}

// recoverPartialPlanFindings attempts to extract complete plan findings and
// tasks from a truncated reviewer response. Returns (planResult, true) when
// at least one complete finding or task was recovered anywhere in the
// structure OR the supplied `prior` PlanResult already carries findings/tasks
// to preserve; (zero, false) otherwise.
//
// The optional `prior` argument carries plan-level findings and task results
// that were already collected before the truncation point — e.g. the Pass-1
// `plan_findings` and any complete Pass-2 chunk task results accumulated in
// the chunked path. Prior plan_findings are prepended to recovered ones; prior
// tasks are prepended to recovered tasks (de-duped by task_index, with prior
// winning on collisions since the prior copy is the complete one).
//
// The returned PlanResult has Partial=true, a single minor "truncation
// marker" finding appended to PlanFindings noting the total merged count
// across plan and tasks, and a NextAction that either preserves the parsed
// value (when non-empty) or falls back to a generic message pointing the
// caller at ANTI_TANGENT_PLAN_MAX_TOKENS / max_tokens_override.
func recoverPartialPlanFindings(rawJSON []byte, prior verdict.PlanResult) (verdict.PlanResult, bool) {
	pr, parsedOK := verdict.PlanResult{}, false
	if len(rawJSON) > 0 {
		pr, parsedOK = verdict.ParsePlanResultPartial(rawJSON)
	}
	priorHasContent := len(prior.PlanFindings) > 0 || len(prior.Tasks) > 0
	if !parsedOK && !priorHasContent {
		return verdict.PlanResult{}, false
	}
	// Merge prior into pr. Prior findings/tasks come first so they appear
	// before anything salvaged from the truncating chunk's partial bytes.
	if priorHasContent {
		pr.PlanFindings = append(append([]verdict.Finding{}, prior.PlanFindings...), pr.PlanFindings...)
		// De-dupe tasks by TaskIndex: prior wins on collision because prior
		// task results came from cleanly-closed chunks.
		seen := make(map[int]struct{}, len(prior.Tasks))
		merged := make([]verdict.PlanTaskResult, 0, len(prior.Tasks)+len(pr.Tasks))
		for _, t := range prior.Tasks {
			seen[t.TaskIndex] = struct{}{}
			merged = append(merged, t)
		}
		for _, t := range pr.Tasks {
			if _, dup := seen[t.TaskIndex]; dup {
				continue
			}
			merged = append(merged, t)
		}
		pr.Tasks = merged
		// Prefer the prior PlanVerdict/NextAction when pr is empty (the
		// truncating chunk may not have re-emitted them).
		if pr.PlanVerdict == "" {
			pr.PlanVerdict = prior.PlanVerdict
		}
		if pr.NextAction == "" {
			pr.NextAction = prior.NextAction
		}
	}
	count := len(pr.PlanFindings)
	for _, t := range pr.Tasks {
		count += len(t.Findings)
	}
	pr.PlanFindings = append(pr.PlanFindings, verdict.Finding{
		Severity:   verdict.SeverityMinor,
		Category:   verdict.CategoryOther,
		Criterion:  "reviewer_response",
		Evidence:   fmt.Sprintf("reviewer output truncated at the max_tokens cap; %d complete findings recovered across plan and tasks", count),
		Suggestion: "Raise ANTI_TANGENT_PLAN_MAX_TOKENS or pass max_tokens_override on the next call to capture more.",
	})
	// Mirror the per-task helper's contract: NextAction MUST mention
	// re-running with max_tokens_override. Preserve a non-empty
	// reviewer-supplied NextAction but append the mitigation hint when it
	// doesn't already mention the override.
	switch {
	case pr.NextAction == "":
		pr.NextAction = "Address recovered findings; reviewer output was truncated. Re-call with a higher max_tokens_override (or raise ANTI_TANGENT_PLAN_MAX_TOKENS) to capture the full review."
	case !strings.Contains(pr.NextAction, "max_tokens_override"):
		pr.NextAction = pr.NextAction + " Reviewer output was truncated; re-call with a higher max_tokens_override (or raise ANTI_TANGENT_PLAN_MAX_TOKENS) to capture the full review."
	}
	// Ensure Tasks is non-nil so JSON marshaling emits [] rather than null.
	if pr.Tasks == nil {
		pr.Tasks = []verdict.PlanTaskResult{}
	}
	pr.Partial = true
	return pr, true
}

func tooLargeEnvelope(id string, model config.ModelRef, size, limit int, suggestion string) Envelope {
	return Envelope{
		SessionID: id,
		Verdict:   string(verdict.VerdictFail),
		Findings: []verdict.Finding{{
			Severity:   verdict.SeverityMajor,
			Category:   verdict.CategoryTooLarge,
			Criterion:  "payload",
			Evidence:   fmt.Sprintf("payload %d bytes exceeds cap %d", size, limit),
			Suggestion: suggestion,
		}},
		NextAction: "Reduce the payload and retry.",
		ModelUsed:  model.String(),
	}
}

func validateCompletionTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "validate_completion",
		Description: "Final validation before declaring a task complete. " +
			"The reviewer checks the full implementation against every acceptance criterion " +
			"and non-goal. Treat any `fail` or `warn` findings as work to do before claiming done.",
	}
}

type ValidateCompletionArgs struct {
	SessionID         string    `json:"session_id"  jsonschema:"required"`
	Summary           string    `json:"summary"     jsonschema:"required"`
	FinalFiles        []FileArg `json:"final_files,omitempty"`
	FinalDiff         string    `json:"final_diff,omitempty"`
	TestEvidence      string    `json:"test_evidence,omitempty"`
	ModelOverride     string    `json:"model_override,omitempty"`
	MaxTokensOverride int       `json:"max_tokens_override,omitempty"`
}

// ValidatePlanArgs is the input schema for the plan-level reviewer.
type ValidatePlanArgs struct {
	PlanText          string `json:"plan_text"      jsonschema:"required"`
	ModelOverride     string `json:"model_override,omitempty"`
	MaxTokensOverride int    `json:"max_tokens_override,omitempty"`
	Mode              string `json:"mode,omitempty"`
}

func validatePlanTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "validate_plan",
		Description: "Validate an implementation plan as a whole BEFORE dispatching subagents to implement individual tasks. " +
			"Returns per-task findings and ready-to-paste structured headers (Goal / Acceptance criteria / Non-goals / Context) for tasks that lack them. " +
			"Call this once at plan-handoff time; the per-task `validate_task_spec` is still called by each implementing subagent at task start.",
	}
}

func totalCompletionBytes(files []FileArg, finalDiff string) int {
	return totalBytes(files) + len(finalDiff)
}

// evidenceTruncationPatterns are case-insensitive substrings that strongly
// indicate the caller pasted truncated/elided evidence rather than a complete
// diff or full file contents. The reviewer cannot verify acceptance criteria
// against placeholder text, so the guard rejects these submissions before the
// reviewer call to fail-fast with a clear error.
//
// The list is intentionally narrow: only patterns that have negligible chance
// of appearing in legitimate code or diffs. "diff --git with zero @@" is NOT
// included — it false-fires on mode-only / rename-only / binary diffs.
var evidenceTruncationPatterns = []string{
	"(truncated)",
	"[truncated]",
	"// ... unchanged",
	"<!-- truncated -->",
}

// evidenceEllipsisLine matches a line that contains only `...` (with optional
// surrounding whitespace). The (?m) flag anchors ^/$ to line boundaries.
var evidenceEllipsisLine = regexp.MustCompile(`(?m)^\s*\.\.\.\s*$`)

// checkEvidenceShape inspects args for malformed evidence shapes. Returns a
// non-empty human-readable reason string when a rule fires; empty string when
// the evidence looks structurally sound. The reason is what populates the
// rejection finding's Evidence field.
//
// Order of checks (fail-fast on the first hit so the reason points at the
// most-likely cause):
//  1. final_diff substring + ellipsis-line scan
//  2. final_files empty Path
//  3. final_files content substring + ellipsis-line scan
func checkEvidenceShape(args ValidateCompletionArgs) string {
	if args.FinalDiff != "" {
		lower := strings.ToLower(args.FinalDiff)
		for _, p := range evidenceTruncationPatterns {
			if idx := strings.Index(lower, p); idx >= 0 {
				return fmt.Sprintf("final_diff contains truncation marker %q at offset %d", p, idx)
			}
		}
		if loc := evidenceEllipsisLine.FindStringIndex(args.FinalDiff); loc != nil {
			return fmt.Sprintf("final_diff contains a placeholder line `...` at offset %d", loc[0])
		}
	}
	for i, f := range args.FinalFiles {
		if strings.TrimSpace(f.Path) == "" {
			return fmt.Sprintf("final_files[%d].path is empty", i)
		}
	}
	for i, f := range args.FinalFiles {
		lower := strings.ToLower(f.Content)
		for _, p := range evidenceTruncationPatterns {
			if idx := strings.Index(lower, p); idx >= 0 {
				return fmt.Sprintf("final_files[%d].content (path %q) contains truncation marker %q at offset %d", i, f.Path, p, idx)
			}
		}
		if loc := evidenceEllipsisLine.FindStringIndex(f.Content); loc != nil {
			return fmt.Sprintf("final_files[%d].content (path %q) contains a placeholder line `...` at offset %d", i, f.Path, loc[0])
		}
	}
	return ""
}

// rejectionCacheEntry is one cached rejection envelope keyed by canonical
// content hash. The envelope's ReviewMS field is preserved so cache-hit
// rejections look identical to the original rejection from the caller's POV.
type rejectionCacheEntry struct {
	envelope  Envelope
	expiresAt time.Time
}

// rejectionCache stores recent malformed-evidence rejections so repeat
// submissions of the same broken payload don't re-run the (cheap but still
// non-zero) guard logic and don't pollute logs. In-process, no persistence.
var (
	rejectionCacheMu sync.Mutex
	rejectionCache   = map[[32]byte]rejectionCacheEntry{}
)

const rejectionCacheTTL = 5 * time.Minute

// evidenceCacheKey returns a deterministic SHA-256 over a canonical JSON
// encoding of the rejection-relevant args. final_files is pre-sorted by Path
// so that an order-only difference between two otherwise-identical submissions
// still hits the cache. Plain string concatenation would risk collisions
// (e.g. SessionID="a" + FinalDiff="bc" vs SessionID="ab" + FinalDiff="c");
// JSON-encoded boundaries make those distinct.
func evidenceCacheKey(args ValidateCompletionArgs) [32]byte {
	sortedFiles := append([]FileArg(nil), args.FinalFiles...)
	sort.Slice(sortedFiles, func(i, j int) bool { return sortedFiles[i].Path < sortedFiles[j].Path })
	keyInput := struct {
		SessionID    string    `json:"session_id"`
		FinalDiff    string    `json:"final_diff"`
		FinalFiles   []FileArg `json:"final_files"`
		TestEvidence string    `json:"test_evidence"`
	}{
		SessionID:    args.SessionID,
		FinalDiff:    args.FinalDiff,
		FinalFiles:   sortedFiles,
		TestEvidence: args.TestEvidence,
	}
	keyJSON, _ := json.Marshal(keyInput)
	return sha256.Sum256(keyJSON)
}

// lookupCachedRejection returns the cached rejection envelope and true when a
// non-expired entry exists for key; otherwise zero/false. Expired entries are
// evicted on lookup.
func lookupCachedRejection(key [32]byte) (Envelope, bool) {
	rejectionCacheMu.Lock()
	defer rejectionCacheMu.Unlock()
	entry, ok := rejectionCache[key]
	if !ok {
		return Envelope{}, false
	}
	if time.Now().After(entry.expiresAt) {
		delete(rejectionCache, key)
		return Envelope{}, false
	}
	return entry.envelope, true
}

// storeRejection caches env under key with a freshly-computed expiry.
func storeRejection(key [32]byte, env Envelope) {
	rejectionCacheMu.Lock()
	defer rejectionCacheMu.Unlock()
	now := time.Now()
	for k, v := range rejectionCache {
		if now.After(v.expiresAt) {
			delete(rejectionCache, k)
		}
	}
	rejectionCache[key] = rejectionCacheEntry{
		envelope:  env,
		expiresAt: now.Add(rejectionCacheTTL),
	}
}

// malformedEvidenceEnvelope builds the rejection envelope for a guard hit.
// Severity is major (not critical) because the caller can almost always fix
// the submission by re-sending complete evidence; the work isn't necessarily
// wrong, just unverifiable.
func malformedEvidenceEnvelope(sessionID, reason, modelUsed string) Envelope {
	return Envelope{
		SessionID: sessionID,
		Verdict:   string(verdict.VerdictFail),
		Findings: []verdict.Finding{{
			Severity:   verdict.SeverityMajor,
			Category:   verdict.CategoryMalformedEvidence,
			Criterion:  "evidence_shape",
			Evidence:   reason,
			Suggestion: "Submit full file contents in final_files, or a complete unified diff (no truncation markers) in final_diff.",
		}},
		NextAction: "Re-submit with complete evidence; current submission appears truncated.",
		ModelUsed:  modelUsed,
	}
}

// ValidateCompletion runs the post-implementation reviewer call. Eight-step
// ordering (preserved here to keep the AC-mapping legible):
//
//  1. summary required check
//  2. at-least-one-evidence check
//  3. lightweight marker (empty session_id + non-empty evidence)
//  4. effectiveMaxTokens + clampFinding
//  5. payload-cap check
//  6. evidence-shape guard (with rejection cache) — runs BEFORE session lookup
//  7. session lookup (skipped in lightweight mode)
//  8. spec selection — synthesized in lightweight mode, sess.Spec otherwise
//
// In lightweight mode the handler synthesizes a minimal TaskSpec and does NOT
// create or update any session in the store. The returned envelope's
// SessionID/SessionExpiresAt/SessionTTLRemainingSeconds fields stay zero.
func (h *handlers) ValidateCompletion(ctx context.Context, _ *mcp.CallToolRequest, args ValidateCompletionArgs) (*mcp.CallToolResult, Envelope, error) {
	// 1. summary required (session_id is no longer required — see step 3).
	if args.Summary == "" {
		return nil, Envelope{}, errors.New("summary is required")
	}

	// 2. at-least-one-evidence: rejects the "totally empty call" case
	// regardless of whether session_id is set.
	if len(args.FinalFiles) == 0 && args.FinalDiff == "" && args.TestEvidence == "" {
		return nil, Envelope{}, errors.New("validate_completion: at least one of final_files, final_diff, or test_evidence must be non-empty")
	}

	// 3. lightweight marker.
	lightweight := args.SessionID == ""

	// 4. max-tokens override + clamp finding.
	maxTokens, clamp, err := effectiveMaxTokens(args.MaxTokensOverride, h.deps.Cfg.PerTaskMaxTokens, h.deps.Cfg.MaxTokensCeiling)
	if err != nil {
		return nil, Envelope{}, err
	}

	// 5. payload-cap check. In lightweight mode the surfaced session_id stays
	// empty; otherwise we don't have the session yet, so use args.SessionID.
	if size := totalCompletionBytes(args.FinalFiles, args.FinalDiff); size > h.deps.Cfg.MaxPayloadBytes {
		return envelopeResult(prependClamp(tooLargeEnvelope(args.SessionID, h.deps.Cfg.PostModel, size, h.deps.Cfg.MaxPayloadBytes,
			"Send a unified diff via final_diff, or split the call into smaller chunks."), clamp))
	}

	// 6. evidence-shape guard. Runs BEFORE session lookup so a broken payload
	// rejects fast regardless of session state. Cache hit → return the same
	// envelope without re-running the guard or hitting the reviewer.
	cacheKey := evidenceCacheKey(args)
	if cached, ok := lookupCachedRejection(cacheKey); ok {
		return envelopeResult(prependClamp(cached, clamp))
	}
	if reason := checkEvidenceShape(args); reason != "" {
		env := malformedEvidenceEnvelope(args.SessionID, reason, h.deps.Cfg.PostModel.String())
		storeRejection(cacheKey, env)
		return envelopeResult(prependClamp(env, clamp))
	}

	// 7/8. session lookup + spec selection.
	var sess *session.Session
	var spec session.TaskSpec
	var sessID string
	if lightweight {
		// Synthesize a minimal spec for the reviewer. No session is created.
		spec = session.TaskSpec{
			Title: "(lightweight task)",
			Goal:  args.Summary,
		}
	} else {
		var ok bool
		sess, ok = h.deps.Sessions.Get(args.SessionID)
		if !ok {
			return envelopeResult(prependClamp(notFoundEnvelope(args.SessionID, h.deps.Cfg.PostModel), clamp))
		}
		spec = sess.Spec
		sessID = sess.ID
	}

	model, err := h.resolveModel(args.ModelOverride, h.deps.Cfg.PostModel)
	if err != nil {
		return nil, Envelope{}, err
	}

	rendered, err := prompts.RenderPost(prompts.PostInput{
		Spec:         spec,
		Summary:      args.Summary,
		Files:        toPromptFiles(args.FinalFiles),
		FinalDiff:    args.FinalDiff,
		TestEvidence: args.TestEvidence,
	})
	if err != nil {
		return nil, Envelope{}, fmt.Errorf("render post prompt: %w", err)
	}

	result, modelUsed, ms, partialRaw, err := h.review(ctx, model, rendered, maxTokens)
	if err != nil {
		if errors.Is(err, providers.ErrResponseTruncated) {
			if env, ok := recoverPartialFindings(sessID, model, partialRaw, "ANTI_TANGENT_PER_TASK_MAX_TOKENS"); ok {
				env = prependClamp(env, clamp)
				if !lightweight {
					env = h.withSessionTTL(env, sess)
				}
				return envelopeResult(env)
			}
			env := truncatedEnvelope(sessID, model)
			env = prependClamp(env, clamp)
			if !lightweight {
				env = h.withSessionTTL(env, sess)
			}
			return envelopeResult(env)
		}
		return nil, Envelope{}, err
	}

	if !lightweight {
		h.deps.Sessions.SetPostFindings(sess.ID, result.Findings)
		// Re-fetch after SetPostFindings so LastAccessed reflects the final mutation.
		if refreshed, ok := h.deps.Sessions.Get(sess.ID); ok {
			sess = refreshed
		}
		sessID = sess.ID
	}

	env := Envelope{
		SessionID:  sessID,
		Verdict:    string(result.Verdict),
		Findings:   result.Findings,
		NextAction: result.NextAction,
		ModelUsed:  modelUsed,
		ReviewMS:   ms,
	}
	env = prependClamp(env, clamp)
	if !lightweight {
		env = h.withSessionTTL(env, sess)
	}
	return envelopeResult(env)
}

func (h *handlers) ValidatePlan(ctx context.Context, _ *mcp.CallToolRequest, args ValidatePlanArgs) (*mcp.CallToolResult, verdict.PlanResult, error) {
	if args.PlanText == "" {
		return nil, verdict.PlanResult{}, errors.New("plan_text is required")
	}
	if args.Mode != "" && args.Mode != "quick" && args.Mode != "thorough" {
		return nil, verdict.PlanResult{}, errors.New(`mode must be "quick" or "thorough"`)
	}

	maxTokens, clamp, err := effectiveMaxTokens(args.MaxTokensOverride, h.deps.Cfg.PlanMaxTokens, h.deps.Cfg.MaxTokensCeiling)
	if err != nil {
		return nil, verdict.PlanResult{}, err
	}

	if size := len(args.PlanText); size > h.deps.Cfg.MaxPayloadBytes {
		return planEnvelopeResult(prependPlanClamp(tooLargePlanResult(size, h.deps.Cfg.MaxPayloadBytes), clamp), h.deps.Cfg.PlanModel.String(), 0)
	}
	tasks, _ := planparser.SplitTasks(args.PlanText)
	if len(tasks) == 0 {
		return planEnvelopeResult(prependPlanClamp(noHeadingsPlanResult(), clamp), h.deps.Cfg.PlanModel.String(), 0)
	}

	// Adaptive plan budget: apply only when no override was supplied. The
	// early effectiveMaxTokens call above already validated/clamped explicit
	// overrides and attached the clamp finding for payload-too-large and
	// no-headings early exits; we must not disturb that path.
	if args.MaxTokensOverride == 0 {
		maxTokens = adaptivePlanMaxTokens(h.deps.Cfg, len(tasks))
	}

	model, err := h.resolveModel(args.ModelOverride, h.deps.Cfg.PlanModel)
	if err != nil {
		return nil, verdict.PlanResult{}, err
	}

	var pr verdict.PlanResult
	var modelUsed string
	var ms int64
	var partialRaw []byte
	if len(tasks) <= h.deps.Cfg.PlanTasksPerChunk {
		pr, modelUsed, ms, partialRaw, err = h.reviewPlanSingle(ctx, model, args.PlanText, maxTokens, args.Mode)
	} else {
		pr, modelUsed, ms, partialRaw, err = h.reviewPlanChunked(ctx, model, args.PlanText, tasks, h.deps.Cfg.PlanTasksPerChunk, maxTokens, args.Mode)
	}
	if err != nil {
		if errors.Is(err, providers.ErrResponseTruncated) {
			// `pr` carries any partial state already collected before the
			// truncation point — for the chunked path that means Pass-1
			// plan_findings plus any cleanly-closed Pass-2 chunk task
			// results. Pass it as `prior` so those aren't dropped if the
			// truncating chunk's bytes yield further recovery.
			if recovered, ok := recoverPartialPlanFindings(partialRaw, pr); ok {
				recovered = prependPlanClamp(recovered, clamp)
				return planEnvelopeResult(recovered, model.String(), 0)
			}
			truncated := truncatedPlanResult()
			truncated = prependPlanClamp(truncated, clamp)
			return planEnvelopeResult(truncated, model.String(), 0)
		}
		return nil, verdict.PlanResult{}, err
	}
	pr = prependPlanClamp(pr, clamp)
	return planEnvelopeResult(pr, modelUsed, ms)
}

// reviewPlanSingle runs one reviewer call for the entire plan — the
// behavior used today for plans whose task count is at or below
// h.deps.Cfg.PlanTasksPerChunk. Renders the prompt internally.
// On ErrResponseTruncated, the returned []byte carries the partial response
// bytes (possibly empty if the provider returned none) so the caller can
// attempt partial-findings recovery via recoverPartialPlanFindings.
func (h *handlers) reviewPlanSingle(ctx context.Context, model config.ModelRef, planText string, maxTokens int, mode string) (verdict.PlanResult, string, int64, []byte, error) {
	rendered, err := prompts.RenderPlan(prompts.PlanInput{PlanText: planText, Mode: mode})
	if err != nil {
		return verdict.PlanResult{}, "", 0, nil, fmt.Errorf("render plan prompt: %w", err)
	}
	rv, err := h.deps.Reviews.Get(model.Provider)
	if err != nil {
		return verdict.PlanResult{}, "", 0, nil, err
	}
	start := time.Now()
	req := providers.Request{
		Model:      model.Model,
		System:     rendered.System,
		User:       rendered.User,
		MaxTokens:  maxTokens,
		JSONSchema: verdict.PlanSchema(),
	}
	resp, err := rv.Review(ctx, req)
	if err != nil {
		if errors.Is(err, providers.ErrResponseTruncated) {
			return verdict.PlanResult{}, "", 0, resp.RawJSON, err
		}
		return verdict.PlanResult{}, "", 0, nil, err
	}
	r, err := verdict.ParsePlan(resp.RawJSON)
	if err != nil {
		// One retry with explicit reminder.
		req.User = rendered.User + "\n\n" + verdict.RetryHint()
		resp, err = rv.Review(ctx, req)
		if err != nil {
			if errors.Is(err, providers.ErrResponseTruncated) {
				return verdict.PlanResult{}, "", 0, resp.RawJSON, err
			}
			return verdict.PlanResult{}, "", 0, nil, err
		}
		r, err = verdict.ParsePlan(resp.RawJSON)
		if err != nil {
			return verdict.PlanResult{}, "", 0, nil, fmt.Errorf("plan provider response failed schema after retry: %w", err)
		}
	}
	modelUsed := model.Provider + ":" + resp.Model
	if resp.Model == "" {
		modelUsed = model.String()
	}
	return r, modelUsed, time.Since(start).Milliseconds(), nil, nil
}

func noHeadingsPlanResult() verdict.PlanResult {
	return verdict.PlanResult{
		PlanVerdict: verdict.VerdictFail,
		PlanFindings: []verdict.Finding{{
			Severity:   verdict.SeverityCritical,
			Category:   verdict.CategoryOther,
			Criterion:  "structure",
			Evidence:   "no `### Task N:` headings detected",
			Suggestion: "use `### Task N: Title` for each task; this tool expects numbered tasks",
		}},
		Tasks:      []verdict.PlanTaskResult{},
		NextAction: "Add `### Task N: Title` headings for each task and re-run validate_plan.",
	}
}

func tooLargePlanResult(size, limit int) verdict.PlanResult {
	return verdict.PlanResult{
		PlanVerdict: verdict.VerdictFail,
		PlanFindings: []verdict.Finding{{
			Severity:   verdict.SeverityMajor,
			Category:   verdict.CategoryTooLarge,
			Criterion:  "payload",
			Evidence:   fmt.Sprintf("plan_text %d bytes exceeds cap %d", size, limit),
			Suggestion: "Split the plan into smaller chunks or pass a unified diff.",
		}},
		Tasks:      []verdict.PlanTaskResult{},
		NextAction: "Reduce plan_text size and retry.",
	}
}

// planEnvelopeResult marshals the PlanResult into a CallToolResult (mirrors envelopeResult).
//
// Two universal post-processing steps run here so every exit path — happy,
// partial-recovery, legacy-truncation, too-large, no-headings — gets them
// for free:
//
//  1. verdict.ApplyPlanQualitySanity normalizes plan_quality (handles
//     synthetic PlanResults that bypass ParsePlan / ParsePlanResultPartial).
//  2. SummaryBlock is populated with the rendered paste-ready text block.
func planEnvelopeResult(pr verdict.PlanResult, modelUsed string, ms int64) (*mcp.CallToolResult, verdict.PlanResult, error) {
	verdict.ApplyPlanQualitySanity(&pr)
	pr.SummaryBlock = formatPlanSummary(pr, modelUsed, ms)
	body, err := json.MarshalIndent(struct {
		verdict.PlanResult
		ModelUsed string `json:"model_used"`
		ReviewMS  int64  `json:"review_ms"`
	}{pr, modelUsed, ms}, "", "  ")
	if err != nil {
		return nil, verdict.PlanResult{}, err
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(body)}},
	}, pr, nil
}

// reviewPlanChunked runs Pass 1 (plan-findings-only) plus one per-chunk call
// per ceil(len(tasks)/chunkSize) batches of tasks. Each per-chunk call carries
// the full plan as context but instructs the reviewer to emit results only for
// the tasks in the chunk. Results merge into a PlanResult identical in shape
// to the single-call path.
//
// On ErrResponseTruncated, the returned []byte carries the partial response
// bytes from the FIRST call that truncated. This is best-effort: if Pass 1
// truncates, the bytes can yield a plan_findings list; if a per-chunk Pass
// 2..K+1 truncates, the bytes can yield a partial tasks[] list for that
// chunk only. The chunked path does not aggregate partial bytes across
// multiple successful calls — that is a follow-up.
func (h *handlers) reviewPlanChunked(
	ctx context.Context,
	model config.ModelRef,
	planText string,
	tasks []planparser.RawTask,
	chunkSize int,
	maxTokens int,
	mode string,
) (verdict.PlanResult, string, int64, []byte, error) {
	if chunkSize <= 0 {
		// Defense-in-depth: config.Load already rejects PlanTasksPerChunk <= 0
		// at startup, but a zero/negative chunkSize would turn the loop below
		// into an infinite spin. Fail loudly instead.
		return verdict.PlanResult{}, "", 0, nil, fmt.Errorf("reviewPlanChunked: chunkSize must be positive, got %d", chunkSize)
	}
	rv, err := h.deps.Reviews.Get(model.Provider)
	if err != nil {
		return verdict.PlanResult{}, "", 0, nil, err
	}

	var totalMs int64
	var modelUsed string

	// ----- Pass 1: plan-findings only -----
	rendered, err := prompts.RenderPlanFindingsOnly(prompts.PlanInput{PlanText: planText, Mode: mode})
	if err != nil {
		return verdict.PlanResult{}, "", 0, nil, fmt.Errorf("render plan_findings_only: %w", err)
	}
	req := providers.Request{
		Model:      model.Model,
		System:     rendered.System,
		User:       rendered.User,
		MaxTokens:  maxTokens,
		JSONSchema: verdict.PlanFindingsOnlySchema(),
	}
	start := time.Now()
	resp, err := rv.Review(ctx, req)
	if err != nil {
		if errors.Is(err, providers.ErrResponseTruncated) {
			return verdict.PlanResult{}, "", 0, resp.RawJSON, err
		}
		return verdict.PlanResult{}, "", 0, nil, err
	}
	pf, err := verdict.ParsePlanFindingsOnly(resp.RawJSON)
	if err != nil {
		req.User = rendered.User + "\n\n" + verdict.RetryHint()
		resp, err = rv.Review(ctx, req)
		if err != nil {
			if errors.Is(err, providers.ErrResponseTruncated) {
				return verdict.PlanResult{}, "", 0, resp.RawJSON, err
			}
			return verdict.PlanResult{}, "", 0, nil, err
		}
		pf, err = verdict.ParsePlanFindingsOnly(resp.RawJSON)
		if err != nil {
			return verdict.PlanResult{}, "", 0, nil, fmt.Errorf("plan_findings_only failed schema after retry: %w", err)
		}
	}
	totalMs += time.Since(start).Milliseconds()
	modelUsed = model.Provider + ":" + resp.Model
	if resp.Model == "" {
		modelUsed = model.String()
	}

	result := verdict.PlanResult{
		PlanVerdict:  pf.PlanVerdict,
		PlanFindings: pf.PlanFindings,
		NextAction:   pf.NextAction,
		PlanQuality:  pf.PlanQuality,
		Tasks:        make([]verdict.PlanTaskResult, 0, len(tasks)),
	}

	// ----- Passes 2..K+1: per-task chunks -----
	n := len(tasks)
	for i := 0; i < n; i += chunkSize {
		end := i + chunkSize
		if end > n {
			end = n
		}
		chunkTasks := tasks[i:end]

		chunkResult, ms, partialRaw, err := h.reviewOnePlanChunk(ctx, rv, model, planText, chunkTasks, maxTokens, mode)
		if err != nil {
			if errors.Is(err, providers.ErrResponseTruncated) {
				// Return the partially-built result (Pass-1 plan_findings plus
				// any complete chunk task results accumulated so far) so the
				// caller can merge it with anything recoverable from the
				// truncating chunk's partial bytes. Without this, the Pass-1
				// findings would be silently dropped.
				totalMs += ms
				return result, modelUsed, totalMs, partialRaw, err
			}
			return verdict.PlanResult{}, "", 0, nil, err
		}
		totalMs += ms
		result.Tasks = append(result.Tasks, chunkResult.Tasks...)
	}

	if len(result.Tasks) != len(tasks) {
		return verdict.PlanResult{}, "", 0, nil,
			fmt.Errorf("chunked plan review returned %d task results, expected %d",
				len(result.Tasks), len(tasks))
	}

	return result, modelUsed, totalMs, nil, nil
}

// reviewOnePlanChunk runs one per-chunk reviewer call with identity validation
// and the existing schema-retry-once pattern.
//
// On ErrResponseTruncated, the returned []byte carries the partial response
// bytes from whichever attempt truncated (first or retry) so the caller can
// attempt partial-findings recovery. Non-truncation errors return a nil
// []byte and are wrapped in the usual "plan_tasks_chunk failed after retry"
// message.
func (h *handlers) reviewOnePlanChunk(
	ctx context.Context,
	rv providers.Reviewer,
	model config.ModelRef,
	planText string,
	chunkTasks []planparser.RawTask,
	maxTokens int,
	mode string,
) (verdict.TasksOnly, int64, []byte, error) {
	rendered, err := prompts.RenderPlanTasksChunk(prompts.PlanChunkInput{
		PlanText:   planText,
		ChunkTasks: chunkTasks,
		Mode:       mode,
	})
	if err != nil {
		return verdict.TasksOnly{}, 0, nil, fmt.Errorf("render plan_tasks_chunk: %w", err)
	}

	req := providers.Request{
		Model:      model.Model,
		System:     rendered.System,
		User:       rendered.User,
		MaxTokens:  maxTokens,
		JSONSchema: verdict.TasksOnlySchema(),
	}

	// attempt mutates req.User in place; each call overwrites it before
	// Review, so the retry sees the hint-augmented body cleanly. On
	// ErrResponseTruncated, the returned []byte carries resp.RawJSON.
	attempt := func(user string) (verdict.TasksOnly, int64, []byte, error) {
		req.User = user
		start := time.Now()
		resp, err := rv.Review(ctx, req)
		if err != nil {
			if errors.Is(err, providers.ErrResponseTruncated) {
				return verdict.TasksOnly{}, time.Since(start).Milliseconds(), resp.RawJSON, err
			}
			return verdict.TasksOnly{}, 0, nil, err
		}
		ms := time.Since(start).Milliseconds()
		parsed, err := verdict.ParseTasksOnly(resp.RawJSON)
		if err != nil {
			return verdict.TasksOnly{}, ms, nil, err
		}
		if err := validateChunkIdentity(parsed, chunkTasks); err != nil {
			return verdict.TasksOnly{}, ms, nil, err
		}
		return parsed, ms, nil, nil
	}

	parsed, ms, partialRaw, err := attempt(rendered.User)
	if err == nil {
		return parsed, ms, nil, nil
	}
	// Truncation on the first attempt: surface partial bytes immediately
	// rather than retry — the retry would just be cut off the same way.
	if errors.Is(err, providers.ErrResponseTruncated) {
		return verdict.TasksOnly{}, ms, partialRaw, err
	}
	// Schema or identity failure → retry once with hint.
	parsed2, ms2, partialRaw2, err2 := attempt(rendered.User + "\n\n" + verdict.RetryHint())
	if err2 != nil {
		if errors.Is(err2, providers.ErrResponseTruncated) {
			return verdict.TasksOnly{}, ms + ms2, partialRaw2, err2
		}
		return verdict.TasksOnly{}, ms + ms2, nil, fmt.Errorf("plan_tasks_chunk failed after retry: %w", err2)
	}
	return parsed2, ms + ms2, nil, nil
}

// taskPrefixRe matches a leading "Task <number>: " prefix (with optional
// trailing whitespace) so we can normalize reviewer-returned task_title values
// that drop the prefix compared to the planparser.RawTask.Title form.
var taskPrefixRe = regexp.MustCompile(`^Task \d+:\s*`)

// normalizeTaskTitle trims surrounding whitespace then removes a single leading
// "Task N: " prefix if present. Comparison is case-sensitive after normalization.
func normalizeTaskTitle(s string) string {
	return taskPrefixRe.ReplaceAllString(strings.TrimSpace(s), "")
}

// validateChunkIdentity checks that the parsed chunk response contains exactly
// the expected tasks **in the same order** as chunkTasks: count match, each
// position's task_title equals the corresponding chunkTasks[i].Title (after
// normalizing both sides by trimming whitespace and removing any leading
// "Task N: " prefix), and no normalized title appears more than once.
// Mismatch and duplicate errors report the original (un-normalized, trimmed)
// reviewer title so the caller can correlate with the raw response.
// Returns a descriptive error on any mismatch — the prompt template instructs
// the reviewer to emit tasks "in the same order", so positional drift is a
// reviewer-side error worth retrying.
func validateChunkIdentity(parsed verdict.TasksOnly, chunkTasks []planparser.RawTask) error {
	if len(parsed.Tasks) != len(chunkTasks) {
		return fmt.Errorf("chunk identity: got %d tasks, expected %d", len(parsed.Tasks), len(chunkTasks))
	}
	// Pre-compute how many times each normalized expected title appears so that
	// plans with intentionally duplicate normalized titles (e.g. "Add tests" for
	// two different tasks) are not incorrectly rejected.
	wantCounts := make(map[string]int, len(chunkTasks))
	for _, ct := range chunkTasks {
		wantCounts[normalizeTaskTitle(strings.TrimSpace(ct.Title))]++
	}

	seen := make(map[string]int, len(chunkTasks))
	for i, t := range parsed.Tasks {
		gotOriginal := strings.TrimSpace(t.TaskTitle)
		wantOriginal := strings.TrimSpace(chunkTasks[i].Title)
		got := normalizeTaskTitle(gotOriginal)
		want := normalizeTaskTitle(wantOriginal)
		if got != want {
			return fmt.Errorf("chunk identity: tasks[%d].task_title %q, expected %q", i, gotOriginal, wantOriginal)
		}
		seen[got]++
		if seen[got] > wantCounts[got] {
			return fmt.Errorf("chunk identity: tasks[%d].task_title %q duplicated within chunk", i, gotOriginal)
		}
	}
	return nil
}
