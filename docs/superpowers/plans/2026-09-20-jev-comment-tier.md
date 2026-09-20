# Jev Comment Tier Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a second, semantic tier to `anti-tangent-guard`'s write-time comment hook that asks TypeSafe's Jev whether a touched comment block narrates change history, and blocks when it does.

**Architecture:** The regex tier in `comment_scan.py` runs first and unchanged. When it finds nothing and the tier is enabled, a new `jev_scan.py` builds the comment blocks this edit touched, sends one request per block to `https://api.typesafe.ai/v1/systemone`, and blocks on a `change_history` probability at or above the threshold. Every failure allows the write. `comment_scan.py` stays network-free; only a pure helper is extracted from it.

**Tech Stack:** Python 3 standard library only (`urllib.request`, `concurrent.futures`, `json`, `fnmatch`) — the hooks ship no dependencies and run under `python3 -I`. Bash for the hook wrapper and the eval suite. Tests are `unittest`, run directly.

**Spec:** `docs/superpowers/specs/2026-09-20-jev-comment-tier-design.md`

## Global Constraints

- **Fail open, always.** Any error, timeout, unparsable response or unexpected exception allows the write **in the absence of a flag**. A failure never blocks and never turns another block's flag into a pass: a confident flag on one block still refuses the write even when a second request failed, because the flag is evidence and the failure is only missing evidence. A hook that raises must never reach the user as a traceback.
- **No network in unit tests.** Every test stubs the transport. The repository rule is absolute (`CLAUDE.md`, Testing Conventions).
- **`comment_scan.py` stays network-free and behaviour-identical.** `violations()` must return exactly what it returns today; `evals/fp-report.sh` must stay at zero false positives and `evals/run.sh` must stay green after the helper extraction.
- **The key goes only to the default host or loopback** unless `ANTI_TANGENT_JEV_URL_TRUSTED=1`.
- **The tier is off unless** `ANTI_TANGENT_JEV=1` **and** `TYPESAFE_API_KEY` is non-empty. `ANTI_TANGENT_COMMENT_GUARD=0` disables both tiers.
- **Comment policy applies to this plan's own code.** No comment may reference a task number, this plan, an issue or a version as history. `ANTI_TANGENT_TICKET_PATTERN=YN-\d+` is set in this environment.
- **Budget for the whole tier: 4 seconds wall clock**, measured from the moment `run()` is entered — configuration, block building and requests all inside it — 3 seconds per request, at most 20 blocks, 2,000 characters per block. The bound is enforced by abandoning a slow worker, not by cancelling it: a request already in flight cannot be stopped, and the executor's threads are joined by an interpreter-exit handler, so the hook body leaves through `os._exit` once it has a decision.

**User decisions (already made):**
- Write time only — the `PreToolUse` Edit/Write hook. Not the close-time hook, not the MCP server.
- A flagged comment blocks, like a regex hit.
- Off unless `ANTI_TANGENT_JEV=1` and a key is present.
- No path restriction by default: the setting is the control.
- A comment describing an earlier version's bug is change history, including in a test.
- A touched comment block is judged whole, pre-existing lines included — history in a block an edit touches gets cleaned up.
- The tier blocks at most twice for the same file within a 30-minute window in a session, then yields to the close-time reviewer. The window is what keeps a stamp from an abandoned sitting of work spending a later edit's refusals; a lost race between two concurrent hooks can cost one extra refusal, never a missing one.

---

### Task 0: Rebase onto main and open 0.24.0

**Goal:** This branch builds on current main and carries the version the release workflow expects.

**Files:**
- Modify: `CHANGELOG.md` (new `## [0.24.0]` section at the top of the entries)
- Modify: `VERSION`

**Acceptance Criteria:**
- [ ] The branch is named `version/0.24.0` and contains main's current head as an ancestor.
- [ ] `CHANGELOG.md` has a `## [0.24.0] - 2026-09-20` section with an `### Added` subsection.
- [ ] `VERSION` reads `0.24.0`.
- [ ] `go build ./...` and `go test -race ./...` pass on the rebased tree.

**Verify:**

```bash
go build ./... \
  && go test -race ./... \
  && git merge-base --is-ancestor origin/main HEAD \
  && grep -q '^## \[0.24.0\] - 2026-09-20$' CHANGELOG.md \
  && awk '/^## \[0.24.0\]/{f=1} f&&/^### Added/{ok=1} END{exit !ok}' CHANGELOG.md \
  && [ "$(cat VERSION)" = "0.24.0" ] \
  && [ "$(git branch --show-current)" = "version/0.24.0" ]
```

→ exit 0

**Steps:**

- [ ] **Step 1: Fetch and rebase**

```bash
git fetch origin main
git rebase origin/main
```

If the rebase conflicts in `CHANGELOG.md`, keep both sections: main's newest entry stays above the older ones, and this branch adds a new one in Step 3.

- [ ] **Step 2: Rename the branch**

```bash
git branch -m version/0.24.0
git branch --show-current
```

- [ ] **Step 3: Open the changelog section**

Insert directly above the current top `## [` line in `CHANGELOG.md`:

```markdown
## [0.24.0] - 2026-09-20

### Added

- `anti-tangent-guard`'s write-time comment hook gains a second tier: with `ANTI_TANGENT_JEV=1`
  and `TYPESAFE_API_KEY` set, a comment block the edit touches is sent to TypeSafe's Jev, and a
  block that reads as change history is refused. The regex tells run first and unchanged; the tier
  never runs when they already refuse the write, and every failure allows it.
```

- [ ] **Step 4: Set the version**

```bash
printf '0.24.0\n' > VERSION
```

- [ ] **Step 5: Verify and commit**

```bash
go build ./... && go test -race ./...
git add CHANGELOG.md VERSION
git commit -m "chore: open 0.24.0"
```

```json:metadata
{"files": ["CHANGELOG.md", "VERSION"], "verifyCommand": "go build ./... && go test -race ./... && git merge-base --is-ancestor origin/main HEAD && grep -q '^## \\[0.24.0\\] - 2026-09-20$' CHANGELOG.md && [ \"$(cat VERSION)\" = \"0.24.0\" ]", "acceptanceCriteria": ["branch is version/0.24.0 with main as ancestor", "CHANGELOG has the dated 0.24.0 section with ### Added", "VERSION reads 0.24.0", "go build ./... and go test -race ./... pass"], "modelTier": "mechanical"}
```

---

### Task 1: Extract the block-continuation decision from `violations()`

**Goal:** The judgement "may this `*`-led line be read as a block-comment continuation?" becomes a pure pair of functions both tiers can call, with `violations()` behaving exactly as before.

**Files:**
- Modify: `plugin/anti-tangent-guard/hooks/comment_scan.py` (add `block_state` / `allow_star`; rewrite the body of `violations()`'s loop to use them)
- Modify: `plugin/anti-tangent-guard/hooks/comment_scan_test.py` (append a test class)

**Acceptance Criteria:**
- [ ] `block_state(path, context)` returns `None` for no context, `True` when the context cannot be tokenized, else a `(block_lines, context_lines)` pair.
- [ ] `allow_star(state, raw)` returns True for `None`/`True` state, and otherwise `raw in block_lines or raw not in context_lines`.
- [ ] `violations()` computes the state lazily — a call with no starred candidate never tokenizes the context.
- [ ] `_ScanTimeout` still propagates out of the state computation rather than being absorbed.
- [ ] `evals/fp-report.sh` reports zero false positives and `evals/run.sh` passes unchanged.

**Verify:** `python3 plugin/anti-tangent-guard/hooks/comment_scan_test.py -v && bash plugin/anti-tangent-guard/evals/fp-report.sh && bash plugin/anti-tangent-guard/evals/run.sh` → OK, zero false positives, all eval cases pass

**Steps:**

- [ ] **Step 1: Write the failing tests**

Append to `plugin/anti-tangent-guard/hooks/comment_scan_test.py`, before the `unittest.main()` block:

```python
class BlockStateHelper(unittest.TestCase):
    # A starred line inside a real KDoc block is a comment; the same bytes
    # inside a raw string are string content. Only the file separates them,
    # which is what block_state answers and allow_star applies.
    GO_RAW = 'package x\n\nconst s = `\n * Previously the parser rejected tabs.\n`\n'
    KDOC = '/**\n * Previously the parser rejected tabs.\n */\nfun a() {}\n'

    def _call(self, path, context, raw):
        code = ("import sys, json; sys.path.insert(0, %r);"
                "from comment_scan import block_state, allow_star;"
                "print(json.dumps(allow_star(block_state(%r, %r), %r)))"
                % (HOOKS, path, context, raw))
        r = subprocess.run([sys.executable, "-c", code], capture_output=True,
                           text=True, timeout=30)
        self.assertEqual(r.returncode, 0, r.stderr)
        return json.loads(r.stdout)

    def test_star_in_raw_string_is_declined(self):
        self.assertFalse(self._call("X.go", self.GO_RAW,
                                    " * Previously the parser rejected tabs."))

    def test_star_in_kdoc_is_allowed(self):
        self.assertTrue(self._call("X.kt", self.KDOC,
                                   " * Previously the parser rejected tabs."))

    def test_no_context_allows_shape_only(self):
        self.assertTrue(self._call("X.kt", None, " * anything"))

    def test_line_absent_from_context_allows_shape_only(self):
        self.assertTrue(self._call("X.go", self.GO_RAW, " * a fragment"))

    # The criteria name laziness and the timeout, so both are asserted rather
    # than assumed: a context walk on every line would cost a tokenize per
    # call, and a swallowed _ScanTimeout turns a bounded scan unbounded.
    def test_context_is_not_tokenized_without_a_starred_candidate(self):
        code = ("import sys; sys.path.insert(0, %r);"
                "import comment_scan as cs;"
                "cs.block_comment_lines = lambda *a, **k: (_ for _ in ()).throw("
                "AssertionError('tokenized'));"
                "print(cs.violations('X.go', ['// plain line'], 'ctx\\n', strict=True))"
                % HOOKS)
        r = subprocess.run([sys.executable, "-c", code], capture_output=True,
                           text=True, timeout=30)
        self.assertEqual(r.returncode, 0, r.stderr)

    def test_tokenizer_failure_falls_back_to_shape_only(self):
        code = ("import sys; sys.path.insert(0, %r);"
                "import comment_scan as cs;"
                "cs.block_comment_lines = lambda *a, **k: (_ for _ in ()).throw(ValueError('x'));"
                "print(cs.block_state('X.kt', 'ctx'))" % HOOKS)
        r = subprocess.run([sys.executable, "-c", code], capture_output=True,
                           text=True, timeout=30)
        self.assertEqual(r.stdout.strip(), "True", r.stderr)

    def test_scan_timeout_is_not_absorbed(self):
        code = ("import sys; sys.path.insert(0, %r);"
                "import comment_scan as cs;"
                "cs.block_comment_lines = lambda *a, **k: (_ for _ in ()).throw(cs._ScanTimeout());"
                "cs.block_state('X.kt', 'ctx')" % HOOKS)
        r = subprocess.run([sys.executable, "-c", code], capture_output=True,
                           text=True, timeout=30)
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("_ScanTimeout", r.stderr)
```

- [ ] **Step 2: Run to verify it fails**

Run: `python3 plugin/anti-tangent-guard/hooks/comment_scan_test.py BlockStateHelper -v`
Expected: FAIL — `ImportError: cannot import name 'block_state'`

- [ ] **Step 3: Add the helpers**

In `comment_scan.py`, directly above `def violations(`:

```python
def block_state(path, context):
    """What `context` says about `*`-led lines, or None when it says nothing.

    Returns None when there is no context to ask, True when the context
    cannot be tokenized (callers then fall back to the shape-only answer),
    and otherwise the pair allow_star() needs. Only _ScanTimeout escapes: it
    bounds the whole call and must not be absorbed here.
    """
    if context is None:
        return None
    try:
        return (block_comment_lines(context, _interpolates(path), _backticks(path)),
                set(context.splitlines()))
    except _ScanTimeout:
        raise
    except Exception:
        return True


def allow_star(state, raw):
    """Whether comment_spans may read raw as a block continuation.

    A line the context does not contain is one the context cannot speak for:
    an edit's operands can hand over a fragment of a file line, and reading
    that absence as "outside a block" would decline a genuine continuation.
    """
    if state is None or state is True:
        return True
    block_lines, context_lines = state
    return raw in block_lines or raw not in context_lines
```

- [ ] **Step 4: Use them from `violations()`**

Replace the loop body's state handling in `violations()` — the `block_lines` / `context_lines` block — with the lazy call:

```python
    out = []
    state = None
    computed = False
    try:
        with scan_deadline():
            for raw in added_lines:
                star_ok = True
                if context is not None and starred_candidate(path, raw):
                    if not computed:
                        state = block_state(path, context)
                        computed = True
                    star_ok = allow_star(state, raw)
                for span in comment_spans(path, raw, star_ok):
                    hit = next((why for pat, why in TELLS if pat.search(span)), None)
                    if hit is not None:
                        out.append((raw.strip(), hit))
                        break
    except Exception:
        if strict:
            raise
        return []
    return out
```

- [ ] **Step 5: Run the tests and both gates**

```bash
python3 plugin/anti-tangent-guard/hooks/comment_scan_test.py -v
bash plugin/anti-tangent-guard/evals/fp-report.sh
bash plugin/anti-tangent-guard/evals/run.sh
```
Expected: OK; zero false positives; every eval case passes. A change in any of the three means the extraction altered behaviour — fix it rather than updating the gate.

- [ ] **Step 6: Commit**

```bash
git add plugin/anti-tangent-guard/hooks/comment_scan.py plugin/anti-tangent-guard/hooks/comment_scan_test.py
git commit -m "refactor(guard): make the block-continuation decision callable"
```

```json:metadata
{"files": ["plugin/anti-tangent-guard/hooks/comment_scan.py", "plugin/anti-tangent-guard/hooks/comment_scan_test.py"], "verifyCommand": "python3 plugin/anti-tangent-guard/hooks/comment_scan_test.py -v && bash plugin/anti-tangent-guard/evals/fp-report.sh && bash plugin/anti-tangent-guard/evals/run.sh", "acceptanceCriteria": ["block_state returns None/True/pair", "allow_star applies it", "state computed lazily", "_ScanTimeout propagates", "fp-report and run.sh unchanged"], "modelTier": "standard"}
```

---

### Task 2: Build the comment blocks an edit touches

**Goal:** `jev_scan.build_blocks()` turns a hook payload's touched lines plus post-edit text into the exact comment text that will be judged.

**Files:**
- Create: `plugin/anti-tangent-guard/hooks/jev_scan.py`
- Create: `plugin/anti-tangent-guard/hooks/jev_scan_test.py`
- Modify: `plugin/anti-tangent-guard/hooks/comment_scan.py` (add `hash_string_lines`)

**Acceptance Criteria:**
- [ ] A run of consecutive lines carrying comment spans is one block when the edit touched any line in it; untouched runs are not returned.
- [ ] Only comment spans are returned, never the raw code around a trailing comment.
- [ ] A touched line that is a fragment of a file line matches that line by containment.
- [ ] When no touched line matches any context line, the fallback returns blocks built from the touched lines alone.
- [ ] A starred line inside a Go raw string is not a comment line; a column-zero `#` inside a Python triple-quoted string is not either.
- [ ] A block longer than 2,000 characters is windowed around the touched lines, never truncated from the top.
- [ ] At most 20 blocks are returned, and `build_blocks()` itself reports that it capped — the caller never has to know the uncapped count.
- [ ] When the touched lines alone exceed 2,000 characters, the neighbours are dropped first and the touched text is then cut to the cap from its end, keeping its first 2,000 characters. Neighbouring text is never kept in preference to touched text.

**Verify:** `python3 plugin/anti-tangent-guard/hooks/jev_scan_test.py BlockBuilder -v` → OK

**Steps:**

- [ ] **Step 1: Write the failing tests**

Create `plugin/anti-tangent-guard/hooks/jev_scan_test.py`:

```python
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


if __name__ == "__main__":
    unittest.main()
```

- [ ] **Step 2: Run to verify it fails**

Run: `python3 plugin/anti-tangent-guard/hooks/jev_scan_test.py BlockBuilder -v`
Expected: FAIL — `ModuleNotFoundError: No module named 'jev_scan'`

- [ ] **Step 3: Add the docstring-line helper to `comment_scan.py`**

Append near `block_comment_lines`:

```python
_TRIPLE = ('"""', "'''")


def hash_string_lines(path, text):
    """Line texts sitting inside an open triple-quoted string, as a set.

    A column-zero `#` is read as a comment by comment_spans, which is right
    in code and wrong inside a docstring. Only hash-family files have the
    shape, and only a caller with the whole file can tell the two apart.
    """
    # `text` is None on the no-context fallback path, where there is no file
    # to ask about strings at all.
    if not text or "#" not in openers(path):
        return set()
    inside, delim, out = False, None, set()
    for line in text.splitlines():
        if inside:
            out.add(line)
            if delim in line:
                inside, delim = False, None
            continue
        for d in _TRIPLE:
            if line.count(d) % 2 == 1:
                inside, delim = True, d
                break
    return out
```

- [ ] **Step 4: Write `jev_scan.py`'s builder**

```python
"""Second tier for the write-time comment guard: a semantic read of a touched block.

The regex tier answers "does this line carry a reference?". This one answers
"does this comment tell the story of how the code changed?", which no pattern
set can decide, and it answers it for the whole block an edit touches.
"""
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, os.path.join(os.environ.get("ATG_ROOT", ""), "hooks"))

from comment_scan import (  # noqa: E402
    allow_star, block_state, comment_spans, hash_string_lines, starred_candidate,
)

BLOCK_CHARS = 2000
MAX_BLOCKS = 20


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
    """Comment spans for every line, with the file's own answer on `*` and `#`."""
    state = block_state(path, context)
    docstring = hash_string_lines(path, context)
    out = []
    for raw in lines:
        if raw in docstring:
            out.append([])
            continue
        star_ok = allow_star(state, raw) if starred_candidate(path, raw) else True
        out.append([s.strip() for s in comment_spans(path, raw, star_ok) if s.strip()])
    return out


def _touched_indexes(lines, touched):
    """Indexes of context lines this edit wrote, matched by containment.

    An Edit's operands can add a fragment of a file line, which equals no
    line of the post-edit text; the fragment is still what was written.
    """
    hit = set()
    for frag in touched:
        frag = frag.strip()
        if not frag:
            continue
        for i, line in enumerate(lines):
            if line == frag or frag in line:
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


def _runs(spans, touched_idx, report=False):
    blocks, i = [], 0
    while i < len(spans):
        if not spans[i]:
            i += 1
            continue
        start = i
        while i < len(spans) and spans[i]:
            i += 1
        local = [j - start for j in range(start, i) if j in touched_idx]
        if not local:
            continue
        texts = ["\n".join(line_spans) for line_spans in spans[start:i]]
        blocks.append(Block(_window(texts, local), start + 1))
    capped = len(blocks) > MAX_BLOCKS
    return (blocks[:MAX_BLOCKS], capped) if report else blocks[:MAX_BLOCKS]
```

- [ ] **Step 5: Run the tests**

Run: `python3 plugin/anti-tangent-guard/hooks/jev_scan_test.py BlockBuilder -v`
Expected: PASS — every test in the class, with no count pinned here, since the class grows

- [ ] **Step 6: Commit**

```bash
git add plugin/anti-tangent-guard/hooks/jev_scan.py plugin/anti-tangent-guard/hooks/jev_scan_test.py plugin/anti-tangent-guard/hooks/comment_scan.py
git commit -m "feat(guard): build the comment blocks an edit touches"
```

```json:metadata
{"files": ["plugin/anti-tangent-guard/hooks/jev_scan.py", "plugin/anti-tangent-guard/hooks/jev_scan_test.py", "plugin/anti-tangent-guard/hooks/comment_scan.py"], "verifyCommand": "python3 plugin/anti-tangent-guard/hooks/jev_scan_test.py BlockBuilder -v", "acceptanceCriteria": ["touched run is one block including untouched neighbours", "untouched runs excluded", "spans only, never raw code", "fragment matches by containment", "fallback when nothing matches", "raw-string star and docstring hash excluded", "windowed around touched lines", "capped at 20 blocks"], "modelTier": "standard"}
```

---

### Task 3: The enablement gate and configuration

**Goal:** `jev_scan.config()` answers whether the tier runs at all, and with what settings, refusing to hand the key to a host a repository chose.

**Files:**
- Modify: `plugin/anti-tangent-guard/hooks/jev_scan.py`
- Modify: `plugin/anti-tangent-guard/hooks/jev_scan_test.py`

**Acceptance Criteria:**
- [ ] The tier is off unless `ANTI_TANGENT_JEV` is exactly `1` and `TYPESAFE_API_KEY` is non-empty; each off case reports a distinct reason.
- [ ] `ANTI_TANGENT_COMMENT_GUARD=0` turns it off too.
- [ ] The threshold parses from `ANTI_TANGENT_JEV_THRESHOLD`, and anything outside `(0, 1]` — including `0`, `1.5`, `nan`, `inf`, `abc`, empty — falls back to 0.7.
- [ ] A non-default URL is used only for loopback, or with `ANTI_TANGENT_JEV_URL_TRUSTED=1`; otherwise the default host is used and the reason is reported.
- [ ] A path matching any glob in `ANTI_TANGENT_JEV_EXCLUDE` (colon-separated) turns the tier off for that call.

**Verify:** `python3 plugin/anti-tangent-guard/hooks/jev_scan_test.py Config -v` → OK

**Steps:**

- [ ] **Step 1: Write the failing tests**

Append to `jev_scan_test.py`, before `if __name__`:

```python
class Config(unittest.TestCase):
    BASE = {"ANTI_TANGENT_JEV": "1", "TYPESAFE_API_KEY": "k"}

    def cfg(self, path="x.go", **over):
        env = dict(self.BASE)
        env.update(over)
        return jev_scan.config(env, path)

    def test_enabled_with_setting_and_key(self):
        self.assertTrue(self.cfg().enabled)

    def test_off_without_setting(self):
        c = self.cfg(ANTI_TANGENT_JEV="0")
        self.assertFalse(c.enabled)
        self.assertEqual(c.reason, "setting")

    def test_off_without_key(self):
        c = self.cfg(TYPESAFE_API_KEY="")
        self.assertFalse(c.enabled)
        self.assertEqual(c.reason, "no-key")

    def test_off_when_comment_guard_disabled(self):
        c = self.cfg(ANTI_TANGENT_COMMENT_GUARD="0")
        self.assertFalse(c.enabled)
        self.assertEqual(c.reason, "guard=0")

    def test_off_for_excluded_path(self):
        c = self.cfg(path="/repo/secrets/keys.go",
                     ANTI_TANGENT_JEV_EXCLUDE="*/secrets/*:*/vendor/*")
        self.assertFalse(c.enabled)
        self.assertEqual(c.reason, "excluded")

    def test_threshold_default_and_clamping(self):
        self.assertEqual(self.cfg().threshold, 0.7)
        self.assertEqual(self.cfg(ANTI_TANGENT_JEV_THRESHOLD="0.85").threshold, 0.85)
        for bad in ("0", "-1", "1.5", "nan", "inf", "abc", ""):
            self.assertEqual(self.cfg(ANTI_TANGENT_JEV_THRESHOLD=bad).threshold, 0.7, bad)

    def test_untrusted_url_does_not_receive_the_key(self):
        c = self.cfg(ANTI_TANGENT_JEV_URL="https://evil.example/v1/systemone")
        self.assertEqual(c.url, jev_scan.DEFAULT_URL)
        self.assertEqual(c.url_reason, "untrusted-host")

    def test_loopback_url_is_allowed(self):
        c = self.cfg(ANTI_TANGENT_JEV_URL="http://127.0.0.1:8931/v1/systemone")
        self.assertEqual(c.url, "http://127.0.0.1:8931/v1/systemone")

    def test_trusted_flag_allows_any_host(self):
        c = self.cfg(ANTI_TANGENT_JEV_URL="https://proxy.internal/v1/systemone",
                     ANTI_TANGENT_JEV_URL_TRUSTED="1")
        self.assertEqual(c.url, "https://proxy.internal/v1/systemone")
```

- [ ] **Step 2: Run to verify it fails**

Run: `python3 plugin/anti-tangent-guard/hooks/jev_scan_test.py Config -v`
Expected: FAIL — `AttributeError: module 'jev_scan' has no attribute 'config'`

- [ ] **Step 3: Implement**

Add to `jev_scan.py` (imports `fnmatch` and `urlparse` at the top):

```python
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
```

- [ ] **Step 4: Run the tests**

Run: `python3 plugin/anti-tangent-guard/hooks/jev_scan_test.py Config -v`
Expected: PASS, 9 tests

- [ ] **Step 5: Commit**

```bash
git add plugin/anti-tangent-guard/hooks/jev_scan.py plugin/anti-tangent-guard/hooks/jev_scan_test.py
git commit -m "feat(guard): gate the Jev tier on the setting, the key and the host"
```

```json:metadata
{"files": ["plugin/anti-tangent-guard/hooks/jev_scan.py", "plugin/anti-tangent-guard/hooks/jev_scan_test.py"], "verifyCommand": "python3 plugin/anti-tangent-guard/hooks/jev_scan_test.py Config -v", "acceptanceCriteria": ["off unless setting is 1 and key present, with distinct reasons", "COMMENT_GUARD=0 turns it off", "threshold clamped to (0,1] with 0.7 fallback", "non-default host refused unless loopback or trusted", "exclude globs honoured"], "modelTier": "standard"}
```

---

### Task 4: Redact credentials before anything leaves the machine

**Goal:** A commented-out secret is replaced before a block is sent.

**Files:**
- Modify: `plugin/anti-tangent-guard/hooks/jev_scan.py`
- Modify: `plugin/anti-tangent-guard/hooks/jev_scan_test.py`

**Acceptance Criteria:**
- [ ] An assignment whose name contains `key`, `secret`, `token`, `password` or `passwd` has its value replaced with `<redacted>`, for `=`, `:` and `: ` forms.
- [ ] A bare credential-shaped run is replaced, defined exactly: 24 or more characters from `[A-Za-z0-9+/_]` containing **at least one digit and at least one letter**, with optional `=` padding; or 32 or more hexadecimal characters. A hyphen is not in the alphabet, so hyphenated English is out of scope by construction.
- [ ] Ordinary prose is left untouched, including a hyphenated compound over 24 characters and a single digitless word over 24 characters — neither meets the rule above.
- [ ] Redaction runs on the block text, so it covers pre-existing lines pulled in by the block.

**Verify:** `python3 plugin/anti-tangent-guard/hooks/jev_scan_test.py Redaction -v` → OK

**Steps:**

- [ ] **Step 1: Write the failing tests**

```python
class Redaction(unittest.TestCase):
    def test_named_assignment_is_redacted(self):
        for line in ("export AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI0K7MDENGbPxRfiCY",
                     'api_key: "sk-live-9d8f7a6b5c4d3e2f1a0b"',
                     "password = hunter2hunter2hunter2"):
            self.assertIn("<redacted>", jev_scan.redact(line), line)

    def test_bare_high_entropy_token_is_redacted(self):
        for line in ("the old value was AKIAIOSFODNN7EXAMPLEQWERTYUIOP",
                     "was dGhpcyBpcyBhIGxvbmcgYmFzZTY0IHN0cmluZzEyMw==",
                     "hash 5f4dcc3b5aa765d61d8327deb882cf995f4dcc3b"):
            self.assertIn("<redacted>", jev_scan.redact(line), line)

    def test_padding_is_consumed(self):
        padded = "was dGhpcyBpcyBhIGxvbmcgYmFzZTY0IHN0cmluZzEyMw=="
        self.assertEqual(jev_scan.redact(padded), "was <redacted>")

    def test_boundaries(self):
        short = "A1" + "b" * 21          # 23 characters: under the run length
        exact = "A1" + "b" * 22          # 24 characters: at it
        self.assertEqual(jev_scan.redact(short), short)
        self.assertIn("<redacted>", jev_scan.redact(exact))

    def test_prose_is_untouched(self):
        for line in ("The count cap used to return a plain error to the caller.",
                     "A well-known copy-on-write trade-off, documented upstream.",
                     "a backward-compatibility-preserving migration path",
                     "SupercalifragilisticexpialidociousBehaviour"):
            self.assertEqual(jev_scan.redact(line), line)

    def test_a_credential_cannot_survive_the_block_cap(self):
        secret = "AKIAIOSFODNN7EXAMPLEQWERTYUIOP1234"
        filler = ["// filler %d" % i for i in range(300)]
        context = "\n".join(filler + ["// old value was " + secret]) + "\n"
        blocks = jev_scan.build_blocks("x.go", ["// old value was " + secret], context)
        self.assertTrue(blocks)
        self.assertNotIn(secret[:12], blocks[0].text)
```

- [ ] **Step 2: Run to verify it fails**

Run: `python3 plugin/anti-tangent-guard/hooks/jev_scan_test.py Redaction -v`
Expected: FAIL — no attribute `redact`

- [ ] **Step 3: Implement**

```python
_SECRET_ASSIGN = re.compile(
    r"(?i)\b([\w.-]*(?:key|secret|token|password|passwd)[\w.-]*)\s*[:=]\s*\S+")
# A credential's shape is a long run with no word structure: mixed case AND
# digits, or a long hex run. A hyphen is deliberately NOT in the alphabet --
# "backward-compatibility-preserving" is 33 characters of ordinary English and
# must survive, and a hyphenated credential still trips the assignment rule
# above whenever it is assigned to anything named like a secret.
# A digit AND a letter, 24 characters or more, no hyphen. The digit is what
# ordinary English of that length does not have -- an AWS-style key is all
# caps with digits, a base64 secret is mixed, and
# "Supercalifragilisticexpialidocious" has no digit anywhere in it.
# The trailing lookahead, not \b, ends the run: a word boundary does not sit
# between "=" and the end of a line, so `\b` after optional padding can only
# match by leaving the padding behind.
_MIXED_TOKEN = re.compile(r"\b(?=[A-Za-z0-9+/_]*\d)(?=[A-Za-z0-9+/_]*[A-Za-z])"
                          r"[A-Za-z0-9+/_]{24,}={0,2}(?![A-Za-z0-9+/_=])")
_HEX_TOKEN = re.compile(r"\b[0-9a-fA-F]{32,}\b")


def redact(text):
    text = _SECRET_ASSIGN.sub(lambda m: "%s=<redacted>" % m.group(1), text)
    text = _MIXED_TOKEN.sub("<redacted>", text)
    return _HEX_TOKEN.sub("<redacted>", text)
```

Wire it into `_runs()`, **per span, before windowing**. Task 2 built that line without redaction, because `redact()` did not exist yet; replace it now:

```python
        texts = ["\n".join(redact(s) for s in line_spans) for line_spans in spans[start:i]]
```

Redacting the windowed text instead would let the 2,000-character cut land inside a credential and ship the surviving half, which no pattern would then match.

- [ ] **Step 4: Run the tests**

Run: `python3 plugin/anti-tangent-guard/hooks/jev_scan_test.py Redaction BlockBuilder -v`
Expected: PASS — including the existing trailing-comment test, whose `sk-live-abcdef` value never enters a block because only the comment span is taken.

- [ ] **Step 5: Commit**

```bash
git add plugin/anti-tangent-guard/hooks/jev_scan.py plugin/anti-tangent-guard/hooks/jev_scan_test.py
git commit -m "feat(guard): redact credentials before a block is sent"
```

```json:metadata
{"files": ["plugin/anti-tangent-guard/hooks/jev_scan.py", "plugin/anti-tangent-guard/hooks/jev_scan_test.py"], "verifyCommand": "python3 plugin/anti-tangent-guard/hooks/jev_scan_test.py Redaction -v", "acceptanceCriteria": ["named secret assignments redacted", "bare high-entropy tokens redacted", "prose untouched", "redaction applied to block text"], "modelTier": "mechanical"}
```

---

### Task 5: The question file

**Goal:** The shipped question is the measured question, held as reviewable data.

**Files:**
- Create: `plugin/anti-tangent-guard/hooks/jev-question.json`
- Modify: `plugin/anti-tangent-guard/hooks/jev_scan.py` (loader)
- Modify: `plugin/anti-tangent-guard/hooks/jev_scan_test.py`

**Acceptance Criteria:**
- [ ] The file holds one Choice question with exactly the options `change_history`, `compatibility_contract` and `present_behaviour`.
- [ ] `change_history` carries `what`, `not_for` and `examples`; the other two carry `what` and `examples`.
- [ ] The loader returns the question dict and caches it for the process.
- [ ] The loader returns `None` for a missing file, for invalid JSON, and for valid JSON that is not a Choice question whose three options each carry a `what` — a wrong-shaped question would otherwise reach the service and be answered against criteria nobody wrote.

**Verify:** `python3 plugin/anti-tangent-guard/hooks/jev_scan_test.py QuestionFile -v` → OK

**Steps:**

- [ ] **Step 1: Write the question file**

Create `plugin/anti-tangent-guard/hooks/jev-question.json` with exactly the wording measured on 2026-09-19:

```json
{
  "type": "choice",
  "instructions": {
    "question": "What kind of statement does `comment` make?",
    "focus": "If any statement in the comment narrates change history, choose change_history even when the rest explains present behaviour."
  },
  "criteria": {
    "change_history": {
      "what": "Narrates how the code or its tests got here: what an earlier version did or lacked, what was replaced or removed, a bug an earlier version had, or the issue, pull request, task, review round or release that changed it.",
      "not_for": "'Used to' in the sense of 'is used for', or run-time state that no longer exists.",
      "examples": ["Previously the parser rejected tabs here.", "The retry helper this replaced swallowed timeouts.", "Added for the PR 88 backfill job."]
    },
    "compatibility_contract": {
      "what": "Names a version, format or external identifier only to state what the code reads, accepts or talks to now.",
      "examples": ["Reads the v3 wire format the gateway still sends.", "Accepts 2.x config files from older clients."]
    },
    "present_behaviour": {
      "what": "Explains the code as it is now: what it does, why, an invariant, a hazard, or run-time state.",
      "examples": ["The lock used to guard the map is held by the caller.", "Returns nil when the file no longer exists on disk.", "The #2 slot is reserved for the fallback provider."]
    }
  }
}
```

- [ ] **Step 2: Write the failing test**

```python
class QuestionFile(unittest.TestCase):
    def test_options_are_the_measured_three(self):
        q = jev_scan.question()
        self.assertEqual(q["type"], "choice")
        self.assertEqual(sorted(q["criteria"]),
                         ["change_history", "compatibility_contract", "present_behaviour"])
        self.assertIn("not_for", q["criteria"]["change_history"])
        for name in ("change_history", "compatibility_contract", "present_behaviour"):
            self.assertTrue(q["criteria"][name]["examples"], name)

    def test_missing_file_returns_none(self):
        self.assertIsNone(jev_scan.question(path="/nonexistent/jev-question.json"))

    def test_the_loader_caches_for_the_process(self):
        with tempfile.NamedTemporaryFile("w", suffix=".json", delete=False) as fh:
            json.dump(jev_scan.question(), fh)
        self.addCleanup(lambda: os.path.exists(fh.name) and os.unlink(fh.name))
        first = jev_scan.question(path=fh.name)
        os.unlink(fh.name)
        self.assertEqual(jev_scan.question(path=fh.name), first)

    def test_malformed_files_return_none(self):
        bad = ['{"type": "choice"',
               '{"type": "noul", "criteria": {}}',
               '{"type": "choice", "criteria": {"change_history": {"what": "x"}}}',
               '{"type": "choice", "criteria": {"change_history": {},'
               ' "compatibility_contract": {"what": "x"},'
               ' "present_behaviour": {"what": "x"}}}',
               '[]']
        for text in bad:
            with tempfile.NamedTemporaryFile("w", suffix=".json", delete=False) as fh:
                fh.write(text)
            self.addCleanup(os.unlink, fh.name)
            self.assertIsNone(jev_scan.question(path=fh.name), text)
```

- [ ] **Step 3: Run to verify it fails**

Run: `python3 plugin/anti-tangent-guard/hooks/jev_scan_test.py QuestionFile -v`
Expected: FAIL — no attribute `question`

- [ ] **Step 4: Implement the loader**

```python
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
```

- [ ] **Step 5: Run the tests and commit**

```bash
python3 plugin/anti-tangent-guard/hooks/jev_scan_test.py QuestionFile -v
git add plugin/anti-tangent-guard/hooks/jev-question.json plugin/anti-tangent-guard/hooks/jev_scan.py plugin/anti-tangent-guard/hooks/jev_scan_test.py
git commit -m "feat(guard): ship the comment-history question as data"
```

```json:metadata
{"files": ["plugin/anti-tangent-guard/hooks/jev-question.json", "plugin/anti-tangent-guard/hooks/jev_scan.py", "plugin/anti-tangent-guard/hooks/jev_scan_test.py"], "verifyCommand": "python3 plugin/anti-tangent-guard/hooks/jev_scan_test.py QuestionFile -v", "acceptanceCriteria": ["three measured options", "change_history carries not_for", "loader caches", "missing file returns None"], "modelTier": "mechanical"}
```

---

### Task 6: Ask the service, under one deadline

**Goal:** `jev_scan.judge()` returns the first flagged block or nothing, never raising, never exceeding the deadline.

**Files:**
- Modify: `plugin/anti-tangent-guard/hooks/jev_scan.py`
- Modify: `plugin/anti-tangent-guard/hooks/jev_scan_test.py`

**Acceptance Criteria:**
- [ ] One request per block, at most `MAX_WORKERS` at a time, each carrying `Authorization: Bearer <key>`, the pinned model and `state = {"comment": <block text>}`.
- [ ] A probability at or above the threshold returns a flag carrying the block and the probability; a clean verdict still carries the highest probability seen, so the calibration runner can read it back.
- [ ] A connection error, HTTP error, timeout, malformed JSON, missing field or unexpected exception returns no flag and an error class.
- [ ] Requests are submitted one at a time with the remaining budget checked before each, so none starts after the deadline.
- [ ] `judge()` takes one parameter for the bound, `deadline`, and it is an absolute monotonic time from the caller, not a duration — block building spends the same budget the requests do. A caller with a duration passes `time.monotonic() + duration`.
- [ ] **The transport refuses redirects.** A 3xx is an error like any other, so the bearer token cannot be replayed to a host the trust rule never approved.
- [ ] A failure on one block never suppresses a flag on another: a confident flag is returned even when an earlier or simultaneous request failed, and a run with failures and no flag returns `jev-error`.
- [ ] `judge()` returns a verdict by the deadline, whatever the transport does — including a transport that never returns and a resolver that never answers. A worker still running at that point is abandoned, not awaited.
- [ ] The order contract is stated and tested: the first completed **batch** of requests decides, and within a batch the lowest block index wins. A block that completes in a later batch never beats one from an earlier batch, whatever its position in the file.
- [ ] Transport is injectable, so no test touches the network.

**Verify:** `python3 plugin/anti-tangent-guard/hooks/jev_scan_test.py Judge -v` → OK

**Steps:**

- [ ] **Step 1: Write the failing tests**

```python
class Judge(unittest.TestCase):
    def cfg(self, **over):
        env = {"ANTI_TANGENT_JEV": "1", "TYPESAFE_API_KEY": "k"}
        env.update(over)
        return jev_scan.config(env, "x.go")

    @staticmethod
    def answer(p):
        return {"model": "jev-1.13.0",
                "answers": {"kind": {"type": "choice", "choice": "change_history",
                                     "probabilities": {"change_history": p,
                                                       "compatibility_contract": 0.0,
                                                       "present_behaviour": 1 - p},
                                     "confidence": 0.9}}}

    def test_flags_at_or_above_threshold(self):
        blocks = [jev_scan.Block("The count cap used to return a plain error.", 1)]
        r = jev_scan.judge(blocks, self.cfg(), transport=lambda *a, **k: self.answer(0.82))
        self.assertEqual(r.flagged.text, blocks[0].text)
        self.assertAlmostEqual(r.probability, 0.82)

    def test_below_threshold_passes(self):
        blocks = [jev_scan.Block("Returns nil when the file is absent.", 1)]
        r = jev_scan.judge(blocks, self.cfg(), transport=lambda *a, **k: self.answer(0.31))
        self.assertIsNone(r.flagged)
        self.assertEqual(r.event, "jev-pass")

    def test_request_shape(self):
        seen = {}

        def transport(url, body, headers, timeout):
            seen.update(url=url, body=body, headers=headers, timeout=timeout)
            return self.answer(0.1)

        jev_scan.judge([jev_scan.Block("A comment.", 1)], self.cfg(), transport=transport)
        self.assertEqual(seen["url"], jev_scan.DEFAULT_URL)
        self.assertEqual(seen["headers"]["Authorization"], "Bearer k")
        self.assertEqual(seen["body"]["model"], "jev-1.13.0")
        self.assertEqual(seen["body"]["state"], {"comment": "A comment."})
        self.assertIn("kind", seen["body"]["questions"])

    def test_every_failure_passes(self):
        for boom in (OSError("no route"), ValueError("bad json"), RuntimeError("?")):
            def transport(*a, **k):
                raise boom
            r = jev_scan.judge([jev_scan.Block("c", 1)], self.cfg(), transport=transport)
            self.assertIsNone(r.flagged, boom)
            self.assertEqual(r.event, "jev-error")

    def test_missing_field_passes(self):
        r = jev_scan.judge([jev_scan.Block("c", 1)], self.cfg(),
                           transport=lambda *a, **k: {"answers": {}})
        self.assertIsNone(r.flagged)
        self.assertEqual(r.event, "jev-error")

    def test_returns_within_the_deadline_against_a_transport_that_never_returns(self):
        forever = threading.Event()
        self.addCleanup(forever.set)

        def never(*a, **k):
            forever.wait()
            return self.answer(0.9)

        start = time.monotonic()
        r = jev_scan.judge([jev_scan.Block("c%d" % i, i) for i in range(20)],
                           self.cfg(), transport=never,
                           deadline=time.monotonic() + 0.5)
        elapsed = time.monotonic() - start
        self.assertLess(elapsed, 2.0, "judge waited for a worker it should have abandoned")
        self.assertIsNone(r.flagged)
        self.assertEqual(r.event, "jev-error")

    def test_no_request_starts_after_the_deadline(self):
        started = []
        release = threading.Event()
        self.addCleanup(release.set)

        def slow(*a, **k):
            started.append(time.monotonic())
            release.wait()
            return self.answer(0.0)

        begin = time.monotonic()
        jev_scan.judge([jev_scan.Block("c%d" % i, i) for i in range(20)],
                       self.cfg(), transport=slow, deadline=begin + 0.3)
        self.assertTrue(started)
        for at in started:
            self.assertLessEqual(at, begin + 0.3 + 0.05,
                                 "a request started after the deadline")
        self.assertLessEqual(len(started), jev_scan.MAX_WORKERS)

    def test_a_batch_is_resolved_by_block_index(self):
        # Two flags land in the same wait; the lower index must win, so the
        # message a writer sees does not depend on thread scheduling.
        gate = threading.Event()
        self.addCleanup(gate.set)

        def paired(url, body, headers, timeout):
            gate.wait(0.2)
            return self.answer(0.9)

        blocks = [jev_scan.Block("first block", 1), jev_scan.Block("second block", 2)]
        r = jev_scan.judge(blocks, self.cfg(), transport=paired)
        self.assertEqual(r.flagged.text, "first block")

    def test_the_transport_refuses_a_redirect(self):
        # A 3xx must reach judge() as an error, never as a second request
        # carrying the key somewhere the host rule never approved.
        import http.server
        import threading as th

        class Redirector(http.server.BaseHTTPRequestHandler):
            def do_POST(self):
                self.send_response(302)
                self.send_header("Location", "http://127.0.0.1:1/v1/systemone")
                self.end_headers()

            def log_message(self, *a):
                pass

        server = http.server.HTTPServer(("127.0.0.1", 0), Redirector)
        th.Thread(target=server.serve_forever, daemon=True).start()
        self.addCleanup(server.shutdown)
        cfg = self.cfg(ANTI_TANGENT_JEV_URL="http://127.0.0.1:%d/v1/systemone"
                       % server.server_port)
        r = jev_scan.judge([jev_scan.Block("c", 1)], cfg)
        self.assertIsNone(r.flagged)
        self.assertEqual(r.event, "jev-error")

    def test_a_flag_survives_another_block_failing(self):
        calls = []

        def mixed(url, body, headers, timeout):
            calls.append(body["state"]["comment"])
            if body["state"]["comment"] == "bad block":
                raise OSError("no route")
            return self.answer(0.9)

        blocks = [jev_scan.Block("bad block", 1), jev_scan.Block("flagging block", 2)]
        r = jev_scan.judge(blocks, self.cfg(), transport=mixed)
        self.assertIsNotNone(r.flagged, "a failure must not suppress another block's flag")
        self.assertEqual(r.flagged.text, "flagging block")

    def test_failures_without_a_flag_report_the_error(self):
        def failing(url, body, headers, timeout):
            raise OSError("no route")

        r = jev_scan.judge([jev_scan.Block("c", 1)], self.cfg(), transport=failing)
        self.assertIsNone(r.flagged)
        self.assertEqual(r.event, "jev-error")

    def test_clean_verdict_carries_the_highest_probability(self):
        r = jev_scan.judge([jev_scan.Block("c", 1)], self.cfg(),
                           transport=lambda *a, **k: self.answer(0.42))
        self.assertIsNone(r.flagged)
        self.assertAlmostEqual(r.probability, 0.42)
```

Add `import threading` and `import time` to the test file's imports.

- [ ] **Step 2: Run to verify it fails**

Run: `python3 plugin/anti-tangent-guard/hooks/jev_scan_test.py Judge -v`
Expected: FAIL — no attribute `judge`

- [ ] **Step 3: Implement**

```python
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


def judge(blocks, cfg, transport=None, deadline=None):
    """The first flag to complete, or a clean verdict, inside the deadline.

    Completion decides, not file position: the hook refuses the write either
    way, and waiting for an earlier block to come back would spend the budget
    on ordering nobody reads. Several requests can land in one wait, and a set
    has no order, so a batch is resolved by block index -- that is what makes
    the same inputs give the same message twice.

    Resolution, connection and read all happen inside the worker, because a
    socket timeout does not bound getaddrinfo. That makes a worker
    unstoppable, so the deadline is enforced by walking away from it rather
    than by cancelling it.
    """
    q = question()
    if q is None:
        return Verdict(event="jev-error", detail="no-question-file")
    transport = transport or _http
    headers = {"Authorization": "Bearer " + cfg.key, "Content-Type": "application/json"}
    # An ABSOLUTE monotonic deadline, because the caller's clock started
    # before block building and that time is part of the same budget. A
    # caller with a duration in hand passes time.monotonic() + duration.
    end = time.monotonic() + DEADLINE_S if deadline is None else deadline
    errors, best, pending, queue = [], 0.0, {}, list(blocks)
    pool = concurrent.futures.ThreadPoolExecutor(max_workers=MAX_WORKERS)
    try:
        while True:
            left = end - time.monotonic()
            if left <= 0:
                errors.append("deadline")
                break
            while queue and len(pending) < MAX_WORKERS and time.monotonic() < end:
                block = queue.pop(0)
                body = {"model": cfg.model, "state": {"comment": block.text},
                        "questions": {"kind": q}}
                pending[pool.submit(transport, cfg.url, body, headers,
                                    min(PER_REQUEST_S, max(0.01, end - time.monotonic())))] = block
            if not pending:
                break
            done, _ = concurrent.futures.wait(
                pending, timeout=left,
                return_when=concurrent.futures.FIRST_COMPLETED)
            if not done:
                errors.append("deadline")
                break
            for fut in sorted(done, key=lambda f: blocks.index(pending[f])):
                block = pending.pop(fut)
                try:
                    p = _probability(fut.result())
                except Exception as exc:
                    errors.append(type(exc).__name__)
                    continue
                best = max(best, p)
                # A flag stands even when another request failed: the flag is
                # evidence, the failure is only missing evidence, and waiting
                # for the rest would spend budget to reach the same refusal.
                if p >= cfg.threshold:
                    return Verdict(block, p, "jev-block")
    finally:
        _shutdown(pool)
    if errors:
        return Verdict(probability=best, event="jev-error", detail=errors[0])
    return Verdict(probability=best)
```

Add `import concurrent.futures`, `import json`, `import re`, `import time`, `import urllib.request` and `from urllib.parse import urlparse` to the module imports.

- [ ] **Step 4: Run the tests**

Run: `python3 plugin/anti-tangent-guard/hooks/jev_scan_test.py Judge -v`
Expected: PASS — every test in the class. The deadline test must finish in about a second, not ten.

- [ ] **Step 5: Commit**

```bash
git add plugin/anti-tangent-guard/hooks/jev_scan.py plugin/anti-tangent-guard/hooks/jev_scan_test.py
git commit -m "feat(guard): ask the service under one deadline, failing open"
```

```json:metadata
{"files": ["plugin/anti-tangent-guard/hooks/jev_scan.py", "plugin/anti-tangent-guard/hooks/jev_scan_test.py"], "verifyCommand": "python3 plugin/anti-tangent-guard/hooks/jev_scan_test.py Judge -v", "acceptanceCriteria": ["one request per block with bearer, pinned model and state", "flags at or above threshold", "every failure returns no flag", "resolution inside the worker", "returns within the deadline", "transport injectable"], "modelTier": "standard"}
```

---

### Task 7: Strikes, the yield, and the error breaker

**Goal:** Two blocks on one file are the limit, and a dead service costs one slow edit rather than every edit.

**Files:**
- Modify: `plugin/anti-tangent-guard/hooks/jev_scan.py`
- Modify: `plugin/anti-tangent-guard/hooks/jev_scan_test.py`

**Acceptance Criteria:**
- [ ] `strike(dir, session, path)` returns 1, then 2, then 3 for the same session and path; a different path or session counts separately.
- [ ] A stamp older than `STRIKE_TTL_S` is ignored, so a later edit starts from 1.
- [ ] Concurrent hook processes counting the same session and path cannot corrupt the stamp: it is written to a temp file beside it and moved into place, so a lost race costs one extra refusal, never a missing one. (The yield itself is Task 8's, which owns the caller.)
- [ ] `breaker_open(dir)` is true for `BREAKER_S` after `breaker_trip(dir)`, false before and after.
- [ ] Every filesystem failure is swallowed: an unwritable directory must not break the hook.

**Verify:** `python3 plugin/anti-tangent-guard/hooks/jev_scan_test.py Strikes -v` → OK

**Steps:**

- [ ] **Step 1: Write the failing tests**

```python
class Strikes(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.mkdtemp()

    def tearDown(self):
        shutil.rmtree(self.dir, ignore_errors=True)

    def test_counts_per_session_and_path(self):
        self.assertEqual(jev_scan.strike(self.dir, "s1", "/a.go"), 1)
        self.assertEqual(jev_scan.strike(self.dir, "s1", "/a.go"), 2)
        self.assertEqual(jev_scan.strike(self.dir, "s1", "/a.go"), 3)
        self.assertEqual(jev_scan.strike(self.dir, "s1", "/b.go"), 1)
        self.assertEqual(jev_scan.strike(self.dir, "s2", "/a.go"), 1)

    def test_expired_stamp_restarts(self):
        jev_scan.strike(self.dir, "s1", "/a.go")
        stamp = jev_scan._strike_path(self.dir, "s1", "/a.go")
        old = time.time() - jev_scan.STRIKE_TTL_S - 1
        os.utime(stamp, (old, old))
        self.assertEqual(jev_scan.strike(self.dir, "s1", "/a.go"), 1)

    def test_unwritable_dir_is_survivable(self):
        self.assertEqual(jev_scan.strike("/proc/nonexistent", "s1", "/a.go"), 1)

    def test_concurrent_writers_leave_a_readable_stamp(self):
        # Eight processes racing one stamp. A lost update is allowed and costs
        # an extra refusal; a corrupt stamp is not, because the next read
        # would fail and the count would restart silently.
        code = ("import sys; sys.path.insert(0, %r); import jev_scan;"
                "jev_scan.strike(%r, 's1', '/a.go')" % (HOOKS, self.dir))
        procs = [subprocess.Popen([sys.executable, "-c", code]) for _ in range(8)]
        for proc in procs:
            proc.wait(timeout=30)
        stamp = jev_scan._strike_path(self.dir, "s1", "/a.go")
        with open(stamp) as fh:
            value = int(fh.read().strip())
        self.assertGreaterEqual(value, 1)
        self.assertLessEqual(value, 8)
        self.assertEqual(jev_scan.strike(self.dir, "s1", "/a.go"), value + 1)

    def test_breaker_opens_and_closes(self):
        self.assertFalse(jev_scan.breaker_open(self.dir))
        jev_scan.breaker_trip(self.dir)
        self.assertTrue(jev_scan.breaker_open(self.dir))
        stamp = os.path.join(self.dir, jev_scan.BREAKER_FILE)
        old = time.time() - jev_scan.BREAKER_S - 1
        os.utime(stamp, (old, old))
        self.assertFalse(jev_scan.breaker_open(self.dir))
```

Add `import shutil`, `import tempfile` to the test imports.

- [ ] **Step 2: Run to verify it fails**

Run: `python3 plugin/anti-tangent-guard/hooks/jev_scan_test.py Strikes -v`
Expected: FAIL — no attribute `strike`

- [ ] **Step 3: Implement**

```python
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
```

Add `import hashlib` to the module imports.

- [ ] **Step 4: Run the tests**

Run: `python3 plugin/anti-tangent-guard/hooks/jev_scan_test.py Strikes -v`
Expected: PASS — every test in the class

- [ ] **Step 5: Commit**

```bash
git add plugin/anti-tangent-guard/hooks/jev_scan.py plugin/anti-tangent-guard/hooks/jev_scan_test.py
git commit -m "feat(guard): bound the refusals and the outage"
```

```json:metadata
{"files": ["plugin/anti-tangent-guard/hooks/jev_scan.py", "plugin/anti-tangent-guard/hooks/jev_scan_test.py"], "verifyCommand": "python3 plugin/anti-tangent-guard/hooks/jev_scan_test.py Strikes -v", "acceptanceCriteria": ["strike counts per session and path", "expired stamp restarts the count", "third strike returns 3 for Task 8 to interpret", "atomic stamp write proven by concurrent writers", "breaker opens for 60s", "filesystem failures swallowed"], "modelTier": "standard"}
```

---

### Task 8: Wire the tier into the hook body

**Goal:** `check_comment_write.py` runs the tier when the regex tier finds nothing, prints the block message, and reports its trace event on stdout.

**Files:**
- Modify: `plugin/anti-tangent-guard/hooks/check_comment_write.py`
- Modify: `plugin/anti-tangent-guard/hooks/jev_scan.py` (add `run()` and the messages)
- Modify: `plugin/anti-tangent-guard/hooks/jev_scan_test.py`
- Create: `plugin/anti-tangent-guard/evals/jev-stub.py` (the loopback stand-in these tests drive; Task 10 wires the same file into the eval runner)

**Acceptance Criteria:**
- [ ] A regex violation exits 2 without the tier running at all — no request reaches the stub, no strike is written, stdout is empty.
- [ ] With the tier off, the body exits 0 and prints `jev-skip|<reason>` on stdout.
- [ ] A flag on strike 1 or 2 exits 4, with the offending comment and its probability on stderr.
- [ ] A flag on strike 3 exits 0, prints `jev-yield|...` and writes the yield message to stderr.
- [ ] Any exception inside the tier exits 0 with `jev-error|<class>` — nothing escapes as a traceback.
- [ ] After a failure, the next call within the breaker window prints `jev-skip|breaker` and sends no request to the stub.
- [ ] The first failure in a session also writes one warning message to stderr; a second failure in the same session writes none.
- [ ] **There is no environment variable that can make the tier answer without the service.** Tests point `ANTI_TANGENT_JEV_URL` at a loopback stub, which the host rule already permits; nothing repository-controlled can substitute a verdict.
- [ ] The body leaves through `os._exit` once it has a decision, so a worker abandoned at the deadline cannot hold the hook open.

**Verify:** `python3 plugin/anti-tangent-guard/hooks/jev_scan_test.py HookBody -v` → OK

**Steps:**

- [ ] **Step 1: Write the loopback stub these tests drive**

Create `plugin/anti-tangent-guard/evals/jev-stub.py`. It lives under `evals/` because Task 10 gives
it a second job there, driving the shipped eval suite:

```python
"""A local stand-in for the System One endpoint, for the eval suite.

Prints its port on stdout, then serves one fixed probability. With --hang it
accepts the connection and never answers, which is what the hook's deadline
is for. Every request is appended to the log file named by --log, so a case
can assert that no request was made at all.
"""
import argparse
import json
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer

p = argparse.ArgumentParser()
p.add_argument("--prob", type=float, default=0.0)
p.add_argument("--log", default="")
p.add_argument("--port-file", default="")
args = p.parse_args()

# Hanging is decided per REQUEST, not per process: the eval suite owns one
# stub for the whole run, and a mode fixed at startup could not serve both
# the answering cases and the one that must never be answered.
HANG_SENTINEL = "HANG-THIS-REQUEST"


class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        body = self.rfile.read(int(self.headers.get("Content-Length") or 0))
        text = body.decode("utf-8", "replace")
        if args.log:
            with open(args.log, "a") as fh:
                fh.write(text + "\n")
        if HANG_SENTINEL in text:
            import time
            time.sleep(60)
            return
        payload = json.dumps({
            "model": "jev-1.13.0",
            "answers": {"kind": {"type": "choice", "choice": "change_history",
                                 "probabilities": {"change_history": args.prob,
                                                   "compatibility_contract": 0.0,
                                                   "present_behaviour": 1 - args.prob},
                                 "confidence": 0.9}},
            "usage": {"input_tokens": 1, "output_tokens": 1}}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

    def log_message(self, *a):
        pass


server = HTTPServer(("127.0.0.1", 0), Handler)
# The port goes to a file when one is named, because a background job's
# stdout is not readable by the shell that started it; a caller holding the
# pipe (the unit tests) reads it from stdout instead.
if args.port_file:
    with open(args.port_file, "w") as fh:
        fh.write("%d\n" % server.server_port)
sys.stdout.write("%d\n" % server.server_port)
sys.stdout.flush()
server.serve_forever()
```

- [ ] **Step 2: Write the failing tests**

These drive the real body in a subprocess against `evals/jev-stub.py` on loopback. There is deliberately **no** environment seam for injecting a verdict: one would be a production bypass, since a repository's own settings can set environment for these hooks — the same hole the URL trust rule closes. Loopback is the seam, and the host rule already allows it.

```python
class HookBody(unittest.TestCase):
    """The real hook body, against a loopback stub — the only seam there is."""

    def setUp(self):
        self.dir = tempfile.mkdtemp()
        self.file = os.path.join(self.dir, "x.go")
        self.requests = os.path.join(self.dir, "requests.log")
        self.port = self.start_stub("0.95")

    def tearDown(self):
        self.stub.terminate()
        self.stub.wait(timeout=10)
        shutil.rmtree(self.dir, ignore_errors=True)

    def start_stub(self, prob):
        stub = os.path.join(os.path.dirname(HOOKS), "evals", "jev-stub.py")
        self.stub = subprocess.Popen(
            [sys.executable, "-B", stub, "--prob", prob, "--log", self.requests],
            stdout=subprocess.PIPE, text=True)
        return int(self.stub.stdout.readline().strip())

    def run_body(self, content, session="s1", **env_over):
        with open(self.file, "w") as fh:
            fh.write("package x\n")
        payload = json.dumps({"tool_name": "Write", "session_id": session,
                              "tool_input": {"file_path": self.file, "content": content}})
        env = dict(os.environ)
        env.update({"ATG_ROOT": os.path.dirname(HOOKS), "ANTI_TANGENT_JEV": "1",
                    "TYPESAFE_API_KEY": "k",
                    "ANTI_TANGENT_JEV_URL": "http://127.0.0.1:%d/v1/systemone" % self.port,
                    "ANTI_TANGENT_GUARD_TRACE_LOG": os.path.join(self.dir, "trace.log")})
        env.pop("ANTI_TANGENT_TICKET_PATTERN", None)
        env.update(env_over)
        return subprocess.run([sys.executable, "-I", "-B",
                               os.path.join(HOOKS, "check_comment_write.py")],
                              input=payload, capture_output=True, text=True,
                              env=env, timeout=30)

    def requests_made(self):
        if not os.path.exists(self.requests):
            return 0
        with open(self.requests) as fh:
            return len([l for l in fh if l.strip()])

    def test_regex_violation_never_reaches_the_tier(self):
        r = self.run_body("// fixes #58\npackage x\n")
        self.assertEqual(r.returncode, 2)
        self.assertEqual(r.stdout.strip(), "")
        self.assertEqual(self.requests_made(), 0)

    def test_disabled_tier_reports_why(self):
        r = self.run_body("// A plain comment.\n", ANTI_TANGENT_JEV="0")
        self.assertEqual(r.returncode, 0)
        self.assertTrue(r.stdout.startswith("jev-skip|setting"), r.stdout)
        self.assertEqual(self.requests_made(), 0)

    def test_flag_blocks_and_quotes_the_comment(self):
        r = self.run_body("// The count cap used to return a plain error.\npackage x\n")
        self.assertEqual(r.returncode, 4)
        self.assertIn("used to return a plain error", r.stderr)
        self.assertIn("0.95", r.stderr)
        self.assertTrue(r.stdout.startswith("jev-block|"), r.stdout)

    def test_third_strike_yields(self):
        body = "// The count cap used to return a plain error.\npackage x\n"
        for _ in range(2):
            self.run_body(body)
        r = self.run_body(body)
        self.assertEqual(r.returncode, 0)
        self.assertTrue(r.stdout.startswith("jev-yield|"), r.stdout)
        self.assertIn("task close", r.stderr)

    def test_a_different_session_starts_its_own_count(self):
        body = "// The count cap used to return a plain error.\npackage x\n"
        for _ in range(3):
            self.run_body(body, session="s1")
        r = self.run_body(body, session="s2")
        self.assertEqual(r.returncode, 4, "a fresh session must not inherit strikes")

    def test_failure_allows_warns_once_and_opens_the_breaker(self):
        # A port nothing listens on: the request fails without a stub in the way.
        dead = {"ANTI_TANGENT_JEV_URL": "http://127.0.0.1:9/v1/systemone"}
        first = self.run_body("// A plain comment.\n", **dead)
        self.assertEqual(first.returncode, 0)
        self.assertTrue(first.stdout.startswith("jev-error|"), first.stdout)
        self.assertTrue(first.stderr.strip(), "the first failure must say so once")

        before = self.requests_made()
        second = self.run_body("// The count cap used to return a plain error.\npackage x\n")
        self.assertEqual(second.returncode, 0)
        self.assertTrue(second.stdout.startswith("jev-skip|breaker"), second.stdout)
        self.assertEqual(self.requests_made(), before, "the breaker must stop the request")
        self.assertEqual(second.stderr.strip(), "", "only the first failure warns")
```

- [ ] **Step 3: Run to verify it fails**

Run: `python3 plugin/anti-tangent-guard/hooks/jev_scan_test.py HookBody -v`
Expected: FAIL — the body exits 0 and prints nothing.

- [ ] **Step 4: Add `run()` and the messages to `jev_scan.py`**

```python
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
        return WARN_MESSAGE % detail
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
```

- [ ] **Step 5: Call it from the body**

In `check_comment_write.py`, replace the tail from `bad = violations(path, lines, context)` with:

```python
bad = violations(path, lines, context)
if bad:
    print("BLOCKED: this edit adds comment(s) carrying change history.\n", file=sys.stderr)
    for line, why in bad[:10]:
        print("  %s\n    -> contains %s" % (line[:200], why), file=sys.stderr)
    print(
        "\nComments must explain non-trivial behaviour or a non-obvious invariant, and must read\n"
        "correctly to someone who never saw this change. Issue, task and version references belong\n"
        "in the commit message, not the code. Rewrite the comment and retry.",
        file=sys.stderr,
    )
    sys.exit(2)

import jev_scan  # noqa: E402

trace_dir = os.path.dirname(os.environ.get("ANTI_TANGENT_GUARD_TRACE_LOG")
                            or "/tmp/claude-hooks/anti-tangent-guard.log")
code, event, message = jev_scan.run(path, lines, context, os.environ,
                                    data.get("session_id") or "-", trace_dir)
if message:
    sys.stderr.write(message)
if event:
    sys.stdout.write(event)
# os._exit, not sys.exit: a worker abandoned at the deadline is still alive,
# and the interpreter-exit handler for thread pools would join it, holding
# the hook open long past the budget. The streams are flushed by hand first,
# because os._exit does not do it.
sys.stderr.flush()
sys.stdout.flush()
os._exit(code)
```

Update the module docstring's exit-code list to name 4 as the Jev block.

- [ ] **Step 6: Run the tests**

Run: `python3 plugin/anti-tangent-guard/hooks/jev_scan_test.py -v`
Expected: PASS, all classes

- [ ] **Step 7: Commit**

```bash
git add plugin/anti-tangent-guard/hooks/check_comment_write.py plugin/anti-tangent-guard/hooks/jev_scan.py plugin/anti-tangent-guard/hooks/jev_scan_test.py plugin/anti-tangent-guard/evals/jev-stub.py
git commit -m "feat(guard): run the semantic tier when the tells find nothing"
```

```json:metadata
{"files": ["plugin/anti-tangent-guard/hooks/check_comment_write.py", "plugin/anti-tangent-guard/hooks/jev_scan.py", "plugin/anti-tangent-guard/hooks/jev_scan_test.py", "plugin/anti-tangent-guard/evals/jev-stub.py"], "verifyCommand": "python3 plugin/anti-tangent-guard/hooks/jev_scan_test.py -v", "acceptanceCriteria": ["regex violation short-circuits the tier", "disabled tier reports the reason", "flag exits 4 quoting the comment and probability", "third strike yields at exit 0", "exceptions exit 0 with jev-error", "breaker skips and errors trip it"], "modelTier": "standard"}
```

---

### Task 9: Carry the events through the wrapper

**Goal:** The wrapper captures the body's stdout, maps exit 4 to a block, and writes one trace line per outcome; nothing reaches Claude Code's transcript.

**Files:**
- Modify: `plugin/anti-tangent-guard/hooks/check-comment-write`
- Modify: `plugin/anti-tangent-guard/hooks/hooks.json`

**Acceptance Criteria:**
- [ ] The body's stdout is captured to a run-scoped temp file removed by an `EXIT` trap, so a hook that exits normally or is terminated by a trappable signal leaves nothing behind. A `SIGKILL` leaves the file, which `mktemp`'s own directory eventually reclaims; the hook's own stdout stays empty.
- [ ] A failed `mktemp` allows the write rather than redirecting into an empty path.
- [ ] `PIPESTATUS` still reads the body's status — command substitution is not used.
- [ ] Exit 4 becomes exit 2 with a `jev-block` trace line carrying the probability.
- [ ] Exit 0 traces the event the body reported (`jev-pass`, `jev-skip`, `jev-yield`, `jev-error`), falling back to `pass` when the body reported none.
- [ ] `hooks.json` sets `"timeout": 10` on the `check-comment-write` entry, asserted by a test that selects it by command rather than by position — the plugin registers a second `PreToolUse` matcher for its start gate.

**Verify:** `python3 plugin/anti-tangent-guard/hooks/jev_scan_test.py Wrapper -v && bash plugin/anti-tangent-guard/evals/run.sh` → OK, and all existing cases still pass (the new Jev cases arrive in Task 10)

**Steps:**

- [ ] **Step 1: Capture the body's stdout**

Replace the invocation and status handling at the end of `check-comment-write`:

```bash
ATG_OUT="$(mktemp "${TMPDIR:-/tmp}/atg-comment-write.XXXXXX")" || { trace "skip" "no-tmp"; exit 0; }
# The trap covers what rm cannot: an interrupted hook, and the host's own
# timeout killing this process while python3 is still running.
trap 'rm -f "$ATG_OUT"' EXIT
printf '%s' "$ATG_INPUT" | ATG_ROOT="$PLUGIN_ROOT" \
    python3 -I -B "$PLUGIN_ROOT/hooks/check_comment_write.py" > "$ATG_OUT"
status=${PIPESTATUS[1]}
# Read, then remove: the body reports its Jev outcome on stdout, and a
# pipeline is the only shape that keeps PIPESTATUS, so the text cannot be
# collected with command substitution.
ATG_EVENT="$(head -c 200 "$ATG_OUT" 2>/dev/null | tr -d '\r\n')"
rm -f "$ATG_OUT"
ATG_EV="${ATG_EVENT%%|*}"
ATG_DETAIL="${ATG_EVENT#*|}"
[[ "$ATG_DETAIL" == "$ATG_EVENT" ]] && ATG_DETAIL=""

case "$status" in
    0) trace "${ATG_EV:-pass}" "${ATG_DETAIL:-ext=${ATG_EXT:-?}}"; exit 0 ;;
    2) trace "block" "comment-hygiene"; exit 2 ;;
    3) trace "skip" "unreadable-target"; exit 0 ;;
    4) trace "jev-block" "${ATG_DETAIL:-?}"; exit 2 ;;
    *) trace "error" "python-exit=$status"; exit 0 ;;
esac
```

- [ ] **Step 2: Set the hook timeout**

In `hooks.json`, the `check-comment-write` entry — the first `PreToolUse` matcher, `Edit|Write`; the second one registers `check-task-start` and is not yours — becomes:

```json
{ "type": "command", "command": "\"${CLAUDE_PLUGIN_ROOT}/hooks/check-comment-write\"", "timeout": 10 }
```

- [ ] **Step 3: Exercise it by hand**

```bash
# The loopback stub, not an environment seam: nothing may inject a verdict.
python3 -B plugin/anti-tangent-guard/evals/jev-stub.py --prob 0.95 --log /tmp/atg-req.log &
STUB=$!; sleep 1
PORT=$(head -1 /proc/$STUB/fd/1 2>/dev/null || echo "")   # or run it with --port-file
export ANTI_TANGENT_GUARD_TRACE_LOG=/tmp/atg-manual.log
export ANTI_TANGENT_JEV=1 TYPESAFE_API_KEY=k
export ANTI_TANGENT_JEV_URL="http://127.0.0.1:$PORT/v1/systemone"
printf '{"tool_name":"Write","session_id":"s1","tool_input":{"file_path":"/tmp/atg-x.go","content":"// The count cap used to return a plain error.\\npackage x\\n"}}' \
  | plugin/anti-tangent-guard/hooks/check-comment-write; echo "exit=$?"
tail -2 /tmp/atg-manual.log
kill $STUB
```

Simpler in practice: start the stub with `--port-file /tmp/atg-port`, then read the port from
that file.
Expected: `exit=2`, and a trace line reading `… | comment-write | jev-block | p=0.95`. Nothing on stdout.

- [ ] **Step 4: Write the wrapper tests**

Append to `jev_scan_test.py`. These drive the wrapper binary, not the body, so they cover the
plumbing the eval suite cannot see:

```python
class Wrapper(HookBody):
    """The bash wrapper's own contract, driven through the real binary."""

    WRAPPER = os.path.join(HOOKS, "check-comment-write")

    def run_wrapper(self, content, extra_path="", **env_over):
        with open(self.file, "w") as fh:
            fh.write("package x\n")
        payload = json.dumps({"tool_name": "Write", "session_id": "w1",
                              "tool_input": {"file_path": self.file, "content": content}})
        env = dict(os.environ)
        env.update({"CLAUDE_PLUGIN_ROOT": os.path.dirname(HOOKS),
                    "ANTI_TANGENT_JEV": "1", "TYPESAFE_API_KEY": "k",
                    "ANTI_TANGENT_JEV_URL": "http://127.0.0.1:%d/v1/systemone" % self.port,
                    "ANTI_TANGENT_GUARD_TRACE_LOG": os.path.join(self.dir, "trace.log")})
        env.pop("ANTI_TANGENT_TICKET_PATTERN", None)
        if extra_path:
            env["PATH"] = extra_path
        env.update(env_over)
        return subprocess.run([self.WRAPPER], input=payload, capture_output=True,
                              text=True, env=env, timeout=30)

    def trace(self):
        with open(os.path.join(self.dir, "trace.log")) as fh:
            return fh.read()

    def test_flag_maps_to_exit_two_with_its_probability(self):
        r = self.run_wrapper("// The count cap used to return a plain error.\npackage x\n")
        self.assertEqual(r.returncode, 2)
        self.assertEqual(r.stdout, "", "the body's event must not reach the transcript")
        self.assertIn("jev-block | p=0.95", self.trace())

    def test_pass_traces_the_reported_event(self):
        r = self.run_wrapper("// Returns nil when the file is absent.\npackage x\n")
        self.assertEqual(r.returncode, 0)
        self.assertEqual(r.stdout, "")
        self.assertIn("jev-pass", self.trace())

    def test_disabled_tier_traces_its_reason(self):
        r = self.run_wrapper("// A plain comment.\n", ANTI_TANGENT_JEV="0")
        self.assertEqual(r.returncode, 0)
        self.assertIn("jev-skip | setting", self.trace())

    def test_no_temp_files_are_left_behind(self):
        before = set(os.listdir(tempfile.gettempdir()))
        self.run_wrapper("// A plain comment.\npackage x\n")
        left = {n for n in set(os.listdir(tempfile.gettempdir())) - before
                if n.startswith("atg-comment-write.")}
        self.assertEqual(left, set())

    def test_a_failed_mktemp_allows_the_write(self):
        # Every dependency present EXCEPT a working mktemp. An empty PATH
        # would not test this: the wrapper checks for python3 first and would
        # exit on that instead, never reaching the branch under test.
        shim = os.path.join(self.dir, "bin")
        os.makedirs(shim, exist_ok=True)
        for tool in ("python3", "jq", "cat", "tr", "mkdir", "date", "dirname",
                     "wc", "mv", "rm", "head", "printf", "seq", "sleep"):
            found = shutil.which(tool)
            if found:
                os.symlink(found, os.path.join(shim, tool))
        with open(os.path.join(shim, "mktemp"), "w") as fh:
            fh.write("#!/bin/sh\nexit 1\n")
        os.chmod(os.path.join(shim, "mktemp"), 0o755)

        r = self.run_wrapper("// The count cap used to return a plain error.\npackage x\n",
                             extra_path=shim)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("no-tmp", self.trace())

    def test_hooks_json_declares_the_timeout(self):
        # Selected by command, not by position: the plugin registers more
        # than one PreToolUse matcher, and their order is not a contract.
        with open(os.path.join(HOOKS, "hooks.json")) as fh:
            hooks = json.load(fh)
        entries = [h for matcher in hooks["hooks"]["PreToolUse"]
                   for h in matcher["hooks"] if "check-comment-write" in h["command"]]
        self.assertEqual(len(entries), 1, "expected exactly one comment-write hook entry")
        self.assertEqual(entries[0]["timeout"], 10)
```

- [ ] **Step 5: Run the tests and the existing suite**

Run: `python3 plugin/anti-tangent-guard/hooks/jev_scan_test.py Wrapper -v && bash plugin/anti-tangent-guard/evals/run.sh`
Expected: OK, and every existing case passes — the capture must not change any existing outcome.

- [ ] **Step 6: Commit**

```bash
git add plugin/anti-tangent-guard/hooks/check-comment-write plugin/anti-tangent-guard/hooks/hooks.json plugin/anti-tangent-guard/hooks/jev_scan_test.py
git commit -m "feat(guard): trace the semantic tier's outcome"
```

```json:metadata
{"files": ["plugin/anti-tangent-guard/hooks/check-comment-write", "plugin/anti-tangent-guard/hooks/hooks.json", "plugin/anti-tangent-guard/hooks/jev_scan_test.py"], "verifyCommand": "python3 plugin/anti-tangent-guard/hooks/jev_scan_test.py Wrapper -v && bash plugin/anti-tangent-guard/evals/run.sh", "acceptanceCriteria": ["body stdout captured to a temp file and removed", "PIPESTATUS still used", "exit 4 becomes exit 2 with jev-block", "exit 0 traces the reported event", "hooks.json timeout is 10"], "modelTier": "standard"}
```

---

### Task 10: Eval cases against a stub server

**Goal:** The shipped eval suite drives the real hook binary against a local stub, so the tier's behaviour is pinned end to end without touching the network.

**Files:**
- Modify: `plugin/anti-tangent-guard/evals/run.sh` (stub lifecycle, unset list, `EXPECTED_CASE_COUNT`, group comment, trace and request-log assertions)
- Modify: `plugin/anti-tangent-guard/evals/jev-stub.py` (created in Task 8; gains per-request hanging)
- Modify: `plugin/anti-tangent-guard/evals/guard-evals.json` (six new cases)

**Acceptance Criteria:**
- [ ] The stub hangs **per request** — a comment containing the sentinel `HANG-THIS-REQUEST` gets no answer — so one server covers both the answering and the never-answering cases.
- [ ] The runner starts the stub as a background process, captures its PID from `$!` rather than discovering it with `pgrep`, reads the port from a file the stub writes, and kills and waits for that PID in the existing `cleanup` trap.
- [ ] Every inherited Jev variable is unset before the case loop — `ANTI_TANGENT_JEV`, `ANTI_TANGENT_JEV_URL`, `ANTI_TANGENT_JEV_MODEL`, `ANTI_TANGENT_JEV_THRESHOLD`, `ANTI_TANGENT_JEV_EXCLUDE`, `ANTI_TANGENT_JEV_URL_TRUSTED`, `TYPESAFE_API_KEY` — and only then is the runner's own loopback `ANTI_TANGENT_JEV_URL` exported, so no case can reach the real service.
- [ ] Six new cases: tier off by setting; tier off with no key; a flag blocking with `jev-block` and the probability asserted in the trace; a regex hit short-circuiting, proven by the absence of its own comment text from the shared request log; a request the stub never answers, allowing the write; and a third attempt on one path yielding with `jev-yield` in the trace and three requests carrying that case's marker.
- [ ] `EXPECTED_CASE_COUNT` is raised from its current value of 173 to 179, the header's group partition gains a named Jev group, and both count checks pass.

**Verify:** `bash plugin/anti-tangent-guard/evals/run.sh` → all cases pass, count assertion holds

**Steps:**

- [ ] **Step 1: The stub already exists**

Task 8 created `plugin/anti-tangent-guard/evals/jev-stub.py` for the hook-body tests: it takes
`--prob`, `--log` and `--port-file`, binds an ephemeral loopback port, and never answers a request
whose body contains `HANG-THIS-REQUEST`. This task only wires its lifecycle into the runner.

- [ ] **Step 2: Start it from the runner**

In `run.sh`, after `HOOK_CWD` is created and before the case loop:

```bash
# A loopback stub stands in for the endpoint. Loopback is the one non-default
# host the hook will talk to without the operator's trust flag, which is what
# makes this possible without weakening that rule for everyone else.
JEV_LOG="$WORKDIR/jev-requests.log"
JEV_PORT_FILE="$WORKDIR/jev-port"
: > "$JEV_LOG"
# An ordinary background job, so $! is the PID of the process this run owns.
# pgrep would match a concurrent run's stub, and killing that one breaks a
# suite nobody is looking at.
python3 -B "$(dirname "${BASH_SOURCE[0]}")/jev-stub.py" \
    --prob 0.95 --log "$JEV_LOG" --port-file "$JEV_PORT_FILE" &
JEV_PID=$!
for _ in $(seq 1 50); do
    [[ -s "$JEV_PORT_FILE" ]] && break
    sleep 0.1
done
JEV_PORT=$(cat "$JEV_PORT_FILE" 2>/dev/null)
[[ -n "$JEV_PORT" ]] || { echo "FAIL: the Jev stub never reported a port"; exit 1; }
export ANTI_TANGENT_JEV_URL="http://127.0.0.1:$JEV_PORT/v1/systemone"
```

Extend the existing `cleanup()`:

```bash
cleanup() {
    if [[ -n "${JEV_PID:-}" ]]; then
        kill "$JEV_PID" 2>/dev/null
        wait "$JEV_PID" 2>/dev/null
    fi
    rm -rf "$WORKDIR"
}
```

And add to the unset list beside the existing three:

```bash
# Order matters: clear everything inherited, INCLUDING the URL, and only then
# export the one this run owns. A developer with a real endpoint in their
# environment must not have the suite spend their key.
unset ANTI_TANGENT_JEV
unset ANTI_TANGENT_JEV_URL
unset ANTI_TANGENT_JEV_MODEL
unset ANTI_TANGENT_JEV_THRESHOLD
unset ANTI_TANGENT_JEV_EXCLUDE
unset ANTI_TANGENT_JEV_URL_TRUSTED
unset TYPESAFE_API_KEY
export ANTI_TANGENT_JEV_URL="http://127.0.0.1:$JEV_PORT/v1/systemone"
```

Move the `export ANTI_TANGENT_JEV_URL=...` line from the stub-start block above down to here, so
the whole environment contract reads in one place.

- [ ] **Step 3: Add the cases**

Append to `guard-evals.json` (ids 174-179 continue from the current maximum of 173; `{{TMPDIR}}` is substituted by the runner as the existing cases use it):

```json
{
  "id": 174,
  "name": "jev-off-by-default",
  "hook": "check-comment-write",
  "stdin_raw": "{\"tool_name\":\"Write\",\"session_id\":\"j1\",\"tool_input\":{\"file_path\":\"{{TMPDIR}}/atg-eval.go\",\"content\":\"// The count cap used to return a plain error.\\npackage x\\n\"}}",
  "expected_exit": 0,
  "reason": "the tier is opt-in: without ANTI_TANGENT_JEV=1 a prose-history comment is not sent anywhere"
},
{
  "id": 175,
  "name": "jev-needs-a-key",
  "hook": "check-comment-write",
  "env": {"ANTI_TANGENT_JEV": "1"},
  "stdin_raw": "{\"tool_name\":\"Write\",\"session_id\":\"j2\",\"tool_input\":{\"file_path\":\"{{TMPDIR}}/atg-eval.go\",\"content\":\"// The count cap used to return a plain error.\\npackage x\\n\"}}",
  "expected_exit": 0,
  "reason": "the setting alone does not enable the tier; without a key there is nothing to call with"
},
{
  "id": 176,
  "name": "jev-flag-blocks",
  "hook": "check-comment-write",
  "env": {"ANTI_TANGENT_JEV": "1", "TYPESAFE_API_KEY": "eval-key"},
  "stdin_raw": "{\"tool_name\":\"Write\",\"session_id\":\"j3\",\"tool_input\":{\"file_path\":\"{{TMPDIR}}/atg-eval.go\",\"content\":\"// The count cap used to return a plain error.\\npackage x\\n\"}}",
  "expected_exit": 2,
  "expected_stderr_contains": ["reads as change history", "change_history"],
  "reason": "a block the stub scores above the threshold is refused, through the real hook binary"
},
{
  "id": 177,
  "name": "jev-regex-hit-short-circuits",
  "hook": "check-comment-write",
  "env": {"ANTI_TANGENT_JEV": "1", "TYPESAFE_API_KEY": "eval-key"},
  "stdin_raw": "{\"tool_name\":\"Write\",\"session_id\":\"j4\",\"tool_input\":{\"file_path\":\"{{TMPDIR}}/atg-eval.go\",\"content\":\"// fixes #58\\npackage x\\n\"}}",
  "expected_exit": 2,
  "expected_stderr_contains": ["an issue or pull-request reference"],
  "reason": "a tell already refuses this write, so the tier must not spend a request or an egress on it"
}
```

- [ ] **Step 4: Add the hanging and yielding cases**

```json
{
  "id": 178,
  "name": "jev-third-attempt-yields",
  "hook": "check-comment-write",
  "env": {"ANTI_TANGENT_JEV": "1", "TYPESAFE_API_KEY": "eval-key"},
  "repeat": 3,
  "stdin_raw": "{\"tool_name\":\"Write\",\"session_id\":\"j6\",\"tool_input\":{\"file_path\":\"{{TMPDIR}}/atg-eval-yield.go\",\"content\":\"// YIELD-CASE-MARKER: the count cap used to return a plain error.\\npackage x\\n\"}}",
  "expected_exit": 0,
  "reason": "two refusals then a yield: the third attempt on one path allows the write and hands the comment to the close-time reviewer"
},
{
  "id": 179,
  "name": "jev-unanswered-request-allows",
  "hook": "check-comment-write",
  "env": {"ANTI_TANGENT_JEV": "1", "TYPESAFE_API_KEY": "eval-key"},
  "stdin_raw": "{\"tool_name\":\"Write\",\"session_id\":\"j5\",\"tool_input\":{\"file_path\":\"{{TMPDIR}}/atg-eval.go\",\"content\":\"// HANG-THIS-REQUEST the count cap used to return a plain error.\\npackage x\\n\"}}",
  "expected_exit": 0,
  "reason": "the stub never answers this one: the deadline must expire and the write must proceed, because a service that cannot answer must not stop an edit"
}
```

`repeat` is new: the runner must invoke a case's hook that many times and assert the expected exit
on the LAST invocation only. Add it beside the existing per-case fields, defaulting to 1, and note
in the runner's header comment that only this case uses it.

**The order of these two is load-bearing, and the JSON above is written in that order (178 before 179).** A hanging request is a `jev-error`, which trips the
60-second breaker, and every later case in the same trace directory would then take
`jev-skip|breaker` instead of reaching the stub. The yielding case therefore runs first, as 150,
and the hanging one last, as 151. A post-loop assertion pins that the yield case really reached
the service three times rather than being skipped:

```bash
# The stub logs request BODIES, which carry the comment text but not the file
# path, so the marker has to live in the comment itself.
if [[ "$(grep -c 'YIELD-CASE-MARKER' "$JEV_LOG")" != "3" ]]; then
    echo "FAIL: the yield case did not make three requests"
    FAILED=$((FAILED + 1))
fi
```

- [ ] **Step 5: Assert the trace lines and the short-circuit**

After the case loop in `run.sh`, beside the other post-loop checks:

```bash
# Case 177 is only meaningful if nothing was sent. The stub logs every request
# body it receives, so an empty log for that case's comment text is the proof
# an exit code cannot give.
if grep -q "fixes #58" "$JEV_LOG" 2>/dev/null; then
    echo "FAIL: a regex-refused write reached the endpoint"
    FAILED=$((FAILED + 1))
fi
# The exit codes for 176 and 178 are 2 and 0, which several other outcomes
# also produce. The trace line is what says WHICH path ran.
if ! grep -q "comment-write | jev-block | p=0.95" "$ANTI_TANGENT_GUARD_TRACE_LOG"; then
    echo "FAIL: no jev-block trace line with its probability"
    FAILED=$((FAILED + 1))
fi
if ! grep -q "comment-write | jev-yield" "$ANTI_TANGENT_GUARD_TRACE_LOG"; then
    echo "FAIL: no jev-yield trace line"
    FAILED=$((FAILED + 1))
fi
```

- [ ] **Step 6: Update the counts**

Set `EXPECTED_CASE_COUNT=179` — the current value is 173 and this task adds six — and add to the group comment above it:

```
# A group covers the semantic tier: off by default, off without a key,
# a flag refusing the write through the real binary, a tell refusing it first
# without the tier ever being asked, a request the stub never answers, and a
# third attempt on one path yielding.
```

- [ ] **Step 7: Run the suite**

Run: `bash plugin/anti-tangent-guard/evals/run.sh`
Expected: all cases pass, both count checks hold, and the trace and request-log assertions are silent.

- [ ] **Step 8: Commit**

```bash
git add plugin/anti-tangent-guard/evals/
git commit -m "test(guard): pin the semantic tier against a loopback stub"
```

```json:metadata
{"files": ["plugin/anti-tangent-guard/evals/run.sh", "plugin/anti-tangent-guard/evals/jev-stub.py", "plugin/anti-tangent-guard/evals/guard-evals.json"], "verifyCommand": "bash plugin/anti-tangent-guard/evals/run.sh", "acceptanceCriteria": ["stub hangs per request via the sentinel", "runner captures the stub PID from $! and kills and waits for it", "every inherited Jev variable including the URL unset before the loopback URL is exported", "six new cases pass", "short-circuit proven by its comment text being absent from the request log", "yield proven by three marker-carrying requests", "jev-block and jev-yield asserted in the trace", "EXPECTED_CASE_COUNT raised from 173 to 179 with the group partition"], "modelTier": "standard"}
```

---

### Task 11: The calibration set and its runner

**Goal:** The labeled comments and a runner live in the repository, so the question's accuracy can be re-measured whenever the wording or the model changes.

**USER-ORDERED GATE — NON-SKIPPABLE.** This task was requested by the user in the current conversation. It MUST NOT be closed by walking around it, by declaring it "verified inline", or by substituting a cheaper check. Close only after every item in `acceptanceCriteria` has been re-validated independently, with output captured.

**Files:**
- Create: `plugin/anti-tangent-guard/evals/build-jev-comments.py`
- Create: `plugin/anti-tangent-guard/evals/jev-comments.jsonl`
- Create: `plugin/anti-tangent-guard/evals/jev-eval.py`

**Acceptance Criteria:**
- [ ] Every fixture's `block` text is produced by `jev_scan.build_blocks()`, so the measured text is byte-for-byte what the hook would send.
- [ ] Each row carries `id`, `source`, `path`, `block`, `label` (`history` / `not_history` / `ambiguous`) and `regex_hit`. The id includes the source, a per-row tag and a hash of the block text, and the builder fails on a duplicate rather than writing one.
- [ ] The operator has confirmed **every** judgement-call label before the file is committed: all `ambiguous` rows and all `head-history-wording` rows. The other four sources carry labels from a recorded decision rather than a fresh reading — the reviewed `GUARD_LABELS` table in the builder (a case's `expected_exit` describes what the hook did, never whether the comment is history), `fp-class.tsv`'s own `true_positive` / `false_positive` column, the cue-word absence in the sample, and which side of a rewrite a row came from — and are listed for the operator but need no per-row verdict.
- [ ] `jev-eval.py` refuses to run unless `TYPESAFE_API_KEY` and `ANTI_TANGENT_JEV=1` are both set **and** no CI marker is present (`CI`, `GITHUB_ACTIONS`, `BUILD_NUMBER`), so a CI job that happens to carry a key still cannot spend money.
- [ ] It caches answers keyed by the row id and a hash of **both** `jev-question.json` and the configured model, so neither an edited question nor a model change can be scored against stale answers.
- [ ] It prints recall and precision per source group — `tp/pos` and `tp/(tp+fp)`, with `n/a` where the denominator is zero — alongside the raw counts.
- [ ] Every row's probability is recorded as returned, including clean ones, so a re-score needs no second run.

**Verify:** `ANTI_TANGENT_JEV=1 TYPESAFE_API_KEY=$KEY python3 plugin/anti-tangent-guard/evals/jev-eval.py --limit 5` → prints a per-group table for 5 rows; `python3 plugin/anti-tangent-guard/evals/jev-eval.py` without the variables → exits non-zero with a message naming them

**Steps:**

- [ ] **Step 1: Rebuild the fixtures with the hook's own builder**

The measured set was built by a throwaway script whose block shape differs from
`jev_scan.build_blocks()`. This builder produces the committed fixture from all five sources,
running every candidate through the hook's own builder so fixture and hook agree byte for byte.
Save it as `plugin/anti-tangent-guard/evals/build-jev-comments.py` and commit it with the fixture,
so the set can be rebuilt rather than re-judged from scratch.

```python
"""Build jev-comments.jsonl with the hook's own block builder."""
import hashlib
import json
import os
import random
import re
import subprocess
import sys

REPO = subprocess.run(["git", "rev-parse", "--show-toplevel"],
                      capture_output=True, text=True).stdout.strip()
sys.path.insert(0, os.path.join(REPO, "plugin/anti-tangent-guard/hooks"))
import comment_scan  # noqa: E402
import jev_scan  # noqa: E402

CUES = re.compile(r"(?i)\b(previously|no longer|used to|until (task|v)|originally|"
                  r"formerly|was changed|this replaced|now (uses|returns|reads))\b")
RANDOM_SAMPLE = 45
SEED = 20260919

# Guard eval cases carry a label written here rather than derived from
# expected_exit: exit 0 means "the hook allowed it", which is equally true of
# genuine history when the tier is off, has no key, or timed out.
GUARD_LABELS = {
    "comment-write-issue-ref-blocks": "history",
    "comment-write-invariant-why-passes": "not_history",
    "jev-off-by-default": "history",
    "jev-needs-a-key": "history",
    "jev-flag-blocks": "history",
    "jev-regex-hit-short-circuits": "history",
    "jev-third-attempt-yields": "history",
    "jev-unanswered-request-allows": "history",
}

rows, seen_blocks, seen_ids = [], set(), set()


def add(source, label, path, block_text, raw_lines, context, tag):
    """One fixture row, keyed so no two rows can share an id."""
    if not block_text or block_text in seen_blocks:
        return
    seen_blocks.add(block_text)
    digest = hashlib.sha256(block_text.encode("utf-8")).hexdigest()[:8]
    row_id = "%s:%s:%s" % (source, tag, digest)
    if row_id in seen_ids:
        raise SystemExit("duplicate row id: %s" % row_id)
    seen_ids.add(row_id)
    rows.append({"id": row_id, "source": source, "path": path, "block": block_text,
                 "label": label,
                 "regex_hit": bool(comment_scan.violations(path, raw_lines, context))})


def add_from_file(source, label, path, line_no):
    text = open(os.path.join(REPO, path), errors="replace").read()
    line = text.splitlines()[line_no - 1]
    for block in jev_scan.build_blocks(path, [line], text):
        add(source, label, "%s:%d" % (path, line_no), block.text, [line], text,
            "%s-%d" % (path.replace("/", "_"), line_no))


tracked = [f for f in subprocess.run(["git", "ls-files", "*.go", "*.py", "*.sh"],
                                     capture_output=True, text=True, cwd=REPO).stdout.split()
           if "/testdata/" not in f]

# 1. Comments at HEAD whose wording can narrate history or describe run time.
for path in tracked:
    for i, line in enumerate(open(os.path.join(REPO, path), errors="replace"), 1):
        if re.match(r"^\s*(//|#|\*)", line) and CUES.search(line):
            add_from_file("head-history-wording", "history", path, i)

# 2. The repository's own classified scanner hits: path, count, verdict, text.
for raw in open(os.path.join(REPO, "plugin/anti-tangent-guard/evals/fp-class.tsv")):
    if raw.startswith("#") or not raw.strip():
        continue
    path, _count, verdict, text = raw.rstrip("\n").split("\t", 3)
    label = "history" if verdict == "true_positive" else "not_history"
    src = open(os.path.join(REPO, path), errors="replace").read().splitlines()
    idx = next((i for i, l in enumerate(src, 1) if l.strip() == text.strip()), None)
    if idx:
        add_from_file("fp-class", label, path, idx)

# 3. Ordinary comments, sampled reproducibly.
random.seed(SEED)
pool = []
for path in tracked:
    for i, line in enumerate(open(os.path.join(REPO, path), errors="replace"), 1):
        if re.match(r"^\s*(//|#[^!]|\*)", line) and not CUES.search(line) \
                and not comment_scan.violations(path, [line]):
            pool.append((path, i))
for path, i in random.sample(pool, min(RANDOM_SAMPLE, len(pool))):
    add_from_file("head-random", "not_history", path, i)

# 4. The guard's own eval cases, labelled from the table above.
cases = json.load(open(os.path.join(REPO, "plugin/anti-tangent-guard/evals/guard-evals.json")))
cases = cases if isinstance(cases, list) else next(v for v in cases.values() if isinstance(v, list))
for case in cases:
    label = GUARD_LABELS.get(case.get("name"))
    if not label or not case.get("stdin_raw"):
        continue
    payload = json.loads(case["stdin_raw"])
    inp = payload.get("tool_input") or {}
    content = inp.get("content") or inp.get("new_string") or ""
    path = (inp.get("file_path") or "x.go").replace("{{TMPDIR}}", "/tmp")
    for block in jev_scan.build_blocks(path, content.splitlines(), content):
        add("guard-evals", label, path, block.text, content.splitlines(), content,
            str(case["id"]))

# 5. Paired fixtures: the same comment as history and as its rewrite. Both
# sides are read from git, since only one of them exists in the worktree.
PAIRS = [("4bb1729", "plugin/anti-tangent-guard/hooks/git_added_lines_test.py"),
         ("53709c6", "plugin/anti-tangent-guard/hooks/git_added_lines.py"),
         ("a174d25", "plugin/anti-tangent-guard/hooks/git_added_lines.py"),
         ("1e9da90", "plugin/anti-tangent-guard/hooks/check-task-complete")]
for commit, path in PAIRS:
    changed = subprocess.run(["git", "show", "--unified=0", "--format=", commit, "--", path],
                             capture_output=True, text=True, cwd=REPO).stdout
    for rev, label, sign in ((commit + "^", "history", "-"), (commit, "not_history", "+")):
        text = subprocess.run(["git", "show", "%s:%s" % (rev, path)],
                              capture_output=True, text=True, cwd=REPO).stdout
        if not text:
            continue
        touched = [l[1:] for l in changed.splitlines()
                   if l.startswith(sign) and not l.startswith(sign * 3)]
        for n, block in enumerate(jev_scan.build_blocks(path, touched, text)):
            add("cleanup-commit-pair", label, "%s:%s" % (rev, path), block.text,
                touched, text, "%s-%s-%d" % (commit[:7], label, n))

out = os.path.join(REPO, "plugin/anti-tangent-guard/evals/jev-comments.jsonl")
with open(out, "w") as fh:
    for row in rows:
        fh.write(json.dumps(row, sort_keys=True) + "\n")
print("%d rows" % len(rows))
for source in sorted({r["source"] for r in rows}):
    group = [r for r in rows if r["source"] == source]
    print("  %-22s %3d rows, %d history" % (source, len(group),
                                            sum(r["label"] == "history" for r in group)))
```

`regex_hit` is computed from the **source lines with their delimiters**, never from the block: a
block holds stripped spans (`A comment.`), which `violations()` would not read as comments at all,
so scoring it there would record `False` for every row.

Run it, and check the printed per-source counts look like the measured set (roughly 50 rows of
`head-history-wording`, 19 of `fp-class`, 45 sampled, a handful of eval cases and 8 pair rows)
before going on. Every label is a proposal until Step 2.

- [ ] **Step 2: Ask the operator to confirm the labels**

Present the rows whose label is a judgement call — every `ambiguous` row, and every row whose source is `head-history-wording` — and ask the operator to confirm or flip each. Use `AskUserQuestion` with the row text in the option descriptions. Record their verdict in the row's `label` and set `"judged_by": "operator"`.

This question is open despite the header decisions because Decision 5 settles the *policy* (an earlier version's bug is history) while these rows ask whether a given sentence states one — a reading of the text, not of the policy.

- [ ] **Step 3: Write the runner**

```python
"""Score jev-comments.jsonl against the shipped question. Never runs in CI."""
import hashlib
import json
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(os.path.dirname(HERE), "hooks"))

import jev_scan  # noqa: E402

if os.environ.get("ANTI_TANGENT_JEV") != "1" or not os.environ.get("TYPESAFE_API_KEY"):
    sys.exit("set ANTI_TANGENT_JEV=1 and TYPESAFE_API_KEY to run the calibration suite")

# Every row is a paid request, so the refusal is positive rather than a
# convention: a CI job that inherits a key must still not spend it.
for marker in ("CI", "GITHUB_ACTIONS", "BUILD_NUMBER"):
    if os.environ.get(marker):
        sys.exit("the calibration suite is manual: %s is set" % marker)

# The cache namespace covers BOTH inputs that can change an answer: the
# question's wording and the model that answers it. A model change with an
# unchanged question would otherwise be scored against last month's numbers.
cfg = jev_scan.config(dict(os.environ), "calibration.go")
question_hash = hashlib.sha256(
    open(os.path.join(os.path.dirname(HERE), "hooks", "jev-question.json"), "rb").read()
    + cfg.model.encode("utf-8")
).hexdigest()[:12]
```

```python
rows = [json.loads(l) for l in open(os.path.join(HERE, "jev-comments.jsonl"))
        if l.strip()]
rows = [r for r in rows if r["label"] != "ambiguous"]
if "--limit" in sys.argv:
    rows = rows[:int(sys.argv[sys.argv.index("--limit") + 1])]

cache_path = os.path.join(HERE, ".jev-eval-cache-%s.json" % question_hash)
cache = json.load(open(cache_path)) if os.path.exists(cache_path) else {}

for row in rows:
    if row["id"] in cache:
        continue
    # One block per request, the shape the hook sends. judge() reports the
    # highest probability it saw whether or not it flagged, so a clean row is
    # scored on its real number rather than on a default.
    verdict = jev_scan.judge([jev_scan.Block(row["block"], 0)], cfg)
    if verdict.event == "jev-error":
        sys.exit("row %s failed: %s" % (row["id"], verdict.detail))
    cache[row["id"]] = verdict.probability
    json.dump(cache, open(cache_path, "w"))

groups = {}
for row in rows:
    flagged = cache[row["id"]] >= cfg.threshold or row["regex_hit"]
    jev_only = cache[row["id"]] >= cfg.threshold
    g = groups.setdefault(row["source"], {"pos": 0, "neg": 0, "tp": 0, "fp": 0,
                                          "regex_tp": 0, "jev_tp": 0})
    if row["label"] == "history":
        g["pos"] += 1
        g["tp"] += flagged
        g["jev_tp"] += jev_only
        g["regex_tp"] += bool(row["regex_hit"])
    else:
        g["neg"] += 1
        g["fp"] += flagged

def rate(num, den):
    return "n/a" if not den else "%.0f%%" % (100.0 * num / den)


print("%-28s %4s %4s %4s %4s %5s %5s %10s %11s"
      % ("group", "pos", "neg", "tp", "fp", "regex", "jev", "recall", "precision"))
for name, g in sorted(groups.items()):
    print("%-28s %4d %4d %4d %4d %5d %5d %10s %11s"
          % (name, g["pos"], g["neg"], g["tp"], g["fp"], g["regex_tp"], g["jev_tp"],
             "%s (%d/%d)" % (rate(g["tp"], g["pos"]), g["tp"], g["pos"]),
             "%s (%d/%d)" % (rate(g["tp"], g["tp"] + g["fp"]), g["tp"], g["tp"] + g["fp"])))
tp = sum(g["tp"] for g in groups.values())
fp = sum(g["fp"] for g in groups.values())
pos = sum(g["pos"] for g in groups.values())
print("\ntogether: recall %d/%d, %d false flags in %d clean comments"
      % (tp, pos, fp, sum(g["neg"] for g in groups.values())))
```

- [ ] **Step 4: Run it on a handful of rows**

```bash
set -a; . <path to your key file>; set +a
ANTI_TANGENT_JEV=1 python3 plugin/anti-tangent-guard/evals/jev-eval.py --limit 5
```
Expected: a per-group table. Capture the output into the task's close.

- [ ] **Step 5: Run the whole set and record the numbers**

```bash
ANTI_TANGENT_JEV=1 python3 plugin/anti-tangent-guard/evals/jev-eval.py
```
Expected: recall and precision per group. If prose-history recall has fallen below 30 of 43, stop and report — the shipped block builder is producing different text from the measured set, and that is a finding, not a number to accept.

- [ ] **Step 6: Commit**

```bash
git add plugin/anti-tangent-guard/evals/build-jev-comments.py plugin/anti-tangent-guard/evals/jev-comments.jsonl plugin/anti-tangent-guard/evals/jev-eval.py
git commit -m "test(guard): commit the calibration set and its runner"
```

```json:metadata
{"files": ["plugin/anti-tangent-guard/evals/build-jev-comments.py", "plugin/anti-tangent-guard/evals/jev-comments.jsonl", "plugin/anti-tangent-guard/evals/jev-eval.py"], "verifyCommand": "ANTI_TANGENT_JEV=1 python3 plugin/anti-tangent-guard/evals/jev-eval.py --limit 5", "acceptanceCriteria": ["fixtures built by jev_scan.build_blocks", "rows carry id, source, block, label, regex_hit", "operator confirmed the judgement-call labels", "runner refuses without the key and the setting", "cache keyed by row id and question hash", "prints recall and precision per group"], "modelTier": "standard", "userGate": true, "tags": ["user-gate"], "gateScope": "calibration labels confirmed by the operator before the fixture is committed", "failurePolicy": "stop and report"}
```

---

### Task 12: Document the tier

**Goal:** An operator can decide whether to turn this on, and knows what happens when they do.

**Files:**
- Modify: `plugin/anti-tangent-guard/README.md`
- Modify: `plugin/anti-tangent-guard/.claude-plugin/plugin.json`
- Modify: `CHANGELOG.md`
- Modify: `CLAUDE.md` (the "What This Repo Is Not" paragraph on what the plugins block)

**Acceptance Criteria:**
- [ ] The README's write-time guard section covers: the two tiers and the order they run in; the host rule AND its honest limit — a repository whose settings you trust can define hook commands, so the rule covers the accidental and partially-trusted cases, not a repo you have already trusted; that the semantic tier judges the whole touched block while the tells judge added lines, and why; all seven variables (`ANTI_TANGENT_JEV`, `TYPESAFE_API_KEY`, threshold, model, URL, `ANTI_TANGENT_JEV_URL_TRUSTED`, exclude) and the host rule; what leaves the machine and that zero retention is enterprise-only; fail-open, the breaker and the two-block yield; the CA-bundle failure mode under `python3 -I`; and the Windows gap.
- [ ] `plugin.json`'s version goes from `0.5.0` to `0.6.0` and its description no longer implies pattern matching is all the write-time hook does.
- [ ] The `CHANGELOG.md` 0.24.0 entry names the tier, its gate and its failure behaviour.
- [ ] `CLAUDE.md`'s "What This Repo Is Not" paragraph counts the guard's ways of blocking correctly once the tier lands — it currently says "three ways" and "three kill switches" — and names `ANTI_TANGENT_JEV` as the tier's own switch alongside the existing three.
- [ ] `bash scripts/check-protocol-docs.sh` (if present) and `go test -race ./...` pass.

**Verify:** `go test -race ./... && bash plugin/anti-tangent-guard/evals/run.sh && python3 plugin/anti-tangent-guard/hooks/jev_scan_test.py -v && python3 plugin/anti-tangent-guard/hooks/comment_scan_test.py -v` → all green

**Steps:**

- [ ] **Step 1: Write the README sections**

The README already carries `## Write-time comment guard (PreToolUse hook)`, `## Configuration`,
`## Kill switches`, `## Fail-open policy`, `## Trace log` and `## Running the evals`. Extend those
rather than duplicating them: the block below goes immediately after the write-time guard's own
section (before `## Comment-hygiene scan at close`), and four short additions go into the existing
sections, named under the block.

```markdown
## The semantic tier (optional, off by default)

The pattern set catches change history that carries a token: a task id, an issue or pull-request
reference, a version a change verb governs, your configured tracker key. History written as
ordinary prose — "the count cap used to return a plain error" — carries none, and no pattern
decides it, because the ambiguity is in what the sentence means.

Set `ANTI_TANGENT_JEV=1` and provide `TYPESAFE_API_KEY`, and the write-time hook asks TypeSafe's
Jev about the comment when the patterns find nothing. **While it is on, comment text from every
repository you edit is sent to TypeSafe**, redacted for anything shaped like a credential.
TypeSafe offers zero data retention on enterprise plans only.

### What it judges

The whole comment block your edit touches, not only the line you added — including lines that were
already there. That is deliberate and it differs from the pattern tier, which judges added lines
alone: this project's comment policy says history in a comment you touch gets rewritten as part of
the change.

The file you are editing is scanned locally to find where comment blocks begin and end, so every
line passes through the hook on your own machine. Only the blocks your edit touched are sent, and
only after credential redaction; a comment elsewhere in the file leaves no trace and never reaches
TypeSafe.

### Settings

These also belong in the README's own `## Configuration` section, each as its own `###`
subsection beside `ANTI_TANGENT_TICKET_PATTERN`; the table here is the summary.

| Variable | Default | Meaning |
|---|---|---|
| `ANTI_TANGENT_JEV` | unset | Must be exactly `1`. |
| `TYPESAFE_API_KEY` | unset | Required. |
| `ANTI_TANGENT_JEV_THRESHOLD` | `0.7` | Flag at or above. Anything outside (0, 1] falls back. |
| `ANTI_TANGENT_JEV_MODEL` | `jev-1.13.0` | Pinned: an alias moves under a tuned threshold. |
| `ANTI_TANGENT_JEV_URL` | the TypeSafe endpoint | Honoured for loopback, or with `ANTI_TANGENT_JEV_URL_TRUSTED=1`. |
| `ANTI_TANGENT_JEV_EXCLUDE` | unset | Colon-separated globs never sent. |

**Why the URL is restricted.** Environment reaches these hooks from several places — your shell,
a CI job, and a repository's own checked-in settings — and an arbitrary endpoint would be handed
your key along with the comment text. The default host and loopback are the only ones that get it
without `ANTI_TANGENT_JEV_URL_TRUSTED=1`, which you set in your own global settings when you route
through a proxy.

**What this rule does not defend against.** A repository whose settings you have trusted can
define hook *commands*, not only environment — at which point it can read your key directly, and
no rule here changes that. This restriction is for the accidental and the partially-trusted case:
an endpoint inherited from a shell profile or a CI job, or a repository that sets one for its own
tooling. Trusting a repository's settings is still the decision that matters.

### When it cannot answer

Every failure allows the write: no key, no network, DNS failure, TLS failure, a timeout, a
malformed response, an unexpected error. After one failure the tier steps aside for 60 seconds, so
a dead service costs one slow edit rather than every edit, and prints one warning per session.

A refusal you disagree with is bounded too: the tier blocks a given file at most twice per
session, then allows the write and says so. That comment still reaches `validate_completion` at
task close, which is the enforcement that exists without this tier at all.

### Two failure modes worth knowing

Under `python3 -I` the user site directory is dropped, so a Python whose certificates live there
(a python.org install on macOS) cannot verify TLS and every call fails — silently, since failures
allow the write. The per-session warning is how you notice; the trace log names the class. Set
`SSL_CERT_FILE` for a corporate CA.

On Windows there is no `O_NOFOLLOW` — `comment_scan.py`'s capped read asks for it unconditionally
— so a `Write` over an existing file is not scanned at all and this tier never runs for it. An
`Edit` is judged without the post-edit text.
```

Then four additions to the sections that already exist:

- `## Kill switches`: a fourth bullet — `ANTI_TANGENT_JEV` unset or not exactly `1` disables the
  semantic tier alone, leaving the pattern tier, the completion gate and the start gate untouched.
- `## Fail-open policy`: the tier's own row — every failure allows the write, one warning per
  session, and the 60-second breaker after a failure.
- `## Trace log`: the five new events (`jev-block`, `jev-pass`, `jev-skip`, `jev-yield`,
  `jev-error`) and what each records.
- `## Running the evals`: one line for the calibration suite, that it needs a key and the setting,
  and that CI never runs it.

- [ ] **Step 2: Update `plugin.json`**

Bump `version` from `0.5.0` to `0.6.0` and extend the description with one clause: a second, optional tier reads the touched comment block semantically when `ANTI_TANGENT_JEV=1` and a key is present.

- [ ] **Step 3: Fill in the changelog entry**

Expand the 0.24.0 `### Added` bullet written in Task 0 with the gate, the block unit, the yield and the fail-open behaviour.

- [ ] **Step 4: Update `CLAUDE.md`**

In "What This Repo Is Not", the sentence describing what `anti-tangent-guard` blocks gains the semantic tier. Two counts in that paragraph move: "blocks three ways of its own" and "three kill switches". The tier is a second way the write-time hook refuses a comment, gated by `ANTI_TANGENT_JEV` — which is a fourth switch, distinct from `ANTI_TANGENT_COMMENT_GUARD` (that one turns off comment scanning entirely, both tiers).

- [ ] **Step 5: Run everything**

```bash
go test -race ./...
bash plugin/anti-tangent-guard/evals/run.sh
bash plugin/anti-tangent-guard/evals/fp-report.sh
python3 plugin/anti-tangent-guard/hooks/jev_scan_test.py -v
python3 plugin/anti-tangent-guard/hooks/comment_scan_test.py -v
[[ -f scripts/check-protocol-docs.sh ]] && bash scripts/check-protocol-docs.sh
```
Expected: all green.

- [ ] **Step 6: Commit**

```bash
git add plugin/anti-tangent-guard/README.md plugin/anti-tangent-guard/.claude-plugin/plugin.json CHANGELOG.md CLAUDE.md
git commit -m "docs(guard): document the semantic tier"
```

```json:metadata
{"files": ["plugin/anti-tangent-guard/README.md", "plugin/anti-tangent-guard/.claude-plugin/plugin.json", "CHANGELOG.md", "CLAUDE.md"], "verifyCommand": "go test -race ./... && bash plugin/anti-tangent-guard/evals/run.sh && bash plugin/anti-tangent-guard/evals/fp-report.sh && python3 plugin/anti-tangent-guard/hooks/jev_scan_test.py -v && python3 plugin/anti-tangent-guard/hooks/comment_scan_test.py -v && { [ ! -f scripts/check-protocol-docs.sh ] || bash scripts/check-protocol-docs.sh; }", "acceptanceCriteria": ["README covers tiers, block unit, variables, egress, fail-open, breaker, yield, CA bundle, Windows", "plugin.json version and description updated", "changelog entry complete", "CLAUDE.md mentions the tier and its switch", "all suites green"], "modelTier": "mechanical"}
```

---

## Verification of the whole plan

```bash
go test -race ./...
python3 plugin/anti-tangent-guard/hooks/comment_scan_test.py -v
python3 plugin/anti-tangent-guard/hooks/jev_scan_test.py -v
bash plugin/anti-tangent-guard/evals/run.sh
bash plugin/anti-tangent-guard/evals/fp-report.sh
```

All five must pass. The calibration runner (Task 11) is deliberately not in this list: it costs money and needs a key.
