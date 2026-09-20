"""Offline invariants for build-jev-comments.py.

Exercises add()/route()/check_duplicate()/State/refuse_if_undecided()
directly with synthetic inputs (no git, no corpus walk), plus one
end-to-end rebuild-parity check against the committed fixture. Reads only
this checkout's files and its local git history; never touches the network.
"""
import contextlib
import filecmp
import hashlib
import importlib.util
import io
import os
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))


def _load(name, filename):
    spec = importlib.util.spec_from_file_location(name, os.path.join(HERE, filename))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


# A hyphenated filename cannot be `import`ed by name, so the module under
# test is loaded by path, the same way it must be at commit time when nothing
# on sys.path knows it exists yet.
bjc = _load("build_jev_comments", "build-jev-comments.py")


class AddInvariants(unittest.TestCase):
    """add() takes the "is this text new" decision from its caller (see
    check_duplicate()/route(), covered by CheckDuplicateInvariants and
    RouteInvariants below) and does not re-derive it. Its own guard is
    narrower and independent: a row-id collision, which a caller cannot
    avoid merely by getting the duplicate check right, since id and block
    text are different axes."""

    def test_a_row_id_repeat_raises_even_past_check_duplicate(self):
        # Two rows with distinct block text could in principle still collide
        # on id (a hash collision, or a caller reusing a tag), which nothing
        # upstream of add() guards against; this is add()'s own, independent
        # safety net, simulated here by pre-seeding the id it will compute.
        state = bjc.State()
        forced_id = bjc._row_id("fp-class", "tag1", "second text")  # noqa: SLF001
        state.seen_ids.add(forced_id)
        with self.assertRaises(SystemExit) as ctx:
            bjc.add(state, "fp-class", "history", "a.go:2", "second text", True, "tag1")
        self.assertIn("duplicate row id", str(ctx.exception))

    def test_empty_block_text_is_not_added(self):
        state = bjc.State()
        bjc.add(state, "fp-class", "history", "a.go:1", "", True, "tag1")
        self.assertEqual(state.rows, [])
        self.assertEqual(state.undecided, [])

    def test_row_carries_every_required_field(self):
        state = bjc.State()
        bjc.add(state, "fp-class", "history", "a.go:1", "hello world", True, "mytag")
        row = state.rows[0]
        self.assertEqual(set(row), {"id", "source", "path", "block", "label",
                                     "judged_by", "regex_hit"})
        self.assertEqual(row["source"], "fp-class")
        self.assertEqual(row["path"], "a.go:1")
        self.assertEqual(row["block"], "hello world")
        self.assertEqual(row["label"], "history")
        self.assertEqual(row["judged_by"], "derived")
        self.assertIs(row["regex_hit"], True)

    def test_id_is_source_tag_and_a_hash_of_the_block_text(self):
        state = bjc.State()
        bjc.add(state, "fp-class", "history", "a.go:1", "hello world", True, "mytag")
        source, tag, digest = state.rows[0]["id"].split(":")
        self.assertEqual(source, "fp-class")
        self.assertEqual(tag, "mytag")
        self.assertEqual(digest, hashlib.sha256(b"hello world").hexdigest()[:8])


class CheckDuplicateInvariants(unittest.TestCase):
    """check_duplicate() is the compare route() runs once a candidate's
    label is settled (resolved from DECISIONS, for a judgement-call row): a
    repeated block text is only dangerous when it carries a DIFFERENT label
    than what is already on record -- that is a contradiction in the fixture
    meant to settle the right answer. A same-label repeat is one comment
    reached twice, not two different answers, so it is coalesced rather than
    treated as new evidence."""

    def test_new_text_is_reported_new_and_nothing_is_recorded(self):
        state = bjc.State()
        self.assertTrue(bjc.check_duplicate(state, "fp-class", "history", "new text", "t1"))
        self.assertEqual(state.coalesced, [])

    def test_empty_text_is_never_new(self):
        state = bjc.State()
        self.assertFalse(bjc.check_duplicate(state, "fp-class", "history", "", "t1"))

    def test_same_text_same_label_is_coalesced_not_added_again(self):
        state = bjc.State()
        bjc.add(state, "fp-class", "history", "a.go:1", "shared text", True, "t1")
        proceed = bjc.check_duplicate(state, "head-random", "history", "shared text", "t2")
        self.assertFalse(proceed)
        # a caller that respects the "proceed" answer therefore writes one row
        self.assertEqual(len(state.rows), 1)

    def test_same_text_same_label_is_recorded_for_the_summary(self):
        state = bjc.State()
        bjc.add(state, "fp-class", "history", "a.go:1", "shared text", True, "t1")
        kept_id = state.rows[0]["id"]
        bjc.check_duplicate(state, "head-random", "history", "shared text", "t2")
        self.assertEqual(len(state.coalesced), 1)
        recorded_kept_id, kept_source, dropped_id, dropped_source = state.coalesced[0]
        self.assertEqual(recorded_kept_id, kept_id)
        self.assertEqual(kept_source, "fp-class")
        self.assertEqual(dropped_source, "head-random")
        self.assertEqual(dropped_id, bjc._row_id("head-random", "t2", "shared text"))  # noqa: SLF001

    def test_coalescing_produces_a_summary_line_naming_both_sources(self):
        state = bjc.State()
        bjc.add(state, "fp-class", "history", "a.go:1", "shared text", True, "t1")
        bjc.check_duplicate(state, "head-random", "history", "shared text", "t2")
        summary = bjc.format_coalesced_summary(state.coalesced)
        self.assertIn("1 block(s) coalesced", summary)
        self.assertIn("fp-class", summary)
        self.assertIn("head-random", summary)

    def test_no_coalescing_produces_an_empty_summary(self):
        self.assertEqual(bjc.format_coalesced_summary([]), "")

    def test_same_text_different_label_exits_non_zero_naming_both_sources(self):
        # The one case that matters: two independent labelling rules
        # disagreeing about the same text must never be resolved by picking
        # a winner silently. Written so it fails loudly if this check is
        # ever relaxed back to a same-source-only or an unconditional skip.
        state = bjc.State()
        bjc.add(state, "fp-class", "history", "a.go:1", "shared text", True, "t1")
        with self.assertRaises(SystemExit) as ctx:
            bjc.check_duplicate(state, "head-random", "not_history", "shared text", "t2")
        message = str(ctx.exception)
        self.assertIn("fp-class", message)
        self.assertIn("head-random", message)
        self.assertIn("history", message)
        self.assertIn("not_history", message)
        # the disagreement must not have been silently absorbed
        self.assertEqual(state.coalesced, [])

    def test_a_disagreement_leaves_the_original_row_as_the_only_one_on_record(self):
        state = bjc.State()
        bjc.add(state, "fp-class", "history", "a.go:1", "shared text", True, "t1")
        with self.assertRaises(SystemExit):
            bjc.check_duplicate(state, "head-random", "not_history", "shared text", "t2")
        self.assertEqual(len(state.rows), 1)
        self.assertEqual(state.rows[0]["source"], "fp-class")


class RouteInvariants(unittest.TestCase):
    """route() is what the three source-construction callers actually call.
    Its extra responsibility beyond check_duplicate() is judgement-call
    safety: an UNCONFIRMED guess (head-history-wording always proposes
    "history") must never be compared against a settled verdict as if it
    were a real answer."""

    def test_an_undecided_candidate_matching_a_different_settled_source_is_not_coalesced(self):
        state = bjc.State()
        bjc.add(state, "fp-class", "history", "a.go:1", "shared text", True, "t1")
        bjc.route(state, "head-history-wording", "history", "b.go:2", "shared text", False, "t2")
        # Reaches the operator instead of silently agreeing with fp-class's
        # verdict, which this guess was never actually compared to.
        self.assertEqual(len(state.undecided), 1)
        self.assertEqual(state.coalesced, [])
        self.assertEqual(len(state.rows), 1)  # only the original fp-class row

    def test_two_undecided_candidates_from_the_same_source_still_coalesce(self):
        # The one case route() DOES coalesce pre-decision: two cue words in
        # one physical comment run, found by the SAME source, are one
        # occurrence, not two competing guesses.
        state = bjc.State()
        bjc.route(state, "head-history-wording", "history", "a.go:1", "shared text", False, "t1")
        bjc.route(state, "head-history-wording", "history", "a.go:2", "shared text", False, "t2")
        self.assertEqual(len(state.undecided), 1)
        self.assertEqual(len(state.coalesced), 1)

    def test_a_decided_candidate_agreeing_with_a_settled_source_coalesces(self):
        state = bjc.State()
        bjc.add(state, "fp-class", "history", "a.go:1", "shared text", True, "t1")
        row_id = bjc._row_id("head-history-wording", "t2", "shared text")  # noqa: SLF001
        bjc.DECISIONS[row_id] = "history"
        try:
            bjc.route(state, "head-history-wording", "history", "b.go:2", "shared text", False, "t2")
        finally:
            del bjc.DECISIONS[row_id]
        self.assertEqual(state.undecided, [])
        self.assertEqual(len(state.rows), 1)  # coalesced, no second row
        self.assertEqual(len(state.coalesced), 1)

    def test_a_decided_candidate_disagreeing_with_a_settled_source_raises(self):
        state = bjc.State()
        bjc.add(state, "fp-class", "history", "a.go:1", "shared text", True, "t1")
        row_id = bjc._row_id("head-history-wording", "t2", "shared text")  # noqa: SLF001
        bjc.DECISIONS[row_id] = "not_history"
        try:
            with self.assertRaises(SystemExit) as ctx:
                bjc.route(state, "head-history-wording", "history", "b.go:2", "shared text", False, "t2")
        finally:
            del bjc.DECISIONS[row_id]
        message = str(ctx.exception)
        self.assertIn("fp-class", message)
        self.assertIn("head-history-wording", message)


class JudgementCallInvariants(unittest.TestCase):
    def test_head_history_wording_row_with_no_decision_is_withheld_and_named(self):
        state = bjc.State()
        bjc.add(state, "head-history-wording", "history", "a.go:1", "undecided text", False, "tag1")
        self.assertEqual(state.rows, [])
        self.assertEqual(len(state.undecided), 1)
        row_id, block_text = state.undecided[0]
        self.assertTrue(row_id.startswith("head-history-wording:tag1:"))
        self.assertEqual(block_text, "undecided text")

    def test_ambiguous_row_with_no_decision_is_withheld_regardless_of_source(self):
        state = bjc.State()
        bjc.add(state, "fp-class", "ambiguous", "a.go:1", "unsure text", False, "tag1")
        self.assertEqual(state.rows, [])
        self.assertEqual(len(state.undecided), 1)

    def test_a_derived_source_needs_no_decision(self):
        state = bjc.State()
        bjc.add(state, "fp-class", "history", "a.go:1", "settled text", True, "tag1")
        self.assertEqual(len(state.rows), 1)
        self.assertEqual(state.rows[0]["judged_by"], "derived")
        self.assertEqual(state.undecided, [])

    def test_a_recorded_decision_lands_the_row_stamped_operator(self):
        digest = hashlib.sha256(b"unsure text").hexdigest()[:8]
        row_id = "fp-class:tag1:%s" % digest
        bjc.DECISIONS[row_id] = "not_history"
        try:
            state = bjc.State()
            bjc.add(state, "fp-class", "ambiguous", "a.go:1", "unsure text", False, "tag1")
        finally:
            del bjc.DECISIONS[row_id]
        self.assertEqual(state.undecided, [])
        self.assertEqual(len(state.rows), 1)
        self.assertEqual(state.rows[0]["label"], "not_history")
        self.assertEqual(state.rows[0]["judged_by"], "operator")

    def test_refuse_if_undecided_names_every_id_and_its_block_text(self):
        state = bjc.State()
        bjc.add(state, "head-history-wording", "history", "a.go:1", "text one", False, "tag1")
        bjc.add(state, "head-history-wording", "history", "b.go:2", "text two", False, "tag2")
        stderr = io.StringIO()
        with contextlib.redirect_stderr(stderr), self.assertRaises(SystemExit) as ctx:
            bjc.refuse_if_undecided(state)
        # The count lives in the SystemExit's own message; each id and its
        # block text are printed to stderr as they are found, so the operator
        # sees every row before the process exits, not just how many there are.
        self.assertIn("2 row(s)", str(ctx.exception))
        self.assertEqual(len(state.undecided), 2)
        printed = stderr.getvalue()
        for row_id, block_text in state.undecided:
            self.assertIn(row_id, printed)
            self.assertIn(block_text, printed)

    def test_refuse_if_undecided_is_a_noop_once_nothing_is_undecided(self):
        state = bjc.State()
        bjc.add(state, "fp-class", "history", "a.go:1", "settled text", True, "tag1")
        bjc.refuse_if_undecided(state)  # must not raise


class AddFromFileRegexHit(unittest.TestCase):
    """add_from_file's regex_hit must be computed against the bare file
    path, not the compound "path:line" string the row displays -- an
    extension like ".go:41" matches nothing in comment_scan.SCAN_EXTS and
    would silently record False for every row this function adds."""

    def setUp(self):
        # Seeded straight into the blob cache: the builder reads every
        # harvested file from git at HARVEST_REV, so a file written to disk
        # would never be seen. The path only needs to look like a Go file.
        self.rel_path = "plugin/anti-tangent-guard/evals/testdata/regex-hit-fixture.go"
        bjc._BLOBS[self.rel_path] = b"package x\n\n// fixes #58\nfunc f() {}\n"  # noqa: SLF001

    def tearDown(self):
        bjc._BLOBS.pop(self.rel_path, None)  # noqa: SLF001

    def test_regex_hit_true_for_a_genuine_tell(self):
        state = bjc.State()
        bjc.add_from_file(state, "test-source", "history", self.rel_path, 3)
        self.assertTrue(state.rows)
        self.assertTrue(all(r["regex_hit"] for r in state.rows))

    def test_display_path_is_still_the_compound_form(self):
        state = bjc.State()
        bjc.add_from_file(state, "test-source", "history", self.rel_path, 3)
        self.assertEqual(state.rows[0]["path"], "%s:3" % self.rel_path)

    def test_an_already_seen_same_label_block_is_coalesced_not_re_added(self):
        state = bjc.State()
        existing_id = bjc._row_id("other-source", "other-tag", "fixes " + "#58")  # noqa: SLF001
        state.seen_blocks["fixes " + "#58"] = (existing_id, "history", "other-source")
        bjc.add_from_file(state, "test-source", "history", self.rel_path, 3)
        self.assertEqual(state.rows, [])
        self.assertEqual(len(state.coalesced), 1)


class GuardEvalsAndCleanupPairsDedup(unittest.TestCase):
    """Regression coverage against the real, committed guard-evals.json and
    PAIRS commits -- offline (local files and local git history only)."""

    def test_guard_evals_dedups_two_cases_sharing_identical_content(self):
        # Two hand-written cases carry byte-identical Write content, to
        # exercise two different mechanics against the same underlying
        # tell; add_guard_evals must not raise on the second one, and the
        # corpus must carry that text exactly once.
        state = bjc.State()
        bjc.add_guard_evals(state)
        blocks = [r["block"] for r in state.rows]
        self.assertEqual(len(blocks), len(set(blocks)))
        duplicated_text = "fixes " + "#58"
        self.assertEqual(sum(1 for b in blocks if b == duplicated_text), 1)

    def test_cleanup_pairs_never_raises_on_the_committed_pairs(self):
        state = bjc.State()
        bjc.add_cleanup_pairs(state)  # must not raise
        self.assertTrue(state.rows)  # one pair's corrected path yields real rows


class GitReadsAreChecked(unittest.TestCase):
    """A revision git cannot read stops the build instead of shrinking it.

    Offline: every case here names an object this repository cannot hold,
    or a path a real commit does not touch, and asks local git about it.
    """

    def _with_pairs(self, pairs):
        original = bjc.PAIRS
        bjc.PAIRS = pairs
        self.addCleanup(setattr, bjc, "PAIRS", original)

    def test_every_committed_pair_names_a_full_hash(self):
        for commit, _ in bjc.PAIRS:
            self.assertRegex(commit, r"^[0-9a-f]{40}$")

    def test_a_revision_git_cannot_resolve_raises(self):
        missing = "0" * 40
        self._with_pairs([(missing, "plugin/anti-tangent-guard/hooks/comment_scan_test.py")])
        with self.assertRaises(SystemExit) as ctx:
            bjc.add_cleanup_pairs(bjc.State())
        self.assertIn(missing, str(ctx.exception))

    def test_a_path_the_commit_does_not_touch_raises(self):
        commit, _ = bjc.PAIRS[0]
        self._with_pairs([(commit, "no/such/file.go")])
        with self.assertRaises(SystemExit) as ctx:
            bjc.add_cleanup_pairs(bjc.State())
        self.assertIn("no/such/file.go", str(ctx.exception))

    def test_a_side_absent_at_its_revision_raises(self):
        # A path the commit touches but that does not exist at the parent
        # (a file the commit created) cannot be a before/after pair.
        commit, _ = bjc.PAIRS[0]
        original_show = bjc._git_show  # noqa: SLF001

        def parent_side_missing(args, what):
            if args[0].endswith("^:plugin/anti-tangent-guard/hooks/comment_scan_test.py"):
                args = [args[0].replace("comment_scan_test.py", "never-existed.py")]
            return original_show(args, what)

        bjc._git_show = parent_side_missing  # noqa: SLF001
        self.addCleanup(setattr, bjc, "_git_show", original_show)
        self._with_pairs([(commit, "plugin/anti-tangent-guard/hooks/comment_scan_test.py")])
        with self.assertRaises(SystemExit):
            bjc.add_cleanup_pairs(bjc.State())

    def test_a_vanished_harvest_rev_stops_the_build(self):
        original = bjc.HARVEST_REV
        bjc.HARVEST_REV = "0" * 40
        self.addCleanup(setattr, bjc, "HARVEST_REV", original)
        with self.assertRaises(SystemExit) as ctx:
            bjc.build_rows()
        self.assertIn("0" * 40, str(ctx.exception))


class HarvestReadsThePinnedCommit(unittest.TestCase):
    """Every harvested byte comes from git at HARVEST_REV. What the working
    tree holds at the same path is never consulted, so a later commit that
    edits a harvested file cannot move a single row."""

    def test_a_path_absent_at_the_harvest_rev_reads_as_empty(self):
        self.assertEqual(bjc._blob("no/such/file-anywhere.go"), b"")  # noqa: SLF001

    def test_a_file_that_changed_since_the_harvest_rev_is_read_as_it_was_then(self):
        # The builder's own module is excluded from the corpus, but it is a
        # tracked file that HAS changed since HARVEST_REV -- this very
        # pinning is such a change -- which makes it the one path guaranteed
        # to tell a git read from a working-tree read.
        path = "plugin/anti-tangent-guard/evals/build-jev-comments.py"
        pinned = bjc._blob(path)  # noqa: SLF001
        with open(os.path.join(bjc.REPO, path), "rb") as fh:
            on_disk = fh.read()
        self.assertTrue(pinned, "the pinned read must find the file")
        self.assertNotEqual(pinned, on_disk)
        self.assertNotIn(b"HARVEST_REV", pinned)

    def test_every_tracked_file_exists_at_the_harvest_rev(self):
        tracked = bjc.tracked_files()
        self.assertTrue(tracked)
        for path in tracked:
            self.assertTrue(bjc._blob(path), "%s is listed but empty" % path)  # noqa: SLF001

    def test_open_repo_file_translates_newlines_like_a_disk_open_would(self):
        # Line iteration and text.splitlines() must agree on numbering, and
        # both must see a CRLF file the way open() shows it: one '\n' per
        # line, no '\r' left behind to sit inside a block's text.
        path = "synthetic/crlf.go"
        bjc._BLOBS[path] = b"package x\r\n// one\r\n// two\r\n"  # noqa: SLF001
        try:
            with bjc._open_repo_file(path) as fh:  # noqa: SLF001
                iterated = list(fh)
            with bjc._open_repo_file(path) as fh:  # noqa: SLF001
                split = fh.read().splitlines()
        finally:
            del bjc._BLOBS[path]  # noqa: SLF001
        self.assertEqual(iterated, ["package x\n", "// one\n", "// two\n"])
        self.assertEqual(split, ["package x", "// one", "// two"])


class CorpusEligible(unittest.TestCase):
    def test_this_tools_own_files_are_excluded(self):
        for path in bjc._SELF_FILES:  # noqa: SLF001
            self.assertFalse(bjc._corpus_eligible(path))  # noqa: SLF001

    def test_a_testdata_path_is_excluded(self):
        self.assertFalse(bjc._corpus_eligible(  # noqa: SLF001
            "plugin/anti-tangent-guard/evals/testdata/fixture.go"))

    def test_an_ordinary_tracked_file_is_eligible(self):
        self.assertTrue(bjc._corpus_eligible("internal/mcpsrv/handlers.go"))  # noqa: SLF001


class RebuildParity(unittest.TestCase):
    def test_rebuild_matches_the_committed_fixture_byte_for_byte(self):
        if not os.path.exists(bjc.OUT_PATH):
            self.skipTest("jev-comments.jsonl has not been built yet")
        state = bjc.build_rows()
        bjc.refuse_if_undecided(state)
        fd, rebuilt_path = tempfile.mkstemp(suffix=".jsonl")
        os.close(fd)
        try:
            bjc.write_fixture(state.rows, rebuilt_path)
            self.assertTrue(filecmp.cmp(bjc.OUT_PATH, rebuilt_path, shallow=False))
        finally:
            os.remove(rebuilt_path)

    def test_a_missing_decision_reopens_the_gate_on_the_real_corpus(self):
        """The committed DECISIONS table minus one entry must refuse again,
        naming exactly the row that lost its verdict -- proving the gate
        actually rejects an incomplete table, not merely that today's
        complete one happens to satisfy it."""
        if not os.path.exists(bjc.OUT_PATH):
            self.skipTest("jev-comments.jsonl has not been built yet")
        removed_id = sorted(bjc.DECISIONS)[0]
        removed_label = bjc.DECISIONS.pop(removed_id)
        try:
            state = bjc.build_rows()
            stderr = io.StringIO()
            with contextlib.redirect_stderr(stderr), self.assertRaises(SystemExit) as ctx:
                bjc.refuse_if_undecided(state)
            self.assertIn("1 row(s)", str(ctx.exception))
            self.assertIn(removed_id, stderr.getvalue())
        finally:
            bjc.DECISIONS[removed_id] = removed_label


if __name__ == "__main__":
    unittest.main()
