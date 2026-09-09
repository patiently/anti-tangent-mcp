"""PreToolUse body: refuse an Edit/Write that ADDS a change-history comment.

Reads the hook payload as JSON on stdin. The wrapper consumes the hook's own
stdin and re-feeds it here, so nothing else in this process may read stdin.
Exit 0 = allow, 2 = block, anything else = internal error the wrapper converts
to allow.
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
    lines = added(inp.get("old_string") or "", inp.get("new_string") or "")
else:
    content = inp.get("content") or ""
    existing = read_text_capped(path)
    if existing is MISSING:
        # Nothing on disk yet, so every line of the new content is added.
        lines = content.splitlines()
    elif existing is None:
        # A symlink, a FIFO, a directory, an oversized or unreadable file.
        # Nothing here can be compared against, and a scan that cannot see the
        # old text would report the whole file as added.
        sys.exit(0)
    else:
        lines = added(existing, content)

bad = violations(path, lines)
if not bad:
    sys.exit(0)

print("BLOCKED: this edit adds comment(s) carrying change history.\n", file=sys.stderr)
for line, why in bad[:10]:
    # The line comes from the payload and is unbounded there. bad[:10] bounds
    # how many are echoed, not how long each one is, and a single very long
    # line would otherwise reach the model as megabytes of hook stderr.
    print("  %s\n    -> contains %s" % (line[:200], why), file=sys.stderr)
print(
    "\nComments must explain non-trivial behaviour or a non-obvious invariant, and must read\n"
    "correctly to someone who never saw this change. Issue, task and version references belong\n"
    "in the commit message, not the code. Rewrite the comment and retry.",
    file=sys.stderr,
)
sys.exit(2)
