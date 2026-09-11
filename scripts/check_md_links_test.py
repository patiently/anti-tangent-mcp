#!/usr/bin/env python3
"""Tests for check_md_links. Run: python3 -B scripts/check_md_links_test.py"""

import os
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import check_md_links as c  # noqa: E402


class SlugTest(unittest.TestCase):
    def slug(self, heading):
        return c.github_slug(c.rendered_text(c.heading_text(heading)))

    def test_github_slug_rules(self):
        cases = {
            "## Branch & Version Conventions": "branch--version-conventions",
            "### `validate_task_spec` arguments": "validate_task_spec-arguments",
            "## Companion: bm-scribe plugin (v0.7.1+)": "companion-bm-scribe-plugin-v071",
            "## 9. Personal namespace (`<USERNAME>/`)": "9-personal-namespace-username",
            "## See [the spec](docs/spec.md) first": "see-the-spec-first",
            "## Q&amp;A": "qa",
            "## Closing hashes ##": "closing-hashes",
            "## C#": "c",
            "## Café": "café",
        }
        for heading, want in cases.items():
            with self.subTest(heading=heading):
                self.assertEqual(self.slug(heading), want)

    def test_code_span_inside_link_text(self):
        self.assertEqual(self.slug("## See [`foo`](x.md)"), "see-foo")

    def test_link_syntax_inside_code_span_is_text(self):
        self.assertEqual(self.slug("## `[x](y)` literal"), "xy-literal")

    def test_underscore_emphasis(self):
        cases = {
            "## The _core_ loop": "the-core-loop",
            "## __init__": "init",
            "### Applying bm_commands to BM v0.21.1": "applying-bm_commands-to-bm-v0211",
            "## `_private_` field": "_private_-field",
        }
        for heading, want in cases.items():
            with self.subTest(heading=heading):
                self.assertEqual(self.slug(heading), want)

    def test_non_headings(self):
        for line in ("#hashtag", "    ## indented is code", "####### seven", "text # not a heading"):
            with self.subTest(line=line):
                self.assertIsNone(c.heading_text(line))


class CheckTest(unittest.TestCase):
    def errors(self, files):
        with tempfile.TemporaryDirectory() as root:
            for rel, body in files.items():
                path = os.path.join(root, rel)
                os.makedirs(os.path.dirname(path), exist_ok=True)
                with open(path, "w", encoding="utf-8") as fh:
                    fh.write(body)
            found, _ = c.check(root, sorted(p for p in files if p.endswith(".md")))
            return [msg for _, _, msg in found]

    def test_valid_links_and_anchors_pass(self):
        self.assertEqual(self.errors({
            "README.md": "# Intro\n[a](docs/core.md#setup)\n[b](#intro)\n[c](docs/core.md)\n"
                         "[d](https://example.com/x.md#nope)\n[e](mailto:a@b.c)\n",
            "docs/core.md": "## Setup\n",
        }), [])

    def test_missing_file_fails(self):
        errs = self.errors({"README.md": "[a](docs/missing.md)\n"})
        self.assertEqual(len(errs), 1)
        self.assertIn("broken relative link", errs[0])

    def test_unknown_anchor_in_another_file_fails(self):
        errs = self.errors({
            "README.md": "[demo](docs/protocol/core.md#this-anchor-does-not-exist-anywhere)\n",
            "docs/protocol/core.md": "# Core\n",
        })
        self.assertEqual(len(errs), 1)
        self.assertIn("broken anchor", errs[0])

    def test_unknown_same_document_anchor_fails(self):
        errs = self.errors({"README.md": "# Intro\n[a](#outro)\n"})
        self.assertEqual(len(errs), 1)
        self.assertIn("'outro'", errs[0])

    def test_repeated_headings_get_numbered_ids(self):
        errs = self.errors({"a.md": "## Dup\n## Dup\n[a](#dup)\n[b](#dup-1)\n[c](#dup-2)\n"})
        self.assertEqual(len(errs), 1)
        self.assertIn("'dup-2'", errs[0])

    def test_heading_inside_a_fence_is_not_an_anchor(self):
        errs = self.errors({"a.md": "```bash\n# setup\n```\n[a](#setup)\n"})
        self.assertEqual(len(errs), 1)
        self.assertIn("'setup'", errs[0])

    def test_fenced_heading_does_not_shift_numbering(self):
        self.assertEqual(self.errors({"a.md": "```\n## Dup\n```\n## Dup\n[a](#dup)\n"}), [])

    def test_link_inside_a_fence_is_not_checked(self):
        self.assertEqual(self.errors({"a.md": "```md\n[x](missing.md)\n```\n"}), [])

    def test_tildes_do_not_close_a_backtick_fence(self):
        errs = self.errors({"a.md": "```\n~~~\n# not-a-heading\n```\n[a](#not-a-heading)\n"})
        self.assertEqual(len(errs), 1)
        self.assertIn("broken anchor", errs[0])

    def test_shorter_run_does_not_close_a_fence(self):
        errs = self.errors({"a.md": "````\n```\n# inside\n````\n[a](#inside)\n"})
        self.assertEqual(len(errs), 1)
        self.assertIn("broken anchor", errs[0])

    def test_deeply_indented_run_does_not_close_a_fence(self):
        errs = self.errors({"a.md": "```\n    ```\n# fake\n```\n[a](#fake)\n"})
        self.assertEqual(len(errs), 1)
        self.assertIn("'fake'", errs[0])

    def test_fence_inside_a_list_item(self):
        errs = self.errors({
            "a.md": "1. Step\n\n   ```bash\n   # comment\n   ```\n\n## Real\n[a](#real)\n[b](#comment)\n",
        })
        self.assertEqual(len(errs), 1)
        self.assertIn("'comment'", errs[0])

    def test_unclosed_fence_fails(self):
        errs = self.errors({"a.md": "text\n```\n[x](missing.md)\n"})
        self.assertEqual(len(errs), 1)
        self.assertIn("never closed", errs[0])

    def test_inline_triple_backticks_are_not_a_fence(self):
        self.assertEqual(self.errors({"a.md": "```code``` here\n## After\n[a](#after)\n"}), [])

    def test_nothing_inside_an_html_comment_is_an_anchor(self):
        errs = self.errors({
            "a.md": "<!--\n## Hidden\n-->\n## Shown <!-- note -->\n<!-- ## Also hidden -->\n"
                    '<!-- <a id="ghost"></a> -->\n'
                    "[a](#shown)\n[b](#hidden)\n[c](#also-hidden)\n[d](#ghost)\n",
        })
        self.assertEqual(len(errs), 3)
        for frag in ("'hidden'", "'also-hidden'", "'ghost'"):
            self.assertTrue(any(frag in e for e in errs), frag)

    def test_link_inside_an_html_comment_is_not_checked(self):
        self.assertEqual(self.errors({"a.md": "<!-- [x](missing.md) -->\n<!--\n[y](missing.md)\n-->\n"}), [])

    def test_unclosed_html_comment_fails(self):
        errs = self.errors({"a.md": "text\n<!--\n[x](missing.md)\n"})
        self.assertEqual(len(errs), 1)
        self.assertIn("never closed", errs[0])

    def test_comment_opener_inside_a_code_span_is_text(self):
        self.assertEqual(self.errors({"a.md": "Use `<!--` to open one.\n## After\n[a](#after)\n"}), [])

    def test_link_inside_a_code_span_is_not_checked(self):
        self.assertEqual(self.errors({"a.md": "Write `[text](docs/example.md)` to link.\n"}), [])

    def test_links_beside_or_around_a_code_span_are_checked(self):
        errs = self.errors({"a.md": "`code` then [x](missing.md)\n[`foo`](gone.md)\n"})
        self.assertEqual(len(errs), 2)
        self.assertTrue(any("missing.md" in e for e in errs))
        self.assertTrue(any("gone.md" in e for e in errs))

    def test_html_anchor_inside_a_code_span_is_not_an_anchor(self):
        errs = self.errors({"a.md": 'Use `<a id="x"></a>` for one.\n[a](#x)\n'})
        self.assertEqual(len(errs), 1)
        self.assertIn("'x'", errs[0])

    def test_explicit_html_anchor(self):
        self.assertEqual(self.errors({"a.md": '<a id="custom"></a>\n[a](#custom)\n'}), [])

    def test_root_relative_path(self):
        self.assertEqual(self.errors({"docs/a.md": "[x](/README.md)\n", "README.md": ""}), [])

    def test_fragment_on_a_non_markdown_target_is_not_checked(self):
        self.assertEqual(self.errors({"a.md": "[x](main.go#L10)\n", "main.go": ""}), [])

    def test_title_and_angle_bracket_targets(self):
        self.assertEqual(self.errors({
            "a.md": '[x](b.md "Title")\n[y](<b c.md>)\n',
            "b.md": "", "b c.md": "",
        }), [])


class ScopeTest(unittest.TestCase):
    def test_scope(self):
        cases = {
            "README.md": True,
            "CLAUDE.md": True,
            "docs/team-setup/codescene-stats.md": True,
            "plugin/anti-tangent-guard/README.md": True,
            "docs/superpowers/plans/2026-09-10-anti-tangent-v0.20.0.md": False,
            "examples/project-knowledge/epic.md": False,
            "examples/project-knowledge/dogfood/epics/gh-23/main.md": False,
            "examples/project-knowledge/README.md": True,
        }
        for path, want in cases.items():
            with self.subTest(path=path):
                self.assertEqual(c.in_scope(path), want)


if __name__ == "__main__":
    unittest.main()
