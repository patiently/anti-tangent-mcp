#!/bin/bash
#
# bench.sh — measurement harness for plugin/anti-tangent-shunt/evals/benchmarks.md
#
# Drives the REAL anti-tangent-mcp server over MCP stdio (newline-delimited
# JSON-RPC 2.0, exactly what a host does), issuing one `tools/call` per run
# for each of the four benchmark scenarios. Real provider calls are made —
# this costs real money — so it is deliberately NOT run as part of `go test`
# or CI; it is a manual, checked-in reproduction script.
#
# What it does NOT do: compute medians or write benchmarks.md/benchmarks.tsv.
# Those are hand-authored from this script's raw output so every number in
# the checked-in tables traces back to an actual run. This script's job is
# only to produce that raw output faithfully and reproducibly.
#
# Usage:
#   export OPENAI_API_KEY=...                          # or the provider matching the model below
#   export ANTI_TANGENT_WORKER_MODEL=openai:gpt-5.6-luna
#   export CORPUS_DIR=/tmp/bench-corpus                 # the pinned corpus checkout (see benchmarks.md Step 1)
#   bash plugin/anti-tangent-shunt/evals/bench.sh
#
# Output: one TSV row per run to stdout (12 runs: 3 each for scenarios 1-3,
# plus scenario 4's own 3), columns:
#   scenario_id  run  input_tokens  output_tokens  review_ms  value_bytes  lines_written  raw_file
# where value_bytes is the byte length of the returned `answer` (scenarios
# 1-3) or of the file code_write just wrote (scenario 4, before it is
# deleted for the next run), lines_written is populated for scenario 4 only,
# and raw_file is the full captured JSON-RPC response for that run so any
# number here can be traced back to what the server actually returned.
#
# Any run that errors is still emitted, as a row with "ERROR" in place of
# the numeric fields and the error text in raw_file's sibling .err file —
# never silently dropped, and the script's own exit code is non-zero if any
# run errored. It keeps going after an error so a bad run doesn't cost you
# the rest of the data.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"

ATM_BIN="${ATM_BIN:-/tmp/atm}"
CORPUS_DIR="${CORPUS_DIR:-/tmp/bench-corpus}"
OUT_DIR="${BENCH_OUT_DIR:-/tmp/bench-out}"
RAW_DIR="$OUT_DIR/raw"

: "${ANTI_TANGENT_WORKER_MODEL:?set ANTI_TANGENT_WORKER_MODEL to the exact model id to record in benchmarks.md, e.g. openai:gpt-5.6-luna}"

if [ ! -d "$CORPUS_DIR" ]; then
  echo "CORPUS_DIR ($CORPUS_DIR) does not exist — clone the pinned corpus first (see benchmarks.md Step 1)" >&2
  exit 1
fi

mkdir -p "$OUT_DIR" "$RAW_DIR"

if [ ! -x "$ATM_BIN" ]; then
  echo "building $ATM_BIN from $REPO_ROOT ..." >&2
  ( cd "$REPO_ROOT" && go build -o "$ATM_BIN" ./cmd/anti-tangent-mcp ) || exit 1
fi

# Fixed harness config, disclosed here and in benchmarks.md. These are set
# UNCONDITIONALLY (not merely defaulted) — an ambient ANTI_TANGENT_* value
# from the caller's own shell (e.g. a dev setup pointing PLAN_ROOTS at a
# different tree entirely) must not silently steer this harness somewhere
# other than the pinned corpus:
#   - PLAN_ROOTS covers both the read-only corpus and the write-only scratch
#     dir code_write's target lives under.
#   - MAX_PAYLOAD_BYTES is raised from the 204800-byte default: scenario 2's
#     source+test pair is ~216KB, just over the default cap. Raising the cap
#     changes nothing about what is measured (both "without" and "with" use
#     the same input); it only lets the call complete instead of refusing.
#   - WORKER_MAX_TOKENS is raised from the 4096-byte default to the 16384
#     ceiling so a table-driven test covering several functions (scenario 4)
#     is not cut off mid-generation.
#   - STATS_DIR is scoped under OUT_DIR so this run's per-call token log
#     doesn't mix into any other stats directory the caller has configured.
export ANTI_TANGENT_PLAN_ROOTS="$CORPUS_DIR:$OUT_DIR"
export ANTI_TANGENT_MAX_PAYLOAD_BYTES="262144"
export ANTI_TANGENT_WORKER_MAX_TOKENS="16384"
export ANTI_TANGENT_STATS_DIR="$OUT_DIR/stats"

FAILED=0

# ── MCP stdio plumbing ──────────────────────────────────────────────────

coproc ATM { exec "$ATM_BIN" 2>"$OUT_DIR/atm.stderr"; }
ATM_IN="${ATM[1]}"
ATM_OUT="${ATM[0]}"

cleanup() {
  kill "$ATM_PID" 2>/dev/null || true
  wait "$ATM_PID" 2>/dev/null || true
}
trap cleanup EXIT

send() { printf '%s\n' "$1" >&"$ATM_IN"; }
recv() { local line; IFS= read -r line <&"$ATM_OUT" || return 1; printf '%s' "$line"; }

REQ_ID=1
# call_tool NAME ARGS_JSON -> prints the raw JSON-RPC response line
call_tool() {
  local name="$1" args_json="$2" req
  REQ_ID=$((REQ_ID + 1))
  req=$(jq -nc --arg name "$name" --argjson args "$args_json" --argjson id "$REQ_ID" \
    '{jsonrpc:"2.0", id:$id, method:"tools/call", params:{name:$name, arguments:$args}}')
  send "$req"
  recv
}

# ── Handshake ───────────────────────────────────────────────────────────

send '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"anti-tangent-bench","version":"1.0.0"}}}'
init_resp=$(recv)
if ! jq -e '.result.serverInfo' >/dev/null 2>&1 <<<"$init_resp"; then
  echo "initialize handshake failed: $init_resp" >&2
  cat "$OUT_DIR/atm.stderr" >&2
  exit 1
fi
send '{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}'

# ── One run of a bulk_read scenario (scenarios 1-3) ────────────────────
# Emits its TSV row to stdout and returns non-zero on error (already
# reported in the row) without aborting the whole script.
run_bulk_read() {
  local scenario_id="$1" run="$2" question="$3"; shift 3
  local paths_json resp raw_file
  paths_json=$(printf '%s\n' "$@" | jq -R . | jq -sc .)
  resp=$(call_tool bulk_read "$(jq -nc --arg q "$question" --argjson p "$paths_json" '{question:$q, paths:$p}')")
  raw_file="$RAW_DIR/${scenario_id}-run${run}.json"
  printf '%s\n' "$resp" > "$raw_file"

  if jq -e '.error' >/dev/null 2>&1 <<<"$resp"; then
    printf '%s\t%s\tERROR\tERROR\tERROR\tERROR\t\t%s\n' "$scenario_id" "$run" "$raw_file"
    echo "ERROR ($scenario_id run $run): $(jq -c '.error' <<<"$resp")" >&2
    return 1
  fi
  local verdict answer in_tok out_tok ms bytes
  verdict=$(jq -r '.result.structuredContent.verdict // empty' <<<"$resp")
  if [ -n "$verdict" ]; then
    printf '%s\t%s\tERROR\tERROR\tERROR\tERROR\t\t%s\n' "$scenario_id" "$run" "$raw_file"
    echo "REFUSED ($scenario_id run $run): $(jq -c '.result.structuredContent.findings' <<<"$resp")" >&2
    return 1
  fi
  answer=$(jq -r '.result.structuredContent.answer' <<<"$resp")
  in_tok=$(jq -r '.result.structuredContent.input_tokens' <<<"$resp")
  out_tok=$(jq -r '.result.structuredContent.output_tokens' <<<"$resp")
  ms=$(jq -r '.result.structuredContent.review_ms' <<<"$resp")
  bytes=$(printf '%s' "$answer" | wc -c)
  printf '%s\t%s\t%s\t%s\t%s\t%s\t\t%s\n' "$scenario_id" "$run" "$in_tok" "$out_tok" "$ms" "$bytes" "$raw_file"
}

# ── One run of the code_write scenario (scenario 4) ────────────────────
run_code_write() {
  local scenario_id="$1" run="$2" spec="$3" reference_path="$4" target_path="$5"
  local resp raw_file
  rm -f "$target_path"
  resp=$(call_tool code_write "$(jq -nc --arg s "$spec" --arg r "$reference_path" --arg t "$target_path" \
    '{spec:$s, reference_path:$r, target_path:$t, overwrite:false}')")
  raw_file="$RAW_DIR/${scenario_id}-run${run}.json"
  printf '%s\n' "$resp" > "$raw_file"

  if jq -e '.error' >/dev/null 2>&1 <<<"$resp"; then
    printf '%s\t%s\tERROR\tERROR\tERROR\tERROR\tERROR\t%s\n' "$scenario_id" "$run" "$raw_file"
    echo "ERROR ($scenario_id run $run): $(jq -c '.error' <<<"$resp")" >&2
    return 1
  fi
  local in_tok out_tok ms lines bytes
  in_tok=$(jq -r '.result.structuredContent.input_tokens' <<<"$resp")
  out_tok=$(jq -r '.result.structuredContent.output_tokens' <<<"$resp")
  ms=$(jq -r '.result.structuredContent.review_ms' <<<"$resp")
  lines=$(jq -r '.result.structuredContent.lines_written' <<<"$resp")
  if [ ! -f "$target_path" ]; then
    printf '%s\t%s\tERROR\tERROR\tERROR\tERROR\tERROR\t%s\n' "$scenario_id" "$run" "$raw_file"
    echo "ERROR ($scenario_id run $run): code_write reported success but $target_path is missing" >&2
    return 1
  fi
  bytes=$(wc -c < "$target_path")
  printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$scenario_id" "$run" "$in_tok" "$out_tok" "$ms" "$bytes" "$lines" "$raw_file"
}

# ── Scenarios (exact prompts pinned here AND reproduced verbatim in benchmarks.md) ──

Q1='Which exported functions in this file mutate package-level state, and what are their receivers?'
Q2='Which behaviours does the test file cover that the source file'"'"'s exported API exposes, and which exported functions have no test?'
Q3='Which functions cross package boundaries between these files, and in which direction does each call flow?'
SPEC4='A table-driven test for the exported functions in the reference file, covering the zero value, one ordinary case, and one error case per function.'

echo -e "scenario_id\trun\tinput_tokens\toutput_tokens\treview_ms\tvalue_bytes\tlines_written\traw_file"

for run in 1 2 3; do
  run_bulk_read single_large_file "$run" "$Q1" \
    "$CORPUS_DIR/promql/engine.go" || FAILED=1
done

for run in 1 2 3; do
  run_bulk_read source_test_pair "$run" "$Q2" \
    "$CORPUS_DIR/web/api/v1/api.go" "$CORPUS_DIR/web/api/v1/api_test.go" || FAILED=1
done

for run in 1 2 3; do
  run_bulk_read multi_file_cross_package "$run" "$Q3" \
    "$CORPUS_DIR/rules/group.go" "$CORPUS_DIR/model/timestamp/timestamp.go" "$CORPUS_DIR/model/value/value.go" || FAILED=1
done

for run in 1 2 3; do
  run_code_write code_write "$run" "$SPEC4" \
    "$CORPUS_DIR/tsdb/head.go" "$OUT_DIR/head_bench_test.go" || FAILED=1
done

exit "$FAILED"
