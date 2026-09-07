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
misread as validate_completion's — defeating the guard for the one failure
mode it exists to catch. Since anti-tangent-mcp 0.18.0 every envelope carries
a `tool:` line naming the MCP tool that produced it, and this hook's pass
signal and verdict extraction are both scoped to blocks where that line reads
`tool: validate_completion`.

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

Runs the full eval suite (17 cases) against the hook and exits non-zero on
any mismatch.
