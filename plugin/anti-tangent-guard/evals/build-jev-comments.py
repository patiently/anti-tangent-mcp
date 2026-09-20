"""Build jev-comments.jsonl with the hook's own block builder.

The fixture is regenerated from git and from the eval corpus on every run
(see State/add() below), never hand-edited: rerunning this script is how the
calibration set tracks the repository and the guard's own eval cases as both
evolve. The only thing that survives a rerun untouched is DECISIONS, which is
this file's own source, not the generated output.
"""
import hashlib
import json
import os
import random
import re
import subprocess
import sys

REPO = subprocess.run(["git", "rev-parse", "--show-toplevel"],
                       capture_output=True, text=True).stdout.strip()
sys.path.insert(0, os.path.join(REPO, "plugin/anti-tangent-guard/hooks"))
import comment_scan  # noqa: E402
import jev_scan  # noqa: E402

CUES = re.compile(r"(?i)\b(previously|no longer|used to|until (task|v)|originally|"
                   r"formerly|was changed|this replaced|now (uses|returns|reads))\b")
RANDOM_SAMPLE = 45
SEED = 20260919

OUT_PATH = os.path.join(REPO, "plugin/anti-tangent-guard/evals/jev-comments.jsonl")
FP_CLASS_PATH = os.path.join(REPO, "plugin/anti-tangent-guard/evals/fp-class.tsv")
GUARD_EVALS_PATH = os.path.join(REPO, "plugin/anti-tangent-guard/evals/guard-evals.json")

# Guard eval cases carry a label written here rather than derived from
# expected_exit: exit 0 means "the hook allowed it", which is equally true of
# genuine history when the tier is off, has no key, or timed out.
GUARD_LABELS = {
    "comment-write-issue-ref-blocks": "history",
    "comment-write-invariant-why-passes": "not_history",
    "jev-off-by-default": "history",
    "jev-needs-a-key": "history",
    "jev-flag-blocks": "history",
    "jev-regex-hit-short-circuits": "history",
    "jev-third-attempt-yields": "history",
    "jev-unanswered-request-allows": "history",
}

# Operator decisions, keyed by row id. A judgement-call row's label is not
# something this builder can derive: a human read the comment and said what it
# is. Keeping those verdicts here, in the repository, is what makes a rebuild
# reproduce the same fixture instead of silently reverting to a guess -- the
# rows themselves are regenerated from git and from the eval corpus every run,
# so a label written into the output file would not survive.
DECISIONS = {
    "head-history-wording:cmd_anti-tangent-mcp_main.go-46:49726d7d": "history",
    "head-history-wording:gnome-topbar_daemon_internal_config_config.go-66:c5f2f991": "not_history",
    "head-history-wording:gnome-topbar_daemon_internal_mcphttp_client.go-197:266d571a": "not_history",
    "head-history-wording:gnome-topbar_daemon_internal_tray_claude.go-113:cda5ade4": "not_history",
    "head-history-wording:gnome-topbar_daemon_internal_tray_tray.go-374:88b67501": "not_history",
    "head-history-wording:internal_config_config_test.go-407:6e78f64c": "history",
    "head-history-wording:internal_config_config_test.go-426:ae76aad5": "history",
    "head-history-wording:internal_mcpsrv_context_files.go-36:afbf5c0b": "history",
    "head-history-wording:internal_mcpsrv_context_files_test.go-59:c20ca4b8": "history",
    "head-history-wording:internal_mcpsrv_context_files_test.go-104:3ebde6f2": "history",
    "head-history-wording:internal_mcpsrv_file_source_fifo_test.go-103:a34a5cef": "history",
    "head-history-wording:internal_mcpsrv_file_target_unix.go-20:1f7583cb": "not_history",
    "head-history-wording:internal_mcpsrv_handlers.go-432:d73a65fd": "not_history",
    "head-history-wording:internal_mcpsrv_handlers.go-2358:69b87876": "not_history",
    "head-history-wording:internal_mcpsrv_handlers.go-2454:f79631b2": "not_history",
    "head-history-wording:internal_mcpsrv_handlers.go-2757:74ff1d80": "not_history",
    "head-history-wording:internal_mcpsrv_handlers_plan_test.go-601:c2552c40": "history",
    "head-history-wording:internal_mcpsrv_handlers_plan_test.go-2151:57b26f67": "history",
    "head-history-wording:internal_mcpsrv_handlers_plan_test.go-2223:d853e22d": "history",
    "head-history-wording:internal_mcpsrv_handlers_plan_test.go-2397:90e82d3f": "history",
    "head-history-wording:internal_mcpsrv_handlers_plan_test.go-2442:12b5970c": "history",
    "head-history-wording:internal_mcpsrv_handlers_stats_test.go-522:e9ae2206": "history",
    "head-history-wording:internal_mcpsrv_handlers_task_align_test.go-16:b364ef7e": "not_history",
    "head-history-wording:internal_mcpsrv_handlers_test.go-806:5add13e3": "history",
    "head-history-wording:internal_mcpsrv_handlers_test.go-1330:873e875f": "history",
    "head-history-wording:internal_mcpsrv_handlers_test.go-1701:cd01489d": "history",
    "head-history-wording:internal_mcpsrv_handlers_test.go-1973:fb2822fb": "history",
    "head-history-wording:internal_mcpsrv_handlers_test.go-2793:a2a29822": "history",
    "head-history-wording:internal_mcpsrv_handlers_test.go-3556:3bc5f5ec": "history",
    "head-history-wording:internal_mcpsrv_review_error.go-34:ce588ea1": "not_history",
    "head-history-wording:internal_mcpsrv_review_error.go-63:f1656afe": "not_history",
    "head-history-wording:internal_mcpsrv_review_error.go-211:4c073dc8": "not_history",
    "head-history-wording:internal_mcpsrv_review_error_test.go-117:7abe4dbe": "history",
    "head-history-wording:internal_mcpsrv_summary.go-279:fdd9889b": "history",
    "head-history-wording:internal_mcpsrv_summary_forgery_test.go-524:c44940c9": "not_history",
    "head-history-wording:internal_mcpsrv_task_spec_input.go-132:81bb31f7": "not_history",
    "head-history-wording:internal_mcpsrv_worker_handlers_test.go-181:5f2af21e": "history",
    "head-history-wording:internal_mcpsrv_worker_handlers_test.go-241:8a7d04c0": "history",
    "head-history-wording:internal_planparser_planparser_test.go-108:cc189abc": "history",
    "head-history-wording:internal_prompts_prompts.go-680:67c31a5f": "history",
    "head-history-wording:internal_prompts_prompts_test.go-1292:bb76a573": "history",
    "head-history-wording:internal_prompts_prompts_test.go-1463:d66f90e0": "history",
    "head-history-wording:internal_stalecomments_stalecomments.go-67:508c9c7d": "not_history",
    "head-history-wording:internal_verdict_plan_test.go-86:a4c6fbc6": "history",
    "head-history-wording:internal_verdict_plan_test.go-829:9b7127ad": "history",
    "head-history-wording:internal_verdict_verdict.go-2:f42d5934": "not_history",
    "head-history-wording:plugin_anti-tangent-guard_evals_run.sh-286:346bfcad": "not_history",
    "head-history-wording:plugin_anti-tangent-guard_hooks_comment_scan.py-519:9679ed24": "not_history",
    "head-history-wording:plugin_anti-tangent-shunt_evals_check-benchmark-table.sh-19:4791ba12": "history",
    "head-history-wording:internal_mcpsrv_summary_contract_test.go-205:c0b49b84": "history",
    "head-history-wording:internal_mcpsrv_summary_forgery_test.go-58:f36f96ae": "history",
}

# Paired fixtures: the same comment as history and as its rewrite, read from
# two commits either side of a cleanup. (commit, path): "commit^" is the
# before state (label history), "commit" is the after state (label
# not_history). The path is the one each commit actually touches, verified
# against this repository's own history rather than assumed.
#
# Three of these four legitimately contribute no rows, which is correct
# rather than a bug, each for its own reason: a174d25's rewrite lives inside
# a Python docstring, and jev_scan.build_blocks() only ever windows
# `#`/`//`/`/*`/`*` comment syntax, never docstring prose; 1e9da90's file has
# no extension, so comment_scan.openers() falls back to the C-family markers
# and never recognises the file's actual `#` comments; 53709c6's rewrite is a
# `#`-commented code excerpt quoted inside a .md file, and .md is not in
# openers()'s hash-family map either, so it gets the same C-family fallback.
# All three faithfully mirror what the real hook would do with the same
# touched lines -- none of them is scanned by it either -- so
# build_blocks() correctly returns nothing for them.
PAIRS = [("4bb1729", "plugin/anti-tangent-guard/hooks/comment_scan_test.py"),
         ("53709c6", "docs/superpowers/plans/2026-09-11-anti-tangent-v0.21.0.md"),
         ("a174d25", "plugin/anti-tangent-guard/hooks/git_added_lines.py"),
         ("1e9da90", "plugin/anti-tangent-guard/hooks/check-task-complete")]


class State(object):
    """The rows built so far, and the bookkeeping that keeps them unique.

    Held as an explicit object, rather than the module globals a one-shot
    script would use, so a test can build a fresh one per case and call add()
    directly -- the duplicate-block and undecided-row invariants below need no
    git, no network, and no full corpus walk to exercise in isolation.
    """

    __slots__ = ("rows", "seen_blocks", "seen_ids", "undecided", "coalesced")

    def __init__(self):
        self.rows = []
        # block text -> (row_id, label, source) of whichever row is on
        # record for it. Checked by check_duplicate() before a caller ever
        # calls add() for a text that might repeat.
        self.seen_blocks = {}
        self.seen_ids = set()
        self.undecided = []
        # (kept_id, kept_source, dropped_id, dropped_source) for every
        # same-label repeat check_duplicate() absorbed -- the coalescing
        # summary main() prints, so a dedupe is never silent.
        self.coalesced = []


def _judgement_call(source, label):
    """Whether this row's label needs a human rather than a rule.

    Two shapes qualify. A `head-history-wording` row was selected BECAUSE it
    carries a cue word, and a cue word is evidence of history, not proof of it.
    An `ambiguous` proposal is the builder saying outright that it could not
    tell. Every other source carries a label that follows from a recorded fact
    -- a `fp-class` verdict column, a reviewed table, which side of a rewrite
    the row came from -- and needs no reading.
    """
    return source == "head-history-wording" or label == "ambiguous"


def _row_id(source, tag, block_text):
    digest = hashlib.sha256(block_text.encode("utf-8")).hexdigest()[:8]
    return "%s:%s:%s" % (source, tag, digest)


def check_duplicate(state, source, label, block_text, tag):
    """Whether a candidate row is new, a harmless repeat, or a contradiction.

    A repeated block text is only dangerous in one way: two independent
    labelling rules landing on the same text can disagree, which would put a
    contradiction in the very fixture meant to settle the right answer (a
    `fp-class` verdict and a cue-word-absence guess CAN disagree; nothing
    stops that by construction, so it must be caught rather than trusted to
    agree by luck). A same-label repeat carries no such risk -- one comment
    reached twice by two routes is still one comment, not two different
    answers for it -- so it is recorded in state.coalesced (for the
    builder's printed summary) and the caller is told to skip rather than
    write a second row for text already on record.

    Returns True when the caller should go on to add() a new row, False when
    it should skip. Raises SystemExit on a label disagreement, naming both
    sides, and never mutates state -- add() is what commits a decision made
    here, in the one place both branches (proceed or skip) end up.
    """
    if not block_text:
        return False
    existing = state.seen_blocks.get(block_text)
    if existing is None:
        return True
    existing_id, existing_label, existing_source = existing
    candidate_id = _row_id(source, tag, block_text)
    if existing_label == label:
        state.coalesced.append((existing_id, existing_source, candidate_id, source))
        return False
    raise SystemExit(
        "duplicate block with conflicting labels: %s (source=%s, label=%s) disagrees with "
        "%s (source=%s, label=%s); the set must not hide one"
        % (candidate_id, source, label, existing_id, existing_source, existing_label))


def route(state, source, label, path, block_text, regex_hit, tag):
    """Decide what happens to one candidate block, and do it -- the one
    place all three source-construction callers share, so none of them
    repeats this logic.

    A judgement-call row (head-history-wording's "history" guess is a
    hypothesis, not evidence -- see _judgement_call) is never compared using
    that raw guess: its DECISIONS entry is resolved FIRST, and only a
    resolved, operator-confirmed label ever reaches check_duplicate(). An
    UNDECIDED judgement-call row is coalesced only against an existing
    UNDECIDED proposal from the SAME source (the one case this is provably
    safe: two cue words in one physical comment run are one occurrence, not
    two); against anything else -- in particular a DIFFERENT, already-
    settled source's verdict for the identical text -- it is left alone and
    still reaches add(), so the operator sees it rather than an unconfirmed
    guess silently agreeing with a verdict it was never actually compared
    to.
    """
    if not block_text:
        return
    row_id = _row_id(source, tag, block_text)
    if _judgement_call(source, label):
        decided = DECISIONS.get(row_id)
        if decided is None:
            existing = state.seen_blocks.get(block_text)
            if existing is not None and existing[2] == source:
                state.coalesced.append((existing[0], existing[2], row_id, source))
                return
            add(state, source, label, path, block_text, regex_hit, tag)
            return
        label = decided
    if not check_duplicate(state, source, label, block_text, tag):
        return
    add(state, source, label, path, block_text, regex_hit, tag)


def add(state, source, label, path, block_text, regex_hit, tag):
    """One fixture row. The caller has already decided this text is new (see
    check_duplicate); add() does not re-derive that decision, only commits
    the row and registers its identity so a LATER check_duplicate() call can
    find it.

    regex_hit is a plain bool the caller already computed against the real
    source lines with their delimiters -- see the module docstring on
    add_from_file for why that path must not be the same string stored below
    as the row's own `path` field.
    """
    if not block_text:
        return
    row_id = _row_id(source, tag, block_text)
    if row_id in state.seen_ids:
        raise SystemExit("duplicate row id: %s" % row_id)
    state.seen_ids.add(row_id)
    state.seen_blocks[block_text] = (row_id, label, source)
    judged_by = "derived"
    if _judgement_call(source, label):
        if row_id not in DECISIONS:
            state.undecided.append((row_id, block_text))
            return
        label, judged_by = DECISIONS[row_id], "operator"
    state.rows.append({"id": row_id, "source": source, "path": path, "block": block_text,
                        "label": label, "judged_by": judged_by, "regex_hit": bool(regex_hit)})


def refuse_if_undecided(state):
    """Nothing is written until every judgement call has a recorded verdict.

    Printing the block text with each id is the point: the operator answers by
    reading these, and an id alone would send them back to the source file.
    """
    if not state.undecided:
        return
    for row_id, block_text in state.undecided:
        sys.stderr.write("\n%s\n    %s\n" % (row_id, block_text.replace("\n", "\n    ")))
    raise SystemExit("\n%d row(s) have no operator decision; add them to DECISIONS and re-run"
                      % len(state.undecided))


def add_from_file(state, source, label, path, line_no):
    """Read one line back out of the repository and add its touched block.

    build_blocks windows a run's neighbours regardless of which line was
    touched, so a run with more than one cue word or scanner hit in it (or
    whose text elsewhere matches another touched line by containment)
    resolves to the same block text for every one of those lines -- each one
    is run through route() rather than assumed new.

    regex_hit is computed against `path` -- the bare file path, which is what
    comment_scan.violations() needs to recognise the extension -- separately
    from the compound "path:line" string this row is given to display. Folding
    the line number into the scan path the way the row's display string does
    would leave violations() looking at an extension like ".go:41", which
    matches nothing in SCAN_EXTS and would silently zero out regex_hit for
    every row this function ever adds.
    """
    text = open(os.path.join(REPO, path), errors="replace").read()
    line = text.splitlines()[line_no - 1]
    regex_hit = bool(comment_scan.violations(path, [line], text))
    tag = "%s-%d" % (path.replace("/", "_"), line_no)
    for block in jev_scan.build_blocks(path, [line], text):
        route(state, source, label, "%s:%d" % (path, line_no), block.text, regex_hit, tag)


# This tool's own four files, excluded from the corpus it builds. Once they
# are committed alongside the fixture they produce, git ls-files starts
# returning them too; without this exclusion the corpus would shift the
# moment its own tooling became tracked, purely as a side effect of shipping
# it -- not because anything in the repository this tool actually measures
# changed.
_SELF_FILES = {
    "plugin/anti-tangent-guard/evals/build-jev-comments.py",
    "plugin/anti-tangent-guard/evals/build-jev-comments-test.py",
    "plugin/anti-tangent-guard/evals/jev-eval.py",
    "plugin/anti-tangent-guard/evals/jev-eval-test.py",
}


def _corpus_eligible(path):
    return "/testdata/" not in path and path not in _SELF_FILES


def tracked_files():
    return [f for f in subprocess.run(["git", "ls-files", "*.go", "*.py", "*.sh"],
                                       capture_output=True, text=True, cwd=REPO).stdout.split()
            if _corpus_eligible(f)]


def add_head_history_wording(state, tracked):
    """Comments at HEAD whose wording can narrate history or describe run time."""
    for path in tracked:
        for i, line in enumerate(open(os.path.join(REPO, path), errors="replace"), 1):
            if re.match(r"^\s*(//|#|\*)", line) and CUES.search(line):
                add_from_file(state, "head-history-wording", "history", path, i)


def add_fp_class(state):
    """The repository's own classified scanner hits: path, count, verdict, text."""
    for raw in open(FP_CLASS_PATH):
        if raw.startswith("#") or not raw.strip():
            continue
        path, _count, verdict, text = raw.rstrip("\n").split("\t", 3)
        label = "history" if verdict == "true_positive" else "not_history"
        src = open(os.path.join(REPO, path), errors="replace").read().splitlines()
        idx = next((i for i, l in enumerate(src, 1) if l.strip() == text.strip()), None)
        if idx:
            add_from_file(state, "fp-class", label, path, idx)


def _dirty_lines(path, lines, text):
    """Line indexes (0-based) that sit in a comment run carrying a cue word
    or a regex hit ANYWHERE in the run, not only on that line itself.

    Calling jev_scan.build_blocks() once per candidate line -- the
    authoritative way to learn what a single line's block contains -- costs
    an O(file length) scan per call; over every comment line in the tracked
    corpus that is minutes, not seconds. This gets the same answer from one
    O(file length) pass per FILE instead, by reusing jev_scan's own line-span
    classifier (the same one build_blocks calls) to find run boundaries,
    rather than re-deriving what counts as a run from scratch.
    """
    spans = jev_scan._spans_per_line(path, lines, text)  # noqa: SLF001
    dirty = set()
    i = 0
    while i < len(spans):
        if spans[i] is None:
            i += 1
            continue
        start = i
        while i < len(spans) and spans[i] is not None:
            i += 1
        run_lines = lines[start:i]
        if any(CUES.search(l) or comment_scan.violations(path, [l]) for l in run_lines):
            dirty.update(range(start, i))
    return dirty


def add_head_random(state, tracked):
    """Ordinary comments, sampled reproducibly.

    Walks a fixed random permutation of the eligible pool, rather than
    drawing a plain k-sample, so a candidate whose block turns out to be
    ambiguous (see below) can be passed over in favour of the next one while
    staying fully reproducible under SEED.

    Two checks keep a "not_history" row honest about what it actually
    scores. First, the run a candidate's line belongs to must itself be
    clean, not just the line (_dirty_lines) -- build_blocks always windows
    the whole run, so a line with no cue word or hit of its own can still sit
    in a run whose OTHER lines carry one. Second, the candidate's line must
    resolve to EXACTLY ONE block: _touched_indexes matches a touched fragment
    by exact text AND by containment anywhere else in the file, so a short or
    repeated line (a bare "//" separator is the common case) also touches
    every OTHER, unrelated run sharing or containing that text. That is the
    right call for the hook, whose Edit operand really can be ambiguous about
    which occurrence was written, but not for a builder sampling ONE specific
    occurrence: it would pull in unrelated, possibly-flagged blocks and label
    them not_history purely because the one line drawn from the pool was
    clean on its own.
    """
    random.seed(SEED)
    pool = []
    for path in tracked:
        text = open(os.path.join(REPO, path), errors="replace").read()
        lines = text.splitlines()
        dirty = _dirty_lines(path, lines, text)
        for i, line in enumerate(lines, 1):
            if i - 1 in dirty:
                continue
            if re.match(r"^\s*(//|#[^!]|\*)", line) and not CUES.search(line) \
                    and not comment_scan.violations(path, [line]):
                pool.append((path, i))
    added = 0
    for path, i in random.sample(pool, len(pool)):
        if added >= RANDOM_SAMPLE:
            break
        text = open(os.path.join(REPO, path), errors="replace").read()
        line = text.splitlines()[i - 1]
        if len(jev_scan.build_blocks(path, [line], text)) != 1:
            continue
        before = len(state.rows)
        add_from_file(state, "head-random", "not_history", path, i)
        # add_from_file can still drop this candidate silently (its block
        # already belongs to another source, or to an earlier candidate in
        # this same walk) -- only a genuine addition counts against the
        # budget.
        added += len(state.rows) - before


def add_guard_evals(state):
    """The guard's own eval cases, labelled from GUARD_LABELS.

    Two hand-written cases can carry byte-identical Write content by
    construction (comment-write-issue-ref-blocks and
    jev-regex-hit-short-circuits both use "fixes #58", to test different
    mechanics of the same underlying tell); route() coalesces the second one
    into the first since both are labelled the same way -- one distinct
    comment reached from two fixtures, not two different answers for it.
    """
    cases = json.load(open(GUARD_EVALS_PATH))
    cases = cases if isinstance(cases, list) else next(v for v in cases.values() if isinstance(v, list))
    for case in cases:
        label = GUARD_LABELS.get(case.get("name"))
        if not label or not case.get("stdin_raw"):
            continue
        payload = json.loads(case["stdin_raw"])
        inp = payload.get("tool_input") or {}
        content = inp.get("content") or inp.get("new_string") or ""
        path = (inp.get("file_path") or "x.go").replace("{{TMPDIR}}", "/tmp")
        lines = content.splitlines()
        regex_hit = bool(comment_scan.violations(path, lines, content))
        tag = str(case["id"])
        for block in jev_scan.build_blocks(path, lines, content):
            route(state, "guard-evals", label, path, block.text, regex_hit, tag)


def _add_cleanup_side(state, commit, path, changed, rev, label, sign):
    """One side (before or after) of one cleanup commit."""
    text = subprocess.run(["git", "show", "%s:%s" % (rev, path)],
                           capture_output=True, text=True, cwd=REPO).stdout
    if not text:
        return
    touched = [l[1:] for l in changed.splitlines()
               if l.startswith(sign) and not l.startswith(sign * 3)]
    regex_hit = bool(comment_scan.violations(path, touched, text))
    for n, block in enumerate(jev_scan.build_blocks(path, touched, text)):
        tag = "%s-%s-%d" % (commit[:7], label, n)
        route(state, "cleanup-commit-pair", label, "%s:%s" % (rev, path), block.text, regex_hit, tag)


def add_cleanup_pairs(state):
    """Both sides of a cleanup commit, read from git since only one exists on disk."""
    for commit, path in PAIRS:
        changed = subprocess.run(["git", "show", "--unified=0", "--format=", commit, "--", path],
                                  capture_output=True, text=True, cwd=REPO).stdout
        for rev, label, sign in ((commit + "^", "history", "-"), (commit, "not_history", "+")):
            _add_cleanup_side(state, commit, path, changed, rev, label, sign)


def build_rows():
    """Run every source over the repository and return a populated State.

    Deterministic given the repository's current commit and working tree:
    the same tracked files and the same eval corpus reproduce the same rows
    in the same order, which is what lets a rebuild be diffed byte-for-byte
    against the committed fixture.
    """
    state = State()
    tracked = tracked_files()
    # The four already-decided sources run first, so a cue word that also
    # happens to land on text one of them already classified does not force
    # a duplicate row or an operator verdict on something already settled --
    # see add()'s docstring for how a collision is resolved between them.
    add_fp_class(state)
    add_head_random(state, tracked)
    add_guard_evals(state)
    add_cleanup_pairs(state)
    add_head_history_wording(state, tracked)
    return state


def write_fixture(rows, out_path=OUT_PATH):
    with open(out_path, "w") as fh:
        for row in rows:
            fh.write(json.dumps(row, sort_keys=True) + "\n")


def format_coalesced_summary(coalesced):
    """The coalescing summary text, or "" when nothing was coalesced.

    A silent dedupe is how a set quietly stops covering what people think it
    covers, so every same-text/same-label repeat check_duplicate() absorbed
    is named here, not just counted.
    """
    if not coalesced:
        return ""
    lines = ["%d block(s) coalesced (same text, same label):" % len(coalesced)]
    for kept_id, kept_source, dropped_id, dropped_source in coalesced:
        lines.append("  %s (%s) also reached via %s (%s)" % (kept_id, kept_source, dropped_source, dropped_id))
    return "\n".join(lines)


def main():
    state = build_rows()
    # Before the file is opened, not after: a partial write would leave a
    # fixture that looks committed but is missing exactly the rows a human
    # still owes a verdict on.
    refuse_if_undecided(state)
    write_fixture(state.rows)
    print("%d rows" % len(state.rows))
    for source in sorted({r["source"] for r in state.rows}):
        group = [r for r in state.rows if r["source"] == source]
        print("  %-22s %3d rows, %d history" % (source, len(group),
                                                  sum(r["label"] == "history" for r in group)))
    summary = format_coalesced_summary(state.coalesced)
    if summary:
        print(summary)


if __name__ == "__main__":
    main()
