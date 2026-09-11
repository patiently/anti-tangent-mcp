"""Run the shipped comment-hygiene scanner over this repository's own source.

Every tracked file with a scannable extension is read as its HEAD blob, not from
the working tree: fp-class.tsv is a classification of committed content, and a
working-tree read would let an uncommitted edit change what is scanned under a
verdict nobody has passed on it yet.

Vendored third-party bundles are skipped: minified `*.min.js`, and anything
under the vendored asset directories named below. Their comments belong to
upstream, so a banner that happened to match a tell would demand a
classification entry for a comment nobody here can ever rewrite. The scanner's
false-positive rate on someone else's release artefact says nothing about
whether it blocks writes in this repository, which is the only thing this gate
measures.

Every comment line in the blob is offered to the scanner as if it were an added
line, which is the worst case the write-time hook can see: a file rewritten in
full. That makes the hit set an upper bound on what this repository's own
comments can ever trigger.

Output is TSV on stdout, one hit per line:

    <path> <TAB> <line> <TAB> <why> <TAB> <matched comment text>

The line number is for a human reading the report. fp-report.sh keys its join
on path plus comment text, so a comment that merely shifts keeps its verdict.
A tab inside a comment would add a field and corrupt every column after it, so
backslashes and tabs in the matched text are escaped on the way out; the
escaped form is what fp-class.tsv must carry, since the two are only ever
compared for equality, never decoded.

Exit 1 if any tracked, scannable file could not be read out of HEAD. Skipping
one silently would shrink the hit set without shrinking the risk, which is the
one failure this gate cannot tolerate.
"""
import os
import subprocess
import sys

# The environment is settled BEFORE the import below, because comment_scan
# binds the ticket tell at import time. Setting it afterwards would have no
# effect and the run would silently measure the shipped tells instead.
#
# Default: the gate measures the shipped tells, not whatever the developer
# happens to have configured for their own project, so a local pattern cannot
# move a verdict recorded in fp-class.tsv.
_pattern = None
_argv = sys.argv[1:]
if _argv:
    if _argv[0] != "--ticket-pattern" or len(_argv) != 2:
        sys.stderr.write("usage: fp-scan.py [--ticket-pattern <regex>]\n")
        sys.exit(2)
    _pattern = _argv[1]

if _pattern is None:
    os.environ.pop("ANTI_TANGENT_TICKET_PATTERN", None)
else:
    os.environ["ANTI_TANGENT_TICKET_PATTERN"] = _pattern

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(os.path.dirname(HERE), "hooks"))
import comment_scan  # noqa: E402
from comment_scan import scannable, violations  # noqa: E402

# _ticket_tell drops a pattern that will not compile or runs past the length
# cap, and says nothing when it does. Under the flag that silence is a trap:
# the operator would read "0 tracker hits" as proof the pattern is safe, when
# the pattern was never installed. Asking the module what it actually bound
# cannot drift from its own rules the way a re-implemented check would.
if _pattern is not None and comment_scan._ticket is None:
    sys.stderr.write(
        "fp-scan: the pattern was not installed — it failed to compile, or "
        "exceeded the %d-character cap. Nothing was measured.\n"
        % comment_scan._TICKET_PATTERN_MAX_LEN)
    sys.exit(2)

TICKET_WHY = "a tracker reference"

VENDORED_DIRS = ("gnome-topbar/daemon/internal/server/assets/",)


def vendored(path):
    return path.endswith(".min.js") or path.startswith(VENDORED_DIRS)


def escape(text):
    """Make text safe as the last field of a TSV record.

    Backslash first, then tab: escaping tab first would leave the introduced
    backslash to be doubled by the second pass.
    """
    return text.replace("\\", "\\\\").replace("\t", "\\t")


def main():
    root = subprocess.check_output(
        ["git", "rev-parse", "--show-toplevel"], text=True).strip()
    listing = subprocess.check_output(["git", "ls-files", "-z"], cwd=root)
    unreadable = []
    hits = 0
    ticket_hits = 0
    for path in listing.decode("utf-8", errors="replace").split("\0"):
        if not path or not scannable(path) or vendored(path):
            continue
        blob = subprocess.run(
            ["git", "show", "HEAD:" + path], cwd=root, capture_output=True)
        if blob.returncode != 0:
            unreadable.append(path)
            continue
        text = blob.stdout.decode("utf-8", errors="replace")
        for n, raw in enumerate(text.splitlines(), 1):
            for line, why in violations(path, [raw]):
                sys.stdout.write("%s\t%d\t%s\t%s\n" % (path, n, why, escape(line)))
                hits += 1
                if why == TICKET_WHY:
                    ticket_hits += 1
    if unreadable:
        sys.stderr.write(
            "fp-scan: %d tracked file(s) could not be read out of HEAD.\n"
            "Every tracked source file must be scanned or this gate proves nothing.\n"
            "A file staged or created but not yet committed reads as unreadable here —\n"
            "commit it, then re-run.\n" % len(unreadable))
        for path in unreadable:
            sys.stderr.write("  %s\n" % path)
        return 1
    # violations() stops at the FIRST matching tell, in TELLS order, so a line
    # a built-in tell already catches is not counted here. That is the number
    # an operator wants: what this pattern would newly block, not how often it
    # matches text something else already flags.
    if _pattern is None:
        sys.stderr.write("fp-scan: %d hit(s)\n" % hits)
    else:
        sys.stderr.write(
            "fp-scan: %d hit(s), %d of them newly attributable to the pattern\n"
            % (hits, ticket_hits))
    return 0


if __name__ == "__main__":
    sys.exit(main())
