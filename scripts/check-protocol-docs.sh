#!/usr/bin/env bash
# Guards the role-scoped protocol docs. Two failure modes matter here and
# neither is caught by a byte cap: a cross-reference that points nowhere, and
# a section identifier that got duplicated or dropped during a move. Section
# numbers are cited externally (README, ~/.claude mirrors, "§5.1" in prose),
# so losing one is a silent break.
set -uo pipefail
cd "$(dirname "$0")/.."

fail=0

# --- relative link check -------------------------------------------------
# Every markdown link target in the scanned files, resolved relative to the
# file that contains it (the only way GitHub, VS Code, and every other
# renderer resolve them). External URLs, mailto:, and pure same-document
# anchors (#foo) are skipped — this guard is for relative paths only.
#
# LIMITATION (deliberate): only inline links — [text](target) — are checked.
# Reference-style links ([text][ref] plus a separate `[ref]: target`
# definition line) are NOT matched, and never have been on purpose. None
# exist anywhere in the scanned files today, so this is not a live gap.
#
# A prior round did try to close it with a bash/awk extractor plus a fence
# tracker to skip documented examples of the syntax. Review found it
# introduced more risk than it removed and it was reverted before merge.
# Recorded here so nobody repeats the same four traps:
#   - A single shared boolean toggled by both ``` and ~~~ desyncs on an
#     unclosed fence, or on an odd number of ~~~ lines appearing inside a
#     ``` block (plausible in docs that discuss markdown syntax itself) —
#     and everything after that point in the file is silently skipped, with
#     the guard still reporting clean. A silent-miss bug in a guard is worse
#     than the gap it was meant to close.
#   - CommonMark allows 1-3 spaces of leading indentation on a definition
#     line; an anchored `^\[` pattern misses those.
#   - Angle-bracketed targets, `[ref]: <path with spaces>`, mis-extract:
#     the literal `<`/`>` end up in the path, or extraction truncates at the
#     first space inside the brackets.
#   - Labels containing `]` (`[a][b]]: target`, however rare) break a naive
#     `[^]]+` label pattern.
# If reference-style links are ever introduced into these files, extend this
# guard carefully and re-prove all four cases before trusting it — a linked
# markdown-parsing library beats hand-rolled bash/awk for this.
while IFS=$'\t' read -r src target; do
  case "$target" in
    '#'*|http://*|https://*|mailto:*) continue ;;
  esac
  path="${target%% *}"  # drop an optional trailing "title"
  path="${path%%#*}"    # drop an optional #anchor
  resolved="$(dirname "$src")/$path"
  if [ ! -e "$resolved" ]; then
    echo "::error file=$src::broken relative link -> $target (resolved: $resolved)"
    fail=1
  fi
done < <(
  for f in INTEGRATION.md README.md docs/protocol/*.md; do
    [ -f "$f" ] || continue
    grep -oE '\]\([^)]+\)' "$f" | sed -E 's/^\]\(//; s/\)$//' | while IFS= read -r t; do
      printf '%s\t%s\n' "$f" "$t"
    done
  done
)

# --- section identifier uniqueness ---------------------------------------
# §3.4 and §2 are deliberately absent from the source document — this list
# is not a contiguous range, it is the tracked set. Each must appear exactly
# once across docs/protocol/*.md: zero means dropped in a move, more than
# one means duplicated.
sections=(
  '## Scope and limits'
  '## 1\.' '### 3\.1' '### 3\.2' '### 3\.3' '### 3\.5' '### 3\.6' '### 3\.7' '### 3\.8'
  '## 4\.' '### 4\.2' '### 4\.3'
  '## 5\.' '### 5\.1' '### 5\.2' '### 5\.3' '### 5\.4' '### 5\.5' '### 5\.6' '### 5\.7'
  '## 6\.'
)
for s in "${sections[@]}"; do
  n=$(grep -hcE "^$s" docs/protocol/*.md 2>/dev/null | awk '{s+=$1} END{print s+0}')
  if [ "$n" != "1" ]; then
    echo "::error::section '$s' appears $n times across docs/protocol/ (expected exactly 1)"
    fail=1
  fi
done

[ "$fail" -eq 0 ] && echo "✓ protocol docs OK"
exit "$fail"
