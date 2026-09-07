#!/usr/bin/env bash
# Eval runner for check-task-complete, the anti-tangent-guard PostToolUse hook.
#
# Reads guard-evals.json (shape: {skill_name, description, evals: [...]}, 21
# cases), builds each case's stdin payload and synthetic transcript, invokes
# the hook, and compares its exit code (and, for a block, its stderr message)
# against what the case expects.
#
# A block case (expected_exit=2) is only counted a pass if BOTH the exit code
# is 2 AND the expected substring appears on stderr — an exit-code-only check
# would also pass against a hook that always exits 2, which asserts nothing.
#
# Usage:
#   bash evals/run.sh
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PLUGIN_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
HOOK="$PLUGIN_DIR/hooks/check-task-complete"
EVALS_FILE="$SCRIPT_DIR/guard-evals.json"

# The eval table this suite implements (see task-12-brief.md Step 4, the two
# tool-scoping cases added for task-12b, the two forged-marker cases added for
# task-12c — see task-12b-review.md Critical #1 — the two ESCAPED cases added
# for task-12d, which run the current server's own rendering through the hook
# rather than a hand-written fixture (task-12c-review.md Critical #1 /
# Important #2), and one direct-call JSON-result case added for the v0.18.0
# final review's Important #1, which pairs a validate_completion tool_use
# with its tool_result exactly as the server's envelopeResult marshals it)
# has exactly 22 rows. Both checks below must hold or the count assertion is
# vacuous: the JSON file must declare 22 cases, AND the loop must actually
# execute 22 of them (a silently-skipped case would satisfy the first check
# alone).
EXPECTED_CASE_COUNT=22

WORKDIR=$(mktemp -d "${TMPDIR:-/tmp}/anti-tangent-guard-evals.XXXXXX")
cleanup() { rm -rf "$WORKDIR"; }
trap cleanup EXIT

PASSED=0
FAILED=0
TOTAL=0

# build_stub_dir EXCLUDE — creates a directory under $WORKDIR containing
# symlinks to every external utility the hook uses (jq, python3, cat, tr,
# mkdir, date, dirname) EXCEPT $EXCLUDE, and prints the directory path. Used
# to test "jq absent" / "python3 absent" without emptying PATH outright — a
# bare PATH= would also break cat/tr/mkdir/date, which the hook needs
# regardless of which dependency is under test, and would prove nothing about
# the dependency check specifically.
build_stub_dir() {
    local exclude="$1"
    local stub
    stub=$(mktemp -d "$WORKDIR/stub.XXXXXX")
    local util
    for util in jq python3 cat tr mkdir date dirname; do
        [[ "$util" == "$exclude" ]] && continue
        local real
        real=$(command -v "$util" 2>/dev/null) || continue
        ln -s "$real" "$stub/$util"
    done
    printf '%s\n' "$stub"
}

# run_case renders one eval object (by index) to a stdin file plus, unless
# stdin_raw or no_transcript is set, a synthetic transcript file with
# {{TRANSCRIPT}} substituted into input.transcript_path, then invokes the
# hook and checks its exit code and (for blocks) stderr message.
run_case() {
    local idx="$1"
    local id name reason expected_exit
    id=$(jq -r ".evals[$idx].id" "$EVALS_FILE")
    name=$(jq -r ".evals[$idx].name" "$EVALS_FILE")
    reason=$(jq -r ".evals[$idx].reason" "$EVALS_FILE")
    expected_exit=$(jq -r ".evals[$idx].expected_exit" "$EVALS_FILE")

    local case_dir stdin_file stderr_file
    case_dir=$(mktemp -d "$WORKDIR/case-$id.XXXXXX")
    stdin_file="$case_dir/stdin.json"
    stderr_file="$case_dir/stderr.txt"

    local stdin_raw
    stdin_raw=$(jq -r ".evals[$idx].stdin_raw // empty" "$EVALS_FILE")

    if [[ -n "$stdin_raw" ]]; then
        printf '%s' "$stdin_raw" > "$stdin_file"
    else
        local no_transcript transcript_path
        no_transcript=$(jq -r ".evals[$idx].no_transcript // false" "$EVALS_FILE")
        if [[ "$no_transcript" == "true" ]]; then
            # A path that is guaranteed not to exist — the hook must treat a
            # missing transcript as fail-open, not touch this directory.
            transcript_path="$case_dir/does-not-exist/transcript.jsonl"
        else
            transcript_path="$case_dir/transcript.jsonl"
            : > "$transcript_path"
            local line_count li
            line_count=$(jq -r ".evals[$idx].transcript_raw_lines | length" "$EVALS_FILE")
            for ((li = 0; li < line_count; li++)); do
                jq -r ".evals[$idx].transcript_raw_lines[$li]" "$EVALS_FILE" >> "$transcript_path"
            done
        fi
        jq -c --arg tp "$transcript_path" '.evals['"$idx"'].input | .transcript_path = $tp' "$EVALS_FILE" > "$stdin_file"
    fi

    # Build the env-var prefix from the case's "env" object, if any.
    local env_json env_assignments=()
    env_json=$(jq -r ".evals[$idx].env // empty" "$EVALS_FILE")
    if [[ -n "$env_json" ]]; then
        local kv
        while IFS= read -r kv; do
            [[ -n "$kv" ]] && env_assignments+=("$kv")
        done < <(jq -r ".evals[$idx].env | to_entries[] | \"\(.key)=\(.value)\"" "$EVALS_FILE")
    fi

    local path_exclude bash_path
    path_exclude=$(jq -r ".evals[$idx].path_stub_exclude // empty" "$EVALS_FILE")
    bash_path=$(command -v bash)

    local exit_code=0
    if [[ -n "$path_exclude" ]]; then
        local stub
        stub=$(build_stub_dir "$path_exclude")
        PATH="$stub" "$bash_path" "$HOOK" < "$stdin_file" > /dev/null 2> "$stderr_file" || exit_code=$?
    elif [[ ${#env_assignments[@]} -gt 0 ]]; then
        env "${env_assignments[@]}" "$bash_path" "$HOOK" < "$stdin_file" > /dev/null 2> "$stderr_file" || exit_code=$?
    else
        "$bash_path" "$HOOK" < "$stdin_file" > /dev/null 2> "$stderr_file" || exit_code=$?
    fi

    TOTAL=$((TOTAL + 1))

    if [[ "$exit_code" != "$expected_exit" ]]; then
        printf '  \033[31mFAIL\033[0m  [%s] %-45s expected exit=%s got=%s\n' "$id" "$name" "$expected_exit" "$exit_code"
        echo "         reason: $reason"
        echo "         stderr:"
        sed 's/^/           /' "$stderr_file"
        FAILED=$((FAILED + 1))
        return
    fi

    if [[ "$expected_exit" == "2" ]]; then
        local expect_count ei missing=0
        expect_count=$(jq -r ".evals[$idx].expected_stderr_contains | length" "$EVALS_FILE")
        for ((ei = 0; ei < expect_count; ei++)); do
            local needle
            needle=$(jq -r ".evals[$idx].expected_stderr_contains[$ei]" "$EVALS_FILE")
            if ! grep -qF -- "$needle" "$stderr_file"; then
                printf '  \033[31mFAIL\033[0m  [%s] %-45s exit code matched, but stderr missing: %s\n' "$id" "$name" "$needle"
                missing=1
            fi
        done
        if [[ "$missing" == "1" ]]; then
            FAILED=$((FAILED + 1))
            return
        fi
    fi

    printf '  \033[32mPASS\033[0m  [%s] %-45s %s\n' "$id" "$name" "$reason"
    PASSED=$((PASSED + 1))
}

# ── Main ──

json_count=$(jq -r '.evals | length' "$EVALS_FILE")
if [[ "$json_count" -ne "$EXPECTED_CASE_COUNT" ]]; then
    echo "guard-evals.json declares $json_count case(s), expected exactly $EXPECTED_CASE_COUNT — update run.sh's EXPECTED_CASE_COUNT if this table grew on purpose."
    exit 1
fi

echo "check-task-complete evals"
echo "────────────────────────────────────────────────────────────────"

for ((i = 0; i < json_count; i++)); do
    run_case "$i"
done

echo ""
echo "════════════════════════════════════════════════════════════════"
printf 'Total: \033[32m%d passed\033[0m, \033[31m%d failed\033[0m, %d total\n' "$PASSED" "$FAILED" "$TOTAL"

if [[ "$TOTAL" -ne "$EXPECTED_CASE_COUNT" ]]; then
    echo "ran $TOTAL case(s), expected exactly $EXPECTED_CASE_COUNT — a case was skipped without failing loudly."
    exit 1
fi

[[ "$FAILED" -gt 0 ]] && exit 1
exit 0
