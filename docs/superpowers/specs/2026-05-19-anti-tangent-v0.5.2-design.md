# MCP feedback improvements (v0.5.2) — design

**Status:** draft 2026-05-19
**Target version:** 0.5.2 (patch bump — additive inputs and prompt-template tunes; one server-side response-shape change behind a stable verdict contract)
**Predecessor specs:** [`2026-05-18-mcp-feedback-v0.5.0-design.md`](2026-05-18-mcp-feedback-v0.5.0-design.md) shipped as 0.5.0; v0.5.1 was a docs trim + OpenAI strict-mode schema fix with no design doc.
**Source feedback:** [Issue #22](https://github.com/patiently/anti-tangent-mcp/issues/22). Field notes anonymized per repo policy.

## Background

A second anonymized field report exercised `anti-tangent-mcp` v0.5.0 across a 7-task plan-led implementation workload. Every task converged to green with no production bugs reaching the commit — so this release is signal-density and calibration work, not a stability fix.

Eight distinct improvements surfaced. They cluster into three architectural moves:

- **Server-side response-shape changes** (verdict ladder, `malformed_evidence` patterns, `controller_verified_references` suppression scope): touch `internal/verdict` and `internal/mcpsrv/handlers.go`. No new public input fields. The verdict change is a stable contract — `pass`/`warn`/`fail` keep meaning; thresholds shift. Verdict derivation moves to handler-level (after all finding-list mutations) so the envelope's verdict always matches the envelope's findings.
- **Session propagation + one new public input + one new finding category** (normative-body rendering at `validate_completion`; `harness_shape_attestation` input on `validate_task_spec`; `attestation_contradiction` finding category): `Session.Spec.NormativeTestBodies` already exists from v0.5.0 — no change needed for #2's session-propagation work. `internal/session/session.go` DOES change for #6: define a new `HarnessShapeAttestation` struct AND add `HarnessShapeAttestations []HarnessShapeAttestation` to `TaskSpec` (the struct must live in `session` because `mcpsrv` already imports `session` — defining the type in `mcpsrv` would create an import cycle once `session.TaskSpec` references it). `internal/mcpsrv/task_spec_input.go` gains a normalizer for `harness_shape_attestation`. All four reviewer-output JSON schemas (`schema.json`, `plan_schema.json`, `tasks_only_schema.json`, `plan_findings_only_schema.json`) add `attestation_contradiction` to their `category` enums (the only output-side schema change in v0.5.2; the v0.5.1 `schema_invariants_test.go` continues to enforce `properties == required`).
- **Prompt-template tunes** (major→minor demotion when normative bodies disambiguate; `harness_shape_attestation` rendering; `.trimIndent()` pre-task heuristic): edits in `internal/prompts/templates/pre.tmpl` and `post.tmpl`; golden-file regeneration in `internal/prompts/testdata/`.

The v0.5.0 normative-body work landed correctly at `validate_task_spec`, but the field report shows AC-vs-fixture mismatches still fire at `validate_completion`. The root cause is plumbing: the post-hook prompt never received the normative bodies the pre-hook stored. v0.5.2 closes that loop and pairs it with a reviewer-side demotion rule so resolved-by-normative-body ambiguities present as `minor` advisories rather than `major` blockers.

This release adds one new optional `validate_task_spec` input (`harness_shape_attestation`) and changes the verdict-severity ladder. Backward-compatibility splits along two axes:

- **New input + new output category**: the prompts only instruct reviewers to emit `attestation_contradiction` when attestations are present in the task spec, so callers that do not pass `harness_shape_attestation` should not normally see this category. The category sits in all four reviewer-output schemas (required for the OpenAI strict-mode invariant), which means a non-compliant reviewer COULD technically emit it outside the documented trigger; callers that exhaustively switch on `category` SHOULD therefore handle it generically (parse-and-surface) rather than asserting it never appears, but no dispatch-template change is required.
- **Verdict ladder**: ALL callers may see redistributed verdicts under the new ladder — borderline cases shift (single-`minor` moves from `fail` to `pass`; three-`minor` moves from `pass` to `warn`). The `verdict` field still resolves to one of `{pass, warn, fail}`, so callers branching on equality continue to work; callers that conditionally degrade on `verdict != "pass"` will see fewer false alarms on single-minor and slightly more triggers on three-minor.

## Scope

In scope:

- **#1 Verdict-severity ladder.** Server-side after-parser derivation of `verdict` from finding-severity counts. Applies to all four tools (`validate_plan`, `validate_task_spec`, `check_progress`, `validate_completion`). The reviewer's `verdict` claim becomes advisory; the server-computed value wins. New `noise_cluster` advisory finding when the ≥3-minor → warn rule fires.
- **#2 Session-propagated normative bodies.** `Session.Spec.NormativeTestBodies` is already populated by `validate_task_spec` (added in v0.5.0). v0.5.2 adds a `post.tmpl` section that renders the bodies as binding context. No struct change; the post-hook handler already passes `Spec` into the prompt context via `prompts.PostInput`. Lightweight mode (empty `session_id`) is unaffected — the section renders empty when no bodies are present.
- **#3 Reviewer-emitted major→minor demotion.** Prompt instruction in `pre.tmpl` and `post.tmpl`: if the reviewer would emit `major ambiguous_spec` but a `normative_test_bodies` entry resolves the ambiguity, emit `minor` and append `(resolved-by-normative-body: <short citation>)` to the suggestion field.
- **#4 `malformed_evidence` shape-guard patterns.** Extend `internal/mcpsrv/handlers.go`'s `evidenceTruncationPatterns` slice. The existing `checkEvidenceShape` walker applies every entry in that slice to BOTH `final_diff` AND every `final_files[].content`, so adding patterns to the slice automatically extends both. New patterns: `/* ... */`, `/* ...rest unchanged */`, `// snip`, `// elided`, `// ... rest unchanged`, and `/...` (lone forward-slash-ellipsis). Same finding category, same 5-minute hash cache.
- **#5 CVR claim-level suppression.** Change `controller_verified_references` suppression semantics from per-substring to per-claim: any substring match between any CVR entry and any part of the claim suppresses the entire claim's `unverifiable_codebase_claim`. No fuzzy variant matching.
- **#6 `harness_shape_attestation` input on `validate_task_spec` + `attestation_contradiction` finding category.** New optional structured input: each entry is `{harness: string, path: string, assertions: []string}`. Normalizer mirrors `controller_verified_references`. Reviewer-prompt section instructs the reviewer to flag ACs that EXPLICITLY contradict an attestation (e.g. the AC asks for behavior a `does not` assertion forbids, OR the AC asserts a fixture state that contradicts a stated positive assertion) using the new `attestation_contradiction` category at the reviewer's chosen severity (typically `major`). The category is intentionally distinct from `convention_deviation` (which is severity-floored to `minor` at parser-level for "reviewer can't verify the implementation"); attestations are caller-attested shape facts, so a reviewer-detected contradiction with the AC's prose is a hard finding, not a "can't verify."
- **#7 `.trimIndent()` pre-task heuristic.** New instruction in `pre.tmpl`: when the plan text contains `.trimIndent()` / `.trimMargin()` / `textwrap.dedent` / tagged-template `dedent` alongside multi-line literal comparison, emit a `minor ambiguous_spec` finding pointing at INTEGRATION.md §3.7.
- **#9 `check_progress` docs nudge.** Single-sentence addition to INTEGRATION.md §4's lifecycle table and the implementer paste-clause: "call this if a test that 'should' fail doesn't, or if you've spent more than ~5 min debugging behavior the spec leaves under-specified."
- CHANGELOG entry for 0.5.2 with `### Added` (harness_shape_attestation), `### Changed` (verdict ladder, normative-body propagation, demotion rule, CVR scope, trimIndent heuristic, docs nudge), and `### Fixed` (malformed_evidence pattern gap).

Out of scope:

- **#8 `validate_completion` latency outlier investigation.** Deferred. Profiling work that may produce a soft `final_files` payload threshold + advisory finding lands in a later release. No code change in v0.5.2.
- **CodeScene-side improvements** (fixture-aware downweighting, baseline-aware `pre_commit_code_health_safeguard`, `failed_pre_existing` quality-gates verdict). Filed at `codescene-oss/codescene-mcp-server` separately.
- **Persistent storage.** All new state stays in-memory; `NormativeTestBodies` lives on the existing `Session` and expires with the session's 4h sliding TTL.
- **Other new finding categories.** The `noise_cluster` advisory uses `category: other` with `criterion: noise_cluster`. The demotion advisory rides on existing `ambiguous_spec`. The only new category added in v0.5.2 is `attestation_contradiction`, which #6 introduces and threads through all four reviewer-output schemas + the parser's `validCategory` allowlist.
- **Other reviewer-output schema changes.** The only output-side schema delta in v0.5.2 is adding `attestation_contradiction` to the `category` enums in the four schema files. No other field additions, no `required`/`properties` changes. The v0.5.1 `schema_invariants_test.go` continues to enforce that every property is in `required`, catching any accidental drift.

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

### Where derivation runs: handler-level, post-mutation

Derivation runs AFTER every finding-list mutation. The parser's job is to load and validate the reviewer response (including the existing per-category severity floors in `applySeverityFloor` for `unverifiable_codebase_claim` and `convention_deviation`); derivation does not happen inside the parser. The handler is responsible for invoking the derivation helper after all other handler-side post-processing has run, immediately before returning the envelope.

For `validate_task_spec` (`internal/mcpsrv/handlers.go`), the per-task post-mutation pipeline becomes:

```go
result, err := verdict.Parse(raw)          // schema validation + severity floors
result.Findings = suppressTestabilityExtractionScopeDrift(result.Findings, inputs.TestabilityExtractions)
result.Findings = suppressUnverifiableCodebaseClaim(result.Findings, inputs.ControllerVerifiedReferences)  // new from #5
result.Findings = normalizeTaskSpecUnverifiableFindings(result.Findings)
if cc.Clamp != (verdict.Finding{}) {
    // CRITICAL: prepend the clamp BEFORE finalization so it
    // participates in the severity ladder. Today's prependClamp at
    // envelope-construction time runs AFTER any verdict derivation,
    // so a result with 2 minors + 1 minor clamp would stay `pass`
    // under the new ladder unless the clamp is in result.Findings
    // when FinalizeVerdict runs.
    result.Findings = append([]verdict.Finding{cc.Clamp}, result.Findings...)
}
result = verdict.FinalizeVerdict(result)   // new: derive verdict + append noise_cluster if applicable
// envelope is built from `result` directly; no separate prependClamp
// call afterwards — the clamp is already at result.Findings[0].
```

`check_progress` and `validate_completion` get the same pipeline shape: insert any clamp finding into `result.Findings[0]` BEFORE `FinalizeVerdict`, then build the envelope from `result` without a trailing `prependClamp` step.

Rationale: today's `prependClamp(env, cc.Clamp)` runs at handlers.go line 148 AFTER the envelope is built from `result.Findings`. If `FinalizeVerdict` runs on `result` (before envelope construction), the clamp wouldn't yet be in `result.Findings` and wouldn't contribute to the severity ladder. v0.5.2 moves clamp insertion ahead of finalization so a clamp's `minor` severity is counted alongside any other minors (matters for the new `≥3 minor → warn` rule).

For `validate_plan`, derivation runs per-task AND at the plan level after any plan-side rollups. The helper signature is symmetric:

```go
// FinalizeVerdict mutates r in place:
//   - Sets r.Verdict per the severity-ladder rule.
//   - Appends a `noise_cluster` advisory finding (severity: minor,
//     category: other, criterion: noise_cluster) when the ≥3-minor → warn
//     branch fires AND no critical/major exists.
// Idempotent: calling twice produces the same envelope (the
// noise_cluster advisory is itself counted at minor severity, so the
// helper checks for an existing noise_cluster before appending).
func FinalizeVerdict(r Result) Result

// FinalizePlanVerdict derives per-task and plan-level verdicts from
// the current findings AND re-applies ApplyPlanQualitySanity so
// PlanQuality stays consistent with the freshly-derived PlanVerdict
// (e.g. a reviewer-emitted `plan_quality: rigorous` becomes `rough`
// if finalization concludes `plan_verdict: fail`). Pure derivation
// without the sanity rerun would leave PlanQuality stale — ApplyPlanQualitySanity
// runs at parse time, before any handler-level finding mutation
// (suppression, rollup) and before the ladder runs, so the parse-time
// PlanQuality value reflects the reviewer's verdict, not the
// server-finalized one. FinalizePlanVerdict therefore:
//   1. derives per-task verdicts via the same severity-ladder helper,
//   2. derives plan-level verdict from PlanFindings,
//   3. appends noise_cluster advisories per the ladder (at task level
//      and plan level when applicable),
//   4. re-runs ApplyPlanQualitySanity(pr) so PlanQuality is consistent.
// Idempotent like FinalizeVerdict.
func FinalizePlanVerdict(p *PlanResult)
```

The reviewer's own `verdict` field is parsed by `Parse` for schema validation (must be one of `{pass, warn, fail}`) but is overwritten by `FinalizeVerdict`. This is documented in `verdict.go` and in INTEGRATION.md so callers know the reviewer's claimed verdict is advisory.

### Server-synthesized envelopes

Several envelopes are constructed by handler code without going through the reviewer-response parser. These split into two categories with different finalization treatment:

**Category A — hard rejections (built directly as `mcpsrv.Envelope`, no `verdict.Result` intermediate).**

These envelopes (`notFoundEnvelope`, `tooLargeEnvelope`, the `malformed_evidence` rejection envelope, etc.) set `Verdict: VerdictFail` explicitly and carry one synthetic finding describing why. They do NOT go through `FinalizeVerdict` (the helper's signature is `Result → Result`; these envelopes are `mcpsrv.Envelope` and never become a `Result`). Adding a parallel `finalizeEnvelopeVerdict(env Envelope) Envelope` helper is YAGNI — the rejection envelopes already set the correct verdict.

The MANDATORY adjustment for this category: bump the severity of each rejection's synthetic finding to `critical` so that IF a caller (or a future code path) ever derived a verdict from the envelope's findings list, the ladder would produce `fail` (≥1 critical → fail). Without this, the finding+verdict pair becomes self-inconsistent under the new ladder (verdict=fail but findings=[1 major] would derive to warn). Required severity bumps:

- `malformed_evidence` rejection finding → `critical` (was `major`)
- `payload_too_large` rejection finding → `critical` (was `major`)
- `session_not_found` rejection finding → `critical` (was `major`)

The rejection envelope's `Verdict` field stays `fail` — already correct under the new ladder; no helper invocation needed.

**Category B — envelopes that DO carry a `verdict.Result` (or `PlanResult`) under the hood.**

- **`max_tokens_override` clamp** — today appended to an envelope AFTER it is built (via `prependClamp` at handlers.go:148, :345, :908, etc.). v0.5.2 changes the ordering: the clamp is prepended into `result.Findings` BEFORE `FinalizeVerdict` runs, so its `minor` severity participates in the ladder (matters for the `≥3 minor → warn` rule). The trailing `prependClamp(env, ...)` calls are removed in the per-task handlers and the validate_plan handler; clamp insertion lives in the new path. (`prependClamp` may stay as a helper used only on hard-rejection envelopes, where the clamp is appended for display but the envelope's `Verdict: fail` is already correct and finalization is not invoked.)
- **Partial recovery — success path** (`ParseResultPartial` / `ParsePlanResultPartial` recovered at least one complete finding) — the parser produced a Result; the handler passes that Result through `FinalizeVerdict` / `FinalizePlanVerdict` like a normal parse. The synthetic truncation marker stays `minor`; the recovered findings + the marker go through the ladder.
- **Partial recovery — no-recovery fallback** (`truncatedEnvelope` for per-task, `truncatedPlanResult` for plan-level): MANDATORY severity bump from `minor` → `major` on the single synthetic truncation finding. Today's per-task `truncatedEnvelope` (handlers.go:390–403) emits `Verdict: warn` with a single `minor` `reviewer_response` finding — under the new ladder, a single `minor` derives to `pass`, leaving the explicit `Verdict: warn` self-inconsistent with the findings. Bump the finding to `major` so the ladder derives `warn` from it (≥1 major → warn), then either (a) let `FinalizeVerdict` derive the verdict so the assignment lines up, or (b) keep the explicit `Verdict: warn` and rely on derivation parity — both produce `warn`. Apply the same bump to the plan-level `truncatedPlanResult` no-recovery branch. NOT a hard rejection (still `warn`, not `fail`) — the caller can retry with a higher `max_tokens_override`.
- **Cache hits** on `validate_plan` — return the cached envelope unchanged. The cache stores a previously-finalized envelope (the cache key includes the prompt, model, mode, and token budget; the cached envelope was finalized on its original computation), so re-running finalization on a cache hit would be a no-op anyway.

### Caller contract

The contract `verdict ∈ {pass, warn, fail}` is unchanged. Callers parsing `verdict` continue to get one of those three values. Externally-visible changes:

- Borderline cases redistribute (single-minor was sometimes `fail`; now `pass`; three-minor was sometimes `pass`; now `warn`).
- Hard rejections (`malformed_evidence`, `payload_too_large`, `session_not_found`) continue to produce `fail` via the bumped-to-`critical` synthetic findings.

### Files touched

- `internal/verdict/verdict.go`: add `FinalizeVerdict(r Result) Result` helper. Implements the ladder + idempotent `noise_cluster` advisory append.
- `internal/verdict/plan.go`: add `FinalizePlanVerdict(p *PlanResult)`. Walks each task's findings (calls `FinalizeVerdict`-equivalent on each task), then computes plan-level verdict from `PlanFindings`.
- `internal/mcpsrv/handlers.go`: in each per-task handler (`ValidateTaskSpec`, `CheckProgress`, `ValidateCompletion`), reorder the clamp-insertion: prepend `cc.Clamp` (and the `validate_plan` `prependPlanClamp` equivalent) into `result.Findings` / `pr.PlanFindings` BEFORE invoking `FinalizeVerdict` / `FinalizePlanVerdict`, then build the envelope from the finalized result without a trailing `prependClamp(env, ...)` call. Bump the severity of `malformed_evidence` / `payload_too_large` / `session_not_found` synthetic findings to `critical`. Bump the severity of the `truncatedEnvelope` (handlers.go:390–403) synthetic `reviewer_response` finding from `minor` to `major` so the new ladder produces `warn` consistently; apply the same bump to the plan-level `truncatedPlanResult` no-recovery branch. The existing `prependClamp` / `prependPlanClamp` helpers may remain in use for hard-rejection envelopes only (where the envelope's `Verdict: fail` is already correct and finalization is not invoked) — confirm by grepping every call site after the refactor.
- `internal/mcpsrv/handlers_test.go`: tests confirming verdict derivation runs AFTER suppression (a reviewer response with 5 scope_drift findings, all of which match a `testability_extractions` entry, finalizes to `pass`, not `fail` — both suppression AND derivation must have happened before the envelope returns).
- `internal/verdict/verdict_test.go`: parametric table-driven test of the ladder. Includes idempotence test (`FinalizeVerdict(FinalizeVerdict(r))` is equal to `FinalizeVerdict(r)`).

## Session-propagated normative bodies (#2)

### Current behavior

`validate_task_spec` accepts `normative_test_bodies []string` as input. The pre-hook handler writes the value to the session as `Session.TaskSpec.NormativeTestBodies`. `pre.tmpl` renders the bodies. `validate_completion` reads the session by `session_id` but does not surface the bodies in `post.tmpl`.

### New behavior

The post-hook renders the bodies before the diff/files block. The reviewer is instructed to treat them as authoritative for fixture state, exact strings, and assertions.

The data is already plumbed: `prompts.PostInput.Spec` is `session.TaskSpec` (see `internal/prompts/prompts.go:40-50`), `TaskSpec` already carries `NormativeTestBodies []string` (added in v0.5.0; see `internal/session/session.go:23`), and `ValidateCompletion` already passes the looked-up session's `Spec` into the prompt-render. The minimal change is a `post.tmpl`-only edit that adds a conditional section referencing `.Spec.NormativeTestBodies`. NO handler change. NO struct change. NO new field on `prompts.PostInput`.

`post.tmpl` gains a new section, rendered only when `len(.Spec.NormativeTestBodies) > 0`. It belongs immediately after the existing exit-contracts block (or, if exit-contracts is also empty, immediately after the `## Task spec` header block):

```
{{if .Spec.NormativeTestBodies}}## Normative test bodies (binding)

The following test bodies were declared as binding when the implementer started this task. They are authoritative for fixture state, exact strings, and assertions. The AC list above is authoritative for behavior — but when an AC and a normative body appear to disagree on a fixture value, the body wins.

Do NOT flag AC-vs-fixture mismatches when a normative body explicitly pins the value.

{{range .Spec.NormativeTestBodies}}---
{{.}}
---
{{end}}
{{end}}
```

Lightweight mode (empty `session_id`, no session lookup) constructs a `PostInput` with an empty `Spec`, so `.Spec.NormativeTestBodies` is nil → the section renders as nothing → no behavior change.

### Files touched

- `internal/prompts/templates/post.tmpl`: add the conditional `## Normative test bodies (binding)` section. No handler change.
- `internal/prompts/testdata/post_*.golden`: regenerate (intentional template change).
- `internal/prompts/prompts_test.go`: add `Contains` assertions that, when `PostInput.Spec.NormativeTestBodies` is non-empty, the rendered prompt contains the section header and each body verbatim. Add a converse test: when empty, the section header does NOT appear (no orphan `## Normative test bodies (binding)` heading).
- `internal/mcpsrv/handlers_test.go`: add a session-round-trip test (`ValidateTaskSpec` stores bodies → `ValidateCompletion` reviewer prompt contains them).

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

`checkEvidenceShape` (`internal/mcpsrv/handlers.go` around line 711) rejects `validate_completion` inputs containing known placeholder/truncation markers. The walker checks BOTH `final_diff` AND every `final_files[].content` against the same `evidenceTruncationPatterns` slice, plus a separate `evidenceEllipsisLine` regex for lone-`...` lines, plus an empty-`final_files[].path` check. Rejected inputs short-circuit before the reviewer is invoked; rejections cache by canonical content hash for 5 minutes.

### New behavior

Extend the `evidenceTruncationPatterns` slice with six additional patterns observed in the field:

1. `/* ... */` (C-style block comment with ellipsis only)
2. `/* ...rest unchanged */` and variants with `rest unchanged` inside a block comment
3. `// snip`
4. `// elided`
5. `// ... rest unchanged` (line comment variant — distinct from the existing `// ... unchanged` in the inner spacing)
6. `/...` (forward-slash-ellipsis as a standalone substring; targets the field-observed "/..." abbreviation pattern)

Each is a lowercased literal substring (the walker `strings.ToLower`'s the input and compares against lowercase patterns). The walker is unchanged; only the pattern slice grows. Because the same walker iterates both `final_diff` and each `final_files[].content`, the new patterns auto-cover BOTH inputs — no separate code path needed.

The finding shape stays identical to existing rejections (the `Evidence` string already names which input the pattern matched and at which offset, e.g. `"final_files[2].content (path \"foo.go\") contains truncation marker \"/...\" at offset 47"`). Severity stays `critical` per the #1 verdict-ladder bump (was `major` — bumped so the synthetic finding produces `fail` under the new ladder; documented in the verdict-ladder section above).

### Files touched

- `internal/mcpsrv/handlers.go`: extend the `evidenceTruncationPatterns` slice; bump synthetic finding severity to `critical`.
- `internal/mcpsrv/handlers_test.go`: parametric per-pattern test asserting rejection fires AND that each new pattern is checked on BOTH `final_diff` AND `final_files[].content`. Bump expected severity to `critical` in any pre-existing tests that asserted `major`.

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

`pre.tmpl` gains a new section, rendered only when `len(.Spec.HarnessShapeAttestations) > 0`. (The template context is `prompts.PreInput`, which carries spec data at `.Spec.*` — see how existing fields like `.Spec.AcceptanceCriteria`, `.Spec.NonGoals`, `.Spec.PinnedBy` are referenced in `pre.tmpl`.)

```
## Harness shape attestations (caller-attested)

The controller has declared the following shape facts about test harnesses or fixtures referenced in this task. Treat each attestation as authoritative context — the controller has verified the assertions before dispatch. The reviewer does NOT independently verify these against the codebase.

These attestations are NOT exhaustive. An assertion list states what the controller confirmed; the absence of a capability from the list means "not asserted," NOT "forbidden." Do NOT flag ACs merely because they depend on a capability that isn't in the list.

Flag ONLY EXPLICIT contradictions:

  (a) An AC asks the implementer to do something a `does not …` (or analogous negative) assertion explicitly forbids.

  (b) An AC asserts a fixture state, value, or invariant that DIRECTLY contradicts a stated positive assertion (e.g. attestation says "records emitted spans"; AC says "no spans should be recorded").

When (a) or (b) holds, emit a finding with:
  - `category: attestation_contradiction`
  - `severity: major` (or `critical` for a structural contradiction that prevents the task from being implementable)
  - `criterion: <which attestation is contradicted, quoted>`
  - `evidence: <which AC contradicts it, quoted>`
  - `suggestion: <revised AC or explicit harness change request>`

{{range .Spec.HarnessShapeAttestations}}- **{{.Harness}}** (at `{{.Path}}`):
{{range .Assertions}}    - {{.}}
{{end}}{{end}}
```

### Files touched

- `internal/session/session.go`: define the `HarnessShapeAttestation` struct type here AND add `HarnessShapeAttestations []HarnessShapeAttestation` to `TaskSpec` (alongside the v0.5.0 `NormativeTestBodies` field). The struct must live in `session` (or a lower-level package) — `mcpsrv` already imports `session`, so defining the type in `mcpsrv` would create an import cycle when `session.TaskSpec` references it. Struct definition with explicit JSON tags (since this type is exposed to MCP clients through reflection on `ValidateTaskSpecArgs`, the tags pin the caller-visible field names; without tags, reflection may surface capitalized Go names like `Harness` / `Path` / `Assertions` in the generated MCP input schema instead of the documented lowercase ones):

  ```go
  // HarnessShapeAttestation declares a caller-attested shape fact about a
  // test harness or fixture referenced in a task spec. See INTEGRATION.md
  // §3 for the use case and §6 (or wherever finding categories are listed)
  // for the matching attestation_contradiction output category.
  type HarnessShapeAttestation struct {
      Harness    string   `json:"harness"`
      Path       string   `json:"path"`
      Assertions []string `json:"assertions"`
  }
  ```

  The `TaskSpec` field uses the existing pattern: `HarnessShapeAttestations []HarnessShapeAttestation \`json:"harness_shape_attestations,omitempty"\``.
- `internal/mcpsrv/task_spec_input.go`: add `normalizeHarnessShapeAttestation([]session.HarnessShapeAttestation) ([]session.HarnessShapeAttestation, error)` — the normalizer consumes/produces the `session`-defined type. Mirrors the existing `normalizeBoundedStringList` pattern (trim, dedup, cap counts and rune lengths).
- `internal/mcpsrv/handlers.go`: add `HarnessShapeAttestation []session.HarnessShapeAttestation \`json:"harness_shape_attestation,omitempty"\`` to `ValidateTaskSpecArgs`. The `mcp.AddTool` registration in `server.go` reflects on this args struct to generate the MCP tool's input schema — no edit to `server.go` or to `validateTaskSpecTool()` (which only sets Name + Description) is needed; the schema is auto-derived from the struct tags. Both the outer `json:"harness_shape_attestation,omitempty"` tag on the args field AND the per-field tags on the `session.HarnessShapeAttestation` struct (`json:"harness"`, `json:"path"`, `json:"assertions"`) are required to keep the public MCP input shape exactly `{"harness_shape_attestation": [{"harness": ..., "path": ..., "assertions": [...]}]}`. Thread the normalized value through `normalizeTaskSpecInputs` into `session.TaskSpec.HarnessShapeAttestations` and into the pre-hook prompt context.
- `internal/prompts/prompts.go`: `PreInput.Spec` already passes through `session.TaskSpec`, so the new field is automatically visible to the template — no struct change to `prompts.PreInput`.
- `internal/prompts/templates/pre.tmpl`: add the new `## Harness shape attestations` section.
- `internal/prompts/testdata/pre_*.golden`: regenerate.
- `internal/prompts/prompts_test.go`: assertions for the new section's header + each rendered attestation's fields.
- `internal/mcpsrv/handlers_test.go`: input validation tests (caps, dedup, empty handling) + session round-trip + reviewer-prompt context test + tool-registration test confirming the MCP input schema exposes `harness_shape_attestation`.
- `internal/verdict/schema.json`, `internal/verdict/plan_schema.json`, `internal/verdict/tasks_only_schema.json`, `internal/verdict/plan_findings_only_schema.json`: add `"attestation_contradiction"` to the `category` enum (4 files, identical one-line addition each). v0.5.1's `schema_invariants_test.go` re-runs and continues to pass.
- `internal/verdict/verdict.go`: add `CategoryAttestationContradiction Category = "attestation_contradiction"` constant alongside the existing category constants.
- `internal/verdict/parser_partial.go`: where `applySeverityFloor` is defined (around line 9–22), update its doc comment to clarify that `attestation_contradiction` is intentionally NOT floored — distinct from `convention_deviation` / `unverifiable_codebase_claim` which ARE floored. No code change to `applySeverityFloor` itself; the function only ever floors categories it explicitly tests for, and `attestation_contradiction` is not on that list.
- `internal/verdict/parser.go`: include `CategoryAttestationContradiction` in the `validCategory` switch (around line 59 alongside `CategoryConventionDeviation` / `CategoryOther`). Without this, the parser rejects the reviewer's category as unknown.
- `INTEGRATION.md` §3 and §4.2: document the new input alongside `pinned_by` / `controller_verified_references` / `normative_test_bodies`. Document the new `attestation_contradiction` finding category in §6 FAQ or wherever the finding categories are listed.
- `README.md`: extend the v0.5.0-era list of `validate_task_spec` optional inputs from four to five.

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

- `### Added`:
  - `harness_shape_attestation` input on `validate_task_spec` (one bullet describing shape + caps).
  - `attestation_contradiction` finding category (NOT severity-floored — distinct from `convention_deviation`). Added to all four reviewer-output schemas and to the parser's `validCategory` allowlist.
- `### Changed`:
  - Verdict-severity ladder (server-computed; pass/warn/fail thresholds documented). Synthetic-finding severities adjusted to preserve fail-grade semantics for hard rejections (`malformed_evidence`, `payload_too_large`, `session_not_found` bumped to `critical`).
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
- **New `attestation_contradiction` output category.** Callers that exhaustively switch on the `category` enum (e.g. for routing findings to different UI lanes) SHOULD be updated to handle `attestation_contradiction` generically. The prompts instruct reviewers to emit this category only when a task supplies `harness_shape_attestation`, so callers that do not pass that input should not normally see it — but since the category sits in all four reviewer-output schemas (required for the OpenAI strict-mode invariant), the server cannot prevent a non-compliant reviewer from emitting it outside the documented trigger. Treat-unknown-categories-as-parseable is the safe default. The category enum landing in all four reviewer-output schemas is the trigger that makes this a publicly-observed change rather than purely internal.
- **`harness_shape_attestation`.** Net-new input field. Existing dispatch templates ignore it. Callers wanting the value update their dispatch clause to pass it from the task spec. Opting in is when the new `attestation_contradiction` output category becomes reachable (see above).
- **Session propagation.** A v0.5.1 caller that calls `validate_task_spec` then `validate_completion` continues to work; if they didn't pass `normative_test_bodies` they continue to get a post-hook prompt without that section. A v0.5.2 caller that did pass bodies at pre-hook automatically gets them rendered at post-hook.
- **Schema files.** All four `internal/verdict/*_schema.json` files add `"attestation_contradiction"` to their `category` enums (#6). No other output-schema changes. The OpenAI strict-mode invariant (#22 history: required-must-cover-properties) remains satisfied — verified by the v0.5.1 `schema_invariants_test.go` regression test, which runs on every PR.

## Testing strategy

Per-item, in addition to the unit tests already named above:

- **Verdict ladder:** parametric table-driven test with all severity-count combinations covering each branch boundary. Asserts `noise_cluster` advisory is appended only when the ≥3-minor → warn branch fires AND no critical / major are present.
- **Normative-body propagation:** integration-style test that simulates a `validate_task_spec` → `validate_completion` flow with the same `session_id`, asserts the post-hook prompt contains the bodies, asserts the lightweight (empty session_id) flow does not error and does not include the section.
- **Demotion advisory:** prompt golden + a reviewer-output parsing test that confirms a reviewer-emitted `minor` finding with `(resolved-by-normative-body: …)` suffix in `suggestion` is parsed as-is. Server doesn't re-derive severity.
- **Malformed-evidence patterns:** parametric per-pattern test asserting rejection and finding shape match.
- **CVR claim-level:** three-case table: (a) one substring matches a CVR entry → claim suppressed; (b) no substring matches → claim retained; (c) substring < 4 code points → not used for matching.
- **`harness_shape_attestation`:** input normalization (caps, dedup, error messages) + prompt-rendering golden + session round-trip + MCP tool-registration test confirming the field shows up in the registered tool's input schema. Mechanism note: the input schema is reflected from `ValidateTaskSpecArgs` struct tags by the `mcp.AddTool` registration in `internal/mcpsrv/server.go`; adding the field to the args struct with the correct `json:"harness_shape_attestation,omitempty"` tag is sufficient for MCP clients to see it. The test exercises the registration call site (no edits to `validateTaskSpecTool()` in `handlers.go`, which only carries Name + Description).
- **`.trimIndent()` heuristic:** prompt golden assertion (no reviewer-output test — the heuristic is a prompt instruction; the reviewer's compliance is tested via E2E only and remains off the per-PR path).
- **Docs nudge:** TWO targeted assertions, not a permissive count. (a) The lifecycle table row at INTEGRATION.md §4 contains the literal substring `test that 'should' fail`. (b) The implementer paste-clause's "During work" step also contains the literal substring `test that 'should' fail`. Exact count `grep -c` MUST equal 2 (failing if 1 or 3+ — catches accidental partial edits or duplicated paste).

Mainline run: `go test -race ./...` as always. E2E (`-tags=e2e`) remains opt-in.

## Open questions deferred to plan-time

- Whether `harness_shape_attestation` entries get surfaced verbatim in the reviewer's finding `evidence` field, or only quoted by short name. Design assumes verbatim quoting in `evidence` is acceptable since attestations are bounded (≤ 480 code points per assertion).
- The advisory wording for `noise_cluster` is a default; tightening it during implementation review is fine if it can stay under ~280 code points.

## References

- Issue #22 (https://github.com/patiently/anti-tangent-mcp/issues/22) — full field-feedback rollup.
- v0.5.0 design (`docs/superpowers/specs/2026-05-18-mcp-feedback-v0.5.0-design.md`) — added the original four optional `validate_task_spec` inputs and `normative_test_bodies` at pre-hook time.
- v0.5.1 INTEGRATION.md trim (`docs/superpowers/plans/2026-05-19-integration-md-trim.md`) — reduced INTEGRATION.md to 33,186 chars; informs CHANGELOG bullet sizing and the `.trimIndent()` §3.7 cross-reference shape.
- OpenAI strict-mode invariant regression test (`internal/verdict/schema_invariants_test.go`) — landed in v0.5.1; ensures any future schema field is added to both `properties` and `required`.
