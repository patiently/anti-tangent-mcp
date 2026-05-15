# Review findings — plan

Review of `2026-05-14-review-noise-and-evidence-context.md` against the companion spec and the current code in `internal/{mcpsrv,prompts,session,verdict,config}`.

## Blocking gaps

### 1. `plan_quality: rough` missing on no-analysis truncation

Spec §2 requires the truncated-plan path to land at `plan_quality: rough`. Task 2 Step 7 updates `truncatedPlanResult()`'s Severity / Suggestion / NextAction but never touches `PlanQuality`.

Tracing the current code with the planned change applied:

- `truncatedPlanResult()` (`internal/mcpsrv/handlers.go:482-495`) leaves `PlanQuality` empty.
- After Step 7 the single finding becomes `SeverityMajor` (no critical).
- `ApplyPlanQualitySanity` (`internal/verdict/plan_parser.go:95-137`) sees `PlanVerdict==Warn`, no critical findings anywhere, and empty `PlanQuality`. The default branch maps `Warn → PlanQualityActionable`.

Net effect: a no-analysis truncation surfaces as `actionable`, not `rough`. The plan's Task 2 Step 5 assertions never check `PlanQuality`, so this would ship silently broken.

**Fix:** set `PlanQuality: verdict.PlanQualityRough` explicitly in `truncatedPlanResult()` (or extend `ApplyPlanQualitySanity` to force rough when the truncation marker is the only finding), and add a `PlanQuality` assertion to Step 5.

### 2. Ambiguous clamp ordering in Task 2 Step 3

Step 3 says:

> Payload-too-large and no-heading exits can continue to use `effectiveMaxTokens` before splitting if needed for clamp composition; if you keep early clamp behavior, reject negative override before size/headings checks

This leaves a real design choice to the implementer. The current code (`handlers.go:937`) calls `effectiveMaxTokens` early, *before* `planparser.SplitTasks`, and threads the `clamp` finding into `tooLargePlanResult` / `noHeadingsPlanResult` via `prependPlanClamp`. Moving the helper after splitting (required to know `taskCount`) drops that wiring unless an early call is preserved.

Two implementers will reasonably disagree on which path to take, and no test pins the behavior on the too-large / no-headings paths. Pick one and add an assertion.

### 3. `pinned_by` never reaches `post.tmpl`

Spec §5 says pinned_by "is available to pre, mid, and post prompts once a session is created." The plan adds `PinnedBy` to `session.TaskSpec` (Task 1 Step 3) and to `PreInput` (implicitly via `Spec`), but only `pre.tmpl` is updated.

`post.tmpl` (`internal/prompts/templates/post.tmpl:1-14`) renders `.Spec.Title`, `.Spec.Goal`, `.Spec.AcceptanceCriteria`, `.Spec.NonGoals`, `.Spec.Context` only. The validate_completion reviewer never sees the pinned anchors. The YN-10178 motivating case — "existing behavior remains unchanged" ACs — is graded by `post.tmpl`, not `pre.tmpl`, so the field-report problem only gets partly solved.

**Resolve before dispatch** by either:

- Adding `{{if .Spec.PinnedBy}}Pinned by: …{{end}}` to `post.tmpl` (and a `TestRenderPost_WithPinnedBy*` golden assertion in Task 1 / Task 4), or
- Narrowing the spec's "pre, mid, and post" wording to "pre only" and confirming the YN-10178 unchanged-behavior fix is scoped to spec validation only.

## Smaller issues

### 4. Plan-level existing `unverifiable_codebase_claim` findings aren't merged

Spec §3 prefers merging an existing reviewer-emitted plan-level unverifiable finding into the rollup "when practical." `normalizePlanUnverifiableFindings` (Task 3 Step 3) only iterates `pr.Tasks[i].Findings`. If the reviewer emits a plan-level unverifiable, it stays alongside the appended rollup, producing a near-duplicate.

`allPlanFindingsAreMinorUnverifiable` correctly counts both for calibration purposes, so this is UX/redundancy, not a logic bug. Spec open question 2 already flags the ambiguity — the plan should either implement the merge or explicitly defer with a comment, and at minimum add a test case covering "reviewer returns plan-level unverifiable + task-level unverifiable" so future merge work has a regression target.

### 5. Slice-alias mutation in `normalizePlanUnverifiableFindings`

Signature `func normalizePlanUnverifiableFindings(pr verdict.PlanResult) verdict.PlanResult` implies pure-function semantics. The body uses `kept := pr.Tasks[i].Findings[:0]` and `pr.Tasks[i].Findings = kept`. Because `pr.Tasks` is a slice header copied by value but sharing the underlying array, this mutates the caller's `Tasks[i].Findings` backing array.

Harmless at the single call site in `planEnvelopeResult` today, but the signature lies. Pick one:

- Take `*verdict.PlanResult` to make the mutation explicit, or
- Allocate `kept := make([]verdict.Finding, 0, len(pr.Tasks[i].Findings))`.

### 6. Ordering coupling in `planEnvelopeResult`

The plan inserts `normalizePlanUnverifiableFindings` and `calibratePlanVerdictForUnverifiableOnly` *before* `verdict.ApplyPlanQualitySanity(&pr)`. The "preserve rigorous" branch in the calibration helper relies on inspecting the reviewer's emitted `PlanQuality` before sanity rules apply (sanity wouldn't change a valid `rigorous`, but it would overwrite an empty value). A future refactor reordering these would silently break the rigorous-preservation contract.

Add a one-line comment at the call site noting the order is load-bearing.

### 7. Test coverage gaps

- No assertion that `PlanQuality` is anything specific on the truncation path (gap #1).
- No test of the too-large / no-headings clamp interaction with adaptive budgets (gap #2).
- No test of plan-level reviewer-emitted unverifiable findings interacting with rollup (gap #4).
- `TestValidatePlan_RollsUpTaskUnverifiableFindings` uses 2 tasks and `buildPlanWithNTasks(2)` — at default `PlanTasksPerChunk=8`, this stays on the single-call path. The rollup logic also needs to behave correctly when the chunked path stitches results across passes. Worth one chunked-path rollup test (≥ 9 tasks) given chunking is the YN-10178-shaped scenario.

### 8. Branch convention not called out

`CLAUDE.md` requires a `version/0.4.0` branch and a matching `## [0.4.0]` CHANGELOG entry (CI enforced). The plan adds the CHANGELOG entry in Task 5 but never instructs creating the branch. Currently on `main`. One line in the File Structure section or Task 6 would close this.

### 9. `effectivePlanMaxTokens` API inconsistency

Signature `effectivePlanMaxTokens(args ValidatePlanArgs, cfg config.Config, taskCount int)` takes the entire args/cfg structs; the existing `effectiveMaxTokens` takes primitive ints. Mild stylistic drift, not blocking. Either align (pass `override int, defaultMax int, ceiling int, taskCount int`) or document why the new helper wants the full structs.

## What holds up well

- The `max(PlanMaxTokens, min(MaxTokensCeiling, 2000 + 800*taskCount))` formula matches the spec exactly; the 8-task test value (8400) is correct given the asserted config.
- `calibratePlanVerdictForUnverifiableOnly` correctly preserves `rigorous` per spec §4.
- The referenced-path regex `[A-Za-z0-9_./-]+\.(?:md|txt|json|ya?ml)\b` matches the spec; `\b` handles trailing punctuation in summaries (e.g. "Created docs/audit.md.").
- Validation order for `pinned_by` / `phase` (handler-boundary rejection before provider cost) matches spec §5/§6.
- The Task 4 detection runs *after* the at-least-one-evidence guard, so the lightweight-completion path stays intact.
- The new `pinned_by` empty-vs-present template gating in `pre.tmpl` (Task 1 Step 8) is faithful to the spec text.

## Recommended next step

Close gaps 1–3 before dispatching. They are concrete, would survive into shipped behavior, and the tests as written do not catch them. Gaps 4–9 can be addressed during implementation or deferred with explicit comments.
