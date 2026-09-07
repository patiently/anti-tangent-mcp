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
2. **The last summary block's verdict is `fail`.** The gate ran, but its most
   recent verdict in the window says the work is not done.

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
2. **Source-side escaping.** Since the same release, `internal/mcpsrv/
   summary.go` prefixes every continuation line of a multi-line free-text
   field with a non-whitespace sentinel (`| `), so such a line can never
   match this hook's `^\s*label:` patterns — or present as a second, bare
   `anti-tangent envelope` header — at all, regardless of what it contains.
   This covers **every** formatter that emits an `anti-tangent envelope`
   header, not only the three per-task tools: `validate_plan`,
   `prime_project_knowledge` and `extract_project_knowledge` render their
   own blocks under headers carrying the same substring, and for one release
   they were left un-escaped while the per-task envelope was fixed — a
   forged block smuggled through a single `validate_plan` finding's
   `criterion` satisfied this hook with no gate call at all. What keeps that
   from recurring is `internal/mcpsrv/summary_forgery_test.go`, which
   enumerates the header-emitting formatters from the server's own source
   and drives a forged payload through every free-text field of each: a new
   formatter, or a new field, fails it until it is escaped.

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

### What this does not defend against

**1. A block fabricated outright.** Neither defence makes a pasted block
trustworthy in itself. The marker pass signal accepts any `anti-tangent
envelope` block that starts a line in the window — and a subagent writing its
own report can simply compose one. Verified: a report whose text is a
fabricated `tool: validate_completion` / `verdict: pass` block, with no gate
call anywhere in the window, satisfies this guard.

That is inherent to the marker path rather than an oversight in it. The hook
reads a transcript, and nothing in a transcript distinguishes text a subagent
pasted from the gate's real output from text it composed. Closing it would
need the server to sign each block and the hook to verify that signature —
far beyond an advisory guard. The direct signal (an
`mcp__anti-tangent__validate_completion` `tool_use` in the window) is the one
that cannot be fabricated this way; the marker path exists so a subagent that
genuinely ran the gate in its own session still counts, and it extends that
report the same trust the rest of anti-tangent does.

**2. A forged block boundary from a server older than 0.18.0.** Source-side
escaping is the only thing that stops a reviewer-authored field from starting
a new block (see the split above), so it is the half that a caller cannot
supply locally. Against an older server, a genuine `validate_plan`,
`prime_project_knowledge` or `extract_project_knowledge` block whose finding
text contains a bare `anti-tangent envelope` line followed by
`tool: validate_completion` / `verdict: pass` still satisfies this guard —
positionally and correctly, because the forgery is a real block boundary
rather than a misread line. The version requirement below is not only about
the `tool:` tag; this is the other reason for it.

**3. Anything outside the block grammar this hook reads.** Both defences are
scoped to the bare `anti-tangent envelope` header and the
`tool:`/`session_id:`/`verdict:` lines. `internal/mcpsrv/summary_forgery_test.go`
pins that scope for the server's plain-string fields; enum-typed fields
(`verdict`, `severity`, `category`, …) are excluded there because
`internal/verdict`'s parsers reject an out-of-enum value first. A consumer
keying on some other line of these blocks is on its own.

So this guard raises the cost of skipping the gate from "say nothing" to
"knowingly fabricate a gate result". It is a drift guard, not an adversarial
control — consistent with the server being advisory throughout (see the root
`CLAUDE.md`, "What This Repo Is Not").

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

Runs the full eval suite (21 cases) against the hook and exits non-zero on
any mismatch. Cases 18/19 are deliberately un-escaped fixtures — they test
positional extraction against an older server. Cases 20/21 are the current
server's own rendering, pinned byte-for-byte to the formatters by
`internal/mcpsrv/guard_eval_fixture_test.go`, so the escaping half is
exercised end-to-end through the real hook rather than only through a Go
mirror of its regexes.
