"""PreToolUse body: refuse an Edit/Write that ADDS a change-history comment.

Reads the hook payload as JSON on stdin. The wrapper consumes the hook's own
stdin and re-feeds it here, so nothing else in this process may read stdin.
Exit 0 = allow, 2 = block, anything else = internal error the wrapper converts
to allow.
"""
import json
import os
import sys

sys.path.insert(0, os.path.join(os.environ.get("ATG_ROOT", ""), "hooks"))
from comment_scan import violations, added, scannable  # noqa: E402

data = json.loads(sys.stdin.read())
inp = data.get("tool_input") or {}
path = inp.get("file_path") or ""

if (data.get("tool_name") or "") not in ("Edit", "Write") or not scannable(path):
    sys.exit(0)

if data["tool_name"] == "Edit":
    lines = added(inp.get("old_string") or "", inp.get("new_string") or "")
else:
    content = inp.get("content") or ""
    try:
        with open(path, "rb") as fh:
            # Read one byte past the cap rather than trusting a prior stat: the
            # file can grow between getsize() and read(). Binary mode, and the
            # cap counted on the raw bytes, because a text-mode read counts
            # decoded characters and would let a multibyte file slip past a
            # byte cap. errors="replace" keeps a file that is not valid UTF-8
            # (one stray Latin-1 byte is enough) from raising here and taking
            # the whole scan down with it, which the wrapper would read as an
            # internal error and allow.
            raw = fh.read(2_000_001)
        if len(raw) > 2_000_000:
            sys.exit(0)
        lines = added(raw.decode("utf-8", errors="replace"), content)
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
