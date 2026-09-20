import os
import sys
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


if __name__ == "__main__":
    unittest.main()
