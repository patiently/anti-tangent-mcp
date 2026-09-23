# Run outcome scorecard — design

**Status:** approved design (brainstorming output), pre-implementation
**Date:** 2026-09-23
**Release vehicle:** `version/0.26.0` (backward-compatible minor; current released is 0.25.0),
one branch and one release carrying both parts. The gnome-topbar half is cut as its own
`gnome-topbar-vX.Y.Z` tag from the same merge (it versions independently).

---

## 1. Summary

Give anti-tangent a performance signal that survives a model change. Today the stats subsystem
records what anti-tangent *said* (`events.jsonl`, per call) and how each task went through the
lifecycle (`plan-runs.jsonl`, per task, opt-in). Neither records whether anti-tangent was
*right*. The stats design explicitly ruled that out for lack of ground truth
(`2026-06-02-anti-tangent-stats-design.md` §1.3).

This design adds the ground truth: an independent review of the finished work, reported back per
task, joined with anti-tangent's own verdicts, and aggregated into a scorecard grouped by
**cohort** (reviewer model × server version × implementer model). When a new model arrives, its
cohort accumulates runs and the scorecard says whether it escapes more defects than the one it
replaced.

Two parts, one release:

- **Part 1 — server.** A content-free per-task run snapshot (`runs.jsonl`), a new advisory tool
  `record_review_outcome` (`outcomes.jsonl`), a deterministic `scorecard.json`, and a new
  protocol part telling the controller when to call it.
- **Part 2 — gnome-topbar daemon.** A `/ui/runs` page (runs, per-task drill-down, cohort
  scorecard) and opt-in publishing of run records to Basic Memory, gated by
  `ANTI_TANGENT_SHARE_STATS` (default `0`), so several users on one BM can pool their runs.

### 1.1 Goals

- One consolidated record per plan run: plan verdict, every task's lifecycle verdicts, the models
  that reviewed and implemented each task, and what the independent review found in each task.
- A headline number per cohort — **escape rate** — with its sample size and uncertainty, and a
  regression flag that stays silent until the sample can support it.
- Zero behaviour change when `ANTI_TANGENT_STATS_DIR` is unset. Nothing leaves the machine unless
  `ANTI_TANGENT_SHARE_STATS=1` on a daemon.
- The server stays advisory: `record_review_outcome` never blocks, never fails a caller's work.

### 1.2 Non-goals

- **No finding-to-finding matching.** Outcome findings are attributed to a *task*, not matched
  against a specific anti-tangent finding. Matching free text is itself an LLM judgment.
- **No replay harness.** Re-running recorded inputs against a candidate model needs input
  capture; a separate piece if ever wanted.
- **No served endpoint on the MCP server.** Output is files; the daemon (already an HTTP server)
  renders them. Same stance as the stats design.
- **No CodeRabbit or post-merge-defect ingestion** in this release. The outcome `source` field is
  an enum so either can be added later without a schema change.

### 1.3 Ground-truth sources

| `source` | Who calls | When | Quality |
|---|---|---|---|
| `final_review` | the controller | after the whole-plan final review, before finishing the branch | every run; LLM, not human-adjudicated |
| `review_now` | the `review-now` skill (claude-sandbox) | after the human finishes dropping/reclassifying findings in the console | human-adjudicated; only runs reviewed that way |

The scorecard reports each source separately; they are never summed.

---

## 2. Architecture

```
 validate_plan ─► planrun.Store ─┐
 validate_task_spec ─────────────┤  (existing) row mutations
 validate_completion ────────────┤
                                 ▼
                      stats.RunSnapshotter ── append ─► runs.jsonl        (NEW, content-free)
                                                                   │
 controller / review-now ─► record_review_outcome ── append ─► outcomes.jsonl (NEW, content-free)
                                                                   │
                      stats.Compactor (existing trigger) ──────────┤
                        └─ scorecard.Compute(runs, outcomes) ─► scorecard.json  (NEW)
                                                                   │
 gnome-topbar daemon ── reads runs/outcomes/scorecard (+ plan-runs.jsonl titles, local only)
    ├─ /ui/runs page
    └─ ANTI_TANGENT_SHARE_STATS=1 ─► BM notes anti-tangent/runs/<run_hash>
                                     ◄─ reads everyone's notes ─► shared scorecard
```

The scorecard logic lives in a new **public** package `scorecard/` at the root module
(`github.com/patiently/anti-tangent-mcp/scorecard`, stdlib only), because the daemon is a separate
Go module and must compute the *same* scorecard over shared records. The daemon's `go.mod` gains
`require github.com/patiently/anti-tangent-mcp` with `replace => ../..`. `internal/stats` imports
`scorecard`; `scorecard` imports nothing from `internal/`.

---

## 3. Part 1 — server

### 3.1 Stamp the models and version on the task row

`planrun.TaskRow` gains:

```go
ReviewModel   string `json:"review_model,omitempty"`   // provider:model of the latest validate_completion
PreModel      string `json:"pre_model,omitempty"`      // provider:model of validate_task_spec
ServerVersion string `json:"server_version,omitempty"` // mcpsrv.Version at the time of the row write
```

`Run` gains `PlanModel string` (the model `validate_plan` used). These come from the envelope's
`ModelUsed`, which each handler already has. Today the model is only on `events.jsonl` and can be
joined to a row only through the session hash.

### 3.2 `runs.jsonl` — content-free run snapshot

Written by a new `stats.RunSnapshotter` whenever a row changes (the same call sites that write the
ledger), gated on `ANTI_TANGENT_STATS_DIR` **only**. It is not gated on `ANTI_TANGENT_PLAN_LEDGER`
because it carries no content: no task titles, no plan headings, no finding text.

One line per row write; a reader keeps the latest line per `(run_hash, task_index)`:

```json
{"ts":"…","run_hash":"r_9c1e…","plan_verdict":"pass","plan_quality":"rigorous",
 "plan_model":"openai:gpt-5.6-terra","task_count":11,"server_version":"0.26.0",
 "task":{"index":7,"pre_verdict":"warn","post_verdict":"pass","checkpoints":1,"attempts":1,
         "severity":{"major":1},"waived":0,"escalated":false,"lite":false,"unmatched":false,
         "pre_model":"…","review_model":"…","codescene_state":"ran"}}
```

A header line (`"header":true`, no `task`) is written when `validate_plan` mints the run, so a
run whose tasks never report still appears.

`run_hash` is the salted digest of `plan_run_id` using the existing salt in `state.json`, so the
file is safe to share and still joins with `outcomes.jsonl` on the same machine. The raw
`plan_run_id` is never written to this file.

Snapshots survive a server restart without the ledger. An outcome that arrives days later joins
on disk, not in memory.

Retention: pruned by the Compactor with the existing `StatsRetentionDays`, keyed on `ts`.

### 3.3 `record_review_outcome` tool

The tenth tool. Deterministic, **no reviewer call**, advisory.

Input:

```json
{
  "plan_run_id": "pr_66a80fec6143",
  "source": "final_review | review_now",
  "reviewer_model": "anthropic:claude-opus-5-5",        // optional: what did the outcome review
  "implementer_models": [{"task_index": 7, "model": "anthropic:claude-sonnet-5"}],  // optional
  "findings": [
    {"task_index": 7, "severity": "major", "category": "correctness"},
    {"task_index": 0, "severity": "minor", "category": "docs"}
  ]
}
```

- `task_index` is 1-based; `0` means "not attributable to one task" and is counted separately.
- `severity` is `critical | major | minor`. Callers map their own scales (a review-now `nit` is
  dropped by the caller, not sent).
- `category` is free text, normalised to lower-case and truncated to 40 chars, stored for the
  histogram only. It is the one free-text field; callers are told to send a category word, not a
  finding description.
- An empty `findings` array is valid and meaningful: "the independent review found nothing".
- `implementer_models` is how the implementer model reaches the record. The controller knows which
  model it dispatched each task on (model routing makes this per task); the server cannot see it.
  `review_now` callers usually omit it.
- A second call with the same `(plan_run_id, source)` supersedes the first (review-now re-rounds).

Validation (returns a structured error, never panics): unknown `source`, severity outside the
enum, `task_index` < 0 or > the run's task count when the run is known. An **unknown
`plan_run_id` is accepted** (the run may be from before a restart with no snapshot yet), with
`run_known:false` in the response. The scorecard simply cannot place it until a snapshot exists.

Writes one line to `outcomes.jsonl` (`run_hash`, `source`, `ts`, models, findings), then returns:

```json
{"recorded": true, "run_known": true, "tasks_scored": 11,
 "escapes": [{"task_index": 7, "anti_tangent_verdict": "pass", "outcome_severity": "major"}],
 "summary_block": "…"}
```

The response reports this run's escapes only, so the controller sees the result immediately. It
does not wait for the next compaction.

When `ANTI_TANGENT_STATS_DIR` is unset the tool is still registered and returns
`{"recorded": false, "reason": "stats disabled"}`. Hiding the tool would break protocol text that
names it.

Logging: one summary line on exit, the same shape as `validate_plan` (per root `CLAUDE.md`).

### 3.4 Scorecard

`scorecard.Compute(runs, outcomes []Record, cfg) Scorecard`, pure and deterministic. The Compactor
calls it on the existing trigger and writes `scorecard.json` next to `rollup.json`.
`record_review_outcome` also triggers a recompute (single-flight, async). The scorecard
changes when an outcome lands, and waiting for the next hook-count threshold would leave it stale.

**Unit of scoring:** a *scored task* is a task row that has a final `post_verdict` and belongs to
a run with at least one outcome record for the given source.

**Cohort key (per scored task):** `review_model` × `server_version` × `implementer_model`
(`"unknown"` when not reported). The scorecard also provides roll-ups that collapse
`implementer_model`, and one that collapses everything except `review_model`, the view that
matters when a new reviewer model lands.

**Metrics per (cohort, source):**

| Metric | Definition |
|---|---|
| `escape_rate` (headline) | scored tasks with final AT verdict `pass` **and** ≥1 outcome finding of `major`/`critical` in that task ÷ scored tasks with final AT verdict `pass` |
| `minor_escape_rate` | same with `minor` only; reported, never flagged |
| `unconfirmed_flag_rate` | scored tasks whose final AT verdict is `warn`/`fail` and whose outcome has no `major`/`critical` in that task ÷ scored tasks with final `warn`/`fail` |
| `waive_rate` | waived AT findings ÷ AT findings on final completions (from `TaskRow.Waived` and `Severity`) |
| `caught_and_fixed` | scored tasks whose latest snapshot has `post_verdict: pass` while an earlier snapshot line for the same task had `warn`/`fail`; count only |
| `unattributed_findings` | outcome findings with `task_index: 0`, by severity |
| `calls_per_task` | mean of `attempts + checkpoints + 1` over scored tasks (from the snapshot) |
| `review_ms_p50` / `p95` | from `events.jsonl` events whose `model` equals the cohort's `review_model`, restricted to `validate_completion`, over the cohort's time span. Events cannot be joined per task, so this is per model, not per task. |
| `runs`, `tasks` | sample sizes; always shown next to every rate |

Why `unconfirmed_flag_rate` uses the *final* verdict: a task that warned and was then fixed ends
at `pass`, and its clean outcome is a catch, not a false alarm. Only a warn/fail the implementer
could not clear, and that the independent review then did not confirm, counts as possible noise.

**Uncertainty and the regression flag.** Every rate carries a Wilson 90% interval. The baseline
for a cohort is the cohort (same source and collapse level) whose most recent scored task
precedes the current cohort's first scored task. `regression: true` is set only when **both**
cohorts have `runs ≥ min_runs` (default **10**, `ANTI_TANGENT_SCORECARD_MIN_RUNS`) **and** the
current cohort's escape-rate lower bound exceeds the baseline's upper bound. Below the threshold
the field is `"insufficient_data"`, never `false`, so a quiet flag cannot be mistaken for a clean
bill.

`scorecard.json` shape (keys are a cross-component contract with the daemon, snake_case, tagged):

```json
{"generated_at":"…","min_runs":10,
 "cohorts":[{"key":{"review_model":"…","server_version":"0.26.0","implementer_model":"…"},
             "source":"final_review","runs":14,"tasks":96,
             "escape_rate":{"value":0.06,"lo":0.03,"hi":0.12,"n":71},
             "minor_escape_rate":{…},"unconfirmed_flag_rate":{…},"waive_rate":{…},
             "caught_and_fixed":9,"unattributed_findings":{"minor":3},
             "baseline":{"review_model":"…","server_version":"0.25.0","implementer_model":"…"},
             "regression":"insufficient_data"}],
 "by_review_model":[…same shape, implementer_model and server_version collapsed…]}
```

The Compactor's LLM summary prompt receives the `by_review_model` view, so `summary.md` can
narrate the trend. The prompt's existing instruction against claiming correctness is relaxed for
this block only: it may now state escape rates, because they have ground truth.

### 3.5 Protocol

Every existing protocol part is within 200 bytes of its 16,000-byte limit (`controller.md` is at
15,802), so the controller step goes in a **new sixth part**, `docs/protocol/outcome.md`
(controller-scoped, loaded once per run after the last task, well under the limit). It carries the
new step, §5.10 "Recording the review outcome". The number continues §5, and no existing section
is renumbered. It covers:

- when: after the final whole-plan review returns, before `finishing-a-development-branch`;
- how to attribute each final-review finding to a task (file → task via the plan's `Files:` lists
  or `plan_run_report`), `task_index: 0` when it genuinely spans tasks;
- pass `implementer_models` from the dispatch record;
- send a category word, never finding text;
- put `anti-tangent-plan-run: <plan_run_id>` on its own line in the PR body (one line per run),
  which is how `review-now` finds the run(s) for a PR.

`INTEGRATION.md` gains one router line for the new part (it has ~330 bytes of headroom). The
plugin bundle gets the new file, synced per the repo rule. The size-check CI job's file list is
extended to the new part.

### 3.6 Config

| Env var | Default | Effect |
|---|---|---|
| `ANTI_TANGENT_SCORECARD_MIN_RUNS` | `10` | runs per cohort before `regression` can be true/false |

No other new server env var. `runs.jsonl`, `outcomes.jsonl` and `scorecard.json` exist iff
`ANTI_TANGENT_STATS_DIR` is set.

---

## 4. Part 2 — gnome-topbar daemon

### 4.1 Reader

New package `daemon/internal/atruns` reads `runs.jsonl`, `outcomes.jsonl`, `scorecard.json`, and
(if present, local display only) `plan-runs.jsonl` for task titles, joined by hashing the ledger's
`plan_run_id` with the salt from `state.json`. Absence of any file means `Present=false`, never an
error, the same as `atstats`.

### 4.2 `/ui/runs` page

Behind the existing `uiAuth`. Three views:

1. **Scorecard**: `by_review_model` table (escape rate with interval and n, unconfirmed-flag
   rate, waive rate, regression badge) per source; a toggle for *mine* / *shared* (shared only
   when sharing is on).
2. **Runs list**: newest first; plan verdict, task count, reviewer/implementer models, whether
   each outcome source has reported, escape count.
3. **Run detail**: one row per task, with AT pre/post verdict, severity counts, waived, attempts
   and models, next to outcome findings per source; escapes highlighted. Task titles are shown only
   when the local ledger supplies them.

The existing `/ui/stats` page links to it. No tray menu change beyond that link.

### 4.3 BM sharing

Gated by `ANTI_TANGENT_SHARE_STATS` in the **daemon's** environment, default `0`. Anything other
than exactly `1` means off (the same convention as `ANTI_TANGENT_JEV`).

When on, after each refresh the daemon publishes every run that has at least one outcome record
as a BM note:

- permalink `anti-tangent/runs/<run_hash>`, project from a new daemon config key
  `share_project` (defaults to the BM project the daemon already uses);
- note type `at_run`; body is one fenced `json` block holding exactly the run's `runs.jsonl`
  latest-per-task lines and its `outcomes.jsonl` lines, plus `publisher` (the BM username the
  daemon already knows) and `schema: 1`;
- **never** task titles, plan headings, `plan-runs.jsonl` content, or the raw `plan_run_id`;
- idempotent: overwritten in place when the run's content hash changes, skipped otherwise; the
  daemon keeps a small `published.json` of `run_hash → content hash` in its state dir.

The shared scorecard: the daemon lists `at_run` notes in `share_project`, parses the JSON blocks
(skipping any that fail to parse or carry an unknown `schema`, counted and shown as "n skipped"),
and calls the same `scorecard.Compute`. Salts differ per user, so `run_hash` values never collide
across publishers in practice; the (publisher, run_hash) pair is the key regardless.

BM failures degrade to a banner on the page. They never block the local view and never retry in
a tight loop. The daemon's existing stale-session self-heal applies.

---

## 5. Error handling

- Every stats write stays best-effort and swallowed-and-logged; a hook call's result and latency
  are unaffected (unchanged guarantee).
- `record_review_outcome` returns validation errors in its envelope; I/O failure returns
  `recorded:false` with a reason. It never returns an MCP-level error for a bad outcome.
- A malformed line in any `.jsonl` is skipped by readers (server and daemon), counted, and the
  count surfaces in `scorecard.json` as `skipped_lines`.

## 6. Testing

- `scorecard/`: table-driven tests for every metric, including the fix-then-pass case (catch, not
  noise), `task_index: 0`, superseded outcomes, unknown runs, the Wilson bounds against known
  values, and the regression flag at `min_runs − 1`, `min_runs`, overlapping and disjoint intervals.
- `internal/stats`: snapshot lines carry no title/text (assert on the marshalled bytes), hash
  stability across restarts via `state.json`, retention pruning of the two new files.
- `internal/mcpsrv`: handler tests for validation, the stats-disabled response, supersede
  semantics, and an integration test running validate_plan → task spec → completion →
  `record_review_outcome` and asserting the escape shows up in the response and in `scorecard.json`.
- Protocol: the existing size and bundle-identity CI checks cover `outcome.md`.
- Daemon: `atruns` reader tests on fixture files; page handler tests via `httptest`; BM publish
  tests against the existing fake BM client asserting the note body contains no title field and
  that an unchanged run is not republished.
- No test hits the network; `-race` throughout.

## 7. Rollout

- CHANGELOG `## [0.26.0]` `### Added`: the tool, the three files, the protocol part, the env var;
  gnome-topbar changes noted under the same entry as well as in its release notes.
- Docs that count or list the tools move from nine to ten: root `CLAUDE.md` overview and
  architecture block, README, the `server.go` registration comment, and the design spec's tool list.
- claude-sandbox follow-up (separate repo, separate change): `review-now` reads the
  `anti-tangent-plan-run:` PR-body lines and calls `record_review_outcome` with
  `source: review_now` after the console round closes, and the pinned protocol plugin version is
  bumped.
- Merge per the gnome-topbar release procedure: the merge touches both `gnome-topbar/**` and root
  code, so it is a normal `[minor]` release merge; the gnome-topbar tag is pushed from the same
  merge commit afterwards.
