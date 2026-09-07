#!/bin/bash
# Checks that the "Ours" benchmark table in README.md matches
# benchmarks.tsv row for row, cell for cell, joined on scenario_id.
#
# This is deliberately NOT a "does this number appear somewhere in the
# file" grep: two scenarios can share a number, and a stale figure can
# survive unnoticed in unrelated prose. Both of those pass a substring
# check and both are exactly the drift this script exists to catch. It
# parses the actual README table into rows, joins each one to its
# benchmarks.tsv row by scenario_id (never by prose), and compares every
# column — the label included. A scenario present in one file and absent
# from the other fails loudly rather than being silently ignored.
#
# Needs no network and no API keys — it reads two files already in the repo.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PLUGIN_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
README="$PLUGIN_DIR/README.md"
TSV="$SCRIPT_DIR/benchmarks.tsv"

# Column names, in TSV order — the README table's columns must appear in
# this same order (spec: "one column per TSV column, in TSV order").
COLS=(scenario_id label lines without_tokens with_value with_unit savings_pct worker_in worker_out runs statistic)
NCOLS=${#COLS[@]}

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

[ -f "$README" ] || fail "missing $README"
[ -f "$TSV" ] || fail "missing $TSV"

# normalize CELL strips thousands-separator commas, a trailing '%', and
# treats an em dash or empty string as the canonical empty value — so
# "42,993" == "42993", "99%" == "99", and "—" == "" == "".
normalize() {
  local v="$1"
  v="${v#"${v%%[![:space:]]*}"}"   # ltrim
  v="${v%"${v##*[![:space:]]}"}"   # rtrim
  v="${v//,/}"
  v="${v%\%}"
  [ "$v" = "—" ] && v=""
  printf '%s' "$v"
}

# ── Load benchmarks.tsv into tsv_row[scenario_id]="col1<US>col2<US>..." ──
declare -A tsv_row
declare -a tsv_order
US=$'\x1f'

tsv_header_checked=0
while IFS= read -r rawline; do
  # Tab is bash's "IFS whitespace": `IFS=$'\t' read -a` silently COLLAPSES
  # consecutive tabs and would swallow the empty savings_pct field on the
  # code_write row. Swap tabs for the (non-whitespace) unit separator first
  # so `read -a` splits without collapsing empty fields.
  IFS="$US" read -r -a fields <<<"${rawline//$'\t'/$US}"
  if [ "$tsv_header_checked" -eq 0 ]; then
    tsv_header_checked=1
    if [ "${#fields[@]}" -ne "$NCOLS" ]; then
      fail "benchmarks.tsv header has ${#fields[@]} columns, expected $NCOLS"
    fi
    for i in "${!COLS[@]}"; do
      [ "${fields[$i]}" = "${COLS[$i]}" ] || fail "benchmarks.tsv header column $((i+1)) is '${fields[$i]}', expected '${COLS[$i]}'"
    done
    continue
  fi
  [ -z "${fields[*]:-}" ] && continue
  sid="${fields[0]}"
  [ -n "${tsv_row[$sid]:-}" ] && fail "benchmarks.tsv: duplicate scenario_id '$sid'"
  # Pad missing trailing fields (e.g. empty savings_pct at end of line) to NCOLS.
  local_joined=""
  for i in $(seq 0 $((NCOLS - 1))); do
    local_joined+="${fields[$i]:-}${US}"
  done
  tsv_row[$sid]="$local_joined"
  tsv_order+=("$sid")
done < "$TSV"

[ "$tsv_header_checked" -eq 1 ] || fail "benchmarks.tsv has no header row"
[ "${#tsv_order[@]}" -gt 0 ] || fail "benchmarks.tsv has no data rows"

# ── Extract the README's "Ours" table (the one whose header starts with
#    scenario_id) into readme_row[scenario_id]="col1<US>col2<US>..." ──
declare -A readme_row
declare -a readme_order

in_table=0
saw_header=0
saw_sep=0
while IFS= read -r line; do
  if [ "$in_table" -eq 0 ]; then
    # Match a markdown table header row whose first cell is scenario_id.
    if [[ "$line" =~ ^\|[[:space:]]*scenario_id[[:space:]]*\| ]]; then
      in_table=1
      saw_header=1
    fi
    continue
  fi
  # First line inside the table is the header itself (already matched above
  # to enter in_table); the very next line must be the '---' separator row
  # (an 11-column separator has 11 '|'s, not 2, so this checks that
  # stripping every '|', ':', '-' and space leaves nothing behind, rather
  # than matching a fixed column count).
  if [ "$saw_sep" -eq 0 ]; then
    stripped=$(printf '%s' "$line" | tr -d ' \t|:-')
    if [ -z "$stripped" ] && [[ "$line" == *-* ]]; then
      saw_sep=1
      continue
    fi
    fail "README.md: table header for 'scenario_id' is not followed by a '---' separator row"
  fi
  # A line that isn't a table row ends the table.
  [[ "$line" == \|* ]] || break

  IFS='|' read -r -a raw <<<"$line"
  # raw[0] is empty (before the leading '|'); last element is empty or
  # trailing text after the final '|'. Real cells are raw[1..NCOLS].
  if [ "${#raw[@]}" -lt $((NCOLS + 1)) ]; then
    fail "README.md 'Ours' table row has $(( ${#raw[@]} - 1 )) cells, expected $NCOLS: $line"
  fi
  row_joined=""
  for i in $(seq 1 "$NCOLS"); do
    cell="${raw[$i]}"
    cell="${cell#"${cell%%[![:space:]]*}"}"
    cell="${cell%"${cell##*[![:space:]]}"}"
    row_joined+="${cell}${US}"
  done
  sid_raw="${raw[1]}"
  sid="${sid_raw#"${sid_raw%%[![:space:]]*}"}"
  sid="${sid%"${sid##*[![:space:]]}"}"
  [ -n "${readme_row[$sid]:-}" ] && fail "README.md: duplicate scenario_id '$sid' in the Ours table"
  readme_row[$sid]="$row_joined"
  readme_order+=("$sid")
done < "$README"

[ "$saw_header" -eq 1 ] || fail "README.md: no table with a 'scenario_id' header column found"
[ "${#readme_order[@]}" -gt 0 ] || fail "README.md: 'scenario_id' table has no data rows"

# ── Set equality: every scenario_id must appear in both files ──
for sid in "${tsv_order[@]}"; do
  [ -n "${readme_row[$sid]:-}" ] || fail "scenario '$sid' is in benchmarks.tsv but missing from the README Ours table"
done
for sid in "${readme_order[@]}"; do
  [ -n "${tsv_row[$sid]:-}" ] || fail "scenario '$sid' is in the README Ours table but missing from benchmarks.tsv"
done

# ── Cell-by-cell comparison ──
for sid in "${tsv_order[@]}"; do
  IFS="$US" read -r -a tcols <<<"${tsv_row[$sid]}"
  IFS="$US" read -r -a rcols <<<"${readme_row[$sid]}"
  for i in "${!COLS[@]}"; do
    tval=$(normalize "${tcols[$i]:-}")
    rval=$(normalize "${rcols[$i]:-}")
    if [ "$tval" != "$rval" ]; then
      fail "scenario '$sid' column '${COLS[$i]}': README has '${rcols[$i]:-}' but benchmarks.tsv has '${tcols[$i]:-}'"
    fi
  done
done

echo "OK: README Ours table matches benchmarks.tsv (${#tsv_order[@]} scenarios, $NCOLS columns each)"
exit 0
