# anti-tangent-mcp v0.19.0 — recalibrating the pre-task gate

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

No schema change, no new tool input, no envelope field, no plugin bump. It is a prompt-and-docs
release, and — unlike the design it replaces — its effect is measurable with telemetry that
already exists.

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

## Part 3 — docs

`implementer.md` §4.2 gains the stopping rule: what a `warn` carrying only `minor` findings means
and when to proceed. `controller.md` gets the matching sentence. `authoring.md` and README need
no change under this design.

**Size constraint.** CI enforces < 16,000 bytes per protocol part, and `core.md` is at 15,891 —
**109 bytes of headroom**, so nothing lands there. Current: `implementer.md` 14,521,
`controller.md` 12,645, `authoring.md` 6,878, `project-knowledge.md` 10,401. Resync the plugin
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
- No changes to `internal/verdict`, `internal/mcpsrv`, `internal/planparser`, `internal/session`
  or either plugin, so their suites act as regression evidence rather than needing new cases.
- `go test -race ./...`.

## Compatibility

Reviewer behaviour changes materially — that is the point — while every type, schema, tool
argument and envelope field stays byte-identical. No caller has to change anything.

Versioned as **0.19.0** rather than a patch because a caller that has calibrated anything against
the observed verdict distribution will see it shift substantially; the repo's convention reserves
patch for changes that do not alter behaviour callers can observe. Branch `version/0.19.0`, merge
commit tagged `[minor]`.

## References

- Issue [#58](https://github.com/patiently/anti-tangent-mcp/issues/58)
- `~/.local/state/anti-tangent/stats/events.jsonl` — the 191-call ledger this design is built on
  (local, not in the repo; the opt-in stats subsystem, `ANTI_TANGENT_STATS_DIR`)
- `docs/superpowers/specs/2026-05-07-anti-tangent-mcp-design.md` — authoritative design
