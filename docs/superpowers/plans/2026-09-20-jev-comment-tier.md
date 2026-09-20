# Jev Comment Tier Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a second, semantic tier to `anti-tangent-guard`'s write-time comment hook that asks TypeSafe's Jev whether a touched comment block narrates change history, and blocks when it does.

**Architecture:** The regex tier in `comment_scan.py` runs first and unchanged. When it finds nothing and the tier is enabled, a new `jev_scan.py` builds the comment blocks this edit touched, sends one request per block to `https://api.typesafe.ai/v1/systemone`, and blocks on a `change_history` probability at or above the threshold. Every failure allows the write. `comment_scan.py` stays network-free; only a pure helper is extracted from it.

**Tech Stack:** Python 3 standard library only (`urllib.request`, `concurrent.futures`, `json`, `fnmatch`) — the hooks ship no dependencies and run under `python3 -I`. Bash for the hook wrapper and the eval suite. Tests are `unittest`, run directly.

**Spec:** `docs/superpowers/specs/2026-09-20-jev-comment-tier-design.md`

## Global Constraints

- **Fail open, always.** Any error, timeout, unparsable response or unexpected exception allows the write. Only a confident flag blocks. A hook that raises must never reach the user as a traceback.
- **No network in unit tests.** Every test stubs the transport. The repository rule is absolute (`CLAUDE.md`, Testing Conventions).
- **`comment_scan.py` stays network-free and behaviour-identical.** `violations()` must return exactly what it returns today; `evals/fp-report.sh` must stay at zero false positives and `evals/run.sh` must stay green after the helper extraction.
- **The key goes only to the default host or loopback** unless `ANTI_TANGENT_JEV_URL_TRUSTED=1`.
- **The tier is off unless** `ANTI_TANGENT_JEV=1` **and** `TYPESAFE_API_KEY` is non-empty. `ANTI_TANGENT_COMMENT_GUARD=0` disables both tiers.
- **Comment policy applies to this plan's own code.** No comment may reference a task number, this plan, an issue or a version as history. `ANTI_TANGENT_TICKET_PATTERN=YN-\d+` is set in this environment.
- **Budget for the whole tier: 4 seconds wall clock**, 3 seconds per request, at most 20 blocks, 2,000 characters per block.

**User decisions (already made):**
- Write time only — the `PreToolUse` Edit/Write hook. Not the close-time hook, not the MCP server.
- A flagged comment blocks, like a regex hit.
- Off unless `ANTI_TANGENT_JEV=1` and a key is present.
- No path restriction by default: the setting is the control.
- A comment describing an earlier version's bug is change history, including in a test.
- A touched comment block is judged whole, pre-existing lines included — history in a block an edit touches gets cleaned up.
- The tier blocks at most twice for the same file in a session, then yields to the close-time reviewer.

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

**Verify:** `go test -race ./... && grep -q '^## \[0.24.0\]' CHANGELOG.md && git branch --show-current` → tests pass, prints `version/0.24.0`

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
{"files": ["CHANGELOG.md", "VERSION"], "verifyCommand": "go test -race ./... && grep -q '^## \\[0.24.0\\]' CHANGELOG.md", "acceptanceCriteria": ["branch is version/0.24.0 with main as ancestor", "CHANGELOG has a 0.24.0 section", "VERSION reads 0.24.0", "go test -race ./... passes"], "modelTier": "mechanical"}
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
- [ ] At most 20 blocks are returned, and the caller can tell that capping happened.

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

    def test_block_count_is_capped(self):
        context = "".join("// touched %d\nfunc f%d() {}\n" % (i, i) for i in range(30))
        touched = ["// touched %d" % i for i in range(30)]
        blocks = jev_scan.build_blocks("x.go", touched, context)
        self.assertEqual(len(blocks), jev_scan.MAX_BLOCKS)
        self.assertTrue(jev_scan.was_capped(blocks, 30))


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
    if "#" not in openers(path):
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


def was_capped(blocks, found):
    return found > len(blocks)


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


def build_blocks(path, touched, context):
    """Comment blocks this edit touched, in file order, capped."""
    if context:
        lines = context.splitlines()
        spans = _spans_per_line(path, lines, context)
        touched_idx = _touched_indexes(lines, touched)
        if touched_idx:
            return _runs(spans, touched_idx)
    lines = [t for t in touched]
    spans = _spans_per_line(path, lines, None)
    return _runs(spans, set(range(len(lines))))


def _runs(spans, touched_idx):
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
        texts = ["\n".join(s) for s in spans[start:i]]
        blocks.append(Block(_window(texts, local), start + 1))
    return blocks[:MAX_BLOCKS]
```

- [ ] **Step 5: Run the tests**

Run: `python3 plugin/anti-tangent-guard/hooks/jev_scan_test.py BlockBuilder -v`
Expected: PASS, 9 tests

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
- [ ] A bare high-entropy run of 24 or more characters from the base64/hex alphabet is replaced.
- [ ] Ordinary prose, including long hyphenated English and a 40-character sentence, is left untouched.
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
        line = "the old value was AKIAIOSFODNN7EXAMPLEQWERTYUIOP"
        self.assertIn("<redacted>", jev_scan.redact(line))

    def test_prose_is_untouched(self):
        for line in ("The count cap used to return a plain error to the caller.",
                     "A well-known copy-on-write trade-off, documented upstream."):
            self.assertEqual(jev_scan.redact(line), line)
```

- [ ] **Step 2: Run to verify it fails**

Run: `python3 plugin/anti-tangent-guard/hooks/jev_scan_test.py Redaction -v`
Expected: FAIL — no attribute `redact`

- [ ] **Step 3: Implement**

```python
_SECRET_ASSIGN = re.compile(
    r"(?i)\b([\w.-]*(?:key|secret|token|password|passwd)[\w.-]*)\s*[:=]\s*\S+")
# 24 unbroken base64/hex characters is a credential shape, not an English
# word: prose that long carries a space, a hyphen or a vowel pattern long
# before it gets there.
_BARE_TOKEN = re.compile(r"\b[A-Za-z0-9+/_-]{24,}={0,2}\b")


def redact(text):
    text = _SECRET_ASSIGN.sub(lambda m: "%s=<redacted>" % m.group(1), text)
    return _BARE_TOKEN.sub("<redacted>", text)
```

Call it from `_runs()` when constructing each `Block`: `blocks.append(Block(redact(_window(texts, local)), start + 1))`.

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
- [ ] A missing or malformed file raises nothing the caller cannot catch — the loader returns `None`.

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
                _QUESTION_CACHE[path] = json.loads(fh.read().decode("utf-8"))
        except Exception:
            _QUESTION_CACHE[path] = None
    return _QUESTION_CACHE[path]
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
- [ ] A probability at or above the threshold returns a flag carrying the block and the probability.
- [ ] A connection error, HTTP error, timeout, malformed JSON, missing field or unexpected exception returns no flag and an error class.
- [ ] Host resolution happens inside the worker, so a hanging resolver is bounded by the deadline.
- [ ] No request is started after the deadline passes, and `judge()` returns within the deadline plus one request's timeout.
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

    def test_returns_within_the_deadline(self):
        def slow(*a, **k):
            time.sleep(10)
            return self.answer(0.9)

        start = time.monotonic()
        r = jev_scan.judge([jev_scan.Block("c%d" % i, i) for i in range(20)],
                           self.cfg(), transport=slow, deadline=0.5)
        self.assertLess(time.monotonic() - start, jev_scan.PER_REQUEST_S + 2)
        self.assertIsNone(r.flagged)
```

Add `import time` to the test file's imports.

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


def _http(url, body, headers, timeout):
    """One POST, one attempt. A retry in a blocking hook only doubles the wait."""
    req = urllib.request.Request(
        url, data=json.dumps(body).encode("utf-8"), method="POST", headers=headers)
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        return json.loads(resp.read().decode("utf-8"))


def _probability(payload):
    return float(payload["answers"]["kind"]["probabilities"]["change_history"])


def judge(blocks, cfg, transport=None, deadline=DEADLINE_S):
    """The first block that reads as change history, or a clean verdict.

    Resolution, connection and read all happen inside the worker: a socket
    timeout does not bound getaddrinfo, and an unreachable resolver would
    otherwise hold the hook until the host kills it.
    """
    q = question()
    if q is None:
        return Verdict(event="jev-error", detail="no-question-file")
    transport = transport or _http
    headers = {"Authorization": "Bearer " + cfg.key, "Content-Type": "application/json"}
    body = lambda text: {"model": cfg.model, "state": {"comment": text},
                         "questions": {"kind": q}}
    end = time.monotonic() + deadline
    errors = []
    with concurrent.futures.ThreadPoolExecutor(max_workers=MAX_WORKERS) as pool:
        futures = {pool.submit(transport, cfg.url, body(b.text), headers,
                               PER_REQUEST_S): b for b in blocks}
        try:
            for fut in concurrent.futures.as_completed(futures, timeout=max(0.0, end - time.monotonic())):
                block = futures[fut]
                try:
                    p = _probability(fut.result())
                except Exception as exc:
                    errors.append(type(exc).__name__)
                    continue
                if p >= cfg.threshold:
                    return Verdict(block, p, "jev-block")
        except concurrent.futures.TimeoutError:
            errors.append("deadline")
        finally:
            for fut in futures:
                fut.cancel()
    if errors:
        return Verdict(event="jev-error", detail=errors[0])
    return Verdict()
```

Add `import concurrent.futures`, `import json`, `import re`, `import time`, `import urllib.request` and `from urllib.parse import urlparse` to the module imports.

- [ ] **Step 4: Run the tests**

Run: `python3 plugin/anti-tangent-guard/hooks/jev_scan_test.py Judge -v`
Expected: PASS, 6 tests. The deadline test must finish in a few seconds, not 10.

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
- [ ] At strike 3 the caller yields: the write is allowed and the event is `jev-yield`.
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
        with open(stamp, "w") as fh:
            fh.write(str(count))
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
Expected: PASS, 4 tests

- [ ] **Step 5: Commit**

```bash
git add plugin/anti-tangent-guard/hooks/jev_scan.py plugin/anti-tangent-guard/hooks/jev_scan_test.py
git commit -m "feat(guard): bound the refusals and the outage"
```

```json:metadata
{"files": ["plugin/anti-tangent-guard/hooks/jev_scan.py", "plugin/anti-tangent-guard/hooks/jev_scan_test.py"], "verifyCommand": "python3 plugin/anti-tangent-guard/hooks/jev_scan_test.py Strikes -v", "acceptanceCriteria": ["strike counts per session and path", "expired stamp restarts the count", "third strike yields", "breaker opens for 60s", "filesystem failures swallowed"], "modelTier": "standard"}
```

---

### Task 8: Wire the tier into the hook body

**Goal:** `check_comment_write.py` runs the tier when the regex tier finds nothing, prints the block message, and reports its trace event on stdout.

**Files:**
- Modify: `plugin/anti-tangent-guard/hooks/check_comment_write.py`
- Modify: `plugin/anti-tangent-guard/hooks/jev_scan.py` (add `run()` and the messages)
- Modify: `plugin/anti-tangent-guard/hooks/jev_scan_test.py`

**Acceptance Criteria:**
- [ ] A regex violation exits 2 without the tier running at all — no request, no strike, no stdout.
- [ ] With the tier off, the body exits 0 and prints `jev-skip|<reason>` on stdout.
- [ ] A flag on strike 1 or 2 exits 4, with the offending comment and its probability on stderr.
- [ ] A flag on strike 3 exits 0, prints `jev-yield|...` and writes the yield message to stderr.
- [ ] Any exception inside the tier exits 0 with `jev-error|<class>` — nothing escapes as a traceback.
- [ ] The tier is skipped while the breaker is open, and an error trips it.

**Verify:** `python3 plugin/anti-tangent-guard/hooks/jev_scan_test.py HookBody -v` → OK

**Steps:**

- [ ] **Step 1: Write the failing tests**

These drive the real body in a subprocess with a stubbed transport injected through `ANTI_TANGENT_JEV_TRANSPORT`, a module-level seam the body honours only when it names an importable module in the hooks directory.

```python
class HookBody(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.mkdtemp()
        self.file = os.path.join(self.dir, "x.go")

    def tearDown(self):
        shutil.rmtree(self.dir, ignore_errors=True)

    def run_body(self, content, prob="0.95", **env_over):
        with open(self.file, "w") as fh:
            fh.write("package x\n")
        payload = json.dumps({"tool_name": "Write", "session_id": "s1",
                              "tool_input": {"file_path": self.file, "content": content}})
        env = dict(os.environ)
        env.update({"ATG_ROOT": os.path.dirname(HOOKS), "ANTI_TANGENT_JEV": "1",
                    "TYPESAFE_API_KEY": "k", "ANTI_TANGENT_JEV_FAKE_PROB": prob,
                    "ANTI_TANGENT_GUARD_TRACE_LOG": os.path.join(self.dir, "trace.log")})
        env.pop("ANTI_TANGENT_TICKET_PATTERN", None)
        env.update(env_over)
        r = subprocess.run([sys.executable, "-I", "-B",
                            os.path.join(HOOKS, "check_comment_write.py")],
                           input=payload, capture_output=True, text=True,
                           env=env, timeout=30)
        return r

    def test_regex_violation_never_reaches_the_tier(self):
        r = self.run_body("// fixes #58\npackage x\n")
        self.assertEqual(r.returncode, 2)
        self.assertEqual(r.stdout.strip(), "")

    def test_disabled_tier_reports_why(self):
        r = self.run_body("// A plain comment.\n", ANTI_TANGENT_JEV="0")
        self.assertEqual(r.returncode, 0)
        self.assertTrue(r.stdout.startswith("jev-skip|setting"), r.stdout)

    def test_flag_blocks_and_quotes_the_comment(self):
        r = self.run_body("// The count cap used to return a plain error.\npackage x\n")
        self.assertEqual(r.returncode, 4)
        self.assertIn("used to return a plain error", r.stderr)
        self.assertIn("0.95", r.stderr)
        self.assertTrue(r.stdout.startswith("jev-block|"), r.stdout)

    def test_third_strike_yields(self):
        for _ in range(2):
            self.run_body("// The count cap used to return a plain error.\npackage x\n")
        r = self.run_body("// The count cap used to return a plain error.\npackage x\n")
        self.assertEqual(r.returncode, 0)
        self.assertTrue(r.stdout.startswith("jev-yield|"), r.stdout)
        self.assertIn("task close", r.stderr)

    def test_transport_failure_allows_and_trips_the_breaker(self):
        r = self.run_body("// A plain comment.\n", prob="boom")
        self.assertEqual(r.returncode, 0)
        self.assertTrue(r.stdout.startswith("jev-error|"), r.stdout)
```

- [ ] **Step 2: Run to verify it fails**

Run: `python3 plugin/anti-tangent-guard/hooks/jev_scan_test.py HookBody -v`
Expected: FAIL — the body exits 0 and prints nothing.

- [ ] **Step 3: Add `run()` and the messages to `jev_scan.py`**

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


def _fake_transport():
    """Test seam: a fixed probability, or a raised error, instead of a request."""
    raw = os.environ.get("ANTI_TANGENT_JEV_FAKE_PROB")
    if raw is None:
        return None
    def transport(url, body, headers, timeout):
        return {"answers": {"kind": {"probabilities": {"change_history": float(raw)}}}}
    return transport


def run(path, touched, context, env, session, trace_dir):
    """Judge the touched blocks. Returns (exit_code, stdout_event, stderr_text)."""
    try:
        cfg = config(env, path)
        if not cfg.enabled:
            return 0, "jev-skip|%s" % cfg.reason, ""
        if breaker_open(trace_dir):
            return 0, "jev-skip|breaker", ""
        blocks = build_blocks(path, touched, context)
        if not blocks:
            return 0, "jev-skip|no-blocks", ""
        verdict = judge(blocks, cfg, transport=_fake_transport())
        if verdict.event == "jev-error":
            breaker_trip(trace_dir)
            return 0, "jev-error|%s" % verdict.detail, ""
        if verdict.flagged is None:
            return 0, "jev-pass|blocks=%d" % len(blocks), ""
        count = strike(trace_dir, session, path)
        quoted = verdict.flagged.text[:400]
        if count > STRIKE_LIMIT:
            return 0, "jev-yield|%s" % path, YIELD_MESSAGE % quoted
        return 4, "jev-block|p=%.2f" % verdict.probability, \
            BLOCK_MESSAGE % (quoted, verdict.probability)
    except Exception as exc:
        return 0, "jev-error|%s" % type(exc).__name__, ""
```

- [ ] **Step 4: Call it from the body**

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
sys.exit(code)
```

Update the module docstring's exit-code list to name 4 as the Jev block.

- [ ] **Step 5: Run the tests**

Run: `python3 plugin/anti-tangent-guard/hooks/jev_scan_test.py -v`
Expected: PASS, all classes

- [ ] **Step 6: Commit**

```bash
git add plugin/anti-tangent-guard/hooks/check_comment_write.py plugin/anti-tangent-guard/hooks/jev_scan.py plugin/anti-tangent-guard/hooks/jev_scan_test.py
git commit -m "feat(guard): run the semantic tier when the tells find nothing"
```

```json:metadata
{"files": ["plugin/anti-tangent-guard/hooks/check_comment_write.py", "plugin/anti-tangent-guard/hooks/jev_scan.py", "plugin/anti-tangent-guard/hooks/jev_scan_test.py"], "verifyCommand": "python3 plugin/anti-tangent-guard/hooks/jev_scan_test.py -v", "acceptanceCriteria": ["regex violation short-circuits the tier", "disabled tier reports the reason", "flag exits 4 quoting the comment and probability", "third strike yields at exit 0", "exceptions exit 0 with jev-error", "breaker skips and errors trip it"], "modelTier": "standard"}
```

---

### Task 9: Carry the events through the wrapper

**Goal:** The wrapper captures the body's stdout, maps exit 4 to a block, and writes one trace line per outcome; nothing reaches Claude Code's transcript.

**Files:**
- Modify: `plugin/anti-tangent-guard/hooks/check-comment-write`
- Modify: `plugin/anti-tangent-guard/hooks/hooks.json`

**Acceptance Criteria:**
- [ ] The body's stdout is captured to a run-scoped temp file and removed afterwards; the hook's own stdout stays empty.
- [ ] `PIPESTATUS` still reads the body's status — command substitution is not used.
- [ ] Exit 4 becomes exit 2 with a `jev-block` trace line carrying the probability.
- [ ] Exit 0 traces the event the body reported (`jev-pass`, `jev-skip`, `jev-yield`, `jev-error`), falling back to `pass` when the body reported none.
- [ ] `hooks.json` sets `"timeout": 10` for the `Edit|Write` hook.

**Verify:** `bash plugin/anti-tangent-guard/evals/run.sh` → all cases pass (the new Jev cases arrive in Task 10)

**Steps:**

- [ ] **Step 1: Capture the body's stdout**

Replace the invocation and status handling at the end of `check-comment-write`:

```bash
ATG_OUT="$(mktemp "${TMPDIR:-/tmp}/atg-comment-write.XXXXXX")"
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

In `hooks.json`, the `Edit|Write` entry becomes:

```json
{ "type": "command", "command": "\"${CLAUDE_PLUGIN_ROOT}/hooks/check-comment-write\"", "timeout": 10 }
```

- [ ] **Step 3: Exercise it by hand**

```bash
export ANTI_TANGENT_GUARD_TRACE_LOG=/tmp/atg-manual.log
export ANTI_TANGENT_JEV=1 TYPESAFE_API_KEY=k ANTI_TANGENT_JEV_FAKE_PROB=0.95
printf '{"tool_name":"Write","session_id":"s1","tool_input":{"file_path":"/tmp/atg-x.go","content":"// The count cap used to return a plain error.\\npackage x\\n"}}' \
  | plugin/anti-tangent-guard/hooks/check-comment-write; echo "exit=$?"
tail -2 /tmp/atg-manual.log
```
Expected: `exit=2`, and a trace line reading `… | comment-write | jev-block | p=0.95`. Nothing on stdout.

- [ ] **Step 4: Run the existing suite**

Run: `bash plugin/anti-tangent-guard/evals/run.sh`
Expected: every case passes — the capture must not change any existing outcome.

- [ ] **Step 5: Commit**

```bash
git add plugin/anti-tangent-guard/hooks/check-comment-write plugin/anti-tangent-guard/hooks/hooks.json
git commit -m "feat(guard): trace the semantic tier's outcome"
```

```json:metadata
{"files": ["plugin/anti-tangent-guard/hooks/check-comment-write", "plugin/anti-tangent-guard/hooks/hooks.json"], "verifyCommand": "bash plugin/anti-tangent-guard/evals/run.sh", "acceptanceCriteria": ["body stdout captured to a temp file and removed", "PIPESTATUS still used", "exit 4 becomes exit 2 with jev-block", "exit 0 traces the reported event", "hooks.json timeout is 10"], "modelTier": "standard"}
```

---

### Task 10: Eval cases against a stub server

**Goal:** The shipped eval suite drives the real hook binary against a local stub, so the tier's behaviour is pinned end to end without touching the network.

**Files:**
- Modify: `plugin/anti-tangent-guard/evals/run.sh` (stub facility, unset list, `EXPECTED_CASE_COUNT`, group comment)
- Create: `plugin/anti-tangent-guard/evals/jev-stub.py`
- Modify: `plugin/anti-tangent-guard/evals/guard-evals.json` (new cases)

**Acceptance Criteria:**
- [ ] `jev-stub.py` serves `/v1/systemone`, returning a probability taken from its argv, and binds to an ephemeral loopback port it prints.
- [ ] The runner starts it once, exports `ANTI_TANGENT_JEV_URL` pointing at it, and stops it in the existing `cleanup` trap.
- [ ] `ANTI_TANGENT_JEV`, `ANTI_TANGENT_JEV_URL`, `ANTI_TANGENT_JEV_MODEL`, `ANTI_TANGENT_JEV_THRESHOLD`, `ANTI_TANGENT_JEV_EXCLUDE`, `ANTI_TANGENT_JEV_URL_TRUSTED`, `ANTI_TANGENT_JEV_FAKE_PROB` and `TYPESAFE_API_KEY` are unset by the runner before any case.
- [ ] New cases: tier off by setting; tier off with no key; a flag blocking (exit 2) with `jev-block` in the trace; a regex hit short-circuiting (the stub records no request); a stub that never answers, allowing the write; and a third attempt on one path yielding.
- [ ] `EXPECTED_CASE_COUNT` and the header's group partition are updated to include a named Jev group, and both count checks pass.

**Verify:** `bash plugin/anti-tangent-guard/evals/run.sh` → all cases pass, count assertion holds

**Steps:**

- [ ] **Step 1: Write the stub**

Create `plugin/anti-tangent-guard/evals/jev-stub.py`:

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
p.add_argument("--hang", action="store_true")
args = p.parse_args()


class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        body = self.rfile.read(int(self.headers.get("Content-Length") or 0))
        if args.log:
            with open(args.log, "a") as fh:
                fh.write(body.decode("utf-8", "replace") + "\n")
        if args.hang:
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
sys.stdout.write("%d\n" % server.server_port)
sys.stdout.flush()
server.serve_forever()
```

- [ ] **Step 2: Start it from the runner**

In `run.sh`, after `HOOK_CWD` is created and before the case loop:

```bash
# A loopback stub stands in for the endpoint. Loopback is the one non-default
# host the hook will talk to without the operator's trust flag, which is what
# makes this possible without weakening that rule for everyone else.
JEV_LOG="$WORKDIR/jev-requests.log"
: > "$JEV_LOG"
exec {jev_fd}< <(python3 -B "$(dirname "${BASH_SOURCE[0]}")/jev-stub.py" --prob 0.95 --log "$JEV_LOG")
read -r JEV_PORT <&${jev_fd}
JEV_PID=$(pgrep -f "jev-stub.py --prob 0.95 --log $JEV_LOG" | head -1)
export ANTI_TANGENT_JEV_URL="http://127.0.0.1:$JEV_PORT/v1/systemone"
```

Extend the existing `cleanup()`:

```bash
cleanup() {
    [[ -n "${JEV_PID:-}" ]] && kill "$JEV_PID" 2>/dev/null
    rm -rf "$WORKDIR"
}
```

And add to the unset list beside the existing three:

```bash
unset ANTI_TANGENT_JEV
unset ANTI_TANGENT_JEV_MODEL
unset ANTI_TANGENT_JEV_THRESHOLD
unset ANTI_TANGENT_JEV_EXCLUDE
unset ANTI_TANGENT_JEV_URL_TRUSTED
unset ANTI_TANGENT_JEV_FAKE_PROB
unset TYPESAFE_API_KEY
```

`ANTI_TANGENT_JEV_URL` is exported above deliberately, after the unsets, so a case that wants the tier on reaches the stub rather than the real service.

- [ ] **Step 3: Add the cases**

Append to `guard-evals.json` (ids continue from the current maximum; `{{TMPDIR}}` is substituted by the runner as the existing cases use it):

```json
{
  "id": 146,
  "name": "jev-off-by-default",
  "hook": "check-comment-write",
  "stdin_raw": "{\"tool_name\":\"Write\",\"session_id\":\"j1\",\"tool_input\":{\"file_path\":\"{{TMPDIR}}/atg-eval.go\",\"content\":\"// The count cap used to return a plain error.\\npackage x\\n\"}}",
  "expected_exit": 0,
  "reason": "the tier is opt-in: without ANTI_TANGENT_JEV=1 a prose-history comment is not sent anywhere"
},
{
  "id": 147,
  "name": "jev-needs-a-key",
  "hook": "check-comment-write",
  "env": {"ANTI_TANGENT_JEV": "1"},
  "stdin_raw": "{\"tool_name\":\"Write\",\"session_id\":\"j2\",\"tool_input\":{\"file_path\":\"{{TMPDIR}}/atg-eval.go\",\"content\":\"// The count cap used to return a plain error.\\npackage x\\n\"}}",
  "expected_exit": 0,
  "reason": "the setting alone does not enable the tier; without a key there is nothing to call with"
},
{
  "id": 148,
  "name": "jev-flag-blocks",
  "hook": "check-comment-write",
  "env": {"ANTI_TANGENT_JEV": "1", "TYPESAFE_API_KEY": "eval-key"},
  "stdin_raw": "{\"tool_name\":\"Write\",\"session_id\":\"j3\",\"tool_input\":{\"file_path\":\"{{TMPDIR}}/atg-eval.go\",\"content\":\"// The count cap used to return a plain error.\\npackage x\\n\"}}",
  "expected_exit": 2,
  "expected_stderr_contains": ["reads as change history", "change_history"],
  "reason": "a block the stub scores above the threshold is refused, through the real hook binary"
},
{
  "id": 149,
  "name": "jev-regex-hit-short-circuits",
  "hook": "check-comment-write",
  "env": {"ANTI_TANGENT_JEV": "1", "TYPESAFE_API_KEY": "eval-key"},
  "stdin_raw": "{\"tool_name\":\"Write\",\"session_id\":\"j4\",\"tool_input\":{\"file_path\":\"{{TMPDIR}}/atg-eval.go\",\"content\":\"// fixes #58\\npackage x\\n\"}}",
  "expected_exit": 2,
  "expected_stderr_contains": ["an issue or pull-request reference"],
  "reason": "a tell already refuses this write, so the tier must not spend a request or an egress on it"
}
```

- [ ] **Step 4: Assert the short-circuit really made no request**

After the case loop in `run.sh`, beside the other post-loop checks:

```bash
# Case 149 is only meaningful if nothing was sent. The stub logs every request
# body it receives, so an empty log for that case's comment text is the proof
# an exit code cannot give.
if grep -q "fixes #58" "$JEV_LOG" 2>/dev/null; then
    echo "FAIL: a regex-refused write reached the endpoint"
    FAILED=$((FAILED + 1))
fi
```

- [ ] **Step 5: Update the counts**

Set `EXPECTED_CASE_COUNT=149` and add to the group comment above it:

```
# A fourth group covers the semantic tier: off by default, off without a key,
# a flag refusing the write through the real binary, and a tell refusing it
# first without the tier ever being asked.
```

- [ ] **Step 6: Run the suite**

Run: `bash plugin/anti-tangent-guard/evals/run.sh`
Expected: all cases pass, both count checks hold, and the short-circuit assertion is silent.

- [ ] **Step 7: Commit**

```bash
git add plugin/anti-tangent-guard/evals/
git commit -m "test(guard): pin the semantic tier against a loopback stub"
```

```json:metadata
{"files": ["plugin/anti-tangent-guard/evals/run.sh", "plugin/anti-tangent-guard/evals/jev-stub.py", "plugin/anti-tangent-guard/evals/guard-evals.json"], "verifyCommand": "bash plugin/anti-tangent-guard/evals/run.sh", "acceptanceCriteria": ["stub serves loopback with a fixed probability and logs requests", "runner starts and stops it in the trap", "all Jev variables unset before cases", "four new cases pass", "short-circuit proven by an empty request log", "EXPECTED_CASE_COUNT and the group partition updated"], "modelTier": "standard"}
```

---

### Task 11: The calibration set and its runner

**Goal:** The labeled comments and a runner live in the repository, so the question's accuracy can be re-measured whenever the wording or the model changes.

**USER-ORDERED GATE — NON-SKIPPABLE.** This task was requested by the user in the current conversation. It MUST NOT be closed by walking around it, by declaring it "verified inline", or by substituting a cheaper check. Close only after every item in `acceptanceCriteria` has been re-validated independently, with output captured.

**Files:**
- Create: `plugin/anti-tangent-guard/evals/jev-comments.jsonl`
- Create: `plugin/anti-tangent-guard/evals/jev-eval.py`

**Acceptance Criteria:**
- [ ] Every fixture's `block` text is produced by `jev_scan.build_blocks()`, so the measured text is byte-for-byte what the hook would send.
- [ ] Each row carries `id`, `source`, `block`, `label` (`history` / `not_history` / `ambiguous`) and `regex_hit`.
- [ ] The operator has confirmed the labels before the file is committed, and rows they re-judged carry their verdict.
- [ ] `jev-eval.py` requires `TYPESAFE_API_KEY` and `ANTI_TANGENT_JEV=1`, refuses to run without both, and never runs in CI.
- [ ] It caches answers keyed by the row id **and a hash of `jev-question.json`**, so an edited question cannot be scored against stale answers.
- [ ] It prints recall and precision per source group, and the overall counts.

**Verify:** `ANTI_TANGENT_JEV=1 TYPESAFE_API_KEY=$KEY python3 plugin/anti-tangent-guard/evals/jev-eval.py --limit 5` → prints a per-group table for 5 rows; `python3 plugin/anti-tangent-guard/evals/jev-eval.py` without the variables → exits non-zero with a message naming them

**Steps:**

- [ ] **Step 1: Rebuild the fixtures with the hook's own builder**

The measured set was built by a throwaway script whose block shape differs from `jev_scan.build_blocks()`. Rebuild it from the same five sources — the guard's own eval cases, `fp-class.tsv`, comments at HEAD matching the cue words, a random sample of HEAD comments, and the before/after pairs from the four commits that removed history framing (`4bb1729`, `53709c6`, `a174d25`, `1e9da90`) — running every candidate through the hook's builder:

```python
"""Build jev-comments.jsonl with the hook's own block builder."""
import json
import os
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
rows, seen = [], set()


def add(source, path, line_no, label):
    """One fixture row, with the block text the hook itself would send."""
    text = open(os.path.join(REPO, path), errors="replace").read()
    line = text.splitlines()[line_no - 1]
    blocks = jev_scan.build_blocks(path, [line], text)
    if not blocks:
        return
    block = blocks[0].text
    if block in seen:
        return
    seen.add(block)
    rows.append({"id": "%s-%d" % (os.path.basename(path), line_no),
                 "source": source, "path": "%s:%d" % (path, line_no),
                 "block": block, "label": label,
                 "regex_hit": bool(comment_scan.violations(path, block.splitlines()))})


tracked = subprocess.run(["git", "ls-files", "*.go", "*.py", "*.sh"],
                         capture_output=True, text=True, cwd=REPO).stdout.split()
for path in tracked:
    if "/testdata/" in path:
        continue
    for i, line in enumerate(open(os.path.join(REPO, path), errors="replace"), 1):
        if re.match(r"^\s*(//|#|\*)", line) and CUES.search(line):
            add("head-history-wording", path, i, "history")

with open(os.path.join(REPO, "plugin/anti-tangent-guard/evals/jev-comments.jsonl"), "w") as fh:
    for row in rows:
        fh.write(json.dumps(row) + "\n")
print(len(rows), "rows")
```

Run it, then extend it the same way for the other four sources: the guard eval cases supply their comment text directly (label from `expected_exit`), `fp-class.tsv` supplies path plus text (all `history`), the random sample takes comment lines matching neither the cues nor a tell (`not_history`), and each commit pair contributes its removed text as `history` and its replacement as `not_history`. Every label is a proposal until Step 2.

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

question_hash = hashlib.sha256(
    open(os.path.join(os.path.dirname(HERE), "hooks", "jev-question.json"), "rb").read()
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
cfg = jev_scan.config(dict(os.environ), "calibration.go")

for row in rows:
    if row["id"] in cache:
        continue
    # One block per request, which is the shape the hook sends. The verdict
    # carries the probability only when it flagged, so the threshold is
    # lowered here to read the raw number back for every row.
    probe = jev_scan.config(dict(os.environ, ANTI_TANGENT_JEV_THRESHOLD="0.0001"),
                            "calibration.go")
    verdict = jev_scan.judge([jev_scan.Block(row["block"], 0)], probe)
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

print("%-28s %5s %5s %6s %5s %5s" % ("group", "pos", "neg", "regex", "jev", "FP"))
for name, g in sorted(groups.items()):
    print("%-28s %5d %5d %6d %5d %5d"
          % (name, g["pos"], g["neg"], g["regex_tp"], g["jev_tp"], g["fp"]))
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
git add plugin/anti-tangent-guard/evals/jev-comments.jsonl plugin/anti-tangent-guard/evals/jev-eval.py
git commit -m "test(guard): commit the calibration set and its runner"
```

```json:metadata
{"files": ["plugin/anti-tangent-guard/evals/jev-comments.jsonl", "plugin/anti-tangent-guard/evals/jev-eval.py"], "verifyCommand": "ANTI_TANGENT_JEV=1 python3 plugin/anti-tangent-guard/evals/jev-eval.py --limit 5", "acceptanceCriteria": ["fixtures built by jev_scan.build_blocks", "rows carry id, source, block, label, regex_hit", "operator confirmed the judgement-call labels", "runner refuses without the key and the setting", "cache keyed by row id and question hash", "prints recall and precision per group"], "modelTier": "standard", "userGate": true, "tags": ["user-gate"], "gateScope": "calibration labels confirmed by the operator before the fixture is committed", "failurePolicy": "stop and report"}
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
- [ ] The README has a section covering: the two tiers and the order they run in; that the semantic tier judges the whole touched block while the tells judge added lines, and why; the five variables and the host rule; what leaves the machine and that zero retention is enterprise-only; fail-open, the breaker and the two-block yield; the CA-bundle failure mode under `python3 -I`; and the Windows gap.
- [ ] `plugin.json`'s version is bumped and its description no longer implies pattern matching is all the hook does.
- [ ] The `CHANGELOG.md` 0.24.0 entry names the tier, its gate and its failure behaviour.
- [ ] `CLAUDE.md`'s description of what the guard blocks mentions the semantic tier and its kill switch.
- [ ] `bash scripts/check-protocol-docs.sh` (if present) and `go test -race ./...` pass.

**Verify:** `go test -race ./... && bash plugin/anti-tangent-guard/evals/run.sh && python3 plugin/anti-tangent-guard/hooks/jev_scan_test.py -v && python3 plugin/anti-tangent-guard/hooks/comment_scan_test.py -v` → all green

**Steps:**

- [ ] **Step 1: Write the README section**

Add after the existing "The three block conditions" section:

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
the change. The scope stays bounded to that block; other comments in the file are never read and
never sent.

### Settings

| Variable | Default | Meaning |
|---|---|---|
| `ANTI_TANGENT_JEV` | unset | Must be exactly `1`. |
| `TYPESAFE_API_KEY` | unset | Required. |
| `ANTI_TANGENT_JEV_THRESHOLD` | `0.7` | Flag at or above. Anything outside (0, 1] falls back. |
| `ANTI_TANGENT_JEV_MODEL` | `jev-1.13.0` | Pinned: an alias moves under a tuned threshold. |
| `ANTI_TANGENT_JEV_URL` | the TypeSafe endpoint | Honoured for loopback, or with `ANTI_TANGENT_JEV_URL_TRUSTED=1`. |
| `ANTI_TANGENT_JEV_EXCLUDE` | unset | Colon-separated globs never sent. |

**Why the URL is restricted.** A repository's own checked-in settings can set environment for your
hooks. Without this rule, cloning a repository would be enough to have your key posted to a host
of its choosing. Set `ANTI_TANGENT_JEV_URL_TRUSTED=1` in your own global settings if you route
through a proxy.

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

On Windows there is no `O_NOFOLLOW`, so a `Write` over an existing file is not scanned at all and
this tier never runs for it. An `Edit` is judged without the post-edit text.
```

- [ ] **Step 2: Update `plugin.json`**

Bump `version` to `0.5.0` (or the next patch above whatever main carries after the rebase) and extend the description with one clause: a second, optional tier reads the touched comment block semantically when `ANTI_TANGENT_JEV=1` and a key is present.

- [ ] **Step 3: Fill in the changelog entry**

Expand the 0.24.0 `### Added` bullet written in Task 0 with the gate, the block unit, the yield and the fail-open behaviour.

- [ ] **Step 4: Update `CLAUDE.md`**

In "What This Repo Is Not", the sentence describing what `anti-tangent-guard` blocks gains the semantic tier and names `ANTI_TANGENT_JEV` as its own switch, distinct from `ANTI_TANGENT_COMMENT_GUARD`.

- [ ] **Step 5: Run everything**

```bash
go test -race ./...
bash plugin/anti-tangent-guard/evals/run.sh
bash plugin/anti-tangent-guard/evals/fp-report.sh
python3 plugin/anti-tangent-guard/hooks/jev_scan_test.py -v
python3 plugin/anti-tangent-guard/hooks/comment_scan_test.py -v
```
Expected: all green.

- [ ] **Step 6: Commit**

```bash
git add plugin/anti-tangent-guard/README.md plugin/anti-tangent-guard/.claude-plugin/plugin.json CHANGELOG.md CLAUDE.md
git commit -m "docs(guard): document the semantic tier"
```

```json:metadata
{"files": ["plugin/anti-tangent-guard/README.md", "plugin/anti-tangent-guard/.claude-plugin/plugin.json", "CHANGELOG.md", "CLAUDE.md"], "verifyCommand": "go test -race ./... && bash plugin/anti-tangent-guard/evals/run.sh && python3 plugin/anti-tangent-guard/hooks/jev_scan_test.py -v", "acceptanceCriteria": ["README covers tiers, block unit, variables, egress, fail-open, breaker, yield, CA bundle, Windows", "plugin.json version and description updated", "changelog entry complete", "CLAUDE.md mentions the tier and its switch", "all suites green"], "modelTier": "mechanical"}
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
