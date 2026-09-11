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

# The eval table this suite implements. check-task-complete's FIRST TWO block
# conditions (no pass signal anywhere in the window; the last qualifying
# signal's verdict reads fail) are pinned by 22 cases: tool-scoping (a
# check_progress or validate_task_spec block must not satisfy the guard),
# forged-marker resistance (a finding's free-text Evidence containing the
# literal lines "tool: validate_completion" / "verdict: pass" must not be
# read as the block header — both against a hand-written, un-escaped fixture
# and against the current server's actual escaped rendering, the latter
# pinned byte-for-byte by internal/mcpsrv/guard_eval_fixture_test.go so these
# cases track the real formatters rather than a hand-rolled mirror of them),
# and one case pairing a validate_completion tool_use with its tool_result
# exactly as the server's envelopeResult marshals it, exercising the
# direct-call verdict read end-to-end. check-comment-write, the PreToolUse
# comment-hygiene guard, contributes eleven more. check-task-complete's THIRD
# block condition — the submitted diff adds a comment carrying change
# history — is its own comment-hygiene scan: a defence-in-depth pass over the
# LAST validate_completion call's diff evidence in the task window, catching
# a comment write that reached disk without going through Edit/Write. It
# contributes twelve cases of its own: last-call selection, an absolute
# final_diff_path, a relative path failing open, the size cap failing open,
# the kill switch, the trace() reason, a final_files-only close passing
# untouched, an excluded extension, an unchanged context line, and an
# unresolvable plugin root failing open on the scan without masking an
# independently-detected failing verdict.
#
# A zero-false-positive pass over every tracked source file's own comments
# contributes seven cases pinning the tells' precise boundaries — four pin
# shapes that must NOT block (an all-numeric colour literal, an ordinal in
# prose, a compound word, a URL path segment), two confirm a genuine
# reference still blocks, and one pins that a version number naming a
# wire-compatibility contract — what shape of data the code reads now, not
# when it changed — does not block. One further case pins that a broken
# plugin root fails open on the exit code without masking an independently-
# detected failing verdict, with a trace-log reason distinct from "scanned
# and found nothing" so the two can never be confused.
#
# The `#\d+` and version tells are grammar-based rather than proximity-based,
# and the suite pins the boundary this buys. A trigger word merely NEAR the
# digits — "see"/"issue"/"bug"/"reference" anywhere within a short window of
# an unrelated "#N" — is not enough: ordinary prose ("see the #1 best
# practice guide") puts those words near a "#N" that names nothing. The tell
# instead requires the trigger to GOVERN the digits, separated from them by
# nothing but a closed, enumerated bridge set: an optional colon (GitHub's
# "Fixes: #N"), "also" alone ("see also #N"), or an optional "the" leading to
# a REQUIRED connector noun. That "the"-bridge admits only "issue" and "pr" —
# the other candidate connector nouns ("item", "ticket", "bug", "number",
# "no.", "reference") read as ordinary English ("the ticket #4 printer jam",
# "the reference #2 style") indistinguishably from a genuine tracker
# reference by surface form alone, so their "the"-prefixed form is excluded
# entirely and only their bare form (straight after a strong verb, no "the")
# matches. "reference[sd]?" needs its own lookbehind on top of that, since it
# is also a standalone trigger for GitHub's "References #N": the lookbehind
# excludes "the reference #N" without touching a bare "References #N" at a
# clause start.
#
# The version tell mirrors this reasoning: it requires a change verb to
# govern the version on either side, which is why a wire-compatibility
# sentence like "reads the vX.Y.Z output" never matches — no change verb sits
# near the version at all. A bare-parenthesis version shape ("(vX.Y.Z)", the
# version alone inside its own parenthesis with nothing else) is deliberately
# not a tell: a wire-compatibility sentence can parenthesize its version too
# ("backward compatible with (vX.Y.Z)", "matches the wire shape used by the
# daemon (vX.Y.Z)"), and shape alone cannot distinguish that from genuine
# history ("Categories emitted by X (vX.Y.Z).") — the ambiguity is in what
# the surrounding sentence means, not what sits next to the version, so this
# class is deliberately reviewer-led rather than regex-led, the same as any
# version reference with no governing verb and no other regex-extractable
# signal.
#
# Twenty-nine cases pin these boundaries: eight hold both directions of the
# grammar-vs-proximity distinction for the issue and version tells (a trigger
# merely near the digits does not block; a trigger governing the digits
# despite a nearby unrelated noun still blocks); five pin GitHub's own
# closing syntax and the bridge words that admit it without reopening
# ordinary prose; two pin that the bare-parenthesis version shape does not
# block either way — the version alone in its own parenthesis, and a version
# merely somewhere inside a larger parenthetical remark, both fall through to
# reviewer-led judgment; one pins the trace session-identifier column; eight
# confirm the excluded shapes stay excluded — four for the "the"+connector-
# noun forms this suite does not treat as a tell, four for the wire-
# compatibility parenthetical forms; and two pin trace/session-id hardening
# (a malformed rotation cap still blocks rather than aborting the script, and
# a newline embedded in a session id still lands as exactly one physical
# trace line). A final three cases pin "pr" and "pull request" as triggers
# and the bare (no "the") form of the six narrowly-bridged connectors, each
# verified by mutation — the case stops matching once its named trigger is
# actually removed from the pattern, not merely alongside something else
# that happens to also catch the line; "pr" is also a pre-existing
# standalone trigger, so its case cannot isolate the bridge mechanism from
# the standalone one in a single test string and is documented as such
# rather than claimed to test what it does not.
#
# Two more cases pin the on-touch boundary for a moved or reindented existing
# comment: byte-identical content that only relocates within the diff does
# not block, while a comment line whose bytes change — even only by
# reindentation — is treated as added and does block, since changing a
# line's bytes is touching it.
#
# Three final cases pin the hooks against their own hostile environment: a
# Write whose payload exceeds the kernel's cap on a single environment string
# must still block (the payload reaches the Python body on stdin, so it never
# has to fit in the environment at all); a final_diff_path that is a symlink
# must be refused rather than followed, which shows up as the close passing
# where the identical content reached as a regular file blocks; and a
# repo-root json.py must not shadow the standard library inside the
# interpreter that does the analysis, which would fail open and silently
# disable the gate for every close made from that directory.
#
# Six cases cover ground the table above left open. Two pin, one per hook,
# that a symlink planted at the trace-log path is not written through: both
# use a DANGLING link, so following it would create the target, and the
# target's continued absence is the assertion — which is why
# expected_file_absent exists at all, a presence check being unable to state
# it. Two more exercise the write-time hook's existing-file branch, which
# every other Write case misses by targeting a path that does not exist: a
# Write re-stating a violating comment already on disk must not block (the
# occurrence-aware diff consumes it), while one adding a new violation to an
# existing file must. One pins that the close-time trace sink strips the
# field separators out of every argument, so a task id carrying a newline
# cannot forge a second, complete-looking log record. The last is the
# write-time counterpart of the final_diff_path symlink refusal: a Write whose
# target is a symlink is not followed, and traces as an unreadable target
# rather than a pass, so a write the guard never scanned cannot be read out of
# the log as a scan that ran and found nothing. Only the directory and symlink
# shapes of an unreadable target are pinned here; the FIFO and oversized-file
# shapes share the same code path but have no case of their own.
#
# Three cases pin what the close-time scan looked at, which no exit code can
# state. One submits a diff carrying NO path prefix at all — the shape
# diff.noprefix produces, and the one every other final_diff case here is
# blind to, since they all use "+++ b/" — and must still block. The second
# closes on final_files whose paths are all committed: it passes, and the
# trace line must show submitted>0 with scanned=0, which is what separates
# "there was nothing to scan" from "the scan never ran". The third is a
# window holding no validate_completion call at all, whose pass signal is a
# block in the agent's own report text: the gate reads that block, the close
# passes with called=false on the trace, and the scan has nothing to run on,
# because what it reads is a call's submitted evidence. No scan line is
# traced, which no assertion here can state directly — what the case pins is
# that the close still passes, so refusing a marker-only close fails loudly
# here instead of quietly ending the reporting path the marker exists for.
#
# Thirty-five cases cover the write-time scanner's span boundaries, the
# optional ticket pattern at both hooks, the two kill switches, and the close
# hook's final_files fallback — four groups.
#
# Seventeen pin where a comment span starts and stops, which is what decides
# whether a tell is even reached. Three confirm the ordinary comment shapes
# ARE scanned and do block on a real tell (a KDoc block interior, a one-line
# block comment, a trailing comment on a code line). Seven hold the opposite
# boundary, where something that merely looks like a delimiter is not one: a
# "*" not followed by whitespace is a dereference, a tell inside a string
# literal is not comment text, a "#" inside a shell string and a "#" straight
# after a "$" are not comment openers, "*/" is a terminator and never an
# opener, and block text stops at its terminator — pinned once for a block
# that opens its line and once for a block that starts mid-line — so the code
# after it never reaches the tells. Two more pin that the walk covers a whole
# line rather than stopping at the first span it finds: a benign trailing
# comment cannot hide a violating one earlier on the line, and a line that
# OPENS with a clean block comment must still be walked past the terminator
# to the trailing violation. The last
# four are one apiece: tells run per span, so two harmless fragments never
# join into one; quote parity is counted per segment, so a block comment's
# own quote cannot leak into the parity that decides whether a later "//"
# sits inside a string; plain narrative prose inside a block comment is read
# and simply found clean rather than skipped unscanned; and a "#" at column
# zero, which the mid-line delimiter walk cannot see, still blocks. An
# unstarred block interior is a documented MISS with a case of its own, so
# the gap is pinned rather than left to be rediscovered.
#
# Six pin the optional project ticket pattern. Five drive the write-time
# hook: a configured pattern is a
# tell, an unset one adds nothing (there is no built-in ticket tell), an
# uncompilable one is dropped without disabling the other tells, one past
# the length cap is dropped — the fixture is an alternation, so a pass is
# evidence the cap fired rather than an accident of the pattern never
# matching — and a catastrophically backtracking one is bounded by the scan
# deadline and fails open instead of hanging the hook. A sixth drives the
# CLOSE-time hook rather than the write-time one: the tell is appended to
# TELLS when comment_scan is imported, so both hooks honour the variable, and
# the other five all target check-comment-write.
#
# Four pin the two kill switches as one truth table: each names its own
# concern and leaves the other armed (completion gate off still scans
# comments, comment scanning off still gates the close), and only both off
# is an early exit.
#
# Eight pin the final_files fallback, the path taken when no diff was
# submitted at all. Three are the git classification itself: an untracked
# file has every line added and blocks, a tracked unmodified file's
# pre-existing comment is not an added line, and a gitignored path — which
# exits from ls-files exactly like an untracked one — is separated from it
# by check-ignore. Two are rooting: a path outside any repository cannot be
# classified and is skipped, and a nested worktree is resolved against its
# own root rather than the hook cwd, pinned by a TRACKED fixture expecting a
# pass so that only the correct rooting passes. Three are precedence: a
# submitted diff wins even when it adds nothing, an explicitly present but
# EMPTY final_diff still wins (presence, not content, is the key), and a
# repository with overridden diff prefixes is handled end to end.
#
# Ten more pin how the close-time scan divides a diff into files, on one
# irreducible ambiguity: an added line whose content starts with "++ " reaches
# the parser as the bytes "+++ ", byte-identical to a file header, and the
# line on its own says nothing about which it is.
# Four are a bare multi-file diff with no "diff --git" or "--- " line to
# close the first hunk, an added line that really does start with "++ ", a
# "+++ /dev/null" deletion header that must not enter the scan as a path, and
# a hunk that delivers fewer lines than it declared, after which the next file
# header must still be read as one. Two hold the opposite edge of
# that last rule, where the "+++ " is the hunk's LAST owed added line and the
# "@@" after it is the next hunk of the SAME file — what git diff -U0 emits
# as a matter of course — in both spellings of the declared count, explicit
# ("+4,2") and omitted ("+4"). A seventh pins the tell that reaches where a
# declared count cannot: a "--- " line immediately above a "+++ " line is a
# header pair whatever the open hunk still owes, so a hunk over-declaring by
# a single line does not swallow the next file's real header.
#
# The last three are the shapes read wrong, each pinned at its actual
# behaviour with expected_exit 0 and a trace assertion, so the boundary is
# asserted rather than rediscovered: a hunk that UNDER-declares its count,
# where a genuine body line is read as a header; a hunk that OVER-declares it
# under a bare header with no "--- " above and no "@@" after, where a genuine
# header is read as body; and ordinary source text carrying a removed line
# starting "-- " immediately above an added line starting "++ ", which trips
# the header-pair tell -- the price of that tell, and the only one of the
# three that misreads a well-formed diff. A real diff OF a diff does not trip
# it; git writes those with four markers. The trace assertion carries these:
# a case expecting exit 0 would otherwise pass against a scan that never ran
# at all. All three pin the SILENT direction, where the invented path has no
# scannable extension.
#
# An eleventh pins the other direction, which is the one that costs a user
# their close: where the path a collision lands on DOES carry a scannable
# extension, the same shapes block instead, naming a file the diff never
# touched. Widening the header-pair tell fails there loudly rather than
# costing closes quietly.
#
# Three final cases pin the two exec paths inside final_files_added_lines
# that a repository's own config can point at an arbitrary command, end to
# end through check-task-complete rather than at the function level: a
# tracked path governed by a gitattributes filter, in both its clean and
# process forms, must be compared without ever running the configured
# filter command; and a blob missing from a partial clone must not send git
# down a lazy fetch that execs the configured transport, even when the
# repository grants its own scheme with protocol.<scheme>.allow. Each arms
# the command to write a sentinel file and asserts the sentinel's continued
# absence, since an exit code alone cannot tell a skipped exec from one that
# ran and still let the close through.
#
# The transport case pins the three mechanisms TOGETHER. Separating them
# means running git with one of them removed from the environment, which a
# case here cannot express, because the walk builds that environment itself
# rather than inheriting it; comment_scan_test.py takes them apart.
#
# The per-group counts above PARTITION this table: every case belongs to
# exactly one group, and they sum to EXPECTED_CASE_COUNT. Adding a case means
# growing the group that describes it, or writing a new group; a breakdown
# that no longer sums is a breakdown nobody can use to find anything.
#
# Both checks below must hold or the count assertion is vacuous: the JSON
# file must declare EXPECTED_CASE_COUNT cases, AND the loop must actually
# execute that many (a silently-skipped case would satisfy the first check
# alone).
EXPECTED_CASE_COUNT=145

WORKDIR=$(mktemp -d "${TMPDIR:-/tmp}/anti-tangent-guard-evals.XXXXXX")
# Both hooks default their trace log to a fixed shared path under /tmp, and
# rotate it by rename once it passes a size cap. Left unset, a run of this
# suite would append 80-odd lines to whatever real session log is living
# there and could rotate it away entirely. Point every case at a run-scoped
# file instead; a case that sets ANTI_TANGENT_GUARD_TRACE_LOG in its own "env"
# block still wins, since `env` applies after this export.
export ANTI_TANGENT_GUARD_TRACE_LOG="$WORKDIR/trace.log"
# The suite must not inherit ambient values of the variables its own cases
# exercise: a case that sets no "env" block would otherwise test the
# invoking developer's shell rather than the behaviour it names. A case that
# sets one of these in its own "env" block still wins, since `env` applies
# after this unset.
unset ANTI_TANGENT_TICKET_PATTERN
unset ANTI_TANGENT_COMPLETION_GUARD
unset ANTI_TANGENT_COMMENT_GUARD
# Every hook invocation below runs with this as its cwd, run-scoped (inside
# WORKDIR, so isolated from a concurrent run.sh invocation) rather than
# per-case, so a "cwd_fixture" case can place a real file at a RELATIVE path
# and have a relative-path assertion (e.g. that final_diff_path rejects a
# relative path outright) exercise a path that genuinely resolves, instead of
# a relative path that merely doesn't exist anywhere.
HOOK_CWD="$WORKDIR/hook-cwd"
mkdir -p "$HOOK_CWD"
cleanup() {
    rm -rf "$WORKDIR"
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
# exercise. That component cannot be part of the mktemp template: BSD/macOS
# mktemp requires the run of X's to END the template and rejects anything
# after it, so uniqueness comes from an mktemp'd parent and the ".go"
# directory is created with a fixed name inside it. The pair lives under
# WORKDIR, not $TMPDIR, because this function returns early on several
# failure paths: only the module-level `trap cleanup EXIT` is guaranteed to
# run, and a single `rm -rf "$WORKDIR"` there reclaims every case's
# directory including those of an interrupted run.
run_case() {
    local idx="$1"
    local id name reason expected_exit hook_name case_hook
    id=$(jq -r ".evals[$idx].id" "$EVALS_FILE")
    name=$(jq -r ".evals[$idx].name" "$EVALS_FILE")
    reason=$(jq -r ".evals[$idx].reason" "$EVALS_FILE")
    expected_exit=$(jq -r ".evals[$idx].expected_exit" "$EVALS_FILE")
    hook_name=$(jq -r ".evals[$idx].hook // \"check-task-complete\"" "$EVALS_FILE")
    case_hook="$HOOK_DIR/$hook_name"

    local case_tmp case_parent
    case_parent=$(mktemp -d "$WORKDIR/atg-eval-XXXXXX")
    # Checked rather than assumed: this script runs without `set -e`, so a
    # failed mktemp would otherwise leave case_parent empty and every
    # {{TMPDIR}} substitution below would silently resolve against "/target.go".
    if [[ ! -d "$case_parent" ]]; then
        echo "mktemp -d failed under $WORKDIR — case $id cannot be given an isolated {{TMPDIR}}."
        exit 1
    fi
    case_tmp="$case_parent/target.go"
    if ! mkdir "$case_tmp"; then
        echo "could not create $case_tmp — case $id cannot be given an isolated {{TMPDIR}}."
        exit 1
    fi

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

    # Optional "tmpdir_fixture": {relative_path: content} — the {{TMPDIR}}
    # counterpart to cwd_fixture above: materialises a real file under this
    # case's OWN {{TMPDIR}} (case_tmp), which env/stdin/transcript
    # substitution can then reference by the same {{TMPDIR}} token, rather
    # than under the shared HOOK_CWD. Needed for a case that must prove
    # behaviour conditioned on a target file already existing there before
    # the hook runs — a code path guarded by "only act if the file is
    # already present" is untested by a target that starts out absent.
    local has_tmpdir_fixture
    has_tmpdir_fixture=$(jq -r ".evals[$idx] | has(\"tmpdir_fixture\")" "$EVALS_FILE")
    if [[ "$has_tmpdir_fixture" == "true" ]]; then
        local tfixture_rel
        while IFS= read -r tfixture_rel; do
            [[ -n "$tfixture_rel" ]] || continue
            local tfixture_dest="$case_tmp/$tfixture_rel"
            mkdir -p "$(dirname "$tfixture_dest")"
            jq -r --arg k "$tfixture_rel" ".evals[$idx].tmpdir_fixture[\$k]" "$EVALS_FILE" > "$tfixture_dest"
        done < <(jq -r ".evals[$idx].tmpdir_fixture | keys[]" "$EVALS_FILE")
    fi

    # Optional "tmpdir_symlink": {link_relative_path: target} — creates a real
    # symlink under this case's {{TMPDIR}}. Needed to exercise the hooks' refusal
    # to open a caller-supplied path through a symlink, which cannot be asserted
    # against a fixture the harness only ever materialises as a regular file.
    local has_tmpdir_symlink
    has_tmpdir_symlink=$(jq -r ".evals[$idx] | has(\"tmpdir_symlink\")" "$EVALS_FILE")
    if [[ "$has_tmpdir_symlink" == "true" ]]; then
        local link_rel
        while IFS= read -r link_rel; do
            [[ -n "$link_rel" ]] || continue
            local link_target
            link_target=$(jq -r --arg k "$link_rel" ".evals[$idx].tmpdir_symlink[\$k]" "$EVALS_FILE")
            link_target="${link_target//\{\{TMPDIR\}\}/$case_tmp}"
            link_target="${link_target//\{\{DIFFFILE\}\}/$difffile_path}"
            mkdir -p "$(dirname "$case_tmp/$link_rel")"
            ln -sfn "$link_target" "$case_tmp/$link_rel"
        done < <(jq -r ".evals[$idx].tmpdir_symlink | keys[]" "$EVALS_FILE")
    fi

    # Optional "setup_script": a bash snippet run in this case's {{TMPDIR}}
    # before the hook. The fixture hooks above can only place file CONTENT;
    # a case asserting behaviour that depends on git's own view of a path
    # (tracked, untracked, ignored, in a worktree) needs real repository
    # state, which only running git can produce. Failures are fatal to the
    # case rather than silent: a case whose setup did not run would assert
    # against the wrong world and pass for the wrong reason.

    local setup_script
    setup_script=$(jq -r ".evals[$idx].setup_script // empty" "$EVALS_FILE")
    if [[ -n "$setup_script" ]]; then
        setup_script="${setup_script//\{\{TMPDIR\}\}/$case_tmp}"
        if ! ( cd "$case_tmp" && bash -c "$setup_script" ) >/dev/null 2>&1; then
            echo "  SETUP FAILED for case $id" >&2
            return 1
        fi
    fi

    # Optional "hook_cwd": run the hook from here instead of HOOK_CWD. The
    # worktree case turns on the hook's cwd differing from the file's own
    # directory, which is the whole point of rooting git at the file.
    #
    # Resolved AFTER setup_script, never before: the directory a case names
    # here is usually one its own setup just created, so validating first
    # would reject every such case before it could exist.
    local hook_cwd_override case_cwd
    hook_cwd_override=$(jq -r ".evals[$idx].hook_cwd // empty" "$EVALS_FILE")
    hook_cwd_override="${hook_cwd_override//\{\{TMPDIR\}\}/$case_tmp}"
    case_cwd="$HOOK_CWD"
    if [[ -n "$hook_cwd_override" ]]; then
        # A named cwd that does not exist must fail loudly. Falling back to
        # HOOK_CWD would silently assert the opposite of the case's intent.
        [[ -d "$hook_cwd_override" ]] || { echo "  [$id] BAD hook_cwd: $hook_cwd_override" >&2; return 1; }
        case_cwd="$hook_cwd_override"
    fi

    local stdin_raw
    stdin_raw=$(jq -r ".evals[$idx].stdin_raw // empty" "$EVALS_FILE")

    if [[ -n "$stdin_raw" ]]; then
        stdin_raw="${stdin_raw//\{\{TMPDIR\}\}/$case_tmp}"
        stdin_raw="${stdin_raw//\{\{DIFFFILE\}\}/$difffile_path}"
        # Optional "stdin_pad_bytes": expands the literal "{{PAD}}" token into
        # that many filler characters. A case pinning a size limit measured in
        # hundreds of kilobytes (the kernel's cap on one environment string,
        # for instance) would otherwise have to carry that many bytes inline in
        # guard-evals.json, which no reader could diff.
        local pad_bytes
        pad_bytes=$(jq -r ".evals[$idx].stdin_pad_bytes // 0" "$EVALS_FILE")
        if [[ "$pad_bytes" -gt 0 ]]; then
            local pad
            pad=$(head -c "$pad_bytes" /dev/zero | tr '\0' 'x')
            stdin_raw="${stdin_raw//\{\{PAD\}\}/$pad}"
        fi
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

    local path_exclude bash_path timeout_prefix=()
    path_exclude=$(jq -r ".evals[$idx].path_stub_exclude // empty" "$EVALS_FILE")
    bash_path=$(command -v bash)

    # Optional "case_timeout_seconds": wrap the hook in `timeout` so a case
    # whose failure mode is a hang fails as a case instead of stalling the
    # suite. `timeout` returns 124 on expiry, which no hook uses, so a
    # timeout is distinguishable from every real exit status below.
    local case_timeout
    case_timeout=$(jq -r ".evals[$idx].case_timeout_seconds // empty" "$EVALS_FILE")
    if [[ -n "$case_timeout" && "$case_timeout" =~ ^[0-9]+$ ]] && command -v timeout >/dev/null 2>&1; then
        timeout_prefix=(timeout "$case_timeout")
    fi

    local exit_code=0
    if [[ -n "$path_exclude" ]]; then
        local stub
        stub=$(build_stub_dir "$path_exclude")
        ( cd "$case_cwd" && PATH="$stub" "${timeout_prefix[@]}" "$bash_path" "$case_hook" < "$stdin_file" > /dev/null 2> "$stderr_file" ) || exit_code=$?
    elif [[ ${#env_assignments[@]} -gt 0 ]]; then
        ( cd "$case_cwd" && env "${env_assignments[@]}" "${timeout_prefix[@]}" "$bash_path" "$case_hook" < "$stdin_file" > /dev/null 2> "$stderr_file" ) || exit_code=$?
    else
        ( cd "$case_cwd" && "${timeout_prefix[@]}" "$bash_path" "$case_hook" < "$stdin_file" > /dev/null 2> "$stderr_file" ) || exit_code=$?
    fi

    if [[ "$exit_code" == "124" ]]; then
        echo "  TIMED OUT after ${case_timeout}s — the case hung rather than returning" >&2
        return 1
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

    # Optional "expected_file_absent": [paths] — fails if any of them exists
    # after the hook ran. The counterpart to expected_file_contains, which can
    # only assert that something WAS written. "the hook did not write through
    # that symlink" is inexpressible as a presence check: the link's target
    # never being created is the whole assertion, and a hook that followed the
    # link would create it. {{TMPDIR}} is substituted the same way.
    local has_absent
    has_absent=$(jq -r ".evals[$idx] | has(\"expected_file_absent\")" "$EVALS_FILE")
    if [[ "$has_absent" == "true" ]]; then
        local unexpected=0 apath resolved_absent
        while IFS= read -r apath; do
            [[ -n "$apath" ]] || continue
            resolved_absent="${apath//\{\{TMPDIR\}\}/$case_tmp}"
            # -e is false for a dangling symlink, so test both: a link created
            # by tmpdir_symlink must not be mistaken for its target existing.
            if [[ -e "$resolved_absent" || -L "$resolved_absent" ]]; then
                printf '  \033[31mFAIL\033[0m  [%s] %-45s file must not exist: %s\n' "$id" "$name" "$resolved_absent"
                unexpected=1
            fi
        done < <(jq -r ".evals[$idx].expected_file_absent[]" "$EVALS_FILE")
        if [[ "$unexpected" == "1" ]]; then
            FAILED=$((FAILED + 1))
            return
        fi
    fi

    printf '  \033[32mPASS\033[0m  [%s] %-45s %s\n' "$id" "$name" "$reason"
    PASSED=$((PASSED + 1))
}

# ── Main ──

echo "== module tests =="
python3 -B -m unittest discover -s "$HOOK_DIR" -p '*_test.py' -v || exit 1

# check-comment-write carries a copy of comment_scan.py's SCAN_EXTS so it can
# decide "this file is never scanned" without paying for a python3 start. That
# duplication is safe only while the two agree: an extension added to the
# Python set alone would be skipped in bash and never reach the scanner, which
# looks exactly like a clean pass. Compare them here, where a mismatch fails
# the suite instead.
hook_exts=$(sed -n 's/^ATG_SCAN_EXTS="\(.*\)"$/\1/p' "$HOOK_DIR/check-comment-write" \
    | tr ' ' '\n' | sed '/^$/d' | sort -u)
py_exts=$(sed -n '/^SCAN_EXTS = {/,/^}/p' "$HOOK_DIR/comment_scan.py" \
    | grep -oE '"\.[a-z0-9_]+"' | tr -d '"' | sort -u)
if [[ -z "$hook_exts" || -z "$py_exts" ]]; then
    echo "could not read the extension list out of check-comment-write or comment_scan.py — the drift check below would pass vacuously."
    exit 1
fi
if [[ "$hook_exts" != "$py_exts" ]]; then
    echo "check-comment-write's ATG_SCAN_EXTS and comment_scan.py's SCAN_EXTS disagree:"
    diff <(printf '%s\n' "$py_exts") <(printf '%s\n' "$hook_exts") | sed 's/^/  /'
    echo "an extension in only one of them is silently unguarded — update both."
    exit 1
fi

json_count=$(jq -r '.evals | length' "$EVALS_FILE")
if [[ "$json_count" -ne "$EXPECTED_CASE_COUNT" ]]; then
    echo "guard-evals.json declares $json_count case(s), expected exactly $EXPECTED_CASE_COUNT — update run.sh's EXPECTED_CASE_COUNT if this table grew on purpose."
    exit 1
fi

echo "anti-tangent-guard hook evals (check-task-complete + check-comment-write)"
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
