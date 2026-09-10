"""git-derived added lines for a completion that submitted final_files.

Imported by check-task-complete's embedded Python. It lives in a module
rather than in that heredoc so its git calls can be stubbed by a test: the
check-ignore branch below has no other way to be exercised, because a
fixture repository cannot reliably produce an unanswerable status.
"""
import os
import subprocess

from comment_scan import read_text_capped


def _git(cwd, *args):
    """Run git rooted at cwd with output formatting pinned. -> (rc, stdout).

    Prefixes are pinned because the hunk parser matches "+++ b/" only, and a
    user with diff.mnemonicPrefix=true gets "+++ w/" while diff.noprefix=true
    gets a bare path — either one silently yields zero files rather than an
    error. core.quotePath=false keeps non-ASCII paths readable.
    """
    cmd = ["git", "-C", cwd,
           "-c", "diff.noprefix=false",
           "-c", "diff.mnemonicPrefix=false",
           "-c", "core.quotePath=false",
           "--no-pager"] + list(args)
    try:
        p = subprocess.run(cmd, capture_output=True, text=True, timeout=10)
    except Exception:
        return 127, ""
    return p.returncode, p.stdout


def final_files_added_lines(inp):
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
    for entry in (inp.get("final_files") or []):
        path = (entry or {}).get("path") or ""
        if not path or not os.path.isabs(path):
            continue
        parent = os.path.dirname(path)
        if not os.path.isdir(parent):
            continue
        rc, _ = _git(parent, "ls-files", "--error-unmatch", "--", path)
        if rc == 0:
            rc_diff, diff = _git(parent, "diff", "--no-color", "--no-ext-diff",
                                 "HEAD", "--", path)
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
        content = (entry or {}).get("content")
        if not isinstance(content, str):
            content = read_text_capped(path)
            if not isinstance(content, str):
                continue
        out[path] = content.splitlines()
    return out
