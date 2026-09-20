import concurrent.futures
import json
import os
import shutil
import subprocess
import sys
import tempfile
import threading
import time
import unittest

HOOKS = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HOOKS)

import jev_scan  # noqa: E402


class BlockBuilder(unittest.TestCase):
    def test_touched_run_is_one_block_with_untouched_neighbours(self):
        context = ("// The count cap used to return a plain error.\n"
                   "// Also covers the empty-string case.\n"
                   "func a() {}\n")
        blocks = jev_scan.build_blocks(
            "x.go", ["// Also covers the empty-string case."], context)
        self.assertEqual(len(blocks), 1)
        self.assertIn("used to return a plain error", blocks[0].text)
        self.assertIn("empty-string case", blocks[0].text)

    def test_untouched_run_is_not_returned(self):
        context = ("// An old comment.\n"
                   "func a() {}\n"
                   "// A touched comment.\n")
        blocks = jev_scan.build_blocks("x.go", ["// A touched comment."], context)
        self.assertEqual([b.text for b in blocks], ["A touched comment."])

    def test_trailing_comment_contributes_only_its_span(self):
        context = 'key := "sk-live-abcdef" // rotate weekly\n'
        blocks = jev_scan.build_blocks("x.go", ['key := "sk-live-abcdef" // rotate weekly'], context)
        self.assertEqual([b.text for b in blocks], ["rotate weekly"])

    def test_fragment_matches_its_line_by_containment(self):
        context = "\tx := foo() // this used to panic on nil\n"
        blocks = jev_scan.build_blocks(
            "x.go", ["foo() // this used to panic on nil"], context)
        self.assertEqual([b.text for b in blocks], ["this used to panic on nil"])

    def test_no_match_falls_back_to_touched_lines(self):
        blocks = jev_scan.build_blocks(
            "x.go", ["// this used to panic on nil"], "unrelated file text\n")
        self.assertEqual([b.text for b in blocks], ["this used to panic on nil"])

    def test_the_fallback_works_for_hash_family_paths(self):
        # The fallback passes no context, which the triple-quote helper must
        # survive: it is the only caller that can hand it None.
        blocks = jev_scan.build_blocks(
            "x.py", ["# this used to panic on nil"], "unrelated file text\n")
        self.assertEqual([b.text for b in blocks], ["this used to panic on nil"])

    def test_star_inside_go_raw_string_is_not_a_comment(self):
        context = "const s = `\n * Previously the parser rejected tabs.\n`\n"
        blocks = jev_scan.build_blocks(
            "x.go", [" * Previously the parser rejected tabs."], context)
        self.assertEqual(blocks, [])

    def test_hash_inside_python_docstring_is_not_a_comment(self):
        context = '"""Doc.\n\n# Previously this returned nil.\n"""\n'
        blocks = jev_scan.build_blocks(
            "x.py", ["# Previously this returned nil."], context)
        self.assertEqual(blocks, [])

    def test_long_block_is_windowed_around_the_touched_line(self):
        filler = "".join("// filler line %d\n" % i for i in range(200))
        context = filler + "// this used to panic on nil\n"
        blocks = jev_scan.build_blocks("x.go", ["// this used to panic on nil"], context)
        self.assertEqual(len(blocks), 1)
        self.assertLessEqual(len(blocks[0].text), jev_scan.BLOCK_CHARS)
        self.assertIn("used to panic", blocks[0].text)

    def test_block_count_is_capped_and_says_so(self):
        context = "".join("// touched %d\nfunc f%d() {}\n" % (i, i) for i in range(30))
        touched = ["// touched %d" % i for i in range(30)]
        blocks, capped = jev_scan.build_blocks("x.go", touched, context, report=True)
        self.assertEqual(len(blocks), jev_scan.MAX_BLOCKS)
        self.assertTrue(capped)
        self.assertFalse(jev_scan.build_blocks("x.go", ["// one"], "// one\n",
                                               report=True)[1])

    def test_touched_line_longer_than_the_cap_keeps_its_first_characters(self):
        body = "".join("word%04d " % i for i in range(500))   # varied, not uniform
        long_touched = "// " + body
        context = "// neighbour above\n%s\n// neighbour below\n" % long_touched
        blocks = jev_scan.build_blocks("x.go", [long_touched], context)
        self.assertEqual(len(blocks), 1)
        self.assertEqual(len(blocks[0].text), jev_scan.BLOCK_CHARS)
        self.assertEqual(blocks[0].text, body.strip()[:jev_scan.BLOCK_CHARS])
        self.assertNotIn("neighbour", blocks[0].text)

    def test_docstring_delimiters_do_not_hide_real_comments(self):
        context = ('"""Doc with a # inside.\n"""\n'
                   "# A real comment below the docstring.\n")
        blocks = jev_scan.build_blocks("x.py", ["# A real comment below the docstring."], context)
        self.assertEqual([b.text for b in blocks], ["A real comment below the docstring."])

    def test_single_line_triple_quoted_string_does_not_open_a_span(self):
        context = 's = """one line"""\n# A real comment.\n'
        blocks = jev_scan.build_blocks("x.py", ["# A real comment."], context)
        self.assertEqual([b.text for b in blocks], ["A real comment."])

    def test_a_touched_comment_matching_docstring_text_is_still_returned(self):
        context = ('"""Doc.\n# Same comment.\n"""\n'
                   "# Same comment.\n")
        blocks = jev_scan.build_blocks("x.py", ["# Same comment."], context)
        self.assertEqual([b.text for b in blocks], ["Same comment."])


class Redaction(unittest.TestCase):
    def test_named_assignment_is_redacted(self):
        for line in ("export AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI0K7MDENGbPxRfiCY",
                     'api_key: "sk-live-9d8f7a6b5c4d3e2f1a0b"',
                     "password = hunter2hunter2hunter2"):
            self.assertIn("<redacted>", jev_scan.redact(line), line)

    def test_bare_high_entropy_token_is_redacted(self):
        for line in ("the old value was AKIAIOSFODNN7EXAMPLEQWERTYUIOP",
                     "was dGhpcyBpcyBhIGxvbmcgYmFzZTY0IHN0cmluZzEyMw==",
                     "hash 5f4dcc3b5aa765d61d8327deb882cf995f4dcc3b"):
            self.assertIn("<redacted>", jev_scan.redact(line), line)

    def test_padding_is_consumed(self):
        padded = "was dGhpcyBpcyBhIGxvbmcgYmFzZTY0IHN0cmluZzEyMw=="
        self.assertEqual(jev_scan.redact(padded), "was <redacted>")

    def test_boundaries(self):
        short = "A1" + "b" * 21          # 23 characters: under the run length
        exact = "A1" + "b" * 22          # 24 characters: at it
        self.assertEqual(jev_scan.redact(short), short)
        self.assertIn("<redacted>", jev_scan.redact(exact))

    def test_token_starting_with_plus(self):
        token = "+A1" + "b" * 21         # starts with +, 24 characters total
        self.assertIn("<redacted>", jev_scan.redact(token))

    def test_token_starting_with_slash(self):
        token = "/A1" + "b" * 21         # starts with /, 24 characters total
        self.assertIn("<redacted>", jev_scan.redact(token))

    def test_prose_is_untouched(self):
        for line in ("The count cap used to return a plain error to the caller.",
                     "A well-known copy-on-write trade-off, documented upstream.",
                     "a backward-compatibility-preserving migration path",
                     "SupercalifragilisticexpialidociousBehaviour"):
            self.assertEqual(jev_scan.redact(line), line)

    def test_a_credential_cannot_survive_the_block_cap(self):
        secret = "AKIAIOSFODNN7EXAMPLEQWERTYUIOP1234"
        filler = ["// filler %d" % i for i in range(300)]
        context = "\n".join(filler + ["// old value was " + secret]) + "\n"
        blocks = jev_scan.build_blocks("x.go", ["// old value was " + secret], context)
        self.assertTrue(blocks)
        self.assertNotIn(secret[:12], blocks[0].text)

    def test_double_quoted_value_is_fully_redacted(self):
        line = '# password: "correct horse battery staple"'
        self.assertEqual(jev_scan.redact(line), '# password=<redacted>')

    def test_single_quoted_value_is_fully_redacted(self):
        line = "password = 'hunter2 hunter2hunter2'"
        self.assertEqual(jev_scan.redact(line), "password=<redacted>")

    def test_hex_boundary_31_vs_32(self):
        hex31 = "0" * 31                 # 31 hex chars: survives
        hex32 = "0" * 32                 # 32 hex chars: redacted
        self.assertEqual(jev_scan.redact(hex31), hex31)
        self.assertEqual(jev_scan.redact(hex32), "<redacted>")


class Config(unittest.TestCase):
    BASE = {"ANTI_TANGENT_JEV": "1", "TYPESAFE_API_KEY": "k"}

    def cfg(self, path="x.go", **over):
        env = dict(self.BASE)
        env.update(over)
        return jev_scan.config(env, path)

    def test_enabled_with_setting_and_key(self):
        self.assertTrue(self.cfg().enabled)

    def test_off_without_setting(self):
        c = self.cfg(ANTI_TANGENT_JEV="0")
        self.assertFalse(c.enabled)
        self.assertEqual(c.reason, "setting")

    def test_off_without_key(self):
        c = self.cfg(TYPESAFE_API_KEY="")
        self.assertFalse(c.enabled)
        self.assertEqual(c.reason, "no-key")

    def test_off_when_comment_guard_disabled(self):
        c = self.cfg(ANTI_TANGENT_COMMENT_GUARD="0")
        self.assertFalse(c.enabled)
        self.assertEqual(c.reason, "guard=0")

    def test_off_for_excluded_path(self):
        c = self.cfg(path="/repo/secrets/keys.go",
                     ANTI_TANGENT_JEV_EXCLUDE="*/secrets/*:*/vendor/*")
        self.assertFalse(c.enabled)
        self.assertEqual(c.reason, "excluded")

    def test_threshold_default_and_clamping(self):
        self.assertEqual(self.cfg().threshold, 0.7)
        self.assertEqual(self.cfg(ANTI_TANGENT_JEV_THRESHOLD="0.85").threshold, 0.85)
        for bad in ("0", "-1", "1.5", "nan", "inf", "abc", ""):
            self.assertEqual(self.cfg(ANTI_TANGENT_JEV_THRESHOLD=bad).threshold, 0.7, bad)

    def test_untrusted_url_does_not_receive_the_key(self):
        c = self.cfg(ANTI_TANGENT_JEV_URL="https://evil.example/v1/systemone")
        self.assertEqual(c.url, jev_scan.DEFAULT_URL)
        self.assertEqual(c.url_reason, "untrusted-host")

    def test_loopback_url_is_allowed(self):
        c = self.cfg(ANTI_TANGENT_JEV_URL="http://127.0.0.1:8931/v1/systemone")
        self.assertEqual(c.url, "http://127.0.0.1:8931/v1/systemone")

    def test_trusted_flag_allows_any_host(self):
        c = self.cfg(ANTI_TANGENT_JEV_URL="https://proxy.internal/v1/systemone",
                     ANTI_TANGENT_JEV_URL_TRUSTED="1")
        self.assertEqual(c.url, "https://proxy.internal/v1/systemone")


class QuestionFile(unittest.TestCase):
    def test_options_are_the_measured_three(self):
        q = jev_scan.question()
        self.assertEqual(q["type"], "choice")
        self.assertEqual(sorted(q["criteria"]),
                         ["change_history", "compatibility_contract", "present_behaviour"])
        self.assertIn("not_for", q["criteria"]["change_history"])
        for name in ("change_history", "compatibility_contract", "present_behaviour"):
            self.assertTrue(q["criteria"][name]["examples"], name)

    def test_missing_file_returns_none(self):
        self.assertIsNone(jev_scan.question(path="/nonexistent/jev-question.json"))

    def test_the_loader_caches_for_the_process(self):
        with tempfile.NamedTemporaryFile("w", suffix=".json", delete=False) as fh:
            json.dump(jev_scan.question(), fh)
        self.addCleanup(lambda: os.path.exists(fh.name) and os.unlink(fh.name))
        first = jev_scan.question(path=fh.name)
        os.unlink(fh.name)
        self.assertEqual(jev_scan.question(path=fh.name), first)

    def test_malformed_files_return_none(self):
        bad = ['{"type": "choice"',
               '{"type": "noul", "criteria": {}}',
               '{"type": "choice", "criteria": {"change_history": {"what": "x"}}}',
               '{"type": "choice", "criteria": {"change_history": {},'
               ' "compatibility_contract": {"what": "x"},'
               ' "present_behaviour": {"what": "x"}}}',
               '[]']
        for text in bad:
            with tempfile.NamedTemporaryFile("w", suffix=".json", delete=False) as fh:
                fh.write(text)
            self.addCleanup(os.unlink, fh.name)
            self.assertIsNone(jev_scan.question(path=fh.name), text)


class Judge(unittest.TestCase):
    def cfg(self, **over):
        env = {"ANTI_TANGENT_JEV": "1", "TYPESAFE_API_KEY": "k"}
        env.update(over)
        return jev_scan.config(env, "x.go")

    @staticmethod
    def answer(p):
        return {"model": "jev-1.13.0",
                "answers": {"kind": {"type": "choice", "choice": "change_history",
                                     "probabilities": {"change_history": p,
                                                       "compatibility_contract": 0.0,
                                                       "present_behaviour": 1 - p},
                                     "confidence": 0.9}}}

    def test_flags_at_or_above_threshold(self):
        blocks = [jev_scan.Block("The count cap used to return a plain error.", 1)]
        r = jev_scan.judge(blocks, self.cfg(), transport=lambda *a, **k: self.answer(0.82))
        self.assertEqual(r.flagged.text, blocks[0].text)
        self.assertAlmostEqual(r.probability, 0.82)

    def test_below_threshold_passes(self):
        blocks = [jev_scan.Block("Returns nil when the file is absent.", 1)]
        r = jev_scan.judge(blocks, self.cfg(), transport=lambda *a, **k: self.answer(0.31))
        self.assertIsNone(r.flagged)
        self.assertEqual(r.event, "jev-pass")

    def test_request_shape(self):
        seen = {}

        def transport(url, body, headers, timeout):
            seen.update(url=url, body=body, headers=headers, timeout=timeout)
            return self.answer(0.1)

        jev_scan.judge([jev_scan.Block("A comment.", 1)], self.cfg(), transport=transport)
        self.assertEqual(seen["url"], jev_scan.DEFAULT_URL)
        self.assertEqual(seen["headers"]["Authorization"], "Bearer k")
        self.assertEqual(seen["body"]["model"], "jev-1.13.0")
        self.assertEqual(seen["body"]["state"], {"comment": "A comment."})
        self.assertIn("kind", seen["body"]["questions"])

    def test_every_failure_passes(self):
        for boom in (OSError("no route"), ValueError("bad json"), RuntimeError("?")):
            def transport(*a, **k):
                raise boom
            r = jev_scan.judge([jev_scan.Block("c", 1)], self.cfg(), transport=transport)
            self.assertIsNone(r.flagged, boom)
            self.assertEqual(r.event, "jev-error")

    def test_missing_field_passes(self):
        r = jev_scan.judge([jev_scan.Block("c", 1)], self.cfg(),
                           transport=lambda *a, **k: {"answers": {}})
        self.assertIsNone(r.flagged)
        self.assertEqual(r.event, "jev-error")

    def test_returns_within_the_deadline_against_a_transport_that_never_returns(self):
        forever = threading.Event()
        self.addCleanup(forever.set)

        def never(*a, **k):
            forever.wait()
            return self.answer(0.9)

        start = time.monotonic()
        r = jev_scan.judge([jev_scan.Block("c%d" % i, i) for i in range(20)],
                           self.cfg(), transport=never,
                           deadline=time.monotonic() + 0.5)
        elapsed = time.monotonic() - start
        self.assertLess(elapsed, 2.0, "judge waited for a worker it should have abandoned")
        self.assertIsNone(r.flagged)
        self.assertEqual(r.event, "jev-error")

    def test_no_request_starts_after_the_deadline(self):
        started = []
        release = threading.Event()
        self.addCleanup(release.set)

        def slow(*a, **k):
            started.append(time.monotonic())
            release.wait()
            return self.answer(0.0)

        begin = time.monotonic()
        jev_scan.judge([jev_scan.Block("c%d" % i, i) for i in range(20)],
                       self.cfg(), transport=slow, deadline=begin + 0.3)
        self.assertTrue(started)
        for at in started:
            self.assertLessEqual(at, begin + 0.3 + 0.05,
                                 "a request started after the deadline")
        self.assertLessEqual(len(started), jev_scan.MAX_WORKERS)

    def test_a_batch_is_resolved_by_block_index(self):
        # The lower-index block is also the first submitted (queue.pop(0)
        # processes blocks in list order), so a race that merely lets both
        # complete around the same time cannot tell "the tie-break sorts by
        # index" apart from "the first-submitted block happened to finish
        # first anyway" -- both give the same observed winner. Proving the
        # sort really runs needs the HIGHER-index block to finish first in
        # real time while the LOWER-index block still wins.
        #
        # Racing that gap against judge()'s own wait() call is not reliably
        # reproducible: probing this on the target system showed the main
        # thread reacting to a single completed future and returning from
        # wait() before a second, closely-timed completion could join it, at
        # gaps from 100us to 20ms and with or without a threading.Barrier
        # synchronizing worker start. So wait() is patched for this test to
        # block for ALL_COMPLETED rather than FIRST_COMPLETED, which
        # deterministically constructs the "both landed in one batch" case
        # the acceptance criterion describes, while still driving the real
        # _resolve_batch sort against a real concurrent.futures.wait()
        # result. The completion order itself (second finishes at 0.01s,
        # first at 0.05s) is then just an ordinary, non-racy sleep.
        real_wait = concurrent.futures.wait

        def wait_for_all(fs, timeout=None, return_when=None):
            return real_wait(fs, timeout=timeout, return_when=concurrent.futures.ALL_COMPLETED)

        jev_scan.concurrent.futures.wait = wait_for_all
        self.addCleanup(setattr, jev_scan.concurrent.futures, "wait", real_wait)

        def paired(url, body, headers, timeout):
            if body["state"]["comment"] == "second block":
                time.sleep(0.01)
            else:
                time.sleep(0.05)
            return self.answer(0.9)

        blocks = [jev_scan.Block("first block", 1), jev_scan.Block("second block", 2)]
        r = jev_scan.judge(blocks, self.cfg(), transport=paired)
        self.assertEqual(r.flagged.text, "first block")

    def test_the_transport_refuses_a_redirect(self):
        # A 3xx must reach judge() as an error, never as a second request
        # carrying the key somewhere the host rule never approved. Pointing
        # the redirect at an unroutable port would not prove that: a BROKEN
        # _NoRedirects that followed the redirect would also fail to connect
        # there, so judge() would still report jev-error either way, and the
        # test could not tell "refused" from "could not connect". Pointing
        # it at a second, real loopback server that records what it
        # receives can tell the difference -- a correct handler never
        # connects to it at all.
        import http.server
        import threading as th

        sink_requests = []

        class Sink(http.server.BaseHTTPRequestHandler):
            def do_POST(self):
                sink_requests.append(dict(self.headers))
                self.send_response(200)
                self.end_headers()

            def log_message(self, *a):
                pass

        sink = http.server.HTTPServer(("127.0.0.1", 0), Sink)
        th.Thread(target=sink.serve_forever, daemon=True).start()
        self.addCleanup(sink.shutdown)

        class Redirector(http.server.BaseHTTPRequestHandler):
            def do_POST(self):
                self.send_response(302)
                self.send_header("Location", "http://127.0.0.1:%d/v1/systemone"
                                 % sink.server_port)
                self.end_headers()

            def log_message(self, *a):
                pass

        server = http.server.HTTPServer(("127.0.0.1", 0), Redirector)
        th.Thread(target=server.serve_forever, daemon=True).start()
        self.addCleanup(server.shutdown)
        cfg = self.cfg(ANTI_TANGENT_JEV_URL="http://127.0.0.1:%d/v1/systemone"
                       % server.server_port)
        r = jev_scan.judge([jev_scan.Block("c", 1)], cfg)
        self.assertIsNone(r.flagged)
        self.assertEqual(r.event, "jev-error")
        self.assertEqual(sink_requests, [],
                         "the redirect target received a request")
        self.assertFalse(any("Authorization" in h for h in sink_requests),
                         "the key reached the redirect target")

    def test_a_flag_survives_another_block_failing(self):
        calls = []

        def mixed(url, body, headers, timeout):
            calls.append(body["state"]["comment"])
            if body["state"]["comment"] == "bad block":
                raise OSError("no route")
            return self.answer(0.9)

        blocks = [jev_scan.Block("bad block", 1), jev_scan.Block("flagging block", 2)]
        r = jev_scan.judge(blocks, self.cfg(), transport=mixed)
        self.assertIsNotNone(r.flagged, "a failure must not suppress another block's flag")
        self.assertEqual(r.flagged.text, "flagging block")

    def test_failures_without_a_flag_report_the_error(self):
        def failing(url, body, headers, timeout):
            raise OSError("no route")

        r = jev_scan.judge([jev_scan.Block("c", 1)], self.cfg(), transport=failing)
        self.assertIsNone(r.flagged)
        self.assertEqual(r.event, "jev-error")

    def test_clean_verdict_carries_the_highest_probability(self):
        r = jev_scan.judge([jev_scan.Block("c", 1)], self.cfg(),
                           transport=lambda *a, **k: self.answer(0.42))
        self.assertIsNone(r.flagged)
        self.assertAlmostEqual(r.probability, 0.42)


class Strikes(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.mkdtemp()

    def tearDown(self):
        shutil.rmtree(self.dir, ignore_errors=True)

    def test_counts_per_session_and_path(self):
        self.assertEqual(jev_scan.strike(self.dir, "s1", "/a.go"), 1)
        self.assertEqual(jev_scan.strike(self.dir, "s1", "/a.go"), 2)
        self.assertEqual(jev_scan.strike(self.dir, "s1", "/a.go"), 3)
        self.assertEqual(jev_scan.strike(self.dir, "s1", "/b.go"), 1)
        self.assertEqual(jev_scan.strike(self.dir, "s2", "/a.go"), 1)

    def test_expired_stamp_restarts(self):
        jev_scan.strike(self.dir, "s1", "/a.go")
        stamp = jev_scan._strike_path(self.dir, "s1", "/a.go")
        old = time.time() - jev_scan.STRIKE_TTL_S - 1
        os.utime(stamp, (old, old))
        self.assertEqual(jev_scan.strike(self.dir, "s1", "/a.go"), 1)

    def test_unwritable_dir_is_survivable(self):
        self.assertEqual(jev_scan.strike("/proc/nonexistent", "s1", "/a.go"), 1)

    def test_concurrent_writers_leave_a_readable_stamp(self):
        # Eight processes racing one stamp. A lost update is allowed and costs
        # an extra refusal; a corrupt stamp is not, because the next read
        # would fail and the count would restart silently.
        code = ("import sys; sys.path.insert(0, %r); import jev_scan;"
                "jev_scan.strike(%r, 's1', '/a.go')" % (HOOKS, self.dir))
        procs = [subprocess.Popen([sys.executable, "-c", code]) for _ in range(8)]
        for proc in procs:
            proc.wait(timeout=30)
        stamp = jev_scan._strike_path(self.dir, "s1", "/a.go")
        with open(stamp) as fh:
            value = int(fh.read().strip())
        self.assertGreaterEqual(value, 1)
        self.assertLessEqual(value, 8)
        self.assertEqual(jev_scan.strike(self.dir, "s1", "/a.go"), value + 1)

    def test_breaker_opens_and_closes(self):
        self.assertFalse(jev_scan.breaker_open(self.dir))
        jev_scan.breaker_trip(self.dir)
        self.assertTrue(jev_scan.breaker_open(self.dir))
        stamp = os.path.join(self.dir, jev_scan.BREAKER_FILE)
        old = time.time() - jev_scan.BREAKER_S - 1
        os.utime(stamp, (old, old))
        self.assertFalse(jev_scan.breaker_open(self.dir))


if __name__ == "__main__":
    unittest.main()
