# Review-noise and evidence-context UX — design (0.3.3)

**Status:** draft 2026-05-14
**Target version:** 0.3.3 (patch bump)
**Tracking issue:** not filed yet
**Branch:** `version/0.3.3`

## Background

Two YN-10178 field reports exercised anti-tangent on a large implementation plan and the subsequent eight-task execution:

- `anti-tangent-mcp-feedback.md`: three `validate_plan` iterations against a 1,224-line / ~62 KB / 8-task plan.
- `mcp-companion-retrospective.md`: execution-phase call log across `validate_task_spec`, `validate_completion`, and optional CodeScene companion tools.

The reports agree on the core pattern: anti-tangent caught real planning and evidence issues, but too much of the response volume was predictable text-only-reviewer noise. The largest offender was `unverifiable_codebase_claim`: useful as an explicit boundary marker, but high-volume and not actionable after the caller has already verified references. The execution report also showed that terse ACs such as "existing behavior remains unchanged" are often pinned by existing tests, but the current `validate_task_spec` API has no structured way to say that.

This release tightens the UX around those failure modes without changing anti-tangent's architecture: the reviewer remains text-only, advisory, and stateless except for the existing per-task session store.

## Scope

In scope:

- Plan-level truncation UX improvements for `validate_plan`: adaptive default output budget, stronger no-analysis truncation severity, and self-contained retry guidance.
- `validate_plan` post-processing that rolls task-level `unverifiable_codebase_claim` findings into one plan-level checklist finding.
- Verdict calibration so unverifiable-only findings do not keep a plan at `warn` when no actual plan-quality defects remain.
- Additive `validate_task_spec` inputs:
  - `pinned_by: []string` to name existing tests/docs/static checks that pin referenced behavior.
  - `phase: "pre" | "post"` to distinguish normal pre-implementation spec validation from post-hoc/session-recovery calls.
- Prompt updates so `pinned_by` and `phase` guide the reviewer instead of becoming more text to critique.
- Completion preflight hint when a summary references a deliverable path but neither `final_files` nor `final_diff` contains file content.
- Integration docs for caller discipline learned from YN-10178: pre-flight greps, `final_files` for doc deliverables, avoiding "verified in code" self-reassurance in plan prose, explicit commit-policy carve-outs, and CodeScene cadence.

Out of scope:

- A first-class `info` severity. Existing schemas and consumers understand `critical|major|minor`; introducing `info` is cleaner conceptually but broader than needed for this release.
- A `verified_claims` input for `validate_plan`. That is likely useful, but it needs a separate design for claim identity, dedupe, stale verification, and how much trust the reviewer should place in caller assertions.
- `plan_text_from_file`. MCP hosts vary in filesystem access; direct server-side file reads would change the deployment/security model.
- Cross-run finding memory, `iteration_count`, or finding fingerprint deltas. `validate_plan` remains stateless by design.
- Automatic CodeScene invocation. CodeScene remains a companion convention outside this server.

## Bump rationale

`0.3.2 → 0.3.3` (patch). The release adds backward-compatible optional input fields to `validate_task_spec` and changes server-side response normalization for plan review. Existing callers continue to work: omitted `pinned_by` behaves like an empty list, omitted `phase` behaves like `pre`, and output fields retain the existing schema.

The `unverifiable_codebase_claim` rollup changes response shape content but not the JSON schema: there are fewer task-level findings and one plan-level finding. That is behaviorally meaningful but compatible with the advisory contract.

## Design

### 1. Adaptive `validate_plan` output budget

The YN-10178 plan truncated with the default `ANTI_TANGENT_PLAN_MAX_TOKENS=4096`, returning `tasks: 0` and a lone minor truncation finding. Retrying with `max_tokens_override=16000` produced full analysis.

Add an effective-budget helper for `validate_plan` only:

```go
func effectivePlanMaxTokens(args ValidatePlanArgs, cfg config.Config, taskCount int) (int, verdict.Finding, error)
```

Behavior:

- If `max_tokens_override > 0`, preserve the existing override/clamp semantics exactly.
- If no override is supplied, compute a floor of `max(cfg.PlanMaxTokens, min(cfg.MaxTokensCeiling, 2000 + 800*taskCount))`.
- If the adaptive floor exceeds `cfg.PlanMaxTokens`, use it silently. This is not a caller error and should not emit a finding.
- The ceiling still protects cost and provider limits.

Control-flow requirement: preserve the existing early `effectiveMaxTokens` call for explicit overrides before payload-size and no-heading early exits. That keeps negative override rejection and clamp findings on early-exit envelopes. After `planparser.SplitTasks`, apply the adaptive floor only when `max_tokens_override` was omitted.

Rationale: plan output scales roughly with task count. The existing 4096-token default can be adequate for small plans, but a plan-level result needs one block per task plus plan-level findings and summary metadata.

### 2. Truncation severity and `next_action`

Keep partial-recovery behavior from 0.3.0. Change only the no-usable-analysis plan truncation path, i.e. `truncatedPlanResult()` when no complete findings/tasks were recovered.

New behavior:

- `severity: major` instead of `minor`.
- `plan_verdict: warn` remains unchanged.
- `plan_quality: rough` set explicitly in `truncatedPlanResult()`, because no analysis occurred.
- `next_action` is self-contained:

```text
Retry with max_tokens_override >= 16000, or set ANTI_TANGENT_PLAN_MAX_TOKENS in the MCP server env. If overrides are clamped, raise ANTI_TANGENT_MAX_TOKENS_CEILING.
```

The exact numeric suggestion can be `min(MaxTokensCeiling, max(16000, adaptiveFloor))` when the handler has config context. If the helper remains config-free, use 16000 because that is the current default ceiling and the known successful retry value from the field report.

Partial-recovery truncation markers stay `minor`, because the caller received at least some usable review signal.

### 3. Roll up `unverifiable_codebase_claim` in `validate_plan`

Add a plan-result normalization step before `planEnvelopeResult` summary formatting:

```go
func normalizePlanUnverifiableFindings(pr *verdict.PlanResult)
```

Behavior:

1. Collect all task-level findings where `Category == unverifiable_codebase_claim`.
2. Remove those findings from `PlanTaskResult.Findings`.
3. Append one plan-level finding when any were removed:

   - `Severity: minor`
   - `Category: unverifiable_codebase_claim`
   - `Criterion: codebase_reference_checklist`
   - `Evidence:` one compact line per affected task, for example `Task 3: network.kt:724-744; classified.questionDataNeed; ConsentGateResult.ShortConfirmFire`
   - `Suggestion: Pre-flight these references with grep/codebase-aware review before dispatch. Do not treat this checklist as a plan-quality defect if the references were already verified.`

4. Preserve any reviewer-emitted plan-level `unverifiable_codebase_claim` findings as-is. The v0.3.3 rollup only removes task-level findings and appends one task-reference checklist. This avoids a lossy merge rule while keeping verdict calibration correct.

The rollup deliberately does not attempt to parse symbols perfectly. It should use the reviewer-provided `Evidence` text, truncated per task to a deterministic cap if needed, because this is a human checklist not a machine-verification contract. Use a separate cap from `summaryEvidenceMax`; 240 characters per task is enough for roughly two concise lines of paths/symbols without letting one task dominate the rollup.

### 4. Verdict calibration for unverifiable-only results

After rollup, calibrate plan verdicts:

```go
func calibratePlanVerdictForUnverifiableOnly(pr *verdict.PlanResult)
```

If every finding across `plan_findings` and `tasks[].findings` is both:

- `severity: minor`
- `category: unverifiable_codebase_claim`

then set:

- `plan_verdict = pass`
- `plan_quality = actionable` unless it is already `rigorous`
- `next_action` should say no blocking plan-quality findings remain and the caller should pre-flight the rolled-up references before dispatch.

If any non-unverifiable finding remains, do not override the reviewer verdict. This preserves real `ambiguous_spec`, `missing_acceptance_criterion`, and `scope_drift` findings such as the YN-10178 Task 6 scenario-specificity issue.

### 5. `validate_task_spec.pinned_by`

Add optional field:

```go
type ValidateTaskSpecArgs struct {
    // existing fields...
    PinnedBy []string `json:"pinned_by,omitempty"`
}
```

Thread it into `session.TaskSpec` so the value is available throughout the lifecycle once a session is created:

```go
type TaskSpec struct {
    // existing fields...
    PinnedBy []string
    Phase    string
}
```

`pinned_by` examples:

- `DriverInviteRecommendationBotTest.reuses_cached_recommendation_when_user_replies_yes`
- `./gradlew :invite-recommendation:test --tests '*Consent*'`
- `docs/superpowers/YN-10178/scenario-coverage.md`

Prompt behavior:

- Render a `Pinned by:` section in both `pre.tmpl` and `post.tmpl` when non-empty. The completion reviewer needs the same anchors when judging whether terse "existing behavior remains unchanged" ACs were adequately protected.
- Do not change `mid.tmpl` in v0.3.3. Mid-review is optional and drift-focused; the stored field remains available for a later prompt update if field data shows it helps.
- Tell the reviewer to treat these entries as caller-supplied anchors for existing behavior, not as codebase facts it can independently verify.
- For ACs like "existing behavior remains unchanged," prefer a minor `quality` suggestion to enumerate behavior only when `pinned_by` is empty or obviously unrelated.
- Do not emit `unverifiable_codebase_claim` merely because a `pinned_by` entry names a file/class/method. The caller is explicitly supplying it as context.
- Do not add `pinned_by` to `validate_plan` in this release. A plan-level equivalent should be designed as `verified_claims` or task metadata so claim identity, stale verification, and reviewer trust are explicit.

Server-side validation:

- Reject no values for existence. The server is text-only and should not grep.
- Trim whitespace and drop empty entries before rendering/storing.
- Cap entries defensively, e.g. max 50 entries and max 500 characters per entry, to prevent accidental payload bloat. Over-cap can return a handler error or a `payload_too_large`-style envelope; prefer handler error for invalid args before provider cost.

### 6. `validate_task_spec.phase`

Add optional field:

```go
type ValidateTaskSpecArgs struct {
    // existing fields...
    Phase string `json:"phase,omitempty"`
}
```

Accepted values:

- `""` or `"pre"`: normal behavior. Validate the task spec before implementation.
- `"post"`: post-hoc/spec-recovery behavior. The reviewer should still flag contradictions and missing ACs, but should bias toward implementation-alignment risk rather than blocking on AC wording polish.

Invalid values return `phase must be "pre" or "post"` at the handler boundary.

Session semantics: `phase: post` creates and stores a session exactly like `phase: pre`. `Phase` is observability and prompt context only; it does not alter TTL, session identity, or completion validation mechanics.

Prompt behavior:

- Pre/default: current framing, with `pinned_by` context.
- Post: add a short instruction:

```text
Phase: post-hoc. The caller may already have implemented the task and needs a session/review baseline. Prioritize contradictions that could make completion validation unreliable. Do not fail solely because an AC is terse when pinned_by provides concrete existing tests or docs.
```

This is a relief valve, not an encouragement to skip the pre-hook. Docs must continue to say `validate_task_spec` belongs at task start.

### 7. Completion preflight hint for referenced deliverables

YN-10178 Task 8 spent two reviewer calls discovering that a doc deliverable referenced in `summary` was not included in `final_files`.

Add a conservative pre-LLM prompt hint, not a rejection:

- If `summary` contains a markdown-ish path ending in `.md`, `.txt`, `.json`, `.yaml`, or `.yml`, and neither `final_files.Path` nor `final_diff` mentions that path, include a prompt note before review.
- Do not add a synthetic finding in v0.3.3. The reviewer can decide whether the missing content matters to the ACs.

Implementation:

- Extract candidate paths with a narrow regex such as `[A-Za-z0-9_./-]+\.(md|txt|json|ya?ml)`.
- Pass `ReferencedPathsMissingEvidence []string` into `prompts.PostInput`.
- Render a note: `The summary references these paths but they are not present in final_files paths or final_diff text: ... If a referenced path is a deliverable, require full evidence.`

This avoids another hard rejection rule and keeps current successful code-only completion flows unchanged. Source-code extensions such as `.go`, `.kt`, `.py`, and `.ts` are intentionally excluded because they commonly appear in summaries and diffs as implementation references rather than standalone deliverables; false positives would dominate the signal.

### 8. Documentation and tool descriptions

Update `INTEGRATION.md`, `README.md`, and the `validate_plan`/`validate_task_spec` tool descriptions.

Plan-author guidance:

- State explicit commit-policy carve-outs in plan prose. The reviewer reads the plan literally and cannot see repo-level `CLAUDE.md` policy.
- Do not add self-reassurance notes like "all cited file references were verified." They are themselves unverifiable to the reviewer and increase noise.
- For long plans, strip line-number prefixes from host `Read` output before passing `plan_text`.
- For large plans, use `max_tokens_override` or raise `ANTI_TANGENT_PLAN_MAX_TOKENS`; v0.3.3 also adapts defaults by task count.

Task-spec guidance:

- Use `pinned_by` for existing tests, docs, or commands that pin "unchanged behavior" ACs.
- Keep `phase` omitted unless recovering from a post-hoc call; normal usage remains pre-implementation.
- Pre-flight greps before calling the tool when the spec names many codebase references.

Completion guidance:

- Use `final_files` for doc deliverables and generated artifacts whose content is the acceptance criterion.
- Use `final_diff` for code changes when a complete unified diff is enough.
- Include `test_evidence` for commands and outputs, but do not rely on prose summary alone.

CodeScene companion guidance:

- Include a concrete "Enable CodeScene companion" subsection in `INTEGRATION.md` that tells consumers what to add outside anti-tangent: install/configure `codescene-mcp` in their MCP host, provide `CS_ACCESS_TOKEN`, and add a host-level server entry equivalent to `command: npx`, `args: ["-y", "@codescene/codehealth-mcp"]`, with `CS_ACCESS_TOKEN` in the environment. Keep wording host-agnostic and link to the upstream install guide for exact host-specific config shapes.
- State that anti-tangent does not call CodeScene automatically; consumers activate it by adding CodeScene MCP to the same host and by including the companion calls in their dispatch clause.
- Call `pre_commit_code_health_safeguard` for each task that touches production code.
- Call `analyze_change_set` during the verification sweep or before final branch handoff.
- Call `code_health_review` when a safeguard returns `degraded`, to decide whether to recommend a sibling refactor ticket.

New-feature usage guidance:

- Add `validate_task_spec` examples showing `pinned_by` with existing tests/docs/commands and `phase: "post"` only for post-hoc/session-recovery calls.
- Explain how `validate_plan`'s adaptive budget and unverifiable rollup change caller behavior: callers normally omit `max_tokens_override`, retry with a higher override only when truncation guidance says to, and treat the rolled-up `codebase_reference_checklist` as a pre-flight checklist rather than a blocking plan defect.
- Explain `validate_completion` evidence selection: use `final_files` for doc/generated deliverables, `final_diff` for complete code diffs, and expect a prompt warning when the summary mentions `.md`, `.txt`, `.json`, `.yaml`, or `.yml` paths missing from submitted evidence.

## Files touched

```text
Modify  internal/session/session.go               — add PinnedBy and Phase to TaskSpec
Modify  internal/mcpsrv/handlers.go               — new args, validation, adaptive plan budget, normalization hooks, referenced-path detection
Modify  internal/mcpsrv/handlers_test.go          — task-spec args, completion path hints, truncation UX
Modify  internal/mcpsrv/handlers_plan_test.go     — plan rollup and verdict calibration tests
Modify  internal/mcpsrv/summary.go                — ensure rolled-up evidence renders usefully
Modify  internal/prompts/prompts.go               — add PinnedBy/Phase to pre input, referenced-path note to post input
Modify  internal/prompts/templates/pre.tmpl       — render pinned_by and phase guidance
Modify  internal/prompts/templates/post.tmpl      — render pinned_by and referenced-path evidence note
Modify  internal/prompts/prompts_test.go          — anchor assertions
Modify  internal/prompts/testdata/*.golden        — regenerated intentional prompt changes
Modify  README.md                                 — document new args and plan truncation behavior
Modify  INTEGRATION.md                            — caller discipline and companion cadence updates
Modify  CHANGELOG.md                              — add 0.3.3 entry
```

## Testing

Unit tests:

- `ValidateTaskSpec` rejects invalid `phase`.
- `ValidateTaskSpec` trims/drops empty `pinned_by` entries and stores valid entries in the session.
- `ValidateTaskSpec` rejects excessive `pinned_by` entry count or length.
- Pre prompt includes `Pinned by:` when entries are supplied.
- Pre prompt omits post-hoc guidance for default/pre phase.
- Pre prompt includes post-hoc guidance for `phase: post`.
- Post prompt includes `Pinned by:` when entries are supplied.
- `validate_plan` uses adaptive output budget when no override is supplied and task count makes the floor exceed config default.
- `validate_plan` preserves explicit `max_tokens_override` behavior over adaptive budget.
- No-analysis truncation emits `major` finding and self-contained `next_action`.
- Partial-recovery truncation remains `minor`.
- Task-level unverifiable plan findings roll up into one plan-level finding.
- Unverifiable-only plan results calibrate to `plan_verdict: pass` and `plan_quality: actionable`.
- Mixed real findings plus unverifiable findings do not calibrate to pass.
- Referenced doc path missing from completion evidence renders the post prompt note.
- Referenced doc path present in `final_files` or `final_diff` does not render the note.

Golden tests:

- Regenerate pre/post prompt goldens after intentional template changes.
- Add anchor assertions for `Pinned by:` in pre/post prompts, `Phase: post-hoc`, and `summary references these paths`.

Manual verification:

- `go test -race ./...`

No e2e tests are required. All behavior is deterministic with fake reviewers and prompt rendering tests.

## Risks and mitigations

| Risk | Mitigation |
|---|---|
| Rolling up unverifiable claims hides useful per-task context | Rollup evidence lists task numbers and original evidence snippets; task findings still retain real non-unverifiable defects. |
| Calibrating unverifiable-only results to pass creates false confidence | `plan_quality` remains at most `actionable`, and `next_action` still tells callers to pre-flight references. |
| `pinned_by` becomes a way to suppress legitimate ambiguity | Prompt says anchors reduce ambiguity only when related; non-unverifiable contradictions still surface. Server does not auto-suppress task findings. |
| `phase: post` encourages skipping the pre-hook | Docs frame it as recovery only; default remains pre; dispatch clause still requires task-start validation. |
| Adaptive plan token budget increases cost | Ceiling still applies; cost increase is limited to task-count-scaled plans that were likely to truncate otherwise. |
| Referenced-path regex false positives | Render as reviewer context, not a hard rejection; narrow extension list avoids most code-symbol matches. |
| Prompt changes increase golden churn | Anchor assertions plus reviewed golden diffs keep changes intentional. |

## Deferred questions

1. Should adaptive plan budget constants (`2000 + 800*taskCount`) be hard-coded first, or configurable via env vars?
2. Should `pinned_by` live only in `ValidateTaskSpecArgs`, or should `validate_plan` get a future `verified_claims`/task metadata shape?
