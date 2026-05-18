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

This release improves signal density and adds four new optional `validate_task_spec` fields plus a cross-task `exit_contracts` flow without changing core architecture. Anti-tangent remains text-only, advisory, and in-memory; CodeScene remains a companion convention rather than a dependency.

## Scope

In scope:

- Four new optional `validate_task_spec` inputs: `test_strategy_notes`, `codebase_conventions`, `testability_extractions`, and `normative_test_bodies`.
- New finding category `convention_deviation` (minor-floored) for `codebase_conventions`. Added to the reviewer-output JSON schema category enums in `internal/verdict/schema.json`, `plan_schema.json`, `tasks_only_schema.json`, and `plan_findings_only_schema.json`.
- `validate_plan` task results include two new optional fields per task: `exit_contracts` (hybrid: explicit if the task has an `**Exit contracts:**` section, reviewer-inferred otherwise) and `normative_test_bodies` (extracted from `**NORMATIVE TEST BODIES (verbatim):**` sections in the plan markdown). Plus a sibling `exit_contracts_inferred bool` provenance flag per task.
- `validate_completion` accepts optional `exit_contracts []string` plus a sibling `exit_contracts_inferred bool` provenance flag; reviewer assesses each contract against `final_files` / `final_diff`, with severity-on-miss calibrated by provenance (explicit → reviewer may emit `major`; inferred-only → cap at `minor` unless evidence is structurally inconsistent).
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

Four optional `[]string` inputs. All share normalization shape; behavior varies per field.

```go
type ValidateTaskSpecArgs struct {
    // existing fields...
    TestStrategyNotes      []string `json:"test_strategy_notes,omitempty"`
    CodebaseConventions    []string `json:"codebase_conventions,omitempty"`
    TestabilityExtractions []string `json:"testability_extractions,omitempty"`
    NormativeTestBodies    []string `json:"normative_test_bodies,omitempty"`
}
```

**Normalization shape:**
- All four fields: trim whitespace, drop empty entries.
- `test_strategy_notes`, `codebase_conventions`, `testability_extractions`: cap at `maxPinnedByEntries = 50` non-empty entries and `maxPinnedByChars = 500` Unicode code points per entry (same shape as `pinned_by` and `controller_verified_references`).
- `normative_test_bodies`: cap at `maxNormativeTestBodyEntries = 20` entries and `maxNormativeTestBodyChars = 4000` Unicode code points per entry. The larger per-entry cap reflects that test bodies are code (longer than the prose attestations in the other three fields) while keeping the per-field worst case under 80KB — well inside the 200KB payload limit. Paraphrased or excerpted bodies are acceptable when even 4000 characters would truncate a real test; in that case, prefix the entry with `// excerpt:` to signal partial coverage to the reviewer (see §1d below).

**Behavior (varies):**
- `test_strategy_notes` — reviewer guidance. The reviewer reads each note as authoritative caller context and adjusts judgment about test-coverage gaps accordingly. No deterministic suppression; suppression is reviewer-driven.
- `codebase_conventions` — **inverts** the pattern: actively triggers `convention_deviation` findings when the spec implies the implementation will deviate. Not a suppression mechanism.
- `testability_extractions` — deterministic substring suppression of `scope_drift` findings whose evidence names one of the listed helpers (same substring rule as `controller_verified_references`).
- `normative_test_bodies` — reviewer guidance. Rendered as a "Normative test bodies (caller-supplied, treat as binding AC):" section. The reviewer reads the bodies as binding test scope, not advisory illustration. Necessary because `validate_task_spec` otherwise receives only structured fields (Goal/AC/Non-goals/Context) and never sees the plan's per-step code blocks.

**Input validation:** these inputs are validated in Go (`internal/mcpsrv/validate_task_spec.go` and friends), not in JSON schema files. The `internal/verdict/*.json` schemas are reviewer-**output** schemas (structured-output validation), not MCP tool-input schemas — see §1d below for the reviewer-output schema updates.

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

#### 1d. `normative_test_bodies`

Semantics: caller-supplied verbatim test code blocks that the plan treats as binding AC, not advisory illustration. Necessary because `validate_task_spec` does not receive the plan's per-step code blocks — only the structured Goal/AC/Non-goals/Context fields.

Each entry is one normative block: a complete test body when it fits in the per-entry cap, an excerpt prefixed with `// excerpt:` (or language-appropriate comment) otherwise. Multi-test blocks may be concatenated into one entry with internal newlines.

Examples:
- A complete Kotlin test body: `"@Test fun whenInputIsX_thenOutputIsY() { ... }"` (full source up to 4000 chars).
- An excerpt of a longer body: `"// excerpt: see plan §3 test 2 for full body. Key assertions: assertThat(result.decision).isEqualTo(DECLINE); assertThat(result.handlerName).isEqualTo(WINDDOWN_NODE_NAME)"`.
- A Python test: `"def test_decline_phrase_subset(): assert phrase in OUTPUT"`.

Prompt rendering: `pre.tmpl` emits a `Normative test bodies (caller-supplied, treat as binding AC):` section when non-empty. Excerpt-marker entries are rendered as-is; the reviewer learns from the marker convention that the entry is partial coverage of a longer body.

Reviewer guidance:
- Treat each entry as binding test scope — equivalent in authority to a bullet under Acceptance criteria.
- Do not emit `ambiguous_spec` for "test scope unclear" findings about coverage that one of these bodies already pins.
- An entry that begins with `// excerpt:` (or analogous comment marker) is partial — flag invocation-count or assertion-shape gaps only when the excerpt itself reveals them, not when the gap might exist in the omitted portion.
- Continue to flag invocation-count gaps, missing negative assertions, or unrelated coverage holes when the entry is a complete body.

Sourcing convention:
- Plans use `**NORMATIVE TEST BODIES (verbatim):**` as a header above the relevant code block(s) in the task markdown.
- `validate_plan` extracts those blocks deterministically (server-side markdown parsing — not reviewer-driven; see §2c) and populates `PlanTaskResult.NormativeTestBodies`. Controllers thread that field into `validate_task_spec` when dispatching.
- Controllers without a prior `validate_plan` call may populate `normative_test_bodies` manually by reading the task markdown.

#### 1e. Reviewer-output schema updates

The new finding category `convention_deviation` must be added to the reviewer-output JSON schema category enums so providers using structured output do not reject the very category the prompt asks for:

- `internal/verdict/schema.json` — per-task envelope; add to the `category` enum alongside `unverifiable_codebase_claim`, `missing_acceptance_criterion`, etc.
- `internal/verdict/plan_schema.json` — same.
- `internal/verdict/tasks_only_schema.json` — same.
- `internal/verdict/plan_findings_only_schema.json` — same.

Other schema/parser updates:
- `internal/verdict/finding.go` — add `convention_deviation` to the canonical category list.
- Server-side severity flooring for `convention_deviation` matches the existing flooring of `unverifiable_codebase_claim` (minor floor in the parser/normalizer path).
- `malformed_evidence` and other server-only categories continue to be omitted from the reviewer-output schemas, per existing convention.

### 2. Exit contracts — `validate_plan` and `validate_completion`

#### 2a. Plan side (`validate_plan`)

Extend `PlanTaskResult` with three new optional fields:

```go
type PlanTaskResult struct {
    // existing fields including LightweightEligible / LightweightReason ...
    ExitContracts         []string `json:"exit_contracts,omitempty"`
    ExitContractsInferred bool     `json:"exit_contracts_inferred,omitempty"`
    NormativeTestBodies   []string `json:"normative_test_bodies,omitempty"`
}
```

Hybrid authoring for `exit_contracts`:

- **Explicit:** if a task's markdown contains an `**Exit contracts:**` bullet section, the reviewer respects it verbatim and emits `exit_contracts_inferred: false`. Format mirrors `Acceptance criteria:` — a bulleted list of plain-English contract strings.
- **Inferred:** if absent, the reviewer infers `exit_contracts` by reading the plan as a whole and emits `exit_contracts_inferred: true`. Inference rule: "for each task, list symbols, types, constants, or fields it introduces that later tasks in the plan explicitly reference. One contract per consumed surface."
- The provenance flag carries forward to `validate_completion` (see §2b) so the reviewer can calibrate miss severity — model-inferred contracts should not become hard completion gates without controller acknowledgement.

`NormativeTestBodies` extraction (server-deterministic — *not* reviewer-driven):

- After the controller-supplied `plan_text` is split into per-task chunks (existing logic), the server scans each task's markdown for the literal `**NORMATIVE TEST BODIES (verbatim):**` header.
- For each header found, the server extracts the immediately-following fenced code block(s). A fence is opened by ```` ``` ```` (optionally with a language tag) and closed by ```` ``` ```` on its own line; everything between is one entry.
- Adjacent fenced blocks (separated only by whitespace) are extracted as separate entries, in source order, until the next non-whitespace non-fence content.
- If the header is followed by a non-fenced paragraph, the paragraph is one entry up to a blank line.
- Apply `maxNormativeTestBodyEntries = 20` and `maxNormativeTestBodyChars = 4000` per entry. If an entry exceeds the char cap, truncate to `(maxNormativeTestBodyChars - len("\n// truncated"))` and append the literal `\n// truncated` marker so the reviewer sees that the body was clipped server-side.
- The extracted list is set on `PlanTaskResult.NormativeTestBodies` before the reviewer sees the prompt. The reviewer does not extract anything for this field; it only reads what the server populated.
- Empty list when no such header exists; the field is `omitempty`.
- Rationale: verbatim extraction must be exact. LLMs can paraphrase, re-indent, or skip blank lines. Server-side markdown parsing is deterministic and matches the v0.4.0 plan-text chunking precedent (also server-side).

Defensive limits (reviewer-emitted; same shape as evidence caps elsewhere):
- At most 20 contracts per task.
- At most 240 Unicode code points per contract.
- Truncate with the existing `…` suffix used for evidence rollups.

Prompt rendering: `plan_tasks_chunk.tmpl` and `plan.tmpl` extended to ask the reviewer to populate `exit_contracts`. Falsy/empty allowed.

Schema updates:
- Add `exit_contracts`, `exit_contracts_inferred`, and `normative_test_bodies` to `internal/verdict/plan_schema.json` and `internal/verdict/tasks_only_schema.json` task item `properties` (not `required`).
- Keep `additionalProperties: false`.
- The category-enum update for `convention_deviation` (see §1e) applies to these schemas too — `convention_deviation` is added to the `findings[].category` enum across all four reviewer-output schemas.

Cache compatibility: the v0.4.0 identical-plan cache keys already incorporate the rendered prompt content, so adding the new instruction blocks to the prompt naturally invalidates pre-0.5.0 cached entries. No explicit version bump needed in the cache key.

#### 2b. Completion side (`validate_completion`)

Add two optional inputs:

```go
type ValidateCompletionArgs struct {
    // existing fields...
    ExitContracts         []string `json:"exit_contracts,omitempty"`
    ExitContractsInferred bool     `json:"exit_contracts_inferred,omitempty"`
}
```

Same normalization as the `validate_task_spec` fields above (trim, drop empties, 50/500 caps).

Prompt rendering: `post.tmpl` emits an `Exit contracts (must be satisfied):` section when `ExitContracts` is non-empty. The section header explicitly states the provenance:
- When `ExitContractsInferred == false`: header reads `Exit contracts (explicit — author-authored, must be satisfied):`.
- When `ExitContractsInferred == true`: header reads `Exit contracts (reviewer-inferred — verify but do not gate harshly):`.

Reviewer guidance (rendered into the prompt):
- For each contract, assess whether `final_files` / `final_diff` evidence satisfies it.
- On miss for an **explicit** contract: reviewer may emit `severity: major` when there is a hard mismatch (e.g. a named symbol the contract references is absent from the diff). Reviewer should emit `severity: minor` when evidence suggests the contract is satisfied but cannot be verified from the supplied evidence.
- On miss for an **inferred** contract: cap at `severity: minor` unless the evidence is structurally inconsistent (a `final_diff` that clearly contradicts the inferred contract — e.g. the inferred contract names a constant the diff explicitly leaves undefined). The cap is reviewer guidance, not server-side flooring — it preserves the reviewer's ability to escalate when warranted.
- On miss (either provenance): emit a finding with `category: missing_acceptance_criterion`, `criterion: exit_contract`, evidence quoting the contract and the closest matching production-code surface, and a suggestion naming the specific edit that would satisfy the contract.

Threading model: the controller is responsible for threading per-task `exit_contracts` and `exit_contracts_inferred` from the `validate_plan` result into each dispatched implementer's prompt. The implementer then passes both into `validate_completion`. Anti-tangent's plan and per-task hooks remain decoupled (no shared store). A controller that wants stricter behavior may set `exit_contracts_inferred: false` even for reviewer-inferred contracts, treating them as author-approved — that is an explicit controller acknowledgement, not a defect.

#### 2c. Server-deterministic `normative_test_bodies` extraction

Extraction of `normative_test_bodies` is server-deterministic and runs before the reviewer prompt is rendered. It is *not* a reviewer-emitted field. The detailed extraction rules live in §2a above (`NormativeTestBodies extraction`).

Why server-deterministic rather than reviewer-driven:
- The bodies become binding AC in `validate_task_spec`. Verbatim fidelity matters.
- LLMs may paraphrase, re-indent, drop blank lines, or skip blocks. Markdown parsing is exact.
- The v0.4.0 plan-text chunking is already server-side, so this fits the existing extraction precedent.

Controller threading:
- Controllers thread the server-populated `PlanTaskResult.NormativeTestBodies` into `validate_task_spec` (via the new input from §1d).
- Controllers without a prior `validate_plan` call may populate `normative_test_bodies` manually by reading the task markdown themselves. Patrick's downstream dispatch clause in `~/.claude/anti-tangent.md` should document the manual extraction shape for that case.

### 3. Doc-only fixes (D1–D5)

All land in `INTEGRATION.md`. Patrick's `~/.claude/anti-tangent.md` is downstream of `INTEGRATION.md` per the existing mirror policy.

- **D1. Normative test bodies.** New section documenting the `**NORMATIVE TEST BODIES (verbatim):**` header convention for plan tasks that paste literal test code blocks. Paired with a `pre.tmpl` prompt tune that treats this header as binding AC, not advisory illustration.
- **D2. CVR scope clarification.** Expand the `controller_verified_references` section to state explicitly: substring suppression applies only to `unverifiable_codebase_claim` findings. AC ambiguity, scope drift, missing criteria, and `convention_deviation` are NOT suppressed by CVR entries. Field reports indicated this was a reasonable expectation that the docs did not pre-empt.
- **D3. `.trimIndent()` raw-string caveat.** New section warning plan authors that multi-line plan snippets intended for `.trimIndent()` evaluation must keep example phrases on a single source line. Mid-phrase wraps render as newlines at runtime; no MCP catches this. Suggest test assertions on the *rendered* string, not the source layout.
- **D4. Language-scoping prose caveat.** New section noting that the text-only reviewer may surface `ambiguous_spec` findings around closure/scoping semantics (Kotlin `var` + lambda, Python nested-scope, etc.). Implementer mitigation: trust the verbatim plan code block over the prose, paste-then-verify rather than re-interpret.
- **D5. Lightweight-mode visibility.** Restructure the dispatch clause so the lightweight-eligibility criteria are a callout at the *top* of the clause, before the full-protocol steps. Implementers reading the clause encounter lightweight first; full protocol is the fallback. Field reports indicated implementers occasionally ran the full protocol on tasks that qualified for lightweight, costing 60s+ of LLM time per dispatch.

### 4. Prompt-template tunes

- `pre.tmpl`:
  - Render the new `Normative test bodies (caller-supplied, treat as binding AC):` section when `normative_test_bodies` non-empty (pairs with §1d and D1).
  - Joint-coverage heuristic: when two adjacent test stubs cover the same AC bullet, treat them as joint coverage unless the gap is structurally obvious (e.g. missing invocation count). `test_strategy_notes` still wins when present (pairs with §1a).
  - When `codebase_conventions` non-empty, render a `Codebase conventions (caller-supplied):` section and instruct the reviewer to emit `convention_deviation` findings on observed deviations (pairs with §1b).
  - When `testability_extractions` non-empty, render a `Testability extractions (caller-supplied):` section and instruct deterministic substring suppression of matching `scope_drift` findings (pairs with §1c).
- `post.tmpl`:
  - When `exit_contracts` non-empty, render the provenance-aware section (explicit vs reviewer-inferred header) and walk each contract; emit `missing_acceptance_criterion` with `criterion: exit_contract` on miss, calibrating severity by `exit_contracts_inferred` (pairs with §2b).
- `plan.tmpl` and `plan_tasks_chunk.tmpl`:
  - Ask the reviewer to populate `exit_contracts` per task and set `exit_contracts_inferred` honestly; respect explicit `**Exit contracts:**` sections when present.
  - Cap at 20 contracts and 240 code points per contract (pairs with §2a).
  - The reviewer does *not* extract `normative_test_bodies`. The server populates that field before the prompt is rendered (see §2c). The prompt may include the server-extracted bodies as context if it helps the reviewer reason about exit contracts, but should not ask the reviewer to re-emit them.

All template changes regenerate golden files in `internal/prompts/testdata/`.

### 5. CHANGELOG

Add to `CHANGELOG.md`:

```markdown
## [0.5.0] - 2026-05-XX

### Added
- `validate_task_spec` accepts optional `test_strategy_notes`, `codebase_conventions`, `testability_extractions`, and `normative_test_bodies` so controllers can surface joint-coverage intent, module conventions, intentional testability extractions, and binding test bodies that the structured-fields-only spec otherwise hides from the reviewer.
- New finding category `convention_deviation` (minor-floored) emitted when a `codebase_conventions` entry conflicts with the spec. Added to the reviewer-output JSON schema category enums.
- `validate_plan` task results include optional `exit_contracts` (hybrid: explicit `**Exit contracts:**` section if present, reviewer-inferred otherwise) with a sibling `exit_contracts_inferred` provenance flag, plus `normative_test_bodies` extracted from `**NORMATIVE TEST BODIES (verbatim):**` sections.
- `validate_completion` accepts optional `exit_contracts` plus `exit_contracts_inferred`; reviewer flags misses as `missing_acceptance_criterion` with `criterion: exit_contract`, calibrating miss severity by provenance.

### Changed
- `pre.tmpl` treats `normative_test_bodies` as binding AC, treats adjacent complementary tests as joint coverage, and respects the new caller-supplied fields above.
- `post.tmpl` checks `exit_contracts` against final-file evidence when present and adjusts on-miss severity by `exit_contracts_inferred`.
- `plan.tmpl` and `plan_tasks_chunk.tmpl` populate `exit_contracts`, `exit_contracts_inferred`, and `normative_test_bodies` per task.
- Integration docs add the normative-test-bodies convention, CVR-scope clarification, `.trimIndent()` raw-string caveat, language-scoping prose caveat, and a lightweight-mode callout repositioning at the top of the dispatch clause. (Doc-only items folded under `Changed` per repo CLAUDE.md guidance on Keep-a-Changelog subsections; a prior release used `### Documentation`, which is a divergence from the project convention — this release re-aligns.)
```

## Testing Strategy

- Unit-test input normalization (trim, drop empties, 50/500 caps) for the four new `validate_task_spec` fields (`test_strategy_notes`, `codebase_conventions`, `testability_extractions`, `normative_test_bodies`) and for `validate_completion.exit_contracts`, mirroring existing `pinned_by` and `controller_verified_references` tests.
- Unit-test that `convention_deviation` is server-side floored to `minor` and parses through `internal/verdict/finding.go`.
- Schema test that the four reviewer-output schemas (`schema.json`, `plan_schema.json`, `plan_findings_only_schema.json`, `tasks_only_schema.json`) include `convention_deviation` in the `category` enum and continue to reject unknown values (`additionalProperties: false` preserved).
- Schema test that `plan_schema.json` and `tasks_only_schema.json` accept old JSON without the new task-level fields (`exit_contracts`, `exit_contracts_inferred`, `normative_test_bodies`) and new JSON with the fields, preserving `additionalProperties: false`.
- Unit-test parser round-trip for `PlanTaskResult.ExitContracts`, `PlanTaskResult.ExitContractsInferred`, `PlanTaskResult.NormativeTestBodies`, `ValidateCompletionArgs.ExitContractsInferred`, and `Result` envelope with `criterion: exit_contract` findings.
- Unit-test the deterministic `testability_extractions` substring suppression that runs after provider-response normalization inside `validate_task_spec` (same shape as the existing `controller_verified_references` suppression test — pre-hook normalization, not the completion path).
- Unit-test the deterministic `normative_test_bodies` server-side markdown extraction in `validate_plan`: header detection, fenced-block boundaries, paragraph fallback, multi-block ordering, per-entry truncation with `\n// truncated` marker, and entry-count cap.
- Golden test `pre.tmpl` rendering for each new field independently and in combination, including provenance-aware section headers.
- Golden test `post.tmpl` rendering with `exit_contracts` non-empty, covering both `exit_contracts_inferred: false` (explicit-header path) and `exit_contracts_inferred: true` (inferred-header path).
- Golden test `plan.tmpl` and `plan_tasks_chunk.tmpl` rendering with the new instruction blocks.
- Reviewer-behavior tests stay golden + httptest-based per project convention; deterministic behaviors (suppression, flooring, header selection) get unit assertions, reviewer-shaped behaviors (when to emit `convention_deviation`, when to infer `exit_contracts`) get golden prompts and one E2E coverage test per behavior behind `-tags=e2e`.
- Run `go test -race ./...`.

## Rollout

- Branch: `version/0.5.0` per project convention.
- `CHANGELOG.md` updated as work progresses; merge commit carries `[minor]`.
- `INTEGRATION.md` updated in the same release.
- Patrick mirrors `INTEGRATION.md` changes into `~/.claude/anti-tangent.md` after merge (downstream of this repo per existing memory policy).

### Backward compatibility

The release is wire-compatible — old callers continue to work without code changes — but the functional value of the cross-task and normative-body flows requires controller-side updates:

- **Response shape:** old controllers can call `validate_plan` against new servers and receive responses that include the new `exit_contracts`, `exit_contracts_inferred`, and `normative_test_bodies` fields. The old parser ignores unknown fields (`omitempty` on emit, lenient on parse). No regression.
- **Request shape:** old controllers calling `validate_task_spec` / `validate_completion` without the new inputs see identical behavior to v0.4.0. The new reviewer guidance in the prompts is gated on the respective fields being non-empty.
- **Functional value (requires controller update):** the new cross-task contract check fires only when a controller threads `exit_contracts` from a `validate_plan` result into the corresponding `validate_completion` call. Old controllers that do not yet thread will not benefit from the check (no regression, but also no improvement) until they update. The same applies to `normative_test_bodies` and to the other three new `validate_task_spec` fields.
- **Plan-author update:** the explicit `**Exit contracts:**` and `**NORMATIVE TEST BODIES (verbatim):**` sections are optional plan-markdown affordances. Plans without them continue to be valid; the reviewer infers (with provenance flag set) where applicable.

## Open questions

(None at spec time. Resolve during implementation planning.)
