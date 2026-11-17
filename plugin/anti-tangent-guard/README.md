# anti-tangent-guard

Five hooks enforcing anti-tangent-mcp's conventions. Four are on by default: a `PostToolUse` hook that
mandates the `validate_completion` gate at task close, blocks a full-protocol
close that ran with no task session, and detects when submitted diffs add
comments carrying change history; a `PreToolUse` hook on `Edit`/`Write`/
`NotebookEdit` that refuses a dispatched implementer's first edit until
`validate_task_spec` has been called; a `PreToolUse` hook that prevents
such comments from being written in the first place; and a `PostToolUse` hook
on `Edit`/`Write`/`NotebookEdit` that asks a task, once, for a
`check_progress` call when it reaches ten edits without one.

Two more behaviours are **off by default** and turned on together by one switch,
`ANTI_TANGENT_EDIT_GATE` (`off`, `subagents` or `all`): the start gate widens into an
**edit gate** covering every subagent, or every session including the main one, and the
fifth hook, on `Stop` and `SubagentStop`, adds a **stop check** that asks a session to close
the task it opened before it stops, blocking the stop once per stretch of edits. They exist for a spec-driven workflow with no plan, where no
`TaskUpdate` ever fires. See "Edit gate (opt-in)" and "Stop check (opt-in)" below.

## Active on install

Four of its hooks are active with no configuration step. As soon as it is
installed, every `TaskUpdate` call is watched for the completion gate, every
`Edit`/`Write`/`NotebookEdit` call inside a dispatched subagent is watched for
the start gate, and every `Edit`/`Write` call is intercepted for the write-time
comment-hygiene scan, and each of those edits is counted toward the progress
reminder — there is nothing further to turn on. The `Stop`/`SubagentStop` hook is
registered too, but with `ANTI_TANGENT_EDIT_GATE` unset it exits before reading its input.

## What it does, and what it does not do

`PostToolUse` fires **after** the tool call it matches has already run — the
task's status is already `completed` by the time this hook sees the event. It
**cannot prevent** the close. What it does is post-close detection plus a
mandated recovery flow: `exit 2` returns the hook's stderr to the model as
something it must address before its next action, without refusing a state
change that may well be correct. A `PreToolUse` hook could block the close
outright, but could not see the subagent's pasted `summary_block`, which only
exists in the *result* of the very call being closed.

The hook enforces the gate this way — as an installable plugin hook, not as
logic inside the MCP server — deliberately. anti-tangent-mcp's server is
advisory: it never blocks a caller (see the root `CLAUDE.md`, "What This Repo
Is Not"). Enforcement, where a project wants it, lives here instead.

The hook watches for a `TaskUpdate` whose `tool_input.status` is `completed`.
Every other tool call, and every other status, is a silent no-op (exit 0).

## The four block conditions

For a matching close, the hook scans the transcript window for this task (see
"Window scoping" below) for two possible pass signals: a direct
`mcp__anti-tangent__validate_completion` tool call, or an `anti-tangent
envelope` / `session_id:` summary block, **tagged `tool: validate_completion`**,
pasted into a `tool_result` (this is how a subagent's report of running the
gate becomes visible from the controller's own transcript). It blocks
(`exit 2`) in exactly four cases:

1. **Neither signal is present, and the task is a protocol task.** Nothing in
   the window shows the completion gate ran at all. A protocol task is one whose
   window holds a `validate_task_spec` call, an `Agent`/`Task` dispatch (or, in a
   subagent, its own prompt) carrying the full clause heading or the short
   variant's `## Drift protection` heading (as a line of its own), or the
   lightweight marker; the window for this test starts at the task's first
   `in_progress`, as the lightweight marker's does. Any other close — an ordinary
   to-do — never owed a `validate_completion` call and passes with
   `skip | not-protocol-task`. Two cases are given up by this. A controller that
   implements a plan task in its own session with no `validate_task_spec` and no
   dispatch is no longer blocked at close; with `ANTI_TANGENT_EDIT_GATE=all` that
   session cannot edit without a `validate_task_spec` call, which brings the close
   back into scope. And a `validate_task_spec` made before the task's first
   `in_progress` falls outside the window, so a session that calls it first and
   marks the task `in_progress` afterwards is not blocked at close. When the task
   has no `in_progress` entry the window is the whole transcript, so an earlier
   `validate_task_spec` in the session makes a later to-do look like a protocol task. A `validate_completion` call whose result is a tool error
   (a reviewer outage, a secret-gate refusal) is not a call; when every call in
   the window errored, the block message says so rather than saying none was
   made.
2. **The last qualifying signal's verdict is `fail`.** The gate ran, but its
   most recent verdict in the window says the work is not done. This is read
   from either signal: a pasted summary block tagged `tool:
   validate_completion`, or the direct call's own MCP result — the real
   tool result arrives JSON-marshalled, with the summary block's newlines
   surviving only as escapes inside one JSON string, so the hook parses that
   JSON directly for `verdict` rather than pattern-matching the escaped
   text.
3. **The evidence that call submitted adds comment lines carrying change
   history.** A scan of added lines — from the submitted diff, or from git
   for a completion that submits `final_files` — detects comments matching a
   pattern set (patterns stored in `comment_scan.py`), the same patterns the
   write-time hook applies to `Edit` and `Write` calls. This scan detects
   rather than prevents, catching comments that reached disk through `Bash`
   or other pathways the write-time hook cannot intercept.

4. **The last `validate_completion` in the window ran with no task session, and
   nothing in the window shows a lightweight dispatch.** An empty `session_id`
   is the server's lightweight path: the review is made against a synthesized
   spec with no acceptance criteria. Under the full protocol that means step 1
   (`validate_task_spec`) was skipped. The empty session is read from the
   direct call's input, or from a pasted block whose `session_id:` line is
   blank or which carries `mode: lightweight`. The lightweight marker is the
   heading `Drift-protection protocol (lightweight)` — the one
   `examples/lightweight-dispatch.md` carries — found in a user message's own
   text or in the `prompt` of an `Agent` tool call. Inside a `tool_result` it
   is file content, not a dispatch decision, and does not count. Absence means
   full protocol: the default dispatch is the full clause, so the default is
   to block. Unlike the rest of this window (see "Window scoping" below), the
   marker itself is looked for from the task's *first* `in_progress`, not its
   last, so a task dispatched lightweight and later reopened keeps its
   original dispatch marker. Kill switch: `ANTI_TANGENT_SESSION_GUARD=0`.

   Two limits. Under executing-plans there is no dispatch prompt, so a
   lightweight task there needs the heading in the user's instruction — or
   `validate_task_spec` gets called, which is the right outcome. And the hook
   sees that `validate_task_spec` ran, not when: a call made after the edits
   satisfies this rule; the start gate below is what enforces the order, and
   only in dispatched-subagent sessions.

The first two messages and the fourth state the same recovery flow explicitly: reopen the task with
`status=in_progress`, address whatever the gate is asking for, run
`mcp__anti-tangent__validate_completion` (again), and only then re-close.
The third message names the pattern set and instructs the same recovery.

### Why the summary block must be tagged `tool: validate_completion`

`validate_task_spec`, `check_progress`, and `validate_completion` all render
the identical `anti-tangent envelope` / `session_id:` / `verdict:` text —
`formatEnvelopeSummary` is shared verbatim by the three of them. Without a
tool discriminator, a `check_progress` (or `validate_task_spec`) summary
block pasted into a report would satisfy this guard's pass signal exactly
like a `validate_completion` block does, and that tool's verdict would be
misread as validate_completion's. Since anti-tangent-mcp 0.18.0 every
envelope carries a `tool:` line naming the MCP tool that produced it, and
this hook's pass signal and verdict extraction are both scoped to blocks
where that line reads `tool: validate_completion`.

**The tag alone is not the whole defense.** A finding's `Evidence` (and
`Criterion`, and an envelope's `next_action`) is reviewer-authored free text
constrained only to be non-empty — nothing stops it from containing the
literal line `tool: validate_completion`, or `verdict: pass`, as part of its
own content. Two things close that gap, and both matter — an early
version of this hook that tagged blocks but scanned a block's whole text for
the target pattern was fooled by exactly this, reading a forged tag out of
finding text as if it were the header (task-12b-review.md Critical #1):

1. **Positional extraction.** `formatEnvelopeSummary` always writes the
   `tool:`/`session_id:`/`verdict:` lines in a fixed header region, before
   any finding is rendered. This hook takes the FIRST `tool:` line and the
   FIRST `verdict:` line within a block — never a scan for the target value
   anywhere in it — so a line that happens to read the same way, reachable
   only through a finding's text, can never be mistaken for the genuine one.
2. **Source-side escaping.** Since the same release, the server folds every
   continuation line of a multi-line free-text field behind a non-whitespace
   sentinel (`| `), so such a line cannot match this hook's `^\s*label:`
   patterns — or present as a second, bare `anti-tangent envelope` header —
   whatever it contains. `internal/blocktext` holds that one rule;
   `internal/mcpsrv/summary.go` (the per-task envelope, and `validate_plan` /
   `prime_project_knowledge` / `extract_project_knowledge`, whose headers
   carry the same substring) and `internal/planrun/report.go`
   (`plan_run_report`, whose header does not) both apply it.
   `internal/mcpsrv/summary_forgery_test.go` is what keeps it applied: it
   enumerates the block renderers from the server's own source — keyed on
   what a renderer's output is **assigned to**, not on what header literal it
   writes — and drives a forged payload through every free-text field, map
   key and map value of each. That key changed because the earlier one, which
   looked for an `anti-tangent envelope` header literal, was blind to
   `plan_run_report`'s honestly-different header and let two unescaped
   caller-supplied fields ship. Read that test file's header for what the
   scan can and cannot see — it is not a completeness proof, and neither is
   this bullet.

The two defences cover different halves of the problem, and the split
matters:

- Positional extraction is a **within-block** defence. It stops a forged
  `tool:`/`verdict:` line further down a genuine block from being read as
  that block's header, and it needs nothing from the server — so it holds
  even against an older server's un-escaped text (see "Version requirement"
  below).
- Source-side escaping is what stops a forged **block boundary**. A bare
  `anti-tangent envelope` at column 0 inside a field does not get misread
  within its block — it starts a *new* one, which the hook then parses
  positionally and entirely correctly, out of text a reviewer wrote.
  Positional extraction cannot help there; only escaping can, and only on a
  server that has it.

Neither is a standalone fix, and they are not interchangeable.

### How much to trust each pass signal

Read this before relying on the guard for anything.

**The direct signal is the one that cannot be fabricated.** An
`mcp__anti-tangent__validate_completion` `tool_use` entry in the window is
written into the transcript by the harness because the model actually called
the tool. Nothing the model writes as text can produce one.

**The marker path is not a security boundary.** It accepts pasted text, and
nothing in a transcript distinguishes text a subagent copied out of a real
tool result from text it composed. An agent that wants to skip the gate can
simply write an `anti-tangent envelope` / `tool: validate_completion` /
`verdict: pass` block into its own report — verified: that satisfies this
hook, with no gate call anywhere in the window. That is inherent to reading a
transcript rather than an oversight waiting to be patched; closing it would
need the server to sign each block and the hook to verify the signature. The
marker path exists so a subagent that genuinely ran the gate in its own
session still counts, and it extends that report the same trust the rest of
anti-tangent does.

**Positional extraction and source-side escaping are defence-in-depth, not a
completeness claim.** What they buy is that free text cannot *accidentally*
forge the format: a reviewer's evidence that happens to contain a line
reading `verdict: pass`, a task title with a newline pasted into it, a
CodeScene skip reason copied off a terminal. Positional extraction is a
within-block defence and needs nothing from the server; escaping is what
stops a value opening a new block boundary, and only a server that has it can
supply that (hence the version requirement below). Neither is claimed to
cover every route by which text reaches a `tool_result`. The test that pins
the escaping states its own blind spots in its header, and this file will not
restate them as a finite list: an earlier version of this README claimed the
escaping covered "every formatter that emits an `anti-tangent envelope`
header" and listed three residual gaps; the next review found a renderer with
a different, honest header and two unescaped fields — a fourth kind, not on
the list. Treat a green test suite here as evidence that the known paths are
folded, not as a closed surface.

**This is a drift guard, not an adversarial control.** It raises the cost of
skipping the gate from "say nothing" to "knowingly fabricate a gate result",
which catches the case it is built for: a subagent that closed a task without
running the gate. It is consistent with the server being advisory throughout
(see the root `CLAUDE.md`, "What This Repo Is Not"), and it should not be
load-bearing anywhere a genuine adversary is in scope.

### Version requirement

**This hook requires an anti-tangent-mcp server >= 0.18.0.** A server older
than that emits no `tool:` line at all, and an untagged block does **not**
satisfy the guard (accepting untagged blocks would reintroduce the exact hole
described above). Against an older server, every close with only a summary
block as its evidence — no direct `mcp__anti-tangent__validate_completion`
call in the window — blocks with the "no-validation" message even when the
gate genuinely ran. If you cannot upgrade the server yet, set
`ANTI_TANGENT_COMPLETION_GUARD=0` (see "Kill switch" below) to disable this
hook until you can.

The `tool:` tag is not the only reason for the floor. Source-side escaping is
the half a caller cannot supply locally, so against an older server a genuine
`validate_plan`, `prime_project_knowledge` or `extract_project_knowledge`
block whose finding text contains a bare `anti-tangent envelope` line followed
by `tool: validate_completion` / `verdict: pass` satisfies this hook —
positionally and entirely correctly, because that is a real block boundary
rather than a misread line.

## Window scoping

The hook does not read the whole transcript by default. It scopes its search
to entries at or after the most recent `TaskUpdate` that set this task to
`in_progress` — the current attempt — so a stale pass signal from an earlier,
already-abandoned attempt cannot satisfy the gate for this one. If no
`in_progress` entry exists for the task, it falls back to the whole
transcript.

## Start gate (PreToolUse hook)

`check-task-start` fires on `Edit`, `Write` and `NotebookEdit`, and acts only
inside a dispatched subagent. A hook firing there receives the parent's
`transcript_path` and `session_id`; what marks the subagent is `agent_id`, and
its own transcript sits beside the parent's at
`<transcript_path without .jsonl>/subagents/agent-<agent_id>.jsonl`. A payload
with no `agent_id` is the main session, which this fingerprint never gates — so a
controller editing a CHANGELOG is never touched unless the opt-in edit gate below
says otherwise.

The hook reads the subagent's transcript up to its first user entry — the
dispatch prompt — and gates the session only when that entry carries
`## Drift-protection protocol (anti-tangent-mcp)` and not
`Drift-protection protocol (lightweight)`. In a gated session every write is
refused (`exit 2`) until an `mcp__anti-tangent__validate_task_spec` tool call
appears in that transcript and its result is not a tool error. Reads are never gated: read what the change
touches, call `validate_task_spec`, then edit. There is no cache: the scan
stops at the first successful `validate_task_spec` call, which comes before
every allowed edit, so each check reads only the transcript's opening. When
every call errored, the block message says the call failed rather than that it
was never made.

Limits: writes through `Bash` (`cat >`, `sed -i`) bypass this hook, as they
bypass the comment guard; the close-time no-session rule is the backstop. A
controller that rewrites the clause heading defeats the fingerprint and the
hook fails open — the heading is the contract. The `subagents/` layout is what
Claude Code writes today, not a documented interface: a subagent transcript
that is missing or unreadable exits 0, so a layout change disables the gate
rather than blocking every write. Kill switch: `ANTI_TANGENT_SESSION_GUARD=0`.

## Edit gate (opt-in)

`ANTI_TANGENT_EDIT_GATE` widens the start gate. It is read by the same
`check-task-start` hook, case-insensitively:

| Value | Who is gated |
|---|---|
| unset / `off` (default) | Only the fingerprinted subagents above: no change. |
| `subagents` | Every dispatched subagent (any payload with `agent_id`), clause heading or not. |
| `all` | Every subagent, and the main session, whose own `transcript_path` is read. |

Any other value is `off`, with a `skip | bad-edit-gate-value` trace line.

**Where the value comes from.** The hook reads its process environment. A value in the `env`
block of `.claude/settings.local.json` reaches that environment and **wins over** the
environment Claude Code was launched with: a hook in a session started as
`ANTI_TANGENT_EDIT_GATE=off claude`, in a repository whose `settings.local.json` sets it to
`all`, sees `all`. That was observed for the local settings file; the other settings scopes
were not tested, so assume any settings file that sets the variable beats the launch
environment. A subagent whose
dispatch prompt carries the lightweight heading stays exempt in every mode: the controller
ruled it lightweight in a prompt the subagent cannot rewrite. The main session has no
dispatch prompt, so under `all` nothing is fingerprinted: the mode alone gates it.

**What unlocks it** is the start gate's rule, unchanged: a
`mcp__anti-tangent__validate_task_spec` tool call in the session's **own** transcript whose
result is not a tool error. A pasted envelope (`anti-tangent envelope` /
`tool: validate_task_spec` text in a message or a tool result) is not a call. A
controller's call does not unlock its subagent, and a subagent's call does not unlock the
main session. One call unlocks a session for the rest of it; `/clear` starts a new
transcript and re-arms the gate, a compaction does not.

**Which paths are gated.** Only a target inside the payload's `cwd`. Both sides go through
`realpath` first, so `..` and symlinks resolve before the check. A write outside `cwd` —
auto-memory, a scratchpad, another repository — is not gated. Under `cwd`, a target that
matches an `ANTI_TANGENT_EDIT_GATE_EXEMPT` glob is not gated either. The default is
`docs/**/*.md`, which lets a spec be written before `validate_task_spec` can run. It is
deliberately not `*.md`: that would also exempt `CLAUDE.md`, `AGENTS.md`, `SKILL.md`,
`.claude/commands/*.md` and agent definitions, which are instructions that change behaviour.
The match is on the resolved path, so a `docs/x.md` symlinked into `src/` is gated. These
path rules apply to sessions the edit gate gates; a fingerprinted subagent keeps the start
gate's every-path rule.

**Getting out of it.** Under `all`, a one-line fix needs a `validate_task_spec` too. The
switch is read from the hook's environment, which the blocked session cannot change, so:

1. *In the session:* call `validate_task_spec` with a one-line goal and one acceptance
   criterion. That is one reviewer call, and the block message offers nothing else.
   When the anti-tangent server is not connected this exit is unavailable, and exits 2
   and 3 are the way out.
2. *Per session:* launch with the switch set, `ANTI_TANGENT_EDIT_GATE=off claude`. This
   works only when no settings file sets the variable, because a settings value wins.
3. *Per repository:* set it in the `env` block of that repository's
   `.claude/settings.local.json`. Treat a change as taking effect in the next session,
   because Claude Code does not document whether a running session picks it up. The file
   is inside `cwd` and not exempt, so a blocked session's `Write` to it is refused — but
   see the bypass below.

The default exemption covers only `.md` files under `docs/`. A plan generator that writes a
sibling file there, such as a `docs/superpowers/plans/<plan>.md.tasks.json`, is refused on
that file under `all`, and a controller writing a plan never calls `validate_task_spec`
itself. Add the sibling's glob to the list, for example
`ANTI_TANGENT_EDIT_GATE_EXEMPT=docs/**/*.md:docs/**/*.tasks.json`.

**Switches.** `ANTI_TANGENT_EDIT_GATE` is independent of `ANTI_TANGENT_SESSION_GUARD`.
`SESSION_GUARD=0` still turns off the fingerprinted start gate, but not the edit gate: with
the edit gate on, every clause-dispatched subagent is gated by its `agent_id` anyway. The
hook exits before reading stdin only when both are off.

**What it is not.** A drift guard, not an adversarial control. `Bash` writes (`cat >`,
`sed -i`, `git apply`) bypass it, as they bypass the start gate and the comment guard.

**The per-repository setting is also a bypass.** Because a `settings.local.json` value wins,
an agent can turn the gate off for a whole repository by writing
`"ANTI_TANGENT_EDIT_GATE": "off"` into that file's `env` block: through `Bash` while it is
blocked, or with `Write` once it has called `validate_task_spec`. The block in the current
session stays, but every later session in that repository starts ungated. So "a blocked
session cannot lift its own block" holds per session only; per repository it does not. The
block message tells the agent not to change the setting, which is an instruction, not a
control. Check `.claude/settings.local.json` if a repository that should be gated is not. The `subagents/` layout is not a documented
interface, and every failure allows: no `python3`, a malformed payload, an unreadable
transcript, a payload with no `cwd`, or the 10-second timeout in `hooks.json`. A
main-session transcript can be tens of megabytes; a line that names neither the tool nor a
`tool_use_id` is skipped unparsed.

## Stop check (opt-in)

`check-task-stop` is registered on `Stop` and `SubagentStop` and does nothing unless
`ANTI_TANGENT_EDIT_GATE` is set: `subagents` checks `SubagentStop`, `all` checks both. With
the switch off it exits 0 before reading stdin and writes no trace line. Under `subagents`, a
main-session `Stop` exits 0 once stdin names the event, before Python starts, and writes no
trace line either. Any other value is off, with a `skip | bad-edit-gate-value` trace line. It closes the gap
the `TaskUpdate` completion gate leaves in a flow with no task list: nothing marks a task
done, so without this hook nothing asks for `validate_completion`.

It reads the stopping session's own transcript (`transcript_path` for `Stop`;
`agent_transcript_path` for `SubagentStop`, or the `subagents/` file when the payload lacks
it) and finds the last successful `validate_task_spec` call. With none, it allows. With a
successful `validate_completion` after it, the task is closed and it allows, whatever the
verdict. With no `Edit`/`Write`/`NotebookEdit` call since the spec call, there is nothing to
review and it allows (`pass | no-edits`). Otherwise it exits 2, and Claude Code carries on with the message as the next
instruction: call `validate_completion`, then finish. When every `validate_completion` after
the spec call errored, the message says so. Successful means the start gate's rule: a call
this session made whose result is not an error. A pasted envelope never counts.

`Stop` fires at the end of every turn, including one that pauses mid-task to ask the user
something, so the ask is built not to be spent early. It asks once per stretch of edits:
before blocking it creates an empty `stop-asked-<hash>` file beside the trace log (the hash
is of the transcript path, the `validate_task_spec` call id and the id of the task's last
edit), and when that file already exists it allows. A session asked while pausing, which
the message tells to say so and stop without calling `validate_completion`, is asked again
only once it has edited further — so the ask still comes at the task's real end. A pause
costs one extra model turn, not a reviewer call. It never traps a session. It also allows
when the payload's `stop_hook_active` is `true`, and on every failure: no `python3`, an
unreadable transcript, a marker that cannot be written, or the 10-second timeout.

`stop_hook_active` is shared by every `Stop` hook installed: it is `true` whenever any of
them blocked the stop that led to this turn. A turn another plugin's `Stop` hook forced is
therefore never asked about, even when it edited. The markers need a writable directory
beside the trace log; where the log's directory is not writable — owned by another account,
or a log pointed at `/dev/null` — no ask can be recorded, so none is made and the stop check
is in effect off. Usually nothing is logged when that happens, because the trace line goes
to the same unwritable place, so check that the trace log's directory is writable rather
than looking for `skip | no-state`. A bare relative log name puts the markers in the
hook's working directory. Nothing removes the markers, which are empty, or the
progress reminder's `progress-asked-*` files beside them; to clear old ones, run
`find /tmp/claude-hooks -name 'stop-asked-*' -mtime +7 -delete` (and the same for
`progress-asked-*`) against your trace log's directory.

It enforces that an opened task gets closed, not that every change belongs to a task. A
session that closes task A and then edits for task B without a new `validate_task_spec`
stops freely. It does not re-arm the edit gate after `validate_completion`, so edits that
address review findings stay allowed. In a run that also uses `TaskUpdate`, one
`validate_completion` call satisfies both checks.

Kill switch: `ANTI_TANGENT_STOP_GUARD=0`, which turns off the stop check alone.
`ANTI_TANGENT_COMPLETION_GUARD` does not affect it: a deployment that silences the
`TaskUpdate` check keeps this one.

## Progress reminder (PostToolUse hook)

`check-progress-nudge` fires after every `Edit`, `Write` and `NotebookEdit`.
It reads the session's own transcript — the subagent's when the payload
carries `agent_id` (the file beside the parent's, or `transcript_path` itself
when that already names an agent transcript), the main session's otherwise —
and looks at the window
after the last `mcp__anti-tangent__validate_task_spec` call, which is the
current task. When that window reaches `ANTI_TANGENT_PROGRESS_EDITS` edits
(default 10) and holds no `check_progress` call, the hook exits 2 with a
message asking for one. The edit has already happened and is kept: exit 2 on
`PostToolUse` hands the message to the model and undoes nothing.

A task is asked once. A task here is one `validate_task_spec` call: a session
that validates its spec again starts a new count and can be asked again. The
hook records the ask in a `progress-asked-<hash>` file beside the trace log,
created exclusively, so edits sent in one turn —
whose hooks run at the same time and all read the same count — produce one
message, not one each. The files are empty and are not removed; the hash is
of the transcript path and the `validate_task_spec` call's id.

It stays silent when the transcript has no `validate_task_spec` call (no
task, which also covers a lightweight task and a controller's own edits),
when the task has already called `check_progress`, and once the task has
called `validate_completion` — edits after that are fixes to review findings.

Unlike the start gate it also acts in the main session, because a task run
there with no dispatched subagent has no other transcript. Limits: edits made
through `Bash` are not counted, an edit another hook refused is counted (the
count is of attempts in the transcript), and a session that runs several
tasks is judged only on the one after its last `validate_task_spec` call. Every failure
allows: no `python3`, no readable transcript, a malformed payload, or a
directory the ask cannot be recorded in all exit 0, and the hook's entry in
`hooks.json` carries a 10-second timeout. Kill switch:
`ANTI_TANGENT_PROGRESS_GUARD=0`.

## Write-time comment guard (PreToolUse hook)

A `PreToolUse` hook on `Edit` and `Write` tool calls scans added lines in the
diff-to-be-written for comments matching the same pattern set used by the
close-time scan. It refuses the write (exit 2) if a pattern matches on an
added line in a file type the scanner recognizes (determined by extension,
using the same allowlist `comment_scan.py` maintains). The refusal message
directs the model to remove the flagged comment and resubmit the edit.

This hook fires inside dispatched subagents as well as the main agent, since
`PreToolUse` fires for all `Edit`/`Write` calls regardless of origin.

### Write-time fail-open causes

Like the close-time hook, this one allows the write rather than blocking it
whenever it cannot do its job. Each cause gets its own trace-log reason, so
the log distinguishes a write the scanner cleared from one it never looked at:

| Cause | Trace reason |
| --- | --- |
| `ANTI_TANGENT_COMMENT_GUARD=0` | `skip \| guard=0` |
| `python3` absent from `PATH` | `skip \| no-python3` |
| the scanner body unreadable under `$CLAUDE_PLUGIN_ROOT/hooks/` | `skip \| no-body` |
| the `Write` target cannot be read: a symlink, a FIFO, a directory, or a file past the 2,000,000-byte read cap | `skip \| unreadable-target` |
| the body exited with a blocking status but wrote no matching event — `python3` itself exits 2 when the script vanished between the readability check and the interpreter start | `error \| python-exit=2` (or `=4`) |
| any other unexpected internal error | `error \| python-exit=N` |

The unreadable-target row is the one worth understanding. A `Write` over an
existing file is scanned by diffing the new content against what is on disk,
so a target the hook refuses to read leaves nothing to diff against — and a
scan that cannot see the old text would report the entire file as added and
block on comments that were already there. Allowing the write is the right
call, but it means the write lands **unscanned**: only the close-time scan
still sees it. The refusal to read is deliberate rather than incidental —
`O_NOFOLLOW` declines a symlink at the final component, `O_NONBLOCK` keeps a
FIFO from parking the hook inside `open()`, and an `S_ISREG` check rejects
everything else — because the path is caller-supplied and the hook runs
unsandboxed.

Note that a file whose extension is outside the allowlist is *not* a fail-open
case: it traces `skip | ext=…` and is out of scope by policy, not by failure.

### On-touch: what counts as an added comment line

Both scans (write-time and close-time) compare line *text*, not line
*position*. A comment line whose bytes are unchanged from what was already in
the file — even if it moved to a different line number, a different
function, or a different file region — is not treated as added and is never
scanned, no matter how the surrounding code shifted around it. A comment
line whose bytes changed at all — including when the only change is leading
whitespace from a re-indent or a gofmt-style reflow — **is** treated as added
and is scanned like any newly written line.

This is deliberate, not an edge case the scanner happens to get wrong:
changing a line's bytes is touching it, and the on-touch rule this policy is
built on (see the design spec's "The policy") applies to any touch, not only
a change in wording. So re-indenting an existing comment that already
violates the policy blocks the write until the comment is fixed, while a pure
move of the same, unchanged line — a function relocated verbatim, a file
split with no line inside it edited — does not. If a reflow is about to touch
a violating comment incidentally, fix the comment as part of that same edit
rather than treating the reflow as separate from the touch.

**Limitations:**
- `Bash` writes (heredocs, sed -i, and similar) bypass the `Edit`/`Write`
  matcher entirely — comments written through `Bash` are caught, if at all,
  only by the close-time scan.
- anti-tangent-mcp's own `code_write` tool, called with `target_path`, bypasses
  it the same way: the server writes the file itself, so no `Edit`/`Write` ever
  reaches this hook. It is the sharper case of the two, because the generated
  code never enters the agent's context either, so there is no self-review
  step to fall back on — only the close-time scan and the reviewer layer see it.
- `PostToolUse` detects rather than prevents: the close-time scan fires
  **after** the state change to `completed`, so a comment reaching disk
  through `Bash` blocks further progress only at close time, not at write time.
- The scanner recognizes four comment shapes: a full-line comment, a comment
  trailing code on the same line, a `/* … */` opened and closed on one line,
  and a starred block-continuation line (a KDoc/Javadoc body line). An
  *unstarred* block interior is a known miss — nothing on such a line marks
  it as sitting inside a comment.
- The starred block-continuation shape is the one that cannot be decided from
  the line: ` * text` is byte-identical inside a `/* … */` block and inside a
  Go raw string or a Kotlin `"""` block. It is therefore checked against the
  whole post-change file, which is what keeps a markdown bullet in a string
  literal from refusing its own write.
- Where that whole-file check has nothing to say, the shape alone decides and
  the line is treated as a comment. That covers an `Edit` whose target cannot
  be read, an `Edit` with an empty `old_string`, an `Edit` whose `old_string`
  starts part-way through a line (the added text is then a fragment of no line
  in the file), and the close-time scan of a submitted `final_diff`, which
  carries added lines without the files they came from. A write containing a
  starred markdown bullet in one of those positions can block on a false
  positive; `ANTI_TANGENT_COMMENT_GUARD=0` is the escape hatch if you hit
  this.
- The scanner implements a small pattern set capturing common change-history
  markers. The reviewer layer at completion time covers prose narration the
  patterns cannot catch — an engineer writing a sentence like "I rewrote this
  for clarity" in a comment passes the scanner but may be flagged by the
  reviewer.
- An untracked path under `vendor/`, `third_party/` or `node_modules/` is
  not scanned at close time. Every line of an untracked file counts as
  added, so a vendored file whose header narrates its own upstream history
  would otherwise block the close and demand a rewrite of code this
  repository did not write. Those names are matched against the path
  relative to the repository root, so a checkout that itself lives under
  one of them — a clone inside `third_party/`, a CI workspace under
  `node_modules/` — keeps its own files scanned.
- The extension allowlist (`comment_scan.py`'s `SCAN_EXTS`) is keyed on
  `os.path.splitext`, so a file with no extension — including this plugin's
  own extensionless `check-task-complete` and `check-comment-write` hook
  scripts — falls outside it and is not scanned by either layer.
- A `.vue` file is scanned with the JavaScript rules throughout, its template
  block included. The `<!-- -->` comments of that block are not a comment form
  the scanner recognises, so a comment written there is never checked, and
  template text after a `//` is read as a comment.

Set `ANTI_TANGENT_COMMENT_GUARD=0` to disable the comment-hygiene scan at both
write time (PreToolUse on `Edit`/`Write`) and close time (part of the
PostToolUse scan), while leaving the completion-gate check active.

## The semantic tier (optional, off by default)

The pattern set catches change history that carries a token: a task id, an issue or pull-request
reference, a version a change verb governs, your configured tracker key. History written as
ordinary prose — "the count cap used to return a plain error" — carries none, and no pattern
decides it, because the ambiguity is in what the sentence means.

Set `ANTI_TANGENT_JEV=1` and provide `TYPESAFE_API_KEY`, and the write-time hook asks TypeSafe's
Jev about the comment when the patterns find nothing. **While it is on, comment text from every
repository you edit is sent to TypeSafe**, redacted for anything shaped like a credential.
TypeSafe offers zero data retention on enterprise plans only.

**Order.** The two tiers run inside the same `PreToolUse` hook, in a fixed order: the pattern
tier's regex tells always run first, and a match there refuses the write immediately with no Jev
call at all. Only when the pattern tier finds nothing to block on does the hook go on to ask Jev —
so a clean write is judged by both tiers in turn, while a write the pattern tier already refuses
never reaches the second.

### What it judges

The whole comment block your edit touches, not only the line you added — including lines that were
already there. That is deliberate and it differs from the pattern tier, which judges added lines
alone: this project's comment policy says history in a comment you touch gets rewritten as part of
the change.

The file you are editing is scanned locally to find where comment blocks begin and end, so every
line passes through the hook on your own machine. Only the blocks your edit touched are sent, and
only after credential redaction; a comment elsewhere in the file leaves no trace and never reaches
TypeSafe.

### Settings

A summary; each variable also has its own `###` subsection under "Configuration" below, beside
`ANTI_TANGENT_TICKET_PATTERN`.

| Variable | Default | Meaning |
|---|---|---|
| `ANTI_TANGENT_JEV` | unset | Must be exactly `1`. |
| `TYPESAFE_API_KEY` | unset | Required. |
| `ANTI_TANGENT_JEV_THRESHOLD` | `0.7` | Flag at or above. Anything outside (0, 1] falls back. |
| `ANTI_TANGENT_JEV_MODEL` | `jev-1.13.0` | Pinned: an alias moves under a tuned threshold. |
| `ANTI_TANGENT_JEV_URL` | the TypeSafe endpoint | Honoured for loopback, or for an `https` host listed in `~/.claude/anti-tangent-guard/jev-hosts`. |
| `ANTI_TANGENT_JEV_EXCLUDE` | unset | Colon-separated globs never sent. |

**Why the URL is restricted.** Environment reaches these hooks from several places — your shell,
a CI job, and a repository's own checked-in settings — and an arbitrary endpoint would be handed
your key along with the comment text. The default host and loopback are the only ones that get it
on the strength of the environment alone. Any other host has to be named in
`~/.claude/anti-tangent-guard/jev-hosts`, a file in your own home directory: one hostname per line,
`#` comments allowed, and a line written as a URL contributes its host. A file, not a variable, is
the approval on purpose: a variable would arrive through the same environment a repository
controls, and a repository that can set the URL can set anything beside it. The hook resolves `~`
from the account's password-database entry rather than from `$HOME`, so a `HOME` set in that
environment cannot point it at a file a repository wrote. The file must be a regular file (not a
symlink) owned by you and writable by nobody else; anything less approves nothing. An approved host
is reached over `https` only — the approval names a host, not a plaintext wire your key may cross.
With no file at all, the key goes to the default host and to loopback, and nowhere else.

A rejected URL is silently swapped for the default rather than refused outright, so it never costs
you an edit — but that silence is worth being able to see. Every trace event this call produces
carries a `,url=untrusted-host`, `,url=insecure-scheme` or `,url=unparsable-url` suffix when the
override was rejected — for example `jev-pass|blocks=1,url=untrusted-host` — so you can tell your
proxy was never actually used from the trace log alone, without reading `ANTI_TANGENT_JEV_URL` back
out of your settings. No suffix at all means the configured URL, if any, was used as given.

**What this rule does not defend against.** A repository whose settings you have trusted can
define hook *commands*, not only environment — at which point it can read your key directly, and
no rule here changes that. This restriction is for the accidental and the partially-trusted case:
an endpoint inherited from a shell profile or a CI job, or a repository that sets one for its own
tooling. Trusting a repository's settings is still the decision that matters.

### When it cannot answer

Every failure allows the write: no network, a DNS failure, a TLS failure, a timeout, a malformed
response, an unexpected error. After one failure the tier steps aside for 60 seconds, so a dead
service costs one slow edit rather than every edit, and prints one warning per session.

Missing configuration is not a failure and does not touch either of those: `ANTI_TANGENT_JEV` not
exactly `1`, no `TYPESAFE_API_KEY`, or a path matching `ANTI_TANGENT_JEV_EXCLUDE` all mean the
tier never places a call at all, traced as `jev-skip` with its reason, so a repository that simply
hasn't turned this on never trips the breaker or the once-per-session warning either.

A refusal you disagree with is bounded too: the tier blocks a given file at most twice within a
session — a 30-minute window, so a stamp from an abandoned sitting of work does not spend a later
edit's refusals — then allows the write and says so. That comment still reaches
`validate_completion` at task close, which is the enforcement that exists without this tier at
all.

### Two failure modes worth knowing

Under `python3 -I` the user site directory is dropped, so a Python whose certificates live there
(a python.org install on macOS) cannot verify TLS and every call fails — silently, since failures
allow the write. The per-session warning is how you notice; the trace log names the class. Set
`SSL_CERT_FILE` for a corporate CA.

On Windows there is no `O_NOFOLLOW` or `O_NONBLOCK` — `comment_scan.py`'s capped read ORs both
into its `os.open` flags unconditionally, so that expression raises building the call's own
arguments, before `os.open` runs at all, for every path, existing or not. Only `FileNotFoundError`
is caught separately; this exception falls through to the general handler that returns `None`, so
a brand-new file is no more scannable than an existing one. In practice: every `Write` exits
unscanned there — by the pattern tier as well as this one, since both read the file the same way
for a `Write` — while an `Edit` still scans its added lines, since those come from the tool call's
own operands rather than a file read, but with no post-edit file context, so this tier's
block-comment continuation falls back to the touched fragments alone.

Tracked as [#87](https://github.com/patiently/anti-tangent-mcp/issues/87), which also covers the
close-time hook and carries a reproduction that needs no Windows machine. Every failure here is
silent by design — each caller fails open so an unreadable file can never block a write — so on
Windows a clean hook run is not evidence that anything was scanned.

## Comment-hygiene scan at close

Beyond the first two block conditions above, every close gets one more check,
on its own switch and independent of the completion gate's verdict: the LAST
`validate_completion` call in the task window is scanned for added comment
lines carrying change history — the
same rule the write-time `PreToolUse` hook applies to an `Edit`/`Write`, run
again here as defence in depth for a comment that reached disk without going
through either, most commonly a `Bash` heredoc.
Only the last call in the window is scanned, so a re-validation after
rewriting a flagged comment closes cleanly on its own updated diff.

The diff is read from `final_diff` inline, or from `final_diff_path` — an
absolute path, capped at 2,000,000 bytes and failing open on any read
error, including the path being relative or the file exceeding the cap.
Only `+`-prefixed lines count as added; an unchanged context line does not.
The scan reuses `comment_scan.py`'s extension allowlist directly, so a file
type the write-time hook does not scan is not scanned here either. A
violation blocks with its own message, textually distinct from the two
above, and is recorded to the trace log the same way. Set
`ANTI_TANGENT_COMMENT_GUARD=0` to skip this scan while the completion gate
above still runs in full.

**What this scan cannot see.** A close whose task window holds no
`validate_completion` call at all is scanned no further than that: the scan
reads a call's submitted evidence, and a window whose pass signal is a pasted
marker block carries none. The marker path satisfies the completion gate on
its own (see "How much to trust each pass signal" above), so such a close
passes with `called=false` on the trace and no `scan` line beside it. That is
the shape of every close reported up from a subagent's own session, and the
evidence it validated against is in that session, not this transcript.

A completion that submits `final_files` is
read through git rather than through a diff, and git reports a line as added
only while it is uncommitted or its file is untracked. **Work already
committed before the close therefore yields no added lines and is not
scanned** — and committing per task before closing it is the common shape,
so this is the case to plan around, not an edge. The same holds for a
completion whose only evidence is `test_evidence`, and for a `final_files`
path git cannot classify (outside a repository, or gitignored). That gap is
not filled elsewhere: the reviewer's own rule for a change-history comment
applies only when a diff is present in the same call, so such a completion
gets no comment scrutiny from the reviewer either. It is covered by the
write-time `Edit`/`Write` hook alone — and by nothing at all if the code
reached disk through `Bash`. To have committed work scanned, submit it as a
diff from the commit the task started at
(`git diff <task-base-commit> -- <task paths>`) rather than as
`final_files`.

**Paths the repository's own `.gitattributes` takes out of scope.** A
**tracked** `final_files` path carrying a **`filter`** attribute — git-lfs,
git-crypt, or any other clean/smudge driver — is **not scanned at close
time**. `HEAD` holds a pointer or ciphertext for such a path while the
worktree holds content, and the only thing that could make the two comparable
is the filter command the repository itself names, which this hook will not
run. Skipping is the fail-open answer: comparing them directly would report
every line of the file as added. This skip lives on the tracked branch only —
an untracked path carrying the same attribute is still scanned whole. A path
carrying a **`working-tree-encoding`** is *not* skipped — the attribute's
value is the codec name, so the worktree side is decoded with it and the scan
runs normally. A codec Python cannot resolve falls back to skipping the path.

## Dependencies

The hook is a `bash` script that shells out to `jq` (to read the JSON stdin
payload) and `python3` (to walk the transcript, since the signals it looks
for are nested inside `tool_result` content that `jq` alone parses more
awkwardly than a few lines of Python). Both must be on `PATH`.

## Configuration

### `ANTI_TANGENT_TICKET_PATTERN`

A regex for your tracker's key shape, e.g. `ABC-\d+`. Set it per project in `.claude/settings.json`:

```json
{ "env": { "ANTI_TANGENT_TICKET_PATTERN": "ABC-\\d+" } }
```

Both hooks honour it: the tell is appended to the scanner's set when `comment_scan` is imported, so it is live in the write-time `Edit`/`Write` guard and in the close-time scan over a completion's submitted evidence alike.

**There is no default, and without it a comment like `// ABC-1234: the keyword` is not detected.** A generic pattern cannot be made safe: measured over real comment lines, `[A-Z]+-\d+` matches hardware identifiers (`HDMI-0`, `DP-0`) and prose labels (`ROUND-1`) far more often than tracker keys, and the write-time hook blocks writes. An uncompilable or over-long pattern is ignored, and each file's scan runs under a two-second deadline that fails open. The close-time walk over every file a completion named is bounded in turn — a twenty-second budget over the git questions and another over the scans — so neither a stalled git nor a slow pattern can hold the session for minutes. What a budget cuts short is recorded on the trace line rather than reported as a clean scan.

### `ANTI_TANGENT_JEV`

Turns the semantic tier on. Must be exactly `1` — any other value, including unset, leaves the
write-time hook running the pattern tier alone. Also requires `TYPESAFE_API_KEY`; the two are
independent switches, and either one missing keeps the tier off.

### `ANTI_TANGENT_JEV_THRESHOLD`

The `change_history` probability at or above which a judged block flags. Default `0.7`. A value
outside `(0, 1]` — including something unparsable, or `nan` — falls back to the default rather
than silently disabling the check: `0` would flag every block and `nan` compares `False` against
every probability, and neither is a stricter or looser policy, just a broken one.

### `TYPESAFE_API_KEY`

The TypeSafe API key the semantic tier authenticates with. Required for the tier to run; unset or
empty leaves it off (`jev-skip | no-key`) even with `ANTI_TANGENT_JEV=1`.

### `ANTI_TANGENT_JEV_MODEL`

The Jev model id sent with every request. Default `jev-1.13.0`, pinned deliberately: the model's
behaviour on this question was calibrated against that exact id, and an alias could move under a
threshold nobody re-tuned for it.

### `ANTI_TANGENT_JEV_URL`

The TypeSafe endpoint. Default `https://api.typesafe.ai/v1/systemone`. Any other value is honoured
only for a loopback host (`127.0.0.1`, `localhost`, `::1`) or for an `https` URL whose host is
listed in `~/.claude/anti-tangent-guard/jev-hosts` — see "Why the URL is restricted" under the
semantic tier above.

### `~/.claude/anti-tangent-guard/jev-hosts`

Not a variable: a file in your home directory listing the hosts, one per line, that
`ANTI_TANGENT_JEV_URL` may name besides the default host and loopback — a corporate proxy, say.
It lives under `~/.claude/` beside your own global settings and is read from there whatever
`CLAUDE_CONFIG_DIR` or `HOME` say: the path is fixed and the home directory comes from the
password database, because the environment is what this file exists to distrust. Create it with
`chmod 600`; a symlink, a file owned by another account, or one writable by group or other is
ignored, and an approved host is only ever reached over `https`. See "Why the URL is restricted"
and "What this rule does not defend against" under the semantic tier above for what this does and
does not protect.

### `ANTI_TANGENT_JEV_EXCLUDE`

Colon-separated glob patterns (`fnmatch` syntax), matched against the edited file's path as the
tool call names it. A match disables the semantic tier for that write (`jev-skip | excluded`)
while leaving the pattern tier running. Unset by default — nothing is excluded.

### `ANTI_TANGENT_EDIT_GATE`

`off` (default), `subagents` or `all`, case-insensitive. Turns on the edit gate and the stop
check; see "Edit gate (opt-in)" and "Stop check (opt-in)". Any other value is `off`.

### `ANTI_TANGENT_EDIT_GATE_EXEMPT`

Colon-separated globs, relative to the session's `cwd`, naming files the edit gate lets be
written before `validate_task_spec`. `**` crosses directories; `*` and `?` stay within one;
every other character is literal. Unset means `docs/**/*.md`. Set but empty means nothing is
exempt.

### `ANTI_TANGENT_STOP_GUARD`

`0` turns off the stop check alone. Any other value, or unset, leaves it governed by
`ANTI_TANGENT_EDIT_GATE`.

### `ANTI_TANGENT_PROGRESS_EDITS`

How many edits a task makes after `validate_task_spec` before the progress
reminder asks for `check_progress`. Default `10`. A value that is not a whole
number above zero is ignored and the default applies.

## Kill switches

- `ANTI_TANGENT_COMPLETION_GUARD=0` disables the completion-gate check in the
  `PostToolUse` hook — whether a close ran `validate_completion` at all. The
  close-time comment-hygiene scan is a separate concern and keeps running.
- `ANTI_TANGENT_COMMENT_GUARD=0` disables the comment-hygiene scan, both
  write-time and close-time, while leaving the completion-gate check active.
- `ANTI_TANGENT_SESSION_GUARD=0` disables the fingerprinted start gate
  (`PreToolUse`) and the no-session close rule (block condition 4). It leaves the
  completion gate, the comment scan and the opt-in edit gate alone.
- `ANTI_TANGENT_EDIT_GATE` unset or `off` disables the edit gate and the stop check,
  which is the default. `ANTI_TANGENT_STOP_GUARD=0` disables the stop check alone and
  leaves the edit gate on. Neither is touched by `ANTI_TANGENT_COMPLETION_GUARD`.
- Setting all three to `0` is what short-circuits the `PostToolUse` hook to
  `exit 0` before it reads stdin. With any one still on, the hook reads stdin
  and runs the rules that are still enabled.
- `ANTI_TANGENT_PROGRESS_GUARD=0` disables the progress reminder
  (`PostToolUse` on `Edit`/`Write`/`NotebookEdit`) and nothing else. It is not
  one of the three switches above: the reminder is a separate hook.
- `ANTI_TANGENT_JEV` unset or not exactly `1` disables the semantic tier alone,
  leaving the pattern tier, the completion gate and the start gate untouched.
  It has no bearing on the `PostToolUse` hook or the three switches above:
  the semantic tier runs only inside the write-time `PreToolUse` hook.

## Fail-open policy

Neither hook blocks work because it broke. The causes below are the close-time
`PostToolUse` hook's; the write-time hook has its own set, tabulated under
"Write-time fail-open causes" above. Any of the following makes the close-time
hook exit 0 silently, with only a trace-log line (see below) as a record:

- a missing or unreadable `transcript_path`
- `jq` or `python3` absent from `PATH`
- malformed JSON on stdin (the hook cannot know what it is looking at)
- any other unexpected internal error (an `ERR` trap covers this as a
  last-resort backstop)

The comment-hygiene scan (see above) has one fail-open cause of its own: the
scanner module cannot be imported, most commonly a `CLAUDE_PLUGIN_ROOT` that
does not resolve to this plugin's `hooks/` directory. Among the close-time
causes it is the one that is **not** silent by trace-log-line-only convention
above — it gets its own distinct reason, `comment-scan-unavailable`, so it
reads differently from a scan that genuinely ran and found nothing. Without
that distinction, a misconfigured plugin root would disable the scan
permanently and invisibly, indistinguishable on both the exit code and the
trace log from a clean pass.

A malformed line **inside** the transcript is handled differently, and
deliberately not folded into "fail open": the transcript walker skips a
single unparsable JSONL line and lets the remaining lines decide the verdict
as usual, so one corrupt line elsewhere in a long transcript doesn't erase
the block that should fire.

**The semantic tier's own row.** Every failure — no network, DNS, TLS, a timeout, a malformed
response, an unexpected exception — allows the write; only a flag above threshold blocks. A
failure also trips a 60-second breaker so a dead service costs one slow edit rather than every
edit, and prints one warning on stderr per session so a silent, permanent fail-open (an expired CA
bundle, a proxy that refuses `CONNECT`) does not go unnoticed. Missing configuration — the setting
off, no key, an excluded path — is a separate, silent skip: the tier never places a call, so it
trips neither the breaker nor the warning. See "When it cannot answer" under the semantic tier
above.

## Trace log

Every decision — skip, pass, or block — is appended as one line to a trace
log, by default:

```
/tmp/claude-hooks/anti-tangent-guard.log
```

This default path is **shared by every Claude Code session on the machine**,
by design: a cross-session view is what makes the log worth opening at all
when something needs debugging. The session column below is what keeps that
shared file attributable instead of an unlabeled interleave. If you want true
per-session isolation instead, point `ANTI_TANGENT_GUARD_TRACE_LOG` at a
session-specific path.

Because it sits in a world-writable directory, both hooks create the
containing directory mode `700` and refuse to append when the log path is a
symlink, so a link planted there cannot redirect the trace into a file of
someone else's choosing. Both checks are best-effort: a trace that cannot be
written is dropped and never changes a hook's exit status.

Override the location with `ANTI_TANGENT_GUARD_TRACE_LOG`, as an absolute
path. A relative one is resolved against the hook's working directory — the
project root under Claude Code — and the semantic tier's state files (its
breaker, strike and warning stamps) live in the log's directory, so a bare
filename puts the log and those stamps at the root of the repository being
edited, as untracked files. Tail it while debugging:

```bash
tail -f /tmp/claude-hooks/anti-tangent-guard.log
```

Each line carries a UTC timestamp, a short session identifier (`s=` followed
by the first 8 characters of the hook payload's `session_id`, or `s=-` for a
line emitted before the payload was read — the kill-switch skip, the
missing-`jq`/`python3` skip — where the hook genuinely does not know it yet),
the task id for `check-task-complete` (or `?` if the hook exited before
reaching one), and the decision plus its reason (e.g. `skip | no-jq`,
`pass | called=true block=false`, `block | verdict-fail`). The close-time
hook's fourth block condition traces `block | no-session`.

`check-task-start` carries the literal tag `task-start` in that same column,
since it has no task id to report, and its own event set: `pass | spec-called`
(the edit is allowed), `block | no-spec-call` and `block | spec-errored` (the
fingerprinted start gate refused it), `block | edit-gate-no-spec-call` and
`block | edit-gate-spec-errored` (the edit gate refused it), `skip | not-gated`
(neither gate covers this session), `skip | lightweight` (a lightweight dispatch
under the edit gate), `skip | outside` and `skip | exempt` (the path is not
gated), `skip | no-transcript`, `skip | bad-agent-id`,
`skip | bad-edit-gate-value`, `skip | guard=0` (both gates off),
`skip | no-python3`, `skip | no-body`, and `error | python-exit=N` (which
includes an exit 2 with no trace word: the interpreter failing, not the body
blocking).

`check-task-stop` carries the tag `task-stop` and the same `s=<session>.<agent>`
session column as the progress reminder. Its events: `pass | no-task` (no
`validate_task_spec` call), `pass | closed`, `block | open` and
`block | open-errored` (asked to call `validate_completion`), `pass | no-edits`
(no edit since the spec call), `skip | asked` (already asked since the task's
last edit), `skip | already-continued` (`stop_hook_active`),
`skip | not-gated` (an event the mode does not check),
`skip | bad-edit-gate-value` (an unrecognised switch value, read as off),
`skip | no-transcript`, `skip | no-state` (the ask could not be recorded, so it
was not made), `skip | no-python3`, `skip | no-body`, and
`error | python-exit=N` (which includes an exit 2 with no trace word). With the
switch off, and for a main-session `Stop` under `subagents`, it writes nothing.

The close-time hook adds `skip | not-protocol-task` for a close whose window
shows no protocol task.

`check-progress-nudge` carries the literal tag `progress` in that column, and
inside a subagent its session column reads `s=<session>.<agent>` (the first 8
characters of each), because subagents of one session run at the same time
and each has its own count. Its events: `pass | edits=N` (the task has made N edits and nothing is asked: it
is under the threshold, has already called `check_progress`, or was already
asked), `nudge | edits=N` (the hook asked), `skip | no-task` (no
`validate_task_spec` call in the transcript), `skip | completing` (the task
has called `validate_completion`), `skip | no-state` (the ask could not be
recorded beside the trace log, so it was not made), `skip | not-gated` (an
unreadable payload or transcript, or a tool the hook does not act on),
`skip | guard=0`, `skip | no-python3`, `skip | no-body`, and
`error | python-exit=N` (which includes an exit 2 that came with no edit
count: the interpreter failing, not the body asking). The largest `edits=` a task reaches is how many edits
it made before completion, which is the number to tune
`ANTI_TANGENT_PROGRESS_EDITS` against.

The semantic tier adds five events of its own to `check-comment-write`'s trace line, reached only
after a clean pattern-tier pass: `jev-block | p=<probability>` when a touched block scores at or
above the threshold (the write is refused); `jev-pass | blocks=<n>` when every scored block cleared
it; `jev-skip | <reason>` when the tier did not run at all — `setting` (not exactly `1`),
`no-key`, `excluded`, `breaker` (a recent failure's 60-second pause), or `no-blocks` (the edit
touched no comment); `jev-yield | <path>` on the third refusal for the same file within the
session's 30-minute window, when the tier allows the write instead of blocking again — or
`jev-yield | <path>,untracked` when a block was flagged but the refusal count could not be written
(the directory beside the trace log is not writable): a refusal the tier cannot count is one it
could never bound, so it allows every such write, the first included, and says so on stderr each
time, since the stamp that would make that warning once-per-session lives in the same directory;
and `jev-error | <failure class>` on a failure — an exception type name (`ResponseTooLarge` when
the endpoint answered with more than 64 KiB, which is not parsed), or `deadline` /
`deadline-before-request` / `no-question-file` for a budget or configuration problem — which
always allows the write. Every one of these but `jev-block` lets the write through. Any of them
but `jev-skip` carries a `,capped` suffix when the edit touched more comment blocks than the tier
judges in one write, so a verdict reached over a truncated set — a refusal as much as a pass —
reads as such in the trace.

`ANTI_TANGENT_COMMENT_GUARD=0` never produces any of these five: `check-comment-write` short-circuits
on it in bash, before Python ever starts, tracing the wrapper's own `skip | guard=0` line instead
(see "Kill switches" above). The tier's own `config()` checks the same setting again, and from the
write-time hook that branch is unreachable, since the wrapper's check always runs first. It is kept
as defence in depth for a caller that reaches `config()` without the wrapper: `config()` is the one
place that decides whether the tier may send anything at all, and `run()` consults nothing else, so
a host that invoked the Python body directly would get the kill switch from there or not at all.
Nothing shipped depends on that branch today — the calibration suite below calls `config()` only
for the model, threshold, key and URL, and gates itself separately.

Any of these five, when the tier reached its decision using an overridden URL, carries an extra
`,url=untrusted-host`, `,url=insecure-scheme` or `,url=unparsable-url` suffix — for example
`jev-error|URLError,url=untrusted-host` — naming why `ANTI_TANGENT_JEV_URL` was rejected and
swapped for the default; see "Why the URL is restricted" above. No such suffix means the
configured URL, if any, was used as given.

A close whose comment scan ran also emits a `scan` line — for example
`scan | src=final_files submitted=3 scanned=0 lines=0` — naming which
evidence the scan read and how much of it there was to read. `submitted`
counts the paths the evidence named, `scanned` the ones that yielded added
lines, and `lines` those added lines. A completion whose files were all
committed traces `submitted=3 scanned=0`, which is what distinguishes it
from a scan of a real diff that legitimately found nothing. Either budget
running out — the git walk's or the scan's — appends `budget-exhausted`, and
paths dropped by the vendored-directory exemption append
`vendored-skipped=N`; both shrink the result silently otherwise. A git that
answers `129` — its generic usage error, which a git that does not know
`--no-optional-locks` returns and so does a command line malformed any other
way — makes the walk drop that flag and appends `optional-locks-dropped`,
which the counts never show: the walk returns the same answer, having
refreshed the index it meant to leave alone. The marker names what the walk
did, not what git objected to. No `scan` line at all means no scan ran — the
guard was off, no `validate_completion` fell inside the window, or the
scanner could not be loaded (`skip | comment-scan-unavailable`).

The log is capped so it cannot grow without bound: past
`ANTI_TANGENT_GUARD_TRACE_MAX_BYTES` (default 1,048,576 — 1 MiB), the next
write rotates the existing file to a single `.1` sibling (a rename, not a
truncation, so a concurrent appender simply starts a fresh file instead of
writing into a hole) before appending. Identity and rotation are both
best-effort: any failure in either — an unreadable payload, a `mv` that can't
land — is swallowed and never changes the hook's own exit status.

## Running the evals

```bash
bash evals/run.sh
```

Runs the hooks' own unit tests (`hooks/*_test.py`) first, then the full eval
suite (237 cases) against all five hook scripts, and exits non-zero on either — the
cases cover check-task-complete's four block conditions (the third being its
own close-time comment-hygiene scan, the fourth being the no-session rule),
check-comment-write's write-time comment-hygiene guard — both the pattern
tier and, behind a loopback stub server the suite starts and tears down
itself, the semantic tier — check-task-start's start gate, and
check-progress-nudge's once-per-task reminder. See
`evals/run.sh`'s header comment for the
full breakdown by case. Cases 18/19 are deliberately un-escaped fixtures — they
test positional extraction against an older server. Cases 20/21 are the
current server's own rendering, pinned byte-for-byte to the formatters by
`internal/mcpsrv/guard_eval_fixture_test.go`, so the escaping half is
exercised end-to-end through the real hook rather than only through a Go
mirror of its regexes. Case 22 pairs a validate_completion tool_use with its
tool_result exactly as the server's `envelopeResult` marshals it, so the
direct-call verdict read (see "The four block conditions" above) is
exercised end-to-end too.

### The semantic tier's calibration suite

```bash
ANTI_TANGENT_JEV=1 TYPESAFE_API_KEY=... python3 evals/jev-eval.py
```

Separate again, and never run by CI or by `evals/run.sh`: it spends a real request per row against
the live TypeSafe endpoint, so it needs the key and the setting and refuses to run without both.
It scores `evals/jev-comments.jsonl` — 124 labelled comment blocks built by the hook's own block
builder — and prints recall and precision per source group. Read its output with the same care the
design measurement needed. Against that set the pattern tier catches **1 of the 38** `history`-labelled
rows it did not itself select, and **0 of the 30** prose-narrated ones (the `head-history-wording`
group, every label operator-confirmed) — the gap the semantic tier exists to close. A naive count
across all 57 reads 20, but 19 of those come from `fp-class`, a source generated by running the
pattern tier over this repository: those rows exist because the pattern tier already matched them,
so counting them as its own successes measures nothing.

### The false-positive gate

```bash
bash evals/fp-report.sh
```

Separate from the case suite, and answering a different question: not "does
each pinned shape still behave", but "does the scanner, as shipped, misread any
comment in this repository's own source". `fp-scan.py` runs it over every
tracked source file's HEAD blob with every comment line offered as an added
line — the worst case the write-time hook can see. `fp-class.tsv` records what
each hit is, judged by hand. `fp-report.sh` joins the two strictly in both
directions and fails on an unclassified hit, a classification with no hit, a
duplicate key, or a key whose comment text has drifted, so a stale table cannot
report a clean zero. Widening a tell is the change this gate exists to catch:
regenerate with `python3 -B evals/fp-scan.py`, then reconcile `fp-class.tsv` by
hand — every new key needs a human judgement.

The gate itself always measures the shipped tells with
`ANTI_TANGENT_TICKET_PATTERN` unset, so a pattern a developer happens to have
configured locally cannot move a verdict recorded in `fp-class.tsv`. To measure
a candidate tracker pattern before setting it project-wide, pass it to the
scanner directly:

```bash
python3 -B evals/fp-scan.py --ticket-pattern '[A-Z]{2,}-[0-9]+'
```

That reports the total hits and, separately, how many are **newly
attributable** to the pattern — hits no built-in tell already catches, which is
what the pattern would newly block. A pattern that fails to compile or exceeds
the 200-character cap exits 2 and measures nothing, rather than reporting a
reassuring zero. This run is a one-off measurement: its output is not joinable
against `fp-class.tsv`, which classifies the shipped tells only.
