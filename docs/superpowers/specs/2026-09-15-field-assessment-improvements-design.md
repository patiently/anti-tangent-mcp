# anti-tangent-mcp — a self-describing tool surface, converging review loops, and reviewer recall

**Status:** design
**Date:** 2026-09-15
**Source:** a field assessment of one 18-task plan run in a consumer project: 34 anti-tangent
calls across 8 `validate_plan` rounds and the first three tasks. Consumer identifiers are withheld;
tool names, categories, env vars and model IDs are kept verbatim.

## Summary

The run surfaced three kinds of cost, and they need three different fixes, so this design is split
into three parts. Each part merges to `main` when it is finished; all three ship together as one
minor release.

1. **Calls that never reached the reviewer.** 8 of the 34 calls failed on the caller's own
   arguments: a diff path outside `ANTI_TANGENT_PLAN_ROOTS` (twice), `payload_too_large` (twice),
   the `codescene` argument's shape (three different wrong guesses), and a 500-character reference
   cap. None of these constraints is described in the tool schema. Every field description today
   is either empty or the literal word `required`, because a `jsonschema:"required"` tag is read as
   the description. **Part 1** makes the schema and the responses self-describing.
2. **Review loops that did not converge.** One task spent 12 `validate_completion` calls — 8 of
   them reviewed, all `fail` — re-raising the same two findings about code a later task owns. The
   controller had ruled on both, and the implementer's summary cited the rulings. Every call is
   judged from scratch: prior findings are stored and never read, no input carries a ruling, and
   the protocol has no path to DONE while a wrong finding repeats. The implementer finally
   reported done without a passing call. Plan review showed the same shape: a rolled-up reference
   checklist recurred in 6 of 8 rounds. **Part 2** gives findings stable IDs, lets an
   implementer answer a finding once, escalates a rejected answer to the controller, and lets a
   controller ruling settle the finding deterministically.
3. **Real issues the reviewer missed.** About nine existing comments still naming symbols the diff
   deleted, a task whose Non-goals contradicted its own verification gate, and a state-set guard
   that no longer covered two retired states. **Part 3** changes the prompts and adds a
   deterministic removed-symbol hint, gated on a replay that shows each change helps.

## Verification of the assessment

Each claim in the assessment was checked against v0.21.0 and against the run's transcripts before
it was accepted. Four did not hold, and one of those changed the design.

| Assessment claim | What verification found |
|---|---|
| `plan_run_report` is unusable because the 4h TTL is too short | Wrong. The TTL slides on every access, and the durable ledger was enabled. None of the run's 4 `validate_task_spec` calls passed `plan_run_id`, so no task was ever attached, and the ledger only persists attached rows. The not-found message ("Runs expire after 4h0m0s…") pointed at the wrong cause. |
| Why `plan_run_id` was missing | The agents worked from a months-old copy of the protocol imported into the host's user-scope `CLAUDE.md`, which predates `plan_run_id` and `plan_path`. The current protocol plugin was installed but never loaded. That also explains the `codescene` shape guesses. It is an environment fault, fixed outside this repo — and it is why Part 1 moves everything a caller needs into the schema and the responses, which are always current. |
| Raise the 200 KB `validate_completion` payload cap | The cap is deliberate (CHANGELOG) and operator-tunable through `ANTI_TANGENT_MAX_PAYLOAD_BYTES`. The defect is the error's advice to "split into smaller chunks": each call is reviewed alone, and following that advice produced the `insufficient_evidence` loop. |
| Pass `controller_verified_references` to `validate_plan` | That input does not exist on `validate_plan`. Part 2 adds it. |
| The reviewer's output budget truncated large reviews | No. None of the 16 calls that reached the reviewer in the first two tasks returned `partial`. |

## Non-goals

- The server stays advisory. Nothing here blocks a call or corrects code; escalation is a signal
  in the envelope, and rulings change which findings count, not what the implementer may do.
- No prior-round memory in `validate_plan`. Each round stays a fresh full read, so the guidance
  that "a pass on round N is not an audit of rounds 1..N-1" stays true.
- No verification that a controller ruling was written by a controller. The design makes a forged
  ruling visible at DONE, not impossible.
- No change to how `plan_quality` is computed.
- No dangling-reference check on the implementer's report ("see below"): the server sees only the
  `summary` argument, never the DONE report, and the check would be mostly false positives.
- No separate post-review output token budget (no evidence it is needed; see the table above).

---

## Part 1 — a self-describing tool surface

**Principle:** everything an agent needs to make a valid call is in the tool schema or in the
response the call returns. Protocol copies go stale; the schema and the responses come from the
running server.

### 1.1 Schema field descriptions

- Replace the 15 `jsonschema:"required"` tags with real descriptions, and describe every untagged
  field, across all nine tools. Required-ness comes from `omitempty`, so it does not change.
- Descriptions state each limit: `controller_verified_references` and `pinned_by` take at most 50
  entries of at most 500 characters; path fields are absolute and, when `ANTI_TANGENT_PLAN_ROOTS`
  is set, must be under one of its roots; evidence fields count toward the payload cap, with the
  cap's value.
- **Contract test:** start the server in-process, read `tools/list`, and fail if any input
  property has an empty description or the literal `required`, or if a limit stated in a
  description differs from the Go constant that enforces it.

### 1.2 The `codescene` argument

- The inferred schema sets `additionalProperties: false` on every struct (a `jsonschema-go`
  default nobody chose), so one unexpected key rejects the whole `validate_completion` call. The
  field is declared as an open object and decoded by the server; unknown keys are ignored.
- The server recognises the raw output of CodeScene's `analyze_change_set` and reduces it to the
  digest, using the mapping already documented in `docs/team-setup/codescene-stats.md`:
  `quality_gates` → `quality_gate`, `len(results)` → `files_analyzed`, a tally of
  `results[].verdict` → `verdicts`, Σ(`new-pp` − `old-pp`) → `net_pp`.
- The field description gives the digest shape, says raw `analyze_change_set` JSON is accepted,
  and says `pre_commit_code_health_safeguard` sees only uncommitted changes — after a commit it
  reports zero files, which is not a CodeScene run of the task.

### 1.3 A plan run's tasks get attached

- **Advisory.** When `validate_task_spec` omits `plan_run_id` while this server holds a live plan
  run, the result gains one minor `other` finding naming the most recently created live run:
  "pass plan_run_id=pr_… so plan_run_report can include this task". It is added after the verdict
  is finalized, exactly like `validate_plan`'s existing `plan_text` deprecation and `repo_root`
  advisories, so it cannot change the verdict. The most recent run is named because every
  `validate_plan` round mints a new ID and earlier rounds' IDs are superseded.
- **Ledger header.** `validate_plan` appends a run header (ID, plan verdict, plan quality, task
  count, created time) to `plan-runs.jsonl` when the ledger is enabled. `Ledger.Load` returns a
  header-only run, so `plan_run_report` can say "known run, 0 tasks attached" instead of
  "not found". The header carries no task titles.
- **Not-found evidence** lists the causes in order of likelihood: no `validate_task_spec` call
  passed this ID; the run was idle past the TTL; a different or restarted server with no ledger.

### 1.4 Errors that say how to recover

- **Roots.** The `final_diff_path` / `final_files` roots error names the configured roots and
  gives a recipe for writing evidence under one of them, noting that a per-session `/tmp`
  scratchpad is outside them. The same sentence goes into the tool description.
- **`payload_too_large`.** The suggestion no longer says "split into smaller chunks". It states the
  measured size and the cap, and lists what helps: a diff with `-U1`; excluding generated,
  lockfile and snapshot files; not sending a file in both `final_diff` and `final_files`; and, for
  the operator, `ANTI_TANGENT_MAX_PAYLOAD_BYTES`.

### 1.5 The placeholder guard's false positives

- `evidenceEllipsisLine` (`(?m)^\s*\.\.\.\s*$`) matches an unchanged ` ...` line inside a unified
  diff, because `\s*` consumes the context-line space, and matches a Python stub body in
  `final_files`. In `final_diff`, a line that begins with a space or `-` (an unchanged or removed
  line) is not checked; every other line is. In `final_files`, a bare `...` line in a `.py` or
  `.pyi` file is exempt. Every other placeholder
  pattern still applies to both.
- `core.md` advises sending a complete `final_diff` when a file genuinely contains the pattern;
  the diff is scanned too, so that advice is removed.

---

## Part 2 — converging review loops

### 2.1 Finding identity

Every finding in a `validate_task_spec`, `check_progress`, `validate_completion` or
`validate_plan` result carries a server-assigned `id`, server-generated findings included.

- `id = "f_" + hex(sha256(category + "\x1f" + task_key + "\x1f" + criterion_key))[0:8]`.
- `criterion_key` is the criterion lowercased, with runs of whitespace collapsed to one space,
  trimmed, and trailing `.`, `:`, `;` and `,` removed.
- `task_key` is empty for session tools. For a `validate_plan` task finding it is the task title
  with a leading `Task <n>:` removed, normalized the same way. Plans get renumbered between
  rounds — the assessed plan went from 17 to 18 tasks — and a numbered key would change the ID of
  every task after the insertion.
- A second finding with the same ID in one response gets `-2`, a third `-3`, in emitted order.
- IDs appear in envelope findings and on every finding line of the summary block.

The per-task reviewer schema (`internal/verdict/schema.json`) gains `same_as`: a required,
nullable string. Required because OpenAI strict mode demands every property be listed; nullable so
a finding with no predecessor is `null`. The plan schemas are unchanged. A `same_as` that names no
known ID is treated as `null`.

### 2.2 `validate_completion` memory

The session stores:

- the findings of the most recent `validate_completion` call that reached the reviewer, after the
  ruling filter (§2.6) and with IDs — reviewer-parsed findings only, since server findings are not
  judgement calls to answer. A call that never reaches the reviewer (an argument error,
  `payload_too_large`, `malformed_evidence`) does not replace them; and
- the set of every finding ID issued in the session, by any of its tools.

"The previous call" below means that stored call.

There is no cap on calls. The stored findings are replaced, not accumulated. The ID set holds
deterministic fingerprints, so a repeat adds nothing, and only a genuinely new finding adds an
entry. Growth is therefore bounded by the number of distinct findings a reviewer produces, each
behind a paid call — not by the session TTL, which slides on every access and so never expires an
active session.

`post.tmpl` gains the "Prior findings" section the original design specified and never shipped.
It lists the previous call's findings with their IDs, each followed by this call's response to it
when one was given. The reviewer is told, per prior finding: omit it if the evidence now satisfies
it or the response is correct; otherwise re-raise it with `same_as` set and evidence that answers
the response directly. "The summary on its own is not evidence" still applies — a response is an
argument the reviewer must engage, not evidence.

### 2.3 `finding_responses`

New optional input on `validate_completion`: `finding_responses: [{finding_id, response}]`, at
most 50 entries of at most 2000 characters. Each `finding_id` must name a finding from the
previous call. An unknown ID is ignored and draws one minor `other` advisory after finalization.

### 2.4 Repeat and escalate

After the ruling filter (§2.6):

- a finding whose `id` or `same_as` matches a prior finding answered in this call's
  `finding_responses` is marked `repeat_of: <prior id>`;
- if any critical or major finding is marked `repeat_of`, the envelope sets `escalate: true`, the
  summary block gains an `escalate: true` line, and `next_action` is prefixed with server text:
  "Stop resubmitting: report f_… and your responses to your controller for a ruling, then
  resubmit with controller_rulings."

Escalation fires on the first rejected answer. The reviewer has read the response and restated
the finding; another resubmission without a code change or new evidence is the loop. A
resubmission with no responses is not a repeat — nobody disputed anything.

### 2.5 `controller_rulings` on `validate_completion`

New optional input: `controller_rulings: [{finding_id, ruling}]`, at most 50 entries of at most
2000 characters.

- Each `finding_id` must be in the session's issued-ID set; an unknown ID is ignored with one
  minor `other` advisory after finalization.
- Rulings persist on the session. A later call applies them without resending; sending an ID
  again replaces its ruling text. There is no revocation — a new `validate_task_spec` session
  starts clean. Persisting avoids the failure class behind the missing `plan_run_id`: a value that
  has to be carried forward by hand, and gets dropped.
- `post.tmpl` renders a "Controller rulings" section marked authoritative: do not re-raise a ruled
  concern under any category. If new evidence shows a ruling does not cover a problem, the
  reviewer raises a new finding quoting the ruling and the new evidence, with `same_as: null`.
  That is the deliberate escape hatch for a real regression hiding behind a ruling.

### 2.6 The filter pipeline

In this order, on the findings parsed from the reviewer:

1. assign IDs (§2.1);
2. move every finding whose `id` or `same_as` matches a ruled ID into `waived_findings`, each
   entry `{id, severity, category, criterion, ruling}`;
3. mark repeats and set `escalate` (§2.4);
4. append server-generated findings and finalize the verdict from what remains;
5. append post-finalization advisories.

Only reviewer-parsed findings can be waived. Findings the server creates — `payload_too_large`,
`malformed_evidence`, `codescene_not_run`, `codescene_skipped`, `session_not_found`, the
rolled-up reference checklist, and every advisory — are submission or bookkeeping signals that a
resubmission fixes, not judgement calls, and never reach step 2.

### 2.7 Envelope, summary block and guard

- Envelope: `findings[].id`, `findings[].repeat_of` (omitempty), `escalate` (omitempty),
  `waived_findings` (omitempty).
- Summary block: each finding line carries its ID; each waived finding gets a line
  `waived: <id> <severity>/<category> ruling: "<ruling>"`, with the ruling passed through
  `escapeBlockValue` and truncated to 200 characters.
- The implementer pastes the block into its DONE report, so every waiver is in front of the
  controller who supposedly issued it. `anti-tangent-guard` parses only the header's `tool:`,
  `session_id:` and `verdict:` lines; `summary_contract_test.go` and `summary_forgery_test.go` are
  extended to prove a ruling or response string cannot forge a header line.

### 2.8 `plan_run_report`

Each task row gains `waived` (count) and `escalated` (whether any call on the session escalated),
and the report table shows both, so the end-of-plan view names the tasks that closed on rulings.

### 2.9 `validate_plan`

- **IDs** per §2.1, fingerprint only. No prior findings are rendered, so there is no `same_as`.
- **`controller_rulings`** (same shape and limits). The tool is stateless and each round mints a
  new `plan_run_id`, so rulings are resent every round; the controller writes and sends them
  itself, with no subagent in between. They are rendered as authoritative in every plan prompt,
  chunked or not, and matched findings move to `waived_findings` on the plan result (plan-level and per task),
  with `waived:` lines in the summary. IDs cannot be validated without a session, so a ruling that
  matches no finding in a round draws one minor `other` advisory after finalization — the signal
  that the reviewer reworded the finding and it needs a new ruling.
- **`controller_verified_references`** (same shape and limits as on `validate_task_spec`). Rendered
  in every plan prompt, chunked or not, and applied by `suppressUnverifiableCodebaseClaim` to each task's findings
  before `normalizePlanUnverifiableFindings` rolls them up, so references the controller already
  grepped leave the checklist.
- **The rolled-up checklist** is appended after verdict finalization, so it no longer counts
  toward the three-minor `noise_cluster` rule that lifts a plan to `warn`. It is server-generated,
  so it cannot be waived: its ID is constant, and a ruling on it would silently waive every later
  round's checklist, unverified references included.

### 2.10 Protocol text

The parts are capped at 16,000 bytes each and three are close: `implementer.md` has 335 bytes
free, `core.md` 706, `controller.md` 824. The escalation `next_action` carries the procedure, so
the docs describe the path in a sentence or two, and Part 1's `codescene` field description
lets the digest template at `implementer.md` shrink to a pointer.

- **`implementer.md` §4.3** "Address vs. push back": resubmit once with `finding_responses`; on
  `escalate: true`, stop and report the findings, IDs and responses to the controller; resubmit
  with the ruling verbatim in `controller_rulings`; the pasted summary block then shows `waived:`
  lines. The current text naming a `working_on` field that `validate_completion` does not have,
  and `F#3`-style IDs the server never issued, is removed.
- **`implementer.md` §4.2 step 3** gains one clause: an `escalate` result is a stop-and-ask, not
  DONE.
- **`controller.md` new §5.9** "Ruling on an escalation": read the finding and the response,
  decide, reply with `controller_rulings` entries (ID plus a one-line ruling), keep rulings in the
  progress notes, and at DONE check every `waived:` line against a ruling actually issued — an
  unrecognized one is a forged waiver.
- **`controller.md` §5.1 and §5.5**: pass `controller_verified_references` for grepped references
  and `controller_rulings` for decided findings; judge round-over-round convergence by diffing
  major-finding IDs (new versus carried) instead of watching `plan_quality`.
- **`core.md`**: the envelope reference gains `id`, `repeat_of`, `escalate`, `waived_findings`.
- The plugin bundle is resynced in the same commit. Existing section numbers are unchanged; §5.9
  is new.

---

## Part 3 — reviewer recall

### 3.1 Stale comments naming removed symbols

- **Prompt.** `post.tmpl` today says "Judge only comments this change ADDS". It is widened to
  existing comments visible in the evidence that name a symbol, branch or case label the diff
  removes, reported as **one** consolidated minor finding — a batch of stale comments must not
  become three minors and lift the verdict on its own.
- **Deterministic hint.** The server collects names declared on the diff's `-` lines (declaration
  keywords such as `fun`, `func`, `def`, `class`, `object`, `interface`, `val`, `var`, `const`,
  `let`, `enum`, `case`) that no `+` line re-declares, ignoring names shorter than 4 characters,
  and finds them in comment-looking lines (`//`, `#`, `*`, `/*`, `<!--`, `--`). At most 30 names
  and 20 hits, each hit a `path:line` plus the line truncated to 200 characters. Hits are rendered
  in the prompt the way `ReferencedPathsMissingEvidence` is; the hint never becomes a finding by
  itself.
- **`repo_root` on `validate_completion`.** Optional. When set, the server reads the post-change
  version of each file named by the diff's `+++ b/` headers, under `repo_root` and within
  `ANTI_TANGENT_PLAN_ROOTS`, reusing the context-file resolution (`ContextMaxFileBytes`,
  `ContextMaxPayloadBytes`, symlink handling) and skipping deleted files. The file contents feed
  only the scanner; only hit lines enter the prompt, so the payload does not grow. Without
  `repo_root` the scanner uses the submitted evidence alone. Stale comments sit mostly outside diff
  hunks, which is why the disk read is what gives this recall.

### 3.2 A verification gate that contradicts a Non-goal

- **Pre-task and plan check.** `pre.tmpl` and `plan_tasks_chunk.tmpl` gain a fourth check: does a
  step or verification gate ("no new warnings", "compiles", "lint clean") force work a Non-goal
  defers? If so, `ambiguous_spec` at major, quoting both.
- **`verification` input on `validate_task_spec`.** Optional, at most 50 entries of at most 500
  characters: the task's steps and verify commands, which today never reach the per-task review.
  Stored on `TaskSpec` and rendered in both the pre and post prompts.
- **Forced deviations post-task.** `post.tmpl` currently turns a non-goal violation into
  `scope_drift`. When the violation is forced by a gate quoted from the spec fields — never from
  the summary — the reviewer emits one `ambiguous_spec` against the spec instead. Non-major
  pre-task `ambiguous_spec` findings are carried into the post prompt; today only majors are.

### 3.3 Deletions walk

`post.tmpl` asks the reviewer, for each branch, case or state the diff removes, to name what it
handled and check the guards, sets and `when`-style dispatches visible in the evidence that
enumerate those states. Minor unless the evidence shows a regression. This is the least certain
change in this part; §3.4 decides whether it stays.

### 3.4 Replay gate

- **Harness.** A generic, `e2e`-tagged replay that reads fixtures from a directory named by
  `ANTI_TANGENT_REPLAY_DIR` (skipped when unset). A fixture holds recorded `validate_task_spec`
  and `validate_completion` arguments and a list of expectations `{call, any_of_keywords}`. Each
  fixture runs 5 times against the configured reviewer; the harness reports, per expectation, how
  many runs produced a finding matching any keyword.
- **Fixtures** for the assessed run are extracted from its transcripts and saved diffs. They are
  consumer code, so they live outside this repository and are never committed.
- **Ship criterion, per change:** the target issue is named in at least 3 of 5 runs after the
  change and in fewer runs before it, and the final passing diff of the task that ended in `pass`
  draws no new critical or major finding.
- **Spend** is estimated and approved before any paid run.

---

## Input and envelope changes

| Tool | New input | Part |
|---|---|---|
| `validate_task_spec` | `verification` | 3 |
| `validate_completion` | `finding_responses`, `controller_rulings`, `repo_root` | 2, 2, 3 |
| `validate_plan` | `controller_rulings`, `controller_verified_references` | 2 |

| Result | New field | Part |
|---|---|---|
| every finding | `id`, `repeat_of` | 2 |
| `validate_completion` envelope | `escalate`, `waived_findings` | 2 |
| `validate_plan` result | `waived_findings` (plan-level and per task) | 2 |
| `plan_run_report` rows | `waived`, `escalated` | 2 |

## Testing

- `go test -race ./...` for every change; unit tests never touch the network.
- **Part 1:** the `tools/list` contract test; codescene decoding of the digest shape, the raw
  `analyze_change_set` shape and unknown keys; the `plan_run_id` advisory present with a live run,
  absent without one, and never changing the verdict; ledger header round-trip and the
  header-only report; placeholder guard cases for diff context lines, `+` lines, Python stubs and
  the non-exempt patterns.
- **Part 2:** ID stability across criterion whitespace and case, task renumbering and
  in-response duplicates; the filter pipeline order, including an unwaivable server finding with a
  matching ruling; repeat detection by `id` and by `same_as`; escalation only on a critical or
  major repeat; ruling persistence and replacement; unknown-ID advisories; summary forgery tests
  with hostile ruling and response text; `validate_plan` rulings, the unmatched-ruling advisory,
  verified-reference suppression before rollup, and the checklist no longer lifting the verdict.
  Prompt changes regenerate golden files, reviewed before commit.
- **Part 3:** scanner unit tests over multi-language diffs (declarations removed, renamed and
  re-declared; short names; hit caps); `repo_root` resolution outside roots, through symlinks, over
  the byte caps and for deleted files; golden files; then the replay gate.
- Protocol parts stay under 16,000 bytes, `INTEGRATION.md` under 2,000, and the plugin bundle
  identical to `docs/protocol/`, as CI enforces.

## Compatibility

- Every new input is optional. A caller that sends none of them still sees Part 1's
  descriptions, errors and advisories, finding IDs, and the checklist change below; nothing it
  already sends changes meaning.
- New envelope fields are additive. Summary blocks gain IDs on finding lines and optional
  `waived:` and `escalate:` lines; the guard reads only the header.
- The per-task reviewer schema gains a required, nullable `same_as`, which every provider must
  emit. Providers already receive the schema from `internal/verdict`, so no provider code changes
  beyond what the schema invariant tests require.
- The rolled-up checklist moving after finalization can lower a plan verdict that the checklist
  alone had lifted to `warn`. That is the intended correction.

## Release

All three parts ship as **one** minor release. Each part merges to `main` as soon as it is
finished, and no release is cut until Part 3 is on `main`. Each part gets its own implementation
plan.

`release.yml` publishes a release on every push to `main`: it bumps `VERSION` by the merge
subject's marker (patch when there is none) and fails when `CHANGELOG.md` has no entry for the
result. So the mechanics are:

- **One CHANGELOG entry**, `## [0.22.0]`, for the whole release. Each part
  adds its own lines to that entry.
- **Branch.** Each part is developed on `version/0.22.0`, reused in turn (the name is free again
  once the previous part's branch is merged and deleted), so CI's changelog check runs on every
  part.
- **Parts 1 and 2** merge with `[skip ci]` in the PR title, which becomes the squash-merge
  subject. GitHub then skips the push-triggered workflows on `main`, `release.yml` included. The
  pull request's own CI still runs on the branch before merge; `ci.yml`'s push run on `main` is
  skipped for these two merges.
- **Part 3** merges with `[minor]` and without `[skip ci]`. `release.yml` bumps `VERSION` to
  `0.22.0`, validates the entry, tags, and publishes.

**Hazard: another release in between.** While Part 1 or Part 2 sits unreleased on `main`, any other
merge that triggers `release.yml` publishes them early, under that merge's version and without
their release notes. Until Part 3 lands, every other merge to `main` either waits, or carries
`[skip ci]` and adds its notes to the same `## [0.22.0]` entry. That includes the separate
ponytail-plugin work that had planned to be 0.22.0 itself: it waits for Part 3, or ships inside
this release.

## References

- Design spec: `docs/superpowers/specs/2026-05-07-anti-tangent-mcp-design.md` (the unshipped
  "Prior findings" section, §Prompt structure).
- Protocol: `docs/protocol/implementer.md` §4.2, §4.3; `docs/protocol/controller.md` §5.1, §5.5.
- Code: `internal/session/session.go` (`PostFindings`), `internal/prompts/templates/post.tmpl`,
  `internal/mcpsrv/plan_normalize.go` (`normalizePlanUnverifiableFindings`),
  `internal/mcpsrv/task_spec_normalize.go` (`suppressUnverifiableCodebaseClaim`),
  `internal/mcpsrv/review_error.go` (`finish`), `internal/planrun/ledger.go`,
  `internal/mcpsrv/completion_evidence.go`.
