"""PreToolUse body: refuse an Edit/Write in a dispatched implementer's session
until validate_task_spec has been called.

Reads the hook payload as JSON on stdin; the wrapper consumes the hook's own
stdin and re-feeds it here, so nothing else in this process may read stdin.
Exit 0 = allow (the call exists), 3 = allow without deciding (not a subagent,
not an implementer, no readable transcript, an ungated tool), 2 = block. The
wrapper maps every other exit to allow.

A hook firing inside a subagent receives the parent's transcript_path and
session_id; only agent_id marks the subagent, and its own transcript sits
beside the parent's at <parent stem>/subagents/agent-<agent_id>.jsonl. The
main session carries no agent_id and is never a dispatched implementer.

A subagent is an implementer when its FIRST user entry -- the dispatch
prompt -- carries the full clause heading and not the lightweight one.
"""
import json
import os
import sys

FULL_HEADING = "## Drift-protection protocol (anti-tangent-mcp)"
LITE_HEADING = "Drift-protection protocol (lightweight)"
SPEC_TOOL = "mcp__anti-tangent__validate_task_spec"
GATED_TOOLS = ("Edit", "Write", "NotebookEdit")

BLOCK_MESSAGE = """EDIT BEFORE validate_task_spec

This session was dispatched under the full anti-tangent protocol (§4.2), which
requires validate_task_spec before any edit. It has not been called.

Call mcp__anti-tangent__validate_task_spec with the task's fields first. Its
response carries the pre-task review of the spec and the build guidance for this
task; the session_id it returns is what validate_completion needs at the end.

Reads are not gated — read what the change touches, then call it, then edit.

(Disable: ANTI_TANGENT_SESSION_GUARD=0. Trace: {trace})
"""


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


def classify(lines):
    """Return "skip", "pass" or "block" for an iterable of transcript lines.

    Entries before the first user entry (attachments, system records) are
    ignored. The first user entry decides whether the session is gated at all;
    after it, one validate_task_spec tool_use is a pass, and the scan stops
    there. Text merely naming the tool is not a call.

    A malformed entry -- a truthy non-dict "message", or assistant content
    that is not a list -- is skipped rather than raised on, the same way a
    line that fails to parse as JSON already is: one odd entry must not turn
    a gated session's "block" into an uncaught exception the wrapper reads as
    allow.
    """
    seen_first_user = False
    for line in lines:
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
            if FULL_HEADING not in first or LITE_HEADING in first:
                return "skip"
            continue
        if etype == "assistant":
            for c in as_list(msg.get("content")):
                if isinstance(c, dict) and c.get("type") == "tool_use" and c.get("name") == SPEC_TOOL:
                    return "pass"
    return "block" if seen_first_user else "skip"


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


def main():
    try:
        data = json.loads(sys.stdin.read())
    except Exception:
        return 3
    if not isinstance(data, dict) or (data.get("tool_name") or "") not in GATED_TOOLS:
        return 3
    path = subagent_transcript(data)
    if not path:
        return 3
    try:
        with open(path, encoding="utf-8", errors="replace") as fh:
            verdict = classify(fh)
    except OSError:
        return 3
    if verdict == "skip":
        return 3
    if verdict == "pass":
        return 0
    sys.stderr.write(BLOCK_MESSAGE.format(trace=os.environ.get("ATG_TRACE_LOG", "")))
    return 2


if __name__ == "__main__":
    sys.exit(main())
