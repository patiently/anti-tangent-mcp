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
adds a `post.tmpl` rule and two guard-plugin hooks; Part 4 adds one bounded map to the stats
event. Unlike the design this replaces, every part of this one is measurable — Parts 1–2 with
telemetry that already exists, Part 3 with the field Part 4 adds.

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
that touches it. No separate cleanup pass.

The boundary deliberately preserves *invariant*-why while banning *change*-why. The guard hook's
own "POSITIONAL EXTRACTION, NOT FREE SCAN" comment explains a security property; deleting it
would be actively harmful. What must go is the "(task-12b review, Critical #1)" citation attached
to it.

### Enforcement: three layers

Split by what each can decide — a regex cannot tell a good comment from a bad one — and by what
each can see.

**Prevention layer — `PreToolUse` on `Edit` and `Write`, blocking, in `anti-tangent-guard`.** The
only layer that *prevents* rather than detects, and the only one that reaches every execution
path, because it fires on the tool call itself rather than on a transcript someone else has to be
able to read.

- `Edit`: scan the lines of `new_string` that are not present in `old_string`.
- `Write` to a **new** file: scan all of `content`.
- `Write` over an **existing** file: read the on-disk file and scan only the added lines. Scanning
  whole `content` here would demand cleanup of every pre-existing comment in the file — the
  big-bang sweep the on-touch rule exists to avoid, arriving through the back door.
- Same tells, same extension allowlist, same kill switch as the other hook layers.
- `exit 2` returns the reason to the model, which then rewrites the comment before the write
  lands.

False positives cost much more here than at task close: a bad pattern blocks an edit mid-task
rather than a close at the end. The zero-false-positive acceptance criterion below is therefore a
release blocker for this layer specifically, not a nicety.

**One gap this layer does not close.** `Bash` writes — a heredoc, `sed -i`, a generated file —
bypass an `Edit`/`Write` matcher entirely, and this is not hypothetical: agents are routinely
instructed to prefer Bash for file changes. `anti-tangent-shunt` sets a precedent for matching
`Bash` (it intercepts `cat`/`head`/`tail` reads), but recognising *written comment content* inside
arbitrary shell is not tractable, so Bash-written comments fall through to the reviewer layer. A
call made **inside a subagent** is not a second gap: see
[Coverage](#coverage-what-each-layer-reaches) for the measured reach into subagent sessions.

**Reviewer layer — `post.tmpl`, path-independent, primary.** `validate_completion`'s reviewer
already receives the full diff, whichever agent submitted it. It gains a rule to emit a finding
for a comment that misleads, restates obvious code, or narrates history.

- `category: quality`, `criterion: comment_hygiene` — reusing `criterion` as a sub-type
  discriminator, as `pre.tmpl` already does for `codebase_convention` and the raw-string caveat.
  No new category, and therefore no schema change: `schema_invariants_test.go`'s category-enum
  lockstep would otherwise force a new value into every schema in the package.
- **`severity: minor`, pinned in the prompt.** This is load-bearing, not a default.
  `applySeverityFloor` floors only `unverifiable_codebase_claim` and `convention_deviation`;
  `quality` is **not** floored. So an unpinned rule lets the reviewer emit `major`, two majors are
  a `fail` under the same ladder Part 1 is about, and the guard hook hard-blocks the close on
  `verdict: fail`. §4.2 also tells implementers not to report DONE on any `major`. Part 3 would
  have reintroduced at the post gate precisely the unpinned-severity defect Part 1 exists to fix.
  Pin it in the prompt the way `pre.tmpl` pins `convention_deviation` to minor, rather than adding
  a server-side floor — the floor list is for categories the reviewer *cannot verify*, which is
  not this.
- Consequence to accept knowingly: three or more comment findings still lift the verdict to `warn`
  and append `noise_cluster`. That is the correct signal for a diff with pervasive comment
  problems, and it never reaches `fail` on comments alone.
- A comment-hygiene finding must also never flip `submission_defect_only` off. That flag goes
  false when any major/critical is a non-submission category; pinning minor keeps it out of that
  computation entirely.

**Deterministic layer — the guard's `check-task-complete`, blocking, and narrower than it looks.**
The hook walks the transcript and binds each `validate_completion` tool_use's `input`, but retains
only the index and tool_use id — the input is dropped. So this is a real code change, not merely
reading something already in hand.

Scan scope, every clause of which is a defect found in review:

- **The last `validate_completion` in the task's window only.** Scanning every call in the window
  makes a fixed comment un-closable, because the earlier call's diff still matches. Mirrors the
  existing last-block-wins rule.
- **Added lines only** (`+`-prefixed). An unchanged context line carrying an old violation must
  not block, or the on-touch rule becomes a big-bang sweep by the back door.
- **Source files only, by extension allowlist** (`.go`, `.sh`, `.py`, `.ts`, `.js`, `.rs`, `.java`,
  `.kt`, `.rb`, `.c`, `.h`, `.cpp`). `.md`, `.yml`, `.yaml`, `.json` and `.txt` are explicitly
  excluded. This is not tidiness: `CHANGELOG.md` currently carries **40** tell-matching lines and
  *must* contain `#58`, `v0.18.2` and issue references, because this repo's own conventions
  require a changelog entry in every change. Without the filter, every release-work task close
  blocks.
- **Line comments only.** Block-comment interiors, trailing comments and `//` inside string
  literals or URLs are out of scope for v1 and must be documented as such rather than half-handled.
- **Inline `final_diff` and `final_diff_path` both**, with the path read guarded by an
  absolute-path check, a size cap, a timeout, and fail-open on any error. Note the recipe in §4.2
  writes one fixed filename per git dir, so a stale file from another task is possible; fail-open
  plus last-call-in-window scoping bounds the damage, and a mismatch produces a spurious block at
  worst, never a silent pass.

Tells, narrowed from the first draft after measuring them against this repo:

- `task-<n>` — a review-artifact reference. Unambiguous.
- `#<digits>` — an issue or PR reference.
- `v<major>.<minor>.<patch>` — a version reference.

`Task <n>` (unhyphenated) is **dropped**: `internal/mcpsrv/file_consistency.go:21-28` and
`internal/planparser/planparser.go` legitimately describe the product's own input format, whose
literal shape is `### Task 4: Add /healthz endpoint`. A tell that fires on the product's own
grammar is not a tell. The first draft's `review Critical|Important|Major` pattern is also
dropped — it does not even match the example that motivated it, `(task-12b review, Critical #1)`,
because of the comma; `task-<n>` and `#<digits>` already catch that line.

**Acceptance criterion for the pattern set:** run the scanner over every tracked source file at
HEAD and hand-classify every hit. It ships only when the false-positive count is zero. Known
false-positive families to resolve first: `"AC #1"` in test fixture descriptions, and version
strings in comments that state a wire-compatibility contract rather than history. If either
cannot be separated by pattern, that tell moves to the reviewer layer, which can judge it.

Kill switch `ANTI_TANGENT_COMMENT_GUARD=0`, matching the existing `ANTI_TANGENT_COMPLETION_GUARD`.
It disables the **hook only** — `internal/config` reads no such variable, so the reviewer half is
governed by whether the tool is called at all. The first draft named it `..._POLICY`, which
implied a reach it does not have.

### Coverage: what each layer reaches

Stated plainly because the first draft implied more coverage than exists.

**Subagent-driven development — the hook is blind.** It fires on the *closing* agent's
`TaskUpdate` and reads that agent's transcript. Under SDD the subagent calls
`validate_completion` in its own session and the controller closes the task, seeing only the
pasted `summary_block`, which carries `tool:` / `session_id:` / `verdict:` and **no diff**.
`controller.md:52-56` states this as an existing property: "a controller-side hook cannot see
inside a subagent's own session, so the pasted block is the only trace it has."

So the finish-hook layer covers the paths where the closing agent is the calling agent
(`executing-plans`, or a subagent that closes its own task), and nothing else. It is defence in
depth, not the primary gate. `PostToolUse` also fires after the state change, so even where it
can see, it detects rather than prevents.

**The reviewer layer covers every path**, because the diff reaches the reviewer regardless of who
later closes the task.

**Measured: `PreToolUse` fires for both `Write` and `Edit` calls issued inside a subagent.** Two
probes were run, each dispatching one `general-purpose` subagent via the Agent tool and appending
intercepted calls' JSON payloads to a log file.

First probe — matcher `Write` only. From the main session, a `Write` to `control.txt` produced one
log entry; `grep -c "control.txt" /tmp/claude-hooks/subagent-probe.log` returned `1`, confirming
the hook took effect mid-session with no restart. The dispatched subagent's own `Write` call,
creating `subagent.txt`, also produced one entry; `grep -c "subagent.txt"
/tmp/claude-hooks/subagent-probe.log` returned `1`, and that entry carried `agent_id` and
`agent_type` fields absent from the control entry, confirming it was captured inside the
subagent's own session rather than the controller's.

Second probe — matcher `Edit|Write`, logging to a file shared with a concurrently running task, so
entries were attributed by `agent_id` rather than by count. The dispatched subagent created a
scratch file with `Write`, then changed it with `Edit`; both calls appear in the log tagged with
that subagent's `agent_id`, showing `Edit` is intercepted inside a subagent's session on the same
footing as `Write`.

Incidental corroboration, not a designed arm: the same log also captured `Edit` and `Write`
entries from a second, concurrently running subagent (a different `agent_id`) that was doing
unrelated work and was not instrumented for this experiment. It had no stake in the result, so the
observation that its calls were intercepted too is independent evidence from a distinct agent
rather than a repeat of the first.

Both probes exercised one harness and one subagent type (`general-purpose`); no other dispatch
path or agent type was tried, and the result should not be generalised past `Edit` and `Write`.

So, for the two tool calls this layer matches, it closes the SDD gap: a subagent's `Edit` and
`Write` calls are intercepted, and blocked before the write lands, by the same hook the controller
session uses — regardless of transcript visibility or evidence shape. A `PreToolUse` matcher
selects on tool name, not on which session or agent issued the call, which is the mechanism behind
that result and the reason it needs no per-session configuration to hold; within the harness
measured here, every `Edit`/`Write` call seen — from either subagent — was intercepted. The
reviewer layer stays the broader-judgment layer: it alone weighs a comment's substance rather than
match a pattern, so it remains the backstop for the one gap prevention does not close (`Bash`
writes) and for anything a pattern cannot catch on the paths prevention does reach.

### Bookkeeping a third block condition drags in

- `plugin/anti-tangent-guard/README.md` and `controller.md` both say the hook blocks in **exactly
  two cases**; a third invalidates that sentence in both.
- `plugin.json` and the marketplace entry both describe the plugin as the completion gate only.
- A **distinct** stderr message, so the comment block is separable from the two existing ones, and
  a `trace()` line for the new block reason.
- `evals/run.sh`'s `EXPECTED_CASE_COUNT` and `guard-evals.json`'s case-count description string
  must move together; new case ids continue from 23, since the fixture test keys on id.
- `run.sh` materialises only a transcript, so a `final_diff_path` case needs the runner extended
  to write a diff file — otherwise the primary submission route ships with no eval.

## Part 4 — bounded `criterion` counts in the stats ledger

Part 3's effect is otherwise unmeasurable: `stats.Event` records `CategoryCounts` but not
criterion, so comment-hygiene findings would be indistinguishable from every other `quality`
finding in the ledger. Given the whole reason the earlier draft of this release was thrown out is
that it could not be evaluated, shipping a second unmeasurable change would repeat the mistake.

**Not raw criterion.** `pre.tmpl` instructs the reviewer to "quote the verbatim AC text" as the
criterion for AC-quality findings. Recording it raw would:

- write verbatim task-specification text into a plain file on disk, where the ledger currently
  holds **no free text at all** — every string field is a bounded enum (`tool`, `verdict`,
  `model`) or a hash, and even the session id is stored as `SessionHash`; and
- give the map unbounded cardinality, one key per distinct acceptance criterion ever reviewed.

**Instead:** `CriterionCounts map[string]int`, populated only from an allowlist of
server-recognised criterion sentinels — `comment_hygiene`, `noise_cluster`,
`codebase_reference_checklist`, `codebase_convention`, `exit_contract`, `spec`, `structure`,
`max_tokens_override`. Anything else is not counted. Cardinality is bounded by a constant, and no
reviewer- or caller-authored text reaches the file.

`stats.CountFindings` gains the third return value; `recordStat` passes it through. The allowlist
lives beside the sentinels it names, and a test asserts that a finding carrying a verbatim AC
string as its criterion contributes nothing — that test is the guard against the leak, not the
allowlist itself.

This is the release's only change outside `internal/prompts`, the guard plugin and docs.

## Part 5 — docs

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

The guard against over-correction is `validate_completion`: if recalibration makes the pre-gate
miss a real spec defect, it surfaces at the post gate rather than silently shipping. That guard
still holds — Part 3 adds no rule that would mask a spec defect.

Part 3 does make the parts' telemetry non-independent: `validate_completion`'s `quality` finding
count rises by construction once comment findings land in it, so any comparison of
`validate_completion`'s raw `quality` rate must treat the 0.19.0 boundary as a break, not a trend.
Part 4 is what keeps that from being a dead end — `CriterionCounts["comment_hygiene"]` separates
comment findings from the rest of the `quality` bucket, so both the break and Part 3's own effect
are quantifiable rather than merely asserted.

Part 3's prevention layer has a second, subtler measurement property worth stating: if it works,
comment findings should be *rare* at the post gate, because they were blocked at write time. A
high `comment_hygiene` count after release means the prevention layer is being bypassed — most
likely by `Bash` writes — rather than that the policy is failing.

## Testing

- `internal/prompts`: golden files regenerated with `-update`; the diff is the substance of this
  release and must be read line by line before committing, not rubber-stamped.
- `internal/prompts`: the render tests must assert the new clauses appear for the pre phase, and
  that the `Phase: post` branch of `pre.tmpl` (post-hoc baseline) still renders coherently with
  them.
- `internal/prompts`: `post.tmpl` goldens regenerated for the comment-hygiene rule, reviewed the
  same way.
- `plugin/anti-tangent-guard/evals/guard-evals.json`: new cases for the comment scan, each
  pinning a defect found in review — one per surviving tell (`task-<n>`, `#<digits>`,
  `v<x.y.z>`); one asserting an unchanged context line carrying an old violation does NOT block
  (else on-touch becomes a big-bang sweep by the back door); one asserting a `.md` file's added
  lines never block (the CHANGELOG case, which would otherwise block every release task); one
  asserting an unhyphenated `Task 4:` in a comment describing the product's own input grammar does
  NOT block; one asserting only the LAST `validate_completion` in the window is scanned (else a
  fixed comment stays un-closable); one asserting a legitimate invariant-why comment passes; and
  one for the kill switch, which the runner's `env` support already allows. A `final_diff_path`
  case requires extending `run.sh` to materialise a diff file. CI runs these as `hook-evals`.
- The `-`/`+` pair produced by moving or reindenting an existing comment must have a case pinning
  the chosen behaviour in whichever direction is decided; leaving it unpinned is how a gofmt
  reflow or a file move silently becomes a demand to rewrite every historical comment in it.
- `guard_eval_fixture_test.go` pins `formatEnvelopeSummary` byte-for-byte; no envelope change
  here, so it should stay green — treat a failure as a signal something unintended moved.
- `internal/stats`: `CountFindings` returns criterion counts; a test asserts that a finding whose
  criterion is a verbatim acceptance-criterion string contributes **nothing** to the map. That
  test is the actual guard against writing task text to disk — the allowlist is just the
  mechanism.
- `internal/mcpsrv`: `recordStat` threads the new map through; existing envelope and handler tests
  act as regression evidence.
- New `PreToolUse` evals: an `Edit` whose `new_string` adds a violating comment blocks; an `Edit`
  that leaves an existing violating comment untouched in `old_string` does **not** block; a
  `Write` to a new file scans all content; a `Write` over an existing file scans only lines added
  relative to what is on disk; a `.md` write never blocks.
- No changes to `internal/verdict`, `internal/planparser` or `internal/session`, so their suites
  act as regression evidence rather than needing new cases.
- `go test -race ./...`.

## Compatibility

Reviewer behaviour changes materially — that is the point — while every type, schema, tool
argument and envelope field stays byte-identical. No caller has to change anything.

The stats ledger gains a `criterion_counts` object on `validate_*` events. It is additive and
`omitempty`, but anything parsing `events.jsonl` positionally rather than by key should be
checked — `internal/planrun`'s rollup and the external tray consumer both read this file.

`plugin/anti-tangent-guard` goes **0.1.0 → 0.2.0** (two new hooks, a new blocking condition, kill
switch, evals). It stops being a completion-gate-only plugin, so its `plugin.json` description,
marketplace entry and README all need rewriting rather than amending,
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
