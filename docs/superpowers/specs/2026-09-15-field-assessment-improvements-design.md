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
  `final_files` — while an added `+...` line, the one that actually signals elided evidence, never
  matches, because of the leading `+`. In a `final_diff` that has hunk headers, an unchanged
  (leading space) or removed (leading `-`) line is not checked, and an added line is checked with
  its `+` stripped. A `final_diff` without hunk headers is checked line by line as before. A bare
  `...` line in a `.py` or `.pyi` file is exempt, whether it arrives in `final_files` or as an added
  line under that file's `+++` header. Every other placeholder pattern still applies everywhere.
- `core.md` advises sending a complete `final_diff` when a file genuinely contains the pattern;
  the diff is scanned too, so that advice is removed.

---

## Part 2 — converging review loops

### 2.1 Finding identity

Every finding in a `validate_task_spec`, `check_progress`, `validate_completion` or
`validate_plan` result carries a server-assigned `id`, server-generated findings included.

- The **fingerprint** is `"f_" + hex(sha256(category + "\x1f" + task_key + "\x1f" + criterion_key))[0:8]`.
- `criterion_key` is the criterion lowercased, with runs of whitespace collapsed to one space,
  trimmed, and trailing `.`, `:`, `;` and `,` removed.
- `task_key` is empty for session tools. For a `validate_plan` task finding it is the task title
  passed through `normalizeTaskTitle`, which removes a leading `Task <n>:`, then normalized like
  `criterion_key`. Plans get renumbered between rounds — the assessed plan went from 17 to 18
  tasks — and a numbered key would change the ID of every task after the insertion. The title is
  the plan's own heading for the task the result reports on, not the reviewer's `task_title`,
  which can drift between rounds. Two tasks whose titles normalize to the same key share
  fingerprints, so one ruling covers findings on both.
- The **display ID** is the fingerprint, with `-2`, `-3` appended to later findings that share
  it, in emitted order: findings first, then waived entries. Display IDs are assigned once per
  response by one helper, over its final list, server findings and advisories included. Session
  tools call it in the handler, after the last finding is added and before the session is written
  (§2.6), so on a session tool the IDs a caller sees are exactly the IDs the server stores.
  `validate_plan` calls it from `finish` (fresh, recovery and cache-hit paths) and from
  `finalizePlanResult`, which builds the four early exits (plan too large, context too large,
  payload too large, no task headings). `validate_plan` stores no IDs; a cache hit derives the
  same ones again. `envelopeResult` and the plan summary formatter only render.
- A response without a `session_id` — a truncated `validate_task_spec`, which creates no session,
  or a `validate_completion` call without one — still carries IDs, but they belong to no issued
  set, so a later ruling naming one is unknown (§2.5).
- IDs appear in envelope findings and on every finding line of the summary block. `id` and
  `repeat_of` are `omitempty` on `verdict.Finding`, so `prime_project_knowledge` and
  `extract_project_knowledge` results do not change.

A fingerprint is deliberately coarse. On `validate_completion` the criterion is the verbatim AC
text, so one fingerprint means "this category on this AC", and several findings routinely share
one: every comment-hygiene finding uses `criterion: comment_hygiene`, and `pre.tmpl` files every
structural finding under `criterion: spec`. Only the suffix tells them apart, and the suffix
depends on order, so it is stable only inside one stored response. §2.3 therefore matches display
IDs, because it refers to a stored response; §2.4 and §2.5 match fingerprints, because they
refer across rounds.

The per-task reviewer schema (`internal/verdict/schema.json`) gains `same_as`: a required,
nullable string (`["string", "null"]`). Required because OpenAI strict mode demands every
property be listed; nullable so a finding with no predecessor is `null`. In Go it is
`SameAs *string` with `json:"same_as,omitempty"` on `verdict.Finding`, which the full parser and
the truncation-tolerant parser both decode. The plan schemas do not list it, so no provider emits
it on a plan finding. The per-task schema is shared by all three session tools; the server reads
`same_as` only on `validate_completion` and ignores it on `validate_task_spec` and
`check_progress`. A `same_as` that names no finding rendered in the prompt — a prior finding
(§2.2) or a major pre-task finding — is treated as `null`. The server reads `same_as` for the
waiver match (§2.6 step 4) and the repeat match (step 5), then clears it, so the response never
echoes it. `repeat_of` is not a copy of it: it is set only for a prior finding answered in this
call (§2.4).

`verdict.Finding` is also part of `extract_project_knowledge`'s input schema
(`completion_envelopes[].findings[]`), so `id`, `repeat_of` and `same_as` each carry a
`jsonschema` description, or Part 1's contract test fails.

### 2.2 `validate_completion` memory

The session stores:

- the **prior findings**: the reviewer-parsed findings of the most recent `validate_completion`
  call whose review completed, after the ruling filter (§2.6) and with their display IDs. Server
  findings are not stored; they are not judgement calls to answer;
- the **issued-ID set**: every display ID issued in the session, by any of its tools;
- the **rulings** (§2.5), keyed by fingerprint; and
- an **escalated** flag, set when any call on the session escalates and never cleared.

"The previous call" below means the call whose findings are the prior findings.

| `validate_completion` call | Prior findings | Issued IDs, rulings, escalated |
|---|---|---|
| review completed | replaced | written |
| review truncated | kept | written |
| never reached the reviewer (argument error, `payload_too_large`, `malformed_evidence`, `session_not_found`), or the provider call failed | kept | not written |

A truncated review keeps the prior findings because its recovered list is incomplete: a finding
lost to truncation would read as new next round. It still writes rulings, issued IDs and the
escalated flag, because the reviewer saw the rulings and the recovered findings went through the
filter. The session is written only once the review has returned, so a call that errors before
the reviewer answers leaves nothing behind; it has to be resent anyway.

A truncated review runs the same tail as a completed one. `handlePerTaskReviewErr` today builds
and returns the whole response itself, ahead of every handler-specific step; it instead returns
the recovered result, and each of the three session tools runs its normal tail on it. For
`validate_completion` that tail is §2.6 steps 3–10, the plan-run row update and stats included.
A truncated `validate_task_spec` still creates no session, and a truncated `check_progress` still
records no checkpoint, so neither records its IDs.

The session store returns the live `*Session`, which handlers read without the lock. The rulings
map and the issued-ID set are read through accessors that return copies, and step 9 of §2.6 is
one locked update that merges this call's rulings and IDs into the session as it is at that
moment, not an overwrite with the copy read at step 1, so two concurrent calls on one session do
not lose each other's writes.

There is no cap on calls. The prior findings are replaced, not accumulated, and the issued-ID set
only grows when a reviewer produces a display ID it has not produced before, each behind a paid
call. Growth is bounded by that, not by the session TTL, which slides on every access and so
never expires an active session.

`post.tmpl` gains the "Prior findings" section the original design specified and never shipped.
It lists the prior findings with their display IDs, each followed by this call's response to it
when one was given. The reviewer is told, per prior finding: omit it if the evidence now satisfies
it or the response is correct; otherwise re-raise it with `same_as` set to its ID and evidence
that answers the response directly. "The summary on its own is not evidence" still applies — a
response is an argument the reviewer must engage, not evidence. "Major pre-task findings to
verify" shows each finding's display ID, so `same_as` can name a pre-task finding the reviewer
re-raises under another category, and omits a pre-task finding whose fingerprint carries a
ruling.

**`check_progress` shows rulings but does not take or apply them.** `mid.tmpl`'s "Prior
findings" list today holds every pre-task finding plus every checkpoint's findings, so a finding
re-raised at each checkpoint is listed once more each time. `priorFindings` changes in three
ways: each finding shows its display ID; for each fingerprint only the findings from the most
recent call that produced it are listed; and a finding whose fingerprint carries a ruling is left
out. `mid.tmpl` also renders the "Controller rulings" section. `check_progress` has no
`controller_rulings` input and runs no waiver filter: its findings block nothing, and rulings
come from `validate_completion`. If field runs show `unaddressed_finding` recurring across one
session's checkpoints, the stats subsystem's per-session category counts will show it, and
accepting rulings there is an additive change.

### 2.3 `finding_responses`

New optional input on `validate_completion`: `finding_responses: [{finding_id, response}]`, at
most 50 entries of at most 2000 characters. Each `finding_id` must be the exact display ID of a
prior finding; the stored list is fixed, so a suffixed ID is unambiguous here. An ID that names no
prior finding is dropped and draws one minor `other` advisory after finalization. When one call
answers the same ID twice, the last entry wins. The prior findings come from the last review
that completed, so the field description tells the caller to answer IDs from the last response
without `partial: true`.

### 2.4 Repeat and escalate

After the ruling filter (§2.6):

- a finding is marked `repeat_of: <prior id>` when a prior finding answered in this call's
  `finding_responses` shares its fingerprint, or is the finding its `same_as` names;
- if any critical or major finding is marked `repeat_of`, the envelope sets `escalate: true`, the
  summary block gains an `escalate: true` line, and `next_action` is prefixed with server text:
  "Stop resubmitting: report f_… and your responses to your controller for a ruling, then
  resubmit with controller_rulings.", where `f_…` lists the `repeat_of` IDs of the critical and
  major repeats — prior display IDs, which exist before this call's IDs are assigned;
- when `escalate` is true, `submission_defect_only` and its resubmit prefix are not applied. A
  repeated `insufficient_evidence` finding is both a submission defect and an escalation — the
  field loop's exact shape — and "resubmit" is the instruction escalation exists to stop.

Escalation fires on the first rejected answer. The reviewer has read the response and restated
the finding; another resubmission without a code change or new evidence is the loop. A
resubmission with no responses is not a repeat — nobody disputed anything.

### 2.5 `controller_rulings` on `validate_completion`

New optional input: `controller_rulings: [{finding_id, ruling}]`, at most 50 entries of at most
2000 characters.

- Each `finding_id` must be in the session's issued-ID set, whichever of the session's tools
  issued it; an unknown ID is dropped with one minor `other` advisory after finalization. When one
  call sends two rulings on the same fingerprint, the last entry wins. A ruling and a response on
  the same ID in one call are both accepted; the ruling waives the finding at step 4, before the
  repeat match could use the response.
- **A ruling covers its fingerprint.** It waives every later finding whose fingerprint, or whose
  `same_as` finding's fingerprint, equals the ruled ID's — under any suffix, for the rest of the
  session. A ruling on `f_3a9c01e2-2` covers every `f_3a9c01e2` finding, not only the second;
  matching the suffixed ID instead would waive whichever finding happens to land second next
  round.
- Rulings persist on the session. A later call applies them without resending, and a ruling on a
  fingerprint that already has one replaces its text. There is no revocation — a new
  `validate_task_spec` session starts clean. A session holds at most 50 ruled fingerprints; a
  ruling on a 51st is dropped with one minor `other` advisory. Persisting avoids the failure class
  behind the missing `plan_run_id`: a value that has to be carried forward by hand, and gets
  dropped.
- `post.tmpl` renders a "Controller rulings" section marked authoritative: do not re-raise a
  ruled concern under any category. If new evidence shows a problem a ruling does not cover, the
  reviewer raises it and quotes the ruling in `evidence`.
- **Coarse, final and visible.** A ruling also waives a sibling finding that shares its
  fingerprint, and a regression raised against the same AC in the same category. That is the
  price of a loop that always converges, and it is paid in the open: every ruling in force is
  listed, and every waived entry carries its evidence, in the summary block (§2.7), so the
  controller reading the DONE report sees each ruling and what it waived. There is no reviewer-side escape hatch; a schema field that let the reviewer
  contest a ruling would hand the loop back to the reviewer.

On a call without a `session_id`, `finding_responses` and `controller_rulings` are ignored, with
one minor `other` advisory saying they need a session.

Neither input counts toward `ANTI_TANGENT_MAX_PAYLOAD_BYTES`. That cap and its recovery advice
are about evidence, and a payload rejection caused by an argument would point the caller at the
wrong fix. The entry limits bound both inputs instead, and their field descriptions say so.

Advisories about these inputs are produced only on a call that reaches the reviewer. A call
rejected before review carries none, which is what keeps the evidence-rejection cache correct:
`evidenceCacheKey` does not include these inputs, so a cached rejection must not depend on them.

The entry objects keep the `additionalProperties: false` that schema inference gives them. A
misnamed key already fails the required-field check, so opening the object would only tolerate
extra keys alongside correct ones. Both inputs, and `validate_plan`'s `controller_rulings` and
`controller_verified_references`, join Part 1's contract test: new rows in the required-set
table, and each stated limit checked against its Go constant.

### 2.6 The filter pipeline

For a `validate_completion` call whose review completed or was truncated (§2.2), in this order:

1. merge this call's valid rulings with the session's, in memory;
2. render the prompt and run the review;
3. compute a fingerprint for every reviewer-parsed finding;
4. move every finding whose fingerprint, or whose `same_as` finding's fingerprint, carries a
   ruling into `waived_findings`, each entry `{id, severity, category, criterion, evidence, ruling}`;
5. mark repeats and set `escalate` (§2.4);
6. append server-generated findings and finalize the verdict from what remains;
7. append post-finalization advisories, then decide `submission_defect_only` (suppressed when
   `escalate` is set);
8. assign display IDs (§2.1);
9. write the session (§2.2) in one locked update;
10. set the session-TTL fields from the updated session, update the plan-run row, record stats,
    and render the response; `envelopeResult` only renders.

Only reviewer-parsed findings can be waived. Findings the server creates — `payload_too_large`,
`malformed_evidence`, `codescene_not_run`, `codescene_skipped`, `session_not_found`, the
empty-path `insufficient_evidence`, the `test_evidence` finding, the max-tokens clamp,
`noise_cluster`, the rolled-up reference checklist, and every advisory — are submission or
bookkeeping signals that a resubmission fixes, not judgement calls, and never reach step 4.

### 2.7 Envelope, summary block and guard

- Envelope: `findings[].id`, `findings[].repeat_of` (omitempty), `escalate` (omitempty),
  `waived_findings` (omitempty), and `controller_rulings` (omitempty): `[{finding_id, ruling}]`,
  the rulings the call's waiver filter used — the session's merged with this call's valid ones
  (§2.6 step 1) — sorted by `finding_id`. Only a `validate_completion` call whose review
  completed or was truncated sets it; a rejected call and a call without a session carry none.
  Evidence in `waived_findings` and ruling text in `controller_rulings` are not truncated.
- Summary block: each finding line carries its ID. Each applied ruling gets a line
  `ruling: <finding_id> "<ruling>"`, whether or not it waived anything. Each waived finding gets
  a line `waived: <id> <severity>/<category> ruling: "<ruling>"`, followed by an `evidence:` line
  truncated at `summaryEvidenceMax`. Ruling text on both lines is truncated to 200 characters,
  and every value passes through the continuation-line escaping. Both kinds of line sit below the
  header lines, after the finding lines.
- The implementer pastes the block into its DONE report, so every ruling in force, every waiver,
  and what it waived, is in front of the controller who supposedly issued it. A ruling the
  reviewer obeyed waives nothing, so without its `ruling:` line it would leave no trace.
  `anti-tangent-guard` parses only the header's `tool:`, `session_id:` and `verdict:` lines;
  `summary_contract_test.go` and `summary_forgery_test.go` are extended to prove a ruling,
  response or waived-evidence string cannot forge a header line.

### 2.8 `plan_run_report`

Each task row gains `waived`, the count on the session's most recent `validate_completion`, and
`escalated`, the session's escalated flag. The report table shows both, so the end-of-plan view
names the tasks that closed on rulings. Both fields are `omitempty` on the ledger row, so ledger
lines written before them still load. The row is updated on a truncated review too, since that
review runs the normal tail (§2.2).

Waived findings count only in `waived`. The row's severity counts and the stats event are built
from the response's `findings`, which no longer holds them.

### 2.9 `validate_plan`

- **IDs** per §2.1, fingerprint and display ID only. No prior findings are rendered, so there is
  no `same_as`.
- **`controller_rulings`** (same shape and limits). The tool is stateless and each round mints a
  new `plan_run_id`, so rulings are resent every round; the controller writes and sends them
  itself, with no subagent in between. They are rendered as authoritative in every plan prompt,
  chunked or not, and match by fingerprint as in §2.5. IDs cannot be checked against an issued
  set without a session, so only their shape is checked: a `finding_id` that is not a display ID
  (`f_` plus 8 hex digits, optionally `-<n>`) draws one minor `other` advisory after
  finalization. A well-formed ruling that waives nothing in a round draws no advisory — a reviewer
  that honours the rendered ruling and omits the finding is the ruling working, and flagging it
  would add noise to every later round. A reworded finding surfaces anyway, as a major finding
  with a new fingerprint, which is what the controller's round-over-round diff (§2.10) looks for.
- **`controller_verified_references`** (same shape and limits as on `validate_task_spec`),
  rendered in every plan prompt, chunked or not.
- **Caching.** Both inputs are rendered into the prompts `planPassCacheKey` hashes, so they key the
  cache with no further change. `planPassCacheVersion` is bumped, because a stored result now
  carries waived findings and the checklist in its new position. `clonePlanResult` copies every
  slice explicitly, so it gains the plan-level and per-task `waived_findings` slices; otherwise a
  cache hit shares memory with the stored entry.
- **Before the verdict ladder** (`applyPreLadder`, shared by the fresh and truncation-recovery
  paths), in order: the existing `DemoteUnattachedContradictions`; then
  `suppressUnverifiableCodebaseClaim` with the verified references, over the plan-level findings
  and every task's findings; then fingerprints, and ruled findings moved to `waived_findings` — on
  the result for plan-level findings and on `PlanTaskResult` for a task's. Demotion runs first
  because it turns a `contradicted_codebase_claim` into an `unverifiable_codebase_claim`, which
  the suppression must then see. All of this runs before the file-consistency finding and the
  clamp are added, so only reviewer findings can be waived.
- **The rolled-up checklist** is appended after verdict finalization, so it no longer counts
  toward the three-minor `noise_cluster` rule that lifts a plan to `warn`. `finalizePlanVerdict`
  becomes: strip every per-task `unverifiable_codebase_claim` finding and collect its checklist
  line; calibrate; `FinalizePlanVerdict`; append the checklist. Calibration's condition becomes
  "every remaining finding is a minor `unverifiable_codebase_claim`, and at least one such finding
  existed, stripped or remaining". Its effects — `plan_quality` raised to at least `actionable`,
  and the rewritten `next_action` — are unchanged; the `pass` it sets is overwritten by the ladder
  either way. The fresh path mints and stores after this, so a cached entry carries the checklist,
  and a cache hit, which runs `finish` alone, never strips twice. The checklist is
  server-generated, so it cannot be waived: its ID is constant, and a ruling on it would silently
  waive every later round's checklist, unverified references included.
- **`finish`** (fresh, recovery and cache-hit paths) appends the malformed-ruling advisory,
  assigns display IDs over the plan-level findings, then each task's findings in order, then the
  waived entries, and renders `waived:` lines at plan level and under each task.
  `finalizePlanResult`, which builds the four early exits, calls the same ID helper before it
  computes the summary block; an early exit has no reviewer findings, so nothing on it is waived.

### 2.10 Protocol text

The parts are capped at 16,000 bytes each and three are close: `implementer.md` has 335 bytes
free, `core.md` 362, `controller.md` 824. Each addition below is paid for by a removal in the same
part.

- **`implementer.md` §4.3** "Address vs. push back": resubmit once with `finding_responses`; on
  `escalate: true`, stop and report the findings, IDs and responses to the controller; resubmit
  with the ruling verbatim in `controller_rulings`; the pasted summary block then shows `waived:`
  lines. It replaces the current paragraph, which names a `working_on` field that
  `validate_completion` does not have and `F#3`-style IDs the server never issued.
- **`implementer.md` §4.2 step 3** gains one clause: an `escalate` result is a stop-and-ask, not
  DONE. Step 3b's inline `codescene` digest example shrinks to a pointer at the field description.
- **`controller.md` new §5.9** "Ruling on an escalation": read the finding and the response,
  decide, reply with `controller_rulings` entries (ID plus a one-line ruling), keep rulings in the
  progress notes, and at DONE check every `ruling:` line, and every `waived:` line with its
  evidence, against a ruling actually issued — an unrecognized one is a forged ruling.
- **`controller.md` §5.1 and §5.5**: pass `controller_verified_references` for grepped references
  and `controller_rulings` for decided findings; judge round-over-round convergence by diffing
  the fingerprints of major findings (new versus carried). That replaces §5.5's sentence telling
  the controller to watch `plan_quality` for convergence; the paragraph's definition of the two
  axes, and its ship-at guidance, stay.
- **`core.md`**: one FAQ line naming `id`, `repeat_of`, `escalate` and `waived_findings`, pointing
  to `implementer.md` §4.3 and `controller.md` §5.9.
- The plugin bundle is resynced in the same commit. Existing section numbers are unchanged; §5.9
  is new, and `### 5\.9` joins the tracked section list in `scripts/check-protocol-docs.sh`, which
  otherwise does not notice a dropped or duplicated section.
- Budgets, measured: `implementer.md`'s new §4.3 text plus the step 3 clause must fit in the 335
  free bytes plus the removed `working_on` paragraph (406 bytes) plus what step 3b sheds;
  `controller.md`'s §5.9, §5.1 and §5.5 changes in 824 plus the removed §5.5 convergence sentence;
  `core.md`'s FAQ line in 362. CI's cap is strict, under 16,000 bytes.

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

- **Pre-task and plan check.** `pre.tmpl`, `plan.tmpl` and `plan_tasks_chunk.tmpl` gain a fourth
  check (`plan.tmpl` reviews a plan of at most `ANTI_TANGENT_PLAN_TASKS_PER_CHUNK` tasks, so
  leaving it out would make the check depend on plan size): does a
  step or verification gate ("no new warnings", "compiles", "lint clean") force work a Non-goal
  defers? If so, `ambiguous_spec` at major, quoting both.
- **`verification` input on `validate_task_spec`.** Optional, at most 50 entries of at most 500
  characters: the task's steps and verify commands, which today never reach the per-task review.
  Stored on `TaskSpec` and rendered in both the pre and post prompts.
- **Forced deviations post-task.** `post.tmpl` currently turns a non-goal violation into
  `scope_drift`. When the violation is forced by a gate quoted from the spec fields — never from
  the summary — the reviewer emits one minor `ambiguous_spec` against the spec instead: its reader
  is the controller, and a `fail` would send the implementer to change code that followed the
  gate. Non-major pre-task `ambiguous_spec` findings are carried into the post prompt; today only
  majors are.

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
| `validate_completion` envelope | `escalate`, `waived_findings`, `controller_rulings` | 2 |
| `validate_plan` result | `waived_findings` (plan-level and per task) | 2 |
| `plan_run_report` rows | `waived`, `escalated` | 2 |

## Testing

- `go test -race ./...` for every change; unit tests never touch the network.
- **Part 1:** the `tools/list` contract test; codescene decoding of the digest shape, the raw
  `analyze_change_set` shape and unknown keys; the `plan_run_id` advisory present with a live run,
  absent without one, and never changing the verdict; ledger header round-trip and the
  header-only report; placeholder guard cases for diff context lines, `+` lines, Python stubs and
  the non-exempt patterns.
- **Part 2:** fingerprint stability across criterion whitespace, case and trailing punctuation,
  and across task renumbering; display-ID suffixes in emitted order over the final list, server
  findings included; `same_as` in the full and truncation-tolerant parsers; the filter pipeline
  order, including an unwaivable server finding whose fingerprint carries a ruling; a ruling on a
  suffixed ID waiving every finding with that fingerprint; repeat detection by fingerprint and by
  `same_as`; escalation only on a critical or major repeat, and escalation suppressing
  `submission_defect_only`; every row of the §2.2 write table, including a truncated review
  keeping the prior findings; ruling persistence, replacement and the 50-fingerprint cap;
  unknown-ID and no-session advisories; summary forgery tests with hostile ruling, response and
  waived-evidence text; `controller_rulings` and a `ruling:` line for every ruling applied, a
  ruling that waived nothing included, and none on a rejected call or a call without a session;
  `plan_run_report` `waived` and `escalated`, with older ledger lines still loading; `validate_plan` rulings, the malformed-ruling advisory, no advisory for a ruling that
  waives nothing, verified-reference suppression before the strip, the restated calibration
  condition, the checklist no longer lifting the verdict, a cache hit reproducing waivers without
  sharing slices with the stored entry, and IDs on the four early exits; a truncated
  `validate_completion` review applying the filter, writing rulings and updating the plan-run
  row; `check_progress` listing each fingerprint once, omitting ruled findings and rendering
  rulings; `same_as` naming a pre-task finding, a waived prior, and an unknown ID; duplicate IDs
  within one call's responses and rulings; the escalation `next_action` naming the `repeat_of`
  IDs; the new summary lines sitting after the header lines the guard parses; the new inputs'
  rows in the contract test; and concurrent `validate_completion` calls on one session under
  `-race` keeping both calls' rulings. Prompt changes regenerate golden files, reviewed
  before commit. One `-tags=e2e` run per provider confirms the nullable `same_as` is accepted, its
  spend approved before it runs.
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
  `ruling:`, `waived:`, `evidence:` and `escalate:` lines; the guard reads only the header.
- The per-task reviewer schema gains a required, nullable `same_as`, which every provider must
  emit. Providers already receive the schema from `internal/verdict`, so no provider code changes
  beyond what the schema invariant tests require; the per-provider e2e run under Testing confirms
  each one accepts the nullable type.
- The rolled-up checklist moving after finalization can lower a plan verdict that the checklist
  alone had lifted to `warn`. That is the intended correction.

## Release

All three parts ship as **one** minor release. Each part merges to `main` as soon as it is
finished, and no release is cut until Part 3 is on `main`. Each part gets its own implementation
plan.

`release.yml` publishes a release on every push to `main`: it bumps `VERSION` by the merge
subject's marker (patch when there is none) and fails when `CHANGELOG.md` has no entry for the
result. So the mechanics are:

- **One CHANGELOG entry**, `## [0.22.0] - 2026-09-15`, for the whole release. Each part
  adds its own lines to that entry.
- **Branch.** Each part is developed on `version/0.22.0`, reused in turn (the name is free again
  once the previous part's branch is merged and deleted), so CI's changelog check runs on every
  part. Part 2 is the exception: it is developed on `version/0.22.0-part2`, branched from Part 1's
  tip, and its pull request targets `version/0.22.0`, reaching `main` through that branch before
  the release is cut. CI's changelog check matches only `version/X.Y.Z` branch names, so it runs
  on `version/0.22.0`, not on Part 2's own pull request.
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
  `internal/mcpsrv/review_error.go` (`finish`, `applyPreLadder`, `handlePerTaskReviewErr`),
  `internal/mcpsrv/handlers.go` (`ValidateCompletion`, `finalizePlanVerdict`, `envelopeResult`),
  `internal/mcpsrv/submission_defect.go`, `internal/mcpsrv/plan_cache.go` (`planPassCacheKey`),
  `internal/mcpsrv/summary.go`, `internal/verdict/schema.json`, `internal/planrun/ledger.go`,
  `internal/mcpsrv/completion_evidence.go`.
