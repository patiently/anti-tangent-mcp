# Fable usage + gnome-topbar richer stats detail — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Surface per-account Claude Fable weekly usage end-to-end (producer → tray → web) and add rich `/ui/stats` + `/ui/claude` web detail pages, keeping the tray dropdown lean.

**Architecture:** ① The `claude-sandbox` producer (`bin/claude-usage-stats`) extracts per-model weekly windows from `/api/oauth/usage`'s new `limits[]` array into a `weekly_models` map (schema 1.2) and back-fills the now-null `seven_day_opus`/`seven_day_sonnet`. ② The gnome-topbar daemon decodes `weekly_models`, adds a per-model row to the Claude-usage submenu, decodes the full anti-tangent rollup, and serves two new browser detail pages opened from tray "Details…" items. ③ CodeScene logging is out of scope (parked on an expired token); ②'s `/ui/stats` shows a graceful empty-state.

**Tech Stack:** Bash + jq (producer); Go 1.25, `fyne.io/systray`, stdlib `net/http` (daemon); Go golden/table tests under `-race`.

**User decisions (already made):**
- Fable schema shape: "B — weekly_models map" keyed by model display_name.
- Legacy opus/sonnet fields: "Back-fill then keep" (top-level wins, else back-fill from map).
- Detail surface: "Both" — lean tray inline + web detail pages.
- Web layout: "Two pages" (`/ui/stats` + `/ui/claude`), two tray "Details…" items.
- Tray richness: "Lean" — per-model rows in the Claude submenu only; anti-tangent inline unchanged; all other new detail on the web pages; compact overview + top-bar icon untouched.
- AT detail depth: "Curated headline" — rollup aggregates on the web, no raw `events.jsonl` table.
- Order: "① then ② back-to-back."
- ③ CodeScene: parked behind the expired `CS_ACCESS_TOKEN`.

**Specs:** `docs/superpowers/specs/2026-07-06-gnome-topbar-stats-detail-fable-design.md` (②, this repo) and `~/Development/YC/claude-sandbox/docs/superpowers/specs/2026-07-06-claude-stats-per-model-weekly-windows-design.md` (①).

---

## Prerequisite (execution-time, needs your input)

Task 1 lands in the **`claude-sandbox`** repo, which is currently on branch
`sandbox-auto-review-poller` with **uncommitted changes** to unrelated files
(`Dockerfile.sandbox`, `bin/sandbox-auto-review`, `docker-compose.yml`,
`sandbox-entrypoint.sh`, `tests/sandbox-auto-review/run-tests.sh`) plus an
untracked `sandbox-config/` and the untracked ① spec. Before Task 1, get that
repo to a clean-enough state for a dedicated ① branch off `master`. **Options
(your call — I will not move your WIP without direction):** (a) commit your
`sandbox-auto-review-poller` WIP, then branch `claude-stats-weekly-models` off it
or off `master`; (b) `git stash` the WIP, branch off `master`, implement ①, then
restore; (c) implement ① on the current branch and commit only ①'s files. The ①
spec (untracked) should be committed on whichever branch hosts Task 1.

**DISPATCH GATE (blocking):** Task 1 MUST NOT go `in_progress` until this exact
line is recorded here, from ONE of two sources — an explicit user pick, or an
explicit user deferral that authorizes the default:

`① branch: <name> (base <ref>), WIP via option (<a|b|c>) — source: <user-pick|user-deferred-default-applied>`

A bare absence of any answer does NOT satisfy the gate; a recorded
`user-deferred-default-applied` DOES (the default is option (a): branch
`claude-stats-weekly-models` off `master`, WIP committed on `sandbox-auto-review-poller`).
Either way the line must be present before Task 1 starts. Tasks 2–7 (this repo,
branch `gnome-topbar-stats-detail`) are NOT blocked by this gate and may proceed
independently — they degrade gracefully without ①.

---

## File Structure

**① `claude-sandbox` (Task 1)**
- `bin/claude-usage-stats` — `fetch_limits` jq mapping + `SCHEMA_VERSION`.
- `docs/claude-stats/claude-stats.schema.json` — `weekly_models` `$defs`, version bump.
- `docs/claude-stats/README.md`, `docs/claude-stats/claude-stats.example.json` — doc/example.
- `tests/claude-usage-stats/fake-usage.json`, `run-tests.sh` — fixture + assertions.

**② `anti-tangent-mcp` `gnome-topbar/daemon` (Tasks 2–7)**
- `internal/claudestats/claudestats.go` (+ `testdata/claude-stats.json`, `claudestats_test.go`) — decode `weekly_models`.
- `internal/atstats/atstats.go` (+ `atstats_test.go`) — decode full rollup.
- `internal/tray/claude.go` (+ `claude_test.go`) — per-model submenu rows.
- `internal/server/statspage.go` (new) + `statspage_test.go` (new); `internal/server/ui.go` — web pages.
- `internal/tray/tray.go`, `cmd/gnome-topbar-daemon/main.go` — "Details…" items + actions.
- `gnome-topbar/README.md` — Changelog v0.3.0.

---

### Task 1: [claude-sandbox] Producer emits `weekly_models` + back-fills legacy fields

**Goal:** `claude-stats.json`'s `limits` carries a `weekly_models` map (Fable et al.) from the `/api/oauth/usage` `limits[]` array, with `seven_day_opus`/`seven_day_sonnet` back-filled; schema is `1.2`.

**Repo:** `claude-sandbox` (see Prerequisite for branch handling).

**Files:**
- Modify: `bin/claude-usage-stats` (`SCHEMA_VERSION`; `fetch_limits` success + both error branches)
- Modify: `docs/claude-stats/claude-stats.schema.json`
- Modify: `docs/claude-stats/README.md`, `docs/claude-stats/claude-stats.example.json`
- Modify: `tests/claude-usage-stats/fake-usage.json`, `tests/claude-usage-stats/run-tests.sh`

**Acceptance Criteria:**
- [ ] `limits.weekly_models` is an object keyed by model `display_name` → `{utilization, resets_at}`, built from every `limits[]` entry with `kind=="weekly_scoped"` and non-null `scope.model.display_name`.
- [ ] `weekly_models` is `{}` when there are no scoped windows and in both error branches.
- [ ] `seven_day_opus`/`seven_day_sonnet` = top-level value when non-null, else back-filled from the `weekly_models` entry whose display_name **lowercased contains** `"opus"`/`"sonnet"`; on multiple matches the **lexicographically-first key** wins (deterministic via `sort_by(.key)`); no match → `null`. (Observed display_names are exact single words like `"Fable"`/`"Opus"`, so substring+sorted is unambiguous today and robust to fuller names later.)
- [ ] `SCHEMA_VERSION` is `1.2`; schema/README/example document `weekly_models`.
- [ ] `tests/claude-usage-stats/run-tests.sh` passes with the new assertions.

**Verify:** `cd ~/Development/YC/claude-sandbox && ./tests/claude-usage-stats/run-tests.sh` → all cases PASS.

**Steps:**

- [ ] **Step 1: Extend the fixture** `tests/claude-usage-stats/fake-usage.json` — add a `limits` array (keep the existing top-level windows). New file content:

```json
{"five_hour":{"utilization":4.0,"resets_at":"2026-06-03T11:40:00.516530+00:00"},"seven_day":{"utilization":26.0,"resets_at":"2026-06-08T20:00:00.516553+00:00"},"seven_day_opus":null,"seven_day_sonnet":{"utilization":7.0,"resets_at":"2026-06-08T20:00:00.516561+00:00"},"extra_usage":{"is_enabled":true,"monthly_limit":20000,"used_credits":0.0,"utilization":null,"currency":"EUR"},"limits":[{"kind":"session","group":"session","percent":4,"severity":"normal","resets_at":"2026-06-03T11:40:00.516530+00:00","scope":null,"is_active":false},{"kind":"weekly_all","group":"weekly","percent":26,"severity":"normal","resets_at":"2026-06-08T20:00:00.516553+00:00","scope":null,"is_active":true},{"kind":"weekly_scoped","group":"weekly","percent":69,"severity":"normal","resets_at":"2026-06-08T20:00:00.516553+00:00","scope":{"model":{"id":null,"display_name":"Fable"},"surface":null},"is_active":false},{"kind":"weekly_scoped","group":"weekly","percent":12,"severity":"normal","resets_at":"2026-06-08T20:00:00.516553+00:00","scope":{"model":{"id":null,"display_name":"Opus"},"surface":null},"is_active":false}]}
```

- [ ] **Step 2: Add failing assertions** to `tests/claude-usage-stats/run-tests.sh`. Find the existing default-account limits assertion (`.five_hour.utilization==4 and .seven_day.utilization==26`) and extend that same `jq -e` expression with:

```
 and .accounts.default.limits.weekly_models.Fable.utilization==69 and .accounts.default.limits.seven_day_opus.utilization==12 and .accounts.default.limits.seven_day_sonnet.utilization==7
```

- [ ] **Step 3: Run tests → RED.** `./tests/claude-usage-stats/run-tests.sh` → fails (weekly_models absent, seven_day_opus null).

- [ ] **Step 4: Implement the mapping.** In `bin/claude-usage-stats`, replace the success-branch jq in `fetch_limits` with:

```jq
    jq -c --arg f "$fetched" '
        def win: if . == null then null else {utilization: .utilization, resets_at: .resets_at} end;
        ( [ (.limits // [])[]
            | select(.kind == "weekly_scoped" and (.scope.model.display_name // null) != null)
            | {key: .scope.model.display_name, value: {utilization: .percent, resets_at: .resets_at}} ]
          | from_entries ) as $wm
        | ( $wm | to_entries ) as $wme
        | def wmmatch($sub): ($wme | sort_by(.key) | map(select(.key | ascii_downcase | contains($sub))) | (.[0].value // null));
          {
            fetched_at: $f, error: null,
            five_hour: (.five_hour | win),
            seven_day: (.seven_day | win),
            seven_day_opus: ((.seven_day_opus | win) // wmmatch("opus")),
            seven_day_sonnet: ((.seven_day_sonnet | win) // wmmatch("sonnet")),
            weekly_models: $wm,
            extra_usage: (.extra_usage // null)
        }' <<<"$body"
```

  Add `weekly_models:{}` to BOTH error branches (the no-token and non-200 `jq -cn` objects), e.g.:

```
jq -cn --arg f "$fetched" '{fetched_at:$f, error:"no oauth token", five_hour:null, seven_day:null, seven_day_opus:null, seven_day_sonnet:null, weekly_models:{}, extra_usage:null}'
```

  Bump the top-of-file constant: `SCHEMA_VERSION="1.2"`.

- [ ] **Step 5: Run tests → GREEN.** `./tests/claude-usage-stats/run-tests.sh` → all PASS (Fable 69, opus back-filled 12, sonnet top-level 7, five_hour 4, seven_day 26).

- [ ] **Step 6: Update the contract.** In `docs/claude-stats/claude-stats.schema.json`: add under the `limits` `$defs.properties`:

```json
"weekly_models": {
  "type": "object",
  "description": "Per-model weekly sub-limits keyed by model display_name (from /api/oauth/usage limits[] weekly_scoped entries). Empty object when none or on fetch error.",
  "additionalProperties": { "$ref": "#/$defs/window" }
}
```

  Note in the `seven_day_opus`/`seven_day_sonnet` descriptions that they are back-filled from `weekly_models` when the top-level field is null. Bump the `schema_version` const/example in the schema to `1.2`. Add `weekly_models` to `docs/claude-stats/claude-stats.example.json` and document it in `docs/claude-stats/README.md`.

- [ ] **Step 7: Commit** (and commit the untracked ① spec on this branch):

```bash
git add bin/claude-usage-stats docs/claude-stats/ tests/claude-usage-stats/ docs/superpowers/specs/2026-07-06-claude-stats-per-model-weekly-windows-design.md
git commit -m "feat(claude-stats): emit per-model weekly windows (fable) + back-fill opus/sonnet (schema 1.2)"
```

```json:metadata
{"repo": "claude-sandbox", "files": ["bin/claude-usage-stats", "docs/claude-stats/claude-stats.schema.json", "docs/claude-stats/README.md", "docs/claude-stats/claude-stats.example.json", "tests/claude-usage-stats/fake-usage.json", "tests/claude-usage-stats/run-tests.sh"], "verifyCommand": "./tests/claude-usage-stats/run-tests.sh", "acceptanceCriteria": ["weekly_models map from limits[] weekly_scoped", "weekly_models {} when none/error", "opus/sonnet back-filled else null", "SCHEMA_VERSION 1.2 + docs", "run-tests.sh passes"], "modelTier": "standard"}
```

---

### Task 2: [gnome-topbar] Decode `weekly_models` in `claudestats`

**Goal:** `claudestats.Limits` exposes the per-model weekly windows so tray + web can render Fable.

**Repo:** `anti-tangent-mcp` (`gnome-topbar/daemon`).

**Files:**
- Modify: `internal/claudestats/claudestats.go` (add field to `Limits`)
- Modify: `internal/claudestats/testdata/claude-stats.json` (add `weekly_models`)
- Modify: `internal/claudestats/claudestats_test.go` (add test)

**Acceptance Criteria:**
- [ ] `Limits.WeeklyModels` (`map[string]*Window`, json `weekly_models`) decodes present entries.
- [ ] Files without `weekly_models` still decode (`Present:true`, nil/empty map) — unknown-field tolerance preserved.

**Verify:** `cd gnome-topbar/daemon && go test -race ./internal/claudestats/...` → PASS.

**Steps:**

- [ ] **Step 1: Add the field** to the `Limits` struct in `internal/claudestats/claudestats.go`, after `SevenDaySonnet`:

```go
	// WeeklyModels holds per-model weekly sub-limits keyed by model display_name
	// (schema 1.2+, from the producer's /api/oauth/usage limits[] weekly_scoped
	// entries). Nil/empty when the producer emits none.
	WeeklyModels map[string]*Window `json:"weekly_models"`
```

- [ ] **Step 2: Add the failing test** to `internal/claudestats/claudestats_test.go`:

```go
func TestReadWeeklyModels(t *testing.T) {
	s, _ := Read("testdata")
	w := s.Accounts["default"].Limits.WeeklyModels["Fable"]
	if w == nil || w.Utilization == nil || *w.Utilization != 69.0 {
		t.Fatalf("Fable weekly window = %+v, want utilization 69", w)
	}
}
```

- [ ] **Step 3: Run → RED.** `go test -race ./internal/claudestats/ -run TestReadWeeklyModels` → fails (no `weekly_models` in testdata).

- [ ] **Step 4: Extend the testdata.** In `internal/claudestats/testdata/claude-stats.json`, add to `accounts.default.limits` (after `seven_day_sonnet`):

```json
        "weekly_models": { "Fable": { "utilization": 69.0, "resets_at": "2026-06-08T20:00:00.516553+00:00" } },
```

- [ ] **Step 5: Run → GREEN.** `go test -race ./internal/claudestats/...` → PASS.

- [ ] **Step 6: Commit.**

```bash
git add internal/claudestats/
git commit -m "feat(gnome-topbar): decode claude-stats weekly_models (per-model weekly limits)"
```

```json:metadata
{"repo": "anti-tangent-mcp", "files": ["gnome-topbar/daemon/internal/claudestats/claudestats.go", "gnome-topbar/daemon/internal/claudestats/testdata/claude-stats.json", "gnome-topbar/daemon/internal/claudestats/claudestats_test.go"], "verifyCommand": "cd gnome-topbar/daemon && go test -race ./internal/claudestats/...", "acceptanceCriteria": ["WeeklyModels decodes", "absent weekly_models still Present"], "modelTier": "mechanical"}
```

---

### Task 3: [gnome-topbar] Decode the full anti-tangent rollup in `atstats`

**Goal:** `atstats.Stats` carries the rollup fields the web page needs (per-tool, severity, full categories, p50, cache/partial, model usage, window span), keeping existing tray fields.

**Repo:** `anti-tangent-mcp` (`gnome-topbar/daemon`).

**Files:**
- Modify: `internal/atstats/atstats.go` (extend `rollup` + `Stats` + `Read`)
- Modify: `internal/atstats/atstats_test.go` (extend fixture + asserts)

**Acceptance Criteria:**
- [ ] `Stats` exposes `PerTool`, `VerdictCounts`, `FindingsPerCall`, `SeverityHistogram`, full `CategoryHistogram`, `ReviewMSP50`, `CacheHitRate`, `PartialRate`, `ModelUsage`, `WindowStart`, `WindowEnd`.
- [ ] Existing fields (`TotalCalls`, `PassPct`/`WarnPct`/`FailPct`, `TopCategory`, `ReviewMSP95`, `Summary`, `CodeScene`) unchanged; absent file still `Present:false`.

**Verify:** `cd gnome-topbar/daemon && go test -race ./internal/atstats/...` → PASS.

**Steps:**

- [ ] **Step 1: Extend the `rollup` decode struct** in `internal/atstats/atstats.go`:

```go
type rollup struct {
	TotalCalls        int             `json:"total_calls"`
	PerTool           map[string]int  `json:"per_tool"`
	VerdictCounts     map[string]int  `json:"verdict_counts"`
	FindingsPerCall   float64         `json:"findings_per_call"`
	SeverityHistogram map[string]int  `json:"severity_histogram"`
	CategoryHistogram map[string]int  `json:"category_histogram"`
	ReviewMSP50       int64           `json:"review_ms_p50"`
	ReviewMSP95       int64           `json:"review_ms_p95"`
	CacheHitRate      float64         `json:"cache_hit_rate"`
	PartialRate       float64         `json:"partial_rate"`
	ModelUsage        map[string]int  `json:"model_usage"`
	WindowStart       time.Time       `json:"window_start"`
	WindowEnd         time.Time       `json:"window_end"`
	GeneratedAt       time.Time       `json:"generated_at"`
	CodeScene         *CodeSceneStats `json:"codescene"`
}
```

- [ ] **Step 2: Add the same fields to `Stats`** (after `Summary`, before `CodeScene`):

```go
	PerTool           map[string]int `json:"per_tool,omitempty"`
	VerdictCounts     map[string]int `json:"verdict_counts,omitempty"`
	FindingsPerCall   float64        `json:"findings_per_call"`
	SeverityHistogram map[string]int `json:"severity_histogram,omitempty"`
	CategoryHistogram map[string]int `json:"category_histogram,omitempty"`
	ReviewMSP50       int64          `json:"review_ms_p50"`
	CacheHitRate      float64        `json:"cache_hit_rate"`
	PartialRate       float64        `json:"partial_rate"`
	ModelUsage        map[string]int `json:"model_usage,omitempty"`
	WindowStart       time.Time      `json:"window_start"`
	WindowEnd         time.Time      `json:"window_end"`
```

- [ ] **Step 3: Assign in `Read`** — after the existing `s := Stats{...}` construction, set the new fields from `r`:

```go
	s.PerTool = r.PerTool
	s.VerdictCounts = r.VerdictCounts
	s.FindingsPerCall = r.FindingsPerCall
	s.SeverityHistogram = r.SeverityHistogram
	s.CategoryHistogram = r.CategoryHistogram
	s.ReviewMSP50 = r.ReviewMSP50
	s.CacheHitRate = r.CacheHitRate
	s.PartialRate = r.PartialRate
	s.ModelUsage = r.ModelUsage
	s.WindowStart = r.WindowStart
	s.WindowEnd = r.WindowEnd
```

  (`TopCategory` still derives from `topKey(r.CategoryHistogram)`; leave that line.)

- [ ] **Step 4: Extend the test.** In `internal/atstats/atstats_test.go`, add a rollup with the new fields and assert one of each shape. Add to the fixture JSON `"per_tool":{"validate_completion":6},"findings_per_call":3.2,"severity_histogram":{"major":4},"review_ms_p50":900,"cache_hit_rate":0,"partial_rate":0,"model_usage":{"openai:gpt-5.5":10}` and assert:

```go
	if s.PerTool["validate_completion"] != 6 || s.ReviewMSP50 != 900 || s.ModelUsage["openai:gpt-5.5"] != 10 {
		t.Errorf("full rollup decode: got PerTool=%v p50=%d model=%v", s.PerTool, s.ReviewMSP50, s.ModelUsage)
	}
```

- [ ] **Step 5: Run → GREEN.** `go test -race ./internal/atstats/...` → PASS.

- [ ] **Step 6: Commit.**

```bash
git add internal/atstats/
git commit -m "feat(gnome-topbar): decode full anti-tangent rollup (per-tool, severity, p50, model usage)"
```

```json:metadata
{"repo": "anti-tangent-mcp", "files": ["gnome-topbar/daemon/internal/atstats/atstats.go", "gnome-topbar/daemon/internal/atstats/atstats_test.go"], "verifyCommand": "cd gnome-topbar/daemon && go test -race ./internal/atstats/...", "acceptanceCriteria": ["new rollup fields decode", "existing fields unchanged", "absent file Present false"], "modelTier": "mechanical"}
```

---

### Task 4: [gnome-topbar] Per-model rows in the Claude usage submenu

**Goal:** The Claude usage detail submenu shows one row per `weekly_models` entry (Fable visible); overview + icon unchanged.

**Repo:** `anti-tangent-mcp` (`gnome-topbar/daemon`). **Depends on:** Task 2.

**Files:**
- Modify: `internal/tray/claude.go` (`claudeUsageRows`)
- Modify: `internal/tray/claude_test.go`

**Acceptance Criteria:**
- [ ] For each `a.Limits.WeeklyModels` entry with data, `claudeUsageRows` emits `· <Model> <util>% · resets …` (sorted by name), after the `5h`/`7d` rows.
- [ ] `claudeOverviewLabels` output is unchanged (no per-model in the compact overview).

**Verify:** `cd gnome-topbar/daemon && go test -race ./internal/tray/...` → PASS.

**Steps:**

- [ ] **Step 1: Add the failing test** in `internal/tray/claude_test.go`:

```go
func TestClaudeUsageRowsPerModel(t *testing.T) {
	util := 69.0
	reset := time.Date(2026, 6, 8, 20, 0, 0, 0, time.UTC)
	cs := claudestats.Stats{Present: true, Accounts: map[string]claudestats.Account{
		"default": {Limits: &claudestats.Limits{
			WeeklyModels: map[string]*claudestats.Window{"Fable": {Utilization: &util, ResetsAt: &reset}},
		}},
	}}
	rows := claudeUsageRows(cs, time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC))
	found := false
	for _, r := range rows {
		if strings.Contains(r, "Fable") && strings.Contains(r, "69%") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no Fable per-model row in %#v", rows)
	}
}
```

- [ ] **Step 2: Run → RED.** `go test -race ./internal/tray/ -run TestClaudeUsageRowsPerModel` → fails.

- [ ] **Step 3: Implement** in `internal/tray/claude.go` — inside `claudeUsageRows`, in the `a.Limits != nil` `else` branch (limits present, no error), after the `SevenDay` block:

```go
			// Per-model weekly windows (schema 1.2+): Fable, Opus, …
			mnames := make([]string, 0, len(a.Limits.WeeklyModels))
			for name := range a.Limits.WeeklyModels {
				mnames = append(mnames, name)
			}
			sort.Strings(mnames)
			for _, name := range mnames {
				if w := a.Limits.WeeklyModels[name]; w.HasData() {
					rows = append(rows, "· "+windowDetail(name, w, now))
				}
			}
```

  (`sort` is already imported in `claude.go`; `windowDetail` already renders `<name>  <util>% · resets …`.)

- [ ] **Step 4: Run → GREEN.** `go test -race ./internal/tray/...` → PASS (incl. existing overview/icon tests unchanged).

- [ ] **Step 5: Commit.**

```bash
git add internal/tray/claude.go internal/tray/claude_test.go
git commit -m "feat(gnome-topbar): show per-model weekly rows (Fable) in Claude usage submenu"
```

```json:metadata
{"repo": "anti-tangent-mcp", "files": ["gnome-topbar/daemon/internal/tray/claude.go", "gnome-topbar/daemon/internal/tray/claude_test.go"], "verifyCommand": "cd gnome-topbar/daemon && go test -race ./internal/tray/...", "acceptanceCriteria": ["per-model rows after 5h/7d", "overview unchanged"], "modelTier": "standard"}
```

---

### Task 5: [gnome-topbar] `/ui/stats` + `/ui/claude` web detail pages

**Goal:** Two authed browser pages render the curated anti-tangent rollup (+ CodeScene block/empty-state) and per-account Claude usage incl. per-model Fable.

**Repo:** `anti-tangent-mcp` (`gnome-topbar/daemon`). **Depends on:** Tasks 2, 3.

**Files:**
- Create: `internal/server/statspage.go`, `internal/server/statspage_test.go`
- Modify: `internal/server/ui.go` (register handlers + landing cards)

**Acceptance Criteria:**
- [ ] `/ui/stats` (authed) renders verdict mix, per-tool, findings/call, severity, categories, p50/p95, cache/partial, model usage, window span, summary — and a CodeScene block when present, else a "no data yet" hint. No raw events table.
- [ ] `/ui/claude` (authed) renders per-account usage (today/week/month), 5h/weekly/per-model (Fable) limits, and the error/stale states per the matrix below.
- [ ] `/ui/search` landing page links to both.
- [ ] Absent stats render a graceful "no data" page, not a 500.

**`/ui/claude` state matrix (observable output; states are independent, not mutually exclusive):**

| Input | Observable output | Placement |
|---|---|---|
| `cs.Stale(now)` (global) | `⚠ stats stale (generated <Jan 2 15:04>)` | one banner under the `<h1>`, before any account |
| `a.Today/Week/Month` present | `Usage` table row(s) `$<cost> · <tok> tok` | per account; renders regardless of any error |
| `a.Limits == nil` | (no rate-limits section) | per account |
| `a.Limits.Error != nil` | `⚠ limits unavailable (<error>)` — **replaces** the rate-limits table | per account |
| `a.Limits.Error == nil` | `Rate limits` table: 5h, weekly, then each `WeeklyModels` window (sorted) with `HasData()` | per account |
| `a.Error != nil` (ccusage) | `⚠ ccusage error: <error>` | per account, appended after limits |

Precedence within an account: the Usage table always renders when usage is present; the limits section is EITHER the error line (when `Limits.Error`) OR the table (else); the ccusage-error line is independent and additive. The stale banner is global and coexists with all per-account output. All interpolated strings HTML-escaped.

**Verify:** `cd gnome-topbar/daemon && go test -race ./internal/server/...` → PASS.

**Steps:**

- [ ] **Step 1: Create `internal/server/statspage.go`** with the render helpers:

```go
package server

import (
	"fmt"
	"html"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/atstats"
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/claudestats"
)

func esc(s string) string { return html.EscapeString(s) }

// kvTable renders labelled rows as a two-column table.
func kvTable(title string, rows [][2]string) string {
	if len(rows) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<h2>` + esc(title) + `</h2><table>`)
	for _, r := range rows {
		b.WriteString(`<tr><td>` + esc(r[0]) + `</td><td>` + esc(r[1]) + `</td></tr>`)
	}
	b.WriteString(`</table>`)
	return b.String()
}

// histTable renders a count map sorted by descending count then key.
func histTable(title string, m map[string]int) string {
	if len(m) == 0 {
		return ""
	}
	type kv struct {
		k string
		v int
	}
	items := make([]kv, 0, len(m))
	for k, v := range m {
		items = append(items, kv{k, v})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].v != items[j].v {
			return items[i].v > items[j].v
		}
		return items[i].k < items[j].k
	})
	var b strings.Builder
	b.WriteString(`<h2>` + esc(title) + `</h2><table>`)
	for _, it := range items {
		b.WriteString(`<tr><td>` + esc(it.k) + `</td><td>` + strconv.Itoa(it.v) + `</td></tr>`)
	}
	b.WriteString(`</table>`)
	return b.String()
}

// renderStatsPage renders the anti-tangent rollup aggregates + CodeScene block.
func renderStatsPage(at atstats.Stats) string {
	if !at.Present {
		return pageShell("Stats", `<h1>Stats</h1><p class="muted">No anti-tangent stats yet (rollup.json absent).</p>`)
	}
	var b strings.Builder
	b.WriteString(`<h1>anti-tangent stats</h1>`)
	fmt.Fprintf(&b, `<p class="muted">%d calls · window %s → %s</p>`,
		at.TotalCalls, esc(at.WindowStart.Format("Jan 2 15:04")), esc(at.WindowEnd.Format("Jan 2 15:04")))
	b.WriteString(kvTable("Verdicts", [][2]string{
		{"pass", fmt.Sprintf("%.0f%% (%d)", at.PassPct, at.VerdictCounts["pass"])},
		{"warn", fmt.Sprintf("%.0f%% (%d)", at.WarnPct, at.VerdictCounts["warn"])},
		{"fail", fmt.Sprintf("%.0f%% (%d)", at.FailPct, at.VerdictCounts["fail"])},
	}))
	b.WriteString(kvTable("Throughput", [][2]string{
		{"findings/call", fmt.Sprintf("%.2f", at.FindingsPerCall)},
		{"review p50", fmt.Sprintf("%d ms", at.ReviewMSP50)},
		{"review p95", fmt.Sprintf("%d ms", at.ReviewMSP95)},
		{"cache hit", fmt.Sprintf("%.0f%%", at.CacheHitRate*100)},
		{"partial", fmt.Sprintf("%.0f%%", at.PartialRate*100)},
	}))
	b.WriteString(histTable("Per tool", at.PerTool))
	b.WriteString(histTable("Severity", at.SeverityHistogram))
	b.WriteString(histTable("Categories", at.CategoryHistogram))
	b.WriteString(histTable("Model usage", at.ModelUsage))
	b.WriteString(`<h2>CodeScene</h2>`)
	if cs := at.CodeScene; cs != nil {
		b.WriteString(kvTable("Code Health", [][2]string{
			{"latest score", fmt.Sprintf("%.1f", cs.LatestScore)},
			{"latest delta", fmt.Sprintf("%+.1f (%s)", cs.LatestDelta, esc(cs.LatestTrend))},
			{"score p50", fmt.Sprintf("%.1f", cs.ScoreP50)},
			{"runs", strconv.Itoa(cs.Runs)},
			{"reg / imp / neutral", fmt.Sprintf("%d / %d / %d", cs.Regressions, cs.Improvements, cs.Neutral)},
		}))
		b.WriteString(histTable("CodeScene categories", cs.CategoryHistogram))
	} else {
		b.WriteString(`<p class="muted">No data yet. Append <code>analyze_change_set</code> records to <code>codescene-events.jsonl</code>; see <code>docs/team-setup/codescene-stats.md</code>.</p>`)
	}
	if at.Summary != "" {
		b.WriteString(`<h2>Summary</h2><p>` + esc(at.Summary) + `</p>`)
	}
	return pageShell("Stats", b.String())
}

// humanTokensS renders a token count compactly (local copy; tray has its own).
func humanTokensS(n int64) string {
	switch {
	case n < 1000:
		return strconv.FormatInt(n, 10)
	case n < 1_000_000:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1000), ".0") + "k"
	default:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1_000_000), ".0") + "M"
	}
}

func usageStr(u *claudestats.Usage) string {
	return fmt.Sprintf("$%.2f · %s tok", u.CostUSD, humanTokensS(u.TotalTokens))
}

func winStr(w *claudestats.Window, now time.Time) string {
	s := "—"
	if w.Utilization != nil {
		s = fmt.Sprintf("%.0f%%", *w.Utilization)
	}
	if w.ResetsAt != nil && w.ResetsAt.After(now) {
		s += " · resets " + w.ResetsAt.Local().Format("Jan 2 15:04")
	}
	return s
}

// renderClaudePage renders per-account Claude usage + rate limits.
func renderClaudePage(cs claudestats.Stats, now time.Time) string {
	if !cs.Present {
		return pageShell("Claude usage", `<h1>Claude usage</h1><p class="muted">No claude-stats.json present.</p>`)
	}
	var b strings.Builder
	b.WriteString(`<h1>Claude usage</h1>`)
	if cs.Stale(now) {
		b.WriteString(`<p class="muted">⚠ stats stale (generated ` + esc(cs.GeneratedAt.Format("Jan 2 15:04")) + `)</p>`)
	}
	keys := make([]string, 0, len(cs.Accounts))
	for k := range cs.Accounts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		a := cs.Accounts[k]
		name := a.DisplayName
		if name == "" {
			name = k
		}
		b.WriteString(`<h2>` + esc(name) + `</h2>`)
		var urows [][2]string
		if a.Today != nil {
			urows = append(urows, [2]string{"today", usageStr(a.Today)})
		}
		if a.Week != nil {
			urows = append(urows, [2]string{"week", usageStr(a.Week)})
		}
		if a.Month != nil {
			urows = append(urows, [2]string{"month", usageStr(a.Month)})
		}
		b.WriteString(kvTable("Usage", urows))
		if a.Limits != nil {
			if a.Limits.Error != nil {
				b.WriteString(`<p class="muted">⚠ limits unavailable (` + esc(*a.Limits.Error) + `)</p>`)
			} else {
				var lrows [][2]string
				if w := a.Limits.FiveHour; w.HasData() {
					lrows = append(lrows, [2]string{"5h", winStr(w, now)})
				}
				if w := a.Limits.SevenDay; w.HasData() {
					lrows = append(lrows, [2]string{"weekly", winStr(w, now)})
				}
				mnames := make([]string, 0, len(a.Limits.WeeklyModels))
				for mn := range a.Limits.WeeklyModels {
					mnames = append(mnames, mn)
				}
				sort.Strings(mnames)
				for _, mn := range mnames {
					if w := a.Limits.WeeklyModels[mn]; w.HasData() {
						lrows = append(lrows, [2]string{mn, winStr(w, now)})
					}
				}
				b.WriteString(kvTable("Rate limits", lrows))
			}
		}
		if a.Error != nil {
			b.WriteString(`<p class="muted">⚠ ccusage error: ` + esc(*a.Error) + `</p>`)
		}
	}
	return pageShell("Claude usage", b.String())
}
```

- [ ] **Step 2: Register handlers + landing links** in `internal/server/ui.go`. Add inside `registerUI`:

```go
	mux.HandleFunc("/ui/stats", uiAuth(token, func(w http.ResponseWriter, r *http.Request) {
		writeHTML(w, renderStatsPage(p.Snapshot().AntiTangent))
	}))
	mux.HandleFunc("/ui/claude", uiAuth(token, func(w http.ResponseWriter, r *http.Request) {
		writeHTML(w, renderClaudePage(p.Snapshot().ClaudeStats, time.Now()))
	}))
```

  Add two cards to the `/ui/search` `ul.cards` list: `<li><a href="/ui/stats">📊 Stats</a></li>` and `<li><a href="/ui/claude">🤖 Claude usage</a></li>`. Add `"time"` to `ui.go`'s imports if not present.

- [ ] **Step 3: Create `internal/server/statspage_test.go`** (pages are pure functions of the stats):

```go
package server

import (
	"strings"
	"testing"
	"time"

	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/atstats"
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/claudestats"
)

func TestRenderStatsPageCodesceneEmptyState(t *testing.T) {
	at := atstats.Stats{Present: true, TotalCalls: 10, PerTool: map[string]int{"validate_completion": 6}}
	out := renderStatsPage(at)
	if !strings.Contains(out, "anti-tangent stats") || !strings.Contains(out, "No data yet") {
		t.Fatalf("stats page missing rollup or CodeScene empty-state:\n%s", out)
	}
}

func TestRenderStatsPageCodescenePresent(t *testing.T) {
	at := atstats.Stats{Present: true, CodeScene: &atstats.CodeSceneStats{LatestScore: 8.4, LatestTrend: "regression"}}
	if !strings.Contains(renderStatsPage(at), "8.4") {
		t.Fatal("CodeScene score not rendered")
	}
}

func TestRenderClaudePageFableAndError(t *testing.T) {
	util := 69.0
	reset := time.Date(2026, 6, 8, 20, 0, 0, 0, time.UTC)
	errStr := "usage endpoint HTTP 401"
	cs := claudestats.Stats{Present: true, Accounts: map[string]claudestats.Account{
		"default": {DisplayName: "default", Limits: &claudestats.Limits{
			WeeklyModels: map[string]*claudestats.Window{"Fable": {Utilization: &util, ResetsAt: &reset}},
		}},
		"alt": {DisplayName: "alt", Limits: &claudestats.Limits{Error: &errStr}},
	}}
	out := renderClaudePage(cs, time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC))
	if !strings.Contains(out, "Fable") || !strings.Contains(out, "69%") {
		t.Errorf("Fable window not rendered:\n%s", out)
	}
	if !strings.Contains(out, "limits unavailable") {
		t.Errorf("limits error not rendered:\n%s", out)
	}
}

func TestRenderPagesAbsentAreGraceful(t *testing.T) {
	if !strings.Contains(renderStatsPage(atstats.Stats{}), "No anti-tangent stats") {
		t.Error("absent stats not graceful")
	}
	if !strings.Contains(renderClaudePage(claudestats.Stats{}, time.Now()), "No claude-stats.json") {
		t.Error("absent claude stats not graceful")
	}
}
```

- [ ] **Step 4: Run → GREEN.** `go test -race ./internal/server/...` → PASS. Then `go build ./...` from `gnome-topbar/daemon`.

- [ ] **Step 5: Commit.**

```bash
git add internal/server/statspage.go internal/server/statspage_test.go internal/server/ui.go
git commit -m "feat(gnome-topbar): add /ui/stats and /ui/claude web detail pages"
```

```json:metadata
{"repo": "anti-tangent-mcp", "files": ["gnome-topbar/daemon/internal/server/statspage.go", "gnome-topbar/daemon/internal/server/statspage_test.go", "gnome-topbar/daemon/internal/server/ui.go"], "verifyCommand": "cd gnome-topbar/daemon && go test -race ./internal/server/...", "acceptanceCriteria": ["/ui/stats rollup + codescene/empty-state", "/ui/claude usage+limits+Fable+error", "landing links", "absent = graceful"], "modelTier": "standard"}
```

---

### Task 6: [gnome-topbar] Tray "Details…" entry points + daemon wiring

**Goal:** Two tray items open the web pages; shown only when the respective stats are present.

**Repo:** `anti-tangent-mcp` (`gnome-topbar/daemon`). **Depends on:** Task 5.

**Files:**
- Modify: `internal/tray/tray.go` (`Actions` + items + show/hide)
- Modify: `cmd/gnome-topbar-daemon/main.go` (actions)

**Acceptance Criteria:**
- [ ] `Actions` has `OpenStats`/`OpenClaude`; two menu items ("📊 Stats details…", "🤖 Claude usage details…") open `/ui/stats` / `/ui/claude`.
- [ ] Each item shows only when its stats are present (`AntiTangent.Present` / `ClaudeStats.Present && len(Accounts)>0`).

**Verify:** `cd gnome-topbar/daemon && go build ./... && go test -race ./internal/tray/...` → PASS.

**Steps:**

- [ ] **Step 1: Extend `Actions`** in `internal/tray/tray.go`:

```go
	OpenStats   func() // open the /ui/stats detail page
	OpenClaude  func() // open the /ui/claude detail page
```

- [ ] **Step 2: Add the item fields** to the `Tray` struct:

```go
	statsDetailItem  *systray.MenuItem
	claudeDetailItem *systray.MenuItem
```

- [ ] **Step 3: Create the items** in `onReady` (near the stats/claude submenu setup):

```go
	t.statsDetailItem = systray.AddMenuItem("📊 Stats details…", "open the stats detail page")
	t.statsDetailItem.Hide()
	go func() {
		for range t.statsDetailItem.ClickedCh {
			if t.act.OpenStats != nil {
				t.act.OpenStats()
			}
		}
	}()
	t.claudeDetailItem = systray.AddMenuItem("🤖 Claude usage details…", "open the Claude usage detail page")
	t.claudeDetailItem.Hide()
	go func() {
		for range t.claudeDetailItem.ClickedCh {
			if t.act.OpenClaude != nil {
				t.act.OpenClaude()
			}
		}
	}()
```

- [ ] **Step 4: Show/hide in `render`** (under `t.mu`, near the existing stats/claude show/hide):

```go
	showIf(t.statsDetailItem, snap.AntiTangent.Present)
	showIf(t.claudeDetailItem, snap.ClaudeStats.Present && len(snap.ClaudeStats.Accounts) > 0)
```

- [ ] **Step 5: Wire the actions** in `cmd/gnome-topbar-daemon/main.go`, add to the `tray.Actions{...}` literal:

```go
		OpenStats:  func() { openLocal("/ui/stats") },
		OpenClaude: func() { openLocal("/ui/claude") },
```

- [ ] **Step 6: Build + test.** `go build ./... && go test -race ./internal/tray/...` → PASS.

- [ ] **Step 7: Commit.**

```bash
git add internal/tray/tray.go cmd/gnome-topbar-daemon/main.go
git commit -m "feat(gnome-topbar): tray items to open /ui/stats and /ui/claude"
```

```json:metadata
{"repo": "anti-tangent-mcp", "files": ["gnome-topbar/daemon/internal/tray/tray.go", "gnome-topbar/daemon/cmd/gnome-topbar-daemon/main.go"], "verifyCommand": "cd gnome-topbar/daemon && go build ./... && go test -race ./internal/tray/...", "acceptanceCriteria": ["OpenStats/OpenClaude actions", "two Details items open pages", "shown only when present"], "modelTier": "standard"}
```

---

### Task 7: [gnome-topbar] Changelog v0.3.0

**Goal:** Document the feature for the gnome-topbar release.

**Repo:** `anti-tangent-mcp` (`gnome-topbar/`). **Depends on:** Tasks 4, 5, 6.

**Files:**
- Modify: `gnome-topbar/README.md` (Changelog)

**Acceptance Criteria:**
- [ ] A `### v0.3.0` entry describes per-model (Fable) Claude usage + the `/ui/stats` + `/ui/claude` detail pages.

**Verify:** `grep -n "v0.3.0" gnome-topbar/README.md` → shows the entry.

**Steps:**

- [ ] **Step 1: Add the entry** above `### v0.2.2` in `gnome-topbar/README.md`:

```markdown
### v0.3.0
- Per-model weekly Claude usage: the Claude usage submenu shows a row per model (incl. **Fable**), decoded from the producer's new `limits.weekly_models` (claude-stats schema 1.2).
- New web detail pages **📊 Stats** (`/ui/stats`, anti-tangent rollup + CodeScene block/empty-state) and **🤖 Claude usage** (`/ui/claude`, per-account usage, rate limits incl. per-model, error/stale states), opened from tray "Details…" items. Tray dropdown stays lean; the top-bar icon and compact overview are unchanged.
```

- [ ] **Step 2: Commit** (docs-only; per the gnome-topbar release procedure, keep docs separate from code and mark `[skip ci]` when appropriate):

```bash
git add gnome-topbar/README.md
git commit -m "docs(gnome-topbar): changelog v0.3.0 (per-model Fable usage + web detail pages) [skip ci]"
```

```json:metadata
{"repo": "anti-tangent-mcp", "files": ["gnome-topbar/README.md"], "verifyCommand": "grep -n 'v0.3.0' gnome-topbar/README.md", "acceptanceCriteria": ["v0.3.0 entry present"], "modelTier": "mechanical"}
```

---

## Notes for execution

- **Repos:** Task 1 is in `claude-sandbox`; Tasks 2–7 in `anti-tangent-mcp` on branch `gnome-topbar-stats-detail` (already created; the ② spec is committed there).
- **Release:** gnome-topbar has its own versioning (README changelog, currently v0.2.2). This is v0.3.0 (minor). Follow the gnome-topbar release procedure (split code vs docs `[skip ci]`; release runs from the GitHub UI). CI's branch↔CHANGELOG check only fires on `version/X.Y.Z` branches, which this is not.
- **Graceful degradation:** with ① not yet deployed to your machine, `weekly_models` is absent → per-model rows/`/ui/claude` model rows are simply empty; nothing breaks. Fable appears once the producer writes schema 1.2.
- **Full-suite gate before done:** `cd gnome-topbar/daemon && go build ./... && go test -race ./...`.
