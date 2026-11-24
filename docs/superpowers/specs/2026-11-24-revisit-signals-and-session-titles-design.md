# Revisit signals and session-run titles — design

**Status:** approved in brainstorming 2026-11-22/24; revised after a Fable review against the code.
**Ships from:** `version/0.32.0` (minor; merge commit carries `[minor]`). The daemon half ships on its
own tag train as `gnome-topbar-v0.5.0`.
**Builds on:** [`2026-11-20-test-value-and-development-cycle-design.md`](2026-11-20-test-value-and-development-cycle-design.md)
§5 (the 0.31.0 fields), §6 (revisit constraints), §8.4 (what a revisit is) and §11 (the gnome-topbar
follow-on: tray revisit line, ack state, session-run titles). This spec covers the server and the
daemon together.

## 1. Problem

0.31.0 records everything a revisit decision needs — implementer model per task, `kind: session`
runs, `tests_added`, `ac_count`, criterion counts, cohort kind — but nothing reads it as a trigger.
§8.4 of the cycle says "when a revisit signal fires … read the cohort numbers", and no signal fires.
Separately, session runs show on `/ui/runs` by run hash only: their title exists in the server's
session but is never written anywhere the daemon can join.

§6 of the 0.31 spec ruled out absolute bars (`unconfirmed_flags` already sits at 0.75; a 30-day
`new_model` window equals retention; whole-spec volume is ~111 tasks/month) and asked for thresholds
set from 0.31 data. 0.31.0 was released 2026-11-22; on the day this spec was written the local stats
directory held 0 session runs. So this design sets **no tuned thresholds**: every drift signal is a
statistical comparison of a cohort against its own past, gated on sample size.

## 2. Decisions

| # | Decision | Why |
|---|----------|-----|
| D1 | One spec, server and daemon, from `version/0.32.0`. | The tray line has nothing to show without the server array. |
| D2 | Drift = disjoint Wilson 90% intervals (the existing `wilson`, the rule `assignRegression` already uses) between a recent and a baseline window of the same cohort, each side ≥ `MinRuns` eligible tasks from ≥ 2 distinct runs. No tuned bars. | No data yet to tune from. Disjoint 90% intervals are about a 2% two-sided test; 95% would halve power at n = 10–20 for little gain (§3.2). |
| D3 | The server computes signals; the daemon only reads `scorecard.json`. | First-seen state lives in the server's `first-seen.json` (D7); pooled teammates carry none, so a daemon recompute would misreport `new_model`. Deviates from §6's "shared by server and daemon": the function is public and pure, but only the server calls it. |
| D4 | Ack lasts until the signal clears. | A drift persists for weeks; re-nagging adds nothing. A recurrence after clearing is new information. |
| D5 | The summary's "Cycle review due" lead is written deterministically. | Testable; the LLM cannot drop it. |
| D6 | Session titles go to `plan-runs.jsonl` under the existing ledger opt-in, keyed by the salted `session_run_hash`. | Same privacy flag, same prune, the file the daemon already reads; the raw session id never reaches disk. |
| D7 | First-seen models live in their own file, `first-seen.json`, not `state.json`. | Every server process rewrites the whole `State` from memory on each event (`internal/stats/recorder.go:136`), with no cross-process lock, and a 0.31 binary drops unknown fields; a field there would be clobbered. |

## 3. Signals — `scorecard.Revisit`

```go
// package scorecard (public, stdlib-only)
type Signal struct {
    ID        string     `json:"id"`
    Kind      string     `json:"kind"`                // see §3.3
    Cohort    *CohortKey `json:"cohort,omitempty"`    // Kind, ImplementerModel, ReviewModel; ServerVersion empty
    Direction string     `json:"direction,omitempty"` // "up" | "down"; drift kinds only
    Recent    *Rate      `json:"recent,omitempty"`
    Baseline  *Rate      `json:"baseline,omitempty"`
    Source    string     `json:"source,omitempty"`    // unconfirmed_flags_drift only: the outcome source scored
    Model     string     `json:"model,omitempty"`     // new_model only
    Since     *time.Time `json:"since,omitempty"`     // new_model only: when the model was first seen
}

type RevisitOptions struct {
    MinRuns int       // ≤0 means DefaultMinRuns
    Now     time.Time
}

// Gated is one (signal kind, cohort) comparison the gate refused: the
// evidence that a quiet tray means "too small to judge", not "no change".
type Gated struct {
    Kind         string    `json:"kind"`
    Cohort       CohortKey `json:"cohort"`
    RecentN      int       `json:"recent_n"`
    BaselineN    int       `json:"baseline_n"`
    RecentRuns   int       `json:"recent_runs"`
    BaselineRuns int       `json:"baseline_runs"`
}

type RevisitResult struct {
    Signals []Signal
    Gated   []Gated
}

// Revisit is pure. firstSeen maps a normalised model name to when it was first
// seen; the caller owns persisting it (§4.2).
func Revisit(lines []RunLine, outcomes []OutcomeLine, firstSeen map[string]time.Time, opts RevisitOptions) RevisitResult
```

`Signals` is sorted by `ID` and `Gated` by (kind, cohort), so both arrays are stable across
recomputes.

### 3.1 Cohorts and windows

- **Cohort:** (kind, implementer model, review model): `CohortKey` with `ServerVersion` left empty.
  The models are exactly what the scorecard's cohorts use: `run.implementerModel(index)` (the first
  outcome source in `Sources` order that reports one, else the task's own snapshot) and
  `task.reviewModel()` (the latest `validate_completion` call's model). Both return `"unknown"` when
  absent (`scorecard/assemble.go:91-141`), and `"unknown"` is a cohort like any other. `Revisit` reuses
  `assemble`, so these are the same functions, not copies. Kinds are never mixed (`plan` vs
  `session`).
- **Task time:** the timestamp of the task's latest snapshot line (`assemble.go:58-62`).
- **Recent window:** tasks in `[Now − 14d, Now]`. **Baseline:** the same cohort's tasks before
  `Now − 14d` that are still retained. With the default 30-day retention, the baseline is ~16 days.
  Both window lengths are constants (`RevisitWindow = 14 * 24h`), not configuration.
- **Gate:** each side must have ≥ `MinRuns` tasks that are *eligible for that kind* (§3.3), drawn
  from ≥ 2 distinct runs. The gate counts **tasks**, unlike the regression gate, which counts runs:
  gating plan cohorts on 10 runs per side would mean they never fire, and the distinct-run floor stops
  one ten-task plan from firing a signal on its own. `MinRuns` is the existing
  `ANTI_TANGENT_SCORECARD_MIN_RUNS` (default 10). If either side fails the gate, there is no signal,
  and the comparison is listed in `Gated` with its counts. The exception is a comparison with no
  eligible task on either side, which is not listed, so the list stays bounded by the cohorts that
  actually ran.
- Retention of 14 days or less leaves no baseline, so no drift signal can fire; the README says so.

### 3.2 Drift rule

For a proportion kind, compute `wilson(num, n)` (the existing 90% helper, `scorecard/metrics.go:20`)
on each side. Fire when the intervals are disjoint: `recent.Lo > baseline.Hi` → `up`,
`recent.Hi < baseline.Lo` → `down`. Both directions fire.

**Power** (exact binomial, per comparison, n tasks per side):

| Test | n | False fire | 20%→40% | 20%→50% |
|------|---|------------|---------|---------|
| disjoint 90% Wilson (this spec) | 10 / 20 / 40 | ~2% | 9% / 18% / 36% | 19% / 37% / 71% |
| Fisher exact, α = 0.05 | 10 / 20 / 40 | 1–3% | 6% / 19% / 41% | 13% / 41% / 75% |
| two-proportion z, α = 0.05 | 10 / 20 / 40 | 3–6% | 13% / 29% / 50% | 25% / 53% / 82% |

At a matched false-fire rate the test barely matters. The z-test's extra power is bought with more
than twice the false fires. **The lever is tasks per comparison, not the test.**

Measured on the local stats directory (2026-10-25 → 2026-11-23, post-verdicted tasks per 14-day side,
terra reviewer):

- plan · sonnet: ~200–240 per side;
- plan · haiku: ~85–95;
- plan · opus: ~20–25;
- session: 0 so far, and at ~111 whole-spec tasks a month spread over 2–3 models, about 10–25 per
  side.

So the Sonnet and Haiku plan cohorts are well powered, while Opus plan cohorts and every session
cohort are not. That is why the gated comparisons are published (§3.1, §4.1, §5.3): a quiet tray must
read as "too small to judge" where that is the truth. With about a dozen gated comparisons and two
independent looks a month, expect around 0.5 false signals a month, each acked once. The test-value
kinds cannot fire before both windows hold 0.31+ data (§3.3), so not before about mid-December 2026.

### 3.3 Kinds

| Kind | Eligible task | Numerator |
|------|---------------|-----------|
| `test_value_drift` | has a post verdict and `tests_added != nil && *tests_added > 0` | `criteria["test_value"] > 0` |
| `test_volume_drift` | has a post verdict, `tests_added != nil`, `ac_count > 0` | `*tests_added > ac_count` |
| `test_adequacy_share` | has a post verdict and the task's `serverVersion()` ≥ `0.31.0` | `categories["test_adequacy"] > 0` (`test_weakened` is `category: quality`, so excluded by construction) |
| `unconfirmed_flags_drift` | counted by the same rule as `UnconfirmedFlagRate` (`isFlag` / `outcomeCounts`), against one outcome source per run: the first in `Sources` order that the run has | that rule's numerator (`unconfNum` over `flagN`), split by window |
| `new_model` | — | see below |

**Why the eligibility rules exclude pre-0.31 lines:** `tests_added`, `criteria` and the 0.31
`test_adequacy` recalibration only exist from 0.31.0, and a nil `criteria` map cannot be told apart
from "no finding". Without these rules, a recent window that is all 0.31+ against a baseline that is
all pre-0.31 would fire `test_value_drift` up and `test_adequacy_share` down for every plan cohort in
December 2026. A task with `tests_added > 0` is the only case where the test-value lens can fire
(parent spec §4.2), so that rule excludes old lines by construction. `test_volume_drift` already
requires `tests_added`. `unconfirmed_flags_drift` sets `Signal.Source`, and the source is part of its
ID (§3.4). Choosing one source per run, as `run.implementerModel` does, keeps a run with both
`final_review` and `review_now` outcomes from counting twice.

`test_volume_drift` is the deterministic one: no reviewer judgement is involved in either count.

**`new_model`:** fires for each model whose `firstSeen` entry is non-zero and within the last 14 days
of `Now`. The models are the cohort values above (`run.implementerModel`, `task.reviewModel`), with
`"unknown"` excluded; one map holds both roles, so a model is new whichever role it first appears in.
It clears 14 days after first sight. `Revisit` does not add to `firstSeen`; the caller does (§4.2), so
the function stays pure. A companion pure function, `CohortModels(lines, outcomes) []string`, returns
exactly the set `Revisit` reads, so the caller collects first-seen models from the same values.

### 3.4 IDs

- Drift: `<kind>:<cohort.kind>/<implementer model>/<review model>`, e.g.
  `test_volume_drift:session/claude-sonnet-5-5/gpt-5.6-terra`. `unconfirmed_flags_drift` appends
  `/<source>`. Direction is not part of the id: a drift that reverses while acked stays acked until it
  clears.
- `new_model:<model>`.

IDs contain only normalised model names and fixed kind words; no free text.

## 4. Server channels

### 4.1 `scorecard.json`

`Scorecard` gains `Revisit *[]Signal \`json:"revisit,omitempty"\`` and
`RevisitCoverage *[]Gated \`json:"revisit_coverage,omitempty"\``, which carry `RevisitResult`'s two
lists. `Compute` is unchanged.
`Recorder.WriteScorecard` (`internal/stats/runs.go:96`) calls `Revisit` after `Compute`, with the same
lines and outcomes, `r.minRuns` and `r.clock()`, and sets both fields before writing. It runs where
the scorecard already runs: on every recorded outcome and at compaction.

- **When the reads succeed,** both fields point at slices, `[]` when empty, so both keys are
  present.
- **When reading `runs.jsonl` or `outcomes.jsonl` failed** (`runs.go:101-103` logs and continues),
  both fields stay nil and both keys are **absent**. An absent key tells the daemon "no answer", not
  "nothing live" (§5.2), so a transient read failure never forgets acks. The same holds for a file
  written by a pre-0.32 server.

### 4.2 First-seen models — `first-seen.json`

A new file in the stats dir, owned by `WriteScorecard` alone. `Record`, compaction's `saveState` and
every older binary never write it, so the multi-process clobbering that `state.json` is exposed to
(D7) cannot reach it.

```json
{"seeded": true, "models": {"claude-sonnet-5-5": "0001-01-01T00:00:00Z", "claude-haiku-5-5": "2026-12-03T10:12:00Z"}}
```

In `WriteScorecard`:

1. **Read failure guard.** If `runs.jsonl` or `outcomes.jsonl` failed to read, skip the whole
   first-seen step and emit no `revisit` key (§4.1).
2. Under `r.mu`: read `first-seen.json`. A missing file is `{seeded: false}`. A corrupt one logs one
   warning and is treated as missing; any other read error logs one `stats first_seen read failed`
   warning, writes nothing and passes a nil map to `Revisit`, so no `new_model` fires that call.
3. `models := scorecard.CohortModels(lines, outcomes, now)`.
4. **Seeding:** if `!seeded`, set every model in `models` to the **zero time** and set `seeded: true`.
   A zero time never fires `new_model`, so upgrading does not flag every model in use as new. The
   marker is explicit: an empty map is still seeded, so a first call that saw no models does not make
   the next call stamp everything with `clock()`.
5. Otherwise, merge: add each model missing from the map with the same `now` passed to `Revisit`, not a fresh `r.clock()`. Two processes merging
   concurrently converge by a **merge rule**: on write, re-read the file and keep, per model, zero if
   either side has zero, else the earlier time. Write atomically with the existing `writeFileAtomic`.
6. Copy the merged map and release `r.mu`. Pass the **copy** to `Revisit`. `WriteScorecard` can run
   concurrently from the outcome refresh and from compaction (`runs.go:59`, `recorder.go:145,180`), so
   no shared map crosses the lock.
7. If the write fails: one warning per call (`stats first_seen write failed`). The merged map is still
   used for this call's signals, and the next call retries. Never retried in a loop.

Entries are never pruned: one short string per model ever used.

### 4.3 The summary lead

When `sc.Revisit` is non-nil and non-empty, `Compactor.Compact` prepends one plain-text line to the
`summary.md` it writes. Plain text, because `/ui/stats` renders the summary escaped, not as Markdown
(`gnome-topbar/daemon/internal/server/statspage.go:116`):

```
Cycle review due: 2 signals — test_volume_drift up (session · claude-sonnet-5-5 / gpt-5.6-terra); new model claude-haiku-5-5
```

It names at most 3 signals and then `+N more`, because the daemon cuts the summary at 600 characters
(`gnome-topbar/daemon/internal/atstats/atstats.go:123`). It is built by a pure
`revisitLead([]Signal) string`.

- **Only `summary.md` carries the lead.** The text appended to `summaries.jsonl` is the LLM's text
  alone.
- `readPrevSummary` strips a leading `Cycle review due:` line before passing the previous summary
  into the prompt, so the LLM can neither echo a cleared lead nor duplicate a live one.
- `buildSummaryPrompt` adds a revisit block under its **own** condition (non-empty revisit), separate
  from the `ByReviewModel` one at `compactor.go:111`. It carries the signals' JSON and one sentence:
  these are drift comparisons within one cohort; the operator already sees the lead line, so do not
  restate it; do not read a signal as a verdict on quality.
- **When no summary is written** (no reviewer configured, or a reviewer or parse error:
  `compactor.go:62-64,75-82`), `summary.md` is left as it is, and the live channels are `scorecard.json`
  and the tray. D5's guarantee holds only for summaries that are actually written; the README says
  so.

### 4.4 Unchanged

No new env vars, no MCP tool changes, `rollup.json` untouched, nothing blocks.

### 4.5 Docs

- `docs/protocol/cycle.md` §7.4: one sentence naming the `revisit` array, the summary lead and the tray
  line as the trigger. Resync `plugin/anti-tangent-protocol/protocol/` in the same commit.
- README stats section: the `revisit` and `revisit_coverage` arrays, the power table's message (a quiet
  tray with a "too small to judge" count is not an all-clear), `first-seen.json`, the 14-day windows, the gate, that
  retention ≤ 14 days disables drift signals, and that the summary lead appears only when a summary is
  written.
- CHANGELOG `## [0.32.0]`: `### Added` for signals, the lead, session titles, the tray line.

## 5. Daemon and tray

### 5.1 Read

`atruns.Read` already decodes `scorecard.json` for `min_runs` (`readMinRuns`). Replace that with one
decode of `{min_runs, revisit *[]Signal, revisit_coverage *[]Gated}`, filling `Data.MinRuns`,
`Data.Revisit []scorecard.Signal` and `Data.RevisitGated []scorecard.Gated`, plus
`Data.RevisitKnown bool`. `RevisitKnown` is true **only when
the file parsed and the `revisit` key was present**.

- A missing, unparsable or pre-0.32 file, or a file the server wrote after a failed read (§4.1),
  gives `RevisitKnown = false`.
- A malformed entry makes the whole file fail to parse, as it does today; `MinRuns` then falls back
  to 0, as now.

### 5.2 Ack state

The existing `state.Store` (`seen.json`) holds acks under the id prefix `revisit:`.

- **`Store.Retain(prefix string, live []string) error`** drops every seen id that starts with
  `prefix` and is not `prefix+id` for some id in `live`. It uses `MarkSeen`'s path (build the next
  set, `persistSet`, then commit), so a failed write leaves memory and disk unchanged. When nothing
  would be dropped it is a no-op with no write.
- **Guard:** `Retain` is called only when `RevisitKnown` is true. An unknown state must not read as
  "nothing live", or a single bad read would forget every ack.
- **`state.ComputeRevisit(signals, store, now) []scorecard.Signal`**, a pure function, returns the
  unacked signals. It also hides a `new_model` signal whose `Since` is more than 14 days before `now`,
  because a signal only clears when the server recomputes, and an idle server would otherwise leave
  it in the tray indefinitely.
- `state.Snapshot` gains `Revisit []scorecard.Signal \`json:"revisit"\``.
- **Wiring** (`cmd/gnome-topbar-daemon/main.go`):
  - `refreshRuns` reads the data, then calls `p.store.Retain` **outside `p.mu`** when
    `RevisitKnown`, because it writes to disk.
  - It then takes `p.mu`, stores `runsLocal`, and calls `recompute()`.
  - `recompute` (caller holds `p.mu`) sets
    `p.snap.Revisit = state.ComputeRevisit(p.runsLocal.Revisit, p.store, now)` beside `UnackedEvents`.
  - `refreshRuns` does not call `recompute` today, so that call is new.

### 5.3 Tray

- A submenu built from a fixed pool in the style of `makeDonePool`. With n unacked signals and g
  gated comparisons, its title is:
  - `🔁 Cycle review due (n)` when g = 0;
  - `🔁 Cycle review due (n) · g too small to judge` when g > 0;
  - `🔁 Cycle review: none due · g too small to judge` when n = 0 and g > 0.

  It is hidden when n = 0 and g = 0, and whenever `RevisitKnown` is false. Gated comparisons are a
  count only: they need no ack, and the runs page holds the cohort numbers.
- One item per unacked signal. A pure `revisitLabel(Signal) string` builds the label:
  - drift: `<kind> <↑|↓> <cohort.kind> · <impl> / <review> — <recent%> vs <baseline%>` (with
    `· <source>` for `unconfirmed_flags_drift`);
  - new model: `new model: <model>`.
- Clicking an item calls `Actions.AckRevisit(id)`, which runs `store.MarkSeen([]string{"revisit:"+id})`,
  takes `p.mu`, calls `recompute`, and re-renders.
- A final item, `Open runs page`, opens `/ui/runs`.
- The tooltip's "need attention" count adds n, not g.
- **No desktop notification.** The existing event path acks on delivery, which would hide the signal
  as it is shown. Revisit acks are explicit clicks only.
- **Logging:** one warning when `Retain` or an ack's `MarkSeen` fails to persist, never one per
  signal.

## 6. Session-run titles

### 6.1 Server

- **Set the title on the row:** `sess.Row.TaskTitle = sess.Spec.Title` where the session row is
  initialised (beside `internal/session/store.go:91`). `taskSnapshot` already drops `TaskTitle` from
  run lines, so nothing content-bearing reaches `runs.jsonl`.
- **New method `Ledger.AppendSessionTitle(runHash string, title string, at time.Time) error`.** It
  writes, only when the plan ledger is enabled (`ANTI_TANGENT_PLAN_LEDGER=1` plus
  `ANTI_TANGENT_STATS_DIR`; `PlanLedger` is already in the handler deps at
  `internal/mcpsrv/server.go:28`):

  ```json
  {"plan_run_id":"","session_run_hash":"r_…","row":{"index":1,"task_title":"…"},"written_at":"…"}
  ```

- **The line is minimal.** It is not the whole `TaskRow`, and it carries **the salted run hash, not the
  raw session id**, keeping with the ledger's rule that raw ids stay off disk (`ledger.go:39-41`). The
  server already holds the hash (`run_snapshots.go:101`).
- **When it is written:** `recordSessionRow` calls it on the header-writing path (`firstWrite`), so
  once per session run. A failure is logged as plan-row appends are, and the run lines are
  unaffected.
- **Other readers:** `ledgerLine` gains `SessionRunHash string \`json:"session_run_hash,omitempty"\``.
  `plan_run_id` is empty, so `Load` (which matches non-header lines on `PlanRunID`, `ledger.go:183-188`,
  and is only ever called with a non-empty id, `handlers.go:3518,3530`), older binaries and the
  daemon's plan-row branch never match it.
- **Pruning:** `Prune` keys on `Row.CompletedAt`, else `Row.WrittenAt` (`ledger.go:269-278`), and the
  line sets `written_at`, so retention needs no change.

### 6.2 Daemon

- **Titles:** `readTitles` gains one branch. A line with a non-empty `session_run_hash` and a title is
  stored as `Titles[session_run_hash][1]`; no hashing is needed. The run-detail page already shows it
  as "Task 1: …" in `mine` scope (`runspage.go:645-653`).
- **Runs list:** for a run whose header has `kind: session` and a known title, the list's `Plan` cell
  shows the title, escaped and cut to 80 characters, linking as the hash does today
  (`runspage.go:335`). That one change is needed because the list is where a one-task run is found.
- Titles stay local, as today. Without the ledger opt-in, session runs keep showing by hash; the
  README says so.

## 7. Error handling

| Condition | Behaviour |
|-----------|-----------|
| Cohort below the gate, or one run, on either side | No signal. |
| Server read of `runs.jsonl` / `outcomes.jsonl` fails | No `revisit` key; first-seen step skipped; daemon keeps acks. |
| `first-seen.json` corrupt | One warning; treated as unseeded (zero-time seeding, no false signals). |
| `first-seen.json` write fails | One warning per call; signals from the merged map; retried next call. |
| No summary written | `summary.md` unchanged; `scorecard.json` and the tray carry the signals. |
| `scorecard.json` missing, unparsable, or without `revisit` (daemon) | No tray line; `Retain` not called; acks kept. |
| Ack persist fails | One warning; the signal stays visible; the next click retries. |
| Session title ledger append fails | Logged as plan-row appends are; run lines unaffected. |

## 8. Testing

Behaviour, not one test per function.

- **`scorecard`:** one table test over synthetic run lines and outcomes:
  - each drift kind firing up and down;
  - the gate not met on each side, and the single-run floor;
  - plan and session cohorts never compared;
  - a pre-0.31 baseline (no `tests_added`, no `criteria`, `server_version` < 0.31.0) firing nothing;
  - `test_volume_drift` ignoring nil `tests_added` and `ac_count == 0`;
  - `unconfirmed_flags_drift` counting a run that has two outcome sources once, under the first in
    `Sources` order;
  - `new_model` firing within 14 days and clearing after, with zero-time entries never firing;
  - `CohortModels` matching the cohort keys `Compute` produces for the same input;
  - `Gated` listing a comparison gated out on either side (by count and by the single-run floor), and
    leaving out a comparison with no eligible task on either side.

  Plus one assertion that output order and IDs are stable across two calls.
- **`internal/stats`:**
  - seeding on a directory without `first-seen.json` (no signal), then a later new model (signal);
  - a failed `runs.jsonl` read neither seeds nor writes `revisit`;
  - two `first-seen.json` writers merge to zero-or-earliest;
  - `-race`: concurrent `WriteScorecard` from the outcome path and the compaction path;
  - the compactor prepends the lead only when signals exist, keeps it out of `summaries.jsonl`, and
    strips it from the previous summary.
- **`planrun`:** a session title line round-trips, `Load` never matches it, and `Prune` drops it by
  `written_at`.
- **Daemon:**
  - `readTitles` resolves a session title;
  - the runs list shows it;
  - `Store.Retain` forgets only cleared `revisit:` ids and leaves other ids alone;
  - acks are kept when `revisit` is absent;
  - `ComputeRevisit` filters acked signals and stale `new_model` ones;
  - the submenu title for each n/g combination, and the menu hidden when `RevisitKnown` is false;
  - `revisitLabel` for a drift and a new-model signal.

  All through the existing pure seams; no systray in tests.
- No prompt templates change, so no goldens move. No e2e.

## 9. Rollout

1. Branch `version/0.32.0`; CHANGELOG `## [0.32.0]`; `VERSION` not bumped on the branch.
2. Order:
   1. `scorecard.Revisit`, `Signal`, `CohortModels`;
   2. `first-seen.json` and `WriteScorecard`;
   3. the summary lead;
   4. session title ledger lines;
   5. daemon read, `Store.Retain`, snapshot and wiring, tray;
   6. daemon titles and the runs list;
   7. docs, protocol resync, README, CHANGELOG.
3. Merge to `main` with `[minor]`. Then Patrick runs the `gnome-topbar-v0.5.0` release from the GitHub
   UI.
   - A 0.5.0 daemon against a pre-0.32 server shows no tray line and keeps acks.
   - A 0.4.x daemon ignores the new key and the new ledger lines.
   - A 0.31 server process sharing the stats dir neither reads nor writes `first-seen.json`. A
     scorecard it rewrites lacks the key, which the daemon treats as unknown.

## 10. Non-goals

- Team-level revisit over pooled records.
- Desktop notifications for signals.
- Configurable windows or per-kind thresholds.
- Signals on `/ui/runs` or `/ui/stats` beyond the summary lead `/ui/stats` already renders.
- Absolute bars of any kind.
- Any blocking in the MCP server.
- Hysteresis against flapping. A signal at the interval boundary can clear and refire, and its ack
  is forgotten each time. If that is observed, have `Retain` forget only after a full window of
  absence.
