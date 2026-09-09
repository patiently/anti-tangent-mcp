"""Shared comment-hygiene scanner for the anti-tangent-guard hooks.

Scans ADDED comment lines in source files for change-history references.
Behaviour and invariants belong in comments; change history belongs in git.
"""
import os
import re
from collections import Counter

SCAN_EXTS = {
    ".go", ".sh", ".bash", ".py", ".ts", ".tsx", ".js", ".jsx",
    ".rs", ".java", ".kt", ".rb", ".c", ".h", ".cc", ".cpp", ".hpp",
}

# Line-comment openers per extension. Block-comment interiors, trailing
# comments and comment-like text inside string literals are out of scope:
# a partial implementation that half-detects them would produce false
# blocks, which cost more here than a missed violation.
LINE_COMMENT = {
    ".py": ("#",), ".sh": ("#",), ".bash": ("#",), ".rb": ("#",),
}
DEFAULT_COMMENT = ("//",)

TELLS = (
    # Anchored on both sides: bare `\bboundary` still let a digit run straight
    # into the following char of a URL path segment through (".../task-42/setup"
    # has a word boundary right after "42" too, since "/" is non-word) — the
    # boundary itself doesn't rule out a URL. The (?<![\w/-]) lookbehind refuses
    # a "task-" preceded by "/" or "-" (a path segment or a compound word like
    # "subtask-4"), and the (?!/) lookahead refuses one immediately followed by
    # another path segment. [a-zA-Z]* absorbs a trailing letter suffix on the
    # id itself (some task ids carry one), not just on the following prose.
    (re.compile(r"(?<![\w/-])task-\d+[a-zA-Z]*\b(?!/)", re.I), "a task reference"),
    # Bare `#\d+` also fires on an all-numeric hex colour ("#123456") and on
    # ordinary prose ("the #1 rule") — neither is an issue/PR reference. Require
    # a change/reference verb within a short window before the digits; this is
    # a precision trade against recall (a bare "// #25: ..." with no such verb
    # on its own line no longer matches), acceptable because the false case
    # blocks a write mid-edit and the true case has other lines to be caught on.
    (re.compile(
        r"\b(?:fix(?:e[sd])?|close[sd]?|resolve[sd]?|see|ref(?:s|erence[sd]?)?|"
        r"issue|pr|pull\s*request|bug)\b[^#\n]{0,20}#\d+\b",
        re.I,
    ), "an issue or pull-request reference"),
    # A version number that names a wire-compatibility contract ("reads the
    # v0.10.0 stats output", "shape, v0.11.0+", "the live v0.21.1 server",
    # "v0.15.0, may also receive ...") documents what the code does NOW, not
    # when it changed — it reads correctly to someone who never saw the change
    # that introduced it, so post.tmpl's "reads correctly" test does not flag
    # it either. The three exclusions target exactly that shape: a floor
    # annotation ("vX.Y.Z+"), a version adjacent to "live", and a version
    # named as the source of a shape/output a caller receives.
    (re.compile(
        r"(?<!live )\bv\d+\.\d+\.\d+\b(?!\+)"
        r"(?!.{0,30}?\b(?:stats output|server|may also receive)\b)",
        re.I,
    ), "a version reference"),
)


def openers(path):
    return LINE_COMMENT.get(os.path.splitext(path)[1].lower(), DEFAULT_COMMENT)


def scannable(path):
    return os.path.splitext(path)[1].lower() in SCAN_EXTS


def violations(path, added_lines):
    """Return [(line, why)] for added comment lines carrying change history."""
    if not scannable(path):
        return []
    opens = openers(path)
    out = []
    for raw in added_lines:
        line = raw.strip()
        if not any(line.startswith(o) for o in opens):
            continue
        for pat, why in TELLS:
            if pat.search(line):
                out.append((line, why))
                break
    return out


def added(old_text, new_text):
    """Lines in new_text beyond the occurrences already present in old_text.

    Occurrence-aware on purpose. A set would discard a NEWLY ADDED duplicate of
    a comment that already appears elsewhere in the file, which is exactly the
    case a writer hits when copying a bad comment to a second site.
    """
    remaining = Counter(old_text.splitlines())
    out = []
    for line in new_text.splitlines():
        if remaining.get(line, 0) > 0:
            remaining[line] -= 1
        else:
            out.append(line)
    return out
