"""Second tier for the write-time comment guard: a semantic read of a touched block.

The regex tier answers "does this line carry a reference?". This one answers
"does this comment tell the story of how the code changed?", which no pattern
set can decide, and it answers it for the whole block an edit touches.
"""
import concurrent.futures
import fnmatch
import hashlib
import json
import os
import re
import sys
import time
import urllib.error
import urllib.request
from urllib.parse import urlparse

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, os.path.join(os.environ.get("ATG_ROOT", ""), "hooks"))

from comment_scan import (  # noqa: E402
    allow_star, block_state, comment_spans, hash_string_lines, starred_candidate,
)

BLOCK_CHARS = 2000
MAX_BLOCKS = 20
DEFAULT_URL = "https://api.typesafe.ai/v1/systemone"
DEFAULT_MODEL = "jev-1.13.0"
DEFAULT_THRESHOLD = 0.7
LOOPBACK = ("127.0.0.1", "localhost", "::1")


class Config(object):
    __slots__ = ("enabled", "reason", "key", "url", "url_reason", "model", "threshold")


def _threshold(raw):
    """A threshold outside (0, 1] is not a stricter setting, it is a broken one.

    0 flags everything and nan compares False against every probability, so
    both silently replace the policy with something nobody asked for.
    """
    try:
        value = float(raw)
    except (TypeError, ValueError):
        return DEFAULT_THRESHOLD
    if not (0 < value <= 1):
        return DEFAULT_THRESHOLD
    return value


def _url(env):
    """The endpoint, and why it is not the one the environment asked for.

    Environment reaches this hook from a repository's own checked-in
    settings, so an arbitrary URL would be handed the operator's key along
    with the comment text. Loopback is the eval stub; anything else needs the
    operator to say so in their own settings.
    """
    asked = (env.get("ANTI_TANGENT_JEV_URL") or "").strip()
    if not asked or asked == DEFAULT_URL:
        return DEFAULT_URL, ""
    if env.get("ANTI_TANGENT_JEV_URL_TRUSTED") == "1":
        return asked, ""
    try:
        host = urlparse(asked).hostname or ""
    except ValueError:
        return DEFAULT_URL, "unparsable-url"
    if host in LOOPBACK:
        return asked, ""
    return DEFAULT_URL, "untrusted-host"


def config(env, path):
    c = Config()
    c.key = (env.get("TYPESAFE_API_KEY") or "").strip()
    c.model = (env.get("ANTI_TANGENT_JEV_MODEL") or "").strip() or DEFAULT_MODEL
    c.threshold = _threshold(env.get("ANTI_TANGENT_JEV_THRESHOLD"))
    c.url, c.url_reason = _url(env)
    globs = [g for g in (env.get("ANTI_TANGENT_JEV_EXCLUDE") or "").split(":") if g]
    c.enabled, c.reason = False, ""
    if env.get("ANTI_TANGENT_COMMENT_GUARD") == "0":
        c.reason = "guard=0"
    elif env.get("ANTI_TANGENT_JEV") != "1":
        c.reason = "setting"
    elif not c.key:
        c.reason = "no-key"
    elif any(fnmatch.fnmatch(path, g) for g in globs):
        c.reason = "excluded"
    else:
        c.enabled = True
    return c


_SECRET_ASSIGN = re.compile(
    r"(?i)\b([\w.-]*(?:key|secret|token|password|passwd)[\w.-]*)\s*[:=]\s*(?:\"[^\"]*\"|'[^']*'|\S+)")
# A credential-shaped run contains at least one ASCII letter and one digit, or
# is a long hexadecimal run. A hyphen is deliberately NOT in the alphabet --
# "backward-compatibility-preserving" is 33 characters of ordinary English and
# must survive, and a hyphenated credential still trips the assignment rule
# above whenever it is assigned to anything named like a secret.
# The leading lookbehind, not \b, starts the run: word boundary excludes +/ but
# they are in the credential alphabet. The trailing lookahead, not \b, ends it:
# a word boundary does not sit between "=" and the end of a line, so `\b` after
# optional padding can only match by leaving the padding behind.
_MIXED_TOKEN = re.compile(r"(?<![A-Za-z0-9+/_])(?=[A-Za-z0-9+/_]*\d)(?=[A-Za-z0-9+/_]*[A-Za-z])"
                          r"[A-Za-z0-9+/_]{24,}={0,2}(?![A-Za-z0-9+/_=])")
_HEX_TOKEN = re.compile(r"\b[0-9a-fA-F]{32,}\b")


def redact(text):
    text = _SECRET_ASSIGN.sub(lambda m: "%s=<redacted>" % m.group(1), text)
    text = _MIXED_TOKEN.sub("<redacted>", text)
    return _HEX_TOKEN.sub("<redacted>", text)


class Block(object):
    """One comment block an edit touched, as it will be sent."""

    __slots__ = ("text", "line")

    def __init__(self, text, line):
        self.text = text
        self.line = line

    def __repr__(self):
        return "Block(line=%r, text=%r)" % (self.line, self.text)


def _keep_touched(texts, touched_local):
    """The touched lines alone, when they already exceed the cap.

    Dropping the neighbours loses context the model would have used, which is
    the lesser loss: judging a comment the edit did not write is worse than
    judging one with less around it. Past that the touched text itself is cut
    to the cap, keeping its start — something must give at that size, and the
    opening sentence is where a comment says what it is.
    """
    return "\n".join(texts[i] for i in sorted(touched_local))[:BLOCK_CHARS]


def _spans_per_line(path, lines, context):
    """Comment spans for every line, as a tri-state list the run-builder walks.

    Each entry is `None` for a line that carries no comment at all, or a list
    (possibly empty) of stripped span texts for a line that does. The `[]`
    case matters on its own: a comment line with nothing after its marker
    (a bare `//`, a lone ` *`) is still part of a comment block and must not
    read the same as a line with no marker at all -- which is why this asks
    comment_spans for the blank span instead of letting it filter one out.
    """
    state = block_state(path, context)
    docstring = hash_string_lines(path, context)
    out = []
    for i, raw in enumerate(lines):
        if i in docstring:
            out.append(None)
            continue
        star_ok = allow_star(state, raw) if starred_candidate(path, raw) else True
        raw_spans = comment_spans(path, raw, star_ok, keep_empty=True)
        out.append(None if not raw_spans else [s.strip() for s in raw_spans if s.strip()])
    return out


def _touched_indexes(lines, touched):
    """Indexes of context lines this edit wrote, matched by containment.

    An Edit's operands can add a fragment of a file line, which equals no
    line of the post-edit text; the fragment is still what was written. A
    fragment that exact-matches one line can still be a substring of other
    lines too (a duplicated call, a name nested inside a longer line), so an
    exact hit does not excuse a fragment from the substring check -- every
    touched fragment is checked against every line below, not only the ones
    the dict missed.

    The dict pass resolves exact matches in one linear sweep, which is what
    keeps a fresh Write's touched set -- every line of the file, each entry
    equal to its own line once both sides are stripped -- out of the
    substring scan entirely: that pass alone marks every line the scan
    could ever mark. A blank line's stripped text is "", which is never a
    real fragment (`if not frag` drops it before it can match anything) and
    can never contain a non-empty fragment as a substring either, so no
    scan can ever add a blank line to hit. Once hit already covers every
    non-blank line, the scan below is a proven no-op -- hit cannot grow --
    so it is skipped; that is an upper bound on hit, not an assumption
    about which files reach it. Where it does not hold, the touched set is
    an Edit's handful of fragments against a much larger file, and the
    scan runs over that handful, which stays cheap.
    """
    by_stripped = {}
    markable = 0
    for i, line in enumerate(lines):
        stripped = line.strip()
        by_stripped.setdefault(stripped, []).append(i)
        if stripped:
            markable += 1
    hit = set()
    frags = []
    for frag in touched:
        frag = frag.strip()
        if not frag:
            continue
        frags.append(frag)
        exact = by_stripped.get(frag)
        if exact:
            hit.update(exact)
    if frags and len(hit) < markable:
        for i, line in enumerate(lines):
            if any(frag in line for frag in frags):
                hit.add(i)
    return hit


def _window(texts, touched_local):
    """Join texts, keeping the touched lines when the block is over the cap."""
    joined = "\n".join(texts)
    if len(joined) <= BLOCK_CHARS:
        return joined
    first, last = min(touched_local), max(touched_local)
    if len("\n".join(texts[first:last + 1])) > BLOCK_CHARS:
        return _keep_touched(texts, touched_local)
    lo, hi = first, last + 1
    while True:
        grew = False
        if lo > 0 and len("\n".join(texts[lo - 1:hi])) <= BLOCK_CHARS:
            lo -= 1
            grew = True
        if hi < len(texts) and len("\n".join(texts[lo:hi + 1])) <= BLOCK_CHARS:
            hi += 1
            grew = True
        if not grew:
            break
    return "\n".join(texts[lo:hi])[:BLOCK_CHARS]


def build_blocks(path, touched, context, report=False):
    """Comment blocks this edit touched, in file order, capped.

    With report=True, returns (blocks, capped) so a caller can trace that it
    judged less than the edit contained without counting anything itself.
    """
    if context:
        lines = context.splitlines()
        spans = _spans_per_line(path, lines, context)
        touched_idx = _touched_indexes(lines, touched)
        if touched_idx:
            return _runs(spans, touched_idx, report)
    lines = [t for t in touched]
    spans = _spans_per_line(path, lines, None)
    return _runs(spans, set(range(len(lines))), report)


def _trim_blank_edges(texts, local):
    """Leading/trailing blank lines trimmed off a run's texts, or None.

    A blank comment line (no text after its marker) stays inside texts as ""
    so it still reads as the paragraph break it is, but only between real
    content: leading/trailing blanks carry nothing for the model to judge, so
    they are cut off both ends. local is re-indexed to the trimmed texts,
    dropping any index the trim removed. None means nothing touched survived
    the trim -- either the whole run was blank, or every touched line was.
    """
    lo, hi = 0, len(texts)
    while lo < hi and not texts[lo]:
        lo += 1
    while hi > lo and not texts[hi - 1]:
        hi -= 1
    if lo >= hi:
        return None
    local = [j - lo for j in local if lo <= j < hi]
    if not local:
        return None
    return lo, texts[lo:hi], local


def _block_for_run(spans, start, end, touched_idx):
    """The Block for spans[start:end], or None when nothing touched survives.

    Nothing survives either because the run has no touched line at all, or
    because trimming its blank leading/trailing lines removes every touched
    line along with them.
    """
    local = [j - start for j in range(start, end) if j in touched_idx]
    if not local:
        return None
    texts = ["\n".join(redact(s) for s in line_spans) for line_spans in spans[start:end]]
    trimmed = _trim_blank_edges(texts, local)
    if trimmed is None:
        return None
    # The block's own line shifts by however much came off the FRONT, so it
    # still points at the line its first real text sits on.
    lo, texts, local = trimmed
    return Block(_window(texts, local), start + 1 + lo)


def _runs(spans, touched_idx, report=False):
    blocks, i = [], 0
    while i < len(spans):
        if spans[i] is None:
            i += 1
            continue
        start = i
        while i < len(spans) and spans[i] is not None:
            i += 1
        block = _block_for_run(spans, start, i, touched_idx)
        if block is not None:
            blocks.append(block)
    capped = len(blocks) > MAX_BLOCKS
    return (blocks[:MAX_BLOCKS], capped) if report else blocks[:MAX_BLOCKS]


_QUESTION_CACHE = {}


def question(path=None):
    """The shipped Choice question, or None when it cannot be read."""
    path = path or os.path.join(os.path.dirname(os.path.abspath(__file__)),
                                "jev-question.json")
    if path not in _QUESTION_CACHE:
        try:
            with open(path, "rb") as fh:
                q = json.loads(fh.read().decode("utf-8"))
            _QUESTION_CACHE[path] = q if _valid_question(q) else None
        except Exception:
            _QUESTION_CACHE[path] = None
    return _QUESTION_CACHE[path]


def _valid_question(q):
    """A question of the wrong shape is worse than none: it would be sent."""
    if not isinstance(q, dict) or q.get("type") != "choice":
        return False
    criteria = q.get("criteria")
    if not isinstance(criteria, dict):
        return False
    if set(criteria) != {"change_history", "compatibility_contract", "present_behaviour"}:
        return False
    return all(isinstance(v, dict) and v.get("what") for v in criteria.values())


PER_REQUEST_S = 3.0
DEADLINE_S = 4.0
MAX_WORKERS = 4


class Verdict(object):
    __slots__ = ("flagged", "probability", "event", "detail")

    def __init__(self, flagged=None, probability=0.0, event="jev-pass", detail=""):
        self.flagged = flagged
        self.probability = probability
        self.event = event
        self.detail = detail


class _NoRedirects(urllib.request.HTTPRedirectHandler):
    """Refuse every redirect rather than following it with the key attached.

    urlopen follows 3xx by default and carries the Authorization header to
    wherever it is sent. The host rule decides which host may see the key;
    a redirect would let the approved host hand that decision to another.
    """

    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise urllib.error.HTTPError(req.full_url, code, "redirect refused", headers, fp)


_OPENER = urllib.request.build_opener(_NoRedirects)


def _http(url, body, headers, timeout):
    """One POST, one attempt. A retry in a blocking hook only doubles the wait."""
    req = urllib.request.Request(
        url, data=json.dumps(body).encode("utf-8"), method="POST", headers=headers)
    with _OPENER.open(req, timeout=timeout) as resp:
        return json.loads(resp.read().decode("utf-8"))


def _probability(payload):
    return float(payload["answers"]["kind"]["probabilities"]["change_history"])


def _shutdown(pool):
    """Stop scheduling and stop waiting. A running request cannot be cancelled.

    The executor's threads are not daemons and an interpreter-exit handler
    joins them, so a worker stuck in getaddrinfo would hold the process open
    long after this function returned. Callers that must not wait exit through
    os._exit, which skips that handler; see jev_scan.run's contract.
    """
    try:
        pool.shutdown(wait=False, cancel_futures=True)
    except TypeError:
        pool.shutdown(wait=False)


def _deadline(deadline):
    """The caller's absolute deadline, or a fresh DEADLINE_S budget from now.

    A caller that wants block-building time charged to the same budget as
    the requests computes and passes that absolute deadline itself; the
    fresh budget this returns when deadline is None starts only from here.
    """
    return time.monotonic() + DEADLINE_S if deadline is None else deadline


def _submit_ready(pool, queue, pending, cfg, headers, q, transport, end):
    """Top up pending from queue while a worker slot and budget remain.

    Mutates queue and pending in place. Stops the moment the remaining
    budget is non-positive, leaving whatever is left in queue for the
    caller to notice as a deadline miss.
    """
    while queue and len(pending) < MAX_WORKERS:
        remaining = end - time.monotonic()
        if remaining <= 0:
            return
        block = queue.pop(0)
        body = {"model": cfg.model, "state": {"comment": block.text},
                "questions": {"kind": q}}
        pending[pool.submit(transport, cfg.url, body, headers,
                            min(PER_REQUEST_S, remaining))] = block


def _resolve_batch(done, pending, blocks, cfg, errors, best):
    """Resolve one wait() batch in ascending block-index order.

    Within a batch, lowest block index wins, so which of several futures
    that landed in the same wait() call decides is not left to thread
    scheduling; which futures land in the same batch is still
    completion-dependent. Returns as soon as a probability reaches
    cfg.threshold, without resolving the rest of the batch -- that flag is
    the answer, and finishing the batch would only spend budget confirming a
    verdict already reached.
    """
    for fut in sorted(done, key=lambda f: blocks.index(pending[f])):
        block = pending.pop(fut)
        try:
            p = _probability(fut.result())
        except Exception as exc:
            errors.append(type(exc).__name__)
            continue
        best = max(best, p)
        if p >= cfg.threshold:
            return Verdict(block, p, "jev-block"), best
    return None, best


def judge(blocks, cfg, transport=None, deadline=None):
    """The first flag to complete, or a clean verdict, inside the deadline.

    Completion decides, not file position: the hook refuses the write either
    way, and waiting for an earlier block to come back would spend the budget
    on ordering nobody reads. Several requests can land in one wait, and a
    set has no order, so a batch is resolved by block index -- that is what
    keeps two futures completing together from leaving the result to thread
    scheduling. Which requests land in the same batch is still
    completion-dependent, which is why the deadline tests assert a bound on
    elapsed time rather than an exact outcome.

    Resolution, connection and read all happen inside the worker, because a
    socket timeout does not bound getaddrinfo. That makes a worker
    unstoppable, so the deadline is enforced by walking away from it rather
    than by cancelling it.
    """
    # Computed before question() so a slow load of the question file spends
    # the same budget the requests do, not a bonus on top of it.
    end = _deadline(deadline)
    q = question()
    if q is None:
        return Verdict(event="jev-error", detail="no-question-file")
    transport = transport or _http
    headers = {"Authorization": "Bearer " + cfg.key, "Content-Type": "application/json"}
    errors, best, pending, queue = [], 0.0, {}, list(blocks)
    pool = concurrent.futures.ThreadPoolExecutor(max_workers=MAX_WORKERS)
    try:
        while True:
            _submit_ready(pool, queue, pending, cfg, headers, q, transport, end)
            if not pending:
                # Queue empty too: every block resolved. Queue non-empty:
                # the deadline ran out before this pass could submit it.
                if queue:
                    errors.append("deadline")
                break
            left = end - time.monotonic()
            if left <= 0:
                errors.append("deadline")
                break
            done, _ = concurrent.futures.wait(
                pending, timeout=left,
                return_when=concurrent.futures.FIRST_COMPLETED)
            if not done:
                errors.append("deadline")
                break
            # A flag stands even when another request failed: the flag is
            # evidence, the failure is only missing evidence, and waiting
            # for the rest would spend budget to reach the same refusal.
            verdict, best = _resolve_batch(done, pending, blocks, cfg, errors, best)
            if verdict is not None:
                return verdict
    finally:
        _shutdown(pool)
    if errors:
        return Verdict(probability=best, event="jev-error", detail=errors[0])
    return Verdict(probability=best)


STRIKE_TTL_S = 1800
STRIKE_LIMIT = 2
BREAKER_S = 60
BREAKER_FILE = "jev-breaker"


def _strike_path(directory, session, path):
    key = hashlib.sha256(("%s\0%s" % (session, path)).encode("utf-8")).hexdigest()[:16]
    return os.path.join(directory, "jev-strike-%s" % key)


def strike(directory, session, path):
    """How many times this file has been refused in this session, counting now.

    A stamp older than the TTL is a different sitting of work: it starts the
    count again rather than spending a refusal the writer never saw.
    """
    stamp = _strike_path(directory, session, path)
    count = 0
    try:
        if os.path.exists(stamp) and time.time() - os.path.getmtime(stamp) <= STRIKE_TTL_S:
            with open(stamp) as fh:
                count = int((fh.read() or "0").strip() or 0)
        count += 1
        # Two Edit hooks can run at once. The write is atomic so a reader
        # never sees a half-written count; a lost update costs one extra
        # refusal, which is the safe direction for a gate.
        tmp = "%s.%d" % (stamp, os.getpid())
        with open(tmp, "w") as fh:
            fh.write(str(count))
        os.replace(tmp, stamp)
        return count
    except Exception:
        return count or 1


def breaker_trip(directory):
    try:
        with open(os.path.join(directory, BREAKER_FILE), "w") as fh:
            fh.write(str(int(time.time())))
    except Exception:
        pass


def breaker_open(directory):
    try:
        stamp = os.path.join(directory, BREAKER_FILE)
        return os.path.exists(stamp) and time.time() - os.path.getmtime(stamp) <= BREAKER_S
    except Exception:
        return False


BLOCK_MESSAGE = (
    "BLOCKED: a comment this edit touches reads as change history.\n\n"
    "  %s\n\n"
    "  -> change_history %.2f\n\n"
    "Comments explain behaviour, an invariant or a hazard; how the code got here belongs in\n"
    "git. This covers the whole comment you touched, not only the line you added: history\n"
    "already in it is cleaned up as part of the change. Rewrite those lines and retry.\n")

YIELD_MESSAGE = (
    "NOTE: this comment was refused twice and is being allowed through.\n\n"
    "  %s\n\n"
    "It will be judged again at task close by validate_completion. If you believe the\n"
    "verdict is wrong, say so to the operator rather than rewriting it a third time.\n")


WARN_MESSAGE = (
    "NOTE: the comment check could not reach TypeSafe (%s). This write was allowed "
    "and the check is paused for 60 seconds.\nThe trace log carries the failure "
    "class; this warning is printed once per session.\n")

# A distinct wording for the one failure that never dials out: building the
# blocks alone used up the whole budget, so "could not reach" would claim a
# network attempt that never happened.
WARN_MESSAGE_NO_REQUEST = (
    "NOTE: the comment check ran out of its time budget (%s) before it could contact "
    "TypeSafe; no request was sent. This write was allowed and the check is paused "
    "for 60 seconds.\nThe trace log carries the failure class; this warning is "
    "printed once per session.\n")


def _warn_once(trace_dir, session, detail):
    """One line per session, or a silent permanent fail-open goes unnoticed.

    Failures allow the write, so an expired CA bundle or a refused proxy looks
    exactly like a clean run from the outside. The stamp keeps that from
    becoming a warning on every edit.
    """
    try:
        stamp = os.path.join(trace_dir, "jev-warned-%s" % hashlib.sha256(
            session.encode("utf-8")).hexdigest()[:12])
        if os.path.exists(stamp) and time.time() - os.path.getmtime(stamp) <= STRIKE_TTL_S:
            return ""
        with open(stamp, "w") as fh:
            fh.write(detail)
        template = WARN_MESSAGE_NO_REQUEST if detail == "deadline-before-request" else WARN_MESSAGE
        return template % detail
    except Exception:
        return ""


def _failed(trace_dir, session, detail):
    """One exit for every failure: allow the write, open the breaker, warn once."""
    breaker_trip(trace_dir)
    return 0, "jev-error|%s" % detail, _warn_once(trace_dir, session, detail)


def run(path, touched, context, env, session, trace_dir):
    """Judge the touched blocks. Returns (exit_code, stdout_event, stderr_text).

    The transport is never injectable from the environment. A seam for it
    would be a bypass: a repository's own settings reach these hooks, so
    anything that can answer instead of the service can also disable the gate.
    Tests point the URL at loopback, which the host rule already allows.
    """
    # The budget covers the whole tier, so the clock starts here rather than
    # inside judge(): building blocks for a large write is not free, and a
    # deadline that only bounds the waiting is not the one the hook promises.
    until = time.monotonic() + DEADLINE_S
    try:
        cfg = config(env, path)
        if not cfg.enabled:
            return 0, "jev-skip|%s" % cfg.reason, ""
        if breaker_open(trace_dir):
            return 0, "jev-skip|breaker", ""
        blocks, capped = build_blocks(path, touched, context, report=True)
        if not blocks:
            return 0, "jev-skip|no-blocks", ""
        if time.monotonic() >= until:
            return _failed(trace_dir, session, "deadline-before-request")
        verdict = judge(blocks, cfg, deadline=until)
        if verdict.event == "jev-error":
            return _failed(trace_dir, session, verdict.detail)
        if verdict.flagged is None:
            return 0, "jev-pass|blocks=%d%s" % (len(blocks), ",capped" if capped else ""), ""
        count = strike(trace_dir, session, path)
        quoted = verdict.flagged.text[:400]
        if count > STRIKE_LIMIT:
            return 0, "jev-yield|%s" % path, YIELD_MESSAGE % quoted
        return 4, "jev-block|p=%.2f" % verdict.probability, \
            BLOCK_MESSAGE % (quoted, verdict.probability)
    except Exception as exc:
        # Through the same door as every other failure: an unexpected error is
        # the one most likely to repeat on the next edit, so it must open the
        # breaker rather than be paid for again immediately.
        try:
            return _failed(trace_dir, session, type(exc).__name__)
        except Exception:
            return 0, "jev-error|%s" % type(exc).__name__, ""
