# Test value and the development cycle — design

**Status:** design approved in brainstorming 2026-11-19/20; §7 (`practice.tmpl` text) awaits review.
**Ships from:** `version/0.31.0` (minor).
**Follow-on specs (other repos, against the interfaces defined here):** claude-sandbox (pin bump +
audit, review-now test-value lens), gnome-topbar (tray revisit line).

## 1. Problem

Implementation cycles run under superpowers grew test suites by thousands of tests: a unit test per
function, tests for every quirk, guards such as "framework X, which we just removed, must not come
back", and source-text greps that never run the code. We still want tests — ones that exercise the
code and capture what it is for.

Two causes are in reach of anti-tangent:

1. **The process.** superpowers' TDD skill drove red-green per unit, which yields one test per
   function. claude-sandbox PR #108 removes superpowers and with it every piece of process
   guidance it carried (plans, TDD, debugging, verification, finishing), leaving a one-implementer
   flow with no stated cycle.
2. **anti-tangent itself.** `post.tmpl`'s `test_adequacy` emits a minor finding whenever a criterion
   lacks a test that would catch its breakage, even when the task never asked for tests.
   Implementers clear findings by adding tests. Over 2026-10-21 → 2026-11-20 `test_adequacy` was
   the top finding category: 1,676 of ~7,250 findings over 3,235 calls (local `rollup.json`).
   Nothing in the reviewer names over-testing.

## 2. Decisions

| # | Decision |
|---|---|
| D1 | anti-tangent owns the cycle's rules, judging, structured fields and signals. The sandbox owns wiring: commands, edit-gate config, pins/audits, the review-now lens. Rule text lives once, with the tool that judges it; consumers read it from the pinned plugin, never a copy. |
| D2 | The test-value rule is server-delivered text (`test_value.tmpl`), handed to the implementer by `validate_task_spec` and embedded in `post.tmpl`, so both judge identical words. |
| D3 | Test justification is captured at completion: `test_ledger` + `tests_declined` on `validate_completion`. |
| D4 | Severity split by tag; over-testing volume is its own check, major at "≥ half the added tests could go". |
| D5 | `implementer_model` is self-reported on `validate_task_spec`. |
| D6 | Revisit signals surface in both the stats summary and the gnome-topbar tray. |
| D7 | One release (0.31.0) carries everything; drift signals self-gate on sample size. |
| D8 | One rule for all models; no per-model guidance until a signal says so. |
| D9 | The development cycle is part of this spec: a controller protocol part (`cycle.md`) and implementer working practice (`practice.tmpl`). |
| D10 | `test_adequacy` loses its minor catch-all. |
| D11 | Going forward only. Existing test suites are not pruned. |

## 3. The test-value rule

### 3.1 Text — `internal/prompts/templates/test_value.tmpl`

Plain text, no template actions (so it can be mirrored byte-for-byte):

```markdown
### Tests (apply before writing each one)

A test earns its place by failing when behaviour someone depends on breaks.
Before writing a test, name the acceptance criterion or caller-visible
behaviour it protects. If you cannot, do not write it — list it under
`tests_declined` with the reason.

Start from one test per acceptance criterion. Add another only for a failure
or boundary case the criterion names, and write several inputs to one
behaviour as one table-driven or parametrised test, not as separate tests.
Going past that needs a `protects` the earlier tests cannot cover. Prefer one
test that drives a criterion through its public entry point over several that
cover its helpers.

Write:
- tests that run the behaviour an acceptance criterion describes, including
  the failure and boundary cases it names;
- a regression test for the bug this task fixes;
- a guard for a property that matters — after seeing it fail with the
  property broken (say how in `failed_when`).

Do not write:
- absence guards: assertions that something removed stays removed (git and
  review catch a return);
- one test per function, or a test that restates the implementation;
- a second test for behaviour another test already asserts;
- tests that read source text instead of running the code;
- tests pinning incidental output no caller depends on (log wording, map order).

Existing tests are out of scope: do not delete or rewrite tests the task did
not ask you to touch.
```

Both claude-sandbox rules survive and are reinforced: "execute behaviour, do not grep source"
is the `source-grep` prohibition; "every new guard must be seen to fail" is the `failed_when` clause.

### 3.2 Delivery

- `practice.tmpl` (§7) includes `test_value.tmpl`. `validate_task_spec`'s existing
  `implementation_guidance` field becomes `lean.tmpl` + `practice.tmpl`.
- `post.tmpl` embeds `test_value.tmpl` in a new `### Test value` section (§4.2), exactly as
  Over-building embeds `lean.tmpl`.
- `plugin/anti-tangent-protocol/rules/test-value.md` is a byte-identical copy for review-now.
  A CI check enforces identity (same pattern as `docs/protocol/` ↔ plugin bundle).
- `lean.tmpl`'s "never simplify away the tests the task calls for" stays. Over-building's `shrink`
  exemption for test code stays; test value is the new section's job.

### 3.3 §4.2 dispatch clause

One added line in the fenced clause (and the short variant): pass `implementer_model` (your exact
model ID) to `validate_task_spec`, and `test_ledger` / `tests_declined` to `validate_completion`.
The rule itself is not repeated. `implementer.md` is at 15,990 / 16,000 bytes, so the edit pairs with
an equal trim elsewhere in the file; the bundle is resynced in the same commit.

## 4. Judging test value at completion

### 4.1 New `validate_completion` arguments (optional)

```json
"test_ledger":    [{"test": "path::TestName", "protects": "<AC text or caller-visible behaviour>", "failed_when": "<optional: how you saw it fail>"}],
"tests_declined": [{"test": "<what you considered>", "reason": "<why not>"}]
```

Bounds: ≤ 100 entries each; each string ≤ 300 characters, no control characters. Violations return
the usual structured argument error. Both reach the reviewer in a fenced, caller-attested block
(data, never instructions). `failed_when` is not verified.

### 4.2 `### Test value` section in `post.tmpl`

Judges ONLY tests the diff adds, and ONLY when a diff is present (same reasoning as Over-building).
Line numbers come from hunk headers, never invented. For each added test, check the ledger entry
against the code: does the test exercise what `protects` names; is a guard without `failed_when`
plausibly a guard.

**Per-test tags:**

| Tag | Flags | Severity | Shape |
|---|---|---|---|
| `absence:` | asserts a removed framework/symbol/file/dependency stays gone | **major**, one finding per test | `category: quality`, `criterion: test_value_absence` |
| `source-grep:` | reads source text and asserts on it instead of running code | **major**, one per test | `criterion: test_value_source_grep` |
| `trivia:` | per-function / restates the implementation | minor | aggregated into `criterion: test_value` |
| `quirk:` | pins incidental output | minor | aggregated into `criterion: test_value` |

**Volume check (over-testing).** Group added tests by the AC they exercise (`protects` when given,
else the code). For each group with more than one test: would merging them into one table-driven
test, or deleting all but one, lose a failure or boundary case the AC names? If not, it is a
`consolidate:` instance. Report ONE finding, `criterion: test_value_volume`, `evidence` listing
`AC → N tests → M needed` per group and ending `net: -K tests`. **Major when at least half the added
tests could go** without losing a named case; minor otherwise.

**Mandated.** A test an AC itself mandates is built as written and reported as
`criterion: test_value_mandated`, minor, addressed to the spec author (mirrors
`over_building_mandated`).

**Never flagged:** a test running an AC's behaviour, a regression test for the bug the task fixes, a
guard whose ledger entry says how it was seen to fail — unless it is also `source-grep:`.

Majors are disputable per finding through the existing `finding_responses` / escalation path.

### 4.3 `test_adequacy` recalibration

`test_adequacy` keeps one case: **major** when an AC describing behaviour has no test that would
catch its breakage AND the task's acceptance criteria or verification call for tests. The "minor
otherwise" catch-all is removed, and so are minor findings for happy-path-only coverage the brief
did not ask for. Skipped/deselected tests in evidence stay a finding (it is an evidence problem, not
a pressure to add tests).

### 4.4 Deterministic checks (no LLM)

- **Test count.** The server counts test declarations the diff adds: `func Test…` (Go), `@Test`
  (Kotlin/Java), `def test_` (Python), `it(` / `test(` (JS/TS). Table rows and parametrised cases are
  not counted. Yields `tests_added` and `ac_count`. Unknown language → 0; the reviewer still judges.
- **Missing ledger.** If the diff adds files matching test-path conventions (`_test.go`,
  `*Test.kt`, `*Test.java`, `test_*.py`, `*_test.py`, `*.test.*`, `*.spec.*`, `tests/`) and no
  `test_ledger` is supplied: one minor finding, `criterion: test_ledger_absent`. Not a submission
  defect, so older clients are not hard-failed.
- **Summary block** gains one line: `tests: +N for M ACs (declined K)`.

## 5. Model stamping and the data path

- `validate_task_spec` gains optional `implementer_model` (≤ 80 chars, no control characters,
  normalised like scorecard model ids: lower-cased, trailing date stamp stripped). The session
  stores it.
- **Standalone sessions become one-task runs.** Today `runs.jsonl` rows are written only for plan-run
  tasks (`internal/mcpsrv/run_snapshots.go`), so the one-implementer cycle is invisible to the
  scorecard and tray. A session with no `plan_run_id` now writes run rows with
  `RunHash = HashRunID(salt, "session:" + session_id)`, `task_index: 1`, and a header carrying
  `ConfiguredModels`. The plan-run path is unchanged.
- `scorecard.TaskSnapshot` gains `implementer_model`, `tests_added`, `ac_count`, `ledger_entries`,
  `tests_declined` (from the task's latest `validate_completion`; all counts or a model id).
  `run.implementerModel()` prefers an outcome-reported model, then the snapshot's.
- `stats.Event` gains the same counts and `implementer_model`. `countedCriteria` gains
  `test_value`, `test_value_absence`, `test_value_source_grep`, `test_value_volume`,
  `test_value_mandated`, `test_ledger_absent`.
- **Privacy:** ledger text goes to the reviewer and is never written to stats; only counts are.
- **Outcome for one-task runs.** `record_review_outcome` accepts `session_id` in place of
  `plan_run_id` (exactly one required). The run hash is computed from the id and salt, so an expired
  session still records; `run_known` reflects whether ledger rows exist. `task_index` is `1`.
  PR-body line: `anti-tangent-session: <session_id>`.

## 6. Revisit signals

### 6.1 `scorecard.Revisit`

Pure, stdlib-only, shared by server and daemon:

```go
func Revisit(runs []RunLine, outcomes []OutcomeLine, now time.Time) []Signal

type Signal struct {
	Kind        string // new_model | test_value_drift | test_volume_drift | unconfirmed_flags | test_adequacy_top
	Role        string // implementer | reviewer ("" for run-wide kinds)
	Model       string
	ReviewModel string
	Baseline    Rate // existing Wilson Rate
	Current     Rate
	Since       time.Time
}
```

| Kind | Fires when | Gate |
|---|---|---|
| `new_model` | an implementer model, or any tool's reviewer model, first appears within the last 30 days | none |
| `test_value_drift` | per (implementer, reviewer model): share of tasks with a major `test_value*` or any `test_value_volume`; latest 30 tasks vs all earlier, 90% Wilson intervals do not overlap | n ≥ 30 both windows |
| `test_volume_drift` | same windows; share of tasks with `tests_added / ac_count > 2` (deterministic) | n ≥ 30 both windows |
| `unconfirmed_flags` | unconfirmed-flag rate (existing scorecard metric) lower bound above 0.7 | existing `ANTI_TANGENT_SCORECARD_MIN_RUNS` |
| `test_adequacy_top` | `test_adequacy` is the top finding category over the last 30 days on server ≥ 0.31.0 — the recalibration did not take | ≥ 200 findings in window |

Drift is compared within one reviewer model so a reviewer swap does not read as implementer drift.
Constants (30 days, 30 tasks, 2.0, 0.7, 200) live together in `scorecard`.

### 6.2 Channels

1. `scorecard.json` gains a `revisit` array. The periodic LLM prose summary receives the signals and
   leads with a "Cycle review due" section when any are present.
2. gnome-topbar daemon calls `Revisit` over pooled records and renders one line per signal; ack state
   is daemon-side, keyed by (kind, model, since). Follow-on spec.

A signal tells a human to look; it never changes guidance by itself (`cycle.md` §7.4 says what to do).

## 7. Implementer working practice — `practice.tmpl` (DRAFT, review)

Delivered by `validate_task_spec` after `lean.tmpl`; judged where a reviewer section already covers it
(tests in `### Test value`, evidence in the existing evidence rules). Replaces superpowers' TDD,
systematic-debugging and verification-before-completion skills.

```markdown
## Working practice (apply while implementing this task)

Read before you write: the brief, the spec it points at, and the code on the
path you will change, end to end.

Order of work: for each acceptance criterion that describes behaviour, write
the test that would catch its breakage first and see it fail for the right
reason, then make it pass. Criteria, not functions, set the unit: helpers get
no test of their own when a criterion's test already drives them.

{{template "test_value.tmpl"}}

When something fails: reproduce it, find the root cause, fix it there. Do not
patch the symptom, retry until green, or loosen an assertion to pass. If two
attempts at a cause fail, stop and call check_progress with what you know.

Stop and ask your controller when the brief is ambiguous in a way that changes
what you build, when an acceptance criterion cannot pass without doing a
non-goal, or when the fix belongs outside your Files list.

Evidence over assertion: before validate_completion, run the brief's verify
commands and paste the commands and their output into test_evidence. "Should
pass" is not evidence.

Scratch verification (probes, one-off scripts) stays outside the repository.
```

## 8. The development cycle — `docs/protocol/cycle.md`

New controller-facing protocol part, **§7** (new number; existing numbers unchanged), under the
16,000-byte cap, bundled into the plugin, routed from `INTEGRATION.md` (currently 1,861 / 2,000 bytes:
the new row pairs with a trim of the intro paragraph).

### 8.1 Stage map (§7.1)

| # | Stage | Owner | Exit gate |
|---|---|---|---|
| 1 | Brainstorm | human + controller | Spec approved and committed. Last section is the brief: Goal, ACs, Non-goals, Context, Files, exact verify commands, and a scope/test clause ("tests only where an AC needs one; scratch verification outside the repo"). No implementation steps or code. design-walkthrough optional for risky runtime behaviour. |
| 2 | Brief check | controller | Split on independence, not size: one goal, one reviewable PR, no AC waiting on an unmade decision. Otherwise split into sequential briefs (each its own 3–5). Grep the names the brief cites. |
| 3 | Dispatch | controller | One implementer in a fresh context: spec path, Goal and ACs verbatim, §4.2 clause. Model chosen per §7.3. The controller writes no project code. |
| 4 | Implement | implementer | `validate_task_spec` → work per `practice.tmpl` → `validate_completion`. `check_progress` on long tasks or when the guard asks. |
| 5 | Accept | controller | Existing envelope checks (present, `tool: validate_completion`, verdict, no self-written `ruling:`/`waived:`); read the `tests:` line against the ACs; verify output is pasted. Re-run the verify commands only when output is missing, the run was unattended, or the task is high-risk. |
| 6 | Review | review-now / finishup | Tiered by risk: small low-risk diffs get one fresh-context pass; others the persona round. Fix work is one anti-tangent session per finishup run (findings as ACs), not one per round. Fix rounds skip style and add no tests for cases that cannot happen. |
| 7 | Outcome | controller | `record_review_outcome` with `session_id` (or `plan_run_id`), `source: review_now`; PR-body line `anti-tangent-session: <id>`. |
| 8 | Finish | human | Merge. |

`validate_plan` stays for hosts that run multi-task plans; §7.1 names when it fits: several briefs that
must land together.

### 8.2 Parallelism (§7.2)

Default: one implementer per spec; never parallel writers on one spec. Fan out writers only when all
hold: briefs from stage 2 have disjoint file sets and no shared new interface (land a shared interface
first, sequentially); each brief's verify commands pass in isolation; each runs in its own worktree and
PR; the human has review capacity for them (cap 2–3). Read-only fan-out — exploration, search, review
personas — is encouraged: it returns summaries and keeps the controller's context clean.

### 8.3 Roles and models (§7.3)

Capability tier and effort per role, with the current Anthropic mapping dated in the document.
Mapping as of 2026-11-20:

| Role | Model | Effort | Escalate when |
|---|---|---|---|
| Controller / brainstorm | Opus 5.5 | medium | Fable 5.2 for cross-repo or architecture design |
| Implementer, routine | Sonnet 5.5 | medium | Confidently wrong despite good context → Opus |
| Implementer, hard (concurrency, security boundary, unfamiliar domain) | Opus 5.5 | high | Stalls on long multi-step work → Fable 5.2 |
| Exploration / search | Haiku 5.5 | low–medium | Results need judgement → Sonnet (override built-in Explore, which defaults to the session model) |
| Review personas, file-scoped | Sonnet 5.5 | high | existing persona escalation signals → Opus |
| Review personas, whole-repo | Opus 5.5 | high | — |
| Review validator | Fable 5.2 | high | — |
| anti-tangent reviewer | non-Anthropic (configured) | — | — |

Escalation test: did the model not know enough (move up a model) or not try hard enough (raise
effort)? The reviewer stays a different vendor from the implementer.

### 8.4 Revisit (§7.4)

When a §6 signal fires: read the cohort numbers; on `new_model`, re-run the effort sweep and try
removing one harness component at a time, watching escape rate; decide whether `practice.tmpl`,
`test_value.tmpl`, the brief format or §7.3 changes; record the decision in the CHANGELOG.

## 9. Error handling

- Malformed/oversized ledger entries or `implementer_model`: structured argument error, as for other
  arguments.
- `record_review_outcome` with both or neither of `plan_run_id` / `session_id`: `recorded: false` with a
  reason naming the fields.
- Stats and signal computation stay best-effort; a failure never affects a verdict or latency.
- The server stays advisory. A major finding blocks DONE through the protocol, not enforcement.

## 10. Testing

Tests for this work follow its own rule: one test per AC, table-driven across inputs to one behaviour,
declined tests listed in the DONE report.

| Test | Covers |
|---|---|
| handler: `implementation_guidance` contains lean + practice + test-value text; `post.tmpl` renders `### Test value` | delivery (golden files regenerated with `-update`, diff reviewed) |
| CI identity check `rules/test-value.md` ↔ template | seen to fail on a one-byte edit |
| table: test-declaration counting across five languages, incl. table rows not counted | §4.4 count |
| `test_ledger_absent` fires on a test file with no ledger; silent with a ledger | §4.4 |
| standalone session writes a `runs.jsonl` task row with `implementer_model`; scorecard cohort picks it up | §5 |
| `record_review_outcome` by `session_id` records against the session run hash | §5 |
| table: `Revisit` — new model fires; n < 30 silent; non-overlapping intervals fire; overlapping silent; reviewer swap does not fire implementer drift | §6 |

Not written: per-bound validation tests for each new argument (they go through existing, tested
validation helpers).

**Measured acceptance (reviewer behaviour is model-written; no string assertions).** Paid replay on the
production reviewer, n = 10 per set: superpowers-era over-tested diffs → `test_value_volume` major in
≥ 7/10; lean well-tested diffs → no `test_value*` major in ≥ 9/10; same lean set → `test_adequacy`
findings drop versus 0.30.1. A miss tunes the template text before release.

## 11. Rollout

- anti-tangent: branch `version/0.31.0`; CHANGELOG `## [0.31.0]` lists new arguments, the lens,
  `test_adequacy` recalibration, standalone run rows, session-keyed outcomes, `revisit`, `cycle.md`,
  `practice.tmpl`. Protocol parts resynced to the plugin bundle in the same commit.
- claude-sandbox (follow-on spec): pin bump to 0.31.0 with committed audit; review-now test-value
  lens applied by every selected persona, reading `rules/test-value.md` from the pinned plugin;
  finishup inherits it (fixes are cuts/merges); README one-implementer section points at `cycle.md`
  instead of restating it; review-now files outcomes via `session_id`. Parked there: a non-Anthropic
  validator or persona for high-risk diffs; whether `EDIT_GATE=all` pushes small fixes to ungated
  Bash writes.
- gnome-topbar (follow-on spec): tray revisit line and ack state over `scorecard.Revisit`.

## 12. Non-goals

- Pruning existing test suites.
- Per-model guidance text.
- Any blocking in the MCP server.
- Persisting ledger text.
- Watching the sandbox's frontier-critic model choice.
