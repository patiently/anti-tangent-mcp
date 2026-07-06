# gnome-topbar richer stats detail + per-model (fable) usage — design

**Date:** 2026-07-06
**Repo:** `anti-tangent-mcp` (`gnome-topbar/daemon`)
**Depends on:** sub-project ① (claude-sandbox producer emits `limits.weekly_models`; schema 1.2)
**Status:** approved (brainstorm)

## Problem

The tray surfaces only a thin slice of what is actually stored, and cannot show
Fable usage at all:

- **Claude usage:** `claudestats` decodes `limits` but has no field for the new
  per-model weekly windows (`weekly_models`, added by ①). Fable is invisible.
- **anti-tangent:** `atstats` reads `rollup.json` but keeps only `total_calls`,
  pass/warn/fail %, top category, p95, and a summary excerpt. It drops
  `per_tool`, raw `verdict_counts`, `findings_per_call`, `severity_histogram`,
  the full `category_histogram`, `review_ms_p50`, `cache_hit_rate`,
  `partial_rate`, and `model_usage`.
- The tray dropdown is a flat dbusmenu — it can't render dense detail.

## Goal

- Render per-model weekly windows (Fable et al.) in the Claude usage submenu.
- Add two rich web detail pages opened from the tray — `/ui/stats`
  (anti-tangent + CodeScene) and `/ui/claude` (per-account usage) — carrying the
  detail the flat menu can't.
- Keep the tray dropdown **lean**: only per-model rows are added inline; all
  other new detail lives on the web pages. The compact overview row and the
  top-bar icon are unchanged.

## Design

### Data flow

```
claude-sandbox producer ─ claude-stats.json (weekly_models) ─┐
                                                             ├─ Poller ─ Snapshot ─┬─ tray submenu rows
anti-tangent stats ─ rollup.json ────────────────────────────┘                    └─ /ui/stats, /ui/claude
```

`server.Provider` already exposes `Snapshot()` (carrying `AntiTangent` and
`ClaudeStats`), so the web handlers need **no interface change** — they read
`p.Snapshot()` directly.

### 1. `internal/claudestats/claudestats.go` — decode `weekly_models`

Add to the `Limits` struct:

```go
// WeeklyModels holds per-model weekly sub-limits keyed by model display_name
// (schema 1.2+, from the producer's /api/oauth/usage limits[] weekly_scoped
// entries). Empty when the producer emits none.
WeeklyModels map[string]*Window `json:"weekly_models"`
```

Tolerant reader; unknown fields already ignored, so old files (no
`weekly_models`) decode to a nil/empty map. No schema-major change.

### 2. `internal/atstats/atstats.go` — decode the full rollup

Extend the internal `rollup` decode struct and the exported `Stats` with the
fields currently dropped (for the web page). Keep the existing computed
`PassPct`/`WarnPct`/`FailPct`/`TopCategory`/`ReviewMSP95`/`Summary` for the
tray's unchanged inline label.

Add to `Stats`:

```go
PerTool           map[string]int `json:"per_tool"`
VerdictCounts     map[string]int `json:"verdict_counts"`
FindingsPerCall   float64        `json:"findings_per_call"`
SeverityHistogram map[string]int `json:"severity_histogram"`
CategoryHistogram map[string]int `json:"category_histogram"` // full map (topKey already derives TopCategory)
ReviewMSP50       int64          `json:"review_ms_p50"`
CacheHitRate      float64        `json:"cache_hit_rate"`
PartialRate       float64        `json:"partial_rate"`
ModelUsage        map[string]int `json:"model_usage"`
WindowStart       time.Time      `json:"window_start"`
WindowEnd         time.Time      `json:"window_end"`
```

### 3. Tray — per-model rows (`internal/tray/claude.go`)

In `claudeUsageRows`, after the `5h` / `7d` window rows, append one row per
`a.Limits.WeeklyModels` entry (sorted by model name), reusing the existing
`windowDetail`/`utilPct` formatting:

```
· Fable  69% · resets 20:00 (in 6h37m)
```

No change to `claudeOverviewLabels` (compact overview) or `usageIconPNG`
(top-bar icon) — the lean choice.

### 4. Tray entry points (`internal/tray/tray.go`) + actions

- Add two `Actions` callbacks: `OpenStats func()`, `OpenClaude func()`.
- Add two menu items — `📊 Stats details…` and `🤖 Claude usage details…` —
  shown only when `snap.AntiTangent.Present` / `snap.ClaudeStats.Present`
  respectively (same show/hide pattern as the existing submenus). Clicking each
  invokes the corresponding action.

### 5. Daemon wiring (`cmd/gnome-topbar-daemon/main.go`)

Add to the `tray.Actions` literal:

```go
OpenStats:  func() { openLocal("/ui/stats") },
OpenClaude: func() { openLocal("/ui/claude") },
```

(`openLocal` already appends the `?t=` token and opens the in-container browser.)

### 6. Web pages (`internal/server/`)

New pure render helpers in a new `internal/server/statspage.go` (+ test) so
`ui.go` stays focused (it is already sizable):

- `renderStatsPage(at atstats.Stats) string`
- `renderClaudePage(cs claudestats.Stats, now time.Time) string`

Register two handlers in `registerUI` (both behind `uiAuth`):

- **`/ui/stats`** — anti-tangent rollup rendered richly: verdict mix, per-tool
  split, findings/call, severity histogram, category histogram, p50/p95, cache
  hit + partial rate, model usage, window span, and the `summary.md` excerpt.
  Then the **CodeScene** block: score / delta / trend / p50 / regressions /
  improvements / neutral / category histogram when `at.CodeScene != nil`, else a
  one-line empty-state hint (see §7). Renders aggregates from `rollup.json`
  **only** — no raw `events.jsonl` table (the declined "full detail + events"
  option).
- **`/ui/claude`** — per account: display name, today/week/month usage
  (cost + tokens), active block, and limits: 5h, weekly, each `weekly_models`
  entry (Fable et al.), `extra_usage`, plus `limits.error` / stale / ccusage
  `error` states rendered visibly rather than silently dropped.

Add `📊 Stats` and `🤖 Claude usage` cards to the `/ui/search` landing page so
the pages are reachable by browsing too.

Reuse the existing dark-theme `pageShell`. All user/producer-supplied strings
(model names, error text, summary) HTML-escaped.

### 7. CodeScene empty-state

When `at.CodeScene == nil`, `/ui/stats` shows:

> **CodeScene** — no data yet. Append `analyze_change_set` records to
> `codescene-events.jsonl`; see `docs/team-setup/codescene-stats.md`.

This makes the current silent absence visible and points at sub-project ③.

### 8. Versioning / changelog

Minor feature → gnome-topbar **v0.3.0**. Add a `README.md` Changelog entry.
Follow the gnome-topbar release procedure (split code from docs-only `[skip ci]`
commits; release runs from the GitHub UI).

## Testing

- `claudestats`: extend `testdata/claude-stats.json` with a `weekly_models`
  block (incl. Fable); assert decode.
- `atstats`: fixture with the full rollup; assert every new field decodes and
  absent files still yield `Present:false`.
- `tray/claude_test.go`: assert `claudeUsageRows` emits a Fable per-model row and
  that overview/icon output is unchanged.
- `server/statspage_test.go`: substring/golden tests for `/ui/stats` (present;
  CodeScene empty-state; CodeScene present) and `/ui/claude` (per-model Fable
  row; `limits.error`; stale snapshot; multi-account). Handlers behind auth.
- All under `-race`; unit tests hit no network (render helpers are pure;
  handlers use an in-process `Provider` fake).

## Acceptance criteria

- The Claude usage submenu shows a per-model row for every `weekly_models` entry
  (Fable visible) with utilization + reset; overview row and top-bar icon
  unchanged.
- Tray shows `📊 Stats details…` and `🤖 Claude usage details…` items (only when
  the respective stats are present) that open `/ui/stats` / `/ui/claude`.
- `/ui/stats` renders the full anti-tangent rollup aggregates + CodeScene block,
  with the empty-state hint when CodeScene data is absent.
- `/ui/claude` renders per-account usage, limits, per-model windows (Fable),
  extra_usage, and error/stale states.
- `atstats` and `claudestats` decode the new fields; absent files remain
  `Present:false`; unknown fields ignored.
- Tests pass under `-race`; README Changelog updated (v0.3.0).

## Non-goals

- No change to the compact overview row or the top-bar usage icon.
- No raw `events.jsonl` table on the web page (rollup aggregates only).
- No new `Provider` interface methods (Snapshot already suffices).
- Wiring CodeScene event *production* is sub-project ③ (this only displays what
  `rollup.json` carries and hints when it's empty).
