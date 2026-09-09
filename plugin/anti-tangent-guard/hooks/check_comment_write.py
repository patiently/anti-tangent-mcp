"""PreToolUse body: refuse an Edit/Write that ADDS a change-history comment.

Reads the hook payload from ATG_INPUT (not stdin, which is reserved for this
file). Exit 0 = allow, 2 = block, anything else = internal error the wrapper
converts to allow.
"""
import json
import os
import sys

sys.path.insert(0, os.path.join(os.environ.get("ATG_ROOT", ""), "hooks"))
from comment_scan import violations, added, scannable  # noqa: E402

data = json.loads(os.environ.get("ATG_INPUT", ""))
inp = data.get("tool_input") or {}
path = inp.get("file_path") or ""

if (data.get("tool_name") or "") not in ("Edit", "Write") or not scannable(path):
    sys.exit(0)

if data["tool_name"] == "Edit":
    lines = added(inp.get("old_string") or "", inp.get("new_string") or "")
else:
    content = inp.get("content") or ""
    try:
        with open(path) as fh:
            lines = added(fh.read(), content)
    except FileNotFoundError:
        lines = content.splitlines()

bad = violations(path, lines)
if not bad:
    sys.exit(0)

print("BLOCKED: this edit adds comment(s) carrying change history.\n", file=sys.stderr)
for line, why in bad[:10]:
    print("  %s\n    -> contains %s" % (line, why), file=sys.stderr)
print(
    "\nComments must explain non-trivial behaviour or a non-obvious invariant, and must read\n"
    "correctly to someone who never saw this change. Issue, task and version references belong\n"
    "in the commit message, not the code. Rewrite the comment and retry.",
    file=sys.stderr,
)
sys.exit(2)
