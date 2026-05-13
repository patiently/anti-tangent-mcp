# Execution-phase UX — design (0.3.1)

**Status:** approved 2026-05-13
**Target version:** 0.3.1 (patch bump)
**Tracking issue:** [#12](https://github.com/patiently/anti-tangent-mcp/issues/12)
**Branch:** `version/0.3.1`

## Background

Issue [#12](https://github.com/patiently/anti-tangent-mcp/issues/12) is a field report from the execution phase of a five-task subagent-driven implementation. The most architecturally significant finding: an independent codebase-grounded reviewer caught 9 critical/major findings that 7 rounds of `validate_plan` had missed. Every one verified against actual code. The text-only reviewer is structurally bounded by what's in its input; for any plan that lands in real code, pair it with a codebase-aware review.

Beyond that headline, the execution phase surfaced several smaller frictions:

- `check_progress` produced 0 substantive catches across 5 tasks (consistently noise).
- `validate_completion` cycled multiple rounds on tooling friction (summarized diffs, missing files) rather than correctness.
- 2/5 implementers (smaller models) omitted the validate-tool envelopes from their DONE reports; the protocol's "include the envelope" rule isn't enforceable from the server side.
- Trivial tasks (doc-only edits) get the full ~50-line dispatch boilerplate — overhead 3× task complexity.
- Plan-statements about codebase facts (field names, signatures, file existence) get silently passed by the reviewer with no signal to the caller.
- After several rounds of plan iteration, `plan_verdict` stops changing meaningfully but the caller has no separate signal for convergence.

This release bundles seven targeted fixes:

1. Document the text-only boundary explicitly in `INTEGRATION.md`.
2. Add a pre-reviewer evidence-shape guard to `validate_completion`.
3. Add a paste-ready `summary_block` field to every tool response.
4. Demote `check_progress` from RECOMMENDED to OPTIONAL in the dispatch clause.
5. Document a "lightweight protocol mode" for trivial tasks.
6. Add an `unverifiable_codebase_claim` finding category.
7. Add a `plan_quality` field on `PlanResult` (separate from `plan_verdict`).

Out of scope (deferred):

- **`codebase_root` arg / filesystem grep.** Crosses the text-only architectural boundary; deserves its own design pass and probably a minor or major bump.
- **Session-level memory for cross-round verdict deltas / "convergence_signal" computed across calls.** Bigger refactor; `validate_plan` is currently stateless by design.
- **Severity-rubric tightening from #10.** Quality-uncertain; defer until an eval harness exists.

## Bump rationale

`0.3.0 → 0.3.1` (patch). The release is mostly documentation and additive defensive validation:

- Items 1, 4, 5 are doc-only.
- Item 2 (evidence-shape guard) is defensive validation that rejects malformed inputs that produce useless reviewer cycles — bug-fix-shaped behavior change.
- Items 3, 6, 7 add fields (`summary_block`, `unverifiable_codebase_claim` enum value, `plan_quality`) that are backward-compatible: JSON consumers ignoring unknown fields continue to work; Go consumers see additive struct fields whose zero values match prior behavior.

No new args, no breaking changes, no removed surface. Matches the precedent of 0.2.1 (also a patch with prompt-template changes and a CHANGELOG-required entry).

Branch will be `version/0.3.1`; `CHANGELOG.md` gets a `## [0.3.1] - 2026-05-13` block.

## Design

### 1. Documentation updates (items 1, 4, 5)

Three additions to `INTEGRATION.md` and one update to the dispatch-clause template that lives in the same file.

**1a. `### Scope and limits` section** (new, near the top of `INTEGRATION.md`, before per-tool docs):

> **What `anti-tangent-mcp` is good at.** Plan-internal consistency: contradictions between ACs, missing observable assertions, scope creep relative to non-goals, structural completeness of task headers, hedge language in acceptance criteria.
>
> **What it structurally cannot catch.** The reviewer reasons over the plan text and submitted evidence — *not* the codebase. It will not detect:
>
> - Field/symbol names that don't exist in the codebase.
> - Function signatures or insertion points that don't exist.
> - Repo-wide invariants encoded elsewhere (e.g. a constant containing characters another module's validator rejects).
> - Existing conventions in adjacent code.
> - CI/test policy declared in `CLAUDE.md` / `AGENTS.md`.
> - Type-system facts (required fields with no default).
>
> **Pair with a codebase-aware review for any plan that lands in real code.** A text-only reviewer paired with a codebase-aware pass catches both classes of bugs; either alone has a known blind spot.

**1b. `check_progress` demoted to OPTIONAL.** In the `check_progress` per-tool section of `INTEGRATION.md`, prepend:

> **Status:** OPTIONAL / advisory (was RECOMMENDED prior to v0.3.1).
>
> Field data from execution-phase usage shows `check_progress` consistently produces low-signal findings — mid-implementation context is inherently ambiguous (tests not yet written, function not yet finished, assertion not yet reached). The fast-model default magnifies the issue. Call it when *you* sense drift mid-task; do not treat it as a mandatory gate. The strong-model `validate_completion` post-impl call is far higher signal for a typical task.

Update the dispatch-clause template in `INTEGRATION.md` — change Step 2's wording from "**During work (RECOMMENDED).**" to "**During work (OPTIONAL).** Call this only when you suspect you're drifting; otherwise skip to step 3."

**1c. `### Lightweight protocol mode (v0.3.1+)` section** (new, after the dispatch-clause section in `INTEGRATION.md`):

> For trivial tasks — doc-only edits, single-file mechanical relocations, dependency bumps — the full dispatch clause is overhead-heavy (~50 lines of boilerplate for ~15 lines of actual work). Controllers may use a **lightweight clause** for these tasks:
>
> - **Skip** `validate_task_spec` (the spec is fully prescriptive; no design choices for the reviewer to shape).
> - **Skip** `check_progress` (already optional in full mode).
> - **Keep** `validate_completion` as a sanity gate before reporting DONE.
>
> Use lightweight mode when ALL of: (a) the task touches ≤ 2 files; (b) the task is mechanical (no new logic, no test-design choices); (c) the spec includes the literal text or diff to write.
>
> Use the full protocol for: any task that produces new production logic, any task with test-design choices, any task whose ACs require observable invariants.
>
> A reference lightweight dispatch clause is in `examples/lightweight-dispatch.md`.

**1d. New file `examples/lightweight-dispatch.md`** — a ~15-line dispatch template with just the `validate_completion` step, structured for paste-into-implementer-prompt:

```markdown
## Drift-protection protocol (anti-tangent-mcp, lightweight)

Before reporting DONE (REQUIRED). Call `validate_completion` with the
fields below, and AT LEAST ONE of: `final_files` (full file contents),
`final_diff` (a unified diff), or `test_evidence` (test command output).
Copy the `summary_block` field from the response verbatim into your
DONE report.

If the verdict is `fail` or contains `critical`/`major` findings, do not
report DONE — fix the findings and re-validate.

## Task spec (pass these fields verbatim to validate_completion)

- session_id: (use placeholder; this lightweight mode skips
              validate_task_spec, so there is no session_id. Pass an
              empty string and validate_completion will accept it for
              lightweight-mode flows when at least one of final_files /
              final_diff / test_evidence is non-empty.)
- summary: <one-paragraph summary of what was implemented>
```

(Note: the empty-`session_id` path needs a small handler-level allowance. See "Handler change for lightweight mode" below.)

### 2. `validate_completion` evidence-shape guard

**Detection rules** (conservative — high-confidence flags only):

1. `final_diff` contains a truncation marker. Regex match against case-insensitive `(truncated)`, `[truncated]`, `// ... unchanged`, `<!-- truncated -->`, or `^\s*\.\.\.\s*$` on its own line between hunks.
2. `final_diff` starts with `diff --git` but contains zero `@@` hunk headers (header-only stub).
3. `final_files` entries with empty `Path`.
4. `final_files` entries with `Content` containing the same truncation markers from rule 1.

Rules deliberately do NOT include file-count mismatches, "did the diff capture every file the implementer touched", or short-content heuristics — all would false-positive on legitimate small fixes.

**Envelope on rejection.** Reject with a structured envelope (not a Go error). The handler returns:

- `Verdict: "fail"`
- One finding: `Severity: major`, `Category: payload_too_large` (re-using the existing "input failed validation" category), `Criterion: "evidence_shape"`, `Evidence: "<specific pattern that matched, with byte offset>"`, `Suggestion: "Submit full file contents in final_files, or a complete unified diff with @@ hunk headers in final_diff."`
- `NextAction: "Re-submit with complete evidence; current submission appears truncated."`
- No reviewer call. No model cost.

**Caching.** In-process cache, keyed by `(session_id, sha256(final_diff || sorted_final_files || test_evidence))`, maps to the rejection envelope. TTL = 5 minutes. If a controller re-submits the identical malformed payload within 5 minutes, return the cached envelope instantly. Cleared on server restart (acceptable — these are short-lived dev-loop artifacts).

**Handler change for lightweight mode.** When `session_id == ""` AND the lightweight-mode payload (at least one of `final_files`/`final_diff`/`test_evidence`) is present, skip the session lookup but still apply the evidence-shape guard. Synthesize a minimal envelope at the end with empty `session_id`. The `validate_completion` reviewer is still called (it doesn't need a session — `Spec` becomes synthesized from `summary` only, with empty AC list). This is the smallest change to support lightweight mode without breaking the per-task lifecycle elsewhere.

**Testing.**

- 5 unit tests in `internal/mcpsrv/handlers_test.go`: one per rejection rule + one cache-hit test (two identical malformed submissions; fake reviewer's call count stays at 0).
- 1 negative test: complete `final_diff` with `@@` hunks AND complete `final_files` set both pass through to the reviewer (no false rejection).
- 1 lightweight-mode test: empty `session_id` + non-empty `final_files` + valid evidence shape → reviewer is called, envelope returned with empty `session_id`.

### 3. `summary_block` field on every tool response

**Schema additions.**

```go
// internal/mcpsrv/handlers.go
type Envelope struct {
    // ... existing fields ...
    SummaryBlock string `json:"summary_block,omitempty"`
}

// internal/verdict/plan.go
type PlanResult struct {
    // ... existing fields ...
    SummaryBlock string `json:"summary_block,omitempty"`
}
```

`omitempty` keeps the field absent if (somehow) empty — defensive for backward compatibility.

**Format.** Plain text, deterministic order. Per-task envelope:

```text
anti-tangent envelope
  session_id:    <id-or-blank>
  verdict:       <pass|warn|fail>
  partial:       true  (only line present when Partial == true)
  model_used:    <provider:model>
  review_ms:     <N>
  session_ttl_remaining_seconds: <N>  (omitted if no session)
  findings:      <N> total (<critical_count> critical, <major_count> major, <minor_count> minor)
    - [<severity>][<category>] <criterion> — <evidence-truncated-at-120-chars>
    ...
  next_action:   <text>
```

Plan-level envelope:

```text
anti-tangent envelope (validate_plan)
  plan_verdict:  <pass|warn|fail>
  plan_quality:  <rough|actionable|rigorous>
  partial:       true  (only when Partial == true)
  model_used:    <provider:model>
  review_ms:     <N>
  plan_findings: <N> (<crit>/<maj>/<min>)
    - [<sev>][<cat>] <criterion> — <evidence>
  tasks: <N>
    Task <i>: <title>  [<verdict>]  findings: <N> (<crit>/<maj>/<min>)
      - [<sev>] <criterion> — <evidence>
    ...
  next_action:   <text>
```

**Format choices.**

- Plain text — robust against varied paste contexts (bash blocks, markdown, XML-ish dispatch responses).
- Single-line findings; truncate `evidence` at 120 chars with `…` suffix if longer.
- Stable field order; controllers can grep for `verdict:` / `plan_quality:`.
- No emoji, no ANSI color, no leading whitespace tricks.
- Field documented in `INTEGRATION.md` as "human-readable; not a stable machine interface." Callers that need machine data should read the JSON envelope.

**Implementation.** Two helpers in `internal/mcpsrv` (could live in a new `summary.go` to keep `handlers.go` focused):

```go
// formatEnvelopeSummary builds the per-task summary_block.
func formatEnvelopeSummary(env Envelope) string

// formatPlanSummary builds the plan-level summary_block.
func formatPlanSummary(pr verdict.PlanResult, modelUsed string, reviewMS int64) string
```

Both pure, deterministic, fully testable. Called at the end of each handler before returning.

**Dispatch-clause update.** Edit Step 3 of the protocol clause in `INTEGRATION.md`:

> **3. Before reporting DONE (REQUIRED).** Call `validate_completion`. **Copy the `summary_block` field from the response verbatim into your DONE report** — it contains the full envelope (verdict, findings, model_used, review_ms, session_ttl_remaining_seconds) formatted for paste. No need to re-extract JSON fields manually.

This moves the source of truth from "implementer correctly formats JSON" to "implementer copy-pastes one string."

**Testing.**

- 6 format-determinism tests: known `Envelope` / `PlanResult` inputs → byte-for-byte expected output. Inline string literals; no goldens.
- 1 truncation test: a finding with a 500-char `evidence` renders to a line ending in `…` at ≤120 chars.
- 1 omitempty test: synthetic empty envelope (impossible in practice) marshals without the `summary_block` key.
- 4 handler integration tests (one per tool) confirming the field is populated on the response.

### 4. `unverifiable_codebase_claim` finding category

**Schema addition** (`internal/verdict/verdict.go`):

```go
const (
    // ... existing categories ...
    CategoryUnverifiableCodebaseClaim Category = "unverifiable_codebase_claim"
)
```

Add the value to the enum list in `internal/verdict/schema.json`, `internal/verdict/plan_schema.json`, `internal/verdict/plan_findings_only_schema.json`, and `internal/verdict/tasks_only_schema.json` — wherever a `Finding.category` enum is constrained.

**Prompt instruction.** Add a fifth paragraph to the `## Reviewer ground rules` block in `plan.tmpl`, `plan_findings_only.tmpl`, `plan_tasks_chunk.tmpl` (after the 0.3.0 hypothetical-marker):

> When the plan asserts something about the codebase that you cannot verify from the plan text alone — a field name, function signature, file path, insertion point in a graph, existing convention in adjacent code, or a type-system fact — DO emit a finding with `category: unverifiable_codebase_claim`. Severity should be `minor` (the claim might be true; you just can't check). `evidence` quotes the claim verbatim from the plan. `suggestion` says "verify against the actual code before dispatching." Do this instead of silently passing or fabricating a critique. The human will see the checklist and grep the codebase.

**Server-side severity floor.** In `internal/verdict/parser.go` (the strict parse path), after parsing each finding, enforce:

```go
if f.Category == CategoryUnverifiableCodebaseClaim && f.Severity != SeverityMinor {
    f.Severity = SeverityMinor
}
```

Rationale: the reviewer doesn't know if the claim is wrong — only that it can't check. A `major` would be inflated. Apply the same floor in `internal/verdict/parser_partial.go` and `internal/verdict/plan_parser.go` so all parse paths agree.

**Per-task templates excluded.** Do NOT add this paragraph to `pre.tmpl`, `mid.tmpl`, `post.tmpl`. Those templates carry actual code, so the reviewer CAN verify symbols and signatures from what's submitted. The new category is plan-template-scoped only.

**INTEGRATION.md documentation.** Add to the new "Scope and limits" section from item 1:

> When the reviewer encounters a plan statement about codebase facts it can't verify text-only, it now flags an `unverifiable_codebase_claim` finding rather than silently passing. These are explicitly *not failures* — they're a checklist for the human or a codebase-aware follow-up review. A plan that converges to `pass` with several `unverifiable_codebase_claim` findings is still implementable; treat the findings as "things to grep before dispatching."

**Testing.**

- 1 unit test in `verdict/parser_test.go`: reviewer JSON with `category: "unverifiable_codebase_claim"` and `severity: "major"` parses to `severity: "minor"` after the server floor.
- 3 anchor-assertion tests in `prompts_test.go`: each plan template contains the new instruction (anchor `"unverifiable_codebase_claim"` or `"verify against the actual code"`).
- 1 negative test: `pre.tmpl`, `mid.tmpl`, `post.tmpl` do NOT contain `"unverifiable_codebase_claim"`.
- Goldens regenerated for the three plan templates (default + quick) — six total.

### 5. `plan_quality` field with server sanity check

**Schema addition** (`internal/verdict/plan.go`):

```go
type PlanResult struct {
    PlanVerdict  Verdict          `json:"plan_verdict"`
    PlanFindings []Finding        `json:"plan_findings"`
    Tasks        []PlanTaskResult `json:"tasks"`
    NextAction   string           `json:"next_action"`
    Partial      bool             `json:"partial,omitempty"`
    PlanQuality  PlanQuality      `json:"plan_quality"`
    SummaryBlock string           `json:"summary_block,omitempty"`
}

type PlanQuality string

const (
    PlanQualityRough      PlanQuality = "rough"
    PlanQualityActionable PlanQuality = "actionable"
    PlanQualityRigorous   PlanQuality = "rigorous"
)

type PlanFindingsOnly struct {
    PlanVerdict  Verdict     `json:"plan_verdict"`
    PlanFindings []Finding   `json:"plan_findings"`
    NextAction   string      `json:"next_action"`
    PlanQuality  PlanQuality `json:"plan_quality"`
}
```

`plan_quality` is a required field on `PlanResult` (no `omitempty`); on `PlanFindingsOnly` it's also required. Add to `plan_schema.json` and `plan_findings_only_schema.json` enum lists.

**Reviewer prompt — single-call path (`plan.tmpl`).** Add to `## What to evaluate`:

> **plan_quality** — emit one of `"rough"`, `"actionable"`, or `"rigorous"`:
>
> - `rough`: implementer cannot start; spec is missing critical pieces, or contradictions block coherent dispatch.
> - `actionable`: spec is dispatchable but has meaningful gaps an implementer would have to ask clarifying questions about, or quality issues that risk rework.
> - `rigorous`: spec is ready to hand to a fresh implementer with high confidence; remaining findings are stylistic or expected-of-the-process.

**Reviewer prompt — chunked path (`plan_findings_only.tmpl`).** Same instruction. Pass 1 of the chunked review is the plan-wide overview — it's the right place to emit `plan_quality`. The per-chunk `TasksOnly` pass does NOT emit `plan_quality` and the field is not added to `TasksOnly`. `reviewPlanChunked` threads Pass 1's `plan_quality` into the final assembled `PlanResult`.

**Server sanity check** (`verdict/plan_parser.go` and `internal/mcpsrv/handlers.go` where the chunked result is assembled):

```text
if PlanVerdict == "fail"
    → PlanQuality := PlanQualityRough

if any finding (plan-level OR task-level) has Severity == SeverityCritical
    → PlanQuality := PlanQualityRough  (overrides whatever the reviewer said)

if PlanQuality is empty or unrecognized
    if PlanVerdict == "pass" → PlanQuality := PlanQualityRigorous
    if PlanVerdict == "warn" → PlanQuality := PlanQualityActionable
    if PlanVerdict == "fail" → PlanQuality := PlanQualityRough
```

The reviewer's value is trusted EXCEPT when contradicted by hard signals (critical findings or fail verdict). The fallback covers parse-miss and prompt drift.

**`summary_block` integration.** The plan-level `summary_block` from section 3 includes `plan_quality:` on its own line. Pulls from the parsed/sanity-checked value.

**INTEGRATION.md documentation.** Add a paragraph to the `validate_plan` per-tool section:

> The `plan_quality` field (v0.3.1+) is a separate axis from `plan_verdict`. While `plan_verdict` answers "is this dispatchable?" (pass / warn / fail), `plan_quality` answers "how close is this to ship-ready?" (rough / actionable / rigorous). When you see consecutive `warn` verdicts that aren't changing, watch `plan_quality` for convergence: `actionable → rigorous` is a meaningful improvement even if the verdict stays `warn`. Use `plan_quality` to decide when to stop iterating: most callers can ship at `actionable` for ASAP work, and at `rigorous` for quarterly-rewrite scope.

**Testing.**

- 3 unit tests in `verdict/plan_parser_test.go`:
  - Reviewer emits `plan_quality: "rigorous"` with one critical finding → parser overrides to `"rough"`.
  - Reviewer omits `plan_quality` entirely, verdict is `warn` → parser fills `"actionable"`.
  - Reviewer emits invalid value `"sparkly"` → parser falls back to verdict-based default; no parse error.
- 2 anchor-assertion tests in `prompts_test.go`: `plan.tmpl` and `plan_findings_only.tmpl` contain the `plan_quality` instruction.
- 1 negative anchor test: `plan_tasks_chunk.tmpl` does NOT contain the `plan_quality` instruction (per-chunk passes don't emit it).
- 1 single-call handler integration test: stubbed `plan_quality: "rigorous"` round-trips intact through `ValidatePlan`.
- 1 chunked-path handler integration test: Pass 1 emits `plan_quality: "actionable"`, Pass 2+ omit it; final `PlanResult` shows `actionable`.

### Files touched

```text
Modify  INTEGRATION.md                    — Scope-and-limits, check_progress demote, lightweight mode, summary_block, plan_quality, unverifiable_codebase_claim
Modify  README.md                         — short blurb pointing at INTEGRATION.md for the new docs
Create  examples/lightweight-dispatch.md  — reference lightweight clause
Modify  internal/verdict/verdict.go       — CategoryUnverifiableCodebaseClaim constant
Modify  internal/verdict/plan.go          — PlanQuality type/consts + PlanQuality field on PlanResult and PlanFindingsOnly + SummaryBlock field on PlanResult
Modify  internal/verdict/schema.json      — add unverifiable_codebase_claim
Modify  internal/verdict/plan_schema.json — add unverifiable_codebase_claim + plan_quality enum
Modify  internal/verdict/plan_findings_only_schema.json — add unverifiable_codebase_claim + plan_quality enum
Modify  internal/verdict/tasks_only_schema.json         — add unverifiable_codebase_claim
Modify  internal/verdict/parser.go        — severity floor for unverifiable_codebase_claim
Modify  internal/verdict/parser_partial.go — same severity floor
Modify  internal/verdict/plan_parser.go   — severity floor + plan_quality sanity check
Modify  internal/verdict/parser_test.go   — new test
Modify  internal/verdict/plan_parser_test.go — 3 new tests
Modify  internal/prompts/templates/plan.tmpl          — 5th ground-rules paragraph + plan_quality instruction
Modify  internal/prompts/templates/plan_findings_only.tmpl — 5th ground-rules paragraph + plan_quality instruction
Modify  internal/prompts/templates/plan_tasks_chunk.tmpl  — 5th ground-rules paragraph (no plan_quality)
Modify  internal/prompts/testdata/plan_basic.golden   — regen
Modify  internal/prompts/testdata/plan_findings_only.golden — regen
Modify  internal/prompts/testdata/plan_tasks_chunk.golden  — regen
Modify  internal/prompts/testdata/plan_basic_quick.golden  — regen
Modify  internal/prompts/testdata/plan_findings_only_quick.golden — regen
Modify  internal/prompts/testdata/plan_tasks_chunk_quick.golden  — regen
Modify  internal/prompts/prompts_test.go  — anchor tests for new ground-rules paragraph + plan_quality instruction + negative tests
Create  internal/mcpsrv/summary.go        — formatEnvelopeSummary + formatPlanSummary helpers
Create  internal/mcpsrv/summary_test.go   — format-determinism + truncation + omitempty tests
Modify  internal/mcpsrv/handlers.go       — Envelope.SummaryBlock field + populate on every handler return + evidence-shape guard + cache + lightweight-mode session_id-empty allowance
Modify  internal/mcpsrv/handlers_test.go  — 5 evidence-shape tests + 4 summary_block integration tests + 1 lightweight-mode test
Modify  CHANGELOG.md                      — add ## [0.3.1] - 2026-05-13 block
```

## Risks and mitigations

| Risk | Mitigation |
|---|---|
| Reviewer over-fires `unverifiable_codebase_claim` (every code symbol becomes a finding) | Prompt scopes the instruction to *assertions about codebase facts*, not bare references. Reuses 0.3.0 hypothetical-marker disambiguation. PR-time golden review against a known-good plan. If field reports show drift, tighten the prompt in a 0.3.2 patch. |
| Reviewer drifts `plan_quality` to off-list strings ("dispatchable", "ready") | Schema enum rejects at parse-time. Server-side fallback fills from verdict-based default. No user-visible breakage; signal degrades to "verdict echo." |
| Evidence-shape guard false-positives on legitimate diffs that contain the word "truncated" | Detection regex requires literal markers in *contextual positions* (between hunks, on their own line, inside `// ... unchanged` comments). False-positive recovery cost: re-submit with the literal removed. Lower harm than the cycle the guard prevents. |
| `summary_block` format drifts across releases, breaking caller scripts that grep it | Documented as "human-readable; not a stable machine interface." Format-determinism tests catch unintended drift in PRs. |
| `summary_block` size bloats response payload | Each block bounded by 120-char-per-finding truncation. Worst case ~3KB on a 10-task plan; negligible vs. 200KB payload cap. |
| `check_progress` demotion confuses callers who were using it as RECOMMENDED | CHANGELOG entry under `### Changed` calls it out. The dispatch-clause template in `INTEGRATION.md` updates so next paste gets OPTIONAL framing. Existing CLAUDE.md files in consumer repos won't auto-update — per-consumer doc task; acceptable. |
| Lightweight-mode dispatches skip too much and miss a real issue | Doc enumerates use conditions (≤2 files, mechanical, no test-design choices). `validate_completion` still runs (cheap sanity gate). Misuse is recoverable — next dispatch reverts to full clause. |
| Lightweight-mode handler change (empty `session_id` accepted on `validate_completion`) creates an unguarded path | Empty `session_id` is ONLY accepted when at least one of `final_files`/`final_diff`/`test_evidence` is non-empty (the existing rule applies). Negative test asserts an entirely-empty `validate_completion` call is still rejected. The synthesized spec carries empty AC list; the reviewer sees a thin context but the post-impl check is intentionally light for lightweight tasks. |

## Commit shape

Multi-commit plan on `version/0.3.1`, one commit per logical layer for review legibility:

1. `docs: INTEGRATION.md scope-and-limits + check_progress demote + lightweight mode` — pure documentation.
2. `feat(verdict): unverifiable_codebase_claim finding category + severity floor` — schema + parser changes.
3. `feat(verdict): plan_quality field with sanity check` — schema + parser changes.
4. `feat(prompts): 5th ground-rules paragraph + plan_quality instruction` — template + golden regen.
5. `feat(mcpsrv): summary_block field on all tool responses` — helpers + wire-through + tests.
6. `feat(mcpsrv): validate_completion evidence-shape guard + lightweight mode` — handler change + tests.
7. `docs: CHANGELOG / lightweight-dispatch example for 0.3.1` — final documentation.

Merge commit subject defaults to patch — no `[minor]` / `[major]` tag needed (per project release workflow).

## CHANGELOG entry (0.3.1)

```markdown
## [0.3.1] - 2026-05-13

### Added
- `summary_block` field on every tool response: paste-ready textual envelope (verdict, findings, model_used, review_ms, session_ttl_remaining_seconds) that implementers can copy verbatim into DONE reports. Reduces the protocol's reliance on the implementer correctly formatting JSON.
- `plan_quality` field on `PlanResult` (`rough` | `actionable` | `rigorous`). Separate axis from `plan_verdict` — tracks "how close to ship-ready" rather than "is this dispatchable." Reviewer-emitted with a server sanity check (critical findings or `fail` verdict force `rough`).
- `unverifiable_codebase_claim` finding category: lets the reviewer explicitly flag plan statements it cannot verify from text alone (field names, signatures, file paths, repo conventions) rather than silently passing or fabricating critiques. Server enforces `severity: minor` for this category.
- `examples/lightweight-dispatch.md` reference template for trivial tasks (doc edits, mechanical relocations).

### Changed
- `check_progress` demoted from RECOMMENDED to OPTIONAL in the dispatch-clause template. Field data showed 0 substantive catches across 5 representative tasks; the call is now advisory.
- `validate_completion` rejects malformed evidence (summarized diffs, missing hunk headers, truncation markers in `final_files` / `final_diff`) before calling the reviewer. Saves strong-model time on cycles that were driven by evidence-shape friction rather than correctness. Rejected submissions are cached for 5 minutes by content hash to short-circuit identical re-submissions.
- `validate_completion` now accepts an empty `session_id` when `final_files`, `final_diff`, or `test_evidence` is non-empty — supports the new lightweight protocol mode.

### Documentation
- New `## Scope and limits` section in `INTEGRATION.md` explicitly documents the text-only architectural boundary: what the tool catches, what it structurally cannot (codebase symbol existence, function signatures, repo-wide invariants encoded elsewhere, CI/test policy), and the recommendation to pair with a codebase-aware review for any plan that lands in real code.
- New `### Lightweight protocol mode` section in `INTEGRATION.md` documents the controller-side convention for trivial tasks.

Closes [#12](https://github.com/patiently/anti-tangent-mcp/issues/12).
```
