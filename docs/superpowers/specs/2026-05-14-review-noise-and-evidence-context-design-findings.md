# Review findings — spec

Review of `2026-05-14-review-noise-and-evidence-context-design.md` against the companion plan and the current code in `internal/{mcpsrv,prompts,session,verdict,config}`.

## Design ambiguities the plan inherits

### 1. "Available to pre, mid, and post prompts" is under-specified for `pinned_by`

Spec §5:

> Thread it into `session.TaskSpec` so the value is available to pre, mid, and post prompts once a session is created

"Available" can mean either (a) carried on the struct so a future template change can render it, or (b) actually rendered in all three templates. The plan reads it as (a) and only updates `pre.tmpl`. But the motivating YN-10178 case — "existing behavior remains unchanged" ACs being flagged as ambiguous — is graded by `post.tmpl` during `validate_completion`, not by `pre.tmpl`.

Decide explicitly: does `post.tmpl` (and optionally `mid.tmpl`) render a `Pinned by:` block, and does the completion reviewer get the same "anchors are caller-supplied, not codebase facts" guidance? If yes, the spec should add §5 prompt-behavior text for the post template and the plan should add a `TestRenderPost_WithPinnedBy*` assertion plus a golden update. If no, the spec should narrow the "pre, mid, and post" phrasing to "pre only" so the implementer doesn't second-guess.

### 2. `plan_quality: rough` on no-analysis truncation — implementation mechanism is ambiguous

Spec §2:

> `plan_quality: rough` via sanity rules or explicit assignment, because no analysis occurred.

Either mechanism works, but they imply different code paths:

- **Explicit assignment** in `truncatedPlanResult()` is the minimal change and survives all `ApplyPlanQualitySanity` paths because `rough` is a valid value the sanity helper trusts.
- **Sanity rule** means adding a new branch to `ApplyPlanQualitySanity` that detects "Warn + only the truncation marker finding" and forces `rough` — broader behavioral surface, more tests to add.

The plan's Task 2 Step 7 picks neither and silently lands at `actionable` (Warn + empty PlanQuality → default switch → actionable). Pick one path in the spec so the plan doesn't have to guess.

### 3. Clamp ordering for adaptive budget is left to the plan

Spec §1 introduces `effectivePlanMaxTokens(args, cfg, taskCount)` but does not specify where in `ValidatePlan`'s control flow it slots in. The current code computes `clamp` early (before `SplitTasks`) so payload-too-large and no-headings exits can thread it via `prependPlanClamp`. Moving the helper past `SplitTasks` (required for `taskCount`) loses that wiring unless an early `effectiveMaxTokens` call is preserved.

The plan flags this in Task 2 Step 3 but punts the decision. Either:

- The spec says "preserve the early `effectiveMaxTokens` call for clamp/negative rejection; the new helper computes the adaptive floor only when override == 0", or
- The spec says "the new helper subsumes the early call; clamp findings no longer apply to the too-large / no-headings exits (a deliberate simplification)."

### 4. Existing plan-level `unverifiable_codebase_claim` merge is unresolved

Spec §3:

> Preserve any existing plan-level `unverifiable_codebase_claim` finding, but merge its evidence into the same rollup when practical. If merging would become unwieldy, keep plan-level findings but still calibrate verdicts using the rule in section 4.

"When practical" is a deferred decision. The plan does not implement the merge, and no test covers reviewer-emitted plan-level unverifiable + task-level unverifiable interactions. Spec open question 2 already flags this. Either:

- Drop the "merge when practical" phrasing and commit to "task-level rollup is appended; reviewer-emitted plan-level unverifiable findings are preserved as-is" (the plan's actual behavior), or
- Add a deterministic merge rule (e.g. "if any reviewer-emitted plan-level unverifiable exists, append the task-rollup evidence to its `Evidence` field rather than creating a new finding").

### 5. `phase: post` session creation semantics

Spec open question 4:

> Should `phase: post` create a session exactly like `pre`, or should the session carry `Phase` only for observability and prompt context? This design assumes the latter.

"Assumes the latter" is consistent with the plan's implementation (session created normally; `Phase` stored on `TaskSpec`). Promote this from an open question to a decision in §6 so a future reader doesn't reopen it.

## Scope clarifications worth tightening

### 6. `pinned_by` and `phase` are not mirrored on `validate_plan`

Spec scope explicitly excludes adding `pinned_by` to `validate_plan` (out-of-scope and Task 1 non-goal). The reasoning isn't stated. The plan-level analog would be useful — a plan-author saying "Task 3's 'consistent with X' AC is pinned by tests Y/Z" could suppress the rollup checklist for that task. If the deferral is "needs a separate `verified_claims` design," say so once in §5 to head off future bug reports.

### 7. Referenced-path detection — extension list is conservative on purpose

Spec §7 names `.md`, `.txt`, `.json`, `.yaml`, `.yml`. The plan implements this verbatim. Worth a one-liner noting that source-code extensions (`.go`, `.kt`, `.py`, …) are intentionally excluded because they almost always appear in diffs even when not deliverables, and false-positive risk dominates. Otherwise a future "let's also flag .go" PR is inevitable and will undo the conservative choice for no benefit.

### 8. Rollup evidence cap is a magic number

Spec §3 says "truncated per task to a deterministic cap if needed." The plan picks `rollupEvidencePerTaskMax = 240`. Reasonable, but neither doc explains why 240 (vs. the `summaryEvidenceMax = 120` constant already in `summary.go`). State the rationale (e.g. "240 ≈ two lines of evidence per task, enough to identify symbols without dominating the rollup") and either reuse `summaryEvidenceMax` or document why a separate constant is correct.

## Code-grounded checks that hold up

- `internal/verdict/plan_parser.go:78-80` already floors `unverifiable_codebase_claim` to `SeverityMinor` at parse time. The plan's calibration assumes minor severity, which is structurally guaranteed.
- `internal/mcpsrv/handlers.go:482-495` (`truncatedPlanResult`) is the single function that needs the §2 changes — no duplicate paths to update.
- `internal/session/session.go:12-18` (`TaskSpec`) and `internal/mcpsrv/handlers.go:40-48` (`ValidateTaskSpecArgs`) are the only sites needing new fields; nothing serializes `TaskSpec` outside the session store, so adding `PinnedBy` / `Phase` is schema-safe.
- `internal/prompts/prompts.go:40-46` (`PostInput`) is purely additive on the new `ReferencedPathsMissingEvidence` field.
- `internal/config/config.go:25-60` already exposes `PlanMaxTokens` (4096) and `MaxTokensCeiling` (16384) — the spec's `2000 + 800*8 = 8400` example correctly lands between them.
- The "Files touched" section matches every file the plan actually modifies; no orphans either way.

## Recommended next step

Close ambiguities 1–3 with explicit spec edits before the plan is dispatched. They are the three the plan demonstrably trips on. Ambiguities 4–8 can ride along during implementation but are worth resolving while the design is fresh rather than at PR-review time.
