import concurrent.futures
import gc
import json
import os
import shlex
import shutil
import subprocess
import sys
import tempfile
import threading
import time
import unittest
import warnings

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

    def test_an_exact_hit_does_not_excuse_a_fragment_from_the_containment_check(self):
        # "foo()" exact-matches line 0 and is ALSO a substring of lines 1 and
        # 2. The dict fast path alone would stop at the exact hit and miss
        # the other two; every touched fragment must still be checked for
        # containment everywhere, not only the fragments the dict missed.
        lines = ["foo()", "x := foo()", "foo() // c", "unrelated"]
        self.assertEqual(jev_scan._touched_indexes(lines, ["foo()"]), {0, 1, 2})

    def test_a_fragment_with_no_exact_line_still_marks_every_containing_line(self):
        lines = ["fooBar()", "x := fooBar()", "fooBar() // c", "unrelated"]
        self.assertEqual(jev_scan._touched_indexes(lines, ["fooBar"]), {0, 1, 2})

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

    def test_a_blank_marker_paragraph_break_keeps_both_paragraphs(self):
        context = ("// First para, the history.\n"
                   "//\n"
                   "// Second para.\n"
                   "func a() {}\n")
        blocks = jev_scan.build_blocks("x.go", ["// Second para."], context)
        self.assertEqual(len(blocks), 1)
        self.assertEqual(blocks[0].text, "First para, the history.\n\nSecond para.")
        self.assertEqual(blocks[0].line, 1)

    def test_a_blank_marker_paragraph_break_keeps_both_paragraphs_in_python(self):
        context = ("# First para, the history.\n"
                   "#\n"
                   "# Second para.\n"
                   "def a(): pass\n")
        blocks = jev_scan.build_blocks("x.py", ["# Second para."], context)
        self.assertEqual(len(blocks), 1)
        self.assertEqual(blocks[0].text, "First para, the history.\n\nSecond para.")
        self.assertEqual(blocks[0].line, 1)

    def test_a_blank_marker_paragraph_break_keeps_both_paragraphs_in_a_star_block(self):
        context = ("/*\n"
                   " * First para, the history.\n"
                   " *\n"
                   " * Second para.\n"
                   " */\n"
                   "func a() {}\n")
        blocks = jev_scan.build_blocks("x.go", [" * Second para."], context)
        self.assertEqual(len(blocks), 1)
        self.assertEqual(blocks[0].text, "First para, the history.\n\nSecond para.")
        # The opener line "/*" is itself a blank comment line and sits ahead
        # of the first real paragraph, so it is trimmed off the front; the
        # block still reports the line the first paragraph actually sits on.
        self.assertEqual(blocks[0].line, 2)

    def test_a_code_line_still_splits_two_blocks(self):
        context = "// A.\nfunc a() {}\n// B.\n"
        blocks = jev_scan.build_blocks("x.go", ["// A.", "// B."], context)
        self.assertEqual([(b.text, b.line) for b in blocks], [("A.", 1), ("B.", 3)])

    def test_a_genuinely_blank_line_still_splits_two_blocks(self):
        # No marker at all, unlike the paragraph-break cases above -- a blank
        # line is not a comment line and must keep ending a run.
        context = "// A.\n\n// B.\n"
        blocks = jev_scan.build_blocks("x.go", ["// A.", "// B."], context)
        self.assertEqual([(b.text, b.line) for b in blocks], [("A.", 1), ("B.", 3)])

    def test_a_run_of_only_empty_markers_produces_no_block(self):
        context = "//\n//\n//\nfunc a() {}\n"
        blocks = jev_scan.build_blocks("x.go", ["//"], context)
        self.assertEqual(blocks, [])

    def test_a_trailing_empty_marker_is_trimmed_without_changing_the_text(self):
        context = "// Real text.\n//\nfunc a() {}\n"
        blocks = jev_scan.build_blocks("x.go", ["// Real text."], context)
        self.assertEqual([b.text for b in blocks], ["Real text."])

    def test_touching_every_line_of_a_large_file_stays_roughly_linear(self):
        # A fresh Write touches every line of the file, so matching touched
        # lines against context lines one substring test at a time would be
        # quadratic in the file size -- slow enough on a large generated file
        # to blow the hook's own deadline. 5,000 distinct lines is well past
        # the point where a quadratic matcher would already miss this bound.
        n = 5000
        context = "".join("// comment line number %d is here\n" % i for i in range(n))
        touched = ["// comment line number %d is here" % i for i in range(n)]
        start = time.monotonic()
        jev_scan.build_blocks("x.go", touched, context)
        self.assertLess(time.monotonic() - start, 1.0)


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
        # server_close() is registered before shutdown() so LIFO cleanup
        # order runs shutdown() first: it must stop serve_forever's accept
        # loop before the socket underneath it is closed.
        self.addCleanup(sink.server_close)
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
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        cfg = self.cfg(ANTI_TANGENT_JEV_URL="http://127.0.0.1:%d/v1/systemone"
                       % server.server_port)
        r = jev_scan.judge([jev_scan.Block("c", 1)], cfg)
        # The HTTPError _NoRedirects raises keeps the refused response's
        # socket alive through a traceback -> frame -> exception cycle that
        # reference counting cannot break; left alone it is reaped by the
        # interpreter's own shutdown GC, which reports the socket's
        # ResourceWarning through the unhandled "Exception ignored in"
        # path instead of the ordinary warnings machinery. Collecting here,
        # with that one warning silenced for the collection itself, closes
        # it where a normal filter can see it.
        with warnings.catch_warnings():
            warnings.simplefilter("ignore", ResourceWarning)
            gc.collect()
        self.assertIsNone(r.flagged)
        self.assertEqual(r.event, "jev-error")
        self.assertEqual(sink_requests, [],
                         "the redirect target received a request")
        self.assertFalse(any("Authorization" in h for h in sink_requests),
                         "the key reached the redirect target")

    def test_an_oversized_response_is_an_error_not_an_answer(self):
        # Both bodies are valid JSON carrying a flagging probability, so a
        # transport that parsed either would flag. One is exactly at the cap
        # and must still be read; the other is past it and must fail the
        # request before any parse, as ResponseTooLarge rather than as a
        # decode error over a truncated body.
        import http.server
        import threading as th

        def padded(target):
            skeleton = json.dumps(dict(pad="", **self.answer(0.99)))
            return skeleton.replace('"pad": ""', '"pad": "%s"' % ("x" * (target - len(skeleton))))

        bodies = {"/fit": padded(jev_scan.MAX_RESPONSE_BYTES).encode("utf-8"),
                  "/big": padded(jev_scan.MAX_RESPONSE_BYTES + 1).encode("utf-8")}

        class Sized(http.server.BaseHTTPRequestHandler):
            def do_POST(self):
                body = bodies[self.path]
                self.send_response(200)
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def log_message(self, *a):
                pass

        server = http.server.HTTPServer(("127.0.0.1", 0), Sized)
        th.Thread(target=server.serve_forever, daemon=True).start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)

        fit = self.cfg(ANTI_TANGENT_JEV_URL="http://127.0.0.1:%d/fit" % server.server_port)
        r = jev_scan.judge([jev_scan.Block("c", 1)], fit)
        self.assertIsNotNone(r.flagged, "a body at the cap must still be parsed")

        big = self.cfg(ANTI_TANGENT_JEV_URL="http://127.0.0.1:%d/big" % server.server_port)
        r = jev_scan.judge([jev_scan.Block("c", 1)], big)
        self.assertIsNone(r.flagged)
        self.assertEqual(r.event, "jev-error")
        self.assertEqual(r.detail, "ResponseTooLarge")

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

    def test_a_count_that_cannot_be_written_is_none_not_one(self):
        # A path under a regular file: open() fails there for root too, so
        # the store is unusable whoever runs the suite. Reporting 1 would be
        # the bug: 1 never exceeds the limit, so a caller that trusted it
        # would refuse the same file on every attempt.
        blocker = os.path.join(self.dir, "not-a-dir")
        with open(blocker, "w") as fh:
            fh.write("")
        unusable = os.path.join(blocker, "state")
        for _ in range(3):
            self.assertIsNone(jev_scan.strike(unusable, "s1", "/a.go"))

    def test_a_corrupt_stamp_restarts_the_count(self):
        # A stamp that cannot be parsed is replaced, not left in place: left
        # alone it would fail to parse on every attempt, and the count would
        # never move past 1.
        stamp = jev_scan._strike_path(self.dir, "s1", "/a.go")
        with open(stamp, "w") as fh:
            fh.write("not a number")
        self.assertEqual(jev_scan.strike(self.dir, "s1", "/a.go"), 1)
        self.assertEqual(jev_scan.strike(self.dir, "s1", "/a.go"), 2)

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

    def test_warn_once_then_silent(self):
        first = jev_scan._warn_once(self.dir, "s1", "x")
        second = jev_scan._warn_once(self.dir, "s1", "x")
        self.assertEqual(first, jev_scan.WARN_MESSAGE % "x")
        self.assertEqual(second, "")

    def test_warn_once_names_the_no_request_case_honestly(self):
        message = jev_scan._warn_once(self.dir, "s1", "deadline-before-request")
        self.assertEqual(message, jev_scan.WARN_MESSAGE_NO_REQUEST % "deadline-before-request")
        self.assertNotIn("could not reach", message)


class Run(unittest.TestCase):
    """run()'s own outer handler, exercised in-process rather than through judge().

    A refused connection is handled inside judge() and converted to an ordinary
    jev-error verdict, so it never reaches run()'s own try/except. Only a raise
    from somewhere else run() calls proves that handler actually runs.
    """

    def setUp(self):
        self.dir = tempfile.mkdtemp()
        self.file = os.path.join(self.dir, "x.go")

    def tearDown(self):
        shutil.rmtree(self.dir, ignore_errors=True)

    def env(self):
        return {"ANTI_TANGENT_JEV": "1", "TYPESAFE_API_KEY": "k"}

    def test_an_unexpected_exception_fails_open(self):
        original = jev_scan.build_blocks
        jev_scan.build_blocks = lambda *a, **k: (_ for _ in ()).throw(RuntimeError("boom"))
        try:
            code, event, message = jev_scan.run(
                self.file, ["// A comment."], "// A comment.\n", self.env(), "s1", self.dir)
        finally:
            jev_scan.build_blocks = original
        self.assertEqual(code, 0, "an internal error must allow the write")
        self.assertEqual(event, "jev-error|RuntimeError")
        self.assertNotIn("Traceback", message or "")

    def test_a_refusal_over_a_capped_block_set_says_so(self):
        # The cap is decided before judge() runs and has to reach every
        # verdict, not only a pass: a refusal or a yield reached over less
        # than the edit contained is a different fact from one reached over
        # all of it, and the trace is the only place that can be read.
        original = jev_scan.judge
        jev_scan.judge = (lambda blocks, cfg, transport=None, deadline=None:
                          jev_scan.Verdict(blocks[0], 0.9, "jev-block"))
        lines = []
        for i in range(jev_scan.MAX_BLOCKS + 1):
            lines += ["// block %d" % i, "var v%d = %d" % (i, i)]
        context = "\n".join(lines) + "\n"
        try:
            events = [jev_scan.run(self.file, lines, context, self.env(), "s1", self.dir)[1]
                      for _ in range(3)]
        finally:
            jev_scan.judge = original
        self.assertEqual(events[:2], ["jev-block|p=0.90,capped"] * 2)
        self.assertEqual(events[2], "jev-yield|%s,capped" % self.file)

    def test_an_untrusted_url_leaves_a_trace_of_the_silent_fallback(self):
        # judge() is stubbed rather than left to hit the real DEFAULT_URL a
        # rejected override falls back to: this test is about what run()
        # records, not about the network. What matters is which URL cfg
        # actually carries when judge() is called with it.
        original = jev_scan.judge
        seen = []

        def fake_judge(blocks, cfg, transport=None, deadline=None):
            seen.append(cfg.url)
            return jev_scan.Verdict(probability=0.1)

        jev_scan.judge = fake_judge
        try:
            env = dict(self.env())
            env["ANTI_TANGENT_JEV_URL"] = "https://evil.example/v1/systemone"
            code, event, message = jev_scan.run(
                self.file, ["// A comment."], "// A comment.\n", env, "s1", self.dir)
        finally:
            jev_scan.judge = original
        self.assertEqual(seen, [jev_scan.DEFAULT_URL],
                          "the rejected override must never reach the request itself")
        self.assertEqual(code, 0)
        self.assertEqual(event, "jev-pass|blocks=1,url=untrusted-host",
                          "the silent fallback must show up in the trace even though "
                          "the write is allowed either way")


class _HookFixture(unittest.TestCase):
    """Shared plumbing for driving the comment-write hook against a loopback stub.

    Carries no test methods of its own: HookBody and Wrapper each drive a
    different entry point (the hook body directly, and the bash wrapper
    around it) and must inherit only this shared setup, not one another, or
    whichever one is the subclass ends up running the other's tests a second
    time under its own name without exercising anything new.
    """

    def setUp(self):
        self.dir = tempfile.mkdtemp()
        self.file = os.path.join(self.dir, "x.go")
        self.requests = os.path.join(self.dir, "requests.log")
        self.port = self.start_stub("0.95")

    def tearDown(self):
        self.stop_stub()
        shutil.rmtree(self.dir, ignore_errors=True)

    def start_stub(self, prob):
        self.stop_stub()
        stub = os.path.join(os.path.dirname(HOOKS), "evals", "jev-stub.py")
        self.stub = subprocess.Popen(
            [sys.executable, "-B", stub, "--prob", prob, "--log", self.requests],
            stdout=subprocess.PIPE, text=True)
        return int(self.stub.stdout.readline().strip())

    def stop_stub(self):
        # getattr with a default, not self.stub directly: a setUp that fails
        # before its own start_stub call still runs tearDown, which must not
        # raise AttributeError on top of whatever setUp already failed on.
        if getattr(self, "stub", None) is None:
            return
        self.stub.terminate()
        self.stub.wait(timeout=10)
        self.stub.stdout.close()
        self.stub = None

    def requests_made(self):
        if not os.path.exists(self.requests):
            return 0
        with open(self.requests) as fh:
            return len([l for l in fh if l.strip()])

    def _base_env(self):
        """The environment both entry points need, minus their own root variable.

        ANTI_TANGENT_TICKET_PATTERN is popped here rather than at each call
        site: it is read from the operator's own environment, and a value
        the test runner's shell happens to carry would otherwise change
        which lines the regex tier flags out from under these tests.
        """
        env = dict(os.environ)
        env.update({"ANTI_TANGENT_JEV": "1", "TYPESAFE_API_KEY": "k",
                    "ANTI_TANGENT_JEV_URL": "http://127.0.0.1:%d/v1/systemone" % self.port,
                    "ANTI_TANGENT_GUARD_TRACE_LOG": os.path.join(self.dir, "trace.log")})
        env.pop("ANTI_TANGENT_TICKET_PATTERN", None)
        return env


class HookBody(_HookFixture):
    """The real hook body, against a loopback stub — the only seam there is."""

    def run_body(self, content, session="s1", **env_over):
        with open(self.file, "w") as fh:
            fh.write("package x\n")
        payload = json.dumps({"tool_name": "Write", "session_id": session,
                              "tool_input": {"file_path": self.file, "content": content}})
        env = self._base_env()
        env["ATG_ROOT"] = os.path.dirname(HOOKS)
        env.update(env_over)
        return subprocess.run([sys.executable, "-I", "-B",
                               os.path.join(HOOKS, "check_comment_write.py")],
                              input=payload, capture_output=True, text=True,
                              env=env, timeout=30)

    def test_regex_violation_never_reaches_the_tier(self):
        r = self.run_body("// fixes #58\npackage x\n")
        self.assertEqual(r.returncode, 2)
        # The regex tier's own event, and no Jev event after it: the wrapper
        # needs the former to honour the status, and the latter would mean
        # the tier ran.
        self.assertEqual(r.stdout.strip(), "block|comment-hygiene")
        self.assertEqual(self.requests_made(), 0)
        strikes = [f for f in os.listdir(self.dir) if f.startswith("jev-strike-")]
        self.assertEqual(strikes, [], "the tier never ran, so it must not have written a strike")

    def test_pass_outcome_allows_the_write(self):
        self.stub.terminate()
        self.stub.wait(timeout=10)
        port = self.start_stub("0.05")
        r = self.run_body(
            "// The count cap used to return a plain error.\npackage x\n",
            ANTI_TANGENT_JEV_URL="http://127.0.0.1:%d/v1/systemone" % port)
        self.assertEqual(r.returncode, 0)
        self.assertEqual(r.stdout.strip(), "jev-pass|blocks=1")
        self.assertEqual(r.stderr.strip(), "")

    def test_disabled_tier_reports_why(self):
        r = self.run_body("// A plain comment.\n", ANTI_TANGENT_JEV="0")
        self.assertEqual(r.returncode, 0)
        self.assertTrue(r.stdout.startswith("jev-skip|setting"), r.stdout)
        self.assertEqual(self.requests_made(), 0)

    def test_flag_blocks_and_quotes_the_comment(self):
        r = self.run_body("// The count cap used to return a plain error.\npackage x\n")
        self.assertEqual(r.returncode, 4)
        self.assertIn("used to return a plain error", r.stderr)
        self.assertIn("0.95", r.stderr)
        self.assertTrue(r.stdout.startswith("jev-block|"), r.stdout)

    def test_third_strike_yields(self):
        body = "// The count cap used to return a plain error.\npackage x\n"
        for _ in range(2):
            self.run_body(body)
        r = self.run_body(body)
        self.assertEqual(r.returncode, 0)
        self.assertTrue(r.stdout.startswith("jev-yield|"), r.stdout)
        self.assertIn("task close", r.stderr)

    def test_a_different_session_starts_its_own_count(self):
        body = "// The count cap used to return a plain error.\npackage x\n"
        for _ in range(3):
            self.run_body(body, session="s1")
        r = self.run_body(body, session="s2")
        self.assertEqual(r.returncode, 4, "a fresh session must not inherit strikes")

    def test_an_unusable_strike_store_cannot_refuse_even_once(self):
        # The store lives beside the trace log. When the hook cannot write
        # there -- a path under a regular file, which fails for root too --
        # no attempt can be told from the one before it, so there is no
        # third attempt to yield on. Every attempt must yield, the first
        # included: the alternative is the same refusal on the 2nd, 3rd and
        # 10th attempt alike, with nothing the writer can do about it.
        blocker = os.path.join(self.dir, "not-a-dir")
        with open(blocker, "w") as fh:
            fh.write("")
        log = os.path.join(blocker, "hooks", "trace.log")
        body = "// The count cap used to return a plain error.\npackage x\n"
        for attempt in range(1, 11):
            r = self.run_body(body, ANTI_TANGENT_GUARD_TRACE_LOG=log)
            self.assertEqual(r.returncode, 0, "attempt %d refused the write" % attempt)
            self.assertEqual(r.stdout.strip(), "jev-yield|%s,untracked" % self.file,
                             "attempt %d must say the count is untracked" % attempt)
            self.assertIn("cannot record refusals", r.stderr)

    def test_failure_allows_warns_once_and_opens_the_breaker(self):
        # A port nothing listens on: the request fails without a stub in the way.
        dead = {"ANTI_TANGENT_JEV_URL": "http://127.0.0.1:9/v1/systemone"}
        first = self.run_body("// A plain comment.\n", **dead)
        self.assertEqual(first.returncode, 0)
        self.assertTrue(first.stdout.startswith("jev-error|"), first.stdout)
        detail = first.stdout.strip().split("|", 1)[1]
        self.assertEqual(first.stderr, jev_scan.WARN_MESSAGE % detail)

        before = self.requests_made()
        second = self.run_body("// The count cap used to return a plain error.\npackage x\n")
        self.assertEqual(second.returncode, 0)
        self.assertTrue(second.stdout.startswith("jev-skip|breaker"), second.stdout)
        self.assertEqual(self.requests_made(), before, "the breaker must stop the request")
        self.assertEqual(second.stderr.strip(), "", "only the first failure warns")


class Wrapper(_HookFixture):
    """The bash wrapper's own contract, driven through the real binary."""

    WRAPPER = os.path.join(HOOKS, "check-comment-write")

    def run_wrapper(self, content, extra_path="", **env_over):
        with open(self.file, "w") as fh:
            fh.write("package x\n")
        payload = json.dumps({"tool_name": "Write", "session_id": "w1",
                              "tool_input": {"file_path": self.file, "content": content}})
        env = self._base_env()
        env["CLAUDE_PLUGIN_ROOT"] = os.path.dirname(HOOKS)
        if extra_path:
            env["PATH"] = extra_path
        env.update(env_over)
        return subprocess.run([self.WRAPPER], input=payload, capture_output=True,
                              text=True, env=env, timeout=30)

    def trace(self):
        with open(os.path.join(self.dir, "trace.log")) as fh:
            return fh.read()

    def test_flag_maps_to_exit_two_with_its_probability(self):
        r = self.run_wrapper("// The count cap used to return a plain error.\npackage x\n")
        self.assertEqual(r.returncode, 2)
        self.assertEqual(r.stdout, "", "the body's event must not reach the transcript")
        self.assertIn("jev-block | p=0.95", self.trace())

    def test_pass_traces_the_reported_event(self):
        # A second stub, scoring below the threshold: the inherited setUp
        # starts one at 0.95, so a comment sent there flags and this test
        # would pass on the wrong outcome.
        self.port = self.start_stub("0.05")
        r = self.run_wrapper("// Returns nil when the file is absent.\npackage x\n")
        self.assertEqual(r.returncode, 0)
        self.assertEqual(r.stdout, "")
        self.assertIn("jev-pass", self.trace())

    def test_disabled_tier_traces_its_reason(self):
        r = self.run_wrapper("// A plain comment.\n", ANTI_TANGENT_JEV="0")
        self.assertEqual(r.returncode, 0)
        self.assertIn("jev-skip | setting", self.trace())

    def test_no_temp_files_are_left_behind(self):
        before = set(os.listdir(tempfile.gettempdir()))
        self.run_wrapper("// A plain comment.\npackage x\n")
        left = {n for n in set(os.listdir(tempfile.gettempdir())) - before
                if n.startswith("atg-comment-write.")}
        self.assertEqual(left, set())

    def shim_path(self, **scripts):
        """A PATH directory with every tool the wrapper needs, some replaced.

        Each keyword names a tool and gives the script that stands in for it;
        every other tool is a symlink to the real one. An empty PATH would
        not do: the wrapper checks for python3 first and would exit on that,
        never reaching the branch a test means to exercise. bash itself must
        be reachable too: the wrapper's shebang is `#!/usr/bin/env bash`, and
        /usr/bin/env resolves "bash" through the subprocess's own PATH (the
        kernel invokes env by its absolute path, but env's own lookup is not
        exempt from the replaced PATH).
        """
        shim = os.path.join(self.dir, "bin")
        os.makedirs(shim, exist_ok=True)
        for tool in ("bash", "python3", "jq", "cat", "tr", "mkdir", "date", "dirname",
                     "wc", "mv", "rm", "head", "printf", "seq", "sleep", "mktemp"):
            if tool in scripts:
                continue
            found = shutil.which(tool)
            if found:
                os.symlink(found, os.path.join(shim, tool))
        for tool, script in scripts.items():
            with open(os.path.join(shim, tool), "w") as fh:
                fh.write(script)
            os.chmod(os.path.join(shim, tool), 0o755)
        return shim

    def test_a_failed_mktemp_allows_the_write(self):
        shim = self.shim_path(mktemp="#!/bin/sh\nexit 1\n")
        r = self.run_wrapper("// The count cap used to return a plain error.\npackage x\n",
                             extra_path=shim)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("no-tmp", self.trace())

    def test_an_interpreter_that_cannot_open_the_body_allows_the_write(self):
        # python3 exits 2 on its own when it cannot open the script it was
        # handed -- the status the regex tier blocks with. The wrapper checks
        # the body is readable before starting the interpreter, so the body
        # is taken away in between: a python3 shim removes it and then execs
        # the real interpreter, which fails to open it. The content is a
        # genuine regex-tier violation, so an allow here can only mean the
        # wrapper read the bare status as the internal error it is, not as a
        # clean scan.
        root = os.path.join(self.dir, "plugin")
        os.makedirs(os.path.join(root, "hooks"))
        body = os.path.join(root, "hooks", "check_comment_write.py")
        shutil.copy(os.path.join(HOOKS, "check_comment_write.py"), body)
        shim = self.shim_path(python3="#!/bin/sh\nrm -f %s\nexec %s \"$@\"\n"
                              % (shlex.quote(body), shlex.quote(sys.executable)))
        r = self.run_wrapper("// fixes #58\npackage x\n", extra_path=shim,
                             CLAUDE_PLUGIN_ROOT=root)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(r.stdout, "")
        self.assertFalse(os.path.exists(body), "the shim must have removed the body")
        self.assertIn("| error | python-exit=2", self.trace())
        self.assertNotIn("| block |", self.trace())

    def test_hooks_json_declares_the_timeout(self):
        # Selected by command, not by position: the plugin registers more
        # than one PreToolUse matcher, and their order is not a contract.
        with open(os.path.join(HOOKS, "hooks.json")) as fh:
            hooks = json.load(fh)
        entries = [h for matcher in hooks["hooks"]["PreToolUse"]
                   for h in matcher["hooks"] if "check-comment-write" in h["command"]]
        self.assertEqual(len(entries), 1, "expected exactly one comment-write hook entry")
        self.assertEqual(entries[0]["timeout"], 10)


if __name__ == "__main__":
    unittest.main()
