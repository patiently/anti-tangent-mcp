# validate_plan iteration UX — design (0.3.0)

**Status:** approved 2026-05-12
**Target version:** 0.3.0 (minor bump)
**Tracking issue:** [#10](https://github.com/patiently/anti-tangent-mcp/issues/10)
**Branch:** `version/0.3.0`

## Background

Issue [#10](https://github.com/patiently/anti-tangent-mcp/issues/10) is a field report from a 7-round `validate_plan` session against a small compliance-fix plan (5 tasks, ~9 KB markdown). The plan converged to "self-consistent and ready to action" around round 5, but the reviewer kept finding adjacent issues through round 7, where the reviewer's `max_tokens` cap was hit and **the only finding returned was its own truncation note** — ~9 KB of input produced zero usable feedback.

The report enumerates 8 observations. This design bundles the highest-leverage subset into a single release:

1. **Bug fix:** partial findings on truncation (the only "real bug" called out in the report).
2. **Feature:** per-call `max_tokens_override` arg so a caller can pre-emptively raise the cap without env-var access.
3. **Feature:** `mode: quick | thorough` on `validate_plan` to address the "knowing when to stop" friction on small ASAP plans.
4. **Prompt edits:** hypothetical-marker (`e.g. illustrative —` prefix for fabricated code-symbol examples) and a `next_action` specificity nudge.

## Scope

In scope:

- Partial-findings recovery in the provider → handler → response pipeline. Applied to **all four tools** (`validate_task_spec`, `check_progress`, `validate_completion`, `validate_plan`), since `truncatedEnvelope()` and `truncatedPlanResult()` have the same throw-away-findings bug.
- New optional arg `max_tokens_override int` on all four `*Args` structs, clamped to a new `ANTI_TANGENT_MAX_TOKENS_CEILING` config knob (default 16384).
- New optional arg `mode string` on `ValidatePlanArgs`, values `"quick"` / `"thorough"`. Reviewer-side prompt branching only; no server-side findings cap in this release.
- Hypothetical-marker paragraph added to the `## Reviewer ground rules` block in `plan.tmpl`, `plan_findings_only.tmpl`, `plan_tasks_chunk.tmpl`.
- `next_action` specificity nudge added to the `## Output` section of the same three plan templates.
- Goldens regenerated; new anchor-assertion tests added; new parser unit tests; new handler integration tests.

Out of scope (deferred):

- **Severity rubric tightening** (item #2 from issue #10). Quality-uncertain — could under-fire legitimate `major` findings. Defer until an eval harness exists.
- **Convergence signal / `plan_quality` field / verdict-delta framing / whack-a-mole grouping** (items #1, #4, #8). These reshape the response API and need their own design pass.
- **Per-task templates** (`pre.tmpl`, `mid.tmpl`, `post.tmpl`) get the truncation/`max_tokens_override` plumbing but **not** the prompt edits. The per-task templates receive actual code, so the epistemic-boundary framing the plan templates use doesn't apply.

## Bump rationale

`0.2.1 → 0.3.0` (minor). Two backward-compatible new features (`max_tokens_override` and `mode`) plus an additive response field (`partial: bool`). Existing callers see no breakage; new behavior is opt-in via the new args.

Per project convention (`CLAUDE.md`): "backward-compatible feature → bump minor." Both `mode` and `max_tokens_override` qualify. The truncation fix could stand alone as a patch, but bundling it with the features is cheaper than two releases.

## Design

### 1. Partial findings on truncation

**Provider layer.** Currently `internal/providers/{openai,anthropic,google}.go` return `(Response{}, ErrResponseTruncated)` when the finish reason indicates a cap hit (`length` / `max_tokens` / `MAX_TOKENS`), discarding the partial text. Change to return a *populated* `Response` alongside the sentinel:

```go
if parsed.Candidates[0].FinishReason == "MAX_TOKENS" {
    return Response{
        RawJSON:      []byte(parsed.Candidates[0].Content.Parts[0].Text),
        Model:        parsed.ModelVersion,
        InputTokens:  parsed.UsageMetadata.PromptTokenCount,
        OutputTokens: parsed.UsageMetadata.CandidatesTokenCount,
    }, fmt.Errorf("google: %w", ErrResponseTruncated)
}
```

Handler-side, the `errors.Is(err, ErrResponseTruncated)` check stays — but instead of immediately returning a truncated envelope, the handler first attempts partial-recovery on `resp.RawJSON`.

**Tolerant parser.** New file `internal/verdict/parser_partial.go` with two functions:

```go
// ParseResultPartial parses a possibly-truncated reviewer response into a
// Result. Returns (result, true) when partial recovery succeeded with at
// least one complete finding; (result, false) when no findings could be
// recovered (caller should fall back to truncatedEnvelope).
func ParseResultPartial(raw []byte) (Result, bool)

// ParsePlanResultPartial does the same for the plan-level shape.
func ParsePlanResultPartial(raw []byte) (PlanResult, bool)
```

**Algorithm:**

1. Try `json.Unmarshal` first. On success, return the parsed result (no recovery needed — truncation hit between top-level fields where the JSON happened to remain valid). Rare but possible.
2. On parse failure, walk the raw bytes tracking brace/bracket depth and quote state. Find the last complete top-level element inside each unbounded array (`findings[]` for per-task; `plan_findings[]` and each `tasks[].findings[]` for plan). Truncate after that element. Close any open arrays/objects in reverse depth order. Retry `json.Unmarshal`.
3. If the second parse still fails, return `(zero, false)`.

The walk is bounded by input length and uses no third-party dependency — a hand-written state machine in `parser_partial.go` is the right cost.

**Envelope shape.** Add `Partial bool \`json:"partial,omitempty"\`` to both `verdict.Result` and `verdict.PlanResult`. `omitempty` keeps the field absent in the common (non-truncated) case, so JSON consumers see no change. Go consumers see an additive struct field whose zero value matches the old behavior.

**Truncation finding.** Keep emitting it but downgrade severity from `major` to `minor` and reword:

```
Reviewer output truncated at the max_tokens cap; N complete findings recovered.
Raise ANTI_TANGENT_<PER_TASK|PLAN>_MAX_TOKENS or pass max_tokens_override to capture more.
```

So the caller sees both the recovered findings AND the awareness signal. If zero findings were recovered (`ParseResultPartial` returns `(_, false)`), fall back to current behavior — single `major` truncation finding, empty findings list.

### 2. Per-call `max_tokens_override`

**Arg shape.** New optional field on all four `*Args` structs:

```go
MaxTokensOverride int `json:"max_tokens_override,omitempty"`
```

Threaded into `providers.Request.MaxTokens` for that call.

**Clamping.** New config knob `MaxTokensCeiling int` in `internal/config`, sourced from `ANTI_TANGENT_MAX_TOKENS_CEILING` env var, default `16384`. Behavior:

- `MaxTokensOverride == 0` (or unset): use configured default (`PerTaskMaxTokens` or `PlanMaxTokens`).
- `0 < MaxTokensOverride ≤ Ceiling`: use the override directly.
- `MaxTokensOverride > Ceiling`: use the ceiling, AND emit a `minor` finding:

  ```
  max_tokens_override (N) exceeds ceiling (Ceiling); used Ceiling.
  Raise ANTI_TANGENT_MAX_TOKENS_CEILING if you need a larger budget.
  ```

The clamp finding fires once per call, no accumulation.

**Why a ceiling?** (1) Provider APIs have their own output-token limits and we shouldn't proxy arbitrary values into a 400. (2) Cost protection — a runaway caller shouldn't request 100k output tokens. 16k matches the largest common per-provider output cap on currently-used models and is well above any plan we've seen in practice.

### 3. `mode: quick | thorough` on validate_plan

**Arg shape.** Optional `Mode string \`json:"mode,omitempty"\`` on `ValidatePlanArgs`. Values: `""`, `"quick"`, `"thorough"`. Empty defaults to `"thorough"` (current behavior preserved). Invalid values rejected at the handler boundary with `errors.New("mode must be \"quick\" or \"thorough\"")`.

**Prompt branching.** `internal/prompts/prompts.go` gets a new `Mode string` field on `PlanInput`. Each of `plan.tmpl`, `plan_findings_only.tmpl`, `plan_tasks_chunk.tmpl` gets a `{{ if eq .Mode "quick" }}...{{ end }}` block in the `## What to evaluate` section:

```
**Quick mode.** Surface only the most-severe findings — at most 3 per scope
(3 plan-level findings, and at most 3 findings per task). Omit minor nits and
stylistic suggestions. Prefer fewer high-quality findings over many low-value
ones.
```

**No server-side cap.** Reviewer-side instruction only. Trusts the reviewer with the judgment call. If quality drift surfaces (reviewer ignores the cap), add a server-side backstop in a follow-up.

### 4. Prompt ride-alongs

**Hypothetical-marker (4th paragraph in `## Reviewer ground rules`).** Added after the three paragraphs from 0.2.1, in each plan template:

> When you cite a code symbol's signature, an example sibling key, or an adjacent attribute value as an *illustration*, prefix it with `e.g. illustrative —`. The reader does not know whether your illustration matches the codebase, and false confidence is worse than no example.

**`next_action` specificity nudge.** Added in the `## Output` section of each plan template, near the JSON schema directive:

> The `next_action` field must name the single highest-leverage finding the author should address next — not generic advice like "address the warnings." If no findings warrant immediate attention, say so explicitly: "no blocking findings; proceed to dispatch."

Both edits are plan-template-scoped only. Per-task templates (`pre.tmpl`, `mid.tmpl`, `post.tmpl`) stay unchanged for this release.

### Files touched

```
Modify  internal/providers/openai.go      — return partial bytes on truncation
Modify  internal/providers/anthropic.go   — same
Modify  internal/providers/google.go      — same
Modify  internal/providers/reviewer.go    — (no change to sentinel, but document new contract)
Create  internal/verdict/parser_partial.go — tolerant JSON parser
Create  internal/verdict/parser_partial_test.go — parser unit tests
Modify  internal/verdict/verdict.go       — add Partial field to Result and PlanResult
Modify  internal/config/config.go         — add MaxTokensCeiling
Modify  internal/mcpsrv/handlers.go       — wire MaxTokensOverride + Mode args; partial-recovery branch
Modify  internal/mcpsrv/handlers_test.go  — handler integration tests for truncation, clamping, mode
Modify  internal/prompts/prompts.go       — add Mode to PlanInput
Modify  internal/prompts/templates/plan.tmpl
Modify  internal/prompts/templates/plan_findings_only.tmpl
Modify  internal/prompts/templates/plan_tasks_chunk.tmpl
Modify  internal/prompts/testdata/plan_basic.golden
Modify  internal/prompts/testdata/plan_findings_only.golden
Modify  internal/prompts/testdata/plan_tasks_chunk.golden
Create  internal/prompts/testdata/plan_basic_quick.golden
Create  internal/prompts/testdata/plan_findings_only_quick.golden
Create  internal/prompts/testdata/plan_tasks_chunk_quick.golden
Modify  internal/prompts/prompts_test.go  — new anchor-assertion tests
Modify  CHANGELOG.md                       — add ## [0.3.0] - 2026-05-12
Modify  README.md                          — document new args + ceiling env var
Modify  INTEGRATION.md                     — document partial-findings shape + mode arg
```

### Testing

**Unit tests (parser).** `internal/verdict/parser_partial_test.go` — 7 cases:

- Complete result → output matches `json.Unmarshal`.
- Plan-level result complete → output matches `json.Unmarshal`.
- Truncated mid-finding in per-task result → recovers prior findings, `Partial: true`.
- Truncated mid-task in plan result → recovers prior tasks, `Partial: true`.
- Truncated before any finding → returns `(zero, false)`.
- Truncated inside string literal → returns `(zero, false)`.
- Truncated at trailing whitespace after valid JSON → recovers all findings.

**Unit tests (provider).** One new case per provider asserting the truncated response now carries `RawJSON` populated. Three new cases total.

**Unit tests (handler).** `internal/mcpsrv/handlers_test.go`:

- One per tool (4 total) asserting that a stubbed truncated provider response produces an envelope with `partial: true`, recovered findings, AND the downgraded `minor` truncation finding.
- One per tool (4 total) asserting `max_tokens_override` behavior: zero → default, in-range → override used, over-ceiling → ceiling used + clamp finding.
- One asserting invalid `mode` value is rejected with the expected error.
- One asserting `mode: quick` is plumbed into the prompt input.

**Anchor-assertion tests (prompts).** `internal/prompts/prompts_test.go`:

- `TestPlanTemplates_IncludeHypotheticalMarker` — `"e.g. illustrative —"` in all three plan templates.
- `TestPlanTemplates_IncludeNextActionNudge` — `"single highest-leverage finding"` in all three plan templates.
- `TestPlanTemplates_QuickMode_IncludesInstruction` — `"Quick mode."` and `"at most 3 per scope"` in all three plan templates when `Mode == "quick"`.
- `TestPlanTemplates_DefaultMode_OmitsQuickInstruction` — `"Quick mode."` NOT present when `Mode == ""` or `Mode == "thorough"`.

**Golden regeneration.** Three existing plan goldens regenerated (capture ride-alongs); three new `*_quick.golden` created. Diff reviewed at PR time.

**No new E2E.** Truncation, clamping, and mode plumbing are all reproducible with `httptest.Server` — unit-test territory.

## Risks and mitigations

| Risk | Mitigation |
|---|---|
| Tolerant parser misreads a complete response | Try std `json.Unmarshal` first; only fall back on parse error. Cross-cutting test pins parity with std on complete inputs. |
| `mode=quick` reviewer ignores the 3-per-scope cap | No server-side backstop this release. Cue tokens ("at most 3", "Omit") matched the wording style that held in 0.2.1 ground rules. If field reports show drift, add a backstop. |
| `max_tokens_override` clamp finding spams a caller | Single finding per call, no accumulation. If noisy in practice, demote to stderr log line in a follow-up. |
| `Partial bool` field changes envelope shape for older consumers | `json:"partial,omitempty"` keeps it absent in the common case. Additive for both JSON and Go consumers. |
| Hypothetical-marker over-fires (reviewer prefixes every code reference) | PR-time golden-diff review against a known-good plan input. If false-fires surface, refine paragraph wording. |
| Goldens drift across regens | Review each diff at PR time; never bulk-accept. Six goldens, manageable. |
| Per-task templates still throw away truncated output for prompt-tier issues | Out of scope for this release. The truncation *plumbing* covers all four tools; only the prompt edits skip per-task. If per-task `next_action` drift shows up in the wild, follow-up issue. |

## Commit shape

Multi-commit plan on `version/0.3.0`, one commit per logical layer for review legibility:

1. `feat(providers): return partial bytes on truncation` — provider changes + their unit tests.
2. `feat(verdict): tolerant JSON parser for truncated responses` — `parser_partial.go` + tests.
3. `feat(mcpsrv): partial-findings recovery in all four handlers` — handler changes + tests.
4. `feat(mcpsrv): max_tokens_override arg with ceiling clamp` — config + arg + tests.
5. `feat(validate_plan): mode quick|thorough arg` — prompts + args + tests.
6. `chore(prompts): hypothetical-marker + next_action specificity` — prompt edits + goldens + tests.
7. `docs: CHANGELOG / README / INTEGRATION for 0.3.0` — documentation.

Merge commit carries `[minor]` to drive the workflow's version bump.

## CHANGELOG entry (0.3.0)

```markdown
## [0.3.0] - 2026-05-12

### Added
- `max_tokens_override` optional arg on all four tools (`validate_task_spec`, `check_progress`, `validate_completion`, `validate_plan`) for per-call control over the reviewer's output-token budget. Clamped to `ANTI_TANGENT_MAX_TOKENS_CEILING` (default 16384); over-ceiling values emit a `minor` clamp finding.
- `mode: "quick" | "thorough"` optional arg on `validate_plan`. `quick` instructs the reviewer to surface at most 3 most-severe findings per scope (plan-level and each task) and omit stylistic nits; `thorough` (default) preserves prior behavior.
- `partial: true` field on `Result` and `PlanResult` envelopes when the reviewer's response was truncated at the `max_tokens` cap but partial findings could be recovered.
- Hypothetical-marker guardrail (`e.g. illustrative —` prefix) added to `## Reviewer ground rules` in `validate_plan` templates, complementing the 0.2.1 epistemic-boundary work.
- `next_action` specificity nudge in `validate_plan` templates: the field must name the single highest-leverage finding, not generic advice.

### Fixed
- Reviewer-output truncation no longer discards complete findings produced before the cap hit. All four tools now run truncated responses through a tolerant JSON parser and emit any recoverable findings alongside a downgraded (`minor`) truncation marker. Previously, ~9 KB of plan input could yield zero usable feedback when the reviewer's output cap was reached mid-response. Closes [#10](https://github.com/patiently/anti-tangent-mcp/issues/10).
```
