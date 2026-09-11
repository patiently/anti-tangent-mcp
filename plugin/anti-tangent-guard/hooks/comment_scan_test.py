import json
import os
import subprocess
import sys
import tempfile
import time
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
    #
    # The checkout itself lives under a directory named in the exemption
    # list, which is the point: a repository cloned inside third_party/ (or a
    # CI workspace under node_modules/) must still have its own files
    # scanned. Matching the absolute path would exempt every untracked file
    # in the repository, silently, since a skipped path never reaches the
    # caller to be counted.
    def test_untracked_vendored_path_is_not_scanned(self):
        sys.path.insert(0, HOOKS)
        import git_added_lines as g

        tmp = tempfile.mkdtemp()
        root = os.path.join(tmp, "third_party", "checkout")
        vendored_dir = os.path.join(root, "vendor", "lib")
        os.makedirs(vendored_dir)
        vendored = os.path.join(vendored_dir, "u.go")
        own = os.path.join(root, "own.go")
        for p in (vendored, own):
            with open(p, "w") as fh:
                fh.write("// fixes #1\npackage x\n")

        real_git = g._git

        def fake_git(cwd, *args):
            if args[0] == "ls-files":
                return 1, ""       # unmatched -> untracked
            if args[0] == "check-ignore":
                return 1, ""       # definitely not ignored
            if args[0] == "rev-parse":
                return 0, root + "\n"
            raise AssertionError("unexpected git call: %r" % (args,))

        g._git = fake_git
        try:
            out = g.final_files_added_lines(
                {"final_files": [{"path": vendored}, {"path": own}]})
        finally:
            g._git = real_git
        self.assertEqual(sorted(out), [own],
                         "an untracked path under vendor/ must be skipped, and one "
                         "outside it must still be scanned")


class WalkBudgetIsReported(unittest.TestCase):
    # The walk returns a plain dict either way, so a budget that stopped it
    # early looks exactly like a completion whose files were all committed.
    # The caller traces one and blocks on neither, so the difference has to
    # come out of the stats argument or it is lost.
    def test_exhausted_budget_is_reported_and_a_finished_walk_is_not(self):
        sys.path.insert(0, HOOKS)
        import git_added_lines as g

        tmp = tempfile.mkdtemp()
        paths = []
        for name in ("a.go", "b.go"):
            p = os.path.join(tmp, name)
            with open(p, "w") as fh:
                fh.write("// fixes #1\npackage x\n")
            paths.append(p)
        inp = {"final_files": [{"path": p} for p in paths]}

        real_git = g._git

        def fake_git(cwd, *args):
            if args[0] == "ls-files":
                return 1, ""
            if args[0] == "check-ignore":
                return 1, ""
            if args[0] == "rev-parse":
                return 0, tmp + "\n"
            raise AssertionError("unexpected git call: %r" % (args,))

        g._git = fake_git
        try:
            spent = {}
            spent_out = g.final_files_added_lines(
                inp, deadline=time.monotonic() - 1.0, stats=spent)
            fresh = {}
            fresh_out = g.final_files_added_lines(
                inp, deadline=time.monotonic() + 60.0, stats=fresh)
        finally:
            g._git = real_git

        self.assertEqual(spent_out, {}, "an exhausted budget scans nothing")
        self.assertTrue(spent.get("truncated"),
                        "a walk stopped by its budget must say so")
        self.assertEqual(sorted(fresh_out), sorted(paths))
        self.assertFalse(fresh.get("truncated"),
                         "a walk that finished must not claim it was cut short")


class DroppedOptionalLocksIsReported(unittest.TestCase):
    # git answering 129 to a command line carrying --no-optional-locks makes
    # the walk drop the flag for the rest of the process, so every call after
    # that refreshes the index this walk promised to leave alone. Nothing in
    # the returned dict moves, so the degraded run reads exactly like a
    # healthy one unless the stats argument carries it.
    def test_a_usage_error_drops_the_flag_and_says_so(self):
        sys.path.insert(0, HOOKS)
        import git_added_lines as g

        tmp = tempfile.mkdtemp()
        path = os.path.join(tmp, "a.go")
        with open(path, "w") as fh:
            fh.write("// fixes #1\npackage x\n")
        inp = {"final_files": [{"path": path}]}

        class Completed(object):
            def __init__(self, returncode, stdout):
                self.returncode, self.stdout = returncode, stdout

        class Shim(object):
            def __init__(self, run):
                self.run = run

        def answer(argv):
            if "ls-files" in argv:
                return Completed(1, "")            # unmatched -> untracked
            if "check-ignore" in argv:
                return Completed(1, "")            # definitely not ignored
            if "rev-parse" in argv:
                return Completed(0, tmp + "\n")
            return Completed(128, "")

        def modern_git(argv, **kwargs):
            return answer(argv)

        def ancient_git(argv, **kwargs):
            if "--no-optional-locks" in argv:
                return Completed(129, "")          # rejects the whole command line
            return answer(argv)

        real_subprocess = g.subprocess
        held = list(g._LOCK_FLAG)
        try:
            g.subprocess = Shim(modern_git)
            g._LOCK_FLAG[:] = held
            healthy = {}
            g.final_files_added_lines(inp, stats=healthy)

            g.subprocess = Shim(ancient_git)
            g._LOCK_FLAG[:] = held
            degraded = {}
            g.final_files_added_lines(inp, stats=degraded)
        finally:
            g.subprocess = real_subprocess
            g._LOCK_FLAG[:] = held

        self.assertFalse(healthy.get("optional_locks_dropped"),
                         "a git that accepted the flag must not be reported degraded")
        self.assertTrue(degraded.get("optional_locks_dropped"),
                        "a walk that gave up the flag must say so")


class LazyFetchCannotExec(unittest.TestCase):
    # A blob missing from the object store sends read-only git commands down a
    # partial-clone lazy fetch, and the fetch execs the configured transport.
    # That is a second, independent way for a repository's own config to run a
    # command inside this hook, and no amount of filter pinning closes it.
    def test_a_missing_blob_in_a_partial_clone_runs_no_command(self):
        sys.path.insert(0, HOOKS)
        import git_added_lines as g

        tmp = tempfile.mkdtemp()
        sentinel = os.path.join(tmp, "SENTINEL")
        ssh = os.path.join(tmp, "ssh.sh")
        with open(ssh, "w") as fh:
            fh.write("#!/bin/sh\necho fired >> %s\nexit 1\n" % sentinel)
        os.chmod(ssh, 0o755)

        repo = os.path.join(tmp, "r")
        os.makedirs(repo)

        def git(*args):
            subprocess.run(["git", "-C", repo] + list(args),
                           capture_output=True, timeout=30)

        git("init", "-q", ".")
        git("config", "user.email", "t@t")
        git("config", "user.name", "t")
        with open(os.path.join(repo, "f.go"), "w") as fh:
            fh.write("package x\n")
        git("add", "f.go")
        git("commit", "-qm", "i")
        blob = subprocess.run(["git", "-C", repo, "rev-parse", "HEAD:f.go"],
                              capture_output=True, text=True,
                              timeout=30).stdout.strip()
        git("config", "core.repositoryFormatVersion", "1")
        git("config", "extensions.partialClone", "origin")
        git("config", "remote.origin.promisor", "true")
        git("config", "remote.origin.url", "ssh://evil.example/x.git")
        git("config", "core.sshCommand", ssh)
        os.remove(os.path.join(repo, ".git", "objects", blob[:2], blob[2:]))

        rc, out = g._git_bytes(repo, "cat-file", "blob", "HEAD:f.go")
        self.assertNotEqual(rc, 0, "the blob is gone, so this must not succeed")
        self.assertFalse(
            os.path.exists(sentinel),
            "a missing blob must not be allowed to exec the configured transport")


def _repo(tmp, name="r", attrs=None, configs=(), tracked=("f.go", "package x\n"),
          worktree=None):
    """Build a scratch repo; return (repo_path, sentinel_path). Filters armed."""
    sentinel = os.path.join(tmp, "SENTINEL")
    evil = os.path.join(tmp, "evil.sh")
    if not os.path.exists(evil):
        with open(evil, "w") as fh:
            fh.write("#!/bin/sh\necho fired >> %s\n" % sentinel)
        os.chmod(evil, 0o755)
    repo = os.path.join(tmp, name)
    os.makedirs(repo)

    def git(*args):
        subprocess.run(["git", "-C", repo] + list(args),
                       capture_output=True, timeout=30)

    git("init", "-q", ".")
    git("config", "user.email", "t@t")
    git("config", "user.name", "t")
    if attrs:
        with open(os.path.join(repo, ".gitattributes"), "w") as fh:
            fh.write(attrs)
        git("add", ".gitattributes")
        git("commit", "-qm", "attrs")
    name_, body = tracked
    # `tracked` may name a path below the root, which is the shape that
    # separates a cwd-relative probe from a root-relative one.
    target = os.path.join(repo, *name_.split("/"))
    os.makedirs(os.path.dirname(target), exist_ok=True)
    with open(target, "wb") as fh:
        fh.write(body if isinstance(body, bytes) else body.encode("utf-8"))
    git("add", name_)
    git("commit", "-qm", "init")
    for key in configs:
        git("config", key, evil + " " + key)
    if worktree is not None:
        with open(target, "wb") as fh:
            fh.write(worktree if isinstance(worktree, bytes)
                     else worktree.encode("utf-8"))
    return repo, sentinel


class ContentFiltersAreNeverRun(unittest.TestCase):
    # A repository's own config can name the command git runs to convert a
    # worktree file, and this hook must never be the thing that runs it. One
    # fixture per key: with process and clean both set only process fires, so
    # a single fixture arming both would leave clean unexercised.
    def _case(self, key):
        sys.path.insert(0, HOOKS)
        import git_added_lines as g
        tmp = tempfile.mkdtemp()
        repo, sentinel = _repo(
            tmp, attrs="*.go filter=x\n", configs=(key,),
            tracked=("f.go", "package x\n"),
            worktree="package x\nfunc F() {}\n")
        path = os.path.join(repo, "f.go")
        out = g.final_files_added_lines({"final_files": [{"path": path}]})
        # The attribute probe skips this path before any comparison, because a
        # filtered HEAD blob is a pointer or ciphertext and is not comparable
        # to worktree content without running the filter.
        self.assertEqual(out, {}, "a filter-attributed path must be skipped")
        self.assertFalse(os.path.exists(sentinel),
                         "%s must never be executed from inside this hook" % key)

    def test_filter_process_is_not_run(self):
        self._case("filter.x.process")

    def test_filter_clean_is_not_run(self):
        self._case("filter.x.clean")


class BlobReadConvertsNothingWithoutTheSkip(unittest.TestCase):
    # The attribute probe skips filter-attributed paths before the comparison
    # runs, so no fixture can reach the blob read by the ordinary route. This
    # pins the underlying property directly: with the probe forced open, the
    # raw blob read still executes nothing.
    def test_cat_file_runs_no_filter_even_when_the_probe_is_bypassed(self):
        sys.path.insert(0, HOOKS)
        import git_added_lines as g
        tmp = tempfile.mkdtemp()
        repo, sentinel = _repo(
            tmp, attrs="*.go filter=x\n", configs=("filter.x.clean",),
            tracked=("f.go", "package x\n"),
            worktree="package x\nfunc F() {}\n")
        path = os.path.join(repo, "f.go")
        real = g._converted
        g._converted = lambda parent, rel: (False, None)
        try:
            out = g.final_files_added_lines({"final_files": [{"path": path}]})
        finally:
            g._converted = real
        self.assertEqual(out.get(path), ["func F() {}"])
        self.assertFalse(os.path.exists(sentinel),
                         "the raw blob read must convert nothing on its own")


class UndecodableHeadBlob(unittest.TestCase):
    # A blob is under no obligation to be valid UTF-8, and subprocess's text
    # mode raises on the first byte that is not -- which a caller sees only as
    # a failure indistinguishable from git refusing the command. Reading the
    # blob as bytes and decoding BOTH sides with errors="replace" keeps the
    # two comparable: the offending byte becomes the same replacement
    # character on each side, contributes no difference of its own, and only
    # the genuinely added line is reported.
    def test_a_latin1_byte_does_not_widen_the_scan(self):
        sys.path.insert(0, HOOKS)
        import git_added_lines as g
        tmp = tempfile.mkdtemp()
        repo, _ = _repo(
            tmp,
            tracked=("f.go", b"package x\n// caf\xe9 fixes #1 old\n"),
            worktree=b"package x\n// caf\xe9 fixes #1 old\nfunc F() {}\n")
        path = os.path.join(repo, "f.go")
        out = g.final_files_added_lines({"final_files": [{"path": path}]})
        self.assertEqual(out.get(path), ["func F() {}"],
                         "only the added line may be reported")


class StagedNewFile(unittest.TestCase):
    def test_a_tracked_file_absent_from_head_is_whole_file(self):
        sys.path.insert(0, HOOKS)
        import git_added_lines as g
        tmp = tempfile.mkdtemp()
        repo, _ = _repo(tmp)
        new = os.path.join(repo, "new.go")
        with open(new, "w") as fh:
            fh.write("// fixes #1\npackage y\n")
        subprocess.run(["git", "-C", repo, "add", "new.go"],
                       capture_output=True, timeout=30)
        out = g.final_files_added_lines({"final_files": [{"path": new}]})
        self.assertEqual(out.get(new), ["// fixes #1", "package y"])

    def test_a_staged_new_vendored_file_is_skipped(self):
        sys.path.insert(0, HOOKS)
        import git_added_lines as g
        tmp = tempfile.mkdtemp()
        repo, _ = _repo(tmp)
        vend = os.path.join(repo, "vendor", "lib")
        os.makedirs(vend)
        new = os.path.join(vend, "u.go")
        with open(new, "w") as fh:
            fh.write("// added in v1.2.3 upstream\npackage lib\n")
        subprocess.run(["git", "-C", repo, "add", "-A"],
                       capture_output=True, timeout=30)
        stats = {}
        out = g.final_files_added_lines({"final_files": [{"path": new}]},
                                        stats=stats)
        self.assertEqual(out, {})
        self.assertEqual(stats["vendored_skipped"], 1)


class NoCommitsYet(unittest.TestCase):
    def test_a_repository_with_no_head_skips_the_path(self):
        sys.path.insert(0, HOOKS)
        import git_added_lines as g
        tmp = tempfile.mkdtemp()
        repo = os.path.join(tmp, "fresh")
        os.makedirs(repo)
        subprocess.run(["git", "-C", repo, "init", "-q", "."],
                       capture_output=True, timeout=30)
        path = os.path.join(repo, "f.go")
        with open(path, "w") as fh:
            fh.write("// fixes #1\n")
        subprocess.run(["git", "-C", repo, "add", "f.go"],
                       capture_output=True, timeout=30)
        out = g.final_files_added_lines({"final_files": [{"path": path}]})
        self.assertEqual(out, {}, "no HEAD means the question is unanswerable")


class HeadProbedOncePerRoot(unittest.TestCase):
    # Two files in ONE worktree, submitted from different directories. Keying
    # the cache on the directory asked from would probe HEAD twice for the
    # same repository.
    def test_one_worktree_is_probed_once(self):
        sys.path.insert(0, HOOKS)
        import git_added_lines as g
        tmp = tempfile.mkdtemp()
        repo, _ = _repo(tmp, tracked=("a.go", "package x\n"))
        sub = os.path.join(repo, "sub")
        os.makedirs(sub)
        second = os.path.join(sub, "b.go")
        with open(second, "w") as fh:
            fh.write("package y\n")
        subprocess.run(["git", "-C", repo, "add", "-A"], capture_output=True, timeout=30)
        subprocess.run(["git", "-C", repo, "commit", "-qm", "two"], capture_output=True, timeout=30)
        with open(os.path.join(repo, "a.go"), "a") as fh:
            fh.write("// fixes #1\n")
        with open(second, "a") as fh:
            fh.write("// fixes #2\n")

        head_probes = []
        real = g._git

        def counting_git(cwd, *args):
            if args[:3] == ("rev-parse", "--verify", "--quiet") and args[3] == "HEAD":
                head_probes.append(cwd)
            return real(cwd, *args)

        g._git = counting_git
        try:
            out = g.final_files_added_lines(
                {"final_files": [{"path": os.path.join(repo, "a.go")},
                                 {"path": second}]})
        finally:
            g._git = real
        self.assertEqual(len(out), 2, "both files should be scanned")
        self.assertEqual(len(head_probes), 1,
                         "one worktree must be probed once, not once per directory")


class SymlinkedWorktreeRoot(unittest.TestCase):
    # --show-toplevel prints the realpath while the submitted path does not,
    # so a relative path computed from the two is wrong the moment any parent
    # is a symlink. ls-files --full-name answers it correctly.
    def test_a_symlinked_root_still_resolves(self):
        sys.path.insert(0, HOOKS)
        import git_added_lines as g
        tmp = tempfile.mkdtemp()
        repo, _ = _repo(tmp, name="real",
                        tracked=("f.go", "package x\n"),
                        worktree="package x\n// fixes #1\n")
        link = os.path.join(tmp, "link")
        os.symlink(repo, link)
        path = os.path.join(link, "f.go")
        out = g.final_files_added_lines({"final_files": [{"path": path}]})
        self.assertEqual(out.get(path), ["// fixes #1"])


class UnanswerableCheckAttr(unittest.TestCase):
    def test_a_check_attr_that_cannot_answer_skips_the_path(self):
        sys.path.insert(0, HOOKS)
        import git_added_lines as g
        calls = []
        real = g._git

        def fake_git(cwd, *args):
            calls.append(args[0])
            if args[0] == "ls-files":
                return 0, "f.go\0"
            if args[0] == "check-attr":
                return 128, ""
            raise AssertionError("nothing may follow an unanswerable check-attr: %r" % (args,))

        tmp = tempfile.mkdtemp()
        path = os.path.join(tmp, "f.go")
        with open(path, "w") as fh:
            fh.write("// fixes #1\n")
        g._git = fake_git
        try:
            out = g.final_files_added_lines({"final_files": [{"path": path}]})
        finally:
            g._git = real
        self.assertEqual(out, {})
        self.assertEqual(calls, ["ls-files", "check-attr"])


class ConvertedContent(unittest.TestCase):
    # A filter driver converts between the HEAD blob and the worktree form, so
    # a filtered file holds a pointer or ciphertext in HEAD against content on
    # disk: comparing the two directly reads as a wholly new file. An encoding
    # difference is the worse of the two, because it reads as no findings at
    # all and the scan still looks like it ran.
    def test_a_filtered_path_is_skipped_not_scanned_whole(self):
        sys.path.insert(0, HOOKS)
        import git_added_lines as g
        tmp = tempfile.mkdtemp()
        repo, sentinel = _repo(
            tmp, attrs="*.js filter=x\n", configs=("filter.x.clean",),
            tracked=("f.js", "version https://example/lfs\noid sha256:dead\n"),
            worktree="// bundle header: fixes #1234 upstream\nconsole.log(1);\n")
        path = os.path.join(repo, "f.js")
        out = g.final_files_added_lines({"final_files": [{"path": path}]})
        self.assertEqual(out, {}, "a filtered path must be skipped")
        self.assertFalse(os.path.exists(sentinel))

    def test_working_tree_encoding_is_decoded_not_skipped(self):
        sys.path.insert(0, HOOKS)
        import git_added_lines as g
        tmp = tempfile.mkdtemp()
        repo = os.path.join(tmp, "enc")
        os.makedirs(repo)

        def git(*args):
            subprocess.run(["git", "-C", repo] + list(args),
                           capture_output=True, timeout=30)

        git("init", "-q", ".")
        git("config", "user.email", "t@t")
        git("config", "user.name", "t")
        with open(os.path.join(repo, ".gitattributes"), "w") as fh:
            fh.write("*.go working-tree-encoding=UTF-16\n")
        git("add", ".gitattributes")
        git("commit", "-qm", "attrs")
        path = os.path.join(repo, "f.go")
        with open(path, "wb") as fh:
            fh.write("package x\n".encode("utf-16"))
        git("add", "f.go")
        git("commit", "-qm", "init")
        with open(path, "wb") as fh:
            fh.write("package x\n// fixes #1\n".encode("utf-16"))

        out = g.final_files_added_lines({"final_files": [{"path": path}]})
        self.assertEqual(out.get(path), ["// fixes #1"],
                         "the declared codec must be used, not skipped")


class ConvertedContentBelowTheRoot(unittest.TestCase):
    # The attribute probe runs in the file's OWN directory, and check-attr
    # resolves a pathname against the current directory. A repository-relative
    # name therefore asks about <dir>/<rel>, a path that exists for no file
    # below the root, and git answers "unspecified" for every attribute of it
    # -- so the ciphertext in HEAD is compared against plaintext on disk and
    # every line of the file reads as added.
    #
    # Two properties are needed to see it, and either alone hides it: the file
    # must sit BELOW the root, and the .gitattributes pattern must be ANCHORED
    # to that path. An unanchored `*.go` matches the misresolved pathname just
    # as well, and a file at the root has no misresolution to expose.
    def test_a_filtered_path_below_the_root_is_skipped(self):
        sys.path.insert(0, HOOKS)
        import git_added_lines as g
        tmp = tempfile.mkdtemp()
        repo, sentinel = _repo(
            tmp, attrs="a/b/x.go filter=x\n", configs=("filter.x.clean",),
            tracked=("a/b/x.go", "ENCRYPTEDGIBBERISH\n"),
            worktree="package x\n// fixes #123, added in v1.2.3\nfunc F() {}\n")
        path = os.path.join(repo, "a", "b", "x.go")
        out = g.final_files_added_lines({"final_files": [{"path": path}]})
        self.assertEqual(out, {},
                         "a filtered path below the root must be skipped")
        self.assertFalse(os.path.exists(sentinel))


class UnreadableTree(unittest.TestCase):
    # The HEAD-membership probe has to tell "not in HEAD" apart from "the tree
    # could not be read": the first licenses scanning the whole file as new,
    # the second must skip. A deleted tree object is the cheapest way to
    # produce the second, and it is indistinguishable from the first to any
    # probe that reports both as a plain failure.
    def test_a_missing_tree_object_skips_the_path(self):
        sys.path.insert(0, HOOKS)
        import git_added_lines as g
        tmp = tempfile.mkdtemp()
        repo, _ = _repo(tmp, tracked=("a/b/f.go", "package x\n"),
                        worktree="package x\n// fixes #1\n")
        oid = subprocess.run(["git", "-C", repo, "rev-parse", "HEAD:a"],
                             capture_output=True, text=True,
                             timeout=30).stdout.strip()
        os.remove(os.path.join(repo, ".git", "objects", oid[:2], oid[2:]))
        path = os.path.join(repo, "a", "b", "f.go")
        out = g.final_files_added_lines({"final_files": [{"path": path}]})
        self.assertEqual(out, {},
                         "an unreadable tree must skip, not report a new file")


class ReadTextCappedEncoding(unittest.TestCase):
    def test_named_codec_is_used(self):
        sys.path.insert(0, HOOKS)
        from comment_scan import read_text_capped
        tmp = tempfile.mkdtemp()
        p = os.path.join(tmp, "a.txt")
        with open(p, "wb") as fh:
            fh.write("hi\n".encode("utf-16"))
        self.assertEqual(read_text_capped(p, "UTF-16"), "hi\n")

    def test_unknown_codec_is_unusable(self):
        sys.path.insert(0, HOOKS)
        from comment_scan import read_text_capped
        tmp = tempfile.mkdtemp()
        p = os.path.join(tmp, "b.txt")
        with open(p, "w") as fh:
            fh.write("hi\n")
        self.assertIsNone(read_text_capped(p, "not-a-codec"),
                          "an unusable codec must fail open, not raise")


class BareAttributeNamesNothing(unittest.TestCase):
    # git prints "set" for an attribute written with no value. It names
    # neither a filter driver nor a codec, so git resolves nothing and runs
    # nothing, and the file stays comparable. Treating "set" as a driver
    # would skip a path that is perfectly scannable.
    def test_a_bare_filter_attribute_does_not_skip(self):
        sys.path.insert(0, HOOKS)
        import git_added_lines as g
        tmp = tempfile.mkdtemp()
        repo, sentinel = _repo(
            tmp, attrs="*.go filter\n", configs=("filter.x.clean",),
            tracked=("f.go", "package x\n"),
            worktree="package x\nfunc F() {}\n")
        path = os.path.join(repo, "f.go")
        out = g.final_files_added_lines({"final_files": [{"path": path}]})
        self.assertEqual(out.get(path), ["func F() {}"],
                         "a bare attribute names no driver, so the path is scannable")
        self.assertFalse(os.path.exists(sentinel))


class MalformedCheckAttr(unittest.TestCase):
    # rc 0 is not by itself an answer. An empty, truncated, or wrong-path
    # response leaves the attributes unknown, and reading that silence as
    # "nothing is converted here" would compare a converted file against its
    # own pointer and report every line of it as added.
    def _skips(self, response):
        sys.path.insert(0, HOOKS)
        import git_added_lines as g
        calls = []
        real = g._git

        def fake_git(cwd, *args):
            calls.append(args[0])
            if args[0] == "ls-files":
                return 0, "f.go\0"
            if args[0] == "check-attr":
                return 0, response
            raise AssertionError("nothing may follow an unanswered check-attr: %r" % (args,))

        tmp = tempfile.mkdtemp()
        path = os.path.join(tmp, "f.go")
        with open(path, "w") as fh:
            fh.write("// fixes #1\n")
        g._git = fake_git
        try:
            out = g.final_files_added_lines({"final_files": [{"path": path}]})
        finally:
            g._git = real
        self.assertEqual(out, {})
        self.assertEqual(calls, ["ls-files", "check-attr"])

    def test_an_empty_response_skips(self):
        self._skips("")

    def test_a_truncated_record_skips(self):
        self._skips("f.go\0filter\0")

    def test_a_record_for_another_path_skips(self):
        self._skips("other.go\0filter\0unspecified\0"
                    "other.go\0working-tree-encoding\0unspecified\0")


GO_RAW = 'package x\n\nconst help = `\n * added in v1.2.3 the --foo flag\n`\n'
KT_RAW = 'val doc = """\n * fixes #4321 in the parser\n"""\n'
# A bare tracker key matches NOTHING by default: _ticket_tell() returns None
# unless ANTI_TANGENT_TICKET_PATTERN is set, so an unconfigured project has no
# tracker tell at all. Measured: ' * ABC-1234: widen the parser' -> []. The
# preservation test therefore uses a built-in tell.
KDOC = '/**\n * fixes #1 in the parser\n */\nfun f() {}\n'


class StarredLineNeedsAnOpenBlock(unittest.TestCase):
    def test_go_raw_string_is_not_a_comment(self):
        sys.path.insert(0, HOOKS)
        from comment_scan import violations
        line = " * added in v1.2.3 the --foo flag"
        self.assertEqual(violations("x.go", [line], GO_RAW), [])
        self.assertNotEqual(violations("x.go", [line]), [],
                            "without context the old behaviour must be kept")

    def test_kotlin_triple_quote_is_not_a_comment(self):
        sys.path.insert(0, HOOKS)
        from comment_scan import violations
        self.assertEqual(
            violations("x.kt", [" * fixes #4321 in the parser"], KT_RAW), [])

    def test_a_genuine_kdoc_still_blocks(self):
        sys.path.insert(0, HOOKS)
        from comment_scan import violations
        self.assertNotEqual(
            violations("x.kt", [" * fixes #1 in the parser"], KDOC), [],
            "a real block continuation must still be caught")


class StrayQuoteDoesNotBlindTheFile(unittest.TestCase):
    # A quote span that ran to end of file would suppress every genuine block
    # comment after it. Ending those spans at end of line confines one stray
    # apostrophe to the line it is on.
    CASES = {
        # ONE apostrophe, deliberately. `fn f<'a>(s: &'a str)` carries two,
        # which pair on the line, so the end-of-line rule never engages and
        # the case proves nothing about the rule it is named for.
        "x.rs": "fn f<'a>() {}\n",
        "x.tsx": "const a = <p>don't</p>;\n",
        "x.c": "#error don't do that\n",
        "x.js": "const re = /it's/;\n",
    }

    def test_a_lone_apostrophe_leaves_later_comments_visible(self):
        sys.path.insert(0, HOOKS)
        from comment_scan import violations
        for path, prefix in self.CASES.items():
            with self.subTest(path=path):
                ctx = prefix + "/**\n * fixes #1\n */\n"
                self.assertNotEqual(
                    violations(path, [" * fixes #1"], ctx), [],
                    "%s: a stray quote must not hide a real block comment" % path)


class TokenizerFailureFallsBack(unittest.TestCase):
    def test_a_raising_walk_keeps_the_old_answer(self):
        sys.path.insert(0, HOOKS)
        import comment_scan as cs
        real = cs.block_comment_lines
        cs.block_comment_lines = lambda text: (_ for _ in ()).throw(RuntimeError("boom"))
        try:
            out = cs.violations("x.kt", [" * fixes #1"], "irrelevant")
        finally:
            cs.block_comment_lines = real
        self.assertNotEqual(out, [], "a failed walk must fall back, not drop the scan")


class TemplateInterpolationStaysCode(unittest.TestCase):
    # A ${...} hole inside a backtick span returns to CODE, so a block
    # comment written inside one is a real comment. Only the second test
    # below discriminates: the other three agree with a walk that jumps
    # straight to the closing backtick, because their comment sits outside
    # the literal. Keep all four -- they document the boundary -- but the
    # interpolated-block-comment case is the one holding the rule down.
    def test_a_block_comment_after_a_template_literal_fires(self):
        sys.path.insert(0, HOOKS)
        from comment_scan import violations
        ctx = 'const t = `\n  ${ compute() }\n`;\n/**\n * fixes #1\n */\n'
        self.assertNotEqual(violations("x.ts", [" * fixes #1"], ctx), [])

    def test_a_starred_line_inside_an_interpolated_block_comment_fires(self):
        sys.path.insert(0, HOOKS)
        from comment_scan import violations
        ctx = 'const t = `text ${ a /*\n * fixes #1\n */ b } more`;\n'
        self.assertNotEqual(
            violations("x.ts", [" * fixes #1"], ctx), [],
            "a block comment opened inside ${} is a comment, not string content")

    def test_nested_braces_do_not_end_the_hole_early(self):
        sys.path.insert(0, HOOKS)
        from comment_scan import violations
        ctx = 'const t = `${ {a:1} }`;\n/**\n * fixes #1\n */\n'
        self.assertNotEqual(violations("x.ts", [" * fixes #1"], ctx), [])

    def test_a_starred_line_in_a_plain_template_literal_is_not_a_comment(self):
        sys.path.insert(0, HOOKS)
        from comment_scan import violations
        ctx = 'const t = `\n * added in v1.2.3\n`;\n'
        self.assertEqual(violations("x.ts", [" * added in v1.2.3"], ctx), [])


class ContextsAreHandedBack(unittest.TestCase):
    def test_the_walk_reports_the_text_it_compared(self):
        sys.path.insert(0, HOOKS)
        import git_added_lines as g
        tmp = tempfile.mkdtemp()
        repo, _ = _repo(tmp, tracked=("f.go", "package x\n"),
                        worktree="package x\nfunc F() {}\n")
        path = os.path.join(repo, "f.go")
        contexts = {}
        out = g.final_files_added_lines({"final_files": [{"path": path}]},
                                        contexts=contexts)
        self.assertEqual(out.get(path), ["func F() {}"])
        self.assertEqual(contexts.get(path), "package x\nfunc F() {}\n",
                         "the scan needs the whole file, not just the added lines")


class EditReconstruction(unittest.TestCase):
    # Neither new_string nor the file on disk is the post-edit text: one is
    # unbalanced when the edit lands inside a literal that already exists, the
    # other does not contain the added line.
    def _run(self, payload, disk):
        tmp = tempfile.mkdtemp()
        path = os.path.join(tmp, "x.go")
        with open(path, "w") as fh:
            fh.write(disk)
        payload = dict(payload)
        payload["file_path"] = path
        data = {"tool_name": "Edit", "tool_input": payload}
        r = subprocess.run(
            [sys.executable, "-I", os.path.join(HOOKS, "check_comment_write.py")],
            input=json.dumps(data), capture_output=True, text=True, timeout=30,
            env=dict(os.environ, ATG_ROOT=os.path.dirname(HOOKS)))
        return r.returncode

    def test_a_starred_line_added_inside_an_existing_raw_string_is_allowed(self):
        disk = 'package x\n\nconst help = `\nusage\n`\n'
        rc = self._run({"old_string": "usage",
                        "new_string": "usage\n * added in v1.2.3 the flag"}, disk)
        self.assertEqual(rc, 0, "string content is not a comment")

    def test_a_starred_line_added_inside_a_real_block_is_refused(self):
        disk = 'package x\n\n/**\n * docs\n */\nfunc F() {}\n'
        rc = self._run({"old_string": " * docs",
                        "new_string": " * docs\n * fixes #1"}, disk)
        self.assertEqual(rc, 2, "a real block continuation must still block")

    def test_a_mid_line_fragment_inside_a_real_block_is_refused(self):
        # An old_string that starts part-way through a file line makes every
        # added line a FRAGMENT, appearing in no line of the reconstructed
        # text. The context can say nothing about such a line, and reading its
        # absence as "this sits in no block" would clear a genuine block
        # continuation. Quoting just the comment text, without its leading
        # indent, is the ordinary way to write this edit.
        disk = 'package x\n\n/**\n * Does the thing.\n */\nfunc F() {}\n'
        rc = self._run({"old_string": "* Does the thing.",
                        "new_string": "* Does the thing, fixes #1"}, disk)
        self.assertEqual(rc, 2,
                         "a fragment the context cannot place must not be cleared")

    def test_replace_all_reconstructs_every_site(self):
        # The marker appears twice: once inside a raw string, once inside a
        # real block comment. Honouring replace_all puts the added line at
        # BOTH sites, so the block-comment copy is a genuine comment carrying
        # a version reference and must block. Reconstructing with a count of
        # 1 reaches only the raw-string site, where the line is string
        # content and nothing fires -- which is why the ORDER here matters
        # and why this fixture discriminates where a two-raw-string one
        # does not.
        disk = 'const t = `\nP\n`;\n/**\nP\n*/\n'
        rc = self._run({"old_string": "P",
                        "new_string": "P\n * added in v1.2.3",
                        "replace_all": True}, disk)
        self.assertEqual(rc, 2, "the block-comment site must still be scanned")

    def test_an_unreadable_target_still_scans_by_line(self):
        # With no context the starred branch treats a candidate line as a
        # comment on shape alone, and the scan still runs: an ordinary //
        # tell must block.
        tmp = tempfile.mkdtemp()
        path = os.path.join(tmp, "x.go")
        os.symlink(os.path.join(tmp, "nowhere"), path)
        data = {"tool_name": "Edit",
                "tool_input": {"file_path": path, "old_string": "a",
                               "new_string": "a\n// fixes #1"}}
        r = subprocess.run(
            [sys.executable, "-I", os.path.join(HOOKS, "check_comment_write.py")],
            input=json.dumps(data), capture_output=True, text=True, timeout=30,
            env=dict(os.environ, ATG_ROOT=os.path.dirname(HOOKS)))
        self.assertEqual(r.returncode, 2,
                         "an unreadable target must not suppress line-based scanning")

    def test_an_empty_old_string_passes_no_context(self):
        # str.replace("", x) inserts between every character. Reconstructing
        # from it would hand the scanner a file that never existed. The added
        # line is STARRED, not a `//` tell: only a starred line consults the
        # context, so only a starred line can tell a None context apart from
        # a fabricated one. With the guard the line fires on shape; without
        # it the fabricated context says the line sits in no block and
        # nothing fires at all.
        disk = 'package x\nfunc F() {}\n'
        rc = self._run({"old_string": "", "new_string": " * fixes #1"}, disk)
        self.assertEqual(rc, 2, "an empty old_string must not fabricate a context")


if __name__ == "__main__":
    unittest.main()
