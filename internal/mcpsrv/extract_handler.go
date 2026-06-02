package mcpsrv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/prompts"
	"github.com/patiently/anti-tangent-mcp/internal/providers"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

// CompletionEnvelopeArg is one completion-stage envelope the caller has
// accumulated. Mirrors prompts.CompletionEnvelopeForExtract but lives at the
// handler boundary so the wire-level JSON contract is decoupled from the
// internal prompt-rendering type.
type CompletionEnvelopeArg struct {
	TaskTitle    string            `json:"task_title,omitempty"`
	Summary      string            `json:"summary"`
	Verdict      string            `json:"verdict"`
	Findings     []verdict.Finding `json:"findings,omitempty"`
	FinalDiff    string            `json:"final_diff,omitempty"`
	FinalFiles   []FileArg         `json:"final_files,omitempty"`
	TestEvidence string            `json:"test_evidence,omitempty"`
}

// ExtractProjectKnowledgeArgs is the input schema for extract_project_knowledge.
// CompletionEnvelopes is required and must be non-empty; optional fields use
// `omitempty` so absent inputs marshal as the JSON-equivalent zero value.
type ExtractProjectKnowledgeArgs struct {
	CompletionEnvelopes []CompletionEnvelopeArg `json:"completion_envelopes" jsonschema:"required"`
	PlanText            string                  `json:"plan_text,omitempty"`
	KBIndex             []KBIndexEntryArg       `json:"kb_index,omitempty"`
	CurrentKBExcerpts   map[string]string       `json:"current_kb_excerpts,omitempty"`
	EpicPermalink       string                  `json:"epic_permalink,omitempty"`
	ModelOverride       string                  `json:"model_override,omitempty"`
	MaxTokensOverride   int                     `json:"max_tokens_override,omitempty"`
}

func extractProjectKnowledgeTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "extract_project_knowledge",
		Description: "Given one or more validate_completion envelopes (plus optional plan text and current KB context), return structured create/update/supersede proposals for the team's project knowledge base. Stateless; no session created. " +
			"Emits paste-ready bm_commands when ANTI_TANGENT_KB_STORE=basic-memory.",
	}
}

// ExtractProjectKnowledge implements the stateless extract tool. The handler
// ordering mirrors PrimeProjectKnowledge (prime_handler.go) so all per-call
// surfaces — validation, payload-cap, evidence accounting, model resolution,
// adaptive token budget, render, review, parse, post-process — appear in the
// same canonical order. Validation order matters: evidence accounting and the
// all-evidence-bare refusal run BEFORE model resolution so a caller combining
// a misspelled model_override with an all-evidence-bare envelope list still
// gets the actionable insufficient_evidence response.
func (h *handlers) ExtractProjectKnowledge(ctx context.Context, _ *mcp.CallToolRequest, args ExtractProjectKnowledgeArgs) (_ *mcp.CallToolResult, _ verdict.ExtractResult, retErr error) {
	// Capture per-call outcome so every exit path — happy, synthetic refusal,
	// oversized payload, truncation, parse-retry exhausted — emits exactly one
	// structured JSON log line on stderr per spec §5.6. The deferred logger
	// reads from these closure vars at return time; each branch updates the
	// vars before returning so the log reflects the actual outcome. The same
	// vars drive the deferred stats record so every structured envelope return
	// lands one events.jsonl record (transport errors — retErr != nil — are
	// skipped, since they produce no structured result).
	var (
		logModelUsed string
		logVerdict   verdict.Verdict
		logMS        int64
		logProposals int
		logFindings  []verdict.Finding
		logOutcome   = "success"
	)
	defer func() {
		slog.Info("extract_project_knowledge",
			slog.String("tool", "extract_project_knowledge"),
			slog.Int64("duration_ms", logMS),
			slog.String("model", logModelUsed),
			slog.String("verdict", string(logVerdict)),
			slog.String("outcome", logOutcome),
			slog.Int("proposals", logProposals),
			slog.Int("findings", len(logFindings)),
			slog.Int("envelopes", len(args.CompletionEnvelopes)),
			slog.Int("kb_index_size", len(args.KBIndex)),
			slog.String("epic", args.EpicPermalink),
		)
		if retErr == nil {
			h.recordStat(statParams{
				tool:      "extract_project_knowledge",
				verdict:   string(logVerdict),
				findings:  logFindings,
				modelUsed: logModelUsed,
				reviewMS:  logMS,
			})
		}
	}()

	// 1. Required-field validation. Empty completion_envelopes is a refusal
	// (not an error) so callers get a structured envelope rather than a
	// transport-level error.
	if len(args.CompletionEnvelopes) == 0 {
		r := extractEmptyEnvelopesResult()
		logOutcome, logModelUsed, logVerdict, logFindings = "empty_envelopes", h.deps.Cfg.ExtractModel.String(), r.Verdict, r.Findings
		return extractEnvelopeResult(r, h.deps.Cfg.ExtractModel.String(), 0)
	}

	// 2. effectiveMaxTokens — resolve the clamp finding BEFORE the payload
	// check so a too-large envelope can carry the clamp for free. Mirrors
	// the v0.5.2 ordering established by ValidateCompletion / PrimeProjectKnowledge.
	maxTokens, clamp, err := effectiveMaxTokens(args.MaxTokensOverride, h.deps.Cfg.ExtractMaxTokens, h.deps.Cfg.MaxTokensCeiling)
	if err != nil {
		return nil, verdict.ExtractResult{}, err
	}

	// 3. Payload-cap check. Payload size = JSON-serialized bytes of the full
	// args (mirrors the prime handler's posture of including every input).
	// The synthetic too-large envelope cites cfg.ExtractModel (NOT a resolved
	// override) so callers always see the configured-default model id.
	argsBytes, _ := json.Marshal(args)
	if size := len(argsBytes); size > h.deps.Cfg.MaxPayloadBytes {
		r := prependExtractClamp(extractTooLargeResult(size, h.deps.Cfg.MaxPayloadBytes), clamp)
		logOutcome, logModelUsed, logVerdict, logFindings = "payload_too_large", h.deps.Cfg.ExtractModel.String(), r.Verdict, r.Findings
		return extractEnvelopeResult(r, h.deps.Cfg.ExtractModel.String(), 0)
	}

	// 4. Per-envelope evidence accounting (BEFORE model resolution).
	// Algorithm:
	//   (a) If FinalDiff == "" AND len(FinalFiles) == 0 AND TestEvidence == "":
	//       evidence-bare; emit one insufficient_evidence (major) pre-finding.
	//   (b) Else, run checkEvidenceShape against {FinalDiff, FinalFiles}.
	//       - returns "" → envelope has clean evidence; no pre-finding.
	//       - returns non-empty AND TestEvidence == "" → evidence-bare;
	//         cite the malformed-shape reason in the insufficient_evidence
	//         pre-finding's evidence field.
	//       - returns non-empty AND TestEvidence != "" → diff/files unusable
	//         but test evidence still grounds the reviewer; envelope counts
	//         as having evidence and accumulates a `quality` (NOT
	//         insufficient_evidence) sub-finding citing the diff issue.
	// If EVERY envelope was evidence-bare, synthesize a refusal envelope NOW
	// (uses cfg.ExtractModel.String() as model_used; does NOT resolve
	// args.ModelOverride or call the reviewer).
	preFindings, allBare := classifyEnvelopes(args.CompletionEnvelopes)
	if allBare {
		r := prependExtractClamp(extractAllBareResult(preFindings), clamp)
		logOutcome, logModelUsed, logVerdict, logFindings = "all_envelopes_bare", h.deps.Cfg.ExtractModel.String(), r.Verdict, r.Findings
		return extractEnvelopeResult(r, h.deps.Cfg.ExtractModel.String(), 0)
	}

	// 5. Resolve model. cfg.ExtractModel is guaranteed non-zero by config.Load.
	// A misspelled override fails here, but only after evidence-accounting and
	// the payload-cap rejection have had their chance to fire.
	model, err := h.resolveModel(args.ModelOverride, h.deps.Cfg.ExtractModel)
	if err != nil {
		return nil, verdict.ExtractResult{}, err
	}

	// 6. Adaptive token budget: only when no explicit override was set.
	// Explicit overrides already routed through effectiveMaxTokens (with
	// clamp) at step 2 above.
	if args.MaxTokensOverride == 0 {
		maxTokens = adaptiveExtractMaxTokens(h.deps.Cfg, len(args.CompletionEnvelopes))
	}

	// 7. Render the prompt. KBStoreIsBasicMemory toggles the "emit
	// bm_commands" branch of the template.
	rendered, err := prompts.RenderExtract(prompts.ExtractInput{
		CompletionEnvelopes:  toPromptCompletionEnvelopes(args.CompletionEnvelopes),
		PlanText:             args.PlanText,
		KBIndex:              toPromptKBIndex(args.KBIndex),
		CurrentKBExcerpts:    args.CurrentKBExcerpts,
		EpicPermalink:        args.EpicPermalink,
		KBStoreIsBasicMemory: h.deps.Cfg.KBStore == "basic-memory",
	})
	if err != nil {
		return nil, verdict.ExtractResult{}, fmt.Errorf("render extract prompt: %w", err)
	}

	// 8. Reviewer call (with one parse-retry inside reviewExtract).
	result, modelUsed, ms, _, err := h.reviewExtract(ctx, model, rendered, maxTokens)
	if errors.Is(err, providers.ErrResponseTruncated) {
		// Synthesise a warn envelope with category:other / criterion:reviewer_response.
		// Mirrors the per-task truncatedResult / primeTruncationResult path. modelUsed
		// is empty when the provider call truncated, so cite the resolved model ref.
		r := prependExtractClamp(extractTruncationResult(), clamp)
		logOutcome, logModelUsed, logVerdict, logFindings = "truncated", model.String(), r.Verdict, r.Findings
		return extractEnvelopeResult(r, model.String(), 0)
	}
	if err != nil {
		logOutcome, logModelUsed, logVerdict = "reviewer_error", model.String(), verdict.VerdictFail
		return nil, verdict.ExtractResult{}, err
	}

	// 8a. Merge preFindings from step 4 into the parsed result. The mixed
	// envelope case (some envelopes have evidence, others are evidence-bare or
	// have malformed diff/files but valid test_evidence) accumulated
	// pre-findings in step 4 but bypassed the all-bare short-circuit. Without
	// this merge those would be dropped. Order: pre-findings come first so the
	// caller sees them above reviewer-emitted findings. The new slice header
	// avoids aliasing preFindings into the returned envelope.
	if len(preFindings) > 0 {
		result.Findings = append(append([]verdict.Finding{}, preFindings...), result.Findings...)
	}

	// 9. Post-processing: KBStore-aware bm_commands gating and
	// kb_store_mismatch findings. Use an EMPTY slice (NOT nil) so the
	// strict-schema "bm_commands required" invariant holds if the result is
	// re-marshaled.
	if h.deps.Cfg.KBStore == "" {
		result.BMCommands = []verdict.BMCommand{}
	} else if h.deps.Cfg.KBStore == "basic-memory" {
		// Mismatch heuristic runs against every Proposal.Permalink AND every
		// BMCommand.ArgsJSON.permalink string value.
		permalinks := make([]string, 0, len(result.Proposals))
		for _, p := range result.Proposals {
			permalinks = append(permalinks, p.Permalink)
		}
		result.Findings = append(result.Findings, kbStoreMismatchFindingsForPermalinks(permalinks)...)
		result.Findings = append(result.Findings, kbStoreMismatchFindingsForBMCommands(result.BMCommands)...)
	}

	// 10. Prepend the clamp finding (no-op when clamp is zero).
	result = prependExtractClamp(result, clamp)

	// 11. Populate the deferred logger's + stats recorder's view (which write
	// the one structured JSON line on stderr and the one events.jsonl record
	// for this call). The success-path outcome label is the default "success"
	// set at function entry.
	logModelUsed, logVerdict, logMS, logProposals, logFindings = modelUsed, result.Verdict, ms, len(result.Proposals), result.Findings

	return extractEnvelopeResult(result, modelUsed, ms)
}

// toPromptCompletionEnvelopes maps the handler-boundary CompletionEnvelopeArg
// slice to the prompts-internal CompletionEnvelopeForExtract slice. Reuses
// toPromptFiles (handlers.go) so file conversion stays in one place.
func toPromptCompletionEnvelopes(in []CompletionEnvelopeArg) []prompts.CompletionEnvelopeForExtract {
	out := make([]prompts.CompletionEnvelopeForExtract, len(in))
	for i, e := range in {
		out[i] = prompts.CompletionEnvelopeForExtract{
			TaskTitle:    e.TaskTitle,
			Summary:      e.Summary,
			Verdict:      e.Verdict,
			Findings:     e.Findings,
			FinalDiff:    e.FinalDiff,
			FinalFiles:   toPromptFiles(e.FinalFiles),
			TestEvidence: e.TestEvidence,
		}
	}
	return out
}

// classifyEnvelopes walks args.CompletionEnvelopes and emits one pre-finding
// per envelope that fails the evidence-shape rules. Returns the accumulated
// pre-findings plus a bool indicating whether EVERY envelope was evidence-bare
// (all three of FinalDiff/FinalFiles/TestEvidence empty, OR diff/files
// malformed and TestEvidence empty). When allBare is true, the caller
// short-circuits to a synthetic refusal envelope.
func classifyEnvelopes(envs []CompletionEnvelopeArg) ([]verdict.Finding, bool) {
	pre := []verdict.Finding{}
	bareCount := 0
	for i, e := range envs {
		// (a) Three-fields-empty → evidence-bare with the canonical message.
		if e.FinalDiff == "" && len(e.FinalFiles) == 0 && e.TestEvidence == "" {
			pre = append(pre, verdict.Finding{
				Severity:   verdict.SeverityMajor,
				Category:   verdict.CategoryInsufficientEvidence,
				Criterion:  fmt.Sprintf("completion_envelopes[%d]", i),
				Evidence:   fmt.Sprintf("envelope[%d] (%q) carries no final_diff, final_files, or test_evidence", i, e.TaskTitle),
				Suggestion: "Re-submit with at least one of final_diff, final_files, or test_evidence populated.",
			})
			bareCount++
			continue
		}
		// (b) Diff/files shape check. Build a synthetic ValidateCompletionArgs
		// (no TestEvidence — checkEvidenceShape doesn't inspect it) and run
		// the existing guard.
		synth := ValidateCompletionArgs{FinalDiff: e.FinalDiff, FinalFiles: e.FinalFiles}
		reason := checkEvidenceShape(synth)
		if reason == "" {
			continue
		}
		if e.TestEvidence == "" {
			// Diff/files unusable AND no test fallback → evidence-bare.
			pre = append(pre, verdict.Finding{
				Severity:   verdict.SeverityMajor,
				Category:   verdict.CategoryInsufficientEvidence,
				Criterion:  fmt.Sprintf("completion_envelopes[%d]", i),
				Evidence:   fmt.Sprintf("envelope[%d] (%q): %s; test_evidence empty", i, e.TaskTitle, reason),
				Suggestion: "Re-submit with a complete unified diff in final_diff, full file contents in final_files, or test_evidence summarising the test run.",
			})
			bareCount++
			continue
		}
		// (c) Diff/files unusable but test_evidence carries the grounding →
		// envelope counts as having evidence; emit a `quality` sub-finding.
		pre = append(pre, verdict.Finding{
			Severity:   verdict.SeverityMinor,
			Category:   verdict.CategoryQuality,
			Criterion:  fmt.Sprintf("completion_envelopes[%d]", i),
			Evidence:   fmt.Sprintf("envelope[%d] (%q): %s; reviewer grounding will rely on test_evidence", i, e.TaskTitle, reason),
			Suggestion: "Submit a complete unified diff or full file contents next time so reviewer grounding does not depend solely on test_evidence.",
		})
	}
	return pre, bareCount == len(envs)
}

// reviewExtract is the extract-shaped sibling of review() (handlers.go:171) and
// reviewPrime() (prime_handler.go). It mirrors review()'s truncation handling:
// on providers.ErrResponseTruncated the caller can build a synthetic envelope
// so the truncation surfaces like every other tool does. The fourth return
// value (partialRaw) is reserved for future partial-recovery work; extract
// has no partial-recovery path in v0.6.0 (see Non-goals).
func (h *handlers) reviewExtract(ctx context.Context, model config.ModelRef, p prompts.Output, maxTokens int) (verdict.ExtractResult, string, int64, []byte, error) {
	rv, err := h.deps.Reviews.Get(model.Provider)
	if err != nil {
		return verdict.ExtractResult{}, "", 0, nil, err
	}
	start := time.Now()
	req := providers.Request{
		Model:      model.Model,
		System:     p.System,
		User:       p.User,
		MaxTokens:  maxTokens,
		JSONSchema: verdict.ExtractSchema(),
	}
	resp, err := rv.Review(ctx, req)
	if err != nil {
		if errors.Is(err, providers.ErrResponseTruncated) {
			return verdict.ExtractResult{}, "", 0, resp.RawJSON, err
		}
		return verdict.ExtractResult{}, "", 0, nil, err
	}
	r, err := verdict.ParseExtract(resp.RawJSON)
	if err != nil {
		// One retry with explicit reminder. Mirrors review() at handlers.go.
		req.User = p.User + "\n\n" + verdict.RetryHint()
		resp, err = rv.Review(ctx, req)
		if err != nil {
			if errors.Is(err, providers.ErrResponseTruncated) {
				return verdict.ExtractResult{}, "", 0, resp.RawJSON, err
			}
			return verdict.ExtractResult{}, "", 0, nil, err
		}
		r, err = verdict.ParseExtract(resp.RawJSON)
		if err != nil {
			return verdict.ExtractResult{}, "", 0, nil, fmt.Errorf("extract provider response failed schema after retry: %w", err)
		}
	}
	modelUsed := model.Provider + ":" + resp.Model
	if resp.Model == "" {
		modelUsed = model.String()
	}
	return r, modelUsed, time.Since(start).Milliseconds(), nil, nil
}

// extractEmptyEnvelopesResult is the synthetic refusal for the empty-
// completion_envelopes case. Verdict: fail; one critical
// insufficient_evidence finding. Three array fields are initialised to
// non-nil empty slices so the strict-schema invariant survives the
// JSON round-trip (nil slice marshals as null, which breaks the contract).
func extractEmptyEnvelopesResult() verdict.ExtractResult {
	return verdict.ExtractResult{
		Verdict: verdict.VerdictFail,
		Findings: []verdict.Finding{{
			Severity:   verdict.SeverityCritical,
			Category:   verdict.CategoryInsufficientEvidence,
			Criterion:  "completion_envelopes",
			Evidence:   "no completion envelopes were supplied",
			Suggestion: "Call extract_project_knowledge with at least one validate_completion envelope to ground proposals against.",
		}},
		Proposals:  []verdict.Proposal{},
		BMCommands: []verdict.BMCommand{},
		NextAction: "Re-call with at least one completion envelope.",
	}
}

// extractAllBareResult is the synthetic refusal for the case where EVERY
// envelope was evidence-bare. Verdict: fail; the per-envelope insufficient_
// evidence findings accumulated by classifyEnvelopes become Findings.
// Empty-slice initializers are LOAD-BEARING (see extractEmptyEnvelopesResult).
func extractAllBareResult(preFindings []verdict.Finding) verdict.ExtractResult {
	// Defensive copy so the returned envelope does not alias the caller's slice.
	findings := append([]verdict.Finding{}, preFindings...)
	return verdict.ExtractResult{
		Verdict:    verdict.VerdictFail,
		Findings:   findings,
		Proposals:  []verdict.Proposal{},
		BMCommands: []verdict.BMCommand{},
		NextAction: "Re-call with envelopes that carry final_diff, final_files, or test_evidence.",
	}
}

// extractTooLargeResult mirrors primeTooLargeResult (prime_handler.go) and
// tooLargeEnvelope (handlers.go): SeverityCritical so the ladder derives fail
// from one critical, matching the explicit Verdict: fail. Verdict is set
// explicitly because this synthetic result short-circuits before any reviewer
// call. (v0.5.2 reconciliation: NOT SeverityMajor as the plan snippet says —
// the live helpers all use SeverityCritical.)
func extractTooLargeResult(size, capBytes int) verdict.ExtractResult {
	return verdict.ExtractResult{
		Verdict: verdict.VerdictFail,
		Findings: []verdict.Finding{{
			Severity:   verdict.SeverityCritical,
			Category:   verdict.CategoryTooLarge,
			Criterion:  "payload",
			Evidence:   fmt.Sprintf("payload %d bytes exceeds cap %d", size, capBytes),
			Suggestion: "Shrink completion_envelopes (drop final_files or use a unified diff), or split into multiple calls.",
		}},
		Proposals:  []verdict.Proposal{},
		BMCommands: []verdict.BMCommand{},
		NextAction: "Reduce the payload and retry.",
	}
}

// extractTruncationResult mirrors truncatedResult (handlers.go) and
// primeTruncationResult (prime_handler.go) for the no-analysis case where
// the reviewer's response was cut off and NO complete findings were
// recovered. Per v0.5.2 the truncation finding is SeverityMajor (NOT minor
// as the plan snippet's text might imply) so the ladder derives warn
// consistently — extract mirrors that posture. Partial-recovery is NOT
// implemented for extract in v0.6.0; this helper is the no-analysis fallback,
// so Partial is left zero (false).
func extractTruncationResult() verdict.ExtractResult {
	return verdict.ExtractResult{
		Verdict: verdict.VerdictWarn,
		Findings: []verdict.Finding{{
			Severity:   verdict.SeverityMajor,
			Category:   verdict.CategoryOther,
			Criterion:  "reviewer_response",
			Evidence:   providers.ErrResponseTruncated.Error(),
			Suggestion: "Raise ANTI_TANGENT_EXTRACT_MAX_TOKENS (bounded by ANTI_TANGENT_MAX_TOKENS_CEILING), shrink completion_envelopes, or pass an explicit max_tokens_override.",
		}},
		Proposals:  []verdict.Proposal{},
		BMCommands: []verdict.BMCommand{},
		NextAction: "Retry with a higher max_tokens_override (or raise the configured max-tokens cap).",
	}
}

// prependExtractClamp inserts the clamp finding at the head of r.Findings when
// clamp is non-zero. Mirrors prependPrimeClamp / prependClamp / prependPlanClamp
// so every flow — success, truncation, payload-too-large — treats the clamp
// finding identically: it lives in Findings, NOT in next_action.
func prependExtractClamp(r verdict.ExtractResult, clamp verdict.Finding) verdict.ExtractResult {
	if clamp.Severity == "" {
		return r
	}
	r.Findings = append([]verdict.Finding{clamp}, r.Findings...)
	return r
}

// extractEnvelopeResult marshals an ExtractResult into an MCP CallToolResult.
// Mirrors primeEnvelopeResult: the wire payload is the ExtractResult plus
// model_used and review_ms siblings. SummaryBlock is populated here so every
// exit path (happy / truncation / too-large / refusal) gets a paste-ready
// summary for free.
func extractEnvelopeResult(r verdict.ExtractResult, modelUsed string, reviewMS int64) (*mcp.CallToolResult, verdict.ExtractResult, error) {
	// Ensure non-nil collections survive marshaling — the wire-format contract
	// requires findings, proposals, and bm_commands to be arrays. The
	// reviewer-parsed paths already enforce this in ParseExtract; the
	// synthetic helpers above also seed empty slices. Belt-and-braces here
	// keeps custom test paths from emitting `null` accidentally.
	if r.Findings == nil {
		r.Findings = []verdict.Finding{}
	}
	if r.Proposals == nil {
		r.Proposals = []verdict.Proposal{}
	}
	if r.BMCommands == nil {
		r.BMCommands = []verdict.BMCommand{}
	}
	r.SummaryBlock = formatExtractSummary(r, modelUsed, reviewMS)
	body, err := json.MarshalIndent(struct {
		verdict.ExtractResult
		ModelUsed string `json:"model_used"`
		ReviewMS  int64  `json:"review_ms"`
	}{r, modelUsed, reviewMS}, "", "  ")
	if err != nil {
		return nil, verdict.ExtractResult{}, err
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(body)}},
	}, r, nil
}

// kbStoreMismatchFindingsForPermalinks reuses the existing
// kbStoreMismatchFindings heuristic by wrapping each permalink in a
// minimal Pick. Keeps the BM-shape detection in one place.
func kbStoreMismatchFindingsForPermalinks(permalinks []string) []verdict.Finding {
	if len(permalinks) == 0 {
		return nil
	}
	picks := make([]verdict.Pick, 0, len(permalinks))
	for _, p := range permalinks {
		picks = append(picks, verdict.Pick{Permalink: p})
	}
	return kbStoreMismatchFindings(picks)
}

// kbStoreMismatchFindingsForBMCommands parses each BMCommand.ArgsJSON and
// probes args["permalink"].(string). Anything starting with `/` or containing
// `://` produces one minor other/kb_store_mismatch finding per offender. If
// ArgsJSON does not parse or has no permalink field, the entry is skipped
// silently (ParseExtract already rejected malformed args_json upstream, but
// the probe is defensive).
func kbStoreMismatchFindingsForBMCommands(cmds []verdict.BMCommand) []verdict.Finding {
	if len(cmds) == 0 {
		return nil
	}
	var permalinks []string
	for _, c := range cmds {
		var args map[string]any
		if err := json.Unmarshal([]byte(c.ArgsJSON), &args); err != nil {
			continue
		}
		perma, ok := args["permalink"].(string)
		if !ok || perma == "" {
			continue
		}
		permalinks = append(permalinks, perma)
	}
	return kbStoreMismatchFindingsForPermalinks(permalinks)
}
