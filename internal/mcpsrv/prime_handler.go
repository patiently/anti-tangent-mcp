package mcpsrv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/prompts"
	"github.com/patiently/anti-tangent-mcp/internal/providers"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

// Defaults / bounds for the optional max_picks argument. Mirrors design §3.1.
const (
	defaultMaxPicks = 10
	maxMaxPicks     = 25
)

// KBIndexEntryArg is one entry in the kb_index argument vector for
// prime_project_knowledge. Mirrors prompts.KBIndexEntry but lives at the
// handler boundary so the wire-level JSON contract is decoupled from the
// internal prompt-rendering type.
type KBIndexEntryArg struct {
	Permalink string   `json:"permalink"`
	Type      string   `json:"type"`
	Title     string   `json:"title"`
	Summary   string   `json:"summary"`
	Tags      []string `json:"tags,omitempty"`
}

// PrimeProjectKnowledgeArgs is the input schema for prime_project_knowledge.
// Required fields match design §3.1; optional fields are typed with
// `omitempty` so absent inputs marshal as the JSON-equivalent zero value
// (empty slice / empty string).
type PrimeProjectKnowledgeArgs struct {
	TaskTitle          string            `json:"task_title"          jsonschema:"required"`
	Goal               string            `json:"goal"                jsonschema:"required"`
	AcceptanceCriteria []string          `json:"acceptance_criteria" jsonschema:"required"`
	NonGoals           []string          `json:"non_goals,omitempty"`
	Context            string            `json:"context,omitempty"`
	KBIndex            []KBIndexEntryArg `json:"kb_index,omitempty"`
	EpicPermalink      string            `json:"epic_permalink,omitempty"`
	MaxPicks           int               `json:"max_picks,omitempty"`
	ModelOverride      string            `json:"model_override,omitempty"`
	MaxTokensOverride  int               `json:"max_tokens_override,omitempty"`
}

func primeProjectKnowledgeTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "prime_project_knowledge",
		Description: "Given a task spec and a kb_index of available knowledge-base notes, return prioritized note picks for the implementer to read before starting. Stateless; no session created. " +
			"Emits paste-ready bm_commands when ANTI_TANGENT_KB_STORE=basic-memory.",
	}
}

// PrimeProjectKnowledge implements the stateless prime tool. The handler
// ordering mirrors ValidateCompletion (handlers.go) so all per-call surfaces
// — validation, payload-cap, model resolution, adaptive token budget, render,
// review, parse, post-process — appear in the same canonical order. See
// design §3.1 / §5.3 for the spec.
func (h *handlers) PrimeProjectKnowledge(ctx context.Context, _ *mcp.CallToolRequest, args PrimeProjectKnowledgeArgs) (*mcp.CallToolResult, verdict.PrimeResult, error) {
	// 1. Required-field validation. Trim whitespace so a caller can't smuggle
	// an empty-looking title past the check with a single space.
	if strings.TrimSpace(args.TaskTitle) == "" || strings.TrimSpace(args.Goal) == "" {
		return nil, verdict.PrimeResult{}, errors.New("task_title and goal are required")
	}
	if len(args.AcceptanceCriteria) == 0 {
		return nil, verdict.PrimeResult{}, errors.New("acceptance_criteria must contain at least one entry")
	}

	// 2. MaxPicks default/ceiling. Zero or negative → default; > ceiling → ceiling.
	maxPicks := args.MaxPicks
	if maxPicks <= 0 {
		maxPicks = defaultMaxPicks
	}
	if maxPicks > maxMaxPicks {
		maxPicks = maxMaxPicks
	}

	// 3. Payload size = sum of all string fields + serialized kb_index bytes.
	// Include every string-typed arg (including ModelOverride) so the cap
	// reflects the full caller payload, not just the "interesting" fields.
	size := len(args.TaskTitle) + len(args.Goal) + len(args.Context) + len(args.EpicPermalink) + len(args.ModelOverride)
	for _, ac := range args.AcceptanceCriteria {
		size += len(ac)
	}
	for _, ng := range args.NonGoals {
		size += len(ng)
	}
	if kbBytes, err := json.Marshal(args.KBIndex); err == nil {
		size += len(kbBytes)
	}

	// 4. effectiveMaxTokens — resolve the clamp finding BEFORE the payload
	// check so a too-large envelope can carry the clamp for free. Mirrors
	// the v0.5.2 ordering established by ValidateCompletion.
	maxTokens, clamp, err := effectiveMaxTokens(args.MaxTokensOverride, h.deps.Cfg.PrimeMaxTokens, h.deps.Cfg.MaxTokensCeiling)
	if err != nil {
		return nil, verdict.PrimeResult{}, err
	}

	// 5. Payload-cap check. The synthetic too-large envelope cites
	// cfg.PrimeModel (NOT a resolved override) so callers always see the
	// configured-default model id — same posture as tooLargeEnvelope.
	if size > h.deps.Cfg.MaxPayloadBytes {
		return primeEnvelopeResult(prependPrimeClamp(primeTooLargeResult(size, h.deps.Cfg.MaxPayloadBytes), clamp), h.deps.Cfg.PrimeModel.String(), 0)
	}

	// 6. Resolve model. Misspelled model_override surfaces here, after the
	// payload-too-large rejection has had its chance to fire — callers
	// don't see two failure modes interleaved.
	model, err := h.resolveModel(args.ModelOverride, h.deps.Cfg.PrimeModel)
	if err != nil {
		return nil, verdict.PrimeResult{}, err
	}

	// 7. Adaptive token budget: only when no explicit override was set.
	// Explicit overrides already routed through effectiveMaxTokens (with
	// clamp) at step 4 above.
	if args.MaxTokensOverride == 0 {
		maxTokens = adaptivePrimeMaxTokens(h.deps.Cfg, len(args.KBIndex))
	}

	// 8. Render the prompt. KBStoreIsBasicMemory toggles the "emit
	// bm_commands" branch of the template.
	rendered, err := prompts.RenderPrime(prompts.PrimeInput{
		TaskTitle:            args.TaskTitle,
		Goal:                 args.Goal,
		AcceptanceCriteria:   args.AcceptanceCriteria,
		NonGoals:             args.NonGoals,
		Context:              args.Context,
		KBIndex:              toPromptKBIndex(args.KBIndex),
		EpicPermalink:        args.EpicPermalink,
		MaxPicks:             maxPicks,
		KBStoreIsBasicMemory: h.deps.Cfg.KBStore == "basic-memory",
	})
	if err != nil {
		return nil, verdict.PrimeResult{}, fmt.Errorf("render prime prompt: %w", err)
	}

	// 9. Reviewer call (with one parse-retry inside reviewPrime).
	result, modelUsed, ms, _, err := h.reviewPrime(ctx, model, rendered, maxTokens)
	if errors.Is(err, providers.ErrResponseTruncated) {
		// Synthesise a warn envelope with category:other / criterion:reviewer_response.
		// Mirrors the per-task truncatedResult path. modelUsed is empty when the
		// provider call truncated, so cite the configured model ref.
		return primeEnvelopeResult(prependPrimeClamp(primeTruncationResult(), clamp), model.String(), 0)
	}
	if err != nil {
		return nil, verdict.PrimeResult{}, err
	}

	// 10. Post-processing: KBStore-aware bm_commands gating and
	// kb_store_mismatch findings. Use an EMPTY slice (NOT nil) so the
	// strict-schema "bm_commands required" invariant holds if the result is
	// re-marshaled.
	if h.deps.Cfg.KBStore == "" {
		result.BMCommands = []verdict.BMCommand{}
	} else if h.deps.Cfg.KBStore == "basic-memory" {
		result.Findings = append(result.Findings, kbStoreMismatchFindings(result.Picks)...)
	}

	// 11. Prepend the clamp finding (no-op when clamp is zero).
	result = prependPrimeClamp(result, clamp)

	// 12. Structured log line, single-shot per-call. Mirrors validate_completion's
	// slog.Info call pattern (key/value attributes, JSON handler on stderr).
	slog.Info("prime_project_knowledge",
		slog.String("tool", "prime_project_knowledge"),
		slog.Int64("duration_ms", ms),
		slog.String("model", modelUsed),
		slog.String("verdict", string(result.Verdict)),
		slog.Int("picks", len(result.Picks)),
		slog.Int("findings", len(result.Findings)),
		slog.Int("kb_index_size", len(args.KBIndex)),
		slog.String("epic", args.EpicPermalink),
	)

	return primeEnvelopeResult(result, modelUsed, ms)
}

// toPromptKBIndex maps the handler-boundary KBIndexEntryArg slice to the
// prompts-internal KBIndexEntry slice. The two types are structurally
// identical but live in separate packages so the wire shape and the
// template shape can evolve independently.
func toPromptKBIndex(in []KBIndexEntryArg) []prompts.KBIndexEntry {
	if len(in) == 0 {
		return nil
	}
	out := make([]prompts.KBIndexEntry, len(in))
	for i, e := range in {
		out[i] = prompts.KBIndexEntry{
			Permalink: e.Permalink,
			Type:      e.Type,
			Title:     e.Title,
			Summary:   e.Summary,
			Tags:      e.Tags,
		}
	}
	return out
}

// kbStoreMismatchFindings emits one minor `other / kb_store_mismatch`
// finding per pick whose permalink does not look like a Basic Memory
// permalink. The detection is intentionally conservative — anything that
// starts with `/` or contains `://` (URI scheme) is flagged. Per design §5.5
// this is a server-side belt-and-braces guard; the reviewer is instructed
// not to invent permalinks but we want to surface drift if it happens.
func kbStoreMismatchFindings(picks []verdict.Pick) []verdict.Finding {
	var fs []verdict.Finding
	for _, p := range picks {
		if strings.HasPrefix(p.Permalink, "/") || strings.Contains(p.Permalink, "://") {
			fs = append(fs, verdict.Finding{
				Severity:   verdict.SeverityMinor,
				Category:   verdict.CategoryOther,
				Criterion:  "kb_store_mismatch",
				Evidence:   fmt.Sprintf("pick permalink %q does not look like a Basic Memory permalink", p.Permalink),
				Suggestion: "Use a Basic Memory permalink (e.g. decisions/0042-…); strip leading slashes and URI schemes.",
			})
		}
	}
	return fs
}

// reviewPrime is the prime-shaped sibling of review() (handlers.go:171). It
// mirrors review()'s truncation handling: on providers.ErrResponseTruncated
// the caller can build a synthetic envelope so the truncation surfaces like
// every other per-task tool does. The fourth return value (`partialRaw`) is
// reserved for future partial-recovery work; prime has no partial-recovery
// path in v0.6.0 (see Non-goals).
func (h *handlers) reviewPrime(ctx context.Context, model config.ModelRef, p prompts.Output, maxTokens int) (verdict.PrimeResult, string, int64, []byte, error) {
	rv, err := h.deps.Reviews.Get(model.Provider)
	if err != nil {
		return verdict.PrimeResult{}, "", 0, nil, err
	}
	start := time.Now()
	req := providers.Request{
		Model:      model.Model,
		System:     p.System,
		User:       p.User,
		MaxTokens:  maxTokens,
		JSONSchema: verdict.PrimeSchema(),
	}
	resp, err := rv.Review(ctx, req)
	if err != nil {
		if errors.Is(err, providers.ErrResponseTruncated) {
			return verdict.PrimeResult{}, "", 0, resp.RawJSON, err
		}
		return verdict.PrimeResult{}, "", 0, nil, err
	}
	r, err := verdict.ParsePrime(resp.RawJSON)
	if err != nil {
		// One retry with explicit reminder. Mirrors review() at handlers.go.
		req.User = p.User + "\n\n" + verdict.RetryHint()
		resp, err = rv.Review(ctx, req)
		if err != nil {
			if errors.Is(err, providers.ErrResponseTruncated) {
				return verdict.PrimeResult{}, "", 0, resp.RawJSON, err
			}
			return verdict.PrimeResult{}, "", 0, nil, err
		}
		r, err = verdict.ParsePrime(resp.RawJSON)
		if err != nil {
			return verdict.PrimeResult{}, "", 0, nil, fmt.Errorf("prime provider response failed schema after retry: %w", err)
		}
	}
	modelUsed := model.Provider + ":" + resp.Model
	if resp.Model == "" {
		modelUsed = model.String()
	}
	return r, modelUsed, time.Since(start).Milliseconds(), nil, nil
}

// primeTooLargeResult mirrors tooLargeEnvelope (handlers.go) and
// tooLargePlanResult: severity Critical so the ladder derives fail from one
// critical, matching the explicit Verdict: fail. Verdict is set explicitly
// because this synthetic result short-circuits before any reviewer call.
func primeTooLargeResult(size, capBytes int) verdict.PrimeResult {
	return verdict.PrimeResult{
		Verdict: verdict.VerdictFail,
		Findings: []verdict.Finding{{
			Severity:   verdict.SeverityCritical,
			Category:   verdict.CategoryTooLarge,
			Criterion:  "payload",
			Evidence:   fmt.Sprintf("payload %d bytes exceeds cap %d", size, capBytes),
			Suggestion: "Shrink kb_index (pre-filter with search_notes) or split into multiple calls.",
		}},
		Picks:      []verdict.Pick{},
		BMCommands: []verdict.BMCommand{},
		NextAction: "Reduce the payload and retry.",
	}
}

// primeTruncationResult mirrors truncatedResult (handlers.go:408) for the
// no-analysis case where the reviewer's response was cut off and NO
// complete findings were recovered. Per v0.5.2 the per-task truncation
// finding is SeverityMajor (was Minor pre-0.5.2) so the ladder derives
// warn consistently — prime mirrors that posture. Partial-recovery is NOT
// implemented for prime in v0.6.0; this helper is the no-analysis
// fallback, so Partial is left zero (false).
func primeTruncationResult() verdict.PrimeResult {
	return verdict.PrimeResult{
		Verdict: verdict.VerdictWarn,
		Findings: []verdict.Finding{{
			Severity:   verdict.SeverityMajor,
			Category:   verdict.CategoryOther,
			Criterion:  "reviewer_response",
			Evidence:   providers.ErrResponseTruncated.Error(),
			Suggestion: "Raise ANTI_TANGENT_PRIME_MAX_TOKENS (bounded by ANTI_TANGENT_MAX_TOKENS_CEILING), shrink kb_index, or pass an explicit max_tokens_override.",
		}},
		Picks:      []verdict.Pick{},
		BMCommands: []verdict.BMCommand{},
		NextAction: "Retry with a higher max_tokens_override (or raise the configured max-tokens cap).",
	}
}

// prependPrimeClamp inserts the clamp finding at the head of r.Findings when
// clamp is non-zero. Mirrors prependClamp / prependPlanClamp (handlers.go)
// so every flow — success, truncation, payload-too-large — treats the clamp
// finding identically: it lives in Findings, NOT in next_action.
func prependPrimeClamp(r verdict.PrimeResult, clamp verdict.Finding) verdict.PrimeResult {
	if clamp.Severity == "" {
		return r
	}
	r.Findings = append([]verdict.Finding{clamp}, r.Findings...)
	return r
}

// primeEnvelopeResult marshals a PrimeResult into an MCP CallToolResult.
// Mirrors planEnvelopeResultFinalized (handlers.go): the wire payload is the
// PrimeResult plus `model_used` and `review_ms` siblings. SummaryBlock is
// populated here so every exit path (happy / truncation / too-large) gets a
// paste-ready summary for free.
func primeEnvelopeResult(r verdict.PrimeResult, modelUsed string, reviewMS int64) (*mcp.CallToolResult, verdict.PrimeResult, error) {
	// Ensure non-nil collections survive marshaling — the wire-format
	// contract requires `findings`, `picks`, and `bm_commands` to be
	// arrays. The reviewer-parsed paths already enforce this in ParsePrime;
	// the synthetic helpers above also seed empty slices. Belt-and-braces
	// here keeps custom test paths from emitting `null` accidentally.
	if r.Findings == nil {
		r.Findings = []verdict.Finding{}
	}
	if r.Picks == nil {
		r.Picks = []verdict.Pick{}
	}
	if r.BMCommands == nil {
		r.BMCommands = []verdict.BMCommand{}
	}
	r.SummaryBlock = formatPrimeSummary(r, modelUsed, reviewMS)
	body, err := json.MarshalIndent(struct {
		verdict.PrimeResult
		ModelUsed string `json:"model_used"`
		ReviewMS  int64  `json:"review_ms"`
	}{r, modelUsed, reviewMS}, "", "  ")
	if err != nil {
		return nil, verdict.PrimeResult{}, err
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(body)}},
	}, r, nil
}
