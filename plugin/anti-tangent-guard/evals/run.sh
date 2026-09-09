#!/usr/bin/env bash
# Eval runner for the anti-tangent-guard hooks: check-task-complete
# (PostToolUse) and check-comment-write (PreToolUse).
#
# Reads guard-evals.json (shape: {skill_name, description, evals: [...]}),
# builds each case's stdin payload — and, unless the case supplies its own
# raw stdin, a synthetic transcript — invokes the case's hook, and compares
# its exit code (and, for a block, its stderr message) against what the case
# expects.
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
HOOK_DIR="$PLUGIN_DIR/hooks"
EVALS_FILE="$SCRIPT_DIR/guard-evals.json"

# The eval table this suite implements (see task-12-brief.md Step 4, the two
# tool-scoping cases added for task-12b, the two forged-marker cases added for
# task-12c — see task-12b-review.md Critical #1 — the two ESCAPED cases added
# for task-12d, which run the current server's own rendering through the hook
# rather than a hand-written fixture (task-12c-review.md Critical #1 /
# Important #2), and one direct-call JSON-result case added for the v0.18.0
# final review's Important #1, which pairs a validate_completion tool_use
# with its tool_result exactly as the server's envelopeResult marshals it)
# has 22 rows; check-comment-write, the PreToolUse comment-hygiene guard,
# contributes eleven more. check-task-complete's own comment-hygiene scan —
# a defence-in-depth pass over the LAST validate_completion call's diff
# evidence in the task window, covering a comment write that reached disk
# without going through Edit/Write — contributes twelve more still: last-call
# selection, an absolute final_diff_path, a relative path failing open, the
# size cap failing open, the kill switch, the trace() reason, a
# final_files-only close passing untouched, an excluded extension, an
# unchanged context line, and an unresolvable plugin root failing open on the
# scan without masking an independently-detected failing verdict, for a
# subtotal of 45. A zero-false-positive pass over every tracked source file's
# comments contributes seven more: four pin a narrowed tell's now-allowed
# shape (an all-numeric colour literal, an ordinal in prose, a compound word,
# and a URL path segment) at exit 0, two re-confirm that a genuine reference
# still blocks after the same narrowing, and one pins that a version number
# naming a wire-compatibility contract — what shape of data this code reads —
# does not block, for a subtotal of 52. One case more asserts that a broken
# plugin root does not silently swallow a genuine violation: a passing
# verdict over a violating diff still fails open on exit code, but the trace
# log must carry a distinct reason for "could not scan" rather than reading
# identically to a clean scan that found nothing, for a subtotal of 53. A
# follow-up pass on the same zero-false-positive work found the first
# narrowing traded one imprecision for another: the issue-reference tell
# matched on trigger-word PROXIMITY, so an ordinary-English sentence putting
# "see"/"issue"/"bug"/"reference" within a short window of an unrelated "#N"
# still blocked, and the version tell's noun-exclusion list could be defeated
# by an unrelated noun ("server") sitting near a genuine change reference and
# wrongly letting it through. Eight more cases hold both directions of the
# re-fix: five pin that a trigger word merely near the digits, with no direct
# grammatical link, does not block; one pins that the trigger is still
# honored when directly followed by one of a small closed set of connector
# nouns (so "fixes issue #N" still blocks); two pin that a genuine
# added/removed-in-version statement now blocks even with an unrelated noun
# in the same sentence, for a subtotal of 61. A third pass found the
# grammatical-attachment rule for #\d+ had overshot: requiring bare whitespace
# between trigger and digits missed GitHub's own "Fixes: #N" / "References #N"
# syntax, "fixes the issue #N", and "see also #N" — the single most common
# real forms. Five cases pin those now block, holding a closed, enumerated set
# of bridge words (a colon; "also" alone; an optional "the" that may only lead
# to a REQUIRED connector noun, never bare to the digits) rather than a wider
# gap that would reopen the five prose false positives from the prior round.
# A sixth pins that a bare parenthetical version tag ("(vX.Y.Z)", the version
# alone inside its own parenthesis with no governing change verb anywhere in
# the sentence) now blocks too — a shape the verb-governs-only version tell
# had been missing entirely, accounting for the great majority of that tell's
# lost recall in the prior round. A seventh confirms the parenthetical
# addition stays narrow: a version merely somewhere inside a larger
# parenthetical remark, not alone in its own parenthesis, still does not
# block, for a subtotal of 68. One more case pins that a written trace line
# carries the session identifier column that keeps a shared trace log
# attributable when more than one session appends to it, for a subtotal of
# 69. A fourth pass on the #\d+ tell found the "the"-bridge from round three
# had been applied to all eight connector nouns when only "issue" needed it:
# "the item #4 dialog", "the ticket #4 printer jam", "the bug #7 spray
# pattern" are ordinary English no regex can separate from a genuine tracker
# reference by shape alone. Rather than narrow the bridge a third time, it
# was restricted to issue/pr only; the other six connector words keep their
# BARE form (straight after a strong verb, no "the") and lose only the "the"
# form. A companion case closes the same hole for "reference[sd]?"
# specifically, which is also a standalone trigger (for "References #N"):
# "the reference #2 style" no longer blocks via a negative lookbehind that
# refuses it as a trigger when directly preceded by "the ", without touching
# a bare "References #N" at the start of a clause. The bare-parenthesis
# version rule added the round before was found to have the same flaw one
# direction over: "backward compatible with (vX.Y.Z)", "accepts (vX.Y.Z) or
# later payloads", "still reads the older (vX.Y.Z) shape", "matches the wire
# shape used by the daemon (vX.Y.Z)" are wire-compatibility sentences that
# happen to parenthesize their version, indistinguishable by shape from the
# genuine-history shape the rule targeted — so it was REMOVED rather than
# narrowed again, deliberately giving up the recall it had recovered; that
# shape is now reviewer-led, not regex-led, same as any version reference
# with no governing verb and no other regex-extractable signal. Case 67 is
# flipped in place (from blocking to not blocking) to match, rather than
# deleted, since it still documents the shape it once covered. Four cases
# pin the newly-clean #\d+ shapes, four more pin the newly-clean version
# shapes, for an exact total. Both checks below must hold or
# the count assertion is vacuous: the JSON file must declare
# EXPECTED_CASE_COUNT cases, AND the loop must actually execute that many (a
# silently-skipped case would satisfy the first check alone).
EXPECTED_CASE_COUNT=77

WORKDIR=$(mktemp -d "${TMPDIR:-/tmp}/anti-tangent-guard-evals.XXXXXX")
# Every hook invocation below runs with this as its cwd, run-scoped (inside
# WORKDIR, so isolated from a concurrent run.sh invocation) rather than
# per-case, so a "cwd_fixture" case can place a real file at a RELATIVE path
# and have a relative-path assertion (e.g. that final_diff_path rejects a
# relative path outright) exercise a path that genuinely resolves, instead of
# a relative path that merely doesn't exist anywhere.
HOOK_CWD="$WORKDIR/hook-cwd"
mkdir -p "$HOOK_CWD"
CASE_TMPDIRS=()
cleanup() {
    rm -rf "$WORKDIR"
    local d
    for d in "${CASE_TMPDIRS[@]:-}"; do
        [[ -n "$d" ]] && rm -rf "$d"
    done
}
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
# case's hook (default check-task-complete; "hook" field selects another,
# e.g. check-comment-write) and checks its exit code and (for blocks) its
# stderr message.
#
# Every case gets its own {{TMPDIR}} — a fresh, case-scoped directory
# substituted for the literal "{{TMPDIR}}" token wherever it appears in
# stdin_raw, in the rendered input JSON, or in an env value. Several
# check-comment-write cases write or read a target file, and a Write over an
# existing file deliberately depends on what is already on disk, so sharing
# one path across cases would make outcomes depend on execution order and on
# residue an earlier run left behind. The directory carries a trailing
# ".go" component so that a case referencing {{TMPDIR}} bare (rather than a
# path beneath it) still lands on a recognized extension — otherwise the
# unreadable-target case would exit 0 via the unrelated "extension not
# scanned" gate instead of the open()-on-a-directory path it means to
# exercise. Registered in CASE_TMPDIRS (not cleaned up locally) because this
# function returns early on several failure paths, and only the module-level
# `trap cleanup EXIT` is guaranteed to run on all of them.
run_case() {
    local idx="$1"
    local id name reason expected_exit hook_name case_hook
    id=$(jq -r ".evals[$idx].id" "$EVALS_FILE")
    name=$(jq -r ".evals[$idx].name" "$EVALS_FILE")
    reason=$(jq -r ".evals[$idx].reason" "$EVALS_FILE")
    expected_exit=$(jq -r ".evals[$idx].expected_exit" "$EVALS_FILE")
    hook_name=$(jq -r ".evals[$idx].hook // \"check-task-complete\"" "$EVALS_FILE")
    case_hook="$HOOK_DIR/$hook_name"

    local case_tmp
    case_tmp=$(mktemp -d "${TMPDIR:-/tmp}/atg-eval-XXXXXX.go")
    CASE_TMPDIRS+=("$case_tmp")

    # HOOK_CWD is shared across every case (that is the point — a relative
    # path needs a stable cwd to resolve against), so a cwd_fixture file left
    # over from an earlier case would otherwise still be on disk when a later
    # case runs, making that later case's outcome depend on execution order.
    # Reset it before this case gets a chance to populate it again. A glob
    # (HOOK_CWD/*) would silently skip a dotfile; find's -mindepth/-maxdepth
    # walk does not.
    find "${HOOK_CWD:?}" -mindepth 1 -maxdepth 1 -exec rm -rf -- {} + 2>/dev/null || true

    local case_dir stdin_file stderr_file
    case_dir=$(mktemp -d "$WORKDIR/case-$id.XXXXXX")
    stdin_file="$case_dir/stdin.json"
    stderr_file="$case_dir/stderr.txt"

    # {{DIFFFILE}} fixture, materialised the same way {{TRANSCRIPT}} already
    # is: a case that declares "diff_file" gets its content written to a real
    # file before stdin/env/transcript substitution runs, so {{DIFFFILE}} can
    # be used anywhere the other tokens are — most usefully inside
    # transcript_raw_lines, where a validate_completion call's
    # final_diff_path lives. An optional "diff_file_min_bytes" pads the file
    # with filler past its literal content, for a case that needs to trip a
    # size cap without inlining megabytes of JSON.
    local difffile_path=""
    local has_diff_file
    has_diff_file=$(jq -r ".evals[$idx] | has(\"diff_file\")" "$EVALS_FILE")
    if [[ "$has_diff_file" == "true" ]]; then
        difffile_path="$case_dir/diff-fixture.diff"
        jq -r ".evals[$idx].diff_file" "$EVALS_FILE" > "$difffile_path"
        local min_bytes cur_bytes
        min_bytes=$(jq -r ".evals[$idx].diff_file_min_bytes // 0" "$EVALS_FILE")
        if [[ "$min_bytes" -gt 0 ]]; then
            cur_bytes=$(wc -c < "$difffile_path")
            if [[ "$cur_bytes" -lt "$min_bytes" ]]; then
                head -c "$((min_bytes - cur_bytes))" /dev/zero | tr '\0' ' ' >> "$difffile_path"
            fi
        fi
    fi

    # Optional "cwd_fixture": {relative_path: content} — materialises a real
    # file under HOOK_CWD (the directory every hook below is invoked from) at
    # each given relative path, so a case asserting behaviour against a
    # RELATIVE path (e.g. that final_diff_path rejects one outright) does so
    # against a path that genuinely resolves to a real file if opened, not
    # merely one that doesn't exist anywhere — otherwise a fail-open assertion
    # would hold whether the relative-path rejection fired or the file was
    # simply never found, and would prove nothing about which.
    local has_cwd_fixture
    has_cwd_fixture=$(jq -r ".evals[$idx] | has(\"cwd_fixture\")" "$EVALS_FILE")
    if [[ "$has_cwd_fixture" == "true" ]]; then
        local fixture_rel
        while IFS= read -r fixture_rel; do
            [[ -n "$fixture_rel" ]] || continue
            local fixture_dest="$HOOK_CWD/$fixture_rel"
            mkdir -p "$(dirname "$fixture_dest")"
            jq -r --arg k "$fixture_rel" ".evals[$idx].cwd_fixture[\$k]" "$EVALS_FILE" > "$fixture_dest"
        done < <(jq -r ".evals[$idx].cwd_fixture | keys[]" "$EVALS_FILE")
    fi

    local stdin_raw
    stdin_raw=$(jq -r ".evals[$idx].stdin_raw // empty" "$EVALS_FILE")

    if [[ -n "$stdin_raw" ]]; then
        stdin_raw="${stdin_raw//\{\{TMPDIR\}\}/$case_tmp}"
        stdin_raw="${stdin_raw//\{\{DIFFFILE\}\}/$difffile_path}"
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
                local raw_line
                raw_line=$(jq -r ".evals[$idx].transcript_raw_lines[$li]" "$EVALS_FILE")
                raw_line="${raw_line//\{\{TMPDIR\}\}/$case_tmp}"
                raw_line="${raw_line//\{\{DIFFFILE\}\}/$difffile_path}"
                printf '%s\n' "$raw_line" >> "$transcript_path"
            done
        fi
        local input
        input=$(jq -c --arg tp "$transcript_path" '.evals['"$idx"'].input | .transcript_path = $tp' "$EVALS_FILE")
        input=$(jq -c --arg d "$case_tmp" --arg df "$difffile_path" \
            'walk(if type == "string" then gsub("\\{\\{TMPDIR\\}\\}"; $d) | gsub("\\{\\{DIFFFILE\\}\\}"; $df) else . end)' \
            <<<"$input")
        printf '%s' "$input" > "$stdin_file"
    fi

    # Build the env-var prefix from the case's "env" object, if any.
    local env_json env_assignments=()
    env_json=$(jq -r ".evals[$idx].env // empty" "$EVALS_FILE")
    if [[ -n "$env_json" ]]; then
        local kv
        while IFS= read -r kv; do
            [[ -n "$kv" ]] || continue
            kv="${kv//\{\{TMPDIR\}\}/$case_tmp}"
            kv="${kv//\{\{DIFFFILE\}\}/$difffile_path}"
            env_assignments+=("$kv")
        done < <(jq -r ".evals[$idx].env | to_entries[] | \"\(.key)=\(.value)\"" "$EVALS_FILE")
    fi

    local path_exclude bash_path
    path_exclude=$(jq -r ".evals[$idx].path_stub_exclude // empty" "$EVALS_FILE")
    bash_path=$(command -v bash)

    local exit_code=0
    if [[ -n "$path_exclude" ]]; then
        local stub
        stub=$(build_stub_dir "$path_exclude")
        ( cd "$HOOK_CWD" && PATH="$stub" "$bash_path" "$case_hook" < "$stdin_file" > /dev/null 2> "$stderr_file" ) || exit_code=$?
    elif [[ ${#env_assignments[@]} -gt 0 ]]; then
        ( cd "$HOOK_CWD" && env "${env_assignments[@]}" "$bash_path" "$case_hook" < "$stdin_file" > /dev/null 2> "$stderr_file" ) || exit_code=$?
    else
        ( cd "$HOOK_CWD" && "$bash_path" "$case_hook" < "$stdin_file" > /dev/null 2> "$stderr_file" ) || exit_code=$?
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

    # Optional "expected_file_contains": {path: [substrings]} — checked after
    # the case has already passed on exit code (and stderr, if a block).
    # Fails if the file is missing, or if any substring is absent, matched as
    # a fixed string (grep -F). {{TMPDIR}} is substituted into the path the
    # same way it is everywhere else a case references its scratch directory.
    local has_files
    has_files=$(jq -r ".evals[$idx] | has(\"expected_file_contains\")" "$EVALS_FILE")
    if [[ "$has_files" == "true" ]]; then
        local file_missing=0
        local fpath
        while IFS= read -r fpath; do
            [[ -n "$fpath" ]] || continue
            local resolved_path
            resolved_path="${fpath//\{\{TMPDIR\}\}/$case_tmp}"
            if [[ ! -f "$resolved_path" ]]; then
                printf '  \033[31mFAIL\033[0m  [%s] %-45s expected file missing: %s\n' "$id" "$name" "$resolved_path"
                file_missing=1
                continue
            fi
            local needle_count ni
            needle_count=$(jq -r --arg k "$fpath" '.evals['"$idx"'].expected_file_contains[$k] | length' "$EVALS_FILE")
            for ((ni = 0; ni < needle_count; ni++)); do
                local file_needle
                file_needle=$(jq -r --arg k "$fpath" '.evals['"$idx"'].expected_file_contains[$k]['"$ni"']' "$EVALS_FILE")
                if ! grep -qF -- "$file_needle" "$resolved_path"; then
                    printf '  \033[31mFAIL\033[0m  [%s] %-45s %s missing substring: %s\n' "$id" "$name" "$resolved_path" "$file_needle"
                    file_missing=1
                fi
            done
        done < <(jq -r ".evals[$idx].expected_file_contains | keys[]" "$EVALS_FILE")
        if [[ "$file_missing" == "1" ]]; then
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
