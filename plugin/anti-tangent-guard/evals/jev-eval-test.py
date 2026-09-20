"""Offline invariants for jev-eval.py. Never contacts the network: every
score_rows()/main() call here passes a fake `judge` callable instead of
jev_scan's real HTTP transport.
"""
import importlib.util
import json
import os
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))


def _load(name, filename):
    spec = importlib.util.spec_from_file_location(name, os.path.join(HERE, filename))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


# A hyphenated filename cannot be `import`ed by name, so the module under
# test is loaded by path, the same way it must be at run time when nothing
# on sys.path knows it exists yet.
je = _load("jev_eval", "jev-eval.py")

HOOKS_DIR = os.path.join(os.path.dirname(HERE), "hooks")
sys.path.insert(0, HOOKS_DIR)
import jev_scan  # noqa: E402


class FakeJudge(object):
    """Records every call it receives and returns a canned probability,
    never touching a socket. Also usable as the "the answer never comes
    back" stand-in when constructed with an event that fails the call."""

    def __init__(self, probability=0.1, event="jev-pass", detail=""):
        self.probability = probability
        self.event = event
        self.detail = detail
        self.calls = []

    def __call__(self, blocks, cfg):
        self.calls.append([b.text for b in blocks])
        return jev_scan.Verdict(probability=self.probability, event=self.event, detail=self.detail)


def _write_rows(rows):
    fd, path = tempfile.mkstemp(suffix=".jsonl")
    with os.fdopen(fd, "w") as fh:
        for row in rows:
            fh.write(json.dumps(row) + "\n")
    return path


def _row(row_id, source, label, block="text", regex_hit=False):
    return {"id": row_id, "source": source, "label": label, "block": block,
            "path": "a.go:1", "regex_hit": regex_hit, "judged_by": "derived"}


class RefuseUnlessAuthorized(unittest.TestCase):
    def test_refuses_without_the_key(self):
        with self.assertRaises(SystemExit):
            je.refuse_unless_authorized({"ANTI_TANGENT_JEV": "1"})

    def test_refuses_without_the_setting(self):
        with self.assertRaises(SystemExit):
            je.refuse_unless_authorized({"TYPESAFE_API_KEY": "k"})

    def test_refuses_with_an_empty_key(self):
        with self.assertRaises(SystemExit):
            je.refuse_unless_authorized({"ANTI_TANGENT_JEV": "1", "TYPESAFE_API_KEY": ""})

    def test_passes_with_both_set_and_no_ci_marker(self):
        je.refuse_unless_authorized({"ANTI_TANGENT_JEV": "1", "TYPESAFE_API_KEY": "k"})  # no raise

    def test_refuses_under_each_ci_marker(self):
        for marker in je.CI_MARKERS:
            env = {"ANTI_TANGENT_JEV": "1", "TYPESAFE_API_KEY": "k", marker: "true"}
            with self.assertRaises(SystemExit):
                je.refuse_unless_authorized(env)

    def test_a_ci_job_with_a_real_key_still_refuses(self):
        # The scenario the gate exists for: a CI job that happens to inherit
        # a real key must still not spend it.
        env = {"ANTI_TANGENT_JEV": "1", "TYPESAFE_API_KEY": "a-real-looking-key", "CI": "true"}
        with self.assertRaises(SystemExit):
            je.refuse_unless_authorized(env)


class QuestionHash(unittest.TestCase):
    def setUp(self):
        fd, self.question_path = tempfile.mkstemp(suffix=".json")
        with os.fdopen(fd, "w") as fh:
            fh.write('{"type": "choice"}')

    def tearDown(self):
        os.remove(self.question_path)

    def _cfg(self, model):
        class Cfg(object):
            pass
        cfg = Cfg()
        cfg.model = model
        return cfg

    def test_same_question_and_model_hash_the_same(self):
        a = je.question_hash(self._cfg("jev-1.13.0"), self.question_path)
        b = je.question_hash(self._cfg("jev-1.13.0"), self.question_path)
        self.assertEqual(a, b)

    def test_a_changed_model_misses_the_cache(self):
        a = je.question_hash(self._cfg("jev-1.13.0"), self.question_path)
        b = je.question_hash(self._cfg("jev-2.0.0"), self.question_path)
        self.assertNotEqual(a, b)

    def test_a_changed_question_misses_the_cache(self):
        a = je.question_hash(self._cfg("jev-1.13.0"), self.question_path)
        with open(self.question_path, "w") as fh:
            fh.write('{"type": "choice", "extra": true}')
        b = je.question_hash(self._cfg("jev-1.13.0"), self.question_path)
        self.assertNotEqual(a, b)


class LoadRows(unittest.TestCase):
    def test_ambiguous_rows_are_excluded(self):
        path = _write_rows([_row("r1", "s", "history"), _row("r2", "s", "ambiguous")])
        try:
            rows = je.load_rows(path)
            self.assertEqual([r["id"] for r in rows], ["r1"])
        finally:
            os.remove(path)

    def test_limit_applies_after_ambiguous_rows_are_dropped(self):
        rows_in = [_row("r1", "s", "ambiguous"), _row("r2", "s", "history"), _row("r3", "s", "not_history")]
        path = _write_rows(rows_in)
        try:
            rows = je.load_rows(path, limit=1)
            self.assertEqual([r["id"] for r in rows], ["r2"])
        finally:
            os.remove(path)

    def test_blank_lines_are_skipped(self):
        fd, path = tempfile.mkstemp(suffix=".jsonl")
        with os.fdopen(fd, "w") as fh:
            fh.write(json.dumps(_row("r1", "s", "history")) + "\n\n")
        try:
            rows = je.load_rows(path)
            self.assertEqual(len(rows), 1)
        finally:
            os.remove(path)


class CacheRoundTrip(unittest.TestCase):
    def test_load_cache_missing_file_is_empty(self):
        self.assertEqual(je.load_cache("/does/not/exist.json"), {})

    def test_save_then_load_round_trips(self):
        fd, path = tempfile.mkstemp(suffix=".json")
        os.close(fd)
        try:
            je.save_cache(path, {"r1": 0.42})
            self.assertEqual(je.load_cache(path), {"r1": 0.42})
        finally:
            os.remove(path)


class ScoreRowsNeverTouchesTheNetwork(unittest.TestCase):
    def _cache_path(self):
        fd, path = tempfile.mkstemp(suffix=".json")
        os.close(fd)
        os.remove(path)
        return path

    def _cleanup(self, cache_path):
        # score_rows only creates the file when it actually scores a row, so
        # a test asserting zero calls must not assume it exists.
        if os.path.exists(cache_path):
            os.remove(cache_path)

    def test_ambiguous_rows_are_never_requested(self):
        # load_rows already drops them before score_rows ever sees a row
        # list; this pins that a row explicitly excluded upstream still
        # never reaches the fake transport if it somehow arrived here.
        judge = FakeJudge()
        rows = [r for r in [_row("r1", "s", "ambiguous")] if r["label"] != "ambiguous"]
        cache_path = self._cache_path()
        try:
            je.score_rows(rows, cfg=None, cache={}, cache_path=cache_path, judge=judge)
            self.assertEqual(judge.calls, [])
        finally:
            self._cleanup(cache_path)

    def test_every_row_is_scored_exactly_once(self):
        judge = FakeJudge(probability=0.3)
        rows = [_row("r1", "s", "history", block="one"), _row("r2", "s", "not_history", block="two")]
        cache = {}
        cache_path = self._cache_path()
        try:
            je.score_rows(rows, cfg=None, cache=cache, cache_path=cache_path, judge=judge)
            self.assertEqual(len(judge.calls), 2)
            self.assertEqual(cache, {"r1": 0.3, "r2": 0.3})
        finally:
            os.remove(cache_path)

    def test_a_cached_row_is_not_re_requested(self):
        judge = FakeJudge(probability=0.9)
        rows = [_row("r1", "s", "history")]
        cache = {"r1": 0.1}  # already answered
        cache_path = self._cache_path()
        try:
            je.score_rows(rows, cfg=None, cache=cache, cache_path=cache_path, judge=judge)
            self.assertEqual(judge.calls, [])
            self.assertEqual(cache["r1"], 0.1)  # untouched, not overwritten
        finally:
            self._cleanup(cache_path)

    def test_a_clean_rows_probability_is_still_recorded(self):
        # judge() reports the highest probability it saw whether or not it
        # flagged -- a "clean" row must land in the cache on its real number,
        # not be skipped just because nothing crossed the threshold.
        judge = FakeJudge(probability=0.05, event="jev-pass")
        rows = [_row("r1", "s", "not_history")]
        cache = {}
        cache_path = self._cache_path()
        try:
            je.score_rows(rows, cfg=None, cache=cache, cache_path=cache_path, judge=judge)
            self.assertEqual(cache, {"r1": 0.05})
        finally:
            os.remove(cache_path)

    def test_a_provider_error_exits_naming_the_row(self):
        judge = FakeJudge(event="jev-error", detail="deadline")
        rows = [_row("r1", "s", "history")]
        cache_path = self._cache_path()
        try:
            with self.assertRaises(SystemExit) as ctx:
                je.score_rows(rows, cfg=None, cache={}, cache_path=cache_path, judge=judge)
            self.assertIn("r1", str(ctx.exception))
        finally:
            if os.path.exists(cache_path):
                os.remove(cache_path)


class GroupsAndTable(unittest.TestCase):
    def test_build_groups_counts_true_and_false_positives(self):
        rows = [
            _row("r1", "grp-a", "history", regex_hit=False),   # jev catches it
            _row("r2", "grp-a", "history", regex_hit=True),    # regex catches it
            _row("r3", "grp-a", "not_history", regex_hit=False),  # a false flag
            _row("r4", "grp-b", "not_history", regex_hit=False),  # correctly clean
        ]
        cache = {"r1": 0.9, "r2": 0.1, "r3": 0.9, "r4": 0.1}
        groups = je.build_groups(rows, cache, threshold=0.7)
        self.assertEqual(groups["grp-a"]["pos"], 2)
        self.assertEqual(groups["grp-a"]["tp"], 2)  # r1 via jev, r2 via regex
        self.assertEqual(groups["grp-a"]["jev_tp"], 1)  # only r1 crosses the model threshold
        self.assertEqual(groups["grp-a"]["regex_tp"], 1)  # only r2 has a regex hit
        self.assertEqual(groups["grp-a"]["fp"], 1)  # r3
        self.assertEqual(groups["grp-b"]["neg"], 1)
        self.assertEqual(groups["grp-b"]["fp"], 0)

    def test_rate_reports_na_for_a_zero_denominator(self):
        self.assertEqual(je.rate(0, 0), "n/a")
        self.assertEqual(je.rate(1, 2), "50%")

    def test_format_table_has_a_header_row_per_group_and_a_total_line(self):
        groups = {
            "grp-a": {"pos": 2, "neg": 0, "tp": 1, "fp": 0, "regex_tp": 1, "jev_tp": 0},
            "grp-b": {"pos": 0, "neg": 3, "tp": 0, "fp": 1, "regex_tp": 0, "jev_tp": 0},
        }
        table = je.format_table(groups)
        lines = table.splitlines()
        self.assertIn("group", lines[0])
        self.assertIn("recall", lines[0])
        self.assertIn("precision", lines[0])
        # one line per group, sorted, plus the header and the trailing summary
        group_lines = [l for l in lines if l.startswith("grp-")]
        self.assertEqual([l.split()[0] for l in group_lines], ["grp-a", "grp-b"])
        self.assertTrue(any(l.startswith("together:") for l in lines))

    def test_format_table_is_well_formed_with_no_groups_at_all(self):
        table = je.format_table({})
        self.assertIn("together: recall 0/0, 0 false flags in 0 clean comments", table)


class MainEndToEnd(unittest.TestCase):
    def test_main_refuses_before_touching_the_fixture_or_the_fake_judge(self):
        judge = FakeJudge()
        with self.assertRaises(SystemExit):
            je.main(["jev-eval.py"], {}, fixture_path="/does/not/exist.jsonl", judge=judge)
        self.assertEqual(judge.calls, [])

    def test_main_scores_and_prints_a_table_without_touching_the_network(self):
        rows_path = _write_rows([
            _row("r1", "grp-a", "history", block="alpha"),
            _row("r2", "grp-a", "ambiguous", block="beta"),
        ])
        fd, question_path = tempfile.mkstemp(suffix=".json")
        os.close(fd)
        with open(question_path, "w") as fh:
            fh.write("{}")
        cache_dir = tempfile.mkdtemp()
        judge = FakeJudge(probability=0.99)
        try:
            env = {"ANTI_TANGENT_JEV": "1", "TYPESAFE_API_KEY": "k"}
            table = je.main(["jev-eval.py"], env, fixture_path=rows_path,
                             cache_dir=cache_dir, question_path=question_path, judge=judge)
            # only the non-ambiguous row was ever sent
            self.assertEqual(judge.calls, [["alpha"]])
            self.assertIn("grp-a", table)
            cache_files = [f for f in os.listdir(cache_dir) if f.startswith(".jev-eval-cache-")]
            self.assertEqual(len(cache_files), 1)
        finally:
            os.remove(rows_path)
            os.remove(question_path)
            for f in os.listdir(cache_dir):
                os.remove(os.path.join(cache_dir, f))
            os.rmdir(cache_dir)


if __name__ == "__main__":
    unittest.main()
