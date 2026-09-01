package mcpsrv

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/patiently/anti-tangent-mcp/internal/codescene"
	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/planparser"
	"github.com/patiently/anti-tangent-mcp/internal/planrun"
	"github.com/patiently/anti-tangent-mcp/internal/prompts"
	"github.com/patiently/anti-tangent-mcp/internal/providers"
	"github.com/patiently/anti-tangent-mcp/internal/session"
	"github.com/patiently/anti-tangent-mcp/internal/stats"
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
	// SubmissionDefectOnly is set on validate_completion responses whose
	// blocking findings are all about the submission rather than the code.
	// The implementer should attach what is missing and re-submit; no rework
	// is implied. Server-computed; see submission_defect.go.
	SubmissionDefectOnly bool `json:"submission_defect_only,omitempty"`
}

// ValidateTaskSpecArgs is the input schema for the pre-hook.
type ValidateTaskSpecArgs struct {
	TaskTitle                    string                            `json:"task_title"           jsonschema:"required"`
	Goal                         string                            `json:"goal"                 jsonschema:"required"`
	AcceptanceCriteria           []string                          `json:"acceptance_criteria,omitempty"`
	NonGoals                     []string                          `json:"non_goals,omitempty"`
	Context                      string                            `json:"context,omitempty"`
	PinnedBy                     []string                          `json:"pinned_by,omitempty"`
	ControllerVerifiedReferences []string                          `json:"controller_verified_references,omitempty"`
	TestStrategyNotes            []string                          `json:"test_strategy_notes,omitempty"`
	CodebaseConventions          []string                          `json:"codebase_conventions,omitempty"`
	TestabilityExtractions       []string                          `json:"testability_extractions,omitempty"`
	NormativeTestBodies          []string                          `json:"normative_test_bodies,omitempty"`
	HarnessShapeAttestation      []session.HarnessShapeAttestation `json:"harness_shape_attestation,omitempty"`
	ProjectKnowledge             string                            `json:"project_knowledge,omitempty"`
	Phase                        string                            `json:"phase,omitempty"`
	ModelOverride                string                            `json:"model_override,omitempty"`
	MaxTokensOverride            int                               `json:"max_tokens_override,omitempty"`
	// PlanRunID ties this task to a plan run minted by validate_plan. Best
	// effort: an unknown or expired id must not fail the review.
	PlanRunID string `json:"plan_run_id,omitempty"`
}

type handlers struct {
	deps          Deps
	planCacheOnce sync.Once
}

func (h *handlers) planCache() *planPassCache {
	h.planCacheOnce.Do(func() {
		if h.deps.planCache == nil {
			h.deps.planCache = newPlanPassCache()
		}
	})
	return h.deps.planCache
}

func validateTaskSpecTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "validate_task_spec",
		Description: "Validate that a task specification is clear and implementable BEFORE you start coding. " +
			"Returns findings on missing/ambiguous goals, weak acceptance criteria, and unstated assumptions. " +
			"Call this once at the start of every task. " +
			"Optional pinned_by entries can name existing tests/docs/commands that pin behavior; optional phase=post is for post-hoc/session-recovery reviews only.",
	}
}

func (h *handlers) ValidateTaskSpec(ctx context.Context, _ *mcp.CallToolRequest, args ValidateTaskSpecArgs) (*mcp.CallToolResult, Envelope, error) {
	if args.TaskTitle == "" || args.Goal == "" {
		return nil, Envelope{}, errors.New("task_title and goal are required")
	}

	inputs, err := normalizeTaskSpecInputs(args, h.deps.Cfg.MaxPayloadBytes)
	if err != nil {
		return nil, Envelope{}, err
	}

	spec := session.TaskSpec{
		Title:                        args.TaskTitle,
		Goal:                         args.Goal,
		AcceptanceCriteria:           args.AcceptanceCriteria,
		NonGoals:                     args.NonGoals,
		Context:                      args.Context,
		PinnedBy:                     inputs.PinnedBy,
		ControllerVerifiedReferences: inputs.ControllerVerifiedReferences,
		TestStrategyNotes:            inputs.TestStrategyNotes,
		CodebaseConventions:          inputs.CodebaseConventions,
		TestabilityExtractions:       inputs.TestabilityExtractions,
		NormativeTestBodies:          inputs.NormativeTestBodies,
		HarnessShapeAttestations:     inputs.HarnessShapeAttestations,
		Phase:                        inputs.Phase,
	}

	cc, err := h.resolvePreCallContext(
		args.MaxTokensOverride,
		h.deps.Cfg.PerTaskMaxTokens,
		args.ModelOverride,
		h.deps.Cfg.PreModel,
		func() (prompts.Output, error) {
			return prompts.RenderPre(prompts.PreInput{Spec: spec, ProjectKnowledge: inputs.ProjectKnowledge})
		},
		"render pre prompt",
	)
	if err != nil {
		return nil, Envelope{}, err
	}

	result, modelUsed, ms, partialRaw, err := h.review(ctx, cc.Model, cc.Rendered, cc.MaxTokens)
	if r, env, handled, retErr := h.handlePerTaskReviewErr(perTaskReviewErrInputs{
		Err:        err,
		Model:      cc.Model,
		PartialRaw: partialRaw,
		EnvVar:     "ANTI_TANGENT_PER_TASK_MAX_TOKENS",
		Clamp:      cc.Clamp,
	}); handled {
		if retErr == nil {
			h.recordStat(statParams{
				tool:      "validate_task_spec",
				verdict:   env.Verdict,
				findings:  env.Findings,
				modelUsed: env.ModelUsed,
				reviewMS:  env.ReviewMS,
				partial:   env.Partial,
				sessionID: env.SessionID,
			})
		}
		return r, env, retErr
	}
	result.Findings = suppressTestabilityExtractionScopeDrift(result.Findings, inputs.TestabilityExtractions)
	result.Findings = suppressUnverifiableCodebaseClaim(result.Findings, inputs.ControllerVerifiedReferences)
	result.Findings = normalizeTaskSpecUnverifiableFindings(result.Findings)
	if cc.Clamp.Severity != "" {
		result.Findings = append([]verdict.Finding{cc.Clamp}, result.Findings...)
	}
	result = verdict.FinalizeVerdict(result)

	// Create the session only after the review succeeds so failed reviews
	// don't leave orphan sessions in the store waiting for TTL eviction.
	sess := h.deps.Sessions.Create(spec, args.PlanRunID)
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
		Partial:    result.Partial,
	}
	env = h.withSessionTTL(env, sess)

	if args.PlanRunID != "" {
		// Best-effort: an unknown or expired run must not fail the review.
		if !h.deps.PlanRuns.AppendRow(args.PlanRunID, planrun.TaskRow{
			SessionID:      sess.ID,
			TaskTitle:      args.TaskTitle,
			PreVerdict:     env.Verdict,
			CodesceneState: planrun.StateMissing,
		}) {
			slog.Warn("plan run row append failed; run unknown or expired",
				"plan_run_id", args.PlanRunID, "session_id", sess.ID)
		}
	}

	h.recordStat(statParams{
		tool:      "validate_task_spec",
		verdict:   env.Verdict,
		findings:  env.Findings,
		modelUsed: env.ModelUsed,
		reviewMS:  env.ReviewMS,
		partial:   env.Partial,
		sessionID: env.SessionID,
	})
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

// statParams carries the fields needed to record one hook call. Grouping them
// avoids a long primitive argument list across the per-handler record sites.
type statParams struct {
	tool         string
	verdict      string
	findings     []verdict.Finding
	modelUsed    string
	reviewMS     int64
	partial      bool
	cached       bool
	payloadBytes int
	sessionID    string // raw id; hashed (salted) inside, never stored raw

	tasksTotal      int
	tasksWithHeader int
}

// recordStat maps a statParams into a stats.Event and records it.
// Nil-safe: when stats are disabled this is a single nil check with no
// allocation.
func (h *handlers) recordStat(p statParams) {
	if h.deps.Stats == nil {
		return
	}
	sev, cat, total := stats.CountFindings(p.findings)
	h.deps.Stats.Record(stats.Event{
		Ts:              time.Now().UTC().Truncate(time.Second),
		Tool:            p.tool,
		Verdict:         p.verdict,
		FindingsTotal:   total,
		SeverityCounts:  sev,
		CategoryCounts:  cat,
		ReviewMS:        p.reviewMS,
		Model:           p.modelUsed,
		Cached:          p.cached,
		Partial:         p.partial,
		PayloadBytes:    p.payloadBytes,
		SessionHash:     h.deps.Stats.HashSession(p.sessionID),
		TasksTotal:      p.tasksTotal,
		TasksWithHeader: p.tasksWithHeader,
	})
}

// planFindings aggregates plan-level and per-task findings from a PlanResult
// into a single slice for stats recording.
func planFindings(pr verdict.PlanResult) []verdict.Finding {
	findings := append([]verdict.Finding(nil), pr.PlanFindings...)
	for _, t := range pr.Tasks {
		findings = append(findings, t.Findings...)
	}
	return findings
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

// FileArg is the shared file-entry shape for check_progress's changed_files
// and extract_project_knowledge's completion-envelope final_files. Content
// is REQUIRED (matches 0.15.0's schema exactly): jsonschema-go derives
// `required` from the absence of `omitempty`/`omitzero`, so dropping this
// tag would silently make content optional on both of those tools too —
// neither resolves a bare path from disk, so an omitted content would ship
// an empty file body to the reviewer with no error. validate_completion does
// NOT use this type for final_files; it has its own CompletionFileArg, whose
// pointer Content field can distinguish "omitted" from "explicitly empty".
type FileArg struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// CompletionFileArg is validate_completion's final_files entry — the only
// tool whose file entries may omit content to have the server read Path from
// disk. Content is a *pointer* so an omitted field (nil) is distinguishable
// from an explicit empty string (""), which Go's JSON unmarshal cannot
// otherwise tell apart: nil means "read Path from disk"; "" means "this file
// is genuinely empty" (e.g. a deletion) and must NOT trigger a read — a read
// would either fail EvalSymlinks on a path that no longer exists, or (for a
// caller using this codebase's own relative-path convention) fail the
// path-must-be-absolute check, either way losing the whole call to a
// transport error instead of a structured envelope.
//
// If both Path and a non-nil Content are supplied, Content wins and Path is
// never read — this is the pre-0.16.0 always-inline behaviour, kept for
// backward compatibility rather than treated as an error.
type CompletionFileArg struct {
	Path    string  `json:"path"`
	Content *string `json:"content,omitempty"`
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
		env := prependClamp(notFoundEnvelope(args.SessionID, h.deps.Cfg.MidModel), clamp)
		h.recordStat(statParams{
			tool:      "check_progress",
			verdict:   env.Verdict,
			findings:  env.Findings,
			modelUsed: env.ModelUsed,
			sessionID: env.SessionID,
		})
		return envelopeResult(env)
	}

	if size := totalBytes(args.ChangedFiles); size > h.deps.Cfg.MaxPayloadBytes {
		env := prependClamp(tooLargeEnvelope(sess.ID, h.deps.Cfg.MidModel, size, h.deps.Cfg.MaxPayloadBytes,
			"Send a smaller changed_files set, or split the checkpoint into smaller chunks."), clamp)
		h.recordStat(statParams{
			tool:         "check_progress",
			verdict:      env.Verdict,
			findings:     env.Findings,
			modelUsed:    env.ModelUsed,
			sessionID:    env.SessionID,
			payloadBytes: size,
		})
		return envelopeResult(env)
	}

	model, rendered, err := h.resolveModelAndRender(
		args.ModelOverride,
		h.deps.Cfg.MidModel,
		func() (prompts.Output, error) {
			return prompts.RenderMid(prompts.MidInput{
				Spec:          sess.Spec,
				PriorFindings: priorFindings(sess),
				WorkingOn:     args.WorkingOn,
				Files:         toPromptFiles(args.ChangedFiles),
				Questions:     args.Questions,
			})
		},
		"render mid prompt",
	)
	if err != nil {
		return nil, Envelope{}, err
	}

	result, modelUsed, ms, partialRaw, err := h.review(ctx, model, rendered, maxTokens)
	if r, env, handled, retErr := h.handlePerTaskReviewErr(perTaskReviewErrInputs{
		Err:        err,
		SessionID:  sess.ID,
		Model:      model,
		PartialRaw: partialRaw,
		EnvVar:     "ANTI_TANGENT_PER_TASK_MAX_TOKENS",
		Clamp:      clamp,
		Sess:       sess,
	}); handled {
		if retErr == nil {
			h.recordStat(statParams{
				tool:         "check_progress",
				verdict:      env.Verdict,
				findings:     env.Findings,
				modelUsed:    env.ModelUsed,
				reviewMS:     env.ReviewMS,
				partial:      env.Partial,
				sessionID:    env.SessionID,
				payloadBytes: totalBytes(args.ChangedFiles),
			})
		}
		return r, env, retErr
	}

	if clamp.Severity != "" {
		result.Findings = append([]verdict.Finding{clamp}, result.Findings...)
	}
	result = verdict.FinalizeVerdict(result)

	h.deps.Sessions.AppendCheckpoint(sess.ID, session.Checkpoint{
		At:        time.Now(),
		WorkingOn: args.WorkingOn,
		FileCount: len(args.ChangedFiles),
		Verdict:   result.Verdict,
		Findings:  result.Findings,
	})

	if sess.PlanRunID != "" {
		if !h.deps.PlanRuns.UpdateRow(sess.PlanRunID, sess.ID, func(row *planrun.TaskRow) {
			row.Checkpoints++
		}) {
			slog.Warn("plan run row update failed; run or row unknown",
				"plan_run_id", sess.PlanRunID, "session_id", sess.ID)
		}
	}

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
		Partial:    result.Partial,
	}
	env = h.withSessionTTL(env, sess)
	h.recordStat(statParams{
		tool:         "check_progress",
		verdict:      env.Verdict,
		findings:     env.Findings,
		modelUsed:    env.ModelUsed,
		reviewMS:     env.ReviewMS,
		partial:      env.Partial,
		sessionID:    env.SessionID,
		payloadBytes: totalBytes(args.ChangedFiles),
	})
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

// toPromptContextFiles converts resolved attachments into the prompts
// package's render-facing shape. The short hash is the same 8-digit prefix
// the summary block shows, so a reviewer citing a file and a human reading
// the summary are looking at the same identity.
func toPromptContextFiles(files []contextFile) []prompts.ContextFile {
	out := make([]prompts.ContextFile, 0, len(files))
	for _, f := range files {
		out = append(out, prompts.ContextFile{
			Path:        f.Source.Path,
			Bytes:       f.Source.Bytes,
			SHA256Short: shortHash(f.Source.SHA256),
			Content:     f.Content,
		})
	}
	return out
}

// contextFilePaths lists the resolved attachment paths, for the one stderr
// line validate_plan logs per call.
func contextFilePaths(files []contextFile) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.Source.Path)
	}
	return out
}

// fileSourcePaths is contextFilePaths for the already-projected provenance
// form. handlePlanReviewErr carries []fileSource rather than []contextFile
// (it never needs the content), and both call sites of
// verdict.DemoteUnattachedContradictions must pass the SAME attached set —
// a divergence there is exactly how the demotion would go all-or-nothing
// again on one path only.
func fileSourcePaths(files []fileSource) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.Path)
	}
	return out
}

// contextSources projects the resolved attachment set down to the provenance
// used by planSummaryMeta.ContextFiles — path and byte count for the summary
// block, nothing the reviewer prompt needed (Content).
func contextSources(files []contextFile) []fileSource {
	out := make([]fileSource, 0, len(files))
	for _, f := range files {
		out = append(out, f.Source)
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

// truncatedResult is the no-recovery fallback for per-task truncation. It
// returns a Result (not an Envelope) so the caller can fold any clamp into
// Findings, run FinalizeVerdict, and assemble the envelope. The synthetic
// finding is SeverityMajor (was minor pre-0.5.2) so the ladder derives warn
// consistently with the previously-explicit Verdict assignment.
func truncatedResult() verdict.Result {
	return verdict.Result{
		Verdict: verdict.VerdictWarn,
		Findings: []verdict.Finding{{
			Severity:   verdict.SeverityMajor,
			Category:   verdict.CategoryOther,
			Criterion:  "reviewer_response",
			Evidence:   providers.ErrResponseTruncated.Error(),
			Suggestion: "Raise ANTI_TANGENT_PER_TASK_MAX_TOKENS or pass max_tokens_override and retry.",
		}},
		NextAction: "Retry with a higher max_tokens_override (or raise the configured max-tokens cap).",
	}
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
// Clamp findings use a non-unverifiable category, so order relative to the
// later unverifiable-rollup finalization does not change rollup behavior.
func prependPlanClamp(pr verdict.PlanResult, clamp verdict.Finding) verdict.PlanResult {
	if clamp.Severity == "" {
		return pr
	}
	pr.PlanFindings = append([]verdict.Finding{clamp}, pr.PlanFindings...)
	return pr
}

// prependPlanDeprecation prepends the plan_text deprecation notice. Minor
// severity so it never changes a verdict — the server is advisory, and a
// deprecated input is not a plan defect. Mirrors prependPlanClamp so it
// survives every early-exit path.
func prependPlanDeprecation(pr verdict.PlanResult, usedPlanText bool) verdict.PlanResult {
	if !usedPlanText {
		return pr
	}
	f := verdict.Finding{
		Severity:   verdict.SeverityMinor,
		Category:   verdict.CategoryOther,
		Criterion:  "input",
		Evidence:   "plan_text was supplied; it is deprecated and will be removed in 1.0.0",
		Suggestion: "pass plan_path with the absolute path to the plan file instead",
	}
	pr.PlanFindings = append([]verdict.Finding{f}, pr.PlanFindings...)
	return pr
}

// prependRepoRootUnusable adds the in-band signal that a supplied repo_root
// could not be used and the Create/Modify disk tier was therefore skipped.
// reason is empty when repo_root was usable or was never supplied, in which
// case pr is returned untouched.
//
// Without it, an unusable repo_root produced an envelope, a summary and a
// findings list byte-identical to a call that never passed repo_root at all
// — including for a plain relative path, which resolveDirInput rejects
// outright. controller.md §5.8 and the tool description both promise
// repo_root "enables the disk tier", so silence there reads as "the tier ran
// and found nothing". A summary-only line would not do: findings are what
// callers parse.
//
// Modelled on prependPlanDeprecation, and applied at the same call sites,
// for the same reason: it is a minor CategoryOther finding, so running it
// through verdict.FinalizeVerdict would let it count toward the
// noise_cluster (3rd-minor) trigger and flip a pass to warn. It must be
// applied POST-ladder and per-call, and never stored on a cache entry.
func prependRepoRootUnusable(pr verdict.PlanResult, reason string) verdict.PlanResult {
	if reason == "" {
		return pr
	}
	f := verdict.Finding{
		Severity:   verdict.SeverityMinor,
		Category:   verdict.CategoryOther,
		Criterion:  "repo_root",
		Evidence:   "repo_root unusable (" + reason + "); Create/Modify disk tier skipped",
		Suggestion: "pass repo_root as an absolute path to an existing directory inside ANTI_TANGENT_PLAN_ROOTS, or omit it and rely on the plan-order tier alone",
	}
	pr.PlanFindings = append([]verdict.Finding{f}, pr.PlanFindings...)
	return pr
}

// recoverPartialFindings attempts to extract complete findings from a
// truncated reviewer response. Returns (result, true) when at least one
// finding was recovered; (zero, false) when the caller should fall back to
// truncatedResult.
//
// The returned Result has Verdict="warn", Findings = recovered list plus a
// single minor "truncation marker" finding noting the count and referencing
// both envVar and max_tokens_override mitigations, Partial=true, and
// NextAction = the parsed result's next_action when non-empty, otherwise a
// generic fallback that points the caller at max_tokens_override.
func recoverPartialFindings(rawJSON []byte, envVar string) (verdict.Result, bool) {
	if len(rawJSON) == 0 {
		return verdict.Result{}, false
	}
	r, ok := verdict.ParseResultPartial(rawJSON)
	if !ok || len(r.Findings) == 0 {
		return verdict.Result{}, false
	}
	marker := verdict.Finding{
		Severity:   verdict.SeverityMinor,
		Category:   verdict.CategoryOther,
		Criterion:  "reviewer_response",
		Evidence:   fmt.Sprintf("reviewer output truncated at the max_tokens cap; %d complete findings recovered", len(r.Findings)),
		Suggestion: "Raise " + envVar + " or pass max_tokens_override on the next call to capture more.",
	}
	r.Findings = append(r.Findings, marker)
	// AC: next_action MUST mention re-running with max_tokens_override. If the
	// reviewer returned a NextAction that already mentions it, preserve it;
	// otherwise append the mitigation hint (or supply a fallback if empty).
	switch {
	case r.NextAction == "":
		r.NextAction = "Address recovered findings; reviewer output was truncated. Re-call with a higher max_tokens_override (or raise " + envVar + ") to capture the full review."
	case !strings.Contains(r.NextAction, "max_tokens_override"):
		r.NextAction = r.NextAction + " Reviewer output was truncated; re-call with a higher max_tokens_override (or raise " + envVar + ") to capture the full review."
	}
	r.Partial = true
	return r, true
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

// tooLargeEnvelope builds the rejection envelope for a payload-too-large hit.
// Critical so the ladder derives fail from one critical, matching the explicit Verdict: fail.
func tooLargeEnvelope(id string, model config.ModelRef, size, limit int, suggestion string) Envelope {
	return Envelope{
		SessionID: id,
		Verdict:   string(verdict.VerdictFail),
		Findings: []verdict.Finding{{
			Severity:   verdict.SeverityCritical,
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
			"and non-goal. Treat any `fail` or `warn` findings as work to do before claiming done. " +
			"Omit a final_files entry's content to have the server read its absolute path, and pass final_diff_path instead of final_diff, to avoid emitting large evidence as output tokens.",
	}
}

type ValidateCompletionArgs struct {
	SessionID             string              `json:"session_id"  jsonschema:"required"`
	Summary               string              `json:"summary"     jsonschema:"required"`
	FinalFiles            []CompletionFileArg `json:"final_files,omitempty"`
	FinalDiff             string              `json:"final_diff,omitempty"`
	FinalDiffPath         string              `json:"final_diff_path,omitempty"`
	TestEvidence          string              `json:"test_evidence,omitempty"`
	ExitContracts         []string            `json:"exit_contracts,omitempty"`
	ExitContractsInferred bool                `json:"exit_contracts_inferred,omitempty"`
	ModelOverride         string              `json:"model_override,omitempty"`
	MaxTokensOverride     int                 `json:"max_tokens_override,omitempty"`
	Codescene             *codescene.Digest   `json:"codescene,omitempty"`
}

// ValidatePlanArgs is the input schema for the plan-level reviewer.
type ValidatePlanArgs struct {
	PlanText          string   `json:"plan_text,omitempty"`
	PlanPath          string   `json:"plan_path,omitempty"`
	ProjectKnowledge  string   `json:"project_knowledge,omitempty"`
	ModelOverride     string   `json:"model_override,omitempty"`
	MaxTokensOverride int      `json:"max_tokens_override,omitempty"`
	Mode              string   `json:"mode,omitempty"`
	ContextPaths      []string `json:"context_paths,omitempty"`
	RepoRoot          string   `json:"repo_root,omitempty"`
}

func validatePlanTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "validate_plan",
		Description: "Validate an implementation plan as a whole BEFORE dispatching subagents to implement individual tasks. " +
			"Returns per-task findings and ready-to-paste structured headers (Goal / Acceptance criteria / Non-goals / Context) for tasks that lack them. " +
			"Call this once at plan-handoff time; the per-task `validate_task_spec` is still called by each implementing subagent at task start. " +
			"Pass plan_path with the ABSOLUTE path to the plan file — the server reads it, so a large plan costs the caller no output tokens. " +
			"plan_text is deprecated and will be removed in 1.0.0. Exactly one of the two must be set. " +
			"If repo policy has carve-outs such as docs-only commit exceptions, state them literally in the plan — the reviewer cannot read external CLAUDE.md policy. " +
			"Optionally pass context_paths: ABSOLUTE paths to source files the plan makes claims about. " +
			"The server reads them whole and the reviewer verifies claims against them instead of emitting unverifiable_codebase_claim. " +
			"Attach only files the plan actually touches — this is by far the most expensive input the tool takes. " +
			"Every attached byte is sent VERBATIM to the configured reviewer vendor's API (a third party), in full, on every reviewer call of the round; " +
			"do not attach secrets, credentials, or files the repo would not otherwise send off-host. " +
			"ANTI_TANGENT_PLAN_ROOTS bounds which directories the server will read on your behalf. " +
			"Optionally pass repo_root (absolute) to enable the disk tier of the Create/Modify consistency check. ",
	}
}

func totalCompletionBytes(files []FileArg, finalDiff string) int {
	return totalBytes(files) + len(finalDiff)
}

// evidenceTruncationPatterns are case-insensitive substrings that strongly
// indicate the caller pasted truncated/elided evidence rather than a complete
// diff or full file contents. The checkEvidenceShape walker applies every
// entry to BOTH final_diff AND every final_files[].content, so adding a
// pattern here automatically extends both inputs. Patterns must be
// lowercased; the walker calls strings.ToLower on the input first.
//
// The list is intentionally narrow: only patterns that have negligible chance
// of appearing in legitimate code or diffs. "diff --git with zero @@" is NOT
// included — it false-fires on mode-only / rename-only / binary diffs.
//
// The bare `/...` substring that v0.5.2 added has been removed in v0.6.1
// because it false-positives on Go's standard `./pkg/...` package-recursion
// syntax in `test_evidence` strings and test-file contents (see #25). Every
// other v0.5.2 placeholder in the list is comment-form (`/* ... */`,
// `// snip`, etc.) and unambiguous; the bare form was the outlier. If a real
// `/...` truncation pattern surfaces in the field, re-add it as a regex that
// requires a leading comment marker (`//`, `/*`, `#`, or start-of-line) so
// the path case can never match.
var evidenceTruncationPatterns = []string{
	"(truncated)",
	"[truncated]",
	"// ... unchanged",
	"<!-- truncated -->",
	// Added in v0.5.2 from field reports:
	"/* ... */",
	"/* ...rest unchanged */",
	"// snip",
	"// elided",
	"// ... rest unchanged",
}

// evidenceEllipsisLine matches a line that contains only `...` (with optional
// surrounding whitespace). The (?m) flag anchors ^/$ to line boundaries.
var evidenceEllipsisLine = regexp.MustCompile(`(?m)^\s*\.\.\.\s*$`)

// checkEvidenceShape inspects finalDiff/files for malformed evidence shapes.
// Returns a non-empty human-readable reason string when a rule fires; empty
// string when the evidence looks structurally sound. The reason is what
// populates the rejection finding's Evidence field.
//
// Decoupled from ValidateCompletionArgs (rather than taking the whole args
// struct) so it works identically for validate_completion's resolved
// []FileArg AND for extract_project_knowledge's per-envelope
// CompletionEnvelopeArg{FinalDiff, FinalFiles []FileArg} — both just pass
// their diff string and file slice directly.
//
// Order of checks (fail-fast on the first hit so the reason points at the
// most-likely cause):
//  1. final_diff substring + ellipsis-line scan
//  2. final_files empty Path
//  3. final_files content substring + ellipsis-line scan
func checkEvidenceShape(finalDiff string, files []FileArg) string {
	if finalDiff != "" {
		lower := strings.ToLower(finalDiff)
		for _, p := range evidenceTruncationPatterns {
			if idx := strings.Index(lower, p); idx >= 0 {
				return fmt.Sprintf("final_diff contains truncation marker %q at offset %d", p, idx)
			}
		}
		if loc := evidenceEllipsisLine.FindStringIndex(finalDiff); loc != nil {
			return fmt.Sprintf("final_diff contains a placeholder line `...` at offset %d", loc[0])
		}
	}
	for i, f := range files {
		if strings.TrimSpace(f.Path) == "" {
			return fmt.Sprintf("final_files[%d].path is empty", i)
		}
	}
	for i, f := range files {
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

// completionInputTooLargeError distinguishes an oversized final_diff_path or
// final_files[i].path from every other resolveCompletionInputs failure. Only
// this case gets mapped to the tooLargeEnvelope shape in ValidateCompletion;
// every other error (missing file, non-regular file, outside
// ANTI_TANGENT_PLAN_ROOTS) stays a plain transport error. bytes is the
// file's TRUE size (from fileSource.Bytes, populated even on the errTooLarge
// return — see resolveFileInput), not the configured cap.
type completionInputTooLargeError struct {
	field string // e.g. "final_diff_path" or "final_files[2].path"
	bytes int
	err   error // wraps errTooLarge
}

func (e *completionInputTooLargeError) Error() string {
	return fmt.Sprintf("%s: %s", e.field, e.err)
}

func (e *completionInputTooLargeError) Unwrap() error { return e.err }

// resolveCompletionInputs fills in args.FinalDiff from disk when
// final_diff_path was supplied, and returns args.FinalFiles converted to
// plain []FileArg with Content resolved from disk for every entry whose
// Content was omitted (nil).
//
// Content is resolved ONLY when nil — see CompletionFileArg: an explicit ""
// means "this file is genuinely empty" (e.g. a deletion) and must NOT
// trigger a read, unlike pre-0.16.0's string-based Content, whose empty
// string was indistinguishable from omitted and always triggered one
// (breaking a deleted-file entry, whose read would fail EvalSymlinks, and
// any relative path, which would fail the path-must-be-absolute check).
//
// Returns the resolved slice rather than mutating args.FinalFiles in place,
// since ValidateCompletionArgs.FinalFiles is wire-typed []CompletionFileArg
// (path + nilable content) while every downstream consumer —
// totalCompletionBytes, checkEvidenceShape, evidenceCacheKey, toPromptFiles,
// referencedPathsMissingEvidence — needs plain []FileArg with Content always
// populated. Every later stage — payload cap, evidence-shape guard, evidence
// cache key, prompt render — must read this resolved slice, never
// args.FinalFiles directly.
//
// Uses the shared MaxPayloadBytes, never PlanMaxPayloadBytes: completion
// evidence did not gain the headroom validate_plan did.
//
// FIX 4: `diskTotal` tracks the running total of bytes actually READ FROM
// DISK during this call, and bails as soon as it crosses maxBytes. Before
// this, the aggregate was only checked AFTER every input had been fully
// resolved (the caller's step 5) — each of e.g. 500 final_files[].path
// entries at ~200KB passes THIS function's per-file resolveFileInput cap
// individually, so all ~100MB gets read into memory before the aggregate
// check downstream ever runs. Bailing here, the moment the running total
// crosses maxBytes, keeps peak memory bounded by the cap regardless of how
// many path entries remain unresolved.
//
// diskTotal deliberately does NOT count inline final_diff / final_files
// content (an explicit Content, or final_diff supplied directly rather
// than via final_diff_path): that content arrives already fully resident
// in memory in this one request (bounded by the client's own output
// ceiling, same as before this release — see the doc comment above), so
// there is no peak-memory reason to bail on it mid-loop, and doing so would
// only replace step 5's existing, more specific tooLargeEnvelope guidance
// with this function's generic one. It is still caught by the aggregate
// check at step 5 exactly as before.
func (h *handlers) resolveCompletionInputs(args *ValidateCompletionArgs) ([]FileArg, error) {
	maxBytes := h.deps.Cfg.MaxPayloadBytes
	roots := h.deps.Cfg.PlanRoots
	diskTotal := 0

	if args.FinalDiffPath != "" {
		content, src, err := resolveFileInput(args.FinalDiffPath, roots, maxBytes)
		if errors.Is(err, errTooLarge) {
			return nil, &completionInputTooLargeError{field: "final_diff_path", bytes: src.Bytes, err: err}
		}
		if err != nil {
			return nil, fmt.Errorf("final_diff_path: %w", err)
		}
		args.FinalDiff = content
		diskTotal += len(content)
	}

	files := make([]FileArg, len(args.FinalFiles))
	for i, f := range args.FinalFiles {
		files[i] = FileArg{Path: f.Path}
		// Content wins over Path when both are supplied — see
		// CompletionFileArg's doc comment. Only a nil Content (never an
		// explicit "") triggers a disk read.
		if f.Content != nil {
			files[i].Content = *f.Content
			continue // inline — not a disk read, not counted in diskTotal
		}
		// Matches checkEvidenceShape's own strings.TrimSpace(f.Path) == ""
		// check on the empty-Path guard below (see its case 2 comment). A
		// bare f.Path == "" check here let a whitespace-only path (content
		// omitted) fall through to resolveFileInput, whose own TrimSpace
		// guard then returned a bare transport error instead of routing back
		// to the malformed_evidence envelope checkEvidenceShape produces.
		if strings.TrimSpace(f.Path) == "" {
			continue
		}
		content, src, err := resolveFileInput(f.Path, roots, maxBytes)
		if errors.Is(err, errTooLarge) {
			return nil, &completionInputTooLargeError{field: fmt.Sprintf("final_files[%d].path", i), bytes: src.Bytes, err: err}
		}
		if err != nil {
			return nil, fmt.Errorf("final_files[%d].path: %w", i, err)
		}
		files[i].Content = content
		diskTotal += len(content)
		if diskTotal > maxBytes {
			// The aggregate of what's been READ FROM DISK so far crossed
			// the cap on this entry's read, even though it individually
			// passed resolveFileInput's own per-file cap above — bail now
			// rather than reading every remaining path entry into memory
			// first. bytes carries the cumulative total (not just this
			// entry's size) so the resulting tooLargeEnvelope's "payload N
			// bytes exceeds cap" evidence line is accurate.
			return nil, &completionInputTooLargeError{
				field: fmt.Sprintf("final_files[%d].path (combined disk-read payload)", i), bytes: diskTotal, err: errTooLarge}
		}
	}
	return files, nil
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
// encoding of the rejection-relevant inputs. files is the RESOLVED slice
// (see resolveCompletionInputs), pre-sorted here by Path so that an
// order-only difference between two otherwise-identical submissions still
// hits the cache. Plain string concatenation would risk collisions (e.g.
// sessionID="a" + finalDiff="bc" vs sessionID="ab" + finalDiff="c");
// JSON-encoded boundaries make those distinct.
func evidenceCacheKey(sessionID, finalDiff string, files []FileArg, testEvidence string) [32]byte {
	sortedFiles := append([]FileArg(nil), files...)
	sort.Slice(sortedFiles, func(i, j int) bool { return sortedFiles[i].Path < sortedFiles[j].Path })
	keyInput := struct {
		SessionID    string    `json:"session_id"`
		FinalDiff    string    `json:"final_diff"`
		FinalFiles   []FileArg `json:"final_files"`
		TestEvidence string    `json:"test_evidence"`
	}{
		SessionID:    sessionID,
		FinalDiff:    finalDiff,
		FinalFiles:   sortedFiles,
		TestEvidence: testEvidence,
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
// Critical so the ladder derives fail from one critical, matching the explicit Verdict: fail.
func malformedEvidenceEnvelope(sessionID, reason, modelUsed string) Envelope {
	return Envelope{
		SessionID: sessionID,
		Verdict:   string(verdict.VerdictFail),
		Findings: []verdict.Finding{{
			Severity:   verdict.SeverityCritical,
			Category:   verdict.CategoryMalformedEvidence,
			Criterion:  "evidence_shape",
			Evidence:   reason,
			Suggestion: "Submit full file contents in final_files, or a complete unified diff (no truncation markers) in final_diff.",
		}},
		NextAction: "Re-submit with complete evidence; current submission appears truncated.",
		ModelUsed:  modelUsed,
	}
}

// resolvedEmptyPathInputs identifies every SUPPLIED path input —
// final_diff_path, or a final_files[i] entry whose content was read from
// disk rather than given inline — that resolved to 0 bytes. This catches
// the case the pre-resolution check at step 2b cannot see: a
// final_diff_path or final_files[i].path whose STRING is non-empty but
// whose file on disk is empty.
//
// This is FIX 2: a caller who supplied a path plainly intended to send that
// file, so a 0-byte resolution is always worth surfacing — regardless of
// what other evidence the call also carries. What differs by case is
// whether that surfacing is a hard rejection or a finding on an otherwise-
// proceeding call; see the two call sites in ValidateCompletion's step 2e.
//
// A final_files[i] entry whose Content was an EXPLICIT "" (never nil — see
// CompletionFileArg's doc comment) is a deliberate deletion marker, not a
// resolution accident, so it is never included here even when it is the
// only final_files entry present; only content this function can tell was
// actually read from disk and came back empty counts. A whitespace-only
// Path is left for checkEvidenceShape's own dedicated empty-path check
// downstream, which produces a clearer reason string for that specific
// case.
//
// Returns nil when no supplied path input resolved empty. Otherwise one
// reason string per offending field, each suitable for either the
// malformed_evidence envelope's Evidence field (hard-reject case) or an
// insufficient_evidence finding's Evidence field (soft case) — see
// ValidateCompletion.
func resolvedEmptyPathInputs(args *ValidateCompletionArgs, resolvedFiles []FileArg) []string {
	var reasons []string
	if args.FinalDiffPath != "" && args.FinalDiff == "" {
		reasons = append(reasons, fmt.Sprintf("final_diff_path resolved to 0 bytes: %s", args.FinalDiffPath))
	}
	for i, f := range resolvedFiles {
		if f.Content != "" {
			continue
		}
		if strings.TrimSpace(f.Path) == "" {
			continue
		}
		if i >= len(args.FinalFiles) || args.FinalFiles[i].Content != nil {
			// Not a disk-resolved entry: either out of range (shouldn't
			// happen — resolvedFiles is built 1:1 from args.FinalFiles) or
			// an explicit "" deletion marker, not a resolution accident.
			continue
		}
		reasons = append(reasons, fmt.Sprintf("final_files[%d].path resolved to 0 bytes: %s", i, args.FinalFiles[i].Path))
	}
	return reasons
}

// hasNonEmptyEvidence reports whether args/resolvedFiles carry any evidence
// content BESIDES path inputs that resolved to empty: non-empty
// test_evidence, a non-empty final_diff (which is already "" when
// final_diff_path resolved to nothing — see resolveCompletionInputs), or at
// least one final_files entry (disk-resolved or explicit) with non-empty
// content. An explicit "" deletion marker never counts as evidence here,
// same as it never counts as an "empty path input mistake" in
// resolvedEmptyPathInputs — it is simply absent from consideration either
// way, matching step 2b's original at-least-one-evidence semantics for that
// case.
func hasNonEmptyEvidence(args *ValidateCompletionArgs, resolvedFiles []FileArg) bool {
	if args.TestEvidence != "" || args.FinalDiff != "" {
		return true
	}
	for _, f := range resolvedFiles {
		if f.Content != "" {
			return true
		}
	}
	return false
}

func majorFindings(findings []verdict.Finding) []verdict.Finding {
	var major []verdict.Finding
	for _, finding := range findings {
		if finding.Severity == verdict.SeverityMajor {
			major = append(major, finding)
		}
	}
	return major
}

// ValidateCompletion runs the post-implementation reviewer call. Eight-step
// ordering (preserved here to keep the AC-mapping legible):
//
//  1. summary required check
//  2. final_diff / final_diff_path mutual-exclusivity check
//     2b. at-least-one-evidence check (final_diff_path counts, pre-resolution)
//     2c. effectiveMaxTokens + clampFinding — computed here (moved ahead of its
//     old step-4 slot) so a too-large PATH input, detected next, can render
//     through the same clamped tooLargeEnvelope shape as the inline
//     payload-cap check below. Independent of evidence, so reordering it
//     changes nothing else.
//     2d. resolveCompletionInputs — materializes final_diff_path and any
//     final_files[].path entries BEFORE the payload cap, evidence-shape
//     guard, and evidence cache key see them. An oversized path input
//     returns the SAME tooLargeEnvelope an oversized inline payload would;
//     every other resolve failure (missing file, non-regular file, outside
//     ANTI_TANGENT_PLAN_ROOTS) stays a plain transport error, matching how
//     validate_plan's plan_path treats those cases.
//     2e. resolved-empty-path check — 2b only sees path STRINGS, so a
//     final_diff_path or final_files[i].path that resolves to an empty file
//     on disk sails past it. A path input resolving to 0 bytes is always a
//     caller mistake worth surfacing (they plainly intended to send that
//     file). When it is the ONLY evidence, this is a hard structured
//     malformed_evidence rejection, same as before — no paid reviewer call
//     with nothing to review. When other real evidence exists (non-empty
//     test_evidence, or another non-empty file), the call proceeds and
//     carries an insufficient_evidence finding naming the empty path
//     instead, so the gap is visible rather than silently reviewed around.
//     test_evidence alone with no path inputs supplied is unaffected either
//     way — see resolvedEmptyPathInputs / hasNonEmptyEvidence.
//  3. lightweight marker (empty session_id + non-empty evidence)
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

	// 2. final_diff / final_diff_path are mutually exclusive.
	if args.FinalDiff != "" && args.FinalDiffPath != "" {
		return nil, Envelope{}, errors.New("final_diff and final_diff_path are mutually exclusive")
	}

	// 2b. at-least-one-evidence: rejects the "totally empty call" case
	// regardless of whether session_id is set. final_diff_path counts as
	// evidence even before it is resolved.
	if len(args.FinalFiles) == 0 && args.FinalDiff == "" && args.FinalDiffPath == "" && args.TestEvidence == "" {
		return nil, Envelope{}, errors.New("validate_completion: at least one of final_files, final_diff, final_diff_path, or test_evidence must be non-empty")
	}

	// 2c. max-tokens override + clamp finding. Computed before resolution so
	// an oversized path input (below) can be rendered through the same
	// clamped tooLargeEnvelope shape the inline payload-cap check uses.
	maxTokens, clamp, err := effectiveMaxTokens(args.MaxTokensOverride, h.deps.Cfg.PerTaskMaxTokens, h.deps.Cfg.MaxTokensCeiling)
	if err != nil {
		return nil, Envelope{}, err
	}

	// 2d. Resolve path inputs BEFORE the payload cap, the evidence-shape
	// guard, and the evidence cache key. checkEvidenceShape must see resolved
	// content: otherwise a caller could bypass the truncation guard entirely
	// by passing a path to a file full of elided content. See design §2.1.
	//
	// An oversized path input surfaces as the SAME tooLargeEnvelope shape an
	// oversized inline payload gets from the check at step 5 below — a
	// caller shouldn't get useful structured guidance one way and a bare
	// transport error the other. Every other resolve failure (missing file,
	// non-regular file, outside ANTI_TANGENT_PLAN_ROOTS) is a caller mistake,
	// not a size problem, and stays a plain transport error — consistent
	// with validate_plan's plan_path (see the errors.Is(rerr, errTooLarge)
	// branch there).
	resolvedFiles, err := h.resolveCompletionInputs(&args)
	if err != nil {
		var tooLarge *completionInputTooLargeError
		if errors.As(err, &tooLarge) {
			env := prependClamp(tooLargeEnvelope(args.SessionID, h.deps.Cfg.PostModel, tooLarge.bytes, h.deps.Cfg.MaxPayloadBytes,
				fmt.Sprintf("%s is %d bytes, over the %d-byte cap; shrink it or split the evidence into smaller chunks.",
					tooLarge.field, tooLarge.bytes, h.deps.Cfg.MaxPayloadBytes)), clamp)
			h.recordStat(statParams{
				tool:         "validate_completion",
				verdict:      env.Verdict,
				findings:     env.Findings,
				modelUsed:    env.ModelUsed,
				sessionID:    env.SessionID,
				payloadBytes: tooLarge.bytes,
			})
			return envelopeResult(env)
		}
		return nil, Envelope{}, err
	}

	// 2e. Check the RESOLVED content for path inputs that came back empty.
	// See resolvedEmptyPathInputs' doc comment for why this can't just
	// reuse the 2b check verbatim.
	var emptyPathFindings []verdict.Finding
	if emptyPathReasons := resolvedEmptyPathInputs(&args, resolvedFiles); len(emptyPathReasons) > 0 {
		if !hasNonEmptyEvidence(&args, resolvedFiles) {
			// The empty-resolved path is the ONLY evidence on the call:
			// hard reject, exactly as before FIX 2 — no paid reviewer call
			// with nothing to review.
			env := malformedEvidenceEnvelope(args.SessionID, emptyPathReasons[0], h.deps.Cfg.PostModel.String())
			clamped := prependClamp(env, clamp)
			h.recordStat(statParams{
				tool:         "validate_completion",
				verdict:      clamped.Verdict,
				findings:     clamped.Findings,
				modelUsed:    clamped.ModelUsed,
				sessionID:    clamped.SessionID,
				payloadBytes: totalCompletionBytes(resolvedFiles, args.FinalDiff),
			})
			return envelopeResult(clamped)
		}
		// Other real evidence exists (non-empty test_evidence, or another
		// non-empty file): FIX 2 — do not reject the call, but don't let
		// the gap go unremarked either. Merged into result.Findings once
		// the reviewer call returns (alongside clamp/codescene findings
		// below), so both the caller and whoever reads the envelope see
		// exactly which path came back empty.
		for _, reason := range emptyPathReasons {
			emptyPathFindings = append(emptyPathFindings, verdict.Finding{
				Severity:   verdict.SeverityMajor,
				Category:   verdict.CategoryInsufficientEvidence,
				Criterion:  "evidence_shape",
				Evidence:   reason,
				Suggestion: "Re-submit with non-empty content for this path, or drop the field if it was included by mistake.",
			})
		}
	}

	if args.Codescene != nil {
		args.Codescene.Normalize()
	}

	// 3. lightweight marker.
	lightweight := args.SessionID == ""

	// 5. payload-cap check. In lightweight mode the surfaced session_id stays
	// empty; otherwise we don't have the session yet, so use args.SessionID.
	if size := totalCompletionBytes(resolvedFiles, args.FinalDiff); size > h.deps.Cfg.MaxPayloadBytes {
		env := prependClamp(tooLargeEnvelope(args.SessionID, h.deps.Cfg.PostModel, size, h.deps.Cfg.MaxPayloadBytes,
			"Send a unified diff via final_diff, or split the call into smaller chunks."), clamp)
		h.recordStat(statParams{
			tool:         "validate_completion",
			verdict:      env.Verdict,
			findings:     env.Findings,
			modelUsed:    env.ModelUsed,
			sessionID:    env.SessionID,
			payloadBytes: size,
		})
		return envelopeResult(env)
	}

	// 5b. exit_contracts normalization. Runs after the payload-cap check
	// (input size already bounded) and before the evidence-shape guard so a
	// malformed exit_contracts list rejects fast regardless of session state.
	exitContracts, err := normalizeCompletionExitContracts(args.ExitContracts)
	if err != nil {
		return nil, Envelope{}, err
	}

	// 6. evidence-shape guard. Runs BEFORE session lookup so a broken payload
	// rejects fast regardless of session state. Cache hit → return the same
	// envelope without re-running the guard or hitting the reviewer.
	cacheKey := evidenceCacheKey(args.SessionID, args.FinalDiff, resolvedFiles, args.TestEvidence)
	if cached, ok := lookupCachedRejection(cacheKey); ok {
		c := prependClamp(cached, clamp)
		h.recordStat(statParams{
			tool:         "validate_completion",
			verdict:      c.Verdict,
			findings:     c.Findings,
			modelUsed:    c.ModelUsed,
			sessionID:    c.SessionID,
			cached:       true,
			payloadBytes: totalCompletionBytes(resolvedFiles, args.FinalDiff),
		})
		return envelopeResult(c)
	}
	if reason := checkEvidenceShape(args.FinalDiff, resolvedFiles); reason != "" {
		env := malformedEvidenceEnvelope(args.SessionID, reason, h.deps.Cfg.PostModel.String())
		storeRejection(cacheKey, env)
		clamped := prependClamp(env, clamp)
		h.recordStat(statParams{
			tool:         "validate_completion",
			verdict:      clamped.Verdict,
			findings:     clamped.Findings,
			modelUsed:    clamped.ModelUsed,
			sessionID:    clamped.SessionID,
			payloadBytes: totalCompletionBytes(resolvedFiles, args.FinalDiff),
		})
		return envelopeResult(clamped)
	}

	// 7/8. session lookup + spec selection.
	var sess *session.Session
	var spec session.TaskSpec
	var sessID string
	var majorPreFindings []verdict.Finding
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
			env := prependClamp(notFoundEnvelope(args.SessionID, h.deps.Cfg.PostModel), clamp)
			h.recordStat(statParams{
				tool:         "validate_completion",
				verdict:      env.Verdict,
				findings:     env.Findings,
				modelUsed:    env.ModelUsed,
				sessionID:    env.SessionID,
				payloadBytes: totalCompletionBytes(resolvedFiles, args.FinalDiff),
			})
			return envelopeResult(env)
		}
		spec = sess.Spec
		sessID = sess.ID
		majorPreFindings = majorFindings(sess.PreFindings)
	}

	model, rendered, err := h.resolveModelAndRender(
		args.ModelOverride,
		h.deps.Cfg.PostModel,
		func() (prompts.Output, error) {
			return prompts.RenderPost(prompts.PostInput{
				Spec:                           spec,
				Summary:                        args.Summary,
				Files:                          toPromptFiles(resolvedFiles),
				FinalDiff:                      args.FinalDiff,
				TestEvidence:                   args.TestEvidence,
				MajorPreFindings:               majorPreFindings,
				ReferencedPathsMissingEvidence: referencedPathsMissingEvidence(args.Summary, resolvedFiles, args.FinalDiff),
				ExitContracts:                  exitContracts,
				ExitContractsInferred:          args.ExitContractsInferred,
				Codescene:                      args.Codescene,
			})
		},
		"render post prompt",
	)
	if err != nil {
		return nil, Envelope{}, err
	}

	result, modelUsed, ms, partialRaw, err := h.review(ctx, model, rendered, maxTokens)
	// In lightweight mode sess is nil so the helper skips TTL application;
	// otherwise sess carries the resolved session and TTL fields are filled in.
	if r, env, handled, retErr := h.handlePerTaskReviewErr(perTaskReviewErrInputs{
		Err:        err,
		SessionID:  sessID,
		Model:      model,
		PartialRaw: partialRaw,
		EnvVar:     "ANTI_TANGENT_PER_TASK_MAX_TOKENS",
		Clamp:      clamp,
		Sess:       sess,
	}); handled {
		if retErr == nil {
			h.recordStat(statParams{
				tool:         "validate_completion",
				verdict:      env.Verdict,
				findings:     env.Findings,
				modelUsed:    env.ModelUsed,
				reviewMS:     env.ReviewMS,
				partial:      env.Partial,
				sessionID:    env.SessionID,
				payloadBytes: totalCompletionBytes(resolvedFiles, args.FinalDiff),
			})
		}
		return r, env, retErr
	}

	if clamp.Severity != "" {
		result.Findings = append([]verdict.Finding{clamp}, result.Findings...)
	}
	if len(emptyPathFindings) > 0 {
		// FIX 2's soft case: merged in here (same pattern as clamp/
		// codescene below) rather than sent through to the reviewer
		// prompt, so this stays a server-computed finding independent of
		// what the reviewer LLM says.
		result.Findings = append(emptyPathFindings, result.Findings...)
	}
	result = verdict.FinalizeVerdict(result)

	if !lightweight {
		h.deps.Sessions.SetPostFindings(sess.ID, result.Findings)
		// Re-fetch after SetPostFindings so LastAccessed reflects the final mutation.
		if refreshed, ok := h.deps.Sessions.Get(sess.ID); ok {
			sess = refreshed
		}
		sessID = sess.ID
	}

	if cs := codesceneFindings(h.deps.Cfg.Codescene, args.Codescene); len(cs) > 0 {
		result.Findings = append(cs, result.Findings...)
		result = verdict.FinalizeVerdict(result)
	}

	env := Envelope{
		SessionID:  sessID,
		Verdict:    string(result.Verdict),
		Findings:   result.Findings,
		NextAction: result.NextAction,
		ModelUsed:  modelUsed,
		ReviewMS:   ms,
		Partial:    result.Partial,
	}
	if !lightweight {
		env = h.withSessionTTL(env, sess)
	}
	if isSubmissionDefectOnly(env.Findings) {
		env.SubmissionDefectOnly = true
		env.NextAction = resubmitNextAction + env.NextAction
	}

	if !lightweight && sess.PlanRunID != "" {
		sev, _, _ := stats.CountFindings(env.Findings)
		state := planrun.StateMissing
		if args.Codescene != nil {
			if args.Codescene.Ran {
				state = planrun.StateRan
			} else {
				state = planrun.StateSkipped
			}
		}
		if !h.deps.PlanRuns.UpdateRow(sess.PlanRunID, sess.ID, func(row *planrun.TaskRow) {
			row.PostVerdict = env.Verdict
			row.Severity = sev
			row.SubmissionOnly = env.SubmissionDefectOnly
			row.Codescene = args.Codescene
			row.CodesceneState = state
			row.CompletedAt = time.Now().UTC()
		}) {
			slog.Warn("plan run row update failed; run or row unknown",
				"plan_run_id", sess.PlanRunID, "session_id", sess.ID)
		}

		// Best-effort ledger append. Never lets a write failure change the
		// result: logged and swallowed, not returned.
		//
		// Snapshot, not Get: this walks run.Rows after the lock is released,
		// and concurrent subagents under the same plan run may be appending.
		if run, ok := h.deps.PlanRuns.Snapshot(sess.PlanRunID); ok {
			for _, row := range run.Rows {
				if row.SessionID == sess.ID {
					if err := h.deps.PlanLedger.Append(run, row); err != nil {
						slog.Warn("plan ledger append failed", "err", err)
					}
					break
				}
			}
		}
	}

	h.recordStat(statParams{
		tool:         "validate_completion",
		verdict:      env.Verdict,
		findings:     env.Findings,
		modelUsed:    env.ModelUsed,
		reviewMS:     env.ReviewMS,
		partial:      env.Partial,
		sessionID:    env.SessionID,
		payloadBytes: totalCompletionBytes(resolvedFiles, args.FinalDiff),
	})
	return envelopeResult(env)
}

func (h *handlers) ValidatePlan(ctx context.Context, _ *mcp.CallToolRequest, args ValidatePlanArgs) (_ *mcp.CallToolResult, _ verdict.PlanResult, retErr error) {
	if args.PlanText == "" && args.PlanPath == "" {
		return nil, verdict.PlanResult{}, errors.New("plan_text or plan_path is required")
	}
	if args.PlanText != "" && args.PlanPath != "" {
		return nil, verdict.PlanResult{}, errors.New("plan_text and plan_path are mutually exclusive")
	}
	if args.Mode != "" && args.Mode != "quick" && args.Mode != "thorough" {
		return nil, verdict.PlanResult{}, errors.New(`mode must be "quick" or "thorough"`)
	}

	maxTokens, clamp, err := effectiveMaxTokens(args.MaxTokensOverride, h.deps.Cfg.PlanMaxTokens, h.deps.Cfg.MaxTokensCeiling)
	if err != nil {
		return nil, verdict.PlanResult{}, err
	}

	projectKnowledge := strings.TrimSpace(args.ProjectKnowledge)
	pkBytes := len(projectKnowledge)

	planText := args.PlanText
	var planSrc fileSource
	var contextFiles []contextFile
	var contextBytes int
	var repoRoot string
	// repoRootUnusable is the reason repo_root could not be resolved, empty
	// when it was usable or was never supplied. See the resolution block
	// below and repoRootUnusableFinding.
	var repoRootUnusable string

	// EXACTLY ONE stderr line per validate_plan call (CLAUDE.md's logging
	// convention), emitted on EXIT rather than on entry. The old entry line
	// sat BELOW both resolveContextPaths returns, so a context_paths failure
	// — the input a caller most often gets wrong — logged nothing at all,
	// and its own comment claiming "the only earlier returns are
	// argument-validation errors and the plan_path too-large envelope" was
	// false. An entry line also cannot carry the verdict or the duration,
	// because neither exists yet: no validate_plan call was observable in
	// the log. This is the deferred log-vars pattern from prime_handler.go /
	// extract_handler.go — each branch sets logOutcome (and logVerdict where
	// there is one) before returning, and the closure reads the live locals
	// for the byte counts, so the single line always describes the actual
	// outcome. Registered here, after argument validation, because a
	// malformed-argument return produces neither a review nor an envelope.
	start := time.Now()
	logVerdict := verdict.Verdict("")
	logOutcome := "success"
	defer func() {
		// planText is empty on the plan_path too-large exit (resolveFileInput
		// returns no content there), so fall back to the size the stat saw.
		planBytesLogged := len(planText)
		if planBytesLogged == 0 && planSrc.Bytes > 0 {
			planBytesLogged = planSrc.Bytes
		}
		if retErr != nil && logOutcome == "success" {
			logOutcome = "error"
		}
		slog.Info("validate_plan",
			slog.String("tool", "validate_plan"),
			slog.Int64("duration_ms", time.Since(start).Milliseconds()),
			slog.String("verdict", string(logVerdict)),
			slog.String("outcome", logOutcome),
			slog.Int("plan_bytes", planBytesLogged),
			slog.Int("project_knowledge_bytes", pkBytes),
			slog.Int("context_files", len(contextFiles)),
			slog.Int("context_bytes", contextBytes),
			slog.Any("context_paths", contextFilePaths(contextFiles)),
			slog.Bool("repo_root", repoRoot != ""),
		)
	}()

	if args.PlanPath != "" {
		var rerr error
		planText, planSrc, rerr = resolveFileInput(
			args.PlanPath, h.deps.Cfg.PlanRoots, h.deps.Cfg.PlanMaxPayloadBytes)
		if errors.Is(rerr, errTooLarge) {
			total := planSrc.Bytes + pkBytes
			pr := prependPlanClamp(
				tooLargePlanResult(total, planSrc.Bytes, pkBytes, 0, h.deps.Cfg.PlanMaxPayloadBytes), clamp)
			h.recordStat(statParams{
				tool:         "validate_plan",
				verdict:      string(pr.PlanVerdict),
				findings:     planFindings(pr),
				modelUsed:    h.deps.Cfg.PlanModel.String(),
				payloadBytes: total,
			})
			logOutcome, logVerdict = "plan_too_large", pr.PlanVerdict
			return planEnvelopeResult(pr, planSummaryMeta{ModelUsed: h.deps.Cfg.PlanModel.String(), Source: planSrc.String()})
		}
		if rerr != nil {
			logOutcome = "plan_path_error"
			return nil, verdict.PlanResult{}, rerr
		}
	}

	// repo_root resolves BEFORE any attachment is read, and a failure to
	// resolve it is NOT fatal.
	//
	// Order: resolving it after resolveContextPaths meant a typo'd or
	// since-deleted repo_root killed the whole review only AFTER every
	// attached file had been read off disk — the most expensive part of the
	// call, thrown away for an optional argument.
	//
	// Non-fatal: repoRoot == "" already means "skip the disk tier" (see
	// file_consistency.go, whose own comment rules out "refusing to review a
	// plan for want of an optional argument"). Degrading to that is the same
	// outcome as omitting repo_root.
	//
	// Silently, though, it was NOT the same outcome to the caller: envelope,
	// summary and findings became byte-identical to a call that never passed
	// repo_root, while controller.md §5.8 and the tool description both
	// promise repo_root "enables the disk tier". resolveDirInput also
	// rejects a plain relative path — an ordinary caller bug — and that was
	// swallowed too. The reason is therefore captured here and surfaced
	// in-band as a finding (repoRootUnusableFinding), not just on stderr:
	// findings are what callers parse.
	if args.RepoRoot != "" {
		resolved, rerr := resolveDirInput(args.RepoRoot, h.deps.Cfg.PlanRoots)
		if rerr != nil {
			repoRootUnusable = rerr.Error()
			slog.Warn("validate_plan: repo_root unusable, skipping the Create/Modify disk tier",
				slog.String("tool", "validate_plan"),
				slog.String("err", repoRootUnusable))
		} else {
			repoRoot = resolved
		}
	}

	var cerr error
	contextFiles, contextBytes, cerr = resolveContextPaths(args.ContextPaths, h.deps.Cfg)
	if cerr != nil {
		var tle *contextTooLargeError
		if errors.As(cerr, &tle) {
			pr := prependPlanDeprecation(
				prependPlanClamp(contextTooLargePlanResult(tle), clamp), args.PlanText != "")
			h.recordStat(statParams{
				tool:         "validate_plan",
				verdict:      string(pr.PlanVerdict),
				findings:     planFindings(pr),
				modelUsed:    h.deps.Cfg.PlanModel.String(),
				payloadBytes: len(planText) + pkBytes,
			})
			logOutcome, logVerdict = "context_too_large", pr.PlanVerdict
			return planEnvelopeResult(pr, planSummaryMeta{ModelUsed: h.deps.Cfg.PlanModel.String(), Source: planSrc.String()})
		}
		logOutcome = "context_paths_error"
		return nil, verdict.PlanResult{}, cerr
	}

	planBytes := len(planText)
	if total := planBytes + pkBytes; total > h.deps.Cfg.PlanMaxPayloadBytes {
		pr := prependPlanDeprecation(
			prependPlanClamp(tooLargePlanResult(total, planBytes, pkBytes, contextBytes, h.deps.Cfg.PlanMaxPayloadBytes), clamp),
			args.PlanText != "")
		h.recordStat(statParams{
			tool:         "validate_plan",
			verdict:      string(pr.PlanVerdict),
			findings:     planFindings(pr),
			modelUsed:    h.deps.Cfg.PlanModel.String(),
			payloadBytes: total + contextBytes,
		})
		logOutcome, logVerdict = "payload_too_large", pr.PlanVerdict
		return planEnvelopeResult(pr, planSummaryMeta{ModelUsed: h.deps.Cfg.PlanModel.String(), Source: planSrc.String(), ContextFiles: contextSources(contextFiles)})
	}
	tasks, _ := planparser.SplitTasks(planText)
	tasksTotal := len(tasks)
	tasksWithHeader := 0
	for _, rt := range tasks {
		if rt.HasStructuredHeader {
			tasksWithHeader++
		}
	}
	if len(tasks) == 0 {
		pr := prependPlanDeprecation(
			prependPlanClamp(noHeadingsPlanResult(), clamp), args.PlanText != "")
		h.recordStat(statParams{
			tool:            "validate_plan",
			verdict:         string(pr.PlanVerdict),
			findings:        planFindings(pr),
			modelUsed:       h.deps.Cfg.PlanModel.String(),
			payloadBytes:    planBytes + pkBytes + contextBytes,
			tasksTotal:      tasksTotal,
			tasksWithHeader: tasksWithHeader,
		})
		logOutcome, logVerdict = "no_headings", pr.PlanVerdict
		return planEnvelopeResult(pr, planSummaryMeta{ModelUsed: h.deps.Cfg.PlanModel.String(), Source: planSrc.String(), ContextFiles: contextSources(contextFiles)})
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
		logOutcome = "model_error"
		return nil, verdict.PlanResult{}, err
	}
	rendered, err := renderPlanReview(renderPlanReviewInputs{
		PlanText:         planText,
		ProjectKnowledge: projectKnowledge,
		Tasks:            tasks,
		ChunkSize:        h.deps.Cfg.PlanTasksPerChunk,
		Mode:             args.Mode,
		ContextFiles:     toPromptContextFiles(contextFiles),
	})
	if err != nil {
		logOutcome = "render_error"
		return nil, verdict.PlanResult{}, err
	}
	// The attached set is re-sent WHOLE on every reviewer call of the round —
	// the findings-only Pass 1 plus one call per task chunk — so billing it
	// once under-reported a 3-call chunked round's attachment traffic by 3x.
	// Plan text has the same property but is deliberately left unmultiplied:
	// payload_bytes has meant "the plan payload the caller submitted" since
	// the field existed, and re-scaling it now would make every historical
	// row incomparable. contextBytes is new in 0.17.0 and has no history to
	// break. Only the paths below that actually render a review use this;
	// the too-large / no-headings early exits never chose a chunking, so
	// they keep the plain figure.
	contextPayloadBytes := contextBytes * rendered.reviewerCalls()
	cacheKey := planPassCacheKey(planText, projectKnowledge, args.Mode, model.String(), maxTokens, args.MaxTokensOverride, rendered, repoRoot)
	if cached, cachedModelUsed, ok := h.planCache().lookup(cacheKey, planSrc.String()); ok {
		// Deprecation is a property of THIS call's input (plan_text vs.
		// plan_path), not of the cached plan content, so it is applied here
		// rather than stored on the entry — a plan_path call must not
		// inherit a deprecation finding left by an earlier plan_text call
		// (or vice versa) that happened to produce the same cache key.
		// lookup() already recomputed SummaryBlock (with THIS call's
		// provenance, never the stored entry's) without the deprecation
		// finding; recompute it again now that the finding may have been
		// added, so SummaryBlock's count/list stays consistent with
		// PlanFindings.
		//
		// The repo_root advisory rides the same rails for the same reason:
		// it describes THIS call's repo_root argument, not the cached plan
		// content, and an unusable repo_root resolves to repoRoot == "",
		// which is exactly the cache key a call that omitted repo_root
		// produces — so the entry really can be shared between the two.
		//
		// finish() ONLY — no applyPreLadder, and above all no verdict ladder:
		// the entry was finalized before it was stored, and
		// normalizePlanUnverifiableFindings is not proven idempotent, so
		// re-running the ladder on a cached entry is not a no-op. See
		// planCallContext for the three call orders and for the two
		// divergences (no checkFileConsistency, no store) this path keeps on
		// purpose.
		cachedCall := planCallContext{
			PlanRuns:         h.deps.PlanRuns,
			Source:           planSrc.String(),
			ModelUsed:        cachedModelUsed,
			ReviewMS:         0,
			UsedPlanText:     args.PlanText != "",
			RepoRootUnusable: repoRootUnusable,
			ContextFiles:     contextSources(contextFiles),
		}
		cachedCall.finish(&cached)
		cachedMeta := cachedCall.meta()
		// The cache key uses the configured model ref. cachedModelUsed is the
		// provider-reported model from the original review being reused.
		h.recordStat(statParams{
			tool:      "validate_plan",
			verdict:   string(cached.PlanVerdict),
			findings:  planFindings(cached),
			modelUsed: cachedModelUsed,
			reviewMS:  0,
			partial:   cached.Partial,
			cached:    true,
			// contextBytes, NOT contextPayloadBytes: a cache hit makes no
			// reviewer call at all, so it sends the attached set zero times.
			// Multiplying by reviewerCalls() here billed a 3-call chunked
			// round's worth of attachment traffic for a round that sent none.
			payloadBytes:    planBytes + pkBytes + contextBytes,
			tasksTotal:      tasksTotal,
			tasksWithHeader: tasksWithHeader,
		})
		logOutcome, logVerdict = "cache_hit", cached.PlanVerdict
		return planEnvelopeResultFinalized(cached, cachedMeta)
	}

	var pr verdict.PlanResult
	var modelUsed string
	var ms int64
	var partialRaw []byte
	if rendered.Single != nil {
		pr, modelUsed, ms, partialRaw, err = h.reviewPlanSingle(ctx, model, *rendered.Single, maxTokens)
	} else {
		pr, modelUsed, ms, partialRaw, err = h.reviewPlanChunked(ctx, model, rendered, maxTokens)
	}
	// `pr` carries any partial state already collected before the truncation
	// point — for the chunked path that means Pass-1 plan_findings plus any
	// cleanly-closed Pass-2 chunk task results. Threading it through as
	// `prior` ensures those aren't dropped if the truncating chunk's bytes
	// yield further recovery.
	// Computed BEFORE the truncation handler so the truncation path can carry
	// it too: this check needs no reviewer and cannot be truncated, so
	// letting a truncated response drop it lost a finding the server already
	// knew for certain.
	fileConsistency := checkFileConsistency(tasks, repoRoot)
	// herr, not retErr: retErr is this function's NAMED return, read by the
	// deferred exit logger. Shadowing it here would still work (the return
	// statement assigns through), but the name would mean two different
	// things in one function.
	//
	// call is built ONCE and used by BOTH exits below — the truncation
	// recovery inside handlePlanReviewErr and the fresh-review tail after it.
	// That shared construction is the point: the recovery path used to
	// hand-assemble its own tail and silently lacked three of this one's
	// steps. See planCallContext.
	call := planCallContext{
		PlanRuns:         h.deps.PlanRuns,
		Source:           planSrc.String(),
		ModelUsed:        modelUsed,
		ReviewMS:         ms,
		UsedPlanText:     args.PlanText != "",
		RepoRootUnusable: repoRootUnusable,
		Clamp:            clamp,
		ContextFiles:     contextSources(contextFiles),
		FileConsistency:  fileConsistency,
		Tasks:            tasks,
	}
	if r, p, handled, herr := h.handlePlanReviewErr(planReviewErrInputs{
		Err:        err,
		Model:      model,
		PartialRaw: partialRaw,
		Prior:      pr,
		Call:       call,
	}); handled {
		if herr == nil {
			h.recordStat(statParams{
				tool:            "validate_plan",
				verdict:         string(p.PlanVerdict),
				findings:        planFindings(p),
				modelUsed:       model.String(),
				reviewMS:        ms,
				partial:         p.Partial,
				payloadBytes:    planBytes + pkBytes + contextPayloadBytes,
				tasksTotal:      tasksTotal,
				tasksWithHeader: tasksWithHeader,
			})
			logOutcome, logVerdict = "truncated", p.PlanVerdict
		} else {
			logOutcome = "review_error"
		}
		return r, p, herr
	}
	call.applyPreLadder(&pr)
	// finalizePlanVerdict (not finalizePlanResult) here: it runs the
	// normalize/calibrate/FinalizePlanVerdict ladder without touching
	// SummaryBlock, so the PlanRunID assignment and the two per-call
	// advisories below can all land before SummaryBlock is computed — once,
	// authoritatively — rather than three times across this path. The ladder
	// stays at the call site rather than inside a planCallContext method
	// because the cache-hit path must NOT run it; see planCallContext.
	finalizePlanVerdict(&pr)
	// Mint BEFORE store, so the cached entry carries the plan_run_id and a
	// later cache hit reuses this run instead of minting a second one for the
	// same plan. finish()'s own mint below is guarded on PlanRunID == "" and
	// is therefore a no-op here.
	call.mintPlanRunID(&pr)
	// Cache the result WITHOUT the per-call advisories: both the deprecation
	// notice and the repo_root advisory are properties of which arguments
	// THIS call used, not of the plan content, so a plan_text call and a
	// plan_path call with byte-identical content correctly share one cache
	// entry. Keying on "which input arg" would split the cache in two for no
	// benefit. finish() applies them per call, after the entry is stored —
	// see the mirrored comment on the cache-hit branch above.
	h.planCache().store(cacheKey, pr, modelUsed)
	call.finish(&pr)
	meta := call.meta()
	h.recordStat(statParams{
		tool:            "validate_plan",
		verdict:         string(pr.PlanVerdict),
		findings:        planFindings(pr),
		modelUsed:       modelUsed,
		reviewMS:        ms,
		partial:         pr.Partial,
		payloadBytes:    planBytes + pkBytes + contextPayloadBytes,
		tasksTotal:      tasksTotal,
		tasksWithHeader: tasksWithHeader,
	})
	logVerdict = pr.PlanVerdict
	return planEnvelopeResultFinalized(pr, meta)
}

type renderedPlanChunk struct {
	Tasks  []planparser.RawTask
	Prompt prompts.Output
}

type renderedPlanReview struct {
	Single       *prompts.Output
	FindingsOnly *prompts.Output
	Chunks       []renderedPlanChunk
}

// reviewerCalls is how many provider calls this render costs: one for a
// single-call plan, or the findings-only Pass 1 plus one per task chunk.
// Used to bill the attached set correctly in stats — the whole attachment
// payload is re-sent on EVERY call of a chunked round, so counting it once
// under-reported a 3-call round's attachment traffic by 3x.
func (r renderedPlanReview) reviewerCalls() int {
	if r.Single != nil {
		return 1
	}
	n := len(r.Chunks)
	if r.FindingsOnly != nil {
		n++
	}
	return n
}

func (r renderedPlanReview) cachePrompts() []planCachePrompt {
	toPrompt := func(o prompts.Output) planCachePrompt {
		return planCachePrompt{System: o.System, UserPrefix: o.UserPrefix, UserSuffix: o.UserSuffix}
	}
	if r.Single != nil {
		return []planCachePrompt{toPrompt(*r.Single)}
	}
	out := make([]planCachePrompt, 0, 1+len(r.Chunks))
	if r.FindingsOnly != nil {
		out = append(out, toPrompt(*r.FindingsOnly))
	}
	for _, chunk := range r.Chunks {
		out = append(out, toPrompt(chunk.Prompt))
	}
	return out
}

// renderPlanReviewInputs bundles the inputs to renderPlanReview. Carrying
// these on a struct keeps the helper signature narrow (1 arg vs. 5) and
// matches CodeScene's "max arguments = 4" code-health threshold; mirrors
// the planReviewErrInputs pattern at review_error.go.
type renderPlanReviewInputs struct {
	PlanText         string
	ProjectKnowledge string
	Tasks            []planparser.RawTask
	ChunkSize        int
	Mode             string
	ContextFiles     []prompts.ContextFile
}

func renderPlanReview(in renderPlanReviewInputs) (renderedPlanReview, error) {
	// One nonce for the whole review, derived once here rather than left to
	// each Render* call: the chunked path below issues a findings-only call
	// plus one call per chunk, all sharing in.ContextFiles. Because the
	// nonce is DETERMINISTIC (prompts.DeriveContextFilesNonce), each of
	// those calls would arrive at the identical value on its own — deriving
	// it once up front and passing it explicitly just avoids repeating the
	// derivation per chunk, and keeps the byte-identical-UserPrefix
	// invariant (required for the provider-side prompt cache, see
	// prompts.PlanChunkInput.ContextFilesNonce) obvious rather than
	// incidental. Determinism ALSO matters one call up: mcpsrv's own
	// plan-pass cache (planPassCacheKey) hashes the rendered prompt text, so
	// a nonce that varied per render would make every validate_plan call
	// carrying context_paths a permanent cache miss.
	var contextFilesNonce string
	if len(in.ContextFiles) > 0 {
		nonce, err := prompts.DeriveContextFilesNonce(in.ContextFiles)
		if err != nil {
			return renderedPlanReview{}, fmt.Errorf("derive context files nonce: %w", err)
		}
		contextFilesNonce = nonce
	}
	if len(in.Tasks) <= in.ChunkSize {
		rendered, err := prompts.RenderPlan(prompts.PlanInput{
			PlanText:          in.PlanText,
			ProjectKnowledge:  in.ProjectKnowledge,
			Mode:              in.Mode,
			ContextFiles:      in.ContextFiles,
			ContextFilesNonce: contextFilesNonce,
		})
		if err != nil {
			return renderedPlanReview{}, fmt.Errorf("render plan prompt: %w", err)
		}
		return renderedPlanReview{Single: &rendered}, nil
	}
	if in.ChunkSize <= 0 {
		return renderedPlanReview{}, fmt.Errorf("renderPlanReview: chunkSize must be positive, got %d", in.ChunkSize)
	}
	findingsOnly, err := prompts.RenderPlanFindingsOnly(prompts.PlanInput{
		PlanText:          in.PlanText,
		ProjectKnowledge:  in.ProjectKnowledge,
		Mode:              in.Mode,
		ContextFiles:      in.ContextFiles,
		ContextFilesNonce: contextFilesNonce,
	})
	if err != nil {
		return renderedPlanReview{}, fmt.Errorf("render plan_findings_only: %w", err)
	}
	rendered := renderedPlanReview{FindingsOnly: &findingsOnly}
	for i := 0; i < len(in.Tasks); i += in.ChunkSize {
		end := i + in.ChunkSize
		if end > len(in.Tasks) {
			end = len(in.Tasks)
		}
		chunkTasks := in.Tasks[i:end]
		chunkPrompt, err := prompts.RenderPlanTasksChunk(prompts.PlanChunkInput{
			PlanText:          in.PlanText,
			ProjectKnowledge:  in.ProjectKnowledge,
			ChunkTasks:        chunkTasks,
			Mode:              in.Mode,
			ContextFiles:      in.ContextFiles,
			ContextFilesNonce: contextFilesNonce,
		})
		if err != nil {
			return renderedPlanReview{}, fmt.Errorf("render plan_tasks_chunk: %w", err)
		}
		rendered.Chunks = append(rendered.Chunks, renderedPlanChunk{Tasks: chunkTasks, Prompt: chunkPrompt})
	}
	return rendered, nil
}

// reviewPlanSingle runs one reviewer call for the entire plan — the
// behavior used today for plans whose task count is at or below
// h.deps.Cfg.PlanTasksPerChunk.
// On ErrResponseTruncated, the returned []byte carries the partial response
// bytes (possibly empty if the provider returned none) so the caller can
// attempt partial-findings recovery via recoverPartialPlanFindings.
func (h *handlers) reviewPlanSingle(ctx context.Context, model config.ModelRef, rendered prompts.Output, maxTokens int) (verdict.PlanResult, string, int64, []byte, error) {
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

// populateNormativeTestBodies fills in pr.Tasks[i].NormativeTestBodies from
// the matching planparser.RawTask body. The reviewer is prompted to emit
// 1-based TaskIndex, but plan_schema.json accepts minimum:0 and some
// reviewers (and the parser_partial fixtures) emit 0-based; detect the base
// from the lowest non-negative index and apply uniformly. Tasks whose index
// is out of range after de-basing are left alone (defensive — chunked-path
// reviewer responses occasionally drift on task_index, and we'd rather emit
// no extraction for that task than panic or misattribute).
func populateNormativeTestBodies(pr *verdict.PlanResult, tasks []planparser.RawTask) {
	base := 1
	for _, t := range pr.Tasks {
		if t.TaskIndex == 0 {
			base = 0
			break
		}
	}
	for i := range pr.Tasks {
		idx := pr.Tasks[i].TaskIndex - base
		if idx < 0 || idx >= len(tasks) {
			continue
		}
		pr.Tasks[i].NormativeTestBodies = planparser.ExtractNormativeTestBodies(tasks[idx].Body)
	}
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

// tooLargePlanResult builds the rejection PlanResult for a cumulative
// payload-too-large hit on (plan content, from either plan_text or plan_path,
// + project_knowledge). total and the cap comparison deliberately exclude
// contextBytes — context_paths has its own separate budget enforced by
// resolveContextPaths/contextTooLargePlanResult — but contextBytes is still
// named in the evidence string so the caller can see the full cost picture
// even when it isn't what tripped this particular cap. Critical so the
// ladder derives fail from one critical, matching the explicit Verdict: fail.
func tooLargePlanResult(total, planBytes, pkBytes, contextBytes, limit int) verdict.PlanResult {
	return verdict.PlanResult{
		PlanVerdict: verdict.VerdictFail,
		PlanFindings: []verdict.Finding{{
			Severity:   verdict.SeverityCritical,
			Category:   verdict.CategoryTooLarge,
			Criterion:  "payload",
			Evidence:   fmt.Sprintf("payload %d bytes > cap %d (plan: %d, project_knowledge: %d, context_paths: %d)", total, limit, planBytes, pkBytes, contextBytes),
			Suggestion: "Split the plan into smaller chunks or pass a unified diff.",
		}},
		Tasks:      []verdict.PlanTaskResult{},
		NextAction: "Reduce plan size and retry.",
	}
}

// contextTooLargePlanResult renders a context_paths cap breach as the same
// too-large envelope shape as an oversized plan, but with the attachment's
// own message — which names the offending file (or the running total) and
// the env var that governs it. A cap breach is content-too-large, so it is
// an envelope; a bad path is bad input, so it stays a transport error.
// See design §3.3.
func contextTooLargePlanResult(err *contextTooLargeError) verdict.PlanResult {
	return verdict.PlanResult{
		PlanVerdict: verdict.VerdictFail,
		PlanFindings: []verdict.Finding{{
			Severity:   verdict.SeverityCritical,
			Category:   verdict.CategoryTooLarge,
			Criterion:  "context_paths",
			Evidence:   err.Error(),
			Suggestion: "Attach fewer or smaller files, or raise the named cap.",
		}},
		Tasks:      []verdict.PlanTaskResult{},
		NextAction: "Reduce the attached file set and retry.",
	}
}

// planEnvelopeResult marshals the PlanResult into a CallToolResult (mirrors envelopeResult).
//
// Two universal post-processing steps run here so every exit path — happy,
// partial-recovery, legacy-truncation, too-large, no-headings — gets them
// for free:
//
//  1. finalizePlanResult runs unverifiable-claim rollup,
//     unverifiable-only calibration, and FinalizePlanVerdict (per-task +
//     plan-level severity ladder + noise_cluster + plan-quality sanity).
//  2. SummaryBlock is populated with the rendered paste-ready text block.
func planEnvelopeResult(pr verdict.PlanResult, meta planSummaryMeta) (*mcp.CallToolResult, verdict.PlanResult, error) {
	return planEnvelopeResultFinalized(finalizePlanResult(pr, meta), meta)
}

// finalizePlanVerdict runs the shared normalize/calibrate/FinalizePlanVerdict
// ladder without touching SummaryBlock. Split out of finalizePlanResult so
// ValidatePlan's fresh-review happy path can run the ladder before the final
// PlanRunID / deprecation-finding state is known, then compute
// formatPlanSummary exactly once after those are settled, instead of the
// three redundant computations this used to produce.
//
// Order is load-bearing:
//  1. rollup unverifiable-codebase-claim findings (else calibration sees
//     noise);
//  2. calibrate verdict for the unverifiable-only case (preserves the
//     v0.4.0 verdict→quality mapping for plans whose only findings are
//     unverifiable claims);
//  3. FinalizePlanVerdict (per-task + plan-level severity ladder +
//     noise_cluster + ApplyPlanQualitySanity rerun).
//
// FinalizePlanVerdict's ApplyPlanQualitySanity rerun replaces the
// stand-alone call this function previously made.
func finalizePlanVerdict(pr *verdict.PlanResult) {
	normalizePlanUnverifiableFindings(pr)
	calibratePlanVerdictForUnverifiableOnly(pr)
	verdict.FinalizePlanVerdict(pr)
}

func finalizePlanResult(pr verdict.PlanResult, meta planSummaryMeta) verdict.PlanResult {
	finalizePlanVerdict(&pr)
	pr.SummaryBlock = formatPlanSummary(pr, meta)
	return pr
}

func planEnvelopeResultFinalized(pr verdict.PlanResult, meta planSummaryMeta) (*mcp.CallToolResult, verdict.PlanResult, error) {
	body, err := json.MarshalIndent(struct {
		verdict.PlanResult
		ModelUsed string `json:"model_used"`
		ReviewMS  int64  `json:"review_ms"`
	}{pr, meta.ModelUsed, meta.ReviewMS}, "", "  ")
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
	rendered renderedPlanReview,
	maxTokens int,
) (verdict.PlanResult, string, int64, []byte, error) {
	if rendered.FindingsOnly == nil {
		return verdict.PlanResult{}, "", 0, nil, errors.New("reviewPlanChunked: missing plan_findings_only prompt")
	}
	rv, err := h.deps.Reviews.Get(model.Provider)
	if err != nil {
		return verdict.PlanResult{}, "", 0, nil, err
	}

	var totalMs int64
	var modelUsed string

	// ----- Pass 1: plan-findings only -----
	// Deliberately NO CachePrefix here. Anthropic keys a cache entry on the
	// full request prefix — tools, then system, then the cached message
	// content — and this call's tools block is verdict.PlanFindingsOnlySchema(),
	// while every chunk call below sends verdict.TasksOnlySchema(). Those
	// differ, so an entry Pass 1 wrote could never be read by a chunk call:
	// it would just pay the ~1.25x cache-write premium for zero reads.
	// Sending the full rendered.FindingsOnly.User as plain text costs the
	// same 1.0x as before this feature existed. Chunk 1 (reviewOnePlanChunk)
	// is the one that actually writes the shared prefix; chunks 2+ read it.
	// Do not add CachePrefix back here without re-deriving the economics in
	// the design doc — see docs/superpowers/specs (0.16.0 caching section).
	req := providers.Request{
		Model:      model.Model,
		System:     rendered.FindingsOnly.System,
		User:       rendered.FindingsOnly.User,
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
		req.User = rendered.FindingsOnly.User + "\n\n" + verdict.RetryHint()
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
		Tasks:        make([]verdict.PlanTaskResult, 0),
	}

	// ----- Passes 2..K+1: per-task chunks -----
	for _, chunk := range rendered.Chunks {
		chunkResult, ms, partialRaw, err := h.reviewOnePlanChunk(ctx, rv, model, chunk.Prompt, chunk.Tasks, maxTokens)
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

	expectedTasks := 0
	for _, chunk := range rendered.Chunks {
		expectedTasks += len(chunk.Tasks)
	}
	if len(result.Tasks) != expectedTasks {
		return verdict.PlanResult{}, "", 0, nil,
			fmt.Errorf("chunked plan review returned %d task results, expected %d",
				len(result.Tasks), expectedTasks)
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
	rendered prompts.Output,
	chunkTasks []planparser.RawTask,
	maxTokens int,
) (verdict.TasksOnly, int64, []byte, error) {
	// CachePrefix is set once here, outside the attempt closure below. Pass 1
	// (reviewPlanChunked) deliberately sends no breakpoint of its own — its
	// tools block differs from this one, so its entry could never be read
	// here — which makes THIS the first call to actually write the shared
	// head to the cache: chunk 1 pays the ~1.25x write, chunks 2+ read it
	// back at ~0.1x input price instead of re-billing the whole plan. The
	// values passed to attempt() are the SUFFIX only — never the full
	// rendered.User — or the prefix would be duplicated into the body and
	// the cache would never match.
	req := providers.Request{
		Model:       model.Model,
		System:      rendered.System,
		CachePrefix: rendered.UserPrefix,
		MaxTokens:   maxTokens,
		JSONSchema:  verdict.TasksOnlySchema(),
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

	parsed, ms, partialRaw, err := attempt(rendered.UserSuffix)
	if err == nil {
		return parsed, ms, nil, nil
	}
	// Truncation on the first attempt: surface partial bytes immediately
	// rather than retry — the retry would just be cut off the same way.
	if errors.Is(err, providers.ErrResponseTruncated) {
		return verdict.TasksOnly{}, ms, partialRaw, err
	}
	// Schema or identity failure → retry once with hint.
	parsed2, ms2, partialRaw2, err2 := attempt(rendered.UserSuffix + "\n\n" + verdict.RetryHint())
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

// PlanRunReportArgs is the input schema for the plan-run report.
type PlanRunReportArgs struct {
	PlanRunID string `json:"plan_run_id" jsonschema:"required"`
}

// PlanRunReportResult is what plan_run_report returns. Deterministic: no
// reviewer call, no provider round-trip, no cost.
type PlanRunReportResult struct {
	PlanRunID    string            `json:"plan_run_id"`
	PlanVerdict  string            `json:"plan_verdict,omitempty"`
	PlanQuality  string            `json:"plan_quality,omitempty"`
	Tasks        []planrun.TaskRow `json:"tasks"`
	Totals       planrun.RunTotals `json:"totals"`
	Findings     []verdict.Finding `json:"findings,omitempty"`
	SummaryBlock string            `json:"summary_block"`
}

func planRunReportTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "plan_run_report",
		Description: "Report the per-task outcome of a finished multi-task plan run: the anti-tangent verdict and the CodeScene result for each task. " +
			"Call once after the last task reports DONE, with the plan_run_id returned by validate_plan. " +
			"Deterministic and free — no reviewer model is called.",
	}
}

func (h *handlers) PlanRunReport(_ context.Context, _ *mcp.CallToolRequest, args PlanRunReportArgs) (*mcp.CallToolResult, PlanRunReportResult, error) {
	if args.PlanRunID == "" {
		return nil, PlanRunReportResult{}, errors.New("plan_run_id is required")
	}

	// Snapshot, not Get: this walks run.Rows after the lock is released, and
	// other subagents under the same plan run may be appending concurrently.
	run, ok := h.deps.PlanRuns.Snapshot(args.PlanRunID)
	if !ok {
		// Fall back to the durable ledger (nil-safe: PlanLedger is nil unless
		// both ANTI_TANGENT_STATS_DIR and ANTI_TANGENT_PLAN_LEDGER are set).
		// This is what lets a report survive a server restart.
		run, ok = h.deps.PlanLedger.Load(args.PlanRunID)
	}
	if !ok {
		res := PlanRunReportResult{
			PlanRunID: args.PlanRunID,
			Tasks:     []planrun.TaskRow{},
			Findings: []verdict.Finding{{
				Severity:  verdict.SeverityMajor,
				Category:  verdict.CategorySessionMissing,
				Criterion: "plan_run_id",
				Evidence: fmt.Sprintf("No plan run %q is known to this server. Runs expire after %s, "+
					"and in-memory state is lost on restart.", args.PlanRunID, h.deps.PlanRuns.TTL()),
				Suggestion: "Nothing to recover — report from the per-task DONE envelopes instead. " +
					"Set ANTI_TANGENT_STATS_DIR and ANTI_TANGENT_PLAN_LEDGER=1 to persist future runs.",
			}},
			SummaryBlock: "anti-tangent plan run report\n  plan_run_id:  " + args.PlanRunID +
				"\n  (unknown or expired — no rows)\n",
		}
		return planRunReportResult(res)
	}

	res := PlanRunReportResult{
		PlanRunID:    run.ID,
		PlanVerdict:  run.PlanVerdict,
		PlanQuality:  run.PlanQuality,
		Tasks:        run.Rows,
		Totals:       planrun.Totals(run),
		SummaryBlock: planrun.Render(run),
	}
	if res.Tasks == nil {
		res.Tasks = []planrun.TaskRow{}
	}
	return planRunReportResult(res)
}

func planRunReportResult(res PlanRunReportResult) (*mcp.CallToolResult, PlanRunReportResult, error) {
	body, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return nil, PlanRunReportResult{}, err
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(body)}},
	}, res, nil
}
