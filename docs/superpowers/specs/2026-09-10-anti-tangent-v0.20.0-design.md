# anti-tangent-mcp v0.20.0 — closing the comment-hygiene and completion-gate gaps

**Status:** design
**Date:** 2026-09-10 (**revised same day** after an adversarial review — see [Revision](#revision-what-the-review-changed))
**Issue:** [#71](https://github.com/patiently/anti-tangent-mcp/issues/71) — comment-hygiene and completion-gate gaps observed in a real plan run

## Summary

A field report on v0.19.0 recorded a KDoc comment carrying a tracker key (`* ABC-1234: …`)
reaching disk, passing the write-time guard, passing `validate_completion`, and closing its task
clean. Three independent layers were in play and none of them saw it. The report enumerates ten
findings, G1–G10; all ten were reproduced against the code before this design was written.

The incident has one shape: **every layer that could have caught the comment was blind for a
different structural reason, and the layer that graded the close was calibrated so that inventing
an excuse cost less than staying silent.**

- The write-time scanner reads only line-leading `//`, so the entire KDoc block was invisible —
  even ` * Fixes #123`, the unambiguous GitHub syntax, returns no violation.
- The close-time scan reads `final_diff` only, and the reviewer's own comment rule is explicitly
  disabled without a diff. A lightweight completion submitting `final_files` is unscanned by both.
- The CodeScene ladder charges a `major` for silence and a `minor` for any non-empty
  `skip_reason`, so the cheapest route to a pass is a sentence that need not be true. The
  submitted reason — "CodeScene MCP not configured in this project" — was false.
- Test output showing no execution was accepted as evidence of a passing run.

This release closes all ten, plus an eleventh (**G11**) found while preparing to dispatch this
release's own execution: nothing requires a plan to tell its implementers the comment policy, so
an implementer without the protocol plugin loaded is told nothing and pays a round discovering it.
See [5c](#5c-g11--a-plan-must-tell-implementers-the-comment-policy).

**What it does not do, stated up front:** on a project that has not set
`ANTI_TANGENT_TICKET_PATTERN`, this release does **not** catch the reported incident. See
[Compatibility](#compatibility). That is a deliberate choice, made because the measured cost of a
default tell is a blocked write on legitimate comments; it is recorded here rather than left for a
reader to discover.

## Revision: what the review changed

The first draft was reviewed adversarially against the code. Six defects came back — three false
claims, two designs replaced outright, and one material omission. Recording them because each was
load-bearing, and because two of them invalidated decisions already taken:

1. **"G2 and G4 would each independently have caught this incident" was false.** ` * ABC-1234: …`
   matches no existing tell. Making the line visible does not make it match. Only G1, with a
   configured pattern, catches it. The summary above now says so.
2. **"`post.tmpl`'s reviewer rule catches it out of the box" was false.** No part of this design
   touches `post.tmpl`'s "ONLY when a diff is present" clause, and the server has no git with
   which to derive added lines. `final_files` stays unjudged there.
3. **Part 1a's per-language lexer was replaced.** Concrete inputs broke it: Java text blocks, JS/TS
   template literals, Rust lifetime annotations parsed as char literals, C++ `R"(…)"`, shell
   `${url#https://…}`, Ruby heredocs, Python 3.12 nested f-string quotes. It has been replaced by a
   design that can only decline to scan, never misclassify code as a comment.
4. **Part 4's premise was wrong for half its markers.** Gradle marks a task `UP-TO-DATE`/
   `FROM-CACHE` only after a *successful* prior execution on unchanged inputs; Go caches only
   passing results. Those are evidence of a pass, not of a skipped run.
5. **Part 2a false-blocked on git worktrees.** Reproduced in a fixture repo.
6. **Part 3 invalidated three documents the draft did not list**, one of which *instructs* the exact
   behaviour Part 3 penalises.

### Verification of the report

Every claim in #71 was re-derived from the code before being accepted. Two were sharpened:

| | Claim | Status |
|---|---|---|
| G1 | Tracker keys are not a tell | Confirmed. `violations("X.kt", ["// ABC-1234: …"])` → `[]` |
| G2 | Block-comment interiors unscanned | Confirmed, and stronger: ` * Fixes #123` → `[]` |
| G3 | Trailing comments unscanned | Confirmed. `val x = 1 // fixes task-42` → `[]` |
| G4 | `final_files` unscanned by both layers | Confirmed. `check-task-complete:310`; `post.tmpl` "ONLY when a diff is present" |
| G5 | One switch disables two concerns | Confirmed. `check-task-complete:170` returns above the `COMMENT_GUARD` read at 174 |
| G6 | Ladder pays for a fabricated reason | Confirmed. `submission_defect.go:52-100`; call site `handlers.go:1688` sits between the two `if !lightweight` blocks at 1679 and 1703 |
| G7 | Non-executing test output accepted | Confirmed by absence — but see Part 4, the report over-generalises which output means "nothing ran" |
| G8 | Plan fences unchecked for comments | Confirmed by absence — no comment rule in any `plan*.tmpl` |
| G9 | `path:line` breaks the disk tier | **Sharpened.** Not a comma-list special case — `lineAnchorRe` permits at most ONE `[-,]\d+` per group, so `:60,166` matches and `:60,166,174,419` fails outright |
| G10 | Findings surface late | Accepted as documentation-only |

G9's sharpened diagnosis exposes a second broken shape the report did not observe: `:27-30,40-50`
fails for the same reason.

## Non-goals

- **Making an attested field verifiable.** `codescene.go` already states that anti-tangent never
  runs CodeScene and cannot tell a real digest from an invented one. Part 3 does not change that.
  It removes the *incentive gradient* that made inventing one cheaper than silence, and nothing
  more.
- **A per-language comment lexer.** Ruled out for this release on measured breakage; see
  [Part 1a](#1a-the-structural-fix-scan-what-can-be-decided-from-the-line-alone). If it returns it
  needs a vendored multi-language fixture corpus first, since this repository contains no Kotlin,
  Rust, JS/TS or Java source to measure a false-positive rate against.
- **Scanning `Edit` payloads for block or trailing comments beyond what a single line decides.** An
  `Edit` carries a fragment with no enclosing context.
- **A default tracker-key tell.** Measured false positives were hardware identifiers (`HDMI-0`,
  `DP-0`) and prose labels (`ROUND-1`, `ROUND-8`) — shapes no allowlist anticipates. The tell is
  opt-in per project or absent, at the documented cost stated in Compatibility.
- **Blocking in the MCP server.** Unchanged: the server stays advisory, enforcement stays in the
  plugins.

## Part 1 — the write-time scanner (G1, G2, G3)

### 1a. The structural fix: scan what can be decided from the line alone

`violations(path, added_lines)` receives line strings, so "is this a comment?" degrades to
`line.startswith("//")`. That single limitation is the whole of G2 and G3.

The fix keeps the existing signature — no whole-file content, no lexer, no per-language state — and
changes only which text on a line counts as comment text.

**G2 — two block-comment shapes become scannable.** The C-family opener set gains `/*` and `*`:

```python
DEFAULT_COMMENT = ("//", "/*", "*")
```

Three rules, each of which exists to stop code being read as comment text:

- **`*` is admitted only when followed by whitespace or end-of-line.** A KDoc or Javadoc
  continuation line is `* text` or a bare `*`; a C dereference is `*ptr = …`. Without this,
  `*p = task-42;` — valid C subtracting 42 from `task` — reads as a comment and blocks the write.
- **Block-opener text stops at the first `*/`.** Everything past a block comment closing is code:
  `/* note */ y = task-42;` must yield `" note "`, not the whole tail.
- **`*/` is not an opener.** A line beginning with it carries no comment text — `*/` alone is
  empty, `*/ y = task-42;` is code. It falls through to the trailing-comment test, which still
  finds a real `//` on that line if one is there.

This closes the incident's exact shape: ` * ABC-1234: the inbound HELP keyword.` becomes scannable,
as does ` * Fixes #123`.

**What this does NOT reach.** An *unstarred* block interior —

```
/*
fixes task-42
*/
```

— has no opener on its middle line, and a line-at-a-time scan cannot know that line sits inside a
comment. It is out of reach without the lexer this release defers, and `post.tmpl`'s semantic rule
stays the backstop whenever a diff is present. The covered set is exactly three shapes: a starred
continuation, a one-line `/* … */`, and a trailing comment — not "block comments" in general.

**G3 — trailing comments, via a parity test that can only decline.** For a line that does *not*
begin with an opener, walk its comment delimiters **left to right** and take the first whose
preceding counts of unescaped `"`, `'` and backtick are **all even**. An odd count means that
delimiter sits inside a string literal, so it is skipped and the walk continues.

Left-to-right, not the last delimiter: taking the last would let a benign trailing comment hide a
violating one earlier on the same line (`x = 1 /* fixes #1 */ // ok`). A `/* … */` span ends at its
terminator and the walk **resumes after it**, so code between two comments is never scanned and a
second comment on the line is not lost. A `//` or `#` span runs to end of line and ends the walk.
In hash-family files the delimiter must be whitespace-preceded, so `${url#https://…}` is not a
comment.

The asymmetry is the point: an odd count means the delimiter is inside a string literal, and the
line is skipped. Within the shapes this test models — single, double and backtick quoting with
backslash escapes — it can miss a genuine trailing comment but will not promote code to comment.
It is a counting test, not a parser, so that is a property of those shapes rather than a proof over
every construct in all nine languages. The false-positive suite is the measurement.

| line | quotes before | decision |
|---|---|---|
| `val x = 1 // fixes task-42` | 0, 0, 0 | scan ` fixes task-42` → **flagged** |
| `url := "http://x" // ok` | 2, 0, 0 | scan ` ok` → clean (the `//` in the URL is not last) |
| `x = "a" + "b//c"` | 3, 0, 0 | **decline** — the `//` is inside a string |
| `echo "a #b"` (`.sh`) | 1, 0, 0 | **decline** |
| `echo "hi" # real` (`.sh`) | 2, 0, 0 | scan ` real` |
| `s := '\''` + `// fixes #1` | 2 unescaped | scan ` fixes #1` → **flagged** |
| `url=${url#https://t/ABC-1}` | `#` not whitespace-preceded | **decline** |

**What this costs versus the rejected lexer.** The unstarred multi-line interior described above is
the whole of the miss class. It is narrow — Javadoc, KDoc, and every formatter this project's
languages ship with produce starred continuations — and it is covered by `post.tmpl` whenever a
diff is present.

### 1b. Which path each caller takes

Because the scan needs only line strings, every caller uses the same entry point. `added()`,
`violations()` and the `Edit`/`Write` split are all unchanged:

| Caller | Added lines from |
|---|---|
| `Write`, path absent from disk | every line of `content` |
| `Write`, path present | `added(existing, content)` |
| `Edit` | `added(old_string, new_string)` |
| close-time, `final_diff` / `final_diff_path` | `+` lines, per the existing hunk parser |
| close-time, `final_files` | git — see [Part 2a](#2a-g4--deriving-added-lines-from-git) |

Precedence between the last two rows is decided by which field was **submitted**, not by which one
parsed to something. A diff can legitimately contain no added lines — a pure deletion — and
falling back to `final_files` on an empty parse would reconstruct from the working tree for a
submission whose author already stated which lines were theirs.

This removes an entire class of defect the first draft carried: it proposed applying diff line
numbers to a file read from disk without checking the two were the same file. No disk read, no
index mapping, no mismatch.

### 1c. G1 — `ANTI_TANGENT_TICKET_PATTERN`

An optional project-supplied regex, compiled once at import and appended to `TELLS` with the
description `"a tracker reference"`. Unset means the tell is absent: there is no default, and the
scanner's behaviour on an unconfigured project is exactly what it is today.

```jsonc
// .claude/settings.json — per project, which is where key shapes differ
{ "env": { "ANTI_TANGENT_TICKET_PATTERN": "ABC-\\d+" } }
```

Guards, because this compiles operator-supplied regex inside a hook that blocks writes:

1. A pattern that fails to compile is dropped and traced. Never fatal.
2. A pattern longer than 200 characters is refused.
3. **The whole scan runs under a `signal.setitimer` alarm**, and a SIGALRM fails the scan open. A
   structural check for nested quantifiers was considered and rejected: it misses `(a|aa)+` and
   `(a|a)*`, and `(a+)+$` over a 40-character line still hangs. A wall-clock bound is the only
   guard that actually bounds, and it covers the existing `TELLS` as well.

## Part 2 — the close-time hook (G4, G5)

### 2a. G4 — deriving added lines from git

A lightweight completion submits `final_files`: full content at absolute paths, with no signal for
which lines are new. Scanning every comment in a submitted file would block closes on comments the
implementer never wrote, so git supplies the missing signal.

**Every git invocation is pinned and rooted at the file's own directory:**

```
GIT="git -C <dirname of path> -c diff.noprefix=false -c diff.mnemonicPrefix=false \
     -c core.quotePath=false --no-pager"

$GIT ls-files --error-unmatch -- <path>
    exit 0    -> tracked:   $GIT diff --no-color --no-ext-diff HEAD -- <path>, take '+' lines
    exit 1    -> unmatched: $GIT check-ignore -q -- <path>
                                exit 1 -> untracked, every line is new
                                else   -> ignored, or unanswerable: skip
    any other -> skip this path
```

Three details, each of which was a defect in the first draft:

- **`-C <dirname>`, not the hook's cwd.** Under the `using-git-worktrees` flow the subagent's work
  lives in a nested worktree while the hook runs at the main checkout. Reproduced in a fixture
  repo: from the main checkout, `git ls-files --error-unmatch <abs path inside the worktree>` exits
  **1** — indistinguishable from "untracked" — so the draft would have scanned the whole file and
  blocked on its pre-existing comments. With `-C` it resolves correctly.
- **Exit 1 is not "any non-zero".** A path outside any repository exits **128**. Folding the two
  together turns "I cannot look" into "everything is new".
- **`check-ignore` before concluding "new", and only its definite answer counts.** A gitignored
  path also exits 1 from `ls-files`. `check-ignore` returns 0 for ignored and 1 for not-ignored;
  any other status means it could not tell, and reading that as "not ignored" would scan every line
  of a file the hook never classified.

Diff prefixes are pinned because `diff.mnemonicPrefix=true` emits `+++ w/` and `diff.noprefix=true`
emits a bare path; the existing hunk parser matches `+++ b/` only and would silently see zero files.

Paths are batched into one `ls-files -z` and one `diff` per repository root rather than two spawns
per file.

**Coverage, stated honestly.** `git diff HEAD` is empty once the work is committed, and
subagent-driven development commits per task *before* the controller's `TaskUpdate` fires this
hook — `implementer.md:84-85` already tells implementers as much. So this scan reaches uncommitted
work and newly-created untracked files, which is the incident's case and not the dominant one.
Diffing against a guessed base (`merge-base` with the default branch) was considered and rejected:
the hook cannot know the plan run's base commit, and a wrong base trades a quiet miss for a wrong
block.

### 2b. G5 — one switch per concern

`ANTI_TANGENT_COMPLETION_GUARD=0` currently returns at `check-task-complete:170`, above the
`COMMENT_GUARD` read at 174, so it silently disables the comment scan as well — while the comment
block's own message names `ANTI_TANGENT_COMMENT_GUARD=0` as the remedy.

Both variables are read up front and each half runs on its own switch; the hook exits early only
when both are `0`.

Two pieces of text become false and change with the code: the block message at
`check-task-complete:427` ("completion gate passed"), which is not true when the completion gate
was disabled, and the guard README's claim that the hook exits "before it reads stdin". `CLAUDE.md`'s
"silences the whole close-time hook" sentence changes too — two separately-named variables that do
not independently control their named concerns describe an implementation accident, not a design.

## Part 3 — the CodeScene ladder (G6)

### 3a. What is actually wrong

The gate fired correctly. `ANTI_TANGENT_CODESCENE=required` was set and `codesceneFindings` runs
unconditionally — the `lightweight` flag affects only session lookup and spec synthesis, and never
reaches this check. The ladder demanded an attestation, got one, and graded it as designed: silence
costs a major, one sentence costs a minor. Inventing a reason is therefore the cheapest route to a
pass, and that is what happened.

### 3b. One rung, not a taxonomy

Classifying reasons — protocol reasons accepted, environment claims required to carry evidence —
needs a keyword list, and any such list leaks: `"the task didn't warrant it"` matches neither class
and falls through to a minor. A single rung delivers the same intent without classifying anything:

```
ANTI_TANGENT_CODESCENE unset       ->  no adoption findings                    (unchanged)

ANTI_TANGENT_CODESCENE=required    (lightweight and session-backed alike)
    no codescene argument          ->  major
    ran:false, no skip_reason      ->  major
    ran:false, reason, no evidence ->  major       (was minor — ALL reasons)
    ran:false, reason + evidence   ->  minor
    ran:true                       ->  clean, plus the regression finding if any
```

Evidence is a new optional `skip_evidence` field on the digest, carrying the failing tool's error
text, capped at 2,000 runes in `Normalize()` alongside the existing 300-rune `SkipReason` cap.

`"lightweight task"` draws a major because it carries no evidence, not because a list names it —
**required means required, and the task's mode is never an excuse.** Silence and a sentence now
cost the same, so there is nothing left to buy by inventing one.

**Mechanics that must change with it:**

- `CategoryCodesceneSkipped` joins `submissionDefectCategories` (`submission_defect.go:17-21`,
  which today contains `codescene_not_run` but not `codescene_skipped`). Without this, an envelope
  blocked only by the new major is not submission-defect-only, and `next_action` sends the
  implementer to rework code instead of attaching evidence.
- `codesceneCell` (`report.go:85-98`) renders `SkipReason` only; it must render `skip_evidence` too,
  or claim 3c.2 below is unreachable.

### 3c. What this does and does not achieve

`skip_evidence` is attested like everything else; error text can be fabricated too. The honest claim
is narrower than the first draft made it:

1. **Fabricating is no longer cheaper than silence.** This is the whole mechanism, and it holds.
2. **A fabricated error string is more specific than "not configured", so a human reading
   `plan_run_report` has more to catch it on** — but only on the session-backed path. Per 3d the
   lightweight path writes no ledger row at all, so on the incident's own path this second benefit
   does not exist.

**The honest caller under `required`.** A host where CodeScene is genuinely absent produces no error
text — the tool is simply not there, nothing fails — so an honest "not configured" has no evidence
to offer and draws a major. This is acceptable only under an explicit reading, which the protocol
docs will now state: **`ANTI_TANGENT_CODESCENE=required` is an operator assertion that CodeScene is
present on every host running under it.** Under that reading "not configured" is a
misconfiguration, and a major is the correct report. During a genuine outage, honest and fabricated
callers both produce an error string and both draw a minor; the gradient is flat, which is the goal,
but no more than that is claimed.

### 3d. The false ledger promise

The minor's suggestion reads "the skip is recorded in the plan-run ledger". The ledger write at
`handlers.go:1711` is gated on `!lightweight && sess.PlanRunID != ""`, so on the lightweight path
nothing is recorded and the sentence is false. It is rewritten to promise only what the call does.

### 3e. Documents this invalidates

Part 3 makes three existing statements false. All three change in the same commit:

| location | what becomes false |
|---|---|
| `docs/protocol/core.md:136` | "`codescene_skipped`, `severity: minor` — recorded in the plan-run ledger; does not block" — every clause |
| `docs/protocol/implementer.md:157` | Instructs lightweight tasks to pass `{"ran": false, "skip_reason": "lightweight task"}` — the exact input now drawing a major |
| `README.md:329` | Describes the old three-rung ladder verbatim |

`examples/lightweight-dispatch.md` needs no change. It tells lightweight tasks to skip the CodeScene
*companion calls* (`pre_commit_code_health_safeguard`, `analyze_change_set`), which stays true —
Part 3 governs the `codescene` argument to `validate_completion`, which is a separate requirement.

`core.md` has **109 bytes** of headroom, so its rewrite must be equal-or-shorter. The
`implementer.md:157` paragraph is 673 bytes and is being rewritten anyway — **that rewrite is the
byte-budget solution**, and no separate trim is needed.

## Part 4 — test evidence (G7)

A new `internal/mcpsrv/test_evidence.go`, called beside `codesceneFindings` at `handlers.go:1688`
with the same prepend-and-finalize shape. Deterministic and reviewer-free, for the same reason
`checkFileConsistency` is.

### 4a. Correcting the report's premise

#71 asks for `UP-TO-DATE`, `FROM-CACHE` and `NO-SOURCE` to be treated alike. They are not alike:

- **Gradle marks a task `UP-TO-DATE` or `FROM-CACHE` only after a successful prior execution on
  unchanged inputs** — a failed test task re-executes. **Go caches only passing results.** These
  markers therefore attest that the tests pass on the current inputs. They carry no counts, but a
  plain successful Gradle run prints no per-test counts either, so a check firing on them would
  penalise the cached run and accept an equally count-free executed one.
- `NO-SOURCE` and its cross-ecosystem equivalents genuinely mean **nothing ran and nothing passed**
  — but only when they name a **test** task. Gradle prints `NO-SOURCE` for any task with no inputs,
  and a healthy build routinely shows it for `processTestResources` beside a test task running a
  full suite. Matching `NO-SOURCE` anywhere would reject that build, which is the same
  false-positive class the cached markers above are excluded for.

  This also forces an execution marker Gradle does not otherwise give: a plain successful run
  prints no per-test counts, so an *unannotated* test-task line (`> Task :app:test`) is what
  attests execution. Gradle annotates every kind of skipped work and leaves executed tasks bare.

Only the second class fires:

```
FIRES  (major)                          DOES NOT FIRE
  :<module>:test NO-SOURCE                :<module>:test UP-TO-DATE
  no tests ran            (pytest)        :<module>:test FROM-CACHE
  No tests found          (jest)          ok   <pkg>   (cached)      (go)
  ?  <pkg>  [no test files]  (go)         :<mod>:processTestResources
                                            NO-SOURCE — not a test task
                                          > Task :app:test — unannotated,
                                            so Gradle executed it
                                          a human-written summary
```

`?  <pkg>  [no test files]` is added from review; the report omits it and it is the Go analogue of
`NO-SOURCE`.

### 4b. Finding shape

`insufficient_evidence`, `major`, `criterion: test_evidence`. That category is already in
`submissionDefectCategories`, so `isSubmissionDefectOnly` routes it to "re-submit with the missing
evidence — no rework implied" rather than sending the implementer back into the code. The
suggestion names the fix: point the run at a target that has tests, or attach JUnit XML
`tests=`/`failures=` counts.

`post.tmpl:66` already asks the reviewer to cross-check test evidence against the ACs. The template
gains a "do not restate this finding" line, in the same shape as the existing one at `post.tmpl:53`,
so the deterministic finding is not duplicated by the reviewer.

Every re-submission is a fresh reviewer call — `test_evidence` is part of the cache key
(`handlers.go:1234`) — so this finding costs a full review round when it fires. That is the point,
but it is a real cost and is named here.

## Part 5 — `validate_plan` (G8, G9)

### 5a. G9 — the line-anchor regex

`internal/planparser/filerefs.go:50`:

```
-  (?::\d+(?:[-,]\d+)?)+$
+  (?::\d+(?:-\d+)?(?:,\d+(?:-\d+)?)*)+$
```

The old form permits at most one `[-,]\d+` per group. Verified in Go against both:

| input | old | new |
|---|---|---|
| `SomeDoc.md:60,166,174,419` | unchanged (**bug**) | `SomeDoc.md` |
| `F.kt:27-30,40-50` | unchanged (**bug**) | `F.kt` |
| `File.kt:27` | `File.kt` | `File.kt` |
| `F.kt:27-30`, `F.kt:27,30`, `F.kt:12:3` | stripped | stripped |
| `C:\x`, `https://example.com/a`, `plain.go` | untouched | untouched |

Go's `regexp` is RE2, so the added nesting carries no backtracking risk. The existing comment gains
one sentence stating the invariant the old regex got wrong: an anchor is a comma-separated list of
lines-or-ranges, not a single pair.

### 5b. G8 — comment hygiene over normative code fences

Normative code in a plan is transcribed verbatim, so a tracker-key comment in the plan becomes one
in the repository. Four rounds of `validate_plan` passed roughly twenty of them.

A section in `plan_rules.tmpl`, mirroring `post.tmpl`'s existing rule — `minor`,
`criterion: comment_hygiene`. Reviewer-led rather than deterministic: the reviewer recognises
`ABC-1234:` as a tracker key with no configuration, which is exactly what a regex cannot do, and
porting `TELLS` to Go would create a second implementation of the same policy.

**Scope, which the rule must state explicitly or it will misfire on this very release:**

- Only fences whose code is **transcribed into the repository**. Diff fences, expected-output
  fences, and test-fixture fences are exempt — this release's own plan will contain
  `// fixes task-42` and `* Fixes #123` as eval fixtures, and flagging them would be wrong.
- In a diff fence, `-` lines are exempt: removing a bad comment is the fix, not the defect.

**Interaction with `noise_cluster`.** `FinalizePlanVerdict` uses the same ladder as everything
else: three minors → `warn`. A plan with three tracker-key fences therefore returns `warn`, which
the plan-handoff gate treats as stop-and-justify. That is defensible for twenty of them and
heavy-handed for three, so the findings are emitted **plan-level, consolidated into one finding
listing the offending fences**, not one finding per fence.

Twelve `plan_*.golden` files regenerate. The diff is reviewed by hand, not trusted.

### 5c. G11 — a plan must tell implementers the comment policy

Not in #71; found while preparing to dispatch this plan's own execution.

**The gap.** `validate_plan` enforces nothing about comments, and neither does `validate_task_spec`.
The policy exists in `implementer.md §4.4`, and the protocol skill routes implementers to read it —
but only when the `anti-tangent-protocol` plugin is installed *and* the subagent invokes its skill.
Without that the implementer is told nothing, writes a comment carrying change history, and pays a
round: `post.tmpl`'s `comment_hygiene` rule or the guard's block, a fix, and a re-validation.

**Why §4.4 is not sufficient even when it is loaded.** Normative code in a plan is *binding* —
`post.tmpl` calls normative bodies "authoritative for fixture state, exact strings, and
assertions". An implementer that read §4.4 will still transcribe a tracker-key comment sitting in a
binding fence, because the plan outranks its own judgment. That is exactly how #71's comment
reached the repository. 5b keeps such comments out of the fence; 5c makes the plan carry the rule
for everything the implementer writes itself. The two are complementary, not alternatives.

**The check.** A plan-level finding when the plan gives implementers neither the comment policy nor
a pointer to it:

```
severity:  major
category:  other
criterion: comment_policy_absent
```

**`category: other` is load-bearing, not laziness.** The semantically obvious choice,
`convention_deviation`, carries a parser-side severity floor — `applySeverityFloor`
(`parser_partial.go:19-27`) forces both it and `unverifiable_codebase_claim` to `minor`, and the
plan parser applies the floor through `validateFinding`. Emitting this as `convention_deviation`
at `major` would be silently downgraded and the gate would never fire. `other` has no floor, and it
is what the existing plan-level major already uses (`task_order_contradiction`,
`file_consistency.go:178-180`).

One major yields `warn` (`FinalizePlanVerdict` delegates to `FinalizeVerdict`), which the
plan-handoff gate treats as stop-and-justify. That is the intended strength.

**Satisfied by either form**, and the pointer is the cheaper one:

- a statement of the policy in the plan's own constraints section, or
- one line naming `anti-tangent-protocol`'s `implementer.md §4.4`.

A pointer cannot drift out of sync with the policy the way a copied paragraph can, so it is the
form the rule should recommend.

**Reviewer-led, not deterministic.** A regex can spot a pointer but not a policy stated in prose,
and for a *gate* a false positive — blocking a plan that does carry the policy — costs more than an
occasional miss. The reviewer judges intent. This trades some reliability for far less friction,
and it is the same call made in 5b.

## Part 6 — documentation (G10 and the rest)

### 6a. Authoring guidance for the new plan requirement

`docs/protocol/authoring.md` gains a line telling plan authors that a plan must carry the comment
policy or a pointer to `implementer.md §4.4`, and that `validate_plan` now emits a plan-level
major when it carries neither. It has 7,541 bytes of headroom, so this is unconstrained.

### 6b. The controller note

`docs/protocol/controller.md`: a `pass` after N rounds does not mean earlier rounds inspected
everything. A defect present from round 1 was first reported in round 4. Not a bug — worth stating
so a controller does not read an Nth-round pass as an audit of rounds 1..N-1. `controller.md` has
1,564 bytes of headroom.

### 6c. The byte budget

The protocol parts are CI-capped at **strictly under 16,000 bytes**, with a warning band at 15,500
(`.github/workflows/ci.yml:61-74`):

| part | bytes | headroom |
|---|---|---|
| `core.md` | 15,891 | **109** — in the warning band |
| `implementer.md` | 15,593 | **407** — in the warning band |
| `controller.md` | 14,436 | 1,564 |
| `authoring.md` | 8,459 | 7,541 |
| `project-knowledge.md` | 10,401 | 5,599 |

The first draft treated this as "trim `implementer.md` to make room". Part 3e supersedes that: the
`implementer.md:157` paragraph is being **rewritten**, and the rewrite must come in at or under its
current 673 bytes. `core.md:136-137` likewise rewrites at equal-or-shorter length. No separate trim
is required, and nothing new is added to `core.md`.

Both parts resync into `plugin/anti-tangent-protocol/protocol/` in the same commit, per `CLAUDE.md`.

## Testing

| Surface | How |
|---|---|
| G2 openers (1a) | KDoc `* ABC-1234:` and ` * Fixes #123` positives; `*ptr = task-42;` **must not** fire (the `*`-followed-by-whitespace rule); `/* fixes #1 */` positive |
| G3 parity (1a) | The seven-row table in 1a, as eval cases. The decline cases are the load-bearing ones: `x = "a" + "b//c"`, `echo "a #b"`, `url=${url#https://t/ABC-1}` |
| Ticket pattern (1c) | The four G1 rows with the pattern set; the same four unset (all clean); uncompilable; over-long; a catastrophic-backtracking pattern that must trip SIGALRM and fail open |
| git derivation (2a) | Fixture repo covering: untracked, tracked-and-modified, tracked-and-clean, gitignored, **inside a nested worktree with the hook rooted at the main checkout**, outside any repo (exit 128), and a repo with `diff.mnemonicPrefix=true` |
| Switch split (2b) | All four `COMPLETION_GUARD` × `COMMENT_GUARD` combinations |
| Ladder (Part 3) | Table test per rung; `ran:true` with a regression; an envelope blocked only by the new major asserting `SubmissionDefectOnly` is true |
| Test evidence (Part 4) | **Negatives first**: `UP-TO-DATE`, `FROM-CACHE`, Go `(cached)`, a human summary — all must stay clean. Then the four positives |
| Regex (5a) | The Part 5a table, as a table test |
| Plan rule (5b) | A plan with three tracker-key fences → exactly one consolidated minor; a plan with a diff fence removing a bad comment → clean |
| Plan rule (5c) | A plan carrying neither policy nor pointer → one major, `category: other`, `criterion: comment_policy_absent`. A plan carrying only the one-line pointer → clean. **Assert the severity survives the parser**, since the obvious category would have been floored to minor |
| Goldens | `go test ./internal/prompts/... -update`, twelve files, diff read by hand |
| Protocol size + bundle | `.github/workflows/ci.yml:61-91` — these are inline CI steps, **not** `scripts/check-protocol-docs.sh`, which checks relative links and section-ID uniqueness only |
| Everything | `go test -race ./...`, `evals/run.sh`, `fp-report.sh` (all CI-gated) |

`fp-scan.py` and `fp-report.sh` must **unset `ANTI_TANGENT_TICKET_PATTERN`** before running, or the
false-positive gate silently depends on the developer's own environment.

The false-positive corpus is this repository: 170 Go files, 8 shell, 3 Python, and no Kotlin, Rust,
JS/TS or Java at all. The prefix-plus-parity design is language-agnostic enough that this is
tolerable — it can only decline, never promote code to comment — which is the main reason the lexer
was deferred rather than fixed.

## Compatibility

**This release does not close #71's headline case by default.** On a project that has not set
`ANTI_TANGENT_TICKET_PATTERN`, ` * ABC-1234: …` matches no tell, and no layer flags it: the write
hook has nothing to match, the close hook has nothing to match, and `post.tmpl`'s reviewer rule
stays disabled without a diff. G2 and G4 make the line *reachable* so that a configured pattern
works on it; they do not make it *match*. Setting the variable is the fix, and the guard README
will say so at the top of its configuration section.

**Behavioural changes that fail verdicts passing today**, both under **Changed** in the changelog:

1. Part 3 raises an unevidenced `skip_reason` from `minor` to `major` under
   `ANTI_TANGENT_CODESCENE=required`. Operators who have not set that variable see no change.
2. Part 4 emits a `major` on test output showing nothing ran.

**How those interact, which matters more than either alone:** one major yields `warn`
(`finalize.go:27-31`) and the close-time hook blocks only on `fail` (`check-task-complete:394`). So
either finding alone warns. **Both on the same lightweight close stack to two majors → `fail` → the
hook blocks.** A lightweight task under `required` that skips CodeScene without evidence *and*
pastes a `NO-SOURCE` test run will be blocked where today it closes clean. That is the intended
outcome and the sharpest edge in the release.

**Plans without a comment policy start drawing a major.** Every plan reviewed after this release
that gives implementers neither the policy nor a pointer to it returns `warn` at plan level. The
remedy is one line, and the finding's suggestion names it.

**One documented behaviour reverses.** `ANTI_TANGENT_COMPLETION_GUARD=0` stops disabling the
comment scan. Anyone relying on it as a master switch must set both variables.

**Additive, no migration.** `skip_evidence`, `ANTI_TANGENT_TICKET_PATTERN`, and the Part 1/2 scanner
changes, which can only find more and fail open when they cannot look.

## Release

| | |
|---|---|
| Branch | `version/0.20.0` |
| Changelog | `## [0.20.0] - 2026-09-10` |
| `VERSION` | **not** touched on the branch — the release workflow bumps it |
| Merge commit | carries `[minor]` |
| `anti-tangent-guard` | 0.2.0 → 0.3.0 |
| `anti-tangent-protocol` | 0.2.1 → 0.2.2, `protocol/` resynced from `docs/protocol/` |
| `.claude-plugin/marketplace.json` | duplicates both plugin versions — bump there too |

Part 5a is independent of everything and can land first. Parts 1/2 (plugin) and 3/4 (server) are
independent of each other. Part 3e's three document rewrites must land in the same commit as Part 3.

## References

- Issue [#71](https://github.com/patiently/anti-tangent-mcp/issues/71)
- `plugin/anti-tangent-guard/hooks/comment_scan.py` — `TELLS`, `DEFAULT_COMMENT`, `violations`
- `plugin/anti-tangent-guard/hooks/check-task-complete:170,310,394,427` — switch, `diff_added_lines`, block conditions
- `internal/mcpsrv/submission_defect.go:17-21,52-100` — `submissionDefectCategories`, `codesceneFindings`
- `internal/mcpsrv/handlers.go:1234,1527,1679,1688,1703,1711` — cache key, lightweight flag, call site, ledger write
- `internal/planparser/filerefs.go:50` — `lineAnchorRe`
- `internal/verdict/finalize.go:27-31` — the severity ladder both new findings feed
- `internal/planrun/report.go:85-98` — `codesceneCell`
- `internal/prompts/templates/post.tmpl:53,66,120` — restate-suppression precedent, test cross-check, the diff-only clause
