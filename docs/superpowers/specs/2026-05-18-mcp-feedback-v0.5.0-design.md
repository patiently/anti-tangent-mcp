# MCP feedback improvements (v0.5.0) — design

**Status:** draft 2026-05-18
**Target version:** 0.5.0 (minor bump — all additive)
**Predecessor spec:** [`2026-05-17-mcp-feedback-improvements-design.md`](2026-05-17-mcp-feedback-improvements-design.md) shipped as 0.4.0.

## Background

A fresh anonymized field report exercised `anti-tangent-mcp` v0.4.0 across a multi-task implementation branch (nine task dispatches plus five fix iterations plus one final whole-branch review). The 0.4.0 controller-side ergonomics (`controller_verified_references`, lightweight eligibility hints, plan cache) landed well and reduced repeat noise. New, still-recurring friction surfaced once those were in place:

- **Cross-task contract gaps.** A task that emits a symbol downstream tasks consume can silently ship a value mismatch (e.g. a missing `handlerName` constant). The per-task `validate_completion` reviewer cannot see downstream consumer expectations from text alone.
- **Codebase-convention deviation.** Type and identifier conventions used elsewhere in the module (e.g. `UUID` vs `String`) are routinely caught only by a downstream code-quality reviewer that greps the broader codebase; anti-tangent is text-only and structurally blind.
- **Test-strategy false positives.** Plans that deliberately split AC coverage across multiple complementary tests (e.g. "representative phrase subset" + "marker-string presence") get repeat `missing_acceptance_criterion` findings on test breadth.
- **Testability extractions.** Helpers extracted into a file purely so tests can call them directly are flagged as `scope_drift`; the pattern is a recurring intentional choice.
- **Normative test bodies.** When a plan task pastes verbatim test bodies expected to land as written, the reviewer routinely flags "test scope unclear" because the prose AC is read in isolation from the per-step code block.
- **`.trimIndent()` source-wrap rendering.** A multi-line plan snippet bound by `.trimIndent()` renders mid-phrase newlines at runtime; no MCP catches this.
- **Language-specific scoping prose.** Closure/scoping semantics (Kotlin `var` + lambda) routinely surface `ambiguous_spec` findings when the underlying code block is unambiguous.
- **Dispatch-clause lightweight-mode visibility.** Some implementers ran the full protocol on tasks that qualified for lightweight mode; the eligibility criteria sit below the full-protocol steps and are easy to skip.

This release improves signal density and adds three new optional `validate_task_spec` fields plus a cross-task `exit_contracts` flow without changing core architecture. Anti-tangent remains text-only, advisory, and in-memory; CodeScene remains a companion convention rather than a dependency.

## Scope

In scope:

- Three new optional `validate_task_spec` inputs: `test_strategy_notes`, `codebase_conventions`, `testability_extractions`.
- New finding category `convention_deviation` (minor-floored) for `codebase_conventions`.
- `validate_plan` task results include a new optional field `exit_contracts` (hybrid: explicit if the task has an `**Exit contracts:**` section, reviewer-inferred otherwise).
- `validate_completion` accepts an optional `exit_contracts` input; reviewer assesses each against `final_files` / `final_diff`, emitting `missing_acceptance_criterion` with `criterion: exit_contract` on miss.
- Prompt-template tunes paired with the new fields and with the doc-only items below.
- `INTEGRATION.md` doc additions: normative-test-bodies convention, CVR scope clarification, `.trimIndent()` raw-string caveat, language-scoping prose caveat, lightweight-mode callout repositioning.
- CHANGELOG entry for 0.5.0.

Out of scope:

- Persistent storage. All new state remains in-memory or in caller-passed inputs.
- Server-side grep or static analysis. Anti-tangent does not read the caller's codebase.
- Plan-side auto-threading of `exit_contracts` to `validate_completion` (no shared store between the stateless plan hook and the stateful per-task session). The controller still carries the strings.
- New severities. `convention_deviation` is floored to `minor`, matching the `unverifiable_codebase_claim` pattern.
- CodeScene-side fixes (parallel-prompt test duplication false-positives, `quality_gates: failed` severity discriminator). Those go to `codescene/codescene-mcp-server` separately.
- `refactor_impact_set` (the field reporter flagged this as not worth the engineering cost).
- Plan-side `Exit contracts:` syntax becoming mandatory. Old plans continue to work; reviewer infers.

## Design

### 1. New optional `validate_task_spec` inputs

Mirrors the v0.4.0 `controller_verified_references` shape: optional `[]string`, defensive limits, prompt rendering when non-empty, deterministic substring suppression where applicable.

```go
type ValidateTaskSpecArgs struct {
    // existing fields...
    TestStrategyNotes      []string `json:"test_strategy_notes,omitempty"`
    CodebaseConventions    []string `json:"codebase_conventions,omitempty"`
    TestabilityExtractions []string `json:"testability_extractions,omitempty"`
}
```

Each field is normalized identically to `pinned_by` and `controller_verified_references`:
- Trim whitespace, drop empty entries.
- Cap at `maxPinnedByEntries = 50` non-empty entries.
- Cap each entry at `maxPinnedByChars = 500` Unicode code points.

#### 1a. `test_strategy_notes`

Semantics: caller attestations that AC test coverage is intentionally split across multiple complementary tests.

Examples:
- `"AC #2 is jointly covered by tests A (representative phrase subset) and B (marker-string presence). Treat as joint coverage."`
- `"AC #3's negative case is split: test X asserts the predicate is not flipped; test Y asserts the downstream effect did not fire."`

Prompt rendering: `pre.tmpl` emits a `Test strategy notes (caller-supplied):` section when non-empty.

Reviewer guidance:
- Treat each entry as authoritative caller context about why test coverage is split.
- Do not emit `missing_acceptance_criterion` for joint-coverage gaps when an entry explains the split that covers the gap.
- Continue to emit `missing_acceptance_criterion` when the gap is unrelated (e.g. an AC bullet with zero tests at all, or an invocation-count assertion that is genuinely missing).

#### 1b. `codebase_conventions`

Semantics: caller-supplied "X is canonically Y in this module" conventions. **Inverts** the suppression pattern — entries actively *trigger* findings on observed deviation rather than silencing existing findings.

Examples:
- `"truckerAdId is canonically UUID in memory; serialise to String only at the persistence boundary."`
- `"Instant fields use @Serializable(with = InstantSerializer::class) — see PendingPivotOffer.kt:54."`
- `"Tests in this module key on named arguments, not positional, when the call site has 3+ params."`

Prompt rendering: `pre.tmpl` emits a `Codebase conventions (caller-supplied):` section when non-empty.

Reviewer guidance:
- Treat each entry as a convention that the implementation must follow.
- When the task spec or surrounding context implies the implementation will deviate, emit a new finding:
  - `severity: minor` (floored)
  - `category: convention_deviation`
  - `criterion: codebase_convention`
  - `evidence`: short quote of the spec text or rationale that suggests deviation.
  - `suggestion`: re-state the convention and ask the implementer to confirm or document an exception.
- Do not emit `convention_deviation` when the spec is silent on the convention — only when there is positive evidence of deviation in the spec text.

#### 1c. `testability_extractions`

Semantics: caller attestations that named helpers exist in production code purely so tests can call them directly (not scope creep).

Examples:
- `"buildDeclineWinddownHandlerOutput is a top-level helper for testability, not a scope-drift target."`
- `"runHiringAreaRecheck closure can be hoisted to a private file-local function so the test fixture can spy on it."`

Prompt rendering: `pre.tmpl` emits a `Testability extractions (caller-supplied):` section when non-empty.

Reviewer guidance:
- When a `scope_drift` finding would name one of these helpers (deterministic substring match: entry is substring of evidence or evidence is substring of entry), suppress that specific finding.
- Continue to emit `scope_drift` for unrelated additions.

Schema and parser updates:
- Add all three fields to `internal/verdict/*.json` task-input schemas (input-side; no envelope schema change).
- `convention_deviation` added to the canonical category list in `internal/verdict/finding.go`.
- Server-side severity flooring for `convention_deviation` matches the existing flooring of `unverifiable_codebase_claim`.

### 2. Exit contracts — `validate_plan` and `validate_completion`

#### 2a. Plan side (`validate_plan`)

Extend `PlanTaskResult` with one optional field:

```go
type PlanTaskResult struct {
    // existing fields including LightweightEligible / LightweightReason ...
    ExitContracts []string `json:"exit_contracts,omitempty"`
}
```

Hybrid authoring:

- **Explicit:** if a task's markdown contains an `**Exit contracts:**` bullet section, the reviewer respects it verbatim. Format mirrors `Acceptance criteria:` — a bulleted list of plain-English contract strings.
- **Inferred:** if absent, the reviewer infers `exit_contracts` by reading the plan as a whole. Inference rule: "for each task, list symbols, types, constants, or fields it introduces that later tasks in the plan explicitly reference. One contract per consumed surface."

Defensive limits (reviewer-emitted; same shape as evidence caps elsewhere):
- At most 20 contracts per task.
- At most 240 Unicode code points per contract.
- Truncate with the existing `…` suffix used for evidence rollups.

Prompt rendering: `plan_tasks_chunk.tmpl` and `plan.tmpl` extended to ask the reviewer to populate `exit_contracts`. Falsy/empty allowed.

Schema updates:
- Add `exit_contracts` to `internal/verdict/plan_schema.json` and `internal/verdict/tasks_only_schema.json` task item `properties` (not `required`).
- Keep `additionalProperties: false`.

Cache compatibility: the v0.4.0 identical-plan cache keys already incorporate the rendered prompt content, so adding `exit_contracts` to the prompt naturally invalidates pre-0.5.0 cached entries. No explicit version bump needed in the cache key.

#### 2b. Completion side (`validate_completion`)

Add one optional input:

```go
type ValidateCompletionArgs struct {
    // existing fields...
    ExitContracts []string `json:"exit_contracts,omitempty"`
}
```

Same normalization as the `validate_task_spec` fields above (trim, drop empties, 50/500 caps).

Prompt rendering: `post.tmpl` emits an `Exit contracts (must be satisfied):` section when non-empty, listing each contract.

Reviewer guidance:
- For each contract, assess whether `final_files` / `final_diff` evidence satisfies it.
- On miss, emit a finding:
  - `severity` matches the reviewer's judgement (`major` is appropriate for a hard mismatch; `minor` for "looks present but not verifiable from this evidence").
  - `category: missing_acceptance_criterion` (reuse — no new category).
  - `criterion: exit_contract`.
  - `evidence`: quote the contract and the closest matching production-code surface.
  - `suggestion`: name the specific edit that would satisfy the contract.

Threading model: the controller is responsible for threading per-task `exit_contracts` from the `validate_plan` result into each dispatched implementer's prompt. The implementer then passes them into `validate_completion`. Anti-tangent's plan and per-task hooks remain decoupled (no shared store).

### 3. Doc-only fixes (D1–D5)

All land in `INTEGRATION.md`. Patrick's `~/.claude/anti-tangent.md` is downstream of `INTEGRATION.md` per the existing mirror policy.

- **D1. Normative test bodies.** New section documenting the `**NORMATIVE TEST BODIES (verbatim):**` header convention for plan tasks that paste literal test code blocks. Paired with a `pre.tmpl` prompt tune that treats this header as binding AC, not advisory illustration.
- **D2. CVR scope clarification.** Expand the `controller_verified_references` section to state explicitly: substring suppression applies only to `unverifiable_codebase_claim` findings. AC ambiguity, scope drift, missing criteria, and `convention_deviation` are NOT suppressed by CVR entries. Field reports indicated this was a reasonable expectation that the docs did not pre-empt.
- **D3. `.trimIndent()` raw-string caveat.** New section warning plan authors that multi-line plan snippets intended for `.trimIndent()` evaluation must keep example phrases on a single source line. Mid-phrase wraps render as newlines at runtime; no MCP catches this. Suggest test assertions on the *rendered* string, not the source layout.
- **D4. Language-scoping prose caveat.** New section noting that the text-only reviewer may surface `ambiguous_spec` findings around closure/scoping semantics (Kotlin `var` + lambda, Python nested-scope, etc.). Implementer mitigation: trust the verbatim plan code block over the prose, paste-then-verify rather than re-interpret.
- **D5. Lightweight-mode visibility.** Restructure the dispatch clause so the lightweight-eligibility criteria are a callout at the *top* of the clause, before the full-protocol steps. Implementers reading the clause encounter lightweight first; full protocol is the fallback. Field reports indicated implementers occasionally ran the full protocol on tasks that qualified for lightweight, costing 60s+ of LLM time per dispatch.

### 4. Prompt-template tunes (no API change)

- `pre.tmpl`:
  - Recognize the `NORMATIVE TEST BODIES (verbatim):` header as binding AC (pairs with D1).
  - Joint-coverage heuristic: when two adjacent test stubs cover the same AC bullet, treat them as joint coverage unless the gap is structurally obvious (e.g. missing invocation count). `test_strategy_notes` still wins when present (pairs with F1).
  - When `codebase_conventions` non-empty, emit `convention_deviation` findings on observed deviations (pairs with F2).
  - When `testability_extractions` non-empty, suppress matching `scope_drift` findings via deterministic substring match (pairs with F3).
- `post.tmpl`:
  - When `exit_contracts` non-empty, walk each and emit `missing_acceptance_criterion` with `criterion: exit_contract` on miss (pairs with 2b).
- `plan.tmpl` and `plan_tasks_chunk.tmpl`:
  - Ask the reviewer to populate `exit_contracts` per task; respect explicit `**Exit contracts:**` sections when present.
  - Cap at 20 contracts and 240 code points per contract (pairs with 2a).

All template changes regenerate golden files in `internal/prompts/testdata/`.

### 5. CHANGELOG

Add to `CHANGELOG.md`:

```markdown
## [0.5.0] - 2026-05-XX

### Added
- `validate_task_spec` accepts optional `test_strategy_notes`, `codebase_conventions`, and `testability_extractions` so controllers can surface joint-coverage intent, module conventions, and intentional testability extractions.
- New finding category `convention_deviation` (minor-floored) emitted when a `codebase_conventions` entry conflicts with the spec.
- `validate_plan` task results include optional `exit_contracts` (hybrid: explicit `**Exit contracts:**` section if present, reviewer-inferred otherwise) so controllers can thread cross-task surface contracts into per-task completion gates.
- `validate_completion` accepts optional `exit_contracts`; reviewer flags misses as `missing_acceptance_criterion` with `criterion: exit_contract`.

### Changed
- `pre.tmpl` recognizes `**NORMATIVE TEST BODIES (verbatim):**` headers as binding AC, treats adjacent complementary tests as joint coverage, and respects the three new caller-supplied fields above.
- `post.tmpl` checks `exit_contracts` against final-file evidence when present.
- `plan.tmpl` and `plan_tasks_chunk.tmpl` populate `exit_contracts` per task.

### Documentation
- Integration docs add the normative-test-bodies convention, CVR-scope clarification, `.trimIndent()` raw-string caveat, language-scoping prose caveat, and a lightweight-mode callout repositioning at the top of the dispatch clause.
```

## Testing Strategy

- Unit-test input normalization (trim, drop empties, 50/500 caps) for the three new `validate_task_spec` fields, mirroring existing `pinned_by` and `controller_verified_references` tests.
- Unit-test that `convention_deviation` is server-side floored to `minor` and parses through `internal/verdict/finding.go`.
- Schema test that `internal/verdict/*.json` task-input schemas accept the new fields and reject unknown ones (`additionalProperties: false`).
- Golden test `pre.tmpl` rendering for each new field independently and in combination, including the normative-test-bodies header.
- Golden test `post.tmpl` rendering with `exit_contracts` non-empty.
- Golden test `plan.tmpl` and `plan_tasks_chunk.tmpl` rendering with the `exit_contracts` instruction block.
- Schema test that `plan_schema.json` and `tasks_only_schema.json` accept old JSON without `exit_contracts` and new JSON with the field.
- Unit-test parser round-trip for `PlanTaskResult.ExitContracts` and `Result` envelope with `criterion: exit_contract` findings.
- Reviewer-behavior tests stay golden + httptest-based per project convention; deterministic behaviors (suppression, flooring) get unit assertions, reviewer-shaped behaviors (when to emit `convention_deviation`) get golden prompts and one E2E coverage test behind `-tags=e2e`.
- Run `go test -race ./...`.

## Rollout

- Branch: `version/0.5.0` per project convention.
- `CHANGELOG.md` updated as work progresses; merge commit carries `[minor]`.
- `INTEGRATION.md` updated in the same release.
- Patrick mirrors `INTEGRATION.md` changes into `~/.claude/anti-tangent.md` after merge (downstream of this repo per existing memory policy).
- No upstream coordination required — all changes are additive and backward-compatible. Old controllers omit the new fields; old plans omit the explicit `**Exit contracts:**` section and the reviewer infers.

## Open questions

(None at spec time. Resolve during implementation planning.)
