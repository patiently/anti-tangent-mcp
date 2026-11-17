# Spec-driven workflow and opt-in edit gate — design

Status: approved 2026-11-16. Target release: `0.30.0` (minor). Everything is opt-in and
default-off except Part D, which narrows the existing `TaskUpdate` check so it no longer fires on
ordinary to-dos.

## Problem

anti-tangent assumes a written implementation plan: the skill triggers on "a Goal /
Acceptance-criteria header from an implementation plan", `core.md` scopes the server to "tasks
from a written implementation plan", and `controller.md` / `authoring.md` refer to superpowers'
model-routing tiers and `writing-plans`. A user whose workflow is "brainstorm a spec, dispatch
one implementer against the spec's acceptance criteria" has no documented path, even though the
server already handles it.

Separately, the guard's start gate only covers subagents whose dispatch prompt carries the §4.2
clause heading. A user who wants "no write lands before `validate_task_spec`", including
writes in the main session, has no way to ask for that.

## Goal

1. Document a plan-less, spec-driven loop (`validate_task_spec` → `check_progress` →
   `validate_completion`, no `plan_run_id`) as a first-class path, with the plan-driven path
   unchanged.
2. Add an opt-in, default-off guard mode that refuses `Edit`/`Write`/`NotebookEdit` until the
   session's own transcript holds a successful `validate_task_spec`, for subagents only or for
   every session including the main one.
3. Under the same switch, refuse once to let a session stop when it opened a task with
   `validate_task_spec` and has not closed it with `validate_completion`. Without this, a flow
   that never calls `TaskUpdate` has a start gate and no end gate (Part C).

## Non-goals

- No server (Go) change. `validate_task_spec` already treats `plan_run_id` as optional. Without
  one, the only plan-related output is a minor `plan_run_id` advisory, and that appears only
  when this server process holds a live plan run, which a plan-less user never creates
  (`planRunIDAdvisory`, `internal/mcpsrv/handlers.go`; `attachedByTitleAdvisory`,
  `plan_run_rows.go`). No prompt template changes, so no golden churn.
- No network call, no credential read, no subprocess. The only new hook registrations are
  `Stop` and `SubagentStop` (Part C), and both do nothing unless the switch is set. The only new
  files are empty once-per-task markers beside the trace log, the same kind the progress
  reminder already writes.
- The `TaskUpdate` completion gate's own rules are unchanged except for one narrowing: its
  "closed without `validate_completion`" block now fires only for protocol tasks (Part D). The
  stop check is a second, independent way to reach the same requirement.
- No change to `validate_plan`, `plan_run_report`, `record_review_outcome` or lightweight mode.

## Part A — protocol (`anti-tangent-protocol` 0.4.1 → 0.5.0)

Every part stays under the 16,000-byte CI budget. `core.md` is at 15,995 bytes, so this part
moves its `## Environment variables` section (854 bytes; it already defers to the README for the
full list) into a one-line README pointer to make room. `docs/protocol/` and the plugin bundle
are resynced in the same commit. Section numbers do not change.

- **`SKILL.md` description and "When this applies":** the trigger becomes "a task with
  acceptance criteria — from an implementation plan's task block, a spec's Goal / Acceptance
  criteria section, or a goal stated in the conversation". The last two clauses are word for word
  the claude-sandbox spec's trigger, so the two specs agree.
  The controller bullet in Step 2 gains "plan runs only" on the `validate_plan` /
  `plan_run_report` instructions.
- **`core.md`:**
  - Intro: "tasks from a written implementation plan" becomes "specced tasks, from a plan or a
    spec".
  - §1: a short **Two workflows** paragraph. *Spec-driven* (no plan): one implementer per spec,
    which passes the spec's Goal / Acceptance criteria / Non-goals verbatim and the spec's
    absolute path in `context_paths`, then runs `validate_task_spec` → `check_progress` →
    `validate_completion` with no `plan_run_id`, `task_index` or controller gate. *Plan-driven*:
    the existing §5 controller flow. The "didn't come from a plan" skip bullet becomes "has no
    spec or plan behind it".
  - The audiences list and "system-agnostic" line drop superpowers as the lead example; it stays
    listed as one harness among others.
- **`controller.md`:** "Which tier a review earns" stops naming superpowers'
  `model-routing.json`. It describes the bar (default to a mid-tier reviewer, escalate only for
  the COMPLEX bar) and says a harness with a routing file maps it there. No protocol part names
  `~/.claude/superpowers/model-routing.json` or depends on it existing. §5.1 says outright that
  a spec-driven run has no plan gate and no `plan_run_id`.
- **`authoring.md` §3.1:** "This is the shape superpowers' `writing-plans` skill emits" becomes a
  neutral note: plan generators, including `writing-plans`, emit this shape, and a spec's closing
  Goal / Acceptance criteria section uses the same field names. The `Acceptance Criteria`
  casing note and the "add Non-goals by hand" note stay.
- **`implementer.md`:** the Task-spec field list marks `plan_run_id` / `task_index` as "plan runs
  only", and adds a one-line spec-driven note: the spec path goes in `context_paths`.

## Part B — opt-in edit gate (`anti-tangent-guard` 0.7.1 → 0.8.0)

The gate extends the existing `check-task-start` hook (`PreToolUse` on
`Edit|Write|NotebookEdit`). No new hook entry; its `hooks.json` entry gains `"timeout": 10`,
because the main-session transcript it may now read is much larger than a subagent's.

### Switch

`ANTI_TANGENT_EDIT_GATE`, case-insensitive:

| Value | Who is gated |
|---|---|
| unset / `off` (default) | Exactly today: a subagent whose dispatch prompt carries the §4.2 heading. |
| `subagents` | Every dispatched subagent (any payload with `agent_id`), clause heading or not. |
| `all` | Every subagent, and the main session (no `agent_id`, reads `transcript_path`). |

Any other value is treated as `off`, with one trace line `skip | bad-edit-gate-value`. This is
the guard's existing fail-open rule.

**What `all` keys on in the main session.** The main session has no dispatch prompt, so nothing
is fingerprinted: under `all`, a payload with no `agent_id` is gated by the mode alone, and the
transcript read is `transcript_path` itself (not a `subagents/` file). Under `subagents`,
likewise, a subagent is gated by having an `agent_id`, not by its prompt's heading.

A subagent whose dispatch prompt carries the lightweight heading stays exempt in every mode. The
controller ruled it lightweight in the dispatch prompt, which the subagent cannot rewrite.

### Precedence with the existing switches

- `ANTI_TANGENT_EDIT_GATE` is independent of `ANTI_TANGENT_SESSION_GUARD`. `SESSION_GUARD=0`
  keeps turning off what it covers today, and only that: the start gate fingerprinted by the
  clause heading, and the close-time no-session rule. In the wrapper, `SESSION_GUARD=0` with
  `EDIT_GATE` off exits 0 as today. With `EDIT_GATE` set to `subagents` or `all`, the hook runs
  in that mode, which already covers every clause-dispatched subagent by its `agent_id`. A
  deployment that silenced the old start gate can therefore still opt in to the new one, and
  turning a mode off again means lowering `EDIT_GATE`.
- `ANTI_TANGENT_PROGRESS_GUARD`, `ANTI_TANGENT_COMPLETION_GUARD` and
  `ANTI_TANGENT_COMMENT_GUARD` are unaffected and unaffecting. The progress reminder already acts
  in the main session after a `validate_task_spec`. The two gates line up: the edit gate passes
  at the same call the reminder starts counting from.

### What passes

The same rule the start gate uses today, unchanged: a `validate_task_spec` **`tool_use` entry in
the session's own transcript** whose `tool_result` is not an error. The scan stops at the first
success. As a consequence:

- A pasted envelope (`anti-tangent envelope` / `tool: validate_task_spec` text) in a user message,
  a `tool_result` or a subagent's report never counts. Only a call this session made does,
  matching the tagged-envelope rule the completion guard follows.
- A controller's call does not unlock its subagent, and a subagent's call does not unlock the
  main session. Each session needs its own call.
- An errored call (secret-gate refusal, reviewer outage) does not count, and the block message
  says the call failed (guard 0.7.1 behaviour, kept).
- The main session is unlocked for the rest of that session by one call. `/clear` starts a new
  transcript and re-arms the gate. A compaction does not, because the earlier JSONL lines remain.

Performance: in `all` mode a main-session transcript can run to tens of MB. The scan skips any
line that names neither the tool nor a pending `tool_use_id` before calling `json.loads`.

### Which paths are gated

Only a target inside the session's `cwd` (from the payload) is gated. The target is compared by
`os.path.realpath` on both sides, so `..` segments and symlinks resolve before the check. A
target outside `cwd` passes: memory files, the scratchpad, `~/.claude*` and other repos are
not project drift, and gating them would break auto-memory in `all` mode.

Inside `cwd`, a target matching an exemption glob passes:

- `ANTI_TANGENT_EDIT_GATE_EXEMPT`: colon-separated globs relative to `cwd`. `**` crosses
  directories, `*` does not. Unset means the default `docs/**/*.md`. Set but empty means nothing
  is exempt.
- **Default `docs/**/*.md`, deliberately not `*.md`.** That covers `/brainstorm`'s
  `docs/superpowers/specs/` and other docs. A blanket `*.md` would also exempt `CLAUDE.md`,
  `AGENTS.md`, `SKILL.md`, `.claude/commands/*.md` and agent definitions, which are instructions
  that change behaviour. A repo whose product is Markdown under `docs/` (this one:
  `docs/protocol/`) narrows the list per repo.
- The match uses the resolved path, so a `docs/x.md` symlink to `src/main.go` is gated.

### Getting out of it (main-session cost)

With `all`, a one-line fix also needs a `validate_task_spec`. The way out never runs inside the
blocked session. The hook reads its environment at launch and the session cannot change it:

1. **The cheap path, in session:** the block message says to call `validate_task_spec` with a
   one-line goal and one acceptance criterion. That is one reviewer call. Nothing else is offered
   in-band.
2. **Per session:** launch with the switch set, `ANTI_TANGENT_EDIT_GATE=subagents claude` or
   `=off`.
3. **Per repo:** set `ANTI_TANGENT_EDIT_GATE` in the `env` block of that repo's
   `.claude/settings.local.json`, which beats user scope. A user who enables `all` globally can
   turn it down where it does not fit. The file sits inside `cwd` and is not exempt, so the
   blocked session's `Write` to it is refused. Its `Bash` is not; see Honesty. *To verify during
   implementation:* that settings `env` reaches hook processes, and whether a mid-session edit
   takes effect before restart. The README will state what is observed.

Considered and rejected: an in-chat release phrase typed by the human. Telling a human-typed
prompt apart from skill or command expansions depends on the undocumented `origin.kind` field in
the transcript, and a valve that silently stops working leaves the user stuck.

### Settings precedence and the per-repo bypass (verified 2026-11-16)

A headless `claude -p` run showed that a `.claude/settings.local.json` `env` value reaches
hook processes and wins over the launch environment. Two consequences, both stated in the
README: the per-session launch override works only when no settings file sets the variable,
and the per-repo setting is a bypass. An agent can write `off` there, through `Bash` while
blocked or with `Write` after validating, and every later session in that repository starts
ungated. "A blocked session cannot lift its own block" therefore holds per session, not per
repository. No countermeasure is built; that is Patrick's call.

### Honesty (README)

This is a drift guard, not an adversarial control:

- `Bash` writes (`cat >`, `sed -i`, `git apply`) bypass it, as they bypass the start gate and the
  comment guard today. The same `Bash` can edit `settings.local.json`.
- The transcript layout (`subagents/agent-<id>.jsonl`) is what Claude Code writes today, not a
  documented interface. A missing or unreadable transcript allows.
- Any hook failure allows: no `python3`, bad payload, a timeout. The trace log records why.

### Block message

It names the mode that caused the block, the cheap path, the exempt globs in force, and the
switch that governs it (`ANTI_TANGENT_EDIT_GATE`), with the note that it takes effect at launch. In the default mode the existing messages are unchanged.

## Part C — stop check (`anti-tangent-guard`, same 0.8.0)

New hook pair `check-task-stop` (bash wrapper) and `check_task_stop.py` (body), registered on
`Stop` and `SubagentStop` with `"timeout": 10`. It imports the transcript helpers from
`check_task_start.py` instead of copying them.

### When it acts

| `ANTI_TANGENT_EDIT_GATE` | `Stop` (main session) | `SubagentStop` |
|---|---|---|
| unset / `off` | exits 0 without reading stdin | exits 0 without reading stdin |
| `subagents` | exits 0 | checks |
| `all` | checks | checks |

`ANTI_TANGENT_EDIT_GATE` alone turns it on. Its own kill switch, `ANTI_TANGENT_STOP_GUARD=0`,
turns off the stop check and nothing else, so the edit gate stays. `ANTI_TANGENT_COMPLETION_GUARD`
and `ANTI_TANGENT_SESSION_GUARD` do not affect it. That matters because claude-sandbox runs with
`COMPLETION_GUARD=0` (the `TaskUpdate` check's noise; see Part D), and tying the stop check to
that switch would leave it inert in the setup it was built for.

### The rule

It reads the stopping session's own transcript: `transcript_path` for `Stop`, and the subagent's
for `SubagentStop`. That is `agent_transcript_path` when the payload carries it, otherwise the
same `<stem>/subagents/agent-<agent_id>.jsonl` derivation the start gate uses. The payload shape
is checked against the current Claude Code during implementation.

- It finds the last successful `validate_task_spec` call. The rule for "successful" is the
  start gate's: a `tool_use` this session made whose `tool_result` is not an error. A pasted
  envelope never counts.
- If there is none, it allows. No task was opened, and under the gate's modes the start gate
  already kept such a session from editing project files.
- If a successful `validate_completion` call comes after it, it allows. The task was closed,
  whatever the verdict. The verdict itself is the agent's to act on, as with the per-task tools.
- Otherwise it blocks with exit 2. The message says the task opened by `validate_task_spec` was
  never closed: call `validate_completion` with the session's `session_id` and the evidence,
  then finish. If every `validate_completion` call errored, the message says that instead.

### Never trapping the session, and not asking too early

`Stop` fires at the end of every turn, including one that pauses mid-task to ask the user
something, so the ask is keyed on the task's last edit rather than on the task:

- **No edit, no ask.** With no `Edit`/`Write`/`NotebookEdit` call since the spec call there is
  nothing to review, and the hook allows (`pass | no-edits`).
- **Once per stretch of edits.** Before blocking it creates a `stop-asked-<hash>` file next to
  the trace log, exclusively, with `O_CREAT|O_EXCL`. The hash covers the transcript path, the
  `validate_task_spec` call's id and the id of the task's last edit. When the file already
  exists, it allows. A session asked while pausing, and told by the message to say so and stop
  without calling `validate_completion`, is asked again only once it has edited further. The
  ask therefore still comes at the task's real end, and a pause costs one extra model turn,
  not a reviewer call.
- `stop_hook_active: true` in the payload also allows. That is Claude Code's own
  already-continued-once signal, and honouring it guards against a marker that cannot be
  written.
- Fail-open like every other hook: no `python3`, a bad payload, an unreadable transcript, a
  marker directory that cannot be created, or a timeout all allow, with a trace line.

### How it fits the other gates

- It does not re-arm the start gate after `validate_completion`. Edits made afterwards to address
  findings stay allowed, as the progress reminder already treats them.
- The "one call unlocks the whole session" gap in `all` narrows. Work on a second task without a
  new `validate_task_spec` ends in a stop the check lets through, because the first task was
  closed. That second task is still unreviewed. The README says so plainly: the stop check
  enforces that an opened task is closed, not that every change belongs to a task.
- In a plan-driven run that also uses `TaskUpdate`, both checks can fire for the same task. Each
  is satisfied by the same `validate_completion` call, so the work is done only once.

## Part D — `TaskUpdate` completion check scoped to protocol tasks (`anti-tangent-guard`, same 0.8.0)

**This is the one behaviour change to a default-on check in this release.** Today
`check-task-complete` blocks every `TaskUpdate` → `completed` whose window shows no
`validate_completion`, ordinary to-dos included. That noise is why claude-sandbox runs with
`ANTI_TANGENT_COMPLETION_GUARD=0` and so loses the check entirely.

**New rule.** The "closed without `validate_completion`" block (the `no-validation` path) fires
only when the task's window, as today's "Window scoping" defines it, shows the task is a protocol
task:

- a `validate_task_spec` `tool_use` in the window;
- an `Agent` / `Task` `tool_use` whose prompt carries the full clause heading
  (`## Drift-protection protocol (anti-tangent-mcp)`) or the lightweight one; or
- the lightweight marker the hook already collects (`lite_idx`).

A close with none of these exits 0 and logs a trace line `skip | not-protocol-task`.

**Unchanged.** Every path that starts from a `validate_completion` call or a tagged pass signal
behaves exactly as today: a `fail` verdict, every call errored, the no-session rule, and the
close-time comment scan. Those already imply a protocol task. Both kill switches keep their
meaning.

**Coverage given up, stated in the CHANGELOG and README.** Suppose a controller implements a plan
task itself, in its own session, with no `validate_task_spec` and no dispatch. That close used to
be blocked and now passes. Today's block assumed every `TaskUpdate` was a plan task, so it could
not tell this case from a to-do. Under `ANTI_TANGENT_EDIT_GATE=all` that session needs
`validate_task_spec` before editing anyway, which brings the close back into scope.

**Window fallback.** When the task has no `in_progress` entry, today's fallback scans the whole
transcript. Then any earlier `validate_task_spec` in the session makes a later plain to-do look
like a protocol task. That is the existing over-reach, made no worse, and the README says so.

## What auto-executes (for the CHANGELOG and the downstream audit)

- `check-task-start` already runs on every `Edit`/`Write`/`NotebookEdit`.
- New: `check-task-stop` runs on every `Stop` and `SubagentStop`. With the switch off it reads
  one environment variable and exits 0 before reading stdin. With it on, it reads the stopping
  session's own transcript, and at most once per stretch of edits it creates one empty marker file beside
  the trace log.
- Default (`off`): one more environment read, then today's path, byte for byte.
- `subagents`: reads the subagent's own transcript (as today), now for every subagent.
- `all`: also reads the main session's `transcript_path`. That is the one new read, and it is a
  file the session itself writes. It also calls `realpath` on the target and on `cwd`.
- Changed, default-on: `check-task-complete` (already running on every `TaskUpdate`) now also
  looks for protocol evidence in the task window before its no-validation block (Part D). It
  reads nothing new, because it scans the same window it already scans.
- No network, no credential or secret read, no subprocess, and no writes beyond the existing
  trace log and the stop check's empty markers.

## Tests

In `check_task_start_test.py` (unit) and `evals/guard-evals.json` (end to end through the bash
wrapper, which is where the environment variables are read):

| Case | Expect |
|---|---|
| off by default: unset env, main session, no call | allow (exit 0), trace `not-gated` |
| off by default: unset env, subagent without the clause | allow |
| `subagents`: subagent without the clause, no call | block |
| `subagents`: same, after a successful call | allow |
| `all`: main session, no call | block |
| `all`: main session, successful call | allow |
| `all`: main session, only errored calls | block with the errored message |
| `all`: pasted envelope text only (no `tool_use`) | block |
| exempt: `docs/specs/x.md` under default globs | allow |
| not exempt: `CLAUDE.md`, `docs/x.go`, symlinked `docs/x.md` → `src/` | block |
| outside `cwd` (`/tmp/x`, `../other/x`) | allow |
| `ANTI_TANGENT_EDIT_GATE_EXEMPT=` (empty) makes `docs/x.md` gated | block |
| bad value (`yes`) | allow, trace `bad-edit-gate-value` |
| lightweight dispatch under `subagents` | allow |
| stop, off by default: spec call, no completion | allow, stdin not read |
| stop, `all`, main session: spec call, no completion | block |
| stop, `all`: spec call then successful completion | allow |
| stop, `all`: spec call, completion only errored | block with the errored message |
| stop, `all`: pasted `tool: validate_completion` envelope only | block |
| stop, `all`: no spec call at all | allow |
| stop, `all`: second stop with no edit in between (marker exists) | allow |
| stop, `all`: spec call and no edit since | allow, `no-edits` |
| stop, `all`: asked, then more edits, then stop again | block again (unit test: the harness cannot change a transcript between invocations) |
| stop, `all`: completion for task A, then a new spec call for B without completion | block |
| stop, `subagents`: `Stop` in main session | allow |
| stop, `subagents`: `SubagentStop` with an open task | block |
| stop, `STOP_GUARD=0` with `all` | allow; the edit gate still blocks |
| stop, `COMPLETION_GUARD=0` with `all` | **block**: the stop check is not tied to it |
| edit, `SESSION_GUARD=0` with `all` | **block**: the edit gate is independent |
| edit, `SESSION_GUARD=0` with `off`, clause-dispatched subagent | allow, as today |
| TaskUpdate close, plain to-do (no protocol evidence in window) | allow, trace `not-protocol-task` |
| TaskUpdate close, window has `validate_task_spec`, no completion | block, as today |
| TaskUpdate close, window has a full-clause dispatch, no envelope | block, as today |
| TaskUpdate close, lightweight dispatch, no completion | block, as today |
| TaskUpdate close, plain to-do with a `fail` completion verdict | block, as today |
| stop, `stop_hook_active: true` | allow |

Each case is shown to discriminate. The guarding line is broken (for example the mode check
forced to `off`, the realpath dropped, the errored-result check removed), the case is watched
fail, and the line is restored. The break and the failing output are recorded in the
implementation report, not in the code.

## Docs and release bookkeeping

- `CHANGELOG.md` `## [0.30.0]`: Added (edit gate, spec-driven protocol path), Changed (protocol
  wording, `hooks.json` timeout), and a "What auto-executes" paragraph copied from above.
- Guard README: new "Edit gate (opt-in)" and "Stop check (opt-in)" sections, Kill switches, Configuration
  (`ANTI_TANGENT_EDIT_GATE`, `ANTI_TANGENT_EDIT_GATE_EXEMPT`), Fail-open. Also the "Active on
  install" paragraph and `plugin.json` description. Root README guard summary.
- Root `CLAUDE.md` "What This Repo Is Not": the guard's list of blocking behaviours and kill
  switches gains the opt-in gate.
- Branch: CI ties `version/X.Y.Z` to the CHANGELOG. This work is on
  `feature/spec-driven-edit-gate`, so it gets renamed to (or cherry-picked onto)
  `version/0.30.0` before a PR. No `version/0.30.0` exists today. `VERSION` is not bumped on the
  branch.

## Decisions

- `ANTI_TANGENT_EDIT_GATE` is independent of `ANTI_TANGENT_SESSION_GUARD`.
- The stop check is governed by `ANTI_TANGENT_EDIT_GATE`, with its own `ANTI_TANGENT_STOP_GUARD`
  kill switch.
- The `TaskUpdate` scoping (Part D) ships in 0.30.0.
- The default exemption is `docs/**/*.md`.
- The lightweight-heading exemption holds in every mode.
- The switch values are `off | subagents | all`.
