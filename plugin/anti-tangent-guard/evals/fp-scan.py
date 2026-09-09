"""Run the shipped comment-hygiene scanner over this repository's own source.

Every tracked file with a scannable extension is read as its HEAD blob, not from
the working tree: the classification in fp-class.tsv is keyed on `path:line`, and
a working-tree read would let an uncommitted edit shift those line numbers under
a classification that was written against committed content.

Every comment line in the blob is offered to the scanner as if it were an added
line, which is the worst case the write-time hook can see: a file rewritten in
full. That makes the hit set an upper bound on what this repository's own
comments can ever trigger.

Output is TSV on stdout, one hit per line:

    <path>:<line> <TAB> <why> <TAB> <matched comment text>

Exit 1 if any tracked, scannable file could not be read out of HEAD. Skipping
one silently would shrink the hit set without shrinking the risk, which is the
one failure this gate cannot tolerate.
"""
import os
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(os.path.dirname(HERE), "hooks"))
from comment_scan import scannable, violations  # noqa: E402


def main():
    root = subprocess.check_output(
        ["git", "rev-parse", "--show-toplevel"], text=True).strip()
    listing = subprocess.check_output(["git", "ls-files", "-z"], cwd=root)
    unreadable = []
    hits = 0
    for path in listing.decode("utf-8", errors="replace").split("\0"):
        if not path or not scannable(path):
            continue
        blob = subprocess.run(
            ["git", "show", "HEAD:" + path], cwd=root, capture_output=True)
        if blob.returncode != 0:
            unreadable.append(path)
            continue
        text = blob.stdout.decode("utf-8", errors="replace")
        for n, raw in enumerate(text.splitlines(), 1):
            for line, why in violations(path, [raw]):
                sys.stdout.write("%s:%d\t%s\t%s\n" % (path, n, why, line))
                hits += 1
    if unreadable:
        sys.stderr.write(
            "fp-scan: %d tracked file(s) could not be read out of HEAD.\n"
            "Every tracked source file must be scanned or this gate proves nothing.\n"
            "A file staged or created but not yet committed reads as unreadable here —\n"
            "commit it, then re-run.\n" % len(unreadable))
        for path in unreadable:
            sys.stderr.write("  %s\n" % path)
        return 1
    sys.stderr.write("fp-scan: %d hit(s)\n" % hits)
    return 0


if __name__ == "__main__":
    sys.exit(main())
