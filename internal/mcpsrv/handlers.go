package mcpsrv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/patiently/anti-tangent-mcp/internal/config"
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
	sess := h.deps.Sessions.Create(spec)

	rendered, err := prompts.RenderPre(prompts.PreInput{Spec: spec})
	if err != nil {
		return nil, Envelope{}, fmt.Errorf("render pre prompt: %w", err)
	}

	result, modelUsed, ms, err := h.review(ctx, model, rendered)
	if err != nil {
		return nil, Envelope{}, err
	}

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
		MaxTokens:  4096,
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
