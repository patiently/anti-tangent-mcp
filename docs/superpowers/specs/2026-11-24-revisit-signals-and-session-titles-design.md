# Revisit signals and session-run titles — design

**Status:** approved in brainstorming 2026-11-22/24.
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
| D2 | Drift = disjoint Wilson 95% intervals between a recent and a baseline window of the same cohort, each side ≥ `MinRuns` scored tasks. No tuned bars. | No data yet to tune from; disjoint intervals are conservative at small n. |
| D3 | The server computes signals; the daemon only reads `scorecard.json`. | First-seen state lives in the server's `state.json`; pooled teammates carry none, so a daemon recompute would misreport `new_model`. Deviates from §6's "shared by server and daemon": the function is public and pure, but only the server calls it. |
| D4 | Ack lasts until the signal clears. | A drift persists for weeks; re-nagging adds nothing. A recurrence after clearing is new information. |
| D5 | The summary's "Cycle review due" lead is written deterministically. | Testable; the LLM cannot drop it. |
| D6 | Session titles go to `plan-runs.jsonl` under the existing ledger opt-in, keyed `session_id`. | Same privacy flag, same prune, the file the daemon already reads. |

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
    Model     string     `json:"model,omitempty"`     // new_model only
    Since     time.Time  `json:"since"`               // new_model: first seen; drift: start of the recent window
}

type RevisitOptions struct {
    MinRuns int       // ≤0 means DefaultMinRuns
    Now     time.Time
}

// Revisit is pure. firstSeen maps a normalised model name to when it was first
// seen; the caller owns persisting it (§4.2).
func Revisit(lines []RunLine, outcomes []OutcomeLine, firstSeen map[string]time.Time, opts RevisitOptions) []Signal
```

Output is sorted by `ID` so the array is stable across recomputes.

### 3.1 Cohorts and windows

- **Cohort:** (kind, normalised implementer model, normalised review model): `CohortKey` with
  `ServerVersion` left empty. Kinds are never mixed (`plan` vs `session`). A task with no implementer
  model is in the cohort with an empty implementer model, as the scorecard already does.
- **Task time:** the timestamp of the task's latest snapshot line.
- **Recent window:** tasks in `[Now − 14d, Now]`. **Baseline:** the same cohort's tasks before
  `Now − 14d` that are still retained. With the default 30-day retention, the baseline is ~16 days.
  Both window lengths are constants (`RevisitWindow = 14 * 24h`), not configuration.
- **Gate:** each side must have ≥ `MinRuns` tasks that are *eligible for that kind* (§3.3). `MinRuns`
  is the existing `ANTI_TANGENT_SCORECARD_MIN_RUNS` (default 10). If either side fails the gate, no
  signal and no record of the miss.

### 3.2 Drift rule

For a proportion kind, compute `wilson(num, n)` (the existing `metrics.go` helper) on each side. Fire
when the intervals are disjoint: `recent.Lo > baseline.Hi` → `up`, `recent.Hi < baseline.Lo` → `down`.
Both directions fire.

### 3.3 Kinds

| Kind | Eligible task | Numerator |
|------|---------------|-----------|
| `test_value_drift` | has a post verdict | `criteria["test_value"] > 0` |
| `test_volume_drift` | has a post verdict, `tests_added != nil`, `ac_count > 0` | `*tests_added > ac_count` |
| `test_adequacy_share` | has a post verdict | `categories["test_adequacy"] > 0` (`test_weakened` is `category: quality`, so excluded by construction) |
| `unconfirmed_flags_drift` | scored against an outcome, as `UnconfirmedFlagRate` counts it today | the same numerator `UnconfirmedFlagRate` uses (`unconfNum` / `flagN`), split by window |
| `new_model` | — | see below |

`test_volume_drift` is the deterministic one: no reviewer judgement is involved in either count.

**`new_model`:** fires for each implementer or review model whose `firstSeen` entry is within the last
14 days of `Now`. Implementer models are normalised with `NormalizeImplementerModel`, review models
with `NormalizeModel`; one map holds both (a model is new whichever role it first appears in). It
clears 14 days after first sight. `Revisit` does not add to `firstSeen`; the caller does (§4.2), so
the function stays pure.

### 3.4 IDs

- Drift: `<kind>:<cohort.kind>/<implementer model>/<review model>`, e.g.
  `test_volume_drift:session/claude-sonnet-5-5/gpt-5.6-terra`. Direction is not part of the id: a
  drift that reverses while acked stays acked until it clears.
- `new_model:<model>`.

IDs contain only normalised model names and fixed kind words; no free text.

## 4. Server channels

### 4.1 `scorecard.json`

`Scorecard` gains `Revisit []Signal \`json:"revisit"\``, always present (`[]` when empty). `Compute`
is unchanged. `Recorder.WriteScorecard` (`internal/stats/runs.go`) calls `Revisit` after `Compute`
with the same lines and outcomes, `r.minRuns` and `r.clock()`, and sets `sc.Revisit` before writing.
It runs where the scorecard already runs: on every recorded outcome and at compaction.

### 4.2 First-seen models in `state.json`

`stats.State` gains `FirstSeen map[string]time.Time \`json:"first_seen,omitempty"\``.

In `WriteScorecard`, under `r.mu`:

1. Collect every normalised implementer and review model in the lines read.
2. **Seeding:** if `r.state.FirstSeen == nil` (first run of a 0.32 server on an existing directory, or
   a fresh one), set every collected model to the **zero time** (`time.Time{}`). A zero first-seen is
   never within 14 days of `Now`, so seeding raises no signal; upgrading must not flag every model in
   use as new. On a brand-new stats directory this means the very first models are not "new" either.
   That is correct, because there is nothing to revisit against.
3. Otherwise add any collected model missing from the map with `r.clock()`.
4. If the map changed, `saveState`. On failure: log one warning per call (`stats first_seen save
   failed`), keep the in-memory map, and pass it to `Revisit` anyway; the next write retries the save.
   Never retried in a loop.

Entries are never pruned: one short string per model ever used.

### 4.3 The summary lead

When `sc.Revisit` is non-empty, `Compactor.Compact` prepends one line to the summary it writes:

```
**Cycle review due:** 2 signals — test_volume_drift ↑ (session · claude-sonnet-5-5 / gpt-5.6-terra), new_model (claude-haiku-5-5)
```

`buildSummaryPrompt` also receives the revisit JSON with one added sentence: the signals are drift
comparisons within a cohort; the operator already sees the lead line, so do not restate it, and do not
interpret a signal as a verdict on quality. The lead is built by a pure `revisitLead([]Signal) string`.

### 4.4 Unchanged

No new env vars, no MCP tool changes, `rollup.json` untouched, nothing blocks.

### 4.5 Docs

- `docs/protocol/cycle.md` §7.4: one sentence naming the `revisit` array, the summary lead and the tray
  line as the trigger. Resync `plugin/anti-tangent-protocol/protocol/` in the same commit.
- README stats section: the `revisit` array, the `first_seen` field, the 14-day windows, the gate.
- CHANGELOG `## [0.32.0]`: `### Added` for signals, the lead, session titles, the tray line.

## 5. Daemon and tray

### 5.1 Read

`atruns.Read` already decodes `scorecard.json` for `min_runs` (`readMinRuns`). Replace that with one
decode of `{min_runs, revisit}` into `Data.MinRuns` and `Data.Revisit []scorecard.Signal`, plus a
`Data.RevisitRead bool` that is true only when the file was read and parsed. A pre-0.32 file has no
`revisit` key and decodes to nil with `RevisitRead` true; that means "no signals", which is right,
because a pre-0.32 server has none.

### 5.2 Ack state

The existing `state.Store` (`seen.json`) holds acks under the id prefix `revisit:`.

- `Store.Retain(prefix string, live []string) error`: drops every seen id starting with `prefix` that
  is not `prefix+id` for some id in `live`. It uses `MarkSeen`'s build-next-set → `persistSet` →
  commit path, so a failed write leaves memory and disk unchanged. It is a no-op without a write when
  nothing would be dropped.
- **Guard:** the daemon calls `Retain` only when `RevisitRead` is true. A missing or unparsable
  `scorecard.json` must not read as "nothing live", or one bad read would forget every ack.
- `state.Snapshot` gains `Revisit []scorecard.Signal \`json:"revisit"\``: the read signals whose
  `revisit:`+ID is not seen, computed beside `ComputeEvents` by a pure `ComputeRevisit`.

### 5.3 Tray

- A submenu `🔁 Cycle review due (n)`, hidden at n = 0, built from a fixed pool like `makeDonePool`.
- One item per unacked signal. Label: `<kind> <↑|↓> <cohort.kind> · <impl> / <review> — <recent%> vs
  <baseline%>`, or `new model: <model>`. A pure `revisitLabel(Signal) string` builds it.
- Clicking an item calls `Actions.AckRevisit(id)`, which does `MarkSeen("revisit:"+id)` and re-renders.
- A final item, `Open runs page`, opens `/ui/runs`.
- The tooltip's "need attention" count adds n.
- **No desktop notification.** The existing event path acks on delivery, which would hide the signal as
  it is shown. Revisit acks are explicit clicks only.
- Logging: one warning when `Retain` or the ack's `MarkSeen` fails to persist, never one per signal.

## 6. Session-run titles

### 6.1 Server

When the plan ledger is enabled (`ANTI_TANGENT_PLAN_LEDGER=1` plus `ANTI_TANGENT_STATS_DIR`),
`recordSessionRow` (`internal/mcpsrv/run_snapshots.go`) also appends a ledger line whenever it writes
run lines (that is, once the row has a post verdict):

```json
{"plan_run_id":"","session_id":"<id>","row":{"index":1,"task_title":"<Session.Title>", "...": "…"}}
```

- `ledgerLine` gains `SessionID string \`json:"session_id,omitempty"\``. This is a different field
  from `TaskRow.SessionID`, which is `json:"-"` and stays so.
- `plan_run_id` is empty, so the daemon's `readTitles`, `Ledger.Load(id)` for any real id, and an older
  binary sharing the file never match it as a plan-run row. `Load` and every other ledger reader that
  matches on `plan_run_id` skip lines with a non-empty `session_id` explicitly, so `Load("")` cannot
  match them.
- `Prune` keys on `Row.CompletedAt` / `WrittenAt`, which these lines carry; retention needs no change.
- The ledger write needs a `Ledger` handle in the handler deps where it is not already reachable;
  failures are logged like the existing plan-row ledger appends (no new log line shape).

### 6.2 Daemon

`readTitles` gains one branch: a line with a non-empty `session_id` and a title is keyed by
`scorecard.HashSessionRunID(salt, session_id)` at index 1. `/ui/runs` already looks up
`Titles[hash][index]`, so session runs show their title with no page change. Titles stay local, as
today.

Without the ledger opt-in, session runs keep showing by hash; the README says so.

## 7. Error handling

| Condition | Behaviour |
|-----------|-----------|
| Cohort below the gate on either side | No signal. |
| `first_seen` save fails | One warning per call; signals from the in-memory map; retried next write. |
| `scorecard.json` missing or unparsable (daemon) | No tray line; `Retain` not called; acks kept. |
| Malformed `revisit` entry | The whole file fails to parse as today (`readMinRuns` returns 0); same guard. |
| Ack persist fails | One warning; the signal stays visible; the next click retries. |
| Ledger session-line append fails | Logged as plan-row appends are; run lines unaffected. |

## 8. Testing

Behaviour, not one test per function.

- **`scorecard`:** one table test over synthetic run lines and outcomes: each drift kind firing up and
  down; gate not met on each side; plan and session cohorts not compared; `test_volume_drift` ignoring
  tasks with nil `tests_added` or `ac_count == 0`; `new_model` firing within 14 days and clearing
  after; zero-time seeded entries never firing. One assertion that output order and IDs are stable
  across two calls.
- **`internal/stats`:** `WriteScorecard` writes `revisit` and persists `first_seen`, seeding on a
  directory with no `first_seen` (no signal) and stamping a later new model (signal). The compactor
  prepends the lead only when signals exist (`revisitLead` covered by the same test).
- **`planrun`:** a session ledger line round-trips, is invisible to `Load` for any id including `""`,
  and is pruned by `Prune`.
- **Daemon:** `readTitles` resolves a session title; `Store.Retain` forgets only cleared `revisit:` ids
  and leaves other ids alone; `ComputeRevisit` filters acked signals; the read-failure guard keeps
  acks; `revisitLabel` for a drift and a new-model signal. All through the existing pure seams; no
  systray in tests.
- No prompt templates change, so no goldens move. No e2e.

## 9. Rollout

1. Branch `version/0.32.0`; CHANGELOG `## [0.32.0]`; `VERSION` not bumped on the branch.
2. Order: (1) `scorecard.Revisit` + `Signal`; (2) `state.json` `first_seen`, `WriteScorecard`, the
   summary lead; (3) session ledger lines; (4) daemon read, `Store.Retain`, snapshot, tray; (5) daemon
   titles; (6) docs, protocol resync, README, CHANGELOG.
3. Merge to `main` with `[minor]`. Then Patrick runs the `gnome-topbar-v0.5.0` release from the GitHub
   UI. A 0.5.0 daemon against a pre-0.32 server shows no tray line; a 0.4.x daemon ignores the new key.

## 10. Non-goals

- Team-level revisit over pooled records.
- Desktop notifications for signals.
- Configurable windows or per-kind thresholds.
- Signals on `/ui/runs` or `/ui/stats` beyond the summary lead `/ui/stats` already renders.
- Absolute bars of any kind.
- Any blocking in the MCP server.
