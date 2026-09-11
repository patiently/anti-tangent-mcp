"""git-derived added lines for a completion that submitted final_files.

Imported by check-task-complete's embedded Python. It lives in a module
rather than in that heredoc so its git calls can be stubbed by a test: the
check-ignore branch below has no other way to be exercised, because a
fixture repository cannot reliably produce an unanswerable status.

An untracked path is judged on the `content` the completion submitted,
falling back to the file on disk only when the entry carried none. That is
the hook's contract -- it scans the evidence the last validate_completion
submitted -- and it is also the text the reviewer saw.
"""
import codecs
import os
import subprocess
import time

from comment_scan import READ_CAP_BYTES, added, read_text_capped


# --no-optional-locks keeps these read-only questions from refreshing the
# index. 129 is git's GENERIC usage error and says only that git rejected the
# command line: a git that does not know this flag answers that way, and so
# does a command line malformed for any other reason. Either way every call
# here would fail at once, and the caller cannot tell a failed git from a
# repository with nothing to report -- the scan would silently yield nothing.
# So the first 129 drops the flag for the rest of the process and retries
# without it, whether or not the flag was what git objected to: the flag is a
# courtesy to the developer's index, never load bearing for what this module
# returns.
_LOCK_FLAG = ["--no-optional-locks"]


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

    The pins are best-effort hardening, not a completeness claim. What this
    module actually relies on is a property of the calls it makes: none of
    them converts worktree content, and none of them fetches a missing
    object. The pins cover two command-valued keys that an ordinary
    read-only question would otherwise reach:

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


# A wall-clock bound on the whole per-path walk. A TRACKED path in a repository
# not seen yet is the expensive one: ls-files, check-attr, rev-parse
# --show-toplevel, rev-parse --verify HEAD, the HEAD-membership probe and
# cat-file -- six git calls of up to ten seconds each, so a single path can
# overrun this bound by a minute before the walk notices. (An untracked one
# costs three: ls-files, check-ignore, rev-parse.) Nothing caps how many paths
# a completion names, so without the bound a stalled git -- index.lock
# contention, a network filesystem -- would hold the developer's session for
# minutes.
# Running out stops the walk and returns what was gathered: this scan is
# defence in depth, and a partial answer must never become a blocked close.
GIT_BUDGET_SECONDS = 20.0

# Directory names whose contents were written somewhere else. Every line of
# an untracked file counts as added, so a vendored source file whose header
# narrates its own upstream history would block the close and demand a
# rewrite of code this repository does not own.
VENDORED_DIRS = frozenset(("vendor", "third_party", "node_modules"))


def _repo_root(cwd, cache):
    """Absolute worktree root containing cwd, or "" if git cannot name one."""
    if cwd not in cache:
        rc, out = _git(cwd, "rev-parse", "--show-toplevel")
        cache[cwd] = out.strip() if rc == 0 else ""
    return cache[cwd]


def _is_vendored(path, root=""):
    """True when a path segment BELOW root names a vendored directory.

    Matched against the repository-relative path, never the absolute one. A
    checkout that itself lives under a directory named here -- a clone inside
    third_party/, a CI workspace under node_modules/ -- would otherwise exempt
    every untracked file in the whole repository from the scan, and silently,
    since a skipped path never reaches the caller at all. Only a segment is
    matched, so src/vendored_config.go and myvendor/ are untouched.

    Without a root the absolute path is matched instead: that is the
    over-skipping shape, and it is preferred to the alternative of scanning
    vendored code, because this walk must never turn into a blocked close.
    """
    rel = path
    if root:
        try:
            rel = os.path.relpath(path, root)
        except ValueError:
            rel = path
    return any(seg in VENDORED_DIRS for seg in rel.replace("\\", "/").split("/"))


# Attribute values that name nothing. "unspecified" and "unset" are the plain
# negatives; "set" is what git prints for a bare attribute written without a
# value, and it names neither a filter driver nor a codec -- there is no
# filter.<name>.* for git to resolve and no encoding to decode with, so
# nothing is converted and the path is safe to compare. Measured: `*.go
# filter` reports "set" and runs no configured command.
_ATTR_UNSET = ("unspecified", "unset", "set")


def _converted(parent, name):
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

    `name` is the file's own basename, NOT the repository-relative path the
    rest of this module addresses blobs by. check-attr resolves a pathname
    against the CURRENT DIRECTORY, and this call runs in the file's own
    directory, so a repository-relative name asks about <parent>/<rel> -- a
    path that does not exist for any file below the root. git answers
    "unspecified" for every attribute of it, and reading that as an answer
    would compare a git-crypt or git-lfs file against its own ciphertext and
    report every line of it as added.
    """
    rc, out = _git(parent, "check-attr", "-z", "filter",
                   "working-tree-encoding", "--", name)
    if rc != 0:
        return True, None
    fields = out.split("\0")
    attrs = {}
    for i in range(0, len(fields) - 2, 3):
        if fields[i] != name:
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

    Absence from HEAD is established POSITIVELY, and kept distinct from the
    failures that merely look like it. ls-tree answers all three states apart
    without reading the blob: rc 0 with a record means the path is in HEAD,
    rc 0 with no record means it is genuinely absent, and a non-zero rc means
    the tree could not be read at all -- a missing or corrupt tree object, or
    a treeless partial clone whose trees this module refuses to fetch.
    Collapsing that third state onto "absent" would make every line of the
    file read as new, which blocks a close on comments the implementer never
    wrote. This module's standing rule is that a question it could not answer
    means skip.

    The pathspec carries `:(top)` because ls-tree matches relative to the
    CURRENT DIRECTORY, and this call runs in the file's own directory rather
    than at the repository root. A bare repository-relative path matches
    nothing from a subdirectory, and ls-tree reports that as rc 0 with no
    record -- a present file read as absent, the same whole-file scan this
    three-way split exists to prevent.
    """
    rc, listed = _git(parent, "ls-tree", "-z", "HEAD", "--", ":(top)" + rel)
    if rc != 0:
        return None
    if not [record for record in listed.split("\0") if record]:
        return new_text.splitlines(), True
    rc_blob, blob = _git_bytes(parent, "cat-file", "blob", "HEAD:" + rel)
    if rc_blob != 0 or len(blob) > READ_CAP_BYTES:
        return None
    return added(blob.decode("utf-8", errors="replace"), new_text), False


def final_files_added_lines(inp, deadline=None, stats=None, contexts=None):
    """{path: [added lines]} for a completion that submitted final_files.

    final_files carries whole file contents and no signal for which lines are
    new, so scanning all of it would block a close on comments the
    implementer never wrote. git supplies the missing signal.

    Rooted at each file's OWN directory rather than this hook's cwd. Under the
    worktree flow the implementer's files live in a nested worktree while this
    hook runs at the main checkout, and from there `ls-files --error-unmatch`
    reports a worktree path as unmatched -- indistinguishable from untracked,
    which would treat every line as added.

    Exit 1 from ls-files means "unmatched"; anything else (128 for a path
    outside a repository) means the question could not be answered, and every
    such path is skipped rather than guessed at. A staged file still matches
    even when the repository has no HEAD yet; that case is caught downstream,
    by the head-existence check the tracked branch runs before reading any
    blob. The same rule governs check-ignore below: only its definite "not
    ignored" status licenses treating a file as new.

    A `stats` dict, if given, is filled with what the caller cannot see from
    the return value: "truncated" says the budget stopped the walk early, and
    "vendored_skipped" counts the paths the exemption above dropped. Both
    outcomes shrink the result silently, so without them a walk that never
    finished is indistinguishable from one that found nothing.
    "optional_locks_dropped" says git rejected a command line carrying
    --no-optional-locks with a 129, so the walk dropped the flag and ran
    without it, leaving the developer index open to a refresh this walk means
    not to cause. 129 is git's generic usage error, so this does not establish
    that the flag itself was what git objected to.

    A `contexts` dict, if given, is filled with the full text each answer was
    derived from. The scanner needs it to tell a block-comment continuation
    from a line of a raw string literal, which cannot be decided from the
    added lines alone. It is an out-param for the same reason `stats` is: the
    return shape is what the close-time hook counts, and widening it would
    break that reader.
    """
    out = {}
    roots = {}
    heads = {}
    vendored_skipped = 0
    truncated = False
    if deadline is None:
        deadline = time.monotonic() + GIT_BUDGET_SECONDS
    for entry in (inp.get("final_files") or []):
        if time.monotonic() > deadline:
            truncated = True
            break
        path = (entry or {}).get("path") or ""
        if not path or not os.path.isabs(path):
            continue
        parent = os.path.dirname(path)
        if not os.path.isdir(parent):
            continue
        rc, listed = _git(parent, "ls-files", "-z", "--full-name",
                          "--error-unmatch", "--", path)
        if rc == 0:
            rel = listed.split("\0")[0]
            if not rel:
                continue
            skip, encoding = _converted(parent, os.path.basename(path))
            if skip:
                continue
            new_text = read_text_capped(path, encoding)
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
            if lines and contexts is not None:
                # Only paths that reach `out` are ever looked up again, and a
                # context is a whole file: holding one for every unchanged
                # path a completion names would grow with the submission
                # rather than with the work.
                contexts[path] = new_text
            continue
        if rc != 1:
            continue
        # check-ignore: 0 = ignored, 1 = not ignored, anything else = it could
        # not tell. Only a definite 1 licenses scanning the whole file as new;
        # reading an error as "not ignored" would scan every line of a file
        # this hook was never able to classify.
        rc_ign, _ = _git(parent, "check-ignore", "-q", "--", path)
        if rc_ign != 1:
            continue
        if _is_vendored(path, _repo_root(parent, roots)):
            vendored_skipped += 1
            continue
        content = (entry or {}).get("content")
        if not isinstance(content, str):
            content = read_text_capped(path)
            if not isinstance(content, str):
                continue
        out[path] = content.splitlines()
        if contexts is not None:
            contexts[path] = content
    if stats is not None:
        stats["truncated"] = truncated
        stats["vendored_skipped"] = vendored_skipped
        stats["optional_locks_dropped"] = not _LOCK_FLAG
    return out
