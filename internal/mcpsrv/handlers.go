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

// stubs for tools defined in later tasks; let the build pass.
func checkProgressTool() *mcp.Tool      { return &mcp.Tool{Name: "check_progress"} }
func validateCompletionTool() *mcp.Tool { return &mcp.Tool{Name: "validate_completion"} }

type CheckProgressArgs struct{}
type ValidateCompletionArgs struct{}

func (h *handlers) CheckProgress(_ context.Context, _ *mcp.CallToolRequest, _ CheckProgressArgs) (*mcp.CallToolResult, Envelope, error) {
	return nil, Envelope{}, errors.New("not implemented yet (Task 13)")
}
func (h *handlers) ValidateCompletion(_ context.Context, _ *mcp.CallToolRequest, _ ValidateCompletionArgs) (*mcp.CallToolResult, Envelope, error) {
	return nil, Envelope{}, errors.New("not implemented yet (Task 14)")
}
