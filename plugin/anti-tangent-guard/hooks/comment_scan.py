"""Shared comment-hygiene scanner for the anti-tangent-guard hooks.

Scans ADDED comment lines in source files for change-history references.
Behaviour and invariants belong in comments; change history belongs in git.
"""
import os
import re
import stat
from collections import Counter

# Cap on how much of a caller-supplied file either hook will read.
READ_CAP_BYTES = 2_000_000

# Returned by read_text_capped when nothing exists at the path. Distinct from
# None ("something is there, but it must not or cannot be read") because the
# two hooks want opposite things from the two cases: a Write to a path that
# does not exist yet means every line of the new content is added and worth
# scanning, while a path that cannot be read is one neither hook may act on.
MISSING = object()


def read_text_capped(path):
    """Read path as decoded text. Returns MISSING, or None if unusable.

    The path is caller-supplied and both hooks run unsandboxed, so it gets the
    three guards the server applies to the same kind of field: O_NOFOLLOW
    refuses a symlink at the final component, O_NONBLOCK keeps a FIFO from
    parking the hook forever inside open(), and S_ISREG rejects every other
    kind of special file — a directory included.

    One byte past the cap is read rather than trusting a prior stat, since the
    file can grow in between. The cap is counted on raw bytes because a
    text-mode read counts decoded characters and would let a multibyte file
    slip past a byte cap. errors="replace" keeps a file that is not valid UTF-8
    (one stray Latin-1 byte is enough) from raising and taking the whole scan
    down with it.

    None means "no usable content", and every caller must fail OPEN on it: a
    file that cannot be read must never turn into a blocked write or a blocked
    task close. This project weights a false block above a miss.
    """
    fd = -1
    try:
        try:
            fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
        except FileNotFoundError:
            return MISSING
        if not stat.S_ISREG(os.fstat(fd).st_mode):
            return None
        with os.fdopen(fd, "rb") as fh:
            fd = -1
            raw = fh.read(READ_CAP_BYTES + 1)
        if len(raw) > READ_CAP_BYTES:
            return None
        return raw.decode("utf-8", errors="replace")
    except Exception:
        return None
    finally:
        if fd >= 0:
            try:
                os.close(fd)
            except Exception:
                pass

SCAN_EXTS = {
    ".go", ".sh", ".bash", ".py", ".ts", ".tsx", ".js", ".jsx",
    ".rs", ".java", ".kt", ".rb", ".c", ".h", ".cc", ".cpp", ".hpp",
}

# Comment openers per extension. Hash-family files have no block comment
# form; C-family files do, and two of its shapes are recognised here: an
# opener `/*`, and a continuation `*`.
#
# `*/` is NOT an opener. A line beginning with it carries no comment text --
# `*/` alone is empty and `*/ y = x - 1;` is code -- so admitting it and
# returning the tail would hand ordinary code to the tells.
LINE_COMMENT = {
    ".py": ("#",), ".sh": ("#",), ".bash": ("#",), ".rb": ("#",),
}
DEFAULT_COMMENT = ("//", "/*", "*")


def _quotes_balanced(prefix):
    """True when no quote style is left open at the end of prefix.

    A counting test, not a parser: it cannot tell which style opened a span,
    only whether an odd number of some style is outstanding. Odd means the
    delimiter that follows sits inside a string literal. Backslash-escaped
    quotes do not count, so 'a\\'b' reads as balanced.
    """
    counts = {'"': 0, "'": 0, "`": 0}
    esc = False
    for ch in prefix:
        if esc:
            esc = False
        elif ch == "\\":
            esc = True
        elif ch in counts:
            counts[ch] += 1
    return all(n % 2 == 0 for n in counts.values())


_HASH_DELIM = re.compile(r"(?<=\s)#")
_SLASH_DELIM = re.compile(r"//|/\*")


def _line_comment_spans(opens, raw):
    """Every comment span on a code line, concatenated, else "".

    Walks delimiters LEFT TO RIGHT and takes the first whose preceding quote
    counts are all even; an odd count means that delimiter sits inside a
    string literal, so it is skipped and the walk continues. Taking the last
    delimiter instead would let a benign trailing comment hide a violating
    one earlier on the same line (`x = 1 /* fixes #1 */ // ok`).

    A `/* ... */` span ends at its terminator and the walk RESUMES after it,
    so the code between two block comments is never scanned and a second
    comment on the line is not lost. A `//` or `#` span runs to end of line
    and ends the walk.

    Within the quoting shapes counted by _quotes_balanced, this can decline a
    genuine comment but will not promote code to comment. In hash-family files
    the delimiter must be whitespace-preceded, so shell parameter expansion
    (`${url#https://…}`) is not a comment.
    """
    delim = _HASH_DELIM if "#" in opens else _SLASH_DELIM
    out, pos, seg = [], 0, 0
    while pos < len(raw):
        m = delim.search(raw, pos)
        if m is None:
            break
        start = m.start()
        # Parity is counted over the CURRENT CODE SEGMENT, not the whole
        # prefix. An earlier block comment's text is not code, and counting
        # its quotes corrupts the answer for every delimiter after it: an
        # unbalanced quote inside `/* say "hi */` would make a later `//`
        # sitting inside a string look balanced, promoting string content to
        # comment text. `seg` advances past each block terminator.
        if not _quotes_balanced(raw[seg:start]):
            pos = m.end()
            continue
        if m.group(0) == "/*":
            end = raw.find("*/", m.end())
            if end < 0:
                out.append(raw[m.end():])
                break
            out.append(raw[m.end():end])
            pos = seg = end + 2
            continue
        out.append(raw[m.end():])
        break
    return [t for t in out if t.strip()]


def comment_spans(path, raw):
    """Every comment span on one line, as a list; empty when there are none.

    A LIST, not one joined string. Tells are matched per span, because joining
    them can synthesise a reference that exists in neither: "/* fixes */ x
    /* #1 */" concatenates into text the issue tell matches, while each span
    alone stays clean.

    Almost everything goes through the left-to-right walk above; only a block
    continuation `*` and a column-zero `#` are handled directly, because
    neither is a delimiter that walk recognises.
    """
    opens = openers(path)
    line = raw.strip()
    # Only two shapes are handled here. A block CONTINUATION `*` and a
    # column-zero `#` are not delimiters the walk recognises -- _HASH_DELIM
    # requires whitespace before the `#`, which a line-leading one has not
    # got. `//` and `/*` ARE recognised, and sending them through the walk is
    # what lets a line carrying both a block opener and a trailing `//`
    # yield BOTH spans, instead of stopping at the first and missing
    # whatever the second one says.
    if "*" in opens and line.startswith("*") and not line.startswith("*/"):
        rest = line[1:]
        # `*p = x - 1;` is a dereference and a subtraction, not a comment.
        if rest and not rest[0].isspace():
            return []
        end = rest.find("*/")
        if end >= 0:
            rest = rest[:end]
        return [rest] if rest.strip() else []
    if "#" in opens and line.startswith("#"):
        rest = line[1:]
        return [rest] if rest.strip() else []
    return _line_comment_spans(opens, raw)

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
    out = []
    for raw in added_lines:
        for span in comment_spans(path, raw):
            hit = next((why for pat, why in TELLS if pat.search(span)), None)
            if hit is not None:
                out.append((raw.strip(), hit))
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
