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
# The join is one-to-one and strict in BOTH directions, because either kind of
# drift silently empties the gate:
#   - a raw hit with no classification means a widened pattern started matching
#     something nobody has judged yet;
#   - a classification with no raw hit means the line moved or the comment was
#     rewritten, and the entry now vouches for nothing;
#   - a duplicate key means one of the two entries is being ignored;
#   - matching keys whose comment TEXT differs means the line number still
#     resolves but to different content, so the verdict no longer applies.
# Counting before those checks would let a stale table report a clean zero.
#
# Regenerating after a deliberate change to the tells or to a flagged comment:
#   python3 -B plugin/anti-tangent-guard/evals/fp-scan.py > /tmp/fp-raw.tsv
# then reconcile fp-class.tsv against it by hand — every new key needs a human
# judgement, which is the entire point of keeping the classification separate
# from the scan.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLASS_FILE="$SCRIPT_DIR/fp-class.tsv"
RAW_FILE="$(mktemp "${TMPDIR:-/tmp}/atg-fp-raw.XXXXXX.tsv")"
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
    if (NF != 3) {
        printf("  MALFORMED classification line %d: expected 3 tab-separated fields, got %d\n", FNR, NF)
        errors++
        next
    }
    if ($1 in verdict) {
        printf("  DUPLICATE classification for %s\n", $1)
        errors++
        next
    }
    if ($2 != "true_positive" && $2 != "false_positive") {
        printf("  BAD verdict %s for %s (expected true_positive or false_positive)\n", $2, $1)
        errors++
        next
    }
    verdict[$1] = $2
    text[$1] = $3
    classified++
    next
}
{
    if (NF != 3) {
        printf("  MALFORMED raw hit line %d\n", FNR)
        errors++
        next
    }
    if ($1 in seen) {
        printf("  DUPLICATE raw hit for %s\n", $1)
        errors++
        next
    }
    seen[$1] = 1
    raw++
    if (!($1 in verdict)) {
        printf("  UNCLASSIFIED hit %s\n    %s\n", $1, $3)
        errors++
        next
    }
    if (text[$1] != $3) {
        printf("  STALE classification for %s\n    classified: %s\n    scanned:    %s\n", $1, text[$1], $3)
        errors++
        next
    }
    if (verdict[$1] == "false_positive") {
        printf("  FALSE POSITIVE %s\n    %s\n", $1, $3)
        fp++
    }
}
END {
    for (k in verdict) if (!(k in seen)) {
        printf("  UNKNOWN classification %s — no such hit in the scan\n", k)
        errors++
    }
    printf("scanned hits: %d   classified: %d\n", raw, classified)
    printf("FALSE POSITIVES: %d\n", fp)
    if (errors > 0) {
        printf("join errors: %d — the count above is not trustworthy until they are resolved.\n", errors)
        exit 1
    }
    if (fp > 0) exit 1
}
' class_file="$CLASS_FILE" "$CLASS_FILE" "$RAW_FILE"
