# anti-tangent-mcp — honest plan-run accounting, fewer dead calls, and a lean check that agrees with itself

**Status:** design
**Date:** 2026-09-22
**Target:** 0.25.0 (minor)
**Source:** a field report on one 7-task plan run in a consumer project, on v0.24.0: 30
anti-tangent calls (`validate_plan` 7, `validate_task_spec` 9, `validate_completion` 13,
`plan_run_report` 1, `check_progress` 0), three tasks on the full lifecycle and three lightweight.
Consumer identifiers are withheld; tool names, categories, finding IDs, env vars and model IDs are
kept verbatim.

## Summary

The report rated the run net positive but expensive, and ranked ten recommendations. Every factual
claim in it held against v0.24.0; two of its readings did not, and verification found defects it
missed (see the next section). The fixes fall into three parts with different kinds of evidence,
so the design is split the same way as 0.22.0's. All three ship as one minor release.

1. **`plan_run_report` counts sessions, not tasks.** The report the controller reads first said
   "3 of 7 completed", "2 never dispatched" and "−6.0 net problem points" for a run where 6 of 7
   tasks were dispatched and the branch moved −2. Re-validations append rows, lightweight tasks
   cannot be recorded at all, and cumulative CodeScene deltas are summed. Separately, every
   `over_building` finding outside a plan task shares one fixed ID, which lets a plan ruling waive
   a completion finding. **Part 1** fixes the bookkeeping and the ID scope. Deterministic Go.
2. **Calls and rounds lost to friction.** 5 of 30 calls were dead: three input-cap rejections and
   two truncations that each needed a manual retry. Two deterministic plan findings could not be
   cleared by anything the controller did. Lightweight mode dropped the controller's rulings, and
   a hand-assembled diff was reviewed as if it were real. **Part 2** removes each cause. Mostly Go,
   one prompt addition.
3. **The lean check contradicts itself.** The only `over_building` instance the run produced was a
   plan-level `reuse:` asking for a shared helper, re-raised as `yagni:` on the task that built
   it. Two one-caller production functions shipped because verification findings asked for
   direct pinning, and the completion review did not name them. **Part 3** changes the prompts and
   ships each change only on replay evidence.

## Verification of the report

Each recommendation was checked against v0.24.0 before it was accepted.

| Report claim | What verification found |
|---|---|
| `plan_run_report` rows are sessions, the task counts are wrong, the CodeScene total is triple-counted | True, all three. Rows are appended per `validate_task_spec` session (`planrun.go` `AppendRow`); `ValidateCompletionArgs` has no `plan_run_id`, so lightweight tasks can never be recorded; `Totals` sums every row's `net_pp` although the protocol asks each task for a branch-vs-base run. |
| The same ID `f_b720ec2d` fired as plan `reuse:` and completion `yagni:` | True, but not a coincidence. A fingerprint hashes category, task key and criterion only; plan-level findings and every session tool use the empty task key, and `over_building` always has `category: quality`, `criterion: over_building`. So `f_b720ec2d` is the ID of **every** such finding, in any plan and any session — and the session tools' "only IDs this session issued" ruling guard checks the display ID alone, so a plan-level ruling on it waives a completion finding. |
| Nothing aims a finding on AC-mandated structure at the plan author | False. `post.tmpl` already addresses such a finding to the plan author. What is inconsistent is its neighbour line, "never flag … anything explicitly requested", and the one-caller test, which ignores exit contracts in the same prompt. |
| Let `reuse:` see sibling code by extracting `internal fun` signatures server-side | Rejected: language-specific code analysis is a non-goal. `context_paths` on `validate_completion` is accepted instead (Part 3). |
| Raise the caps so the codebase-reference checklist clears | Half right. The 50-entry `controller_verified_references` cap did cause a dead call and is raised; but making the checklist waivable by a ruling is rejected (§2.6). |
| Add a `lean_rung` field and a protocol pointer to `implementation_guidance` | Not warranted by this run: the dispatch briefs were verbatim, so no implementer had a lean decision to make. The field gets a pointer in `next_action` instead, which costs no protocol bytes (`implementer.md` has 46 bytes of headroom). |
| The reviewer misread a multi-file inline `final_diff` | The diff was malformed: one `diff --git` header over two files' hunks, which git never produces. The server does not detect it (§2.8). |
| Make `check_progress` the lean checkpoint | Not taken. One run is not enough to justify a mandatory reviewer call per task. |

**Found by verification, not in the report:**

- Truncated reviews report `review_ms: 0`; every truncation path drops the elapsed time.
- The `task_order_contradiction` false positive on plan abbreviations comes from the deterministic
  disk-tier file check, not the reviewer, and no ruling can waive it.
- The report hides its own duplicates: the "N rows for M tasks" note is in an `else if` that never
  runs when tasks are also missing.
- The ledger persists only completed rows, so a report recovered after a restart differs from the
  live one.
- `testability_extractions` suppress a scope finding at task start but never reach `post.tmpl`, so
  the completion review can call the same helper `yagni:`.
- A `warn` with an open major finding passes both the server's `next_action` and the guard's
  close-time check, although the protocol forbids reporting DONE with one.

## Non-goals

- The server stays advisory. Nothing here blocks a call or corrects code.
- No server-side storage of plan findings for task reviews. Intent travels from plan to task the
  way it already does: through the task's `Context:` line (§3.1).
- No language-specific extraction of signatures or symbols.
- No change to `check_progress`'s role; no `lean_rung` field; no protocol pointer to
  `implementation_guidance`.
- No `session_id` on `validate_task_spec`. Letting a re-validation continue a session would make
  spec reviews converge, but it changes review behaviour and is separate work.
- No change to the guard plugin. The DONE rule is surfaced in `next_action` only.
- The codebase-reference checklist stays unwaivable.
- `validate_plan`'s output budget and truncation handling are unchanged; its budget already scales
  with the task count.
- The replay harness does not learn to replay `validate_plan` (§3.5).

---

## Part 1 — plan-run accounting and finding identity

### 1.1 A run knows its tasks

When `validate_plan` creates a run it stores the parsed tasks' numbers and headings
(`Run.Tasks []{Index, Title}`), not only their count. `TaskCount` stays, equal to `len(Tasks)`,
so older ledgers still load. The ledger header line carries the titles; a header without them
loads with the count alone.

### 1.2 A call finds its task

`validate_task_spec` gains an optional `task_index` (1-based). A call carrying a `plan_run_id` is
resolved to a plan task in this order:

1. `task_index`, when it is within the run's tasks. An out-of-range index adds a minor advisory
   (`category: other`, `criterion: task_index`) that never changes the verdict, and resolution
   falls through.
2. The submitted `task_title`, compared with each stored title after `normalizeTaskTitle` (which
   drops a leading `Task N:`), case-folding and collapsing whitespace. Exactly one match is
   required; duplicate headings match nothing.
3. Otherwise the row is keyed by the normalized submitted title, and numbered after the plan's
   tasks (`TaskCount+1`, `+2`, … in order of first appearance). The report marks it unmatched.

A run recovered from a ledger without titles skips step 2.

### 1.3 Re-validation updates, never appends

A call that resolves to a task with an existing row updates it: `PreVerdict` becomes the new
verdict, `Attempts` increments, and the new session is attached. Every session attached to a row
keeps updating it, so an implementer that re-validated and then carried on with its first
`session_id` still lands on the right row. A `sessionID → row` index replaces the linear
`UpdateRow` scan. Post-completion fields are overwritten by whichever attached session completes
last.

The row's `Index` is the plan's task number. That makes the table's `#` column match the plan, and
it is what the ledger's last-line-per-`Index` load already assumes.

### 1.4 Lightweight tasks are recorded

`validate_completion` gains optional `plan_run_id`, `task_index` and `task_title`, read only
without a `session_id` (with a session, the session carries the link and these are ignored). A
lightweight call carrying a live `plan_run_id` resolves its task as in §1.2 and upserts that row
with `Lite: true`, no pre-verdict, and the post fields from the call. It writes a ledger line. An
unknown or expired `plan_run_id` is logged to stderr and otherwise ignored, as it is on
`validate_task_spec`: a bookkeeping hint never fails a review.

### 1.5 Counting tasks, not rows

- **Completed:** plan tasks whose row has a post verdict.
- **Never dispatched:** plan tasks with no row, listed as `Task N: <title>` when titles are known.
- **Unmatched:** rows that resolved to no plan task, counted and listed separately. This replaces
  the "N rows for M tasks" note, which could be hidden.
- **pass / warn / fail:** one per task, from its row's post verdict.
- The table's verdict column shows the post verdict, or `pre:<verdict>` for a task that has not
  completed, so an abandoned task reads differently from one in progress. New columns: attempts,
  and a `lite` marker.

### 1.6 CodeScene: the branch delta, counted once

- `codescene.Digest` gains an optional `base_ref`. The `codescene` input's description asks for
  the base ref that was passed to `analyze_change_set`.
- The run total groups rows that have a digest by `base_ref` (an absent one is its own group),
  takes the row with the latest `CompletedAt` in each group, and sums those. For the assessed run
  that is −2, not −6. The line reads "branch net problem points (latest per base ref)".
- Per-row values in the table are unchanged.
- **Missing CodeScene runs** counts only rows that reached completion without a digest; a task
  still open is not missing one.

### 1.7 Ledger

Spec-time rows and lightweight rows are written to the ledger as they happen, not only at
completion. Load keeps the last line per `Index`, so a recovered report matches the live one,
open rows included. Older ledger lines, without the new fields, still load. The
`unattachedPlanRunFinding` text for a ledger-recovered run, which says the ledger records a task
only when it completes, is rewritten to match.

### 1.8 `review_ms` on truncation

Every truncation path (`h.review`, `reviewPlanSingle`, `reviewPlanChunked`, `runReview`) returns
the elapsed time instead of 0, and stats record it.

### 1.9 Plan-level finding scope

Plan-level findings, and their waived entries, are fingerprinted with a reserved task key that no
normalized task title can equal (a control character), in both `assignPlanIDs` and
`waivePlanFindings`, which must change together. Plan per-task findings keep their title key, and
session-tool findings keep the empty key, so neither of those IDs changes.

After this, a plan-level `over_building` finding no longer shares an ID with any session finding.
A plan-level ID passed to `validate_completion`'s `controller_rulings` gets the existing
"unknown ID" advisory instead of waiving a completion finding, and the reverse no longer waives a
plan-level finding.

---

## Part 2 — friction and lightweight mode

### 2.1 `verification` step length

`verification` gets its own limit, 50 entries of at most 2,000 characters, instead of reusing
`pinned_by`'s 500. The field holds step text; the 200 KB payload cap already bounds the total.
The tool description and the schema contract test move with the constant.

### 2.2 `controller_verified_references` cap

Raised from 50 to 200 entries (500 characters each, unchanged) on both `validate_task_spec` and
`validate_plan`. A plan that cites more code facts than the old cap had no way to clear the
checklist the intended way: list what was verified.

### 2.3 The reviewer can read the brief

`validate_task_spec` gains `context_paths`, with the same resolution and limits as
`validate_plan`'s (absolute paths under `ANTI_TANGENT_PLAN_ROOTS` when set, at most 50 files,
`ContextMaxFileBytes` each and `ContextMaxPayloadBytes` in total), resolved through
`resolveContextFiles`. The files render in `pre.tmpl` through the existing `context_files` partial,
with its nonce delimiters, as the attached files that its `reuse:` rule already mentions. The
prompt says a term or step an attached file defines is defined. The files are not stored on the
session.

`controller.md` step 6 asks the controller to name the task's brief file in `context_paths` (see
Protocol text).

### 2.4 Truncation

- The per-task output budget (`ANTI_TANGENT_PER_TASK_MAX_TOKENS`, used by `validate_task_spec`,
  `check_progress` and `validate_completion`) defaults to **8192**, up from 4096. For a reasoning
  reviewer, thinking tokens count against this cap; providers bill tokens used, not the cap.
- When a per-task review is truncated, the caller gave no `max_tokens_override`, and the budget
  was below `MaxTokensCeiling`, the review runs **once more** at `MaxTokensCeiling`. Only when the
  retry also truncates does the existing partial recovery or truncated-result path run, on the
  retry's output. `review_ms` covers both attempts. A retried call writes one warning line to
  stderr.
- The per-task truncation suggestion names the value to pass (`max_tokens_override:
  <MaxTokensCeiling>`), as the plan one already does.

### 2.5 Plan abbreviations in the disk tier

The disk tier of the file-consistency check skips a `Modify:` reference whose first path segment
is an ALL-CAPS identifier (`^[A-Z][A-Z0-9_]+$`) that does not exist at `repo_root`. Such a segment
is a plan-defined abbreviation (`NET`, `MAIN/...`). A top-level entry that exists, such as
`VERSION` or `LICENSE`, is checked as before. Parsing abbreviation definitions out of Global
Constraints was rejected: plans write them in too many formats.

### 2.6 The codebase-reference checklist on a pass

When the checklist is all that remains, `next_action` leads with the decision: "Plan passes:
dispatch. The `codebase_reference_checklist` finding lists references the reviewer could not
verify; pre-flight any you have not already checked, or list them in
`controller_verified_references`."

The checklist stays unwaivable. Its ID is the same every round while its contents change, so a
ruling given in one round would silently waive references first raised in the next.

### 2.7 Rulings in lightweight mode

Without a session, `validate_completion` accepts `controller_rulings` the way `validate_plan`
does: shape-checked only (`planRulings`, with its malformed-ruling advisory), rendered into
`post.tmpl`'s controller-rulings section, and applied after the review by the same waiver filter,
with the empty task key. Waived findings appear in `waived_findings`, and the summary block gets
its `ruling:` lines, as with a session.

`finding_responses` still need the session that raised the findings: the server has no earlier
finding to show the reviewer. Their advisory now says so and points to the remedy: fix the
finding, or have the controller pass `controller_rulings`, which work without a session.

### 2.8 Malformed diffs are rejected before review

Before review, `final_diff` or the contents of `final_diff_path` are checked hunk by hunk. Within
one file section, each hunk's old and new start lines must come after the previous hunk's end.
A violation cannot come from git, which never emits two files' hunks under one header or
overlapping hunks in one file. The call is rejected with the existing pre-review
`malformed_evidence` shape, naming the file and the offending hunk header, with the suggestion
"regenerate with `git diff <base>..HEAD`, and pass the file as `final_diff_path`". Combined-diff
sections (`@@@`) and sections with no parseable hunk header are not checked. A section that
repeats a file, as `git log -p` output does, is checked on its own.

### 2.9 DONE with an open finding

When a `validate_completion` result, with or without a session, still carries a critical or major
finding outside the submission-defect categories, and neither `escalate` nor
`submission_defect_only` already prefixes `next_action`, it is prefixed: "Do not report DONE:
N critical/major finding(s) remain open (<ids>). Fix and re-validate, or ask the controller for a
ruling."

### 2.10 A pointer to `implementation_guidance`

When a `validate_task_spec` envelope carries `implementation_guidance`, its `next_action` ends
with "Read `implementation_guidance` before writing code."

### 2.11 Lightweight example

`examples/lightweight-dispatch.md` (no byte cap) shows `plan_run_id` and `task_index`, the
`git diff` recipe with `final_diff_path`, and that a controller ruling works without a session.

---

## Part 3 — lean coherence

Every change here is prompt text. Each ships only if its replay fixture moves, under the criterion
in §3.5. A change that does not move its fixture is reverted, as 0.22.0's deletions walk was.

### 3.1 A plan's `reuse:` carries its justification to the task

In `plan_lean_rules.tmpl`, a `reuse:` instance asking a later task to reuse what an earlier task
introduces must give, in `suggestion`, the `Context:` line to add to the introducing task, naming
the consuming task and the symbol ("Task 2 Context: `X` is shared; Task 3 reuses it."). The
completion review already treats structure that `Context:` justifies as deliberate.

### 3.2 The completion review's one-caller test

In `post.tmpl`:

- `yagni:` "a layer with one caller" does not apply to a symbol that an exit contract names, or
  whose later consumer the task's `Context:` or Non-goals name.
- "Never flag … anything explicitly requested" becomes "anything the Goal or `Context:` asks for".
  Structure an acceptance criterion mandated is still reported, addressed to the plan author, as
  the next paragraph already says.

### 3.3 Test-side seams first

- In `pre.tmpl` item 2 and the plan templates' verification guidance, a finding that asks for
  behaviour to be pinned directly gives the test-side route first, prefixed `test-side:`: assert
  through the composed path, or add a seam in the test sources. It proposes a new production
  symbol only when no test route exists, and says why. The prefix makes the change measurable
  (§3.5).
- `post.tmpl` lists the session's `testability_extractions`, when there are any, among the things
  it never flags. The spec review already accepted them as deliberate.

### 3.4 `reuse:` can see sibling code

`validate_completion` gains `context_paths`, with and without a session, resolved as in §2.3. The
files render as related code that is not part of the change. `post.tmpl`'s `reuse:` also fires
when an attached related file already has the helper. The prompt states that a related file is
never evidence that an acceptance criterion is met. The input is documented in the tool
description and the examples; it enters `controller.md` only if the byte trim leaves room.

### 3.5 Replay gate

- **Harness.** An expectation gains `absent: true`: a run meets it when **no** reviewer finding
  matches. Relative `context_paths` and `final_diff_path` in a fixture resolve against the
  fixture's directory, and the harness sets `ANTI_TANGENT_PLAN_ROOTS` to cover it.
  `replay_test.go`'s fixed count of lean fixtures is updated.
- **Fixtures**, synthetic and written for this repository (it is public; nothing comes from the
  assessed run), in `internal/mcpsrv/testdata/replay/lean/`:

  | Fixture | Measures | Expectation |
  |---|---|---|
  | `shared-helper.json` | §3.2 | a helper with one caller in the diff, which `Context:` and an exit contract name for Task 3, draws no `over_building` naming it (`absent`) |
  | `ac-mandated.json` | existing rule | a one-caller production function an AC mandates is reported under `over_building`, addressed to the plan author |
  | `testability-extraction.json` | §3.3 second half | a helper declared in `testability_extractions` draws no `yagni:` naming it (`absent`) |
  | `pin-directly.json` | §3.3 first half | a `validate_task_spec` whose AC needs a restore pinned and whose verification exercises it only end to end draws a finding with `test-side:` |
  | `reuse-sibling.json` | §3.4 | a diff re-implementing a helper from an attached related file draws a `reuse:` naming it |

  `ac-mandated.json` measures a rule that already exists and missed once in the field: when its
  v0.24.0 baseline already meets the criterion, no prompt change is made for it.
- **Ship criterion, per change:** the v0.24.0 baseline shows the problem; after the change the
  expectation is met in at least 4 of 5 runs; `over-built.json` and `lean.json` do not drop. Every
  hit is judged by its recorded match text, not the keyword count.
- **Not replay-gated:** §3.1's plan-side wording, because the harness does not replay
  `validate_plan`. It ships on golden tests and review. §3.2 does not depend on it: a controller
  who writes the `Context:` line by hand gets the same completion behaviour.
- **Spend** is estimated and approved before any paid run.

## Part 4 — the task-order false positive

A field report on v0.24.0: `validate_plan` with `repo_root` raised a **major**
`task_order_contradiction` saying an existing file "does not exist", on three rounds running, and a
`controller_rulings` entry naming the finding's id did not waive it. Every change here is
deterministic Go; there is no replay gate.

### 4.1 A line list with spaces is an anchor

`lineAnchorRe` accepts a comma-separated list of lines and ranges only with no space after a comma.
`a.go:6-22, 29` kept its suffix, the disk tier statted `a.go:6-22, 29` and reported it missing.
The anchor admits optional whitespace after each comma, and a trailing `, …` or `, ...` that
elides the rest of the list. Everything the strip already leaves alone stays alone: a Windows drive
letter, a URL, `line:col`, a colon followed by anything but digits.

### 4.2 Every path on a Files bullet is read

`cleanRefPath` returns the first backtick span of a bullet and drops the rest, so on
``- Modify: `a.go`, `b.go` `` the check never looks at `b.go` — neither on disk nor against the
Create: bullets of later tasks, which hides a genuine ordering violation whenever it is not first
on its line.

A Files bullet yields every path in its **leading path list**:

- **Backticked:** the spans from the first one onward, as long as what separates two spans is only
  commas, semicolons, `&`, `+`, whitespace or the word `and`. The first span followed by anything
  else ends the list, so a description — ``- Modify: `a.go` (replace the `Foo` stub)`` or
  ``- Modify: `README.md` — add a row under `## Configure` `` — never turns a code span into a
  path. This repo's own plans carry hundreds of bullets with more than one backtick span, most
  of them prose of exactly that shape.
- **Unquoted** (no backtick on the line): the comma-separated pieces. The first piece contributes its
  first word, as before; a later piece counts only when it is a single word, and the list ends at
  the first that is not. A piece that is only line numbers (`29`, `40-50`, `…`) continues the
  previous path's anchor and is skipped.

Each path is cleaned as today (trailing parenthetical, line anchor, `path.Clean`), and a path
repeated on one bullet counts once. A bullet's verbs apply to every path on it.

Three shapes are dropped rather than checked, because reading them as paths would add false
positives the check never raised before. The first two occur in this repo's own plans:

- **A pattern** — a path containing `*`, `?` or `{` (`testdata/pre_*.golden`,
  `{pre,post}.tmpl`). It names a set of files, not one; statting it reports a file that does not
  exist. This applies to the first path of a bullet too.
- **A sibling shorthand** — a path with no `/` that follows, on the same bullet, a path that has a
  directory: `` `internal/prompts/testdata/pre_basic.golden`, `post_basic.golden` `` means a file
  beside the first, and the root file it would otherwise be statted as does not exist. Whether a
  bare name means a sibling or a root file (`README.md`) cannot be told from the text, so it is not
  checked at all, which is what it was before.
- **A word that is not a file name** — an item containing whitespace or parentheses, `etc.`, or a
  later item with neither `/` nor `.`: `` `main.go` and `Run` ``, `` `main.go`, `run()` ``. A code span
  naming a symbol would otherwise be statted as a missing file. The cost is that a first path
  containing a space is not checked either.

### 4.3 A ruling waives the deterministic finding

`validate_plan`'s schema says a ruling "waives every finding with the same id". `applyPreLadder`
waived before it appended the file-consistency finding, so the one finding a controller had
proven false could not be ruled away, and it held the verdict down every round.

The waiver runs after the finding joins the list, so a ruling covers it like any other plan-level
finding and it moves into `waived_findings`. The clamp advisory still joins after the waiver: it
describes this call's budget, not the plan. The finding's id is its fingerprint of category,
plan scope and criterion — not its evidence — so it is the same every round, and a ruling keeps
matching as the plan is edited.

**Cost, accepted (Patrick, 2026-09-23):** the check reports every violation in one finding, so a
ruling on it also covers a genuine violation that a later round adds. The waived entry keeps the
full evidence, and the summary block shows it on its `waived:` line.

### 4.4 Tests

- `planparser`: `a.go:6-22, 29`; `a.go:1-2, 5, …`; a backticked and an unquoted multi-path bullet;
  a bullet whose later spans are prose (only the first span is a path); an unquoted list with a
  line-number continuation; a repeated path; a glob and a brace pattern (dropped); a sibling
  shorthand (dropped) next to a slash-free list (`go.mod`, `go.sum`, kept); the existing
  drive-letter, URL, `line:col` and anchor-only cases.
- `checkFileConsistency`: a multi-path bullet of existing files with comma-space anchors draws no
  finding; a multi-path bullet whose second path a later task creates is reported.
- `validate_plan`: a ruling on the finding's id waives it into `waived_findings` with its evidence
  and lets the verdict pass; the id a first round reports is the id the waived entry carries on the
  next round, after the evidence changed.

---

## Input and envelope changes

| Tool | New input | Part |
|---|---|---|
| `validate_task_spec` | `task_index`, `context_paths` | 1, 2 |
| `validate_completion` | `plan_run_id`, `task_index`, `task_title` (lightweight only); `controller_rulings` without a session; `context_paths` | 1, 2, 3 |
| `codescene` argument | `base_ref` | 1 |

| Result | Change | Part |
|---|---|---|
| `plan_run_report` | rows per plan task; `#` is the task number; attempts and `lite` columns; `pre:` verdicts; unmatched and never-dispatched lists; branch net problem points | 1 |
| plan-level finding IDs | new scope key; every plan-level ID changes once | 1 |
| `validate_task_spec` `next_action` | implementation-guidance pointer | 2 |
| `validate_completion` `next_action` | "Do not report DONE" prefix | 2 |
| `validate_plan` `next_action` on a checklist-only pass | leads with "dispatch" | 2 |

## Protocol text

- `controller.md` step 6: pass `plan_run_id` to each implementer's `validate_task_spec`, or its
  `validate_completion` for a lightweight task, and name the task's brief in `context_paths`. The
  part has 75 bytes of headroom, so the same commit trims the part to fit.
- No change to `implementer.md` or `core.md`.
- The plugin bundle is resynced in the same commit, as CI requires.

## Testing

- `go test -race ./...` for every change; unit tests never touch the network. Prompt changes
  regenerate golden files, reviewed before commit.
- **Part 1:** task resolution by index, by normalized title, ambiguous titles, out-of-range index
  (advisory, verdict unchanged) and the unmatched fallback; upsert on re-validation, attempts, an
  old session still updating its row; lightweight rows with and without a live run; completed,
  never-dispatched and unmatched counts, and the pass/warn/fail counts, over the assessed run's
  shape (5 spec attempts on Task 1, three lightweight tasks, one task never run); CodeScene totals
  over rows sharing a base ref, over two base refs, and without base refs; missing CodeScene only
  on completed rows; ledger round-trip of spec-time and lightweight rows, and older lines without
  the new fields; `review_ms` on each truncation path; plan-level IDs differing from session IDs
  for the same category and criterion; a plan-level ID in `validate_completion`'s rulings getting
  the unknown-ID advisory; the report golden.
- **Part 2:** the new `verification` and `controller_verified_references` caps at and over their
  limits; `context_paths` on `validate_task_spec` through roots, symlinks and the byte caps, and
  in the pre-prompt golden; the retry once and only once, not with an override, not at the
  ceiling, with `review_ms` over both attempts; the alias skip for `NET` and `MAIN/x`, and no skip
  for an existing `VERSION` or a lowercase missing path; the checklist-only `next_action`;
  lightweight rulings waiving, malformed rulings advised, and responses advised; the hunk-order
  check against real `git diff` output (never rejected), the concatenated two-file shape and
  overlapping hunks (rejected), and combined diffs (skipped); the DONE prefix and its precedence
  under `escalate` and `submission_defect_only`; the guidance pointer; schema contract rows for
  every new input and limit.
- **Part 3:** golden files for every template change; `absent` expectations in the harness unit
  tests; then the replay gate.
- **Part 4:** the cases in §4.4; no replay gate.
- Protocol parts stay under 16,000 bytes, `INTEGRATION.md` under 2,000, and the plugin bundle
  identical to `docs/protocol/`, as CI enforces.

## Compatibility

- Every new input is optional. A caller that sends none of them gets the corrected report, the
  new `next_action` texts, the higher default budget and the diff check, and nothing it already
  sends changes meaning.
- Plan-level finding IDs change once. A ruling a controller carries across the upgrade on a
  plan-level finding silently stops matching (`validate_plan` checks a ruling's shape only), so
  the finding reappears under its new ID and needs ruling again. Plan per-task and session IDs are
  unchanged.
- The `plan_run_report` layout changes: `#` becomes the plan's task number and the counts are per
  task. Ledgers written by earlier versions still load; without titles, title matching and the
  never-dispatched names are unavailable for that run.
- The default per-task budget doubles. `ANTI_TANGENT_PER_TASK_MAX_TOKENS` still overrides it; cost
  follows tokens used, and a truncated call now costs up to one extra review.
- A hand-assembled diff that git could not have produced, previously reviewed, is now rejected
  before review. That is the intended change.

## Release

All three parts ship as **one** minor release, and nothing reaches `main` before Part 3 is done.

- **One CHANGELOG entry**, `## [0.25.0]`, dated the day the release pull request merges; each
  part adds its own lines to it.
- **Branches.** Part 1 is developed on `version/0.25.0`. Parts 2 and 3 are developed on
  `version/0.25.0-part2` and `version/0.25.0-part3`, each branched from the previous part's tip,
  and each part's pull request targets `version/0.25.0`. CodeRabbit does not auto-review a pull
  request whose base is not `main`, so each round is triggered with `@coderabbitai review`.
- **The release** is one pull request from `version/0.25.0` to `main`, titled with `[minor]`.
  `release.yml` bumps `VERSION`, validates the entry, tags and publishes. `VERSION` is not bumped
  on the branch.
- **Process.** This work changes anti-tangent's own review behaviour, evidence handling and
  plan-run bookkeeping, so its plans' tasks are not gated with anti-tangent. The gate is the task
  review plus the whole-branch review, with CodeScene and `go test -race` in every implementer
  brief. Each part gets its own implementation plan.

## References

- Code: `internal/planrun/planrun.go` (`Run`, `AppendRow`, `UpdateRow`), `internal/planrun/report.go`
  (`Totals`, the summary lines), `internal/planrun/ledger.go` (`Load`),
  `internal/codescene/codescene.go` (`Digest`), `internal/mcpsrv/finding_ids.go`
  (`assignPlanIDs`), `internal/mcpsrv/plan_normalize.go` (`waivePlanFindings`,
  `calibratePlanVerdictForUnverifiableOnly`), `internal/verdict/identity.go` (`Fingerprint`),
  `internal/mcpsrv/finding_rulings.go` (`planRulings`, the no-session advisory),
  `internal/mcpsrv/task_spec_input.go` (list caps), `internal/mcpsrv/context_files.go`,
  `internal/mcpsrv/file_consistency.go` (disk tier), `internal/mcpsrv/review_error.go`
  (`runReview`), `internal/mcpsrv/handlers.go` (`ValidateTaskSpec`, `ValidateCompletion`,
  `checkEvidenceShape`, the truncation paths), `internal/config/config.go`
  (`PerTaskMaxTokens`, `MaxTokensCeiling`).
- Templates: `internal/prompts/templates/post.tmpl` (Over-building), `pre.tmpl`, `lean.tmpl`,
  `plan_lean_rules.tmpl`, `context_files`.
- Harness: `internal/mcpsrv/replay_harness_test.go`, `replay_e2e_test.go`, `replay_test.go`,
  `internal/mcpsrv/testdata/replay/lean/`.
- Earlier designs: `2026-09-15-field-assessment-improvements-design.md` (finding identity,
  rulings, the replay gate), `2026-09-18-lean-by-default-design.md` (the lean ruleset and
  `implementation_guidance`), `2026-09-10-anti-tangent-v0.20.0-design.md` (lightweight mode).
