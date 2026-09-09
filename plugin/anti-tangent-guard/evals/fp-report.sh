#!/usr/bin/env bash
# Zero-false-positive gate for the comment-hygiene scanner.
#
# fp-scan.py produces the raw hits the shipped scanner finds in this
# repository's own committed source. fp-class.tsv, maintained by hand next to
# it, records what each of those hits IS: a `true_positive` is a comment that
# genuinely carries change history and that the scanner is right to flag; a
# `false_positive` is ordinary prose the patterns misread. This script joins the
# two and fails unless every false-positive count is zero.
#
# THE JOIN KEY IS PATH PLUS COMMENT TEXT, NOT PATH AND LINE NUMBER. Identical
# text is the same judgement wherever in the file it sits, and the text is what
# a human actually vouched for. Keying on a line number instead would turn any
# commit that inserts a line above a classified comment into a red build, in a
# file the commit never meant to touch — and on a pull_request event, where the
# scan runs against the merge commit, it would surface line shifts the branch
# never made. Occurrences are counted rather than deduplicated: one entry
# vouches for exactly N copies of that text at that path, so a copy-paste of a
# flagged comment to a second site in the same file still has to be re-judged.
#
# The join is strict in BOTH directions, because either kind of drift silently
# empties the gate:
#   - a raw hit with no classification means a widened pattern, or a newly
#     written comment, started matching something nobody has judged yet;
#   - a classification with no raw hit means the comment was rewritten or
#     deleted, and the entry now vouches for nothing;
#   - a duplicate key means one of the two entries is being ignored;
#   - a count that does not match means the same text gained or lost a copy.
# A rewritten comment therefore surfaces twice — UNKNOWN at its old text and
# UNCLASSIFIED at its new one — which is the property that makes this gate
# worth having. Counting before those checks would let a stale table report a
# clean zero.
#
# Regenerating after a deliberate change to the tells or to a flagged comment:
#   python3 -B plugin/anti-tangent-guard/evals/fp-scan.py > /tmp/fp-raw.tsv
# then reconcile fp-class.tsv against it by hand — every new key needs a human
# judgement, which is the entire point of keeping the classification separate
# from the scan.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLASS_FILE="$SCRIPT_DIR/fp-class.tsv"
# The template ends in its run of X's, and so carries no extension: BSD/macOS
# mktemp rejects a template with anything after the X's. Nothing downstream
# reads this file by name — awk takes it as a positional argument — so the
# missing ".tsv" costs nothing. The result is checked before the redirect
# below and before the trap, because this script runs without `set -e` and a
# failed mktemp would otherwise leave an empty path on both.
RAW_FILE="$(mktemp "${TMPDIR:-/tmp}/atg-fp-raw.XXXXXX")"
if [[ ! -f "$RAW_FILE" ]]; then
    echo "mktemp failed — refusing to run the scan without a scratch file to hold it."
    exit 1
fi
trap 'rm -f "$RAW_FILE"' EXIT

if [[ ! -f "$CLASS_FILE" ]]; then
    echo "missing $CLASS_FILE — the gate cannot classify anything without it."
    exit 1
fi

if ! python3 -B "$SCRIPT_DIR/fp-scan.py" > "$RAW_FILE"; then
    echo "fp-scan.py failed; refusing to report a count from a partial scan."
    exit 1
fi

awk -F'\t' '
FILENAME == class_file {
    if ($0 ~ /^#/ || $0 ~ /^[[:space:]]*$/) next
    if (NF != 4) {
        printf("  MALFORMED classification line %d: expected 4 tab-separated fields, got %d\n", FNR, NF)
        errors++
        next
    }
    key = $1 SUBSEP $4
    if (key in verdict) {
        printf("  DUPLICATE classification for %s\n    %s\n", $1, $4)
        errors++
        next
    }
    if ($2 !~ /^[1-9][0-9]*$/) {
        printf("  BAD count %s for %s (expected a positive integer)\n    %s\n", $2, $1, $4)
        errors++
        next
    }
    if ($3 != "true_positive" && $3 != "false_positive") {
        printf("  BAD verdict %s for %s (expected true_positive or false_positive)\n    %s\n", $3, $1, $4)
        errors++
        next
    }
    verdict[key] = $3
    want[key] = $2 + 0
    path_of[key] = $1
    text_of[key] = $4
    classified += $2 + 0
    next
}
{
    if (NF != 4) {
        printf("  MALFORMED raw hit line %d\n", FNR)
        errors++
        next
    }
    key = $1 SUBSEP $4
    raw++
    if (!(key in verdict)) {
        # Reported once per distinct key: a text that appears N times
        # unclassified is one judgement missing, not N.
        if (!(key in unclassified)) {
            printf("  UNCLASSIFIED hit %s:%s\n    %s\n", $1, $2, $4)
            errors++
        }
        unclassified[key] = 1
        next
    }
    got[key]++
    if (verdict[key] == "false_positive") {
        printf("  FALSE POSITIVE %s:%s\n    %s\n", $1, $2, $4)
        fp++
    }
}
END {
    for (k in verdict) {
        if (!(k in got)) {
            printf("  UNKNOWN classification %s — no hit with this comment text in the scan\n    %s\n", path_of[k], text_of[k])
            errors++
        } else if (got[k] != want[k]) {
            printf("  COUNT MISMATCH %s — classified %d occurrence(s), scanned %d\n    %s\n", path_of[k], want[k], got[k], text_of[k])
            errors++
        }
    }
    printf("scanned hits: %d   classified: %d\n", raw, classified)
    if (errors > 0) {
        # No false-positive count is printed on this path. It would be computed
        # from a join that did not hold, and a "FALSE POSITIVES: 0" line is the
        # first thing a reader takes for a pass.
        printf("join errors: %d — no false-positive count can be trusted until they are resolved.\n", errors)
        printf("::error file=plugin/anti-tangent-guard/evals/fp-class.tsv::%d join error(s) against the scan, listed in the log above. fp-class.tsv classifies every comment-hygiene hit found in the source of this repository, keyed on path plus comment text. Regenerate the scan with: python3 -B plugin/anti-tangent-guard/evals/fp-scan.py > /tmp/fp-raw.tsv — then reconcile fp-class.tsv against it by hand, since every new or rewritten comment needs a human judgement.\n", errors)
        exit 1
    }
    printf("FALSE POSITIVES: %d\n", fp)
    if (fp > 0) exit 1
}
' class_file="$CLASS_FILE" "$CLASS_FILE" "$RAW_FILE"
