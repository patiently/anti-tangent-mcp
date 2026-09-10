import json
import os
import subprocess
import sys
import tempfile
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


class IndeterminateCheckIgnore(unittest.TestCase):
    # ls-files says "unmatched" and check-ignore then fails to answer. The
    # path must be SKIPPED: reading an unanswerable status as "not ignored"
    # would scan every line of a file the hook never classified.
    def test_unanswerable_check_ignore_skips_the_path(self):
        sys.path.insert(0, HOOKS)
        import git_added_lines as g

        calls = []
        real_git = g._git

        def fake_git(cwd, *args):
            calls.append(args[0])
            if args[0] == "ls-files":
                return 1, ""       # unmatched
            if args[0] == "check-ignore":
                return 128, ""     # could not answer
            raise AssertionError("unexpected git call: %r" % (args,))

        # The helper skips a path whose PARENT does not exist before it ever
        # reaches git, so the fixture needs a real directory or this test
        # passes without exercising the branch at all.
        tmp = tempfile.mkdtemp()
        path = os.path.join(tmp, "y.go")
        with open(path, "w") as fh:
            fh.write("// fixes #1\n")
        g._git = fake_git
        try:
            out = g.final_files_added_lines({"final_files": [{"path": path}]})
        finally:
            g._git = real_git
        self.assertEqual(out, {}, "an unanswerable check-ignore must skip, not scan")
        self.assertEqual(calls, ["ls-files", "check-ignore"],
                         "no diff or content read may follow an unanswerable status")


class VendoredUntrackedPath(unittest.TestCase):
    # Every line of an untracked file counts as added, so a vendored source
    # file whose header narrates its own upstream history would block the
    # close and demand a rewrite of code this repository does not own. The
    # exemption is by directory name and applies only to the untracked
    # branch: a tracked file under vendor/ diffs to the lines someone here
    # actually changed, and those are fair game.
    def test_untracked_vendored_path_is_not_scanned(self):
        sys.path.insert(0, HOOKS)
        import git_added_lines as g

        real_git = g._git

        def fake_git(cwd, *args):
            if args[0] == "ls-files":
                return 1, ""       # unmatched -> untracked
            if args[0] == "check-ignore":
                return 1, ""       # definitely not ignored
            raise AssertionError("unexpected git call: %r" % (args,))

        tmp = tempfile.mkdtemp()
        vendored_dir = os.path.join(tmp, "vendor", "lib")
        os.makedirs(vendored_dir)
        vendored = os.path.join(vendored_dir, "u.go")
        own = os.path.join(tmp, "own.go")
        for p in (vendored, own):
            with open(p, "w") as fh:
                fh.write("// fixes #1\npackage x\n")
        g._git = fake_git
        try:
            out = g.final_files_added_lines(
                {"final_files": [{"path": vendored}, {"path": own}]})
        finally:
            g._git = real_git
        self.assertEqual(sorted(out), [own],
                         "an untracked path under vendor/ must be skipped, and one "
                         "outside it must still be scanned")


if __name__ == "__main__":
    unittest.main()
