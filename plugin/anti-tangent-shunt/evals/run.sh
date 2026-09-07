#!/bin/bash
# Copyright 2026 Spotify AB
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#
# Ported from Spotify's shunt eval runner, commit
# 3c24ca30ff63e1f5bbad1c43fe5324daff579123 (see THIRD_PARTY_NOTICES.md for the
# source repository URL). Upstream ships this licence via a repository-root
# LICENSE file rather than a per-file header; this header carries the licence
# and attribution with the file.
#
# Modified 2026-09 for anti-tangent-mcp: dropped the --benchmark mode and the
# transport eval suite (both exercised a CLI transport that does not exist in
# this repo), switched hook decisions from a JSON-on-stdout contract to
# exit-code (2 = block, 0 = allow) + stderr, and added the operational
# leftover-reference check at the end so it runs under the same command CI
# runs rather than only as a manual step.

# Test runner for the shunt-derived evals: PreToolUse hook routing decisions
# for the Read and Bash hooks that delegate to mcp__anti-tangent__bulk_read.
#
# Usage:
#   bash evals/run.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PLUGIN_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
FIXTURES="$SCRIPT_DIR/.fixtures"
PASSED=0
FAILED=0
TOTAL=0

generate_fixture() {
  local path="$1" lines="$2"
  if [ "$lines" -eq 0 ]; then
    touch "$path"
  else
    seq 1 "$lines" | awk '{print "line "NR}' > "$path"
  fi
}

setup_fixtures() {
  local evals_file="$1"
  rm -rf "$FIXTURES"
  mkdir -p "$FIXTURES"

  local count
  count=$(jq '.evals | length' "$evals_file")

  for ((i = 0; i < count; i++)); do
    local fixture
    fixture=$(jq -r ".evals[$i].fixture" "$evals_file")
    [ "$fixture" = "null" ] && continue

    local lines
    lines=$(jq -r ".evals[$i].fixture.lines" "$evals_file")

    local input_path
    input_path=$(jq -r ".evals[$i].input.tool_input.file_path // empty" "$evals_file")
    if [ -z "$input_path" ]; then
      input_path=$(jq -r ".evals[$i].input.tool_input.command // empty" "$evals_file" | sed -E 's/^(cat|head|tail|less|more) +(-[^ ]+ +)*//' | sed 's/ .*//' | tr -d '"'"'")
    fi
    input_path=$(echo "$input_path" | sed "s|{{FIXTURES}}|$FIXTURES|")

    # Commands the parser is not meant to extract a path from (grep, git, …)
    # reduce to the command name itself. Generating that would drop a junk
    # file in the working directory; the fixtures those evals rely on are
    # created by their siblings anyway.
    case "$input_path" in
      "$FIXTURES"/*) generate_fixture "$input_path" "$lines" ;;
    esac
  done
}

# run_eval invokes the hook once and derives its decision from the exit code
# (2 = block, anything else = allow) rather than a JSON field on stdout — the
# hooks in this port signal a block with exit 2 + a stderr message, matching
# the completion-guard hook shipped alongside them. A block is additionally
# required to name mcp__anti-tangent__bulk_read on stderr, so a passing block
# case proves the message, not just the exit code.
run_eval() {
  local hook="$1" name="$2" input="$3" expected="$4" reason="$5" env_json="$6"
  TOTAL=$((TOTAL + 1))

  local stderr_file exit_code actual
  stderr_file=$(mktemp)
  exit_code=0

  if [ -n "$env_json" ] && [ "$env_json" != "null" ]; then
    local env_cmd=""
    while IFS='=' read -r key val; do
      env_cmd="$env_cmd $key=$val"
    done < <(echo "$env_json" | jq -r 'to_entries[] | "\(.key)=\(.value)"')
    echo "$input" | env $env_cmd bash "$hook" >/dev/null 2>"$stderr_file" || exit_code=$?
  else
    echo "$input" | bash "$hook" >/dev/null 2>"$stderr_file" || exit_code=$?
  fi

  if [ "$exit_code" -eq 2 ]; then
    actual="block"
  else
    actual="allow"
  fi

  if [ "$actual" != "$expected" ]; then
    printf "  \033[31mFAIL\033[0m  %-30s expected=%s got=%s (exit=%s)\n" "$name" "$expected" "$actual" "$exit_code"
    FAILED=$((FAILED + 1))
    rm -f "$stderr_file"
    return
  fi

  if [ "$actual" = "block" ] && ! grep -q 'mcp__anti-tangent__bulk_read' "$stderr_file"; then
    printf "  \033[31mFAIL\033[0m  %-30s block message missing mcp__anti-tangent__bulk_read\n" "$name"
    FAILED=$((FAILED + 1))
    rm -f "$stderr_file"
    return
  fi

  printf "  \033[32mPASS\033[0m  %-30s %s\n" "$name" "$reason"
  PASSED=$((PASSED + 1))
  rm -f "$stderr_file"
}

run_suite() {
  local hook="$1" evals_file="$2" label="$3"

  setup_fixtures "$evals_file"

  echo ""
  echo "$label"
  echo "────────────────────────────────────────────────────────────────"

  local count
  count=$(jq '.evals | length' "$evals_file")

  for ((i = 0; i < count; i++)); do
    local name expected reason input
    name=$(jq -r ".evals[$i].name" "$evals_file")
    expected=$(jq -r ".evals[$i].expected_decision" "$evals_file")
    reason=$(jq -r ".evals[$i].reason" "$evals_file")
    input=$(jq -c ".evals[$i].input" "$evals_file" | sed "s|{{FIXTURES}}|$FIXTURES|g")

    local env_json
    env_json=$(jq -r ".evals[$i].env // empty" "$evals_file")
    run_eval "$hook" "$name" "$input" "$expected" "$reason" "$env_json"
  done

  rm -rf "$FIXTURES"
}

# check_no_operational_references enforces that no *operational* reference to
# the upstream delegation target survives the port, while allowing the
# attribution that legitimately names it (licence headers, the modification
# notice, README acknowledgement, benchmark comparison). Allowlisted by PATH
# — and, within the two hooks, comment lines only — never by keyword, since a
# keyword filter would let an operational line pass merely by containing a
# word like "upstream".
#
# The two target words are built from concatenated fragments rather than
# spelled out here: this function's own pattern and messages live inside the
# directory it scans, and spelling them out would make this check fail
# against itself on every run — a self-reference bug, not a real leftover.
check_no_operational_references() {
  local w1 w2 pattern repo_root leftovers
  w1="Por""tal"
  w2="Ai""KA"
  pattern="${w1}|${w2}"
  repo_root="$(cd "$PLUGIN_DIR/../.." && pwd)"

  echo ""
  echo "Operational-reference check"
  echo "────────────────────────────────────────────────────────────────"

  leftovers=$(cd "$repo_root" && grep -rinIE "$pattern" plugin/anti-tangent-shunt/ --exclude-dir=.git \
    | grep -vE '^plugin/anti-tangent-shunt/hooks/check-(file-size|bash-read):[0-9]+:[[:space:]]*#' \
    | grep -vE '^plugin/anti-tangent-shunt/(README\.md|evals/benchmarks\.md):' || true)

  if [ -n "$leftovers" ]; then
    printf "  \033[31mFAIL\033[0m  operational %s/%s reference found:\n%s\n" "$w1" "$w2" "$leftovers"
    FAILED=$((FAILED + 1))
    TOTAL=$((TOTAL + 1))
    return
  fi

  printf "  \033[32mPASS\033[0m  clean: no operational %s/%s references\n" "$w1" "$w2"
  PASSED=$((PASSED + 1))
  TOTAL=$((TOTAL + 1))
}

# ── Main ──

run_suite "$SCRIPT_DIR/../hooks/check-file-size" "$SCRIPT_DIR/hook-evals.json" "Read hook (check-file-size)"
run_suite "$SCRIPT_DIR/../hooks/check-bash-read" "$SCRIPT_DIR/bash-hook-evals.json" "Bash hook (check-bash-read)"
check_no_operational_references

echo ""
echo "════════════════════════════════════════════════════════════════"
printf "Total: \033[32m%d passed\033[0m, \033[31m%d failed\033[0m, %d total\n" "$PASSED" "$FAILED" "$TOTAL"
echo ""

[ "$FAILED" -gt 0 ] && exit 1
exit 0
