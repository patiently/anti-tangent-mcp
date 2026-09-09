# anti-tangent-mcp v0.19.0 — pre-task gate calibration, and comment hygiene

**Status:** design
**Date:** 2026-09-08 (**revised 2026-09-09** — see [Revision](#revision-the-first-diagnosis-was-wrong))
**Issue:** [#58](https://github.com/patiently/anti-tangent-mcp/issues/58) — `validate_task_spec` rarely converges to `pass`

## Summary

`validate_task_spec` returns `fail` on 58% of real calls and `warn` on a further 32%. The
implementer is left with a verdict it cannot act on and no stopping rule, so in both field cases
in #58 it proceeded on its own judgement — including the case where the tool was right.

The cause is **severity calibration in `pre.tmpl`, not the severity ladder.** The pre-hook prompt
asks the reviewer to enumerate every implicit assumption in a brief and gives it no instruction
on how severe an assumption is, while defining `major` as "a competent implementer would still
misimplement it" — which is how an enumerated assumption reads. The reviewer duly emits
`ambiguous_spec` at `major`, and two majors are a `fail`.

This release ports two clauses that the **post**-hook prompt already has and the pre-hook prompt
lacks:

1. **A severity-calibration clause** — reserve `major` for ambiguity that would actually cause a
   competent implementer to build the wrong thing; consolidate related assumptions; prefer
   `minor` `quality` findings for the rest.
2. **A `Context:` authority clause** — `Context:` is already rendered to the pre reviewer, and
   `post.tmpl` already tells the post reviewer to treat it as the disambiguator. `pre.tmpl` does
   not. This is what Case A needed.

Plus a docs correction stating the pre-gate's expected terminal state.

It also carries a **second, unrelated concern** — a comment-hygiene policy and its enforcement
(Part 3). The two ship together because they were requested together; they share no code. A
reader looking for why this release does two things will not find a technical reason.

No schema change, no new tool input, no envelope field. Parts 1 and 2 are prompt-only; Part 3
adds a `post.tmpl` rule, a guard-hook check and a plugin version bump. Unlike the design this
replaces, the effect of Parts 1–2 is measurable with telemetry that already exists.

## Revision: the first diagnosis was wrong

The 2026-09-08 draft of this spec argued that `pass` was "structurally unreachable" because
`FinalizeVerdict`'s `minor >= 3 → warn` rung collided with `pre.tmpl`'s instruction to emit one
finding per implicit assumption. It proposed a `spec_quality` axis mirroring `plan_quality`, plus
a `normative_code_bodies` input.

The production stats ledger refutes it. `ANTI_TANGENT_STATS_DIR` was enabled on the maintainer's
machine, and `events.jsonl` holds **191 real `validate_task_spec` calls** (2026-08-09 → 2026-09-08,
reviewer `openai:gpt-5.6-sol`):

| verdict | calls | share | what drove it |
| --- | --- | --- | --- |
| `fail` | 111 | 58% | 90 × `major >= 2`, 21 × `critical >= 1`, **0 minor-driven** |
| `warn` | 61 | 32% | **43 × exactly one major**, 18 × the minor cluster |
| `pass` | 19 | 10% | 0–2 findings |

Category totals across all 191 calls: `ambiguous_spec` **522**, `unverifiable_codebase_claim` 117,
`missing_acceptance_criterion` 72, `quality` 45.

Three conclusions, each fatal to the earlier design:

- **Majors drive 154 of 191 non-passes (81%).** The `minor >= 3` rung the earlier draft was built
  around explains 18 calls (9%).
- **`pass` is reachable.** It happened 19 times. "Structurally unreachable" was false.
- **A `spec_quality` axis would be dead on most calls.** `ApplyPlanQualitySanity`'s contract —
  which the mirrored `ApplySpecQualitySanity` would inherit — forces `rough` whenever the verdict
  is `fail`. That is 58% of calls carrying no reviewer information. Of the remaining `warn`
  slice, 43 of 61 carry a major that §4.2 already tells the implementer to address or explain.

There is also a live precedent for the axis going flat: `plan-runs.jsonl` records 7 real plan
runs and **every one is `pass / actionable`**. `plan_quality` has never varied in production. The
earlier draft's own Residual section worried that `spec_quality` would become "a second thing
callers have to learn to ignore"; the sibling axis is already there.

Recording this rather than quietly rewriting, because the wrong diagnosis is easy to re-derive
from the code alone — the ladder-versus-prompt contradiction is real, it is just small. Only the
ledger distinguishes a real mechanism from a dominant one.

## Non-goals

- **A `spec_quality` axis.** Superseded — see above. Revisit only if a stopping-rule gap survives
  recalibration, and design it against the post-fix ledger rather than against the code.
- **`normative_code_bodies`.** Superseded by the `Context:` authority clause, which needs no new
  argument, parser header, session field or caps. Note also that the channel it would have
  mirrored has no documented producer: `implementer.md`'s §4.2 field list — what implementers
  actually paste from — omits `normative_test_bodies` entirely, so a sibling field would have
  shipped with no sender.
- **Changing `FinalizeVerdict`'s ladder.** Still declined, but now for a better reason than the
  earlier draft's: the ladder is not what is producing the verdicts. Nothing consumes the
  pre-hook verdict word anyway (the guard hook reads only `tool: validate_completion` blocks;
  `plan_run_report`'s `Render` never prints `pre_verdict`), so this stays available as a later
  lever if calibration proves insufficient.
- **Persistent storage**, per the standing non-goal.

## Part 1 — severity calibration in `pre.tmpl`

Two edits, both modelled on wording already proven in `post.tmpl`.

### 1a. Bound the assumptions clause

`pre.tmpl`'s third check currently reads:

> 3. Implicit assumptions — list any assumptions a fresh implementer would have to make. Each
> becomes a finding so the spec author can either pin them down or explicitly mark them as
> implementer's discretion.

"Each becomes a finding" sets no severity and invites one finding per assumption. It gains a
consolidation-and-severity bound, reusing the shape of the consolidation sentence `pre.tmpl`
already applies to test-only tasks ("prefer one consolidated finding instead of one finding per
scenario"): related assumptions collapse into one finding, and an assumption is `minor` unless a
competent implementer could plausibly resolve it the wrong way.

### 1b. Add the calibration paragraph

`post.tmpl` carries this, and `pre.tmpl` has no analogue:

> When the provided evidence addresses every AC and the implementer's narrative is internally
> consistent with it, prefer `verdict: pass` with a `category: quality` finding for nit-level
> concerns over `verdict: fail`. Reserve `severity: critical` and `severity: major` for evidence
> that affirmatively contradicts an AC…

The pre-hook version sits next to the existing severity definition: when the goal is clear and
each AC is individually testable, prefer `pass` with `minor` `quality` findings for nit-level
concerns; reserve `major` for ambiguity that would cause a competent implementer to build the
wrong thing, and `critical` for a spec that cannot be implemented as written. Wanting an AC
written more explicitly is not, on its own, a major.

This is deliberately the same shape as the clause that governs the post hook, so the two gates
are calibrated against each other rather than drifting apart.

## Part 2 — `Context:` authority in `pre.tmpl`

`Context:` is already rendered to the pre reviewer (`pre.tmpl`, `{{if .Spec.Context}}`), and
`post.tmpl` already instructs the post reviewer:

> The `Context:` block in the task spec above is authoritative. If an AC reads one way literally
> but `Context:` explicitly anticipates or approves a deviation … treat `Context:` as the
> disambiguator. Do not emit a finding solely because an AC's literal phrasing conflicts with a
> deviation that `Context:` permits.

`pre.tmpl` gets the pre-hook analogue: do not emit `ambiguous_spec` or
`missing_acceptance_criterion` for an ambiguity that `Context:` already resolves — including when
`Context:` resolves it in code rather than prose.

That last clause is Case A. Its brief carried the complete Go source for the type, every method
signature and the tests, and every finding was about prose the code directly below it answered.
The channel was already open; nothing told the reviewer the material was binding.

## Part 3 — comment hygiene and its enforcement

Separate concern from #58, requested alongside it. Agents write comments that mislead other
agents, and that narrate why a change was made and which issue it referenced. That is history;
git already holds it, and a comment repeating it goes stale the moment the next change lands.

### The policy

**A comment may** explain non-trivial behaviour, or a non-obvious invariant or hazard that would
bite the next editor. The test is that it reads correctly to someone who never saw the change
that introduced it.

**A comment may not** carry change history: issue, PR or task references; version references
("added in v0.5.0"); review references ("task-12b review, Critical #1"); or narration of what the
code used to do ("previously", "no longer", "this replaced").

**On touch**, a comment that fails these criteria is removed or rewritten as part of the task
that touches it. There is no separate cleanup pass — roughly 135 comment lines in this repo
currently match the banned patterns, and a big-bang sweep would be a large mechanical diff mixed
into a release about something else. The codebase converges as it is worked on.

Note the boundary deliberately preserves *invariant*-why while banning *change*-why. The guard
hook's own "POSITIONAL EXTRACTION, NOT FREE SCAN" comment explains a security property; deleting
it would be actively harmful. What must go is the "(task-12b review, Critical #1)" citation
attached to it.

### Enforcement: two layers, split on what each can actually judge

A regex cannot tell a good comment from a bad one. An LLM cannot be relied on to catch every
instance. So the split is by decidability, not by preference:

**Deterministic — `plugin/anti-tangent-guard`'s `check-task-complete`, blocking.** The hook
already walks the transcript and parses each `mcp__anti-tangent__validate_completion` tool_use's
`input`, so the submitted `final_diff` (or the file named by `final_diff_path`) is reachable in
the loop that already runs. It scans **added lines only** (`+`-prefixed) that are comments, for
the unambiguous mechanical tells:

- an issue or PR reference — `#<digits>`
- a version reference — `v<major>.<minor>.<patch>`
- a task or review reference — `Task <n>` / `task-<n>` / `review Critical|Important|Major`

On a match it blocks the close with the same reopen-fix-revalidate recovery flow the guard
already uses for a missing `validate_completion`.

Deliberately **excluded** from the deterministic layer: prose-history tells like "previously",
"used to" and "no longer". They cannot be matched without false positives — `verdict.go:2` says
"JSON schema **used to** constrain provider responses", which is a correct comment, and blocking
a task close on it would be worse than missing a violation. Those go to the reviewer.

**Judgement — `post.tmpl`, advisory.** `validate_completion`'s reviewer already receives the full
diff. It gains a rule to emit a finding for a comment that misleads, restates obvious code, or
narrates history. Emitted as `category: quality` with `criterion: comment_hygiene`, following the
existing convention where `criterion` discriminates a sub-type (`noise_cluster`,
`codebase_reference_checklist`) — so **no new category, and no schema change.** That matters more
than it looks: `schema_invariants_test.go`'s category-enum lockstep requires every non-plan schema
to carry exactly the canonical category set, so a new category would touch every schema in the
package.

Kill switch `ANTI_TANGENT_COMMENT_POLICY=0`, mirroring the guard's existing
`ANTI_TANGENT_COMPLETION_GUARD=0`. Fail open on every error, as the guard already does.

The standing non-goal that "the server is advisory, never blocking" is not violated: the blocking
half is a Claude Code plugin hook the operator installs separately and can disable, exactly as
the completion guard already is. The MCP server still never blocks.

### What this cannot do

`PostToolUse` fires after the state change, so the hook detects a bad comment at task close, it
does not prevent one being written — the same limitation the guard's README already documents for
the completion gate. Preventing it would need a `PreToolUse` hook on `Edit`/`Write`, which is the
`anti-tangent-shunt` pattern and is not proposed here. The scan is also diff-shaped: a task that
submits only `final_files` with no diff has nothing for the deterministic layer to read, and falls
through to the reviewer layer alone.

## Part 4 — docs

`implementer.md` §4.2 gains two things: the stopping rule (what a `warn` carrying only `minor`
findings means and when to proceed) and the comment policy, since comments are written by
implementers. `controller.md` gets the matching stopping-rule sentence.

This repo's own `CLAUDE.md` gets the comment policy too. The policy ships as a product surface —
the guard hook and `post.tmpl` enforce it for every consumer — and this repo picks it up like any
other consumer, but stating it in `CLAUDE.md` puts it in front of agents working here without a
protocol read.

`authoring.md` and README need no change.

**Size constraint, and it is now tight.** CI enforces < 16,000 bytes per protocol part.
`core.md` is at 15,891 — **109 bytes of headroom**, so nothing lands there. `implementer.md` is
at 14,521, leaving **1,479 bytes** to absorb both the stopping rule and the comment policy. That
is enough for a terse policy but not a discursive one; if it does not fit, the comment policy
moves to `authoring.md` (6,878, ample room) and `implementer.md` carries a one-line pointer.
`controller.md` 12,645, `project-knowledge.md` 10,401. Resync the plugin
bundle in the same commit:

```bash
rm -f plugin/anti-tangent-protocol/protocol/*.md
cp docs/protocol/*.md plugin/anti-tangent-protocol/protocol/
```

`scripts/check-protocol-docs.sh` requires exactly one `^### 3\.6` match across
`docs/protocol/*.md` and validates every relative link, so any new heading must not collide with
an existing numbered section.

## How this release is evaluated

The earlier draft could not be evaluated at all: `stats.Event` records no quality field, so its
own revisit criterion was unmeasurable. This one is measurable with what already ships, because
`severity_counts` and `category_counts` are recorded per call.

The pre-fix baseline is the table above. After the change, over a comparable window of
`validate_task_spec` calls:

- the share of `fail` verdicts driven by `major >= 2` should fall materially from 90/111;
- the `ambiguous_spec` share of all findings should fall from 522/~800;
- `pass` should rise from 10%.

If the distribution does not move, the reviewer is not honouring the calibration clause and the
next lever is the ladder — which this release deliberately leaves untouched and available.

The guard against over-correction is `validate_completion`, which was `pass` with zero findings
in both #58 field cases and is unchanged here: if recalibration makes the pre-gate miss real spec
defects, they surface at the post gate rather than silently shipping.

## Testing

- `internal/prompts`: golden files regenerated with `-update`; the diff is the substance of this
  release and must be read line by line before committing, not rubber-stamped.
- `internal/prompts`: the render tests must assert the new clauses appear for the pre phase, and
  that the `Phase: post` branch of `pre.tmpl` (post-hoc baseline) still renders coherently with
  them.
- `internal/prompts`: `post.tmpl` goldens regenerated for the comment-hygiene rule, reviewed the
  same way.
- `plugin/anti-tangent-guard/evals/guard-evals.json`: new cases for the comment scan — one per
  banned pattern class (issue/PR, version, task/review reference), one asserting a `+`-prefixed
  comment is required (an unchanged context line carrying an old violation must NOT block, or the
  on-touch rule becomes a big-bang sweep by the back door), one asserting a legitimate
  invariant-why comment passes, and one asserting the `used to` false positive from `verdict.go:2`
  does not block. CI runs these as the `hook-evals` job.
- `guard_eval_fixture_test.go` pins `formatEnvelopeSummary` byte-for-byte; no envelope change
  here, so it should stay green — treat a failure as a signal something unintended moved.
- No changes to `internal/verdict`, `internal/mcpsrv`, `internal/planparser` or `internal/session`,
  so their suites act as regression evidence rather than needing new cases.
- `go test -race ./...`.

## Compatibility

Reviewer behaviour changes materially — that is the point — while every type, schema, tool
argument and envelope field stays byte-identical. No caller has to change anything.

`plugin/anti-tangent-guard` goes **0.1.0 → 0.2.0** (new blocking condition, kill switch, evals),
and the marketplace catalog version bumps with it per the convention set in `22c3fbb`. An operator
running the 0.1.0 hook against a 0.19.0 server sees the completion gate behave exactly as before
and simply gets no comment enforcement; the two halves are independent.

Versioned as **0.19.0** rather than a patch because a caller that has calibrated anything against
the observed verdict distribution will see it shift substantially; the repo's convention reserves
patch for changes that do not alter behaviour callers can observe. Branch `version/0.19.0`, merge
commit tagged `[minor]`.

## References

- Issue [#58](https://github.com/patiently/anti-tangent-mcp/issues/58)
- `~/.local/state/anti-tangent/stats/events.jsonl` — the 191-call ledger this design is built on
  (local, not in the repo; the opt-in stats subsystem, `ANTI_TANGENT_STATS_DIR`)
- `docs/superpowers/specs/2026-05-07-anti-tangent-mcp-design.md` — authoritative design
