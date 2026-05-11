package mcpsrv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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
	SessionID  string            `json:"session_id"`
	Verdict    string            `json:"verdict"`
	Findings   []verdict.Finding `json:"findings"`
	NextAction string            `json:"next_action"`
	ModelUsed  string            `json:"model_used"`
	ReviewMS   int64             `json:"review_ms"`
}

// ValidateTaskSpecArgs is the input schema for the pre-hook.
type ValidateTaskSpecArgs struct {
	TaskTitle          string   `json:"task_title"           jsonschema:"required"`
	Goal               string   `json:"goal"                 jsonschema:"required"`
	AcceptanceCriteria []string `json:"acceptance_criteria,omitempty"`
	NonGoals           []string `json:"non_goals,omitempty"`
	Context            string   `json:"context,omitempty"`
	ModelOverride      string   `json:"model_override,omitempty"`
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
	}

	rendered, err := prompts.RenderPre(prompts.PreInput{Spec: spec})
	if err != nil {
		return nil, Envelope{}, fmt.Errorf("render pre prompt: %w", err)
	}

	result, modelUsed, ms, err := h.review(ctx, model, rendered)
	if err != nil {
		return nil, Envelope{}, err
	}

	// Create the session only after the review succeeds so failed reviews
	// don't leave orphan sessions in the store waiting for TTL eviction.
	sess := h.deps.Sessions.Create(spec)
	h.deps.Sessions.SetPreFindings(sess.ID, result.Findings)

	env := Envelope{
		SessionID:  sess.ID,
		Verdict:    string(result.Verdict),
		Findings:   result.Findings,
		NextAction: result.NextAction,
		ModelUsed:  modelUsed,
		ReviewMS:   ms,
	}
	return envelopeResult(env)
}

// review runs a single reviewer call with one parse-retry on malformed JSON.
func (h *handlers) review(ctx context.Context, model config.ModelRef, p prompts.Output) (verdict.Result, string, int64, error) {
	rv, err := h.deps.Reviews.Get(model.Provider)
	if err != nil {
		return verdict.Result{}, "", 0, err
	}
	start := time.Now()

	req := providers.Request{
		Model:      model.Model,
		System:     p.System,
		User:       p.User,
		MaxTokens:  h.deps.Cfg.PerTaskMaxTokens,
		JSONSchema: verdict.Schema(),
	}
	resp, err := rv.Review(ctx, req)
	if err != nil {
		return verdict.Result{}, "", 0, err
	}
	r, err := verdict.Parse(resp.RawJSON)
	if err != nil {
		// One retry with explicit reminder.
		req.User = p.User + "\n\n" + verdict.RetryHint()
		resp, err = rv.Review(ctx, req)
		if err != nil {
			return verdict.Result{}, "", 0, err
		}
		r, err = verdict.Parse(resp.RawJSON)
		if err != nil {
			return verdict.Result{}, "", 0, fmt.Errorf("provider response failed schema after retry: %w", err)
		}
	}

	modelUsed := model.Provider + ":" + resp.Model
	if resp.Model == "" {
		modelUsed = model.String()
	}
	return r, modelUsed, time.Since(start).Milliseconds(), nil
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

func envelopeResult(env Envelope) (*mcp.CallToolResult, Envelope, error) {
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
	SessionID     string    `json:"session_id"     jsonschema:"required"`
	WorkingOn     string    `json:"working_on"     jsonschema:"required"`
	ChangedFiles  []FileArg `json:"changed_files,omitempty"`
	Questions     []string  `json:"questions,omitempty"`
	ModelOverride string    `json:"model_override,omitempty"`
}

func (h *handlers) CheckProgress(ctx context.Context, _ *mcp.CallToolRequest, args CheckProgressArgs) (*mcp.CallToolResult, Envelope, error) {
	if args.SessionID == "" || args.WorkingOn == "" {
		return nil, Envelope{}, errors.New("session_id and working_on are required")
	}

	sess, ok := h.deps.Sessions.Get(args.SessionID)
	if !ok {
		return envelopeResult(notFoundEnvelope(args.SessionID, h.deps.Cfg.MidModel))
	}

	if size := totalBytes(args.ChangedFiles); size > h.deps.Cfg.MaxPayloadBytes {
		return envelopeResult(tooLargeEnvelope(sess.ID, h.deps.Cfg.MidModel, size, h.deps.Cfg.MaxPayloadBytes))
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

	result, modelUsed, ms, err := h.review(ctx, model, rendered)
	if err != nil {
		return nil, Envelope{}, err
	}

	h.deps.Sessions.AppendCheckpoint(sess.ID, session.Checkpoint{
		At:        time.Now(),
		WorkingOn: args.WorkingOn,
		FileCount: len(args.ChangedFiles),
		Verdict:   result.Verdict,
		Findings:  result.Findings,
	})

	env := Envelope{
		SessionID:  sess.ID,
		Verdict:    string(result.Verdict),
		Findings:   result.Findings,
		NextAction: result.NextAction,
		ModelUsed:  modelUsed,
		ReviewMS:   ms,
	}
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

func tooLargeEnvelope(id string, model config.ModelRef, size, limit int) Envelope {
	return Envelope{
		SessionID: id,
		Verdict:   string(verdict.VerdictFail),
		Findings: []verdict.Finding{{
			Severity:   verdict.SeverityMajor,
			Category:   verdict.CategoryTooLarge,
			Criterion:  "payload",
			Evidence:   fmt.Sprintf("payload %d bytes exceeds cap %d", size, limit),
			Suggestion: "Send a unified diff instead of full files, or split the call.",
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
	SessionID     string    `json:"session_id"  jsonschema:"required"`
	Summary       string    `json:"summary"     jsonschema:"required"`
	FinalFiles    []FileArg `json:"final_files,omitempty"`
	TestEvidence  string    `json:"test_evidence,omitempty"`
	ModelOverride string    `json:"model_override,omitempty"`
}

// ValidatePlanArgs is the input schema for the plan-level reviewer.
type ValidatePlanArgs struct {
	PlanText      string `json:"plan_text"      jsonschema:"required"`
	ModelOverride string `json:"model_override,omitempty"`
}

func validatePlanTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "validate_plan",
		Description: "Validate an implementation plan as a whole BEFORE dispatching subagents to implement individual tasks. " +
			"Returns per-task findings and ready-to-paste structured headers (Goal / Acceptance criteria / Non-goals / Context) for tasks that lack them. " +
			"Call this once at plan-handoff time; the per-task `validate_task_spec` is still called by each implementing subagent at task start.",
	}
}

func (h *handlers) ValidateCompletion(ctx context.Context, _ *mcp.CallToolRequest, args ValidateCompletionArgs) (*mcp.CallToolResult, Envelope, error) {
	if args.SessionID == "" || args.Summary == "" {
		return nil, Envelope{}, errors.New("session_id and summary are required")
	}

	sess, ok := h.deps.Sessions.Get(args.SessionID)
	if !ok {
		return envelopeResult(notFoundEnvelope(args.SessionID, h.deps.Cfg.PostModel))
	}

	if size := totalBytes(args.FinalFiles); size > h.deps.Cfg.MaxPayloadBytes {
		return envelopeResult(tooLargeEnvelope(sess.ID, h.deps.Cfg.PostModel, size, h.deps.Cfg.MaxPayloadBytes))
	}

	model, err := h.resolveModel(args.ModelOverride, h.deps.Cfg.PostModel)
	if err != nil {
		return nil, Envelope{}, err
	}

	rendered, err := prompts.RenderPost(prompts.PostInput{
		Spec:         sess.Spec,
		Summary:      args.Summary,
		Files:        toPromptFiles(args.FinalFiles),
		TestEvidence: args.TestEvidence,
	})
	if err != nil {
		return nil, Envelope{}, fmt.Errorf("render post prompt: %w", err)
	}

	result, modelUsed, ms, err := h.review(ctx, model, rendered)
	if err != nil {
		return nil, Envelope{}, err
	}

	h.deps.Sessions.SetPostFindings(sess.ID, result.Findings)

	env := Envelope{
		SessionID:  sess.ID,
		Verdict:    string(result.Verdict),
		Findings:   result.Findings,
		NextAction: result.NextAction,
		ModelUsed:  modelUsed,
		ReviewMS:   ms,
	}
	return envelopeResult(env)
}

func (h *handlers) ValidatePlan(ctx context.Context, _ *mcp.CallToolRequest, args ValidatePlanArgs) (*mcp.CallToolResult, verdict.PlanResult, error) {
	if args.PlanText == "" {
		return nil, verdict.PlanResult{}, errors.New("plan_text is required")
	}
	if size := len(args.PlanText); size > h.deps.Cfg.MaxPayloadBytes {
		return planEnvelopeResult(tooLargePlanResult(size, h.deps.Cfg.MaxPayloadBytes), h.deps.Cfg.PlanModel.String(), 0)
	}
	tasks, _ := planparser.SplitTasks(args.PlanText)
	if len(tasks) == 0 {
		return planEnvelopeResult(noHeadingsPlanResult(), h.deps.Cfg.PlanModel.String(), 0)
	}

	model, err := h.resolveModel(args.ModelOverride, h.deps.Cfg.PlanModel)
	if err != nil {
		return nil, verdict.PlanResult{}, err
	}

	pr, modelUsed, ms, err := h.reviewPlanSingle(ctx, model, args.PlanText)
	if err != nil {
		return nil, verdict.PlanResult{}, err
	}
	return planEnvelopeResult(pr, modelUsed, ms)
}

// reviewPlanSingle runs one reviewer call for the entire plan — the
// behavior used today for plans whose task count is at or below
// h.deps.Cfg.PlanTasksPerChunk. Renders the prompt internally.
func (h *handlers) reviewPlanSingle(ctx context.Context, model config.ModelRef, planText string) (verdict.PlanResult, string, int64, error) {
	rendered, err := prompts.RenderPlan(prompts.PlanInput{PlanText: planText})
	if err != nil {
		return verdict.PlanResult{}, "", 0, fmt.Errorf("render plan prompt: %w", err)
	}
	rv, err := h.deps.Reviews.Get(model.Provider)
	if err != nil {
		return verdict.PlanResult{}, "", 0, err
	}
	start := time.Now()
	req := providers.Request{
		Model:      model.Model,
		System:     rendered.System,
		User:       rendered.User,
		MaxTokens:  h.deps.Cfg.PlanMaxTokens,
		JSONSchema: verdict.PlanSchema(),
	}
	resp, err := rv.Review(ctx, req)
	if err != nil {
		return verdict.PlanResult{}, "", 0, err
	}
	r, err := verdict.ParsePlan(resp.RawJSON)
	if err != nil {
		// One retry with explicit reminder.
		req.User = rendered.User + "\n\n" + verdict.RetryHint()
		resp, err = rv.Review(ctx, req)
		if err != nil {
			return verdict.PlanResult{}, "", 0, err
		}
		r, err = verdict.ParsePlan(resp.RawJSON)
		if err != nil {
			return verdict.PlanResult{}, "", 0, fmt.Errorf("plan provider response failed schema after retry: %w", err)
		}
	}
	modelUsed := model.Provider + ":" + resp.Model
	if resp.Model == "" {
		modelUsed = model.String()
	}
	return r, modelUsed, time.Since(start).Milliseconds(), nil
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
func planEnvelopeResult(pr verdict.PlanResult, modelUsed string, ms int64) (*mcp.CallToolResult, verdict.PlanResult, error) {
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
func (h *handlers) reviewPlanChunked(
	ctx context.Context,
	model config.ModelRef,
	planText string,
	tasks []planparser.RawTask,
	chunkSize int,
) (verdict.PlanResult, string, int64, error) {
	rv, err := h.deps.Reviews.Get(model.Provider)
	if err != nil {
		return verdict.PlanResult{}, "", 0, err
	}

	var totalMs int64
	var modelUsed string

	// ----- Pass 1: plan-findings only -----
	rendered, err := prompts.RenderPlanFindingsOnly(prompts.PlanInput{PlanText: planText})
	if err != nil {
		return verdict.PlanResult{}, "", 0, fmt.Errorf("render plan_findings_only: %w", err)
	}
	req := providers.Request{
		Model:      model.Model,
		System:     rendered.System,
		User:       rendered.User,
		MaxTokens:  h.deps.Cfg.PlanMaxTokens,
		JSONSchema: verdict.PlanFindingsOnlySchema(),
	}
	start := time.Now()
	resp, err := rv.Review(ctx, req)
	if err != nil {
		return verdict.PlanResult{}, "", 0, err
	}
	pf, err := verdict.ParsePlanFindingsOnly(resp.RawJSON)
	if err != nil {
		req.User = rendered.User + "\n\n" + verdict.RetryHint()
		resp, err = rv.Review(ctx, req)
		if err != nil {
			return verdict.PlanResult{}, "", 0, err
		}
		pf, err = verdict.ParsePlanFindingsOnly(resp.RawJSON)
		if err != nil {
			return verdict.PlanResult{}, "", 0, fmt.Errorf("plan_findings_only failed schema after retry: %w", err)
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

		chunkResult, ms, err := h.reviewOnePlanChunk(ctx, rv, model, planText, chunkTasks)
		if err != nil {
			return verdict.PlanResult{}, "", 0, err
		}
		totalMs += ms
		result.Tasks = append(result.Tasks, chunkResult.Tasks...)
	}

	if len(result.Tasks) != len(tasks) {
		return verdict.PlanResult{}, "", 0,
			fmt.Errorf("chunked plan review returned %d task results, expected %d",
				len(result.Tasks), len(tasks))
	}

	return result, modelUsed, totalMs, nil
}

// reviewOnePlanChunk runs one per-chunk reviewer call with identity validation
// and the existing schema-retry-once pattern.
func (h *handlers) reviewOnePlanChunk(
	ctx context.Context,
	rv providers.Reviewer,
	model config.ModelRef,
	planText string,
	chunkTasks []planparser.RawTask,
) (verdict.TasksOnly, int64, error) {
	rendered, err := prompts.RenderPlanTasksChunk(prompts.PlanChunkInput{
		PlanText:   planText,
		ChunkTasks: chunkTasks,
	})
	if err != nil {
		return verdict.TasksOnly{}, 0, fmt.Errorf("render plan_tasks_chunk: %w", err)
	}

	req := providers.Request{
		Model:      model.Model,
		System:     rendered.System,
		User:       rendered.User,
		MaxTokens:  h.deps.Cfg.PlanMaxTokens,
		JSONSchema: verdict.TasksOnlySchema(),
	}

	// Build the expected-title set once per chunk for identity validation.
	expected := make(map[string]struct{}, len(chunkTasks))
	for _, t := range chunkTasks {
		expected[strings.TrimSpace(t.Title)] = struct{}{}
	}

	// attempt mutates req.User in place; each call overwrites it before
	// Review, so the retry sees the hint-augmented body cleanly.
	attempt := func(user string) (verdict.TasksOnly, int64, error) {
		req.User = user
		start := time.Now()
		resp, err := rv.Review(ctx, req)
		if err != nil {
			return verdict.TasksOnly{}, 0, err
		}
		ms := time.Since(start).Milliseconds()
		parsed, err := verdict.ParseTasksOnly(resp.RawJSON)
		if err != nil {
			return verdict.TasksOnly{}, ms, err
		}
		if err := validateChunkIdentity(parsed, expected, len(chunkTasks)); err != nil {
			return verdict.TasksOnly{}, ms, err
		}
		return parsed, ms, nil
	}

	parsed, ms, err := attempt(rendered.User)
	if err == nil {
		return parsed, ms, nil
	}
	// Schema or identity failure → retry once with hint.
	parsed2, ms2, err2 := attempt(rendered.User + "\n\n" + verdict.RetryHint())
	if err2 != nil {
		return verdict.TasksOnly{}, ms + ms2, fmt.Errorf("plan_tasks_chunk failed after retry: %w", err2)
	}
	return parsed2, ms + ms2, nil
}

// validateChunkIdentity checks that the parsed chunk response contains exactly
// the expected number of tasks, that every returned task_title is in the
// expected set, and that no title appears more than once (which would mask a
// dropped task while still satisfying the count check). Returns a descriptive
// error on any mismatch.
func validateChunkIdentity(parsed verdict.TasksOnly, expected map[string]struct{}, want int) error {
	if len(parsed.Tasks) != want {
		return fmt.Errorf("chunk identity: got %d tasks, expected %d", len(parsed.Tasks), want)
	}
	seen := make(map[string]struct{}, want)
	for i, t := range parsed.Tasks {
		title := strings.TrimSpace(t.TaskTitle)
		if _, ok := expected[title]; !ok {
			return fmt.Errorf("chunk identity: tasks[%d].task_title %q not in requested chunk", i, title)
		}
		if _, dup := seen[title]; dup {
			return fmt.Errorf("chunk identity: tasks[%d].task_title %q duplicated within chunk", i, title)
		}
		seen[title] = struct{}{}
	}
	return nil
}
