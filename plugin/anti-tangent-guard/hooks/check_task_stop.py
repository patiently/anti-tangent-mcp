"""Stop / SubagentStop body: refuse to let a session stop when it opened a
task with validate_task_spec, edited since, and has not closed the task with
validate_completion -- once per stretch of edits.

Reads the hook payload as JSON on stdin; the wrapper consumes the hook's own
stdin and re-feeds it here, so nothing else in this process may read stdin.
Exit 0 = allow (no open task, or no edit in it), 2 = block (the message is on stderr), 3 = allow
without deciding (not gated in this mode, already continued once, already
asked since the task's last edit, no readable transcript, no place to record the ask). The
wrapper maps every other exit to allow. One word for the trace line goes to
stdout.

The transcript read is the stopping session's own: transcript_path for Stop,
and for SubagentStop agent_transcript_path, or the subagents/ file beside the
parent's when the payload lacks it (transcript_path there is the parent's).

A call counts when this session made it as an assistant tool_use and its
tool_result is not an error; a call still awaiting its result counts too.
Text that merely names a tool, such as a pasted envelope, is not a call.

Stop fires at the end of every turn, including a turn that pauses mid-task to
ask the user something. The ask is therefore keyed on the task's last edit,
not on the task: a session that is asked while pausing and stops anyway is
asked again once it has edited further, so the ask is not spent before the
task's real end. A task with no edit since validate_task_spec has nothing to
review and is never asked.
"""
import hashlib
import json
import os
import re
import sys

# The wrapper runs this under python3 -I, which leaves the script's own
# directory off sys.path, so the sibling module is put back by hand.
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from check_task_start import as_list, edit_gate_mode, subagent_transcript  # noqa: E402

SPEC_TOOL = "mcp__anti-tangent__validate_task_spec"
COMPLETION_TOOL = "mcp__anti-tangent__validate_completion"
EDIT_TOOLS = ("Edit", "Write", "NotebookEdit")
# A line is parsed only when it names a watched tool as a JSON string or holds
# an errored tool_result. Under "all" this runs at every turn end over the
# whole main-session transcript, and a line holding a large file read must
# not cost a json.loads. Inside an embedded string a quote is escaped, so a
# tool name merely mentioned in file content does not match.
WATCHED = tuple('"%s"' % name for name in (SPEC_TOOL, COMPLETION_TOOL) + EDIT_TOOLS)
ERROR_RESULT = re.compile(r'"is_error"\s*:\s*true')

STOP_MESSAGE = """TASK OPENED BUT NOT CLOSED: call validate_completion

This session opened a task with mcp__anti-tangent__validate_task_spec and is
stopping without closing it. {state}

Before finishing, call mcp__anti-tangent__validate_completion with the
session_id validate_task_spec returned, a summary, the change as final_diff
(or final_diff_path) or final_files, and test evidence. Act on its findings.

If the task is not finished -- you are pausing to ask the user something or
to wait for them -- do not call validate_completion: say so and stop. You
will be asked again only after further edits.

(Disable: ANTI_TANGENT_STOP_GUARD=0. Trace: {trace})
"""

NO_CALL = "No validate_completion call followed it."
ERRORED = ("Every validate_completion call after it came back as a tool error (refused or\n"
           "failed), so no verdict was produced. Read the error and fix its cause first.")


class Task:
    """The task opened by the last successful validate_task_spec call."""

    def __init__(self, key, closed, errored, last_edit):
        self.key = key
        self.closed = closed
        self.errored = errored
        self.last_edit = last_edit


def worth_parsing(line):
    """Return True when a transcript line can hold a call scan collects or an
    errored result: it names a watched tool, or carries "is_error": true."""
    return any(name in line for name in WATCHED) or ERROR_RESULT.search(line) is not None


def scan(lines):
    """Return the Task opened by the last successful validate_task_spec call in
    an iterable of transcript lines, or None when there is none.

    It collects the calls of the two tools and the edit tools in order and the ids whose result was an
    error, then judges: the task is the last spec call not in that set, and it
    is closed when a validate_completion after it is not in that set either.
    last_edit is the key of the last successful Edit/Write/NotebookEdit call
    after the spec call, or "" when there is none: a refused edit changed
    nothing, so it must not open a new stretch to ask about. A line worth_parsing rejects is
    skipped before it is parsed. A malformed line or entry is skipped rather
    than raised on.
    """
    calls = []
    failed = set()
    for number, line in enumerate(lines):
        if not worth_parsing(line):
            continue
        try:
            entry = json.loads(line)
        except Exception:
            continue
        if not isinstance(entry, dict):
            continue
        msg = entry.get("message")
        if not isinstance(msg, dict):
            continue
        etype = entry.get("type")
        for part in as_list(msg.get("content")):
            if not isinstance(part, dict):
                continue
            if etype == "assistant" and part.get("type") == "tool_use" and part.get("name") in (SPEC_TOOL, COMPLETION_TOOL) + EDIT_TOOLS:
                call_id = part.get("id")
                key = call_id if isinstance(call_id, str) and call_id else "line-%d" % number
                calls.append((part.get("name"), key))
            elif etype == "user" and part.get("type") == "tool_result" and part.get("is_error") is True:
                tuid = part.get("tool_use_id")
                if isinstance(tuid, str):
                    failed.add(tuid)
    opened = [i for i, (name, key) in enumerate(calls) if name == SPEC_TOOL and key not in failed]
    if not opened:
        return None
    start = opened[-1]
    after = [key for name, key in calls[start + 1:] if name == COMPLETION_TOOL]
    closed = any(key not in failed for key in after)
    edits = [key for name, key in calls[start + 1:] if name in EDIT_TOOLS and key not in failed]
    return Task(calls[start][1], closed, bool(after) and not closed, edits[-1] if edits else "")


def own_transcript(data):
    """Return the stopping session's transcript path, or ""."""
    if data.get("hook_event_name") == "SubagentStop":
        path = data.get("agent_transcript_path")
        if isinstance(path, str) and path:
            return path
        return subagent_transcript(data)
    path = data.get("transcript_path")
    return path if isinstance(path, str) else ""


def claim(directory, transcript, key):
    """Record that this stretch of a task's edits has been asked about, and
    return True when this call is the one that recorded it. Raises OSError when the record cannot be
    written."""
    digest = hashlib.sha256(("%s\0%s" % (transcript, key)).encode("utf-8")).hexdigest()[:16]
    stamp = os.path.join(directory, "stop-asked-%s" % digest)
    flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_NOFOLLOW", 0)
    try:
        os.close(os.open(stamp, flags, 0o600))
    except FileExistsError:
        return False
    return True


def gated(event, mode):
    if event == "SubagentStop":
        return mode in ("subagents", "all")
    if event == "Stop":
        return mode == "all"
    return False


def decide(data, mode, try_claim):
    """Return (exit code, trace word, task) for one payload."""
    event = data.get("hook_event_name")
    if not gated(event, mode):
        return 3, "not-gated", None
    if data.get("stop_hook_active") is True:
        return 3, "already-continued", None
    path = own_transcript(data)
    if not path:
        return 3, "no-transcript", None
    try:
        with open(path, encoding="utf-8", errors="replace") as fh:
            task = scan(fh)
    except OSError:
        return 3, "no-transcript", None
    if task is None:
        return 0, "no-task", None
    if task.closed:
        return 0, "closed", task
    if not task.last_edit:
        return 0, "no-edits", task
    try:
        first = try_claim(path, task.key + "\0" + task.last_edit)
    except OSError:
        return 3, "no-state", task
    if not first:
        return 3, "asked", task
    return 2, "open-errored" if task.errored else "open", task


def main():
    try:
        data = json.loads(sys.stdin.read())
    except Exception:
        return 3
    if not isinstance(data, dict):
        return 3
    mode = edit_gate_mode(os.environ.get("ATG_EDIT_GATE")) or "off"
    trace_log = os.environ.get("ATG_TRACE_LOG", "")

    def try_claim(path, key):
        if not trace_log:
            raise OSError("no state directory")
        return claim(os.path.dirname(trace_log), path, key)

    code, word, task = decide(data, mode, try_claim)
    sys.stdout.write(word)
    if code == 2:
        sys.stderr.write(STOP_MESSAGE.format(state=ERRORED if task.errored else NO_CALL, trace=trace_log))
    return code


if __name__ == "__main__":
    sys.exit(main())
