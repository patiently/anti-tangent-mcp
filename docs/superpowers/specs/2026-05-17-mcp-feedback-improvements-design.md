# MCP feedback improvements — design

**Status:** draft 2026-05-17
**Target version:** 0.4.0 (minor bump)
**Tracking issue:** [#18](https://github.com/patiently/anti-tangent-mcp/issues/18)

## Background

Two anonymized field reports exercised `anti-tangent-mcp` and the optional CodeScene MCP companion on multi-task implementation branches:

- Report A: seven sequential tasks across production, test, prompt, and compile-check work.
- Report B: five sequential tasks across data helpers, pure guard logic, wiring, harness extensions, and a lightweight data-file landing task.

Both reports concluded that anti-tangent was net positive. It caught load-bearing spec defects, real major ambiguities, and test-design improvements. They also identified repeatable friction: repeated text-only `unverifiable_codebase_claim` findings after a controller has already grep-verified references, protocol overhead on mechanical tasks, and weak adoption of the deterministic CodeScene mid-task check.

This release improves signal density and caller ergonomics without changing the core architecture: anti-tangent remains text-only, advisory, and in-memory; CodeScene remains a companion convention rather than a dependency.

## Scope

In scope:

- Roll up per-task `validate_task_spec` `unverifiable_codebase_claim` findings into one checklist finding.
- Add `controller_verified_references` to `validate_task_spec` so callers can say which codebase references were already grep-verified.
- Add lightweight-task eligibility annotations to `validate_plan` task results.
- Add a short-lived in-memory cache for identical successful `validate_plan` pass results.
- Tune `validate_task_spec` prompt guidance for test-only tasks.
- Document the expected mitigation response when an implementer proceeds after major pre-task findings.
- Strengthen integration guidance for `pinned_by`, pre-flight grep, stale line references, lightweight dispatch, and shorter dispatch clauses.
- Add CodeScene companion guidance for already-degraded files, per-task snapshot ledgers, test-file quietness, recommended mid-task cadence, and pasteable companion envelopes.
- Draft anonymized upstream CodeScene issue bodies from the observed field friction.

Out of scope:

- Persistent storage. The plan cache is process-local and TTL-bound.
- Server-side grep or static analysis. Anti-tangent does not read the caller's codebase.
- Automatic CodeScene invocation from anti-tangent.
- Enforcing CodeScene findings or anti-tangent findings as blocking MCP errors.
- Implementing CodeScene runtime changes in this repository.
- Changing existing `critical|major|minor` severities or adding an `info` severity.

## Design

### 1. Per-task unverifiable rollup

Normalize `validate_task_spec` reviewer output before creating the session and before returning the envelope.

Behavior:

1. Collect findings where `category == unverifiable_codebase_claim`.
2. Remove those findings from the main finding list.
3. Append one replacement finding when any were removed:

   - `severity: minor`
   - `category: unverifiable_codebase_claim`
   - `criterion: codebase_reference_checklist`
   - `evidence`: the original evidence strings joined with `; ` and truncated with the existing `rollupEvidencePerTaskMax = 240` cap.
   - `suggestion`: `Pre-flight these references with grep or codebase-aware review before implementation. If they were already verified, treat this as a checklist rather than a spec-quality defect.`

4. Store the normalized findings in the session's `PreFindings` so completion and summaries see the same compact shape the caller saw.

Verdict handling:

- Keep the reviewer-emitted verdict unchanged for `validate_task_spec`.
- Do not force-pass unverifiable-only per-task specs. A `warn` on pre-task validation is an acceptable nudge that implementation should consciously verify references.

Rationale: plan-level rollup already proved useful, but implementers still saw repeated per-task file:line and symbol claims. This keeps the useful checklist while reducing finding count. The plan-level rollup can force-pass unverifiable-only plans because the controller is doing a dispatchability check; the per-task hook should keep the warning because the implementer is now at the codebase and is the right actor to verify references before editing.

### 2. Controller-verified references

Add an optional `validate_task_spec` input:

```go
type ValidateTaskSpecArgs struct {
    // existing fields...
    ControllerVerifiedReferences []string `json:"controller_verified_references,omitempty"`
}
```

Semantics:

- Entries are caller-supplied attestations that specific paths, symbols, line anchors, commands, or adjacent patterns were checked before dispatch.
- The server does not verify them.
- The reviewer should not emit `unverifiable_codebase_claim` for references covered by the deterministic substring rule below.
- If the reviewer still sees a contradiction or ambiguity, it should emit the appropriate non-unverifiable finding.

Prompt rendering:

- Render a `Controller-verified references:` section in `pre.tmpl` when non-empty.
- State that these entries are trusted only as caller-provided context, not as codebase truth independently known by the reviewer.
- Tell the reviewer to suppress an `unverifiable_codebase_claim` for claim C only when some entry in `controller_verified_references` is a substring of C, or C is a substring of some entry. Do not suppress logical contradictions, missing ACs, or ambiguity findings.

Validation:

- Trim whitespace and drop empty entries.
- Apply the same defensive limits as `pinned_by`: at most `maxPinnedByEntries = 50` non-empty entries and at most `maxPinnedByChars = 500` Unicode code points per entry.

Relationship to `pinned_by`:

- Use `pinned_by` when reverting or breaking the behavior would make a named test, command, document, or static check fail.
- Use `controller_verified_references` when the controller grep-verified a codebase fact that appears in the spec.
- A single path can appear in both if it is both an anchor and a verified reference.

This broader field replaces a narrower `plan_reference_checklist_acknowledged` flag. It covers both validate-plan checklist acknowledgment and ad hoc controller greps.

### 3. Lightweight eligibility annotations

Extend `PlanTaskResult` with optional task metadata:

```go
type PlanTaskResult struct {
    // existing fields...
    LightweightEligible bool   `json:"lightweight_eligible,omitempty"`
    LightweightReason   string `json:"lightweight_reason,omitempty"`
}
```

Reviewer prompt criteria:

- Mark `lightweight_eligible: true` only when all are true:
  - The task touches at most two files, or is docs/config/data-only.
  - The task is mechanical and has no production-design or test-design choices.
  - The plan includes literal text, exact diff, exact command, or exact insertion shape.
- Keep `lightweight_eligible: false` when the task introduces new production logic, requires scenario/test harness design choices, or depends on nuanced state transitions.
- `lightweight_reason` should be a short controller-facing explanation.

Backward compatibility:

- Older reviewer JSON without these fields still parses.
- `omitempty` keeps responses compact when false/empty.
- Controllers may ignore the fields.

Schema updates:

- Add both fields to `internal/verdict/plan_schema.json` and `internal/verdict/tasks_only_schema.json` task item `properties`.
- Do not add either field to the task item `required` lists.
- Keep `additionalProperties: false`; otherwise provider structured-output validation will reject reviewer JSON that includes the new fields.
- Update `PlanTaskResult` with matching optional Go fields.

Integration behavior:

- If a task is marked lightweight-eligible, controllers may dispatch it under lightweight mode: skip `validate_task_spec` and `check_progress`, still require `validate_completion` with evidence.
- The annotation is advisory. The controller may override it based on local risk.
- Require a non-empty `lightweight_reason` whenever `lightweight_eligible` is true; false/empty remains the default.

### 4. In-memory `validate_plan` pass cache

Add a process-local cache for identical successful `validate_plan` calls.

Cache key:

- canonical hash of `plan_text`
- `mode`
- effective reviewer model
- effective max-token budget
- hash of the rendered prompt content (`prompts.Output.System` plus `prompts.Output.User`)
- schema/cache version string as a defense-in-depth invalidator

Cache value:

- normalized `PlanResult`
- `model_used`
- expiry time

Behavior:

- Cache only results whose final normalized `plan_verdict == pass`.
- TTL: 3 minutes.
- Cache hit returns the cached result with `review_ms: 0`.
- Preserve the reviewer-emitted `next_action` by prepending a visible cache marker, for example: `[cached <=3m] <original next_action>`.
- Do not cache errors, truncation, `warn`, or `fail` results.
- Do not persist across process restarts.

Rationale: repeated validation of an unchanged already-passing plan burns 30-60 seconds and one LLM call without adding signal. The repeated call usually happens within seconds or a couple of minutes, so a 3-minute TTL catches the common waste while reducing stale-result risk during active prompt or plan iteration. Including rendered prompt content in the key means edits to `plan.tmpl`, `plan_findings_only.tmpl`, or `plan_tasks_chunk.tmpl` naturally miss the cache without relying only on a manual version bump.

### 5. Test-only task prompt tuning

Update `pre.tmpl` so the reviewer handles test-only tasks more precisely.

Guidance:

- If the task is explicitly test-only, treat `null`, `unchanged`, or similar assertions as acceptable when the spec names the initial state, fixture setup, or existing test harness assumption.
- Still flag missing invocation counts, missing negative assertions, unclear scenario setup, or unclear path through the system.
- Prefer one consolidated finding when the same ambiguity repeats across multiple scenarios.

Rationale: the first field report showed a useful test-design catch around invocation counts, but also repeated lower-value findings for each `null/unchanged` scenario. The tuning should preserve the former and reduce the latter.

### 6. Major pre-finding mitigation in DONE reports

Do not add a new tool. Add integration guidance:

- If `validate_task_spec` returns a `major` finding and the implementer proceeds, the DONE report must include a one-sentence mitigation for each major pre-finding.
- The mitigation should say whether the spec was clarified, the plan body resolved it, codebase inspection resolved it, or the finding was intentionally accepted.
- `validate_completion` remains the required post-implementation gate.

Post-hook requirement:

- Thread prior `major` pre-findings from the session into `post.tmpl` in a compact `Major pre-task findings to verify:` section.
- Instruct the reviewer to check whether the summary, final evidence, or test evidence explicitly mitigates each major pre-finding.
- If a major pre-finding appears unresolved and could affect AC completion, the reviewer should emit a completion finding mapped to the relevant AC or to `spec`.

Rationale: this creates a lightweight structured answer to “what was your mitigation plan?” without increasing MCP surface area.

### 7. Integration docs updates

Update `README.md`, `INTEGRATION.md`, and lightweight dispatch examples.

Anti-tangent guidance:

- Explain per-task `unverifiable_codebase_claim` rollup.
- Explain `controller_verified_references` with examples.
- Strengthen `pinned_by` rule of thumb: use it when a named test, command, static check, or document pins behavior; use `context` for background; using both is acceptable.
- Prefer stable symbols and search anchors over stale file:line ranges after earlier tasks modify the same file.
- Explain `lightweight_eligible` and how controllers can override it.
- Shorten the dispatch clause by moving verbose protocol details into the skill/template docs and leaving task-specific fields plus mandatory gates in the prompt.
- Add one-line retry examples for truncation findings using `max_tokens_override` before asking callers to reason through env vars.

Short dispatch target shape:

```markdown
## Drift protection

Use anti-tangent per the standard dispatch protocol. For this task:
- Call `validate_task_spec` before edits unless `lightweight_eligible: true` is explicitly set by the controller.
- Call `validate_completion` before DONE and paste its `summary_block`.
- If CodeScene MCP is configured, run `pre_commit_code_health_safeguard` after meaningful code changes.
- If any major pre-task finding is accepted rather than fixed, include a one-sentence mitigation in DONE.

Task spec fields for validate_task_spec:
...
```

### 8. CodeScene companion guidance

Anti-tangent docs should describe CodeScene as recommended when configured, while keeping anti-tangent itself independent.

Guidance changes:

- Keep anti-tangent `check_progress` optional.
- Make CodeScene `pre_commit_code_health_safeguard` recommended after meaningful code changes when CodeScene MCP is configured.
- Add a concrete example invocation pattern and expected result shape.
- For already-degraded files, compare each finding's `value` vs `value_before`; do not treat the top-level `failed` gate as equally meaningful for every task.
- For multi-task work on one hotspot file, run `analyze_change_set` after each task and record a short ledger row: task, commit or checkpoint, file, finding, value_before, value, threshold.
- For test-only tasks, CodeScene is usually a cheap sanity check; do not suppress it solely because the file is a test unless the project has known test-metric noise.

Companion envelope recommendation:

- Document a desired CodeScene `summary_block` analog: tool name, base/ref or files, quality gate, findings count, key deltas, next action.
- Treat this as upstream feedback for CodeScene, not an anti-tangent runtime feature.

### 9. CodeScene upstream issue drafts

Add anonymized drafts under `docs/feedback/codescene/`:

1. Already-degraded file verdicts should foreground per-finding delta over top-level `failed`.
2. Per-task attribution helper or ledger for branch-local multi-task work.
3. Pasteable companion envelope for DONE reports.
4. Document expected quiet behavior for test-heavy files, or clarify how test files are classified.

Public-repo hygiene:

- Strip private ticket IDs, product names, internal file/class/function names, and internal SHAs.
- Preserve the shape of the evidence and public tool names.
- Do not include consumer code snippets.

## Testing Strategy

- Unit test `validate_task_spec` per-task rollup, including session-stored `PreFindings`.
- Unit test that non-unverifiable findings are preserved and verdict is not force-passed.
- Unit/golden test `controller_verified_references` rendering and input normalization.
- Schema/parser test that `PlanTaskResult` accepts old JSON without lightweight fields and new JSON with fields.
- Unit test lightweight eligibility fields survive `validate_plan` response parsing and summary formatting, including schema tests for `plan_schema.json` and `tasks_only_schema.json`.
- Unit test plan cache hit, miss by changed plan text, miss by changed mode/model/budget/rendered prompt, expiry-after-TTL miss, `review_ms: 0` on hit, and non-caching of warn/fail.
- Unit/golden test `controller_verified_references` whitespace and empty-entry normalization using the same 50-entry / 500-character limits as `pinned_by`.
- Golden test test-only prompt guidance. Treat the “prefer one consolidated finding” behavior as field-verified reviewer guidance rather than a deterministic unit-test assertion.
- Documentation review for dispatch-clause examples and CodeScene issue drafts.
- Run `go test -race ./...`.

## Rollout

- Ship as `0.4.0` because optional `ValidateTaskSpecArgs` fields and optional `PlanTaskResult` fields are new feature surface even though they are backward-compatible.
- Update `CHANGELOG.md` while implementing.
- Keep all new fields optional.
- Keep existing lightweight mode behavior unchanged; the new plan annotation only helps controllers choose it.

## Risks

- `controller_verified_references` could suppress a useful reminder when the controller verification was stale or wrong. Mitigation: docs must say it is caller-supplied context, not proof, and reviewers should still flag contradictions.
- Lightweight eligibility is heuristic. A false `lightweight_eligible: true` can cause the controller to skip `validate_task_spec` and `check_progress` on a task that needed them. Mitigation: default false, require non-empty `lightweight_reason` when true, require all three prompt criteria to hold, and keep controller override explicit.
- Plan pass cache could hide changed reviewer behavior during rapid prompt iteration. Mitigation: short TTL and schema/cache version in the key.
- CodeScene guidance could be read as anti-tangent owning CodeScene behavior. Mitigation: separate companion guidance from upstream issue drafts and state that anti-tangent never calls CodeScene itself.
