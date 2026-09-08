# anti-tangent-mcp v0.19.0 — spec-gate convergence

**Status:** design
**Date:** 2026-09-08
**Issue:** [#58](https://github.com/patiently/anti-tangent-mcp/issues/58) — `validate_task_spec` rarely converges to `pass`

## Summary

`validate_task_spec`'s verdict carries almost no decision value. Across the two dogfooding cases
in #58 the verdict never reached `pass`, and in both cases the implementer proceeded on its own
judgement — including the case where the tool was right. This release does three things:

1. Adds `spec_quality` (`rough` / `actionable` / `rigorous`) as a second axis on
   `validate_task_spec`, mirroring `plan_quality` on `validate_plan`, so a caller facing a
   non-moving `warn` has a convergence signal and a stopping rule.
2. Adds `normative_code_bodies` — the sibling of `normative_test_bodies` for non-test
   implementation code pasted verbatim into a task brief — so a brief that already answers a
   question in code stops being failed for leaving it open in prose.
3. States the expected terminal state in the protocol docs, which today imply `pass` is
   reachable when it is not.

The severity ladder in `internal/verdict/finalize.go` is **not** changed. See
[Non-goals](#non-goals) for why, and [Residual](#residual-what-this-release-does-not-fix) for what
that leaves unfixed.

## Diagnosis

#58 reads the non-convergence as a reviewer-quality problem. It is not. `pass` is close to
structurally unreachable, and two pieces of the system combine to make it so.

**The ladder.** `FinalizeVerdict` (`internal/verdict/finalize.go:15`) discards the reviewer's
verdict and re-derives it from the finding mix:

```
critical >= 1 OR major >= 2  → fail
major >= 1 OR minor >= 3     → warn
otherwise                    → pass
```

So `pass` requires **zero major and at most two minor findings, total.**

**The prompt.** `pre.tmpl`'s third evaluation instruction is an open-ended enumeration:

> 3. Implicit assumptions — list any assumptions a fresh implementer would have to make.
> **Each becomes a finding** so the spec author can either pin them down or explicitly mark them
> as implementer's discretion.

These two contradict each other. The prompt asks a reviewer to enumerate every implicit
assumption in a task brief; the ladder converts the third one into `warn`. There is no
well-specified task for which a competent reviewer finds two or fewer implicit assumptions. Case
B's `fail → fail → warn → warn` is not a reviewer failing to converge — it is a reviewer
converging correctly onto a floor the ladder puts at `warn`.

Two details sharpen this:

- **The unverifiable-claim path is already mitigated and is not the cause.**
  `normalizeTaskSpecUnverifiableFindings` (`internal/mcpsrv/task_spec_normalize.go:9`) rolls every
  `unverifiable_codebase_claim` up into a single `codebase_reference_checklist` minor before
  finalization runs. Naming a lot of real code costs one minor, not many. The residual generator
  of minors is the assumptions clause.
- **The design already half-knows the conclusion.** `FinalizeVerdict` appends a `noise_cluster`
  advisory exactly when minors ≥ 3 with no critical or major, and its own evidence string says
  "each finding is individually advisory; the cluster lifts verdict to warn." The authors
  recognised that a minor cluster is low-signal, and lifted the verdict on it regardless.

**Case A is a different failure.** Its four findings were `major`, not minor, and were
`ambiguous_spec` / `missing_acceptance_criterion` / `quality` about prose that the brief's own
verbatim Go answered directly below it. `normative_test_bodies` exists for exactly this shape but
covers only test code; there is no channel for non-test implementation code supplied in a brief.
That gap is Part 2.

## Non-goals

- **Changing the severity ladder.** Retuning `minor >= 3 → warn` for the pre-hook is the most
  direct fix and was explicitly considered and declined. The verdict is a published value: §4.2
  instructs implementers on how to read it, and `plugin/anti-tangent-guard` keys on the identical
  envelope format. Changing what an existing `warn` *means* breaks every caller that already
  calibrated against it, whereas adding an axis breaks none. The cost of declining it is recorded
  under [Residual](#residual-what-this-release-does-not-fix).
- **A second vocabulary.** `spec_quality` reuses `rough` / `actionable` / `rigorous` verbatim.
  `controller.md:96` already teaches those three words; a parallel set of names for the same idea
  would be gratuitous.
- **Deterministic server-side suppression for normative code bodies.** See
  [Part 2](#part-2--normative_code_bodies) — it is not decidable.
- **Persistent storage.** All new state lives on the existing in-memory `Session` and `Run` and
  expires with their TTLs.

## Part 1 — `spec_quality`

### The mechanism that fixes Case B

`ApplySpecQualitySanity` mirrors `ApplyPlanQualitySanity`
(`internal/verdict/plan_parser.go:172`) exactly, including the rule that matters most:

- `fail` verdict → `rough`
- any `critical` finding → `rough`
- empty or invalid value → verdict-derived default (`pass`→`rigorous`, `warn`→`actionable`,
  `fail`→`rough`)
- **otherwise a valid reviewer-emitted value is trusted**

That last rule is the whole fix. A spec sitting at `warn` because of three prose-level minors can
still be marked `rigorous` by the reviewer, and that is the stopping signal an implementer
currently has no way to see. The verdict is untouched; the second axis carries the information.

It runs **after** `FinalizeVerdict`, exactly as `FinalizePlanVerdict` already calls
`ApplyPlanQualitySanity` last (`internal/verdict/finalize.go`), so a reviewer-emitted `rigorous`
becomes `rough` when finalization concludes `fail`.

### Schema

A new `internal/verdict/task_spec_schema.json`, used only by `validate_task_spec`. This follows
the precedent of `plan_schema.json` / `prime_schema.json` / `extract_schema.json`, and leaves
`check_progress` and `validate_completion` on the existing shared `schema.json` with no change at
all.

`spec_quality` must be listed in the schema's `required` array — **not** optional.
`TestReviewerSchemas_RequireAllProperties_ForOpenAIStrictMode`
(`internal/verdict/schema_invariants_test.go:55`) asserts that every object node's `required` set
equals its `properties` set, because OpenAI structured-outputs strict mode demands it.
`plan_schema.json:5` lists `plan_quality` in `required` for the same reason. The new schema must
be added to `reviewerSchemas()` so all four invariant tests walk it.

### Result and parsing

`verdict.Result` gains:

```go
SpecQuality SpecQuality `json:"spec_quality,omitempty"`
```

This is **required**, not merely convenient: `Parse` (`internal/verdict/parser.go:13`) calls
`dec.DisallowUnknownFields()`, so a reviewer emitting `spec_quality` against a `Result` without
the field would fail to decode and burn the retry. `check_progress` and `validate_completion`
keep a schema that forbids the property, so they never populate it, and `omitempty` keeps it out
of their envelopes.

`h.review` (`internal/mcpsrv/handlers.go:228`) currently hardcodes `JSONSchema: verdict.Schema()`.
It gains a schema parameter rather than growing a near-duplicate `reviewTaskSpec` — the result
type is identical and only the constraint differs, so the `reviewPrime` / `reviewExtract`
precedent of a whole parallel function does not apply. All three call sites are updated
(`handlers.go:143` pre, `:476`, `:1638`); two pass `verdict.Schema()` unchanged.

### Surfacing

- `Envelope` (`internal/mcpsrv/handlers.go:30`) gains
  `SpecQuality string \`json:"spec_quality,omitempty"\``.
- `formatEnvelopeSummary` (`internal/mcpsrv/summary.go:31`) emits one `spec_quality:` line, after
  `verdict:`, through `escapeBlockValue` like every other value.
- `planrun.TaskRow` (`internal/planrun/planrun.go:27`) gains
  `PreSpecQuality string \`json:"pre_spec_quality,omitempty"\``, set at `handlers.go:199`
  alongside the existing `PreVerdict: env.Verdict`. Without it `plan_run_report` shows
  `pre_verdict: warn` for every task with no way to tell a converged spec from an abandoned one —
  the same blindness this release fixes at the call level. `Run` already carries `PlanQuality`,
  so this is symmetric with the existing design.

### Guard-plugin safety

Verified, not assumed. `plugin/anti-tangent-guard/hooks/check-task-complete:193-195` matches:

```python
HEADER_RE  = re.compile(r"(?m)^anti-tangent envelope$")
TOOL_RE    = re.compile(r"(?m)^\s*tool:\s*(\S+)\s*$")
VERDICT_RE = re.compile(r"(?m)^\s*verdict:\s*(\w+)")
```

`VERDICT_RE` is anchored at `^\s*verdict:`, so a `  spec_quality:  rigorous` line cannot match
it, and extraction is "first match within a block" rather than a fixed line number — so inserting
a line does not shift anything the hook reads. A regression test in
`internal/mcpsrv/summary_contract_test.go` pins this rather than leaving it to inspection.

## Part 2 — `normative_code_bodies`

The sibling of `normative_test_bodies` for non-test implementation code, which is the gap #58
names for Case A.

### Extraction

`planparser` gains the header `**NORMATIVE CODE (verbatim):**`.
`ExtractNormativeTestBodies` (`internal/planparser/normative_bodies.go:33`) is generalized to take
the header string as a parameter, with both public entry points delegating to it. The
fence-chaining, paragraph-fallback, CRLF-normalization, per-entry truncation and cap logic is
reused rather than reimplemented — that function has ten tests pinning edge cases
(`normative_bodies_test.go`) that must not be forked. Caps are shared and unchanged: 20 entries,
4000 runes per entry, `\n// truncated` marker.

### Plumbing

Follows the path `normative_test_bodies` already cut:

- `verdict.PlanTaskResult` gains `NormativeCodeBodies []string`, populated server-side by
  `populateNormativeTestBodies` (`internal/mcpsrv/handlers.go:2361`, also invoked from
  `internal/mcpsrv/review_error.go:189`), which is renamed to reflect that it now fills both.
- `internal/mcpsrv/plan_cache.go:180` deep-copies the new slice alongside the existing one.
- `ValidateTaskSpecArgs.NormativeCodeBodies` → `session.TaskSpec.NormativeCodeBodies`, normalized
  in `internal/mcpsrv/task_spec_input.go` under the same caps
  (`maxNormativeTestBodyEntries` / `maxNormativeTestBodyChars`, `task_spec_input.go:19-20`) and
  counted toward the payload total.
- `pre.tmpl` renders a "Normative code bodies (caller-supplied, treat as binding
  implementation)" section.
- `post.tmpl` renders the bodies too, which costs nothing beyond the template: the handler
  already passes the looked-up session's `Spec` into `PostInput`. This is the v0.5.2 precedent
  for `normative_test_bodies` (`post.tmpl:26`) — template-only, no handler or struct change.
  Lightweight mode (empty `session_id`) constructs an empty `Spec`, so the section renders as
  nothing.

### Suppression rule

`pre.tmpl` and `post.tmpl` today carry a downgrade rule for `major` + `ambiguous_spec` when a
normative *test* body pins the answer, tagging the suggestion
`(resolved-by-normative-body: …)`. The new rule mirrors it for code bodies, and covers
**both** `ambiguous_spec` and `missing_acceptance_criterion`, tagged
`(resolved-by-normative-code: …)`. Case A's four majors were exactly those two categories, so
covering only `ambiguous_spec` would leave half of it unfixed.

### Limitation, stated plainly

This is prompt-side only, and depends on reviewer compliance.
`controller_verified_references` gets a deterministic server-side suppression
(`suppressUnverifiableCodebaseClaim`) because substring-matching a path is decidable; "this
acceptance criterion's ambiguity is answered by that block of code" is not. The same limitation
already applies to `normative_test_bodies` and is not made worse here — but it means Part 2
reduces Case A rather than guaranteeing its absence.

## Part 3 — docs

- `implementer.md` §4.2: state the terminal state. The clause currently says to treat `critical`
  as blocking and `major` as address-or-explain, and stops — implying `pass` is the target.
  It gains the stopping rule: proceed at `warn` when `spec_quality` is `actionable` or better and
  the remaining findings are prose-level, and treat a non-moving `spec_quality` as the signal to
  stop iterating.
- `controller.md`: the convergence sentence for `spec_quality`, mirroring the one already at
  `controller.md:96` for `plan_quality`.
- `authoring.md`: a §3.6-shaped entry for the `**NORMATIVE CODE (verbatim):**` convention,
  written next to the existing normative-test-bodies entry.
- README: the new `validate_task_spec` input and the new response field.

**Size constraint.** CI enforces < 16,000 bytes per protocol part. Current sizes: `core.md`
15,891 (**109 bytes of headroom**), `implementer.md` 14,521, `controller.md` 12,645,
`project-knowledge.md` 10,401, `authoring.md` 6,878. No new prose may land in `core.md`. The
plugin bundle must be resynced in the same commit:

```bash
rm -f plugin/anti-tangent-protocol/protocol/*.md
cp docs/protocol/*.md plugin/anti-tangent-protocol/protocol/
```

## Testing

- `internal/verdict`: `ApplySpecQualitySanity` tests mirroring the existing `plan_quality` set
  (`plan_test.go:239-332`) — critical forces rough, fail forces rough, empty and invalid fall
  back per verdict, and **a valid `rigorous` survives a `warn` verdict**, which is the behavior
  the release exists to provide.
- `internal/verdict`: `task_spec_schema.json` added to `reviewerSchemas()`, so all four
  strict-mode invariants walk it.
- `internal/verdict`: `Parse` accepts `spec_quality` and still rejects genuinely unknown fields.
- `internal/planparser`: extraction tests for the new header mirroring
  `normative_bodies_test.go`, plus a test that the two headers extract independently from one
  task body.
- `internal/prompts`: golden files regenerated with `-update`; diff reviewed before commit.
- `internal/mcpsrv`: `summary_contract_test.go` regression pinning that the added
  `spec_quality:` line does not disturb `TOOL_RE` / `VERDICT_RE` extraction; integration coverage
  for the field round-tripping into the envelope and into `plan_run_report`.
- `go test -race ./...` throughout.

## Compatibility

Additive in every direction, so **0.19.0** — a minor bump. Branch `version/0.19.0`, merge commit
tagged `[minor]`, `CHANGELOG.md` entry written alongside the code.

- A caller that never passes `normative_code_bodies` gets today's behavior exactly.
- A caller that ignores `spec_quality` is unaffected; the verdict it already reads is unchanged
  for the same finding mix.
- `check_progress` and `validate_completion` request and parse the same schema they do today.
- An older server returns no `spec_quality`; a newer client must treat its absence as "unknown",
  not as `rough`.

## Residual: what this release does not fix

The ladder is unchanged by choice, so `pass` remains close to unreachable on any richly-specified
task: `minor >= 3 → warn` still collides with "each implicit assumption becomes a finding." This
design routes around that rather than repairing it. `spec_quality: rigorous` becomes the signal
to proceed and the docs say so, but the verdict field itself keeps carrying less information than
its name suggests.

That is a deliberate trade — the verdict is a published interface with at least two consumers
calibrated against it — and it should be revisited if `spec_quality` turns out in practice to be
a second thing callers have to learn to ignore rather than the stopping rule it is meant to be.

## References

- Issue [#58](https://github.com/patiently/anti-tangent-mcp/issues/58)
- `docs/superpowers/specs/2026-05-18-mcp-feedback-v0.5.0-design.md` — the original
  `normative_test_bodies` design and its caps
- `docs/superpowers/specs/2026-05-19-anti-tangent-v0.5.2-design.md` — session propagation to
  `post.tmpl`, the precedent Part 2 follows
- `docs/superpowers/specs/2026-05-07-anti-tangent-mcp-design.md` — authoritative design
