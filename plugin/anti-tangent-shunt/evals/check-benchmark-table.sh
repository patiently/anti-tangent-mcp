#!/bin/bash
# Checks that every "scenario_id"-keyed benchmark table in this plugin's
# published docs — the "Ours" table in README.md, AND benchmarks.md's own
# "Computed table" — matches benchmarks.tsv row for row, cell for cell,
# joined on scenario_id.
#
# This is deliberately NOT a "does this number appear somewhere in the
# file" grep: two scenarios can share a number, and a stale figure can
# survive unnoticed in unrelated prose. Both of those pass a substring
# check and both are exactly the drift this script exists to catch. It
# parses each markdown table into rows, joins each one to its
# benchmarks.tsv row by scenario_id (never by prose), and compares every
# column present in that table. A scenario present in one file and absent
# from the other fails loudly rather than being silently ignored.
#
# benchmarks.md's Computed table was added to this check after it drifted
# to a stale 100% savings figure that only the README/TSV pair was being
# verified against (see finding 9 / m1 in the 2026-09-07 final review) —
# this script originally covered README<->TSV only, which is exactly why
# that drift went unnoticed.
#
# Needs no network and no API keys — it reads files already in the repo.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PLUGIN_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
README="$PLUGIN_DIR/README.md"
TSV="$SCRIPT_DIR/benchmarks.tsv"
BENCHMD="$SCRIPT_DIR/benchmarks.md"

# Column names, in TSV order — the README table's columns must appear in
# this same order (spec: "one column per TSV column, in TSV order").
COLS=(scenario_id label lines without_tokens with_value with_unit savings_pct worker_in worker_out runs statistic)
NCOLS=${#COLS[@]}

# benchmarks.md's "Computed table" carries a subset of those columns, in
# its own order, with no "label"/"runs"/"statistic" columns. MDIDX maps
# each MDCOLS entry to its index in COLS/tsv_row.
MDCOLS=(scenario_id lines without_tokens with_value with_unit savings_pct worker_in worker_out)
MDIDX=(0 2 3 4 5 6 7 8)
NMD=${#MDCOLS[@]}

# Literal header cell text each markdown table is expected to carry, in
# order — these are the human-readable display names, NOT the COLS/MDCOLS
# machine names, since README.md and benchmarks.md each render their own
# header row. parse_scenario_table validates every one of these, not just
# the leading "scenario_id" cell, so a renamed or reordered later column
# fails loudly instead of silently passing.
README_HEADER=(scenario_id Scenario Lines Without With Unit Savings "Worker in" "Worker out" Runs Stat)
MD_HEADER=(scenario_id lines without with unit savings "worker in" "worker out")

US=$'\x1f'

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

[ -f "$README" ] || fail "missing $README"
[ -f "$TSV" ] || fail "missing $TSV"
[ -f "$BENCHMD" ] || fail "missing $BENCHMD"

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

# parse_scenario_table FILE NCOLS EXPECTED_HEADER_VAR OUT_ROW_VAR OUT_ORDER_VAR
#
# Extracts the first markdown table in FILE whose header row starts with
# "scenario_id" into OUT_ROW_VAR[scenario_id]="col1<US>col2<US>..." and
# OUT_ORDER_VAR (an array of scenario_ids in file order). NCOLS is the exact
# number of data columns the table is expected to have (not counting the
# empty leading cell before the first '|') and EXPECTED_HEADER_VAR names an
# array of the NCOLS literal header cell values, in order — every header
# cell is checked against it, not just the first ("scenario_id"). Every data
# row must also have EXACTLY ncols cells: an extra, missing, or renamed
# column — in the header or in any row — fails loudly rather than being
# truncated or ignored.
parse_scenario_table() {
  local file="$1" ncols="$2"
  local -n expected_header="$3"
  local -n out_row="$4"
  local -n out_order="$5"

  local in_table=0 saw_header=0 saw_sep=0
  local line stripped row_joined sid_raw sid i cell
  local -a raw

  while IFS= read -r line; do
    if [ "$in_table" -eq 0 ]; then
      # Match a markdown table header row whose first cell is scenario_id.
      if [[ "$line" =~ ^\|[[:space:]]*scenario_id[[:space:]]*\| ]]; then
        in_table=1
        saw_header=1
        IFS='|' read -r -a raw <<<"$line"
        if [ "${#raw[@]}" -ne $((ncols + 1)) ]; then
          fail "$file: 'scenario_id' header row has $(( ${#raw[@]} - 1 )) cells, expected exactly $ncols: $line"
        fi
        for i in "${!expected_header[@]}"; do
          cell="${raw[$((i + 1))]}"
          cell="${cell#"${cell%%[![:space:]]*}"}"
          cell="${cell%"${cell##*[![:space:]]}"}"
          if [ "$cell" != "${expected_header[$i]}" ]; then
            fail "$file: 'scenario_id' header column $((i + 1)) is '$cell', expected '${expected_header[$i]}'"
          fi
        done
      fi
      continue
    fi
    # First line inside the table is the header itself (already matched
    # above to enter in_table); the very next line must be the '---'
    # separator row (an N-column separator has N '|'s, not 2, so this
    # checks that stripping every '|', ':', '-' and space leaves nothing
    # behind, rather than matching a fixed column count).
    if [ "$saw_sep" -eq 0 ]; then
      stripped=$(printf '%s' "$line" | tr -d ' \t|:-')
      if [ -z "$stripped" ] && [[ "$line" == *-* ]]; then
        saw_sep=1
        continue
      fi
      fail "$file: table header for 'scenario_id' is not followed by a '---' separator row"
    fi
    # A line that isn't a table row ends the table.
    [[ "$line" == \|* ]] || break

    IFS='|' read -r -a raw <<<"$line"
    # raw[0] is empty (before the leading '|'); real cells are raw[1..ncols].
    # Exact equality, not "at least ncols": a row with extra cells (an added
    # or renamed column) must fail here, not have its extra cell silently
    # dropped by the loop below.
    if [ "${#raw[@]}" -ne $((ncols + 1)) ]; then
      fail "$file 'scenario_id' table row has $(( ${#raw[@]} - 1 )) cells, expected exactly $ncols: $line"
    fi
    row_joined=""
    for i in $(seq 1 "$ncols"); do
      cell="${raw[$i]}"
      cell="${cell#"${cell%%[![:space:]]*}"}"
      cell="${cell%"${cell##*[![:space:]]}"}"
      row_joined+="${cell}${US}"
    done
    sid_raw="${raw[1]}"
    sid="${sid_raw#"${sid_raw%%[![:space:]]*}"}"
    sid="${sid%"${sid##*[![:space:]]}"}"
    [ -n "${out_row[$sid]:-}" ] && fail "$file: duplicate scenario_id '$sid' in its scenario_id table"
    out_row[$sid]="$row_joined"
    out_order+=("$sid")
  done < "$file"

  [ "$saw_header" -eq 1 ] || fail "$file: no table with a 'scenario_id' header column found"
  [ "${#out_order[@]}" -gt 0 ] || fail "$file: 'scenario_id' table has no data rows"
}

# ── Load benchmarks.tsv into tsv_row[scenario_id]="col1<US>col2<US>..." ──
declare -A tsv_row
declare -a tsv_order

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

# ── README.md "Ours" table vs. benchmarks.tsv ──
declare -A readme_row
declare -a readme_order
parse_scenario_table "$README" "$NCOLS" README_HEADER readme_row readme_order

for sid in "${tsv_order[@]}"; do
  [ -n "${readme_row[$sid]:-}" ] || fail "scenario '$sid' is in benchmarks.tsv but missing from the README Ours table"
done
for sid in "${readme_order[@]}"; do
  [ -n "${tsv_row[$sid]:-}" ] || fail "scenario '$sid' is in the README Ours table but missing from benchmarks.tsv"
done

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

# ── benchmarks.md "Computed table" vs. benchmarks.tsv ──
declare -A md_row
declare -a md_order
parse_scenario_table "$BENCHMD" "$NMD" MD_HEADER md_row md_order

for sid in "${tsv_order[@]}"; do
  [ -n "${md_row[$sid]:-}" ] || fail "scenario '$sid' is in benchmarks.tsv but missing from benchmarks.md's Computed table"
done
for sid in "${md_order[@]}"; do
  [ -n "${tsv_row[$sid]:-}" ] || fail "scenario '$sid' is in benchmarks.md's Computed table but missing from benchmarks.tsv"
done

for sid in "${tsv_order[@]}"; do
  IFS="$US" read -r -a tcols <<<"${tsv_row[$sid]}"
  IFS="$US" read -r -a mcols <<<"${md_row[$sid]}"
  for j in "${!MDCOLS[@]}"; do
    tsv_idx="${MDIDX[$j]}"
    tval=$(normalize "${tcols[$tsv_idx]:-}")
    mval=$(normalize "${mcols[$j]:-}")
    if [ "$tval" != "$mval" ]; then
      fail "scenario '$sid' column '${MDCOLS[$j]}' (benchmarks.md Computed table): has '${mcols[$j]:-}' but benchmarks.tsv has '${tcols[$tsv_idx]:-}'"
    fi
  done
done

echo "OK: benchmarks.md Computed table matches benchmarks.tsv (${#tsv_order[@]} scenarios, $NMD columns each)"
exit 0
