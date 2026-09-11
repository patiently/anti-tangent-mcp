#!/usr/bin/env bash
# Guards the repository's markdown. Two failure modes matter here and neither
# is caught by a byte cap: a cross-reference that points nowhere, and a
# protocol section identifier that got duplicated or dropped during a move.
# Section numbers are cited externally (README, ~/.claude mirrors, "§5.1" in
# prose), so losing one is a silent break.
set -uo pipefail
cd "$(dirname "$0")/.." || { echo "::error::failed to cd to repo root"; exit 1; }

fail=0

# --- relative link and anchor check --------------------------------------
# Every tracked markdown file outside the exclusions in check_md_links.py,
# with each #anchor matched against the headings of the file it points at.
# That needs GitHub's heading-id rules and a code-fence tracker, so it lives
# in Python rather than here. The checker's own tests run first: a checker
# that has stopped detecting anything would otherwise report clean.
if ! python3 -B scripts/check_md_links_test.py; then
  echo "::error file=scripts/check_md_links_test.py::link checker tests fail; its verdict on the docs cannot be trusted"
  fail=1
elif ! python3 -B scripts/check_md_links.py; then
  fail=1
fi

# --- section identifier uniqueness ---------------------------------------
# §3.4 and §2 are deliberately absent from the source document — this list
# is not a contiguous range, it is the tracked set. Each must appear exactly
# once across docs/protocol/*.md: zero means dropped in a move, more than
# one means duplicated.
sections=(
  '## Scope and limits'
  '## 1\.' '### 3\.1' '### 3\.2' '### 3\.3' '### 3\.5' '### 3\.6' '### 3\.7' '### 3\.8'
  '### 3\.9'
  '## 4\.' '### 4\.2' '### 4\.3' '### 4\.4'
  '## 5\.' '### 5\.1' '### 5\.2' '### 5\.3' '### 5\.4' '### 5\.5' '### 5\.6' '### 5\.7'
  '### 5\.8'
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
