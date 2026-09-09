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
    # the trigger to GOVERN the digits, separated from them by nothing but:
    # optional whitespace, an optional colon (GitHub's own "Fixes: #N"
    # closing syntax), and then EITHER "also" alone (a citation-style "see
    # also #N" has no ordinary-prose reading) OR a REQUIRED connector noun,
    # optionally preceded by "the". The "the"-bridge admits only "issue" and
    # "pr" — not the full connector set — because "the item #4 dialog", "the
    # ticket #4 printer jam", "the bug #7 spray pattern" are all ordinary
    # English with "the <noun> #N" read as "the Nth <noun>", and no regex can
    # tell those from a genuine "the issue #N" / "the PR #N" tracker
    # reference by surface form alone. The other connector nouns keep only
    # their BARE form (no "the", directly after a strong verb — "fixes issue
    # #N", "closes bug #N"), which needs no "the" in front of it to read
    # unambiguously as a reference. "reference"/"references" is also a
    # standalone trigger (for
    # GitHub's own "References #N" syntax) with the same ordinary-English
    # collision: "the reference #2 style" reads as "the second reference",
    # not a tracker link. `(?<!the )` refuses "reference[sd]?" as a trigger
    # when directly preceded by "the " — "References #N" at the start of a
    # clause is unaffected, only the "the reference #N" shape is excluded.
    (re.compile(
        r"\b(?:fix(?:e[sd])?|close[sd]?|resolve[sd]?|see|ref(?:s)?|pr|"
        r"pull\s*request|(?<!the )reference[sd]?)\b\s*(?::\s*)?"
        r"(?:also\s*|(?:the\s+(?:issue|pr)|issue|bug|ticket|pr|item|number|"
        r"no\.?|reference[sd]?)\s*)?"
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
    # anywhere near the version and so never matches, without needing to name
    # the nouns ("output", "server", "shape") that happen to appear in it.
    #
    # A bare-parenthesis shape — "the version alone inside its own
    # parenthesis, nothing else" — is deliberately NOT a tell, even though it
    # would catch a verb-free pattern common in this project's own history
    # ("Categories emitted by prime_project_knowledge (vX.Y.Z)."). A
    # wire-compatibility sentence can put its version in parentheses too
    # ("backward compatible with (vX.Y.Z)", "accepts (vX.Y.Z) or later
    # payloads", "matches the wire shape used by the daemon (vX.Y.Z)") and
    # shape alone cannot tell those from a genuine history reference — both
    # are exactly "(vX.Y.Z)" with nothing else inside the parens. Unlike the
    # tells above, no enumerable word closes this gap:
    # the ambiguity is in what the surrounding SENTENCE means, not in what
    # sits next to the version. A false positive here blocks a write
    # mid-edit; a false negative is still caught by post.tmpl's semantic
    # comment-hygiene rule, which names a version reference as a defect on
    # its own judgement, verb or no verb. So this class — a version with
    # neither a governing verb nor extractable regex signal — is left
    # deliberately, permanently reviewer-led rather than regex-led. See the
    # design spec's Part 3 for the measured recall this costs.
    #
    # The optional preposition bridge is written `\s*(?:(?:in|to|as of|for)\s*)?`
    # rather than `\s*(?:in|to|as of|for)?\s*`. Two adjacent greedy `\s*` with
    # only an optional group between them give whitespace no single owner: on a
    # long run of spaces that is not followed by a version, the engine retries
    # every split point between them before failing, which is quadratic in the
    # length of the run. Keeping the trailing `\s*` inside the optional group
    # leaves exactly one way to divide the whitespace.
    (re.compile(
        r"\b(?:add(?:s|ed)?|remov(?:es|ed)?|bump(?:s|ed)?|deprecat(?:es|ed)?|"
        r"releas(?:es|ed)?|ship(?:s|ped)?|chang(?:es|ed)?|fix(?:es|ed)?|"
        r"introduc(?:es|ed)?|since)\b\s*(?:(?:in|to|as of|for)\s*)?v\d+\.\d+\.\d+\b"
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
