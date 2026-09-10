# anti-tangent-mcp v0.20.0 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the ten comment-hygiene and completion-gate gaps reported in issue #71, plus an eleventh (G11) found while preparing this plan's own dispatch, so a comment carrying change history cannot reach disk unseen and a completion cannot be graded a pass on invented or absent evidence.

**Architecture:** Three independent surfaces. The `anti-tangent-guard` plugin's Python scanner learns to see block-comment interiors and trailing comments without a lexer — by extending the opener set and adding a quote-parity test that can only decline to scan, never promote code to comment. Its close-time hook learns to ask git which lines are new when a completion submits `final_files` instead of a diff, and its two kill switches are separated so each controls the concern it names. The MCP server's CodeScene ladder is flattened to one rung so that inventing a skip reason stops being cheaper than staying silent, and a deterministic test-evidence check rejects output showing that nothing ran.

**Tech Stack:** Go 1.x (server, `-race` tests, RE2 regexp), Python 3 (guard hooks, stdlib only), Bash (hook wrappers), Go `text/template` (reviewer prompts, golden-file tested).

**Python tests use `unittest` from the standard library, run as
`python3 -m unittest discover -s plugin/anti-tangent-guard/hooks -p '*_test.py'`.** Not pytest:
the hooks are stdlib-only by design and CI installs no Python packages (`ci.yml` installs `jq` and
nothing else). Bare `assert` functions are NOT discovered by `unittest`, so every Python test in
this plan is a `unittest.TestCase` method.

**Spec:** `docs/superpowers/specs/2026-09-10-anti-tangent-v0.20.0-design.md`

## Global Constraints

- **The MCP server never blocks and never corrects.** Enforcement lives in plugin hooks only. No task may add a blocking behaviour to `internal/`.
- **Fail open, always.** Every scanner, git call, and lexical test in the guard plugin must treat "I could not decide" as "allow". A false block costs more than a miss. This is the existing weighting in `comment_scan.py` and no task may reverse it.
- **`*/` is not a comment opener.** A line beginning with it carries no comment text, and returning its tail hands ordinary code to the tells. Block-opener text stops at the first `*/` for the same reason.
- **`ATG_SCAN_EXTS` in `check-comment-write` duplicates `SCAN_EXTS` in `comment_scan.py`.** `evals/run.sh:522-540` asserts they stay identical. No task changes one without the other.
- **`EXPECTED_CASE_COUNT` in `evals/run.sh:155` must equal the case count in `guard-evals.json`.** Every task adding eval cases updates it in the same commit.
- **Protocol parts are capped at strictly under 16,000 bytes** (`.github/workflows/ci.yml:61-74`), with a warning at 15,500. `core.md` has 109 bytes of headroom and `implementer.md` has 407 — rewrites in those two files must come in at or under the length they replace.
- **`plugin/anti-tangent-protocol/protocol/` must be byte-identical to `docs/protocol/`.** Resync in the same commit as any protocol edit: `rm -f plugin/anti-tangent-protocol/protocol/*.md && cp docs/protocol/*.md plugin/anti-tangent-protocol/protocol/`
- **Comments: `anti-tangent-protocol` `implementer.md` §4.4.** In full, because this plan is enforcing it: comments explain non-trivial behaviour, or a non-obvious invariant or hazard that would bite the next editor, and must read correctly to someone who never saw the change that introduced it. They carry no issue, pull-request, task or version references and no "previously" / "no longer" / "this replaced" — git holds that.
- **Scope of that rule in THIS release: comments you add or edit, and nothing else.** `evals/run.sh` and `check-task-complete` already contain comments the policy would flag ("Task 16", "Task 8's pinned re-validation semantics", "final review Important #1"), and Tasks 2–5 all edit those files. Rewriting every such comment is a file-wide cleanup with a large diff and no test behind it, which is not what any of these tasks is for. Leave pre-existing comments alone unless the lines you are changing carry them; a cleanup pass is its own release.
- **Test fixtures containing `// fixes #58` are data, not comments, and are exempt** — this release's own eval table is full of them by necessity.
- **`VERSION` is not edited on this branch.** The release workflow bumps it.

**User decisions (already made):**
- "All ten in one release" — G1–G10 ship together as 0.20.0, not split across releases.
- G11 was added after that decision, during this plan's own handoff: `validate_plan` gates on a plan that gives implementers no comment policy. It is an eleventh gap, not one of the ten, and it rides in Task 9.
- G6: two ladders keyed on `ANTI_TANGENT_CODESCENE` mode, not on lightweight-vs-session. "If codescene is required it should run even for lightweight tasks"; unset stays lenient.
- G7: "Deterministic, server-side" — a Go check beside `codesceneFindings`, not a reviewer prompt rule.
- G7 scope (after review): fire only on markers meaning nothing ran. `UP-TO-DATE` / `FROM-CACHE` / `(cached)` attest a prior passing run and must NOT fire.
- G4: "Ask git" for the added/pre-existing distinction on `final_files`.
- G5: "Make the switches match their names" — behaviour changes, docs follow.
- G8: "Reviewer-led, prose in plan_rules.tmpl", not a ported Go regex.
- G1: "Keep opt-in only, say so honestly" — no default tell, and the spec states outright that the reported incident is not caught on an unconfigured project.
- Part 1a (after review): prefix + parity, NOT a per-language lexer. The lexer is explicitly deferred.

---

### Task 1: Line-anchor regex accepts comma lists

**Goal:** `validate_plan`'s disk tier stops reporting existing files as missing when a Files bullet anchors more than two line numbers.

**Files:**
- Modify: `internal/planparser/filerefs.go`
- Test: `internal/planparser/filerefs_test.go`

**Acceptance Criteria:**
- [ ] `Modify: \`docs/SomeDoc.md:60,166,174,419\`` yields the path `docs/SomeDoc.md`
- [ ] `F.kt:27-30,40-50` yields `F.kt`
- [ ] `File.kt:27`, `F.kt:27-30`, `F.kt:27,30`, `F.kt:12:3` all still strip
- [ ] `C:\x`, `https://example.com/a` and `plain.go` are returned untouched
- [ ] The `lineAnchorRe` comment states that an anchor is a comma-separated list of lines-or-ranges

**Verify:** `go test ./internal/planparser/... -run TestLineAnchor -v` → PASS

**Steps:**

- [ ] **Step 1: Write the failing test**

Add to `internal/planparser/filerefs_test.go`:

```go
func TestLineAnchorStripsCommaLists(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"docs/SomeDoc.md:60,166,174,419", "docs/SomeDoc.md"},
		{"F.kt:27-30,40-50", "F.kt"},
		{"File.kt:27", "File.kt"},
		{"F.kt:27-30", "F.kt"},
		{"F.kt:27,30", "F.kt"},
		{"F.kt:12:3", "F.kt"},
		{"plain.go", "plain.go"},
		{`C:\x`, `C:\x`},
		{"https://example.com/a", "https://example.com/a"},
	} {
		if got := stripLineAnchor(tc.in); got != tc.want {
			t.Errorf("stripLineAnchor(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
```

- [ ] **Step 2: Run it and watch the two comma-list rows fail**

Run: `go test ./internal/planparser/... -run TestLineAnchorStripsCommaLists -v`
Expected: FAIL on `docs/SomeDoc.md:60,166,174,419` and `F.kt:27-30,40-50`, both returning the input unchanged.

- [ ] **Step 3: Widen the regex**

In `internal/planparser/filerefs.go`, replace the `lineAnchorRe` definition at line 50:

```go
	lineAnchorRe = regexp.MustCompile(`(?::\d+(?:-\d+)?(?:,\d+(?:-\d+)?)*)+$`)
```

- [ ] **Step 4: Record the invariant the old form got wrong**

Immediately above `lineAnchorRe`, after the existing paragraph ending "…is followed by slashes, not digits.", insert:

```go
	// An anchor is a comma-separated LIST of lines-or-ranges, not a single
	// line or a single pair: "a.md:60,166,174,419" and "a.md:27-30,40-50"
	// are both ordinary plan bullets. A form permitting only one separator
	// per group matches ":60,166" and then fails the whole anchor, leaving
	// the digits attached to the path — which the disk tier stats verbatim
	// and reports as a file that does not exist.
```

- [ ] **Step 5: Run the test**

Run: `go test ./internal/planparser/... -run TestLineAnchorStripsCommaLists -v`
Expected: PASS

- [ ] **Step 6: Run the whole package to catch regressions in FileRefs**

Run: `go test -race ./internal/planparser/...`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/planparser/filerefs.go internal/planparser/filerefs_test.go
git commit -m "fix(planparser): accept multi-element line anchors on Files bullets"
```

---

### Task 2: Scanner sees block-comment interiors and trailing comments

**Goal:** `comment_scan.py` recognises three comment shapes it cannot see today — a starred block continuation (` * text`), a block comment opened and closed on one line (`/* text */`), and a trailing comment after code — without a lexer, and without any of the three admitting code as comment text.

**Files:**
- Modify: `plugin/anti-tangent-guard/hooks/comment_scan.py`
- Modify: `plugin/anti-tangent-guard/evals/guard-evals.json`
- Modify: `plugin/anti-tangent-guard/evals/run.sh:155`

**Acceptance Criteria:**
- [ ] ` * ABC-1234: the inbound HELP keyword.` is scanned as comment text — asserted on `comment_spans`' return value, since no tell matches it without a configured pattern and `violations` therefore cannot show it was read at all
- [ ] ` * Fixes #123` is flagged as an issue reference
- [ ] `/* fixes task-42 */` is flagged
- [ ] `val x = 1 // fixes task-42` is flagged
- [ ] `*p = task-42;` is NOT flagged — `*` opens a comment only when followed by whitespace or end-of-line
- [ ] `*/ y = task-42;` is NOT flagged — `*/` is not an opener, and code after a terminator is code
- [ ] `/* note */ y = task-42;` is NOT flagged — block text stops at the first `*/`
- [ ] `x = 1; /* note */ y = task-42;` is NOT flagged — the same truncation applies to a block comment that starts mid-line, not only to one at the start
- [ ] `x = 1 /* fixes #1 */ // ok` IS flagged — spans are walked left to right, so a benign trailing comment cannot hide a violating one before it
- [ ] `var y = 1 /* fixes */ z /* #1 */` is NOT flagged — tells run against each span separately; joining spans would synthesise a reference present in neither
- [ ] An unstarred block interior (`/*` / `fixes task-42` / `*/` across three lines) is a DOCUMENTED miss with an eval asserting exit 0, not a silent gap
- [ ] `x = "a" + "b//c"` is NOT flagged even with a tell inside the string
- [ ] `echo "a #b"` in a `.sh` file is NOT flagged
- [ ] `url=${url#https://t/task-42}` in a `.sh` file is NOT flagged
- [ ] Every pre-existing eval case still passes

**Verify:** `bash plugin/anti-tangent-guard/evals/run.sh` → all cases pass

**Steps:**

- [ ] **Step 1: Write the failing checks as a scratch harness**

Run this to record current behaviour before changing anything:

```bash
cd plugin/anti-tangent-guard/hooks && python3 - <<'PY'
from comment_scan import violations
cases = [
    ("X.kt", " * Fixes #123",              True),
    ("X.kt", "/* fixes task-42 */",        True),
    ("X.kt", "val x = 1 // fixes task-42", True),
    ("X.c",  "*p = task-42;",              False),
    ("X.go", 'x = "a" + "b//c fixes #1"',  False),
    ("X.sh", 'echo "a #b fixes #1"',       False),
    ("X.sh", "url=${url#https://t/task-42}", False),
]
for path, line, want in cases:
    got = bool(violations(path, [line]))
    print(("OK  " if got == want else "FAIL"), f"want={want} got={got}  {line}")
PY
```

Expected before the change: the first three print `FAIL want=True got=False`; the last four print `OK`.

- [ ] **Step 2: Replace the opener table and add the two helpers**

In `plugin/anti-tangent-guard/hooks/comment_scan.py`, replace the `LINE_COMMENT` / `DEFAULT_COMMENT` block (and the comment above it that declares block and trailing comments out of scope) with:

```python
# Comment openers per extension. Hash-family files have no block comment
# form; C-family files do, and two of its shapes are recognised here: an
# opener `/*`, and a continuation `*`.
#
# `*/` is NOT an opener. A line beginning with it carries no comment text --
# `*/` alone is empty and `*/ y = task-42;` is code -- so admitting it and
# returning the tail would hand ordinary code to the tells.
LINE_COMMENT = {
    ".py": ("#",), ".sh": ("#",), ".bash": ("#",), ".rb": ("#",),
}
DEFAULT_COMMENT = ("//", "/*", "*")


def _quotes_balanced(prefix):
    """True when no quote style is left open at the end of prefix.

    A counting test, not a parser: it cannot tell which style opened a span,
    only whether an odd number of some style is outstanding. Odd means the
    delimiter that follows sits inside a string literal. Backslash-escaped
    quotes do not count, so 'a\\'b' reads as balanced.
    """
    counts = {'"': 0, "'": 0, "`": 0}
    esc = False
    for ch in prefix:
        if esc:
            esc = False
        elif ch == "\\":
            esc = True
        elif ch in counts:
            counts[ch] += 1
    return all(n % 2 == 0 for n in counts.values())


_HASH_DELIM = re.compile(r"(?<=\s)#")
_SLASH_DELIM = re.compile(r"//|/\*")


def _line_comment_spans(opens, raw):
    """Every comment span on a code line, concatenated, else "".

    Walks delimiters LEFT TO RIGHT and takes the first whose preceding quote
    counts are all even; an odd count means that delimiter sits inside a
    string literal, so it is skipped and the walk continues. Taking the last
    delimiter instead would let a benign trailing comment hide a violating
    one earlier on the same line (`x = 1 /* fixes #1 */ // ok`).

    A `/* ... */` span ends at its terminator and the walk RESUMES after it,
    so the code between two block comments is never scanned and a second
    comment on the line is not lost. A `//` or `#` span runs to end of line
    and ends the walk.

    Within the quoting shapes counted by _quotes_balanced, this can decline a
    genuine comment but will not promote code to comment. In hash-family files
    the delimiter must be whitespace-preceded, so shell parameter expansion
    (`${url#https://…}`) is not a comment.
    """
    delim = _HASH_DELIM if "#" in opens else _SLASH_DELIM
    out, pos = [], 0
    while pos < len(raw):
        m = delim.search(raw, pos)
        if m is None:
            break
        start = m.start()
        if not _quotes_balanced(raw[:start]):
            pos = m.end()
            continue
        if m.group(0) == "/*":
            end = raw.find("*/", m.end())
            if end < 0:
                out.append(raw[m.end():])
                break
            out.append(raw[m.end():end])
            pos = end + 2
            continue
        out.append(raw[m.end():])
        break
    return [t for t in out if t.strip()]


def comment_spans(path, raw):
    """Every comment span on one line, as a list; empty when there are none.

    A LIST, not one joined string. Tells are matched per span, because joining
    them can synthesise a reference that exists in neither: "/* fixes */ x
    /* #1 */" concatenates to "fixes   #1", which the issue tell matches while
    each span alone is clean.

    Two shapes. A line whose first non-space characters open a comment yields
    everything after the opener. A line that starts with code yields the spans
    found by the left-to-right walk above.
    """
    opens = openers(path)
    line = raw.strip()
    for o in opens:
        if not line.startswith(o):
            continue
        rest = line[len(o):]
        # A bare `*` opens a comment only as a block continuation — `* text`
        # or a lone `*`. `*p = task-42;` is a dereference and a subtraction,
        # and reading it as comment text would let ordinary C block a write.
        if o == "*" and rest and not rest[0].isspace():
            continue
        # Everything past the block terminator is code again. Without this,
        # `/* note */ y = task-42;` hands the subtraction to the tells.
        if o in ("/*", "*"):
            end = rest.find("*/")
            if end >= 0:
                rest = rest[:end]
        return [rest] if rest.strip() else []
    return _line_comment_spans(opens, raw)
```

- [ ] **Step 3: Route `violations` through `comment_text`**

Replace the body of `violations` with:

```python
def violations(path, added_lines):
    """Return [(line, why)] for added comment lines carrying change history."""
    if not scannable(path):
        return []
    out = []
    for raw in added_lines:
        for span in comment_spans(path, raw):
            hit = next((why for pat, why in TELLS if pat.search(span)), None)
            if hit is not None:
                out.append((raw.strip(), hit))
                break
    return out
```

- [ ] **Step 4: Re-run the scratch harness**

Run the Step 1 heredoc again.
Expected: all seven rows print `OK`.

- [ ] **Step 5: Add eval cases**

Append these to the `evals` array in `plugin/anti-tangent-guard/evals/guard-evals.json`, renumbering `id` to continue from the current maximum (93):

```json
{
  "id": 94,
  "name": "comment-write-kdoc-issue-ref-blocks",
  "hook": "check-comment-write",
  "stdin_raw": "{\"tool_name\":\"Write\",\"tool_input\":{\"file_path\":\"{{TMPDIR}}/atg-eval.kt\",\"content\":\"/**\\n * Fixes #123\\n */\\nobject X\\n\"}}",
  "expected_exit": 2,
  "expected_stderr_contains": ["an issue or pull-request reference"],
  "reason": "a KDoc block interior is comment text and must be scanned"
},
{
  "id": 95,
  "name": "comment-write-block-comment-one-line-blocks",
  "hook": "check-comment-write",
  "stdin_raw": "{\"tool_name\":\"Write\",\"tool_input\":{\"file_path\":\"{{TMPDIR}}/atg-eval.go\",\"content\":\"/* fixes task-42 */\\npackage x\\n\"}}",
  "expected_exit": 2,
  "expected_stderr_contains": ["a task reference"],
  "reason": "a single-line block comment must be scanned"
},
{
  "id": 96,
  "name": "comment-write-trailing-comment-blocks",
  "hook": "check-comment-write",
  "stdin_raw": "{\"tool_name\":\"Write\",\"tool_input\":{\"file_path\":\"{{TMPDIR}}/atg-eval.kt\",\"content\":\"val x = 1 // fixes task-42\\n\"}}",
  "expected_exit": 2,
  "expected_stderr_contains": ["a task reference"],
  "reason": "a trailing comment on a code line must be scanned"
},
{
  "id": 97,
  "name": "comment-write-dereference-not-a-comment",
  "hook": "check-comment-write",
  "stdin_raw": "{\"tool_name\":\"Write\",\"tool_input\":{\"file_path\":\"{{TMPDIR}}/atg-eval.c\",\"content\":\"int f(void){ int task=1;\\n*p = task-42;\\nreturn 0; }\\n\"}}",
  "expected_exit": 0,
  "reason": "`*` opens a comment only when followed by whitespace; *p is a dereference"
},
{
  "id": 98,
  "name": "comment-write-tell-inside-string-allowed",
  "hook": "check-comment-write",
  "stdin_raw": "{\"tool_name\":\"Write\",\"tool_input\":{\"file_path\":\"{{TMPDIR}}/atg-eval.go\",\"content\":\"package x\\nvar s = \\\"a\\\" + \\\"b//c fixes #1\\\"\\n\"}}",
  "expected_exit": 0,
  "reason": "an odd quote count before the delimiter means it is inside a literal"
},
{
  "id": 99,
  "name": "comment-write-shell-string-hash-allowed",
  "hook": "check-comment-write",
  "stdin_raw": "{\"tool_name\":\"Write\",\"tool_input\":{\"file_path\":\"{{TMPDIR}}/atg-eval.sh\",\"content\":\"echo \\\"a #b fixes #1\\\"\\n\"}}",
  "expected_exit": 0,
  "reason": "a # inside a shell string literal is not a comment"
},
{
  "id": 100,
  "name": "comment-write-shell-param-expansion-allowed",
  "hook": "check-comment-write",
  "stdin_raw": "{\"tool_name\":\"Write\",\"tool_input\":{\"file_path\":\"{{TMPDIR}}/atg-eval.sh\",\"content\":\"url=${url#https://t/task-42}\\n\"}}",
  "expected_exit": 0,
  "reason": "a # not preceded by whitespace is parameter expansion, not a comment"
},
{
  "id": 101,
  "name": "comment-write-block-terminator-not-an-opener",
  "hook": "check-comment-write",
  "stdin_raw": "{\"tool_name\":\"Write\",\"tool_input\":{\"file_path\":\"{{TMPDIR}}/atg-eval.c\",\"content\":\"int f(void){ int task=1; int *p=&task;\\n*/ *p = task-42;\\nreturn 0; }\\n\"}}",
  "expected_exit": 0,
  "reason": "`*/` is not an opener; code after a block terminator is code, not comment text"
},
{
  "id": 102,
  "name": "comment-write-block-text-stops-at-terminator",
  "hook": "check-comment-write",
  "stdin_raw": "{\"tool_name\":\"Write\",\"tool_input\":{\"file_path\":\"{{TMPDIR}}/atg-eval.c\",\"content\":\"int f(void){ int task=1; int *p=&task;\\n/* note */ *p = task-42;\\nreturn 0; }\\n\"}}",
  "expected_exit": 0,
  "reason": "block-opener text stops at the first `*/`, so the trailing subtraction is not scanned"
},
{
  "id": 103,
  "name": "comment-write-unstarred-block-interior-is-a-known-miss",
  "hook": "check-comment-write",
  "stdin_raw": "{\"tool_name\":\"Write\",\"tool_input\":{\"file_path\":\"{{TMPDIR}}/atg-eval.go\",\"content\":\"/*\\nfixes task-42\\n*/\\npackage x\\n\"}}",
  "expected_exit": 0,
  "reason": "an unstarred block interior has no opener on its middle line; documented miss, backstopped by post.tmpl when a diff is present"
},
{
  "id": 104,
  "name": "comment-write-midline-block-stops-at-terminator",
  "hook": "check-comment-write",
  "stdin_raw": "{\"tool_name\":\"Write\",\"tool_input\":{\"file_path\":\"{{TMPDIR}}/atg-eval.c\",\"content\":\"int f(void){ int task=1; int *p=&task;\\nint q=1; /* note */ *p = task-42;\\nreturn 0; }\\n\"}}",
  "expected_exit": 0,
  "reason": "a block comment starting mid-line ends at its terminator; the code after it must not reach the tells"
},
{
  "id": 105,
  "name": "comment-write-earlier-comment-not-hidden-by-later-one",
  "hook": "check-comment-write",
  "stdin_raw": "{\"tool_name\":\"Write\",\"tool_input\":{\"file_path\":\"{{TMPDIR}}/atg-eval.go\",\"content\":\"package x\\nvar y = 1 /* fixes #1 */ // ok\\n\"}}",
  "expected_exit": 2,
  "expected_stderr_contains": ["an issue or pull-request reference"],
  "reason": "spans are walked left to right, so a benign trailing comment cannot hide a violating one earlier on the line"
},
{
  "id": 106,
  "name": "comment-write-fragments-across-spans-do-not-join",
  "hook": "check-comment-write",
  "stdin_raw": "{\"tool_name\":\"Write\",\"tool_input\":{\"file_path\":\"{{TMPDIR}}/atg-eval.go\",\"content\":\"package x\\nvar y = 1 /* fixes */ z /* #1 */\\n\"}}",
  "expected_exit": 0,
  "reason": "tells run per span; joining them would synthesise `fixes #1` from two harmless fragments"
}
```

- [ ] **Step 6: Update the declared case count**

In `plugin/anti-tangent-guard/evals/run.sh`, line 155:

```bash
EXPECTED_CASE_COUNT=106
```

Also update the `description` field at the top of `guard-evals.json` to read `(106 cases)`.

- [ ] **Step 7: Run the eval suite**

Run: `bash plugin/anti-tangent-guard/evals/run.sh`
Expected: all 106 cases pass, including every pre-existing one.

- [ ] **Step 8: Measure the new false-positive surface**

Run: `bash plugin/anti-tangent-guard/evals/fp-report.sh`
Expected: no new false positives versus the report on `main`. If any appear they are in scope for this task: within the shapes the parity test models, a hit means a defect in `_quotes_balanced` or `comment_text`, not an acceptable cost. If one comes from a construct the test does not model, record it as a known miss rather than widening the test.

- [ ] **Step 9: Commit**

```bash
git add plugin/anti-tangent-guard/hooks/comment_scan.py \
        plugin/anti-tangent-guard/evals/guard-evals.json \
        plugin/anti-tangent-guard/evals/run.sh
git commit -m "feat(guard): scan block-comment interiors and trailing comments"
```

---

### Task 3: Optional project ticket pattern, bounded by a wall clock

**Goal:** A project can name its tracker-key shape and have the scanner enforce it, with no default and no way for a bad pattern to hang a blocking hook.

**Files:**
- Modify: `plugin/anti-tangent-guard/hooks/comment_scan.py`
- Modify: `plugin/anti-tangent-guard/evals/guard-evals.json`
- Modify: `plugin/anti-tangent-guard/evals/run.sh:155`
- Modify: `plugin/anti-tangent-guard/evals/fp-scan.py`
- Modify: `plugin/anti-tangent-guard/evals/fp-report.sh`
- Create: `plugin/anti-tangent-guard/hooks/comment_scan_test.py`

**Acceptance Criteria:**
- [ ] With `ANTI_TANGENT_TICKET_PATTERN='ABC-\d+'`, `// ABC-1234: the keyword` is flagged as a tracker reference
- [ ] With the variable unset, the same line is clean
- [ ] A pattern that fails to compile is ignored, and the scanner still runs its other tells
- [ ] A pattern longer than 200 characters is ignored
- [ ] A catastrophic-backtracking pattern causes the scan to return no violations rather than hanging
- [ ] A 201-character pattern is refused and a 200-character one is accepted, asserted by a test rather than by inspection
- [ ] `scan_deadline` running where the timer cannot be installed (a non-main thread) still SCANS, rather than failing open and disabling every tell
- [ ] `fp-scan.py` and `fp-report.sh` unset the variable so the false-positive gate does not depend on the developer's environment

**Verify:** `bash plugin/anti-tangent-guard/evals/run.sh` → all cases pass

**Steps:**

- [ ] **Step 1: Write the failing checks**

```bash
cd plugin/anti-tangent-guard/hooks && python3 - <<'PY'
import os, subprocess, sys
def run(env, line):
    code = ("from comment_scan import violations;"
            "print(bool(violations('X.kt', [%r])))" % line)
    e = dict(os.environ); e.pop("ANTI_TANGENT_TICKET_PATTERN", None); e.update(env)
    return subprocess.run([sys.executable, "-c", code], capture_output=True,
                          text=True, env=e).stdout.strip()
print("set     ->", run({"ANTI_TANGENT_TICKET_PATTERN": r"ABC-\d+"}, "// ABC-1234: the keyword"), "(want True)")
print("unset   ->", run({}, "// ABC-1234: the keyword"), "(want False)")
print("bad re  ->", run({"ANTI_TANGENT_TICKET_PATTERN": "ABC-(\\d+"}, "// fixes task-42"), "(want True)")
PY
```

Expected before the change: `set -> False` (wrong), `unset -> False`, `bad re -> True`.

- [ ] **Step 2: Add the imports**

At the top of `comment_scan.py`, extend the import block:

```python
import contextlib
import os
import re
import signal
import stat
from collections import Counter
```

- [ ] **Step 3: Append the ticket tell after the `TELLS` tuple**

```python
# ANTI_TANGENT_TICKET_PATTERN is an optional project-supplied tell for the
# local tracker-key shape. There is deliberately NO default: measured over
# real comment lines, a generic `[A-Z]+-\d+` matches hardware identifiers
# (HDMI-0, DP-0) and prose labels (ROUND-1, ROUND-8) far more often than a
# tracker key, and an allowlist cannot anticipate those. An unconfigured
# project therefore gets no ticket tell at all, and the reported shape
# `ABC-1234:` goes unflagged here.
_TICKET_PATTERN_MAX_LEN = 200


def _ticket_tell():
    pat = os.environ.get("ANTI_TANGENT_TICKET_PATTERN", "")
    if not pat or len(pat) > _TICKET_PATTERN_MAX_LEN:
        return None
    try:
        return (re.compile(pat), "a tracker reference")
    except re.error:
        return None


_ticket = _ticket_tell()
if _ticket is not None:
    TELLS = TELLS + (_ticket,)
```

- [ ] **Step 4: Add the wall-clock bound**

Below the ticket tell:

```python
# A wall-clock bound on one call to violations(). ANTI_TANGENT_TICKET_PATTERN
# is operator-supplied and Python's re backtracks, so a pattern can take
# unbounded time on a hostile line. Structural checks do not close this: a
# nested-quantifier test misses (a|aa)+ and (a|a)*, and (a+)+$ over a
# forty-character line still hangs. Only a timer bounds it, and it covers the
# built-in TELLS too.
#
# Installing the timer can fail for reasons that are not the scanner's
# business: SIGALRM does not exist on Windows, and on Unix `signal.signal`
# raises in any thread but the main one even though the constant is present.
# Both cases run the scan UNBOUNDED rather than failing it -- a missing
# deadline must not become a missing scan, which is what letting the error
# reach violations()'s fail-open handler would do.
#
# The handler is saved and restored, but a previously ARMED ITIMER_REAL is
# not: the timer is simply disarmed on the way out. That is sound only
# because this runs in a hook process that owns its own lifetime and arms no
# other real-time timer. A caller that did would need its remaining time
# read and re-armed here.
SCAN_TIMEOUT_SECONDS = 2.0


class _ScanTimeout(Exception):
    pass


@contextlib.contextmanager
def scan_deadline(seconds=SCAN_TIMEOUT_SECONDS):
    def _fire(signum, frame):
        raise _ScanTimeout()

    try:
        previous = signal.signal(signal.SIGALRM, _fire)
    except (AttributeError, ValueError, OSError):
        # No SIGALRM, or not the main thread. Run without a bound.
        yield
        return
    try:
        signal.setitimer(signal.ITIMER_REAL, seconds)
    except (AttributeError, ValueError, OSError):
        signal.signal(signal.SIGALRM, previous)
        yield
        return
    try:
        yield
    finally:
        signal.setitimer(signal.ITIMER_REAL, 0)
        signal.signal(signal.SIGALRM, previous)
```

- [ ] **Step 5: Wrap the scan loop**

Replace the body of `violations` written in Task 2 with:

```python
def violations(path, added_lines):
    """Return [(line, why)] for added comment lines carrying change history.

    Anything that stops the scan completing — the deadline above, or any
    exception from a caller-supplied pattern — yields no violations. This
    module's standing rule is that an undecidable scan allows the write.
    """
    if not scannable(path):
        return []
    out = []
    try:
        with scan_deadline():
            for raw in added_lines:
                for span in comment_spans(path, raw):
                    hit = next((why for pat, why in TELLS if pat.search(span)), None)
                    if hit is not None:
                        out.append((raw.strip(), hit))
                        break
    except Exception:
        return []
    return out
```

- [ ] **Step 6: Re-run the Step 1 checks**

Expected: `set -> True`, `unset -> False`, `bad re -> True`.

- [ ] **Step 7: Confirm the deadline actually fires**

```bash
cd plugin/anti-tangent-guard/hooks && \
ANTI_TANGENT_TICKET_PATTERN='(a+)+$' timeout 20 python3 -c \
  "from comment_scan import violations; print(violations('X.kt', ['// ' + 'a'*40 + '!']))"
```
Expected: prints `[]` within a few seconds. A `timeout` kill (exit 124) means the bound is not working.

- [ ] **Step 7a: Teach the harness a per-case timeout**

The catastrophic-regex case below is the one eval whose failure mode is a HANG, and a hang in a
CI-gated suite is worse than a wrong exit code: it takes the whole release's eval run with it.
`run.sh` has no per-case timeout, so add one. In the case-invocation function, replace the
`bash_path` assignment with:

```bash
    local bash_path timeout_prefix=()
    bash_path=$(command -v bash)

    # Optional "case_timeout_seconds": wrap the hook in `timeout` so a case
    # whose failure mode is a hang fails as a case instead of stalling the
    # suite. `timeout` returns 124 on expiry, which no hook uses, so a
    # timeout is distinguishable from every real exit status below.
    local case_timeout
    case_timeout=$(jq -r ".evals[$idx].case_timeout_seconds // empty" "$EVALS_FILE")
    if [[ -n "$case_timeout" && "$case_timeout" =~ ^[0-9]+$ ]] && command -v timeout >/dev/null 2>&1; then
        timeout_prefix=(timeout "$case_timeout")
    fi
```

Then prefix each of the three hook invocations with `"${timeout_prefix[@]}"`, and after the exit
code is captured add:

```bash
    if [[ "$exit_code" == "124" ]]; then
        echo "  TIMED OUT after ${case_timeout}s — the case hung rather than returning" >&2
        return 1
    fi
```

A case with no `case_timeout_seconds` is unaffected: `timeout_prefix` stays empty and expands to
nothing.

- [ ] **Step 8: Add eval cases**

Append to `guard-evals.json`, continuing the ids:

```json
{
  "id": 107,
  "name": "comment-write-ticket-pattern-set-blocks",
  "hook": "check-comment-write",
  "env": {"ANTI_TANGENT_TICKET_PATTERN": "ABC-\\d+"},
  "stdin_raw": "{\"tool_name\":\"Write\",\"tool_input\":{\"file_path\":\"{{TMPDIR}}/atg-eval.kt\",\"content\":\"/**\\n * ABC-1234: the inbound keyword.\\n */\\nobject X\\n\"}}",
  "expected_exit": 2,
  "expected_stderr_contains": ["a tracker reference"],
  "reason": "a configured project ticket pattern is a tell"
},
{
  "id": 108,
  "name": "comment-write-ticket-pattern-unset-allows",
  "hook": "check-comment-write",
  "stdin_raw": "{\"tool_name\":\"Write\",\"tool_input\":{\"file_path\":\"{{TMPDIR}}/atg-eval.kt\",\"content\":\"/**\\n * ABC-1234: the inbound keyword.\\n */\\nobject X\\n\"}}",
  "expected_exit": 0,
  "reason": "there is no default ticket tell; unconfigured projects are unchanged"
},
{
  "id": 109,
  "name": "comment-write-ticket-pattern-uncompilable-ignored",
  "hook": "check-comment-write",
  "env": {"ANTI_TANGENT_TICKET_PATTERN": "ABC-(\\d+"},
  "stdin_raw": "{\"tool_name\":\"Write\",\"tool_input\":{\"file_path\":\"{{TMPDIR}}/atg-eval.go\",\"content\":\"// fixes task-42\\npackage x\\n\"}}",
  "expected_exit": 2,
  "expected_stderr_contains": ["a task reference"],
  "reason": "an uncompilable pattern is dropped without disabling the other tells"
},
{
  "id": 110,
  "name": "comment-write-ticket-pattern-over-long-ignored",
  "hook": "check-comment-write",
  "env": {"ANTI_TANGENT_TICKET_PATTERN": "ABC-\\d+(?:xxxxxxxxxx){25}"},
  "stdin_raw": "{\"tool_name\":\"Write\",\"tool_input\":{\"file_path\":\"{{TMPDIR}}/atg-eval.go\",\"content\":\"// fixes task-42\\npackage x\\n\"}}",
  "expected_exit": 2,
  "expected_stderr_contains": ["a task reference"],
  "reason": "a pattern past the 200-character cap is ignored without disabling the other tells"
},
{
  "id": 111,
  "name": "comment-write-ticket-pattern-catastrophic-fails-open",
  "hook": "check-comment-write",
  "env": {"ANTI_TANGENT_TICKET_PATTERN": "(a+)+$"},
  "stdin_raw": "{\"tool_name\":\"Write\",\"tool_input\":{\"file_path\":\"{{TMPDIR}}/atg-eval.go\",\"content\":\"// aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa!\\npackage x\\n\"}}",
  "expected_exit": 0,
  "case_timeout_seconds": 20,
  "reason": "the scan deadline bounds a backtracking pattern and fails open rather than hanging the hook"
}
```

`run.sh` already supports a per-case `"env"` object (it builds an `env_assignments` array at line 398 and invokes the hook through `env "${env_assignments[@]}"`), so no harness change is needed here. Note that its invocation is an if/elif chain: a case setting BOTH `env` and `path_stub_exclude` gets only the `path_stub_exclude` branch. None of these three cases uses both.

- [ ] **Step 9: Update the case count**

`EXPECTED_CASE_COUNT=111` in `run.sh:155`, and `(111 cases)` in the JSON `description`.

- [ ] **Step 10: Make the FP gate environment-independent**

In `plugin/anti-tangent-guard/evals/fp-scan.py`, immediately after the imports:

```python
# The false-positive gate must measure the shipped tells, not whatever the
# developer happens to have configured for their own project.
os.environ.pop("ANTI_TANGENT_TICKET_PATTERN", None)
```

In `plugin/anti-tangent-guard/evals/fp-report.sh`, near the top after `set -uo pipefail`:

```bash
unset ANTI_TANGENT_TICKET_PATTERN
```

- [ ] **Step 10a: Test the two safety ACs that no eval can reach**

The 200/201 boundary and the non-main-thread path are properties of the module, not of the hook, so
they belong beside it rather than in the eval table. `unittest` from the standard library, run as
`python3 -m unittest discover -s plugin/anti-tangent-guard/hooks -p '*_test.py'` — the hooks are
stdlib-only and CI installs no Python packages. Bare `assert` functions would not be discovered, so
these are `TestCase` methods.

Create `plugin/anti-tangent-guard/hooks/comment_scan_test.py`:

```python
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
```

- [ ] **Step 10b: Make CI run it**

`evals/run.sh` is the only Python-aware entry point CI already calls, so wire the suite in near the
top, before the eval table runs, and let a failure fail the script:

```bash
echo "== module tests =="
python3 -m unittest discover -s "$HOOK_DIR" -p '*_test.py' -v || exit 1
```

Confirm it actually runs and passes:

```bash
python3 -m unittest discover -s plugin/anti-tangent-guard/hooks -p '*_test.py' -v
```
Expected: 5 tests, OK.

- [ ] **Step 11: Run both suites**

Run: `bash plugin/anti-tangent-guard/evals/run.sh && bash plugin/anti-tangent-guard/evals/fp-report.sh`
Expected: 111 cases pass; FP report unchanged from `main`.

- [ ] **Step 12: Commit**

```bash
git add plugin/anti-tangent-guard/hooks/comment_scan.py \
        plugin/anti-tangent-guard/hooks/comment_scan_test.py \
        plugin/anti-tangent-guard/evals/
git commit -m "feat(guard): optional project ticket pattern with a scan deadline"
```

---

### Task 4: Separate the two close-time kill switches

**Goal:** `ANTI_TANGENT_COMPLETION_GUARD=0` disables the completion gate and nothing else; `ANTI_TANGENT_COMMENT_GUARD=0` disables comment scanning and nothing else.

**Files:**
- Modify: `plugin/anti-tangent-guard/hooks/check-task-complete`
- Modify: `plugin/anti-tangent-guard/evals/guard-evals.json`
- Modify: `plugin/anti-tangent-guard/evals/run.sh:155`

**Acceptance Criteria:**
- [ ] `COMPLETION_GUARD=0`, `COMMENT_GUARD=1`, a close carrying a bad comment → exit 2 on comment hygiene
- [ ] `COMPLETION_GUARD=0`, `COMMENT_GUARD=1`, a close with no validate_completion at all → exit 0
- [ ] `COMPLETION_GUARD=1`, `COMMENT_GUARD=0`, a close with no validate_completion → exit 2
- [ ] Both `0` → exit 0, and the trace file records a `skip` line — asserted on the trace, not inferred from the exit code
- [ ] The comment-block message no longer claims "the completion gate passed"

**Verify:** `bash plugin/anti-tangent-guard/evals/run.sh` → all cases pass

**Steps:**

- [ ] **Step 1: Replace the early exit**

In `plugin/anti-tangent-guard/hooks/check-task-complete`, replace lines 170-174 (the `[[ "${ANTI_TANGENT_COMPLETION_GUARD:-1}" == "0" ]] && …` line, the three-line comment below it, and the `COMMENT_GUARD=` assignment) with:

```bash
# Two switches, two concerns. COMPLETION_GUARD governs whether a close is
# checked for having run validate_completion at all; COMMENT_GUARD governs
# the comment-hygiene scan over the evidence that call submitted. Neither
# implies the other, so the hook exits early only when both are off — a
# variable that names one concern must not silently disable the other.
COMPLETION_GUARD="${ANTI_TANGENT_COMPLETION_GUARD:-1}"
COMMENT_GUARD="${ANTI_TANGENT_COMMENT_GUARD:-1}"
if [[ "$COMPLETION_GUARD" == "0" && "$COMMENT_GUARD" == "0" ]]; then
    trace "?" "skip" "guard=0"
    exit 0
fi
```

- [ ] **Step 2: Hoist the comment check above the completion logic**

Delete the `VIOLATIONS=…` block currently nested inside `if [[ "$CALLED" == "true" || "$BLOCK" == "true" ]]; then` (lines 420-448, from `VIOLATIONS=$(…)` through the `fi` that closes `if [[ "$VIOLATIONS_COUNT" -gt 0 ]]`). Insert this immediately after the `comment_scan_unavailable` trace line (currently line 392) and before `if [[ "$VERDICT" == "fail" ]]; then`:

```bash
# Comment hygiene runs on its own switch, ahead of the completion gate and
# independent of its verdict: a close that skipped validation entirely, and
# one that passed, are both closes whose evidence can carry a bad comment.
if [[ "$COMMENT_GUARD" != "0" ]]; then
    VIOLATIONS=$(echo "$RESULT" | jq -c '.comment_violations // []')
    VIOLATIONS_COUNT=$(echo "$VIOLATIONS" | jq 'length')
    if [[ "$VIOLATIONS_COUNT" -gt 0 ]]; then
        trace "$TASK_ID" "block" "comment-hygiene"
        {
            echo "TASK CLOSED WITH COMMENTS CARRYING CHANGE HISTORY"
            echo
            echo "Task #$TASK_ID was closed, and the evidence its last validate_completion"
            echo "submitted carries added comment(s) referencing change history."
            echo
            # Both fields come from the submitted evidence and are unbounded
            # there; a single very long line would otherwise reach the model as
            # megabytes of hook stderr it is expected to act on.
            echo "$VIOLATIONS" | jq -r '.[0:10][] | "  " + .path[0:200] + ": " + .line[0:200] + "\n    -> contains " + .why'
            echo
            echo "Comments must explain non-trivial behaviour or a non-obvious invariant, and must read"
            echo "correctly to someone who never saw this change. Issue, task and version references belong"
            echo "in the commit message, not the code. Rewrite the comment(s), then:"
            echo
            echo "  1. TaskUpdate taskId=$TASK_ID status=in_progress"
            echo "  2. Fix the flagged comment(s)"
            echo "  3. Call mcp__anti-tangent__validate_completion again with the updated evidence"
            echo "  4. Re-close once the verdict is pass"
            echo
            echo "(Disable: ANTI_TANGENT_COMMENT_GUARD=0. Trace: $TRACE_LOG)"
        } >&2
        exit 2
    fi
fi

if [[ "$COMPLETION_GUARD" == "0" ]]; then
    trace "$TASK_ID" "skip" "completion-guard=0"
    exit 0
fi
```

- [ ] **Step 3: Run the existing suite to catch a broken restructure**

Run: `bash plugin/anti-tangent-guard/evals/run.sh`
Expected: all 111 cases pass — the count this task inherits, before its own four are added. A failure here means the hoist changed an exit path, not that a case is wrong.

- [ ] **Step 4: Add the four-combination eval cases**

Append these to `guard-evals.json`, continuing the ids. Two transcript shapes are used: `DIRTY` (a `validate_completion` whose diff adds a bad comment, verdict pass) and `NONE` (a task window with no `validate_completion` at all).

```json
{
  "id": 112,
  "name": "switch-completion-off-comment-on-still-blocks",
  "env": {"ANTI_TANGENT_COMPLETION_GUARD": "0", "ANTI_TANGENT_COMMENT_GUARD": "1"},
  "input": {
    "tool_name": "TaskUpdate",
    "tool_input": {"status": "completed", "taskId": "1"},
    "transcript_path": "{{TRANSCRIPT}}"
  },
  "transcript_raw_lines": [
    "{\"type\": \"assistant\", \"message\": {\"content\": [{\"type\": \"tool_use\", \"name\": \"TaskUpdate\", \"input\": {\"taskId\": \"1\", \"status\": \"in_progress\"}}]}}",
    "{\"type\": \"assistant\", \"message\": {\"content\": [{\"type\": \"tool_use\", \"id\": \"t1\", \"name\": \"mcp__anti-tangent__validate_completion\", \"input\": {\"final_diff\": \"+++ b/a.go\\n+// fixes #58\\n\"}}]}}",
    "{\"type\": \"user\", \"message\": {\"content\": [{\"type\": \"tool_result\", \"tool_use_id\": \"t1\", \"content\": \"{\\\"tool\\\":\\\"validate_completion\\\",\\\"session_id\\\":\\\"s\\\",\\\"verdict\\\":\\\"pass\\\"}\"}]}}"
  ],
  "expected_exit": 2,
  "expected_stderr_contains": ["COMMENTS CARRYING CHANGE HISTORY"],
  "reason": "COMPLETION_GUARD names the completion gate only; comment scanning survives it"
},
{
  "id": 113,
  "name": "switch-completion-off-no-validation-allows",
  "env": {"ANTI_TANGENT_COMPLETION_GUARD": "0", "ANTI_TANGENT_COMMENT_GUARD": "1"},
  "input": {
    "tool_name": "TaskUpdate",
    "tool_input": {"status": "completed", "taskId": "1"},
    "transcript_path": "{{TRANSCRIPT}}"
  },
  "transcript_raw_lines": [
    "{\"type\": \"assistant\", \"message\": {\"content\": [{\"type\": \"tool_use\", \"name\": \"TaskUpdate\", \"input\": {\"taskId\": \"1\", \"status\": \"in_progress\"}}]}}",
    "{\"type\": \"assistant\", \"message\": {\"content\": [{\"type\": \"tool_use\", \"name\": \"Bash\", \"input\": {\"command\": \"echo hi\"}}]}}"
  ],
  "expected_exit": 0,
  "reason": "with the completion gate off, a close that never validated is allowed"
},
{
  "id": 114,
  "name": "switch-comment-off-no-validation-blocks",
  "env": {"ANTI_TANGENT_COMPLETION_GUARD": "1", "ANTI_TANGENT_COMMENT_GUARD": "0"},
  "input": {
    "tool_name": "TaskUpdate",
    "tool_input": {"status": "completed", "taskId": "1"},
    "transcript_path": "{{TRANSCRIPT}}"
  },
  "transcript_raw_lines": [
    "{\"type\": \"assistant\", \"message\": {\"content\": [{\"type\": \"tool_use\", \"name\": \"TaskUpdate\", \"input\": {\"taskId\": \"1\", \"status\": \"in_progress\"}}]}}",
    "{\"type\": \"assistant\", \"message\": {\"content\": [{\"type\": \"tool_use\", \"name\": \"Bash\", \"input\": {\"command\": \"echo hi\"}}]}}"
  ],
  "expected_exit": 2,
  "expected_stderr_contains": ["WITHOUT anti-tangent validate_completion"],
  "reason": "COMMENT_GUARD names comment scanning only; the completion gate survives it"
},
{
  "id": 115,
  "name": "switch-both-off-allows",
  "env": {
    "ANTI_TANGENT_COMPLETION_GUARD": "0",
    "ANTI_TANGENT_COMMENT_GUARD": "0",
    "ANTI_TANGENT_GUARD_TRACE_LOG": "{{TMPDIR}}/trace.log"
  },
  "expected_file_contains": {"{{TMPDIR}}/trace.log": ["| skip | guard=0"]},
  "input": {
    "tool_name": "TaskUpdate",
    "tool_input": {"status": "completed", "taskId": "1"},
    "transcript_path": "{{TRANSCRIPT}}"
  },
  "transcript_raw_lines": [
    "{\"type\": \"assistant\", \"message\": {\"content\": [{\"type\": \"tool_use\", \"name\": \"TaskUpdate\", \"input\": {\"taskId\": \"1\", \"status\": \"in_progress\"}}]}}",
    "{\"type\": \"assistant\", \"message\": {\"content\": [{\"type\": \"tool_use\", \"id\": \"t1\", \"name\": \"mcp__anti-tangent__validate_completion\", \"input\": {\"final_diff\": \"+++ b/a.go\\n+// fixes #58\\n\"}}]}}",
    "{\"type\": \"user\", \"message\": {\"content\": [{\"type\": \"tool_result\", \"tool_use_id\": \"t1\", \"content\": \"{\\\"tool\\\":\\\"validate_completion\\\",\\\"session_id\\\":\\\"s\\\",\\\"verdict\\\":\\\"pass\\\"}\"}]}}"
  ],
  "expected_exit": 0,
  "reason": "both concerns disabled is the only early exit"
}
```

Case 115 deliberately carries the bad-comment transcript: with both switches off it must still exit 0, which is what proves the early exit is reached rather than the comment block being skipped by accident. The exit code alone cannot show WHICH path produced it, so the case also pins the trace line — `run.sh` already supports `expected_file_contains`, and the case gives itself a private `ANTI_TANGENT_GUARD_TRACE_LOG` so it is not reading another case's log.

- [ ] **Step 5: Update the case count**

`EXPECTED_CASE_COUNT=115` in `run.sh:155`, and `(115 cases)` in the JSON `description`.

- [ ] **Step 6: Run the suite**

Run: `bash plugin/anti-tangent-guard/evals/run.sh`
Expected: 115 cases pass.

- [ ] **Step 7: Commit**

```bash
git add plugin/anti-tangent-guard/hooks/check-task-complete plugin/anti-tangent-guard/evals/
git commit -m "fix(guard): give each close-time kill switch its own concern"
```

---

### Task 5: Scan `final_files` completions using git for the added lines

**Goal:** A lightweight completion that submits full file contents instead of a diff is no longer unscanned, without ever flagging a comment the implementer did not add.

**Files:**
- Modify: `plugin/anti-tangent-guard/hooks/check-task-complete`
- Create: `plugin/anti-tangent-guard/hooks/git_added_lines.py`
- Modify: `plugin/anti-tangent-guard/hooks/comment_scan_test.py`
- Modify: `plugin/anti-tangent-guard/evals/guard-evals.json`
- Modify: `plugin/anti-tangent-guard/evals/run.sh:155`

**Acceptance Criteria:**
- [ ] A `final_files` completion naming an untracked file whose content carries `// fixes #1` blocks the close
- [ ] A `final_files` completion naming a tracked file whose *pre-existing* comment carries `// fixes #1`, with no modification, does NOT block
- [ ] A path inside a nested git worktree resolves against that worktree, not the hook's cwd
- [ ] A gitignored path is skipped
- [ ] A path outside any repository is skipped
- [ ] `final_diff` still wins when both fields are present
- [ ] Git is invoked with diff prefixes pinned, so a user's `diff.mnemonicPrefix` or `diff.noprefix` cannot blind the parser
- [ ] A submitted `final_diff` that parses to ZERO added lines still wins — the fallback is chosen on what was submitted, not on what parsed
- [ ] An explicitly submitted EMPTY `final_diff` (`""`) also wins — precedence keys on the field being present, not on it being truthy, and an eval covers this separately from the non-empty deletion diff
- [ ] A `check-ignore` status other than a definite "not ignored" skips the path rather than scanning it, asserted by a unit test with a stubbed git rather than by a fixture repository
- [ ] The nested-worktree eval expects exit **0** on a fixture that only correct rooting can pass — an untracked fixture expecting exit 2 passes under the buggy rooting too and is not a test
- [ ] Every `run.sh` hook invocation runs from `case_cwd`; `grep -c 'cd \"$HOOK_CWD\"'` returns 0
- [ ] `hook_cwd` is resolved AFTER `setup_script` runs — the directory a case names is one its own setup creates
- [ ] `_git` and `final_files_added_lines` live ONLY in `hooks/git_added_lines.py`; the hook imports them and carries no second copy
- [ ] The commit stages `git_added_lines.py` and `comment_scan_test.py`, not just the hook and the evals
- [ ] The nested-worktree, gitignored, both-fields-present and overridden-diff-prefix cases are each exercised by a HOOK-LEVEL eval, not only by a manual shell check

**Verify:** `bash plugin/anti-tangent-guard/evals/run.sh` → all cases pass

**Steps:**

- [ ] **Step 1: Add `subprocess` to the embedded Python imports**

In `check-task-complete`, line 196:

```python
import json, os, re, subprocess, sys
```

- [ ] **Step 2: Create the git helper as an importable module**

The helpers go in `plugin/anti-tangent-guard/hooks/git_added_lines.py`, NOT inline in the `PY=`
heredoc. Two reasons, and the second is why it is not optional: `check-task-complete` already
imports `comment_scan` this way so the pattern exists, and the `check-ignore` branch below has no
reachable test unless the function can be imported and its `_git` stubbed.

Create the file with:

```python
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
```

- [ ] **Step 2a: Import it from the hook**

In the `PY=` heredoc in `check-task-complete`, alongside the existing `comment_scan` import inside
the same `try:` block that sets `comment_scan_unavailable`:

```python
        from git_added_lines import final_files_added_lines
```

The heredoc already puts `$ATG_ROOT/hooks` on `sys.path` for `comment_scan`, so no new path setup
is needed. An import failure here is the same class of event as a missing `comment_scan` and must
set `comment_scan_unavailable` the same way — a scan that cannot load must never look like a scan
that found nothing.

Remove `import subprocess` from the heredoc's import line if you added it in Step 1: the subprocess
work now lives in the module.

- [ ] **Step 3: Use it when no diff was submitted**

Replace the line `added_by_path = diff_added_lines(in_window_completions[-1])` with:

```python
        last_completion = in_window_completions[-1]
        # Precedence is decided by what was SUBMITTED, never by what parsed.
        # A diff can legitimately yield no added lines -- a pure deletion, or
        # a shape this parser does not recognise -- and falling back on an
        # empty result would scan the working tree for a submission whose
        # author already told us which lines were theirs.
        # Presence, not truthiness. An explicitly submitted empty final_diff
        # is still a statement about which lines are the author's -- reading
        # it as absent would reconstruct from the working tree instead.
        if ("final_diff" in last_completion or
                "final_diff_path" in last_completion):
            added_by_path = diff_added_lines(last_completion)
        else:
            added_by_path = final_files_added_lines(last_completion)
```

- [ ] **Step 4: Verify the worktree behaviour by hand before trusting the evals**

```bash
T=$(mktemp -d) && cd "$T" && git init -q main && cd main
git config user.email t@t && git config user.name t
echo "// fixes #1" > a.go && printf '.worktrees/\n' > .gitignore
git add -A && git commit -qm init
git worktree add -q .worktrees/feat -b feat
echo "// fixes #2" > .worktrees/feat/b.go
WT="$PWD/.worktrees/feat/b.go"
echo "hook cwd (main checkout):"; git ls-files --error-unmatch "$WT" >/dev/null 2>&1; echo "  exit=$? (1 = looks untracked, the trap)"
echo "rooted at the file's dir:"; git -C "$(dirname "$WT")" ls-files --error-unmatch "$WT" >/dev/null 2>&1; echo "  exit=$? (1 = genuinely untracked, correct)"
git -C "$(dirname "$WT")" add b.go
echo "after add, rooted:"; git -C "$(dirname "$WT")" ls-files --error-unmatch "$WT" >/dev/null 2>&1; echo "  exit=$? (0 = tracked, correct)"
```

Expected: the rooted calls distinguish untracked from tracked; the unrooted one cannot.

- [ ] **Step 5: Add a `setup_script` hook to the eval harness**

These cases need a real git repository, which `tmpdir_fixture` cannot build — it only materialises file contents. `run.sh` has no setup hook, so add one. Insert this immediately after the `tmpdir_symlink` block (which ends with `done < <(jq -r ".evals[$idx].tmpdir_symlink | keys[]" "$EVALS_FILE")` followed by `fi`) and before `local stdin_raw`:

```bash
    # Optional "setup_script": a bash snippet run in this case's {{TMPDIR}}
    # before the hook. The fixture hooks above can only place file CONTENT;
    # a case asserting behaviour that depends on git's own view of a path
    # (tracked, untracked, ignored, in a worktree) needs real repository
    # state, which only running git can produce. Failures are fatal to the
    # case rather than silent: a case whose setup did not run would assert
    # against the wrong world and pass for the wrong reason.
    # Optional "hook_cwd": run the hook from this directory instead of
    # HOOK_CWD. The worktree case turns on the hook's cwd differing from the
    # file's own directory, which is the whole point of rooting git at the
    # file; a case that cannot move the cwd cannot express it.

    local setup_script
    setup_script=$(jq -r ".evals[$idx].setup_script // empty" "$EVALS_FILE")
    if [[ -n "$setup_script" ]]; then
        setup_script="${setup_script//\{\{TMPDIR\}\}/$case_tmp}"
        if ! ( cd "$case_tmp" && bash -c "$setup_script" ) >/dev/null 2>&1; then
            echo "  SETUP FAILED for case $idx" >&2
            return 1
        fi
    fi

    # Optional "hook_cwd": run the hook from here instead of HOOK_CWD. The
    # worktree case turns on the hook's cwd differing from the file's own
    # directory, which is the whole point of rooting git at the file.
    #
    # Resolved AFTER setup_script, never before: the directory a case names
    # here is usually one its own setup just created, so validating first
    # would reject every such case before it could exist.
    local hook_cwd_override case_cwd
    hook_cwd_override=$(jq -r ".evals[$idx].hook_cwd // empty" "$EVALS_FILE")
    hook_cwd_override="${hook_cwd_override//\{\{TMPDIR\}\}/$case_tmp}"
    case_cwd="$HOOK_CWD"
    if [[ -n "$hook_cwd_override" ]]; then
        # A named cwd that does not exist must fail loudly. Falling back to
        # HOOK_CWD would silently assert the opposite of the case's intent.
        [[ -d "$hook_cwd_override" ]] || { echo "  BAD hook_cwd: $hook_cwd_override" >&2; return 1; }
        case_cwd="$hook_cwd_override"
    fi
```

- [ ] **Step 5a: Make every invocation branch honour `case_cwd`**

Computing `case_cwd` changes nothing on its own — `run.sh` invokes the hook in three separate
branches (`path_stub_exclude`, env-assignments, and plain), and each one hard-codes
`cd "$HOOK_CWD"`. Change all three to `cd "$case_cwd"`.

This is the step the whole worktree case rests on: without it the override is dead code and the
case silently runs from the main checkout, which is the exact wrong behaviour it exists to catch.
Verify before moving on:

```bash
grep -c 'cd "$HOOK_CWD"' plugin/anti-tangent-guard/evals/run.sh   # expect 0
grep -c 'cd "$case_cwd"' plugin/anti-tangent-guard/evals/run.sh   # expect 3
```

If Task 3's `timeout_prefix` has already landed, each branch also carries `"${timeout_prefix[@]}"`
immediately before `"$bash_path"`; keep both changes on the same three lines.

- [ ] **Step 6: Add the eval cases**

Each path below is absolute via `{{TMPDIR}}`, which `run.sh` substitutes into `transcript_raw_lines` (line 385) as well as into `setup_script`.

```json
{
  "id": 116,
  "name": "final-files-untracked-blocks",
  "setup_script": "mkdir -p r && cd r && git init -q . && git config user.email t@t && git config user.name t && echo x > seed && git add seed && git commit -qm i && printf '// fixes #1\\npackage x\\n' > new.go",
  "input": {
    "tool_name": "TaskUpdate",
    "tool_input": {"status": "completed", "taskId": "1"},
    "transcript_path": "{{TRANSCRIPT}}"
  },
  "transcript_raw_lines": [
    "{\"type\": \"assistant\", \"message\": {\"content\": [{\"type\": \"tool_use\", \"name\": \"TaskUpdate\", \"input\": {\"taskId\": \"1\", \"status\": \"in_progress\"}}]}}",
    "{\"type\": \"assistant\", \"message\": {\"content\": [{\"type\": \"tool_use\", \"id\": \"t1\", \"name\": \"mcp__anti-tangent__validate_completion\", \"input\": {\"final_files\": [{\"path\": \"{{TMPDIR}}/r/new.go\"}]}}]}}",
    "{\"type\": \"user\", \"message\": {\"content\": [{\"type\": \"tool_result\", \"tool_use_id\": \"t1\", \"content\": \"{\\\"tool\\\":\\\"validate_completion\\\",\\\"session_id\\\":\\\"s\\\",\\\"verdict\\\":\\\"pass\\\"}\"}]}}"
  ],
  "expected_exit": 2,
  "expected_stderr_contains": ["COMMENTS CARRYING CHANGE HISTORY"],
  "reason": "an untracked file submitted as final_files has every line added"
},
{
  "id": 117,
  "name": "final-files-tracked-unmodified-allows",
  "setup_script": "mkdir -p r2 && cd r2 && git init -q . && git config user.email t@t && git config user.name t && printf '// fixes #1\\npackage x\\n' > old.go && git add old.go && git commit -qm i",
  "input": {
    "tool_name": "TaskUpdate",
    "tool_input": {"status": "completed", "taskId": "1"},
    "transcript_path": "{{TRANSCRIPT}}"
  },
  "transcript_raw_lines": [
    "{\"type\": \"assistant\", \"message\": {\"content\": [{\"type\": \"tool_use\", \"name\": \"TaskUpdate\", \"input\": {\"taskId\": \"1\", \"status\": \"in_progress\"}}]}}",
    "{\"type\": \"assistant\", \"message\": {\"content\": [{\"type\": \"tool_use\", \"id\": \"t1\", \"name\": \"mcp__anti-tangent__validate_completion\", \"input\": {\"final_files\": [{\"path\": \"{{TMPDIR}}/r2/old.go\"}]}}]}}",
    "{\"type\": \"user\", \"message\": {\"content\": [{\"type\": \"tool_result\", \"tool_use_id\": \"t1\", \"content\": \"{\\\"tool\\\":\\\"validate_completion\\\",\\\"session_id\\\":\\\"s\\\",\\\"verdict\\\":\\\"pass\\\"}\"}]}}"
  ],
  "expected_exit": 0,
  "reason": "a pre-existing comment in an unmodified tracked file is not an added line"
},
{
  "id": 118,
  "name": "final-files-outside-repo-skipped",
  "setup_script": "mkdir -p nr && printf '// fixes #1\\npackage x\\n' > nr/loose.go",
  "input": {
    "tool_name": "TaskUpdate",
    "tool_input": {"status": "completed", "taskId": "1"},
    "transcript_path": "{{TRANSCRIPT}}"
  },
  "transcript_raw_lines": [
    "{\"type\": \"assistant\", \"message\": {\"content\": [{\"type\": \"tool_use\", \"name\": \"TaskUpdate\", \"input\": {\"taskId\": \"1\", \"status\": \"in_progress\"}}]}}",
    "{\"type\": \"assistant\", \"message\": {\"content\": [{\"type\": \"tool_use\", \"id\": \"t1\", \"name\": \"mcp__anti-tangent__validate_completion\", \"input\": {\"final_files\": [{\"path\": \"{{TMPDIR}}/nr/loose.go\"}]}}]}}",
    "{\"type\": \"user\", \"message\": {\"content\": [{\"type\": \"tool_result\", \"tool_use_id\": \"t1\", \"content\": \"{\\\"tool\\\":\\\"validate_completion\\\",\\\"session_id\\\":\\\"s\\\",\\\"verdict\\\":\\\"pass\\\"}\"}]}}"
  ],
  "expected_exit": 0,
  "reason": "a path outside any repository cannot be classified and is skipped"
},
{
  "id": 119,
  "name": "final-files-nested-worktree-resolves-against-the-worktree",
  "setup_script": "mkdir -p w && cd w && git init -q . && git config user.email t@t && git config user.name t && echo x > seed && printf '.wt/\\n' > .gitignore && git add seed .gitignore && git commit -qm i && git worktree add -q .wt/feat -b feat && printf '// fixes #1\\npackage x\\n' > .wt/feat/tracked.go && git -C .wt/feat add tracked.go && git -C .wt/feat commit -qm base && printf '// fixes #1\\npackage x\\n\\nfunc F() {}\\n' > .wt/feat/tracked.go",
  "hook_cwd": "{{TMPDIR}}/w",
  "input": {
    "tool_name": "TaskUpdate",
    "tool_input": {"status": "completed", "taskId": "1"},
    "transcript_path": "{{TRANSCRIPT}}"
  },
  "transcript_raw_lines": [
    "{\"type\": \"assistant\", \"message\": {\"content\": [{\"type\": \"tool_use\", \"name\": \"TaskUpdate\", \"input\": {\"taskId\": \"1\", \"status\": \"in_progress\"}}]}}",
    "{\"type\": \"assistant\", \"message\": {\"content\": [{\"type\": \"tool_use\", \"id\": \"t1\", \"name\": \"mcp__anti-tangent__validate_completion\", \"input\": {\"final_files\": [{\"path\": \"{{TMPDIR}}/w/.wt/feat/tracked.go\"}]}}]}}",
    "{\"type\": \"user\", \"message\": {\"content\": [{\"type\": \"tool_result\", \"tool_use_id\": \"t1\", \"content\": \"{\\\"tool\\\":\\\"validate_completion\\\",\\\"session_id\\\":\\\"s\\\",\\\"verdict\\\":\\\"pass\\\"}\"}]}}"
  ],
  "expected_exit": 0,
  "reason": "THIS FIXTURE DISCRIMINATES, which the obvious one does not. The worktree file is TRACKED and already carries `// fixes #1`; the only line this task adds is clean. Rooted correctly at the file's own directory, git diffs it, sees one harmless added line, and the close is allowed. Rooted at the hook cwd -- the main checkout -- ls-files reports the path unmatched, the hook treats every line as new, and the pre-existing comment blocks. Expecting exit 0 is therefore a test only the correct rooting passes. An untracked fixture expecting exit 2 would pass under BOTH implementations and could never fail."
},
{
  "id": 120,
  "name": "final-files-gitignored-path-skipped",
  "setup_script": "mkdir -p g && cd g && git init -q . && git config user.email t@t && git config user.name t && echo x > seed && printf 'build/\\n' > .gitignore && git add seed .gitignore && git commit -qm i && mkdir -p build && printf '// fixes #1\\npackage x\\n' > build/gen.go",
  "input": {
    "tool_name": "TaskUpdate",
    "tool_input": {"status": "completed", "taskId": "1"},
    "transcript_path": "{{TRANSCRIPT}}"
  },
  "transcript_raw_lines": [
    "{\"type\": \"assistant\", \"message\": {\"content\": [{\"type\": \"tool_use\", \"name\": \"TaskUpdate\", \"input\": {\"taskId\": \"1\", \"status\": \"in_progress\"}}]}}",
    "{\"type\": \"assistant\", \"message\": {\"content\": [{\"type\": \"tool_use\", \"id\": \"t1\", \"name\": \"mcp__anti-tangent__validate_completion\", \"input\": {\"final_files\": [{\"path\": \"{{TMPDIR}}/g/build/gen.go\"}]}}]}}",
    "{\"type\": \"user\", \"message\": {\"content\": [{\"type\": \"tool_result\", \"tool_use_id\": \"t1\", \"content\": \"{\\\"tool\\\":\\\"validate_completion\\\",\\\"session_id\\\":\\\"s\\\",\\\"verdict\\\":\\\"pass\\\"}\"}]}}"
  ],
  "expected_exit": 0,
  "reason": "a gitignored path exits 1 from ls-files like an untracked one; check-ignore is what separates them"
},
{
  "id": 121,
  "name": "final-files-diff-wins-even-when-it-adds-nothing",
  "setup_script": "mkdir -p d && cd d && git init -q . && git config user.email t@t && git config user.name t && echo x > seed && git add seed && git commit -qm i && printf '// fixes #1\\npackage x\\n' > new.go",
  "input": {
    "tool_name": "TaskUpdate",
    "tool_input": {"status": "completed", "taskId": "1"},
    "transcript_path": "{{TRANSCRIPT}}"
  },
  "transcript_raw_lines": [
    "{\"type\": \"assistant\", \"message\": {\"content\": [{\"type\": \"tool_use\", \"name\": \"TaskUpdate\", \"input\": {\"taskId\": \"1\", \"status\": \"in_progress\"}}]}}",
    "{\"type\": \"assistant\", \"message\": {\"content\": [{\"type\": \"tool_use\", \"id\": \"t1\", \"name\": \"mcp__anti-tangent__validate_completion\", \"input\": {\"final_diff\": \"+++ b/seed\\n-gone\\n\", \"final_files\": [{\"path\": \"{{TMPDIR}}/d/new.go\"}]}}]}}",
    "{\"type\": \"user\", \"message\": {\"content\": [{\"type\": \"tool_result\", \"tool_use_id\": \"t1\", \"content\": \"{\\\"tool\\\":\\\"validate_completion\\\",\\\"session_id\\\":\\\"s\\\",\\\"verdict\\\":\\\"pass\\\"}\"}]}}"
  ],
  "expected_exit": 0,
  "reason": "a submitted diff with zero added lines still wins; falling back to final_files here would block on a comment the diff's author never claimed"
},
{
  "id": 122,
  "name": "final-files-honours-overridden-diff-prefixes",
  "setup_script": "mkdir -p m && cd m && git init -q . && git config user.email t@t && git config user.name t && git config diff.mnemonicPrefix true && git config diff.noprefix false && printf 'package x\\n' > mod.go && git add mod.go && git commit -qm i && printf '// fixes #1\\npackage x\\n' > mod.go",
  "input": {
    "tool_name": "TaskUpdate",
    "tool_input": {"status": "completed", "taskId": "1"},
    "transcript_path": "{{TRANSCRIPT}}"
  },
  "transcript_raw_lines": [
    "{\"type\": \"assistant\", \"message\": {\"content\": [{\"type\": \"tool_use\", \"name\": \"TaskUpdate\", \"input\": {\"taskId\": \"1\", \"status\": \"in_progress\"}}]}}",
    "{\"type\": \"assistant\", \"message\": {\"content\": [{\"type\": \"tool_use\", \"id\": \"t1\", \"name\": \"mcp__anti-tangent__validate_completion\", \"input\": {\"final_files\": [{\"path\": \"{{TMPDIR}}/m/mod.go\"}]}}]}}",
    "{\"type\": \"user\", \"message\": {\"content\": [{\"type\": \"tool_result\", \"tool_use_id\": \"t1\", \"content\": \"{\\\"tool\\\":\\\"validate_completion\\\",\\\"session_id\\\":\\\"s\\\",\\\"verdict\\\":\\\"pass\\\"}\"}]}}"
  ],
  "expected_exit": 2,
  "expected_stderr_contains": ["COMMENTS CARRYING CHANGE HISTORY"],
  "reason": "with diff.mnemonicPrefix set the repo emits `+++ w/`; the pinned -c flags are what keep the hunk parser from silently seeing zero files"
}
```

Case 118's `{{TMPDIR}}` is itself inside the harness `WORKDIR`, which is not a git repository, so `ls-files` there exits 128 and the path is skipped. If the harness workdir is ever moved inside a repo this case silently changes meaning — assert the exit code, and if it starts passing for the wrong reason, point the case at `/tmp` explicitly.

- [ ] **Step 6a: Test the check-ignore status the eval table cannot force**

Forcing `git check-ignore` to return neither 0 nor 1 from a fixture repository is not reliably
reproducible, so this acceptance criterion is covered where the branch lives. That requires the
helper to be importable, which is why Step 2 puts it in a module rather than inline — there is one
implementation location and no conditional path.

Append to `plugin/anti-tangent-guard/hooks/comment_scan_test.py` (created in Task 3):

```python
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
```

Add `import tempfile` to the file's imports.

- [ ] **Step 7: Update the case count**

`EXPECTED_CASE_COUNT=122` in `run.sh:155`, and `(122 cases)` in the JSON `description`.

- [ ] **Step 8: Run the suite**

Run: `bash plugin/anti-tangent-guard/evals/run.sh`
Expected: 122 cases pass. A `SETUP FAILED` line means the Step 5 harness hook is wrong, not the case.

- [ ] **Step 9: Commit**

```bash
git add plugin/anti-tangent-guard/hooks/check-task-complete \
        plugin/anti-tangent-guard/hooks/git_added_lines.py \
        plugin/anti-tangent-guard/hooks/comment_scan_test.py \
        plugin/anti-tangent-guard/evals/
git commit -m "feat(guard): scan final_files completions using git for added lines"
```

---

### Task 6: `skip_evidence` on the CodeScene digest

**Goal:** A caller can attach the error text behind a CodeScene skip, bounded like every other free-text field on the digest.

**Files:**
- Modify: `internal/codescene/codescene.go`
- Test: `internal/codescene/codescene_test.go`

**Acceptance Criteria:**
- [ ] `Digest.SkipEvidence` marshals as `skip_evidence` and is omitted when empty
- [ ] `Normalize()` trims it and truncates it to 2,000 runes with a single ellipsis
- [ ] A multi-byte string is not split mid-codepoint
- [ ] `SkipReason`'s existing 300-rune cap is unchanged, asserted by a regression test on both the constant and the truncation, not by inspection

**Verify:** `go test -race ./internal/codescene/...` → PASS

**Steps:**

- [ ] **Step 1: Write the failing test**

Add to `internal/codescene/codescene_test.go`:

```go
func TestNormalizeBoundsSkipEvidence(t *testing.T) {
	d := &Digest{SkipEvidence: "  " + strings.Repeat("é", 2500) + "  "}
	d.Normalize()
	got := []rune(d.SkipEvidence)
	if len(got) != codesceneSkipEvidenceMaxRunes+1 {
		t.Fatalf("got %d runes, want %d (cap plus ellipsis)",
			len(got), codesceneSkipEvidenceMaxRunes+1)
	}
	if got[len(got)-1] != '…' {
		t.Errorf("truncated value does not end in an ellipsis: %q", string(got[len(got)-3:]))
	}
	if got[0] != 'é' {
		t.Errorf("leading whitespace was not trimmed: %q", string(got[0]))
	}
}
```

- [ ] **Step 2: Run it and watch it fail to compile**

Run: `go test ./internal/codescene/... -run TestNormalizeBoundsSkipEvidence`
Expected: FAIL — `d.SkipEvidence undefined` and `codesceneSkipEvidenceMaxRunes` undefined.

- [ ] **Step 3: Add the field**

In the `Digest` struct, immediately after `SkipReason`:

```go
	SkipEvidence   string         `json:"skip_evidence,omitempty"`
```

- [ ] **Step 4: Add the cap and document why it is larger than SkipReason's**

Beside `codesceneSkipReasonMaxRunes`:

```go
// codesceneSkipEvidenceMaxRunes bounds SkipEvidence's length in Normalize.
// It is an order of magnitude above the SkipReason cap because the two hold
// different things: a reason is a sentence a caller composes, while evidence
// is a tool's own error output pasted verbatim, and a useful stack or MCP
// error easily runs past a few hundred runes. Like SkipReason it is outside
// the request-level payload cap and lands in plan-runs.jsonl, so it needs a
// bound of its own.
const codesceneSkipEvidenceMaxRunes = 2000
```

- [ ] **Step 5: Bound it in `Normalize`**

Immediately after the existing `d.SkipReason = truncateRunes(...)` line:

```go
	d.SkipEvidence = truncateRunes(strings.TrimSpace(d.SkipEvidence), codesceneSkipEvidenceMaxRunes)
```

- [ ] **Step 5a: Pin the cap the AC says is unchanged**

The AC promises `SkipReason`'s 300-rune cap survives, and nothing asserts it. Add:

```go
func TestSkipReasonCapUnchanged(t *testing.T) {
	assert.Equal(t, 300, codesceneSkipReasonMaxRunes)

	d := &Digest{SkipReason: strings.Repeat("r", 400)}
	d.Normalize()
	assert.Equal(t, codesceneSkipReasonMaxRunes+1, len([]rune(d.SkipReason)),
		"SkipReason must still truncate at its own cap, not at SkipEvidence's")
}
```

- [ ] **Step 5b: Test the JSON contract the first AC actually states**

The truncation test does not exercise marshalling at all. Add:

```go
func TestSkipEvidenceJSONContract(t *testing.T) {
	b, err := json.Marshal(&Digest{SkipEvidence: "MCP error: tool not found"})
	require.NoError(t, err)
	assert.Contains(t, string(b), `"skip_evidence":"MCP error: tool not found"`)

	b, err = json.Marshal(&Digest{})
	require.NoError(t, err)
	assert.NotContains(t, string(b), "skip_evidence",
		"the field must be omitted when empty, not emitted as an empty string")
}
```

- [ ] **Step 6: Run the tests**

Run: `go test -race ./internal/codescene/...`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/codescene/
git commit -m "feat(codescene): carry skip_evidence on the digest"
```

---

### Task 7: Flatten the CodeScene ladder to one rung

**Goal:** Under `ANTI_TANGENT_CODESCENE=required`, inventing a skip reason stops being cheaper than staying silent, and the resulting finding routes the implementer to attach evidence rather than to rework code.

**Files:**
- Modify: `internal/mcpsrv/submission_defect.go`
- Modify: `internal/planrun/report.go:85-98`
- Test: `internal/mcpsrv/handlers_test.go`
- Test: `internal/planrun/report_test.go`

**Acceptance Criteria:**
- [ ] Mode unset → no adoption finding for any digest shape
- [ ] `required`, no `codescene` argument → major `codescene_not_run`
- [ ] `required`, `ran:false`, empty `skip_reason` → major `codescene_not_run`
- [ ] `required`, `ran:false`, `skip_reason:"lightweight task"`, no evidence → **major** `codescene_skipped`
- [ ] `required`, `ran:false`, any reason plus non-empty `skip_evidence` → minor `codescene_skipped`
- [ ] `required`, `ran:true` → no adoption finding; a regression still yields its own minor
- [ ] `CategoryCodesceneSkipped` is in `submissionDefectCategories`, so an envelope blocked only by the new major reports `SubmissionDefectOnly` and gets the re-submit next_action
- [ ] The minor's suggestion no longer claims the skip is recorded in the plan-run ledger
- [ ] `codesceneCell` renders the evidence alongside the reason
- [ ] A handler-level test asserts the finalized envelope's `SubmissionDefectOnly` and its re-submit `next_action`, not just the helper's return value

**Verify:** `go test -race ./internal/mcpsrv/... ./internal/planrun/...` → PASS

**Steps:**

- [ ] **Step 1: Write the failing ladder test**

Add to `internal/mcpsrv/handlers_test.go`:

```go
func TestCodesceneFindingsLadder(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mode     string
		digest   *codescene.Digest
		severity verdict.Severity
		category verdict.Category
	}{
		{"unset-no-arg", "", nil, "", ""},
		{"unset-bare-skip", "", &codescene.Digest{SkipReason: "whatever"}, "", ""},
		{"required-no-arg", "required", nil,
			verdict.SeverityMajor, verdict.CategoryCodesceneNotRun},
		{"required-no-reason", "required", &codescene.Digest{},
			verdict.SeverityMajor, verdict.CategoryCodesceneNotRun},
		{"required-reason-no-evidence", "required",
			&codescene.Digest{SkipReason: "lightweight task"},
			verdict.SeverityMajor, verdict.CategoryCodesceneSkipped},
		{"required-reason-with-evidence", "required",
			&codescene.Digest{SkipReason: "not configured", SkipEvidence: "MCP error: tool not found"},
			verdict.SeverityMinor, verdict.CategoryCodesceneSkipped},
		{"required-ran", "required", &codescene.Digest{Ran: true},
			"", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := codesceneFindings(tc.mode, tc.digest)
			if tc.severity == "" {
				require.Empty(t, got)
				return
			}
			require.Len(t, got, 1)
			assert.Equal(t, tc.severity, got[0].Severity)
			assert.Equal(t, tc.category, got[0].Category)
		})
	}
}

func TestCodesceneSkippedMajorIsSubmissionDefectOnly(t *testing.T) {
	f := codesceneFindings("required", &codescene.Digest{SkipReason: "lightweight task"})
	require.Len(t, f, 1)
	assert.True(t, isSubmissionDefectOnly(f),
		"a major codescene_skipped must route to re-submit, not to code rework")
}
```

- [ ] **Step 2: Run it and watch two subtests fail**

Run: `go test ./internal/mcpsrv/... -run 'TestCodescene' -v`
Expected: `required-reason-no-evidence` fails (gets `minor`), and `TestCodesceneSkippedMajorIsSubmissionDefectOnly` fails (`codescene_skipped` is not in the map).

- [ ] **Step 3: Add the category to the submission-defect map**

In `internal/mcpsrv/submission_defect.go`, inside `submissionDefectCategories`:

```go
	verdict.CategoryCodesceneSkipped:     true,
```

Extend that map's doc comment with a sentence explaining why a skip belongs there:

```go
// submissionDefectCategories are the findings that describe what the
// implementer sent rather than what they built. Fixing one means attaching
// more evidence, not changing code. A declared CodeScene skip qualifies for
// the same reason a missing one does: the remedy is to run the tool or to
// attach the error text proving it could not run, never to touch the code.
```

- [ ] **Step 4: Replace the ladder**

Replace the `case !d.Ran && strings.TrimSpace(d.SkipReason) == "":` and `case !d.Ran:` arms of `codesceneFindings` with:

```go
		case !d.Ran && strings.TrimSpace(d.SkipReason) == "":
			out = append(out, verdict.Finding{
				Severity:  verdict.SeverityMajor,
				Category:  verdict.CategoryCodesceneNotRun,
				Criterion: "codescene_adoption",
				Evidence:  "`codescene.ran` is false and no `skip_reason` was given.",
				Suggestion: "State why CodeScene was skipped in `skip_reason` and attach the " +
					"failing tool's error text as `skip_evidence`, or run `analyze_change_set` " +
					"and re-submit the result.",
			})
		case !d.Ran && strings.TrimSpace(d.SkipEvidence) == "":
			// An unevidenced skip is graded exactly as an omitted result, and
			// the equality is the point. The server cannot verify any of these
			// fields, so relative severity is the only lever it has: were a
			// bare reason to cost less than silence, composing one would be
			// the cheapest route to a pass and it need not be true. Equal
			// rungs leave nothing to buy by inventing a reason. A lightweight
			// task is not exempt — `required` asserts CodeScene is present on
			// the host.
			out = append(out, verdict.Finding{
				Severity:  verdict.SeverityMajor,
				Category:  verdict.CategoryCodesceneSkipped,
				Criterion: "codescene_adoption",
				Evidence: "CodeScene reported as skipped — " + strings.TrimSpace(d.SkipReason) +
					" — with no `skip_evidence` to substantiate it.",
				Suggestion: "Run CodeScene `analyze_change_set` and re-submit the result, or " +
					"attach the failing tool's own error output as `skip_evidence`. This is a " +
					"submission defect — no code rework is implied.",
			})
		case !d.Ran:
			out = append(out, verdict.Finding{
				Severity:   verdict.SeverityMinor,
				Category:   verdict.CategoryCodesceneSkipped,
				Criterion:  "codescene_adoption",
				Evidence:   "CodeScene deliberately skipped: " + strings.TrimSpace(d.SkipReason),
				Suggestion: "No action needed if the evidence holds.",
			})
```

- [ ] **Step 5: Update the no-argument arm's suggestion**

In the `case d == nil:` arm, replace the `Suggestion` with:

```go
				Suggestion: "Run CodeScene `analyze_change_set` and re-submit with the result as " +
					"the `codescene` argument, or pass {\"ran\": false, \"skip_reason\": \"…\", " +
					"\"skip_evidence\": \"…\"} if the skip is deliberate. This is a submission " +
					"defect — no code rework is implied.",
```

- [ ] **Step 6: Run the ladder tests**

Run: `go test ./internal/mcpsrv/... -run 'TestCodescene' -v`
Expected: PASS

- [ ] **Step 7: Write the failing report-cell test**

Add to `internal/planrun/report_test.go`:

```go
func TestCodesceneCellShowsEvidence(t *testing.T) {
	row := TaskRow{
		CodesceneState: StateSkipped,
		Codescene: &codescene.Digest{
			SkipReason:   "not configured",
			SkipEvidence: "MCP error: tool not found",
		},
	}
	got := codesceneCell(row)
	assert.Contains(t, got, "not configured")
	assert.Contains(t, got, "MCP error: tool not found")
}
```

- [ ] **Step 8: Render the evidence**

In `internal/planrun/report.go`, replace the `case StateSkipped:` arm of `codesceneCell` with:

```go
	case StateSkipped:
		reason := "no reason given"
		if row.Codescene != nil && strings.TrimSpace(row.Codescene.SkipReason) != "" {
			reason = strings.TrimSpace(row.Codescene.SkipReason)
		}
		// The evidence is what distinguishes a skip a reader can check from
		// one they cannot. Omitting it here would leave the ledger showing
		// only the caller's own sentence.
		if row.Codescene != nil && strings.TrimSpace(row.Codescene.SkipEvidence) != "" {
			return "skipped (" + reason + ": " + strings.TrimSpace(row.Codescene.SkipEvidence) + ")"
		}
		return "skipped (" + reason + ")"
```

- [ ] **Step 9: Run both packages**

Run: `go test -race ./internal/mcpsrv/... ./internal/planrun/...`
Expected: PASS. Existing tests asserting the old minor-for-any-reason behaviour will fail — update them to supply `skip_evidence`, since the behaviour change is the point of this task.

- [ ] **Step 9a: Assert the next_action the AC actually promises**

`isSubmissionDefectOnly(f)` on a bare finding does not prove the finalized envelope routes the
implementer to re-submit. Add a handler-level test beside the existing `validate_completion` tests,
reusing whatever request helper they already define:

```go
func TestUnevidencedCodesceneSkipRoutesToResubmit(t *testing.T) {
	// required mode, lightweight call, the skip is the ONLY blocking finding.
	env := callValidateCompletionWithCodescene(t,
		"required",
		&codescene.Digest{SkipReason: "lightweight task"},
	)
	assert.True(t, env.SubmissionDefectOnly)
	assert.Contains(t, env.NextAction, "Re-submit with the missing evidence")
}
```

Match the helper name and signature to the neighbouring tests in `handlers_test.go`; if none takes
a CodeScene mode, set `h.deps.Cfg.Codescene` the way those tests already configure the handler.

- [ ] **Step 10: Commit**

```bash
git add internal/mcpsrv/submission_defect.go internal/mcpsrv/handlers_test.go \
        internal/planrun/report.go internal/planrun/report_test.go
git commit -m "feat(codescene): grade an unevidenced skip as harshly as silence"
```

---

### Task 8: Reject test evidence showing that nothing ran

**Goal:** `validate_completion` stops accepting output which states that no test executed, without penalising a cached run that attests a prior pass.

**Files:**
- Create: `internal/mcpsrv/test_evidence.go`
- Create: `internal/mcpsrv/test_evidence_test.go`
- Modify: `internal/mcpsrv/handlers.go:1688`
- Modify: `internal/prompts/templates/post.tmpl`

**Acceptance Criteria:**
- [ ] `:app:test NO-SOURCE` yields one major `insufficient_evidence` with criterion `test_evidence`
- [ ] pytest's `no tests ran`, jest's `No tests found`, and Go's `?   pkg   [no test files]` each yield the same finding
- [ ] `:app:test UP-TO-DATE`, `:app:test FROM-CACHE` and `ok  pkg  0.01s (cached)` yield **nothing**
- [ ] A Gradle log with several `UP-TO-DATE` compile tasks and one executed test task yields nothing
- [ ] A Gradle log with `:app:processTestResources NO-SOURCE` **and** an executed `> Task :app:test` yields nothing — `NO-SOURCE` is scoped to test tasks, and an unannotated test-task line counts as execution
- [ ] `:app:testClasses NO-SOURCE` alone does NOT fire — it is not a test-running task
- [ ] A no-execution marker paired with a ZERO count (`no tests ran` beside `0 passed`) still fires — an execution marker must carry a positive count
- [ ] A human-written summary ("all 4 tests pass") yields nothing
- [ ] Empty `test_evidence` yields nothing
- [ ] The finding is submission-defect-only, so `next_action` tells the implementer to re-submit
- [ ] `post.tmpl` tells the reviewer not to restate this finding

**Verify:** `go test -race ./internal/mcpsrv/... ./internal/prompts/...` → PASS

**Steps:**

- [ ] **Step 1: Write the failing test, negatives first**

Create `internal/mcpsrv/test_evidence_test.go`:

```go
package mcpsrv

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

func TestTestEvidenceFindingsStaysQuiet(t *testing.T) {
	for name, evidence := range map[string]string{
		"empty":            "",
		"gradle-uptodate":  "> Task :app:test UP-TO-DATE\nBUILD SUCCESSFUL in 1s",
		"gradle-cached":    "> Task :app:test FROM-CACHE\nBUILD SUCCESSFUL in 1s",
		"go-cached":        "ok  \tgithub.com/x/y\t(cached)",
		"gradle-mixed":     "> Task :app:compileKotlin UP-TO-DATE\n> Task :app:test\nBUILD SUCCESSFUL in 12s",
		"gradle-nosource-nontest": "> Task :app:processTestResources NO-SOURCE\n> Task :app:test\nBUILD SUCCESSFUL in 12s",
		"gradle-testclasses":      "> Task :app:testClasses NO-SOURCE\n> Task :app:test\nBUILD SUCCESSFUL in 9s",
		"human-summary":    "all 4 tests pass",
		"go-passing":       "ok  \tgithub.com/x/y\t0.412s",
		"pytest-real":      "collected 4 items\n\n=== 4 passed in 0.10s ===",
	} {
		t.Run(name, func(t *testing.T) {
			assert.Empty(t, testEvidenceFindings(evidence))
		})
	}
}

func TestTestEvidenceFindingsFiresOnNoExecution(t *testing.T) {
	for name, evidence := range map[string]string{
		"gradle-no-source": "> Task :app:test NO-SOURCE\nBUILD SUCCESSFUL in 1s",
		"gradle-variant":   "> Task :app:testDebugUnitTest NO-SOURCE\nBUILD SUCCESSFUL in 1s",
		"pytest":           "collected 0 items\n\n=== no tests ran in 0.01s ===",
		"jest":             "No tests found, exiting with code 1",
		"zero-count":       "collected 0 items\n\n=== no tests ran in 0.01s ===\n0 passed",
		"zero-completed":   "> Task :app:test NO-SOURCE\n0 tests completed",
		"go-no-test-files": "?   \tgithub.com/x/y\t[no test files]",
	} {
		t.Run(name, func(t *testing.T) {
			got := testEvidenceFindings(evidence)
			require.Len(t, got, 1)
			assert.Equal(t, verdict.SeverityMajor, got[0].Severity)
			assert.Equal(t, verdict.CategoryInsufficientEvidence, got[0].Category)
			assert.Equal(t, "test_evidence", got[0].Criterion)
		})
	}
}

func TestTestEvidenceFindingIsSubmissionDefectOnly(t *testing.T) {
	f := testEvidenceFindings("> Task :app:test NO-SOURCE")
	require.Len(t, f, 1)
	assert.True(t, isSubmissionDefectOnly(f))
}
```

- [ ] **Step 2: Run it and watch it fail to compile**

Run: `go test ./internal/mcpsrv/... -run TestTestEvidence`
Expected: FAIL — `testEvidenceFindings` undefined.

- [ ] **Step 3: Write the check**

Create `internal/mcpsrv/test_evidence.go`:

```go
// Package mcpsrv: the deterministic, reviewer-free check that submitted test
// evidence describes a run that actually executed tests.
package mcpsrv

import (
	"regexp"

	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

// noExecutionMarkers match output stating that no test ran and none passed.
//
// The distinction that governs this list is whether the marker attests a
// prior PASS. Gradle marks a task UP-TO-DATE or FROM-CACHE only after a
// successful execution on unchanged inputs — a failing test task re-executes
// — and `go test` caches only passing results. Those markers therefore say
// the tests pass on the current inputs; they carry no counts, but neither
// does a plain successful Gradle run, so treating them as missing evidence
// would penalise the cached run and accept an equally count-free executed
// one. NO-SOURCE and its equivalents below make the opposite statement:
// there was nothing to run.
var noExecutionMarkers = []*regexp.Regexp{
	// NO-SOURCE is scoped to a TEST task. Gradle reports it for any task with
	// no inputs, and a healthy build routinely prints it for
	// processTestResources or a resource task while the test task beside it
	// runs a full suite; matching NO-SOURCE anywhere would reject that build.
	// The pattern requires a task segment ending in "test"/"Test", so
	// :app:test, :shared:jvmTest and :app:testDebugUnitTest match while
	// :app:processTestResources does not.
	regexp.MustCompile(`(?m)^.*:[A-Za-z0-9_.-]*[Tt]est\s+NO-SOURCE\s*$`),
	regexp.MustCompile(`(?i)\bno tests ran\b`),
	regexp.MustCompile(`(?i)\bno tests found\b`),
	regexp.MustCompile(`\[no test files\]`),
}

// executionMarkers match output showing that at least one test did run, and
// suppress the finding when they appear alongside a no-execution marker. A
// multi-module build legitimately reports NO-SOURCE for a module with no
// tests while another module runs its suite, and that submission is fine.
var executionMarkers = []*regexp.Regexp{
	regexp.MustCompile(`(?m)^ok\s+\S+\s+\d`),
	// The count must be POSITIVE. `\d+` also matches zero, so evidence
	// pairing a no-execution marker with "0 tests ran" or "0 passed" would
	// suppress the very finding that evidence calls for.
	regexp.MustCompile(`(?i)\b[1-9]\d* (?:tests?|examples?) (?:passed|ran|completed)\b`),
	regexp.MustCompile(`(?i)\b[1-9]\d* passed\b`),
	// "tests completed" needs a positive count for the same reason the two
	// above do: "0 tests completed" beside a no-execution marker would
	// otherwise suppress the finding that evidence exists to produce.
	regexp.MustCompile(`(?i)\b[1-9]\d* tests? completed\b`),
	// A Gradle test-task line carrying no status at all is an EXECUTED task:
	// Gradle annotates skipped work (UP-TO-DATE, FROM-CACHE, NO-SOURCE,
	// SKIPPED) and leaves a task it actually ran unannotated. Without this, a
	// build whose test task ran and whose resource task was NO-SOURCE has no
	// recognised execution marker at all, because a plain successful Gradle
	// run prints no per-test counts.
	regexp.MustCompile(`(?m)^> Task :[A-Za-z0-9_.:-]*[Tt]est\s*$`),
}

// testEvidenceFindings reports evidence whose own text says no test executed.
// It never inspects a summary the implementer wrote themselves: a marker is a
// verbatim string from a build tool, and prose carrying none draws nothing.
func testEvidenceFindings(evidence string) []verdict.Finding {
	if evidence == "" {
		return nil
	}
	matched := false
	for _, re := range noExecutionMarkers {
		if re.MatchString(evidence) {
			matched = true
			break
		}
	}
	if !matched {
		return nil
	}
	for _, re := range executionMarkers {
		if re.MatchString(evidence) {
			return nil
		}
	}
	return []verdict.Finding{{
		Severity:  verdict.SeverityMajor,
		Category:  verdict.CategoryInsufficientEvidence,
		Criterion: "test_evidence",
		Evidence: "The submitted `test_evidence` states that no test executed — it contains a " +
			"no-source / no-tests-found marker and nothing showing a suite running.",
		Suggestion: "Point the run at a target that has tests and re-submit its output, or attach " +
			"JUnit XML `tests=`/`failures=` counts. This is a submission defect — no code rework " +
			"is implied.",
	}}
}
```

- [ ] **Step 4: Run the tests**

Run: `go test -race ./internal/mcpsrv/... -run TestTestEvidence -v`
Expected: PASS

- [ ] **Step 4a: Assert the routing through the handler, not just the helper**

`isSubmissionDefectOnly(f)` on the bare finding does not prove the wired-up envelope reports it. Add a handler-level test beside the existing `validate_completion` tests, following whatever fixture helper they already use to build a request:

```go
func TestNoExecutionEvidenceRoutesToResubmit(t *testing.T) {
	// A lightweight completion (empty session_id) whose only defect is the
	// test evidence: the envelope must tell the implementer to re-submit,
	// not to rework code.
	env := callValidateCompletion(t, CompletionArgs{
		Summary:      "added the parser",
		FinalFiles:   []CompletionFileArg{{Path: "/tmp/x.go", Content: strPtr("package x\n")}},
		TestEvidence: "> Task :app:test NO-SOURCE\nBUILD SUCCESSFUL in 1s",
	})
	assert.True(t, env.SubmissionDefectOnly)
	assert.Contains(t, env.NextAction, "Re-submit with the missing evidence")
}
```

Match `callValidateCompletion` and `strPtr` to the helpers the neighbouring tests in `handlers_test.go` already define; do not add new ones if equivalents exist.

- [ ] **Step 5: Wire it into the handler**

In `internal/mcpsrv/handlers.go`, replace the `codesceneFindings` call site at line 1688 with:

```go
	if cs := codesceneFindings(h.deps.Cfg.Codescene, args.Codescene); len(cs) > 0 {
		result.Findings = append(cs, result.Findings...)
		result = verdict.FinalizeVerdict(result)
	}
	if te := testEvidenceFindings(args.TestEvidence); len(te) > 0 {
		result.Findings = append(te, result.Findings...)
		result = verdict.FinalizeVerdict(result)
	}
```

- [ ] **Step 6: Suppress the reviewer restating it**

In `internal/prompts/templates/post.tmpl`, in the `{{if .TestEvidence}}` block, after the `{{.TestEvidence}}` line, add:

```
anti-tangent independently flags test evidence whose own text states that no test executed; do not emit a separate finding solely restating that. Cross-check the evidence against the AC list as described above.
```

- [ ] **Step 7: Regenerate the affected goldens**

Run: `go test ./internal/prompts/... -update`
Then: `git diff internal/prompts/testdata/` — read it. Only the `post_*` goldens carrying a test-evidence section should change, and only by the added sentence.

- [ ] **Step 8: Run both packages**

Run: `go test -race ./internal/mcpsrv/... ./internal/prompts/...`
Expected: PASS

- [ ] **Step 9: Commit**

```bash
git add internal/mcpsrv/test_evidence.go internal/mcpsrv/test_evidence_test.go \
        internal/mcpsrv/handlers.go internal/prompts/templates/post.tmpl \
        internal/prompts/testdata/
git commit -m "feat(mcpsrv): reject test evidence stating no test executed"
```

---

### Task 9: Comment rules for plans — fences and policy

**Goal:** `validate_plan` flags a change-history comment inside plan code that will be transcribed verbatim into the repository (G8), and gates on a plan that gives its implementers no comment policy at all (G11).

**Files:**
- Modify: `internal/prompts/templates/plan_rules.tmpl`
- Test: `internal/prompts/prompts_test.go`
- Create: `internal/mcpsrv/plan_comment_policy_e2e_test.go`
- Modify: `internal/prompts/testdata/` — the twelve `plan_*.golden` files, regenerated

**Acceptance Criteria:**
- [ ] `plan_rules.tmpl` carries a comment-hygiene section naming tracker keys, issue/PR references and change narration
- [ ] The section states the fence finding is `minor` with criterion `comment_hygiene`
- [ ] The section exempts diff fences' `-` lines, expected-output fences, and test-fixture fences
- [ ] The section asks for ONE consolidated plan-level finding listing the offending fences, not one per fence
- [ ] The section requires the plan to carry the comment policy OR a pointer to `implementer.md §4.4`, and emits `severity: major`, `category: other`, `criterion: comment_policy_absent` when it carries neither
- [ ] The policy finding uses `category: other` — **not** `convention_deviation`, which `applySeverityFloor` would silently downgrade to `minor`
- [ ] A plan carrying only the one-line pointer draws no policy finding, asserted by an `e2e`-tagged test that issues a real `validate_plan` call — the template test proves only that the instruction renders
- [ ] That same test asserts the emitted finding arrives as `major`/`other`, which is what would catch a floored category
- [ ] The e2e test pins the provider and model its neighbours pin, asserts presence on the no-policy plan and only absence on the pointer plan, and asserts no total finding count
- [ ] A second e2e test covers the FENCE half: three violating fences consolidate into exactly one `minor` `comment_hygiene` finding, and a plan whose only fences are a diff, an expected-output block and a test fixture draws none
- [ ] One canonical pointer line is defined in `plan_rules.tmpl` and reproduced byte-for-byte in both `authoring.md` (Task 10) and the e2e fixture — the three must not drift
- [ ] Stability is measured, not assumed: **both** e2e tests pass five consecutive times before either is considered reliable
- [ ] All twelve `plan_*.golden` files regenerate and the diff contains only the new section
- [ ] Every locally checkable part of the rule — the canonical pointer line, the severities, the consolidation instruction, all three exemptions — is asserted in the template test, which needs no credentials. Only "does a live reviewer act on it" is left to the e2e tier that can be skipped

**Verify:** `go test -race ./internal/prompts/...` → PASS, and `go test -tags=e2e ./internal/mcpsrv/... -run 'TestCommentPolicyFindingE2E|TestCommentHygieneFenceFindingE2E'` → PASS, five consecutive times (needs provider keys; e2e is not run on every PR)

**Why both rules live in one task:** they are two paragraphs of the same `plan_rules.tmpl` section and share one regeneration of the same twelve golden files. Splitting them would mean regenerating twelve goldens twice and reading the same diff twice.

**Steps:**

- [ ] **Step 1: Add the section**

In `internal/prompts/templates/plan_rules.tmpl`, immediately before the final `{{end}}`, add:

```
### Comment hygiene in normative code

Code inside a fence in this plan is transcribed into the repository as written, so a comment in a fence becomes a comment in the codebase. Flag a comment that carries a tracker key (`ABC-1234:`), an issue or pull-request reference, or change narration — "added in v0.5.0", "previously", "no longer", "this replaced". A comment must read correctly to someone who never saw the change that introduced it; a version reference stating a live compatibility contract ("reads the v0.10.0 output") is not a defect.

Three exemptions, and they matter more than the rule: a fence showing a DIFF exempts its `-` lines, because removing such a comment is the fix; a fence showing EXPECTED OUTPUT or a TEST FIXTURE is data, not code that lands in the repository, and a plan whose subject is comment scanning will legitimately contain strings like `// fixes #1` as fixtures. When in doubt about whether a fence is transcribed, do not flag it.

Emit at most ONE finding for the whole plan: `category: quality`, `criterion: comment_hygiene`, `severity: minor`, with `evidence` listing each offending task and comment, and `suggestion` giving the rewrites. One finding per fence would push a plan with three of them to `warn` on finding count alone.

### Comment policy for implementers

A plan is the one artifact every implementing subagent reads. If it says nothing about how comments are to be written, an implementer working without the `anti-tangent-protocol` plugin loaded has no policy at all, writes a comment carrying change history, and spends a review round discovering it.

Check whether the plan gives implementers the comment policy in EITHER form: a statement of it in a constraints or conventions section, or a one-line pointer to the protocol. Either satisfies this; the pointer is preferable because it cannot drift out of sync with the policy it names.

The canonical pointer line is exactly:

    Comments: anti-tangent-protocol implementer.md §4.4

Accept that line, and accept any wording that unambiguously names the same document and section — backticks around the filename, a trailing full stop, or "see" in front are all the same pointer. Do not require the canonical spelling byte-for-byte; do require that a reader could follow it to the section.

When the plan carries neither, emit ONE plan-level finding: `category: other`, `criterion: comment_policy_absent`, `severity: major`, with `suggestion` giving the exact line the author can paste. Do not emit it per task, and do not emit it when the plan carries either form.
```

**`category: other` is not a fallback here — it is required.** `applySeverityFloor` (`internal/verdict/parser_partial.go:19-27`) forces `convention_deviation` and `unverifiable_codebase_claim` to `minor`, and the plan parser applies the floor through `validateFinding`. Emitting the policy finding as `convention_deviation` at `major` would be silently downgraded and the gate would never fire. `other` has no floor, and it is what the existing plan-level major already uses (`task_order_contradiction`, `internal/mcpsrv/file_consistency.go:178-180`).

- [ ] **Step 2: Add a template test asserting the section renders**

Add to `internal/prompts/prompts_test.go`:

```go
func TestPlanRulesCarriesCommentHygiene(t *testing.T) {
	out, err := RenderPlan(PlanInput{PlanText: "# Plan\n\n### Task 1: do thing\n"})
	require.NoError(t, err)
	body := out.System + "\n" + out.User
	assert.Contains(t, body, "Comment hygiene in normative code")
	assert.Contains(t, body, "criterion: comment_hygiene")
	assert.Contains(t, body, "Emit at most ONE finding")
	// The policy gate is a major, and the category it is emitted under is the
	// only one of the plausible choices that survives applySeverityFloor.
	assert.Contains(t, body, "criterion: comment_policy_absent")
	assert.Contains(t, body, "`category: other`")
	assert.NotContains(t, body, "convention_deviation")

	// Everything about this rule that can be checked without a live reviewer
	// IS checked here. The e2e tests below assert that a reviewer acts on
	// these instructions; these assert that the instructions are present and
	// say what they are supposed to say. Only the former needs credentials,
	// so only the former can be skipped -- which is why the wording contract
	// lives at this tier.
	assert.Contains(t, body, "Comments: anti-tangent-protocol implementer.md \u00a74.4",
		"the canonical pointer line must appear verbatim; authoring.md and the e2e fixture copy it")
	assert.Contains(t, body, "Emit at most ONE finding for the whole plan",
		"consolidation is what keeps a three-fence plan off warn")
	assert.Contains(t, body, "severity: minor")
	for _, exemption := range []string{"DIFF", "EXPECTED OUTPUT", "TEST FIXTURE"} {
		assert.Contains(t, body, exemption,
			"each fence exemption must be named in the rule, not implied")
	}
}
```

`RenderPlan(in PlanInput) (Output, error)` is the real signature (`prompts.go:416`); `Output` carries `System` and `User`, and the neighbouring golden tests join them as `out.System+"\n---USER---\n"+out.User`. `plan_rules` is shared by all three plan templates, so this one assertion covers `RenderPlanTasksChunk` and `RenderPlanFindingsOnly` too — which is why all twelve goldens move in Step 4.

- [ ] **Step 3: Run it and watch it fail**

Run: `go test ./internal/prompts/... -run TestPlanRulesCarriesCommentHygiene -v`
Expected: FAIL before Step 1 is applied; PASS after.

- [ ] **Step 3a: Cover the finding's BEHAVIOUR, not only the prompt text**

The template test above proves the instruction is rendered. It cannot prove the reviewer acts on it — that needs a real provider call, which is what the `e2e` tag is for in this repo (`internal/mcpsrv/*_e2e_test.go`, `//go:build e2e`). Add to a new `internal/mcpsrv/plan_comment_policy_e2e_test.go`:

```go
//go:build e2e

package mcpsrv

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The task heading is assembled rather than written literally. A bare
// "### Task 1:" at the start of a line is a heading to anything scanning the
// document this fixture lives in, that document's own plan parser included.
const planBody = "# P\n\n" + "###" + " Task 1: Add a helper\n\n" +
	"**Goal:** add one function.\n\n" +
	"**Acceptance criteria:**\n- [ ] Helper() exists\n\n" +
	"**Steps:**\n\n- [ ] Write it\n"

// The canonical pointer line from plan_rules.tmpl, byte for byte. The
// template also accepts equivalent wordings, but a fixture asserting the
// canonical form is what pins the form the documentation tells authors to
// paste.
const policyPointer = "## Global Constraints\n\n" +
	"Comments: anti-tangent-protocol implementer.md \u00a74.4\n\n"

// A plan naming the policy pointer must draw no policy finding; the same plan
// without it must draw exactly one, at major. The severity is the load-bearing
// assertion: the obvious category for this finding carries a parser-side floor
// that would reduce it to minor and the gate would never fire.
func TestCommentPolicyFindingE2E(t *testing.T) {
	res := runValidatePlanE2E(t, policyPointer+planBody)
	for _, f := range res.PlanFindings {
		assert.NotEqual(t, "comment_policy_absent", f.Criterion,
			"a plan carrying the pointer must draw no policy finding")
	}

	res = runValidatePlanE2E(t, planBody)
	var got []string
	for _, f := range res.PlanFindings {
		if f.Criterion == "comment_policy_absent" {
			got = append(got, string(f.Severity)+"/"+string(f.Category))
		}
	}
	require.Len(t, got, 1, "expected exactly one policy finding, got %v", got)
	assert.Equal(t, "major/other", got[0],
		"a floored category would arrive as minor and never gate")
}

// The fence half of the rule needs its own coverage: a violating normative
// fence must draw exactly ONE consolidated minor however many fences carry a
// comment, and the three exemptions must stay silent. Consolidation is the
// assertion that matters -- one finding per fence would push a three-fence
// plan to warn on count alone, which is the behaviour the template text was
// written to avoid.
func TestCommentHygieneFenceFindingE2E(t *testing.T) {
	fence := func(body string) string {
		return "```" + "go\n" + body + "\n```" + "\n\n"
	}
	violating := policyPointer + "# P\n\n" + "###" + " Task 1: Add helpers\n\n" +
		"**Goal:** add three helpers.\n\n**Acceptance criteria:**\n- [ ] they exist\n\n" +
		"**Steps:**\n\n" +
		fence("// fixes task-42\nfunc A() {}") +
		fence("// see issue #7\nfunc B() {}") +
		fence("// added in v0.5.0\nfunc C() {}")

	res := runValidatePlanE2E(t, violating)
	var hygiene int
	for _, f := range res.PlanFindings {
		if f.Criterion == "comment_hygiene" {
			hygiene++
			assert.Equal(t, "minor", string(f.Severity))
		}
	}
	assert.Equal(t, 1, hygiene,
		"three violating fences must consolidate into ONE finding, not three")

	// The exemptions: a diff fence REMOVING a bad comment, an expected-output
	// fence, and a test-fixture fence. None is transcribed into the codebase.
	exempt := policyPointer + "# P\n\n" + "###" + " Task 1: Clean up\n\n" +
		"**Goal:** remove a bad comment and add a fixture.\n\n" +
		"**Acceptance criteria:**\n- [ ] it is gone\n\n**Steps:**\n\n" +
		"```" + "diff\n-// fixes task-42\n+// explains the invariant\n```" + "\n\n" +
		"Expected output:\n\n```" + "text\n// fixes task-42\n```" + "\n\n" +
		"Test fixture:\n\n```" + "json\n{\"content\": \"// fixes task-42\"}\n```" + "\n\n"

	res = runValidatePlanE2E(t, exempt)
	for _, f := range res.PlanFindings {
		assert.NotEqual(t, "comment_hygiene", f.Criterion,
			"diff, expected-output and fixture fences are exempt: %s", f.Evidence)
	}
}
```

Match `runValidatePlanE2E` to whatever helper `plan_caching_e2e_test.go` already uses to issue a `validate_plan` call; do not add a second harness if one exists. If no such helper exists, build the request the same way that file does and extract the plan findings from the returned envelope.

**Reproducibility, because this asserts on a live model's output.** Pin the same provider and model
the neighbouring e2e tests pin — read it from their setup rather than choosing one here, so the
whole e2e tier moves together. Two assertions are deliberately asymmetric: the *presence* of one
`major`/`other` `comment_policy_absent` finding on a plan carrying no policy is the contract, while
the pointer-carrying plan asserts only the ABSENCE of that criterion and says nothing about what
else the reviewer emits. Do not assert a total finding count on either — that is reviewer-dependent
and would make the test flake on unrelated prose.

**Stability has a number, not a feeling.** Run it five times back to back before calling it stable;
five for five is the bar. Anything less means the `plan_rules` wording is too weak to gate on —
tighten the template text in Step 1 and re-measure rather than loosening the assertion. Record the
observed pass count in the commit message so a later flake can be compared against a baseline
instead of argued about.

```bash
for i in 1 2 3 4 5; do
  go test -tags=e2e ./internal/mcpsrv/... -count=1 \
    -run 'TestCommentPolicyFindingE2E|TestCommentHygieneFenceFindingE2E' \
    || echo "RUN $i FAILED"
done
```

- [ ] **Step 4: Regenerate the goldens**

Run: `go test ./internal/prompts/... -update`

- [ ] **Step 5: Read the golden diff**

Run: `git diff --stat internal/prompts/testdata/`
Expected: exactly the twelve `plan_*.golden` files change. Then `git diff internal/prompts/testdata/plan_basic.golden` and confirm the only change is the new section. A change in a `post_*` or `pre_*` golden here means something else was edited by accident.

- [ ] **Step 6: Run the package**

Run: `go test -race ./internal/prompts/...`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/prompts/templates/plan_rules.tmpl internal/prompts/prompts_test.go \
        internal/prompts/testdata/ internal/mcpsrv/plan_comment_policy_e2e_test.go
git commit -m "feat(prompts): flag change-history comments in normative plan fences"
```

---

### Task 10: Documentation the behaviour changes invalidate

**Goal:** Every document describing the old CodeScene ladder, the old kill-switch semantics, or the missing round-N caveat is corrected, and the protocol bundle stays byte-identical within its size cap.

**Files:**
- Modify: `docs/protocol/core.md:133-137`
- Modify: `docs/protocol/implementer.md:157`
- Modify: `docs/protocol/controller.md`
- Modify: `docs/protocol/authoring.md`
- Modify: `README.md:329`
- Modify: `CLAUDE.md`
- Modify: `plugin/anti-tangent-guard/README.md`
- Modify: `plugin/anti-tangent-protocol/protocol/` — resynced wholesale, never hand-edited

**Acceptance Criteria:**
- [ ] `core.md` describes the new ladder and is **at or under 15,891 bytes**
- [ ] `implementer.md` no longer instructs `{"ran": false, "skip_reason": "lightweight task"}` and is **at or under 15,593 bytes**
- [ ] `implementer.md` tells lightweight tasks to **run** `analyze_change_set` under `required` mode — skipping the companion calls is guidance for the unset mode only. Anything else walks implementers straight into a major, since an unevidenced skip is now graded exactly as silence
- [ ] `controller.md` carries the round-N caveat and stays under 16,000 bytes
- [ ] `README.md:329` describes the new ladder including `skip_evidence`
- [ ] `CLAUDE.md`'s "silences the whole close-time hook" sentence matches the new switch behaviour
- [ ] The guard README documents `ANTI_TANGENT_TICKET_PATTERN` and states that without it the tracker-key shape is not detected
- [ ] The guard README's claim that the hook exits "before it reads stdin" is corrected — with only one switch set it now reads stdin and runs the other half
- [ ] `docs/protocol/authoring.md` tells plan authors that a plan must carry the comment policy or a pointer to `implementer.md` §4.4, and that `validate_plan` now emits a plan-level major when it carries neither
- [ ] `diff -r docs/protocol plugin/anti-tangent-protocol/protocol` is empty

**Verify:** `bash scripts/check-protocol-docs.sh && for f in docs/protocol/*.md; do echo "$f $(wc -c < $f)"; done` → all under 16000, script passes

**Steps:**

- [ ] **Step 1: Record the starting byte counts**

```bash
for f in docs/protocol/*.md; do echo "$f $(wc -c < "$f")"; done
```
Expected: `core.md 15891`, `implementer.md 15593`, `controller.md 14436`. These are the ceilings for Steps 2-4.

- [ ] **Step 2: Rewrite `core.md`'s ladder**

Replace the four-case list under `**A validate_completion call returned category: codescene_not_run or category: codescene_skipped.**` (currently lines 133-137) so the `skip_reason` case reads:

```markdown
- `codescene: {"ran": false, "skip_reason": "…"}` with no `skip_evidence` → `codescene_skipped`, `severity: major` — graded as silence is, so a reason costs nothing to invent and buys nothing.
- `codescene: {"ran": false, "skip_reason": "…", "skip_evidence": "<the tool's own error text>"}` → `codescene_skipped`, `severity: minor`.
```

Delete the phrase "recorded in the plan-run ledger; does not block" — a lightweight call writes no ledger row, and two majors do block.

- [ ] **Step 3: Rewrite `implementer.md:157`**

Replace the whole `**Lightweight mode and ANTI_TANGENT_CODESCENE=required.**` paragraph with:

```markdown
**Lightweight mode and `ANTI_TANGENT_CODESCENE=required`.** What lightweight mode changes depends on the CodeScene mode.

With `ANTI_TANGENT_CODESCENE` unset, lightweight tasks skip the CodeScene MCP companion calls (`pre_commit_code_health_safeguard` / `analyze_change_set`) — there is nothing meaningful for static analysis on a trivial doc edit — and the `codescene` argument is optional.

Under `ANTI_TANGENT_CODESCENE=required` they do not. The mode is an operator assertion that CodeScene is present on this host, so **run `analyze_change_set` and submit its result, on a lightweight task exactly as on any other**. Being lightweight is not a reason to skip it. `{"ran": false, "skip_reason": "…", "skip_evidence": "<the failing tool's own error text>"}` is for a run that was attempted and failed, not for one that was never attempted; a skip carrying no `skip_evidence` draws a major, exactly as omitting the argument does.
```

- [ ] **Step 3a: Check the two ceilings immediately**

```bash
for f in docs/protocol/core.md docs/protocol/implementer.md; do echo "$f $(wc -c < "$f")"; done
```
Expected: `core.md` ≤ 15891 and `implementer.md` ≤ 15593. If either grew, tighten the replacement prose — do NOT trim unrelated sections to make room.

- [ ] **Step 4: Add the controller caveat**

In `docs/protocol/controller.md`, in the section covering `validate_plan` rounds, add:

```markdown
A `pass` on round N is not an audit of rounds 1..N-1. The reviewer re-reads the whole plan each round, but a defect present since round 1 can first surface in round 4 — earlier rounds finding other things is not evidence they inspected everything. Treat each round's findings as additive, and do not read a late-arriving finding as a regression you introduced.
```

- [ ] **Step 5: Rewrite `README.md:329`**

In the sentence describing the ladder, replace `` `{"ran": false, "skip_reason": "…"}` emits a `minor codescene_skipped` finding instead `` with:

```markdown
`{"ran": false, "skip_reason": "…"}` with no `skip_evidence` emits a `major codescene_skipped`, graded exactly as silence is; adding `skip_evidence` with the failing tool's own error text lowers it to `minor`
```

Add `skip_evidence` to the field list earlier in the same sentence.

- [ ] **Step 6: Correct `CLAUDE.md`**

In the "What This Repo Is Not" bullet, replace `ANTI_TANGENT_COMPLETION_GUARD=0` silences the whole close-time hook, and `ANTI_TANGENT_COMMENT_GUARD=0` turns off comment scanning at both write time and close time while leaving the completion gate running` with:

```markdown
`ANTI_TANGENT_COMPLETION_GUARD=0` turns off the completion gate only, and `ANTI_TANGENT_COMMENT_GUARD=0` turns off comment scanning at both write time and close time; each governs its own concern, and setting both is what silences the close-time hook entirely
```

- [ ] **Step 7: Correct the guard README's kill-switch section**

`plugin/anti-tangent-guard/README.md:334-340` describes the old coupling and the old short-circuit. Replace the two bullets under `## Kill switches` with:

```markdown
- `ANTI_TANGENT_COMPLETION_GUARD=0` disables the completion-gate check in the
  `PostToolUse` hook — whether a close ran `validate_completion` at all. The
  close-time comment-hygiene scan is a separate concern and keeps running.
- `ANTI_TANGENT_COMMENT_GUARD=0` disables the comment-hygiene scan, both
  write-time and close-time, while leaving the completion-gate check active.
- Setting both to `0` is what short-circuits the `PostToolUse` hook to
  `exit 0` before it reads stdin. With only one set, the hook reads stdin and
  runs the half that is still enabled.
```

- [ ] **Step 8: Document the ticket pattern in the guard README**

In `plugin/anti-tangent-guard/README.md`'s configuration section, add:

```markdown
### `ANTI_TANGENT_TICKET_PATTERN`

A regex for your tracker's key shape, e.g. `ABC-\d+`. Set it per project in `.claude/settings.json`:

```json
{ "env": { "ANTI_TANGENT_TICKET_PATTERN": "ABC-\\d+" } }
```

**There is no default, and without it a comment like `// ABC-1234: the keyword` is not detected.** A generic pattern cannot be made safe: measured over real comment lines, `[A-Z]+-\d+` matches hardware identifiers (`HDMI-0`, `DP-0`) and prose labels (`ROUND-1`) far more often than tracker keys, and this hook blocks writes. An uncompilable or over-long pattern is ignored, and the whole scan runs under a two-second deadline that fails open.
```

- [ ] **Step 8a: Tell plan authors about the new requirement**

In `docs/protocol/authoring.md`, in the section on what a plan must contain, add:

```markdown
**State the comment policy, or point at it.** A plan is the one artifact every implementing
subagent reads, and an implementer working without this plugin loaded has no comment policy
otherwise. Either restate the policy in the plan's constraints section, or carry the canonical pointer line,
byte for byte:

```
Comments: anti-tangent-protocol implementer.md §4.4
```

`validate_plan` emits a plan-level `major` (`criterion: comment_policy_absent`) when a plan carries
neither. Equivalent wordings are accepted, but this is the line to paste.
```

`authoring.md` is 8,459 bytes with 7,541 of headroom, so this is unconstrained — but it is still a protocol part and resyncs with the rest in the next step.

- [ ] **Step 9: Resync the protocol bundle**

```bash
rm -f plugin/anti-tangent-protocol/protocol/*.md
cp docs/protocol/*.md plugin/anti-tangent-protocol/protocol/
```

- [ ] **Step 10: Verify size and identity**

```bash
for f in docs/protocol/*.md; do echo "$f $(wc -c < "$f")"; done
diff -r docs/protocol plugin/anti-tangent-protocol/protocol && echo "bundle identical"
bash scripts/check-protocol-docs.sh
```
Expected: every part strictly under 16000, bundle identical, script passes.

- [ ] **Step 11: Commit**

```bash
git add docs/protocol/ plugin/anti-tangent-protocol/protocol/ README.md CLAUDE.md \
        plugin/anti-tangent-guard/README.md
git commit -m "docs: correct the ladder, the kill switches, and the plan-round caveat"
```

---

### Task 11: Versions and changelog

**Goal:** The release metadata matches what shipped, and CI's branch-name-to-changelog check passes.

**Files:**
- Modify: `CHANGELOG.md`
- Modify: `plugin/anti-tangent-guard/.claude-plugin/plugin.json`
- Modify: `plugin/anti-tangent-protocol/.claude-plugin/plugin.json`
- Modify: `.claude-plugin/marketplace.json`

**Acceptance Criteria:**
- [ ] `CHANGELOG.md`'s `## [0.20.0] - 2026-09-10` block has populated `### Added`, `### Changed` and `### Fixed` sections
- [ ] The two behaviour changes that fail verdicts passing today are under **Changed**, not Fixed, and name the stacking interaction
- [ ] The entry states that without `ANTI_TANGENT_TICKET_PATTERN` the reported tracker-key shape is still not detected
- [ ] The entry records that plans lacking a comment policy now draw a plan-level major
- [ ] `anti-tangent-guard` is `0.3.0` in both its `plugin.json` and `marketplace.json`
- [ ] `anti-tangent-protocol` is `0.2.2` in both
- [ ] `VERSION` is untouched
- [ ] Both `e2e`-tagged reviewer tests run five times when provider keys are present, and the pass count is written to a committed result file — `go test -race ./...` does not build them
- [ ] When keys are absent the same file records SKIPPED with the reason, so the gap is durable rather than a line that scrolled past
- [ ] The branch check distinguishes three outcomes — entry present, entry missing, not a version branch — because CI skips it in the third case
- [ ] The branch-name-to-changelog check the goal names is actually executed, not assumed

**Verify:** `git diff --stat VERSION` → empty; `jq -r '.plugins[] | "\(.name) \(.version)"' .claude-plugin/marketplace.json` matches each `plugin.json`; and the branch-name-to-changelog check in Step 4a reports the entry present for 0.20.0

**Steps:**

- [ ] **Step 1: Fill in the changelog body**

Replace the empty `### Added` / `### Changed` / `### Fixed` headings under `## [0.20.0] - 2026-09-10` with:

```markdown
### Added
- **`ANTI_TANGENT_TICKET_PATTERN`** (`anti-tangent-guard`) — an optional per-project regex for your tracker's key shape, so `// ABC-1234: …` is caught at write time. There is deliberately no default: a generic pattern matches hardware identifiers and prose labels far more often than tracker keys, and this hook blocks writes. **Without it set, the tracker-key shape reported in #71 is still not detected.**
- **`skip_evidence` on the `codescene` argument** — the failing tool's own error text, which is what now separates a checkable CodeScene skip from an unverifiable one.
- The write-time scanner reads **three comment shapes it previously could not**: a starred block continuation (a KDoc or Javadoc body line), a `/* … */` opened and closed on one line, and a comment after code on the same line. Implemented as an opener extension plus a quote-parity test that declines any delimiter whose preceding quotes are unbalanced. An *unstarred* block interior is a known miss — a line-at-a-time scan cannot tell that such a line sits inside a comment — and is covered by the reviewer's own comment rule whenever a diff is present. The parity test is conservative for the single-line quoting shapes it models, not a general lexer.
- The close-time hook scans completions that submitted **`final_files` instead of a diff**, asking git which lines are new. Rooted at each file's own directory, so a nested worktree resolves correctly.

### Changed
- **`ANTI_TANGENT_CODESCENE=required` now grades a skip with no `skip_evidence` as a `major`**, exactly as it grades saying nothing. Previously any non-empty `skip_reason` bought a `minor`, which made composing a sentence the cheapest route to a pass. `required` is an operator assertion that CodeScene is present on the host, and a task being lightweight is not a skip reason. Operators who have not set the variable see no change.
- **`validate_completion` emits a `major` when `test_evidence` states that no test executed** — `NO-SOURCE`, pytest's "no tests ran", jest's "No tests found", Go's `[no test files]`. Cached and up-to-date output does **not** fire: Gradle marks a task `UP-TO-DATE`/`FROM-CACHE` only after a successful prior run and Go caches only passing results, so those attest a pass.
- Those two findings **stack**. One major alone yields `warn`, and the close-time hook blocks only on `fail` — but a lightweight task under `required` that skips CodeScene without evidence *and* pastes a no-execution test run now produces two majors, a `fail`, and a blocked close where it previously closed clean.
- **`ANTI_TANGENT_COMPLETION_GUARD=0` no longer disables comment scanning.** Each switch now governs the concern it names; setting both is what silences the close-time hook entirely.

### Fixed
- `validate_plan` no longer reports an existing file as missing when a `Files:` bullet anchors more than two line numbers (`docs/x.md:60,166,174,419`). The anchor pattern permitted only one separator per group, so it matched the first pair and then failed the whole anchor, leaving the digits attached to the path. `a.kt:27-30,40-50` was broken the same way.
- `validate_plan` flags change-history comments inside normative code fences, which is where the transcribed-into-the-repository ones come from.
- `validate_plan` emits a plan-level `major` (`criterion: comment_policy_absent`) when a plan gives its implementers neither the comment policy nor a pointer to `implementer.md` §4.4. Without it, an implementer working without the protocol plugin loaded is told nothing and spends a review round discovering the policy. One line satisfies it, and the finding's suggestion gives that line.
```

- [ ] **Step 2: Bump the plugin versions**

```bash
cd /home/pgilmore/Development/Patiently/anti-tangent-mcp
jq '.version = "0.3.0"' plugin/anti-tangent-guard/.claude-plugin/plugin.json > /tmp/g.json && mv /tmp/g.json plugin/anti-tangent-guard/.claude-plugin/plugin.json
jq '.version = "0.2.2"' plugin/anti-tangent-protocol/.claude-plugin/plugin.json > /tmp/p.json && mv /tmp/p.json plugin/anti-tangent-protocol/.claude-plugin/plugin.json
jq '(.plugins[] | select(.name == "anti-tangent-guard") | .version) = "0.3.0"
  | (.plugins[] | select(.name == "anti-tangent-protocol") | .version) = "0.2.2"' \
  .claude-plugin/marketplace.json > /tmp/m.json && mv /tmp/m.json .claude-plugin/marketplace.json
```

- [ ] **Step 3: Confirm the two sources agree**

```bash
jq -r '.plugins[] | "\(.name) \(.version)"' .claude-plugin/marketplace.json
for f in plugin/*/.claude-plugin/plugin.json; do echo "$f $(jq -r .version "$f")"; done
git diff --stat VERSION
```
Expected: guard `0.3.0` and protocol `0.2.2` in both listings; the `VERSION` diff is empty.

- [ ] **Step 4: Run everything, and say what "everything" excludes**

```bash
go test -race ./... && \
bash plugin/anti-tangent-guard/evals/run.sh && \
bash plugin/anti-tangent-guard/evals/fp-report.sh && \
bash plugin/anti-tangent-shunt/evals/run.sh && \
bash scripts/check-protocol-docs.sh
```
Expected: all pass.

`go test -race ./...` does NOT build the `e2e`-tagged tests, so the two Task 9 tests — the only
behavioural coverage G11 and G8's fence rule have — are excluded from that line. Run them
separately when provider credentials are present, and record the outcome either way:

```bash
NOTE=docs/superpowers/plans/2026-09-10-anti-tangent-v0.20.0.e2e-result.md
if [ -n "${ANTHROPIC_API_KEY:-}${OPENAI_API_KEY:-}${GOOGLE_API_KEY:-}" ]; then
  PASSES=0
  for i in 1 2 3 4 5; do
    if go test -tags=e2e ./internal/mcpsrv/... -count=1 \
         -run 'TestCommentPolicyFindingE2E|TestCommentHygieneFenceFindingE2E'; then
      PASSES=$((PASSES + 1))
    fi
  done
  printf '# v0.20.0 reviewer e2e result\n\nRan: %s\nModel: %s\nPasses: %d/5\n' \
    "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "${ANTI_TANGENT_MODEL:-<default>}" "$PASSES" > "$NOTE"
  [ "$PASSES" -eq 5 ] || echo "NOT STABLE: $PASSES/5 — tighten plan_rules.tmpl before merging"
else
  printf '# v0.20.0 reviewer e2e result\n\nRan: %s\nSKIPPED: no provider key set.\nG11 and the fence rule ship without behavioural verification.\n' \
    "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "$NOTE"
  cat "$NOTE"
fi
git add "$NOTE"
```

Five runs, not one, because Task 9 sets five consecutive passes as the reliability bar and this is
the only place both tests are run together. The result file is committed with the release: a skip
is a legitimate outcome, but "all tests pass" is a false claim if the only behavioural test of a
new gate never ran, and a durable record is what stops that claim being made by accident later.

- [ ] **Step 4a: Confirm the CI check the goal names**

The goal says CI's branch-name-to-changelog check passes; the Verify line only compares versions.
That check lives in `.github/workflows/ci.yml` ("Verify CHANGELOG.md entry for branch version") and
is reproducible locally:

```bash
BRANCH=$(git rev-parse --abbrev-ref HEAD)
if [[ ! "$BRANCH" =~ ^version/([0-9]+\.[0-9]+\.[0-9]+)$ ]]; then
  echo "NOT A VERSION BRANCH ($BRANCH) — CI skips this check, and so does this release"
  exit 1
fi
VERSION="${BASH_REMATCH[1]}"
if grep -q "^## \[$VERSION\]" CHANGELOG.md; then
  echo "changelog entry present for $VERSION"
else
  echo "MISSING changelog entry for $VERSION"
  exit 1
fi
```
Expected: `changelog entry present for 0.20.0`.

The three outcomes are kept distinct on purpose. CI *skips* this check on a non-version branch, so
a bare `|| echo "MISSING"` would report a missing entry for a branch the check never applied to —
and this release must be cut from `version/0.20.0` regardless, so a non-version branch is a
precondition failure rather than a pass.

- [ ] **Step 5: Commit**

```bash
git add CHANGELOG.md \
        plugin/anti-tangent-guard/.claude-plugin/plugin.json \
        plugin/anti-tangent-protocol/.claude-plugin/plugin.json \
        .claude-plugin/marketplace.json
git commit -m "chore: changelog and plugin versions for v0.20.0"
```

---

## Notes for the executor

**Two things in this plan will trip the tooling it is changing.**

1. Tasks 2, 3, 5 and 9 contain strings like `// fixes #1`, `* Fixes #123` and `// fixes task-42` inside eval fixtures and prompt examples. These are **data, not comments**. The write-time guard scans added *comment* lines in source files, and a JSON string in `guard-evals.json` is neither — but if a hook does fire while you are editing, the correct response is to confirm the string is a fixture and proceed, not to weaken it.

2. Running `validate_plan` on this plan with an `anti-tangent-mcp` older than 0.20.0 will report a `task_order_contradiction` for any `Files:` bullet anchoring more than two line numbers — that bug is Task 1. This plan's bullets deliberately avoid that shape.
