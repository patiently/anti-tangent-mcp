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
import os
import subprocess
import time

from comment_scan import read_text_capped


def _git(cwd, *args):
    """Run git rooted at cwd with output formatting pinned. -> (rc, stdout).

    final_files_added_lines below only checks a diff line for a leading
    "+++", which every prefix style still produces, so the diff pins are not
    load-bearing for it. They are pinned anyway so this module's own parsing
    never has to anticipate the prefix and quoting styles a developer's git
    configuration can produce. core.fsmonitor is pinned for a different
    reason: a repository config can point it at an arbitrary command, which
    git would otherwise run from inside this hook. --no-optional-locks keeps
    these read-only questions from writing an index.
    """
    cmd = ["git", "-C", cwd,
           "-c", "diff.noprefix=false",
           "-c", "diff.mnemonicPrefix=false",
           "-c", "core.quotePath=false",
           "-c", "core.fsmonitor=false",
           "--no-optional-locks",
           "--no-pager"] + list(args)
    try:
        p = subprocess.run(cmd, capture_output=True, text=True, timeout=10)
    except Exception:
        return 127, ""
    return p.returncode, p.stdout


# A wall-clock bound on the whole per-path walk. Each path can cost two git
# calls of up to ten seconds each and nothing caps how many paths a
# completion names, so a stalled git -- index.lock contention, a network
# filesystem -- would otherwise hold the developer's session for minutes.
# Running out stops the walk and returns what was gathered: this scan is
# defence in depth, and a partial answer must never become a blocked close.
GIT_BUDGET_SECONDS = 20.0

# Directory names whose contents were written somewhere else. Every line of
# an untracked file counts as added, so a vendored source file whose header
# narrates its own upstream history would block the close and demand a
# rewrite of code this repository does not own.
VENDORED_DIRS = frozenset(("vendor", "third_party", "node_modules"))


def _is_vendored(path):
    return any(seg in VENDORED_DIRS for seg in path.replace("\\", "/").split("/"))


def final_files_added_lines(inp, deadline=None):
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
    outside a repository or a repo with no HEAD) means the question could not
    be answered, and every such path is skipped rather than guessed at. The
    same rule governs check-ignore below: only its definite "not ignored"
    status licenses treating a file as new.
    """
    out = {}
    if deadline is None:
        deadline = time.monotonic() + GIT_BUDGET_SECONDS
    for entry in (inp.get("final_files") or []):
        if time.monotonic() > deadline:
            break
        path = (entry or {}).get("path") or ""
        if not path or not os.path.isabs(path):
            continue
        parent = os.path.dirname(path)
        if not os.path.isdir(parent):
            continue
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
        if rc != 1:
            continue
        # check-ignore: 0 = ignored, 1 = not ignored, anything else = it could
        # not tell. Only a definite 1 licenses scanning the whole file as new;
        # reading an error as "not ignored" would scan every line of a file
        # this hook was never able to classify.
        rc_ign, _ = _git(parent, "check-ignore", "-q", "--", path)
        if rc_ign != 1:
            continue
        if _is_vendored(path):
            continue
        content = (entry or {}).get("content")
        if not isinstance(content, str):
            content = read_text_capped(path)
            if not isinstance(content, str):
                continue
        out[path] = content.splitlines()
    return out
