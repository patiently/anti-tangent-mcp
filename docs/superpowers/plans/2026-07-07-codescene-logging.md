# CodeScene logging hook + verdict/pp contract — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Capture each `analyze_change_set` run as a counts-only record and redesign the anti-tangent CodeScene record + rollup around the tool's real categorical output (verdicts / quality-gate / problem-points), so a CodeScene trend can surface.

**Architecture:** A `PostToolUse` hook on `mcp__codescene__analyze_change_set` parses the tool's result and appends one record to `$ANTI_TANGENT_STATS_DIR/codescene-events.jsonl`; anti-tangent's Compactor (`internal/stats/codescene.go`) aggregates it into `rollup.json`'s `codescene` block. This plan is the **server contract + hook + doc** (the independent chunk); the gnome-topbar display update is a documented follow-up (needs PR #47's `/ui/stats` block).

**Tech Stack:** Go 1.25 (`internal/stats`), `bash`+`jq` (hook), `-race` tests.

**User decisions (already made):**
- Record shape: "A — verdict/pp + redesign contract" (drop numeric score; use verdicts/quality-gate/problem-points).
- Scope: "Full vertical" — but this **plan** covers hook + server + doc; gnome-topbar display is a follow-up after #47 merges.
- Records **every** `analyze_change_set` run (only that tool is matched; no dedup).

**Spec:** `docs/superpowers/specs/2026-07-07-codescene-logging-verdict-pp-design.md`

---

## Prerequisite (branch + CHANGELOG)

This is an **anti-tangent server** change → the repo's `version/X.Y.Z` convention applies (CI enforces branch name ↔ a matching `## [X.Y.Z]` CHANGELOG entry). VERSION is `0.10.0`, `version/0.10.0` is taken → target **`version/0.11.0`** (backward-compatible feature). The spec is committed on `codescene-logging` (off `main`); rename/rebase it to `version/0.11.0` before implementing, and Task 1 adds the `## [0.11.0]` CHANGELOG entry. The gnome-topbar display (follow-up) uses its own branch stacked on `gnome-topbar-stats-detail` (#47).

## File Structure

- `internal/stats/codescene.go` — `CodesceneEvent` (read shape), `Verdicts`, `CodesceneRollup`, `computeCodescene`, `readCodescene`/`pruneCodescene` (unchanged). Redesigned to verdict/pp.
- `internal/stats/codescene_test.go` — updated aggregation tests.
- `CHANGELOG.md` — `## [0.11.0]` entry.
- `examples/hooks/codescene-log.sh` (new) — the PostToolUse hook.
- `examples/hooks/codescene-log_test.sh` (new) — hook test over a captured fixture.
- `examples/hooks/testdata/analyze_change_set.stdin.json` (new) — captured hook stdin fixture.
- `docs/team-setup/codescene-stats.md` — rewritten contract + install snippet.

---

### Task 1: Server — redesign the CodeScene record + rollup (verdict/pp)

**Goal:** `internal/stats/codescene.go` reads the new verdict/pp record and aggregates it into the new `codescene` rollup block; the `codescene` key is still omitted when there are no records.

**Files:**
- Modify: `internal/stats/codescene.go`
- Modify: `internal/stats/codescene_test.go`
- Modify: `CHANGELOG.md`

**Acceptance Criteria:**
- [ ] `CodesceneEvent` has `Ts, Tool, QualityGate, FilesAnalyzed, Verdicts{Improved,Degraded,Stable}, Trend, NetPP, CategoryCounts`; no `Score*`/`Delta` fields remain.
- [ ] `CodesceneRollup` has `Runs, GatesPassed, GatesFailed, LatestGate, LatestTrend, LatestNetPP, NetPPP50, Regressions, Improvements, Neutral, FilesAnalyzed, CategoryHistogram, WindowStart, WindowEnd`; no `latest_score`/`latest_delta`/`score_p50`.
- [ ] `computeCodescene` counts gates (`QualityGate=="passed"`→`GatesPassed`, `=="failed"`→`GatesFailed`; any other/empty value counts toward neither) + trends (`Trend=="regression"`→`Regressions`, `=="improvement"`→`Improvements`, **any other value (incl. "neutral"/empty)**→`Neutral`), sums `FilesAnalyzed`, aggregates `CategoryCounts`, takes `Latest*` from the newest `Ts` (**tie-break: on equal timestamps the last record in input/slice order wins** — the `!e.Ts.Before(WindowEnd)` update makes this deterministic), computes `NetPPP50` via `percentile`, returns `nil` for empty input. (Records carry `trend` ∈ {`regression`,`improvement`,`neutral`} and `quality_gate` ∈ {`passed`,`failed`,`unknown`}, produced by Task 2's hook.)
- [ ] `CHANGELOG.md` has a `## [0.11.0] - 2026-07-07` entry describing the redesign.

**Verify:** `go test -race ./internal/stats/...` → PASS

**Steps:**

- [ ] **Step 1: Rewrite the tests** in `internal/stats/codescene_test.go` (replace the score-based `TestComputeCodescene` and `TestCodesceneRollupJSONContract`; keep `TestComputeCodesceneEmptyIsNil` and `TestPruneCodescene` — the latter only sets `Ts`/`Tool`, still valid):

```go
func TestComputeCodescene(t *testing.T) {
	base := time.Unix(1700000000, 0).UTC()
	events := []CodesceneEvent{
		{Ts: base, Tool: "analyze_change_set", QualityGate: "passed", FilesAnalyzed: 3,
			Verdicts: Verdicts{Improved: 2, Stable: 1}, Trend: "improvement", NetPP: -1.5,
			CategoryCounts: map[string]int{"Complex Method": 1}},
		{Ts: base.Add(time.Hour), Tool: "analyze_change_set", QualityGate: "failed", FilesAnalyzed: 5,
			Verdicts: Verdicts{Degraded: 4, Stable: 1}, Trend: "regression", NetPP: 2.3,
			CategoryCounts: map[string]int{"Complex Method": 2, "Bumpy Road Ahead": 1}},
	}
	cr := computeCodescene(events)
	if cr == nil {
		t.Fatal("expected non-nil rollup")
	}
	if cr.Runs != 2 || cr.GatesPassed != 1 || cr.GatesFailed != 1 {
		t.Errorf("runs/gates = %d/%d/%d", cr.Runs, cr.GatesPassed, cr.GatesFailed)
	}
	if cr.LatestGate != "failed" || cr.LatestTrend != "regression" || cr.LatestNetPP != 2.3 {
		t.Errorf("latest = %v/%v/%v", cr.LatestGate, cr.LatestTrend, cr.LatestNetPP)
	}
	if cr.Regressions != 1 || cr.Improvements != 1 || cr.Neutral != 0 {
		t.Errorf("trend counts = %d/%d/%d", cr.Regressions, cr.Improvements, cr.Neutral)
	}
	if cr.FilesAnalyzed != 8 {
		t.Errorf("FilesAnalyzed = %d, want 8", cr.FilesAnalyzed)
	}
	// net_pp values {-1.5, 2.3}; percentile(50) with ceil-rank picks the lower → -1.5.
	if cr.NetPPP50 != -1.5 {
		t.Errorf("NetPPP50 = %v, want -1.5", cr.NetPPP50)
	}
	if cr.CategoryHistogram["Complex Method"] != 3 || cr.CategoryHistogram["Bumpy Road Ahead"] != 1 {
		t.Errorf("category histogram = %v", cr.CategoryHistogram)
	}
	if !cr.WindowStart.Equal(base) || !cr.WindowEnd.Equal(base.Add(time.Hour)) {
		t.Errorf("window = %v..%v", cr.WindowStart, cr.WindowEnd)
	}
}

func TestCodesceneRollupJSONContract(t *testing.T) {
	cr := CodesceneRollup{CategoryHistogram: map[string]int{"Complex Method": 1}}
	b, err := json.Marshal(cr)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)
	for _, key := range []string{
		"runs", "gates_passed", "gates_failed", "latest_gate", "latest_trend",
		"latest_net_pp", "net_pp_p50", "regressions", "improvements", "neutral",
		"files_analyzed", "category_histogram", "window_start", "window_end",
	} {
		if !strings.Contains(s, `"`+key+`"`) {
			t.Errorf("missing json key %q in marshaled CodesceneRollup", key)
		}
	}
}
```

- [ ] **Step 2: Run tests → RED.** `go test -race ./internal/stats/ -run TestComputeCodescene` → fails to compile (new fields undefined).

- [ ] **Step 3: Rewrite `internal/stats/codescene.go`** (keep `codesceneFile`, `readCodescene`, `pruneCodescene` as-is):

```go
package stats

import (
	"math"
	"time"
)

const codesceneFile = "codescene-events.jsonl"

// Verdicts is the per-file verdict tally from an analyze_change_set run.
type Verdicts struct {
	Improved int `json:"improved"`
	Degraded int `json:"degraded"`
	Stable   int `json:"stable"`
}

// CodesceneEvent is the per-run record the hook appends (see
// docs/team-setup/codescene-stats.md). anti-tangent only READS this file; it
// never writes it. Counts + metadata only — no file paths. analyze_change_set is
// categorical (verdicts / quality-gate / problem-points), not a 1-10 score.
type CodesceneEvent struct {
	Ts             time.Time      `json:"ts"`
	Tool           string         `json:"tool"`
	QualityGate    string         `json:"quality_gate"` // passed|failed
	FilesAnalyzed  int            `json:"files_analyzed"`
	Verdicts       Verdicts       `json:"verdicts"`
	Trend          string         `json:"trend"` // improvement|regression|neutral
	NetPP          float64        `json:"net_pp"`
	CategoryCounts map[string]int `json:"category_counts,omitempty"`
}

// CodesceneRollup is the nested `codescene` block in rollup.json.
type CodesceneRollup struct {
	Runs              int            `json:"runs"`
	GatesPassed       int            `json:"gates_passed"`
	GatesFailed       int            `json:"gates_failed"`
	LatestGate        string         `json:"latest_gate"`
	LatestTrend       string         `json:"latest_trend"`
	LatestNetPP       float64        `json:"latest_net_pp"`
	NetPPP50          float64        `json:"net_pp_p50"`
	Regressions       int            `json:"regressions"`
	Improvements      int            `json:"improvements"`
	Neutral           int            `json:"neutral"`
	FilesAnalyzed     int            `json:"files_analyzed"`
	CategoryHistogram map[string]int `json:"category_histogram"`
	WindowStart       time.Time      `json:"window_start"`
	WindowEnd         time.Time      `json:"window_end"`
}

func readCodescene(dir string) ([]CodesceneEvent, error) {
	return readJSONL[CodesceneEvent](dir, codesceneFile)
}

// pruneCodescene rewrites codescene-events.jsonl keeping only records at/after cutoff.
func pruneCodescene(dir string, cutoff time.Time) error {
	events, err := readCodescene(dir)
	if err != nil {
		return err
	}
	kept := events[:0]
	for _, e := range events {
		if !e.Ts.Before(cutoff) {
			kept = append(kept, e)
		}
	}
	return rewriteJSONL(dir, codesceneFile, kept)
}

// computeCodescene aggregates per-run records. Returns nil when there are none,
// so the rollup's `codescene` key is omitted entirely (absence == no data).
func computeCodescene(events []CodesceneEvent) *CodesceneRollup {
	if len(events) == 0 {
		return nil
	}
	cr := &CodesceneRollup{
		CategoryHistogram: map[string]int{},
		WindowStart:       events[0].Ts,
		WindowEnd:         events[0].Ts,
		Runs:              len(events),
	}
	nps := make([]int64, 0, len(events)) // net_pp*100 as int64 for percentile()
	latest := events[0]
	for _, e := range events {
		if e.Ts.Before(cr.WindowStart) {
			cr.WindowStart = e.Ts
		}
		if !e.Ts.Before(cr.WindowEnd) {
			cr.WindowEnd = e.Ts
			latest = e
		}
		switch e.QualityGate {
		case "passed":
			cr.GatesPassed++
		case "failed":
			cr.GatesFailed++
		}
		switch e.Trend {
		case "regression":
			cr.Regressions++
		case "improvement":
			cr.Improvements++
		default:
			cr.Neutral++
		}
		cr.FilesAnalyzed += e.FilesAnalyzed
		for k, v := range e.CategoryCounts {
			cr.CategoryHistogram[k] += v
		}
		nps = append(nps, int64(math.Round(e.NetPP*100)))
	}
	cr.LatestGate = latest.QualityGate
	cr.LatestTrend = latest.Trend
	cr.LatestNetPP = latest.NetPP
	cr.NetPPP50 = float64(percentile(nps, 50)) / 100
	return cr
}
```

- [ ] **Step 4: Run tests → GREEN.** `go test -race ./internal/stats/...` → PASS.

- [ ] **Step 5: Add the CHANGELOG entry.** In `CHANGELOG.md`, above `## [0.10.0]`, add:

```markdown
## [0.11.0] - 2026-07-07

### Changed
- CodeScene stats record + rollup redesigned around `analyze_change_set`'s actual
  categorical output (per-file verdicts, quality-gate, problem-points) instead of a
  numeric Code Health score, which the tool does not return for a change set.
  `CodesceneEvent`/`CodesceneRollup` drop `score_before`/`score_after`/`delta`/
  `latest_score`/`score_p50`; add `quality_gate`/`verdicts`/`net_pp` and rollup
  `gates_passed`/`gates_failed`/`latest_gate`/`latest_net_pp`/`net_pp_p50`.

### Added
- `examples/hooks/codescene-log.sh`: a PostToolUse hook that appends one counts-only
  record per `analyze_change_set` run to `codescene-events.jsonl`. See
  `docs/team-setup/codescene-stats.md`.
```

- [ ] **Step 6: Commit.**

```bash
git add internal/stats/codescene.go internal/stats/codescene_test.go CHANGELOG.md
git commit -m "feat(stats): redesign CodeScene record + rollup around verdicts/quality-gate/problem-points"
```

```json:metadata
{"files": ["internal/stats/codescene.go", "internal/stats/codescene_test.go", "CHANGELOG.md"], "verifyCommand": "go test -race ./internal/stats/...", "acceptanceCriteria": ["CodesceneEvent verdict/pp fields, no Score*", "CodesceneRollup gate/pp fields, no score", "computeCodescene aggregates + nil on empty", "CHANGELOG 0.11.0"], "modelTier": "standard"}
```

---

### Task 2: CodeScene logging hook (script + test)

**Goal:** A fire-and-forget `bash`+`jq` PostToolUse hook that appends one Task-1-shaped record per `analyze_change_set` run to `$ANTI_TANGENT_STATS_DIR/codescene-events.jsonl`, with no paths/code/functions.

**Files:**
- Create: `examples/hooks/codescene-log.sh`
- Create: `examples/hooks/testdata/analyze_change_set.stdin.json`
- Create: `examples/hooks/codescene-log_test.sh`

**Acceptance Criteria:**
- [ ] Given a real hook stdin (a captured `analyze_change_set` PostToolUse payload), the script appends one JSON line with ALL `CodesceneEvent` JSON keys: `ts` (RFC3339 UTC, `date -u +%Y-%m-%dT%H:%M:%SZ`), `tool` (the literal `"analyze_change_set"`), `quality_gate` (`.quality_gates` verbatim, or `"unknown"` if absent), `files_analyzed` (= `.results | length`), `verdicts.{improved,degraded,stable}` (count of `results[]` per `.verdict`), `trend`, `net_pp`, `category_counts` (count of `results[].findings[].category`).
- [ ] The record contains NO file paths, function names, or `name`/`locations` keys (privacy).
- [ ] Skips silently (`exit 0`, no write) when ANY of: `ANTI_TANGENT_STATS_DIR` is unset; `jq` is absent; the stdin is not valid JSON; `tool_response` is missing; the extracted result is not valid JSON or has no `results` array; or `results` is empty.
- [ ] `trend` = sign of `net_pp` (`>0`→regression, `<0`→improvement, else neutral); `net_pp` = `Σ(finding.new-pp − finding.old-pp)` over all findings (missing side → 0).

**Verify:** `examples/hooks/codescene-log_test.sh` → prints `OK`, exit 0.

**Steps:**

- [ ] **Step 0 (blocking): pin the `tool_response` shape empirically.** Add a throwaway debug hook on the same matcher (`command: "cat >> /tmp/cs-hook-stdin.json"`), trigger one real `analyze_change_set`, inspect `/tmp/cs-hook-stdin.json` to confirm the result is at `.tool_response` (a JSON string to re-parse) or `.tool_response.content[0].text`. Save that payload as `examples/hooks/testdata/analyze_change_set.stdin.json`, remove the debug hook. The script below handles both shapes, but the fixture must be a real payload.

- [ ] **Step 1: Create `examples/hooks/codescene-log.sh`:**

```bash
#!/usr/bin/env bash
# codescene-log.sh — Claude Code PostToolUse hook for mcp__codescene__analyze_change_set.
# Appends one counts-only record per run to $ANTI_TANGENT_STATS_DIR/codescene-events.jsonl.
# Fire-and-forget: always exit 0; silent skip when stats are off or the payload is unusable.
# Record shape matches internal/stats CodesceneEvent (verdicts / quality-gate / problem-points).
set -uo pipefail

dir="${ANTI_TANGENT_STATS_DIR:-}"
[ -n "$dir" ] || exit 0
command -v jq >/dev/null 2>&1 || exit 0

input=$(cat)

# tool_response is usually a JSON string; some MCP shapes wrap it as
# {content:[{text:...}]}. Extract the analyze_change_set JSON either way.
resp=$(printf '%s' "$input" | jq -r '
  .tool_response
  | if type=="string" then .
    elif type=="object" and ((.content?|type)=="array") then (.content[0].text // "")
    else tojson end' 2>/dev/null)
[ -n "$resp" ] || exit 0

record=$(printf '%s' "$resp" | jq -c --arg ts "$(date -u +%Y-%m-%dT%H:%M:%SZ)" '
  select((.results?|type)=="array") |
  ([.results[].findings[]? | ((.["new-pp"] // 0) - (.["old-pp"] // 0))] | add // 0) as $np |
  {
    ts: $ts,
    tool: "analyze_change_set",
    quality_gate: (.quality_gates // "unknown"),
    files_analyzed: (.results | length),
    verdicts: {
      improved: ([.results[] | select(.verdict=="improved")] | length),
      degraded: ([.results[] | select(.verdict=="degraded")] | length),
      stable:   ([.results[] | select(.verdict=="stable")]   | length)
    },
    trend: (if $np > 0 then "regression" elif $np < 0 then "improvement" else "neutral" end),
    net_pp: $np,
    category_counts: ([.results[].findings[]?.category] | group_by(.) | map({key: .[0], value: length}) | from_entries)
  }' 2>/dev/null)
[ -n "$record" ] || exit 0

mkdir -p "$dir"
printf '%s\n' "$record" >> "$dir/codescene-events.jsonl"
exit 0
```

Make it executable: `chmod +x examples/hooks/codescene-log.sh`.

- [ ] **Step 2: Create the test `examples/hooks/codescene-log_test.sh`:**

```bash
#!/usr/bin/env bash
# Drives codescene-log.sh with the captured fixture and asserts the record.
set -uo pipefail
here=$(cd "$(dirname "$0")" && pwd)
out=$(mktemp -d)
export ANTI_TANGENT_STATS_DIR="$out"

# Happy path: real captured PostToolUse stdin → one record with the right fields.
"$here/codescene-log.sh" < "$here/testdata/analyze_change_set.stdin.json"
line=$(cat "$out/codescene-events.jsonl")
[ "$(printf '%s' "$line" | jq -r '.tool')" = "analyze_change_set" ] || { echo "FAIL tool"; exit 1; }
printf '%s' "$line" | jq -e '.quality_gate and (.files_analyzed|type=="number") and (.verdicts.degraded|type=="number") and (.trend) and (.net_pp|type=="number") and (.category_counts|type=="object")' >/dev/null || { echo "FAIL shape"; exit 1; }
# Privacy: no file paths / function names / locations leaked.
printf '%s' "$line" | grep -qiE '"name"|"locations"|/|\.go|function' && { echo "FAIL privacy: path/fn leaked"; exit 1; }

# Skip path: unset dir → no write, exit 0.
rm -f "$out/codescene-events.jsonl"
( unset ANTI_TANGENT_STATS_DIR; "$here/codescene-log.sh" < "$here/testdata/analyze_change_set.stdin.json" )
[ -f "$out/codescene-events.jsonl" ] && { echo "FAIL: wrote despite unset dir"; exit 1; }

# Skip path: empty results → no write.
echo '{"tool_response":"{\"results\":[],\"quality_gates\":\"passed\"}"}' | "$here/codescene-log.sh"
[ -f "$out/codescene-events.jsonl" ] && { echo "FAIL: wrote on empty results"; exit 1; }

echo OK
```

  Make it executable. Note: if Step 0 shows `tool_response` is the content-wrapper shape, the empty-results skip-test literal must match that shape too; adjust that one line accordingly.

- [ ] **Step 3: Run → GREEN.** `chmod +x examples/hooks/*.sh && examples/hooks/codescene-log_test.sh` → `OK`.

- [ ] **Step 4: Commit.**

```bash
git add examples/hooks/codescene-log.sh examples/hooks/codescene-log_test.sh examples/hooks/testdata/analyze_change_set.stdin.json
git commit -m "feat(hooks): PostToolUse hook logging analyze_change_set to codescene-events.jsonl"
```

```json:metadata
{"files": ["examples/hooks/codescene-log.sh", "examples/hooks/codescene-log_test.sh", "examples/hooks/testdata/analyze_change_set.stdin.json"], "verifyCommand": "examples/hooks/codescene-log_test.sh", "acceptanceCriteria": ["record fields match CodesceneEvent", "no path/fn leak", "silent skip on unset dir / empty results", "trend = sign(net_pp)"], "modelTier": "standard"}
```

---

### Task 3: Contract doc + wire the hook into settings

**Goal:** `docs/team-setup/codescene-stats.md` documents the new record/rollup and install; the hook is registered in `~/.claude/settings.json` so it fires live.

**Files:**
- Modify: `docs/team-setup/codescene-stats.md`
- Modify: `~/.claude/settings.json` (host config — additive PostToolUse entry)

**Acceptance Criteria:**
- [ ] `codescene-stats.md` shows the new counts-only record, the new rollup block, "records every analyze_change_set run," and the settings.json install snippet — no `score_before`/`score_after` references remain (`grep -cE 'score_before|score_after' docs/team-setup/codescene-stats.md` → `0`).
- [ ] `~/.claude/settings.json` has a `PostToolUse` entry with `matcher: "mcp__codescene__analyze_change_set"`, `hooks[0].command` = the **resolved absolute** path (`git rev-parse --show-toplevel` → `/home/pgilmore/Development/Patiently/anti-tangent-mcp/examples/hooks/codescene-log.sh`, no placeholder), added alongside the existing `matcher:"*"` entry (both preserved; file remains valid JSON).

**Verify:** `jq -e '.hooks.PostToolUse[] | select(.matcher=="mcp__codescene__analyze_change_set")' ~/.claude/settings.json` → prints the entry; `grep -cE 'score_before|score_after' docs/team-setup/codescene-stats.md` → `0`.

**Steps:**

- [ ] **Step 1: Rewrite `docs/team-setup/codescene-stats.md`** — replace the score-based "Record shape" and "How it surfaces" sections with the verdict/pp record (from Task 1's `CodesceneEvent`) and the new `CodesceneRollup` block; add a "Enable it" section with the settings.json snippet and a one-line rationale (analyze_change_set is categorical, not scored). State "one record per `analyze_change_set` run (mid-task `pre_commit_code_health_safeguard`/`code_health_review` are not matched)".

- [ ] **Step 2: Register the hook** in `~/.claude/settings.json` — add to the `hooks.PostToolUse` array (do NOT remove the existing `matcher:"*"` superset entry):

```json
{
  "matcher": "mcp__codescene__analyze_change_set",
  "hooks": [
    { "type": "command", "command": "/home/pgilmore/Development/Patiently/anti-tangent-mcp/examples/hooks/codescene-log.sh", "timeout": 10 }
  ]
}
```

  Apply with a JSON-safe merge (e.g. `jq` in place with a temp file), then validate `jq . ~/.claude/settings.json`.

- [ ] **Step 3: Verify.** Run the Verify commands above.

- [ ] **Step 4: Commit** (repo doc only; settings.json is host config, not committed):

```bash
git add docs/team-setup/codescene-stats.md
git commit -m "docs: codescene-stats record/rollup redesign + hook install"
```

```json:metadata
{"files": ["docs/team-setup/codescene-stats.md"], "verifyCommand": "grep -c score_before docs/team-setup/codescene-stats.md", "acceptanceCriteria": ["doc shows verdict/pp record + rollup + install", "no score_before refs", "settings.json has the scoped PostToolUse entry alongside the existing one"], "modelTier": "standard"}
```

---

## End-to-end verification (after Task 3)

Trigger a real `analyze_change_set` (e.g. against `main`), then:
- `tail -1 $ANTI_TANGENT_STATS_DIR/codescene-events.jsonl | jq .` → a well-formed record landed (the hook fired live).
- The Compactor folds it into `rollup.json`'s `codescene` block on its next compaction (covered deterministically by Task 1's `computeCodescene` test).

## Follow-up (NOT in this plan — needs PR #47)

gnome-topbar display: update `atstats.CodeSceneStats` + `codeSceneLabel` (tray) + the `/ui/stats` CodeScene block to the new rollup fields (drop score, show gate/trend/net_pp/reg-imp-neutral/categories). Do this on a branch stacked on `gnome-topbar-stats-detail` (which owns the `/ui/stats` block), or after #47 merges to `main`.
