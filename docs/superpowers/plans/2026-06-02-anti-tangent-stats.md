# anti-tangent stats + LLM performance summary — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an opt-in (`ANTI_TANGENT_STATS_DIR`) statistics subsystem that records one counts-only record per hook call, periodically asks a reviewer LLM for a prose performance summary, and additionally aggregates agent-appended CodeScene Code Health metrics — all written as plain files, with zero behavior change when disabled.

**Architecture:** A new `internal/stats` package with four small, independently testable units — `Event`/`CountFindings`, `State`/`due` (cadence + salt), `Rollup` (deterministic aggregation), and `Compactor` (rollup.json + LLM `summary.md`) — fronted by a nil-safe `Recorder` (append + single-flight async compaction). `internal/mcpsrv` gains one nil dependency (`Deps.Stats *stats.Recorder`); each handler maps its own result into an `Event` at its finalize point. `stats` imports only `internal/verdict`, `internal/providers`, and `internal/config` (never `internal/mcpsrv`), so there is no import cycle. The CodeScene companion is the agent appending raw records to `codescene-events.jsonl` (the server never sees those calls) which the Compactor reads into a nested `codescene` block in `rollup.json`.

**Tech Stack:** Go (stdlib only — `encoding/json`, `crypto/sha256`, `crypto/rand`, `os`, `sync`, `sync/atomic`, `log/slog`); `go test -race`; existing `providers.Reviewer` for the summary LLM call.

**Authoritative spec:** `docs/superpowers/specs/2026-06-02-anti-tangent-stats-design.md` (§1–§11 main subsystem; §12 CodeScene companion; §3.3 / §12.4 carry the load-bearing snake_case `rollup.json` contract with the gnome-topbar consumer).

---

## File Structure

**New package `internal/stats`:**
- `event.go` — `Event` record (counts + metadata only) + `CountFindings` helper.
- `state.go` — `State` (cadence + salt), `loadState`/`saveState`, `newSalt`, `due` trigger.
- `rollup.go` — `Rollup` struct (pinned snake_case json tags) + `computeRollup` + `percentile`.
- `io.go` — generic JSONL append/read/rewrite helpers + file-name constants.
- `compactor.go` — `Compactor.Compact` (writes `rollup.json`, calls reviewer, writes `summary.md`/`summaries.jsonl`) + summary prompt/schema.
- `codescene.go` — `CodesceneEvent` (read shape), `CodesceneRollup`, `computeCodescene`, `readCodescene`/`pruneCodescene` (added in Task 9).
- `recorder.go` — `Recorder` + `Options` + `New` + `Record` + `HashSession` + `compact` (single-flight async).
- `*_test.go` — internal (`package stats`) tests for each unit.

**Modified:**
- `internal/config/config.go` — six `ANTI_TANGENT_STATS_*` fields + parsing/validation.
- `internal/config/stats_config_test.go` — new test file (`package config_test`).
- `internal/mcpsrv/server.go` — `Deps.Stats *stats.Recorder`.
- `internal/mcpsrv/handlers.go` — `recordStats` / `recordPlanStats` / `recordResultStats` helpers + insertions in the per-task hooks and `ValidatePlan`.
- `internal/mcpsrv/prime_handler.go`, `extract_handler.go` — record insertions.
- `internal/mcpsrv/handlers_stats_test.go` — new wiring test.
- `cmd/anti-tangent-mcp/main.go` — construct the `Recorder` when enabled; pass into `Deps`.
- `CHANGELOG.md`, `README.md`, `CLAUDE.md`, new `docs/team-setup/codescene-stats.md`, `INTEGRATION.md`.

---

## Task 1: CHANGELOG `[0.10.0]` entry + config vars

**Files:**
- Modify: `CHANGELOG.md` (new `## [0.10.0]` block at top of entries)
- Modify: `internal/config/config.go` (struct fields + defaults + parsing)
- Test: `internal/config/stats_config_test.go` (create)

- [ ] **Step 1: Add the CHANGELOG entry**

Insert this block immediately above the existing `## [0.9.1] - 2026-05-29` line in `CHANGELOG.md`:

```markdown
## [0.10.0] - 2026-06-02

### Added
- Opt-in statistics subsystem (`ANTI_TANGENT_STATS_DIR`): records one counts-only record per hook call to `events.jsonl`, periodically aggregates a deterministic `rollup.json` and an LLM-written `summary.md`, and prunes by `ANTI_TANGENT_STATS_RETENTION_DAYS`. Entirely inert when the var is unset (no files, no overhead, no behavior change). Records hold counts + metadata only — no finding text, no plan/spec content, no raw session id (salted hash only). New vars: `ANTI_TANGENT_STATS_MODEL`, `ANTI_TANGENT_STATS_SUMMARY_INTERVAL`, `ANTI_TANGENT_STATS_SUMMARY_THRESHOLD`, `ANTI_TANGENT_STATS_RETENTION_DAYS`, `ANTI_TANGENT_STATS_MAX_TOKENS`.
- CodeScene companion (spec §12): the agent appends one counts-only record per `analyze_change_set` run to `codescene-events.jsonl`; the Compactor aggregates them into a nested `codescene` block in `rollup.json` and retention-prunes the file. See `docs/team-setup/codescene-stats.md`.

### Changed

### Fixed

### Removed

### Deprecated

### Security
```

- [ ] **Step 2: Write the failing config test**

Create `internal/config/stats_config_test.go`:

```go
package config_test

import (
	"testing"
	"time"

	"github.com/patiently/anti-tangent-mcp/internal/config"
)

func envWith(extra map[string]string) func(string) string {
	return func(k string) string {
		if k == "ANTHROPIC_API_KEY" {
			return "test-key"
		}
		return extra[k]
	}
}

func TestStatsDefaults(t *testing.T) {
	cfg, err := config.Load(envWith(nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.StatsDir != "" {
		t.Errorf("StatsDir = %q, want empty (disabled)", cfg.StatsDir)
	}
	if cfg.StatsModel != cfg.MidModel {
		t.Errorf("StatsModel = %v, want MidModel %v", cfg.StatsModel, cfg.MidModel)
	}
	if cfg.StatsSummaryInterval != 24*time.Hour {
		t.Errorf("StatsSummaryInterval = %v, want 24h", cfg.StatsSummaryInterval)
	}
	if cfg.StatsSummaryThreshold != 50 {
		t.Errorf("StatsSummaryThreshold = %d, want 50", cfg.StatsSummaryThreshold)
	}
	if cfg.StatsRetentionDays != 30 {
		t.Errorf("StatsRetentionDays = %d, want 30", cfg.StatsRetentionDays)
	}
	if cfg.StatsMaxTokens != 2048 {
		t.Errorf("StatsMaxTokens = %d, want 2048", cfg.StatsMaxTokens)
	}
}

func TestStatsOverridesAndClamp(t *testing.T) {
	cfg, err := config.Load(envWith(map[string]string{
		"ANTI_TANGENT_STATS_DIR":               "/tmp/at-stats",
		"ANTI_TANGENT_STATS_MODEL":             "openai:gpt-5-mini",
		"ANTI_TANGENT_STATS_SUMMARY_INTERVAL":  "1h",
		"ANTI_TANGENT_STATS_SUMMARY_THRESHOLD": "5",
		"ANTI_TANGENT_STATS_RETENTION_DAYS":    "7",
		"ANTI_TANGENT_STATS_MAX_TOKENS":        "999999", // above ceiling
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.StatsDir != "/tmp/at-stats" {
		t.Errorf("StatsDir = %q", cfg.StatsDir)
	}
	if cfg.StatsModel.String() != "openai:gpt-5-mini" {
		t.Errorf("StatsModel = %v", cfg.StatsModel)
	}
	if cfg.StatsSummaryInterval != time.Hour {
		t.Errorf("interval = %v", cfg.StatsSummaryInterval)
	}
	if cfg.StatsMaxTokens != cfg.MaxTokensCeiling {
		t.Errorf("StatsMaxTokens = %d, want clamped to ceiling %d", cfg.StatsMaxTokens, cfg.MaxTokensCeiling)
	}
}

func TestStatsInvalidValues(t *testing.T) {
	for _, k := range []string{
		"ANTI_TANGENT_STATS_SUMMARY_INTERVAL",
		"ANTI_TANGENT_STATS_SUMMARY_THRESHOLD",
		"ANTI_TANGENT_STATS_RETENTION_DAYS",
		"ANTI_TANGENT_STATS_MAX_TOKENS",
	} {
		// Non-positive / unparseable values must error and name the var.
		bad := "0"
		if k == "ANTI_TANGENT_STATS_SUMMARY_INTERVAL" {
			bad = "nope"
		}
		if _, err := config.Load(envWith(map[string]string{k: bad})); err == nil {
			t.Errorf("%s=%q: expected error, got nil", k, bad)
		}
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/config/ -run TestStats -v`
Expected: FAIL — `cfg.StatsDir` / `cfg.StatsModel` etc. undefined (compile error).

- [ ] **Step 4: Add the config fields**

In `internal/config/config.go`, add these fields to the `Config` struct (after `MaxTokensCeiling int`, before the `KBStore` doc comment):

```go
	// Stats subsystem (opt-in; see spec 2026-06-02). StatsDir == "" disables
	// it entirely.
	StatsDir              string
	StatsModel            ModelRef
	StatsSummaryInterval  time.Duration
	StatsSummaryThreshold int
	StatsRetentionDays    int
	StatsMaxTokens        int
```

In `Load`, add these defaults to the `cfg := Config{...}` literal (after `MaxTokensCeiling: 16384,`):

```go
		StatsSummaryInterval:  24 * time.Hour,
		StatsSummaryThreshold: 50,
		StatsRetentionDays:    30,
		StatsMaxTokens:        2048,
```

- [ ] **Step 5: Add the parsing block**

In `internal/config/config.go`, insert this block immediately AFTER the `ANTI_TANGENT_MAX_TOKENS_CEILING` parsing block (the one ending `cfg.MaxTokensCeiling = n }`) and BEFORE the `ANTI_TANGENT_LOG_LEVEL` block, so the ceiling is already parsed when we clamp:

```go
	cfg.StatsDir = env("ANTI_TANGENT_STATS_DIR")

	// StatsModel: explicit override -> MidModel.
	if v := env("ANTI_TANGENT_STATS_MODEL"); v != "" {
		mr, err := ParseModelRef(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_STATS_MODEL: %w", err)
		}
		cfg.StatsModel = mr
	} else {
		cfg.StatsModel = cfg.MidModel
	}

	if v := env("ANTI_TANGENT_STATS_SUMMARY_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_STATS_SUMMARY_INTERVAL: %w", err)
		}
		if d <= 0 {
			return Config{}, fmt.Errorf("ANTI_TANGENT_STATS_SUMMARY_INTERVAL: must be positive, got %s", d)
		}
		cfg.StatsSummaryInterval = d
	}
	if v := env("ANTI_TANGENT_STATS_SUMMARY_THRESHOLD"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_STATS_SUMMARY_THRESHOLD: %w", err)
		}
		if n <= 0 {
			return Config{}, fmt.Errorf("ANTI_TANGENT_STATS_SUMMARY_THRESHOLD: must be positive, got %d", n)
		}
		cfg.StatsSummaryThreshold = n
	}
	if v := env("ANTI_TANGENT_STATS_RETENTION_DAYS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_STATS_RETENTION_DAYS: %w", err)
		}
		if n <= 0 {
			return Config{}, fmt.Errorf("ANTI_TANGENT_STATS_RETENTION_DAYS: must be positive, got %d", n)
		}
		cfg.StatsRetentionDays = n
	}
	if v := env("ANTI_TANGENT_STATS_MAX_TOKENS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_STATS_MAX_TOKENS: %w", err)
		}
		if n <= 0 {
			return Config{}, fmt.Errorf("ANTI_TANGENT_STATS_MAX_TOKENS: must be positive, got %d", n)
		}
		cfg.StatsMaxTokens = n
	}
	if cfg.StatsMaxTokens > cfg.MaxTokensCeiling {
		cfg.StatsMaxTokens = cfg.MaxTokensCeiling
	}
```

- [ ] **Step 6: Run the test to verify it passes**

Run: `go test ./internal/config/ -run TestStats -v`
Expected: PASS (all three tests).

- [ ] **Step 7: Commit**

```bash
git add CHANGELOG.md internal/config/config.go internal/config/stats_config_test.go
git commit -m "feat(stats): add ANTI_TANGENT_STATS_* config + CHANGELOG [0.10.0]"
```

---

## Task 2: `stats.Event` + `CountFindings`

**Files:**
- Create: `internal/stats/event.go`
- Test: `internal/stats/event_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/stats/event_test.go`:

```go
package stats

import (
	"testing"

	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

func TestCountFindings(t *testing.T) {
	findings := []verdict.Finding{
		{Severity: verdict.SeverityMajor, Category: verdict.CategoryScopeDrift},
		{Severity: verdict.SeverityMajor, Category: verdict.CategoryAmbiguousSpec},
		{Severity: verdict.SeverityMinor, Category: verdict.CategoryScopeDrift},
	}
	sev, cat, total := CountFindings(findings)
	if total != 3 {
		t.Fatalf("total = %d, want 3", total)
	}
	if sev["major"] != 2 || sev["minor"] != 1 {
		t.Fatalf("severity = %v", sev)
	}
	if cat["scope_drift"] != 2 || cat["ambiguous_spec"] != 1 {
		t.Fatalf("category = %v", cat)
	}
}

func TestCountFindingsEmpty(t *testing.T) {
	sev, cat, total := CountFindings(nil)
	if sev != nil || cat != nil || total != 0 {
		t.Fatalf("want nil,nil,0; got %v,%v,%d", sev, cat, total)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/stats/ -run TestCountFindings -v`
Expected: FAIL — package/`CountFindings` not defined.

- [ ] **Step 3: Write the implementation**

Create `internal/stats/event.go`:

```go
// Package stats records compact, counts-only statistics about anti-tangent hook
// calls and periodically asks a reviewer LLM for a prose performance summary.
// Everything is opt-in via ANTI_TANGENT_STATS_DIR and best-effort: a stats
// failure never affects a hook's result or latency.
//
// Import direction: stats imports internal/verdict, internal/providers, and
// internal/config only. internal/mcpsrv imports stats, never the reverse, so
// there is no import cycle.
package stats

import (
	"time"

	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

// Event is one counts-only record appended per hook call. It deliberately holds
// NO finding text, plan/spec content, or raw session id (SessionHash is a salted
// digest, never the raw id).
type Event struct {
	Ts             time.Time      `json:"ts"`
	Tool           string         `json:"tool"`
	Verdict        string         `json:"verdict,omitempty"`
	FindingsTotal  int            `json:"findings_total"`
	SeverityCounts map[string]int `json:"severity_counts,omitempty"`
	CategoryCounts map[string]int `json:"category_counts,omitempty"`
	ReviewMS       int64          `json:"review_ms"`
	Model          string         `json:"model,omitempty"`
	Cached         bool           `json:"cached,omitempty"`
	Partial        bool           `json:"partial,omitempty"`
	PayloadBytes   int            `json:"payload_bytes,omitempty"`
	SessionHash    string         `json:"session_hash,omitempty"`
}

// CountFindings builds severity and category histograms (and the total) from a
// finding slice. Returns nil maps when there are no findings so empty Events
// serialize without empty objects.
func CountFindings(findings []verdict.Finding) (severity, category map[string]int, total int) {
	if len(findings) == 0 {
		return nil, nil, 0
	}
	severity = make(map[string]int)
	category = make(map[string]int)
	for _, f := range findings {
		severity[string(f.Severity)]++
		category[string(f.Category)]++
	}
	return severity, category, len(findings)
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test -race ./internal/stats/ -run TestCountFindings -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/stats/event.go internal/stats/event_test.go
git commit -m "feat(stats): add Event record + CountFindings helper"
```

---

## Task 3: `stats.State` + salt + `due` trigger

**Files:**
- Create: `internal/stats/state.go`
- Test: `internal/stats/state_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/stats/state_test.go`:

```go
package stats

import (
	"testing"
	"time"
)

func TestStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := State{
		LastSummaryAt:      time.Unix(1700000000, 0).UTC(),
		EventsSinceSummary: 7,
		Salt:               "deadbeef",
	}
	if err := saveState(dir, want); err != nil {
		t.Fatalf("saveState: %v", err)
	}
	got := loadState(dir)
	if !got.LastSummaryAt.Equal(want.LastSummaryAt) || got.EventsSinceSummary != 7 || got.Salt != "deadbeef" {
		t.Fatalf("round-trip mismatch: got %+v want %+v", got, want)
	}
}

func TestLoadStateMissingGeneratesSalt(t *testing.T) {
	st := loadState(t.TempDir())
	if st.Salt == "" {
		t.Fatal("expected a generated salt for a missing state file")
	}
	if !st.LastSummaryAt.IsZero() || st.EventsSinceSummary != 0 {
		t.Fatalf("expected zero cadence, got %+v", st)
	}
}

func TestDue(t *testing.T) {
	base := time.Unix(1700000000, 0).UTC()
	cases := []struct {
		name  string
		since int
		last  time.Time
		want  bool
	}{
		{"threshold hit", 50, base, true},
		{"under both", 10, base, false},
		{"interval elapsed", 0, base.Add(-25 * time.Hour), true},
		{"interval not elapsed", 0, base.Add(-1 * time.Hour), false},
	}
	for _, c := range cases {
		got := due(base, State{LastSummaryAt: c.last, EventsSinceSummary: c.since}, 24*time.Hour, 50)
		if got != c.want {
			t.Errorf("%s: due = %v, want %v", c.name, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/stats/ -run 'TestState|TestLoadState|TestDue' -v`
Expected: FAIL — `State`/`loadState`/`saveState`/`due` not defined.

- [ ] **Step 3: Write the implementation**

Create `internal/stats/state.go`:

```go
package stats

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

const stateFile = "state.json"

// State persists cadence + the session-hash salt across process restarts so a
// freshly-launched stdio server neither re-summarizes immediately nor loses the
// interval.
type State struct {
	LastSummaryAt      time.Time `json:"last_summary_at"`
	EventsSinceSummary int       `json:"events_since_summary"`
	Salt               string    `json:"salt"`
}

// loadState reads state.json from dir. A missing or corrupt file yields a zero
// State with a freshly-generated salt, so a bad file never blocks recording.
func loadState(dir string) State {
	b, err := os.ReadFile(filepath.Join(dir, stateFile))
	if err != nil {
		return State{Salt: newSalt()}
	}
	var st State
	if err := json.Unmarshal(b, &st); err != nil || st.Salt == "" {
		return State{Salt: newSalt()}
	}
	return st
}

func saveState(dir string, st State) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, stateFile), b, 0o644)
}

func newSalt() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand.Read effectively never fails; degrade to a time-derived
		// salt rather than returning an empty (unsalted) value.
		return hex.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(b[:])
}

// due reports whether a compaction should run now, per the spec §5 hybrid
// trigger. New() seeds LastSummaryAt on first enable, so the interval branch is
// measured from enable time, not the zero epoch.
func due(now time.Time, st State, interval time.Duration, threshold int) bool {
	return st.EventsSinceSummary >= threshold || now.Sub(st.LastSummaryAt) >= interval
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test -race ./internal/stats/ -run 'TestState|TestLoadState|TestDue' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/stats/state.go internal/stats/state_test.go
git commit -m "feat(stats): add State persistence, salt, and due trigger"
```

---

## Task 4: `stats.Rollup` deterministic aggregation

**Files:**
- Create: `internal/stats/rollup.go`
- Test: `internal/stats/rollup_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/stats/rollup_test.go`:

```go
package stats

import (
	"testing"
	"time"
)

func TestComputeRollup(t *testing.T) {
	base := time.Unix(1700000000, 0).UTC()
	events := []Event{
		{Ts: base, Tool: "validate_task_spec", Verdict: "pass", FindingsTotal: 0, ReviewMS: 100, Model: "anthropic:m"},
		{Ts: base.Add(time.Minute), Tool: "validate_completion", Verdict: "warn", FindingsTotal: 2,
			SeverityCounts: map[string]int{"major": 1, "minor": 1}, CategoryCounts: map[string]int{"scope_drift": 2},
			ReviewMS: 300, Model: "anthropic:m", Partial: true},
		{Ts: base.Add(2 * time.Minute), Tool: "validate_plan", Verdict: "fail", FindingsTotal: 1,
			SeverityCounts: map[string]int{"critical": 1}, CategoryCounts: map[string]int{"missing_acceptance_criterion": 1},
			ReviewMS: 500, Model: "openai:n", Cached: true},
	}
	r := computeRollup(events, base.Add(time.Hour))

	if r.TotalCalls != 3 {
		t.Errorf("TotalCalls = %d, want 3", r.TotalCalls)
	}
	if r.PerTool["validate_plan"] != 1 || r.PerTool["validate_task_spec"] != 1 {
		t.Errorf("PerTool = %v", r.PerTool)
	}
	if r.VerdictCounts["pass"] != 1 || r.VerdictCounts["warn"] != 1 || r.VerdictCounts["fail"] != 1 {
		t.Errorf("VerdictCounts = %v", r.VerdictCounts)
	}
	if r.FindingsPerCall != 1.0 {
		t.Errorf("FindingsPerCall = %v, want 1.0", r.FindingsPerCall)
	}
	if r.SeverityHistogram["major"] != 1 || r.SeverityHistogram["critical"] != 1 {
		t.Errorf("SeverityHistogram = %v", r.SeverityHistogram)
	}
	if r.CategoryHistogram["scope_drift"] != 2 {
		t.Errorf("CategoryHistogram = %v", r.CategoryHistogram)
	}
	if r.CacheHitRate <= 0.33 || r.CacheHitRate >= 0.34 {
		t.Errorf("CacheHitRate = %v, want ~0.333", r.CacheHitRate)
	}
	if r.PartialRate <= 0.33 || r.PartialRate >= 0.34 {
		t.Errorf("PartialRate = %v, want ~0.333", r.PartialRate)
	}
	if r.ReviewMSP50 != 300 || r.ReviewMSP95 != 500 {
		t.Errorf("p50/p95 = %d/%d, want 300/500", r.ReviewMSP50, r.ReviewMSP95)
	}
	if r.ModelUsage["anthropic:m"] != 2 || r.ModelUsage["openai:n"] != 1 {
		t.Errorf("ModelUsage = %v", r.ModelUsage)
	}
	if !r.WindowStart.Equal(base) || !r.WindowEnd.Equal(base.Add(2*time.Minute)) {
		t.Errorf("window = %v..%v", r.WindowStart, r.WindowEnd)
	}
}

func TestComputeRollupEmpty(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	r := computeRollup(nil, now)
	if r.TotalCalls != 0 {
		t.Errorf("TotalCalls = %d, want 0", r.TotalCalls)
	}
	if !r.WindowStart.Equal(now) || !r.WindowEnd.Equal(now) || !r.GeneratedAt.Equal(now) {
		t.Errorf("empty rollup windows = %v..%v gen %v", r.WindowStart, r.WindowEnd, r.GeneratedAt)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/stats/ -run TestComputeRollup -v`
Expected: FAIL — `Rollup`/`computeRollup` not defined.

- [ ] **Step 3: Write the implementation**

Create `internal/stats/rollup.go`:

```go
package stats

import (
	"math"
	"sort"
	"time"
)

// Rollup is the deterministic aggregate written to rollup.json. The json tags
// are a LOAD-BEARING cross-component contract — the gnome-topbar consumer
// (branch feat/gnome-topbar, Task 17) reads these exact snake_case keys. Go
// marshals PascalCase by default, which would silently break that consumer, so
// every field is tagged. Changing/dropping a key is a breaking change.
//
// The Codescene field is added in Task 9 (the CodeScene companion).
type Rollup struct {
	WindowStart       time.Time      `json:"window_start"`
	WindowEnd         time.Time      `json:"window_end"`
	TotalCalls        int            `json:"total_calls"`
	PerTool           map[string]int `json:"per_tool"`
	VerdictCounts     map[string]int `json:"verdict_counts"`
	FindingsPerCall   float64        `json:"findings_per_call"`
	SeverityHistogram map[string]int `json:"severity_histogram"`
	CategoryHistogram map[string]int `json:"category_histogram"`
	ReviewMSP50       int64          `json:"review_ms_p50"`
	ReviewMSP95       int64          `json:"review_ms_p95"`
	CacheHitRate      float64        `json:"cache_hit_rate"`
	PartialRate       float64        `json:"partial_rate"`
	ModelUsage        map[string]int `json:"model_usage"`
	GeneratedAt       time.Time      `json:"generated_at"`
}

// computeRollup aggregates events into a Rollup. now stamps GeneratedAt (and the
// window for an empty event set).
func computeRollup(events []Event, now time.Time) Rollup {
	r := Rollup{
		PerTool:           map[string]int{},
		VerdictCounts:     map[string]int{},
		SeverityHistogram: map[string]int{},
		CategoryHistogram: map[string]int{},
		ModelUsage:        map[string]int{},
		GeneratedAt:       now,
		TotalCalls:        len(events),
	}
	if len(events) == 0 {
		r.WindowStart, r.WindowEnd = now, now
		return r
	}
	var totalFindings, cached, partial int
	latencies := make([]int64, 0, len(events))
	r.WindowStart, r.WindowEnd = events[0].Ts, events[0].Ts
	for _, e := range events {
		if e.Ts.Before(r.WindowStart) {
			r.WindowStart = e.Ts
		}
		if e.Ts.After(r.WindowEnd) {
			r.WindowEnd = e.Ts
		}
		r.PerTool[e.Tool]++
		if e.Verdict != "" {
			r.VerdictCounts[e.Verdict]++
		}
		totalFindings += e.FindingsTotal
		for k, v := range e.SeverityCounts {
			r.SeverityHistogram[k] += v
		}
		for k, v := range e.CategoryCounts {
			r.CategoryHistogram[k] += v
		}
		if e.Model != "" {
			r.ModelUsage[e.Model]++
		}
		if e.Cached {
			cached++
		}
		if e.Partial {
			partial++
		}
		latencies = append(latencies, e.ReviewMS)
	}
	n := float64(len(events))
	r.FindingsPerCall = float64(totalFindings) / n
	r.CacheHitRate = float64(cached) / n
	r.PartialRate = float64(partial) / n
	r.ReviewMSP50 = percentile(latencies, 50)
	r.ReviewMSP95 = percentile(latencies, 95)
	return r
}

// percentile returns the nearest-rank p-th percentile of xs (p in 1..100).
func percentile(xs []int64, p int) int64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]int64(nil), xs...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	rank := int(math.Ceil(float64(p)/100*float64(len(s)))) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(s) {
		rank = len(s) - 1
	}
	return s[rank]
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test -race ./internal/stats/ -run TestComputeRollup -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/stats/rollup.go internal/stats/rollup_test.go
git commit -m "feat(stats): add Rollup aggregation with pinned json contract"
```

---

## Task 5: JSONL io helpers + `stats.Compactor`

**Files:**
- Create: `internal/stats/io.go`
- Create: `internal/stats/compactor.go`
- Test: `internal/stats/compactor_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/stats/compactor_test.go`:

```go
package stats

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/patiently/anti-tangent-mcp/internal/providers"
)

type fakeReviewer struct {
	resp providers.Response
	err  error
}

func (f fakeReviewer) Name() string { return "fake" }
func (f fakeReviewer) Review(ctx context.Context, req providers.Request) (providers.Response, error) {
	return f.resp, f.err
}

func sampleEvents(base time.Time) []Event {
	return []Event{
		{Ts: base, Tool: "validate_task_spec", Verdict: "pass", ReviewMS: 100, Model: "anthropic:m"},
		{Ts: base.Add(time.Minute), Tool: "validate_completion", Verdict: "warn", FindingsTotal: 1,
			SeverityCounts: map[string]int{"major": 1}, ReviewMS: 200, Model: "anthropic:m"},
	}
}

func TestCompactWritesRollupAndSummary(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1700000000, 0).UTC()
	c := &Compactor{
		dir:       dir,
		reviewer:  fakeReviewer{resp: providers.Response{RawJSON: []byte(`{"summary":"All green. 2 calls."}`)}},
		model:     "anthropic:m",
		maxTokens: 2048,
		timeout:   5 * time.Second,
		logger:    slog.Default(),
	}
	c.Compact(now, sampleEvents(now))

	// rollup.json present + parseable + correct count.
	rb, err := os.ReadFile(filepath.Join(dir, rollupFile))
	if err != nil {
		t.Fatalf("rollup.json: %v", err)
	}
	var r Rollup
	if err := json.Unmarshal(rb, &r); err != nil {
		t.Fatalf("rollup unmarshal: %v", err)
	}
	if r.TotalCalls != 2 {
		t.Errorf("TotalCalls = %d, want 2", r.TotalCalls)
	}

	// summary.md present with the canned text.
	sb, err := os.ReadFile(filepath.Join(dir, summaryMDFile))
	if err != nil {
		t.Fatalf("summary.md: %v", err)
	}
	if string(sb) != "All green. 2 calls." {
		t.Errorf("summary.md = %q", string(sb))
	}

	// summaries.jsonl has one entry.
	recs, err := readJSONL[summaryRecord](dir, summariesFile)
	if err != nil || len(recs) != 1 {
		t.Fatalf("summaries.jsonl: recs=%d err=%v", len(recs), err)
	}
}

func TestCompactReviewerErrorSkipsSummary(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1700000000, 0).UTC()
	c := &Compactor{
		dir:      dir,
		reviewer: fakeReviewer{err: context.DeadlineExceeded},
		model:    "anthropic:m", maxTokens: 2048, timeout: time.Second, logger: slog.Default(),
	}
	c.Compact(now, sampleEvents(now))

	if _, err := os.Stat(filepath.Join(dir, rollupFile)); err != nil {
		t.Errorf("rollup.json should exist even on reviewer error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, summaryMDFile)); !os.IsNotExist(err) {
		t.Errorf("summary.md should be absent on reviewer error, stat err = %v", err)
	}
}

func TestCompactNilReviewerWritesRollupOnly(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1700000000, 0).UTC()
	c := &Compactor{dir: dir, reviewer: nil, logger: slog.Default()}
	c.Compact(now, sampleEvents(now))
	if _, err := os.Stat(filepath.Join(dir, rollupFile)); err != nil {
		t.Errorf("rollup.json should exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, summaryMDFile)); !os.IsNotExist(err) {
		t.Errorf("summary.md should be absent with nil reviewer, stat err = %v", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/stats/ -run TestCompact -v`
Expected: FAIL — `Compactor`/`rollupFile`/`readJSONL`/`summaryRecord` not defined.

- [ ] **Step 3: Write the io helpers**

Create `internal/stats/io.go`:

```go
package stats

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

const (
	eventsFile    = "events.jsonl"
	rollupFile    = "rollup.json"
	summaryMDFile = "summary.md"
	summariesFile = "summaries.jsonl"
)

// appendJSONL appends one JSON-marshaled value as a line to dir/name.
func appendJSONL(dir, name string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}

// readJSONL reads a JSONL file into a slice, skipping blank and corrupt lines
// (best-effort). A missing file is not an error (returns nil).
func readJSONL[T any](dir, name string) ([]T, error) {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []T
	for _, line := range bytes.Split(b, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var v T
		if err := json.Unmarshal(line, &v); err != nil {
			continue
		}
		out = append(out, v)
	}
	return out, nil
}

// rewriteJSONL atomically replaces dir/name with the given items.
func rewriteJSONL[T any](dir, name string, items []T) error {
	var buf bytes.Buffer
	for _, it := range items {
		b, err := json.Marshal(it)
		if err != nil {
			return err
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	return os.WriteFile(filepath.Join(dir, name), buf.Bytes(), 0o644)
}

func writeJSON(dir, name string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name), b, 0o644)
}
```

- [ ] **Step 4: Write the Compactor**

Create `internal/stats/compactor.go`:

```go
package stats

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/patiently/anti-tangent-mcp/internal/providers"
)

// summarySchema constrains the reviewer to a single prose field. All providers
// require a non-empty JSONSchema and force JSON output, so we cannot request
// free prose directly — we ask for {"summary": "..."} and extract it.
const summarySchema = `{"type":"object","properties":{"summary":{"type":"string"}},"required":["summary"],"additionalProperties":false}`

const summarySystemPrompt = "You are an operations analyst. Given aggregate, anonymized statistics about an advisory code-review tool's own activity, write a brief (3-6 sentence) descriptive operational report: verdict mix, finding density and dominant categories, latency, model usage, cache/partial rates, and the trend vs the previous window if provided. This tool is advisory and has NO ground truth on whether findings were correct or acted upon — do NOT claim findings were right, wrong, useful, or ignored. Respond with a JSON object: {\"summary\": \"<markdown>\"}."

type summaryResponse struct {
	Summary string `json:"summary"`
}

type summaryRecord struct {
	Ts          time.Time `json:"ts"`
	WindowStart time.Time `json:"window_start"`
	WindowEnd   time.Time `json:"window_end"`
	Text        string    `json:"text"`
}

// Compactor computes the rollup and (when a reviewer is configured) the prose
// summary. It is stateless beyond its config; the Recorder owns event-file I/O
// and snapshots events in before calling Compact.
type Compactor struct {
	dir       string
	reviewer  providers.Reviewer // nil => summary step skipped
	model     string
	maxTokens int
	timeout   time.Duration
	logger    *slog.Logger
}

// Compact writes rollup.json from events, then (if a reviewer is configured)
// asks for a narrative and writes summary.md + appends summaries.jsonl.
// Best-effort: every error is logged and swallowed; rollup.json is always
// attempted before the LLM step so machine stats stay fresh when it fails.
func (c *Compactor) Compact(now time.Time, events []Event) {
	rollup := computeRollup(events, now)
	if err := writeJSON(c.dir, rollupFile, rollup); err != nil {
		c.logger.Warn("stats rollup write failed", "err", err)
	}
	if c.reviewer == nil {
		return
	}
	prompt := buildSummaryPrompt(rollup, c.readPrevSummary())
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	resp, err := c.reviewer.Review(ctx, providers.Request{
		Model:      c.model,
		System:     summarySystemPrompt,
		User:       prompt,
		MaxTokens:  c.maxTokens,
		JSONSchema: []byte(summarySchema),
	})
	if err != nil {
		c.logger.Warn("stats summary skipped (reviewer error)", "err", err)
		return
	}
	var sr summaryResponse
	if err := json.Unmarshal(resp.RawJSON, &sr); err != nil || strings.TrimSpace(sr.Summary) == "" {
		c.logger.Warn("stats summary unparseable", "err", err)
		return
	}
	if err := os.WriteFile(filepath.Join(c.dir, summaryMDFile), []byte(sr.Summary), 0o644); err != nil {
		c.logger.Warn("stats summary.md write failed", "err", err)
		return
	}
	if err := appendJSONL(c.dir, summariesFile, summaryRecord{
		Ts: now, WindowStart: rollup.WindowStart, WindowEnd: rollup.WindowEnd, Text: sr.Summary,
	}); err != nil {
		c.logger.Warn("stats summaries.jsonl append failed", "err", err)
	}
}

func (c *Compactor) readPrevSummary() string {
	b, err := os.ReadFile(filepath.Join(c.dir, summaryMDFile))
	if err != nil {
		return ""
	}
	return string(b)
}

func buildSummaryPrompt(r Rollup, prev string) string {
	b, _ := json.MarshalIndent(r, "", "  ")
	var sb strings.Builder
	fmt.Fprintf(&sb, "Window: %s to %s\n\nRollup (JSON):\n%s\n",
		r.WindowStart.Format(time.RFC3339), r.WindowEnd.Format(time.RFC3339), string(b))
	if strings.TrimSpace(prev) != "" {
		fmt.Fprintf(&sb, "\nPrevious window's summary (for trend comparison):\n%s\n", prev)
	}
	return sb.String()
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -race ./internal/stats/ -run TestCompact -v`
Expected: PASS (all three).

- [ ] **Step 6: Commit**

```bash
git add internal/stats/io.go internal/stats/compactor.go internal/stats/compactor_test.go
git commit -m "feat(stats): add JSONL io helpers + Compactor (rollup + LLM summary)"
```

---

## Task 6: `stats.Recorder` (append + single-flight async compaction)

**Files:**
- Create: `internal/stats/recorder.go`
- Test: `internal/stats/recorder_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/stats/recorder_test.go`:

```go
package stats

import (
	"log/slog"
	"sync"
	"testing"
	"time"
)

func newTestRecorder(t *testing.T, threshold int) *Recorder {
	t.Helper()
	r, err := New(Options{
		Dir:              t.TempDir(),
		Reviewer:         nil,
		SummaryInterval:  24 * time.Hour,
		SummaryThreshold: threshold,
		RetentionDays:    30,
		Logger:           slog.Default(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r
}

func TestNilRecorderIsNoOp(t *testing.T) {
	var r *Recorder
	r.Record(Event{Tool: "validate_task_spec"}) // must not panic
	if got := r.HashSession("abc"); got != "" {
		t.Errorf("nil HashSession = %q, want empty", got)
	}
}

func TestRecordAppends(t *testing.T) {
	r := newTestRecorder(t, 1000) // threshold high so no compaction fires
	for i := 0; i < 3; i++ {
		r.Record(Event{Ts: time.Now().UTC(), Tool: "validate_task_spec", Verdict: "pass"})
	}
	events, err := readEvents(r.dir)
	if err != nil {
		t.Fatalf("readEvents: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("appended %d events, want 3", len(events))
	}
	if r.state.EventsSinceSummary != 3 {
		t.Errorf("EventsSinceSummary = %d, want 3", r.state.EventsSinceSummary)
	}
}

func TestHashSessionStableAndSalted(t *testing.T) {
	r := newTestRecorder(t, 1000)
	a := r.HashSession("session-1")
	if a == "" || a == "session-1" {
		t.Fatalf("hash = %q (must be non-empty and not the raw id)", a)
	}
	if a != r.HashSession("session-1") {
		t.Error("hash not stable for same id")
	}
	if a == r.HashSession("session-2") {
		t.Error("different ids hashed equal")
	}
	if r.HashSession("") != "" {
		t.Error("empty id should hash to empty")
	}
}

func TestRecordSingleFlightCompaction(t *testing.T) {
	r := newTestRecorder(t, 1) // every record is "due"

	var mu sync.Mutex
	calls := 0
	started := make(chan struct{})
	release := make(chan struct{})
	r.runCompaction = func(now time.Time) {
		mu.Lock()
		calls++
		mu.Unlock()
		started <- struct{}{}
		<-release // hold the single in-flight slot open
	}

	r.Record(Event{Ts: time.Now().UTC(), Tool: "validate_task_spec"})
	<-started // first compaction is now running and holding the slot
	r.Record(Event{Ts: time.Now().UTC(), Tool: "validate_task_spec"}) // should NOT launch a second
	close(release)
	time.Sleep(20 * time.Millisecond) // let any erroneous second launch run

	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("compaction ran %d times, want 1 (single-flight)", calls)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/stats/ -run 'TestNilRecorder|TestRecord|TestHashSession' -v`
Expected: FAIL — `Recorder`/`Options`/`New`/`readEvents` not defined.

- [ ] **Step 3: Write the implementation**

Create `internal/stats/recorder.go`:

```go
package stats

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/patiently/anti-tangent-mcp/internal/providers"
)

// Options configures a Recorder. Constructed in main from config when
// ANTI_TANGENT_STATS_DIR is set.
type Options struct {
	Dir              string
	Reviewer         providers.Reviewer // nil => summary step skipped (rollup still written)
	Model            string
	MaxTokens        int
	RequestTimeout   time.Duration
	SummaryInterval  time.Duration
	SummaryThreshold int
	RetentionDays    int
	Logger           *slog.Logger
}

// Recorder appends counts-only events and launches single-flight async
// compaction when the trigger is due. All methods are nil-safe: a nil *Recorder
// (stats disabled) is a no-op, so the disabled call path is a single nil check.
type Recorder struct {
	dir           string
	mu            sync.Mutex // guards events.jsonl I/O + state
	state         State
	interval      time.Duration
	threshold     int
	retentionDays int
	compactor     *Compactor
	running       atomic.Bool
	clock         func() time.Time
	logger        *slog.Logger
	// runCompaction is launched (async, single-flight) when due. Defaults to
	// r.compact; tests override it.
	runCompaction func(now time.Time)
}

// New creates the stats dir (if needed), verifies it is writable, and loads or
// initializes state.json (fresh salt + LastSummaryAt=now on first enable).
// Returns an error only when the dir is unusable; the caller logs a warning and
// runs with stats disabled.
func New(opts Options) (*Recorder, error) {
	if err := os.MkdirAll(opts.Dir, 0o755); err != nil {
		return nil, fmt.Errorf("stats dir: %w", err)
	}
	probe := filepath.Join(opts.Dir, ".write-probe")
	if err := os.WriteFile(probe, []byte("ok"), 0o644); err != nil {
		return nil, fmt.Errorf("stats dir not writable: %w", err)
	}
	_ = os.Remove(probe)

	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	clock := func() time.Time { return time.Now().UTC() }

	st := loadState(opts.Dir)
	if st.LastSummaryAt.IsZero() {
		st.LastSummaryAt = clock()
		_ = saveState(opts.Dir, st)
	}

	r := &Recorder{
		dir:           opts.Dir,
		state:         st,
		interval:      opts.SummaryInterval,
		threshold:     opts.SummaryThreshold,
		retentionDays: opts.RetentionDays,
		compactor: &Compactor{
			dir:       opts.Dir,
			reviewer:  opts.Reviewer,
			model:     opts.Model,
			maxTokens: opts.MaxTokens,
			timeout:   opts.RequestTimeout,
			logger:    logger,
		},
		clock:  clock,
		logger: logger,
	}
	r.runCompaction = r.compact
	return r, nil
}

// HashSession returns a salted, non-reversible digest of a session id, or "" for
// a nil recorder / empty id. The raw id is never written to disk.
func (r *Recorder) HashSession(id string) string {
	if r == nil || id == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(r.state.Salt + ":" + id))
	return hex.EncodeToString(sum[:8])
}

// Record appends one event (best-effort) and, if a compaction is now due and
// none is in flight, launches one asynchronously. Safe on a nil Recorder.
func (r *Recorder) Record(ev Event) {
	if r == nil {
		return
	}
	r.mu.Lock()
	if err := appendJSONL(r.dir, eventsFile, ev); err != nil {
		r.logger.Warn("stats append failed", "err", err)
	} else {
		r.state.EventsSinceSummary++
		_ = saveState(r.dir, r.state)
	}
	st := r.state
	r.mu.Unlock()

	now := r.clock()
	if due(now, st, r.interval, r.threshold) && r.running.CompareAndSwap(false, true) {
		go func() {
			defer r.running.Store(false)
			r.runCompaction(now)
		}()
	}
}

// compact snapshots events under the lock, runs the Compactor (LLM call happens
// without the lock held), then prunes by retention and stamps state.
func (r *Recorder) compact(now time.Time) {
	r.mu.Lock()
	events, err := readEvents(r.dir)
	r.mu.Unlock()
	if err != nil {
		r.logger.Warn("stats read events failed", "err", err)
		return
	}

	r.compactor.Compact(now, events)

	cutoff := now.AddDate(0, 0, -r.retentionDays)
	r.mu.Lock()
	if err := pruneEvents(r.dir, cutoff); err != nil {
		r.logger.Warn("stats prune failed", "err", err)
	}
	r.state.LastSummaryAt = now
	r.state.EventsSinceSummary = 0
	_ = saveState(r.dir, r.state)
	r.mu.Unlock()
}

func readEvents(dir string) ([]Event, error) {
	return readJSONL[Event](dir, eventsFile)
}

// pruneEvents rewrites events.jsonl keeping only records at/after cutoff.
func pruneEvents(dir string, cutoff time.Time) error {
	events, err := readEvents(dir)
	if err != nil {
		return err
	}
	kept := events[:0]
	for _, e := range events {
		if !e.Ts.Before(cutoff) {
			kept = append(kept, e)
		}
	}
	return rewriteJSONL(dir, eventsFile, kept)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/stats/ -v`
Expected: PASS (all stats tests, including the single-flight test under `-race`).

- [ ] **Step 5: Commit**

```bash
git add internal/stats/recorder.go internal/stats/recorder_test.go
git commit -m "feat(stats): add nil-safe Recorder with single-flight async compaction"
```

---

## Task 7: Wire `Deps.Stats`, main construction, and the per-task hooks

**Files:**
- Modify: `internal/mcpsrv/server.go` (add field + import)
- Modify: `cmd/anti-tangent-mcp/main.go` (construct Recorder when enabled)
- Modify: `internal/mcpsrv/handlers.go` (add `recordStats` helper + 3 insertions)
- Test: `internal/mcpsrv/handlers_stats_test.go` (create)

- [ ] **Step 1: Add the Deps field**

In `internal/mcpsrv/server.go`, add the import `"github.com/patiently/anti-tangent-mcp/internal/stats"` and add to `Deps`:

```go
	// Stats is nil when ANTI_TANGENT_STATS_DIR is unset; all call sites are
	// nil-safe no-ops in that case.
	Stats *stats.Recorder
```

- [ ] **Step 2: Construct the Recorder in main**

In `cmd/anti-tangent-mcp/main.go`, add the import `"github.com/patiently/anti-tangent-mcp/internal/stats"`. Then, immediately BEFORE the `mcpsrv.Version = version` line, insert:

```go
	var statsRec *stats.Recorder
	if cfg.StatsDir != "" {
		if err := providers.ValidateModel(cfg.StatsModel); err != nil {
			fail(logger, "stats model invalid", err)
		}
		// A missing API key for the stats provider disables only the summary
		// step (reviewer == nil); recording + rollup still work.
		statsReviewer := registry[cfg.StatsModel.Provider]
		rec, err := stats.New(stats.Options{
			Dir:              cfg.StatsDir,
			Reviewer:         statsReviewer,
			Model:            cfg.StatsModel.Model,
			MaxTokens:        cfg.StatsMaxTokens,
			RequestTimeout:   cfg.RequestTimeout,
			SummaryInterval:  cfg.StatsSummaryInterval,
			SummaryThreshold: cfg.StatsSummaryThreshold,
			RetentionDays:    cfg.StatsRetentionDays,
			Logger:           logger,
		})
		if err != nil {
			logger.Warn("stats disabled", "err", err)
		} else {
			statsRec = rec
			logger.Info("stats enabled", "dir", cfg.StatsDir, "model", cfg.StatsModel.String(), "summary_enabled", statsReviewer != nil)
		}
	}
```

Then add `Stats: statsRec,` to the `mcpsrv.New(mcpsrv.Deps{...})` literal.

- [ ] **Step 3: Add the recordStats helper**

In `internal/mcpsrv/handlers.go`, add the import `"github.com/patiently/anti-tangent-mcp/internal/stats"` (alongside the existing imports; `time` and `verdict` are already imported). Add this method near the other `(h *handlers)` helpers (e.g. just below `withSessionTTL`):

```go
// recordStats maps a finalized Envelope into a stats.Event and records it.
// Nil-safe: when stats are disabled this is a single nil check with no
// allocation. tool is the MCP tool name; payloadBytes/cached are values the
// caller already has on hand.
func (h *handlers) recordStats(tool string, env Envelope, payloadBytes int, cached bool) {
	if h.deps.Stats == nil {
		return
	}
	sev, cat, total := stats.CountFindings(env.Findings)
	h.deps.Stats.Record(stats.Event{
		Ts:             time.Now().UTC().Truncate(time.Second),
		Tool:           tool,
		Verdict:        env.Verdict,
		FindingsTotal:  total,
		SeverityCounts: sev,
		CategoryCounts: cat,
		ReviewMS:       env.ReviewMS,
		Model:          env.ModelUsed,
		Cached:         cached,
		Partial:        env.Partial,
		PayloadBytes:   payloadBytes,
		SessionHash:    h.deps.Stats.HashSession(env.SessionID),
	})
}
```

- [ ] **Step 4: Insert the three per-task hook calls**

In `internal/mcpsrv/handlers.go`:

- In **`ValidateTaskSpec`**, immediately before its final `return envelopeResult(env)` (the one right after `env = h.withSessionTTL(env, sess)`), insert:
  ```go
	h.recordStats("validate_task_spec", env, 0, false)
  ```
- In **`CheckProgress`**, immediately before its final `return envelopeResult(env)` (after `env = h.withSessionTTL(env, sess)`), insert:
  ```go
	h.recordStats("check_progress", env, totalBytes(args.ChangedFiles), false)
  ```
- In **`ValidateCompletion`**, immediately before its final `return envelopeResult(env)` (after the `if !lightweight { env = h.withSessionTTL(env, sess) }` block), insert:
  ```go
	h.recordStats("validate_completion", env, totalCompletionBytes(args.FinalFiles, args.FinalDiff), false)
  ```

- [ ] **Step 5: Write the wiring test**

Create `internal/mcpsrv/handlers_stats_test.go`. This builds handlers with a Stats recorder (high threshold + long interval so no async compaction fires during the test) and asserts one event lands in `events.jsonl` after a `ValidateTaskSpec` call.

```go
package mcpsrv

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/providers"
	"github.com/patiently/anti-tangent-mcp/internal/session"
	"github.com/patiently/anti-tangent-mcp/internal/stats"
)

// statsFakeReviewer returns a canned pass verdict for any review.
type statsFakeReviewer struct{}

func (statsFakeReviewer) Name() string { return "anthropic" }
func (statsFakeReviewer) Review(ctx context.Context, req providers.Request) (providers.Response, error) {
	return providers.Response{
		RawJSON: []byte(`{"verdict":"pass","findings":[],"next_action":"proceed"}`),
		Model:   req.Model,
	}, nil
}

func TestValidateTaskSpecRecordsStats(t *testing.T) {
	dir := t.TempDir()
	rec, err := stats.New(stats.Options{
		Dir: dir, Reviewer: nil,
		SummaryInterval: 24 * time.Hour, SummaryThreshold: 1000, RetentionDays: 30,
		Logger: slog.Default(),
	})
	if err != nil {
		t.Fatalf("stats.New: %v", err)
	}

	cfg, err := config.Load(func(k string) string {
		if k == "ANTHROPIC_API_KEY" {
			return "x"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	h := &handlers{deps: Deps{
		Cfg:      cfg,
		Sessions: session.NewStore(cfg.SessionTTL),
		Reviews:  providers.Registry{"anthropic": statsFakeReviewer{}},
		Stats:    rec,
	}}

	_, _, err = h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:          "Add healthz",
		Goal:               "Expose a liveness probe",
		AcceptanceCriteria: []string{"GET /healthz returns 200 ok"},
	})
	if err != nil {
		t.Fatalf("ValidateTaskSpec: %v", err)
	}

	b, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("events.jsonl: %v", err)
	}
	if len(b) == 0 {
		t.Fatal("expected one event recorded, file is empty")
	}
}

func TestNilStatsDisabledNoFiles(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := config.Load(func(k string) string {
		if k == "ANTHROPIC_API_KEY" {
			return "x"
		}
		return ""
	})
	h := &handlers{deps: Deps{
		Cfg: cfg, Sessions: session.NewStore(cfg.SessionTTL),
		Reviews: providers.Registry{"anthropic": statsFakeReviewer{}}, Stats: nil,
	}}
	_, _, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "t", Goal: "g", AcceptanceCriteria: []string{"a"},
	})
	if err != nil {
		t.Fatalf("ValidateTaskSpec: %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("nil Stats must write no files, found %d", len(entries))
	}
}
```

> Note: the per-task handlers resolve the model from `cfg` and call the reviewer via the registry; `statsFakeReviewer` returns a valid `Result` JSON so the handler reaches its finalize point. If the existing test suite already provides a reviewer fake/helper, prefer reusing it and delete the local one.

- [ ] **Step 6: Run the tests + full build**

Run: `go build ./... && go test -race ./internal/mcpsrv/ -run 'TestValidateTaskSpecRecordsStats|TestNilStatsDisabledNoFiles' -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/mcpsrv/server.go cmd/anti-tangent-mcp/main.go internal/mcpsrv/handlers.go internal/mcpsrv/handlers_stats_test.go
git commit -m "feat(stats): wire Deps.Stats + record per-task hook calls"
```

---

## Task 8: Record `validate_plan`, `prime`, and `extract`

**Files:**
- Modify: `internal/mcpsrv/handlers.go` (`recordPlanStats` + `recordResultStats` helpers; 2 plan insertions)
- Modify: `internal/mcpsrv/prime_handler.go` (1 insertion + import)
- Modify: `internal/mcpsrv/extract_handler.go` (1 insertion + import)

- [ ] **Step 1: Add the two helpers**

In `internal/mcpsrv/handlers.go`, add below `recordStats`:

```go
// recordPlanStats maps a PlanResult into a stats.Event: plan_verdict -> verdict,
// plan-level + per-task findings aggregated into the histograms.
func (h *handlers) recordPlanStats(pr verdict.PlanResult, modelUsed string, ms int64, payloadBytes int, cached bool) {
	if h.deps.Stats == nil {
		return
	}
	findings := append([]verdict.Finding(nil), pr.PlanFindings...)
	for _, t := range pr.Tasks {
		findings = append(findings, t.Findings...)
	}
	sev, cat, total := stats.CountFindings(findings)
	h.deps.Stats.Record(stats.Event{
		Ts:             time.Now().UTC().Truncate(time.Second),
		Tool:           "validate_plan",
		Verdict:        string(pr.PlanVerdict),
		FindingsTotal:  total,
		SeverityCounts: sev,
		CategoryCounts: cat,
		ReviewMS:       ms,
		Model:          modelUsed,
		Cached:         cached,
		Partial:        pr.Partial,
		PayloadBytes:   payloadBytes,
	})
}

// recordResultStats records prime/extract calls: no pass/warn/fail verdict, just
// their findings (e.g. kb_gap, insufficient_evidence) into the category histogram.
func (h *handlers) recordResultStats(tool string, findings []verdict.Finding, modelUsed string, ms int64, payloadBytes int, cached bool) {
	if h.deps.Stats == nil {
		return
	}
	sev, cat, total := stats.CountFindings(findings)
	h.deps.Stats.Record(stats.Event{
		Ts:             time.Now().UTC().Truncate(time.Second),
		Tool:           tool,
		Verdict:        "",
		FindingsTotal:  total,
		SeverityCounts: sev,
		CategoryCounts: cat,
		ReviewMS:       ms,
		Model:          modelUsed,
		Cached:         cached,
		PayloadBytes:   payloadBytes,
	})
}
```

- [ ] **Step 2: Insert the two plan calls**

In `internal/mcpsrv/handlers.go`, in `ValidatePlan`:

- At the **computed** finalize, change the tail (currently `pr = finalizePlanResult(pr, modelUsed, ms)` / `h.planCache().store(...)` / `return planEnvelopeResultFinalized(pr, modelUsed, ms)`) to record before returning:
  ```go
	pr = finalizePlanResult(pr, modelUsed, ms)
	h.planCache().store(cacheKey, pr, modelUsed)
	h.recordPlanStats(pr, modelUsed, ms, planBytes+pkBytes, false)
	return planEnvelopeResultFinalized(pr, modelUsed, ms)
  ```
- At the **cache-hit** return (currently `return planEnvelopeResultFinalized(cached, cachedModelUsed, 0)`), record with `cached: true`:
  ```go
	h.recordPlanStats(cached, cachedModelUsed, 0, planBytes+pkBytes, true)
	return planEnvelopeResultFinalized(cached, cachedModelUsed, 0)
  ```

> `planBytes` and `pkBytes` are computed near the top of `ValidatePlan` (the payload-size guard) and are in scope at both return sites.

- [ ] **Step 3: Insert the prime call**

In `internal/mcpsrv/prime_handler.go`, add the imports `"time"`, `"github.com/patiently/anti-tangent-mcp/internal/stats"`, and (if not present) `"github.com/patiently/anti-tangent-mcp/internal/verdict"`. Before the final `return primeEnvelopeResult(result, modelUsed, ms)`, insert:

```go
	h.recordResultStats("prime_project_knowledge", result.Findings, modelUsed, ms, 0, false)
```

- [ ] **Step 4: Insert the extract call**

In `internal/mcpsrv/extract_handler.go`, add the same imports. Before the final `return extractEnvelopeResult(result, modelUsed, ms)`, insert:

```go
	h.recordResultStats("extract_project_knowledge", result.Findings, modelUsed, ms, 0, false)
```

- [ ] **Step 5: Run build + full mcpsrv tests**

Run: `go build ./... && go test -race ./internal/mcpsrv/ -v`
Expected: PASS (existing suite unaffected; recording is nil-safe and the new helpers compile).

- [ ] **Step 6: Commit**

```bash
git add internal/mcpsrv/handlers.go internal/mcpsrv/prime_handler.go internal/mcpsrv/extract_handler.go
git commit -m "feat(stats): record validate_plan, prime, and extract calls"
```

---

## Task 9: CodeScene companion — aggregate `codescene-events.jsonl` into `rollup.json`

**Files:**
- Create: `internal/stats/codescene.go`
- Modify: `internal/stats/rollup.go` (add `Codescene` field)
- Modify: `internal/stats/compactor.go` (`Compact` reads CodeScene events; signature gains a param)
- Modify: `internal/stats/recorder.go` (`compact` snapshots + prunes CodeScene events)
- Modify: `internal/stats/compactor_test.go` (update the 3 `Compact(now, ...)` calls to the new signature)
- Test: `internal/stats/codescene_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/stats/codescene_test.go`:

```go
package stats

import (
	"testing"
	"time"
)

func TestComputeCodescene(t *testing.T) {
	base := time.Unix(1700000000, 0).UTC()
	events := []CodesceneEvent{
		{Ts: base, Tool: "analyze_change_set", ScoreBefore: 8.0, ScoreAfter: 8.5, Delta: 0.5, Trend: "improvement",
			FilesAnalyzed: 3, CategoryCounts: map[string]int{"complex-method": 1}},
		{Ts: base.Add(time.Hour), Tool: "analyze_change_set", ScoreBefore: 8.5, ScoreAfter: 8.2, Delta: -0.3, Trend: "regression",
			FilesAnalyzed: 5, CategoryCounts: map[string]int{"complex-method": 2, "bumpy-road": 1}},
	}
	cr := computeCodescene(events, base.Add(2*time.Hour))
	if cr == nil {
		t.Fatal("expected non-nil rollup")
	}
	if cr.Runs != 2 {
		t.Errorf("Runs = %d, want 2", cr.Runs)
	}
	if cr.LatestScore != 8.2 || cr.LatestDelta != -0.3 || cr.LatestTrend != "regression" {
		t.Errorf("latest = %v/%v/%v", cr.LatestScore, cr.LatestDelta, cr.LatestTrend)
	}
	if cr.Regressions != 1 || cr.Improvements != 1 || cr.Neutral != 0 {
		t.Errorf("trend counts = %d/%d/%d", cr.Regressions, cr.Improvements, cr.Neutral)
	}
	if cr.CategoryHistogram["complex-method"] != 3 || cr.CategoryHistogram["bumpy-road"] != 1 {
		t.Errorf("category histogram = %v", cr.CategoryHistogram)
	}
	if !cr.WindowStart.Equal(base) || !cr.WindowEnd.Equal(base.Add(time.Hour)) {
		t.Errorf("window = %v..%v", cr.WindowStart, cr.WindowEnd)
	}
}

func TestComputeCodesceneEmptyIsNil(t *testing.T) {
	if cr := computeCodescene(nil, time.Now()); cr != nil {
		t.Errorf("empty input must yield nil (omitted key), got %+v", cr)
	}
}

func TestPruneCodescene(t *testing.T) {
	dir := t.TempDir()
	base := time.Unix(1700000000, 0).UTC()
	old := CodesceneEvent{Ts: base.Add(-48 * time.Hour), Tool: "analyze_change_set"}
	fresh := CodesceneEvent{Ts: base, Tool: "analyze_change_set"}
	if err := appendJSONL(dir, codesceneFile, old); err != nil {
		t.Fatal(err)
	}
	if err := appendJSONL(dir, codesceneFile, fresh); err != nil {
		t.Fatal(err)
	}
	if err := pruneCodescene(dir, base.Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, _ := readCodescene(dir)
	if len(got) != 1 || !got[0].Ts.Equal(base) {
		t.Fatalf("after prune got %d events, want 1 (the fresh one)", len(got))
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/stats/ -run 'TestComputeCodescene|TestPruneCodescene' -v`
Expected: FAIL — `CodesceneEvent`/`computeCodescene`/`readCodescene`/`pruneCodescene`/`codesceneFile` not defined.

- [ ] **Step 3: Write codescene.go**

Create `internal/stats/codescene.go`:

```go
package stats

import "time"

const codesceneFile = "codescene-events.jsonl"

// CodesceneEvent is the per-run record the AGENT appends (see INTEGRATION.md and
// docs/team-setup/codescene-stats.md). anti-tangent only ever READS this file;
// it never writes it. Counts + metadata only — no file paths.
type CodesceneEvent struct {
	Ts             time.Time      `json:"ts"`
	Tool           string         `json:"tool"`
	ScoreBefore    float64        `json:"score_before"`
	ScoreAfter     float64        `json:"score_after"`
	Delta          float64        `json:"delta"`
	Trend          string         `json:"trend"`
	FilesAnalyzed  int            `json:"files_analyzed"`
	CategoryCounts map[string]int `json:"category_counts,omitempty"`
}

// CodesceneRollup is the nested `codescene` block in rollup.json. snake_case
// json tags are part of the same cross-component contract as Rollup (§12.4).
type CodesceneRollup struct {
	Runs              int            `json:"runs"`
	LatestScore       float64        `json:"latest_score"`
	LatestDelta       float64        `json:"latest_delta"`
	LatestTrend       string         `json:"latest_trend"`
	ScoreP50          float64        `json:"score_p50"`
	Regressions       int            `json:"regressions"`
	Improvements      int            `json:"improvements"`
	Neutral           int            `json:"neutral"`
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
func computeCodescene(events []CodesceneEvent, now time.Time) *CodesceneRollup {
	if len(events) == 0 {
		return nil
	}
	cr := &CodesceneRollup{
		CategoryHistogram: map[string]int{},
		WindowStart:       events[0].Ts,
		WindowEnd:         events[0].Ts,
		Runs:              len(events),
	}
	scores := make([]int64, 0, len(events)) // score*100 so we can reuse percentile (int64)
	latest := events[0]
	for _, e := range events {
		if e.Ts.Before(cr.WindowStart) {
			cr.WindowStart = e.Ts
		}
		if !e.Ts.Before(cr.WindowEnd) {
			cr.WindowEnd = e.Ts
			latest = e
		}
		switch e.Trend {
		case "regression":
			cr.Regressions++
		case "improvement":
			cr.Improvements++
		default:
			cr.Neutral++
		}
		for k, v := range e.CategoryCounts {
			cr.CategoryHistogram[k] += v
		}
		scores = append(scores, int64(e.ScoreAfter*100))
	}
	cr.LatestScore = latest.ScoreAfter
	cr.LatestDelta = latest.Delta
	cr.LatestTrend = latest.Trend
	cr.ScoreP50 = float64(percentile(scores, 50)) / 100
	return cr
}
```

- [ ] **Step 4: Add the Rollup.Codescene field**

In `internal/stats/rollup.go`, add this field to the `Rollup` struct (after `GeneratedAt`):

```go
	Codescene *CodesceneRollup `json:"codescene,omitempty"`
```

- [ ] **Step 5: Make Compact aggregate CodeScene**

In `internal/stats/compactor.go`, change the `Compact` signature and body head:

```go
func (c *Compactor) Compact(now time.Time, events []Event, csEvents []CodesceneEvent) {
	rollup := computeRollup(events, now)
	if cs := computeCodescene(csEvents, now); cs != nil {
		rollup.Codescene = cs
	}
	if err := writeJSON(c.dir, rollupFile, rollup); err != nil {
		c.logger.Warn("stats rollup write failed", "err", err)
	}
	// ... (rest unchanged: nil-reviewer guard, summary call, writes)
```

- [ ] **Step 6: Update the Recorder to snapshot + prune CodeScene events**

In `internal/stats/recorder.go`, in `compact`, change the snapshot and the Compact call, and add the CodeScene prune:

```go
	r.mu.Lock()
	events, err := readEvents(r.dir)
	csEvents, csErr := readCodescene(r.dir)
	r.mu.Unlock()
	if err != nil {
		r.logger.Warn("stats read events failed", "err", err)
		return
	}
	if csErr != nil {
		r.logger.Warn("stats read codescene events failed", "err", csErr)
		csEvents = nil
	}

	r.compactor.Compact(now, events, csEvents)

	cutoff := now.AddDate(0, 0, -r.retentionDays)
	r.mu.Lock()
	if err := pruneEvents(r.dir, cutoff); err != nil {
		r.logger.Warn("stats prune failed", "err", err)
	}
	if err := pruneCodescene(r.dir, cutoff); err != nil {
		r.logger.Warn("stats codescene prune failed", "err", err)
	}
	r.state.LastSummaryAt = now
	r.state.EventsSinceSummary = 0
	_ = saveState(r.dir, r.state)
	r.mu.Unlock()
```

- [ ] **Step 7: Update the Task-5 compactor tests to the new signature**

In `internal/stats/compactor_test.go`, change each of the three `c.Compact(now, sampleEvents(now))` calls to `c.Compact(now, sampleEvents(now), nil)`. Then add one assertion to `TestCompactWritesRollupAndSummary` confirming a CodeScene block round-trips — append this near the end of that test:

```go
	// CodeScene block: present only when codescene events are passed.
	csNow := now
	c.Compact(csNow, sampleEvents(csNow), []CodesceneEvent{
		{Ts: csNow, Tool: "analyze_change_set", ScoreAfter: 8.2, Delta: -0.3, Trend: "regression"},
	})
	rb2, _ := os.ReadFile(filepath.Join(dir, rollupFile))
	var r2 Rollup
	if err := json.Unmarshal(rb2, &r2); err != nil {
		t.Fatalf("rollup2 unmarshal: %v", err)
	}
	if r2.Codescene == nil || r2.Codescene.Runs != 1 || r2.Codescene.LatestTrend != "regression" {
		t.Errorf("codescene block = %+v", r2.Codescene)
	}
```

- [ ] **Step 8: Run the full stats suite + build**

Run: `go build ./... && go test -race ./internal/stats/ -v`
Expected: PASS (all stats tests, including the new CodeScene tests and the updated compactor tests).

- [ ] **Step 9: Commit**

```bash
git add internal/stats/codescene.go internal/stats/rollup.go internal/stats/compactor.go internal/stats/recorder.go internal/stats/compactor_test.go internal/stats/codescene_test.go
git commit -m "feat(stats): aggregate CodeScene records into rollup.json codescene block"
```

---

## Task 10: Documentation — team-setup doc, INTEGRATION.md pointer, README, CLAUDE.md

**Files:**
- Create: `docs/team-setup/codescene-stats.md`
- Modify: `INTEGRATION.md` (≤ ~200-byte pointer; MUST stay < 40,000 bytes)
- Modify: `README.md` (dotenv block + on-disk files paragraph)
- Modify: `CLAUDE.md` ("What This Repo Is Not" persistence line)

- [ ] **Step 1: Create the team-setup doc**

Create `docs/team-setup/codescene-stats.md`:

````markdown
# Capturing CodeScene stats over time

CodeScene (the [codescene-oss MCP server](https://github.com/codescene-oss/codescene-mcp-server)) recomputes Code Health on each call but keeps **no history**, and anti-tangent's server never sees those calls (they run in the agent's MCP-client context). So to accumulate a Code Health trend, the **agent** appends one counts-only record per run to a file in `ANTI_TANGENT_STATS_DIR`, and anti-tangent's Compactor aggregates it into `rollup.json`.

This is active only when `ANTI_TANGENT_STATS_DIR` is set **and** CodeScene is configured in the host.

## What to append, when

Once per task, after the pre-DONE `analyze_change_set` call (the branch-vs-base Code Health delta — the meaningful per-task metric), append one JSON line to `$ANTI_TANGENT_STATS_DIR/codescene-events.jsonl`. In the subagent/controller model, the controller appends from the implementer's DONE report; in the inline model, the single agent appends when it runs `analyze_change_set`. Mid-task `pre_commit_code_health_safeguard` and `code_health_review` drill-downs are **not** recorded.

## Record shape (counts + metadata only)

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

- `trend` ∈ `regression | improvement | neutral` (the sign of `delta`).
- **No file paths, no code, no raw session id** — privacy parity with anti-tangent's own `events.jsonl`.
- Best-effort: any field CodeScene doesn't return may be omitted.

## How it surfaces

During compaction, anti-tangent reads `codescene-events.jsonl`, aggregates the current window, and writes a nested `codescene` block into `rollup.json`:

```json
"codescene": {
  "runs": 12, "latest_score": 8.4, "latest_delta": -0.3,
  "latest_trend": "regression", "score_p50": 8.6,
  "regressions": 3, "improvements": 7, "neutral": 2,
  "category_histogram": {"complex-method": 5, "bumpy-road": 2},
  "window_start": "...Z", "window_end": "...Z"
}
```

Consumers read `rollup.json` and look for the optional `codescene` block — **absence means "no CodeScene data this window," not an error.** The raw `codescene-events.jsonl` is retention-pruned by `ANTI_TANGENT_STATS_RETENTION_DAYS` alongside `events.jsonl`.
````

- [ ] **Step 2: Add the INTEGRATION.md pointer**

In `INTEGRATION.md`, at the END of the `### CodeScene MCP companion (optional)` section (immediately after the line ending `... skip all CodeScene calls too.`), add a blank line then this single line:

```markdown
**CodeScene stats:** CodeScene keeps no history — see [docs/team-setup/codescene-stats.md](docs/team-setup/codescene-stats.md) to log Code Health to `codescene-events.jsonl`.
```

- [ ] **Step 3: Verify INTEGRATION.md stays under budget**

Run: `wc -c INTEGRATION.md`
Expected: a number **< 40000** (the CI `INTEGRATION.md size budget` job fails at ≥ 40000).

If it is ≥ 40000, apply this fallback trim to reclaim bytes, then re-run `wc -c`: in the same section, delete the redundant clause ` High-signal, unlike anti-tangent's \`check_progress\`.` from the `pre_commit_code_health_safeguard` bullet (the point is already made elsewhere in the file). Re-check until `wc -c` is < 40000.

- [ ] **Step 4: Add the README env vars**

In `README.md`, in the environment-variable / dotenv section (where the other `ANTI_TANGENT_*` vars are documented), add:

```dotenv
# --- Opt-in statistics (off unless ANTI_TANGENT_STATS_DIR is set) ---
# Output directory; enables the subsystem. Files written: events.jsonl
# (per-call counts), rollup.json (aggregate; machine-readable), summary.md
# (latest LLM narrative), summaries.jsonl (history), state.json (cadence+salt),
# and codescene-events.jsonl (agent-appended CodeScene Code Health records,
# aggregated into rollup.json's `codescene` block).
ANTI_TANGENT_STATS_DIR=
ANTI_TANGENT_STATS_MODEL=            # summarizer model; defaults to ANTI_TANGENT_MID_MODEL
ANTI_TANGENT_STATS_SUMMARY_INTERVAL=24h
ANTI_TANGENT_STATS_SUMMARY_THRESHOLD=50
ANTI_TANGENT_STATS_RETENTION_DAYS=30
ANTI_TANGENT_STATS_MAX_TOKENS=2048   # clamped by ANTI_TANGENT_MAX_TOKENS_CEILING
```

- [ ] **Step 5: Amend CLAUDE.md non-goal**

In `CLAUDE.md`, under `## What This Repo Is Not`, replace the persistence bullet:

```markdown
- No persistent storage. Sessions live in memory and are lost on restart by design.
```

with:

```markdown
- No persistent storage by default. Sessions live in memory and are lost on restart. The one exception is the **opt-in** stats subsystem (`ANTI_TANGENT_STATS_DIR`, off by default), which writes plain files — there is still no metrics endpoint; stats output is files, not a served endpoint.
```

- [ ] **Step 6: Final full-suite verification**

Run: `go build ./... && go test -race ./... && wc -c INTEGRATION.md`
Expected: build OK; all tests PASS; `INTEGRATION.md` < 40000 bytes.

- [ ] **Step 7: Commit**

```bash
git add docs/team-setup/codescene-stats.md INTEGRATION.md README.md CLAUDE.md
git commit -m "docs(stats): team-setup CodeScene-stats doc + README/CLAUDE/INTEGRATION updates"
```

---

## Self-Review (completed by plan author)

**Spec coverage (main subsystem §1–§11):** §3.1 Event → Task 2; §3.2 Recorder → Task 6; §3.3 Rollup (pinned tags) → Task 4; §3.4 Compactor → Task 5; §4 on-disk layout → Tasks 5–6 (`io.go` constants) + §12 file in Task 9; §5 trigger → Task 3 (`due`) + Task 6 (launch); §6 integration seam → Tasks 7–8; §7 config → Task 1; §8 error handling → best-effort swallow-and-log throughout Tasks 5–6 + main warn-and-disable in Task 7; §9 testing → tests in every task; §10 docs → Task 10; §11 acceptance → covered by the disabled-path test (Task 7 `TestNilStatsDisabledNoFiles`), append test (Task 6/7), compaction tests (Task 5), retention (Tasks 6/9), no-raw-id (Task 6 `TestHashSessionStableAndSalted` + Event has no raw id field).

**Spec coverage (CodeScene companion §12):** §12.2 agent appends → documented in Task 10 doc (no Go writer, by design); §12.3 record shape → Task 9 `CodesceneEvent` + Task 10 doc; §12.4 Compactor aggregation + `codescene` block + retention prune → Task 9; §12.5 consumer ingestion → Task 10 doc; §12.6 deliverables → Tasks 9–10; §12.7 acceptance → Task 9 tests (block present, absent→omitted, prune) + Task 10 budget check.

**Placeholder scan:** none — every code step carries complete code; every run step has an expected result.

**Type consistency:** `Event`, `Rollup`, `CodesceneRollup`, `Compactor.Compact(now, events, csEvents)`, `Recorder.{Record,HashSession,compact}`, `recordStats`/`recordPlanStats`/`recordResultStats`, and the file-name constants are used consistently across tasks. Task 9 explicitly updates the Task-5 `Compact` call sites when the signature gains `csEvents`. The `rollup.json` json tags match spec §3.3/§12.4 exactly.

**Known scoping note (intentional, not a gap):** early-return envelopes (session_not_found, payload_too_large) are not recorded; recording happens at each handler's reviewed/cached finalize. This matches the spec's "one record per (completed) hook call" intent and keeps the disabled path a single nil check.
