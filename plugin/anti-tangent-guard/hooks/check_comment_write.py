"""PreToolUse body: refuse an Edit/Write that ADDS a change-history comment.

Reads the hook payload as JSON on stdin. The wrapper consumes the hook's own
stdin and re-feeds it here, so nothing else in this process may read stdin.
Exit 0 = allow, 2 = block (regex tier), 3 = allow without having scanned (the
target could not be read), 4 = block (the semantic tier flagged a comment as
change history), anything else = internal error the wrapper converts to
allow. Only 2 and 4 stop the tool call; 3 exists so the trace log can
distinguish a scan that ran and found nothing from one that never ran.
"""
import json
import os
import sys

# Both entries are needed because this runs under `python3 -I`, which puts
# neither the script's own directory nor the user site directory on sys.path.
# ATG_ROOT is what the wrapper hands over and wins; this file's directory is
# the same place and stands in for a root that resolved to nothing, so a
# misconfigured root cannot quietly turn the scan into an import error the
# wrapper reads as "allow".
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, os.path.join(os.environ.get("ATG_ROOT", ""), "hooks"))
from comment_scan import MISSING, violations, added, read_text_capped, scannable  # noqa: E402

data = json.loads(sys.stdin.read())
inp = data.get("tool_input") or {}
path = inp.get("file_path") or ""

if (data.get("tool_name") or "") not in ("Edit", "Write") or not scannable(path):
    sys.exit(0)

if data["tool_name"] == "Edit":
    old_string = inp.get("old_string") or ""
    new_string = inp.get("new_string") or ""
    lines = added(old_string, new_string)
    # The post-edit text, which is neither operand on its own: new_string is
    # unbalanced when the edit lands inside a literal that already exists on
    # disk, and the disk text does not contain the added line. An empty
    # old_string is refused because str.replace("") inserts between every
    # character and would hand the scanner a file that never existed.
    existing = read_text_capped(path)
    if old_string and isinstance(existing, str) and old_string in existing:
        context = (existing.replace(old_string, new_string)
                   if inp.get("replace_all")
                   else existing.replace(old_string, new_string, 1))
    else:
        context = None
else:
    content = inp.get("content") or ""
    existing = read_text_capped(path)
    if existing is MISSING:
        # Nothing on disk yet, so every line of the new content is added.
        lines = content.splitlines()
    elif existing is None:
        # A symlink, a FIFO, a directory, an oversized or unreadable file.
        # Nothing here can be compared against, and a scan that cannot see the
        # old text would report the whole file as added. Allowing the write is
        # the right call; reporting it as 3 rather than 0 keeps it out of the
        # wrapper's "pass" arm, since no scan actually happened.
        sys.exit(3)
    else:
        lines = added(existing, content)
    context = content

bad = violations(path, lines, context)
if bad:
    print("BLOCKED: this edit adds comment(s) carrying change history.\n", file=sys.stderr)
    for line, why in bad[:10]:
        # `line` is payload text and has no length limit of its own. bad[:10]
        # caps how many lines get echoed, not how long any one of them is, so
        # the [:200] here is what stops one oversized line from turning this
        # stderr block into megabytes the model then has to read.
        print("  %s\n    -> contains %s" % (line[:200], why), file=sys.stderr)
    print(
        "\nComments must explain non-trivial behaviour or a non-obvious invariant, and must read\n"
        "correctly to someone who never saw this change. Issue, task and version references belong\n"
        "in the commit message, not the code. Rewrite the comment and retry.",
        file=sys.stderr,
    )
    sys.exit(2)

import jev_scan  # noqa: E402

trace_dir = os.path.dirname(os.environ.get("ANTI_TANGENT_GUARD_TRACE_LOG")
                            or "/tmp/claude-hooks/anti-tangent-guard.log")
code, event, message = jev_scan.run(path, lines, context, os.environ,
                                    data.get("session_id") or "-", trace_dir)
if message:
    sys.stderr.write(message)
if event:
    sys.stdout.write(event)
# os._exit, not sys.exit: a worker abandoned at the deadline is still alive,
# and the interpreter-exit handler for thread pools would join it, holding
# the hook open long past the budget. The streams are flushed by hand first,
# because os._exit does not do it.
sys.stderr.flush()
sys.stdout.flush()
os._exit(code)
