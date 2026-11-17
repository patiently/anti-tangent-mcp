"""PreToolUse body: refuse an Edit/Write until the session has made a
successful validate_task_spec call.

Reads the hook payload as JSON on stdin; the wrapper consumes the hook's own
stdin and re-feeds it here, so nothing else in this process may read stdin.
Exit 0 = allow (the call exists), 3 = allow without deciding (not gated, no
readable transcript, an ungated tool or path), 2 = block. The wrapper maps
every other exit to allow. One word for the trace line goes to stdout; the
wrapper only blocks on an exit 2 that came with one, because the interpreter
itself exits 2 when it cannot start this file.

A hook firing inside a subagent receives the parent's transcript_path and
session_id; only agent_id marks the subagent, and its own transcript sits
beside the parent's at <parent stem>/subagents/agent-<agent_id>.jsonl.

Two things gate a session, independently:

- The fingerprint (ATG_SESSION_GUARD, on unless the wrapper says "0"): a
  subagent whose FIRST user entry -- the dispatch prompt -- carries the full
  clause heading and not the lightweight one. Every path is gated.
- The edit gate (ATG_EDIT_GATE, "off" unless the wrapper says otherwise):
  "subagents" gates every subagent, "all" also the main session. A lightweight
  dispatch is never gated. Only paths inside the project are gated, and of
  those, not the ones matching ANTI_TANGENT_EDIT_GATE_EXEMPT. The project is
  CLAUDE_PROJECT_DIR when Claude Code sets it, else the payload's cwd: the
  payload's cwd follows a Bash `cd`, so after one it names a subdirectory and
  a sibling of it would read as outside.

ANTI_TANGENT_EDIT_GATE_EXEMPT is read here, raw, not normalised by the wrapper
into an ATG_ variable like the two switches: the wrapper needs the switches to
decide whether to start Python at all, and the exemption list only matters
once it has. Unset and set-but-empty mean different things (the default, and
nothing exempt), a distinction os.environ keeps.
"""
import json
import os
import re
import sys

FULL_HEADING = "## Drift-protection protocol (anti-tangent-mcp)"
LITE_HEADING = "Drift-protection protocol (lightweight)"
SPEC_TOOL = "mcp__anti-tangent__validate_task_spec"
GATED_TOOLS = ("Edit", "Write", "NotebookEdit")
EDIT_GATE_MODES = ("off", "subagents", "all")
DEFAULT_EXEMPT = ("docs/**/*.md",)

BLOCK_MESSAGE = """EDIT BEFORE validate_task_spec

This session was dispatched under the full anti-tangent protocol (§4.2), which
requires validate_task_spec before any edit. It has not been called.

Call mcp__anti-tangent__validate_task_spec with the task's fields first. Its
response carries the pre-task review of the spec and the build guidance for this
task; the session_id it returns is what validate_completion needs at the end.

Reads are not gated — read what the change touches, then call it, then edit.

(Disable: ANTI_TANGENT_SESSION_GUARD=0. Trace: {trace})
"""

ERRORED_MESSAGE = """EDIT AFTER A validate_task_spec TOOL ERROR

This session was dispatched under the full anti-tangent protocol (§4.2), which
requires validate_task_spec before any edit. It was called, but every call came
back as a tool error (refused or failed), so no task session was opened.

Read the error, fix its cause (for example leave a refused path out of
context_paths), and call mcp__anti-tangent__validate_task_spec again before
editing.

(Disable: ANTI_TANGENT_SESSION_GUARD=0. Trace: {trace})
"""

EDIT_GATE_MESSAGE = """EDIT BEFORE validate_task_spec (ANTI_TANGENT_EDIT_GATE={mode})

This session refuses edits to project files until it has made a successful
mcp__anti-tangent__validate_task_spec call of its own. {state}

Call validate_task_spec with the task's goal and acceptance criteria: from the
spec (pass the spec's absolute path in context_paths), or as stated in the
conversation. For a one-line fix, a one-line goal and a single acceptance
criterion are enough. Use the session_id it returns for check_progress and
validate_completion. Reads are not gated.

Not gated: files outside {cwd}, and files under it matching
ANTI_TANGENT_EDIT_GATE_EXEMPT ({globs}).

If validate_task_spec is not available in this session, stop and tell the
user: the anti-tangent server is not connected, and only they can fix that.

Whether this gate is on is the user's setting. Do not change it: not in this
session's environment, and not in any settings file such as
.claude/settings.local.json. (Trace: {trace})
"""

EDIT_GATE_NO_CALL = "It has not made one."
EDIT_GATE_ERRORED = ("It called it, but every call came back as a tool error (refused or failed),\n"
                     "so no task session was opened. Read the error and fix its cause first.")


def as_list(value):
    """Return value when it is already a list, else []. A malformed
    transcript entry can carry a "content" that is neither a string nor a
    list; callers that only handle the list shape use this instead of
    re-deriving the same isinstance check at each call site."""
    return value if isinstance(value, list) else []


def text_of(content):
    """Return the plain text of a message "content" value: itself when it is
    already a string, the joined text of its "text"-typed dict parts when it
    is a list, and "" for anything else -- a shape a malformed transcript
    entry can carry but a real one never does."""
    if isinstance(content, str):
        return content
    return "\n".join(c.get("text") or "" for c in as_list(content)
                     if isinstance(c, dict) and c.get("type") == "text")


def is_spec_call(c):
    return isinstance(c, dict) and c.get("type") == "tool_use" and c.get("name") == SPEC_TOOL


def is_error_result(c):
    return isinstance(c, dict) and c.get("type") == "tool_result" and c.get("is_error") is True


def spec_outcome(etype, c, pending):
    """Fold one content block into the set of validate_task_spec call ids
    still awaiting a result. Return "pass" for a call that cannot be matched
    to a result (no id) or whose result succeeded, "errored" for a result that
    was a tool error, and "" for anything else."""
    if etype == "assistant" and is_spec_call(c):
        tuid = c.get("id")
        if isinstance(tuid, str) and tuid:
            pending.add(tuid)
            return ""
        return "pass"
    if etype == "user" and isinstance(c, dict) and c.get("type") == "tool_result":
        tuid = c.get("tool_use_id")
        if tuid in pending:
            if is_error_result(c):
                pending.discard(tuid)
                return "errored"
            return "pass"
    return ""


def classify(lines, rule="full"):
    """Return "skip", "pass", "block" or "errored" for an iterable of
    transcript lines. "errored" is a block where every validate_task_spec call
    came back as a tool error.

    rule says what the first user entry must show for the session to be
    gated: "full" the full clause heading and not the lightweight one (the
    fingerprint), "not_lite" anything but the lightweight heading (a subagent
    under the edit gate), "none" nothing (the main session under the edit
    gate, whose first user entry is the user's own prompt).

    Entries before the first user entry (attachments, system records) are
    ignored. After it, one validate_task_spec tool_use is a pass unless its
    tool_result is an error (a refused call opened no session); the scan
    stops at the first successful result. Text merely naming the tool is not a
    call. Past the first user entry, a line that names neither the tool nor a
    tool_use_id is not parsed at all: a main-session transcript can be tens of
    megabytes and is read before every edit.

    A malformed entry -- a truthy non-dict "message", or assistant content
    that is not a list -- is skipped rather than raised on, the same way a
    line that fails to parse as JSON already is: one odd entry must not turn
    a gated session's "block" into an uncaught exception the wrapper reads as
    allow.
    """
    seen_first_user = rule == "none"
    errored = False
    pending = set()
    for line in lines:
        if seen_first_user and SPEC_TOOL not in line and not (pending and "tool_use_id" in line):
            continue
        try:
            entry = json.loads(line)
        except Exception:
            continue
        if not isinstance(entry, dict):
            continue
        etype = entry.get("type")
        msg = entry.get("message")
        if not isinstance(msg, dict):
            msg = {}
        if not seen_first_user:
            if etype != "user":
                continue
            seen_first_user = True
            first = text_of(msg.get("content"))
            if LITE_HEADING in first or (rule == "full" and FULL_HEADING not in first):
                return "skip"
            continue
        for c in as_list(msg.get("content")):
            outcome = spec_outcome(etype, c, pending)
            if outcome == "pass":
                return "pass"
            errored = errored or outcome == "errored"
    if not seen_first_user:
        return "skip"
    if pending:
        return "pass"
    return "errored" if errored else "block"


def subagent_transcript(data):
    """Return the dispatched subagent's own transcript path, or "" when the
    payload is not from a subagent. agent_id and transcript_path must both be
    non-empty strings; a non-string value (a malformed or hostile payload)
    is refused before it ever reaches the identifier check or a path join,
    and an agent_id that is not a plain identifier is refused rather than
    joined into a path."""
    agent_id = data.get("agent_id")
    parent = data.get("transcript_path")
    if not isinstance(agent_id, str) or not isinstance(parent, str) or not agent_id or not parent:
        return ""
    if not all(ch.isalnum() or ch in "-_" for ch in agent_id):
        return ""
    stem = os.path.splitext(parent)[0]
    return os.path.join(stem, "subagents", "agent-" + agent_id + ".jsonl")


def edit_gate_mode(raw):
    """Return the edit-gate mode for a raw switch value: "off", "subagents"
    or "all", case-insensitively, with surrounding blanks ignored; "" and
    unset are "off". Anything else is None, which callers treat as "off"."""
    value = (raw or "").strip().lower() or "off"
    return value if value in EDIT_GATE_MODES else None


def exempt_globs(raw):
    """Return the exemption globs: the default when the variable is unset
    (None), else its colon-separated non-empty entries -- so a set but empty
    value exempts nothing."""
    if raw is None:
        return list(DEFAULT_EXEMPT)
    return [g.strip() for g in raw.split(":") if g.strip()]


def glob_regex(glob):
    """Compile a path glob in which "**" matches across directories, "*" and
    "?" stay within one, and every other character is literal."""
    out, i = [], 0
    while i < len(glob):
        if glob.startswith("**/", i):
            out.append("(?:.*/)?")
            i += 3
        elif glob.startswith("**", i):
            out.append(".*")
            i += 2
        elif glob[i] == "*":
            out.append("[^/]*")
            i += 1
        elif glob[i] == "?":
            out.append("[^/]")
            i += 1
        else:
            out.append(re.escape(glob[i]))
            i += 1
    return re.compile("".join(out) + r"\Z", re.DOTALL)


def path_scope(target, cwd, globs, project=None):
    """Return "gated", "outside" or "exempt" for a write target.

    Both sides go through realpath first, so ".." segments and symlinks are
    resolved before the comparison: a docs/x.md that links into src/ is
    judged as the file it writes. A relative target is taken against cwd,
    where the tool runs; containment and the exemption globs are judged
    against project, which defaults to cwd. Without a usable target or cwd
    the call cannot be placed, and it is "outside" -- the hook's failures
    allow."""
    if not isinstance(target, str) or not target or not isinstance(cwd, str) or not cwd:
        return "outside"
    if not isinstance(project, str) or not project:
        project = cwd
    root = os.path.realpath(project)
    real = os.path.realpath(os.path.join(os.path.realpath(cwd), target))
    rel = os.path.relpath(real, root)
    if rel == os.curdir or rel == os.pardir or rel.startswith(os.pardir + os.sep) or os.path.isabs(rel):
        return "outside"
    rel = rel.replace(os.sep, "/")
    return "exempt" if any(glob_regex(g).match(rel) for g in globs) else "gated"


def write_target(data):
    """Return the path an Edit/Write/NotebookEdit call writes, or ""."""
    inp = data.get("tool_input")
    if not isinstance(inp, dict):
        return ""
    value = inp.get("notebook_path") if data.get("tool_name") == "NotebookEdit" else inp.get("file_path")
    return value if isinstance(value, str) else ""


def read_verdict(path, rule):
    try:
        with open(path, encoding="utf-8", errors="replace") as fh:
            return classify(fh, rule)
    except OSError:
        return None


def decide(data, mode, fingerprint, globs):
    """Return (exit code, trace word, message) for one payload.

    The fingerprint is checked first and, when it gates, wins: a session
    dispatched under the full clause keeps the every-path gate whatever
    the mode. Otherwise the edit gate applies, path scope first so an exempt
    or outside write never reads a transcript."""
    sub = subagent_transcript(data)
    if data.get("agent_id") and not sub:
        return 3, "bad-agent-id", ""
    if sub and fingerprint:
        verdict = read_verdict(sub, "full")
        if verdict is None:
            return 3, "no-transcript", ""
        if verdict == "pass":
            return 0, "spec-called", ""
        if verdict in ("block", "errored"):
            return 2, "no-spec-call" if verdict == "block" else "spec-errored", \
                ERRORED_MESSAGE if verdict == "errored" else BLOCK_MESSAGE
    if mode == "off" or (not sub and mode != "all"):
        return 3, "not-gated", ""
    target = write_target(data)
    project = os.environ.get("CLAUDE_PROJECT_DIR") or data.get("cwd")
    scope = path_scope(target, data.get("cwd"), globs, project)
    if scope != "gated":
        return 3, scope, ""
    if sub:
        verdict = read_verdict(sub, "not_lite")
    else:
        main_path = data.get("transcript_path")
        if not isinstance(main_path, str) or not main_path:
            return 3, "no-transcript", ""
        verdict = read_verdict(main_path, "none")
    if verdict is None:
        return 3, "no-transcript", ""
    if verdict == "skip":
        return 3, "lightweight", ""
    if verdict == "pass":
        return 0, "spec-called", ""
    state = EDIT_GATE_ERRORED if verdict == "errored" else EDIT_GATE_NO_CALL
    message = EDIT_GATE_MESSAGE.format(
        mode=mode, state=state, cwd=project, globs=":".join(globs) or "none",
        trace="{trace}")
    return 2, "edit-gate-" + ("spec-errored" if verdict == "errored" else "no-spec-call"), message


def main():
    try:
        data = json.loads(sys.stdin.read())
    except Exception:
        return 3
    if not isinstance(data, dict) or (data.get("tool_name") or "") not in GATED_TOOLS:
        return 3
    mode = edit_gate_mode(os.environ.get("ATG_EDIT_GATE")) or "off"
    fingerprint = os.environ.get("ATG_SESSION_GUARD", "1") != "0"
    globs = exempt_globs(os.environ.get("ANTI_TANGENT_EDIT_GATE_EXEMPT"))
    code, word, message = decide(data, mode, fingerprint, globs)
    sys.stdout.write(word)
    if code == 2:
        sys.stderr.write(message.replace("{trace}", os.environ.get("ATG_TRACE_LOG", "")))
    return code


if __name__ == "__main__":
    sys.exit(main())
