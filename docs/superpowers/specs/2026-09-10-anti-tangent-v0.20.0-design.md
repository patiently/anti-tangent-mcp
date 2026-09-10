# anti-tangent-mcp v0.20.0 — closing the comment-hygiene and completion-gate gaps

**Status:** design
**Date:** 2026-09-10
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
- Non-executing test output (`:module:test UP-TO-DATE`) was accepted as "4 tests passed".

This release closes all ten. Two of them (G2, G4) are the ones that would each independently have
caught this incident; the rest close the routes around them.

### Verification of the report

The report was written by an agent observing its own run. Every claim was re-derived from the
code before being accepted, and two were sharpened:

| | Claim | Status |
|---|---|---|
| G1 | Tracker keys are not a tell | Confirmed. `violations("X.kt", ["// ABC-1234: …"])` → `[]` |
| G2 | Block-comment interiors unscanned | Confirmed, and stronger: ` * Fixes #123` → `[]` |
| G3 | Trailing comments unscanned | Confirmed. `val x = 1 // fixes task-42` → `[]` |
| G4 | `final_files` unscanned by both layers | Confirmed. `check-task-complete:310`; `post.tmpl` "ONLY when a diff is present" |
| G5 | One switch disables two concerns | Confirmed. `check-task-complete:170` returns above the `COMMENT_GUARD` read at 174 |
| G6 | Ladder pays for a fabricated reason | Confirmed. `submission_defect.go:52-83`; call site `handlers.go:1688` is outside both `if !lightweight` guards |
| G7 | Non-executing test output accepted | Confirmed by absence — no such check anywhere |
| G8 | Plan fences unchecked for comments | Confirmed by absence — no comment rule in any `plan*.tmpl` |
| G9 | `path:line` breaks the disk tier | **Sharpened.** Not "the comma-list form may be the trigger" — `lineAnchorRe` permits at most ONE `[-,]\d+` per group, so `:60,166` matches and `:60,166,174,419` fails outright |
| G10 | Findings surface late | Accepted as documentation-only |

G9's sharpened diagnosis also exposes a second broken shape the report did not observe:
`:27-30,40-50` fails for the same reason.

## Non-goals

- **Making an attested field verifiable.** `codescene.go` already states that anti-tangent never
  runs CodeScene and cannot tell a real digest from an invented one. G6 does not change that. It
  removes the *incentive gradient* that made inventing one cheaper than silence, and nothing more.
- **Scanning `Edit` payloads for block or trailing comments.** An `Edit` carries a fragment with no
  enclosing context, so a `* Fixes #123` inside a comment is indistinguishable from one inside a
  string literal. This stays out of scope for the reason the existing code gives.
- **A default tracker-key regex.** Measured false positives were hardware identifiers (`HDMI-0`,
  `DP-0`) and prose labels (`ROUND-1`, `ROUND-8`) — shapes no allowlist anticipates. The tell is
  opt-in per project or absent.
- **Blocking in the MCP server.** Unchanged: the server stays advisory, enforcement stays in the
  plugins.

## Part 1 — the write-time scanner (G1, G2, G3)

### 1a. The structural fix: scan exactly where content allows it

`violations(path, added_lines)` receives a list of line strings and nothing else, so "is this line
a comment?" degrades to `line.startswith("//")`. That single limitation is the whole of G2 and G3.

Where the caller holds whole-file content, the question can be answered properly. `comment_scan.py`
gains a parallel entry point rather than changing the existing one:

```python
violations(path, added_lines)                     # unchanged — line-prefix only
violations_full(path, full_text, added_indices)   # new — lexed
added(old, new)                                   # unchanged — returns line strings
added_indices(old, new)                           # new — same occurrence-aware algorithm,
                                                  #       returns indices into new.splitlines()
```

`violations_full` lexes `full_text`, yielding comment text for each line index it is asked about —
line comments, block-comment interiors and trailing comments alike — then runs the existing `TELLS`
over that text.

Two lexer families:

- **C-style** (`.go .ts .tsx .js .jsx .rs .java .kt .c .h .cc .cpp .hpp`): `//` to end of line,
  `/* … */` spans, string literals `"…"` and `'…'` with backslash escapes, Go raw strings
  (backticks), Kotlin raw strings (`"""`). Block-comment nesting is depth-tracked **only** for
  `.kt` and `.rs`, whose languages permit it; Go, Java and C terminate on the first `*/` and
  depth-tracking them would mis-scan.
- **Hash-style** (`.py .sh .bash .rb`): `#` to end of line, string literals including Python
  triple-quotes and Ruby `=begin`/`=end`.

**Every lexer failure fails open.** Any exception, or any state the lexer cannot resolve, skips the
file and reports no violation. This is the module's existing weighting — a false block costs more
than a miss — and it is what bounds the risk of replacing a trivially-conservative check with a
correct-if-the-lexer-is-correct one.

### 1b. Which path each caller takes

| Caller | Whole-file content? | Path |
|---|---|---|
| `Write`, path absent from disk | yes (payload) | `violations_full`, all indices |
| `Write`, path present | yes (payload) | `violations_full` + `added_indices` |
| `Edit` | no (fragment) | `violations` — unchanged |
| close-time, `final_files` | yes (payload or disk) | `violations_full`, indices from git (Part 2) |
| close-time, `final_diff` | only if the path is on disk | `violations_full` with indices from hunk headers; else `violations` |

A unified diff's hunk headers (`@@ -a,b +c,d @@`) carry new-file line numbers, so where the file is
readable on disk the diff path reaches the exact scan too. Where it is not — a diff produced on
another machine — the existing line-prefix behaviour stands unchanged.

### 1c. G1 — `ANTI_TANGENT_TICKET_PATTERN`

An optional project-supplied regex, compiled once at import and appended to `TELLS` with the
description `"a tracker reference"`. Unset means the tell is absent: there is no default, and the
scanner's behaviour on an unconfigured project is exactly what it is today.

```jsonc
// .claude/settings.json — per project, which is where key shapes differ
{ "env": { "ANTI_TANGENT_TICKET_PATTERN": "ABC-\\d+" } }
```

Three guards, because this compiles operator-supplied regex inside a hook that blocks writes:

1. A pattern that fails to compile is dropped and traced. Never fatal.
2. A pattern longer than 200 characters is refused.
3. A pattern containing a nested quantifier (a quantified group that itself contains a quantifier)
   is refused, as a cheap ReDoS guard. Python's `re` backtracks; an operator typo should not be
   able to hang a `PreToolUse` hook.

Out of the box a tracker key is still caught — by `post.tmpl`'s reviewer rule, which recognises
`ABC-1234:` as a tracker reference without any configuration, and which Part 2 makes fire on
lightweight completions for the first time.

## Part 2 — the close-time hook (G4, G5)

### 2a. G4 — deriving added lines from git

A lightweight completion submits `final_files`: full content at absolute paths, with no signal for
which lines are new. That absence is exactly why `post.tmpl` refuses to judge comment hygiene
without a diff, and scanning every comment in a submitted file would block closes on comments the
implementer never wrote.

The hook runs inside the repository, so git supplies the missing signal:

```
for each final_files path:
    git ls-files --error-unmatch <path>
        non-zero  ->  untracked: every line is new          (the incident's exact case)
        zero      ->  git diff HEAD -- <path>, take '+' lines
    no git / not a repo / path clean / any git error
                  ->  skip this path
```

Every failure mode skips rather than blocks, consistent with the rest of the hook. The known blind
spot is a subagent that has already committed its work: `git diff HEAD` is then empty and the path
is skipped. Reconstructing a base commit is not attempted — the hook has no reliable way to know
one, and guessing would trade a quiet miss for a wrong block.

### 2b. G5 — one switch per concern

`ANTI_TANGENT_COMPLETION_GUARD=0` currently returns at `check-task-complete:170`, above the
`COMMENT_GUARD` read at 174, so it silently disables the comment scan as well — while the comment
block's own message names `ANTI_TANGENT_COMMENT_GUARD=0` as the remedy.

Both variables are read up front and each half runs on its own switch; the hook exits early only
when both are `0`.

`CLAUDE.md` currently documents the coupling as intended ("silences the whole close-time hook").
That sentence and the guard README change with the code. Two separately-named variables that do
not independently control their named concerns describe an implementation accident, not a design.

## Part 3 — the CodeScene ladder (G6)

### 3a. What is actually wrong

The gate fired correctly. `ANTI_TANGENT_CODESCENE=required` was set and `codesceneFindings` runs
unconditionally — the `lightweight` flag affects only session lookup and spec synthesis, and never
reaches this check. The ladder demanded an attestation, got one, and graded it as designed:

| submitted | finding |
|---|---|
| no `codescene` argument | **major** `codescene_not_run` |
| `ran:false`, empty `skip_reason` | **major** |
| `ran:false` + *any* non-empty `skip_reason` | **minor** `codescene_skipped` |

Silence costs a major; one sentence costs a minor. Inventing a reason is therefore the cheapest
route to a pass, and that is what happened.

### 3b. One rung, not a taxonomy

The issue proposes classifying reasons — protocol reasons accepted, environment claims required to
carry evidence. Working that through, the classification needs a keyword list, and any such list
leaks: `"the task didn't warrant it"` matches neither class and falls through to a minor, leaving
the incentive intact for anyone who phrases around it.

A single rung delivers the same intent without classifying anything:

```
ANTI_TANGENT_CODESCENE unset       ->  no adoption findings                    (unchanged)

ANTI_TANGENT_CODESCENE=required    (lightweight and session-backed alike)
    no codescene argument          ->  major
    ran:false, no skip_reason      ->  major
    ran:false, reason, no evidence ->  major       (was minor — ALL reasons)
    ran:false, reason + evidence   ->  minor
    ran:true                       ->  clean, plus the regression finding if any
```

Evidence is a new optional `skip_evidence` field on the digest, carrying the failing tool's actual
error text. The field is additive and callers omitting it get a major, which is the intended
pressure.

This drops the keyword list entirely. `"lightweight task"` draws a major because it carries no
evidence, not because a list names it — **required means required, and the task's mode is never an
excuse.** And the gradient is gone: silence and a sentence now cost exactly the same, so there is
no longer anything to buy by inventing one.

### 3c. What this does not achieve

`skip_evidence` is attested like everything else; error text can be fabricated too. Two things are
nonetheless gained, and they are the whole claim:

1. Fabricating is no longer *cheaper* than silence, which is what selected for it.
2. A fabricated error string is specific enough for a human reading `plan_run_report` to catch,
   where "not configured" was not.

### 3d. The false ledger promise

The minor's suggestion reads "the skip is recorded in the plan-run ledger". The ledger write at
`handlers.go:1711` is gated on `!lightweight && sess.PlanRunID != ""`, so on the lightweight path
nothing is recorded and the sentence is false. It is rewritten to promise only what the call
actually does.

## Part 4 — test evidence (G7)

A new `internal/mcpsrv/test_evidence.go`, called beside `codesceneFindings` at `handlers.go:1688`
with the same prepend-and-finalize shape. Deterministic and reviewer-free, for the same reason
`checkFileConsistency` is: it cannot drift and it costs no tokens.

**The naive check misfires.** A real Gradle build prints `UP-TO-DATE` for a dozen compile tasks
beside one genuinely executed test task. The check therefore fires only when a line naming a
**test** task carries a no-op status *and* nothing in the evidence indicates tests actually ran:

```
:<module>:test           UP-TO-DATE | FROM-CACHE | NO-SOURCE
ok      <pkg>    (cached)                                     # Go
no tests ran | No tests found                                 # pytest / jest
```

→ `insufficient_evidence`, `major`, `criterion: test_evidence`. That category is already in
`submissionDefectCategories`, so `isSubmissionDefectOnly` routes it to "re-submit with the missing
evidence — no rework implied" rather than sending the implementer back into the code, which is the
correct reading: the tests may well have passed, but the pasted invocation does not show it.

The suggestion names the fix: force a run (`cleanTest`, `--rerun-tasks`, `-count=1`) or attach
JUnit XML `tests=`/`failures=` counts.

Evidence that is a human summary ("all 4 tests pass") carries no marker and draws nothing. The
check only ever fires on pasted tool output.

## Part 5 — `validate_plan` (G8, G9)

### 5a. G9 — the line-anchor regex

`internal/planparser/filerefs.go:50`:

```
-  (?::\d+(?:[-,]\d+)?)+$
+  (?::\d+(?:-\d+)?(?:,\d+(?:-\d+)?)*)+$
```

The old form permits at most one `[-,]\d+` per group. Verified in Go against both forms:

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

A short section in `plan_rules.tmpl`, mirroring `post.tmpl`'s existing rule: code inside a fence
lands in the codebase as written, so a comment carrying a tracker key, an issue or pull-request
reference, or change narration is a finding — `minor`, `criterion: comment_hygiene`.

Reviewer-led rather than deterministic on purpose. The reviewer recognises `ABC-1234:` as a tracker
key with no configuration, which is precisely what a regex cannot do and what `ANTI_TANGENT_TICKET_PATTERN`
exists to work around. Porting `TELLS` to Go would also create a second implementation of the same
policy; the repo already pays that tax once (`SCAN_EXTS` duplicated in bash and Python, with
`evals/run.sh` asserting they match) and this surface is much larger.

Twelve `plan_*.golden` files regenerate. The diff is reviewed by hand, not trusted.

## Part 6 — documentation (G10)

### 6a. The controller note

`docs/protocol/controller.md`: a `pass` after N rounds does not mean earlier rounds inspected
everything. A defect present from round 1 was first reported in round 4. Not a bug — worth stating
so a controller does not read an Nth-round pass as an audit of rounds 1..N-1.

### 6b. A byte-budget constraint this release must solve

The protocol parts are CI-capped at 16,000 bytes each:

| part | bytes | headroom |
|---|---|---|
| `core.md` | 15,891 | **109** |
| `implementer.md` | 15,593 | **407** |
| `controller.md` | 14,436 | 1,564 |
| `authoring.md` | 8,459 | 7,541 |

`skip_evidence` (Part 3) and the test-evidence rule (Part 4) are both implementer-facing and belong
in `implementer.md`, which has 407 bytes free. They will not fit as prose. Existing
`implementer.md` text is trimmed to make room, and the trim is reviewed as a change in its own
right rather than smuggled in — a protocol part is read once per dispatched subagent, so what comes
out matters as much as what goes in.

`controller.md` absorbs 6a comfortably. Nothing goes into `core.md`.

Both parts resync into `plugin/anti-tangent-protocol/protocol/` in the same commit, per
`CLAUDE.md`.

## Testing

| Surface | How |
|---|---|
| Lexer (Part 1a) | Eval cases in `guard-evals.json`: KDoc and `/* */` positives; trailing-comment positives; **string-literal negatives that must not fire** — `url := "http://x" // ok`, `s = "# not a comment"`, a Go raw string containing `*/`, a Kotlin `"""` block containing `//` |
| Ticket pattern (1c) | The four G1 table rows with the pattern set; the same four with it unset (all clean); an uncompilable pattern; an over-long pattern; a nested-quantifier pattern |
| git derivation (2a) | Fixture repo: untracked file, tracked-and-modified file, tracked-and-clean file, non-repo directory |
| Switch split (2b) | Each of the four `COMPLETION_GUARD` × `COMMENT_GUARD` combinations |
| Ladder (Part 3) | Table test per rung, including `ran:true` with a regression |
| Test evidence (Part 4) | **Negatives first**: a Gradle build with compile tasks `UP-TO-DATE` and a test task that ran; a Go run mixing `(cached)` and executed packages; a human-written summary. Then the positives |
| Regex (5a) | The Part 5a table, as a table test |
| Goldens (5b) | `go test ./internal/prompts/... -update`, twelve files, diff read by hand |
| Protocol (Part 6) | `scripts/check-protocol-docs.sh` — size cap and bundle identity |
| Everything | `go test -race ./...`, `evals/run.sh`, `fp-report.sh` (all three CI-gated) |

`fp-scan.py` is re-run over real repository comments after Part 1 lands, to measure the new false-
positive surface before merge rather than after.

## Compatibility

**Behavioural changes that fail verdicts passing today.** Two, both intended, both belonging under
**Changed** in the changelog rather than **Fixed**:

1. Part 4 emits a `major` on cached or up-to-date test output. Anyone mid-plan pasting a cached run
   starts getting blocked on their next close. This is the sharpest edge in the release.
2. Part 3 raises an unevidenced `skip_reason` from `minor` to `major` under
   `ANTI_TANGENT_CODESCENE=required`. Operators who have not set that variable see no change.

**Additive, no migration.** `skip_evidence`, `ANTI_TANGENT_TICKET_PATTERN`, and every Part 1/2
scanner change (which can only ever find more, and fail open when they cannot look).

**One documented behaviour reverses.** `ANTI_TANGENT_COMPLETION_GUARD=0` stops disabling the comment
scan. Anyone relying on it as a master switch must now set both variables; the guard README and
`CLAUDE.md` say so.

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

Part 5a is independent of everything else and can land alone. Parts 1/2 (plugin) and 3/4 (server)
are independent of each other.

## References

- Issue [#71](https://github.com/patiently/anti-tangent-mcp/issues/71)
- `plugin/anti-tangent-guard/hooks/comment_scan.py` — `TELLS`, `DEFAULT_COMMENT`, `violations`
- `plugin/anti-tangent-guard/hooks/check-task-complete:170,310` — the switch and `diff_added_lines`
- `internal/mcpsrv/submission_defect.go:52` — `codesceneFindings`
- `internal/mcpsrv/handlers.go:1527,1688,1711` — lightweight flag, call site, ledger write
- `internal/planparser/filerefs.go:50` — `lineAnchorRe`
- `internal/prompts/templates/post.tmpl` — the "ONLY when a diff is present" clause
