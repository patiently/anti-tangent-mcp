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


if __name__ == "__main__":
    unittest.main()
