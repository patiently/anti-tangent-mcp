# anti-tangent-guard

Two hooks enforcing anti-tangent-mcp's conventions: a `PostToolUse` hook that
mandates the `validate_completion` gate at task close and detects when submitted
diffs add comments carrying change history, and a `PreToolUse` hook that prevents
such comments from being written in the first place.

## Active on install

This plugin has two hooks and no configuration step. As soon as it is
installed, every `TaskUpdate` call is watched for the completion gate, and
every `Edit`/`Write` call is intercepted for the write-time comment-hygiene
scan — there is nothing further to turn on.

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

## The three block conditions

For a matching close, the hook scans the transcript window for this task (see
"Window scoping" below) for two possible pass signals: a direct
`mcp__anti-tangent__validate_completion` tool call, or an `anti-tangent
envelope` / `session_id:` summary block, **tagged `tool: validate_completion`**,
pasted into a `tool_result` (this is how a subagent's report of running the
gate becomes visible from the controller's own transcript). It blocks
(`exit 2`) in exactly three cases:

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
3. **The submitted diff contains added comment lines carrying change history.**
   A scan of added lines in the diff detects comments matching a pattern set
   (patterns stored in `comment_scan.py`), the same patterns the write-time
   hook applies to `Edit` and `Write` calls. This scan detects rather than
   prevents, catching comments that reached disk through `Bash` or other
   pathways the write-time hook cannot intercept.

The first two messages state the same recovery flow explicitly: reopen the task with
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
- The scanner reads full-line comments only. Multi-line comments, including
  those that span across lines, are not detected.
- The scanner implements a small pattern set capturing common change-history
  markers. The reviewer layer at completion time covers prose narration the
  patterns cannot catch — an engineer writing a sentence like "I rewrote this
  for clarity" in a comment passes the scanner but may be flagged by the
  reviewer.
- The extension allowlist (`comment_scan.py`'s `SCAN_EXTS`) is keyed on
  `os.path.splitext`, so a file with no extension — including this plugin's
  own extensionless `check-task-complete` and `check-comment-write` hook
  scripts — falls outside it and is not scanned by either layer.

Set `ANTI_TANGENT_COMMENT_GUARD=0` to disable the comment-hygiene scan at both
write time (PreToolUse on `Edit`/`Write`) and close time (part of the
PostToolUse scan), while leaving the completion-gate check active.

## Comment-hygiene scan at close

Beyond the first two block conditions above, a close that is otherwise going to
pass gets one more check: the LAST `validate_completion` call in the task
window is scanned for added comment lines carrying change history — the
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

**What this scan cannot see.** A completion whose evidence is `final_files`
or `test_evidence` alone carries no diff of any kind, so there is nothing
here to read — the close is not blocked on comment hygiene, one way or the
other. That gap is not filled elsewhere: the reviewer's own rule for a
change-history comment applies only when a diff is present in the same
call, so a diff-less completion gets no comment scrutiny from the reviewer
either. Such a close is covered by the write-time `Edit`/`Write` hook alone
— and by nothing at all if the code reached disk through `Bash`.

## Dependencies

The hook is a `bash` script that shells out to `jq` (to read the JSON stdin
payload) and `python3` (to walk the transcript, since the signals it looks
for are nested inside `tool_result` content that `jq` alone parses more
awkwardly than a few lines of Python). Both must be on `PATH`.

## Kill switches

- `ANTI_TANGENT_COMPLETION_GUARD=0` short-circuits the `PostToolUse` hook to
  `exit 0` unconditionally, before it reads stdin or does any work. This
  disables both the completion-gate check and the close-time comment-hygiene
  scan.
- `ANTI_TANGENT_COMMENT_GUARD=0` disables the comment-hygiene scan (both
  write-time and close-time) while leaving the completion-gate check active.

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

Override the location with `ANTI_TANGENT_GUARD_TRACE_LOG`. Tail it while
debugging:

```bash
tail -f /tmp/claude-hooks/anti-tangent-guard.log
```

Each line carries a UTC timestamp, a short session identifier (`s=` followed
by the first 8 characters of the hook payload's `session_id`, or `s=-` for a
line emitted before the payload was read — the kill-switch skip, the
missing-`jq`/`python3` skip — where the hook genuinely does not know it yet),
the task id for `check-task-complete` (or `?` if the hook exited before
reaching one), and the decision plus its reason (e.g. `skip | no-jq`,
`pass | called=true block=false`, `block | verdict-fail`).

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

Runs the full eval suite (93 cases) against both hooks and exits non-zero on
any mismatch — check-task-complete's three block conditions (the third being
its own close-time comment-hygiene scan), plus check-comment-write's
write-time comment-hygiene guard. See `evals/run.sh`'s header comment for the
full breakdown by case. Cases 18/19 are deliberately un-escaped fixtures — they
test positional extraction against an older server. Cases 20/21 are the
current server's own rendering, pinned byte-for-byte to the formatters by
`internal/mcpsrv/guard_eval_fixture_test.go`, so the escaping half is
exercised end-to-end through the real hook rather than only through a Go
mirror of its regexes. Case 22 pairs a validate_completion tool_use with its
tool_result exactly as the server's `envelopeResult` marshals it, so the
direct-call verdict read (see "The three block conditions" above) is
exercised end-to-end too.

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
