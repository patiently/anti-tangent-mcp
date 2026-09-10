import json
import os
import subprocess
import sys
import unittest

HOOKS = os.path.dirname(os.path.abspath(__file__))


def _scan(line, pattern=None, path="X.kt"):
    """Run violations() in a fresh interpreter with a controlled environment."""
    code = ("import sys; sys.path.insert(0, %r);"
            "from comment_scan import violations;"
            "print(bool(violations(%r, [%r])))" % (HOOKS, path, line))
    env = dict(os.environ)
    env.pop("ANTI_TANGENT_TICKET_PATTERN", None)
    if pattern is not None:
        env["ANTI_TANGENT_TICKET_PATTERN"] = pattern
    r = subprocess.run([sys.executable, "-c", code], capture_output=True,
                       text=True, env=env, timeout=30)
    if r.returncode != 0:
        raise AssertionError(r.stderr)
    return r.stdout.strip() == "True"


class CommentSpanExtraction(unittest.TestCase):
    # The tracker-key line from the field report. No tell matches it without a
    # configured pattern, so a violations() assertion cannot show it was
    # scanned at all -- only comment_spans can, which is why this asserts the
    # extracted text rather than the outcome.
    def test_kdoc_continuation_text_is_extracted(self):
        code = ("import sys, json; sys.path.insert(0, %r);"
                "from comment_scan import comment_spans;"
                "print(json.dumps(comment_spans('X.kt',"
                " ' * ABC-1234: the inbound HELP keyword.')))" % HOOKS)
        r = subprocess.run([sys.executable, "-c", code], capture_output=True,
                           text=True, timeout=30)
        self.assertEqual(json.loads(r.stdout), [" ABC-1234: the inbound HELP keyword."],
                         r.stderr)

    def test_terminator_line_yields_no_span(self):
        code = ("import sys, json; sys.path.insert(0, %r);"
                "from comment_scan import comment_spans;"
                "print(json.dumps(comment_spans('X.c', '*/ *p = task-42;')))" % HOOKS)
        r = subprocess.run([sys.executable, "-c", code], capture_output=True,
                           text=True, timeout=30)
        self.assertEqual(json.loads(r.stdout), [], r.stderr)


class TicketPatternLength(unittest.TestCase):
    # Exactly at the cap is accepted, one character more is refused. Both
    # patterns match the same line, so length is the only variable. The
    # alternation keeps the padded form compilable and still matching.
    AT_CAP = "ABC-\\d+|" + "Z" * 192

    def test_at_cap_is_accepted(self):
        self.assertEqual(len(self.AT_CAP), 200)
        self.assertTrue(_scan("// ABC-1234: x", self.AT_CAP))

    def test_one_over_cap_is_refused(self):
        over = self.AT_CAP + "Z"
        self.assertEqual(len(over), 201)
        self.assertFalse(_scan("// ABC-1234: x", over))


class NonMainThread(unittest.TestCase):
    # SIGALRM exists on Unix in every thread, but signal.signal raises outside
    # the main one. The deadline must degrade to an unbounded scan; were that
    # error to reach violations()'s fail-open handler the scanner would go
    # silently blind in any threaded caller.
    def test_scans_from_a_worker_thread(self):
        code = ("import sys, threading; sys.path.insert(0, %r);"
                "from comment_scan import violations;"
                "r = [];"
                "t = threading.Thread(target=lambda: r.append("
                "violations('X.go', ['// fixes task-42'])));"
                "t.start(); t.join(); print(bool(r and r[0]))" % HOOKS)
        r = subprocess.run([sys.executable, "-c", code], capture_output=True,
                           text=True, timeout=30)
        self.assertEqual(r.stdout.strip(), "True", r.stderr)


if __name__ == "__main__":
    unittest.main()
