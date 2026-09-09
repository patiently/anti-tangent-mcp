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
    # Bare `#\d+` fires on an all-numeric hex colour ("#123456") and on
    # ordinary prose using "#1" as an ordinal or rank ("the #1 rule", "the #1
    # priority"). A trigger word merely NEAR the digits is not enough either:
    # "see the #1 best practice guide" and "issue #1 is always price
    # sensitivity" both put an ordinary-English sense of a trigger word within
    # a few characters of a "#N" that names nothing. The tell instead requires
    # the trigger to GOVERN the digits — separated from them by nothing but
    # optional whitespace and, for a closed set of connector nouns, one of
    # those nouns — so "see #N" and "fixes issue #N" match while "see the #1
    # guide" does not (the word "the" is not in the allowed span). Three of
    # the connectors below — issue, bug, reference — carry ordinary-English
    # senses "fix"/"close"/"resolve"/"ref"/"pr" do not, so they are accepted
    # only as the connector after one of those verbs, never as a trigger on
    # their own; a bare "issue #1" or "bug ... #1" with no governing verb no
    # longer matches at all, trading recall for precision on exactly the
    # ambiguous case a regex cannot otherwise resolve.
    (re.compile(
        r"\b(?:fix(?:e[sd])?|close[sd]?|resolve[sd]?|see|ref(?:s)?|pr|pull\s*request)\b"
        r"\s*(?:(?:issue|bug|ticket|item|number|no\.?|reference[sd]?)\s*)?"
        r"#\d+\b",
        re.I,
    ), "an issue or pull-request reference"),
    # A version number that names a wire-compatibility contract ("reads the
    # v0.10.0 stats output", "shape, v0.11.0+", "the live v0.21.1 server",
    # "v0.15.0, may also receive ...") documents what the code does NOW, not
    # when it changed — it reads correctly to someone who never saw the
    # change that introduced it. Keying on a nearby noun to exclude that
    # shape has the mirror-image flaw of the tell above: an unrelated noun
    # like "server" merely sitting near a genuine change reference ("added
    # v1.2.3 support for the metrics server") would suppress a real
    # violation. The tell instead requires a change verb to GOVERN the
    # version, on either side of it ("added in vX.Y.Z", "since vX.Y.Z",
    # "vX.Y.Z removes ...") — this fails safe: a wire-compatibility sentence
    # like "reads the vX.Y.Z output" or "accepts vX.Y.Z+" has no change verb
    # anywhere near the version and so never matches, without needing to
    # name the nouns ("output", "server", "shape") that happen to appear in
    # it.
    (re.compile(
        r"\b(?:add(?:s|ed)?|remov(?:es|ed)?|bump(?:s|ed)?|deprecat(?:es|ed)?|"
        r"releas(?:es|ed)?|ship(?:s|ped)?|chang(?:es|ed)?|fix(?:es|ed)?|"
        r"introduc(?:es|ed)?|since)\b\s*(?:in|to|as of|for)?\s*v\d+\.\d+\.\d+\b"
        r"|"
        r"\bv\d+\.\d+\.\d+\b\s*(?:add(?:s|ed)?|remov(?:es|ed)?|bump(?:s|ed)?|"
        r"deprecat(?:es|ed)?|releas(?:es|ed)?|ship(?:s|ped)?|chang(?:es|ed)?|"
        r"fix(?:es|ed)?|introduc(?:es|ed)?)\b",
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
