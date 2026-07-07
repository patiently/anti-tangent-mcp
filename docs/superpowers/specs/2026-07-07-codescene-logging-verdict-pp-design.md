# CodeScene logging hook + verdict/pp contract redesign — design

**Date:** 2026-07-07
**Repo:** `anti-tangent-mcp` (server `internal/stats` + `gnome-topbar/daemon`) + a host `PostToolUse` hook
**Status:** approved (brainstorm) — sub-project ③
**Supersedes contract in:** `docs/team-setup/codescene-stats.md`

## Problem

`docs/team-setup/codescene-stats.md` and `internal/stats/codescene.go` define the
per-run CodeScene record around a numeric `score_before`/`score_after`/`delta`.
The actual codescene-oss MCP `analyze_change_set` tool does **not** return a
score — verified live 2026-07-07. It returns **categorical** data:

```json
{ "quality_gates": "passed|failed",
  "results": [ { "name": "<file>", "verdict": "improved|degraded|stable",
    "findings": [ { "category": "Complex Method|Bumpy Road Ahead|Deep, Nested Complexity|Complex Conditional|Overall Code Complexity|…",
      "change-type": "introduced|degraded|improved|fixed|unchanged",
      "new-pp": <problem-points>, "old-pp": <…>, "threshold": <…>,
      "change-details": [ … ] } ] } ] }
```

(A 1–10 score exists only via the *single-file* `code_health_score`, not for a
change set.) So the score-based record is unimplementable from `analyze_change_set`,
and CodeScene stats never populate because **nothing writes `codescene-events.jsonl`**.

## Goal

Automatically capture each `analyze_change_set` run as a counts-only record, and
redesign the record + rollup + display around what the tool actually reports
(verdicts, quality-gate, problem-points, category counts), so a CodeScene trend
surfaces in the gnome-topbar tray end-to-end.

## Design

### Data flow

```
analyze_change_set runs
  → PostToolUse hook parses tool_response, appends 1 line to codescene-events.jsonl
  → anti-tangent Compactor (internal/stats/codescene.go) aggregates → rollup.json "codescene" block
  → gnome-topbar reads rollup.json → tray codeScene line + /ui/stats CodeScene block
```

Matching only `analyze_change_set` records the intended calls — it *is* the
branch-vs-base tool; the mid-task tools (`pre_commit_code_health_safeguard`,
`code_health_review`) aren't matched, so no dedup is needed. One record per
`analyze_change_set` run.

### New record shape (`codescene-events.jsonl`)

Counts-only — **no file paths, no code, no function names, no session id**
(privacy parity with anti-tangent's own `events.jsonl`):

```json
{
  "ts": "2026-07-07T09:05:00Z",
  "tool": "analyze_change_set",
  "quality_gate": "passed",
  "files_analyzed": 8,
  "verdicts": { "improved": 1, "degraded": 6, "stable": 1 },
  "trend": "regression",
  "net_pp": 2.30,
  "category_counts": { "Complex Method": 5, "Bumpy Road Ahead": 2, "Deep, Nested Complexity": 2 }
}
```

- `quality_gate` = `results`-level `quality_gates` (`passed`/`failed`).
- `files_analyzed` = `len(results)`.
- `verdicts` = per-file `verdict` tally.
- `net_pp` = `Σ(finding.new-pp) − Σ(finding.old-pp)` across all findings (missing
  side → 0; `introduced` contributes `+new-pp`, `fixed` contributes `−old-pp`).
  Positive = more problem points after = worse.
- `trend` = sign of `net_pp`: `>0 → regression`, `<0 → improvement`, `0 → neutral`.
- `category_counts` = count of findings per `category`.

### Component 1 — the hook

A `bash`+`jq` script shipped in this repo at `examples/hooks/codescene-log.sh`,
wired via a scoped `PostToolUse` entry in `~/.claude/settings.json`:

```json
{ "hooks": { "PostToolUse": [
  { "matcher": "mcp__codescene__analyze_change_set",
    "hooks": [ { "type": "command", "command": "<repo>/examples/hooks/codescene-log.sh", "timeout": 10 } ] } ] } }
```

(Additive to the existing `matcher:"*"` superset-notifier entry — both fire.)

Behaviour:
- Read stdin JSON; extract the `analyze_change_set` result from `tool_response`.
- Build the record via `jq`; append one line to `$ANTI_TANGENT_STATS_DIR/codescene-events.jsonl`.
- **Fire-and-forget:** always `exit 0`. Silent skip when `ANTI_TANGENT_STATS_DIR`
  is unset, the response is unparseable, or `results` is empty/absent.

**IMPLEMENTATION STEP 0 (blocking, do first):** empirically pin the `tool_response`
shape. Claude Code docs indicate `tool_response` is a string, but the MCP
content-block wrapper is uncertain. Install a throwaway debug hook
(`command: cat >> /tmp/cs-hook-stdin.json`) on the same matcher, trigger one real
`analyze_change_set`, and inspect the captured stdin to confirm whether the result
is at `.tool_response` (string to re-parse) or `.tool_response.content[0].text`.
Write the parser against the observed shape; remove the debug hook.

### Component 2 — server `internal/stats/codescene.go` (+ test)

Redesign the read + rollup contract. anti-tangent **server** change → its own
`version/X.Y.Z` branch + `CHANGELOG.md` entry.

`CodesceneEvent` (read shape) becomes:

```go
type CodesceneEvent struct {
	Ts             time.Time      `json:"ts"`
	Tool           string         `json:"tool"`
	QualityGate    string         `json:"quality_gate"`   // passed|failed
	FilesAnalyzed  int            `json:"files_analyzed"`
	Verdicts       Verdicts       `json:"verdicts"`
	Trend          string         `json:"trend"`          // improvement|regression|neutral
	NetPP          float64        `json:"net_pp"`
	CategoryCounts map[string]int `json:"category_counts,omitempty"`
}
type Verdicts struct{ Improved, Degraded, Stable int } // json improved/degraded/stable
```

`CodesceneRollup` (rollup.json block) — drop `latest_score`/`latest_delta`/
`score_p50`; keep `runs`/`regressions`/`improvements`/`neutral`/`category_histogram`/
window; add gate + pp + files:

```go
type CodesceneRollup struct {
	Runs              int            `json:"runs"`
	GatesPassed       int            `json:"gates_passed"`
	GatesFailed       int            `json:"gates_failed"`
	LatestGate        string         `json:"latest_gate"`
	LatestTrend       string         `json:"latest_trend"`
	LatestNetPP       float64        `json:"latest_net_pp"`
	NetPPP50          float64        `json:"net_pp_p50"`
	Regressions       int            `json:"regressions"`   // runs with trend=regression
	Improvements      int            `json:"improvements"`  // trend=improvement
	Neutral           int            `json:"neutral"`       // trend=neutral
	FilesAnalyzed     int            `json:"files_analyzed"` // sum across runs in window
	CategoryHistogram map[string]int `json:"category_histogram"`
	WindowStart       time.Time      `json:"window_start"`
	WindowEnd         time.Time      `json:"window_end"`
}
```

`computeCodescene`: `Runs=len`; gate/trend counts; `Latest*` from the newest `Ts`;
`NetPPP50` via the existing percentile helper (scale `net_pp*100` → int64 → /100,
matching the old ScoreP50 pattern; sort handles negatives); `FilesAnalyzed` = Σ;
`CategoryHistogram` aggregates `CategoryCounts`; window from min/max `Ts`. Returns
`nil` when there are no events (block omitted — unchanged). Update `codescene_test.go`.

### Component 3 — contract doc

Rewrite `docs/team-setup/codescene-stats.md`: the new record shape, the new
rollup block, "records every `analyze_change_set` run," and the hook install
snippet. Note the reason (analyze_change_set is categorical, not scored).

### Component 4 — gnome-topbar display

**gnome-topbar** change → its own version; the `/ui/stats` block lives on the
**PR #47** branch, so this **stacks on #47** (branch off `gnome-topbar-stats-detail`,
or rebase after #47 merges).

- `atstats.CodeSceneStats` (`internal/atstats/atstats.go`): mirror the new
  `CodesceneRollup` (drop `LatestScore`/`LatestDelta`/`ScoreP50`; add
  `GatesPassed`/`GatesFailed`/`LatestGate`/`LatestNetPP`/`NetPPP50`/`FilesAnalyzed`;
  keep `Runs`/`LatestTrend`/`Regressions`/`Improvements`/`Neutral`/`CategoryHistogram`).
- `codeSceneLabel` (tray, `internal/tray/menu.go`): drop the score, e.g.
  `📊 CodeScene — <N> runs · gate <latest_gate> · <latest_trend> · <R>r/<I>i`.
- `/ui/stats` CodeScene block (`internal/server/statspage.go`): render
  latest gate/trend/net_pp, net_pp p50, gates passed/failed, runs, reg/imp/neutral,
  files_analyzed, + the category histogram. Keep the empty-state hint.

### Testing

- **Hook**: unit-test the `jq` transform against a captured real `analyze_change_set`
  fixture (the 8-file ② change set is a good one); assert every record field +
  the privacy invariant (no `name`/paths/functions in the output). Test the skip
  paths (no `ANTI_TANGENT_STATS_DIR`, empty `results`, unparseable input → exit 0,
  no write).
- **Server**: `computeCodescene` test over multi-run events for gate/trend counts,
  `net_pp_p50`, latest-by-ts, category aggregation; empty → nil.
- **gnome-topbar**: `atstats` decode of the new block; `/ui/stats` render test for
  the new fields; `codeSceneLabel` string test.
- **End-to-end**: trigger a real `analyze_change_set`, confirm one record appends,
  the Compactor produces the block, and `/ui/stats` renders it.

## Acceptance criteria

- A `PostToolUse` hook on `mcp__codescene__analyze_change_set` appends exactly one
  counts-only record per run to `$ANTI_TANGENT_STATS_DIR/codescene-events.jsonl`,
  with no file paths/code/function names/session id; it never blocks or errors the
  tool (`exit 0`), and silently skips when stats are off or the response is empty.
- `internal/stats/codescene.go` reads the new record and produces the new
  `codescene` rollup block; absence of records still omits the block.
- `docs/team-setup/codescene-stats.md` documents the new record + rollup + install.
- gnome-topbar renders the new block in the tray line and `/ui/stats` (no score).
- All tests pass under `-race`; server CHANGELOG updated.

## Sequencing / branching

1. **Hook + contract doc + server `codescene.go`** — anti-tangent server change on a
   new `version/X.Y.Z` branch (bugfix/minor). Independent of gnome-topbar.
2. **gnome-topbar display** — stacks on PR #47 (needs its `/ui/stats` block); land
   after #47 merges or as a stacked branch.

The record/rollup shapes in this spec are the shared contract between the two.

## Non-goals

- No 1–10 Code Health score; no per-file `cs review` scoring in the hook.
- No change to the mid-task CodeScene tools or their (non-)recording.
- No new anti-tangent MCP tool — this is stats plumbing + a host hook.
