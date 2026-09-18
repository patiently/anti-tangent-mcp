# anti-tangent-mcp — lean by default

**Status:** design
**Date:** 2026-09-18
**Source:** the [ponytail](https://github.com/DietrichGebert/ponytail) plugin (MIT, commit
`e3ba2aa6f1e6f0bc4d69eb09c9f0d0a93af56156`, 2026-09-14) and a brainstorm on 2026-09-14 that
compared bundling it against reproducing its effect inside the protocol. Its ruleset is one
6.6 KB `SKILL.md`; the other ~760 lines are mode switching, a statusline badge and per-host
output formats, none of which changes what gets built.

## Summary

Ponytail makes an agent build the leanest thing that works: reuse before stdlib, stdlib before
a dependency, one line before fifty, no interface with one implementation. This design puts
that behaviour where anti-tangent already has leverage, at three moments, and closes the hole
that would otherwise let the first two be skipped.

1. **Plan level.** `validate_plan` flags a plan that mandates over-building — an interface for
   one implementation, a dependency for what the stdlib does, scaffolding for a phase the plan
   does not deliver — before any implementer builds it. The plan is where most over-building
   is decided; an implementer's lean ruleset cannot undo an AC that says "create a
   `StorageBackend` interface". `authoring.md` tells plan authors what is flagged and how one
   line of `Context:` pre-empts it. (Part 1)
2. **Implementer level.** `validate_task_spec` returns a ~2 KB ruleset, adapted from ponytail's,
   in a new envelope field. It reaches exactly the audience that calls that tool — implementers
   — on every MCP host, and it is one text the reviewer is also handed, so the two cannot
   drift. (Part 2)
3. **Review level.** `check_progress` and `validate_completion` check the diff against that same
   ruleset and report departures as one rolled-up `quality` / `over_building` finding, always
   `minor`. (Part 3)
4. **The session guard.** Delivery of (2) rides on `validate_task_spec` being called before the
   first edit, and nothing enforces that today: an empty `session_id` on `validate_completion`
   is accepted as lightweight mode, and the close-time guard never looks for step 1. A
   `validate_completion` without a session is reviewed against a synthesized spec with **no
   acceptance criteria**, so a skipped step 1 hollows out step 3 as well. The guard plugin gains
   a write-time start gate and a close-time no-session rule under one new kill switch, and the
   server makes a lightweight completion visible on every host. (Part 4)

Why not bundle ponytail, or a plugin like it: a `SubagentStart` hook injects text at the top of
a subagent's context — the strongest position there is — but it only sees `agent_type`, and
superpowers-extended-cc dispatches implementers and every reviewer alike as `general-purpose`.
There is no filter that reaches implementers alone. Ponytail's own on/off state is one file
shared by every session on the machine, which parallel worktrees would fight over. Anti-tangent
has no cross-session state: one server process per Claude session, spawned by it over stdio,
shared with that session's subagents and nobody else.

## Verified constraints

Each fact the design leans on was checked against `main` at v0.22.0 or against live state.

| Constraint | What was found |
|---|---|
| Protocol part headroom | `implementer.md` 15,954 / 16,000 bytes, `controller.md` 15,940, `core.md` 15,929: no room for a pointer in any of them. `authoring.md` 9,401: room for a section. |
| `SubagentStart` input | `agent_type` and `agent_id` only; no dispatch prompt. `hookSpecificOutput.additionalContext` does inject text (ponytail depends on it). |
| Who calls `validate_task_spec` | The implementing subagent, once, before edits (§4.2 step 1). Controllers never (§5). |
| What enforces it | `validate_completion` with an unknown `session_id` returns a critical `session_not_found`; an **empty** one is the lightweight path (`handlers.go:1683`), reviewed against a synthesized spec — `Goal = summary`, no ACs (`handlers.go:1758`). The guard's close-time hook looks for `validate_completion` only. `plan_run_report` shows the task as `Incomplete`, at run end. |
| Implementer transcripts | Each dispatched subagent has its own `subagents/agent-*.jsonl`, and its **first user message is the dispatch prompt**. On the development machine, 16 of 1,206 subagent transcripts open with `## Drift-protection protocol (anti-tangent-mcp)`; all are implementers. |
| Verdict ladder | `finalize.go:15`: `critical ≥ 1 or major ≥ 2 → fail`; `major ≥ 1 or minor ≥ 3 → warn`, with a `noise_cluster` advisory; else `pass`. One minor finding never moves the verdict. |
| Prior art in the repo | `stale_comments` is a rolled-up always-minor finding; `plan_comment_rules.tmpl` exempts its findings from quick mode's cap; `THIRD_PARTY_NOTICES.md` and `internal/notices` attribute Spotify's MIT shunt hooks and a test asserts the entry. |
| Process model | One `anti-tangent-mcp` per `claude` process (`"type": "stdio"`); subagents use the parent's connection. All state is in that process's memory. |

## Non-goals

- Intensity levels, mode switching, "stop ponytail", a statusline badge.
- A debt ledger, a repo-wide audit, a scoreboard, the `ponytail:` comment marker.
- A plugin that injects the ruleset into every subagent, or a dedicated implementer agent type.
- Any change to superpowers' `writing-plans` or dispatch prompts.
- An env kill switch for the guidance or the reviewer check. Both are advisory and always
  `minor`; nobody has asked. (The ruleset's own first rung.)
- The server refusing an empty `session_id` unless `lightweight: true` is passed. It would be
  the only host-independent enforcement, but it breaks the documented lightweight contract for
  a gain the guard delivers where it is installed. Revisit only if implementers on hosts
  without the guard turn out to skip step 1.
- Any new verdict category or finding schema field, and no renumbering of protocol sections
  (§3.10 is appended; nothing existing moves).

## Part 1 — the plan-level check

### 1.1 `plan_lean_rules.tmpl`

A new template, `{{define "plan_lean_rules"}}`, included by `plan.tmpl` and by `pre.tmpl`. It
takes a scope word, so "anywhere in the plan" reads "anywhere in the task spec" when one task is
all the reviewer has. It climbs ponytail's ladder as six tags — the vocabulary Part 3 uses on code — and says what plan
text can and cannot settle. The plan is a closed world: "nothing else in this plan uses it" is
checkable in a way codebase claims are not.

| Tag | Flagged when a task… | Evidence the reviewer must cite |
|---|---|---|
| `reuse` | writes a helper an **attached file** (`context_paths`) or **Project knowledge** already provides | the attached path and symbol. Never from the black box: silence there is not approval |
| `stdlib` | adds a dependency, or hand-writes a utility, for what the language's standard library covers | language from paths and fences; general knowledge, not a codebase claim. "An installed dependency covers it" only when a manifest (`go.mod`, `package.json`, …) is attached |
| `native` | builds in application code what the platform provides: uniqueness in a service instead of a DB constraint, a date-picker library over `<input type="date">`, JS over CSS | the step or AC text |
| `yagni` | mandates an interface or abstract type with one implementation, a factory or registry for one product, a config value or flag no task varies, a layer with one caller — **across the whole plan** | the introducing task, and the absence of a second consumer anywhere in the plan |
| `delete` | scaffolds for a phase this plan does not deliver: placeholder modules, "extension points", empty hooks, stubs for later | the step text. A whole task outside the plan's goal is scope, not this |
| `shrink` | fenced code — transcribed verbatim, as the comment-hygiene rule already states — does in many lines what a shorter form does in few | the fence. Test fences are exempt from `shrink` only: arrange/act/assert is verbose by nature |

**Never flagged:** validation at trust boundaries, error handling that prevents data loss,
security measures, accessibility basics, tests an AC calls for, normative test bodies, and
anything the plan's Goal explicitly asks for.

**`Context:` is the escape hatch.** A structure the author justifies there — "Task 9 adds the S3
backend", "chosen for the TZ edge cases stdlib mishandles", a decision note quoted from Project
knowledge — draws no finding. `Context:` is already authoritative in `pre.tmpl` and `post.tmpl`,
so one line written once propagates through all three checks. That is the answer to the
triple-flag hazard: a deliberate interface flagged by `validate_plan`, again by
`validate_task_spec`, again on the built code. A controller ruling waives a finding at plan
level only; `Context:` is what reaches the task.

### 1.2 Emission

At most **one** `over_building` finding per task, in that task's findings: `category: quality`,
`criterion: over_building`, `severity: minor`. `evidence` lists each instance on its own line as
`<tag>: <what>. <replacement>.` — ponytail-review's line format. `suggestion` is the rewritten
AC or step, or the `Context:` line to add when the structure is deliberate. A cross-task pattern
(three tasks each writing their own config loader) is one plan-level finding instead.

The finding is exempt from quick mode's minor cap, with the clause `plan_comment_rules.tmpl`
already uses: minor because that is the severity it is worth, not because it is discretionary.
Without the exemption quick mode drops the check entirely, since every finding it makes is
minor.

### 1.3 Where it runs

- `plan.tmpl` — every `validate_plan` round, per task and plan-wide.
- `pre.tmpl` — `validate_task_spec` runs the same task-scoped check on the one spec it is given,
  so a task dispatched from an unvalidated plan still gets it. At task start it informs rather
  than redirects: the implementer builds every AC. But it seeds the session's pre-task findings,
  so the completion review's `same_as` can link the built structure back to the spec that
  mandated it.

### 1.4 `authoring.md` §3.10 "Lean by default"

About 1.5 KB, well inside the part's headroom, placed after §3.9 and before the write-time
comment-guard section. Content, in this order:

- The six tags, one line each, as "what `validate_plan` flags as `over_building`".
- Put the justification in `Context:`, not in the dispatch conversation: the same check runs at
  task start and at completion, and only `Context:` reaches them.
- Attach the files a task might duplicate via `context_paths`. A helper the reviewer can see is
  a `reuse` finding; one it cannot see is silence, not approval.
- A cross-reference to §3.5: an AC that names an interface is an implementation step in
  disguise.

The plugin bundle is resynced in the same commit. No other protocol part changes.

## Part 2 — the implementer ruleset

### 2.1 `lean.tmpl`

The text `validate_task_spec` returns, and the text Part 3 hands the reviewer as the definition
of the check. Written once, for both readers. Rendered by a new `prompts.LeanGuidance()`.

```markdown
## Build guidance (apply while implementing this task)

Build every acceptance criterion in its leanest working form. Lean means
efficient, not careless: read the task and the code it touches first, trace
the real flow end to end, then climb the ladder and stop at the first rung
that holds.

1. Already in this codebase? A helper, util, type or pattern that already
   lives here → reuse it. Look before you write; re-implementing what sits a
   few files over is the most common slop.
2. Stdlib does it? Use it.
3. Native platform feature covers it? `<input type="date">` over a picker
   lib, CSS over JS, a DB constraint over app code.
4. An already-installed dependency solves it? Use it. Never add a new one
   for what a few lines can do.
5. Can it be one line? One line.
6. Only then: the minimum code that works.

Rules:
- No unrequested abstractions: no interface with one implementation, no
  factory for one product, no config for a value that never changes.
- No boilerplate, no scaffolding "for later"; later can scaffold for itself.
- Deletion over addition. Boring over clever — clever is what someone
  decodes at 3am.
- Fewest files, shortest working diff — once you understand the problem.
  The smallest change in the wrong place is not lean, it is a second bug.
- Bug fix = root cause, not symptom. Grep every caller before you edit; one
  guard in the shared function is a smaller diff than a guard in every caller.
- Two stdlib options, same size? Take the one that is correct on edge cases.
- A deliberate simplification with a known ceiling (global lock, O(n²) scan,
  naive heuristic) gets a comment naming the ceiling and the upgrade path.

Scope is set by the acceptance criteria, not by this guidance: never skip,
narrow or defer an AC to be lean. Speculative work beyond the ACs is what
to skip. If an AC itself mandates more structure than its goal needs, build
it as written — scope is the controller's call. The completion review will
still name it, and your summary_block carries that to the controller.
Anything the task spec's Context: justifies is deliberate; build it.

Never simplify away: input validation at trust boundaries, error handling
that prevents data loss, security measures, accessibility basics, the tests
the task calls for, or anything explicitly requested.

The reviewer checks your changes against this ruleset at check_progress and
validate_completion and reports departures as quality / over_building, minor.
```

### 2.2 Provenance

| Kept verbatim or near it | Narrowed or changed | Dropped |
|---|---|---|
| Ladder rungs 2–7; the root-cause rule; the six Rules; "read fully, then be lazy"; "never simplify away" | **Rung 1** ("does this need to exist?") applies only beyond the ACs; an AC is never skipped. **"Ship the lazy version and question it"** becomes: build the AC as written; the completion finding and `summary_block` carry the objection to the controller, and `same_as` links it to the pre-task finding — no new protocol. **The `ponytail:` comment marker** becomes an unprefixed comment: a third-party brand in consumer code is not ours to mandate, and the debt ledger is a non-goal. | Persistence and mode switching; intensity levels; "code first, three lines" (reports follow the protocol); "one runnable check, no frameworks" (ACs and TDD own tests); the hardware-calibration paragraph (domain-specific — a hardware task says it in `Context:`); Caveman pairing |

### 2.3 Envelope field

`Envelope.ImplementationGuidance string` with `json:"implementation_guidance,omitempty"`. Set on
every `validate_task_spec` response that reached the reviewer, including re-validation rounds:
each mints a new session, and 2 KB per round is not worth a special case. Empty on
`check_progress` and `validate_completion` (same struct) and on the error envelopes
(`payload_too_large`, provider failure), where the caller is about to retry anyway. Not part of
`summary_block` — the block is what gets pasted into DONE reports, and the guidance would be
noise there.

Lightweight-mode tasks skip `validate_task_spec` and so get no guidance. They are mechanical by
definition, and `validate_completion` still runs Part 3's check on their diff.

### 2.4 Delivery

Field only; no protocol pointer. The heading is the instruction, `implementer.md` has 46 bytes,
and the Part 3 check is the backstop for an implementer that skims it or loses it over a long
task. `next_action` is untouched: it is the reviewer's single highest-leverage item, and
diluting it costs more than a pointer buys. The one real weakness of a tool-result position —
that it is never seen if the tool is never called — is what Part 4 removes.

## Part 3 — the reviewer check

### 3.1 `post.tmpl`

A new `### Over-building` section after `### Stale comments`, which includes `lean.tmpl` framed
as "the implementer was handed this ruleset at task start". Evidence rules mirror comment
hygiene's, because the reasoning is identical — only what this change *added* can be judged:

- **Diff present:** judge the `+` lines. `path:line` comes from hunk headers.
- **`final_files` only:** emit no over-building finding. A new interface cannot be told from
  one that was already there, and the summary is not evidence.
- **`reuse`** only when the evidence itself shows the existing thing: the diff adds a helper a
  submitted file already has, or the task spec's `Context:` or `pinned_by` names it. The
  reviewer never sees the rest of the codebase; absence is silence, not approval.
- **`stdlib` / `native`:** language from the paths; general knowledge, not a codebase claim.
- **Test code** is exempt from `shrink`; the other tags apply — a mocking dependency for what a
  stub does is still `native`.

Never flagged: the Part 1 list, plus anything `Context:` justifies. `Context:` is already the
disambiguator in `post.tmpl`; no new rule is needed.

**Structure an AC mandated.** The implementer built it as written, so it *is* flagged — but
`evidence` says the AC mandated it, `suggestion` is addressed to the plan author ("drop the
interface from the AC, or justify it in `Context:`"), and `same_as` points at the pre-task
`over_building` finding. The implementer is not expected to act; this is how the objection
reaches the controller through `summary_block` without any new protocol.

### 3.2 `mid.tmpl`

The same section with the structural tags only — `reuse`, `stdlib`, `native`, `yagni`,
`delete` — and not `shrink`. Mid-task is when an unnecessary abstraction is cheapest to remove,
before tests and callers accrete on it, which is why it belongs there despite "style is noise
mid-task"; `shrink` *is* style, so it waits for completion. `check_progress` stays optional;
nothing changes about when implementers call it.

### 3.3 Emission

**One finding per call**, like `stale_comments`: `category: quality`, `criterion:
over_building`, `severity: minor`. `evidence` lists every instance as `path:line: <tag>: <what>.
<replacement>.` and ends with ponytail-review's estimate, `net: -N lines`. `suggestion` gives the
replacement code or the removal.

Rolled-up matters because of the verdict ladder: one finding can never tip the verdict by
itself, and a diff with four over-built spots is not labelled a noise cluster. An implementer
who agrees with three instances and disputes one answers the one finding via
`finding_responses` and fixes the rest. At completion, `same_as` is the pre-task
`over_building` finding when the structure it named got built, else the prior finding it
repeats, else null. `check_progress` keeps setting `same_as` to null, as it does for every
finding today.

### 3.4 Stats

`over_building` joins `countedCriteria` in `internal/stats/event.go`. `criterion` is free text
on the wire; the sentinel is what makes it countable in the opt-in ledger.

## Part 4 — the session guard

### 4.1 The hole

Under the full clause, `validate_task_spec` is REQUIRED before any edit, and four layers each
fail to enforce it:

| Layer | Catches | Misses |
|---|---|---|
| The pasted clause | — | prompt-only |
| `validate_completion` with an unknown `session_id` → critical `session_not_found` | an invented or expired id | an empty id, accepted as lightweight whenever any evidence field is non-empty. The server cannot tell "dispatched lightweight" from "skipped step 1" |
| `anti-tangent-guard` close-time hook | a close with no `validate_completion`, or one that returned `fail` | it never looks for `validate_task_spec` |
| `plan_run_report` | the task lands in `Incomplete` | end of run only, and only with `plan_run_id` threaded |

One invariant falls out: a `validate_completion` with a non-empty `session_id` proves
`validate_task_spec` ran, because the session exists no other way. The only gap is an
implementer that skips step 1 and then calls completion with an empty id. That completion is
reviewed against a synthesized spec with no ACs — the build gets a pass-shaped review, and the
trimming happens afterwards by hand.

### 4.2 One concern, one switch

`ANTI_TANGENT_SESSION_GUARD=0` turns off both rules below and nothing else. The concern: *a
full-protocol task has a task session before its first edit and at its close.* It is scoped by
concern, not by hook, like the two existing switches; the close-time rule is deliberately not
under `ANTI_TANGENT_COMPLETION_GUARD`, so an operator can keep the completion gate and drop the
session rules, or the reverse.

### 4.3 Rule A — the write-time start gate

A new `PreToolUse` hook, `check-task-start`, matched on `Edit|Write|NotebookEdit`. This is the
rule that fires before implementation starts.

**Fingerprint.** Read the transcript from the top until the first `type: "user"` entry and take
its text. `full` is true when it contains `## Drift-protection protocol (anti-tangent-mcp)`;
`lite` when it contains `Drift-protection protocol (lightweight)`. If not `full`, or if `lite`,
exit 0. Only a dispatched implementer's session has that first message: controllers see the
clause in file reads and in `Agent` inputs, never as their own first user message, so a
controller editing a CHANGELOG is never touched.

**Gate.** Otherwise scan the rest of the transcript for an assistant `tool_use` named
`mcp__anti-tangent__validate_task_spec`. Found: write the sentinel and exit 0. Not found: exit 2
with, on stderr:

```
EDIT BEFORE validate_task_spec

This session was dispatched under the full anti-tangent protocol (§4.2), which
requires validate_task_spec before any edit. It has not been called.

Call mcp__anti-tangent__validate_task_spec with the task's fields first. Its
response carries the pre-task review of the spec and the build guidance for this
task; the session_id it returns is what validate_completion needs at the end.

Reads are not gated — read what the change touches, then call it, then edit.

(Disable: ANTI_TANGENT_SESSION_GUARD=0. Trace: <trace log>)
```

Reads stay free. "Read fully, then be lazy" is exactly the order wanted.

**Sentinel.** When the hook input carries `scratchpad_dir`, the first positive writes
`<scratchpad_dir>/anti-tangent-guard/session-ok`, and later invocations exit 0 on its presence
without opening the transcript. Without `scratchpad_dir` there is no cache; the transcript before
the first edit is short. The sentinel is per session by construction — the scratchpad is.

**Cost.** A non-matching session pays reading the transcript head once per write. A matching
session pays a scan per write only until step 1 happens.

**Limits, stated in the README.** Writes through `Bash` (`cat >`, `sed -i`) bypass it, exactly
as they bypass the write-time comment guard; Rule B is the backstop. A controller that rewrites
the clause heading defeats the fingerprint, and the hook fails open — the heading is the
contract, and the README says so. A hook that cannot parse the transcript exits 0.

**One fact the first implementation task confirms live:** that a hook firing inside a subagent
receives the subagent's `transcript_path` and not the parent's. The fixture evals cannot prove
it; a two-minute dispatch with the trace log open can. If it turns out otherwise, Rule A is
dropped from the release and Rule B ships alone, with this spec amended.

### 4.4 Rule B — the close-time no-session rule

`check-task-complete` gains a fourth block condition:

> The task's last `validate_completion` in the window ran with an empty `session_id`, **and**
> the window carries no lightweight dispatch marker → block.

Signals, in the two topologies the hook already handles:

| Topology | Empty session | Lightweight marker |
|---|---|---|
| Direct call (executing-plans, same session) | `completion_inputs[-1].session_id == ""`; the hook already collects these inputs | the marker text in any user message in the window |
| Pasted block (subagent-driven, controller closes) | the pasted block's `session_id:` line is blank (`summary.go:42`), or it carries the `mode: lightweight` line from §4.5 | the marker text in the `prompt` input of an `Agent` or `Task` `tool_use` in the window — the dispatch prompt is in the controller's own transcript |

The **marker** is the lightweight clause's heading, `Drift-protection protocol (lightweight)`,
which `examples/lightweight-dispatch.md` already carries. Absence means full protocol: the full
clause is the default dispatch, so the default is to block. A controller writing its own
lightweight wording keeps that one heading line; the example and the guard README say so. No
protocol part changes.

**Precedence** among block conditions: comment violations → `verdict: fail` → **no session
under full protocol** → neither signal present; otherwise pass. Trace event `block | no-session`. Message:

```
TASK CLOSED WITHOUT A TASK SESSION

Task #<id> was closed on a validate_completion that ran with an empty
session_id. That is lightweight mode: the review was made against a
synthesized spec with no acceptance criteria. Nothing in this window shows a
lightweight dispatch, so this task was dispatched under the full protocol and
validate_task_spec was never called.

  1. TaskUpdate taskId=<id> status=in_progress
  2. Call mcp__anti-tangent__validate_task_spec with the task's fields
  3. Call mcp__anti-tangent__validate_completion with the returned session_id
  4. Re-close once the verdict is pass

If the controller did dispatch this task lightweight, its dispatch prompt
must carry the heading "Drift-protection protocol (lightweight)".

(Disable: ANTI_TANGENT_SESSION_GUARD=0. Trace: <trace log>)
```

With Rule A in place, a *late* `validate_task_spec` — called after the edits to obtain a session
— is impossible in the subagent topology, because the first edit is refused until the call
exists. It remains possible under executing-plans, where there is no dispatch prompt to
fingerprint; Rule B covers the omission there and the README says the order is not checked.

### 4.5 Server side — a lightweight completion is visible on every host

An empty-session `validate_completion` sets `Envelope.Lightweight bool` (`json:"lightweight,omitempty"`)
and prints one extra line in `summary_block`, immediately after `verdict:`:

```
  mode:          lightweight
```

Only in lightweight mode; the block is byte-identical otherwise. A controller reading a DONE
report for a task it dispatched with the full clause sees it with no plugin installed. The
guard's block regexes are anchored per key, so the extra key does not disturb an older guard;
a fixture proves guard 0.4.0 still reads `tool:`, `session_id:` and `verdict:` from a block that
carries it. Non-breaking.

### 4.6 Plugin packaging

`anti-tangent-guard` 0.4.0 → 0.5.0: a new hook script, a new `hooks.json` entry, a new env var.
README changes: "The three block conditions" becomes four; a new "Start gate (PreToolUse)"
section; the kill-switch table gains `ANTI_TANGENT_SESSION_GUARD`; a "Limits" note under each
rule as above. `plugin.json`'s description names the third switch.

## Input and envelope changes

| Surface | Change | Compatibility |
|---|---|---|
| `Envelope` | `implementation_guidance` (string, `omitempty`), `lightweight` (bool, `omitempty`) | additive |
| `summary_block` | `mode: lightweight` line, lightweight completions only | additive; older guards ignore the key |
| Tool inputs | none | — |
| Finding schema | none; `over_building` is a criterion string under the existing `quality` category | — |
| Env | `ANTI_TANGENT_SESSION_GUARD` (guard plugin only) | new, default on |

## Attribution

Ponytail is MIT-licensed, copyright (c) 2026 DietrichGebert. `THIRD_PARTY_NOTICES.md` gains a
`## ponytail` section in the shape of the shunt entry: source URL, copyright line, licence, the
pinned upstream commit, and the derived files — `internal/prompts/templates/lean.tmpl` (from
`skills/ponytail/SKILL.md`) and `internal/prompts/templates/plan_lean_rules.tmpl` (the tag
vocabulary and examples from `skills/ponytail-review/SKILL.md`). `internal/notices/notices_test.go`
asserts the copyright holder, the URL, "MIT" and both paths, so removing the entry breaks
`go test ./...`.

## Testing

**Unit (`go test -race ./...`):**

- `prompts`: goldens for `pre`, `mid`, `post`, `plan`, `plan_findings_only` regenerated and
  reviewed; a new golden for `LeanGuidance()`; a test that `lean.tmpl` renders identically into
  the envelope and into `mid`/`post` (one text, two readers).
- `mcpsrv`: `validate_task_spec` sets `implementation_guidance` on a reviewed response and not
  on `payload_too_large`; `check_progress`/`validate_completion` leave it empty; an
  empty-session `validate_completion` sets `lightweight` and prints the `mode:` line; a
  session-backed one prints no such line; `summary_block` is byte-identical to today's when not
  lightweight.
- `stats`: `over_building` is counted; `Over_Building` normalises to it.
- `notices`: the ponytail entry and both file paths.
- `verdict`: nothing new to test — no schema change — but `schema_invariants_test.go` runs
  unchanged as the proof.

**Guard evals (`plugin/anti-tangent-guard/evals/run.sh`):**

- Rule B, six cells: {direct, pasted} × {full clause + empty session → block, lightweight
  heading + empty session → pass, session present → pass}; plus `mode: lightweight` line with
  the full clause → block; plus `ANTI_TANGENT_SESSION_GUARD=0` → pass.
- Rule A: implementer transcript without the call → block; with the call → pass; lightweight
  heading → pass; a controller transcript (clause only inside an `Agent` input) → pass; a
  session with no clause anywhere → pass; sentinel present → pass without reading the
  transcript; unparsable transcript → pass; kill switch → pass.
- The false-positive gate is untouched: neither rule scans comments.

**Replay gate (`-tags=e2e`, `ANTI_TANGENT_REPLAY_DIR`):** two fixtures, both synthetic, so
they can be committed — under `internal/mcpsrv/testdata/replay/lean/`, with
`ANTI_TANGENT_REPLAY_DIR` pointed at it for the run — an
over-built diff (an interface with one implementation, a hand-rolled `contains` over a slice, a
config struct for one value) and a lean diff of similar size that does the same job. Ship
criterion: the over-built fixture draws an `over_building` finding naming at least two of the
three plants in ≥ 3 of 5 runs; the lean fixture draws none in ≥ 4 of 5; neither draws a new
critical or major. Spend is estimated and approved before the paid run.

**CI:** the protocol size and bundle-sync checks cover `authoring.md`; the `tools/list`
description contract test from v0.22.0 runs unchanged, since no input changed.

## Compatibility

- Every envelope change is additive and `omitempty`. A consumer that ignores unknown fields
  sees no difference on non-lightweight calls.
- The `summary_block` grammar gains one optional key in the header region. Guard 0.4.0 parses
  such a block correctly (fixture-proven, §4.5).
- Guard 0.5.0 against a v0.22.0 server: Rule B works from the blank `session_id:` line alone;
  Rule A does not depend on the server at all.
- A controller with a non-canonical lightweight clause gets a false block from Rule B until
  the heading line is added. Documented; kill switch available.

## Release

Branch `version/0.23.0`, merge with `[minor]`. `CHANGELOG.md` `## [0.23.0] - 2026-09-18`:

- **Added:** `implementation_guidance` on `validate_task_spec`; the `over_building` criterion at
  plan, task-start, mid-task and completion; `authoring.md` §3.10; `lightweight` / `mode:
  lightweight` on lightweight completions; guard 0.5.0 with `check-task-start`, the no-session
  close rule and `ANTI_TANGENT_SESSION_GUARD`; the ponytail attribution.
- **Changed:** the guard README's block-condition count and kill-switch table;
  `examples/lightweight-dispatch.md` names its heading as the guard's marker.

The work splits into two merges to `main`, in this order: the server and protocol parts
(Parts 1–3, §4.5, attribution) with `[skip ci]`, then the guard (§4.3–4.4, 4.6) with `[minor]`,
whose first task is the live `transcript_path` confirmation. One release, after both.

## References

- Ponytail: https://github.com/DietrichGebert/ponytail at `e3ba2aa6f1e6f0bc4d69eb09c9f0d0a93af56156`;
  `skills/ponytail/SKILL.md`, `skills/ponytail-review/SKILL.md`, `hooks/ponytail-runtime.js`
  (the `SubagentStart` output shape), `hooks/ponytail-subagent.js`.
- Prior spec: `docs/superpowers/specs/2026-09-15-field-assessment-improvements-design.md`
  (finding identity, `finding_responses`, `controller_rulings`, `stale_comments`, the replay
  harness).
- Protocol: `docs/protocol/authoring.md` §3.5, §3.9; `docs/protocol/implementer.md` §4.2 steps
  1 and 3, "Lightweight protocol mode"; `docs/protocol/controller.md` §5.
- Code: `internal/mcpsrv/handlers.go` (`Envelope`, `validate_completion`'s lightweight path),
  `internal/mcpsrv/summary.go`, `internal/verdict/finalize.go`, `internal/stats/event.go`,
  `internal/prompts/templates/{plan_comment_rules,post,mid,pre,plan}.tmpl`,
  `plugin/anti-tangent-guard/hooks/{check-task-complete,check-comment-write,hooks.json}`,
  `examples/lightweight-dispatch.md`, `THIRD_PARTY_NOTICES.md`, `internal/notices/`.
