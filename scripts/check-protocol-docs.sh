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
