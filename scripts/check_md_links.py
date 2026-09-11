#!/usr/bin/env python3
"""Relative-link and anchor check over the repository's tracked markdown.

Each inline link target, [text](target), is resolved relative to the file that
contains it, which is how GitHub and every other renderer resolves it. Two
things fail the check: a target that does not exist, and a #fragment that names
no anchor in the markdown file it points at. A file's anchors are the ids GitHub
generates for its headings, plus any explicit <a id="..."> or <a name="...">.
http, https and mailto targets are skipped.

Only inline links are checked. Reference-style links, [text][ref] with a
separate `[ref]: target` definition line, are not, and none exist in the
scanned files. Recognising them has four traps, each of which produces a silent
miss rather than a loud failure: a fence tracker that lets ``` and ~~~ close
each other desyncs and skips the rest of the file; a definition line may be
indented by one to three spaces; an angle-bracketed target may contain spaces;
and a label may contain an escaped ]. Prove all four before adding them.
"""

import html
import os
import re
import subprocess
import sys
import unicodedata
from urllib.parse import unquote

# docs/superpowers/ holds dated specs and plans, each a record of the tree as it
# stood when written, so their paths and headings have since moved on purpose.
# The notes under examples/ link with Basic Memory permalinks and template
# placeholders, which resolve inside Basic Memory rather than the repository;
# the README among them is ordinary repository prose and stays in.
EXCLUDED_PREFIXES = ("docs/superpowers/", "examples/")
INCLUDED_ANYWAY = frozenset({"examples/project-knowledge/README.md"})

EXTERNAL_PREFIXES = ("http://", "https://", "mailto:")

FENCE_RE = re.compile(r"^([ \t]*)(`{3,}|~{3,})(.*)$")
ATX_RE = re.compile(r"^ {0,3}#{1,6}(?:[ \t]+(.*?))?[ \t]*$")
LINK_RE = re.compile(r"\]\(([^)]+)\)")
HTML_ANCHOR_RE = re.compile(r"""<a\s[^>]*?\b(?:id|name)\s*=\s*["']([^"']+)["']""", re.IGNORECASE)
CODE_SPAN_RE = re.compile(r"(`+)(.+?)\1")
CODE_PLACEHOLDER_RE = re.compile(r"\0(\d+)\0")
IMAGE_RE = re.compile(r"!\[[^\]]*\]\([^)]*\)")
INLINE_LINK_RE = re.compile(r"\[([^\]]*)\]\([^)]*\)")
TAG_RE = re.compile(r"<[^>]+>")
# An underscore run with a letter or digit on neither side is an emphasis
# delimiter (_core_, __init__); one inside a word (snake_case) is text.
UNDERSCORE_EMPHASIS_RE = re.compile(r"(?<![^\W_])_+|_+(?![^\W_])")


def in_scope(path):
    return path in INCLUDED_ANYWAY or not path.startswith(EXCLUDED_PREFIXES)


def github_slug(text):
    """The base id GitHub gives a heading whose rendered text is `text`.

    Lowercased; every character that is not a letter, digit, combining mark,
    space, hyphen or underscore is dropped; then each space becomes a hyphen.
    Spaces are not collapsed first, so "a & b" slugs to "a--b".
    """
    kept = (c for c in text.strip().lower() if c in " -_" or unicodedata.category(c)[0] in "LNM")
    return "".join(kept).replace(" ", "-")


class Slugger:
    """Assigns heading ids in document order, as GitHub does: an id already
    given to an earlier heading gets the next free -1, -2, ... suffix."""

    def __init__(self):
        self.occurrences = {}

    def slug(self, text):
        base = github_slug(text)
        result = base
        while result in self.occurrences:
            self.occurrences[base] += 1
            result = f"{base}-{self.occurrences[base]}"
        self.occurrences[result] = 0
        return result


def rendered_text(raw):
    """A heading's text as GitHub renders it.

    Code spans are set aside first and restored verbatim last, so neither a
    `<PLACEHOLDER>` nor a `[x](y)` inside backticks is read as markup, while a
    link whose text is a code span still reduces to that text. Everywhere else,
    images are dropped, links reduce to their text, inline HTML tags are
    removed, underscore emphasis delimiters are dropped and entities decoded.
    """
    spans = []

    def hold(m):
        spans.append(m.group(2))
        return f"\0{len(spans) - 1}\0"

    text = CODE_SPAN_RE.sub(hold, raw)
    text = IMAGE_RE.sub("", text)
    text = INLINE_LINK_RE.sub(r"\1", text)
    text = TAG_RE.sub("", text)
    text = UNDERSCORE_EMPHASIS_RE.sub("", text)
    text = html.unescape(text)
    return CODE_PLACEHOLDER_RE.sub(lambda m: spans[int(m.group(1))], text)


def heading_text(line):
    """The raw text of an ATX heading line, closing #s removed, or None."""
    m = ATX_RE.match(line)
    if not m:
        return None
    return re.sub(r"(?:^|[ \t]+)#+$", "", m.group(1) or "")


def _indent(whitespace):
    return len(whitespace.expandtabs(4))


def _find_outside_code(line, needle, pos, code_ranges):
    i = line.find(needle, pos)
    while i != -1 and any(a <= i < b for a, b in code_ranges):
        i = line.find(needle, i + 1)
    return i


def strip_html_comments(line):
    """-> (the line with its HTML comments removed, whether one is still open
    at its end). A <!-- inside a code span is text, not an opener."""
    code_ranges = [m.span() for m in CODE_SPAN_RE.finditer(line)]
    out, pos = [], 0
    while True:
        start = _find_outside_code(line, "<!--", pos, code_ranges)
        if start == -1:
            out.append(line[pos:])
            return "".join(out), False
        out.append(line[pos:start])
        end = line.find("-->", start + 4)
        if end == -1:
            return "".join(out), True
        pos = end + 3


def parse(text):
    """-> (anchors, links, errors) for one markdown document.

    links are (line number, raw target) pairs; errors are (line number,
    message) pairs. Nothing inside a fenced code block or an HTML comment is a
    heading, an anchor or a link, and nothing inside an inline code span is an
    anchor or a link: a `# comment` in a shell example must not become an id,
    and neither may a heading GitHub never renders.

    A fence closes only on its opening character, at least as long as the
    opener, indented less than four columns past it, with nothing after it. An
    opener is accepted at any indentation so that a fence inside a list item is
    recognised; the cost is that an indented code line starting with ``` opens
    one. A fence or comment still open at the end of the file is an error
    rather than a quiet reason to skip the rest of it, which is what turns that
    cost, and any other desync, into a loud failure.
    """
    anchors, links, errors = set(), [], []
    slugger = Slugger()
    fence = None    # (character, run length, indent, opening line number)
    comment = None  # opening line number of an HTML comment not yet closed
    for n, line in enumerate(text.splitlines(), 1):
        if comment is not None:
            end = line.find("-->")
            if end == -1:
                continue
            line = line[end + 3:]
            comment = None
        m = FENCE_RE.match(line)
        if fence is None:
            # A backtick run with a backtick later on the line is an inline
            # code span, not a fence opener.
            if m and not (m.group(2)[0] == "`" and "`" in m.group(3)):
                fence = (m.group(2)[0], len(m.group(2)), _indent(m.group(1)), n)
                continue
        else:
            if (m and m.group(2)[0] == fence[0] and len(m.group(2)) >= fence[1]
                    and _indent(m.group(1)) < fence[2] + 4 and not m.group(3).strip()):
                fence = None
            continue
        line, still_open = strip_html_comments(line)
        if still_open:
            comment = n
        # An anchor or link written inside a code span is an example of the
        # syntax, not markup GitHub renders. Only a match's start is tested, so
        # a link whose text is itself a code span is still read as a link.
        code = [m.span() for m in CODE_SPAN_RE.finditer(line)]
        anchors.update(m.group(1) for m in HTML_ANCHOR_RE.finditer(line)
                       if not any(a <= m.start() < b for a, b in code))
        raw = heading_text(line)
        if raw is not None:
            anchors.add(slugger.slug(rendered_text(raw)))
        links.extend((n, m.group(1)) for m in LINK_RE.finditer(line)
                     if not any(a <= m.start() < b for a, b in code))
    if fence is not None:
        errors.append((fence[3], "code fence is never closed; everything after it renders as code"))
    if comment is not None:
        errors.append((comment, "HTML comment is never closed; everything after it is hidden"))
    return anchors, links, errors


def split_target(raw):
    """-> (path, fragment) of a link target, its title and angle brackets removed."""
    if raw.startswith("<"):
        dest = raw[1:].split(">", 1)[0]
    else:
        dest = raw.split(None, 1)[0]
    path, _, fragment = dest.partition("#")
    return unquote(path), unquote(fragment)


def resolve(src, path):
    """The repository-relative path a link in `src` points at. A leading slash
    is the repository root, as GitHub resolves it."""
    if path.startswith("/"):
        return os.path.normpath(path.lstrip("/"))
    return os.path.normpath(os.path.join(os.path.dirname(src), path))


def check(root, files):
    """-> (errors, stats). errors are (file, line, message) triples; stats
    counts what was actually checked, so a clean run can say what it vouches for."""
    parsed = {}

    def load(rel):
        if rel not in parsed:
            with open(os.path.join(root, rel), encoding="utf-8") as fh:
                parsed[rel] = parse(fh.read())
        return parsed[rel]

    errors = []
    stats = {"files": len(files), "links": 0, "anchors": 0}
    for src in files:
        _, links, doc_errors = load(src)
        errors.extend((src, n, msg) for n, msg in doc_errors)
        for n, raw in links:
            raw = raw.strip()
            if not raw or raw.startswith(EXTERNAL_PREFIXES):
                continue
            path, fragment = split_target(raw)
            target = resolve(src, path) if path else src
            full = os.path.join(root, target)
            stats["links"] += 1
            if not os.path.exists(full):
                errors.append((src, n, f"broken relative link -> {raw} (resolved: {target})"))
                continue
            # Fragments on anything but a markdown file (a source file's #L12
            # line anchor, a directory) name nothing this check can see.
            if not fragment or not target.lower().endswith(".md") or not os.path.isfile(full):
                continue
            stats["anchors"] += 1
            if fragment not in load(target)[0]:
                errors.append((src, n, f"broken anchor -> {raw} (no heading or <a id> in {target} has the id '{fragment}')"))
    return errors, stats


def markdown_files(root):
    out = subprocess.run(
        ["git", "-C", root, "ls-files", "-z", "--", "*.md"],
        check=True, capture_output=True, text=True,
    ).stdout
    return sorted(
        p for p in out.split("\0")
        if p and in_scope(p) and os.path.isfile(os.path.join(root, p))
    )


def main():
    root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    try:
        files = markdown_files(root)
    except (OSError, subprocess.CalledProcessError) as exc:
        print(f"::error::could not list tracked markdown files: {exc}")
        return 1
    if not files:
        print("::error::no tracked markdown files in scope; the link check would vouch for nothing")
        return 1
    errors, stats = check(root, files)
    for src, n, msg in errors:
        print(f"::error file={src},line={n}::{msg}")
    if errors:
        return 1
    print(f"✓ markdown links OK: {stats['links']} relative links and {stats['anchors']} anchors across {stats['files']} files")
    return 0


if __name__ == "__main__":
    sys.exit(main())
