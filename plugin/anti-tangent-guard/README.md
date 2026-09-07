# anti-tangent-guard

A single `PostToolUse` hook that enforces anti-tangent-mcp's `validate_completion`
gate at task close.

## Active on install

This plugin has one hook and no configuration step. As soon as it is
installed, every `TaskUpdate` call is watched — there is nothing further to
turn on.

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

## The two block conditions

For a matching close, the hook scans the transcript window for this task (see
"Window scoping" below) for two possible pass signals: a direct
`mcp__anti-tangent__validate_completion` tool call, or an `anti-tangent
envelope` / `session_id:` summary block, **tagged `tool: validate_completion`**,
pasted into a `tool_result` (this is how a subagent's report of running the
gate becomes visible from the controller's own transcript). It blocks
(`exit 2`) in exactly two cases:

1. **Neither signal is present.** Nothing in the window shows the completion
   gate ran at all.
2. **The last qualifying signal's verdict is `fail`.** The gate ran, but its
   most recent verdict in the window says the work is not done. This is read
   from either signal: a pasted summary block tagged `tool:
   validate_completion`, or the direct call's own MCP result — the real
   tool result arrives JSON-marshalled, with the summary block's newlines
   surviving only as escapes inside one JSON string, so the hook parses that
   JSON directly for `verdict` rather than pattern-matching the escaped
   text.

Both messages state the same recovery flow explicitly: reopen the task with
`status=in_progress`, address whatever the gate is asking for, run
`mcp__anti-tangent__validate_completion` (again), and only then re-close.

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

## Dependencies

The hook is a `bash` script that shells out to `jq` (to read the JSON stdin
payload) and `python3` (to walk the transcript, since the signals it looks
for are nested inside `tool_result` content that `jq` alone parses more
awkwardly than a few lines of Python). Both must be on `PATH`.

## Kill switch

Set `ANTI_TANGENT_COMPLETION_GUARD=0` to short-circuit the hook to `exit 0`
unconditionally, before it reads stdin or does any work.

## Fail-open policy

This hook never blocks work because it broke. Any of the following makes it
exit 0 silently, with only a trace-log line (see below) as a record:

- a missing or unreadable `transcript_path`
- `jq` or `python3` absent from `PATH`
- malformed JSON on stdin (the hook cannot know what it is looking at)
- any other unexpected internal error (an `ERR` trap covers this as a
  last-resort backstop)

A malformed line **inside** the transcript is handled differently, and
deliberately not folded into "fail open": the transcript walker skips a
single unparsable JSONL line and lets the remaining lines decide the verdict
as usual, so one corrupt line elsewhere in a long transcript doesn't erase
the block that should fire.

## Trace log

Every decision — skip, pass, or block — is appended as one line to a trace
log, by default:

```
/tmp/claude-hooks/anti-tangent-guard.log
```

Override the location with `ANTI_TANGENT_GUARD_TRACE_LOG`. Tail it while
debugging:

```bash
tail -f /tmp/claude-hooks/anti-tangent-guard.log
```

Each line carries a UTC timestamp, the task id (or `?` if the hook exited
before reaching one), and the decision plus its reason (e.g.
`skip | no-jq`, `pass | called=true block=false`, `block | verdict-fail`).

## Running the evals

```bash
bash evals/run.sh
```

Runs the full eval suite (22 cases) against the hook and exits non-zero on
any mismatch. Cases 18/19 are deliberately un-escaped fixtures — they test
positional extraction against an older server. Cases 20/21 are the current
server's own rendering, pinned byte-for-byte to the formatters by
`internal/mcpsrv/guard_eval_fixture_test.go`, so the escaping half is
exercised end-to-end through the real hook rather than only through a Go
mirror of its regexes. Case 22 pairs a validate_completion tool_use with its
tool_result exactly as the server's `envelopeResult` marshals it, so the
direct-call verdict read (see "The two block conditions" above) is
exercised end-to-end too.
