# anti-tangent-mcp v0.21.0 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stop `anti-tangent-guard`'s close-time hook from executing commands that a repository's own `.git/config` names, fix a scanner defect found in the same audit, and give the false-positive gate a mode that measures a candidate ticket pattern — the third item is a small operator feature rather than a defect fix, and is in scope because the audit showed the gate cannot currently measure the pattern v0.20.0 asks operators to set.

**Architecture:** The hook derives "which lines are new" for a `final_files` completion by diffing the worktree against `HEAD`. `git diff` must convert the worktree side, and that conversion runs repository-defined content filters. The fix removes the conversion entirely: the `HEAD` side comes from `git cat-file blob` (raw object), the worktree side from an ordinary capped file read, and the comparison happens in Python via `added()` — the function the write-time hook already uses. Two config pins close a second, unrelated exec path through partial-clone lazy fetch. Separately, the `*`-continuation comment heuristic gains file context so it stops firing inside raw string literals.

**Tech Stack:** Python 3 (stdlib only — no third-party imports in hooks), git plumbing, bash eval harness, `unittest`.

**Spec:** `docs/superpowers/specs/2026-09-11-anti-tangent-v0.21.0-design.md`

## Global Constraints

- **The hooks are stdlib-only.** No third-party imports. They run under `python3 -I`, which puts neither the script directory nor user site-packages on `sys.path`.
- **Fail open, always.** Any unanswerable question, exception, or deadline must yield *no* violations and *no* added lines for that path. A false block is weighted strictly worse than a miss. Never map an error onto "the whole file is new".
- **Comment policy is enforced on your own commits** by this repo's `anti-tangent-guard` PreToolUse hook. Comments explain non-trivial behaviour or a non-obvious invariant. They must NOT carry change history: no issue/PR/task references, no version references, no "previously" / "no longer" / "this replaced". A comment must read correctly to someone who never saw the change that introduced it.
- **Do not bump `VERSION`.** It stays at `0.20.1`. The release workflow bumps it; pre-bumping on a version branch breaks the workflow's CHANGELOG validation.
- **`_git`'s return contract is `(rc, stdout)`** and every caller checks `rc` before trusting `stdout`. Preserve it.
- **`final_files_added_lines()` returns `{path: [lines]}`.** `check-task-complete` derives its stats from that shape. Do not change the return type — new outputs go through optional out-params, the way `stats` already does.
- Branch is `version/0.21.0`, already created and checked out.

**User decisions (already made):**
- All three findings ship in v0.21.0 together (one branch, one CHANGELOG block, one PR).
- The coverage narrowing is accepted: `filter`-attributed (git-lfs / git-crypt) files are skipped, and moved lines are no longer reported as added.
- **Invalid UTF-8 is decoded, not skipped.** A blob carrying bytes that are not valid UTF-8 is decoded with `errors="replace"` on both sides and scanned — today such a file is silently skipped, so this WIDENS coverage. Skipping is reserved for a blob that cannot be retrieved at all, exceeds the cap, or declares a codec Python cannot resolve. These are different conditions and the plan must not conflate them.
- `working-tree-encoding` files are **not** skipped — the detection is recovered by decoding with the declared codec.

---

## File Structure

| File | Responsibility after this plan |
|---|---|
| `plugin/anti-tangent-guard/hooks/git_added_lines.py` | Derive added lines for a `final_files` completion, making only git calls that convert nothing and fetch nothing. |
| `plugin/anti-tangent-guard/hooks/comment_scan.py` | Tell matching, span extraction, capped reads, and the new block-comment tokenizer. |
| `plugin/anti-tangent-guard/hooks/check_comment_write.py` | PreToolUse caller; supplies post-change file text as scan context. |
| `plugin/anti-tangent-guard/hooks/check-task-complete` | PostToolUse caller (bash wrapper around embedded Python); threads context from the git walk into the scan. |
| `plugin/anti-tangent-guard/evals/fp-scan.py` | False-positive gate; optionally measures a configured ticket pattern. |
| `plugin/anti-tangent-guard/hooks/comment_scan_test.py` | Unit tests for all of the above. |
| `plugin/anti-tangent-guard/evals/guard-evals.json` | Hook-level end-to-end cases. |

---

## Task 1: Pin git shut and add a bytes-mode call

**Goal:** No git call this module makes can be sent down a lazy fetch that execs a repository-configured transport, and a caller can read a blob without `subprocess` raising on non-UTF-8 bytes.

**Files:**
- Modify: `plugin/anti-tangent-guard/hooks/git_added_lines.py:20-63`
- Modify: `plugin/anti-tangent-guard/hooks/comment_scan_test.py` (append test class)

**Acceptance Criteria:**
- [ ] `_git` passes `-c protocol.allow=never` and sets `GIT_NO_LAZY_FETCH=1` in the subprocess environment
- [ ] `-c diff.noprefix=false` and `-c diff.mnemonicPrefix=false` are gone (nothing parses diff output after Task 2)
- [ ] `-c core.quotePath=false` and `-c core.fsmonitor=false` remain
- [ ] `_git_bytes(cwd, *args)` exists and returns `(rc, bytes)`; `_git` still returns `(rc, str)`
- [ ] Both share one implementation, and both keep the `--no-optional-locks` 129-retry behaviour
- [ ] A partial-clone fixture with a missing blob and a `core.sshCommand` sentinel runs `cat-file blob` and the sentinel is never created
- [ ] `_git`'s docstring no longer claims the pins protect a diff parser

**Verify:** `python3 plugin/anti-tangent-guard/hooks/comment_scan_test.py -v` → OK

**Steps:**

- [ ] **Step 1: Write the failing test**

Append to `plugin/anti-tangent-guard/hooks/comment_scan_test.py`, before the `unittest.main()` block:

```python
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
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `python3 plugin/anti-tangent-guard/hooks/comment_scan_test.py LazyFetchCannotExec -v`
Expected: FAIL — `AttributeError: module 'git_added_lines' has no attribute '_git_bytes'`

- [ ] **Step 3: Replace `_git` with a pinned, dual-mode implementation**

In `plugin/anti-tangent-guard/hooks/git_added_lines.py`, replace the whole `_git` function (and keep `_LOCK_FLAG` and its comment exactly as they are above it) with:

```python
def _git(cwd, *args):
    """Run git rooted at cwd with config pinned. -> (rc, stdout as text)."""
    return _git_call(cwd, args, True)


def _git_bytes(cwd, *args):
    """Run git rooted at cwd with config pinned. -> (rc, stdout as bytes).

    A blob is under no obligation to be valid UTF-8, and subprocess's text
    mode raises on the first byte that is not -- inside subprocess.run, where
    the caller sees a failure and cannot tell it apart from git refusing the
    command. Reading bytes lets the caller decode with the same
    errors="replace" the worktree side uses, so both sides of a comparison
    degrade identically on the same bytes instead of one of them vanishing.
    """
    return _git_call(cwd, args, False)


def _git_call(cwd, args, text):
    """Shared body of _git and _git_bytes.

    The pins are best-effort hardening and must not be read as a completeness
    claim -- the list has been found short twice. What this module actually
    relies on is a property of the calls it makes: none of them converts
    worktree content, and none of them fetches a missing object. The pins
    cover two command-valued keys that an ordinary read-only question would
    otherwise reach:

    core.fsmonitor is a command git runs while answering, and a repository
    config can point it anywhere. protocol.allow governs the transport a
    partial clone spawns to lazily fetch an object it does not have, which
    reaches core.sshCommand, remote.*.uploadpack and a git-remote-* helper on
    PATH. GIT_NO_LAZY_FETCH stops that fetch being attempted at all; the two
    are independent mechanisms and neither is known to subsume the other
    across git versions, so both are applied.

    core.quotePath is not a command. It is pinned because ls-files output is
    parsed for a repository-relative path, and quoting would corrupt any name
    outside ASCII.
    """
    pins = ["-C", cwd,
            "-c", "core.quotePath=false",
            "-c", "core.fsmonitor=false",
            "-c", "protocol.allow=never",
            "--no-pager"]
    env = dict(os.environ, GIT_NO_LAZY_FETCH="1")
    empty = "" if text else b""

    def run(prefix):
        try:
            p = subprocess.run(["git"] + prefix + pins + list(args),
                               capture_output=True, text=text, timeout=10,
                               env=env)
        except Exception:
            return 127, empty
        return p.returncode, p.stdout

    rc, out = run(_LOCK_FLAG)
    if rc == 129 and _LOCK_FLAG:
        del _LOCK_FLAG[:]
        rc, out = run([])
    return rc, out
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `python3 plugin/anti-tangent-guard/hooks/comment_scan_test.py LazyFetchCannotExec -v`
Expected: PASS

- [ ] **Step 5: Run the whole unit suite — the existing `_git` stubs must still work**

Run: `python3 plugin/anti-tangent-guard/hooks/comment_scan_test.py -v`
Expected: OK. The existing stub tests replace `g._git` wholesale, so they are unaffected by the internal split.

- [ ] **Step 6: Commit**

```bash
git add plugin/anti-tangent-guard/hooks/git_added_lines.py \
        plugin/anti-tangent-guard/hooks/comment_scan_test.py
git commit -m "fix(guard): pin git against lazy-fetch exec and add a bytes-mode call"
```

---

## Task 2: Replace the worktree diff with a raw blob comparison

**Goal:** The tracked-path branch derives added lines without asking git to convert worktree content, and an unanswerable question skips the path instead of scanning the whole file.

**Files:**
- Modify: `plugin/anti-tangent-guard/hooks/git_added_lines.py:114-193` (and the import at `:17`)
- Modify: `plugin/anti-tangent-guard/hooks/comment_scan_test.py` (append test classes)

**Acceptance Criteria:**
- [ ] No `git diff` call remains anywhere in `git_added_lines.py`
- [ ] `rel` comes from `ls-files -z --full-name --error-unmatch`, never from `os.path.relpath`
- [ ] Absence from `HEAD` is established by `rev-parse --verify --quiet HEAD:<rel>` returning rc 1 — not by `cat-file` failing
- [ ] `HEAD` existence is cached on the worktree ROOT (not the submitted path's directory), so one repository reached through several subdirectories is probed once; a repository with no commits skips the path
- [ ] The blob is read via `_git_bytes`, capped at `READ_CAP_BYTES`, decoded with `errors="replace"`
- [ ] A `HEAD` blob containing a non-UTF-8 byte yields only the genuinely added line — today that path returns `{}`, so this widens coverage
- [ ] A tracked file with `filter.x.process` set produces correct added lines and no sentinel; same for `filter.x.clean`, as a separate fixture
- [ ] A staged-new file yields its whole content; the same path under `vendor/` is skipped and counted in `vendored_skipped`
- [ ] A worktree root reached through a symlink still resolves and scans

**Verify:** `python3 plugin/anti-tangent-guard/hooks/comment_scan_test.py -v` → OK

**Steps:**

- [ ] **Step 1: Write the failing tests**

Append to `comment_scan_test.py` before `unittest.main()`. A shared fixture builder first:

```python
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
    with open(os.path.join(repo, name_), "wb") as fh:
        fh.write(body if isinstance(body, bytes) else body.encode("utf-8"))
    git("add", name_)
    git("commit", "-qm", "init")
    for key in configs:
        git("config", key, evil + " " + key)
    if worktree is not None:
        with open(os.path.join(repo, name_), "wb") as fh:
            fh.write(worktree if isinstance(worktree, bytes)
                     else worktree.encode("utf-8"))
    return repo, sentinel


class ContentFiltersAreNeverRun(unittest.TestCase):
    # git diff had to convert the worktree side before comparing, and that
    # conversion runs whatever command the repository's own config names. Both
    # keys are live; with both set only process fires, so each needs its own
    # fixture or one of them is never exercised.
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
        self.assertEqual(out.get(path), ["func F() {}"])
        self.assertFalse(os.path.exists(sentinel),
                         "%s must never be executed from inside this hook" % key)

    def test_filter_process_is_not_run(self):
        self._case("filter.x.process")

    def test_filter_clean_is_not_run(self):
        self._case("filter.x.clean")


class UndecodableHeadBlob(unittest.TestCase):
    # One byte that is not UTF-8, anywhere in the file, makes subprocess's
    # text mode raise inside _git, which reports it as (127, "") -- the same
    # shape as git refusing the command. The diff is then unanswerable and the
    # path is dropped, so a file like this is currently unscannable and
    # nothing says so. Reading the blob as bytes and decoding both sides with
    # errors="replace" makes it comparable without widening what counts as
    # added.
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
    # Two files in different subdirectories of ONE worktree. Keying the cache
    # on the directory asked from would probe HEAD twice for the same
    # repository.
    def test_one_worktree_is_probed_once(self):
        sys.path.insert(0, HOOKS)
        import git_added_lines as g
        tmp = tempfile.mkdtemp()
        repo, _ = _repo(tmp, tracked=("a.go", "package x\n"),
                        worktree="package x\n// fixes #1\n")
        sub = os.path.join(repo, "sub")
        os.makedirs(sub)
        second = os.path.join(sub, "b.go")
        with open(second, "w") as fh:
            fh.write("package y\n")
        subprocess.run(["git", "-C", repo, "add", "-A"], capture_output=True, timeout=30)
        subprocess.run(["git", "-C", repo, "commit", "-qm", "two"], capture_output=True, timeout=30)
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `python3 plugin/anti-tangent-guard/hooks/comment_scan_test.py ContentFiltersAreNeverRun UndecodableHeadBlob -v`
Expected: FAIL — the filter sentinel exists, and the Latin-1 case returns `{}` rather than the added line.

Note the Latin-1 case fails by returning NOTHING, not by returning too much. Today the diff output carries the offending byte, `_git`'s `text=True` raises on it, `_git` converts that to `(127, "")`, and `rc_diff != 0` drops the path. Measured: today's `final_files_added_lines` returns `{}` for that file. Reading the blob as bytes is what makes it scannable at all.

- [ ] **Step 3: Rewrite the tracked branch**

In `git_added_lines.py`, change the import line at the top from

```python
from comment_scan import read_text_capped
```

to

```python
from comment_scan import READ_CAP_BYTES, added, read_text_capped
```

Then add these two helpers immediately above `final_files_added_lines`:

```python
def _head_exists(cwd, root, cache):
    """True when this worktree has a HEAD commit at all.

    Keyed on the worktree ROOT, not on the directory the question was asked
    from: one repository reached through several of its subdirectories is one
    answer, and keying on the directory would re-probe for each of them. A
    root that could not be named falls back to the directory, which is a
    cache miss every time rather than a wrong answer.

    Without this check, `HEAD:<rel>` answering "absent" in a repository that
    has no commits would be read as a new file and scan every line of it.
    """
    key = root or cwd
    if key not in cache:
        rc, _ = _git(cwd, "rev-parse", "--verify", "--quiet", "HEAD")
        cache[key] = rc == 0
    return cache[key]


def _tracked_added_lines(parent, rel, new_text):
    """(lines, whole_file) for a tracked path, or None when unanswerable.

    Absence from HEAD is established POSITIVELY, by a call that never reads
    the object. Inferring it from a failing cat-file would fold a decode
    error, a timeout and an unfetchable blob into "every line is new", which
    blocks a close on comments the implementer never wrote. This module's
    standing rule is that a question it could not answer means skip.
    """
    rc, _ = _git(parent, "rev-parse", "--verify", "--quiet", "HEAD:" + rel)
    if rc == 1:
        return new_text.splitlines(), True
    if rc != 0:
        return None
    rc_blob, blob = _git_bytes(parent, "cat-file", "blob", "HEAD:" + rel)
    if rc_blob != 0 or len(blob) > READ_CAP_BYTES:
        return None
    return added(blob.decode("utf-8", errors="replace"), new_text), False
```

Now replace the tracked branch inside `final_files_added_lines`. The current block is:

```python
        rc, _ = _git(parent, "ls-files", "--error-unmatch", "--", path)
        if rc == 0:
            rc_diff, diff = _git(parent, "diff", "--no-color", "--no-ext-diff",
                                 "--no-textconv", "HEAD", "--", path)
            if rc_diff != 0:
                continue
            lines = [ln[1:] for ln in diff.splitlines()
                     if ln.startswith("+") and not ln.startswith("+++")]
            if lines:
                out[path] = lines
            continue
```

Replace it with:

```python
        rc, listed = _git(parent, "ls-files", "-z", "--full-name",
                          "--error-unmatch", "--", path)
        if rc == 0:
            rel = listed.split("\0")[0]
            if not rel:
                continue
            new_text = read_text_capped(path)
            if not isinstance(new_text, str):
                continue
            root = _repo_root(parent, roots)
            if not _head_exists(parent, root, heads):
                continue
            result = _tracked_added_lines(parent, rel, new_text)
            if result is None:
                continue
            lines, whole_file = result
            if whole_file and _is_vendored(path, root):
                vendored_skipped += 1
                continue
            if lines:
                out[path] = lines
            continue
```

Add the `heads` cache next to `roots` at the top of the function — find `roots = {}` and make it:

```python
    roots = {}
    heads = {}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `python3 plugin/anti-tangent-guard/hooks/comment_scan_test.py -v`
Expected: OK. The existing `IndeterminateCheckIgnore` test asserts `calls == ["ls-files", "check-ignore"]`, which still holds — the untracked path is unchanged.

- [ ] **Step 5: Confirm no diff call survives**

Run: `grep -n '"diff"' plugin/anti-tangent-guard/hooks/git_added_lines.py`
Expected: no output.

- [ ] **Step 6: Commit**

```bash
git add plugin/anti-tangent-guard/hooks/git_added_lines.py \
        plugin/anti-tangent-guard/hooks/comment_scan_test.py
git commit -m "fix(guard): derive added lines from the raw HEAD blob, never a worktree diff"
```

---

## Task 3: Honour the attributes that govern content conversion

**Goal:** A `filter`-attributed path is skipped rather than reported as wholly new, and a `working-tree-encoding` path keeps being scanned by decoding with the declared codec.

**Files:**
- Modify: `plugin/anti-tangent-guard/hooks/comment_scan.py:24-66` (`read_text_capped`)
- Modify: `plugin/anti-tangent-guard/hooks/git_added_lines.py` (new `_converted` helper + call site)
- Modify: `plugin/anti-tangent-guard/hooks/comment_scan_test.py` (append test class)

**Acceptance Criteria:**
- [ ] `read_text_capped(path, encoding=None)` decodes with `encoding` when given, `utf-8` otherwise
- [ ] An unknown codec name makes `read_text_capped` return `None` (its existing `except Exception` already covers `LookupError`; assert it)
- [ ] `git check-attr -z filter working-tree-encoding -- <rel>` is consulted once per tracked path
- [ ] A path whose `filter` attribute names a filter driver is skipped. A **bare** `filter` attribute, which `check-attr` reports as `set`, is NOT skipped: it names no driver, so git has no `filter.<name>.*` to resolve and runs nothing. Verified — `*.go filter` reports `f.go|filter|set|` and `git diff` fires no configured command. The same reasoning covers a bare `working-tree-encoding`, which names no codec
- [ ] A path with `working-tree-encoding=UTF-16` still yields its genuine `// fixes #1`
- [ ] A `check-attr` that fails to answer skips the path — including an rc-0 response that is empty, truncated, or names a different path, since rc 0 alone is not an answer
- [ ] No call added here runs a filter
- [ ] `ContentFiltersAreNeverRun` from Task 2 is UPDATED here, not left to fail: those fixtures route `*.go` to `filter=x`, which is exactly the skip condition this task introduces
- [ ] A new test pins the blob read's own no-conversion property with the attribute probe bypassed, since the skip now shadows it

**Verify:** `python3 plugin/anti-tangent-guard/hooks/comment_scan_test.py -v` → OK

**Steps:**

- [ ] **Step 1: Write the failing tests**

Append to `comment_scan_test.py`:

```python
class ConvertedContent(unittest.TestCase):
    # The clean filter removed here is what used to make a converted file
    # comparable to its worktree form. A pointer or ciphertext in HEAD against
    # content on disk reads as a wholly new file; an encoding difference reads
    # as no findings at all, which is the worse of the two because the scan
    # looks like it ran.
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
```

- [ ] **Step 2: Run to verify failure**

Run: `python3 plugin/anti-tangent-guard/hooks/comment_scan_test.py ConvertedContent ReadTextCappedEncoding -v`
Expected: FAIL — `read_text_capped()` takes 1 positional argument, and the filtered path is scanned whole.

- [ ] **Step 3: Update the Task 2 filter tests this task invalidates**

Task 2's `ContentFiltersAreNeverRun` asserts `out.get(path) == ["func F() {}"]` for a fixture whose `.gitattributes` routes `*.go` to `filter=x`. That attribute is precisely what `_converted` now skips on, so the expectation must move — the sentinel assertion, which is the part that matters, stays exactly as it is.

Replace the body of `ContentFiltersAreNeverRun._case` with:

```python
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
```

The skip now shadows the blob comparison for every filter-attributed path, so the no-conversion property of `cat-file` itself needs a test that does not go through the probe. Add:

```python
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
```

- [ ] **Step 4: Give `read_text_capped` an encoding**

In `comment_scan.py`, change the signature and the decode line:

```python
def read_text_capped(path, encoding=None):
```

and

```python
        return raw.decode(encoding or "utf-8", errors="replace")
```

Add this paragraph to its docstring, after the `errors="replace"` paragraph:

```
    An encoding may be named by the caller when something authoritative says
    the bytes on disk are not UTF-8 -- a working-tree-encoding attribute is
    the case that matters. A codec name Python cannot resolve raises, which
    the handler below turns into None, so an unusable name fails open like
    every other unreadable path.
```

- [ ] **Step 5: Add the attribute probe to `git_added_lines.py`**

Add `import codecs` to the imports at the top, then add above `_head_exists`:

```python
# Attribute values that name nothing. "unspecified" and "unset" are the plain
# negatives; "set" is what git prints for a bare attribute written without a
# value, and it names neither a filter driver nor a codec -- there is no
# filter.<name>.* for git to resolve and no encoding to decode with, so
# nothing is converted and the path is safe to compare. Measured: `*.go
# filter` reports "set" and runs no configured command.
_ATTR_UNSET = ("unspecified", "unset", "set")


def _converted(parent, rel):
    """(skip, encoding) from the attributes governing content conversion.

    A `filter` attribute means HEAD holds a pointer or ciphertext while the
    worktree holds content. Nothing can compare those two without running the
    filter command, which is the one thing this module will not do, so the
    path is skipped.

    A working-tree-encoding is recoverable and must not be skipped: its value
    IS the codec name, so decoding the worktree with it costs a codecs lookup
    rather than a subprocess. Skipping it instead would give up a detection
    that works -- and give it up silently, since the scan would still report
    a clean run.
    """
    rc, out = _git(parent, "check-attr", "-z", "filter",
                   "working-tree-encoding", "--", rel)
    if rc != 0:
        return True, None
    fields = out.split("\0")
    attrs = {}
    for i in range(0, len(fields) - 2, 3):
        if fields[i] != rel:
            # A record for some other path means this response is not an
            # answer about this file, and reading it as one would apply the
            # wrong attributes.
            return True, None
        attrs[fields[i + 1]] = fields[i + 2]
    # rc 0 is not by itself an answer. A truncated or empty response leaves
    # both keys missing, and defaulting those to "unspecified" would read
    # silence as "nothing is converted here" -- the fail-CLOSED direction this
    # module is not allowed to take. Both records must be present.
    if "filter" not in attrs or "working-tree-encoding" not in attrs:
        return True, None
    if attrs["filter"] not in _ATTR_UNSET:
        return True, None
    enc = attrs["working-tree-encoding"]
    if enc in _ATTR_UNSET:
        return False, None
    try:
        codecs.lookup(enc)
    except Exception:
        return True, None
    return False, enc
```

- [ ] **Step 6: Call it from the tracked branch**

In `final_files_added_lines`, the tracked branch from Task 2 currently reads the worktree immediately after resolving `rel`. Insert the probe between them:

```python
            rel = listed.split("\0")[0]
            if not rel:
                continue
            skip, encoding = _converted(parent, rel)
            if skip:
                continue
            new_text = read_text_capped(path, encoding)
            if not isinstance(new_text, str):
                continue
```

- [ ] **Step 7: Add the unanswerable-`check-attr` test**

```python
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
```

- [ ] **Step 8: Run to verify passing**

Run: `python3 plugin/anti-tangent-guard/hooks/comment_scan_test.py -v`
Expected: OK — including the rewritten `ContentFiltersAreNeverRun`.

- [ ] **Step 9: Commit**

```bash
git add plugin/anti-tangent-guard/hooks/comment_scan.py \
        plugin/anti-tangent-guard/hooks/git_added_lines.py \
        plugin/anti-tangent-guard/hooks/comment_scan_test.py
git commit -m "fix(guard): skip filtered paths and decode working-tree-encoding ones"
```

---

## Task 4: Decide the starred-continuation heuristic from file context

**Goal:** A `*`-led line inside a raw string literal stops being scanned as a block-comment continuation, without losing any detection that works today.

**Scope limit, stated up front:** matching is by line TEXT, not by line position, because the close-time caller derives its lines from a multiset difference and has no indices to offer. So a line whose exact text appears BOTH inside a real block comment and inside a raw string in the same file still fires. That is the conservative direction — it is the answer today's shape-only heuristic already gives — and it is not a regression. Do not try to fix it by matching on position; that would require changing what every caller passes.

**Files:**
- Modify: `plugin/anti-tangent-guard/hooks/comment_scan.py:157-190` (`comment_spans`) and `:370-391` (`violations`)
- Modify: `plugin/anti-tangent-guard/hooks/comment_scan_test.py` (append test class)

**Acceptance Criteria:**
- [ ] `block_comment_lines(text)` returns line texts for which a block was ALREADY open at the start of the line (continuations). A line that opens a block partway through itself is deliberately excluded — the starred branch only asks about continuations
- [ ] A `${…}` hole inside a backtick span returns to code state, so a genuine `/* */` inside one is still found; nested braces do not end the hole early
- [ ] Single- and double-quoted spans end at end of line; a lone `'` opens nothing
- [ ] Backtick and triple-quoted spans do cross newlines
- [ ] `comment_spans(path, raw, allow_star=True)` falls through to the normal walk when `allow_star` is False
- [ ] `violations(path, added_lines, context=None)` computes the walk lazily — only when a starred candidate appears — and caches it per call
- [ ] A walk that raises falls back to today's behaviour rather than dropping the scan
- [ ] `context=None` behaves exactly as before
- [ ] Go raw string and Kotlin triple-quoted fixtures yield `[]`; a KDoc `/** * fixes #1 */` still yields a violation (a bare tracker key is NOT usable here — there is no default tracker tell)
- [ ] A Rust lifetime, a JSX apostrophe, a C `#error` apostrophe and a JS regex literal each followed by a genuine `/** * fixes #1 */` all still fire
- [ ] A template literal followed by a genuine block comment still fires, and a starred line inside a block comment opened within `${…}` is still treated as inside

**Verify:** `python3 plugin/anti-tangent-guard/hooks/comment_scan_test.py -v` → OK

**Steps:**

- [ ] **Step 1: Write the failing tests**

Append to `comment_scan_test.py`:

```python
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
        "x.rs": "fn f<'a>(s: &'a str) {}\n",
        "x.tsx": "const a = <p>don't</p>;\n",
        "x.c": "#error don't do that\n",
        "x.js": "const re = /it's/;\n",
    }

    def test_a_lone_apostrophe_leaves_later_comments_visible(self):
        sys.path.insert(0, HOOKS)
        from comment_scan import violations
        for path, prefix in self.CASES.items():
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
```

- [ ] **Step 2: Run to verify failure**

Run: `python3 plugin/anti-tangent-guard/hooks/comment_scan_test.py StarredLineNeedsAnOpenBlock -v`
Expected: FAIL — `violations()` takes 2 positional arguments but 3 were given.

- [ ] **Step 3: Add the tokenizer**

In `comment_scan.py`, add immediately above `def comment_spans(`:

```python
# Quote forms that genuinely cross a newline. A single or double quote does
# not: no language in SCAN_EXTS carries one over unescaped, so an unclosed one
# is a typo. Treating it as an open span is what lets a Rust lifetime or an
# apostrophe in JSX text swallow every comment after it in the file.
_MULTILINE_QUOTES = ('"""', "'''", "`")


def block_comment_lines(text):
    """Line texts sitting inside an open /* */ block, as a set.

    The question cannot be answered from a line on its own: ` * text` is a
    block continuation inside /* */ and ordinary string content inside a raw
    literal, and the two are byte-identical. Only the surrounding file
    separates them.

    A line is in the set when a block was ALREADY open as the line began --
    which is what a continuation is. A line that opens a block partway through
    itself is not in the set, and does not need to be: the starred branch only
    ever asks about continuation lines, and the opener goes through the
    ordinary left-to-right walk.

    Line comments, string literals and template-literal interpolation are all
    tracked in the same pass. Backticks are scanned character by character
    rather than jumped over, because a `${ ... }` hole returns to CODE: a
    genuine block comment can live inside one, and today's scanner finds it.
    Skipping to the closing backtick would silently lose that detection.

    Matched by TEXT rather than line number because the caller may hold a
    sparse subset of the file with no indices to offer. A line whose text
    appears both inside and outside a block resolves as inside, which is the
    answer the shape-only heuristic already gives.
    """
    inside = set()
    stack = []
    for line in text.splitlines():
        if stack and stack[-1] == "block":
            inside.add(line)
        i, n = 0, len(line)
        while i < n:
            top = stack[-1] if stack else None
            if top == "block":
                end = line.find("*/", i)
                if end < 0:
                    break
                stack.pop(); i = end + 2; continue
            if top == "`":
                if line.startswith("${", i):
                    stack.append("interp"); i += 2; continue
                if line[i] == "\\":
                    i += 2; continue
                if line[i] == "`":
                    stack.pop(); i += 1; continue
                i += 1; continue
            if top in _MULTILINE_QUOTES:
                end = line.find(top, i)
                if end < 0:
                    break
                i = end + len(top); stack.pop(); continue
            ch = line[i]
            if ch == "\\":
                i += 2; continue
            if top == "interp" and ch == "{":
                stack.append("interp"); i += 1; continue
            if top == "interp" and ch == "}":
                stack.pop(); i += 1; continue
            if line.startswith("//", i):
                break
            if line.startswith("/*", i):
                stack.append("block"); i += 2; continue
            opened = None
            for q in _MULTILINE_QUOTES:
                if line.startswith(q, i):
                    opened = q; break
            if opened is not None:
                stack.append(opened); i += len(opened); continue
            if ch in ('"', "'"):
                j = i + 1
                while j < n:
                    if line[j] == "\\":
                        j += 2; continue
                    if line[j] == ch:
                        break
                    j += 1
                i = j + 1; continue
            i += 1
    return inside


def starred_candidate(path, raw):
    """True when raw is the shape the block-continuation branch would claim."""
    line = raw.strip()
    return ("*" in openers(path) and line.startswith("*")
            and not line.startswith("*/"))
```

- [ ] **Step 4: Let `comment_spans` decline the starred branch**

Change the signature and the starred guard in `comment_spans`:

```python
def comment_spans(path, raw, allow_star=True):
```

and

```python
    if allow_star and "*" in opens and line.startswith("*") and not line.startswith("*/"):
```

Append to its docstring:

```
    allow_star=False makes the continuation branch decline, so the line goes
    through the ordinary left-to-right walk instead. The caller uses it when
    it can see the whole file and the file says this line is not inside an
    open block.
```

- [ ] **Step 5: Make `violations` consult the context**

Replace the body of `violations` with:

```python
def violations(path, added_lines, context=None):
    """Return [(line, why)] for added comment lines carrying change history.

    `context` is the full post-change text of the file, when the caller has
    it. It decides one question only: whether a `*`-led line really sits
    inside an open block comment, which is the premise the continuation
    heuristic asserts and cannot check from the line alone.

    The walk over that context is LAZY -- it runs only once a starred
    candidate actually appears, and at most once per call. A file with no
    starred added line pays nothing, and a tokenizer that raises or eats the
    deadline cannot take a `//` finding elsewhere in the same file down with
    it.

    Anything that stops the scan completing — the deadline above, or any
    exception from a caller-supplied pattern — yields no violations. This
    module's standing rule is that an undecidable scan allows the write.
    """
    if not scannable(path):
        return []
    out = []
    block_lines = None
    try:
        with scan_deadline():
            for raw in added_lines:
                allow_star = True
                if context is not None and starred_candidate(path, raw):
                    if block_lines is None:
                        try:
                            block_lines = block_comment_lines(context)
                        except Exception:
                            block_lines = True
                    if block_lines is not True:
                        allow_star = raw in block_lines
                for span in comment_spans(path, raw, allow_star):
                    hit = next((why for pat, why in TELLS if pat.search(span)), None)
                    if hit is not None:
                        out.append((raw.strip(), hit))
                        break
    except Exception:
        return []
    return out
```

`block_lines is True` is the sentinel for "the walk failed, keep the old answer" — distinct from a legitimately empty set.

- [ ] **Step 6: Run to verify passing**

Run: `python3 plugin/anti-tangent-guard/hooks/comment_scan_test.py -v`
Expected: OK — including the pre-existing `CommentSpanExtraction` tests, which call `comment_spans` with two arguments and get `allow_star=True` by default.

- [ ] **Step 7: Commit**

```bash
git add plugin/anti-tangent-guard/hooks/comment_scan.py \
        plugin/anti-tangent-guard/hooks/comment_scan_test.py
git commit -m "fix(guard): require an open block before reading a starred line as a comment"
```

---

## Task 5: Thread file context through both hooks

**Goal:** Both callers supply the post-change file text, so the fix from Task 4 is actually reached in production rather than only in unit tests.

**Files:**
- Modify: `plugin/anti-tangent-guard/hooks/git_added_lines.py` (`contexts` out-param)
- Modify: `plugin/anti-tangent-guard/hooks/check_comment_write.py:31-49`
- Modify: `plugin/anti-tangent-guard/hooks/check-task-complete:532-580` (embedded Python)
- Modify: `plugin/anti-tangent-guard/hooks/comment_scan_test.py` (append test class)

**Acceptance Criteria:**
- [ ] `final_files_added_lines(inp, deadline=None, stats=None, contexts=None)` fills `contexts[path]` with the text its answer was derived from
- [ ] The return shape stays `{path: [lines]}`
- [ ] `check-task-complete` passes `contexts.get(fpath)` as the third argument to `violations`
- [ ] The `final_diff` branch passes `None` (no file text exists there)
- [ ] `check_comment_write.py` passes `content` for `Write`
- [ ] For `Edit` it passes the reconstructed post-edit text, honouring `replace_all`
- [ ] An `Edit` whose file cannot be read passes `None` rather than failing the scan
- [ ] An `Edit` with an empty `old_string` passes `None` — `str.replace("", x)` inserts at every position and would fabricate a file

**Verify:** `python3 plugin/anti-tangent-guard/hooks/comment_scan_test.py -v && bash plugin/anti-tangent-guard/evals/run.sh`

**Steps:**

- [ ] **Step 1: Write the failing test**

Append to `comment_scan_test.py`:

```python
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

    def test_replace_all_reconstructs_every_site(self):
        # Two raw strings; replace_all edits both. Reconstructing with a count
        # of 1 would leave the second literal unbalanced in the context text.
        disk = 'package x\n\nconst a = `\nusage\n`\nconst b = `\nusage\n`\n'
        rc = self._run({"old_string": "usage",
                        "new_string": "usage\n * added in v1.2.3 the flag",
                        "replace_all": True}, disk)
        self.assertEqual(rc, 0, "string content is not a comment at either site")

    def test_an_unreadable_target_still_scans_by_line(self):
        # context is None here, so the starred branch keeps its old behaviour
        # rather than the scan being dropped: an ordinary // tell must block.
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
        # from it would hand the scanner a file that never existed.
        disk = 'package x\n'
        rc = self._run({"old_string": "", "new_string": "// fixes #1"}, disk)
        self.assertEqual(rc, 2, "the line-based scan must still see the tell")
```

- [ ] **Step 2: Run to verify failure**

Run: `python3 plugin/anti-tangent-guard/hooks/comment_scan_test.py ContextsAreHandedBack EditReconstruction -v`
Expected: FAIL — unexpected keyword `contexts`, and the raw-string edit exits 2.

- [ ] **Step 3: Add the `contexts` out-param**

In `git_added_lines.py`, change the signature:

```python
def final_files_added_lines(inp, deadline=None, stats=None, contexts=None):
```

Add to its docstring, after the `stats` paragraph:

```
    A `contexts` dict, if given, is filled with the full text each answer was
    derived from. The scanner needs it to tell a block-comment continuation
    from a line of a raw string literal, which cannot be decided from the
    added lines alone. It is an out-param for the same reason `stats` is: the
    return shape is what the close-time hook counts, and widening it would
    break that reader.
```

In the tracked branch, after `out[path] = lines`, add:

```python
            if contexts is not None:
                contexts[path] = new_text
```

In the untracked branch, after `out[path] = content.splitlines()`, add:

```python
        if contexts is not None:
            contexts[path] = content
```

- [ ] **Step 4: Pass context from the write-time hook**

In `check_comment_write.py`, replace the block from `if data["tool_name"] == "Edit":` through `bad = violations(path, lines)` with:

```python
if data["tool_name"] == "Edit":
    old_string = inp.get("old_string") or ""
    new_string = inp.get("new_string") or ""
    lines = added(old_string, new_string)
    # The post-edit text, which is neither operand on its own: new_string is
    # unbalanced when the edit lands inside a literal that already exists on
    # disk, and the disk text does not contain the added line. An empty
    # old_string is refused because str.replace("") inserts between every
    # character and would hand the scanner a file that never existed.
    existing = read_text_capped(path)
    if old_string and isinstance(existing, str) and old_string in existing:
        context = (existing.replace(old_string, new_string)
                   if inp.get("replace_all")
                   else existing.replace(old_string, new_string, 1))
    else:
        context = None
else:
    content = inp.get("content") or ""
    existing = read_text_capped(path)
    if existing is MISSING:
        # Nothing on disk yet, so every line of the new content is added.
        lines = content.splitlines()
    elif existing is None:
        # A symlink, a FIFO, a directory, an oversized or unreadable file.
        # Nothing here can be compared against, and a scan that cannot see the
        # old text would report the whole file as added. Allowing the write is
        # the right call; reporting it as 3 rather than 0 keeps it out of the
        # wrapper's "pass" arm, since no scan actually happened.
        sys.exit(3)
    else:
        lines = added(existing, content)
    context = content

bad = violations(path, lines, context)
```

- [ ] **Step 5: Pass context from the close-time hook**

In `check-task-complete`, inside the embedded Python, change the `git_stats = {}` block to also build a contexts dict:

```python
        git_stats = {}
        git_contexts = {}
        if ("final_diff" in last_completion or
                "final_diff_path" in last_completion):
            added_by_path = diff_added_lines(last_completion)
        else:
            added_by_path = final_files_added_lines(last_completion,
                                                    stats=git_stats,
                                                    contexts=git_contexts)
```

and change the scan loop's `violations` call:

```python
            for line, why in scan_violations(fpath, added,
                                             git_contexts.get(fpath)):
```

A `final_diff` submission leaves `git_contexts` empty, so `.get` yields `None` and that path keeps today's behaviour — there is no file text to walk when only a diff was submitted.

- [ ] **Step 6: Run to verify passing**

Run: `python3 plugin/anti-tangent-guard/hooks/comment_scan_test.py -v`
Expected: OK

Run: `bash plugin/anti-tangent-guard/evals/run.sh`
Expected: all 142 cases pass.

- [ ] **Step 7: Commit**

```bash
git add plugin/anti-tangent-guard/hooks/git_added_lines.py \
        plugin/anti-tangent-guard/hooks/check_comment_write.py \
        plugin/anti-tangent-guard/hooks/check-task-complete \
        plugin/anti-tangent-guard/hooks/comment_scan_test.py
git commit -m "feat(guard): hand the scanner the file text both hooks already hold"
```

---

## Task 6: Let the false-positive gate measure a configured ticket pattern

**Goal:** An operator can find out what `ANTI_TANGENT_TICKET_PATTERN` would newly block in this repository before committing to it.

**Files:**
- Modify: `plugin/anti-tangent-guard/evals/fp-scan.py:36-46` and `:83-93`

**Acceptance Criteria:**
- [ ] With no flag, the env var is popped exactly as today, and stdout AND stderr are proven byte-identical to the pre-change version with `cmp` (not merely rc 0)
- [ ] `--ticket-pattern <regex>` sets the variable instead, before `comment_scan` is imported
- [ ] A pattern that `_ticket_tell()` would silently drop (bad regex, over the length cap) exits non-zero with a message, rather than reporting a clean run
- [ ] In `--ticket-pattern` mode the stderr summary reports total hits AND how many carry the ticket tell; in default mode the existing one-number summary is preserved byte-for-byte (the `cmp` above is what proves it)
- [ ] A missing argument to the flag exits non-zero

**Verify:** "byte-identical" has to be *compared*, not asserted — capture the pre-change output from the committed version and diff against it, keeping stdout and stderr separate (the TSV is the artefact `fp-report.sh` joins on):
```bash
git stash push -- plugin/anti-tangent-guard/evals/fp-scan.py
python3 plugin/anti-tangent-guard/evals/fp-scan.py >/tmp/fp-base.tsv 2>/tmp/fp-base.err
git stash pop
python3 plugin/anti-tangent-guard/evals/fp-scan.py >/tmp/fp-new.tsv 2>/tmp/fp-new.err; echo "default rc=$?"
cmp /tmp/fp-base.tsv /tmp/fp-new.tsv && echo "stdout byte-identical"
cmp /tmp/fp-base.err /tmp/fp-new.err && echo "stderr byte-identical"
python3 plugin/anti-tangent-guard/evals/fp-scan.py --ticket-pattern '[A-Z]{2,}-[0-9]+' >/dev/null 2>&1; echo "flag rc=$?"
python3 plugin/anti-tangent-guard/evals/fp-scan.py --ticket-pattern '(' >/dev/null 2>&1; echo "bad-regex rc=$? (want 2)"
python3 plugin/anti-tangent-guard/evals/fp-scan.py --ticket-pattern >/dev/null 2>&1; echo "missing-arg rc=$? (want 2)"
```
Expected: both `cmp`s silent; default rc 0; flag rc 0; bad-regex rc 2; missing-arg rc 2.

**Steps:**

- [ ] **Step 1: Replace the env-pop block with argument handling**

In `fp-scan.py`, replace these lines:

```python
# The false-positive gate must measure the shipped tells, not whatever the
# developer happens to have configured for their own project.
os.environ.pop("ANTI_TANGENT_TICKET_PATTERN", None)

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(os.path.dirname(HERE), "hooks"))
from comment_scan import scannable, violations  # noqa: E402
```

with:

```python
# The environment is settled BEFORE the import below, because comment_scan
# binds the ticket tell at import time. Setting it afterwards would have no
# effect and the run would silently measure the shipped tells instead.
#
# Default: the gate measures the shipped tells, not whatever the developer
# happens to have configured for their own project, so a local pattern cannot
# move a verdict recorded in fp-class.tsv.
_pattern = None
_argv = sys.argv[1:]
if _argv:
    if _argv[0] != "--ticket-pattern" or len(_argv) != 2:
        sys.stderr.write("usage: fp-scan.py [--ticket-pattern <regex>]\n")
        sys.exit(2)
    _pattern = _argv[1]

if _pattern is None:
    os.environ.pop("ANTI_TANGENT_TICKET_PATTERN", None)
else:
    os.environ["ANTI_TANGENT_TICKET_PATTERN"] = _pattern

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(os.path.dirname(HERE), "hooks"))
import comment_scan  # noqa: E402
from comment_scan import scannable, violations  # noqa: E402

# _ticket_tell drops a pattern that will not compile or runs past the length
# cap, and says nothing when it does. Under the flag that silence is a trap:
# the operator would read "0 tracker hits" as proof the pattern is safe, when
# the pattern was never installed. Asking the module what it actually bound
# cannot drift from its own rules the way a re-implemented check would.
if _pattern is not None and comment_scan._ticket is None:
    sys.stderr.write(
        "fp-scan: the pattern was not installed — it failed to compile, or "
        "exceeded the %d-character cap. Nothing was measured.\n"
        % comment_scan._TICKET_PATTERN_MAX_LEN)
    sys.exit(2)

TICKET_WHY = "a tracker reference"
```

- [ ] **Step 2: Count the ticket hits separately**

In `main()`, change `hits = 0` to:

```python
    hits = 0
    ticket_hits = 0
```

and inside the inner loop, replace:

```python
            for line, why in violations(path, [raw]):
                sys.stdout.write("%s\t%d\t%s\t%s\n" % (path, n, why, escape(line)))
                hits += 1
```

with:

```python
            for line, why in violations(path, [raw]):
                sys.stdout.write("%s\t%d\t%s\t%s\n" % (path, n, why, escape(line)))
                hits += 1
                if why == TICKET_WHY:
                    ticket_hits += 1
```

- [ ] **Step 3: Report the split**

Replace the final summary line:

```python
    sys.stderr.write("fp-scan: %d hit(s)\n" % hits)
```

with:

```python
    # violations() stops at the FIRST matching tell, in TELLS order, so a line
    # a built-in tell already catches is not counted here. That is the number
    # an operator wants: what this pattern would newly block, not how often it
    # matches text something else already flags.
    if _pattern is None:
        sys.stderr.write("fp-scan: %d hit(s)\n" % hits)
    else:
        sys.stderr.write(
            "fp-scan: %d hit(s), %d of them newly attributable to the pattern\n"
            % (hits, ticket_hits))
```

- [ ] **Step 4: Verify all three modes**

```bash
python3 plugin/anti-tangent-guard/evals/fp-scan.py >/tmp/fp-a.tsv 2>&1; echo "default rc=$?"
python3 plugin/anti-tangent-guard/evals/fp-scan.py --ticket-pattern '[A-Z]{2,}-[0-9]+' >/dev/null 2>/tmp/fp-b.err; echo "flag rc=$?"; tail -1 /tmp/fp-b.err
python3 plugin/anti-tangent-guard/evals/fp-scan.py --ticket-pattern '(' >/dev/null 2>&1; echo "bad-regex rc=$? (want 2)"
python3 plugin/anti-tangent-guard/evals/fp-scan.py --ticket-pattern >/dev/null 2>&1; echo "missing-arg rc=$? (want 2)"
```

Expected: default rc 0; flag rc 0 with a two-number summary; bad-regex rc 2; missing-arg rc 2.

- [ ] **Step 5: Confirm the gate itself is unmoved**

Run: `bash plugin/anti-tangent-guard/evals/fp-report.sh`
Expected: the same verdict as on `main` — the default path must be byte-identical.

- [ ] **Step 6: Commit**

```bash
git add plugin/anti-tangent-guard/evals/fp-scan.py
git commit -m "feat(guard): let fp-scan measure a configured ticket pattern"
```

---

## Task 7: Hook-level evals for the two exec paths

**Goal:** Prove end-to-end through `check-task-complete` that neither exec path runs — by observing the eval suite green with the protections in place, AND red (`file must not exist: .../SENTINEL`) with each protection removed in turn.

> **USER-ORDERED GATE — NON-SKIPPABLE.** This task was requested by the user in the current conversation. It MUST NOT be closed by walking around it, by declaring it "verified inline", or by substituting a cheaper check. Close only after every item in `acceptanceCriteria` has been re-validated independently, with output captured.

A green suite alone does not close this task. An eval that passes whether or not the bug is present is not a test, and these three exist solely to catch a regression in the one property this release claims. Both observations — protections on, protections removed — must be captured.

**Files:**
- Modify: `plugin/anti-tangent-guard/evals/guard-evals.json` (append to `.evals`)

**Acceptance Criteria:**
- [ ] Three new cases: `filter.x.clean`, `filter.x.process`, and the partial-clone lazy fetch
- [ ] Each points the repository-configured command at a sentinel file and asserts it via `expected_file_absent`
- [ ] Each expects exit 0 — the added line carries no tell, so only an executed command can change the outcome
- [ ] Every new case has a `reason` saying what it would catch if it regressed
- [ ] Ids continue from the current maximum
- [ ] Captured output shows the suite GREEN with protections in place
- [ ] Captured output shows each case RED when its protection is removed, then restored
- [ ] The filter mutation disables BOTH `_converted`'s skip and the blob comparison — the skip fires first, so mutating only the comparison leaves the case green and proves nothing

**Verify:** `bash plugin/anti-tangent-guard/evals/run.sh` → all cases pass

**Steps:**

- [ ] **Step 1: Note the assertion mechanism**

The harness already supports exactly this. `expected_file_absent: [paths]` (`evals/run.sh:664-688`) fails a case if any listed path exists after the hook ran, with `{{TMPDIR}}` substituted. Eval 88 uses it for the symlink case. So each new case arms a repository-configured command that writes a sentinel, and asserts the sentinel never appears — a direct assertion, not an inference from the exit code.

Get the current maximum id:

```bash
python3 -c "import json;d=json.load(open('plugin/anti-tangent-guard/evals/guard-evals.json'));print(max(c['id'] for c in d['evals']))"
```

- [ ] **Step 2: Append the three cases**

```bash
python3 - <<'PY'
import json
p = "plugin/anti-tangent-guard/evals/guard-evals.json"
d = json.load(open(p))
n = max(c["id"] for c in d["evals"])

SENTINEL = "{{TMPDIR}}/SENTINEL"
EVIL = ("printf '#!/bin/sh\\necho fired >> {{TMPDIR}}/SENTINEL\\n' > {{TMPDIR}}/evil.sh && "
        "chmod +x {{TMPDIR}}/evil.sh && ")

def filter_repo(cfg):
    return (
        "mkdir -p r && cd r && git init -q . && "
        "git config user.email t@t && git config user.name t && "
        + EVIL +
        "echo '*.go filter=x' > .gitattributes && "
        "git add .gitattributes && git commit -qm a && "
        "printf 'package x\\n' > f.go && git add f.go && git commit -qm i && "
        "git config %s '{{TMPDIR}}/evil.sh' && "
        "printf 'package x\\nfunc F() {}\\n' > f.go" % cfg)

LAZY = (
    "mkdir -p r && cd r && git init -q . && "
    "git config user.email t@t && git config user.name t && "
    "printf '#!/bin/sh\\necho fired >> {{TMPDIR}}/SENTINEL\\nexit 1\\n' > {{TMPDIR}}/ssh.sh && "
    "chmod +x {{TMPDIR}}/ssh.sh && "
    "printf 'package x\\n' > f.go && git add f.go && git commit -qm i && "
    "B=$(git rev-parse HEAD:f.go) && "
    "git config core.repositoryFormatVersion 1 && "
    "git config extensions.partialClone origin && "
    "git config remote.origin.promisor true && "
    "git config remote.origin.url ssh://evil.example/x.git && "
    "git config core.sshCommand '{{TMPDIR}}/ssh.sh' && "
    "printf 'package x\\nfunc F() {}\\n' > f.go && "
    "rm -f .git/objects/${B:0:2}/${B:2}")

def case(i, name, setup, reason):
    return {
        "id": i, "name": name, "setup_script": setup,
        "input": {"tool_name": "TaskUpdate",
                  "tool_input": {"status": "completed", "taskId": "1"},
                  "transcript_path": "{{TRANSCRIPT}}"},
        "transcript_raw_lines": [
            '{"type": "assistant", "message": {"content": [{"type": "tool_use", "name": "TaskUpdate", "input": {"taskId": "1", "status": "in_progress"}}]}}',
            '{"type": "assistant", "message": {"content": [{"type": "tool_use", "id": "t1", "name": "mcp__anti-tangent__validate_completion", "input": {"final_files": [{"path": "{{TMPDIR}}/r/f.go"}]}}]}}',
            '{"type": "user", "message": {"content": [{"type": "tool_result", "tool_use_id": "t1", "content": "{\\"tool\\":\\"validate_completion\\",\\"session_id\\":\\"s\\",\\"verdict\\":\\"pass\\"}"}]}}',
        ],
        "expected_exit": 0,
        "expected_file_absent": [SENTINEL],
        "reason": reason,
    }

d["evals"].append(case(
    n + 1, "final-files-filter-clean-is-never-run", filter_repo("filter.x.clean"),
    "a tracked final_files path in a repository whose own config names a clean filter "
    "must not run it. git diff had to convert the worktree side before comparing, which "
    "is what executed it; the sentinel proves the conversion is gone."))
d["evals"].append(case(
    n + 2, "final-files-filter-process-is-never-run", filter_repo("filter.x.process"),
    "the process form of the same hazard. With both keys set only process fires, so it "
    "needs its own fixture or the clean case would be the only one ever exercised."))
d["evals"].append(case(
    n + 3, "final-files-lazy-fetch-is-never-run", LAZY,
    "a blob missing from a partial clone sends git down a lazy fetch that execs the "
    "configured transport. This is a second exec path with no filter involved, so no "
    "amount of filter pinning covers it; protocol.allow=never and GIT_NO_LAZY_FETCH do."))
json.dump(d, open(p, "w"), indent=2, ensure_ascii=False)
open(p, "a").write("\n")
print("appended ids %d-%d" % (n + 1, n + 3))
PY
```

- [ ] **Step 3: Run the suite**

Run: `bash plugin/anti-tangent-guard/evals/run.sh`
Expected: every case passes, including the three new ones.

- [ ] **Step 4: Prove the new cases can actually fail**

A case that passes under the bug is not a test. The mutation must remove EVERY protection standing in front of the path, not only the last one — otherwise the case stays green and the mutation proves the opposite of what it appears to.

**Filter cases — two layers, both must go.** After Task 3 a filter-attributed path is skipped by `_converted` *before* the comparison runs, and the comparison itself converts nothing. Restoring the old `git diff` alone leaves the case green, because the skip fires first and the diff is never reached. Disable both:

```bash
# layer 1: make the attribute probe stop skipping
#   in git_added_lines.py, temporarily make _converted's body `return False, None`
# layer 2: put the converting call back.
#   _tracked_added_lines(parent, rel, new_text) has NO `path` in scope, and a
#   bare `rel` pathspec silently matches nothing when parent is a subdirectory
#   -- rc 0, no output, no filter, so the mutation would look like proof and
#   be the opposite. Use the :(top) magic pathspec, which is repo-root
#   relative. Verified to fire the filter from a subdirectory.
#   rc, d = _git(parent, "diff", "--no-color", "HEAD", "--", ":(top)" + rel)
#   return [ln[1:] for ln in d.splitlines()
#           if ln.startswith("+") and not ln.startswith("+++")], False
bash plugin/anti-tangent-guard/evals/run.sh 2>&1 | grep -E 'FAIL|filter'
git checkout plugin/anti-tangent-guard/hooks/git_added_lines.py
```

**Lazy-fetch case — two independent pins, both must go.** Either one alone still blocks the fetch:

```bash
# drop BOTH `-c protocol.allow=never` from _git_call's pins
# and GIT_NO_LAZY_FETCH from its env
bash plugin/anti-tangent-guard/evals/run.sh 2>&1 | grep -E 'FAIL|lazy'
git checkout plugin/anti-tangent-guard/hooks/git_added_lines.py
```

Expected in each case: the failure must be specifically `file must not exist: .../SENTINEL` for the matching eval. Any other RED — an exception, a changed exit code — means the mutation did not reach the filter and proves nothing; fix the mutation and re-run. Restore, re-run the full suite green, and only then commit. Capture both the GREEN and the RED output — this task's gate metadata requires evidence from both sides.

- [ ] **Step 5: Commit**

```bash
git add plugin/anti-tangent-guard/evals/guard-evals.json
git commit -m "test(guard): hook-level evals for the filter and lazy-fetch exec paths"
```

---

## Task 8: Release bookkeeping

**Goal:** The branch satisfies CI's branch-name/CHANGELOG check and the plugin advertises its new version.

**Files:**
- Modify: `CHANGELOG.md` (new block at the top of the released sections)
- Modify: `plugin/anti-tangent-guard/.claude-plugin/plugin.json:4`

**Acceptance Criteria:**
- [ ] `CHANGELOG.md` has `## [0.21.0] - 2026-09-11` with `### Security`, `### Fixed`, `### Added`
- [ ] `VERSION` is untouched at `0.20.1`
- [ ] `anti-tangent-guard`'s `plugin.json` version is `0.4.0`
- [ ] All repo gates pass

**Verify:** `go test -race ./... && bash scripts/check-protocol-docs.sh && python3 scripts/check_md_links.py`

**Steps:**

- [ ] **Step 1: Add the changelog block**

Insert directly above the `## [0.20.1] - 2026-09-11` heading in `CHANGELOG.md`:

```markdown
## [0.21.0] - 2026-09-11

### Security

- `anti-tangent-guard`'s close-time hook no longer executes commands named by the
  repository it is pointed at. Deriving added lines for a `final_files` completion used
  `git diff HEAD -- <path>`, which must convert the worktree side and so runs a
  `filter.<name>.clean` or `filter.<name>.process` command from that repository's
  `.git/config`. The comparison now reads the raw `HEAD` blob with `git cat-file blob`
  and diffs in-process, converting nothing.
- The same hook no longer reaches a second, unrelated exec path: a blob missing from a
  partial clone sent git down a lazy fetch that spawned `core.sshCommand`,
  `remote.*.uploadpack`, or a `git-remote-*` helper. Git is now invoked with
  `protocol.allow=never` and `GIT_NO_LAZY_FETCH=1`.

### Fixed

- A `*`-led line inside a raw string literal — a Go backtick string, a Kotlin
  triple-quoted string — is no longer scanned as a block-comment continuation, so a
  changelog or help text embedded in source stops refusing its own write. The scanner
  now decides from the surrounding file whether a block comment is actually open.
- A path carrying a `filter` attribute (git-lfs, git-crypt) is skipped rather than
  reported as wholly new, and a `working-tree-encoding` path is decoded with its
  declared codec instead of being misread as UTF-8.
- A `HEAD` blob carrying bytes that are not valid UTF-8 is now decoded with replacement and
  scanned. Previously the decode failure made the whole comparison unanswerable and the file was
  silently skipped, so a file with one stray byte was never checked. A blob that cannot be
  retrieved, exceeds the read cap, or declares a codec Python cannot resolve is still skipped —
  deliberately, rather than guessed at.

### Added

- `fp-scan.py --ticket-pattern <regex>` measures what a candidate
  `ANTI_TANGENT_TICKET_PATTERN` would newly flag in this repository, and refuses to run
  silently when the pattern would be dropped for being invalid or over the length cap.
```

- [ ] **Step 2: Bump the plugin version**

```bash
python3 - <<'PY'
import json
p = "plugin/anti-tangent-guard/.claude-plugin/plugin.json"
d = json.load(open(p))
d["version"] = "0.4.0"
json.dump(d, open(p, "w"), indent=2, ensure_ascii=False)
open(p, "a").write("\n")
PY
git diff --stat plugin/anti-tangent-guard/.claude-plugin/plugin.json
```

- [ ] **Step 3: Confirm VERSION was not touched**

Run: `git diff --name-only origin/main...HEAD | grep -x VERSION; echo "exit=$? (1 means VERSION is untouched, which is correct)"`

- [ ] **Step 4: Run every gate**

```bash
go build ./...
go test -race ./...
bash scripts/check-protocol-docs.sh
python3 scripts/check_md_links.py
python3 plugin/anti-tangent-guard/hooks/comment_scan_test.py
bash plugin/anti-tangent-guard/evals/run.sh
bash plugin/anti-tangent-guard/evals/fp-report.sh
```

Expected: all green.

- [ ] **Step 5: Commit**

```bash
git add CHANGELOG.md plugin/anti-tangent-guard/.claude-plugin/plugin.json
git commit -m "chore: changelog and plugin version for v0.21.0"
```

---

## Final verification

Run everything once more from a clean tree and confirm the security property directly:

```bash
go test -race ./...
python3 plugin/anti-tangent-guard/hooks/comment_scan_test.py
bash plugin/anti-tangent-guard/evals/run.sh
grep -n '"diff"' plugin/anti-tangent-guard/hooks/git_added_lines.py || echo "no diff call remains"
grep -n 'protocol.allow=never' plugin/anti-tangent-guard/hooks/git_added_lines.py
grep -n 'GIT_NO_LAZY_FETCH' plugin/anti-tangent-guard/hooks/git_added_lines.py
```

Then open the PR against `main` with `[minor]` in the merge commit subject, per the repo's branch conventions.
