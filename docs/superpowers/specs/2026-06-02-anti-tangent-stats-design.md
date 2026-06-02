# anti-tangent stats + periodic LLM performance summary — design

**Status:** approved design (brainstorming output), pre-implementation
**Date:** 2026-06-02
**Release vehicle:** `version/0.10.0` (backward-compatible minor; current released is 0.9.1) with a `## [0.10.0]` CHANGELOG `### Added` entry.

---

## 1. Summary

Add an **opt-in** subsystem that records one compact statistics record at the end of every
hook call and, on a regular cadence, asks a reviewer LLM to write a short prose
"performance summary" of recent activity. Everything is written to a directory the operator
chooses via `ANTI_TANGENT_STATS_DIR`. When that variable is unset, the server behaves
exactly as today — no files, no overhead, no behavior change.

This deliberately relaxes one stated non-goal (no persistent storage) in a **scoped,
opt-in, off-by-default** way. It does **not** add a metrics endpoint — output is plain files,
not a served endpoint — so that non-goal is preserved.

A **companion** (§12) lets the same `ANTI_TANGENT_STATS_DIR` additionally accumulate CodeScene
Code Health metrics: the agent appends a raw record per task (anti-tangent's server never sees
those calls), and the Compactor aggregates them into `rollup.json`. It adds **no new MCP tool**;
its only Go change is the Compactor aggregation step (§12.4).

### 1.1 Why

- Make anti-tangent's own activity observable over time: throughput, verdict mix, what kinds
  of findings it surfaces most, latency, model usage, truncation/cache rates.
- Provide the machine-readable data source for the deferred gnome-topbar "anti-tangent stats"
  panel (`rollup.json`).
- Keep it cheap and unobtrusive: a single appended line per call, an occasional small LLM call.

### 1.2 Goals

- Zero behavior change when `ANTI_TANGENT_STATS_DIR` is unset (backward-compat guarantee,
  matching how every other optional feature in this repo is gated).
- A hook call must **never** fail or measurably slow down because of stats. Best-effort,
  errors swallowed-and-logged.
- Privacy by construction: records contain **counts + metadata only** — no finding text, no
  plan/spec/task content, no raw session ids.

### 1.3 Non-goals

- **No new MCP tool / served endpoint.** Output is files on disk; consumers (e.g. the
  gnome-topbar panel) read those files directly.
- **No correctness / precision / recall metrics.** anti-tangent is advisory and has no ground
  truth on whether a finding was right or acted upon. The summary is *descriptive* only.
- **No background daemon / goroutine ticker.** Compaction is opportunistic (§5).
- **No OTel / Prometheus exporter.**
- **No finding text persistence.** Counts and categories only.

---

## 2. Architecture

A new package `internal/stats`, wired into the existing MCP server through one nil-safe
dependency. The package has three small, independently testable units plus the shared types.

```
                          end of every hook call
                                   │
   internal/mcpsrv (handlers) ─────┤  h.Stats.RecordEnvelope(tool, env, payloadBytes, cached)
                                   ▼
   internal/stats.Recorder ── append Event ──► events.jsonl
        │  (mutex; bumps "events since last summary"; checks trigger)
        │
        ├─ trigger due? ── go (single-flight) ─► Compactor
        │                                          │ recompute Rollup ─► rollup.json
        │                                          │ Reviewer.Review(rollup) ─► summary.md (+ summaries.jsonl)
        │                                          │ prune events past retention ─► events.jsonl
        ▼
   state.json  {last_summary_at, events_since_summary, salt}
```

When `ANTI_TANGENT_STATS_DIR` is unset, `Deps.Stats` is `nil` and every call site is a
nil-check no-op.

---

## 3. Components (`internal/stats`)

Each unit has one responsibility, a small interface, and is unit-testable without network.

### 3.1 `Event` (record shape — counts + metadata only)

```
Event {
  Ts             time.Time          // truncated to the second
  Tool           string             // validate_plan | validate_task_spec | check_progress |
                                     //   validate_completion | prime_project_knowledge |
                                     //   extract_project_knowledge
  Verdict        string             // "pass" | "warn" | "fail" | ""  (empty for tools whose
                                     //   envelope carries no verdict)
  FindingsTotal  int
  SeverityCounts map[string]int     // "critical" | "major" | "minor" -> n
  CategoryCounts map[string]int     // verdict.Category -> n
  ReviewMS       int64
  Model          string             // provider:model actually used (from Envelope.ModelUsed)
  Cached         bool
  Partial        bool
  PayloadBytes   int
  SessionHash    string             // salted hash of the session id, or "" — raw id never stored
}
```

Serialized one-per-line to `events.jsonl`.

### 3.2 `Recorder`

- Constructed only when stats are enabled; otherwise the `*Recorder` is `nil` and all call
  sites no-op.
- Seam is generic: `Record(Event)`. The `stats` package imports only `internal/verdict`
  (for `Finding`/`Category`/`Severity`), never `internal/mcpsrv`, so there is no import
  cycle — handlers (in `mcpsrv`) import `stats`, not the reverse.
- A shared helper `stats.CountFindings([]verdict.Finding) (severity, category map[string]int, total int)`
  builds the histograms; each handler constructs its `Event` from the result it already has
  (see §6 — the result shapes differ across tools) and calls `Record`.
- Appends the JSON line under a mutex; increments an in-memory `eventsSinceSummary`.
- After appending, evaluates the compaction trigger (§5) and, if due and no compaction is
  in flight, launches the `Compactor` in a goroutine (single-flight guarded).
- **Best-effort:** any error (marshal, write, mkdir) is logged to stderr via `slog` and
  swallowed. The method has no error return that the handler must handle.

### 3.3 `Rollup` (deterministic, no LLM)

Aggregation computed by reading `events.jsonl`. **The `json` tags below are a load-bearing
cross-component contract** — the gnome-topbar consumer (branch `feat/gnome-topbar`, plan Task 17)
reads `rollup.json` by these exact snake_case keys. Go marshals struct fields PascalCase by
default, which would silently break that consumer, so every field carries an explicit tag:

```go
Rollup struct {
  WindowStart       time.Time      `json:"window_start"`
  WindowEnd         time.Time      `json:"window_end"`
  TotalCalls        int            `json:"total_calls"`
  PerTool           map[string]int `json:"per_tool"`
  VerdictCounts     map[string]int `json:"verdict_counts"`      // keys: pass, warn, fail
  FindingsPerCall   float64        `json:"findings_per_call"`   // mean
  SeverityHistogram map[string]int `json:"severity_histogram"`
  CategoryHistogram map[string]int `json:"category_histogram"`
  ReviewMSP50       int64          `json:"review_ms_p50"`
  ReviewMSP95       int64          `json:"review_ms_p95"`
  CacheHitRate      float64        `json:"cache_hit_rate"`
  PartialRate       float64        `json:"partial_rate"`
  ModelUsage        map[string]int `json:"model_usage"`
  GeneratedAt       time.Time      `json:"generated_at"`
}
```

Written to `rollup.json`. This is the machine-readable file external consumers read. It is
recomputed during compaction (and always written, even if the LLM step later fails).
**Changing or dropping any key above is a breaking change for the consumer** — coordinate
across both branches before doing so.

### 3.4 `Compactor`

On trigger (run async, single-flight):

1. Read `events.jsonl`; compute `Rollup`; write `rollup.json`.
2. Build a compact prompt from the `Rollup` (plus a short delta-vs-previous-window note if a
   prior summary exists) and call the stats `providers.Reviewer` (model = `StatsModel`,
   max tokens = `StatsMaxTokens`, the existing `RequestTimeout`).
3. On success: overwrite `summary.md` with the latest narrative and append a timestamped
   entry (window + text) to `summaries.jsonl`.
4. On LLM error/timeout: `rollup.json` is already written; **skip** `summary.md` (machine
   stats stay fresh even when the narrative fails). Log and continue.
5. Prune `events.jsonl` of records older than the retention window (`StatsRetentionDays`),
   then update `state.json` (`last_summary_at = now`, `events_since_summary = 0`).

The summary prompt instructs the reviewer to produce a brief, descriptive operational
report — verdict rates, finding density, dominant categories, latency, model usage, and the
trend vs the previous window — and to **avoid** any claim about whether findings were correct
or acted upon (no ground truth exists).

---

## 4. On-disk layout (under `$ANTI_TANGENT_STATS_DIR`)

```
events.jsonl      # append-only per-call records (counts + metadata), pruned by retention
rollup.json       # deterministic aggregate stats (machine-readable; gnome-topbar reads this)
summary.md        # latest LLM performance narrative (overwritten each compaction)
summaries.jsonl   # history: one entry per compaction (ts + window + text)
state.json        # {last_summary_at, events_since_summary, salt}
```

`state.json` persists the cadence and the session-hash salt across process restarts, so a
freshly-launched stdio server neither re-summarizes immediately nor loses the interval. The
salt is generated once (crypto-random) on first enable and reused thereafter.

---

## 5. Compaction trigger

Evaluated at the end of each recorded call:

```
due := (now - state.last_summary_at) >= StatsSummaryInterval
     || state.events_since_summary    >= StatsSummaryThreshold
```

If `due` and no compaction is currently in flight, launch one asynchronously. Single-flight
(one in-flight at a time) means a burst of calls cannot stack compactions; the async launch
means the hook returns immediately. This honors "regular intervals" while remaining robust to
the stdio server's short, bursty lifetime: the interval fires on the next call after it
elapses, and the count threshold covers busy stretches.

---

## 6. Integration seam

- `mcpsrv.Deps` gains `Stats *stats.Recorder` (nil ⇒ disabled). Constructed in
  `cmd/anti-tangent-mcp/main.go` from config; passed into `mcpsrv.New`.
- The result shapes differ across tools, so each handler maps **its own** result into a
  `stats.Event` at its finalize point (one nil-safe call: `if h.Stats != nil { h.Stats.Record(ev) }`):
  - **`validate_task_spec`, `check_progress`, `validate_completion`** — from the shared
    `mcpsrv.Envelope` (`Verdict`, `Findings`, `ModelUsed`, `ReviewMS`, `Partial`).
  - **`validate_plan`** — from its `PlanResult`: `plan_verdict` → `Verdict`; findings
    aggregated across the plan-level and per-task results → histograms; `review_ms`, model,
    `cached`, `partial` from the plan envelope.
  - **`prime_project_knowledge`, `extract_project_knowledge`** — from their result shapes:
    their findings (e.g. `kb_gap`, `insufficient_evidence`) → `CategoryCounts`; `Verdict`
    is `""` (these tools have no pass/warn/fail verdict); `review_ms`/model as available.
  All six share `stats.CountFindings` for the histogram mapping; the payload byte count and
  `cached` flag are values each handler already has on hand. Recording prime/extract is the
  only part that reads result shapes the implementer must confirm per handler; if a field is
  absent there, it is recorded as zero/empty (the `Event` tolerates partial population).
- Disabled path cost: a single `if h.Stats != nil` per call. No allocation, no I/O.

---

## 7. Configuration (new `ANTI_TANGENT_STATS_*` vars)

All parsed and validated in `internal/config` exactly like the existing vars (positive-int /
duration / model-ref checks, named errors on bad values).

| Var | Default | Meaning |
|---|---|---|
| `ANTI_TANGENT_STATS_DIR` | `""` (disabled) | enable + output directory |
| `ANTI_TANGENT_STATS_MODEL` | falls back to `MidModel` | summarizer model (cheap by default) |
| `ANTI_TANGENT_STATS_SUMMARY_INTERVAL` | `24h` | time trigger (Go duration) |
| `ANTI_TANGENT_STATS_SUMMARY_THRESHOLD` | `50` | count trigger (positive int) |
| `ANTI_TANGENT_STATS_RETENTION_DAYS` | `30` | event pruning window (positive int) |
| `ANTI_TANGENT_STATS_MAX_TOKENS` | `2048` | summary output cap (ceiling-clamped by `ANTI_TANGENT_MAX_TOKENS_CEILING`) |

`StatsModel` resolution: explicit `ANTI_TANGENT_STATS_MODEL` → else `MidModel`. The chosen
model is validated against the provider allowlist at startup (same as the other model refs);
an unconfigured provider (missing API key) disables the **summary** step only — recording and
the deterministic rollup still work.

---

## 8. Error handling

Best-effort end to end; stats never affects hook semantics or latency:

- `ANTI_TANGENT_STATS_DIR` set but not creatable/writable → log a warning at startup and run
  with stats **disabled** (do not fail startup).
- Append/marshal error → logged + swallowed; the hook result is unaffected.
- Compaction is async + single-flight; an LLM error leaves `rollup.json` fresh and skips the
  narrative.
- Concurrency: the MCP server may serve concurrent calls; the recorder's append + counter are
  mutex-guarded, and `events.jsonl` appends are independent lines.

---

## 9. Testing (`go test -race`, no network)

- **Recorder:** append produces a parseable JSONL line; a `nil` recorder is a no-op; mapping a
  `verdict.Result` (mixed severities/categories) yields the correct `SeverityCounts` /
  `CategoryCounts` / `FindingsTotal`.
- **Rollup:** deterministic aggregation over a fixed event set → expected percentiles, rates,
  and histograms (including empty/edge inputs).
- **Trigger:** table tests over `(elapsed, count)` → `due`; single-flight guard prevents
  stacked compactions; `state.json` round-trips `last_summary_at` / `events_since_summary` /
  `salt` across reload.
- **Compactor:** with a **fake `providers.Reviewer`** returning a canned summary → `rollup.json`
  + `summary.md` + `summaries.jsonl` written; on reviewer error → `rollup.json` written,
  `summary.md` absent.
- **Retention:** events older than the window are pruned, newer kept, and the rollup is
  computed *before* pruning.

Unit tests must not hit the network (repo convention); the reviewer is always faked here.

---

## 10. Docs + non-goal reversal

- **`CLAUDE.md` "What This Repo Is Not":** amend the persistence line to: persistence is
  **opt-in and off by default** (the stats subsystem); keep "no metrics endpoint" as-is and
  note that stats output is files, not a served endpoint.
- **Design spec (`2026-05-07-anti-tangent-mcp-design.md`) non-goals:** add a pointer noting the
  v0.10.0 opt-in stats subsystem relaxes "no persistent storage" in a gated way; metrics
  endpoint remains a non-goal.
- **README:** add the `ANTI_TANGENT_STATS_*` vars to the dotenv block, with a one-paragraph
  description of the on-disk files.
- **CHANGELOG:** `## [0.10.0]` with an `### Added` entry. The version-branch ↔ CHANGELOG CI
  check requires this when the `version/0.10.0` branch is pushed.

---

## 11. Acceptance ("done")

- With `ANTI_TANGENT_STATS_DIR` **unset**, behavior is byte-for-byte unchanged and no files are
  written (verified by a test that runs a hook with a nil recorder and asserts no I/O).
- With it set, each hook call appends one counts-only record to `events.jsonl`.
- After the interval or threshold is crossed, a `rollup.json` and (when the reviewer succeeds)
  a `summary.md` + `summaries.jsonl` entry appear; `state.json` advances; old events are pruned.
- An unwritable stats dir or a reviewer error never causes a hook to fail or slow.
- No finding text, plan/spec content, or raw session id appears in any file.
- `go test -race ./...` passes; `CLAUDE.md`, the design spec, README, and CHANGELOG are updated.

---

## 12. Companion: capturing CodeScene metrics over time

A mostly protocol-layer addition (plus a small Compactor extension) that lets operators
accumulate CodeScene Code Health metrics in the same `ANTI_TANGENT_STATS_DIR`, aggregated into
the same `rollup.json` the consumer already reads.

### 12.1 Why this can't reuse §2–§6

CodeScene is a *separate* MCP server we don't own (the open-source
[codescene-oss](https://github.com/codescene-oss/codescene-mcp-server) one). Its tools run in
the **agent's MCP-client context** — anti-tangent's Go binary never sees those calls, so the
server-side `Recorder` (§3.2) structurally cannot record them. CodeScene also keeps **no
history**: each call recomputes Code Health and discards it. To get a trend, *something at the
protocol layer must persist each run*. This is the same reason the rest of the CodeScene
integration "lives at the dispatch-clause layer" (INTEGRATION.md, "CodeScene MCP companion").

### 12.2 Mechanism — the agent appends the raw record

This adds **no new MCP tool** (honoring §1.3 non-goal #1). The agent that ran CodeScene appends
one raw record; a small Compactor extension (§12.4) aggregates those records into `rollup.json`.
The append:

- **Granularity:** one record per task, sourced from the **pre-DONE `analyze_change_set`**
  result (the branch-vs-base Code Health delta — the meaningful per-task metric).
- **Writer:** in the subagent/controller model, the **controller** appends from the
  implementer's DONE report (which already may carry the CodeScene delta per INTEGRATION.md
  §3b); in the inline / vanilla-`CLAUDE.md` model, the single agent appends when it runs
  `analyze_change_set`.
- **Not recorded:** mid-task `pre_commit_code_health_safeguard` (intermediate / noisy; the
  controller-from-DONE path only sees the final result) and `code_health_review` drill-downs.
- **Gating:** only when `ANTI_TANGENT_STATS_DIR` is set **and** CodeScene is configured.
  Neither → nothing is written, no behavior change (parity with the rest of §1.2).

### 12.3 Per-run record shape (counts + metadata only — privacy parity with §3.1)

The agent appends one JSON-per-line to `$ANTI_TANGENT_STATS_DIR/codescene-events.jsonl`. This
file is the durable per-run history; consumers read the *aggregate* in `rollup.json` (§12.4),
not this file:

```json
{
  "ts": "2026-06-02T14:03:00Z",
  "tool": "analyze_change_set",
  "score_before": 8.7,
  "score_after": 8.4,
  "delta": -0.3,
  "trend": "regression",
  "files_analyzed": 5,
  "category_counts": {"complex-method": 2, "bumpy-road": 1}
}
```

- `trend` ∈ `regression | improvement | neutral` (sign of `delta`).
- **No file paths, no code, no raw session id** — same posture as the §3.1 `Event`.
- **Best-effort and tolerant:** any field CodeScene doesn't return is omitted; a failed append
  is swallowed and never blocks DONE.
- A **separate file** from anti-tangent's `events.jsonl`. The Compactor reads *both* but keeps
  them in distinct rollup sections (anti-tangent's own counts at the top level; CodeScene under
  the `codescene` key, §12.4) — they are never mixed into one histogram.

### 12.4 Aggregation into `rollup.json` (Compactor extension)

During compaction (§3.4 step 1) the Compactor *additionally* reads `codescene-events.jsonl`,
aggregates the records in the current window, and writes a nested `codescene` block into
`rollup.json`. This is the **only Go change** in this companion; the agent-side append (§12.2)
stays at the protocol layer. The block keys are a snake_case cross-component contract, same
posture as §3.3:

```go
// Rollup (§3.3) gains one field:
Codescene *CodesceneRollup `json:"codescene,omitempty"`  // absent when no CodeScene events in window

CodesceneRollup struct {
  Runs              int            `json:"runs"`
  LatestScore       float64        `json:"latest_score"`
  LatestDelta       float64        `json:"latest_delta"`
  LatestTrend       string         `json:"latest_trend"`        // regression|improvement|neutral
  ScoreP50          float64        `json:"score_p50"`
  Regressions       int            `json:"regressions"`
  Improvements      int            `json:"improvements"`
  Neutral           int            `json:"neutral"`
  CategoryHistogram map[string]int `json:"category_histogram"`
  WindowStart       time.Time      `json:"window_start"`
  WindowEnd         time.Time      `json:"window_end"`
}
```

The Compactor's retention step (§3.4 step 5) **also** prunes `codescene-events.jsonl` older than
`StatsRetentionDays`, exactly as it prunes `events.jsonl`. If the stats dir is set but
`codescene-events.jsonl` is absent (CodeScene never ran), the `codescene` key is simply omitted —
no error.

### 12.5 Consumer ingestion

The gnome-topbar consumer reads **one file** — `rollup.json` — and looks for the optional
`rollup.codescene` block. Absence means "no CodeScene data this window," not an error. The raw
`codescene-events.jsonl` remains the durable per-run history, but consumers need not parse it.

### 12.6 Deliverables

- **Go (Compactor + Rollup):** extend the `internal/stats` Compactor to read, aggregate, and
  retention-prune `codescene-events.jsonl`, and add the `Codescene *CodesceneRollup` field to
  `Rollup` (§12.4). `-race` unit tests over a fixed CodeScene event set: aggregation math,
  absent/empty file → `codescene` key omitted, retention pruning. No network (repo convention).
- **`docs/team-setup/codescene-stats.md`** (new) — authoritative detail: the per-run record
  shape (§12.3), the `rollup.codescene` contract (§12.4), the dispatch wording the
  controller/agent uses to append, and the privacy note.
- **`INTEGRATION.md`** — a ≤ ~200-byte pointer added to the "CodeScene MCP companion
  (optional)" section, naming `codescene-events.jsonl` and linking the team-setup doc. The
  short filename keeps the pointer small. The file **must stay < 40,000 bytes** (the v0.9.1 CI
  `INTEGRATION.md size budget` job fails at ≥ 40,000); verify with the same `wc -c` check CI
  runs and, if it would exceed, trim the equivalent bytes from adjacent redundant companion
  prose.
- **`CHANGELOG.md`** — a line under the `## [0.10.0]` `### Added` block.
- **Follow-up (outside this repo):** mirror the INTEGRATION.md pointer into the global
  `~/.claude/anti-tangent.md`, per the INTEGRATION.md ↔ global-mirror convention.

### 12.7 Acceptance ("done") — companion

- With `ANTI_TANGENT_STATS_DIR` unset or CodeScene not configured, nothing is written, there is
  no behavior change, and `rollup.json` carries no `codescene` key.
- With both set, a completed task whose DONE report carries an `analyze_change_set` result
  yields exactly one counts-only line appended to `codescene-events.jsonl`.
- After a compaction, `rollup.json` contains a `codescene` block with the §12.4 snake_case keys
  aggregated over the window; CodeScene records older than `StatsRetentionDays` are pruned.
- No file paths, code, or raw session id appear in any `codescene-events.jsonl` record.
- `INTEGRATION.md` stays < 40,000 bytes; the new `docs/team-setup/codescene-stats.md` and the
  `## [0.10.0]` CHANGELOG entry exist; `go test -race ./...` is green.
