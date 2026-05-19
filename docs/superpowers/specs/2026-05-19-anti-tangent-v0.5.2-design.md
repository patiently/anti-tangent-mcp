# MCP feedback improvements (v0.5.2) — design

**Status:** draft 2026-05-19
**Target version:** 0.5.2 (patch bump — additive inputs and prompt-template tunes; one server-side response-shape change behind a stable verdict contract)
**Predecessor specs:** [`2026-05-18-mcp-feedback-v0.5.0-design.md`](2026-05-18-mcp-feedback-v0.5.0-design.md) shipped as 0.5.0; v0.5.1 was a docs trim + OpenAI strict-mode schema fix with no design doc.
**Source feedback:** [Issue #22](https://github.com/patiently/anti-tangent-mcp/issues/22). Field notes anonymized per repo policy.

## Background

A second anonymized field report exercised `anti-tangent-mcp` v0.5.0 across a 7-task plan-led implementation workload. Every task converged to green with no production bugs reaching the commit — so this release is signal-density and calibration work, not a stability fix.

Eight distinct improvements surfaced. They cluster into three architectural moves:

- **Server-side response-shape changes** (verdict ladder, `malformed_evidence` patterns, `controller_verified_references` suppression scope): touch `internal/verdict` and `internal/mcpsrv/handlers.go`. No new public input fields. The verdict change is a stable contract — `pass`/`warn`/`fail` keep meaning; thresholds shift.
- **Session propagation + one new public input** (normative-body carry-over from `validate_task_spec` → `validate_completion`; `harness_shape_attestation` input): `internal/session/session.go` gains a `NormativeTestBodies` field on `Session`; `internal/mcpsrv/task_spec_input.go` gains a normalizer for `harness_shape_attestation`. Reviewer-output JSON schemas (`schema.json`, `plan_schema.json`, `tasks_only_schema.json`, `plan_findings_only_schema.json`) are unchanged — these are input-only.
- **Prompt-template tunes** (major→minor demotion when normative bodies disambiguate; `harness_shape_attestation` rendering; `.trimIndent()` pre-task heuristic): edits in `internal/prompts/templates/pre.tmpl` and `post.tmpl`; golden-file regeneration in `internal/prompts/testdata/`.

The v0.5.0 normative-body work landed correctly at `validate_task_spec`, but the field report shows AC-vs-fixture mismatches still fire at `validate_completion`. The root cause is plumbing: the post-hook prompt never received the normative bodies the pre-hook stored. v0.5.2 closes that loop and pairs it with a reviewer-side demotion rule so resolved-by-normative-body ambiguities present as `minor` advisories rather than `major` blockers.

This release adds one new optional `validate_task_spec` input (`harness_shape_attestation`) and changes the verdict-severity ladder. Both are additive and backward-compatible: callers that don't pass the new field see no behavioral change; callers that parse the `verdict` field continue to get one of `{pass, warn, fail}`.

## Scope

In scope:

- **#1 Verdict-severity ladder.** Server-side after-parser derivation of `verdict` from finding-severity counts. Applies to all four tools (`validate_plan`, `validate_task_spec`, `check_progress`, `validate_completion`). The reviewer's `verdict` claim becomes advisory; the server-computed value wins. New `noise_cluster` advisory finding when the ≥3-minor → warn rule fires.
- **#2 Session-propagated normative bodies.** `Session` struct gains `NormativeTestBodies []string`. `validate_task_spec` writes it during session creation. `validate_completion` reads it and `post.tmpl` renders a binding "Normative test bodies" section before the diff/files block. Lightweight mode (empty `session_id`) is unaffected.
- **#3 Reviewer-emitted major→minor demotion.** Prompt instruction in `pre.tmpl` and `post.tmpl`: if the reviewer would emit `major ambiguous_spec` but a `normative_test_bodies` entry resolves the ambiguity, emit `minor` and append `(resolved-by-normative-body: <short citation>)` to the suggestion field.
- **#4 `malformed_evidence` shape-guard patterns.** Extend the existing regex/match list to reject `final_diff` containing `/* ... */`, `/* ...rest unchanged */`, `// snip`, `// elided`, `// ... rest unchanged`, and `/...` (lone forward-slash-ellipsis). Same finding category, same 5-minute hash cache.
- **#5 CVR claim-level suppression.** Change `controller_verified_references` suppression semantics from per-substring to per-claim: any substring match between any CVR entry and any part of the claim suppresses the entire claim's `unverifiable_codebase_claim`. No fuzzy variant matching.
- **#6 `harness_shape_attestation` input on `validate_task_spec`.** New optional structured input: each entry is `{harness: string, path: string, assertions: []string}`. Normalizer mirrors `controller_verified_references`. Reviewer-prompt section instructs the reviewer to flag contradicting ACs as `convention_deviation` (severity `major`).
- **#7 `.trimIndent()` pre-task heuristic.** New instruction in `pre.tmpl`: when the plan text contains `.trimIndent()` / `.trimMargin()` / `textwrap.dedent` / tagged-template `dedent` alongside multi-line literal comparison, emit a `minor ambiguous_spec` finding pointing at INTEGRATION.md §3.7.
- **#9 `check_progress` docs nudge.** Single-sentence addition to INTEGRATION.md §4's lifecycle table and the implementer paste-clause: "call this if a test that 'should' fail doesn't, or if you've spent more than ~5 min debugging behavior the spec leaves under-specified."
- CHANGELOG entry for 0.5.2 with `### Added` (harness_shape_attestation), `### Changed` (verdict ladder, normative-body propagation, demotion rule, CVR scope, trimIndent heuristic, docs nudge), and `### Fixed` (malformed_evidence pattern gap).

Out of scope:

- **#8 `validate_completion` latency outlier investigation.** Deferred. Profiling work that may produce a soft `final_files` payload threshold + advisory finding lands in a later release. No code change in v0.5.2.
- **CodeScene-side improvements** (fixture-aware downweighting, baseline-aware `pre_commit_code_health_safeguard`, `failed_pre_existing` quality-gates verdict). Filed at `codescene-oss/codescene-mcp-server` separately.
- **Persistent storage.** All new state stays in-memory; `NormativeTestBodies` lives on the existing `Session` and expires with the session's 4h sliding TTL.
- **New finding categories.** The `noise_cluster` advisory uses `category: other` with `criterion: noise_cluster`. The demotion advisory rides on existing `ambiguous_spec`.
- **Reviewer-output schema changes.** Inputs change; outputs do not. `internal/verdict/*_schema.json` files are untouched.

## Verdict-severity ladder (#1)

### Current behavior

The reviewer emits a `verdict` field directly as part of its JSON response. The server validates the value against `{pass, warn, fail}` and surfaces it on the envelope. The reviewer's prompt instructs it to choose a verdict based on its findings, but the mapping from finding severities to verdict is implicit in the prompt and varies run-to-run. Field reports show single-`minor` findings producing `verdict: fail`.

### New behavior

After the reviewer response is parsed, the server computes a definitive `verdict` from the finding severities:

```
critical = count of findings with severity == "critical"
major    = count of findings with severity == "major"
minor    = count of findings with severity == "minor"

if critical >= 1 or major >= 2:
    verdict = "fail"
elif major >= 1 or minor >= 3:
    verdict = "warn"
else:
    verdict = "pass"
```

The server-computed value overrides whatever the reviewer emitted. The reviewer's `verdict` claim is still parsed for schema validation (must be one of `{pass, warn, fail}`) but is discarded — only the server-derived value reaches the response envelope.

When the `minor >= 3 → warn` branch fires (i.e., no `critical` and no `major`, but three or more `minor`), the server appends a synthetic advisory finding to the envelope so the caller sees why:

```json
{
  "severity": "minor",
  "category": "other",
  "criterion": "noise_cluster",
  "evidence": "<N> minor findings on this call (no critical or major). Each finding is individually advisory; the cluster lifts verdict to warn.",
  "suggestion": "Inspect the minor findings as a group. If they're all low-signal noise, the next caller iteration can ignore them collectively. If any one warrants escalation, address it individually."
}
```

The `noise_cluster` advisory is the LAST finding appended (after the reviewer's own findings) and uses `category: other` to avoid claiming structural meaning. It is itself `minor`, so it does not double-count toward the next verdict computation (the verdict is computed once, before the synthetic is appended).

### Affected tools

All four. The change lives in a shared helper called from each handler's response path. The `Result` envelope (`internal/verdict/verdict.go`) and `PlanResult` envelope (`internal/verdict/plan.go`) gain the same derivation. For `validate_plan` the verdict at issue is `plan_verdict` (plan-level findings only — per-task verdicts are derived per-task from each task's findings list with the same rule).

### Caller contract

The contract `verdict ∈ {pass, warn, fail}` is unchanged. Callers parsing `verdict` continue to get one of those three values. The only externally-visible change is which value they get in borderline cases (single-minor was previously sometimes `fail`; now it's `pass`).

### Files touched

- `internal/verdict/verdict.go`: add `DeriveVerdict(findings []Finding) string` helper.
- `internal/verdict/plan.go`: invoke `DeriveVerdict` for both plan-level and per-task results in `PlanResult`.
- `internal/verdict/parser.go` (and `plan_parser.go`): after parsing the reviewer response, call `DeriveVerdict` and overwrite `result.Verdict`. Append the `noise_cluster` synthetic finding when applicable.
- `internal/verdict/verdict_test.go`, `plan_test.go`, `parser_test.go`: tests for each branch (pass / warn / fail / noise_cluster appended).

## Session-propagated normative bodies (#2)

### Current behavior

`validate_task_spec` accepts `normative_test_bodies []string` as input. The pre-hook handler writes the value to the session as `Session.TaskSpec.NormativeTestBodies`. `pre.tmpl` renders the bodies. `validate_completion` reads the session by `session_id` but does not surface the bodies in `post.tmpl`.

### New behavior

The post-hook renders the bodies before the diff/files block. The reviewer is instructed to treat them as authoritative for fixture state, exact strings, and assertions.

The `Session` struct already carries `TaskSpec` which has a `NormativeTestBodies []string` field (added in v0.5.0). No struct change is needed. Only the prompt template and the handler's prompt-render call site change.

`post.tmpl` gains a new section, rendered only when `len(.NormativeTestBodies) > 0`:

```
## Normative test bodies (binding)

The following test bodies were declared as binding when the implementer started this task. They are authoritative for fixture state, exact strings, and assertions. The AC list above is authoritative for behavior — but when an AC and a normative body appear to disagree on a fixture value, the body wins.

Do NOT flag AC-vs-fixture mismatches when a normative body explicitly pins the value.

<for each body in .NormativeTestBodies:>
---
{{body}}
---
</for>
```

The handler's `renderPostPrompt` is updated to pass `session.TaskSpec.NormativeTestBodies` into the template context. Lightweight mode (empty `session_id`, no session lookup) gets an empty list → the new section renders as nothing → no behavior change.

### Files touched

- `internal/mcpsrv/handlers.go`: in `handleValidateCompletion`, pass the normative bodies from the looked-up session into the prompt-render context.
- `internal/prompts/templates/post.tmpl`: add the conditional `## Normative test bodies (binding)` section.
- `internal/prompts/testdata/post_*.golden`: regenerate (intentional template change).
- `internal/prompts/prompts_test.go`: add `Contains` assertions that, when bodies are non-empty, the rendered prompt contains the section header and each body verbatim.
- `internal/mcpsrv/handlers_test.go`: add a session-round-trip test (pre-hook stores bodies → post-hook prompt contains them).

## Reviewer-emitted major→minor demotion (#3)

### Current behavior

The reviewer is told to assign `major` to ambiguity findings that, in its judgment, block the implementer. It does not currently distinguish between a genuinely ambiguous AC and one where the prose is ambiguous but a normative body or code block in context resolves it.

### New behavior

Both `pre.tmpl` and `post.tmpl` gain this instruction near the existing severity guidance:

```
If you would emit a finding with `category: ambiguous_spec` AND `severity: major`, but a normative test body (provided in the `Normative test bodies` section above) explicitly pins the value or assertion that the AC's prose leaves ambiguous, downgrade the severity to `minor` and append `(resolved-by-normative-body: <short citation of which body resolves it, max 80 chars>)` to the `suggestion` field. The `criterion` and `evidence` fields stay as you would have written them.
```

This is reviewer-emitted: the reviewer judges the resolution and writes the demoted finding directly. The server does no post-parse re-severity. The advisory citation in `suggestion` lets callers see why the demotion happened.

### Files touched

- `internal/prompts/templates/pre.tmpl`: add the instruction below the existing severity-classification rules.
- `internal/prompts/templates/post.tmpl`: same instruction.
- `internal/prompts/testdata/pre_*.golden` and `post_*.golden`: regenerate.
- `internal/prompts/prompts_test.go`: add `Contains` assertions for the new instruction string in both templates.

## `malformed_evidence` shape-guard patterns (#4)

### Current behavior

The shape-guard in `internal/mcpsrv/handlers.go` rejects `validate_completion` inputs before sending them to the reviewer when they contain known placeholder/truncation markers: `(truncated)`, `[truncated)`, `// ... unchanged`, lone `...` lines, empty `Path` entries.

### New behavior

Extend the pattern list with six additional patterns observed in the field:

1. `/* ... */` (C-style block comment with ellipsis only)
2. `/* ...rest unchanged */` and variants with `rest unchanged` inside a block comment
3. `// snip`
4. `// elided`
5. `// ... rest unchanged` (line comment variant — distinct from the existing `// ... unchanged` pattern in the leading-space sense)
6. `/...` as a standalone token on a line (forward-slash-ellipsis without a comment prefix)

Each new pattern is a literal substring or a small regex. The existing 5-minute canonical-hash cache for rejection idempotency is unchanged.

The finding shape is identical:

```json
{
  "severity": "major",
  "category": "malformed_evidence",
  "criterion": "<short pattern label>",
  "evidence": "final_diff contains placeholder/truncation marker: <quoted snippet>",
  "suggestion": "Pass full file content via final_files (or, for legitimate inclusion of these literal strings, pass a complete unified diff via final_diff with all lines intact)."
}
```

### Files touched

- `internal/mcpsrv/handlers.go`: extend the shape-guard pattern list.
- `internal/mcpsrv/handlers_test.go`: parametric test exercising each new pattern (one finding emitted, finding shape matches the contract above).

## CVR claim-level suppression (#5)

### Current behavior

There is no Go-side suppression for `unverifiable_codebase_claim` findings. The CVR-driven suppression rule lives entirely in `internal/prompts/templates/pre.tmpl` (around line 48):

> "If a Controller-verified references section is present, treat those entries as caller-supplied attestations that the controller grep-verified specific codebase references before dispatch. Suppress an `unverifiable_codebase_claim` for claim C only when some entry in controller_verified_references is a substring of C, or C is a substring of some entry."

Field reports show the reviewer doesn't consistently follow this instruction: claims that mention multiple symbols only get suppressed when every symbol is CVR-covered, despite the prompt asking for whole-claim substring-match in either direction. This appears to be reviewer-side prompt-compliance drift, not a documented-rule defect.

### New behavior

Belt-and-suspenders: deterministic Go-side suppression mirroring the prompt's rule + a clearer prompt instruction. The Go-side suppression makes correctness independent of reviewer compliance.

**Go-side suppression.** Add a new function `suppressUnverifiableCodebaseClaim(findings []verdict.Finding, cvr []string) []verdict.Finding` in `internal/mcpsrv/task_spec_normalize.go`, mirroring the existing `suppressTestabilityExtractionScopeDrift`:

- For each finding with `category: unverifiable_codebase_claim`, check whether any CVR entry substring-matches the finding's `evidence` OR `criterion` (in either direction: entry-is-substring-of-text OR text-is-substring-of-entry).
- If matched, drop the finding.
- Non-`unverifiable_codebase_claim` findings pass through unchanged.
- Empty CVR list or empty findings list short-circuits to the input (no allocation).
- Empty/whitespace-only `evidence` AND `criterion` is treated as non-match (avoid the `strings.Contains(non_empty, "")` trap that `suppressTestabilityExtractionScopeDrift` already documented).
- CVR entries shorter than 4 code points are skipped in the matching loop (avoid single-letter false matches like a CVR entry of `T` swallowing every claim).

The call ordering in `internal/mcpsrv/handlers.go` becomes:

```go
result.Findings = suppressTestabilityExtractionScopeDrift(result.Findings, inputs.TestabilityExtractions)
result.Findings = suppressUnverifiableCodebaseClaim(result.Findings, inputs.ControllerVerifiedReferences)  // NEW
result.Findings = normalizeTaskSpecUnverifiableFindings(result.Findings)
```

Ordering matters: `normalizeTaskSpecUnverifiableFindings` rolls multiple `unverifiable_codebase_claim` findings into a single `codebase_reference_checklist` finding, so suppression must run before rollup to avoid dropping the wrong (synthetic checklist) finding.

**Prompt tightening.** Update `pre.tmpl`'s line-48 instruction to add a worked example showing a multi-symbol claim where only one symbol matches a CVR entry → entire claim suppressed:

> "Example: claim 'XService.findFoo at path/to/file.kt:L42 returns a sorted slice' with CVR `[\"path/to/file.kt\"]` → suppress (the path matches one of the claim's substrings)."

Defense in depth: the Go-side suppression catches reviewer drift; the prompt clarification keeps the reviewer on-spec from the start.

### Files touched

- `internal/mcpsrv/task_spec_normalize.go`: add `suppressUnverifiableCodebaseClaim` alongside the existing `suppressTestabilityExtractionScopeDrift`. Same defensive-guard pattern.
- `internal/mcpsrv/handlers.go`: insert the new suppression call between the existing `suppressTestabilityExtractionScopeDrift` and `normalizeTaskSpecUnverifiableFindings` calls.
- `internal/prompts/templates/pre.tmpl`: add the worked example to the existing line-48 instruction.
- `internal/prompts/testdata/pre_*.golden`: regenerate.
- `internal/mcpsrv/handlers_test.go` (or a focused `task_spec_normalize_test.go`): add tests covering (a) single CVR entry suppresses a multi-symbol claim where it matches one symbol, (b) no-match keeps the claim, (c) 4-code-point floor — `CVR: ["T"]` does NOT suppress a `T.foo` claim, (d) empty evidence + empty criterion → not suppressed, (e) ordering: suppression runs before rollup so the synthetic `codebase_reference_checklist` finding (if any) is not dropped.
- `INTEGRATION.md` §5.7: clarify the prose — "CVR suppresses matching `unverifiable_codebase_claim` findings by substring match between any CVR entry and the finding's evidence or criterion (either direction). Suppression now runs server-side as well as in the reviewer prompt, so the behavior is deterministic regardless of reviewer compliance."
- `CHANGELOG.md`: bullet under `### Changed`.

## `harness_shape_attestation` input (#6)

### Caller-visible shape

New optional input on `validate_task_spec`:

```json
{
  "task_title": "…",
  "goal": "…",
  "acceptance_criteria": [...],
  "harness_shape_attestation": [
    {
      "harness": "TestHarnessX",
      "path": "test/path/to/file.kt:L100-L200",
      "assertions": [
        "records emitted spans via getEmittedSpans()",
        "does not stub the validator method",
        "asserts emit calls via assertEmittedRequests(times)"
      ]
    },
    {
      "harness": "ScenarioBuilder",
      "path": "test/support/scenario_builder.go",
      "assertions": [
        "produces scenarios with fixed timestamps",
        "must be initialized with WithClock() to override"
      ]
    }
  ]
}
```

### Caps

- At most 25 entries per call.
- Per entry: `harness` ≤ 240 code points; `path` ≤ 240 code points; `assertions` at most 10 entries each ≤ 480 code points.
- Trim leading/trailing whitespace. Dedup entries by canonical-JSON hash (entries with identical fields after trim collapse to one).
- Reject entries with empty `harness` or empty `assertions` array; reject `assertions` with empty strings.

### Normalization

A new helper in `internal/mcpsrv/task_spec_input.go` mirrors the `controller_verified_references` normalizer. Error messages match the established style (`harness_shape_attestation must contain at most 25 entries`, `harness_shape_attestation[i].assertions[j] must be at most 480 characters`).

### Reviewer-prompt rendering

`pre.tmpl` gains a new section, rendered only when `len(.HarnessShapeAttestations) > 0`:

```
## Harness shape attestations (caller-attested)

The controller has declared the following non-trivial shape facts about test harnesses or fixtures referenced in this task. Treat each attestation as authoritative context — the controller has verified the assertions before dispatch. The reviewer does NOT independently verify these against the codebase.

Use the attestations to flag any AC that contradicts an assertion. When an AC asks the implementer to do something a `does not` assertion forbids, OR an AC depends on a capability not listed in the harness's positive assertions, emit a finding with:
  - `category: convention_deviation`
  - `severity: major`
  - `criterion: <which attestation is contradicted>`
  - `evidence: <which AC contradicts it>`
  - `suggestion: <revised AC or explicit harness change request>`

<for each attestation in .HarnessShapeAttestations:>
- **{{harness}}** (at `{{path}}`):
<for each assertion in attestation.assertions:>
    - {{assertion}}
</for>
</for>
```

### Files touched

- `internal/mcpsrv/task_spec_input.go`: add `normalizeHarnessShapeAttestation`.
- `internal/mcpsrv/handlers.go`: thread the field through `validate_task_spec` input parsing into the prompt context; persist on `Session.TaskSpec` so future post-hook rendering can opt into it.
- `internal/session/session.go`: add `HarnessShapeAttestations []HarnessShapeAttestation` to `TaskSpec`, with the supporting struct type defined here.
- `internal/prompts/templates/pre.tmpl`: add the new `## Harness shape attestations` section.
- `internal/prompts/testdata/pre_*.golden`: regenerate.
- `internal/prompts/prompts_test.go`: assertions for the new section's header + each rendered attestation's fields.
- `internal/mcpsrv/handlers_test.go`: input validation tests (caps, dedup, empty handling) + session round-trip + reviewer-prompt context test.
- `INTEGRATION.md` §3 and §4.2: document the new input alongside `pinned_by` / `controller_verified_references` / `normative_test_bodies`.
- `README.md`: add to the `### validate_task_spec arguments` block if such a block exists (today the v0.5.0 summary in README mentions the four existing optional inputs; extend to five).

## `.trimIndent()` pre-task heuristic (#7)

### Current behavior

`pre.tmpl` does not pattern-match for raw-string trimming constructs. Callers learn about the §3.7 caveat from INTEGRATION.md or from a missed test.

### New behavior

Append to the existing pre-task instructions in `pre.tmpl`:

```
RAW-STRING TRIMMING CAVEAT (§3.7 heuristic):
If the task's plan text or context contains `.trimIndent()`, `.trimMargin()`, `textwrap.dedent`, or a tagged-template `dedent`, AND any acceptance criterion compares the implementation's output against a multi-line string literal in the same plan block, emit a `minor` `ambiguous_spec` finding with:
  - criterion: "raw-string trimming caveat — see INTEGRATION.md §3.7"
  - evidence: "<quoted trim construct and the multi-line literal>"
  - suggestion: "Pin example strings the implementation will compare against to a single source line, OR phrase the AC against the rendered string (e.g. 'output contains <phrase>') rather than against source layout."
```

Pure prompt-template change; no new code path.

### Files touched

- `internal/prompts/templates/pre.tmpl`: append the instruction.
- `internal/prompts/testdata/pre_*.golden`: regenerate.
- `internal/prompts/prompts_test.go`: assert the instruction string is present.

## `check_progress` docs nudge (#9)

Single-sentence addition to `INTEGRATION.md` §4 lifecycle table (the `check_progress` row) AND to the lifecycle clause's "During work" step:

```
| During | `check_progress` | Optional (advisory; low-signal in field data — call only when you suspect drift, OR when a test that 'should' fail doesn't, OR you've spent >5 min debugging behavior the spec leaves under-specified) | When you suspect drift mid-task |
```

The paste-clause's "During work (OPTIONAL)" step gets the same wording appended.

### Files touched

- `INTEGRATION.md`: the lifecycle table row and the paste-clause "During work" step.
- `CHANGELOG.md`: bullet under `### Changed`.

## CHANGELOG

`## [0.5.2] - 2026-05-19` opens with the first commit. Per project convention, bullets under:

- `### Added`: `harness_shape_attestation` input on `validate_task_spec` (one bullet describing shape + caps).
- `### Changed`:
  - Verdict-severity ladder (server-computed; pass/warn/fail thresholds documented).
  - `validate_completion` now sees `normative_test_bodies` via session propagation; reviewer instructed to treat them as authoritative for fixture state.
  - Reviewer demotes `major ambiguous_spec` → `minor` with `(resolved-by-normative-body: …)` advisory when applicable.
  - `controller_verified_references` suppression widens from per-substring to per-claim (with 4-code-point floor).
  - `.trimIndent()` raw-string caveat now surfaces as a pre-task heuristic finding linking §3.7.
  - INTEGRATION.md `check_progress` description gains a "test-that-should-fail-doesn't" trigger nudge.
- `### Fixed`:
  - `malformed_evidence` shape-guard extended with six new placeholder/truncation patterns (`/* ... */`, `/* ...rest unchanged */`, `// snip`, `// elided`, `// ... rest unchanged`, `/...`).

## Migration and compat

- **Inputs.** All new fields are optional with safe defaults. Callers on v0.5.1 continue to work unchanged.
- **Outputs.** The `verdict` field stays in `{pass, warn, fail}`. The only externally-visible change is the borderline-case distribution. Callers that conditionally branch on `verdict == "fail"` get fewer false fails on single-minor.
- **`noise_cluster` advisory.** Callers that iterate findings see one extra `minor` advisory when the ≥3-minor-warn branch fires. Existing finding parsers handle it as any other minor; the `criterion: noise_cluster` lets callers identify and downweight it.
- **`harness_shape_attestation`.** Net-new field. Existing dispatch templates ignore it. Callers wanting the value update their dispatch clause to pass it from the task spec.
- **Session propagation.** A v0.5.1 caller that calls `validate_task_spec` then `validate_completion` continues to work; if they didn't pass `normative_test_bodies` they continue to get a post-hook prompt without that section. A v0.5.2 caller that did pass bodies at pre-hook automatically gets them rendered at post-hook.
- **Schema files.** No `internal/verdict/*_schema.json` changes. The OpenAI strict-mode invariant (#22 history: required-must-cover-properties) remains satisfied — verified by the v0.5.1 `schema_invariants_test.go` regression test, which runs on every PR.

## Testing strategy

Per-item, in addition to the unit tests already named above:

- **Verdict ladder:** parametric table-driven test with all severity-count combinations covering each branch boundary. Asserts `noise_cluster` advisory is appended only when the ≥3-minor → warn branch fires AND no critical / major are present.
- **Normative-body propagation:** integration-style test that simulates a `validate_task_spec` → `validate_completion` flow with the same `session_id`, asserts the post-hook prompt contains the bodies, asserts the lightweight (empty session_id) flow does not error and does not include the section.
- **Demotion advisory:** prompt golden + a reviewer-output parsing test that confirms a reviewer-emitted `minor` finding with `(resolved-by-normative-body: …)` suffix in `suggestion` is parsed as-is. Server doesn't re-derive severity.
- **Malformed-evidence patterns:** parametric per-pattern test asserting rejection and finding shape match.
- **CVR claim-level:** three-case table: (a) one substring matches a CVR entry → claim suppressed; (b) no substring matches → claim retained; (c) substring < 4 code points → not used for matching.
- **`harness_shape_attestation`:** input normalization (caps, dedup, error messages) + prompt-rendering golden + session round-trip.
- **`.trimIndent()` heuristic:** prompt golden assertion (no reviewer-output test — the heuristic is a prompt instruction; the reviewer's compliance is tested via E2E only and remains off the per-PR path).
- **Docs nudge:** INTEGRATION.md `grep -c "test that 'should' fail"` returns 1 (lifecycle row) or 2 (lifecycle row + paste-clause "During work" step) per the design.

Mainline run: `go test -race ./...` as always. E2E (`-tags=e2e`) remains opt-in.

## Open questions deferred to plan-time

- Exact regex shape for the six new `malformed_evidence` patterns vs literal substring match — to be settled in the plan task that implements #4. Lean toward literal substring where possible, regex only where necessary (e.g., `// ... rest unchanged` benefits from regex to tolerate whitespace variance).
- Whether `harness_shape_attestation` entries get surfaced verbatim in the reviewer's finding `evidence` field, or only quoted by short name. Design assumes verbatim quoting in `evidence` is acceptable since attestations are bounded (≤ 480 code points per assertion).
- The advisory wording for `noise_cluster` is a default; tightening it during implementation review is fine if it can stay under ~280 code points.

## References

- Issue #22 (https://github.com/patiently/anti-tangent-mcp/issues/22) — full field-feedback rollup.
- v0.5.0 design (`docs/superpowers/specs/2026-05-18-mcp-feedback-v0.5.0-design.md`) — added the original four optional `validate_task_spec` inputs and `normative_test_bodies` at pre-hook time.
- v0.5.1 INTEGRATION.md trim (`docs/superpowers/plans/2026-05-19-integration-md-trim.md`) — reduced INTEGRATION.md to 33,186 chars; informs CHANGELOG bullet sizing and the `.trimIndent()` §3.7 cross-reference shape.
- OpenAI strict-mode invariant regression test (`internal/verdict/schema_invariants_test.go`) — landed in v0.5.1; ensures any future schema field is added to both `properties` and `required`.
